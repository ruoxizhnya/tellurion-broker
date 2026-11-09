// Package gateway 是 BrokerGateway 的进程内实现面：DTO、四个 port 与 Mock 通道。
//
// 边界（docs/ARCHITECTURE.md §9）：
//   - 网关只转发与确认，**不做风控裁决**（RiskPolicy 在调用方进程内先过一遍）；
//   - **不持有订单真相**（那是 core 的 `quant`）；本仓只做通道适配与幂等映射；
//   - 未实现的能力返回 ErrNotSupported，**绝不返回成功形状**。
//
// 今天的实现是 Mock（MockTrader）；XTP 适配器属 X6c，不在本包。
//
// Wire 形状的唯一 owner 是 `tellurion-contracts/broker`（CT-05）。本文件只保留
// **类型别名**（不复制字段），让包内既有调用点与契约类型逐字节一致；迁移前/
// 后的报文快照见 `wire_shape_test.go`。
package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	broker "github.com/ruoxizhnya/tellurion-contracts/broker"
)

// ─── 契约类型别名（owner = tellurion-contracts/broker） ────────────────

// Side 是买卖方向（两值）。
type Side = broker.Side

const (
	SideBuy  = broker.SideBuy
	SideSell = broker.SideSell
)

// OrderType 是委托类型（只两种：市价 / 限价）。
type OrderType = broker.OrderType

const (
	OrderMarket = broker.OrderMarket
	OrderLimit  = broker.OrderLimit
)

// OrderStatus 是订单状态八值（**不是 K9 的 run 三值**，docs/api-schema.md §4）。
type OrderStatus = broker.OrderStatus

const (
	OrderPending         = broker.OrderPending
	OrderAccepted        = broker.OrderAccepted
	OrderPartiallyFilled = broker.OrderPartiallyFilled
	OrderFilled          = broker.OrderFilled
	OrderCancelled       = broker.OrderCancelled
	OrderRejected        = broker.OrderRejected
	OrderExpired         = broker.OrderExpired
	OrderUnknown         = broker.OrderUnknown
)

// OrderRequest 是下单请求。只保留通道需要的字段。
type OrderRequest = broker.OrderRequest

// OrderAck 是下单受理回执。**受理 ≠ 成交。**
type OrderAck = broker.OrderAck

// CancelRequest 是撤单请求：至少 `ClientOrderID` / `BrokerOrderID` 之一非空。
type CancelRequest = broker.CancelRequest

// OrderState 是订单的当前状态快照。
type OrderState = broker.OrderState

// Fill 是归一化成交回报。`FillID` 稳定、跨调用不重复。
type Fill = broker.Fill

// Position 是柜台口径的持仓。`AvailableQuantity` 是柜台事实。
type Position = broker.Position

// Cash 是资金口径快照。
type Cash = broker.Cash

// Fees 是费用累计口径。
type Fees = broker.Fees

// AccountSnapshot 是账户快照。`QueryAccount` 与 `QuerySettlement` 共用同一形状。
type AccountSnapshot = broker.AccountSnapshot

// FlattenedPosition 是一条**已真实提交并确认**的平仓成交。
type FlattenedPosition = broker.FlattenedPosition

// SkippedPosition 是一条**没有确认成交**的持仓（含未提交 / 被拒 / 状态未知）。
type SkippedPosition = broker.SkippedPosition

// EmergencyFlattenResult 是紧急清仓结果。`Sold` 与 `Skipped` 互斥。
type EmergencyFlattenResult = broker.EmergencyFlattenResult

// Fingerprint 是载荷指纹：同一 ClientOrderID 下用它判定「同载荷（重放）」
// 还是「不同载荷（duplicate_order）」。契约类型不能加方法，故保留为自由函数。
//
// 只取参与下单的字段，且按固定顺序序列化，因此与结构体字段声明顺序无关。
func Fingerprint(r OrderRequest) string {
	b, err := json.Marshal([]any{r.ClientOrderID, r.AccountID, r.Symbol, r.Side, r.OrderType, r.Quantity, r.LimitPrice})
	if err != nil {
		// 这里不可能失败（全是基本类型），失败即编程错误，由调用链上游兜底。
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
