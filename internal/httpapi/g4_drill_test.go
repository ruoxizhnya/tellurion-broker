package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tellurion-broker/internal/gateway"
)

// togglableStore 用来演练「幂等存储不可用 → 恢复」：故障开关只影响存储，
// 通道本身仍 connected，因此能证明网关报的是存储不可用而不是 channel_down。
type togglableStore struct {
	mu    sync.Mutex
	inner *gateway.MemoryAckStore
	fail  bool
}

func newTogglableStore() *togglableStore {
	return &togglableStore{inner: gateway.NewMemoryAckStore()}
}

func (s *togglableStore) setFail(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = fail
}

func (s *togglableStore) failing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fail
}

func (s *togglableStore) Load(ctx context.Context, accountID, clientOrderID string) (gateway.IdempotencyRecord, bool, error) {
	if s.failing() {
		return gateway.IdempotencyRecord{}, false, errors.New("pg: connection refused")
	}
	return s.inner.Load(ctx, accountID, clientOrderID)
}

func (s *togglableStore) Reserve(ctx context.Context, accountID, clientOrderID, fingerprint string) (gateway.IdempotencyRecord, bool, error) {
	if s.failing() {
		return gateway.IdempotencyRecord{}, false, errors.New("pg: connection refused")
	}
	return s.inner.Reserve(ctx, accountID, clientOrderID, fingerprint)
}

func (s *togglableStore) SaveAck(ctx context.Context, accountID, clientOrderID string, rec gateway.IdempotencyRecord) error {
	if s.failing() {
		return errors.New("pg: connection refused")
	}
	return s.inner.SaveAck(ctx, accountID, clientOrderID, rec)
}

func (s *togglableStore) Durable() bool { return s.inner.Durable() }

// G4 演练（内存侧）：幂等存储不可用 ⇒ 网关不 fake success，错误是
// idempotency_store_unavailable；存储恢复后同一个 client_order_id 可以继续。
// 真 PG 腿（BROKER_TEST_DSN + 停库）本机未执行，runbook 如实登记。
func TestDrillIdempotencyStoreOutageDoesNotFakeSuccess(t *testing.T) {
	store := newTogglableStore()
	mock := gateway.NewMock(gateway.MockOptions{
		AccountID: "ACC-1",
		Cash:      5000,
		Prices:    map[string]float64{"600000": 10},
		Store:     store,
	})
	srv := httptest.NewServer(New(Config{
		Gateway:    mock,
		Settlement: mock,
		KillSwitch: mock,
		WriteToken: "secret",
	}).Handler())
	defer srv.Close()

	const orderBody = `{"client_order_id":"g4-drill-1","account_id":"ACC-1","symbol":"600000","side":"buy","order_type":"market","quantity":100}`

	store.setFail(true)
	status, body := doJSON(t, srv, http.MethodPost, "/api/broker/orders", orderBody, "secret")
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode error envelope: %v (%s)", err, body)
	}
	t.Logf("outage: status=%d code=%s message=%q", status, envelope.Code, envelope.Message)
	if status != http.StatusServiceUnavailable || envelope.Code != "idempotency_store_unavailable" {
		t.Fatalf("outage = %d %s, want 503 idempotency_store_unavailable", status, body)
	}
	if strings.Contains(string(body), "broker_order_id") || strings.Contains(string(body), `"status":"accepted"`) {
		t.Fatalf("outage must not return a success shape: %s", body)
	}
	fills, err := mock.QueryFills(context.Background(), "ACC-1", time.Time{})
	if err != nil || len(fills) != 0 {
		t.Fatalf("outage must not send: fills=%d err=%v", len(fills), err)
	}
	if _, err := mock.QueryOrder(context.Background(), "g4-drill-1"); !errors.Is(err, gateway.ErrIdempotencyStoreUnavailable) {
		t.Fatalf("query during outage err = %v, want ErrIdempotencyStoreUnavailable", err)
	}

	store.setFail(false)
	status, body = doJSON(t, srv, http.MethodPost, "/api/broker/orders", orderBody, "secret")
	t.Logf("after recovery: status=%d body=%s", status, body)
	if status != http.StatusOK {
		t.Fatalf("after recovery status = %d, body = %s", status, body)
	}
	var ack gateway.OrderAck
	if err := json.Unmarshal(body, &ack); err != nil {
		t.Fatalf("decode ack: %v (%s)", err, body)
	}
	if ack.ClientOrderID != "g4-drill-1" || ack.Status != gateway.OrderFilled || ack.IdempotentReplay {
		t.Fatalf("after recovery ack = %+v, want a fresh filled order", ack)
	}
}
