package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// logKegEvent 记录桶级事件流水
func logKegEvent(tx *sql.Tx, kegID, cycleID int64, eventType, actor string, detail M) error {
	if detail == nil {
		detail = M{}
	}
	b, _ := json.Marshal(detail)
	q := `INSERT INTO keg_events (keg_id, cycle_id, event_type, detail, actor) VALUES ($1,$2,$3,$4,$5)`
	_, err := tx.Exec(q, kegID, nullableCycle(cycleID), eventType, string(b), actor)
	return err
}

func nullableCycle(id int64) interface{} {
	if id == 0 {
		return nil
	}
	return id
}

// runTx 在事务中执行
func runTx(fn func(tx *sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---------- 下拉选项 ----------

func handleOptions(w http.ResponseWriter, r *http.Request) {
	out := M{}

	rows, err := db.Query(`SELECT id, name FROM cities ORDER BY id`)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out["cities"] = scanRows(rows, func(rs rowScanner) (M, error) {
		var id int64
		var name string
		if err := rs.Scan(&id, &name); err != nil {
			return nil, err
		}
		return M{"id": id, "name": name}, nil
	})

	rows, _ = db.Query(`SELECT b.id, c.id, c.name, COALESCE(d.name,'直营'), b.name, b.manager
		FROM bars b JOIN cities c ON c.id=b.city_id
		LEFT JOIN dealers d ON d.id=b.dealer_id ORDER BY b.id`)
	out["bars"] = scanRows(rows, func(rs rowScanner) (M, error) {
		var id, cityID int64
		var city, dealer, name, manager string
		if err := rs.Scan(&id, &cityID, &city, &dealer, &name, &manager); err != nil {
			return nil, err
		}
		return M{"id": id, "city_id": cityID, "city": city, "dealer": dealer, "name": name, "manager": manager}, nil
	})

	rows, _ = db.Query(`SELECT d.id, c.id, c.name, d.name, d.contact FROM dealers d JOIN cities c ON c.id=d.city_id ORDER BY d.id`)
	out["dealers"] = scanRows(rows, func(rs rowScanner) (M, error) {
		var id, cityID int64
		var city, name, contact sql.NullString
		if err := rs.Scan(&id, &cityID, &city, &name, &contact); err != nil {
			return nil, err
		}
		return M{"id": id, "city_id": cityID, "city": city.String, "name": name.String, "contact": contact.String}, nil
	})

	rows, _ = db.Query(`SELECT dr.id, c.id, c.name, dr.name, dr.phone FROM drivers dr JOIN cities c ON c.id=dr.city_id ORDER BY dr.id`)
	out["drivers"] = scanRows(rows, func(rs rowScanner) (M, error) {
		var id, cityID int64
		var city, name, phone sql.NullString
		if err := rs.Scan(&id, &cityID, &city, &name, &phone); err != nil {
			return nil, err
		}
		return M{"id": id, "city_id": cityID, "city": city.String, "name": name.String, "phone": phone.String}, nil
	})

	rows, _ = db.Query(`SELECT wp.id, c.id, c.name, wp.name FROM washplants wp JOIN cities c ON c.id=wp.city_id ORDER BY wp.id`)
	out["washplants"] = scanRows(rows, func(rs rowScanner) (M, error) {
		var id, cityID int64
		var city, name string
		if err := rs.Scan(&id, &cityID, &city, &name); err != nil {
			return nil, err
		}
		return M{"id": id, "city_id": cityID, "city": city, "name": name}, nil
	})

	rows, _ = db.Query(`SELECT id, name, style, abv FROM beers ORDER BY id`)
	out["beers"] = scanRows(rows, func(rs rowScanner) (M, error) {
		var id int64
		var name, style string
		var abv float64
		if err := rs.Scan(&id, &name, &style, &abv); err != nil {
			return nil, err
		}
		return M{"id": id, "name": name, "style": style, "abv": abv}, nil
	})

	rows, _ = db.Query(`SELECT b.id, be.name, b.batch_no, b.status, b.filled_date, b.best_before_days
		FROM batches b JOIN beers be ON be.id=b.beer_id ORDER BY b.id`)
	out["batches"] = scanRows(rows, func(rs rowScanner) (M, error) {
		var id int64
		var beer, batchNo, status string
		var filled sql.NullTime
		var days int
		if err := rs.Scan(&id, &beer, &batchNo, &status, &filled, &days); err != nil {
			return nil, err
		}
		return M{"id": id, "beer": beer, "batch_no": batchNo, "status": status,
			"filled_date": dateStr(filled), "best_before_days": days}, nil
	})

	writeOK(w, out)
}

// ---------- 全局总览 ----------

func handleOverview(w http.ResponseWriter, r *http.Request) {
	out := M{}

	count := func(q string, args ...interface{}) int {
		var n int
		_ = db.QueryRow(q, args...).Scan(&n)
		return n
	}

	out["keg_total"] = count(`SELECT count(*) FROM kegs`)
	out["keg_by_status"] = countMap(`SELECT asset_status, count(*) FROM kegs GROUP BY 1`)
	out["cycle_by_stage"] = countMap(`SELECT stage, count(*) FROM keg_cycles GROUP BY 1`)
	out["batch_by_status"] = countMap(`SELECT status, count(*) FROM batches GROUP BY 1`)

	// 预警：账期逾期未闭环
	out["overdue"] = count(`SELECT count(*) FROM keg_cycles
		WHERE deposit_due_date < CURRENT_DATE AND stage NOT IN ('WASHED','FAILED')`)
	// 预警：司机漏扫
	out["missing_scan"] = count(`SELECT count(*) FROM keg_cycles WHERE missing_scan`)
	// 预警：清洗不合格
	out["wash_fail"] = count(`SELECT count(*) FROM kegs WHERE asset_status='WASH_FAIL'`)
	// 返修预警：待返修 / 待仓库确认 / 待财务确认账单
	out["rework_open"] = count(`SELECT count(*) FROM rework_orders WHERE status='OPEN'`)
	out["rework_pending_confirm"] = count(`SELECT count(*) FROM rework_orders WHERE status='REWORKED'`)
	out["rework_bill_pending"] = count(`SELECT count(*) FROM billing_entries WHERE status='PENDING'`)
	// 预警：丢失
	out["lost"] = count(`SELECT count(*) FROM kegs WHERE asset_status='LOST'`)
	// 预警：变形
	out["deformed"] = count(`SELECT count(*) FROM keg_cycles WHERE deformed`)
	// 预警：多店口味投诉（同一批次 ≥2 家酒吧反馈口味异常）
	out["taste_alerts"] = count(`SELECT count(*) FROM (
		SELECT c.batch_id FROM tap_feedback f JOIN keg_cycles c ON c.id=f.cycle_id
		WHERE f.taste_off GROUP BY c.batch_id HAVING count(DISTINCT f.bar_id) >= 2) t`)
	// 押金金额汇总
	var held, released, deducted float64
	_ = db.QueryRow(`SELECT
		COALESCE(sum(CASE WHEN deposit_status='HELD' THEN deposit_amount ELSE 0 END),0),
		COALESCE(sum(released_amount),0),
		COALESCE(sum(deducted_amount),0) FROM keg_cycles`).Scan(&held, &released, &deducted)
	out["deposit_held"] = held
	out["deposit_released"] = released
	out["deposit_deducted"] = deducted

	// 待回收预约 / 待配送订单
	out["waiting_pickups"] = count(`SELECT count(*) FROM return_reservations WHERE status='WAITING'`)
	out["pending_orders"] = count(`SELECT count(*) FROM orders WHERE status IN ('PLANNED','DELIVERING')`)

	writeOK(w, out)
}

func countMap(q string) M {
	m := M{}
	rows, err := db.Query(q)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err == nil {
			m[k] = v
		}
	}
	return m
}
