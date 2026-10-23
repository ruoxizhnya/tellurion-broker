package gateway

import "errors"

// 12 个契约哨兵（docs/api-schema.md §2，一一对应 12 个 wire code）。
//
// 调用方只 `errors.Is` 这些哨兵；适配器私有错误（XTP 的 ErrOffline 等）必须在
// 适配器边界翻译成本表之一。本包不定义 wire code（网关不是 HTTP 服务）。
var (
	// ErrOrderRejected 是柜台**明确**拒单且回执已拿到：不重试。
	// 与它同时返回的是非 nil 的 OrderAck{Status: rejected}（回执要留痕）。
	ErrOrderRejected = errors.New("broker: order rejected")
	// ErrUnknownState 是已发出但结果未知（超时 / 发送后断连）：**禁止重发**，
	// 只能 QueryOrder / 对账。
	ErrUnknownState = errors.New("broker: order state unknown")
	// ErrChannelDown 是未连接 / 熔断中 / 健康检查失败：不发送。
	ErrChannelDown = errors.New("broker: channel down")
	// ErrDuplicateOrder 是同 ClientOrderID、不同载荷：不发送，绝不发第二笔。
	ErrDuplicateOrder = errors.New("broker: duplicate client order id with different payload")
	// ErrOrderNotFound 是网关**能证明**从未发送过该 ID（不是「柜台侧不存在」）。
	ErrOrderNotFound = errors.New("broker: order not found")
	// ErrOrderNotCancelable 是已成交 / 已过期 / 已终态，不可撤。
	ErrOrderNotCancelable = errors.New("broker: order not cancelable")
	// ErrInvalidOrder 是参数不合法 / 违反申报数量规则：修参数后换新 ID 重提。
	ErrInvalidOrder = errors.New("broker: invalid order")
	// ErrRateLimited 是柜台限流，且**确定未送达**：退避后可用同一 ID 重试。
	ErrRateLimited = errors.New("broker: rate limited")
	// ErrNotSupported 是通道不支持该能力：**禁止**伪造成功形状。
	ErrNotSupported = errors.New("broker: not supported")
	// ErrAccountMismatch 是 accountID 不属于本通道：配置错误，fail-loud。
	ErrAccountMismatch = errors.New("broker: account mismatch")
	// ErrInsufficientBuyingPower 是柜台明确返回买力不足：不重试。
	ErrInsufficientBuyingPower = errors.New("broker: insufficient buying power")
	// ErrIdempotencyStoreUnavailable 是幂等映射（持久化）不可用：**不发送**。
	// 这是可用性事故，不是下单失败。
	ErrIdempotencyStoreUnavailable = errors.New("broker: idempotency store unavailable")
)
