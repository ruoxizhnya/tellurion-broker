// Command brokergateway 是 tellurion-broker 的最小服务入口（ADR-036 G0）。
//
// 定位：交易网关服务。G0 只暴露健康/版本面，证明本仓可独立构建、启动、被探活；
// 订单/撤单/查单/持仓/对账的 HTTP 面归 SVC-BROKER-G1，届时复用 internal/gateway
// 的 BrokerGateway / SettlementQuerier / KillSwitch / HealthChecker 四个 port。
//
// 本进程不接真实柜台：XTP 全量适配器仍受 X6c 阻塞；Mock 只做转发与确认，
// 不做风控裁决，不持有订单真相。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tellurion-broker/internal/ackstore"
	"tellurion-broker/internal/gateway"
	"tellurion-broker/internal/httpapi"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8091", "listen address")
	accountID := flag.String("account-id", "mock", "account id served by the mock channel")
	cash := flag.Float64("cash", 100_000_000, "initial mock cash")
	mockPrices := flag.String("mock-prices", "", "mock reference prices, e.g. 000001=12.5,600000=8.2 (empty = market orders stay accepted)")
	flag.Parse()

	prices, err := parseMockPrices(*mockPrices)
	if err != nil {
		log.Fatalf("brokergateway: mock prices: %v", err)
	}

	// 幂等映射：默认内存实现（不接真钱，XTP 装配闸要求 Durable=true）；
	// 给了 BROKER_DATABASE_DSN 就用持久实现，并启动保留期清理（G2）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var store gateway.AckStore = gateway.NewMemoryAckStore()
	if dsn := os.Getenv("BROKER_DATABASE_DSN"); dsn != "" {
		pool, err := connectAndMigrate(ctx, dsn)
		if err != nil {
			log.Fatalf("brokergateway: idempotency store: %v", err)
		}
		defer pool.Close()
		store = ackstore.NewPgStore(pool)
		go runRetentionCleanup(ctx, pool)
		log.Printf("brokergateway: idempotency store = postgres, acked retention = %v", ackstore.DefaultRetention)
	}

	mock := gateway.NewMock(gateway.MockOptions{
		AccountID: *accountID,
		Cash:      *cash,
		Store:     store,
		Prices:    prices,
	})

	const (
		healthzPath = "/healthz"
		versionPath = "/version"
	)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+healthzPath, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := mock.HealthCheck(ctx); err != nil {
			http.Error(w, `{"status":"down","reason":"channel_unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET "+versionPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"service": "brokergateway",
			"version": Version,
			"commit":  Commit,
			"built":   BuildTime,
		})
	})

	// 版本化订单 HTTP 面（G1）：写侧要求 BROKER_SERVICE_TOKEN，未设即关闭写面。
	writeToken := os.Getenv("BROKER_SERVICE_TOKEN")
	api := httpapi.New(httpapi.Config{
		Gateway:    mock,
		Settlement: mock,
		KillSwitch: mock,
		WriteToken: writeToken,
	})
	mux.Handle("/api/broker/", api.Handler())
	if writeToken == "" {
		log.Printf("brokergateway: BROKER_SERVICE_TOKEN not set; write endpoints are disabled")
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("brokergateway: listening on %s (mock account=%s, order API = /api/broker)", *addr, *accountID)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("brokergateway: listen: %v", err)
	}
}

// parseMockPrices 解析 `-mock-prices` 的 `SYM=PX,SYM=PX` 形式。
//
// 为什么需要：Mock 的市价撮合价来自 `MockOptions.Prices`，没有价格时市价单
// 按既定语义只停在 accepted（不伪造成交）。真进程对拍（SVC-BROKER-G3 步骤 2）
// 必须能把参考价喂进服务，否则对拍只能跑出「受理 ≠ 成交」的形态。
func parseMockPrices(raw string) (map[string]float64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	out := make(map[string]float64)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		kv := strings.SplitN(item, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("want SYM=PX, got %q", item)
		}
		sym := strings.TrimSpace(kv[0])
		if sym == "" {
			return nil, fmt.Errorf("empty symbol in %q", item)
		}
		px, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
		if err != nil || px <= 0 {
			return nil, fmt.Errorf("invalid price in %q", item)
		}
		out[sym] = px
	}
	return out, nil
}

// connectAndMigrate 建池并执行本仓唯一的 DDL（幂等、无版本表）。
func connectAndMigrate(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(connectCtx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, err
	}
	migrateCtx, cancelMigrate := context.WithTimeout(ctx, 30*time.Second)
	defer cancelMigrate()
	if err := ackstore.Migrate(migrateCtx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// cleanupInterval 是进程内保留期清理的周期（裁决 ⑨：只删已回执行，单批 ≤ 5000）。
const cleanupInterval = 24 * time.Hour

// runRetentionCleanup 是本仓选定的清理作业形态：**进程内周期任务**。
//
// 理由：本仓是单副本服务、PG 连接池已在手、不需要额外调度框架与多副本互斥；
// 首次执行在启动后一个周期（不在启动时扫表，避免拖慢就绪）。若将来多副本，
// 需先补分布式互斥再复制本作业——删除是幂等的，但并发跑会白做重复扫描。
func runRetentionCleanup(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := ackstore.CleanupAcked(ctx, pool, ackstore.DefaultRetention)
			if err != nil {
				log.Printf("brokergateway: retention cleanup failed: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("brokergateway: retention cleanup deleted %d acked rows", n)
			}
		}
	}
}
