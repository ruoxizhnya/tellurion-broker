package gateway

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// DefaultLotSize 是默认申报数量单位（A 股 100 股整数倍）。
const DefaultLotSize = 100

// MockOptions 是 Mock 通道的装配参数。
type MockOptions struct {
	// AccountID 是本通道服务的账户；空串表示不校验 accountID（单账户开发态）。
	AccountID string
	// Cash 是初始可用资金。
	Cash float64
	// Now 返回当前时间（默认 time.Now），统一 UTC。
	Now func() time.Time
	// Store 是幂等映射。**nil 时 PlaceOrder 一律返回
	// ErrIdempotencyStoreUnavailable 且不发送**——这是 fail-closed，不是便利默认值。
	Store AckStore
	// Prices 是市价撮合价（按 symbol）。没有价格时市价单只停留在 accepted，
	// 不伪造成交。
	Prices map[string]float64
	// LotSize 是申报数量单位（默认 DefaultLotSize；0 表示不做该校验）。
	LotSize int
	// Reject 是故障注入：返回非空 code 表示柜台明确拒单（用于测试与回放）。
	Reject func(req OrderRequest) (code, reason string)
}

// Mock 是 BrokerGateway + SettlementQuerier + KillSwitch + HealthChecker 的
// 进程内实现（MockTrader）。
//
// 它只做"转发与确认"：不裁决能不能下，不持有业务表，不猜未知状态。
type Mock struct {
	mu        sync.Mutex
	accountID string
	cash      float64
	now       func() time.Time
	store     AckStore
	prices    map[string]float64
	lotSize   int
	reject    func(OrderRequest) (string, string)
	connected bool
	seq       int
	orders    map[string]*OrderState // key: orderKey(accountID, clientOrderID)
	fills     []Fill
	positions map[string]*Position
	fees      Fees
}

// NewMock 构造 Mock 通道（默认已连接）。
func NewMock(opts MockOptions) *Mock {
	m := &Mock{
		accountID: opts.AccountID,
		cash:      opts.Cash,
		now:       opts.Now,
		store:     opts.Store,
		prices:    opts.Prices,
		lotSize:   opts.LotSize,
		reject:    opts.Reject,
		connected: true,
		orders:    make(map[string]*OrderState),
		positions: make(map[string]*Position),
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.prices == nil {
		m.prices = make(map[string]float64)
	}
	if m.lotSize == 0 {
		m.lotSize = DefaultLotSize
	}
	return m
}

// Connect 标记通道可用。
func (m *Mock) Connect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = true
}

// Disconnect 模拟断连：之后的写操作一律 ErrChannelDown（且不发送）。
func (m *Mock) Disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
}

// SeedPosition 注入一笔持仓（仅 Mock：用于开发与 kill-switch 验收）。
func (m *Mock) SeedPosition(symbol string, quantity int, avgCost float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.positions[symbol] = &Position{
		AccountID:         m.accountID,
		Symbol:            symbol,
		Quantity:          quantity,
		AvailableQuantity: quantity,
		AvgCost:           avgCost,
		MarketValue:       float64(quantity) * m.priceLocked(symbol),
		AsOf:              m.now().UTC(),
	}
}

// HealthCheck 见 HealthChecker：只报真实可用性，不报"看起来健康"。
func (m *Mock) HealthCheck(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	ok := m.connected
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: mock channel disconnected", ErrChannelDown)
	}
	return nil
}

// PlaceOrder 见 BrokerGateway。发送前必然先占位幂等映射；占位失败就不发送。
func (m *Mock) PlaceOrder(ctx context.Context, req OrderRequest) (*OrderAck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.validate(req); err != nil {
		return nil, err
	}
	if m.store == nil {
		return nil, fmt.Errorf("%w: no ack store configured", ErrIdempotencyStoreUnavailable)
	}

	fingerprint := req.Fingerprint()
	rec, found, err := m.store.Load(ctx, req.AccountID, req.ClientOrderID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdempotencyStoreUnavailable, err)
	}
	if found {
		if rec.RequestFingerprint != fingerprint {
			return nil, fmt.Errorf("%w: client_order_id %q was used with a different payload", ErrDuplicateOrder, req.ClientOrderID)
		}
		if rec.Ack.Status == "" {
			// 占位在、回执没写成：说明发出后就没结论。禁止重发。
			return nil, fmt.Errorf("%w: client_order_id %q has no acknowledgement", ErrUnknownState, req.ClientOrderID)
		}
		ack := rec.Ack
		ack.IdempotentReplay = true
		return &ack, nil
	}

	if !m.isConnected() {
		return nil, fmt.Errorf("%w: mock channel is down", ErrChannelDown)
	}
	// 先占位再发送：占位写不进去就不许出门。
	if err := m.store.Save(ctx, req.AccountID, req.ClientOrderID, IdempotencyRecord{
		RequestFingerprint: fingerprint,
		Ack:                OrderAck{ClientOrderID: req.ClientOrderID},
	}); err != nil {
		return nil, fmt.Errorf("%w: reserve: %v", ErrIdempotencyStoreUnavailable, err)
	}

	ack, err := m.execute(req)
	// 回执必须落盘：落不下就诚实地报"状态未知"（映射里留的是占位，重放也不会重发）。
	if saveErr := m.store.Save(ctx, req.AccountID, req.ClientOrderID, IdempotencyRecord{
		RequestFingerprint: fingerprint,
		Ack:                ack,
	}); saveErr != nil {
		return nil, fmt.Errorf("%w: persist ack: %v", ErrUnknownState, saveErr)
	}
	return &ack, err
}

// execute 是"发送并确认"的模拟：只写本地状态与成交，不碰幂等映射。
func (m *Mock) execute(req OrderRequest) (OrderAck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now().UTC()
	if m.reject != nil {
		if code, reason := m.reject(req); code != "" {
			ack := OrderAck{ClientOrderID: req.ClientOrderID, Status: OrderRejected, AcceptedAt: now, Code: code, Reason: reason}
			m.orders[m.stateKey(req.AccountID, req.ClientOrderID)] = &OrderState{
				ClientOrderID: req.ClientOrderID,
				Status:        OrderRejected,
				LastUpdate:    now,
				Code:          code,
				Reason:        reason,
			}
			if code == "insufficient_buying_power" {
				return ack, fmt.Errorf("%w: %s", ErrInsufficientBuyingPower, reason)
			}
			return ack, fmt.Errorf("%w: %s (%s)", ErrOrderRejected, reason, code)
		}
	}

	m.seq++
	brokerOrderID := fmt.Sprintf("MOCK-%06d", m.seq)
	price := m.priceLocked(req.Symbol)
	if req.OrderType == OrderLimit && req.LimitPrice > 0 {
		price = req.LimitPrice
	}

	state := &OrderState{
		ClientOrderID:     req.ClientOrderID,
		BrokerOrderID:     brokerOrderID,
		Status:            OrderAccepted,
		RemainingQuantity: req.Quantity,
		LastUpdate:        now,
	}
	m.orders[m.stateKey(req.AccountID, req.ClientOrderID)] = state

	ack := OrderAck{
		ClientOrderID: req.ClientOrderID,
		BrokerOrderID: brokerOrderID,
		Status:        OrderAccepted,
		AcceptedAt:    now,
	}

	// Mock 只在有价格且资金够时立即成交；否则停在 accepted，不伪造成交。
	if price <= 0 || req.OrderType != OrderMarket {
		return ack, nil
	}
	if req.Side == SideBuy && float64(req.Quantity)*price > m.cash {
		state.Status = OrderRejected
		state.Code = "insufficient_buying_power"
		state.Reason = "cash is not enough for the market order"
		ack.Status = OrderRejected
		ack.Code = state.Code
		ack.Reason = state.Reason
		return ack, fmt.Errorf("%w: %s", ErrInsufficientBuyingPower, state.Reason)
	}

	m.fillLocked(req, state, price, now)
	ack.Status = OrderFilled
	return ack, nil
}

// fillLocked 记账一笔全量成交（Mock 不分片撮合）。调用方必须持锁。
func (m *Mock) fillLocked(req OrderRequest, state *OrderState, price float64, at time.Time) {
	amount := float64(req.Quantity) * price
	if req.Side == SideBuy {
		m.cash -= amount
		pos := m.positions[req.Symbol]
		if pos == nil {
			pos = &Position{AccountID: req.AccountID, Symbol: req.Symbol}
			m.positions[req.Symbol] = pos
		}
		total := float64(pos.Quantity) * pos.AvgCost
		pos.Quantity += req.Quantity
		pos.AvailableQuantity = pos.Quantity
		pos.AvgCost = (total + amount) / float64(pos.Quantity)
	} else {
		m.cash += amount
		if pos := m.positions[req.Symbol]; pos != nil {
			pos.Quantity -= req.Quantity
			pos.AvailableQuantity = pos.Quantity
			if pos.Quantity <= 0 {
				delete(m.positions, req.Symbol)
			}
		}
	}
	if pos := m.positions[req.Symbol]; pos != nil {
		pos.MarketValue = float64(pos.Quantity) * price
		pos.AsOf = at
	}

	state.Status = OrderFilled
	state.FilledQuantity = req.Quantity
	state.AvgFillPrice = price
	state.RemainingQuantity = 0
	state.LastUpdate = at

	m.seq++
	m.fills = append(m.fills, Fill{
		FillID:        fmt.Sprintf("MOCK-F-%06d", m.seq),
		ClientOrderID: req.ClientOrderID,
		BrokerOrderID: state.BrokerOrderID,
		Symbol:        req.Symbol,
		Side:          req.Side,
		Quantity:      req.Quantity,
		Price:         price,
		FilledAt:      at,
	})
}

// CancelOrder 见 BrokerGateway：已撤再撤 → 同终态 + nil。
func (m *Mock) CancelOrder(ctx context.Context, req CancelRequest) (*OrderState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.ClientOrderID == "" && req.BrokerOrderID == "" {
		return nil, fmt.Errorf("%w: client_order_id or broker_order_id is required", ErrInvalidOrder)
	}
	if req.ClientOrderID == "" {
		return nil, fmt.Errorf("%w: cancel by broker_order_id is not supported by Mock", ErrNotSupported)
	}
	if !m.isConnected() {
		return nil, fmt.Errorf("%w: mock channel is down", ErrChannelDown)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.orders[m.stateKey(m.accountID, req.ClientOrderID)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrOrderNotFound, req.ClientOrderID)
	}
	switch state.Status {
	case OrderCancelled:
		out := *state
		return &out, nil
	case OrderFilled, OrderRejected, OrderExpired:
		return nil, fmt.Errorf("%w: %s is %s", ErrOrderNotCancelable, req.ClientOrderID, state.Status)
	}

	now := m.now().UTC()
	state.Status = OrderCancelled
	state.RemainingQuantity = 0
	state.LastUpdate = now
	out := *state
	return &out, nil
}

// QueryOrder 见 BrokerGateway。
func (m *Mock) QueryOrder(ctx context.Context, clientOrderID string) (*OrderState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if clientOrderID == "" {
		return nil, fmt.Errorf("%w: client_order_id is required", ErrInvalidOrder)
	}
	if !m.isConnected() {
		return nil, fmt.Errorf("%w: mock channel is down", ErrChannelDown)
	}

	m.mu.Lock()
	if state, ok := m.orders[m.stateKey(m.accountID, clientOrderID)]; ok {
		out := *state
		m.mu.Unlock()
		return &out, nil
	}
	m.mu.Unlock()

	// 本地没有，但映射里可能只有占位（发出途中崩溃）→ unknown，不是 not_found。
	// 单账户开发态（accountID 为空）无从推断判重范围，跳过映射回查。
	if m.store != nil && m.accountID != "" {
		_, found, err := m.store.Load(ctx, m.accountID, clientOrderID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrChannelDown, err)
		}
		if found {
			return &OrderState{
				ClientOrderID: clientOrderID,
				Status:        OrderUnknown,
				LastUpdate:    m.now().UTC(),
			}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrOrderNotFound, clientOrderID)
}

// QueryPositions 见 BrokerGateway：空仓返回 `[]`。
func (m *Mock) QueryPositions(ctx context.Context, accountID string) ([]Position, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.checkAccount(accountID); err != nil {
		return nil, err
	}
	if !m.isConnected() {
		return nil, fmt.Errorf("%w: mock channel is down", ErrChannelDown)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Position, 0, len(m.positions))
	for _, p := range m.positions {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out, nil
}

// QueryAccount 见 BrokerGateway。
func (m *Mock) QueryAccount(ctx context.Context, accountID string) (*AccountSnapshot, error) {
	return m.snapshot(ctx, accountID, time.Time{})
}

// QuerySettlement 见 SettlementQuerier：同一形状，`asOf` 只是快照时间。
func (m *Mock) QuerySettlement(ctx context.Context, accountID string, asOf time.Time) (*AccountSnapshot, error) {
	return m.snapshot(ctx, accountID, asOf)
}

func (m *Mock) snapshot(ctx context.Context, accountID string, asOf time.Time) (*AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.checkAccount(accountID); err != nil {
		return nil, err
	}
	if !m.isConnected() {
		return nil, fmt.Errorf("%w: mock channel is down", ErrChannelDown)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	positions := make([]Position, 0, len(m.positions))
	marketValue := 0.0
	for _, p := range m.positions {
		positions = append(positions, *p)
		marketValue += p.MarketValue
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i].Symbol < positions[j].Symbol })

	at := asOf
	if at.IsZero() {
		at = m.now().UTC()
	} else {
		at = at.UTC()
	}
	acct := m.accountID
	if acct == "" {
		acct = accountID
	}
	return &AccountSnapshot{
		AccountID:   acct,
		AsOf:        at,
		Cash:        Cash{Total: m.cash, Available: m.cash},
		TotalAssets: m.cash + marketValue,
		Positions:   positions,
		Fees:        m.fees,
	}, nil
}

// QueryFills 见 BrokerGateway：`since` 含边界，按 (FilledAt, FillID) 升序。
func (m *Mock) QueryFills(ctx context.Context, accountID string, since time.Time) ([]Fill, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.checkAccount(accountID); err != nil {
		return nil, err
	}
	if !m.isConnected() {
		return nil, fmt.Errorf("%w: mock channel is down", ErrChannelDown)
	}

	m.mu.Lock()
	out := make([]Fill, 0, len(m.fills))
	for _, f := range m.fills {
		if !since.IsZero() && f.FilledAt.Before(since) {
			continue
		}
		out = append(out, f)
	}
	m.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if !out[i].FilledAt.Equal(out[j].FilledAt) {
			return out[i].FilledAt.Before(out[j].FilledAt)
		}
		return out[i].FillID < out[j].FillID
	})
	return out, nil
}

// EmergencyFlatten 见 KillSwitch：只把**真实成交**的持仓写进 Sold，
// 其余一律进 Skipped（绝不伪造成功）。
func (m *Mock) EmergencyFlatten(ctx context.Context, reason string) (*EmergencyFlattenResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.isConnected() {
		return nil, fmt.Errorf("%w: mock channel is down", ErrChannelDown)
	}

	started := m.now().UTC()
	positions, err := m.QueryPositions(ctx, m.accountID)
	if err != nil {
		return nil, err
	}

	result := &EmergencyFlattenResult{
		Sold:      make([]FlattenedPosition, 0, len(positions)),
		Skipped:   make([]SkippedPosition, 0, len(positions)),
		StartedAt: started,
		Reason:    reason,
	}
	for _, p := range positions {
		if p.Quantity <= 0 {
			continue
		}
		// 重入幂等：ID 由 (symbol, quantity) 决定；卖掉之后数量为 0，不会再下。
		clientOrderID := fmt.Sprintf("flatten:%s:%d", p.Symbol, p.Quantity)
		ack, err := m.PlaceOrder(ctx, OrderRequest{
			ClientOrderID: clientOrderID,
			AccountID:     p.AccountID,
			Symbol:        p.Symbol,
			Side:          SideSell,
			OrderType:     OrderMarket,
			Quantity:      p.Quantity,
		})
		if err != nil {
			reason := err.Error()
			if ack != nil && ack.Code != "" {
				reason = ack.Code + ": " + ack.Reason
			}
			result.Skipped = append(result.Skipped, SkippedPosition{
				AccountID: p.AccountID,
				Symbol:    p.Symbol,
				Quantity:  p.Quantity,
				Reason:    reason,
			})
			continue
		}
		if ack.Status != OrderFilled {
			result.Skipped = append(result.Skipped, SkippedPosition{
				AccountID: p.AccountID,
				Symbol:    p.Symbol,
				Quantity:  p.Quantity,
				Reason:    "not confirmed: status=" + string(ack.Status),
			})
			continue
		}
		state, qerr := m.QueryOrder(ctx, clientOrderID)
		if qerr != nil {
			result.Skipped = append(result.Skipped, SkippedPosition{
				AccountID: p.AccountID,
				Symbol:    p.Symbol,
				Quantity:  p.Quantity,
				Reason:    "not confirmed: " + qerr.Error(),
			})
			continue
		}
		result.Sold = append(result.Sold, FlattenedPosition{
			AccountID:     p.AccountID,
			Symbol:        p.Symbol,
			Quantity:      p.Quantity,
			ClientOrderID: clientOrderID,
			BrokerOrderID: state.BrokerOrderID,
			AvgFillPrice:  state.AvgFillPrice,
			FilledAt:      state.LastUpdate,
		})
		result.SoldTotal += p.Quantity
	}
	result.CompletedAt = m.now().UTC()
	return result, nil
}

func (m *Mock) validate(req OrderRequest) error {
	if req.ClientOrderID == "" {
		return fmt.Errorf("%w: client_order_id is required", ErrInvalidOrder)
	}
	if req.AccountID == "" {
		return fmt.Errorf("%w: account_id is required", ErrInvalidOrder)
	}
	if err := m.checkAccount(req.AccountID); err != nil {
		return err
	}
	if req.Symbol == "" {
		return fmt.Errorf("%w: symbol is required", ErrInvalidOrder)
	}
	if !req.Side.Valid() {
		return fmt.Errorf("%w: side %q", ErrInvalidOrder, req.Side)
	}
	if !req.OrderType.Valid() {
		return fmt.Errorf("%w: order_type %q", ErrInvalidOrder, req.OrderType)
	}
	if req.Quantity <= 0 {
		return fmt.Errorf("%w: quantity must be positive", ErrInvalidOrder)
	}
	if m.lotSize > 1 && req.Quantity%m.lotSize != 0 {
		return fmt.Errorf("%w: quantity must be a multiple of %d", ErrInvalidOrder, m.lotSize)
	}
	if req.OrderType == OrderLimit && req.LimitPrice <= 0 {
		return fmt.Errorf("%w: limit_price is required for limit orders", ErrInvalidOrder)
	}
	return nil
}

func (m *Mock) checkAccount(accountID string) error {
	m.mu.Lock()
	want := m.accountID
	m.mu.Unlock()
	if want == "" || accountID == want {
		return nil
	}
	return fmt.Errorf("%w: %q is not served by this channel (%q)", ErrAccountMismatch, accountID, want)
}

func (m *Mock) isConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

// priceLocked 返回撮合价（无价格时 0 = 不撮合）。调用方必须持锁。
func (m *Mock) priceLocked(symbol string) float64 {
	return m.prices[symbol]
}

func orderKey(accountID, clientOrderID string) string {
	return accountID + "\x00" + clientOrderID
}

// stateKey 定位本地订单状态：单账户开发态（m.accountID 为空）时只按
// clientOrderID 索引。accountID 是构造期常量，读取不需要持锁。
func (m *Mock) stateKey(accountID, clientOrderID string) string {
	if m.accountID == "" {
		return clientOrderID
	}
	return orderKey(accountID, clientOrderID)
}

// MemoryAckStore 是 AckStore 的进程内实现（X6a 开发态；跨重启持久化见 R2）。
type MemoryAckStore struct {
	mu      sync.Mutex
	records map[string]IdempotencyRecord
}

// NewMemoryAckStore 构造内存幂等映射。
func NewMemoryAckStore() *MemoryAckStore {
	return &MemoryAckStore{records: make(map[string]IdempotencyRecord)}
}

// Load 见 AckStore。
func (s *MemoryAckStore) Load(ctx context.Context, accountID, clientOrderID string) (IdempotencyRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return IdempotencyRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[orderKey(accountID, clientOrderID)]
	return rec, ok, nil
}

// Save 见 AckStore。
func (s *MemoryAckStore) Save(ctx context.Context, accountID, clientOrderID string, rec IdempotencyRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[orderKey(accountID, clientOrderID)] = rec
	return nil
}
