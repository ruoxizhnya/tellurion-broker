package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrAssemblyIncomplete 是 XTPTrader 构造期拒绝的哨兵（**不是** api-schema §2
// 的 12 个 wire code 之一——它只出现在装配路径，永远不会是调用结果）。
//
// 这是「无幂等键不接真钱」护栏的机械判据：dataflow.md §4-4 的三项装配前置
// （幂等映射 / 对账输入 / 健康检查）缺任何一个，NewXTPTrader 返回 error，
// 真钱适配器根本拿不到实例。
var ErrAssemblyIncomplete = errors.New("broker: xtp assembly rejected")

// XTPConfig 是 XTP 适配器的连接配置。
//
// 密钥**不进本结构**（R8）：凭证只从环境 / 密钥管理注入，随 X6c 全量实现
// 接入；今天骨架没有任何字段承载密钥，从形状上排除了明文落盘的可能。
type XTPConfig struct {
	AccountID string
	Host      string
	Port      int
}

func (c XTPConfig) validate() error {
	if c.AccountID == "" {
		return errors.New("account_id is required")
	}
	if c.Host == "" {
		return errors.New("host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port %d out of range", c.Port)
	}
	return nil
}

// XTPDeps 是真钱通道的装配前置（ARCHITECTURE.md §9.7 R6：缺一构造期拒绝）。
type XTPDeps struct {
	// Acks 是**持久**幂等映射（R2）：跨重启可用的 `ClientOrderID → ack`。
	// 注入 MemoryAckStore 等内存实现时构造不拦——持久性由装配方保证，这里
	// 只拦「忘了注入」。
	Acks AckStore
	// Settlement 是对账输入（R3）：没有真实 broker 侧快照就没有对账方案。
	Settlement SettlementQuerier
	// Health 是通道健康检查（熔断输入）。
	Health HealthChecker
}

// XTPTrader 是真钱通道适配器的 fail-closed 骨架（X6c 施工 ②）。
//
// 与 core 旧 skeleton 的本质区别（§9.1 F4）：**任何路径都不返回成功形状**——
// 没有连接就没有结论，全部调用返回 ErrChannelDown（通道不可用，不发送）；
// EmergencyFlatten 返回 ErrChannelDown，绝不伪造「已清仓」。
//
// 真实的下单 / 撤单 / 回报 / 心跳 / 重连 / 限流随 X6c 全量实现落地；在那之前
// 本类型存在的意义是把装配闸钉住：装不出不合规的真钱适配器。
type XTPTrader struct {
	cfg  XTPConfig
	deps XTPDeps
}

var (
	_ BrokerGateway     = (*XTPTrader)(nil)
	_ SettlementQuerier = (*XTPTrader)(nil)
	_ KillSwitch        = (*XTPTrader)(nil)
	_ HealthChecker     = (*XTPTrader)(nil)
)

// NewXTPTrader 构造 XTPTrader。装配前置缺一 → ErrAssemblyIncomplete（fail-closed，
// 不是 warning）；config 不合法同拒。
func NewXTPTrader(cfg XTPConfig, deps XTPDeps) (*XTPTrader, error) {
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%w: config: %v", ErrAssemblyIncomplete, err)
	}
	if deps.Acks == nil {
		return nil, fmt.Errorf("%w: persistent ack store is required (无幂等映射不接真钱)", ErrAssemblyIncomplete)
	}
	if deps.Settlement == nil {
		return nil, fmt.Errorf("%w: settlement querier is required (无对账方案不接真钱)", ErrAssemblyIncomplete)
	}
	if deps.Health == nil {
		return nil, fmt.Errorf("%w: health checker is required (无熔断输入不接真钱)", ErrAssemblyIncomplete)
	}
	return &XTPTrader{cfg: cfg, deps: deps}, nil
}

// AccountID 返回本适配器服务的账户（判 ErrAccountMismatch 用）。
func (x *XTPTrader) AccountID() string { return x.cfg.AccountID }

// down 是骨架的统一出口：通道不可用，无任何副作用（不写幂等映射、不发单）。
func down() error {
	return fmt.Errorf("%w: xtp adapter has no sdk link (X6c 未落地)", ErrChannelDown)
}

func (x *XTPTrader) PlaceOrder(ctx context.Context, req OrderRequest) (*OrderAck, error) {
	return nil, down()
}

func (x *XTPTrader) CancelOrder(ctx context.Context, req CancelRequest) (*OrderState, error) {
	return nil, down()
}

func (x *XTPTrader) QueryOrder(ctx context.Context, clientOrderID string) (*OrderState, error) {
	return nil, down()
}

func (x *XTPTrader) QueryPositions(ctx context.Context, accountID string) ([]Position, error) {
	if err := x.checkAccount(accountID); err != nil {
		return nil, err
	}
	return nil, down()
}

func (x *XTPTrader) QueryAccount(ctx context.Context, accountID string) (*AccountSnapshot, error) {
	if err := x.checkAccount(accountID); err != nil {
		return nil, err
	}
	return nil, down()
}

func (x *XTPTrader) QueryFills(ctx context.Context, accountID string, since time.Time) ([]Fill, error) {
	if err := x.checkAccount(accountID); err != nil {
		return nil, err
	}
	return nil, down()
}

// QuerySettlement 见 SettlementQuerier：对账不猜——通道不可用就报不可用，
// 不返回半个快照。
func (x *XTPTrader) QuerySettlement(ctx context.Context, accountID string, asOf time.Time) (*AccountSnapshot, error) {
	if err := x.checkAccount(accountID); err != nil {
		return nil, err
	}
	return nil, down()
}

func (x *XTPTrader) EmergencyFlatten(ctx context.Context, reason string) (*EmergencyFlattenResult, error) {
	return nil, down()
}

// HealthCheck 如实报告「不可用」：熔断输入拿到的是真状态，不是乐观假设。
func (x *XTPTrader) HealthCheck(ctx context.Context) error {
	return down()
}

// checkAccount 先判账户归属（配置错误离线可判，不依赖通道），再让调用落到 down()。
func (x *XTPTrader) checkAccount(accountID string) error {
	if accountID != x.cfg.AccountID {
		return fmt.Errorf("%w: %q is not served by this adapter (%q)", ErrAccountMismatch, accountID, x.cfg.AccountID)
	}
	return nil
}
