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
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tellurion-broker/internal/gateway"
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
	flag.Parse()

	// G0 先装配 Mock；AckStore 用内存实现（不接真钱，XTP 装配闸要求 Durable=true）。
	mock := gateway.NewMock(gateway.MockOptions{
		AccountID: *accountID,
		Cash:      *cash,
		Store:     gateway.NewMemoryAckStore(),
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

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("brokergateway: listening on %s (mock account=%s, order HTTP API = G1)", *addr, *accountID)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("brokergateway: listen: %v", err)
	}
}
