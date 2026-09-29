package main

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
)

// 酒吧在店桶列表（店长只能看本店）
func handleBarCycles(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	barID := u.OrgID
	if r.URL.Query().Get("bar_id") != "" && u.Role == "admin" {
		barID = atoi64(r.URL.Query().Get("bar_id"))
	}
	rows, err := db.Query(`
		SELECT c.id, k.keg_code, k.capacity_liters, be.style, be.name, ba.batch_no, ba.status,
		       c.stage, c.deposit_amount, c.deposit_due_date, c.tapped_at, c.emptied_at,
		       c.deposit_status, c.late_return, c.missing_scan, c.lost,
		       (CURRENT_DATE > c.deposit_due_date AND c.stage NOT IN ('WASHED','FAILED','REPAIRING')) AS overdue
		FROM keg_cycles c
		JOIN kegs k ON k.id=c.keg_id
		JOIN batches ba ON ba.id=c.batch_id
		JOIN beers be ON be.id=ba.beer_id
		WHERE c.bar_id=$1
		ORDER BY c.id DESC`, barID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := scanRows(rows, func(rs rowScanner) (M, error) {
		var id int64
		var code, style, beer, batchNo, batchStatus, stage, depositStatus string
		var capacity, deposit float64
		var due sql.NullTime
		var tapped, emptied sql.NullTime
		var late, missing, lost, overdue bool
		if err := rs.Scan(&id, &code, &capacity, &style, &beer, &batchNo, &batchStatus,
			&stage, &deposit, &due, &tapped, &emptied, &depositStatus,
			&late, &missing, &lost, &overdue); err != nil {
			return nil, err
		}
		return M{"cycle_id": id, "keg_code": code, "capacity_liters": capacity,
			"style": style, "beer": beer, "batch_no": batchNo, "batch_status": batchStatus,
			"stage": stage, "deposit_amount": deposit, "deposit_due_date": dateStr(due),
			"tapped_at": tsStr(tapped), "emptied_at": tsStr(emptied),
			"deposit_status": depositStatus, "late_return": late, "missing_scan": missing,
			"lost": lost, "overdue": overdue}, nil
	})
	writeOK(w, M{"cycles": list, "bar_id": barID})
}

// POST /api/cycles/feedback/{id} 开桶反馈：出酒速度/气压/口味/客诉
func handleTapFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	id := tailID(r.URL.Path, "/api/cycles/feedback/")
	var req struct {
		PourSpeed  string `json:"pour_speed"`
		Pressure   string `json:"pressure"`
		TasteOff   bool   `json:"taste_off"`
		TasteNote  string `json:"taste_note"`
		Complaint  string `json:"complaint"`
		ReportedBy string `json:"reported_by"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	u := currentUser(r)

	var kegID, barID int64
	var stage, batchStatus string
	err := runTx(func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT keg_id, bar_id, stage FROM keg_cycles WHERE id=$1 FOR UPDATE`, id).
			Scan(&kegID, &barID, &stage)
		if err == sql.ErrNoRows {
			return errBad("流转记录不存在")
		}
		if err != nil {
			return err
		}
		if u.Role != "admin" && barID != u.OrgID {
			return errBad("只能反馈本店酒桶")
		}
		if stage == "WASHED" || stage == "FAILED" || stage == "RETURNED" {
			return errBad("该桶已回收，不能再反馈开桶问题")
		}
		if err := tx.QueryRow(`SELECT b.status FROM keg_cycles c JOIN batches b ON b.id=c.batch_id WHERE c.id=$1`, id).
			Scan(&batchStatus); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO tap_feedback
			(cycle_id,bar_id,reported_by,pour_speed,pressure,taste_off,taste_note,complaint)
			VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),$6,NULLIF($7,''),NULLIF($8,''))`,
			id, barID, req.ReportedBy, req.PourSpeed, req.Pressure,
			req.TasteOff, req.TasteNote, req.Complaint); err != nil {
			return err
		}
		// 首次反馈视同开桶
		if stage == "DELIVERED" {
			if _, err := tx.Exec(`UPDATE keg_cycles SET stage='TAPPED', tapped_at=now() WHERE id=$1`, id); err != nil {
				return err
			}
		}
		return logKegEvent(tx, kegID, id, "TAP_FEEDBACK", "酒吧",
			M{"pour_speed": req.PourSpeed, "pressure": req.Pressure,
				"taste_off": req.TasteOff, "taste_note": req.TasteNote, "complaint": req.Complaint})
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	warning := ""
	if batchStatus != "NORMAL" {
		warning = "注意：该批次已暂停销售/召回，请勿继续开桶售卖"
	}
	writeOK(w, M{"ok": true, "first_tap": stage == "DELIVERED", "warning": warning})
}

// POST /api/cycles/empty/{id} 酒吧标记酒桶打空
func handleMarkEmpty(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	id := tailID(r.URL.Path, "/api/cycles/empty/")
	u := currentUser(r)
	var kegID, barID int64
	var stage string
	err := runTx(func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT keg_id, bar_id, stage FROM keg_cycles WHERE id=$1 FOR UPDATE`, id).
			Scan(&kegID, &barID, &stage)
		if err == sql.ErrNoRows {
			return errBad("流转记录不存在")
		}
		if err != nil {
			return err
		}
		if u.Role != "admin" && barID != u.OrgID {
			return errBad("只能操作本店酒桶")
		}
		if stage != "TAPPED" {
			return errBad("只有已开桶的酒桶才能标记打空，当前阶段: " + stage)
		}
		if _, err := tx.Exec(`UPDATE keg_cycles SET stage='EMPTY', emptied_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE kegs SET asset_status='EMPTY_RESERVED' WHERE id=$1`, kegID); err != nil {
			return err
		}
		return logKegEvent(tx, kegID, id, "EMPTIED", "酒吧", nil)
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, M{"ok": true})
}

// POST /api/cycles/reserve  预约空桶回收（支持一次多只）
func handleReserveReturn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	var req struct {
		CycleIDs   []int64 `json:"cycle_ids"`
		PickupDate string  `json:"pickup_date"`
		Note       string  `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if len(req.CycleIDs) == 0 || req.PickupDate == "" {
		fail(w, 400, "请选择空桶并选择预约回收日期")
		return
	}
	if _, err := strconv.ParseInt(strings.ReplaceAll(req.PickupDate, "-", ""), 10, 64); err != nil {
		fail(w, 400, "回收日期格式应为 YYYY-MM-DD")
		return
	}
	u := currentUser(r)

	resID := int64(0)
	err := runTx(func(tx *sql.Tx) error {
		var barID int64
		for _, cid := range req.CycleIDs {
			var cBar int64
			var stage string
			if err := tx.QueryRow(`SELECT bar_id, stage FROM keg_cycles WHERE id=$1 FOR UPDATE`, cid).
				Scan(&cBar, &stage); err != nil {
				return errBad("流转记录不存在")
			}
			if u.Role != "admin" && cBar != u.OrgID {
				return errBad("只能预约本店酒桶")
			}
			if stage != "EMPTY" {
				return errBad("仅已打空的酒桶可预约回收")
			}
			barID = cBar
		}
		if err := tx.QueryRow(`INSERT INTO return_reservations
			(cycle_id,bar_id,pickup_date,empty_count,note,status)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),'WAITING') RETURNING id`,
			req.CycleIDs[0], barID, req.PickupDate, len(req.CycleIDs), req.Note).Scan(&resID); err != nil {
			return err
		}
		for _, cid := range req.CycleIDs {
			if _, err := tx.Exec(`INSERT INTO reservation_cycles (reservation_id,cycle_id) VALUES ($1,$2)`,
				resID, cid); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE keg_cycles SET stage='RESERVED', return_reserved_at=now() WHERE id=$1`, cid); err != nil {
				return err
			}
			var kegID int64
			_ = tx.QueryRow(`SELECT keg_id FROM keg_cycles WHERE id=$1`, cid).Scan(&kegID)
			if err := logKegEvent(tx, kegID, cid, "RETURN_RESERVED", "酒吧",
				M{"reservation_id": resID, "pickup_date": req.PickupDate}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, M{"reservation_id": resID})
}

// POST /api/cycles/lost/{id}  酒桶丢失登记
func handleMarkLost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	id := tailID(r.URL.Path, "/api/cycles/lost/")
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &req)
	var kegID int64
	err := runTx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT keg_id FROM keg_cycles WHERE id=$1`, id).Scan(&kegID); err != nil {
			return errBad("流转记录不存在")
		}
		if _, err := tx.Exec(`UPDATE keg_cycles SET lost=TRUE, late_return=TRUE WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE kegs SET asset_status='LOST' WHERE id=$1`, kegID); err != nil {
			return err
		}
		return logKegEvent(tx, kegID, id, "MARKED_LOST", currentUser(r).Name, M{"reason": req.Reason})
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, M{"ok": true})
}

func respondErr(w http.ResponseWriter, err error) {
	if e, ok := err.(*badRequest); ok {
		fail(w, 400, e.Error())
		return
	}
	fail(w, 500, err.Error())
}
