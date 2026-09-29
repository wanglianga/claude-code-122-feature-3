package main

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// 返修单路由分发：/api/repairs/...
//
//	POST /api/repairs/create           清洗厂发起返修
//	GET  /api/repairs/{id}             返修详情（含照片/账单/追溯）
//	POST /api/repairs/submit/{id}      清洗厂返修完成，上传复检结果与桶体照片
//	POST /api/repairs/confirm/{id}     酒厂仓库复检确认（合格恢复灌装）
//	GET  /api/repairs/photos/{pid}     下载桶体照片
func routeRepairs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/repairs/")
	seg := strings.SplitN(path, "/", 2)
	head := seg[0]
	tail := ""
	if len(seg) == 2 {
		tail = seg[1]
	}
	switch {
	case head == "create" && r.Method == http.MethodPost:
		requireRole("washplant admin", handleRepairCreate).ServeHTTP(w, r)
	case head == "submit" && r.Method == http.MethodPost:
		requireRole("washplant admin", handleRepairSubmit).ServeHTTP(w, r)
	case head == "confirm" && r.Method == http.MethodPost:
		requireRole("brewery admin", handleRepairConfirm).ServeHTTP(w, r)
	case head == "photos" && r.Method == http.MethodGet:
		requireRole("washplant brewery finance qc admin",
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handleRepairPhoto(w, r, atoi64(tail))
			})).ServeHTTP(w, r)
	case head != "" && r.Method == http.MethodGet && atoi64(head) > 0:
		requireRole("washplant brewery finance qc admin",
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handleRepairDetail(w, r, atoi64(head))
			})).ServeHTTP(w, r)
	default:
		fail(w, 404, "接口不存在")
	}
}

// photoInput 前端以 base64（可带 data: 前缀）上传桶体照片
type photoInput struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Data        string `json:"data"`
}

// photoBin 解码后的照片二进制
type photoBin struct {
	name  string
	ctype string
	raw   []byte
}

const maxPhotoBytes = 3 * 1024 * 1024 // 单张 3MB

func decodePhoto(p photoInput) (name, contentType string, raw []byte, err error) {
	name = strings.TrimSpace(p.Name)
	if name == "" {
		name = "photo.png"
	}
	contentType = p.ContentType
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "image/png"
	}
	src := p.Data
	if strings.HasPrefix(src, "data:") {
		if i := strings.Index(src, ","); i >= 0 {
			head := src[:i]
			if j := strings.Index(head, ";"); j >= 0 {
				head = head[:j]
			}
			if mime := strings.TrimPrefix(head, "data:"); mime != "" {
				contentType = mime
			}
			src = src[i+1:]
		}
	}
	raw, err = base64.StdEncoding.DecodeString(strings.TrimSpace(src))
	if err != nil {
		return "", "", nil, errBad("照片 " + name + " 不是有效的 base64 数据")
	}
	if len(raw) == 0 {
		return "", "", nil, errBad("照片 " + name + " 内容为空")
	}
	if len(raw) > maxPhotoBytes {
		return "", "", nil, errBad("照片 " + name + " 超过 3MB 限制")
	}
	return name, contentType, raw, nil
}

// padRP 返修单号 RP-0001
func padRP(id int64) string {
	s := itoa(id)
	for len(s) < 4 {
		s = "0" + s
	}
	return "RP-" + s
}

// ---------- 返修单列表 ----------

// GET /api/repairs?status=OPEN|SUBMITTED|CONFIRMED|REJECTED|SCRAPPED
func handleRepairList(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	q := `SELECT r.id, r.repair_no, k.keg_code, k.capacity_liters, r.defect_type,
	       COALESCE(r.defect_note,''), r.repair_fee, r.deposit_deduct, r.fee_party, r.status,
	       COALESCE(r.wash_batch_no,''), COALESCE(b.name,''), COALESCE(d.name,''),
	       ba.batch_no, be.name, be.style, wp.name,
	       COALESCE(r.recheck_result,''), COALESCE(text(r.submitted_at),''),
	       COALESCE(text(r.confirmed_at),''), text(r.created_at),
	       (SELECT count(*) FROM repair_photos p WHERE p.repair_id=r.id),
	       (SELECT count(*) FROM billing_charges ch WHERE ch.repair_id=r.id AND ch.status='PENDING')
		FROM keg_repairs r
		JOIN kegs k ON k.id=r.keg_id
		JOIN batches ba ON ba.id=r.batch_id
		JOIN beers be ON be.id=ba.beer_id
		JOIN washplants wp ON wp.id=r.washplant_id
		LEFT JOIN bars b ON b.id=r.bar_id
		LEFT JOIN drivers d ON d.id=r.driver_id
		WHERE 1=1`
	args := []interface{}{}
	// 清洗厂只能看本厂返修单
	if u.Role == "washplant" {
		q += " AND r.washplant_id=$1"
		args = append(args, u.OrgID)
	}
	if s := r.URL.Query().Get("status"); s != "" {
		q += " AND r.status=" + pidx(args)
		args = append(args, s)
	}
	q += " ORDER BY r.id DESC"
	rows, err := db.Query(q, args...)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := scanRows(rows, func(rs rowScanner) (M, error) {
		var id int64
		var repairNo, code, defectType, note, feeParty, status string
		var washBatch, bar, driver, batchNo, beer, style, wpName string
		var recheck, submitted, confirmed, createdAt string
		var capacity, fee, deduct float64
		var photoN, pendingCharges int64
		if err := rs.Scan(&id, &repairNo, &code, &capacity, &defectType, &note,
			&fee, &deduct, &feeParty, &status, &washBatch, &bar, &driver,
			&batchNo, &beer, &style, &wpName, &recheck, &submitted, &confirmed,
			&createdAt, &photoN, &pendingCharges); err != nil {
			return nil, err
		}
		return M{
			"id": id, "repair_no": repairNo, "keg_code": code, "capacity_liters": capacity,
			"defect_type": defectType, "defect_note": note,
			"repair_fee": fee, "deposit_deduct": deduct, "fee_party": feeParty,
			"status": status, "wash_batch_no": washBatch, "bar": bar, "driver": driver,
			"batch_no": batchNo, "beer": beer, "style": style, "washplant": wpName,
			"recheck_result": recheck, "submitted_at": submitted, "confirmed_at": confirmed,
			"created_at": createdAt, "photo_count": photoN, "pending_charge_count": pendingCharges,
		}, nil
	})

	// 待发起返修的不合格桶（stage=FAILED 且无未关闭返修单）
	canCreate := []M{}
	if u.Role == "washplant" || u.Role == "admin" {
		cq := `SELECT c.id, k.keg_code, k.capacity_liters, ba.batch_no, be.name, be.style,
			COALESCE(b.name,''), COALESCE(d.name,''), COALESCE(wc.wash_batch_no,''),
			COALESCE(wc.defect_note,''), text(wc.checked_at), wp.id, wp.name
			FROM keg_cycles c
			JOIN kegs k ON k.id=c.keg_id
			JOIN batches ba ON ba.id=c.batch_id
			JOIN beers be ON be.id=ba.beer_id
			JOIN wash_checks wc ON wc.id=c.wash_id
			JOIN washplants wp ON wp.id=wc.washplant_id
			LEFT JOIN bars b ON b.id=c.bar_id
			LEFT JOIN drivers d ON d.id=c.driver_id
			WHERE c.stage='FAILED' AND k.asset_status='WASH_FAIL'
			AND NOT EXISTS (SELECT 1 FROM keg_repairs r
				WHERE r.cycle_id=c.id AND r.status IN ('OPEN','SUBMITTED'))`
		cargs := []interface{}{}
		if u.Role == "washplant" {
			cq += " AND wp.id=$1"
			cargs = append(cargs, u.OrgID)
		}
		cq += " ORDER BY wc.checked_at DESC"
		crows, err := db.Query(cq, cargs...)
		if err == nil {
			canCreate = scanRows(crows, func(rs rowScanner) (M, error) {
				var id, wpID int64
				var code, batchNo, beer, style, bar, driver, washBatch, note, checkedAt, wpName string
				var capacity float64
				if err := rs.Scan(&id, &code, &capacity, &batchNo, &beer, &style,
					&bar, &driver, &washBatch, &note, &checkedAt, &wpID, &wpName); err != nil {
					return nil, err
				}
				return M{"cycle_id": id, "keg_code": code, "capacity_liters": capacity,
					"batch_no": batchNo, "beer": beer, "style": style, "bar": bar,
					"driver": driver, "wash_batch_no": washBatch, "defect_note": note,
					"checked_at": checkedAt, "washplant_id": wpID, "washplant": wpName}, nil
			})
		}
	}

	writeOK(w, M{"repairs": list, "can_create": canCreate})
}

// ---------- 发起返修 ----------

// POST /api/repairs/create
// body: {cycle_id, defect_type, defect_note, repair_fee, deposit_deduct, fee_party,
//
//	wash_batch_no?, photos:[{name,content_type,data}]}
func handleRepairCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CycleID       int64        `json:"cycle_id"`
		DefectType    string       `json:"defect_type"`
		DefectNote    string       `json:"defect_note"`
		RepairFee     float64      `json:"repair_fee"`
		DepositDeduct float64      `json:"deposit_deduct"`
		FeeParty      string       `json:"fee_party"`
		WashBatchNo   string       `json:"wash_batch_no"`
		Photos        []photoInput `json:"photos"`
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
	case "异味", "阀门损坏", "变形", "其他":
	default:
		fail(w, 400, "缺陷类型需为 异味/阀门损坏/变形/其他")
		return
	}
	if req.FeeParty != "BAR" && req.FeeParty != "WASHPLANT" {
		fail(w, 400, "费用承担方需为 BAR 或 WASHPLANT")
		return
	}
	if req.RepairFee < 0 || req.DepositDeduct < 0 {
		fail(w, 400, "返修费用与押金扣减不能为负")
		return
	}
	if len(req.Photos) == 0 {
		fail(w, 400, "发起返修必须上传至少 1 张桶体缺陷照片，仓库需留档")
		return
	}
	photos := make([]photoBin, 0, len(req.Photos))
	for _, p := range req.Photos {
		name, ctype, raw, err := decodePhoto(p)
		if err != nil {
			respondErr(w, err)
			return
		}
		photos = append(photos, photoBin{name: name, ctype: ctype, raw: raw})
	}

	u := currentUser(r)
	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		var kegID, barID, driverID, batchID, cityID, washCheckID, washplantID int64
		var stage, depositStatus string
		var deposit float64
		if err := tx.QueryRow(`SELECT keg_id, COALESCE(bar_id,0), COALESCE(driver_id,0), batch_id,
			city_id, stage, deposit_amount, deposit_status, COALESCE(wash_id,0)
			FROM keg_cycles WHERE id=$1 FOR UPDATE`, req.CycleID).
			Scan(&kegID, &barID, &driverID, &batchID, &cityID, &stage, &deposit, &depositStatus, &washCheckID); err != nil {
			return errBad("流转记录不存在")
		}
		if stage != "FAILED" {
			return errBad("仅清洗不合格（FAILED）的桶可发起返修，当前阶段: " + stage)
		}
		if req.DepositDeduct > deposit {
			return errBad(fmt.Sprintf("押金扣减 ¥%.0f 不能超过押金总额 ¥%.0f", req.DepositDeduct, deposit))
		}
		var code string
		if err := tx.QueryRow(`SELECT keg_code FROM kegs WHERE id=$1 FOR UPDATE`, kegID).Scan(&code); err != nil {
			return err
		}

		// 检测记录与清洗批次（追溯依据）
		if washCheckID == 0 {
			return errBad("该桶缺少清洗检测记录，无法发起返修")
		}
		if err := tx.QueryRow(`SELECT washplant_id FROM wash_checks WHERE id=$1`, washCheckID).
			Scan(&washplantID); err != nil {
			return err
		}
		if u.Role == "washplant" && u.OrgID != washplantID {
			return errBad("不能对其他清洗厂的检测记录发起返修")
		}
		washBatch := strings.TrimSpace(req.WashBatchNo)
		if washBatch == "" {
			if err := tx.QueryRow(`SELECT COALESCE(wash_batch_no,'') FROM wash_checks WHERE id=$1`,
				washCheckID).Scan(&washBatch); err != nil {
				return err
			}
		}

		// 同周期已有未关闭返修单
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM keg_repairs WHERE cycle_id=$1
			AND status IN ('OPEN','SUBMITTED')`, req.CycleID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errBad("该桶已有进行中的返修单，请勿重复发起")
		}

		// 返修单号取序列
		var rid int64
		if err := tx.QueryRow(`SELECT nextval(pg_get_serial_sequence('keg_repairs','id'))`).Scan(&rid); err != nil {
			return err
		}
		repairNo := padRP(rid)
		if _, err := tx.Exec(`INSERT INTO keg_repairs
			(id,repair_no,keg_id,cycle_id,wash_check_id,washplant_id,wash_batch_no,bar_id,driver_id,batch_id,
			 defect_type,defect_note,repair_fee,deposit_deduct,fee_party,status)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,NULLIF($9,0),NULLIF($10,0),$11,NULLIF($12,''),$13,$14,$15,'OPEN')`,
			rid, repairNo, kegID, req.CycleID, washCheckID, washplantID, washBatch,
			nzBar(barID), driverID, batchID,
			req.DefectType, req.DefectNote, req.RepairFee, req.DepositDeduct, req.FeeParty); err != nil {
			return err
		}

		// 桶体缺陷照片入库留档
		for _, p := range photos {
			if _, err := tx.Exec(`INSERT INTO repair_photos
				(repair_id,stage,file_name,content_type,data,uploaded_by)
				VALUES ($1,'DEFECT',$2,$3,$4,$5)`, rid, p.name, p.ctype, p.raw, u.ID); err != nil {
				return err
			}
		}

		// 返修费账单：进入对应酒吧或清洗厂账单（待财务确认）
		if req.RepairFee > 0 {
			partyID := barID
			if req.FeeParty == "WASHPLANT" {
				partyID = washplantID
			}
			if _, err := tx.Exec(`INSERT INTO billing_charges
				(repair_id,keg_id,cycle_id,charge_type,party_type,party_id,bar_id,amount,memo,status,created_by)
				VALUES ($1,$2,$3,'REPAIR_FEE',$4,$5,NULLIF($6,0),$7,$8,'PENDING',$9)`,
				rid, kegID, req.CycleID, req.FeeParty, nzParty(partyID), barID,
				req.RepairFee, fmt.Sprintf("%s %s返修费（清洗批次 %s）", repairNo, req.DefectType, washBatch),
				u.ID); err != nil {
				return err
			}
		}
		// 押金扣减账单（押金由酒吧缴纳，承担方恒为酒吧）
		if req.DepositDeduct > 0 {
			if _, err := tx.Exec(`INSERT INTO billing_charges
				(repair_id,keg_id,cycle_id,charge_type,party_type,party_id,bar_id,amount,memo,status,created_by)
				VALUES ($1,$2,$3,'DEPOSIT_DEDUCT','BAR',$4,$5,$6,$7,'PENDING',$8)`,
				rid, kegID, req.CycleID, barID, barID, req.DepositDeduct,
				fmt.Sprintf("%s %s押金扣减（清洗批次 %s）", repairNo, req.DefectType, washBatch), u.ID); err != nil {
				return err
			}
			// 押金扣减同步落周期（金额立即生效，财务确认账单后补记结算人/时间）
			newStatus := "PARTIAL"
			released := deposit - req.DepositDeduct
			if req.DepositDeduct >= deposit {
				newStatus = "DEDUCTED"
				released = 0
			}
			if _, err := tx.Exec(`UPDATE keg_cycles SET deposit_status=$1, released_amount=$2,
				deducted_amount=$3, deduct_reason=$4 WHERE id=$5`,
				newStatus, released, req.DepositDeduct,
				fmt.Sprintf("清洗不合格返修（%s/%s）：押金扣减 ¥%.0f，账单待财务确认", repairNo, req.DefectType, req.DepositDeduct),
				req.CycleID); err != nil {
				return err
			}
		}

		// 桶体暂停再灌装
		if _, err := tx.Exec(`UPDATE kegs SET asset_status='REPAIR', refillable=FALSE WHERE id=$1`, kegID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE keg_cycles SET stage='REPAIRING' WHERE id=$1`, req.CycleID); err != nil {
			return err
		}

		if err := logKegEvent(tx, kegID, req.CycleID, "REPAIR_CREATED", "清洗厂", M{
			"repair_no": repairNo, "defect_type": req.DefectType, "note": req.DefectNote,
			"wash_batch_no": washBatch, "bar_id": barID, "driver_id": driverID,
			"repair_fee": req.RepairFee, "deposit_deduct": req.DepositDeduct,
			"fee_party": req.FeeParty,
		}); err != nil {
			return err
		}

		// 通知被追溯到的上一家酒吧
		if barID != 0 {
			body := fmt.Sprintf(
				"贵店归还的 %s 在清洗批次 %s 检测中发现%s，已转返修（%s）。返修费 ¥%.0f、押金扣减 ¥%.0f，账单待财务确认后入账。",
				code, washBatch, req.DefectType, repairNo, req.RepairFee, req.DepositDeduct)
			if _, err := tx.Exec(`INSERT INTO notifications (batch_id,audience,target_id,title,body)
				VALUES ($1,'BAR',$2,$3,$4)`,
				batchID, barID, "清洗不合格返修通知："+code+"（"+repairNo+"）", body); err != nil {
				return err
			}
		}

		resp = M{"repair_id": rid, "repair_no": repairNo, "asset_status": "REPAIR",
			"refillable": false, "stage": "REPAIRING",
			"repair_fee": req.RepairFee, "deposit_deduct": req.DepositDeduct}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}

func nzBar(id int64) interface{} {
	if id == 0 {
		return nil
	}
	return id
}
func nzParty(id int64) interface{} { return nzBar(id) }

// ---------- 返修完成：清洗厂上传复检结果与照片 ----------

// POST /api/repairs/submit/{id}
// body: {recheck_result:"合格"|"不合格", inspector, note, photos:[...]}
func handleRepairSubmit(w http.ResponseWriter, r *http.Request) {
	id := tailID(r.URL.Path, "/api/repairs/submit/")
	var req struct {
		RecheckResult string       `json:"recheck_result"`
		Inspector     string       `json:"inspector"`
		Note          string       `json:"note"`
		Photos        []photoInput `json:"photos"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.RecheckResult != "合格" && req.RecheckResult != "不合格" {
		fail(w, 400, "复检结论需为 合格 或 不合格")
		return
	}
	if len(req.Photos) == 0 {
		fail(w, 400, "返修完成需上传至少 1 张复检桶体照片")
		return
	}
	photos := make([]photoBin, 0, len(req.Photos))
	for _, p := range req.Photos {
		name, ctype, raw, err := decodePhoto(p)
		if err != nil {
			respondErr(w, err)
			return
		}
		photos = append(photos, photoBin{name: name, ctype: ctype, raw: raw})
	}

	u := currentUser(r)
	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		var kegID, cycleID, wpID int64
		var status, repairNo string
		if err := tx.QueryRow(`SELECT keg_id, cycle_id, washplant_id, status, repair_no
			FROM keg_repairs WHERE id=$1 FOR UPDATE`, id).
			Scan(&kegID, &cycleID, &wpID, &status, &repairNo); err != nil {
			return errBad("返修单不存在")
		}
		if u.Role == "washplant" && u.OrgID != wpID {
			return errBad("不能提交其他清洗厂的返修单")
		}
		if status != "OPEN" {
			return errBad("仅返修中（OPEN）的单据可上传复检结果，当前: " + status)
		}
		if _, err := tx.Exec(`UPDATE keg_repairs SET status='SUBMITTED', recheck_result=$1,
			recheck_inspector=NULLIF($2,''), recheck_note=NULLIF($3,''), submitted_at=now()
			WHERE id=$4`, req.RecheckResult, req.Inspector, req.Note, id); err != nil {
			return err
		}
		for _, p := range photos {
			if _, err := tx.Exec(`INSERT INTO repair_photos
				(repair_id,stage,file_name,content_type,data,uploaded_by)
				VALUES ($1,'RECHECK',$2,$3,$4,$5)`, id, p.name, p.ctype, p.raw, u.ID); err != nil {
				return err
			}
		}
		if err := logKegEvent(tx, kegID, cycleID, "REPAIR_SUBMITTED", "清洗厂", M{
			"repair_no": repairNo, "recheck_result": req.RecheckResult,
			"inspector": req.Inspector, "note": req.Note,
		}); err != nil {
			return err
		}
		resp = M{"repair_id": id, "repair_no": repairNo, "status": "SUBMITTED"}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}

// ---------- 酒厂仓库复检确认 ----------

// POST /api/repairs/confirm/{id}  body: {pass:true|false, note}
// 合格 → 桶回库恢复可灌装；不合格 → 退回清洗厂继续返修
func handleRepairConfirm(w http.ResponseWriter, r *http.Request) {
	id := tailID(r.URL.Path, "/api/repairs/confirm/")
	var req struct {
		Pass bool   `json:"pass"`
		Note string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	u := currentUser(r)
	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		var kegID, cycleID, barID, batchID int64
		var status, repairNo, code string
		if err := tx.QueryRow(`SELECT r.keg_id, r.cycle_id, COALESCE(r.bar_id,0), r.batch_id,
			r.status, r.repair_no, k.keg_code
			FROM keg_repairs r JOIN kegs k ON k.id=r.keg_id
			WHERE r.id=$1 FOR UPDATE`, id).
			Scan(&kegID, &cycleID, &barID, &batchID, &status, &repairNo, &code); err != nil {
			return errBad("返修单不存在")
		}
		if status != "SUBMITTED" {
			return errBad("仅清洗厂已提交复检（SUBMITTED）的单据可由仓库确认，当前: " + status)
		}

		if req.Pass {
			if _, err := tx.Exec(`UPDATE keg_repairs SET status='CONFIRMED', confirmed_by=$1,
				confirmed_at=now(), confirm_note=NULLIF($2,'') WHERE id=$3`, u.ID, req.Note, id); err != nil {
				return err
			}
			// 恢复再灌装，桶回酒厂仓库
			if _, err := tx.Exec(`UPDATE kegs SET asset_status='IN_STOCK', refillable=TRUE,
				current_bar_id=NULL WHERE id=$1`, kegID); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE keg_cycles SET stage='WASHED' WHERE id=$1`, cycleID); err != nil {
				return err
			}
			if err := logKegEvent(tx, kegID, cycleID, "REPAIR_CONFIRMED", "酒厂仓库", M{
				"repair_no": repairNo, "refillable": true, "note": req.Note,
			}); err != nil {
				return err
			}
			if barID != 0 {
				if _, err := tx.Exec(`INSERT INTO notifications (batch_id,audience,target_id,title,body)
					VALUES ($1,'BAR',$2,$3,$4)`, batchID, barID,
					"返修复检合格通知："+code+"（"+repairNo+"）",
					code+" 返修后经酒厂仓库复检合格，已恢复再灌装资格并回库；返修费用与押金扣减以财务账单为准。"); err != nil {
					return err
				}
			}
			resp = M{"repair_id": id, "repair_no": repairNo, "status": "CONFIRMED",
				"asset_status": "IN_STOCK", "refillable": true, "stage": "WASHED"}
		} else {
			// 复检仍不合格：退回清洗厂继续返修
			if _, err := tx.Exec(`UPDATE keg_repairs SET status='OPEN',
				recheck_note=COALESCE(NULLIF(recheck_note,''),'')||$2 WHERE id=$1`,
				id, endNote(req.Note)); err != nil {
				return err
			}
			if err := logKegEvent(tx, kegID, cycleID, "REPAIR_REJECTED", "酒厂仓库", M{
				"repair_no": repairNo, "note": req.Note,
			}); err != nil {
				return err
			}
			resp = M{"repair_id": id, "repair_no": repairNo, "status": "OPEN"}
		}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}

func endNote(note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	return "｜仓库退回原因：" + note
}

// ---------- 返修详情 ----------

func handleRepairDetail(w http.ResponseWriter, r *http.Request, id int64) {
	u := currentUser(r)
	var rid, kegID, cycleID, washCheckID, wpID, barID, driverID, batchID int64
	var repairNo, kegCode, washBatch, defectType, defectNote, feeParty, status string
	var recheckResult, recheckInspector, recheckNote, confirmNote sql.NullString
	var capacity, fee, deduct float64
	var createdAt, submittedAt, confirmedAt sql.NullTime
	err := db.QueryRow(`SELECT r.id, r.keg_id, r.cycle_id, COALESCE(r.wash_check_id,0),
		r.washplant_id, COALESCE(r.bar_id,0), COALESCE(r.driver_id,0), r.batch_id,
		r.repair_no, k.keg_code, k.capacity_liters, COALESCE(r.wash_batch_no,''),
		r.defect_type, COALESCE(r.defect_note,''), r.repair_fee, r.deposit_deduct, r.fee_party, r.status,
		r.recheck_result, r.recheck_inspector, r.recheck_note, r.confirm_note,
		r.created_at, r.submitted_at, r.confirmed_at
		FROM keg_repairs r JOIN kegs k ON k.id=r.keg_id WHERE r.id=$1`, id).
		Scan(&rid, &kegID, &cycleID, &washCheckID, &wpID, &barID, &driverID, &batchID,
			&repairNo, &kegCode, &capacity, &washBatch, &defectType, &defectNote,
			&fee, &deduct, &feeParty, &status,
			&recheckResult, &recheckInspector, &recheckNote, &confirmNote,
			&createdAt, &submittedAt, &confirmedAt)
	if err == sql.ErrNoRows {
		fail(w, 404, "返修单不存在")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	// 清洗厂只能查看本厂返修单
	if u.Role == "washplant" && u.OrgID != wpID {
		fail(w, 403, "不能查看其他清洗厂的返修单")
		return
	}

	repair := M{
		"id": rid, "repair_no": repairNo, "keg_id": kegID, "keg_code": kegCode,
		"cycle_id": cycleID, "capacity_liters": capacity,
		"wash_check_id": washCheckID, "washplant_id": wpID,
		"bar_id": barID, "driver_id": driverID, "batch_id": batchID,
		"wash_batch_no": washBatch, "defect_type": defectType, "defect_note": defectNote,
		"repair_fee": fee, "deposit_deduct": deduct, "fee_party": feeParty, "status": status,
		"recheck_result": recheckResult.String, "recheck_inspector": recheckInspector.String,
		"recheck_note": recheckNote.String, "confirm_note": confirmNote.String,
		"created_at": tsStr(createdAt), "submitted_at": tsStr(submittedAt),
		"confirmed_at": tsStr(confirmedAt),
	}

	// 追溯链路：上一家酒吧 / 回收司机 / 清洗厂 / 酒液批次 / 检测记录
	trace := M{}
	if barID != 0 {
		var name, manager, phone string
		_ = db.QueryRow(`SELECT name, COALESCE(manager,''), COALESCE(phone,'') FROM bars WHERE id=$1`, barID).
			Scan(&name, &manager, &phone)
		trace["bar"] = M{"id": barID, "name": name, "manager": manager, "phone": phone}
	}
	if driverID != 0 {
		var name, phone string
		_ = db.QueryRow(`SELECT name, COALESCE(phone,'') FROM drivers WHERE id=$1`, driverID).
			Scan(&name, &phone)
		trace["driver"] = M{"id": driverID, "name": name, "phone": phone}
	}
	var wpName, cityName string
	_ = db.QueryRow(`SELECT wp.name, c.name FROM washplants wp JOIN cities c ON c.id=wp.city_id
		WHERE wp.id=$1`, wpID).Scan(&wpName, &cityName)
	trace["washplant"] = M{"id": wpID, "name": wpName, "city": cityName}
	var batchNo, beerName, style string
	_ = db.QueryRow(`SELECT ba.batch_no, be.name, be.style FROM batches ba
		JOIN beers be ON be.id=ba.beer_id WHERE ba.id=$1`, batchID).Scan(&batchNo, &beerName, &style)
	trace["batch"] = M{"id": batchID, "batch_no": batchNo, "beer": beerName, "style": style}
	if washCheckID != 0 {
		trace["wash_check"] = mapWashCheck(washCheckID)
	}
	// 回收扫码记录（司机登记的桶况）
	scan := M{}
	var appearance, residue, cap, scannedAt sql.NullString
	if err := db.QueryRow(`SELECT appearance, residue_level, cap_status, text(scanned_at)
		FROM pickup_scans WHERE cycle_id=$1 ORDER BY id DESC LIMIT 1`, cycleID).
		Scan(&appearance, &residue, &cap, &scannedAt); err == nil {
		scan = M{"appearance": appearance.String, "residue_level": residue.String,
			"cap_status": cap.String, "scanned_at": scannedAt.String}
	}
	trace["pickup_scan"] = scan

	// 桶体照片（base64 内联，单单据量小；同时提供下载地址）
	rows, _ := db.Query(`SELECT id, stage, file_name, content_type, encode(data,'base64'),
		text(created_at) FROM repair_photos WHERE repair_id=$1 ORDER BY id`, id)
	photos := scanRows(rows, func(rs rowScanner) (M, error) {
		var pid int64
		var stage, name, ctype, b64, at string
		if err := rs.Scan(&pid, &stage, &name, &ctype, &b64, &at); err != nil {
			return nil, err
		}
		return M{"id": pid, "stage": stage, "file_name": name, "content_type": ctype,
			"created_at": at, "url": "/api/repairs/photos/" + itoa(pid),
			"data_url": "data:" + ctype + ";base64," + b64}, nil
	})

	// 账单
	charges := queryCharges(`WHERE ch.repair_id=$1 ORDER BY ch.id`, id)

	writeOK(w, M{"repair": repair, "trace": trace, "photos": photos, "charges": charges})
}

func mapWashCheck(id int64) M {
	var inspector, note sql.NullString
	var passed, pressure, seal, clean bool
	var at sql.NullTime
	var batchNo sql.NullString
	if err := db.QueryRow(`SELECT COALESCE(wash_batch_no,''), COALESCE(inspector,''),
		passed, pressure_ok, seal_ok, cleanliness_ok, COALESCE(defect_note,''), checked_at
		FROM wash_checks WHERE id=$1`, id).
		Scan(&batchNo, &inspector, &passed, &pressure, &seal, &clean, &note, &at); err != nil {
		return M{}
	}
	return M{"id": id, "wash_batch_no": batchNo.String, "inspector": inspector.String,
		"passed": passed, "pressure_ok": pressure, "seal_ok": seal,
		"cleanliness_ok": clean, "defect_note": note.String, "checked_at": tsStr(at)}
}

// 照片下载：GET /api/repairs/photos/{pid}
func handleRepairPhoto(w http.ResponseWriter, r *http.Request, pid int64) {
	if pid == 0 {
		fail(w, 400, "缺少照片 ID")
		return
	}
	u := currentUser(r)
	var name, ctype string
	var data []byte
	var wpID int64
	err := db.QueryRow(`SELECT p.file_name, p.content_type, p.data, r.washplant_id
		FROM repair_photos p JOIN keg_repairs r ON r.id=p.repair_id WHERE p.id=$1`, pid).
		Scan(&name, &ctype, &data, &wpID)
	if err == sql.ErrNoRows {
		fail(w, 404, "照片不存在")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if u.Role == "washplant" && u.OrgID != wpID {
		fail(w, 403, "不能查看其他清洗厂的返修照片")
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", "inline; filename=\""+name+"\"")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

// ---------- 账单 ----------

func queryCharges(where string, args ...interface{}) []M {
	q := `SELECT ch.id, ch.repair_id, ch.keg_id, COALESCE(ch.cycle_id,0),
		ch.charge_type, ch.party_type, COALESCE(ch.party_id,0), COALESCE(ch.bar_id,0),
		ch.amount, COALESCE(ch.memo,''), ch.status,
		COALESCE(b.name,''), COALESCE(wp.name,''), k.keg_code, r.repair_no,
		COALESCE(text(ch.created_at),''), COALESCE(text(ch.confirmed_at),'')
		FROM billing_charges ch
		JOIN kegs k ON k.id=ch.keg_id
		JOIN keg_repairs r ON r.id=ch.repair_id
		LEFT JOIN bars b ON b.id=ch.bar_id
		LEFT JOIN washplants wp ON wp.id=CASE WHEN ch.party_type='WASHPLANT' THEN ch.party_id ELSE NULL END
		` + where
	rows, err := db.Query(q, args...)
	if err != nil {
		return []M{}
	}
	return scanRows(rows, func(rs rowScanner) (M, error) {
		var id, repairID, kegID, cycleID, partyID, barID int64
		var chargeType, partyType, memo, status, bar, wp, code, repairNo, createdAt, confirmedAt string
		var amount float64
		if err := rs.Scan(&id, &repairID, &kegID, &cycleID, &chargeType, &partyType,
			&partyID, &barID, &amount, &memo, &status, &bar, &wp, &code, &repairNo,
			&createdAt, &confirmedAt); err != nil {
			return nil, err
		}
		partyName := bar
		if partyType == "WASHPLANT" {
			partyName = wp
		}
		return M{"id": id, "repair_id": repairID, "keg_id": kegID, "cycle_id": cycleID,
			"charge_type": chargeType, "party_type": partyType, "party_id": partyID,
			"bar_id": barID, "amount": amount, "memo": memo, "status": status,
			"party_name": partyName, "keg_code": code, "repair_no": repairNo,
			"created_at": createdAt, "confirmed_at": confirmedAt}, nil
	})
}

func billingSummary(where string, args ...interface{}) M {
	q := `SELECT COALESCE(sum(amount) FILTER (WHERE status='PENDING'),0),
		COALESCE(sum(amount) FILTER (WHERE status='CONFIRMED'),0),
		count(*) FILTER (WHERE status='PENDING'),
		count(*) FILTER (WHERE status='CONFIRMED')
		FROM billing_charges ch ` + where
	var pending, confirmed float64
	var pn, cn int64
	_ = db.QueryRow(q, args...).Scan(&pending, &confirmed, &pn, &cn)
	return M{"pending_amount": pending, "confirmed_amount": confirmed,
		"pending_count": pn, "confirmed_count": cn}
}

// GET /api/finance/billing?status=&party_type=
func handleBillingList(w http.ResponseWriter, r *http.Request) {
	where := "WHERE 1=1"
	args := []interface{}{}
	if s := r.URL.Query().Get("status"); s != "" {
		where += " AND ch.status=" + pidx(args)
		args = append(args, s)
	}
	if s := r.URL.Query().Get("party_type"); s != "" {
		where += " AND ch.party_type=" + pidx(args)
		args = append(args, s)
	}
	charges := queryCharges(where+" ORDER BY ch.id DESC", args...)
	writeOK(w, M{
		"charges":  charges,
		"summary":  billingSummary("WHERE 1=1"),
		"by_party": billingByParty(),
	})
}

func billingByParty() []M {
	rows, err := db.Query(`SELECT ch.party_type, COALESCE(ch.party_id,0),
		COALESCE(MAX(CASE WHEN ch.party_type='BAR' THEN b.name ELSE wp.name END),''),
		COALESCE(sum(ch.amount) FILTER (WHERE ch.status='PENDING'),0),
		COALESCE(sum(ch.amount) FILTER (WHERE ch.status='CONFIRMED'),0)
		FROM billing_charges ch
		LEFT JOIN bars b ON b.id=ch.party_id AND ch.party_type='BAR'
		LEFT JOIN washplants wp ON wp.id=ch.party_id AND ch.party_type='WASHPLANT'
		GROUP BY 1,2 ORDER BY 1,2`)
	if err != nil {
		return []M{}
	}
	return scanRows(rows, func(rs rowScanner) (M, error) {
		var partyType, name string
		var partyID int64
		var pending, confirmed float64
		if err := rs.Scan(&partyType, &partyID, &name, &pending, &confirmed); err != nil {
			return nil, err
		}
		return M{"party_type": partyType, "party_id": partyID, "party_name": name,
			"pending_amount": pending, "confirmed_amount": confirmed}, nil
	})
}

// POST /api/finance/billing/confirm/{id}  财务确认返修费用/押金扣减，账单同步入账
func handleBillingConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "仅支持 POST")
		return
	}
	id := tailID(r.URL.Path, "/api/finance/billing/confirm/")
	u := currentUser(r)
	resp := M{}
	err := runTx(func(tx *sql.Tx) error {
		var kegID, cycleID, barID int64
		var chargeType, status string
		var amount float64
		if err := tx.QueryRow(`SELECT keg_id, COALESCE(cycle_id,0), COALESCE(bar_id,0),
			charge_type, amount, status FROM billing_charges WHERE id=$1 FOR UPDATE`,
			id).Scan(&kegID, &cycleID, &barID, &chargeType, &amount, &status); err != nil {
			return errBad("账单条目不存在")
		}
		if status != "PENDING" {
			return errBad("该账单条目已确认，不能重复操作")
		}
		if _, err := tx.Exec(`UPDATE billing_charges SET status='CONFIRMED',
			confirmed_by=$1, confirmed_at=now() WHERE id=$2`, u.ID, id); err != nil {
			return err
		}
		// 押金扣减确认：补记周期结算人/时间（金额在返修登记时已同步生成）
		if chargeType == "DEPOSIT_DEDUCT" && cycleID != 0 {
			if _, err := tx.Exec(`UPDATE keg_cycles SET settled_by=$1, settled_at=now()
				WHERE id=$2 AND settled_at IS NULL`, u.ID, cycleID); err != nil {
				return err
			}
		}
		if err := logKegEvent(tx, kegID, cycleID, "BILLING_CONFIRMED", "财务", M{
			"charge_id": id, "charge_type": chargeType, "amount": amount, "bar_id": barID,
		}); err != nil {
			return err
		}
		resp = M{"id": id, "status": "CONFIRMED", "charge_type": chargeType, "amount": amount}
		return nil
	})
	if err != nil {
		respondErr(w, err)
		return
	}
	writeOK(w, resp)
}

// GET /api/bar/billing  酒吧查看本店返修账单
func handleBarBilling(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	barID := u.OrgID
	charges := queryCharges(`WHERE ch.party_type='BAR' AND ch.bar_id=$1 ORDER BY ch.id DESC`, barID)
	summary := billingSummary(`WHERE ch.party_type='BAR' AND ch.bar_id=$1`, barID)
	writeOK(w, M{"charges": charges, "summary": summary, "bar_id": barID})
}
