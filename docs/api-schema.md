---
status: active
last-verified: 2026-10-23
verified-by: R2 裁决 + 持久幂等映射落地（ADR 0002 / X6c 幂等持久化）
---

# API Schema — tellurion-broker

> **契约地位**：**Go port（进程内）**，不是 HTTP。
> `BrokerGateway` 按"将来可远程"设计（DTO 序列化友好），但当前与实盘运行域同进程 —— 下单是延迟敏感路径，独立进程 = 每笔多一次网络跳。
> 本文件是**本仓对外接口的唯一真相源**。代码与它不一致时，先判断谁错：契约错 → 改本文 + 立 ADR；实现错 → 改代码。
> 跨仓消费方（core / agent / ui / 其他服务）按本文件编码，**不读本仓实现**。
> **今天 vs 目标**：本仓 `internal/gateway` 已落本文四个 port + DTO + 12 哨兵 + 内存 `Mock` + `XTPTrader` fail-closed 骨架（装配闸：缺持久幂等映射 / 对账 / 健康检查任一即构造期拒绝；已装配后全方法恒 `channel_down`，零 fake success），`internal/reconcile` 已落快照口径对账纯函数，`internal/ackstore` 已落持久幂等映射 `broker.order_acks`（2026-10-23，ADR 0002），**未接 core、无 XTP SDK 链路**；core 侧仍有 `LiveTrader`（Mock / XTP）与 `Broker`（`SimulatedBroker`）两套旧面待退役（R4）。现状矩阵与取舍见 [`ARCHITECTURE.md`](ARCHITECTURE.md) §9.8，八条实测事实见 §9.1（基线 core `dbe4c68`）。
> **port 的落点**：契约位是 **contracts**（`BrokerGateway` / 新 DTO 不随 S1 批量迁，随 X6a 定稿）；本仓是实现的物理归属，不是常驻服务（`tellurion:docs/design/qt-decoupling/repo-inventory.md` §1.2）。

## 1. 接口面（目标 port）

```go
// BrokerGateway 是订单生命周期 + 查询面。不含风控裁决，不含本地订单真相。
type BrokerGateway interface {
    PlaceOrder(ctx context.Context, req OrderRequest) (*OrderAck, error)
    CancelOrder(ctx context.Context, req CancelRequest) (*OrderState, error)
    QueryOrder(ctx context.Context, clientOrderID string) (*OrderState, error)
    QueryPositions(ctx context.Context, accountID string) ([]Position, error)
    QueryAccount(ctx context.Context, accountID string) (*AccountSnapshot, error)
    QueryFills(ctx context.Context, accountID string, since time.Time) ([]Fill, error)
}

// SettlementQuerier 是对账输入（账户快照），与 BrokerGateway 分开：
// 对账 worker 只需要这一项，不需要订单生命周期。
type SettlementQuerier interface {
    QuerySettlement(ctx context.Context, accountID string, asOf time.Time) (*AccountSnapshot, error)
}

// KillSwitch 是运维紧急清仓能力，单独成 port：不是所有通道都支持，
// 且语义（绕过 T+1、逐标的报告）与普通下单不同。
type KillSwitch interface {
    EmergencyFlatten(ctx context.Context, reason string) (*EmergencyFlattenResult, error)
}

// HealthChecker 供熔断/健康检查；失败必须返回 ErrChannelDown。
type HealthChecker interface {
    HealthCheck(ctx context.Context) error
}
```

DTO 形状与逐字段判据见 [`ARCHITECTURE.md`](ARCHITECTURE.md) §9.3；方法级「今天 vs 契约位」见 §9.2。**`OrderStatus` 是订单状态八值**（`pending` / `accepted` / `partially_filled` / `filled` / `cancelled` / `rejected` / `expired` / `unknown`），**不是 K9 的 run 三值**。

### 实现矩阵（今天）

| 实现 | port | 用途 | 现状 |
|---|---|---|---|
| `Mock`（`internal/gateway`） | `BrokerGateway` + `SettlementQuerier` + `KillSwitch` + `HealthChecker` | 本仓的新 MockTrader | **可用**（X6a）；注入式 `AckStore`，nil 即不发送；无价格不成交 |
| `XTPTrader`（`internal/gateway`，本仓） | 四个 port + `KillSwitch` | 真实柜台（X6c） | **fail-closed 骨架 + 装配闸**：构造期缺 `AckStore` / `SettlementQuerier` / `HealthChecker` 任一，或 `AckStore.Durable()==false` → `ErrAssemblyIncomplete`（构造路径哨兵，**不是第 13 个 wire code**）；已装配后所有方法恒 `channel_down`；SDK 从零实现未做 |
| `PgStore`（`internal/ackstore`） | `AckStore`（装配 SPI，非四个 wire port） | 持久幂等映射（R2 已裁） | **可用**：`broker.order_acks`，原子 `Reserve`（`ON CONFLICT DO NOTHING`）+ 回执写回 + `Durable=true`；PG 集成测试按 `BROKER_TEST_DSN` 运行 |
| `MemoryAckStore`（`internal/gateway`） | `AckStore` | 开发 / 测试 | 可用；**`Durable=false`**，进不了 XTP 装配闸 |
| `Reconcile`（`internal/reconcile`） | 纯函数（非 port） | 快照口径对账（X6b） | **可用**；输入两侧共用 `AccountSnapshot`，七类差异 × 三级严重度，差异注入验收在册；真实 broker 侧快照归 X6c 全量 |
| `MockTrader` | `LiveTrader`（旧） | 开发 / 回放 / 压测 | 可用；**无编译期断言**，且不做板块申报规则校验 |
| `XTPTrader`（旧，core `pkg/live/broker/xtp`） | `LiveTrader`（旧） | 真实柜台 | **offline skeleton**：`Connect`/`SubmitOrder`/`CancelOrder` 恒返回「SDK not linked」；`EmergencyFlatten` 伪造成功 |
| `SimulatedBroker` | `Broker`（旧，另一套面） | `LiveEngine` 测试替身 | 生产零调用方（AUD-34）；去留见 §9.7 R4 |

### 校验归属（必须分开，不能混）

| 校验 | 归属 |
|---|---|
| 最小单位、委托类型、柜台限流、**交易所申报数量规则**（100 股整数倍 / 科创板 200 股起等） | **网关内**（通道侧纯函数，两个实现共用） |
| 数量上限、集中度、T+1、止损 | **RiskPolicy**（进程内裁决，规则源在 risk 服务） |

> ⚠️ 现状与分界表不一致：申报数量规则今天住在 `risk.ValidateOrderQuantity`（core `pkg/risk/lot.go:136`），只有 XTP 调它（`pkg/live/broker/xtp/xtp.go:387`），Mock 不调。规则**归通道侧、代码位置迁走**见 §9.7 R5；无论迁到哪，**两个实现必须调用同一份**。网关**不 import 风控裁决**。

## 2. 错误码与 fail-closed 语义

> **12 个 code = 12 个 Go 哨兵**。调用方只 `errors.Is` 契约哨兵；适配器私有错误（如 XTP `ErrOffline`）必须在边界翻译成本表之一。

| code | Go 哨兵 | 含义（判据） | 发送了吗 | 调用方行为 |
|---|---|---|---|---|
| `order_rejected` | `ErrOrderRejected` | 柜台**明确**拒单，回执已拿到 | 是 | **不重试**；`OrderAck{Status: rejected, Code, Reason}` 与 error **同时非 nil**（回执要留痕），其余情况 ack 为 nil |
| `unknown_state` | `ErrUnknownState` | 已发出，但**结果未知**（超时 / 发送后断连） | 是（可能） | **禁止重发**；只能 `QueryOrder` / 对账；查不到 → 告警 + 人工介入（真钱不做自动补偿） |
| `channel_down` | `ErrChannelDown` | 未连接 / 熔断中 / 健康检查失败 | 否 | 熔断：停止下单 + 告警；**不静默重试** |
| `duplicate_order` | `ErrDuplicateOrder` | 同 `ClientOrderID`、**不同载荷** | 否 | fail-loud；绝不发第二笔；调用方查单或换 ID（换 ID 前先确认原单不存在） |
| `order_not_found` | `ErrOrderNotFound` | 网关**能证明**从未发送过该 ID | 否 | 可按新单处理（由调用方决定）；不得据此推断柜台侧不存在 |
| `order_not_cancelable` | `ErrOrderNotCancelable` | 已成交 / 已过期 / 已终态，不可撤 | 否 | 查单取终态；不重试撤单 |
| `invalid_order` | `ErrInvalidOrder` | 参数不合法 / 违反申报数量规则 | 否 | 修参数后重新提交（**新** `ClientOrderID`） |
| `rate_limited` | `ErrRateLimited` | 柜台限流，且**确定未送达** | 否 | 退避后可用**同一** `ClientOrderID` 重试 |
| `not_supported` | `ErrNotSupported` | 通道不支持该能力（如 XTP 未实现 `QueryFills`） | 否 | 降级或拒绝；**禁止**伪造成功形状 |
| `account_mismatch` | `ErrAccountMismatch` | `accountID` 不属于本通道 | 否 | 配置错误，fail-loud |
| `insufficient_buying_power` | `ErrInsufficientBuyingPower` | 柜台明确返回买力不足 | 是（已拒绝） | 不重试；告警 |
| `idempotency_store_unavailable` | `ErrIdempotencyStoreUnavailable` | 幂等映射（持久化）不可用 | 否 | **不发送**；这是可用性事故，不是下单失败 |

**最重要的一条**：超时 = 状态未知，不是"失败"。盲目重发是实盘事故的经典来源。
**第二重要的一条**：未实现的能力返回 `not_supported`，**绝不返回成功形状**（今天 XTP `EmergencyFlatten` 反例见 §9.1 F4）。

## 3. 幂等性与重试

- **`PlaceOrder` 幂等键 = `ClientOrderID`**（由调用方生成、重试不变；网关不改写、不生成）。判重范围 = `(AccountID, ClientOrderID)`。
- **发送权由 `AckStore.Reserve` 原子判定**：`Load` 再 `Save` 的两步形状会让并发同 ID 各自读到未命中、各发一笔（实测可复现）；PG 实现用 `INSERT ... ON CONFLICT DO NOTHING RETURNING`，内存实现用同一临界区。
- 同 ID **同载荷** → 返回原 `OrderAck`（`IdempotentReplay=true`），不发送；同 ID **不同载荷** → `duplicate_order`，不发送。
- **重放还原当初的结论**：当初被柜台拒过的单，重放时仍返回**同一个 error**（`order_rejected` / `insufficient_buying_power`）与那份 ack——被拒的单重放不能变成"受理成功"。
- 幂等映射必须**持久**（跨重启、跨进程）：不可用 → `idempotency_store_unavailable`，**不发送**。落点已裁为 `broker.order_acks`（[ADR 0002](adr/0002-幂等映射落-broker-写前日志.md)、[`db-schema.md`](db-schema.md) §1.1）；`AckStore.Durable()` 让 XTP 装配闸拒绝内存实现。
- `CancelOrder` 幂等：已撤再撤 → 同终态 `OrderState` + `nil`；已成交 → `order_not_cancelable`。
- `QueryOrder` / 查询面只读，天然幂等。
- 重试只允许在**确定未送达**时进行（`rate_limited` / 显式失败）；`unknown_state` 必须先 `QueryOrder`。
- `EmergencyFlatten` 重入不重复卖：已无持仓 → 空 `Sold` + `nil`；每个 `Sold` 必须对应真实提交并确认的订单，未确认进 `Skipped`。
  - 清仓单的 `ClientOrderID` 必须**区分轮次**（同一笔持仓重入用同一个 ID；卖掉后重建仓要用新 ID）——否则会重放上一轮的已成交回执，把没下单的持仓报成清仓成功。
  - 重放来的那一单**不算**本次清仓的凭据 → 只能进 `Skipped`。

## 4. 版本与兼容

| 面 | 稳定性 | 变更规则 |
|---|---|---|
| 四个 port 的方法集 | 新增（S1 后 frozen） | 加方法 = MAJOR；新能力走独立 port = MINOR |
| DTO 字段 | stable | 加字段 = MINOR；改名 / 删除 / 改单位 = MAJOR |
| `OrderStatus` 八值 | stable | 加非终态 = MINOR；加终态或改 `unknown` 语义 = MAJOR |
| 12 个错误码 | stable | 加新哨兵 = MINOR；改某个 code 的可重试性 / 含义 = MAJOR |
| `ClientOrderID` 幂等语义 | frozen | 派生 / 重放语义改 = MAJOR |
| `QuerySettlement` 快照口径 | frozen | 从快照改逐笔 = MAJOR |

- 柜台适配器可独立升级，**不得**把券商特有字段泄漏进契约（`OrderReq.BusinessType` 留在 XTP 适配器内部）。
- 旧 `LiveTrader` / `Broker` 标 **deprecated**，随 X6a 迁移删除（core 内部，MAJOR）。

## 5. 不提供什么（边界）

- 不做风控裁决（`RiskPolicy.CheckOrder` 在 core / risk，网关不 import）
- 不做策略
- 不做订单簿 / 撮合（回测撮合在 core）
- 不持有订单真相（只透传柜台状态；订单持久化在 core `quant`，最终归属 U2）
- 不做资金划转
- 不持有对账结论（对账留痕归 `tellurion-risk`，网关只提供 `QuerySettlement` 快照）
