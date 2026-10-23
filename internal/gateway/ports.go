package gateway

import (
	"context"
	"time"
)

// BrokerGateway 是订单生命周期 + 查询面（docs/ARCHITECTURE.md §9.2 A-1）。
//
// 不含风控裁决，不含本地订单真相：调用方先过 RiskPolicy，被拒的事件不进网关。
type BrokerGateway interface {
	// PlaceOrder 的幂等键是 (AccountID, ClientOrderID)：
	// 同 ID 同载荷 → 原 OrderAck + IdempotentReplay=true；
	// 同 ID 不同载荷 → ErrDuplicateOrder（不发送）；
	// 发出后超时/断连 → nil + ErrUnknownState（**禁止重发**）；
	// 未连接 → ErrChannelDown（不发送）；幂等映射不可用 → 不发送。
	PlaceOrder(ctx context.Context, req OrderRequest) (*OrderAck, error)
	// CancelOrder：已撤再撤 → 同终态 + nil；已成交 → ErrOrderNotCancelable；
	// 未知 → ErrOrderNotFound；超时 → ErrUnknownState。
	CancelOrder(ctx context.Context, req CancelRequest) (*OrderState, error)
	// QueryOrder 按 ClientOrderID 查；查不到但可能已送达 → OrderUnknown + nil。
	QueryOrder(ctx context.Context, clientOrderID string) (*OrderState, error)
	// QueryPositions 空仓返回 `[]`（不是 nil）；accountID 不匹配 → ErrAccountMismatch。
	QueryPositions(ctx context.Context, accountID string) ([]Position, error)
	// QueryAccount 返回柜台口径快照，不做本地对账裁决。
	QueryAccount(ctx context.Context, accountID string) (*AccountSnapshot, error)
	// QueryFills 是成交/费用审计面：`since` 含边界，按 (FilledAt, FillID) 升序，
	// FillID 稳定；它**不是**对账输入（对账走 SettlementQuerier）。
	QueryFills(ctx context.Context, accountID string, since time.Time) ([]Fill, error)
}

// SettlementQuerier 是对账输入（账户快照），与 BrokerGateway 分开：
// 对账 worker 只需要这一项，不需要订单生命周期。
type SettlementQuerier interface {
	// QuerySettlement 返回**快照口径**（现金/持仓/费用），不是逐笔成交；
	// 超时 → ErrChannelDown（对账不猜）；不支持 → ErrNotSupported。
	QuerySettlement(ctx context.Context, accountID string, asOf time.Time) (*AccountSnapshot, error)
}

// KillSwitch 是运维紧急清仓能力，单独成 port：不是所有通道都支持，
// 且语义（绕过 T+1、逐标的报告）与普通下单不同。
type KillSwitch interface {
	// EmergencyFlatten：空仓 → 空 Sold + nil（幂等）；每个 Sold 必须对应真实
	// 提交并确认的订单，未确认的进 Skipped；不支持 → ErrNotSupported。
	EmergencyFlatten(ctx context.Context, reason string) (*EmergencyFlattenResult, error)
}

// HealthChecker 供熔断/健康检查；失败必须返回 ErrChannelDown。
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}

// IdempotencyRecord 是 `ClientOrderID → ack` 的持久映射（落点 = `broker.order_acks`，ADR 0002）。
//
// `RequestFingerprint` 用于区分「重放」与「撞 ID 用错载荷」；`Ack.Status` 为空
// 表示**已占位但结果未知**（发出途中崩溃）——加载到这种记录必须返回
// ErrUnknownState，不得重发。
type IdempotencyRecord struct {
	RequestFingerprint string   `json:"request_fingerprint"`
	Ack                OrderAck `json:"ack"`
}

// AckStore 是幂等映射的持久化 port（由装配方注入；网关不直连任何表）。
//
// 判重范围是 (AccountID, ClientOrderID)。实现必须跨重启/跨进程持久；
// 任何实现层故障都要作为 error 冒泡，让网关翻译成
// ErrIdempotencyStoreUnavailable 并且**不发送**。
type AckStore interface {
	// Load 只读；用于重放与查单，不产生副作用。
	Load(ctx context.Context, accountID, clientOrderID string) (IdempotencyRecord, bool, error)
	// Reserve 原子占位（write-ahead）：created=true 表示**本次调用拿到发送权**；
	// created=false 返回已有记录，由调用方按指纹 / 回执分诊。
	//
	// 「查未命中 + 写入占位」必须是一步原子操作——分成 Load 再 Save
	// 会让两个并发同 ID 调用各自读到未命中，然后各发一笔（实测可复现）。
	// PG 实现用 `INSERT ... ON CONFLICT DO NOTHING RETURNING`。
	Reserve(ctx context.Context, accountID, clientOrderID, fingerprint string) (existing IdempotencyRecord, created bool, err error)
	// SaveAck 写回执。占位必须已由 Reserve 建立；没有占位就是实现/调用错误，
	// 必须报错而不是静默插入（否则等于绕过发送权判定）。
	SaveAck(ctx context.Context, accountID, clientOrderID string, rec IdempotencyRecord) error
	// Durable 报告实现是否跨重启持久。XTPTrader 的装配闸据此拒绝内存实现
	// （「无幂等键不接真钱」不能只靠装配方记得）。
	Durable() bool
}
