package main

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
)

// /api/batches  GET 列表 / POST 新建
func routeBatches(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		handleListBatches(w, r)
	case http.MethodPost:
		auth(requireRole("brewery admin", handleCreateBatch))(w, r)
	default:
		fail(w, 405, "仅支持 GET/POST")
	}
}

func handleListBatches(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
		SELECT b.id, be.name, be.style, b.batch_no, b.brewed_date, COALESCE(text(b.filled_date),''),
		       b.best_before_days, b.total_kegs, b.status, COALESCE(b.suspend_reason,'')
		FROM batches b JOIN beers be ON be.id=b.beer_id
		ORDER BY b.id DESC`)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := scanRows(rows, func(rs rowScanner) (M, error) {
		var id, total, days int64
		var beer, style, batchNo, brewed, filled, status, reason string
		if err := rs.Scan(&id, &beer, &style, &batchNo, &brewed, &filled, &days, &total, &status, &reason); err != nil {
			return nil, err
		}
		m := M{"id": id, "beer": beer, "style": style, "batch_no": batchNo,
			"brewed_date": brewed, "filled_date": filled, "best_before_days": days,
			"total_kegs": total, "status": status, "suspend_reason": reason}

		// 该批次投诉酒吧数 / 投诉条数
		var bars, complaints int
		_ = db.QueryRow(`SELECT count(DISTINCT f.bar_id), count(*)
			FROM tap_feedback f JOIN keg_cycles c ON c.id=f.cycle_id
			WHERE c.batch_id=$1 AND f.taste_off`, id).Scan(&bars, &complaints)
		m["taste_bars"] = bars
		m["taste_complaints"] = complaints

		// 在店桶数
		var atBar int
		_ = db.QueryRow(`SELECT count(*) FROM keg_cycles
			WHERE batch_id=$1 AND stage IN ('DELIVERED','TAPPED','EMPTY','RESERVED')`, id).Scan(&atBar)
		m["at_bar_kegs"] = atBar
		return m, nil
	})
	writeOK(w, M{"batches": list})
}

func handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BeerID         int64  `json:"beer_id"`
		BatchNo        string `json:"batch_no"`
		BrewedDate     string `json:"brewed_date"`
		FilledDate     string `json:"filled_date"`
		BestBeforeDays int    `json:"best_before_days"`
		TotalKegs      int    `json:"total_kegs"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.BeerID == 0 || req.BatchNo == "" || req.BrewedDate == "" {
		fail(w, 400, "酒款、批次号、酿造日期必填")
		return
	}
	if req.BestBeforeDays <= 0 {
		req.BestBeforeDays = 30
	}
	var id int64
	err := db.QueryRow(`INSERT INTO batches
		(beer_id,batch_no,brewed_date,filled_date,best_before_days,total_kegs)
		VALUES ($1,$2,$3,NULLIF($4,'')::date,$5,$6) RETURNING id`,
		req.BeerID, req.BatchNo, req.BrewedDate, req.FilledDate, req.BestBeforeDays, req.TotalKegs).Scan(&id)
	if err != nil {
		fail(w, 500, "创建批次失败: "+err.Error())
		return
	}
	writeOK(w, M{"id": id})
}

// /api/batches/<id> 明细；/api/batches/suspend|recall|clear/<id>
func handleBatchSubroute(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/batches/")
	parts := strings.SplitN(p, "/", 2)
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			fail(w, 405, "仅支持 GET")
			return
		}
		batchDetail(w, atoi64(parts[0]))
		return
	}
	action, id := parts[0], atoi64(parts[1])
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	switch action {
	case "suspend":
		batchFreeze(w, r, id, "SUSPENDED", "批次暂停销售")
	case "recall":
		batchFreeze(w, r, id, "RECALLED", "批次召回")
	case "clear":
		batchClear(w, id)
	default:
		fail(w, 404, "未知操作: "+action)
	}
}

func batchDetail(w http.ResponseWriter, batchID int64) {
	var b struct {
		Beer, Style, BatchNo, Brewed, Filled, Status, Reason sql.NullString
		Total, Days                                          int
	}
	err := db.QueryRow(`SELECT be.name, be.style, b.batch_no, text(b.brewed_date),
			COALESCE(text(b.filled_date),''), b.status, COALESCE(b.suspend_reason,''),
			b.total_kegs, b.best_before_days
		FROM batches b JOIN beers be ON be.id=b.beer_id WHERE b.id=$1`, batchID).
		Scan(&b.Beer, &b.Style, &b.BatchNo, &b.Brewed, &b.Filled, &b.Status, &b.Reason, &b.Total, &b.Days)
	if err == sql.ErrNoRows {
		fail(w, 404, "批次不存在")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}

	// 召回库存分层
	tier := func(name, where string, args ...interface{}) M {
		var n int
		var deposit float64
		q := `SELECT count(*), COALESCE(sum(c.deposit_amount),0) FROM keg_cycles c WHERE c.batch_id=$1 AND ` + where
		dbArgs := append([]interface{}{batchID}, args...)
		_ = db.QueryRow(q, dbArgs...).Scan(&n, &deposit)
		return M{"name": name, "kegs": n, "deposit": deposit}
	}
	tiers := []M{
		tier("未开桶·在店", "c.stage='DELIVERED' AND c.tapped_at IS NULL"),
		tier("已开桶·在店/在售", "c.tapped_at IS NOT NULL AND c.stage IN ('TAPPED','EMPTY','RESERVED')"),
		tier("回收在途/待清洗", "c.stage IN ('RETURNED')"),
		tier("清洗不合格返修中", "c.stage='REPAIRING'"),
		tier("已闭环(清洗合格/不合格/丢失)", "c.stage IN ('WASHED','FAILED') OR c.lost"),
	}

	// 待配送订单中的桶（尚未分配具体酒桶 + 已分配未发货）
	rows, _ := db.Query(`
		SELECT o.id, o.order_no, b.name, COALESCE(d.name,'直营'), oi.qty, o.status, o.is_festival
		FROM order_items oi
		JOIN orders o ON o.id=oi.order_id
		JOIN bars b ON b.id=o.bar_id
		LEFT JOIN dealers d ON d.id=o.dealer_id
		WHERE oi.batch_id=$1 AND o.status IN ('PLANNED','DELIVERING')
		ORDER BY o.id`, batchID)
	pending := scanRows(rows, func(rs rowScanner) (M, error) {
		var id, qty int64
		var orderNo, bar, dealer, status string
		var festival bool
		if err := rs.Scan(&id, &orderNo, &bar, &dealer, &qty, &status, &festival); err != nil {
			return nil, err
		}
		return M{"order_id": id, "order_no": orderNo, "bar": bar, "dealer": dealer,
			"qty": qty, "status": status, "is_festival": festival}, nil
	})
	pendingQty := 0
	for _, p := range pending {
		pendingQty += int(p["qty"].(int64))
	}

	// 已收货酒吧明细（区分已开/未开）
	rows, _ = db.Query(`
		SELECT b.id, b.name, c.city_id, ci.name,
			sum(CASE WHEN c.tapped_at IS NOT NULL THEN 1 ELSE 0 END),
			sum(CASE WHEN c.tapped_at IS NULL THEN 1 ELSE 0 END),
			count(*)
		FROM keg_cycles c
		JOIN bars b ON b.id=c.bar_id
		JOIN cities ci ON ci.id=c.city_id
		WHERE c.batch_id=$1 AND c.stage IN ('DELIVERED','TAPPED','EMPTY','RESERVED','RETURNED')
		GROUP BY b.id,b.name,c.city_id,ci.name ORDER BY b.id`, batchID)
	bars := scanRows(rows, func(rs rowScanner) (M, error) {
		var id, cityID, tapped, untapped, total int64
		var name, city string
		if err := rs.Scan(&id, &name, &cityID, &city, &tapped, &untapped, &total); err != nil {
			return nil, err
		}
		return M{"bar_id": id, "bar": name, "city_id": cityID, "city": city,
			"tapped": tapped, "untapped": untapped, "total": total}, nil
	})

	assigned := 0
	for _, t := range tiers {
		assigned += t["kegs"].(int)
	}
	breweryStock := b.Total - assigned - pendingQty
	if breweryStock < 0 {
		breweryStock = 0
	}

	// 投诉明细
	rows, _ = db.Query(`
		SELECT f.id, b.name, COALESCE(f.reported_by,''), COALESCE(f.pour_speed,''),
		       COALESCE(f.pressure,''), f.taste_off, COALESCE(f.taste_note,''),
		       COALESCE(f.complaint,''), f.created_at
		FROM tap_feedback f JOIN keg_cycles c ON c.id=f.cycle_id
		JOIN bars b ON b.id=f.bar_id
		WHERE c.batch_id=$1 ORDER BY f.id`, batchID)
	feedbacks := scanRows(rows, func(rs rowScanner) (M, error) {
		var id int64
		var bar, by, speed, pressure, taste, complaint string
		var off bool
		var at sql.NullTime
		if err := rs.Scan(&id, &bar, &by, &speed, &pressure, &off, &taste, &complaint, &at); err != nil {
			return nil, err
		}
		return M{"id": id, "bar": bar, "by": by, "pour_speed": speed, "pressure": pressure,
			"taste_off": off, "taste_note": taste, "complaint": complaint, "at": tsStr(at)}, nil
	})

	writeOK(w, M{
		"batch": M{
			"id": batchID, "beer": b.Beer.String, "style": b.Style.String,
			"batch_no": b.BatchNo.String, "brewed_date": b.Brewed.String,
			"filled_date": b.Filled.String, "best_before_days": b.Days,
			"total_kegs": b.Total, "status": b.Status.String, "suspend_reason": b.Reason.String,
		},
		"tiers":          tiers,
		"brewery_stock":  breweryStock,
		"pending_orders": M{"items": pending, "kegs": pendingQty},
		"bars":           bars,
		"feedbacks":      feedbacks,
	})
}

// batchFreeze 暂停销售或召回：更新批次并向 已收货酒吧 / 待配送订单 / 经销商库存 发通知
func batchFreeze(w http.ResponseWriter, r *http.Request, batchID int64, status, titlePrefix string) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &req)
	if strings.TrimSpace(req.Reason) == "" {
		fail(w, 400, "请填写原因")
		return
	}

	u := currentUser(r)
	var batchNo, beerName string
	err := db.QueryRow(`SELECT b.batch_no, be.name FROM batches b JOIN beers be ON be.id=b.beer_id WHERE b.id=$1`, batchID).
		Scan(&batchNo, &beerName)
	if err == sql.ErrNoRows {
		fail(w, 404, "批次不存在")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}

	notified := M{"bars": 0, "pending_orders": 0, "dealers": 0}
	err = runTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`UPDATE batches SET status=$1, suspend_reason=$2, suspended_at=now() WHERE id=$3`,
			status, req.Reason, batchID); err != nil {
			return err
		}

		// 1) 已收货酒吧（桶仍在店或回收链路中，未闭环）
		barRows, err := tx.Query(`
			SELECT DISTINCT c.bar_id, b.name,
			       sum(CASE WHEN c.tapped_at IS NULL THEN 1 ELSE 0 END),
			       sum(CASE WHEN c.tapped_at IS NOT NULL THEN 1 ELSE 0 END)
			FROM keg_cycles c JOIN bars b ON b.id=c.bar_id
			WHERE c.batch_id=$1 AND c.stage IN ('DELIVERED','TAPPED','EMPTY','RESERVED','RETURNED')
			GROUP BY c.bar_id,b.name`, batchID)
		if err != nil {
			return err
		}
		type binfo struct {
			id, untapped, tapped int64
			name                 string
		}
		var bars []binfo
		for barRows.Next() {
			var x binfo
			if err := barRows.Scan(&x.id, &x.name, &x.untapped, &x.tapped); err != nil {
				return err
			}
			bars = append(bars, x)
		}
		barRows.Close()
		for _, x := range bars {
			body := "该批次" + titlePrefix + "。贵店在店" + beerName + "（" + batchNo + "）：未开桶 " +
				itoa(x.untapped) + " 只、已开桶 " + itoa(x.tapped) +
				" 只。未开桶请勿继续开桶售卖，已开桶请立即停售并等待回收/复检安排。"
			if _, err := tx.Exec(`INSERT INTO notifications (batch_id,audience,target_id,title,body)
				VALUES ($1,'BAR',$2,$3,$4)`, batchID, x.id,
				titlePrefix+"通知："+batchNo+" "+beerName, body); err != nil {
				return err
			}
			notified["bars"] = notified["bars"].(int) + 1
		}

		// 2) 待配送订单（PLANNED/DELIVERING）
		oRows, err := tx.Query(`
			SELECT o.id, o.order_no, b.name, sum(oi.qty)
			FROM order_items oi JOIN orders o ON o.id=oi.order_id
			JOIN bars b ON b.id=o.bar_id
			WHERE oi.batch_id=$1 AND o.status IN ('PLANNED','DELIVERING')
			GROUP BY o.id,o.order_no,b.name`, batchID)
		if err != nil {
			return err
		}
		type oinfo struct {
			id, qty int64
			no, bar string
		}
		var ords []oinfo
		for oRows.Next() {
			var x oinfo
			if err := oRows.Scan(&x.id, &x.no, &x.bar, &x.qty); err != nil {
				return err
			}
			ords = append(ords, x)
		}
		oRows.Close()
		for _, x := range ords {
			body := "订单 " + x.no + "（" + x.bar + "）含停售批次 " + batchNo + " 共 " + itoa(x.qty) +
				" 只，订单冻结，暂停配送，请联系酒厂更换批次。"
			if _, err := tx.Exec(`INSERT INTO notifications (batch_id,audience,target_id,title,body)
				VALUES ($1,'PENDING_ORDER',$2,$3,$4)`, batchID, x.id,
				"待配送订单冻结："+x.no, body); err != nil {
				return err
			}
			notified["pending_orders"] = notified["pending_orders"].(int) + 1
		}

		// 3) 经销商库存（覆盖在店酒吧所属经销商 + 待配送订单经销商，去重）
		dRows, err := tx.Query(`
			SELECT DISTINCT d.id, d.name
			FROM dealers d WHERE d.id IN (
				SELECT b.dealer_id FROM keg_cycles c JOIN bars b ON b.id=c.bar_id
					WHERE c.batch_id=$1 AND b.dealer_id IS NOT NULL
					AND c.stage IN ('DELIVERED','TAPPED','EMPTY','RESERVED','RETURNED')
				UNION
				SELECT o.dealer_id FROM order_items oi JOIN orders o ON o.id=oi.order_id
					WHERE oi.batch_id=$1 AND o.dealer_id IS NOT NULL
					AND o.status IN ('PLANNED','DELIVERING')
			)`, batchID)
		if err != nil {
			return err
		}
		type dinfo struct {
			id   int64
			name string
		}
		var deals []dinfo
		for dRows.Next() {
			var x dinfo
			if err := dRows.Scan(&x.id, &x.name); err != nil {
				return err
			}
			deals = append(deals, x)
		}
		dRows.Close()
		for _, x := range deals {
			body := "请立即盘点渠道与仓库内 " + beerName + "（" + batchNo + "）库存，冻结发货并回复库存数量，等待品控复检结论。"
			if _, err := tx.Exec(`INSERT INTO notifications (batch_id,audience,target_id,title,body)
				VALUES ($1,'DEALER_STOCK',$2,$3,$4)`, batchID, x.id,
				"经销商库存排查通知："+batchNo, body); err != nil {
				return err
			}
			notified["dealers"] = notified["dealers"].(int) + 1
		}

		// 4) 全渠道广播
		if _, err := tx.Exec(`INSERT INTO notifications (batch_id,audience,target_id,title,body)
			VALUES ($1,'ALL',NULL,$2,$3)`, batchID,
			"全渠道通告："+batchNo+" "+beerName+titlePrefix,
			"原因："+req.Reason+"。已收货酒吧、待配送订单、经销商库存全部冻结，避免继续开桶售卖。"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	_ = u
	writeOK(w, M{"status": status, "notifications": notified})
}

func batchClear(w http.ResponseWriter, batchID int64) {
	res, err := db.Exec(`UPDATE batches SET status='NORMAL', suspend_reason=NULL, suspended_at=NULL
		WHERE id=$1 AND status IN ('SUSPENDED','RECALLED')`, batchID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 400, "批次当前状态不可解除")
		return
	}
	writeOK(w, M{"ok": true})
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
