package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"tellurion-broker/internal/gateway"
)

func newTestServer(t *testing.T) (*httptest.Server, *gateway.Mock) {
	t.Helper()
	mock := gateway.NewMock(gateway.MockOptions{
		AccountID: "ACC-1",
		Cash:      5000,
		Prices:    map[string]float64{"600000": 10},
		Store:     gateway.NewMemoryAckStore(),
	})
	h := New(Config{Gateway: mock, Settlement: mock, KillSwitch: mock, WriteToken: "secret"}).Handler()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, mock
}

func doJSON(t *testing.T, srv *httptest.Server, method, path, body, token string) (int, []byte) {
	t.Helper()
	var r *http.Request
	var err error
	if body == "" {
		r, err = http.NewRequest(method, srv.URL+path, nil)
	} else {
		r, err = http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, buf.Bytes()
}

func assertKeys(t *testing.T, raw []byte, want ...string) {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode object: %v (%s)", err, raw)
	}
	got := make([]string, 0, len(obj))
	for k := range obj {
		got = append(got, k)
	}
	assertStringSet(t, got, want)
}

func assertFirstElementKeys(t *testing.T, raw []byte, want ...string) {
	t.Helper()
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("decode array: %v (%s)", err, raw)
	}
	if len(arr) == 0 {
		t.Fatalf("array is empty, want at least one element: %s", raw)
	}
	assertKeys(t, arr[0], want...)
}

func assertStringSet(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

// 三条窗口期义务之一（G1）：序列化后的键集合被用例钉住，收编契约时
// 「改前 / 改后逐字节一致」才有可比对的基准。
func TestOrderAndAccountPayloadShapes(t *testing.T) {
	srv, _ := newTestServer(t)

	status, body := doJSON(t, srv, http.MethodPost, "/api/broker/orders",
		`{"client_order_id":"c-1","account_id":"ACC-1","symbol":"600000","side":"buy","order_type":"market","quantity":100}`,
		"secret")
	if status != http.StatusOK {
		t.Fatalf("place status = %d, body = %s", status, body)
	}
	assertKeys(t, body,
		"client_order_id", "broker_order_id", "status", "idempotent_replay", "accepted_at")

	status, body = doJSON(t, srv, http.MethodGet, "/api/broker/orders/c-1", "", "")
	if status != http.StatusOK {
		t.Fatalf("query status = %d, body = %s", status, body)
	}
	assertKeys(t, body,
		"client_order_id", "broker_order_id", "status", "filled_quantity",
		"avg_fill_price", "remaining_quantity", "last_update")

	status, body = doJSON(t, srv, http.MethodGet, "/api/broker/accounts/ACC-1/positions", "", "")
	if status != http.StatusOK {
		t.Fatalf("positions status = %d, body = %s", status, body)
	}
	assertFirstElementKeys(t, body,
		"account_id", "symbol", "quantity", "available_quantity", "avg_cost", "market_value", "as_of")

	status, body = doJSON(t, srv, http.MethodGet, "/api/broker/accounts/ACC-1/account", "", "")
	if status != http.StatusOK {
		t.Fatalf("account status = %d, body = %s", status, body)
	}
	assertKeys(t, body, "account_id", "as_of", "cash", "total_assets", "positions", "fees")

	status, body = doJSON(t, srv, http.MethodGet, "/api/broker/accounts/ACC-1/fills?since=2026-01-01T00:00:00Z", "", "")
	if status != http.StatusOK {
		t.Fatalf("fills status = %d, body = %s", status, body)
	}
	assertFirstElementKeys(t, body,
		"fill_id", "client_order_id", "broker_order_id", "symbol", "side", "quantity",
		"price", "commission", "stamp_tax", "transfer_fee", "filled_at")

	asOf := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC).Format(time.RFC3339)
	status, body = doJSON(t, srv, http.MethodGet, "/api/broker/accounts/ACC-1/settlement?as_of="+asOf, "", "")
	if status != http.StatusOK {
		t.Fatalf("settlement status = %d, body = %s", status, body)
	}
	assertKeys(t, body, "account_id", "as_of", "cash", "total_assets", "positions", "fees")

	status, body = doJSON(t, srv, http.MethodPost, "/api/broker/emergency-flatten", `{"reason":"test"}`, "secret")
	if status != http.StatusOK {
		t.Fatalf("flatten status = %d, body = %s", status, body)
	}
	assertKeys(t, body, "sold", "skipped", "sold_total", "started_at", "completed_at", "reason")
}

func TestLimitOrderCanBeCancelledOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t)

	status, body := doJSON(t, srv, http.MethodPost, "/api/broker/orders",
		`{"client_order_id":"c-limit","account_id":"ACC-1","symbol":"600000","side":"buy","order_type":"limit","quantity":100,"limit_price":9.5}`,
		"secret")
	if status != http.StatusOK {
		t.Fatalf("place status = %d, body = %s", status, body)
	}

	status, body = doJSON(t, srv, http.MethodDelete, "/api/broker/orders/c-limit", "", "secret")
	if status != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", status, body)
	}
	assertKeys(t, body,
		"client_order_id", "broker_order_id", "status", "filled_quantity",
		"avg_fill_price", "remaining_quantity", "last_update")
}

func TestWriteEndpointsRequireServiceToken(t *testing.T) {
	mock := gateway.NewMock(gateway.MockOptions{AccountID: "ACC-1", Store: gateway.NewMemoryAckStore()})

	// 未配置 token = 写侧关闭（fail-closed），不是"不校验"。
	noToken := httptest.NewServer(New(Config{Gateway: mock, Settlement: mock, KillSwitch: mock}).Handler())
	defer noToken.Close()
	status, body := doJSON(t, noToken, http.MethodPost, "/api/broker/orders", `{}`, "")
	if status != http.StatusServiceUnavailable || !strings.Contains(string(body), "writes_disabled") {
		t.Fatalf("disabled writes = %d %s, want 503 writes_disabled", status, body)
	}

	withToken, _ := newTestServer(t)
	status, body = doJSON(t, withToken, http.MethodPost, "/api/broker/orders", `{}`, "")
	if status != http.StatusUnauthorized || !strings.Contains(string(body), "unauthorized") {
		t.Fatalf("missing token = %d %s, want 401 unauthorized", status, body)
	}
	status, body = doJSON(t, withToken, http.MethodPost, "/api/broker/orders", `{}`, "wrong")
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %s, want 401", status, body)
	}
	// 读侧不要求 token。
	if status, _ := doJSON(t, withToken, http.MethodGet, "/api/broker/orders/absent", "", ""); status != http.StatusNotFound {
		t.Fatalf("read status = %d, want 404 order_not_found", status)
	}
}

// stubGateway 用来逐条复现 12 个哨兵 → (HTTP status, wire code) 的映射。
type stubGateway struct{ err error }

func (s stubGateway) PlaceOrder(context.Context, gateway.OrderRequest) (*gateway.OrderAck, error) {
	if errors.Is(s.err, gateway.ErrOrderRejected) || errors.Is(s.err, gateway.ErrInsufficientBuyingPower) {
		return &gateway.OrderAck{Status: gateway.OrderRejected, Code: "stub"}, s.err
	}
	return nil, s.err
}
func (s stubGateway) CancelOrder(context.Context, gateway.CancelRequest) (*gateway.OrderState, error) {
	return nil, s.err
}
func (s stubGateway) QueryOrder(context.Context, string) (*gateway.OrderState, error) {
	return nil, s.err
}
func (s stubGateway) QueryPositions(context.Context, string) ([]gateway.Position, error) {
	return nil, s.err
}
func (s stubGateway) QueryAccount(context.Context, string) (*gateway.AccountSnapshot, error) {
	return nil, s.err
}
func (s stubGateway) QueryFills(context.Context, string, time.Time) ([]gateway.Fill, error) {
	return nil, s.err
}

type stubSettlement struct{ err error }

func (s stubSettlement) QuerySettlement(context.Context, string, time.Time) (*gateway.AccountSnapshot, error) {
	return nil, s.err
}

type stubKillSwitch struct{ err error }

func (s stubKillSwitch) EmergencyFlatten(context.Context, string) (*gateway.EmergencyFlattenResult, error) {
	return nil, s.err
}

func TestAllTwelveSentinelCodesMapToWireCodes(t *testing.T) {
	cases := []struct {
		sentinel error
		status   int
		code     string
	}{
		{gateway.ErrOrderRejected, http.StatusConflict, "order_rejected"},
		{gateway.ErrUnknownState, http.StatusConflict, "unknown_state"},
		{gateway.ErrChannelDown, http.StatusServiceUnavailable, "channel_down"},
		{gateway.ErrDuplicateOrder, http.StatusConflict, "duplicate_order"},
		{gateway.ErrOrderNotFound, http.StatusNotFound, "order_not_found"},
		{gateway.ErrOrderNotCancelable, http.StatusConflict, "order_not_cancelable"},
		{gateway.ErrInvalidOrder, http.StatusBadRequest, "invalid_order"},
		{gateway.ErrRateLimited, http.StatusTooManyRequests, "rate_limited"},
		{gateway.ErrNotSupported, http.StatusNotImplemented, "not_supported"},
		{gateway.ErrAccountMismatch, http.StatusForbidden, "account_mismatch"},
		{gateway.ErrInsufficientBuyingPower, http.StatusConflict, "insufficient_buying_power"},
		{gateway.ErrIdempotencyStoreUnavailable, http.StatusServiceUnavailable, "idempotency_store_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			wrapped := errors.Join(errors.New("stub"), tc.sentinel)
			gw := stubGateway{err: wrapped}
			h := New(Config{
				Gateway:    gw,
				Settlement: stubSettlement{err: wrapped},
				KillSwitch: stubKillSwitch{err: wrapped},
				WriteToken: "secret",
			}).Handler()
			srv := httptest.NewServer(h)
			defer srv.Close()

			status, body := doJSON(t, srv, http.MethodPost, "/api/broker/orders", `{}`, "secret")
			if status != tc.status {
				t.Fatalf("status = %d, want %d (%s)", status, tc.status, body)
			}
			var got errorBody
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode: %v (%s)", err, body)
			}
			if got.Code != tc.code {
				t.Fatalf("code = %q, want %q", got.Code, tc.code)
			}
		})
	}
}

// 429 是本仓唯一的限流出口（rate_limited）：必须带可解析的 Retry-After，
// 且值等于本仓声明的退避单位（不是随手写的常数）。
func TestRateLimitedCarriesRetryAfter(t *testing.T) {
	wrapped := errors.Join(errors.New("stub"), gateway.ErrRateLimited)
	h := New(Config{
		Gateway:    stubGateway{err: wrapped},
		Settlement: stubSettlement{err: wrapped},
		KillSwitch: stubKillSwitch{err: wrapped},
		WriteToken: "secret",
	}).Handler()
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp := doResp(t, srv, http.MethodPost, "/api/broker/orders", `{}`, "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	raw := resp.Header.Get("Retry-After")
	if raw == "" {
		t.Fatal("Retry-After 缺失：429 rate_limited 必须告诉调用方何时重试")
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("Retry-After = %q, 必须是整数秒: %v", raw, err)
	}
	if want := int(rateLimitedRetryAfter / time.Second); secs != want {
		t.Fatalf("Retry-After = %d, want %d (rateLimitedRetryAfter)", secs, want)
	}
}

// 成功响应不得带 Retry-After（否则会把调用方引到无谓的退避）。
func TestSuccessResponseHasNoRetryAfter(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := doResp(t, srv, http.MethodPost, "/api/broker/orders",
		`{"client_order_id":"c-ra-ok","account_id":"ACC-1","symbol":"600000","side":"buy","order_type":"market","quantity":100}`,
		"secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Fatalf("成功响应带了 Retry-After = %q，want empty", got)
	}
}

func doResp(t *testing.T, srv *httptest.Server, method, path, body, token string) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = bytes.NewBufferString(body)
	}
	r, err := http.NewRequest(method, srv.URL+path, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}
