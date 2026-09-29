package main

import (
	"database/sql"
	"net/http"
)

// GET /api/finance/deposits?status=&overdue=
func handleDeposits(w http.ResponseWriter, r *http.Request) {
	q := `SELECT c.id, k.keg_code, b.name, ci.name, be.style, ba.batch_no,
	       c.deposit_amount, c.deposit_due_date, c.deposit_status,
	       c.released_amount, c.deducted_amount, COALESCE(c.deduct_reason,''),
	       c.stage, c.late_return, c.missing_scan, c.lost, c.deformed,
	       (CURRENT_DATE - c.deposit_due_date),
	       COALESCE(text(c.settled_at),''),
	       COALESCE(ps.appearance,'完好'), COALESCE(ps.residue_level,'无'), COALESCE(ps.cap_status,'已封盖')
		FROM keg_cycles c
		JOIN kegs k ON k.id=c.keg_id
		JOIN batches ba ON ba.id=c.batch_id
		JOIN beers be ON be.id=ba.beer_id
		LEFT JOIN bars b ON b.id=c.bar_id
		JOIN cities ci ON ci.id=c.city_id
		LEFT JOIN LATERAL (
			SELECT appearance, residue_level, cap_status FROM pickup_scans ps
			WHERE ps.cycle_id=c.id ORDER BY id DESC LIMIT 1
		) ps ON TRUE
		WHERE 1=1`
	args := []interface{}{}
	if s := r.URL.Query().Get("status"); s != "" {
		q += " AND c.deposit_status=" + pidx(args)
		args = append(args, s)
	}
	if r.URL.Query().Get("overdue") == "1" {
		q += " AND c.deposit_due_date < CURRENT_DATE AND c.deposit_status='HELD'"
	}
	q += " ORDER BY c.deposit_due_date, c.id"

	rows, err := db.Query(q, args...)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := scanRows(rows, func(rs rowScanner) (M, error) {
		var id int64
		var code, bar, city, style, batchNo, status, reason, stage string
		var settled, appearance, residue, cap string
		var deposit, released, deducted float64
		var due sql.NullTime
		var late, missing, lost, deformed bool
		var overdueDays int64
		if err := rs.Scan(&id, &code, &bar, &city, &style, &batchNo,
			&deposit, &due, &status, &released, &deducted, &reason,
			&stage, &late, &missing, &lost, &deformed, &overdueDays,
			&settled, &appearance, &residue, &cap); err != nil {
			return nil, err
		}
		return M{"cycle_id": id, "keg_code": code, "bar": bar, "city": city,
			"style": style, "batch_no": batchNo, "deposit_amount": deposit,
			"deposit_due_date": dateStr(due), "deposit_status": status,
			"released_amount": released, "deducted_amount": deducted,
			"deduct_reason": reason, "stage": stage, "late_return": late,
			"missing_scan": missing, "lost": lost, "deformed": deformed,
			"overdue_days": overdueDays, "settled_at": settled,
			"appearance": appearance, "residue_level": residue, "cap_status": cap}, nil
	})

	// 汇总
	summary := M{}
	var heldAmt, releasedAmt, deductedAmt float64
	var heldN, overdueN int
	_ = db.QueryRow(`SELECT
		COALESCE(sum(deposit_amount) FILTER (WHERE deposit_status='HELD'),0),
		count(*) FILTER (WHERE deposit_status='HELD'),
		count(*) FILTER (WHERE deposit_status='HELD' AND deposit_due_date<CURRENT_DATE),
		COALESCE(sum(released_amount),0),
		COALESCE(sum(deducted_amount),0)
		FROM keg_cycles`).Scan(&heldAmt, &heldN, &overdueN, &releasedAmt, &deductedAmt)
	summary = M{"held_amount": heldAmt, "held_count": heldN, "overdue_count": overdueN,
		"released_total": releasedAmt, "deducted_total": deductedAmt}

	writeOK(w, M{"deposits": list, "summary": summary})
}

// POST /api/finance/settle/{id}  按桶释放/扣减押金
// body: {action: release|deduct|partial, deduct_amount, reason}
func handleSettleDeposit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	id := tailID(r.URL.Path, "/api/finance/settle/")
	var req struct {
		Action       string  `json:"action"`
		DeductAmount float64 `json:"deduct_amount"`
		Reason       string  `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}

	u := currentUser(r)
	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		var kegID int64
		var deposit float64
		var status string
		if err := tx.QueryRow(`SELECT keg_id, deposit_amount, deposit_status
			FROM keg_cycles WHERE id=$1 FOR UPDATE`, id).Scan(&kegID, &deposit, &status); err != nil {
			return errBad("流转记录不存在")
		}
		if status != "HELD" {
			return errBad("该桶押金已结算（" + status + "），不能重复操作")
		}

		var released, deducted float64
		var newStatus string
		switch req.Action {
		case "release":
			released, deducted, newStatus = deposit, 0, "RELEASED"
		case "deduct":
			released, deducted, newStatus = 0, deposit, "DEDUCTED"
		case "partial":
			if req.DeductAmount <= 0 || req.DeductAmount >= deposit {
				return errBad("部分扣款金额需大于 0 且小于押金总额")
			}
			released, deducted, newStatus = deposit-req.DeductAmount, req.DeductAmount, "PARTIAL"
		default:
			return errBad("未知结算动作: " + req.Action)
		}

		reason := req.Reason
		if reason == "" {
			switch req.Action {
			case "release":
				reason = "空桶回收且清洗合格，押金全额释放"
			case "deduct":
				reason = "酒桶丢失/严重损坏，押金全额扣除"
			case "partial":
				reason = "桶况异常，按损坏程度部分扣款"
			}
		}

		if _, err := tx.Exec(`UPDATE keg_cycles SET deposit_status=$1, released_amount=$2,
			deducted_amount=$3, deduct_reason=$4, settled_by=$5, settled_at=now()
			WHERE id=$6`, newStatus, released, deducted, reason, u.ID, id); err != nil {
			return err
		}
		if err := logKegEvent(tx, kegID, id, "DEPOSIT_SETTLED", "财务",
			M{"status": newStatus, "released": released, "deducted": deducted, "reason": reason}); err != nil {
			return err
		}
		resp = M{"deposit_status": newStatus, "released_amount": released,
			"deducted_amount": deducted}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}

func pidx(args []interface{}) string {
	return "$" + itoa(int64(len(args)+1))
}
