package main

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

// GET /api/wash/pending  已回收待清洗（按洗桶厂所在城市过滤）
func handleWashPending(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	cityID := int64(0)
	if u.Role == "washplant" {
		_ = db.QueryRow(`SELECT city_id FROM washplants WHERE id=$1`, u.OrgID).Scan(&cityID)
	}
	q := `SELECT c.id, k.keg_code, k.capacity_liters, be.style, be.name, ba.batch_no,
	       b.name, dr.name, c.picked_at,
	       COALESCE(ps.appearance,''), COALESCE(ps.residue_level,''), COALESCE(ps.cap_status,'')
		FROM keg_cycles c
		JOIN kegs k ON k.id=c.keg_id
		JOIN batches ba ON ba.id=c.batch_id
		JOIN beers be ON be.id=ba.beer_id
		LEFT JOIN bars b ON b.id=c.bar_id
		LEFT JOIN drivers dr ON dr.id=c.driver_id
		LEFT JOIN LATERAL (
			SELECT appearance, residue_level, cap_status FROM pickup_scans ps
			WHERE ps.cycle_id=c.id ORDER BY id DESC LIMIT 1
		) ps ON TRUE
		WHERE c.stage='RETURNED' AND ($1=0 OR c.city_id=$1)
		ORDER BY c.picked_at`
	rows, err := db.Query(q, cityID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := scanRows(rows, func(rs rowScanner) (M, error) {
		var id int64
		var code, style, beer, batchNo, bar, driver, appearance, residue, cap string
		var capacity float64
		var picked sql.NullTime
		if err := rs.Scan(&id, &code, &capacity, &style, &beer, &batchNo, &bar, &driver,
			&picked, &appearance, &residue, &cap); err != nil {
			return nil, err
		}
		return M{"cycle_id": id, "keg_code": code, "capacity_liters": capacity,
			"style": style, "beer": beer, "batch_no": batchNo, "bar": bar, "driver": driver,
			"picked_at": tsStr(picked), "appearance": appearance,
			"residue_level": residue, "cap_status": cap}, nil
	})
	writeOK(w, M{"pending": list})
}

// POST /api/wash/checks  清洗厂检测：合格 → 回库可再灌装；不合格 → WASH_FAIL、取消再灌装资格
func handleWashCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	var req struct {
		CycleID       int64  `json:"cycle_id"`
		Inspector     string `json:"inspector"`
		Passed        bool   `json:"passed"`
		PressureOK    *bool  `json:"pressure_ok"`
		SealOK        *bool  `json:"seal_ok"`
		CleanlinessOK *bool  `json:"cleanliness_ok"`
		DefectNote    string `json:"defect_note"`
		WashBatchNo   string `json:"wash_batch_no"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.CycleID == 0 {
		fail(w, 400, "缺少桶流转记录")
		return
	}
	// 任一子项不合格则总体不合格
	pressure, seal, clean := true, true, true
	if req.PressureOK != nil {
		pressure = *req.PressureOK
	}
	if req.SealOK != nil {
		seal = *req.SealOK
	}
	if req.CleanlinessOK != nil {
		clean = *req.CleanlinessOK
	}
	passed := req.Passed && pressure && seal && clean

	u := currentUser(r)
	washID := u.OrgID
	if u.Role == "admin" || washID == 0 {
		_ = db.QueryRow(`SELECT id FROM washplants ORDER BY id LIMIT 1`).Scan(&washID)
	}
	// 清洗批次号：缺省按 WB-日期-WP清洗厂 生成（返修追溯依据）
	washBatch := req.WashBatchNo
	if washBatch == "" {
		washBatch = "WB-" + time.Now().Format("20060102") + "-WP" + strconv.FormatInt(washID, 10)
	}

	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		var kegID int64
		var stage string
		if err := tx.QueryRow(`SELECT keg_id, stage FROM keg_cycles WHERE id=$1 FOR UPDATE`, req.CycleID).
			Scan(&kegID, &stage); err != nil {
			return errBad("流转记录不存在")
		}
		if stage != "RETURNED" {
			return errBad("仅已回收待清洗的桶可登记检测，当前: " + stage)
		}

		var washIDCol int64
		if err := tx.QueryRow(`INSERT INTO wash_checks
			(cycle_id,washplant_id,wash_batch_no,inspector,passed,pressure_ok,seal_ok,cleanliness_ok,defect_note)
			VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6,$7,$8,NULLIF($9,'')) RETURNING id`,
			req.CycleID, washID, washBatch, req.Inspector, passed, pressure, seal, clean, req.DefectNote).
			Scan(&washIDCol); err != nil {
			return err
		}

		if passed {
			if _, err := tx.Exec(`UPDATE keg_cycles SET stage='WASHED', wash_id=$1 WHERE id=$2`,
				washIDCol, req.CycleID); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE kegs SET asset_status='IN_STOCK', current_bar_id=NULL,
				refillable=TRUE WHERE id=$1`, kegID); err != nil {
				return err
			}
			if err := logKegEvent(tx, kegID, req.CycleID, "WASHED", "清洗厂",
				M{"wash_id": washIDCol}); err != nil {
				return err
			}
			resp = M{"passed": true, "asset_status": "IN_STOCK", "refillable": true}
		} else {
			// 不合格：桶冻结、取消再灌装资格，等待清洗厂发起返修（异味/阀门损坏…）
			if _, err := tx.Exec(`UPDATE keg_cycles SET stage='FAILED', wash_id=$1 WHERE id=$2`,
				washIDCol, req.CycleID); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE kegs SET asset_status='WASH_FAIL', current_bar_id=NULL,
				refillable=FALSE WHERE id=$1`, kegID); err != nil {
				return err
			}
			if err := logKegEvent(tx, kegID, req.CycleID, "WASH_FAILED", "清洗厂",
				M{"wash_id": washIDCol, "note": req.DefectNote,
					"pressure_ok": pressure, "seal_ok": seal, "cleanliness_ok": clean}); err != nil {
				return err
			}
			resp = M{"passed": false, "asset_status": "WASH_FAIL", "refillable": false}
		}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}
