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
	orders    map[string]*OrderState // key: stateKey(clientOrderID)
	fills     []Fill
	positions map[string]*Position
	posGen    map[string]int // symbol → 持仓代次（建仓时 +1，清零不回落）
	fees      Fees
}

// 编译期断言：Mock 必须同时满足四个 port 的**全部**方法。
// 四个 port 的方法集是冻结面（docs/ARCHITECTURE.md §9.6），少一个方法就是
// 实现方全红——与其等到调用方编译失败，不如在这里钉住。
var (
	_ BrokerGateway     = (*Mock)(nil)
	_ SettlementQuerier = (*Mock)(nil)
	_ KillSwitch        = (*Mock)(nil)
	_ HealthChecker     = (*Mock)(nil)
)

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
		posGen:    make(map[string]int),
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
//
// 每次注入都是**新一代**持仓（代次 +1）：这样 flatten 的 ClientOrderID
// 与上一轮不同，重入时不会命中旧回执。
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
	m.posGen[symbol]++
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
//
// **幂等域 = 本通道服务的账户**（m.accountID）+ ClientOrderID，不是请求里的
// AccountID：配置了账户时两者必然相等（validate → checkAccount 拦住不匹配的），
// 所以口径一致；单账户开发态（m.accountID 为空）时域退化为 ClientOrderID 单独，
// 与本通道"只服务一个账户"的事实一致，也与本地订单索引（stateKey）同口径。
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
	rec, found, err := m.store.Load(ctx, m.accountID, req.ClientOrderID)
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
		// 重放**原样还原**当初的结论：被柜台拒过的单重放仍然是拒单
		// （docs/api-schema.md §2：order_rejected 时 ack 与 error 同时非 nil），
		// 不能因为"这次没报错"就被调用方当成受理成功。
		if err := ackTerminalError(ack); err != nil {
			return &ack, err
		}
		return &ack, nil
	}

	if !m.isConnected() {
		return nil, fmt.Errorf("%w: mock channel is down", ErrChannelDown)
	}
	// 先占位再发送：占位写不进去就不许出门。
	if err := m.store.Save(ctx, m.accountID, req.ClientOrderID, IdempotencyRecord{
		RequestFingerprint: fingerprint,
		Ack:                OrderAck{ClientOrderID: req.ClientOrderID},
	}); err != nil {
		return nil, fmt.Errorf("%w: reserve: %v", ErrIdempotencyStoreUnavailable, err)
	}

	ack, err := m.execute(req)
	// 回执必须落盘：落不下就诚实地报"状态未知"（映射里留的是占位，重放也不会重发）。
	if saveErr := m.store.Save(ctx, m.accountID, req.ClientOrderID, IdempotencyRecord{
		RequestFingerprint: fingerprint,
		Ack:                ack,
	}); saveErr != nil {
		return nil, fmt.Errorf("%w: persist ack: %v", ErrUnknownState, saveErr)
	}
	return &ack, err
}

// ackTerminalError 把已落盘的回执还原成当初的契约错误。
// 只认两种柜台明确结论：拒单与买力不足；其余（受理 / 成交）没有错误。
func ackTerminalError(ack OrderAck) error {
	if ack.Status != OrderRejected {
		return nil
	}
	if ack.Code == "insufficient_buying_power" {
		return fmt.Errorf("%w: %s", ErrInsufficientBuyingPower, ack.Reason)
	}
	return fmt.Errorf("%w: %s (%s)", ErrOrderRejected, ack.Reason, ack.Code)
}

// execute 是"发送并确认"的模拟：只写本地状态与成交，不碰幂等映射。
func (m *Mock) execute(req OrderRequest) (OrderAck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now().UTC()
	if m.reject != nil {
		if code, reason := m.reject(req); code != "" {
			ack := OrderAck{ClientOrderID: req.ClientOrderID, Status: OrderRejected, AcceptedAt: now, Code: code, Reason: reason}
			m.orders[m.stateKey(req.ClientOrderID)] = &OrderState{
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
	m.orders[m.stateKey(req.ClientOrderID)] = state

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
			// 从无到有 = 新一代持仓（flatten 的 ID 靠它区分轮次）。
			m.posGen[req.Symbol]++
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
				// 清零即删除，但**代次不回落**：重新建仓是新的一代。
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

	state, ok := m.orders[m.stateKey(req.ClientOrderID)]
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
//
// 两个来源：
//  1. 本地订单状态（这个进程真正处理过的那一笔）；
//  2. 幂等映射里有记录、但本地没有状态（另一个进程写的，或本进程写完回执
//     后本地状态已丢）→ `unknown` + `nil`（docs/ARCHITECTURE.md §9.2 冻结表：
//     "查不到但可能已送达 → OrderState{Status: unknown} + nil"）。
//  3. 两处都没有 → ErrOrderNotFound（**能证明**从未发送过这个 ID）。
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
	if state, ok := m.orders[m.stateKey(clientOrderID)]; ok {
		out := *state
		m.mu.Unlock()
		return &out, nil
	}
	m.mu.Unlock()

	// 映射里有记录就说明**可能已经发出去了** → unknown（不是 not_found：
	// not_found 的判据是"网关能证明从未发送过该 ID"）。单账户开发态也要查：
	// 判重域与本地索引同口径（都用 m.accountID），不查就是把"可能已发出"
	// 误报成"从未发出"。
	if m.store != nil {
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
//
// 重入幂等靠**持仓代次**（posGen）而不是"卖过就删行"：
// 同一笔持仓反复 flatten 用同一个 ClientOrderID → 重放，不重复下单；
// 这笔持仓卖掉后**又买回来**（代次 +1）→ 新 ID，真下单。
// 若 ID 只由 (symbol, qty) 组成，第二轮的 flatten 会拿到上一轮的旧回执
// 并把它当成"本次已卖出"——那就是在**伪造成功**。
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
		// 重入幂等：ID 由 (symbol, 持仓代次, 数量) 决定。
		clientOrderID := fmt.Sprintf("flatten:%s:%d:%d", p.Symbol, m.positionGeneration(p.Symbol), p.Quantity)
		ack, err := m.PlaceOrder(ctx, OrderRequest{
			ClientOrderID: clientOrderID,
			AccountID:     p.AccountID,
			Symbol:        p.Symbol,
			Side:          SideSell,
			OrderType:     OrderMarket,
			Quantity:      p.Quantity,
		})
		if err != nil {
			why := err.Error()
			if ack != nil && ack.Code != "" {
				why = ack.Code + ": " + ack.Reason
			}
			result.Skipped = append(result.Skipped, SkippedPosition{
				AccountID: p.AccountID,
				Symbol:    p.Symbol,
				Quantity:  p.Quantity,
				Reason:    why,
			})
			continue
		}
		if ack.IdempotentReplay {
			// 这笔 ID 之前已经下过：那一单不是为**当前**持仓下的，
			// 不能拿它当"本次清仓成功"的凭据。
			result.Skipped = append(result.Skipped, SkippedPosition{
				AccountID: p.AccountID,
				Symbol:    p.Symbol,
				Quantity:  p.Quantity,
				Reason:    "not confirmed: replay of an earlier flatten order",
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

// positionGeneration 返回某标的的持仓代次：持仓从"没有"变成"有"时 +1。
// 卖掉清零后**不回落**——重新建仓是新的一代，ID 才能与上一轮区分开。
func (m *Mock) positionGeneration(symbol string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.posGen[symbol]
}

func (m *Mock) validate(req OrderRequest) error {
	if req.ClientOrderID == "" {
		return fmt.Errorf("%w: client_order_id is required", ErrInvalidOrder)
	}
	// 单账户开发态（未配置通道账户）不校验账户，空账户合法；
	// 配置了账户时，空账户 = 参数不合法（缺必填项）。
	if req.AccountID == "" && m.accountID != "" {
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

// stateKey 定位本地订单状态：口径与幂等映射**完全一致**（都用本通道服务的
// 账户 m.accountID + ClientOrderID），这样 QueryOrder / CancelOrder（两者都
// 不带账户参数）查得到 PlaceOrder 写下的那一笔。
//
// 单账户开发态（m.accountID 为空）时退化为只按 clientOrderID 索引。
func (m *Mock) stateKey(clientOrderID string) string {
	if m.accountID == "" {
		return clientOrderID
	}
	return orderKey(m.accountID, clientOrderID)
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
