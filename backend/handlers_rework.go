package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strings"
)

// ---------- 照片（base64 上传，bytea 留存） ----------

type reworkPhotoInput struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Data        string `json:"data"` // base64
}

const maxPhotoBytes = 5 << 20 // 单张照片 5MB

func decodePhoto(p reworkPhotoInput) (name, ctype string, raw []byte, err error) {
	name = strings.TrimSpace(p.FileName)
	if name == "" {
		name = "photo.png"
	}
	ctype = strings.ToLower(strings.TrimSpace(p.ContentType))
	if ctype == "" {
		ctype = "image/png"
	}
	if ctype == "image/jpg" {
		ctype = "image/jpeg"
	}
	if ctype != "image/png" && ctype != "image/jpeg" && ctype != "image/webp" {
		return "", "", nil, errBad("仅支持 PNG/JPEG/WebP 格式照片: " + name)
	}
	raw, err = base64.StdEncoding.DecodeString(strings.TrimSpace(p.Data))
	if err != nil {
		return "", "", nil, errBad("照片 base64 解析失败: " + name)
	}
	if len(raw) == 0 {
		return "", "", nil, errBad("照片内容为空: " + name)
	}
	if len(raw) > maxPhotoBytes {
		return "", "", nil, errBad("照片超过 5MB 限制: " + name)
	}
	if !looksLikeImage(raw, ctype) {
		return "", "", nil, errBad("照片内容与格式不符: " + name)
	}
	return name, ctype, raw, nil
}

func looksLikeImage(raw []byte, ctype string) bool {
	switch {
	case bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")):
		return ctype == "image/png"
	case bytes.HasPrefix(raw, []byte("\xFF\xD8\xFF")):
		return ctype == "image/jpeg"
	case bytes.HasPrefix(raw, []byte("RIFF")) && len(raw) > 11 && bytes.Equal(raw[8:12], []byte("WEBP")):
		return ctype == "image/webp"
	}
	return false
}

// washBatchNo 由清洗厂 + 首次检测日期推导"清洗批次"（同厂同日检测归为一个清洗批次）
func washBatchNo(washplantID int64, checkedAt sql.NullTime) string {
	if !checkedAt.Valid {
		return ""
	}
	return "WASH-" + checkedAt.Time.Format("20060102") + "-" + pad2(washplantID)
}

func pad2(id int64) string {
	s := itoa(id)
	for len(s) < 2 {
		s = "0" + s
	}
	return s
}

// ---------- GET/POST /api/rework/orders ----------

func handleReworkOrders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		listReworkOrders(w, r)
	case http.MethodPost:
		createReworkOrder(w, r)
	default:
		fail(w, 405, "仅支持 GET/POST")
	}
}

const reworkSelectCols = `SELECT r.id, r.rework_no, r.cycle_id, r.keg_id, k.keg_code,
       COALESCE(b.name,''), COALESCE(dr.name,''), wp.name, r.wash_batch_no,
       be.name, be.style, ba.batch_no,
       r.defect_type, COALESCE(r.defect_note,''), r.rework_fee, r.bill_party,
       r.deposit_deduct, r.status, COALESCE(r.reinspect_result,''),
       COALESCE(r.reinspect_note,''), COALESCE(r.reinspector,''),
       COALESCE(text(r.reinspected_at),''), COALESCE(text(r.confirmed_at),''),
       COALESCE(text(r.created_at),''),
       (SELECT count(*) FROM rework_photos p WHERE p.rework_id=r.id AND p.phase='FOUND'),
       (SELECT count(*) FROM rework_photos p WHERE p.rework_id=r.id AND p.phase='REINSPECT'),
       COALESCE((SELECT bl.status FROM billing_entries bl WHERE bl.rework_id=r.id LIMIT 1),''),
       COALESCE((SELECT CASE WHEN bl.party_type='BAR' THEN bb.name ELSE ww.name END
                  FROM billing_entries bl
                  LEFT JOIN bars bb ON bl.party_type='BAR' AND bb.id=bl.party_id
                  LEFT JOIN washplants ww ON bl.party_type='WASHPLANT' AND ww.id=bl.party_id
                 WHERE bl.rework_id=r.id LIMIT 1),'')
FROM rework_orders r
JOIN kegs k ON k.id=r.keg_id
JOIN keg_cycles c ON c.id=r.cycle_id
JOIN washplants wp ON wp.id=r.washplant_id
JOIN batches ba ON ba.id=c.batch_id
JOIN beers be ON be.id=ba.beer_id
LEFT JOIN bars b ON b.id=r.bar_id
LEFT JOIN drivers dr ON dr.id=r.driver_id`

func listReworkOrders(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	q := reworkSelectCols
	where := []string{}
	args := []interface{}{}
	if u.Role == "washplant" {
		where = append(where, "r.washplant_id=$1")
		args = append(args, u.OrgID)
	} else if u.Role == "bar_manager" {
		where = append(where, "r.bar_id=$1")
		args = append(args, u.OrgID)
	}
	if st := r.URL.Query().Get("status"); st != "" {
		where = append(where, "r.status=$"+itoa(int64(len(args)+1)))
		args = append(args, st)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY r.id DESC"

	rows, err := db.Query(q, args...)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := []M{}
	defer rows.Close()
	for rows.Next() {
		var id, cycleID, kegID, foundPhotos, recheckPhotos int64
		var reworkNo, code, bar, driver, washplant, washBatch string
		var beer, style, batchNo, defect, note, billParty, status string
		var reResult, reNote, reinspector, reAt, confAt, createdAt, billStatus, billPartyName string
		var fee, depositDeduct float64
		if err := rows.Scan(&id, &reworkNo, &cycleID, &kegID, &code, &bar, &driver, &washplant,
			&washBatch, &beer, &style, &batchNo, &defect, &note, &fee, &billParty, &depositDeduct,
			&status, &reResult, &reNote, &reinspector, &reAt, &confAt, &createdAt,
			&foundPhotos, &recheckPhotos, &billStatus, &billPartyName); err != nil {
			fail(w, 500, err.Error())
			return
		}
		list = append(list, M{
			"id": id, "rework_no": reworkNo, "cycle_id": cycleID, "keg_id": kegID,
			"keg_code": code, "bar": bar, "driver": driver, "washplant": washplant,
			"wash_batch_no": washBatch, "beer": beer, "style": style, "batch_no": batchNo,
			"defect_type": defect, "defect_note": note, "rework_fee": fee,
			"bill_party": billParty, "deposit_deduct": depositDeduct, "status": status,
			"reinspect_result": reResult, "reinspect_note": reNote, "reinspector": reinspector,
			"reinspected_at": reAt, "confirmed_at": confAt, "created_at": createdAt,
			"found_photos": foundPhotos, "reinspect_photos": recheckPhotos,
			"bill_status": billStatus, "bill_party_name": billPartyName,
		})
	}

	// 状态计数（同可见范围）
	cq := `SELECT r.status, count(*) FROM rework_orders r`
	cargs := []interface{}{}
	if u.Role == "washplant" {
		cq += " WHERE r.washplant_id=$1"
		cargs = append(cargs, u.OrgID)
	} else if u.Role == "bar_manager" {
		cq += " WHERE r.bar_id=$1"
		cargs = append(cargs, u.OrgID)
	}
	cq += " GROUP BY 1"
	crows, _ := db.Query(cq, cargs...)
	counts := M{}
	for crows.Next() {
		var st string
		var n int
		_ = crows.Scan(&st, &n)
		counts[st] = n
	}
	crows.Close()

	writeOK(w, M{"orders": list, "counts": counts})
}

// POST /api/rework/orders  清洗厂对清洗不合格桶发起返修
func createReworkOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CycleID       int64              `json:"cycle_id"`
		DefectType    string             `json:"defect_type"`
		DefectNote    string             `json:"defect_note"`
		ReworkFee     float64            `json:"rework_fee"`
		BillParty     string             `json:"bill_party"`
		DepositDeduct float64            `json:"deposit_deduct"`
		Photos        []reworkPhotoInput `json:"photos"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.CycleID == 0 {
		fail(w, 400, "缺少桶流转记录")
		return
	}
	switch req.DefectType {
	case "ODOR", "VALVE_DAMAGED", "DEFORMED", "RESIDUE", "OTHER":
	default:
		fail(w, 400, "缺陷类型无效")
		return
	}
	if strings.TrimSpace(req.DefectNote) == "" {
		fail(w, 400, "请填写缺陷描述（异味/阀门损坏情况）")
		return
	}
	if req.BillParty == "" {
		req.BillParty = "BAR"
	}
	if req.BillParty != "BAR" && req.BillParty != "WASHPLANT" {
		fail(w, 400, "费用承担方只能为 BAR 或 WASHPLANT")
		return
	}
	if req.ReworkFee < 0 || req.DepositDeduct < 0 {
		fail(w, 400, "返修费与押金扣减不能为负")
		return
	}
	if len(req.Photos) == 0 {
		fail(w, 400, "请至少上传一张桶体照片（仓库留存）")
		return
	}
	type photo struct {
		name, ctype string
		data        []byte
	}
	var photos []photo
	for _, p := range req.Photos {
		name, ctype, data, err := decodePhoto(p)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		photos = append(photos, photo{name, ctype, data})
	}

	u := currentUser(r)
	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		// 1) 锁周期并校验状态
		var kegID, barID, driverID int64
		var deposit, heldDeducted float64
		var stage, depositStatus string
		if err := tx.QueryRow(`SELECT keg_id, COALESCE(bar_id,0), COALESCE(driver_id,0),
			stage, deposit_amount, deposit_status, COALESCE(deducted_amount,0)
			FROM keg_cycles WHERE id=$1 FOR UPDATE`, req.CycleID).
			Scan(&kegID, &barID, &driverID, &stage, &deposit, &depositStatus, &heldDeducted); err != nil {
			return errBad("流转记录不存在")
		}
		if stage != "FAILED" {
			return errBad("仅清洗不合格(FAILED)的桶可发起返修，当前: " + stage)
		}
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM rework_orders
			WHERE cycle_id=$1 AND status IN ('OPEN','REWORKED')`, req.CycleID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errBad("该桶已有进行中的返修单，请勿重复发起")
		}

		// 2) 追溯：首次不合格检测 → 清洗厂 / 清洗批次
		var washCheckID, washplantID int64
		var checkedAt sql.NullTime
		if err := tx.QueryRow(`SELECT id, washplant_id, checked_at FROM wash_checks
			WHERE cycle_id=$1 AND passed=FALSE ORDER BY id LIMIT 1`, req.CycleID).
			Scan(&washCheckID, &washplantID, &checkedAt); err != nil {
			return errBad("未找到该桶的不合格检测记录，无法发起返修")
		}
		if u.Role == "washplant" && u.OrgID != washplantID {
			return errBad("只能为本清洗厂检测的桶发起返修")
		}
		if u.Role == "admin" && washplantID == 0 {
			return errBad("清洗厂信息缺失")
		}
		wbNo := washBatchNo(washplantID, checkedAt)

		// 3) 押金扣减同步生成（仅押金仍占用中时可扣）
		newDepositStatus := depositStatus
		released, deducted := 0.0, heldDeducted
		if req.DepositDeduct > 0 {
			if depositStatus != "HELD" {
				return errBad("该桶押金已结算(" + depositStatus + ")，不能再生成押金扣减；请仅登记返修费账单")
			}
			if req.DepositDeduct > deposit {
				return errBad("押金扣减不能超过押金总额")
			}
			deducted = req.DepositDeduct
			released = deposit - deducted
			if deducted >= deposit {
				newDepositStatus = "DEDUCTED"
			} else {
				newDepositStatus = "PARTIAL"
			}
			if _, err := tx.Exec(`UPDATE keg_cycles
				SET deposit_status=$1, released_amount=$2, deducted_amount=$3,
				    deduct_reason=NULLIF(ltrim(COALESCE(deduct_reason,'')||$4,'；'),''),
				    settled_by=$5, settled_at=now()
				WHERE id=$6`, newDepositStatus, released, deducted,
				"；清洗不合格返修押金扣减："+req.DefectNote, u.ID, req.CycleID); err != nil {
				return err
			}
		}

		// 4) 生成返修单
		var reworkNo string
		if err := tx.QueryRow(`SELECT 'RW-'||to_char(now(),'MMDD')||'-'||lpad((COALESCE(max(id),0)+1)::text,3,'0')
			FROM rework_orders`).Scan(&reworkNo); err != nil {
			return err
		}
		var reworkID int64
		if err := tx.QueryRow(`INSERT INTO rework_orders
			(rework_no,cycle_id,keg_id,wash_check_id,washplant_id,bar_id,driver_id,wash_batch_no,
			 defect_type,defect_note,rework_fee,bill_party,deposit_deduct,status,created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$13,'OPEN',$14) RETURNING id`,
			reworkNo, req.CycleID, int64(kegID), washCheckID, washplantID,
			nullableID(int64(barID)), nullableID(int64(driverID)), nullStr(wbNo),
			req.DefectType, req.DefectNote, req.ReworkFee, req.BillParty, req.DepositDeduct, u.ID).
			Scan(&reworkID); err != nil {
			return err
		}

		// 5) 桶体保持暂停再灌装（清洗不合格时已冻结，此处幂等确保）
		if _, err := tx.Exec(`UPDATE kegs SET asset_status='WASH_FAIL', refillable=FALSE,
			current_bar_id=NULL WHERE id=$1`, int64(kegID)); err != nil {
			return err
		}

		// 6) 返修费进入责任方账单（PENDING，待财务确认）
		var barName, driverName string
		if barID > 0 {
			_ = tx.QueryRow(`SELECT name FROM bars WHERE id=$1`, int64(barID)).Scan(&barName)
		}
		if driverID > 0 {
			_ = tx.QueryRow(`SELECT name FROM drivers WHERE id=$1`, int64(driverID)).Scan(&driverName)
		}
		if req.ReworkFee > 0 {
			partyID := int64(barID)
			item := "清洗不合格返修费（" + defectName(req.DefectType) + "）"
			if req.BillParty == "WASHPLANT" {
				partyID = washplantID
			} else {
				item += "，押金按规则扣减"
			}
			if _, err := tx.Exec(`INSERT INTO billing_entries
				(party_type,party_id,rework_id,cycle_id,keg_code,item,amount,status)
				SELECT $1,$2,$3,$4,k.keg_code,$5,$6,'PENDING' FROM kegs k WHERE k.id=$7`,
				req.BillParty, partyID, reworkID, req.CycleID, item, req.ReworkFee, int64(kegID)); err != nil {
				return err
			}
		}

		// 7) 照片入库
		for _, p := range photos {
			if _, err := tx.Exec(`INSERT INTO rework_photos
				(rework_id,phase,file_name,content_type,data,uploaded_by)
				VALUES ($1,'FOUND',$2,$3,$4,$5)`,
				reworkID, p.name, p.ctype, p.data, u.ID); err != nil {
				return err
			}
		}

		// 8) 事件留痕
		if err := logKegEvent(tx, int64(kegID), req.CycleID, "REWORK_OPENED", "清洗厂", M{
			"rework_no": reworkNo, "defect": req.DefectType, "defect_note": req.DefectNote,
			"bar": barName, "bar_id": barID, "driver": driverName, "driver_id": driverID,
			"wash_batch": wbNo, "fee": req.ReworkFee, "bill_party": req.BillParty,
			"deposit_deduct": req.DepositDeduct, "deposit_status": newDepositStatus,
		}); err != nil {
			return err
		}
		if req.DepositDeduct > 0 {
			if err := logKegEvent(tx, int64(kegID), req.CycleID, "DEPOSIT_SETTLED", "财务",
				M{"status": newDepositStatus, "released": released, "deducted": deducted,
					"reason": "清洗不合格返修押金扣减"}); err != nil {
				return err
			}
		}
		resp = M{"rework_id": reworkID, "rework_no": reworkNo, "status": "OPEN",
			"deposit_status": newDepositStatus, "bill_party": req.BillParty}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}

func defectName(t string) string {
	switch t {
	case "ODOR":
		return "桶内异味"
	case "VALVE_DAMAGED":
		return "阀门损坏"
	case "DEFORMED":
		return "桶体变形"
	case "RESIDUE":
		return "内壁残液"
	}
	return "其他缺陷"
}

// GET /api/rework/pending  清洗不合格、尚未发起返修的桶（含追溯信息）
func handleReworkPending(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	q := `SELECT c.id, k.id, k.keg_code, k.capacity_liters, be.name, be.style, ba.batch_no,
	       COALESCE(b.name,''), b.id, COALESCE(dr.name,''), dr.id,
	       wp.id, wp.name, w.id, COALESCE(text(w.checked_at),''), COALESCE(w.defect_note,''),
	       c.deposit_amount, c.deposit_status, COALESCE(c.deducted_amount,0),
	       'WASH-'||to_char(w.checked_at,'YYYYMMDD')||'-'||lpad(wp.id::text,2,'0')
	FROM keg_cycles c
	JOIN kegs k ON k.id=c.keg_id
	JOIN batches ba ON ba.id=c.batch_id
	JOIN beers be ON be.id=ba.beer_id
	JOIN wash_checks w ON w.id=c.wash_id
	JOIN washplants wp ON wp.id=w.washplant_id
	LEFT JOIN bars b ON b.id=c.bar_id
	LEFT JOIN drivers dr ON dr.id=c.driver_id
	WHERE c.stage='FAILED'
	  AND NOT EXISTS (SELECT 1 FROM rework_orders r
	                  WHERE r.cycle_id=c.id AND r.status IN ('OPEN','REWORKED'))
	  AND ($1=0 OR wp.id=$1)
	ORDER BY w.checked_at DESC`
	rows, err := db.Query(q, washPlantScope(u))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := scanRows(rows, func(rs rowScanner) (M, error) {
		var cycleID, kegID, barID, driverID, wpID, checkID int64
		var code, beer, style, batchNo, bar, driver, wpName, checkedAt, note string
		var capacity, deposit, deducted float64
		var depositStatus, washBatch sql.NullString
		if err := rs.Scan(&cycleID, &kegID, &code, &capacity, &beer, &style, &batchNo,
			&bar, &barID, &driver, &driverID, &wpID, &wpName, &checkID, &checkedAt, &note,
			&deposit, &depositStatus, &deducted, &washBatch); err != nil {
			return nil, err
		}
		return M{"cycle_id": cycleID, "keg_id": kegID, "keg_code": code,
			"capacity_liters": capacity, "beer": beer, "style": style, "batch_no": batchNo,
			"bar": bar, "bar_id": barID, "driver": driver, "driver_id": driverID,
			"washplant_id": wpID, "washplant": wpName, "wash_check_id": checkID,
			"checked_at": checkedAt, "defect_note": note, "deposit_amount": deposit,
			"deposit_status": depositStatus.String, "deducted_amount": deducted,
			"wash_batch_no": washBatch.String}, nil
	})
	writeOK(w, M{"pending": list})
}

func washPlantScope(u sessionUser) int64 {
	if u.Role == "washplant" {
		return u.OrgID
	}
	return 0
}

// GET /api/rework/orders/{id}  返修单详情（含照片清单与首次检测项）
func handleReworkDetail(w http.ResponseWriter, r *http.Request) {
	id := tailID(r.URL.Path, "/api/rework/orders/")
	if id == 0 {
		handleReworkOrders(w, r)
		return
	}
	u := currentUser(r)
	row := db.QueryRow(reworkSelectCols+` WHERE r.id=$1`, id)
	var reworkID, cycleID, kegID, foundPhotos, recheckPhotos int64
	var reworkNo, code, bar, driver, washplant, washBatch string
	var beer, style, batchNo, defect, note, billParty, status string
	var reResult, reNote, reinspector, reAt, confAt, createdAt, billStatus, billPartyName string
	var fee, depositDeduct float64
	if err := row.Scan(&reworkID, &reworkNo, &cycleID, &kegID, &code, &bar, &driver, &washplant,
		&washBatch, &beer, &style, &batchNo, &defect, &note, &fee, &billParty, &depositDeduct,
		&status, &reResult, &reNote, &reinspector, &reAt, &confAt, &createdAt,
		&foundPhotos, &recheckPhotos, &billStatus, &billPartyName); err != nil {
		fail(w, 404, "返修单不存在")
		return
	}
	if u.Role == "washplant" {
		var wp int64
		_ = db.QueryRow(`SELECT washplant_id FROM rework_orders WHERE id=$1`, id).Scan(&wp)
		if wp != u.OrgID {
			fail(w, 403, "只能查看本清洗厂的返修单")
			return
		}
	} else if u.Role == "bar_manager" {
		var bar int64
		_ = db.QueryRow(`SELECT COALESCE(bar_id,0) FROM rework_orders WHERE id=$1`, id).Scan(&bar)
		if bar != u.OrgID {
			fail(w, 403, "只能查看本店返修单")
			return
		}
	}

	// 周期押金现状
	var depositAmount, released, deducted float64
	var depositStatus string
	_ = db.QueryRow(`SELECT deposit_amount, deposit_status, released_amount, deducted_amount
		FROM keg_cycles WHERE id=$1`, cycleID).
		Scan(&depositAmount, &depositStatus, &released, &deducted)

	// 首次检测项
	var pressureOK, sealOK, cleanOK bool
	var inspector sql.NullString
	_ = db.QueryRow(`SELECT COALESCE(pressure_ok,TRUE), COALESCE(seal_ok,TRUE),
		COALESCE(cleanliness_ok,TRUE), inspector FROM wash_checks WHERE id=
		(SELECT wash_check_id FROM rework_orders WHERE id=$1)`, id).
		Scan(&pressureOK, &sealOK, &cleanOK, &inspector)

	// 照片清单
	prows, _ := db.Query(`SELECT id, phase, file_name, COALESCE(content_type,''),
		COALESCE(text(created_at),'') FROM rework_photos WHERE rework_id=$1 ORDER BY id`, id)
	photos := scanRows(prows, func(rs rowScanner) (M, error) {
		var pid int64
		var phase, fname, ctype, at string
		if err := rs.Scan(&pid, &phase, &fname, &ctype, &at); err != nil {
			return nil, err
		}
		return M{"id": pid, "phase": phase, "file_name": fname,
			"content_type": ctype, "created_at": at}, nil
	})

	writeOK(w, M{
		"order": M{
			"id": reworkID, "rework_no": reworkNo, "cycle_id": cycleID, "keg_id": kegID,
			"keg_code": code, "bar": bar, "driver": driver, "washplant": washplant,
			"wash_batch_no": washBatch, "beer": beer, "style": style, "batch_no": batchNo,
			"defect_type": defect, "defect_note": note, "rework_fee": fee,
			"bill_party": billParty, "deposit_deduct": depositDeduct, "status": status,
			"reinspect_result": reResult, "reinspect_note": reNote, "reinspector": reinspector,
			"reinspected_at": reAt, "confirmed_at": confAt, "created_at": createdAt,
			"bill_status": billStatus, "bill_party_name": billPartyName,
			"deposit_amount": depositAmount, "deposit_status": depositStatus,
			"released_amount": released, "deducted_amount": deducted,
			"first_check": M{"pressure_ok": pressureOK, "seal_ok": sealOK,
				"cleanliness_ok": cleanOK, "inspector": inspector.String},
			"photos": photos,
		},
	})
}

// POST /api/rework/reinspect/{id}  返修完成：清洗厂上传复检结果（可附复检照片）
func handleReworkReinspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	id := tailID(r.URL.Path, "/api/rework/reinspect/")
	var req struct {
		Passed        bool               `json:"passed"`
		PressureOK    *bool              `json:"pressure_ok"`
		SealOK        *bool              `json:"seal_ok"`
		CleanlinessOK *bool              `json:"cleanliness_ok"`
		Note          string             `json:"note"`
		Inspector     string             `json:"inspector"`
		Photos        []reworkPhotoInput `json:"photos"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
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

	type photo struct {
		name, ctype string
		data        []byte
	}
	var photos []photo
	for _, p := range req.Photos {
		name, ctype, data, err := decodePhoto(p)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		photos = append(photos, photo{name, ctype, data})
	}

	u := currentUser(r)
	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		var cycleID, kegID, wpID int64
		var status string
		if err := tx.QueryRow(`SELECT cycle_id, keg_id, washplant_id, status
			FROM rework_orders WHERE id=$1 FOR UPDATE`, id).Scan(&cycleID, &kegID, &wpID, &status); err != nil {
			return errBad("返修单不存在")
		}
		if u.Role == "washplant" && u.OrgID != wpID {
			return errBad("只能复检本清洗厂的返修单")
		}
		if status != "OPEN" {
			return errBad("仅待返修(OPEN)单据可上传复检结果，当前: " + status)
		}

		// 复检同样写入 wash_checks，并在返修单上留痕
		var checkID int64
		if err := tx.QueryRow(`INSERT INTO wash_checks
			(cycle_id,washplant_id,inspector,passed,pressure_ok,seal_ok,cleanliness_ok,defect_note)
			SELECT $1,washplant_id,NULLIF($2,''),$3,$4,$5,$6,NULLIF($7,'')
			FROM rework_orders WHERE id=$8 RETURNING id`,
			cycleID, req.Inspector, passed, pressure, seal, clean, req.Note, id).Scan(&checkID); err != nil {
			return err
		}

		if !passed {
			// 复检仍不合格：保持 OPEN，允许继续返修；桶维持冻结
			if _, err := tx.Exec(`UPDATE rework_orders SET reinspect_result='STILL_FAIL',
				reinspect_note=NULLIF($1,''), reinspector=NULLIF($2,''), reinspected_at=now(),
				recheck_check_id=$3 WHERE id=$4`, req.Note, req.Inspector, checkID, id); err != nil {
				return err
			}
		} else {
			// 复检合格 → REWORKED，等待酒厂仓库确认；确认前桶仍不可灌装
			if _, err := tx.Exec(`UPDATE rework_orders SET status='REWORKED', reinspect_result='PASSED',
				reinspect_note=NULLIF($1,''), reinspector=NULLIF($2,''), reinspected_at=now(),
				recheck_check_id=$3 WHERE id=$4`, req.Note, req.Inspector, checkID, id); err != nil {
				return err
			}
		}
		for _, p := range photos {
			if _, err := tx.Exec(`INSERT INTO rework_photos
				(rework_id,phase,file_name,content_type,data,uploaded_by)
				VALUES ($1,'REINSPECT',$2,$3,$4,$5)`, id, p.name, p.ctype, p.data, u.ID); err != nil {
				return err
			}
		}
		if err := logKegEvent(tx, kegID, cycleID, "REWORK_REINSPECTED", "清洗厂", M{
			"rework_id": id, "passed": passed, "pressure_ok": pressure,
			"seal_ok": seal, "cleanliness_ok": clean, "note": req.Note,
			"next": ternary(passed, "待酒厂仓库确认", "复检仍不合格，继续返修"),
		}); err != nil {
			return err
		}
		if passed {
			resp = M{"passed": true, "reinspect_result": "PASSED", "status": "REWORKED"}
		} else {
			resp = M{"passed": false, "reinspect_result": "STILL_FAIL", "status": "OPEN"}
		}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// POST /api/rework/confirm/{id}  酒厂仓库确认：restore 恢复可灌装 / scrap 报废
func handleReworkConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	id := tailID(r.URL.Path, "/api/rework/confirm/")
	var req struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.Action != "restore" && req.Action != "scrap" {
		fail(w, 400, "操作无效：restore 恢复灌装 / scrap 报废")
		return
	}

	u := currentUser(r)
	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		var cycleID, kegID int64
		var status, reResult string
		if err := tx.QueryRow(`SELECT cycle_id, keg_id, status, COALESCE(reinspect_result,'')
			FROM rework_orders WHERE id=$1 FOR UPDATE`, id).Scan(&cycleID, &kegID, &status, &reResult); err != nil {
			return errBad("返修单不存在")
		}
		if status != "REWORKED" {
			return errBad("仅返修完成、复检合格待确认(REWORKED)的单据可由仓库确认，当前: " + status)
		}
		if req.Action == "restore" {
			if reResult != "PASSED" {
				return errBad("复检未合格，不能恢复灌装；如无法修复请做报废处理")
			}
			if _, err := tx.Exec(`UPDATE rework_orders SET status='CONFIRMED',
				confirmed_by=$1, confirmed_at=now(), confirm_note=NULLIF($2,'') WHERE id=$3`,
				u.ID, req.Note, id); err != nil {
				return err
			}
			// 恢复：桶回库、重新允许灌装；周期回到 WASHED
			if _, err := tx.Exec(`UPDATE kegs SET asset_status='IN_STOCK', refillable=TRUE,
				current_bar_id=NULL WHERE id=$1`, kegID); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE keg_cycles SET stage='WASHED' WHERE id=$1`, cycleID); err != nil {
				return err
			}
			if err := logKegEvent(tx, kegID, cycleID, "REWORK_CONFIRMED", "酒厂仓库", M{
				"rework_id": id, "note": req.Note, "asset_status": "IN_STOCK", "refillable": true,
			}); err != nil {
				return err
			}
			resp = M{"status": "CONFIRMED", "asset_status": "IN_STOCK", "refillable": true}
		} else {
			if _, err := tx.Exec(`UPDATE rework_orders SET status='SCRAPPED',
				confirmed_by=$1, confirmed_at=now(), confirm_note=NULLIF($2,'') WHERE id=$3`,
				u.ID, req.Note, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE kegs SET asset_status='SCRAPPED', refillable=FALSE
				WHERE id=$1`, kegID); err != nil {
				return err
			}
			if err := logKegEvent(tx, kegID, cycleID, "REWORK_SCRAPPED", "酒厂仓库", M{
				"rework_id": id, "note": req.Note,
			}); err != nil {
				return err
			}
			resp = M{"status": "SCRAPPED", "asset_status": "SCRAPPED", "refillable": false}
		}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}

// GET /api/rework/photo/{id}  取桶体照片字节（<img> 直接访问，token 可走 query）
func handleReworkPhoto(w http.ResponseWriter, r *http.Request) {
	// <img> 标签无法携带 Authorization 头，允许 ?token= 访问
	tok := r.URL.Query().Get("token")
	if tok == "" {
		tok = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if _, ok := parseToken(tok); !ok {
		fail(w, 401, "未登录或登录已过期")
		return
	}
	id := tailID(r.URL.Path, "/api/rework/photo/")
	var ctype, fname string
	var data []byte
	err := db.QueryRow(`SELECT COALESCE(content_type,'image/png'), file_name, data
		FROM rework_photos WHERE id=$1`, id).Scan(&ctype, &fname, &data)
	if err == sql.ErrNoRows {
		fail(w, 404, "照片不存在")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `inline; filename="`+fname+`"`)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(data)
}
