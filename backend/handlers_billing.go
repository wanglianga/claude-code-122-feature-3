package main

import (
	"database/sql"
	"net/http"
)

// GET /api/finance/bills?status=&party=  返修费用账单（酒吧/清洗厂）
func handleBillingEntries(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	q := `SELECT bl.id, bl.party_type, bl.party_id,
	       COALESCE(CASE WHEN bl.party_type='BAR' THEN b.name ELSE wp.name END,'-'),
	       COALESCE(ci.name,''), COALESCE(r.rework_no,''), bl.keg_code,
	       bl.item, bl.amount, bl.status,
	       COALESCE(text(bl.confirmed_at),''), COALESCE(text(bl.created_at),''),
	       COALESCE(r.status,'')
	FROM billing_entries bl
	LEFT JOIN bars b ON bl.party_type='BAR' AND b.id=bl.party_id
	LEFT JOIN cities ci ON bl.party_type='BAR' AND ci.id=b.city_id
	LEFT JOIN washplants wp ON bl.party_type='WASHPLANT' AND wp.id=bl.party_id
	LEFT JOIN rework_orders r ON r.id=bl.rework_id
	WHERE 1=1`
	args := []interface{}{}
	// 清洗厂只能看到本厂账单；酒吧只能看到本店账单
	if u.Role == "washplant" {
		q += " AND bl.party_type='WASHPLANT' AND bl.party_id=$1"
		args = append(args, u.OrgID)
	} else if u.Role == "bar_manager" {
		q += " AND bl.party_type='BAR' AND bl.party_id=$1"
		args = append(args, u.OrgID)
	}
	if s := r.URL.Query().Get("status"); s != "" {
		q += " AND bl.status=" + pidx(args)
		args = append(args, s)
	}
	if p := r.URL.Query().Get("party"); p != "" && u.Role != "washplant" && u.Role != "bar_manager" {
		q += " AND bl.party_type=" + pidx(args)
		args = append(args, p)
	}
	q += " ORDER BY bl.id DESC"

	rows, err := db.Query(q, args...)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := scanRows(rows, func(rs rowScanner) (M, error) {
		var id, partyID int64
		var partyType, partyName, city, reworkNo, kegCode, item string
		var amount float64
		var status, confirmedAt, createdAt, reworkStatus string
		if err := rs.Scan(&id, &partyType, &partyID, &partyName, &city, &reworkNo,
			&kegCode, &item, &amount, &status, &confirmedAt, &createdAt, &reworkStatus); err != nil {
			return nil, err
		}
		return M{"id": id, "party_type": partyType, "party_id": partyID,
			"party_name": partyName, "city": city, "rework_no": reworkNo,
			"keg_code": kegCode, "item": item, "amount": amount, "status": status,
			"confirmed_at": confirmedAt, "created_at": createdAt,
			"rework_status": reworkStatus}, nil
	})

	// 汇总：待确认 / 已入账（按承担方）
	summary := M{}
	rows2, _ := db.Query(`SELECT party_type, status, count(*), COALESCE(sum(amount),0)
		FROM billing_entries GROUP BY 1,2 ORDER BY 1,2`)
	groups := []M{}
	for rows2.Next() {
		var pt, st string
		var n int
		var amt float64
		if rows2.Scan(&pt, &st, &n, &amt) == nil {
			groups = append(groups, M{"party_type": pt, "status": st, "count": n, "amount": amt})
		}
	}
	rows2.Close()
	var pendingAmt, confirmedAmt float64
	var pendingN int
	_ = db.QueryRow(`SELECT count(*), COALESCE(sum(amount),0) FROM billing_entries WHERE status='PENDING'`).
		Scan(&pendingN, &pendingAmt)
	_ = db.QueryRow(`SELECT COALESCE(sum(amount),0) FROM billing_entries WHERE status='CONFIRMED'`).
		Scan(&confirmedAmt)
	summary = M{"pending_count": pendingN, "pending_amount": pendingAmt,
		"confirmed_amount": confirmedAmt, "groups": groups}

	writeOK(w, M{"bills": list, "summary": summary})
}

// POST /api/finance/bills/confirm/{id}  财务确认返修费用，账单入账并回写返修单
func handleConfirmBilling(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	id := tailID(r.URL.Path, "/api/finance/bills/confirm/")
	u := currentUser(r)

	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		var reworkID, cycleID sql.NullInt64
		var kegCode, partyType string
		var amount float64
		var status string
		if err := tx.QueryRow(`SELECT COALESCE(rework_id,0), COALESCE(cycle_id,0),
			COALESCE(keg_code,''), party_type, amount, status
			FROM billing_entries WHERE id=$1 FOR UPDATE`,
			id).Scan(&reworkID, &cycleID, &kegCode, &partyType, &amount, &status); err != nil {
			return errBad("账单不存在")
		}
		if status != "PENDING" {
			return errBad("该账单已确认，不能重复入账")
		}
		if _, err := tx.Exec(`UPDATE billing_entries SET status='CONFIRMED',
			confirmed_by=$1, confirmed_at=now() WHERE id=$2`, u.ID, id); err != nil {
			return err
		}

		var kegID int64
		if cycleID.Int64 != 0 {
			_ = tx.QueryRow(`SELECT keg_id FROM keg_cycles WHERE id=$1`, cycleID.Int64).Scan(&kegID)
		}
		if err := logKegEvent(tx, kegID, cycleID.Int64, "REWORK_BILL_CONFIRMED", "财务", M{
			"bill_id": id, "rework_id": reworkID.Int64, "party_type": partyType,
			"amount": amount, "keg_code": kegCode,
		}); err != nil {
			return err
		}
		resp = M{"id": id, "status": "CONFIRMED", "amount": amount, "party_type": partyType}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}
