// Package gateway 是 BrokerGateway 的进程内实现面：DTO、四个 port 与 Mock 通道。
//
// 边界（docs/ARCHITECTURE.md §9）：
//   - 网关只转发与确认，**不做风控裁决**（RiskPolicy 在调用方进程内先过一遍）；
//   - **不持有订单真相**（那是 core 的 `quant`）；本仓只做通道适配与幂等映射；
//   - 未实现的能力返回 ErrNotSupported，**绝不返回成功形状**。
//
// 今天的实现是 Mock（MockTrader）；XTP 适配器属 X6c，不在本包。
package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Side 是买卖方向。
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// Valid 报告 s 是否是已知方向。
func (s Side) Valid() bool { return s == SideBuy || s == SideSell }

// OrderType 是委托类型（只两种：市价 / 限价）。
type OrderType string

const (
	OrderMarket OrderType = "market"
	OrderLimit  OrderType = "limit"
)

// Valid 报告 t 是否是已知委托类型。
func (t OrderType) Valid() bool { return t == OrderMarket || t == OrderLimit }

// OrderStatus 是订单状态八值（**不是 K9 的 run 三值**，docs/api-schema.md §4）。
//
// `unknown` 是**未决**状态，不是拒绝：调用方只能查单 / 对账，禁止重发。
type OrderStatus string

const (
	OrderPending         OrderStatus = "pending"
	OrderAccepted        OrderStatus = "accepted"
	OrderPartiallyFilled OrderStatus = "partially_filled"
	OrderFilled          OrderStatus = "filled"
	OrderCancelled       OrderStatus = "cancelled"
	OrderRejected        OrderStatus = "rejected"
	OrderExpired         OrderStatus = "expired"
	OrderUnknown         OrderStatus = "unknown"
)

// Valid 报告 s 是否是八值之一。
func (s OrderStatus) Valid() bool {
	switch s {
	case OrderPending, OrderAccepted, OrderPartiallyFilled, OrderFilled,
		OrderCancelled, OrderRejected, OrderExpired, OrderUnknown:
		return true
	}
	return false
}

// Terminal 报告 s 是否是终态（filled / cancelled / rejected / expired）。
// `unknown` **不是**终态：它表示还没有结论。
func (s OrderStatus) Terminal() bool {
	switch s {
	case OrderFilled, OrderCancelled, OrderRejected, OrderExpired:
		return true
	}
	return false
}

// OrderRequest 是下单请求。只保留通道需要的字段。
//
// **不含** `BusinessType` / `ExecAlgo` / `StopPrice` / `Trail*`：券商私有字段留在
// 适配器内部，执行层语义留在 core。
type OrderRequest struct {
	// ClientOrderID 由调用方生成、重试不变；网关不改写、不生成（幂等键）。
	ClientOrderID string    `json:"client_order_id"`
	AccountID     string    `json:"account_id"`
	Symbol        string    `json:"symbol"`
	Side          Side      `json:"side"`
	OrderType     OrderType `json:"order_type"`
	Quantity      int       `json:"quantity"`
	LimitPrice    float64   `json:"limit_price,omitempty"`
}

// Fingerprint 是载荷指纹：同一 ClientOrderID 下用它判定「同载荷（重放）」
// 还是「不同载荷（duplicate_order）」。
//
// 只取参与下单的字段，且按固定顺序序列化，因此与结构体字段声明顺序无关。
func (r OrderRequest) Fingerprint() string {
	b, err := json.Marshal([]any{r.ClientOrderID, r.AccountID, r.Symbol, r.Side, r.OrderType, r.Quantity, r.LimitPrice})
	if err != nil {
		// 这里不可能失败（全是基本类型），失败即编程错误，由调用链上游兜底。
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// OrderAck 是下单受理回执。**受理 ≠ 成交。**
type OrderAck struct {
	ClientOrderID    string      `json:"client_order_id"`
	BrokerOrderID    string      `json:"broker_order_id,omitempty"`
	Status           OrderStatus `json:"status"`
	IdempotentReplay bool        `json:"idempotent_replay"`
	AcceptedAt       time.Time   `json:"accepted_at"`
	Code             string      `json:"code,omitempty"`
	Reason           string      `json:"reason,omitempty"`
}

// CancelRequest 是撤单请求：至少 `ClientOrderID` / `BrokerOrderID` 之一非空，
// 优先按 `ClientOrderID` 幂等。
type CancelRequest struct {
	ClientOrderID string `json:"client_order_id,omitempty"`
	BrokerOrderID string `json:"broker_order_id,omitempty"`
}

// OrderState 是订单的当前状态快照。
type OrderState struct {
	ClientOrderID     string      `json:"client_order_id"`
	BrokerOrderID     string      `json:"broker_order_id,omitempty"`
	Status            OrderStatus `json:"status"`
	FilledQuantity    int         `json:"filled_quantity"`
	AvgFillPrice      float64     `json:"avg_fill_price"`
	RemainingQuantity int         `json:"remaining_quantity"`
	LastUpdate        time.Time   `json:"last_update"`
	Code              string      `json:"code,omitempty"`
	Reason            string      `json:"reason,omitempty"`
}

// Fill 是归一化成交回报。`FillID` 稳定、跨调用不重复。
type Fill struct {
	FillID        string    `json:"fill_id"`
	ClientOrderID string    `json:"client_order_id"`
	BrokerOrderID string    `json:"broker_order_id,omitempty"`
	Symbol        string    `json:"symbol"`
	Side          Side      `json:"side"`
	Quantity      int       `json:"quantity"`
	Price         float64   `json:"price"`
	Commission    float64   `json:"commission"`
	StampTax      float64   `json:"stamp_tax"`
	TransferFee   float64   `json:"transfer_fee"`
	FilledAt      time.Time `json:"filled_at"`
}

// Position 是柜台口径的持仓。`AvailableQuantity` 是柜台事实；
// 本地 T+1 记账字段不进 DTO。
type Position struct {
	AccountID         string    `json:"account_id"`
	Symbol            string    `json:"symbol"`
	Quantity          int       `json:"quantity"`
	AvailableQuantity int       `json:"available_quantity"`
	AvgCost           float64   `json:"avg_cost"`
	MarketValue       float64   `json:"market_value"`
	AsOf              time.Time `json:"as_of"`
}

// Cash 是资金口径快照。
type Cash struct {
	Total     float64 `json:"total"`
	Available float64 `json:"available"`
}

// Fees 是费用累计口径。
type Fees struct {
	Commission  float64 `json:"commission"`
	StampTax    float64 `json:"stamp_tax"`
	TransferFee float64 `json:"transfer_fee"`
}

// AccountSnapshot 是账户快照。`QueryAccount` 与 `QuerySettlement` **共用同一
// 形状**（避免两套快照漂移）。
type AccountSnapshot struct {
	AccountID   string     `json:"account_id"`
	AsOf        time.Time  `json:"as_of"`
	Cash        Cash       `json:"cash"`
	TotalAssets float64    `json:"total_assets"`
	Positions   []Position `json:"positions"`
	Fees        Fees       `json:"fees"`
}

// FlattenedPosition 是一条**已真实提交并确认**的平仓成交。
type FlattenedPosition struct {
	AccountID     string    `json:"account_id"`
	Symbol        string    `json:"symbol"`
	Quantity      int       `json:"quantity"`
	ClientOrderID string    `json:"client_order_id"`
	BrokerOrderID string    `json:"broker_order_id,omitempty"`
	AvgFillPrice  float64   `json:"avg_fill_price"`
	FilledAt      time.Time `json:"filled_at"`
}

// SkippedPosition 是一条**没有确认成交**的持仓（含未提交 / 被拒 / 状态未知）。
type SkippedPosition struct {
	AccountID string `json:"account_id"`
	Symbol    string `json:"symbol"`
	Quantity  int    `json:"quantity"`
	Reason    string `json:"reason"`
}

// EmergencyFlattenResult 是紧急清仓结果。`Sold` 与 `Skipped` 互斥：
// 每个 `Sold` 都必须有真实成交依据，未确认的一律进 `Skipped`。
type EmergencyFlattenResult struct {
	Sold        []FlattenedPosition `json:"sold"`
	Skipped     []SkippedPosition   `json:"skipped"`
	SoldTotal   int                 `json:"sold_total"`
	StartedAt   time.Time           `json:"started_at"`
	CompletedAt time.Time           `json:"completed_at"`
	Reason      string              `json:"reason"`
}
