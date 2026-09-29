package main

import (
	"database/sql"
	"net/http"
)

// GET /api/driver/tasks  回收任务（按司机所在城市过滤）
func handleDriverTasks(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	rows, err := db.Query(`
		SELECT rv.id, rv.cycle_id, b.id, b.name, ci.name, rv.pickup_date, rv.empty_count,
		       COALESCE(rv.note,''), rv.status
		FROM return_reservations rv
		JOIN bars b ON b.id=rv.bar_id
		JOIN cities ci ON ci.id=b.city_id
		WHERE ($1=0 OR ci.id=$1)
		ORDER BY rv.status, rv.pickup_date`, cityFilter(u))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := scanRows(rows, func(rs rowScanner) (M, error) {
		var id, firstCycle, barID, emptyCount int64
		var bar, city, pickup, note, status string
		if err := rs.Scan(&id, &firstCycle, &barID, &bar, &city, &pickup, &emptyCount,
			&note, &status); err != nil {
			return nil, err
		}
		m := M{"reservation_id": id, "bar_id": barID, "bar": bar, "city": city,
			"pickup_date": pickup, "empty_count": emptyCount, "note": note, "status": status}

		// 预约应收桶 vs 已扫码桶（差异即漏扫）
		crows, _ := db.Query(`
			SELECT rc.cycle_id, k.keg_code, c.stage,
			       EXISTS(SELECT 1 FROM pickup_scans ps WHERE ps.cycle_id=rc.cycle_id)
			FROM reservation_cycles rc
			JOIN keg_cycles c ON c.id=rc.cycle_id
			JOIN kegs k ON k.id=c.keg_id
			WHERE rc.reservation_id=$1 ORDER BY rc.cycle_id`, id)
		cycles := scanRows(crows, func(x rowScanner) (M, error) {
			var cid int64
			var code, stage string
			var scanned bool
			if err := x.Scan(&cid, &code, &stage, &scanned); err != nil {
				return nil, err
			}
			return M{"cycle_id": cid, "keg_code": code, "stage": stage, "scanned": scanned}, nil
		})
		m["cycles"] = cycles
		scanned := 0
		for _, c := range cycles {
			if c["scanned"].(bool) {
				scanned++
			}
		}
		m["scanned_count"] = scanned
		m["missing_count"] = len(cycles) - scanned
		return m, nil
	})
	writeOK(w, M{"tasks": list})
}

// POST /api/pickups/scan  司机扫码确认空桶
func handlePickupScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	var req struct {
		ReservationID  int64  `json:"reservation_id"`
		KegCode        string `json:"keg_code"`
		Appearance     string `json:"appearance"`
		ResidueLevel   string `json:"residue_level"`
		CapStatus      string `json:"cap_status"`
		EmptyConfirmed bool   `json:"empty_confirmed"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.KegCode == "" {
		fail(w, 400, "缺少酒桶编号（扫码内容）")
		return
	}
	if req.Appearance == "" {
		req.Appearance = "完好"
	}
	if req.ResidueLevel == "" {
		req.ResidueLevel = "无"
	}
	if req.CapStatus == "" {
		req.CapStatus = "已封盖"
	}
	u := currentUser(r)

	result := M{}
	err := runTx(func(tx *sql.Tx) error {
		var kegID int64
		err := tx.QueryRow(`SELECT id FROM kegs WHERE keg_code=$1`, req.KegCode).Scan(&kegID)
		if err == sql.ErrNoRows {
			return errBad("扫码酒桶不存在: " + req.KegCode)
		}
		if err != nil {
			return err
		}

		var cycleID, barID int64
		var stage string
		err = tx.QueryRow(`SELECT id, bar_id, stage FROM keg_cycles
			WHERE keg_id=$1 ORDER BY id DESC LIMIT 1`, kegID).Scan(&cycleID, &barID, &stage)
		if err != nil {
			return errBad("该桶没有在店流转记录")
		}
		if stage == "WASHED" || stage == "RETURNED" {
			return errBad("该桶已回收，请勿重复扫码")
		}
		if stage != "RESERVED" && stage != "EMPTY" {
			return errBad("该桶尚未打空/预约，不能扫码回收（当前: " + stage + "）")
		}

		// 预约归属校验
		if req.ReservationID != 0 {
			var n int
			if err := tx.QueryRow(`SELECT count(*) FROM reservation_cycles
				WHERE reservation_id=$1 AND cycle_id=$2`, req.ReservationID, cycleID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return errBad("该桶不在本次回收预约清单中")
			}
		}
		driverID := u.OrgID
		if u.Role == "admin" {
			driverID = 1
		}

		if _, err := tx.Exec(`INSERT INTO pickup_scans
			(cycle_id,reservation_id,driver_id,scanned_code,empty_confirmed,appearance,residue_level,cap_status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			cycleID, nullableID(req.ReservationID), driverID, req.KegCode,
			req.EmptyConfirmed || true, req.Appearance, req.ResidueLevel, req.CapStatus); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE keg_cycles
			SET stage='RETURNED', picked_at=now(), deformed = deformed OR $2
			WHERE id=$1`, cycleID, req.Appearance == "变形"); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE kegs SET asset_status='PICKED' WHERE id=$1`, kegID); err != nil {
			return err
		}
		if req.ReservationID != 0 {
			if _, err := tx.Exec(`UPDATE return_reservations SET status='PICKED' WHERE id=$1 AND status='WAITING'`,
				req.ReservationID); err != nil {
				return err
			}
		}
		if err := logKegEvent(tx, kegID, cycleID, "PICKED", "司机",
			M{"reservation_id": req.ReservationID, "appearance": req.Appearance,
				"residue": req.ResidueLevel, "cap": req.CapStatus}); err != nil {
			return err
		}
		result = M{"cycle_id": cycleID, "deformed": req.Appearance == "变形"}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, result)
}

// POST /api/driver/audit  回收审计：漏扫标记 + 爽约/延迟归还
// body 可选 {reservation_id}；不传则审计全部
func handleDriverAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	var req struct {
		ReservationID int64 `json:"reservation_id"`
	}
	_ = decodeJSON(r, &req)

	missing, missed, late := []M{}, 0, 0
	err := runTx(func(tx *sql.Tx) error {
		// 1) 预约内未扫码的桶 → 司机漏扫
		q := `SELECT rc.reservation_id, rc.cycle_id, k.keg_code, b.name
			FROM reservation_cycles rc
			JOIN keg_cycles c ON c.id=rc.cycle_id
			JOIN kegs k ON k.id=c.keg_id
			JOIN return_reservations rv ON rv.id=rc.reservation_id
			JOIN bars b ON b.id=rv.bar_id
			WHERE NOT EXISTS (SELECT 1 FROM pickup_scans ps WHERE ps.cycle_id=rc.cycle_id)
			  AND rv.status IN ('PICKED','MISSED')`
		args := []interface{}{}
		if req.ReservationID != 0 {
			q += ` AND rc.reservation_id=$1`
			args = append(args, req.ReservationID)
		}
		rows, err := tx.Query(q, args...)
		if err != nil {
			return err
		}
		type mm struct {
			res, cyc  int64
			code, bar string
		}
		var found []mm
		for rows.Next() {
			var x mm
			if err := rows.Scan(&x.res, &x.cyc, &x.code, &x.bar); err != nil {
				return err
			}
			found = append(found, x)
		}
		rows.Close()
		for _, x := range found {
			if _, err := tx.Exec(`UPDATE keg_cycles SET missing_scan=TRUE WHERE id=$1`, x.cyc); err != nil {
				return err
			}
			var kegID int64
			_ = tx.QueryRow(`SELECT keg_id FROM keg_cycles WHERE id=$1`, x.cyc).Scan(&kegID)
			if err := logKegEvent(tx, kegID, x.cyc, "MISSING_SCAN", "系统审计",
				M{"reservation_id": x.res, "bar": x.bar}); err != nil {
				return err
			}
			missing = append(missing, M{"reservation_id": x.res, "cycle_id": x.cyc,
				"keg_code": x.code, "bar": x.bar})
		}

		// 2) 过了预约日仍 WAITING → 爽约，在店桶标延迟归还
		r2, err := tx.Query(`SELECT rv.id FROM return_reservations rv
			WHERE rv.status='WAITING' AND rv.pickup_date < CURRENT_DATE
			  AND ($1=0 OR rv.id=$1)`, req.ReservationID)
		if err != nil {
			return err
		}
		var ids []int64
		for r2.Next() {
			var id int64
			if err := r2.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		r2.Close()
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE return_reservations SET status='MISSED' WHERE id=$1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE keg_cycles SET late_return=TRUE
				WHERE id IN (SELECT cycle_id FROM reservation_cycles WHERE reservation_id=$1)`, id); err != nil {
				return err
			}
			missed++
		}

		// 3) 账期已过仍未闭环 → 延迟归还（返修流程中属于已回收异常处理，不再计延迟归还）
		res, err := tx.Exec(`UPDATE keg_cycles SET late_return=TRUE
			WHERE deposit_due_date < CURRENT_DATE AND stage NOT IN ('WASHED','FAILED','REPAIRING') AND NOT late_return`)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		late = int(n)
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, M{"missing_scans": missing, "missed_reservations": missed, "new_late_returns": late})
}

// cityFilter 司机限定城市，其他角色 0=不限
func cityFilter(u sessionUser) int64 {
	switch u.Role {
	case "driver":
		var cityID int64
		_ = db.QueryRow(`SELECT city_id FROM drivers WHERE id=$1`, u.OrgID).Scan(&cityID)
		return cityID
	}
	return 0
}
