// Package httpapi 是 tellurion-broker 的版本化 HTTP 面（ADR-036 G1）。
//
// 它只做**报文与错误码翻译**：幂等、未知状态语义、申报规则全在
// internal/gateway 的 port 实现里；本包不裁决、不改单、不持有状态。
//
// 写侧（下单 / 撤单 / 紧急清仓）要求 service token；读侧（查单 / 持仓 /
// 账户 / 成交 / 对账）是本机服务面，不做鉴权（G4 已收口：就绪只看装配闸，
// 见 docs/api-schema.md §1.6）。
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tellurion-broker/internal/gateway"
)

// rateLimitedRetryAfter 是 429 `rate_limited` 的 `Retry-After` 值。
//
// 本仓**不持有柜台限流窗口**：`ErrRateLimited` 由通道/柜台产生（见
// docs/api-schema.md §2），重试窗口在柜台侧，本仓拿不到。因此这里声明的是
// 本仓的**最小退避单位**，与 risk 等兄弟仓的 429 同口径（1s）——而不是一个
// 与任何窗口都对不上的魔数。若将来通道能把具体窗口带出来，应由错误携带
// 窗口并在此换算，**不得**再写第二个常数。
const rateLimitedRetryAfter = 1 * time.Second

// Config 是 HTTP 面的装配参数。四个依赖都必须是已装配的 port 实现。
type Config struct {
	Gateway    gateway.BrokerGateway
	Settlement gateway.SettlementQuerier
	KillSwitch gateway.KillSwitch
	// WriteToken 是写侧 service token（`Authorization: Bearer <token>`）。
	// **空串 = 写侧关闭**（fail-closed，不是"不校验"）。
	WriteToken string
	// Now 返回当前时间（默认 time.Now），用于解析可选时间参数。
	Now func() time.Time
}

// Server 持有四个 port 的引用；方法集是冻结的 HTTP 形状，不含业务逻辑。
type Server struct {
	cfg Config
	now func() time.Time
}

// New 构造 HTTP 面。依赖缺失是装配错误，直接 panic（fail-closed 优于半装配）。
func New(cfg Config) *Server {
	switch {
	case cfg.Gateway == nil:
		panic("httpapi: Gateway is required")
	case cfg.Settlement == nil:
		panic("httpapi: Settlement is required")
	case cfg.KillSwitch == nil:
		panic("httpapi: KillSwitch is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Server{cfg: cfg, now: now}
}

// Handler 返回挂载在 `/api/broker/` 下的路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/broker/orders", s.requireToken(s.handlePlaceOrder))
	mux.HandleFunc("GET /api/broker/orders/{clientOrderID}", s.handleQueryOrder)
	mux.HandleFunc("DELETE /api/broker/orders/{clientOrderID}", s.requireToken(s.handleCancelOrder))
	mux.HandleFunc("GET /api/broker/accounts/{accountID}/positions", s.handlePositions)
	mux.HandleFunc("GET /api/broker/accounts/{accountID}/account", s.handleAccount)
	mux.HandleFunc("GET /api/broker/accounts/{accountID}/fills", s.handleFills)
	mux.HandleFunc("GET /api/broker/accounts/{accountID}/settlement", s.handleSettlement)
	mux.HandleFunc("POST /api/broker/emergency-flatten", s.requireToken(s.handleEmergencyFlatten))
	return mux
}

// requireToken 是写侧鉴权：未配置 token 即关闭写面，配置了则必须完全匹配。
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.WriteToken == "" {
			writeError(w, http.StatusServiceUnavailable, "writes_disabled",
				"BROKER_SERVICE_TOKEN is not set; write endpoints are disabled")
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.WriteToken)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid service token")
			return
		}
		next(w, r)
	}
}

func (s *Server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	var req gateway.OrderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ack, err := s.cfg.Gateway.PlaceOrder(r.Context(), req)
	if err != nil {
		writeGatewayError(w, err, ack)
		return
	}
	writeJSON(w, http.StatusOK, ack)
}

func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	state, err := s.cfg.Gateway.CancelOrder(r.Context(), gateway.CancelRequest{
		ClientOrderID: r.PathValue("clientOrderID"),
	})
	if err != nil {
		writeGatewayError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleQueryOrder(w http.ResponseWriter, r *http.Request) {
	state, err := s.cfg.Gateway.QueryOrder(r.Context(), r.PathValue("clientOrderID"))
	if err != nil {
		writeGatewayError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handlePositions(w http.ResponseWriter, r *http.Request) {
	positions, err := s.cfg.Gateway.QueryPositions(r.Context(), r.PathValue("accountID"))
	if err != nil {
		writeGatewayError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, positions)
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.cfg.Gateway.QueryAccount(r.Context(), r.PathValue("accountID"))
	if err != nil {
		writeGatewayError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleFills(w http.ResponseWriter, r *http.Request) {
	since, err := parseOptionalTime(r.URL.Query().Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "since: "+err.Error())
		return
	}
	fills, err := s.cfg.Gateway.QueryFills(r.Context(), r.PathValue("accountID"), since)
	if err != nil {
		writeGatewayError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, fills)
}

func (s *Server) handleSettlement(w http.ResponseWriter, r *http.Request) {
	asOf, err := parseOptionalTime(r.URL.Query().Get("as_of"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "as_of: "+err.Error())
		return
	}
	snapshot, err := s.cfg.Settlement.QuerySettlement(r.Context(), r.PathValue("accountID"), asOf)
	if err != nil {
		writeGatewayError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleEmergencyFlatten(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "reason is required for emergency flatten")
		return
	}
	result, err := s.cfg.KillSwitch.EmergencyFlatten(r.Context(), body.Reason)
	if err != nil {
		writeGatewayError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func parseOptionalTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errorBody 是错误信封。`ack` 只在「柜台明确拒单」时出现（回执要留痕）。
type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Ack     *gateway.OrderAck `json:"ack,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Code: code, Message: message})
}

// writeGatewayError 是 port 错误的**唯一出口**：先按 wireStatus 映射，再在
// 429 上追加 `Retry-After`。放在这里而不是各 handler 里，是为了让「本仓唯一
// 的 429 = rate_limited 一定带头」成为结构性保证——新增 handler 也不会漏。
func writeGatewayError(w http.ResponseWriter, err error, ack *gateway.OrderAck) {
	status, code := wireStatus(err)
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(int(rateLimitedRetryAfter/time.Second)))
	}
	if ack != nil {
		writeOrderError(w, status, code, err, ack)
		return
	}
	writeError(w, status, code, err.Error())
}

func writeOrderError(w http.ResponseWriter, status int, code string, err error, ack *gateway.OrderAck) {
	writeJSON(w, status, errorBody{Code: code, Message: err.Error(), Ack: ack})
}

// wireStatus 是「12 个契约哨兵 → (HTTP status, wire code)」的唯一映射。
// 未识别的错误按内部错误处理（不猜成 4xx，避免把实现缺陷报成请求问题）。
func wireStatus(err error) (int, string) {
	switch {
	case errors.Is(err, gateway.ErrOrderRejected):
		return http.StatusConflict, "order_rejected"
	case errors.Is(err, gateway.ErrUnknownState):
		return http.StatusConflict, "unknown_state"
	case errors.Is(err, gateway.ErrChannelDown):
		return http.StatusServiceUnavailable, "channel_down"
	case errors.Is(err, gateway.ErrDuplicateOrder):
		return http.StatusConflict, "duplicate_order"
	case errors.Is(err, gateway.ErrOrderNotFound):
		return http.StatusNotFound, "order_not_found"
	case errors.Is(err, gateway.ErrOrderNotCancelable):
		return http.StatusConflict, "order_not_cancelable"
	case errors.Is(err, gateway.ErrInvalidOrder):
		return http.StatusBadRequest, "invalid_order"
	case errors.Is(err, gateway.ErrRateLimited):
		return http.StatusTooManyRequests, "rate_limited"
	case errors.Is(err, gateway.ErrNotSupported):
		return http.StatusNotImplemented, "not_supported"
	case errors.Is(err, gateway.ErrAccountMismatch):
		return http.StatusForbidden, "account_mismatch"
	case errors.Is(err, gateway.ErrInsufficientBuyingPower):
		return http.StatusConflict, "insufficient_buying_power"
	case errors.Is(err, gateway.ErrIdempotencyStoreUnavailable):
		return http.StatusServiceUnavailable, "idempotency_store_unavailable"
	}
	return http.StatusInternalServerError, "internal_error"
}
