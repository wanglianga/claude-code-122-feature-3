package main

import (
	"database/sql"
	"net/http"
)

// GET /api/qc/overview  品控看板：投诉聚合、停售建议、异常桶清单
func handleQCOverview(w http.ResponseWriter, r *http.Request) {
	// 按批次聚合：几家酒吧、几条口味/气压/流速投诉
	rows, err := db.Query(`
		SELECT b.id, be.name, be.style, b.batch_no, b.status,
		       count(DISTINCT f.bar_id),
		       count(*) FILTER (WHERE f.taste_off),
		       count(*) FILTER (WHERE f.pressure IN ('气压过高','气压过低','波动')),
		       count(*) FILTER (WHERE f.pour_speed IN ('偏慢','过快','不出酒')),
		       count(*) FILTER (WHERE f.complaint IS NOT NULL AND f.complaint<>'')
		FROM batches b
		JOIN beers be ON be.id=b.beer_id
		JOIN keg_cycles c ON c.batch_id=b.id
		JOIN tap_feedback f ON f.cycle_id=c.id
		GROUP BY b.id,be.name,be.style,b.batch_no,b.status
		HAVING count(*) > 0
		ORDER BY count(DISTINCT f.bar_id) DESC, count(*) FILTER (WHERE f.taste_off) DESC`)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	batchAlerts := scanRows(rows, func(rs rowScanner) (M, error) {
		var id, bars, taste, pressure, speed, complaints int64
		var beer, style, batchNo, status string
		if err := rs.Scan(&id, &beer, &style, &batchNo, &status,
			&bars, &taste, &pressure, &speed, &complaints); err != nil {
			return nil, err
		}
		suggestion := "持续观察"
		if bars >= 2 && taste >= 2 {
			suggestion = "多家酒吧口味异常，建议立即暂停销售并召回复检"
		} else if taste >= 1 {
			suggestion = "单店口味反馈，建议留样复检并关注同批次其他酒吧"
		} else if pressure+speed > 0 {
			suggestion = "设备/气压类反馈为主，建议上门检查酒头设备"
		}
		return M{"batch_id": id, "beer": beer, "style": style, "batch_no": batchNo,
			"status": status, "bars": bars, "taste": taste, "pressure_issues": pressure,
			"speed_issues": speed, "complaints": complaints,
			"suggestion": suggestion, "suggest_suspend": bars >= 2 && taste >= 2}, nil
	})

	// 异常桶：清洗不合格 / 丢失 / 变形 / 漏扫
	abnormal := func(q string) []M {
		rs, err := db.Query(q)
		if err != nil {
			return []M{}
		}
		return scanRows(rs, func(x rowScanner) (M, error) {
			var cid, kegID int64
			var code, bar, reason string
			if err := x.Scan(&cid, &kegID, &code, &bar, &reason); err != nil {
				return nil, err
			}
			return M{"cycle_id": cid, "keg_id": kegID, "keg_code": code, "bar": bar, "reason": reason}, nil
		})
	}
	washFails := abnormal(`SELECT DISTINCT ON (c.id) c.id,k.id,k.keg_code,COALESCE(b.name,'-'),
		COALESCE(w.defect_note,'清洗不合格')
		FROM keg_cycles c JOIN kegs k ON k.id=c.keg_id
		LEFT JOIN bars b ON b.id=c.bar_id
		JOIN wash_checks w ON w.cycle_id=c.id
		WHERE NOT w.passed AND c.stage='FAILED'
		ORDER BY c.id, w.id`)
	lost := abnormal(`SELECT c.id,k.id,k.keg_code,COALESCE(b.name,'-'),'酒桶丢失'
		FROM keg_cycles c JOIN kegs k ON k.id=c.keg_id
		LEFT JOIN bars b ON b.id=c.bar_id WHERE c.lost`)
	deformed := abnormal(`SELECT c.id,k.id,k.keg_code,COALESCE(b.name,'-'),'桶体变形'
		FROM keg_cycles c JOIN kegs k ON k.id=c.keg_id
		LEFT JOIN bars b ON b.id=c.bar_id WHERE c.deformed AND NOT c.lost`)
	missing := abnormal(`SELECT c.id,k.id,k.keg_code,COALESCE(b.name,'-'),'司机漏扫，桶去向待核查'
		FROM keg_cycles c JOIN kegs k ON k.id=c.keg_id
		LEFT JOIN bars b ON b.id=c.bar_id WHERE c.missing_scan`)

	// 最近反馈流
	frows, _ := db.Query(`
		SELECT f.id, be.style, ba.batch_no, b.name, COALESCE(f.pour_speed,''),
		       COALESCE(f.pressure,''), f.taste_off, COALESCE(f.taste_note,''),
		       COALESCE(f.complaint,''), f.created_at
		FROM tap_feedback f
		JOIN keg_cycles c ON c.id=f.cycle_id
		JOIN batches ba ON ba.id=c.batch_id
		JOIN beers be ON be.id=ba.beer_id
		JOIN bars b ON b.id=f.bar_id
		ORDER BY f.id DESC LIMIT 30`)
	feedbacks := scanRows(frows, func(rs rowScanner) (M, error) {
		var id int64
		var style, batchNo, bar, speed, pressure, taste, complaint string
		var off bool
		var at sql.NullTime
		if err := rs.Scan(&id, &style, &batchNo, &bar, &speed, &pressure,
			&off, &taste, &complaint, &at); err != nil {
			return nil, err
		}
		return M{"id": id, "style": style, "batch_no": batchNo, "bar": bar,
			"pour_speed": speed, "pressure": pressure, "taste_off": off,
			"taste_note": taste, "complaint": complaint, "at": tsStr(at)}, nil
	})

	writeOK(w, M{
		"batch_alerts": batchAlerts,
		"wash_fails":   washFails,
		"lost":         lost,
		"deformed":     deformed,
		"missing_scan": missing,
		"feedbacks":    feedbacks,
	})
}
