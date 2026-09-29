package main

import (
	"net/http"
	"strings"
	"testing"
)

// 返修全链路：清洗不合格 → 发起返修（追溯/费用/押金/暂停灌装/照片）
// → 返修完成上传复检 → 仓库确认恢复灌装 → 财务确认账单 → 酒吧可见账单
func TestRepairFlow(t *testing.T) {
	if err := resetTestDB(); err != nil {
		t.Fatal(err)
	}
	admin := login(t, "admin")
	wash1 := login(t, "wash1")
	brewery := login(t, "brewery")
	finance := login(t, "finance")
	bar1 := login(t, "bar1")
	bar3 := login(t, "bar3")
	driver1 := login(t, "driver1")

	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4//8/AAX+Av6AzxmpAAAAAElFTkSuQmCC"
	photo := func(name string) M {
		return M{"name": name, "content_type": "image/png",
			"data": "data:image/png;base64," + png1x1}
	}

	t.Run("越权访问返修列表被拒", func(t *testing.T) {
		code, _ := doJSON(t, "GET", "/api/repairs", driver1, nil)
		if code != 403 {
			t.Fatalf("司机不应看到返修列表, 实际 %d", code)
		}
	})

	// 910(KEG-0010) 种子为已回收待清洗，先判不合格
	code, d := doJSON(t, "POST", "/api/wash/checks", admin, M{
		"cycle_id": 910, "inspector": "刘师傅", "passed": true,
		"seal_ok": false, "cleanliness_ok": false,
		"defect_note": "密封失效+内壁残留，桶内异味",
	})
	mustOK(t, code, d, "910 判不合格")
	if d["asset_status"] != "WASH_FAIL" || d["refillable"] != false {
		t.Fatalf("应冻结取消灌装: %v", d)
	}

	t.Run("清洗厂看到可发起返修的不合格桶", func(t *testing.T) {
		code, d := doJSON(t, "GET", "/api/repairs", wash1, nil)
		mustOK(t, code, d, "返修列表")
		var hit bool
		for _, x := range d["can_create"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["cycle_id"].(float64) == 910 {
				hit = true
				if m["bar"] != "胡同里 Beer Bar" || m["driver"] != "李师傅" {
					t.Fatalf("追溯信息应含上一家酒吧与回收司机: %v", m)
				}
			}
		}
		if !hit {
			t.Fatalf("待返修列表应包含 910: %v", d["can_create"])
		}
	})

	// 发起返修：无照片应被拒
	code, _ = doJSON(t, "POST", "/api/repairs/create", wash1, M{
		"cycle_id": 910, "defect_type": "异味", "repair_fee": 100,
		"deposit_deduct": 120, "fee_party": "BAR"})
	if code != 400 {
		t.Fatalf("无照片发起返修应 400, 实际 %d", code)
	}

	// 押金扣减超额应被拒
	code, d = doJSON(t, "POST", "/api/repairs/create", wash1, M{
		"cycle_id": 910, "defect_type": "异味", "repair_fee": 100,
		"deposit_deduct": 9999, "fee_party": "BAR", "photos": []M{photo("d.png")}})
	if code != 400 {
		t.Fatalf("超额押金扣减应 400, 实际 %d %v", code, d)
	}

	// 正式发起返修
	code, d = doJSON(t, "POST", "/api/repairs/create", wash1, M{
		"cycle_id": 910, "defect_type": "异味",
		"defect_note": "桶内酸臭异味，阀门完好", "repair_fee": 100,
		"deposit_deduct": 120, "fee_party": "BAR",
		"photos": []M{photo("KEG-0010-defect.png")}})
	mustOK(t, code, d, "发起返修")
	if d["repair_no"] != "RP-0101" || d["asset_status"] != "REPAIR" ||
		d["refillable"] != false || d["stage"] != "REPAIRING" {
		t.Fatalf("返修单状态错误: %v", d)
	}
	rid := int64(d["repair_id"].(float64))

	t.Run("返修登记同步押金扣减并暂停灌装", func(t *testing.T) {
		code, tr := doJSON(t, "GET", "/api/kegs/trace/KEG-0010", admin, nil)
		mustOK(t, code, tr, "追溯")
		if tr["keg"].(map[string]interface{})["asset_status"] != "REPAIR" {
			t.Fatalf("桶应处于返修中: %v", tr["keg"])
		}
		c := tr["current"].(map[string]interface{})
		if c["deposit_status"] != "PARTIAL" || c["deducted_amount"].(float64) != 120 ||
			c["released_amount"].(float64) != 580 {
			t.Fatalf("押金应同步部分扣款 120/释放 580: %v", c)
		}
		var saw bool
		for _, e := range tr["events"].([]interface{}) {
			em := e.(map[string]interface{})
			if em["event_type"] == "REPAIR_CREATED" {
				saw = true
				det := em["detail"].(map[string]interface{})
				if det["bar_id"].(float64) != 6 || det["driver_id"].(float64) != 2 ||
					det["repair_fee"].(float64) != 100 || det["deposit_deduct"].(float64) != 120 {
					t.Fatalf("返修事件追溯信息错误: %v", det)
				}
			}
		}
		if !saw {
			t.Fatalf("时间线缺少 REPAIR_CREATED")
		}
	})

	// 重复发起被拒
	code, _ = doJSON(t, "POST", "/api/repairs/create", wash1, M{
		"cycle_id": 910, "defect_type": "异味", "photos": []M{photo("x.png")}})
	if code != 400 {
		t.Fatalf("重复发起返修应 400, 实际 %d", code)
	}

	// 被追溯酒吧收到通知
	_, nd := doJSON(t, "GET", "/api/notifications", bar3, nil)
	var notified bool
	for _, x := range nd["notifications"].([]interface{}) {
		m := x.(map[string]interface{})
		if strings.Contains(m["title"].(string), "RP-0101") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("胡同里应收到 RP-0101 返修通知")
	}

	// 返修完成：无照片拒、结论非法拒
	code, _ = doJSON(t, "POST", "/api/repairs/submit/"+itoa(rid), wash1,
		M{"recheck_result": "合格", "inspector": "刘师傅"})
	if code != 400 {
		t.Fatalf("无复检照片应 400, 实际 %d", code)
	}
	code, _ = doJSON(t, "POST", "/api/repairs/submit/"+itoa(rid), wash1,
		M{"recheck_result": "也许行", "photos": []M{photo("r.png")}})
	if code != 400 {
		t.Fatalf("非法复检结论应 400, 实际 %d", code)
	}

	// 首次复检被仓库退回（不合格）→ 回到 OPEN
	code, d = doJSON(t, "POST", "/api/repairs/submit/"+itoa(rid), wash1, M{
		"recheck_result": "不合格", "inspector": "刘师傅",
		"note": "仍有轻微异味", "photos": []M{photo("r1.png")}})
	mustOK(t, code, d, "提交复检(不合格)")
	code, d = doJSON(t, "POST", "/api/repairs/confirm/"+itoa(rid), brewery,
		M{"pass": false, "note": "复检照片看不清，请重新深度清洗"})
	mustOK(t, code, d, "仓库退回")
	if d["status"] != "OPEN" {
		t.Fatalf("退回后状态应为 OPEN: %v", d)
	}

	// 再次返修合格并经仓库确认 → 恢复灌装
	code, d = doJSON(t, "POST", "/api/repairs/submit/"+itoa(rid), wash1, M{
		"recheck_result": "合格", "inspector": "刘师傅",
		"note": "蒸汽灭菌两轮，全部合格", "photos": []M{photo("r2.png")}})
	mustOK(t, code, d, "提交复检(合格)")
	code, d = doJSON(t, "POST", "/api/repairs/confirm/"+itoa(rid), brewery,
		M{"pass": true, "note": "复检达标，同意回库"})
	mustOK(t, code, d, "仓库确认合格")
	if d["status"] != "CONFIRMED" || d["asset_status"] != "IN_STOCK" ||
		d["refillable"] != true || d["stage"] != "WASHED" {
		t.Fatalf("确认后应回库恢复灌装: %v", d)
	}
	// OPEN 状态的返修单不能被仓库直接确认
	code, _ = doJSON(t, "POST", "/api/repairs/confirm/1", brewery, M{"pass": true})
	if code != 400 {
		t.Fatalf("RP-0001 处于 OPEN，仓库确认应 400, 实际 %d", code)
	}

	t.Run("返修详情含追溯/照片/账单", func(t *testing.T) {
		code, d := doJSON(t, "GET", "/api/repairs/"+itoa(rid), admin, nil)
		mustOK(t, code, d, "返修详情")
		tr := d["trace"].(map[string]interface{})
		if tr["bar"].(map[string]interface{})["name"] != "胡同里 Beer Bar" {
			t.Fatalf("应追溯到上一家酒吧: %v", tr["bar"])
		}
		if tr["driver"].(map[string]interface{})["name"] != "李师傅" {
			t.Fatalf("应追溯到回收司机: %v", tr["driver"])
		}
		if tr["washplant"].(map[string]interface{})["name"] != "上海净桶中心" {
			t.Fatalf("应含清洗厂: %v", tr["washplant"])
		}
		if tr["batch"].(map[string]interface{})["batch_no"] != "BT2026-WT-01" {
			t.Fatalf("应追溯酒液批次: %v", tr["batch"])
		}
		if tr["pickup_scan"].(map[string]interface{})["residue_level"] != "少量" {
			t.Fatalf("应含司机回收扫码记录: %v", tr["pickup_scan"])
		}
		if len(d["photos"].([]interface{})) != 3 {
			t.Fatalf("应保留 3 张照片(1 缺陷 + 2 复检): %v", d["photos"])
		}
		charges := d["charges"].([]interface{})
		var fee, deduct float64
		for _, x := range charges {
			m := x.(map[string]interface{})
			if m["charge_type"] == "REPAIR_FEE" {
				fee = m["amount"].(float64)
			}
			if m["charge_type"] == "DEPOSIT_DEDUCT" {
				deduct = m["amount"].(float64)
				if m["party_name"] != "胡同里 Beer Bar" {
					t.Fatalf("押金扣减应进入胡同里账单: %v", m)
				}
			}
		}
		if fee != 100 || deduct != 120 {
			t.Fatalf("账单金额错误 fee=%v deduct=%v", fee, deduct)
		}
	})

	// 照片可下载
	req, _ := http.NewRequest("GET", testServer.URL+"/api/repairs/photos/101", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
		t.Fatalf("照片下载应 200 image/*, 实际 %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}

	t.Run("财务账单确认并同步", func(t *testing.T) {
		code, d := doJSON(t, "GET", "/api/finance/billing", finance, nil)
		mustOK(t, code, d, "账单列表")
		var deductID int64
		for _, x := range d["charges"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["repair_no"] == "RP-0101" && m["charge_type"] == "DEPOSIT_DEDUCT" {
				deductID = int64(m["id"].(float64))
			}
		}
		if deductID == 0 {
			t.Fatalf("账单应含 RP-0101 押金扣减条目: %v", d)
		}
		// 胡同里待确认 = 种子 RP-0001（180+150）+ RP-0101（100+120）= 550
		var partyPending float64
		for _, x := range d["by_party"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["party_type"] == "BAR" && m["party_name"] == "胡同里 Beer Bar" {
				partyPending = m["pending_amount"].(float64)
			}
		}
		if partyPending != 550 {
			t.Fatalf("胡同里待确认账单应为 ¥550, 实际 %v", partyPending)
		}
		code, d = doJSON(t, "POST", "/api/finance/billing/confirm/"+itoa(deductID), finance, nil)
		mustOK(t, code, d, "确认押金扣减账单")
		// 重复确认
		code, _ = doJSON(t, "POST", "/api/finance/billing/confirm/"+itoa(deductID), finance, nil)
		if code != 400 {
			t.Fatalf("重复确认应 400, 实际 %d", code)
		}
	})

	t.Run("酒吧只看到本店返修账单", func(t *testing.T) {
		code, d := doJSON(t, "GET", "/api/bar/billing", bar3, nil)
		mustOK(t, code, d, "胡同里账单")
		var n int
		for _, x := range d["charges"].([]interface{}) {
			n++
			m := x.(map[string]interface{})
			if m["keg_code"] != "KEG-0010" && m["keg_code"] != "KEG-0011" {
				t.Fatalf("酒吧账单出现他店桶: %v", m)
			}
		}
		if n == 0 {
			t.Fatalf("胡同里应有返修账单（RP-0001 种子 + RP-0101）")
		}
		// 泡沫实验室不应看到胡同里的账单
		_, d1 := doJSON(t, "GET", "/api/bar/billing", bar1, nil)
		for _, x := range d1["charges"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["keg_code"] == "KEG-0010" || m["keg_code"] == "KEG-0011" {
				t.Fatalf("泡沫实验室不应看到胡同里账单: %v", m)
			}
		}
	})

	// 清洗厂数据隔离：wash1 看不到北京洗桶站的 RP-0001
	code, d = doJSON(t, "GET", "/api/repairs", wash1, nil)
	mustOK(t, code, d, "wash1 列表")
	for _, x := range d["repairs"].([]interface{}) {
		if x.(map[string]interface{})["repair_no"] == "RP-0001" {
			t.Fatalf("wash1 不应看到其他清洗厂的返修单 RP-0001")
		}
	}
	// 详情与照片同样隔离
	if code, _ := doJSON(t, "GET", "/api/repairs/1", wash1, nil); code != 403 {
		t.Fatalf("wash1 查看北京返修单详情应 403, 实际 %d", code)
	}
	reqDenied, _ := http.NewRequest("GET", testServer.URL+"/api/repairs/photos/1", nil)
	reqDenied.Header.Set("Authorization", "Bearer "+wash1)
	if res, err := http.DefaultClient.Do(reqDenied); err != nil || res.StatusCode != 403 {
		t.Fatalf("wash1 查看北京返修照片应 403")
	} else {
		res.Body.Close()
	}

	t.Run("清洗厂承担费用的返修单进入清洗厂账单", func(t *testing.T) {
		// 915(KEG-0015) 已回收 → 判不合格 → 返修，返修费由清洗厂承担
		code, d := doJSON(t, "POST", "/api/wash/checks", wash1, M{
			"cycle_id": 915, "passed": true, "cleanliness_ok": false,
			"defect_note": "清洗前就破损"})
		mustOK(t, code, d, "915 判不合格")
		code, d = doJSON(t, "POST", "/api/repairs/create", wash1, M{
			"cycle_id": 915, "defect_type": "阀门损坏", "repair_fee": 50,
			"deposit_deduct": 0, "fee_party": "WASHPLANT",
			"photos": []M{photo("KEG-0015-defect.png")}})
		mustOK(t, code, d, "915 返修(费用清洗厂)")
		rid2 := int64(d["repair_id"].(float64))
		_, bd := doJSON(t, "GET", "/api/finance/billing?party_type=WASHPLANT", finance, nil)
		var hit bool
		for _, x := range bd["charges"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["repair_id"].(float64) == float64(rid2) && m["party_name"] == "上海净桶中心" &&
				m["amount"].(float64) == 50 {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("¥50 返修费应进入上海净桶中心账单: %v", bd["charges"])
		}
	})
}
