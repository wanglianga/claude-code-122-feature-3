package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// 集成测试：需要本地 PostgreSQL（默认 5433），测试库 kegplatform_test
// 运行：先 createdb kegplatform_test，再 go test ./...

var testServer *httptest.Server

func TestMain(m *testing.M) {
	dsn := os.Getenv("KEG_TEST_DSN")
	if dsn == "" {
		dsn = "host=/tmp port=5433 user=node dbname=kegplatform_test sslmode=disable"
	}
	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		os.Stderr.WriteString("跳过集成测试（无法连接测试库）: " + err.Error() + "\n")
		os.Exit(0)
	}
	if err := db.Ping(); err != nil {
		os.Stderr.WriteString("跳过集成测试（无法连接测试库）: " + err.Error() + "\n")
		os.Exit(0)
	}
	if err := resetTestDB(); err != nil {
		os.Stderr.WriteString("测试库初始化失败: " + err.Error() + "\n")
		os.Exit(1)
	}
	mux := http.NewServeMux()
	registerRoutes(mux)
	testServer = httptest.NewServer(mux)
	code := m.Run()
	testServer.Close()
	os.Exit(code)
}

// ---------- 测试辅助 ----------

func resetTestDB() error {
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		return err
	}
	for _, name := range []string{"db/schema.sql", "db/seed.sql"} {
		b, err := sqlFS.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := db.Exec(string(b)); err != nil {
			return err
		}
	}
	return nil
}

func doJSON(t *testing.T, method, path, token string, body interface{}) (int, M) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, testServer.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败 %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out M
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return res.StatusCode, out
}

func login(t *testing.T, username string) string {
	t.Helper()
	code, d := doJSON(t, "POST", "/api/login", "", M{"username": username, "password": "demo123"})
	if code != 200 {
		t.Fatalf("登录失败 %s: %v", username, d)
	}
	return d["token"].(string)
}

func mustOK(t *testing.T, code int, d M, ctx string) {
	t.Helper()
	if code != 200 {
		t.Fatalf("%s: 期望 200 实际 %d: %v", ctx, code, d["error"])
	}
}

// ---------- 端到端主流程 ----------

func TestEndToEnd(t *testing.T) {
	admin := login(t, "admin")
	bar1 := login(t, "bar1")
	bar2 := login(t, "bar2")
	driver1 := login(t, "driver1")
	wash1 := login(t, "wash1")
	qc := login(t, "qc")
	finance := login(t, "finance")
	dealer := login(t, "dealer1")

	t.Run("登录与鉴权", func(t *testing.T) {
		// 错误密码
		code, _ := doJSON(t, "POST", "/api/login", "", M{"username": "admin", "password": "wrong"})
		if code != 401 {
			t.Fatalf("错误密码应 401, 实际 %d", code)
		}
		// 无 token
		code, _ = doJSON(t, "GET", "/api/overview", "", nil)
		if code != 401 {
			t.Fatalf("无 token 应 401, 实际 %d", code)
		}
		// 越权：司机不得访问财务
		code, d := doJSON(t, "GET", "/api/finance/deposits", driver1, nil)
		if code != 403 {
			t.Fatalf("司机访问财务应 403, 实际 %d (%v)", code, d)
		}
		// 越权：酒吧1 不能操作酒吧2 的桶
		code, d = doJSON(t, "POST", "/api/cycles/feedback/905", bar1, M{"pour_speed": "正常"})
		if code != 400 || !strings.Contains(d["error"].(string), "本店") {
			t.Fatalf("跨店操作应被拒, 实际 %d %v", code, d)
		}
	})

	t.Run("酒吧开桶反馈-打空-预约", func(t *testing.T) {
		// 901 初始 DELIVERED，首次反馈自动开桶
		code, d := doJSON(t, "POST", "/api/cycles/feedback/901", bar1,
			M{"pour_speed": "偏慢", "pressure": "气压过低", "taste_off": false})
		mustOK(t, code, d, "首次反馈")
		if d["first_tap"] != true {
			t.Fatalf("首次反馈应标记开桶: %v", d)
		}
		code, d = doJSON(t, "POST", "/api/cycles/empty/901", bar1, nil)
		mustOK(t, code, d, "标记打空")
		// 未打空的桶不能预约
		code, d = doJSON(t, "POST", "/api/cycles/reserve", bar1,
			M{"cycle_ids": []int64{902}, "pickup_date": "2026-09-28"})
		if code != 400 {
			t.Fatalf("未打空桶预约应 400, 实际 %d %v", code, d)
		}
		code, d = doJSON(t, "POST", "/api/cycles/reserve", bar1,
			M{"cycle_ids": []int64{901}, "pickup_date": "2026-09-28", "note": "测试"})
		mustOK(t, code, d, "预约回收")
	})

	t.Run("司机扫码回收", func(t *testing.T) {
		code, d := doJSON(t, "POST", "/api/pickups/scan", driver1,
			M{"keg_code": "KEG-0001", "appearance": "凹陷", "residue_level": "少量", "cap_status": "已封盖"})
		mustOK(t, code, d, "扫码 KEG-0001")
		// 重复扫码拒绝
		code, d = doJSON(t, "POST", "/api/pickups/scan", driver1, M{"keg_code": "KEG-0001"})
		if code != 400 || !strings.Contains(d["error"].(string), "已回收") {
			t.Fatalf("重复扫码应拒绝, 实际 %d %v", code, d)
		}
		// 不存在的桶
		code, _ = doJSON(t, "POST", "/api/pickups/scan", driver1, M{"keg_code": "KEG-9999"})
		if code != 400 {
			t.Fatalf("不存在桶应 400, 实际 %d", code)
		}
	})

	t.Run("回收审计-漏扫与逾期", func(t *testing.T) {
		code, d := doJSON(t, "POST", "/api/driver/audit", admin, M{})
		mustOK(t, code, d, "审计")
		found := d["missing_scans"].([]interface{})
		var has915 bool
		for _, x := range found {
			m := x.(map[string]interface{})
			if m["cycle_id"].(float64) == 915 {
				has915 = true
			}
		}
		if !has915 {
			t.Fatalf("审计应发现预约 802 的漏扫桶 KEG-0015(cycle 915): %v", found)
		}
	})

	t.Run("清洗检测合格与不合格", func(t *testing.T) {
		// 901 刚回收 → 合格
		code, d := doJSON(t, "POST", "/api/wash/checks", wash1,
			M{"cycle_id": 901, "inspector": "刘师傅", "passed": true,
				"pressure_ok": true, "seal_ok": true, "cleanliness_ok": true})
		mustOK(t, code, d, "清洗合格")
		if d["refillable"] != true || d["asset_status"] != "IN_STOCK" {
			t.Fatalf("合格桶应回库可再灌装: %v", d)
		}
		// 911 种子已是 FAILED；改为对 910 做不合格检测
		code, d = doJSON(t, "POST", "/api/wash/checks", wash1,
			M{"cycle_id": 910, "inspector": "马师傅", "passed": true,
				"pressure_ok": true, "seal_ok": false, "cleanliness_ok": false,
				"defect_note": "密封失效+内壁残留"})
		mustOK(t, code, d, "清洗不合格")
		if d["refillable"] != false || d["asset_status"] != "WASH_FAIL" {
			t.Fatalf("不合格桶应冻结: %v", d)
		}
		// 已检测的桶不能重复检测
		code, _ = doJSON(t, "POST", "/api/wash/checks", wash1, M{"cycle_id": 901, "passed": true})
		if code != 400 {
			t.Fatalf("重复检测应 400, 实际 %d", code)
		}
	})

	t.Run("财务押金结算", func(t *testing.T) {
		// 903 已被预约 801 但尚未扫码；先扫码再洗合格，然后释放
		code, d := doJSON(t, "POST", "/api/pickups/scan", driver1,
			M{"reservation_id": 801, "keg_code": "KEG-0003"})
		mustOK(t, code, d, "扫 KEG-0003")
		code, d = doJSON(t, "POST", "/api/wash/checks", wash1, M{"cycle_id": 903, "passed": true})
		mustOK(t, code, d, "洗 903")
		code, d = doJSON(t, "POST", "/api/finance/settle/903", finance, M{"action": "release"})
		mustOK(t, code, d, "释放 903")
		if d["released_amount"].(float64) != 700 {
			t.Fatalf("应释放 700: %v", d)
		}
		// 重复结算拒绝
		code, d = doJSON(t, "POST", "/api/finance/settle/903", finance, M{"action": "deduct"})
		if code != 400 || !strings.Contains(d["error"].(string), "已结算") {
			t.Fatalf("重复结算应拒绝: %d %v", code, d)
		}
		// 部分扣款
		code, d = doJSON(t, "POST", "/api/finance/settle/905", finance,
			M{"action": "partial", "deduct_amount": 200, "reason": "逾期+桶身划痕"})
		mustOK(t, code, d, "部分扣款 905")
		if d["released_amount"].(float64) != 500 || d["deducted_amount"].(float64) != 200 {
			t.Fatalf("部分扣款金额错误: %v", d)
		}
		// 非法扣款金额
		code, _ = doJSON(t, "POST", "/api/finance/settle/906", finance,
			M{"action": "partial", "deduct_amount": 9999})
		if code != 400 {
			t.Fatalf("超额扣款应 400, 实际 %d", code)
		}
	})

	t.Run("批次停售通知与库存分层", func(t *testing.T) {
		// 批次 104 种子已 SUSPENDED，先解除，再通过接口暂停以验证通知生成
		code, d := doJSON(t, "POST", "/api/batches/clear/104", qc, nil)
		mustOK(t, code, d, "解除 104")
		code, d = doJSON(t, "POST", "/api/batches/suspend/104", qc,
			M{"reason": "集成测试暂停"})
		mustOK(t, code, d, "暂停 104")
		n := d["notifications"].(map[string]interface{})
		// 已收货酒吧：醉猫(KEG-0008) → 1
		if n["bars"].(float64) != 1 {
			t.Fatalf("应通知 1 家在店酒吧: %v", n)
		}
		// 待配送订单：506 含酸啤 2 只 → 1
		if n["pending_orders"].(float64) != 1 {
			t.Fatalf("应通知 1 个待配送订单: %v", n)
		}
		// 经销商：502/506 关联华东、506 无经销商(成都直营) → 华东 1 家
		if n["dealers"].(float64) != 1 {
			t.Fatalf("应通知 1 家经销商: %v", n)
		}
		// 酒吧2 应看到 BAR 通知，酒吧1 不应看到
		_, d2 := doJSON(t, "GET", "/api/notifications", bar2, nil)
		saw := false
		for _, x := range d2["notifications"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["batch_no"] == "BT2026-SOUR-01" && m["audience"] == "BAR" {
				saw = true
			}
		}
		if !saw {
			t.Fatalf("醉猫酒馆应收到该批次的 BAR 通知")
		}
		_, d1 := doJSON(t, "GET", "/api/notifications", bar1, nil)
		for _, x := range d1["notifications"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["batch_no"] == "BT2026-SOUR-01" && m["audience"] == "BAR" {
				t.Fatalf("泡沫实验室不应收到该批次 BAR 定向通知")
			}
		}
		// 经销商可见 DEALER_STOCK 通知
		_, dd := doJSON(t, "GET", "/api/notifications", dealer, nil)
		sawDealer := false
		for _, x := range dd["notifications"].([]interface{}) {
			if x.(map[string]interface{})["audience"] == "DEALER_STOCK" {
				sawDealer = true
			}
		}
		if !sawDealer {
			t.Fatalf("经销商应收到库存排查通知")
		}
		// 召回库存分层
		code, detail := doJSON(t, "GET", "/api/batches/104", admin, nil)
		mustOK(t, code, detail, "批次明细")
		if detail["pending_orders"].(map[string]interface{})["kegs"].(float64) != 2 {
			t.Fatalf("待配送应为 2 只: %v", detail["pending_orders"])
		}
	})

	t.Run("停售批次发货拦截", func(t *testing.T) {
		code, d := doJSON(t, "POST", "/api/orders/dispatch/506", admin,
			M{"driver_id": 1, "bar_receiver": "小唐"})
		if code != 400 || !strings.Contains(d["error"].(string), "暂停销售") {
			t.Fatalf("含停售批次的订单应被拦截: %d %v", code, d)
		}
	})

	t.Run("建单-发货正向闭环", func(t *testing.T) {
		// 新建订单给醉猫酒馆：批次 103 两只
		code, d := doJSON(t, "POST", "/api/orders", admin, M{
			"bar_id": 5, "driver_id": 1, "deposit_term_days": 20,
			"items": []M{{"batch_id": 103, "qty": 2}},
		})
		mustOK(t, code, d, "建单")
		oid := int64(d["order_id"].(float64))
		code, d = doJSON(t, "POST", "/api/orders/dispatch/"+itoa(oid), admin,
			M{"driver_id": 1, "bar_receiver": "老陈"})
		mustOK(t, code, d, "发货")
		if len(d["dispatched"].([]interface{})) != 2 {
			t.Fatalf("应发出 2 只: %v", d)
		}
	})

	t.Run("按桶追溯时间线", func(t *testing.T) {
		code, d := doJSON(t, "GET", "/api/kegs/trace/KEG-0003", admin, nil)
		mustOK(t, code, d, "追溯 KEG-0003")
		types := map[string]bool{}
		for _, e := range d["events"].([]interface{}) {
			types[e.(map[string]interface{})["event_type"].(string)] = true
		}
		for _, want := range []string{"DELIVERED", "EMPTIED", "PICKED", "WASHED", "DEPOSIT_SETTLED"} {
			if !types[want] {
				t.Fatalf("时间线缺少事件 %s: %v", want, types)
			}
		}
	})

	t.Run("多店口味投诉触发品控停售建议", func(t *testing.T) {
		code, d := doJSON(t, "GET", "/api/qc/overview", qc, nil)
		mustOK(t, code, d, "品控总览")
		var hit bool
		for _, x := range d["batch_alerts"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["batch_no"] == "BT2026-ST-01" && m["suggest_suspend"] == true {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("世涛批次被 2 家酒吧投诉口味异常，应建议停售")
		}
	})

	t.Run("清洗不合格返修全流程", func(t *testing.T) {
		wash2 := login(t, "wash2")
		brewery := login(t, "brewery")
		bar3 := login(t, "bar3")

		// 1x1 合法 PNG（base64）
		png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

		// 种子已为 KEG-0011(cycle 911) 生成返修单 1101：wash2 / 胡同里 可见，账单 3001 待确认
		code, d := doJSON(t, "GET", "/api/rework/orders", wash2, nil)
		mustOK(t, code, d, "wash2 返修单列表")
		var seedRW map[string]interface{}
		for _, x := range d["orders"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["rework_no"] == "RW-0921-001" {
				seedRW = m
			}
		}
		if seedRW == nil {
			t.Fatalf("wash2 应看到种子返修单 RW-0921-001: %v", d["orders"])
		}
		if seedRW["bar"] != "胡同里 Beer Bar" || seedRW["driver"] != "李师傅" {
			t.Fatalf("返修单追溯链错误: %v", seedRW)
		}
		if seedRW["rework_fee"].(float64) != 200 || seedRW["deposit_deduct"].(float64) != 150 {
			t.Fatalf("返修费/押金扣减快照错误: %v", seedRW)
		}
		if seedRW["found_photos"].(float64) != 1 {
			t.Fatalf("应留存 1 张发现照片: %v", seedRW)
		}
		// 责任酒吧可见本店返修单
		code, d = doJSON(t, "GET", "/api/rework/orders", bar3, nil)
		mustOK(t, code, d, "酒吧返修单列表")
		saw := false
		for _, x := range d["orders"].([]interface{}) {
			if x.(map[string]interface{})["rework_no"] == "RW-0921-001" {
				saw = true
			}
		}
		if !saw {
			t.Fatalf("胡同里应看到本店返修单")
		}

		// 910（KEG-0010）在前面的用例中被 wash1 判为清洗不合格 → 出现在待发起返修
		code, d = doJSON(t, "GET", "/api/rework/pending", wash1, nil)
		mustOK(t, code, d, "待返修列表")
		var pend map[string]interface{}
		for _, x := range d["pending"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["cycle_id"].(float64) == 910 {
				pend = m
			}
		}
		if pend == nil {
			t.Fatalf("KEG-0010(910) 应在待返修列表: %v", d["pending"])
		}
		if pend["bar"] != "胡同里 Beer Bar" || pend["driver"] != "李师傅" || pend["wash_batch_no"] == "" {
			t.Fatalf("待返修追溯信息缺失: %v", pend)
		}
		// wash2（北京厂）不应看到 wash1 检出的桶
		code, d = doJSON(t, "GET", "/api/rework/pending", wash2, nil)
		mustOK(t, code, d, "wash2 待返修列表")
		for _, x := range d["pending"].([]interface{}) {
			if x.(map[string]interface{})["cycle_id"].(float64) == 910 {
				t.Fatalf("wash2 不应看到 wash1 检出的待返修桶")
			}
		}

		// 发起返修：费用 120 酒吧承担，押金扣减 200/700，附 1 张照片
		code, d = doJSON(t, "POST", "/api/rework/orders", wash1, M{
			"cycle_id": 910, "defect_type": "ODOR", "defect_note": "桶内有明显酸臭味，需除味返修",
			"rework_fee": 120, "bill_party": "BAR", "deposit_deduct": 200,
			"photos": []M{{"file_name": "keg10.png", "content_type": "image/png", "data": png}},
		})
		mustOK(t, code, d, "发起返修 910")
		if d["status"] != "OPEN" || d["deposit_status"] != "PARTIAL" {
			t.Fatalf("返修单状态/押金状态错误: %v", d)
		}
		rwID := int64(d["rework_id"].(float64))

		// 桶仍暂停灌装
		code, d = doJSON(t, "GET", "/api/kegs/trace/KEG-0010", admin, nil)
		mustOK(t, code, d, "追溯 KEG-0010")
		if d["keg"].(map[string]interface{})["refillable"] != false ||
			d["keg"].(map[string]interface{})["asset_status"] != "WASH_FAIL" {
			t.Fatalf("返修期间桶应 WASH_FAIL/禁灌装: %v", d["keg"])
		}
		if d["current"].(map[string]interface{})["deducted_amount"].(float64) != 200 {
			t.Fatalf("押金应已扣减 200: %v", d["current"])
		}

		// 无照片拒绝
		code, _ = doJSON(t, "POST", "/api/rework/orders", wash1, M{
			"cycle_id": 911, "defect_type": "VALVE_DAMAGED", "defect_note": "x", "rework_fee": 10,
		})
		if code != 400 {
			t.Fatalf("无照片发起返修应 400, 实际 %d", code)
		}
		// 重复发起拒绝
		code, _ = doJSON(t, "POST", "/api/rework/orders", wash1, M{
			"cycle_id": 910, "defect_type": "ODOR", "defect_note": "重复", "rework_fee": 1,
			"photos": []M{{"file_name": "a.png", "content_type": "image/png", "data": png}},
		})
		if code != 400 {
			t.Fatalf("重复发起返修应 400, 实际 %d", code)
		}
		// 越权复检
		code, _ = doJSON(t, "POST", "/api/rework/reinspect/"+itoa(rwID), wash2, M{"passed": true})
		if code != 400 {
			t.Fatalf("wash2 复检 wash1 返修单应被拒, 实际 %d", code)
		}

		// 复检仍不合格 → 单据保持 OPEN
		code, d = doJSON(t, "POST", "/api/rework/reinspect/"+itoa(rwID), wash1, M{
			"passed": true, "seal_ok": false, "cleanliness_ok": false, "note": "除味不彻底",
		})
		mustOK(t, code, d, "复检仍不合格")
		if d["passed"] != false || d["status"] != "OPEN" || d["reinspect_result"] != "STILL_FAIL" {
			t.Fatalf("应保持返修中: %v", d)
		}
		// 再次复检合格（附复检照）→ REWORKED，等待仓库确认；桶此时仍不可灌装
		code, d = doJSON(t, "POST", "/api/rework/reinspect/"+itoa(rwID), wash1, M{
			"passed": true, "pressure_ok": true, "seal_ok": true, "cleanliness_ok": true,
			"note": "二次除味完成，复检合格", "inspector": "刘师傅",
			"photos": []M{{"file_name": "ok.png", "content_type": "image/png", "data": png}},
		})
		mustOK(t, code, d, "复检合格")
		if d["status"] != "REWORKED" {
			t.Fatalf("应进入待仓库确认: %v", d)
		}
		code, d = doJSON(t, "GET", "/api/kegs/trace/KEG-0010", admin, nil)
		if d["keg"].(map[string]interface{})["refillable"] != false {
			t.Fatalf("仓库确认前桶仍应禁灌装")
		}

		// 清洗厂不能做仓库确认
		code, _ = doJSON(t, "POST", "/api/rework/confirm/"+itoa(rwID), wash1, M{"action": "restore"})
		if code != 403 {
			t.Fatalf("清洗厂仓库确认应 403, 实际 %d", code)
		}
		// 酒厂仓库确认恢复灌装
		code, d = doJSON(t, "POST", "/api/rework/confirm/"+itoa(rwID), brewery,
			M{"action": "restore", "note": "复检合格，准予回库"})
		mustOK(t, code, d, "仓库确认恢复")
		if d["asset_status"] != "IN_STOCK" || d["refillable"] != true {
			t.Fatalf("应回库恢复灌装: %v", d)
		}
		code, d = doJSON(t, "GET", "/api/kegs/trace/KEG-0010", admin, nil)
		if d["current"].(map[string]interface{})["stage"] != "WASHED" {
			t.Fatalf("周期应回到 WASHED: %v", d["current"])
		}
		types := map[string]bool{}
		for _, e := range d["events"].([]interface{}) {
			types[e.(map[string]interface{})["event_type"].(string)] = true
		}
		for _, want := range []string{"REWORK_OPENED", "REWORK_REINSPECTED", "REWORK_CONFIRMED"} {
			if !types[want] {
				t.Fatalf("时间线缺少返修事件 %s: %v", want, types)
			}
		}

		// 返修单详情含两类照片
		code, d = doJSON(t, "GET", "/api/rework/orders/"+itoa(rwID), brewery, nil)
		mustOK(t, code, d, "返修单详情")
		o := d["order"].(map[string]interface{})
		var foundN, reN int64
		var photoID int64
		for _, p := range o["photos"].([]interface{}) {
			ph := p.(map[string]interface{})
			if ph["phase"] == "FOUND" {
				foundN++
				photoID = int64(ph["id"].(float64))
			}
			if ph["phase"] == "REINSPECT" {
				reN++
			}
		}
		if foundN != 1 || reN != 1 {
			t.Fatalf("应有 1 张发现照 + 1 张复检照: found=%d re=%d", foundN, reN)
		}
		// 照片无 token 401，带 token 200 且为 PNG
		req, _ := http.NewRequest("GET", testServer.URL+"/api/rework/photo/"+itoa(photoID), nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("照片无 token 应 401, 实际 %d", res.StatusCode)
		}
		req, _ = http.NewRequest("GET", testServer.URL+"/api/rework/photo/"+itoa(photoID)+"?token="+wash1, nil)
		res, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		img, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !bytes.HasPrefix(img, []byte("\x89PNG")) {
			t.Fatalf("照片应返回 PNG, status=%d bytes=%d", res.StatusCode, len(img))
		}

		// 账单：910 返修费 120 待财务确认；确认后入账
		code, d = doJSON(t, "GET", "/api/finance/bills?status=PENDING", finance, nil)
		mustOK(t, code, d, "待确认账单")
		var bill map[string]interface{}
		for _, x := range d["bills"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["rework_no"] != nil && m["keg_code"] == "KEG-0010" {
				bill = m
			}
		}
		if bill == nil || bill["amount"].(float64) != 120 || bill["party_type"] != "BAR" {
			t.Fatalf("应存在 KEG-0010 酒吧待确认账单 ¥120: %v", d["bills"])
		}
		billID := int64(bill["id"].(float64))
		code, d = doJSON(t, "POST", "/api/finance/bills/confirm/"+itoa(billID), finance, nil)
		mustOK(t, code, d, "财务确认账单")
		// 重复确认拒绝
		code, _ = doJSON(t, "POST", "/api/finance/bills/confirm/"+itoa(billID), finance, nil)
		if code != 400 {
			t.Fatalf("重复确认账单应 400, 实际 %d", code)
		}
		// 清洗厂承担场景：915 洗成不合格后发起返修，费用挂清洗厂账单
		code, d = doJSON(t, "POST", "/api/wash/checks", wash1, M{
			"cycle_id": 915, "passed": true, "pressure_ok": true, "seal_ok": false,
			"cleanliness_ok": false, "defect_note": "清洗工艺不到位，密封件受损",
		})
		mustOK(t, code, d, "915 清洗不合格")
		code, d = doJSON(t, "POST", "/api/rework/orders", wash1, M{
			"cycle_id": 915, "defect_type": "VALVE_DAMAGED", "defect_note": "判定清洗厂工艺责任",
			"rework_fee": 80, "bill_party": "WASHPLANT", "deposit_deduct": 0,
			"photos": []M{{"file_name": "keg15.png", "content_type": "image/png", "data": png}},
		})
		mustOK(t, code, d, "发起清洗厂承担返修 915")
		code, d = doJSON(t, "GET", "/api/finance/bills", wash1, nil)
		mustOK(t, code, d, "清洗厂账单")
		hitWP := false
		for _, x := range d["bills"].([]interface{}) {
			m := x.(map[string]interface{})
			if m["party_type"] == "WASHPLANT" && m["keg_code"] == "KEG-0015" && m["amount"].(float64) == 80 {
				hitWP = true
			}
		}
		if !hitWP {
			t.Fatalf("清洗厂应看到本厂承担的 KEG-0015 账单: %v", d["bills"])
		}
		// 财务无权确认不存在的账单已在上面覆盖；总览含返修计数
		code, d = doJSON(t, "GET", "/api/overview", admin, nil)
		mustOK(t, code, d, "总览")
		if d["rework_open"].(float64) < 1 {
			t.Fatalf("总览返修中计数应 ≥1: %v", d)
		}
	})
}

func TestRegisterKegsNumbering(t *testing.T) {
	if err := resetTestDB(); err != nil {
		t.Fatal(err)
	}
	admin := login(t, "admin")
	code, d := doJSON(t, "POST", "/api/kegs/register", admin,
		M{"count": 3, "capacity_liters": 20})
	mustOK(t, code, d, "登记新桶")
	created := d["created"].([]interface{})
	want := []string{"KEG-0031", "KEG-0032", "KEG-0033"}
	for i, w := range want {
		if created[i].(string) != w {
			t.Fatalf("新桶编号错误: 得到 %v 期望 %v", created, want)
		}
	}
}
