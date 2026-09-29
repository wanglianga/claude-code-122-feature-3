package main

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

//go:embed db/schema.sql db/seed.sql
var sqlFS embed.FS

//go:embed web/dist
var webFS embed.FS

var db *sql.DB

func main() {
	dsn := os.Getenv("KEG_DSN")
	if dsn == "" {
		dsn = "host=/tmp port=5433 user=node dbname=kegplatform sslmode=disable"
	}
	addr := os.Getenv("KEG_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("连接数据库失败: %v\nDSN=%s", err, dsn)
	}

	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := initDB(); err != nil {
			log.Fatal(err)
		}
		log.Println("数据库初始化完成（schema + 种子数据）")
		return
	}

	mux := http.NewServeMux()
	registerRoutes(mux)

	srv := &http.Server{Addr: addr, Handler: withCORS(mux), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("精酿桶追溯平台启动: http://localhost%s", addr)
	log.Fatal(srv.ListenAndServe())
}

func initDB() error {
	// 已初始化过的库直接跳过（schema 全部 IF NOT EXISTS，种子不可重复灌入）
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema='public'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		log.Printf("数据库已存在 %d 张表，跳过 schema/种子初始化", n)
		return nil
	}
	for _, name := range []string{"db/schema.sql", "db/seed.sql"} {
		b, err := sqlFS.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := db.Exec(string(b)); err != nil {
			return err
		}
	}
	return nil
}

// h 包装：需要登录的接口
func h(handler http.HandlerFunc) http.HandlerFunc {
	return auth(handler)
}

// hr 包装：限定角色
func hr(roles string, handler http.HandlerFunc) http.HandlerFunc {
	return auth(requireRole(roles, handler))
}

func registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/login", handleLogin)

	// 基础数据
	mux.HandleFunc("/api/options", h(handleOptions))
	mux.HandleFunc("/api/overview", h(handleOverview))

	// 酒厂：桶资产 / 批次 / 订单
	mux.HandleFunc("/api/kegs", h(handleListKegs))
	mux.HandleFunc("/api/kegs/register", hr("brewery admin", handleRegisterKegs))
	mux.HandleFunc("/api/kegs/trace/", h(handleKegTrace))
	mux.HandleFunc("/api/batches", routeBatches)
	mux.HandleFunc("/api/batches/", h(handleBatchSubroute))
	mux.HandleFunc("/api/orders", h(handleOrders))
	mux.HandleFunc("/api/orders/dispatch/", hr("brewery admin", handleDispatchOrder))

	// 酒吧
	mux.HandleFunc("/api/bar/cycles", hr("bar_manager admin", handleBarCycles))
	mux.HandleFunc("/api/cycles/feedback/", hr("bar_manager admin", handleTapFeedback))
	mux.HandleFunc("/api/cycles/empty/", hr("bar_manager admin", handleMarkEmpty))
	mux.HandleFunc("/api/cycles/reserve", hr("bar_manager admin", handleReserveReturn))
	mux.HandleFunc("/api/cycles/lost/", hr("brewery finance admin", handleMarkLost))

	// 司机
	mux.HandleFunc("/api/driver/tasks", hr("driver admin", handleDriverTasks))
	mux.HandleFunc("/api/pickups/scan", hr("driver admin", handlePickupScan))
	mux.HandleFunc("/api/driver/audit", hr("driver brewery admin", handleDriverAudit))

	// 清洗厂
	mux.HandleFunc("/api/wash/pending", hr("washplant brewery admin", handleWashPending))
	mux.HandleFunc("/api/wash/checks", hr("washplant admin", handleWashCheck))

	// 清洗不合格返修
	mux.HandleFunc("/api/rework/pending", hr("washplant brewery admin qc", handleReworkPending))
	mux.HandleFunc("/api/rework/orders", hr("washplant brewery admin qc finance bar_manager", handleReworkOrders))
	mux.HandleFunc("/api/rework/orders/", h(handleReworkDetail))
	mux.HandleFunc("/api/rework/reinspect/", hr("washplant admin", handleReworkReinspect))
	mux.HandleFunc("/api/rework/confirm/", hr("brewery admin", handleReworkConfirm))
	mux.HandleFunc("/api/rework/photo/", handleReworkPhoto)

	// 品控
	mux.HandleFunc("/api/qc/overview", hr("qc brewery admin", handleQCOverview))

	// 财务
	mux.HandleFunc("/api/finance/deposits", hr("finance brewery admin", handleDeposits))
	mux.HandleFunc("/api/finance/settle/", hr("finance admin", handleSettleDeposit))
	mux.HandleFunc("/api/finance/bills", hr("finance brewery admin washplant bar_manager", handleBillingEntries))
	mux.HandleFunc("/api/finance/bills/confirm/", hr("finance admin", handleConfirmBilling))

	// 通知
	mux.HandleFunc("/api/notifications", h(handleNotifications))
	mux.HandleFunc("/api/notifications/ack/", h(handleAckNotification))

	// 前端静态资源（SPA）
	dist, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(dist))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, 404, M{"error": "接口不存在"})
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := dist.Open(p); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
