package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type mockClock struct{ at time.Time }

func (c *mockClock) Now() time.Time          { return c.at }
func (c *mockClock) Advance(d time.Duration) { c.at = c.at.Add(d) }

func newClock() *mockClock {
	return &mockClock{at: time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)}
}

// flakyStore 包装 MemoryAckStore，用来注入「映射不可用」与「回执落盘失败」。
type flakyStore struct {
	mu        sync.Mutex
	inner     *MemoryAckStore
	loadErr   error
	failSave  func(n int) bool
	saveCount int
}

func newFlakyStore() *flakyStore { return &flakyStore{inner: NewMemoryAckStore()} }

func (s *flakyStore) Load(ctx context.Context, accountID, clientOrderID string) (IdempotencyRecord, bool, error) {
	s.mu.Lock()
	err := s.loadErr
	s.mu.Unlock()
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	return s.inner.Load(ctx, accountID, clientOrderID)
}

func (s *flakyStore) Save(ctx context.Context, accountID, clientOrderID string, rec IdempotencyRecord) error {
	s.mu.Lock()
	s.saveCount++
	n := s.saveCount
	fail := s.failSave
	s.mu.Unlock()
	if fail != nil && fail(n) {
		return errors.New("store write failed")
	}
	return s.inner.Save(ctx, accountID, clientOrderID, rec)
}

func newMock(t *testing.T, opts MockOptions) (*Mock, *flakyStore, *mockClock) {
	t.Helper()
	c := newClock()
	store := newFlakyStore()
	opts.Now = c.Now
	if opts.Store == nil {
		opts.Store = store
	}
	if opts.AccountID == "" {
		opts.AccountID = "ACC-1"
	}
	if opts.Prices == nil {
		opts.Prices = map[string]float64{"600000": 10}
	}
	return NewMock(opts), store, c
}

func marketReq(id string) OrderRequest {
	return OrderRequest{
		ClientOrderID: id,
		AccountID:     "ACC-1",
		Symbol:        "600000",
		Side:          SideBuy,
		OrderType:     OrderMarket,
		Quantity:      100,
	}
}

func TestPlaceOrderFillsMarketOrder(t *testing.T) {
	m, store, c := newMock(t, MockOptions{Cash: 5000})
	ack, err := m.PlaceOrder(context.Background(), marketReq("c-1"))
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ack.Status != OrderFilled || ack.ClientOrderID != "c-1" || ack.BrokerOrderID == "" {
		t.Fatalf("unexpected ack: %+v", ack)
	}
	if ack.IdempotentReplay {
		t.Fatal("fresh order must not be marked as a replay")
	}
	if !ack.AcceptedAt.Equal(c.Now()) {
		t.Fatalf("accepted_at = %v", ack.AcceptedAt)
	}

	// 回执必须已经落盘（幂等映射里是完整 ack，不是占位）。
	rec, found, err := store.Load(context.Background(), "ACC-1", "c-1")
	if err != nil || !found {
		t.Fatalf("ack not persisted: found=%v err=%v", found, err)
	}
	if rec.Ack.Status != OrderFilled {
		t.Fatalf("persisted ack status = %q", rec.Ack.Status)
	}

	positions, err := m.QueryPositions(context.Background(), "ACC-1")
	if err != nil {
		t.Fatalf("QueryPositions: %v", err)
	}
	if len(positions) != 1 || positions[0].Quantity != 100 || positions[0].AvgCost != 10 {
		t.Fatalf("unexpected positions: %+v", positions)
	}

	fills, err := m.QueryFills(context.Background(), "ACC-1", time.Time{})
	if err != nil {
		t.Fatalf("QueryFills: %v", err)
	}
	if len(fills) != 1 || fills[0].Quantity != 100 || fills[0].Price != 10 {
		t.Fatalf("unexpected fills: %+v", fills)
	}
}

func TestPlaceOrderReplaysSamePayload(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{Cash: 5000})
	first, err := m.PlaceOrder(context.Background(), marketReq("c-1"))
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	replay, err := m.PlaceOrder(context.Background(), marketReq("c-1"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("replay must be marked IdempotentReplay")
	}
	if replay.BrokerOrderID != first.BrokerOrderID || replay.Status != first.Status {
		t.Fatalf("replay differs: %+v vs %+v", replay, first)
	}

	// 关键：没有发第二笔。
	fills, _ := m.QueryFills(context.Background(), "ACC-1", time.Time{})
	if len(fills) != 1 {
		t.Fatalf("replay sent a second order: %d fills", len(fills))
	}
}

func TestPlaceOrderRejectsDifferentPayload(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{Cash: 5000})
	if _, err := m.PlaceOrder(context.Background(), marketReq("c-1")); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	other := marketReq("c-1")
	other.Quantity = 200
	if _, err := m.PlaceOrder(context.Background(), other); !errors.Is(err, ErrDuplicateOrder) {
		t.Fatalf("err = %v, want ErrDuplicateOrder", err)
	}
	fills, _ := m.QueryFills(context.Background(), "ACC-1", time.Time{})
	if len(fills) != 1 {
		t.Fatalf("duplicate must not be sent: %d fills", len(fills))
	}
}

func TestPlaceOrderFailsClosedWithoutStore(t *testing.T) {
	m := NewMock(MockOptions{AccountID: "ACC-1", Cash: 5000, Prices: map[string]float64{"600000": 10}})
	if _, err := m.PlaceOrder(context.Background(), marketReq("c-1")); !errors.Is(err, ErrIdempotencyStoreUnavailable) {
		t.Fatalf("err = %v, want ErrIdempotencyStoreUnavailable", err)
	}
	positions, err := m.QueryPositions(context.Background(), "ACC-1")
	if err != nil {
		t.Fatalf("QueryPositions: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("nothing may be sent without an idempotency store: %+v", positions)
	}
}

func TestPlaceOrderFailsClosedWhenStoreLoadFails(t *testing.T) {
	m, store, _ := newMock(t, MockOptions{Cash: 5000})
	store.loadErr = errors.New("pg down")

	if _, err := m.PlaceOrder(context.Background(), marketReq("c-1")); !errors.Is(err, ErrIdempotencyStoreUnavailable) {
		t.Fatalf("err = %v, want ErrIdempotencyStoreUnavailable", err)
	}
	fills, _ := m.QueryFills(context.Background(), "ACC-1", time.Time{})
	if len(fills) != 0 {
		t.Fatalf("must not send when the mapping cannot be read: %d fills", len(fills))
	}
}

func TestPlaceOrderFailsClosedWhenReserveFails(t *testing.T) {
	m, store, _ := newMock(t, MockOptions{Cash: 5000})
	store.failSave = func(n int) bool { return n == 1 }

	if _, err := m.PlaceOrder(context.Background(), marketReq("c-1")); !errors.Is(err, ErrIdempotencyStoreUnavailable) {
		t.Fatalf("err = %v, want ErrIdempotencyStoreUnavailable", err)
	}
	fills, _ := m.QueryFills(context.Background(), "ACC-1", time.Time{})
	if len(fills) != 0 {
		t.Fatalf("must not send when the reservation cannot be written: %d fills", len(fills))
	}
}

func TestPlaceOrderReportsUnknownWhenAckCannotBePersisted(t *testing.T) {
	m, store, _ := newMock(t, MockOptions{Cash: 5000})
	store.failSave = func(n int) bool { return n == 2 }

	if _, err := m.PlaceOrder(context.Background(), marketReq("c-1")); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("err = %v, want ErrUnknownState", err)
	}
	// 占位还在 → 重放必须继续报 unknown，绝不重发。
	if _, err := m.PlaceOrder(context.Background(), marketReq("c-1")); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("replay err = %v, want ErrUnknownState", err)
	}
	fills, _ := m.QueryFills(context.Background(), "ACC-1", time.Time{})
	if len(fills) != 1 {
		t.Fatalf("fills = %d, want exactly 1", len(fills))
	}
	// QueryOrder 能给出本地已确认的状态（未决时靠查单，不靠重发）。
	state, err := m.QueryOrder(context.Background(), "c-1")
	if err != nil || state.Status != OrderFilled {
		t.Fatalf("QueryOrder = %+v, err=%v", state, err)
	}
}

func TestPlaceOrderChannelDown(t *testing.T) {
	m, store, _ := newMock(t, MockOptions{Cash: 5000})
	m.Disconnect()

	if _, err := m.PlaceOrder(context.Background(), marketReq("c-1")); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("err = %v, want ErrChannelDown", err)
	}
	if _, found, _ := store.Load(context.Background(), "ACC-1", "c-1"); found {
		t.Fatal("channel down must not reserve an idempotency record")
	}
	if err := m.HealthCheck(context.Background()); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("HealthCheck err = %v", err)
	}
	m.Connect()
	if err := m.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck after Connect: %v", err)
	}
}

func TestPlaceOrderValidation(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{Cash: 5000, LotSize: 100})

	cases := []struct {
		name string
		req  OrderRequest
		want error
	}{
		{"empty id", func() OrderRequest { r := marketReq(""); return r }(), ErrInvalidOrder},
		{"empty symbol", func() OrderRequest { r := marketReq("c-1"); r.Symbol = ""; return r }(), ErrInvalidOrder},
		{"bad side", func() OrderRequest { r := marketReq("c-1"); r.Side = "hold"; return r }(), ErrInvalidOrder},
		{"bad type", func() OrderRequest { r := marketReq("c-1"); r.OrderType = "iceberg"; return r }(), ErrInvalidOrder},
		{"zero qty", func() OrderRequest { r := marketReq("c-1"); r.Quantity = 0; return r }(), ErrInvalidOrder},
		{"lot size", func() OrderRequest { r := marketReq("c-1"); r.Quantity = 150; return r }(), ErrInvalidOrder},
		{"limit without price", func() OrderRequest {
			r := marketReq("c-1")
			r.OrderType = OrderLimit
			return r
		}(), ErrInvalidOrder},
		{"account mismatch", func() OrderRequest { r := marketReq("c-1"); r.AccountID = "ACC-2"; return r }(), ErrAccountMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := m.PlaceOrder(context.Background(), tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPlaceOrderCounterRejection(t *testing.T) {
	m, store, _ := newMock(t, MockOptions{
		Cash: 5000,
		Reject: func(req OrderRequest) (string, string) {
			if req.Symbol == "600000" && req.Quantity == 100 {
				return "invalid_order", "lot rule from counter"
			}
			return "", ""
		},
	})
	ack, err := m.PlaceOrder(context.Background(), marketReq("c-1"))
	if !errors.Is(err, ErrOrderRejected) {
		t.Fatalf("err = %v, want ErrOrderRejected", err)
	}
	if ack == nil || ack.Status != OrderRejected || ack.Code != "invalid_order" {
		t.Fatalf("rejection must return the ack for audit: %+v", ack)
	}
	rec, found, _ := store.Load(context.Background(), "ACC-1", "c-1")
	if !found || rec.Ack.Status != OrderRejected {
		t.Fatalf("rejected ack must be persisted: %+v found=%v", rec, found)
	}
}

func TestPlaceOrderInsufficientBuyingPower(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{Cash: 100})
	ack, err := m.PlaceOrder(context.Background(), marketReq("c-1"))
	if !errors.Is(err, ErrInsufficientBuyingPower) {
		t.Fatalf("err = %v, want ErrInsufficientBuyingPower", err)
	}
	if ack == nil || ack.Status != OrderRejected || ack.Code != "insufficient_buying_power" {
		t.Fatalf("ack = %+v", ack)
	}
}

func TestMarketOrderWithoutPriceStaysAccepted(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{Cash: 5000, Prices: map[string]float64{}})
	ack, err := m.PlaceOrder(context.Background(), marketReq("c-1"))
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ack.Status != OrderAccepted {
		t.Fatalf("status = %q, want accepted (no fake fill)", ack.Status)
	}
	state, err := m.QueryOrder(context.Background(), "c-1")
	if err != nil || state.Status != OrderAccepted || state.RemainingQuantity != 100 {
		t.Fatalf("state = %+v, err=%v", state, err)
	}
}

func TestCancelOrder(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{Cash: 5000})
	limit := marketReq("c-1")
	limit.OrderType = OrderLimit
	limit.LimitPrice = 9.5
	if _, err := m.PlaceOrder(context.Background(), limit); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	state, err := m.CancelOrder(context.Background(), CancelRequest{ClientOrderID: "c-1"})
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if state.Status != OrderCancelled {
		t.Fatalf("status = %q", state.Status)
	}
	// 已撤再撤 → 同终态 + nil（幂等）。
	again, err := m.CancelOrder(context.Background(), CancelRequest{ClientOrderID: "c-1"})
	if err != nil || again.Status != OrderCancelled {
		t.Fatalf("repeat cancel = %+v, err=%v", again, err)
	}

	// 已成交 → ErrOrderNotCancelable。
	if _, err := m.PlaceOrder(context.Background(), marketReq("c-2")); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if _, err := m.CancelOrder(context.Background(), CancelRequest{ClientOrderID: "c-2"}); !errors.Is(err, ErrOrderNotCancelable) {
		t.Fatalf("err = %v, want ErrOrderNotCancelable", err)
	}
	// 未知 → ErrOrderNotFound。
	if _, err := m.CancelOrder(context.Background(), CancelRequest{ClientOrderID: "nope"}); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("err = %v, want ErrOrderNotFound", err)
	}
	// 空请求 → ErrInvalidOrder。
	if _, err := m.CancelOrder(context.Background(), CancelRequest{}); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("err = %v, want ErrInvalidOrder", err)
	}
}

func TestQueryOrderUnknownAndPending(t *testing.T) {
	m, store, _ := newMock(t, MockOptions{Cash: 5000})

	if _, err := m.QueryOrder(context.Background(), "nope"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("err = %v, want ErrOrderNotFound", err)
	}
	// 映射里有占位、本地无状态（模拟发出途中崩溃后的另一个进程）→ unknown + nil。
	if err := store.Save(context.Background(), "ACC-1", "c-9", IdempotencyRecord{
		RequestFingerprint: "fp",
		Ack:                OrderAck{ClientOrderID: "c-9"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	state, err := m.QueryOrder(context.Background(), "c-9")
	if err != nil {
		t.Fatalf("QueryOrder: %v", err)
	}
	if state.Status != OrderUnknown {
		t.Fatalf("status = %q, want unknown", state.Status)
	}
}

func TestQueriesRejectWrongAccount(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{Cash: 5000})
	ctx := context.Background()

	if _, err := m.QueryPositions(ctx, "ACC-2"); !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("positions err = %v", err)
	}
	if _, err := m.QueryAccount(ctx, "ACC-2"); !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("account err = %v", err)
	}
	if _, err := m.QueryFills(ctx, "ACC-2", time.Time{}); !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("fills err = %v", err)
	}
	if _, err := m.QuerySettlement(ctx, "ACC-2", time.Time{}); !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("settlement err = %v", err)
	}
}

func TestEmptyListsAreNotNil(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{Cash: 5000})
	ctx := context.Background()

	positions, err := m.QueryPositions(ctx, "ACC-1")
	if err != nil || positions == nil || len(positions) != 0 {
		t.Fatalf("positions = %#v, err=%v", positions, err)
	}
	fills, err := m.QueryFills(ctx, "ACC-1", time.Time{})
	if err != nil || fills == nil || len(fills) != 0 {
		t.Fatalf("fills = %#v, err=%v", fills, err)
	}
	snap, err := m.QueryAccount(ctx, "ACC-1")
	if err != nil || snap.Positions == nil {
		t.Fatalf("snapshot = %+v, err=%v", snap, err)
	}
}

func TestQueryFillsIsInclusiveAndOrdered(t *testing.T) {
	m, _, c := newMock(t, MockOptions{Cash: 100000})
	ctx := context.Background()

	if _, err := m.PlaceOrder(ctx, marketReq("c-1")); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	first := c.Now()
	c.Advance(time.Minute)
	if _, err := m.PlaceOrder(ctx, marketReq("c-2")); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	all, err := m.QueryFills(ctx, "ACC-1", time.Time{})
	if err != nil {
		t.Fatalf("QueryFills: %v", err)
	}
	if len(all) != 2 || all[0].FilledAt.After(all[1].FilledAt) {
		t.Fatalf("fills are not ordered: %+v", all)
	}

	since, err := m.QueryFills(ctx, "ACC-1", first)
	if err != nil {
		t.Fatalf("QueryFills: %v", err)
	}
	if len(since) != 2 {
		t.Fatalf("since must be inclusive, got %d fills", len(since))
	}
}

func TestSettlementUsesSameSnapshotShape(t *testing.T) {
	m, _, c := newMock(t, MockOptions{Cash: 5000})
	ctx := context.Background()
	if _, err := m.PlaceOrder(ctx, marketReq("c-1")); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	asOf := c.Now().Add(-time.Hour)
	snap, err := m.QuerySettlement(ctx, "ACC-1", asOf)
	if err != nil {
		t.Fatalf("QuerySettlement: %v", err)
	}
	if !snap.AsOf.Equal(asOf) {
		t.Fatalf("as_of = %v, want %v", snap.AsOf, asOf)
	}
	if len(snap.Positions) != 1 || snap.Positions[0].Quantity != 100 {
		t.Fatalf("positions = %+v", snap.Positions)
	}
	// 现金 5000 - 1000 = 4000；总资产 = 现金 + 市值 = 5000。
	if snap.Cash.Available != 4000 || snap.TotalAssets != 5000 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	acct, err := m.QueryAccount(ctx, "ACC-1")
	if err != nil {
		t.Fatalf("QueryAccount: %v", err)
	}
	if acct.TotalAssets != snap.TotalAssets || acct.Cash.Available != snap.Cash.Available {
		t.Fatalf("account and settlement disagree: %+v vs %+v", acct, snap)
	}
}

func TestEmergencyFlattenOnlyReportsConfirmedFills(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{
		Cash:   10000,
		Prices: map[string]float64{"600000": 10, "000001": 5},
	})
	ctx := context.Background()

	// 空仓 → 空 Sold + nil。
	empty, err := m.EmergencyFlatten(ctx, "ops drill")
	if err != nil {
		t.Fatalf("EmergencyFlatten(empty): %v", err)
	}
	if empty == nil || len(empty.Sold) != 0 || empty.SoldTotal != 0 || empty.Skipped == nil {
		t.Fatalf("unexpected empty result: %+v", empty)
	}

	m.SeedPosition("600000", 200, 9)
	m.SeedPosition("000001", 100, 5)

	result, err := m.EmergencyFlatten(ctx, "ops drill")
	if err != nil {
		t.Fatalf("EmergencyFlatten: %v", err)
	}
	if len(result.Sold) != 2 || result.SoldTotal != 300 {
		t.Fatalf("unexpected sold: %+v", result)
	}
	for _, s := range result.Sold {
		if s.BrokerOrderID == "" || s.AvgFillPrice <= 0 || s.ClientOrderID == "" {
			t.Fatalf("sold entry has no real fill basis: %+v", s)
		}
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("skipped = %+v", result.Skipped)
	}

	// 重入：已无持仓 → 不重复卖。
	again, err := m.EmergencyFlatten(ctx, "ops drill")
	if err != nil {
		t.Fatalf("EmergencyFlatten(re-entry): %v", err)
	}
	if len(again.Sold) != 0 || again.SoldTotal != 0 {
		t.Fatalf("re-entry must not sell again: %+v", again)
	}
}

func TestEmergencyFlattenPutsUnconfirmedIntoSkipped(t *testing.T) {
	// 没有价格 → 市价单停在 accepted，不能算清仓成功。
	m, _, _ := newMock(t, MockOptions{Cash: 5000, Prices: map[string]float64{}})
	m.SeedPosition("600000", 100, 10)

	result, err := m.EmergencyFlatten(context.Background(), "drill")
	if err != nil {
		t.Fatalf("EmergencyFlatten: %v", err)
	}
	if len(result.Sold) != 0 || result.SoldTotal != 0 {
		t.Fatalf("must not fake a sale: %+v", result)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Symbol != "600000" {
		t.Fatalf("unexpected skipped: %+v", result.Skipped)
	}
}

func TestEmergencyFlattenChannelDown(t *testing.T) {
	m, _, _ := newMock(t, MockOptions{Cash: 5000})
	m.Disconnect()
	if _, err := m.EmergencyFlatten(context.Background(), "drill"); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("err = %v, want ErrChannelDown", err)
	}
}

func TestOrderStatusVocabulary(t *testing.T) {
	count := 0
	for _, s := range []OrderStatus{
		OrderPending, OrderAccepted, OrderPartiallyFilled, OrderFilled,
		OrderCancelled, OrderRejected, OrderExpired, OrderUnknown,
	} {
		if !s.Valid() {
			t.Fatalf("%q must be valid", s)
		}
		count++
	}
	if count != 8 {
		t.Fatalf("vocabulary size = %d, want 8", count)
	}
	if !OrderFilled.Terminal() || !OrderCancelled.Terminal() || !OrderRejected.Terminal() || !OrderExpired.Terminal() {
		t.Fatal("filled/cancelled/rejected/expired must be terminal")
	}
	for _, s := range []OrderStatus{OrderPending, OrderAccepted, OrderPartiallyFilled, OrderUnknown} {
		if s.Terminal() {
			t.Fatalf("%q must not be terminal", s)
		}
	}
	if OrderStatus("degraded").Valid() {
		t.Fatal("a fourth K9-like value must not be accepted")
	}
}
