---
status: active
last-verified: 2026-10-23
verified-by: R2 裁决 + 持久幂等映射落地（ADR 0002）；X6d 按同日裁决暂缓
---

# 架构 — tellurion-broker

## 1. 定位

交易通道网关：`XTPTrader` / `MockTrader` = **同一个 port 的两个实现**。

它出仓的时机排在最后（X6）：单账户单实例阶段，独立进程的收益（多策略共用通道、通道独立重启）兑现不了，而下单是延迟敏感路径。所以**先把 port 定好，实现先放进程内**。

## 2. 分层

```
core（订单意图）
      ↓  BrokerGateway（port，进程内）
适配器层
  ├── MockTrader  → 内存 / 文件
  └── XTPTrader   → 券商柜台（最小单位/委托类型/限流在适配器内）
      ↓
成交回报 / 持仓 ──► core 的 quant（实盘运行域；将来建 exec 镜像须先立 ADR）
      └── QuerySettlement（账户快照）──► 对账（risk 留痕）
```

## 3. 组件职责

| 组件 | 职责 | 不做 |
|---|---|---|
| `BrokerGateway` port | 下单 / 撤单 / 查单 / 持仓 / 成交 | 不做风控 |
| Mock 实现 | 开发与回放 | 不模拟复杂柜台规则 |
| XTP 适配器 | 柜台协议 + 归一化 | 不决定"能不能下" |
| 对账输入 | `QuerySettlement`（账户快照） | 不做对账判断（归 risk） |

## 4. 三份契约（功能的边界就在这三份文件里）

| 维度 | 文件 | 回答什么问题 |
|---|---|---|
| 数据形状 | [`db-schema.md`](db-schema.md) | 本仓拥有哪些表？谁能写？ |
| 接口形状 | [`api-schema.md`](api-schema.md) | 对外暴露什么？错误码与幂等怎么做？ |
| 流向与顺序 | [`dataflow.md`](dataflow.md) | 数据怎么流？哪里 fail-closed？ |

## 5. 依赖方向

```
contracts（BrokerGateway / OrderRequest / Fill）+ kit（logging/http）
        ↑
 tellurion-broker ◄──(进程内 port)── core
        └──► 券商柜台
```

## 6. 关键决策

- [`adr/0001-先-mock-再真钱.md`](adr/0001-先-mock-再真钱.md)

## 7. 非目标（明确不做）

- 不做风控、不做策略、不做撮合
- 不持有订单真相
- 不做资金划转
- 现在不做独立进程（触发条件：多账户共用通道 / 通道级独立高可用）。X6d 于 2026-10-23 裁决**暂缓**，见 `TASKS.md`。

## 8. 分期与状态

| 阶段 | 内容 | 前置 |
|---|---|---|
| X6a | port 定义 + MockTrader 🔶（2026-10-23：port / DTO / 内存 Mock / `ClientOrderID` 幂等已落 `internal/gateway`；**未接 core**） | 无 |
| X6b | 对账口径定稿 🔶（2026-10-23 施工 ②：快照口径纯函数 + 差异注入验收已落 `internal/reconcile`；真实 broker 侧 `QuerySettlement` 归 X6c 全量） | risk 的 `QueryFills` 契约 |
| X6c | XTPTrader 🔶（2026-10-23 施工 ②：装配闸 + fail-closed 骨架已落 `internal/gateway/xtp.go`——构造期强制注入幂等 / 对账 / 健康，缺一 error，已装配后全方法 `channel_down`；SDK 从零实现未做） | 对账 + 幂等 + 熔断三项就位 |
| X6d | 独立进程（可选）——**2026-10-23 裁决：暂缓**，保持非目标；触发条件（多账户共用通道 / 通道级独立高可用）未到 | — |

## 9. 接口对表（M8 前置 · ✅ 2026-10-22）

> **本节是什么**：`tellurion:docs/TASKS.md`「五、X 线」公共前置 ② 要求的**接口对表本体**（模板 = `tellurion:docs/design/qt-decoupling/data-service-contract-and-ownership.md` §4.1）。逐个 port / DTO / 调用面列 **签名 + 错误语义 + 版本策略**；缺一列就是没对完表。
> **本仓不是 HTTP 服务**：`api-schema.md` 的「接口面」是 **Go port（进程内）**，所以本节 C 组按**调用面 × 错误/幂等矩阵**组织，不按 HTTP 端点。
> **实测基线**：core `dbe4c68`（2026-10-22）。行号一律取 `git show HEAD:<path>`。复核：`git -C ../tellurion show HEAD:<path> | sed -n '<line>p'`。
> **列口径**：「今天」= 源码实测；「契约位」= contracts 的落点（`BrokerGateway` / 新 DTO **不随 S1 批量迁**，contracts 已登记「未定形 / S1 不落」，随 X6a 定稿）；「版本策略」按 QT-19 的 MAJOR / MINOR / PATCH 判定。
> **归属口径**：本仓是 **port 实现的物理归属，不是常驻服务**（`tellurion:docs/design/qt-decoupling/repo-inventory.md` §1.2）：柜台适配（XTP / Mock）与日终对账写在本仓，**port 由消费侧（core）定义最终形状、DTO 落 contracts**，下单与裁决留在 core。
> **M5 口径**：六仓 ≠ 六进程（`tellurion:docs/design/qt-decoupling/repo-inventory.md` §1.2）。本仓今天不升格容器；**X6a/X6b 只在 core 进程内接 port**，X6d 独立进程是多账户/高可用的触发式选项，不是本单目标。
> **D1/M8 口径**：真实券商（XTP）**今天未接**；`XTPTrader` 是 offline skeleton。本节把「两实现的语义」写成目标契约，并如实标出 skeleton 与目标的差距（F4/F8）。

### 9.1 八条前置事实（每条都会改变施工判断）

| # | 事实 | 取证（core `dbe4c68`） | 对施工的后果 |
|---|---|---|---|
| F1 | **今天不是一个 port 两个实现，而是两个 port、三个实现**：`LiveTrader`（Mock / XTP）与 `Broker`（`SimulatedBroker`）互不相干；目标 `BrokerGateway` 在 Go 里 **零命中**。 | `LiveTrader`（`pkg/live/trader.go:42`）由 `MockTrader`（`pkg/live/mock_trader.go:207`）与 `XTPTrader`（`pkg/live/broker/xtp/xtp.go:537` 的编译期断言）实现；`Broker`（`pkg/live/engine.go:39`）由 `SimulatedBroker`（`pkg/live/simulated_broker.go:15`，`SubmitOrder` 在 `pkg/live/simulated_broker.go:80`）实现。`BrokerGateway` 在 `pkg`/`cmd` **零命中**；contracts 仓仍标「未定形」（`tellurion-contracts:docs/API.md` §5.2）。Mock 侧**没有** `_ LiveTrader = (*MockTrader)(nil)` 编译期断言（只有 XTP 有），删 Mock 的方法不会被契约钉住。消费面也不止一处：`LiveBridge`（`pkg/backtest/execution/live_bridge.go:138`）与 `PaperSession`（`pkg/live/paper_session.go:381`）都直接调 `SubmitOrder`。 | 目标 `BrokerGateway` 是**新 port**，不是给 `LiveTrader` 改个名。迁移要同时处理三个实现与两条消费链（engine→LiveBridge、paper→PaperSession）；`SimulatedBroker` 的去留是 R4。**先加 Mock 的编译期断言**（一行），否则「两实现同 port」只是文档说法。 |
| F2 | **没有上游幂等键**：`SubmitOrder` 的签名里没有 `ClientOrderID`，订单号由网关/柜台生成；重复提交 = 新订单；撤单不幂等。 | `LiveTrader.SubmitOrder` 六参（`pkg/live/trader.go:42`，实现 `pkg/live/mock_trader.go:207`）；Mock 内部生成 `id.OrderID()`（`pkg/live/mock_trader.go:280`、`pkg/id/order.go:48`），重复提交只会在 `orders` map 里多一条；`CancelOrder` 对非 active 状态直接报错（`pkg/live/mock_trader.go:380`），不是「已撤再撤返回同状态」；XTP 的 `CancelOrder` 同理（`pkg/live/broker/xtp/xtp.go:401`）。 | 目标 `OrderRequest` **必须带 `ClientOrderID`**（上游生成、重试不变），`OrderAck` 回带 `{ClientOrderID, BrokerOrderID}`。同 ID 同载荷 → 原 ack（幂等重放）；同 ID 不同载荷 → `ErrDuplicateOrder` **fail-loud**，绝不发第二笔。**这是 X6a 的第一块砖**，不先定它，后面的超时/重试语义全部不成立。 |
| F3 | **没有可区分的错误分类**：`ErrUnknownState` / `ErrChannelDown` 作为错误哨兵在 Go 里 **零命中**，`OrderRejected` 作为**错误类型**同样零命中（同名命中只有风控拒绝事件 `msgbus.TopicExecOrderRejected`，`pkg/msgbus/topics.go:143`）；XTP 只有一组适配器错误，Mock 只有裸 `fmt.Errorf`。 | XTP 错误族：`ErrOffline` / `ErrNotConnected` / `ErrInvalidConfig` / `ErrOrderNotFound` / `ErrInsufficientBuyingPower` / `ErrSymbolNotSubscribed`（`pkg/live/broker/xtp/xtp.go:54`）；Mock 的拒单/撤单失败是裸字符串（`pkg/live/mock_trader.go:380`）。没有任何地方能回答「这笔是柜台明确拒了，还是超时后状态未知」——正是实盘最危险的两种分支。 | 目标错误集冻结 12 个稳定 code（§9.4 / `api-schema.md` §2），核心三分：**明确拒绝**（`order_rejected` / `invalid_order` / `insufficient_buying_power`，不重试）、**状态未知**（`unknown_state`，必须先查单）、**通道不可用**（`channel_down`，熔断不发送）。适配器自己的错误（`ErrOffline` 等）必须在边界**翻译**成契约错误，调用方只 `errors.Is` 契约哨兵。 |
| F4 | **XTP 是 offline skeleton，不是第二个可用实现；它的 `EmergencyFlatten` 还会伪造成功**。 | `Connect` 在非 offline 模式下也直接返回「SDK not linked」（`pkg/live/broker/xtp/xtp.go:308`、`pkg/live/broker/xtp/xtp.go:324`）；`SubmitOrder` 永远返回同样错误（`pkg/live/broker/xtp/xtp.go:346`、`pkg/live/broker/xtp/xtp.go:397`）；`CancelOrder` 同理（`pkg/live/broker/xtp/xtp.go:401`）；`GetOrder` 只读内存缓存（`pkg/live/broker/xtp/xtp.go:432`）；`EmergencyFlatten` 遍历持仓把每一条直接塞进 `Sold`，**没有提交任何订单**（`pkg/live/broker/xtp/xtp.go:480`、`pkg/live/broker/xtp/xtp.go:508`），返回 nil error。 | X6c 不是「接一下 SDK」：它是**从零实现**（下单/撤单/查单/回报/心跳/重连/限流）。`EmergencyFlatten` 的 fake success 是**资金安全缺陷**：kill-switch 会显示「已清仓」而实际没卖。目标：未实现的能力必须返回 `not_supported` / `channel_down`，**禁止**返回成功形状。 |
| F5 | **校验归属混用，且两个实现的规则不一致**：XTP 调 `risk.ValidateOrderQuantity` 做板块申报数量校验，Mock 完全不校验板块规则（只查 `qty > 0`）。 | `risk.ValidateOrderQuantity` 调用在 `pkg/live/broker/xtp/xtp.go:387`；规则本体在 `pkg/risk/lot.go:136`；Mock 只查 `quantity <= 0`（`pkg/live/mock_trader.go:211`）。`OrderReq` 里的 `BusinessType`（融资/期货/期权/港股通）是 XTP 专有字段（`pkg/live/broker/xtp/xtp.go:213`、`pkg/live/broker/xtp/xtp.go:223`）。 | 这不是「通道裁决 vs 风控裁决」两次调用的混用——`ValidateOrderQuantity` 本身是**交易所申报规则**，按分界表归通道侧，只是**住在 risk 包**。目标：把规则移到通道侧的纯函数（R5），**两个实现都调用同一份**；`RiskPolicy` 只管数量上限/集中度/T+1/止损，网关**不 import 风控裁决**。`BusinessType` 留在 XTP 适配器，**不进契约**。 |
| F6 | **对账的真实输入是 `QuerySettlement` 快照，不是 `QueryFills`；且今天的生产对账是「自己对自己」的零差异自检**。 | `BrokerQuerier.QuerySettlement(ctx, accountID, asOf)`（`pkg/live/reconciliation/reconciliation.go:246`、`pkg/live/reconciliation/reconciliation.go:250`）；纯函数 `Reconcile(local, broker AccountSnapshot, cfg)`（`pkg/live/reconciliation/reconciliation.go:276`）；生产装配把**同一个 `traderAccountView` 同时当 local 和 broker**（`cmd/analysis/setup_reconciliation.go:124`、`cmd/analysis/setup_reconciliation.go:127`），所以自检差异恒为零；`QueryFills` 在 Go 里 **零命中**；`FeeSnap` 恒零（`cmd/analysis/setup_reconciliation.go:107`）。XTP **没有**实现 `BrokerQuerier`（只有 `LiveTrader` 断言，`pkg/live/broker/xtp/xtp.go:537`）。 | 设计文档 §5/§6 的「`QueryFills → 对账`」与代码不符：对账口径是**账户快照**（现金/持仓/费用），不是逐笔成交。目标 `SettlementQuerier.QuerySettlement` 必须由真实券商适配器实现，且验收里必须有**人为注入差异**的腿（不能只跑零差异自检）。`QueryFills` 保留给成交/费用审计，不是对账输入。X6b 的「对账口径定稿」因此要改成「快照口径 + 真实 broker 侧」。 |
| F7 | **DTO 全在 core，且混着执行层语义与券商私有字段**：`OrderResult` / `PositionInfo` / `AccountInfo` 是 `pkg/live` 的；`domain.Order` 带 exec-algo / 止损 / 跟踪字段；`OrderReq` 带 XTP `BusinessType`。 | `OrderResult`（`pkg/live/trader.go:141`）、`AccountInfo`（`pkg/live/trader.go:168`）、`PositionInfo`（`pkg/live/trader.go:178`）；`domain.Order`（`pkg/domain/types.go:153`）的 `ExecAlgo` / `ExecSlices` / `ExecProfile`（`pkg/domain/types.go:171-176`）与 `StopPrice` / `TrailAmount` / `TrailPercent` / `HighWaterMark`（`pkg/domain/types.go:179-182`）；`domain.Trade`（`pkg/domain/backtest.go:48`）；`OrderRecord`（`pkg/live/order_store.go:20`）；`OrderReq` + `BusinessType`（`pkg/live/broker/xtp/xtp.go:213`、`pkg/live/broker/xtp/xtp.go:223`）。订单状态是裸字符串（`pkg/live/trader.go:149`）。 | 目标 `OrderRequest` 只保留**通道需要的字段**（账户/标的/方向/类型/数量/限价/ClientOrderID），**不带** exec-algo / 止损 / 跟踪 / `BusinessType`；`OrderStatus` 是**订单状态枚举**（`pending/accepted/partially_filled/filled/cancelled/rejected/expired/unknown`），**不是 K9 的 run 三值**。`PositionInfo.QuantityToday/Yesterday` 是本地 T+1 记账，不是柜台事实；对外只出 `AvailableQuantity`。 |
| F8 | **XTP 的装配前置（对账 + 幂等 + 熔断）没有被任何构造期检查钉住**：`NewXTPTrader` 只校验 IP/端口/账户/密码。 | `NewXTPTrader`（`pkg/live/broker/xtp/xtp.go:262`）→ `Config.Validate`（`pkg/live/broker/xtp/xtp.go:153`：IP/Port/AccountID/Password）；没有 reconciliation / idempotency / circuit-breaker 参数或检查。ADR 0001 ② 与 `dataflow.md` §4-4 都写「三项缺一不可装配」，今天只是文字。 | X6c 的装配入口必须 **fail-closed**：没有 `SettlementQuerier`、没有持久幂等映射、没有通道健康/熔断注入 → 构造期拒绝（返回 error，不是 warning）。这条不做，真钱路径就没有闸。 |

### 9.2 A 组 · port 与消费方

**A-1 目标 port（契约位 contracts；X6a 定稿时落，不随 S1 批量迁。本仓只放实现，不定义 port）**

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

**A-2 方法级对表**

| 方法 | 今天 | 契约位 | 错误语义（本表冻结） | 版本策略 |
|---|---|---|---|---|
| `PlaceOrder` | `LiveTrader.SubmitOrder(ctx, symbol, direction, orderType, qty, price)`（`pkg/live/trader.go:42`）——**无 ClientOrderID**，Mock 立即成交（`pkg/live/mock_trader.go:207`） | contracts | `ClientOrderID` 必填；同 ID 同载荷 → 原 `OrderAck`（幂等重放，**并还原当初的错误**：被拒过的单重放仍返回 `ErrOrderRejected` / `ErrInsufficientBuyingPower`）；同 ID 不同载荷 → `ErrDuplicateOrder`；柜台明确拒 → 非 nil `OrderAck{Status: rejected}` + `ErrOrderRejected`；发出后超时/断连 → `nil` + `ErrUnknownState`（**禁止重发**）；未连接 → `ErrChannelDown`（不发送）；规则不合法 → `ErrInvalidOrder`；买力不足 → `ErrInsufficientBuyingPower`；幂等映射不可用 → `ErrIdempotencyStoreUnavailable`（不发送）。判重域 = `(本通道服务的账户, ClientOrderID)`——与本地订单索引同口径 | 新 port = MINOR；签名/幂等语义改 = MAJOR |
| `CancelOrder` | `LiveTrader.CancelOrder(ctx, orderID)`；已成交/已撤再撤报错（`pkg/live/mock_trader.go:380`） | contracts | 已撤再撤 → 同终态 `OrderState` + `nil`（幂等）；已成交 → `ErrOrderNotCancelable`；未知 → `ErrOrderNotFound`；超时 → `ErrUnknownState`；未连接 → `ErrChannelDown` | 语义变更（从报错→幂等）= MAJOR |
| `QueryOrder` | `LiveTrader.GetOrder(ctx, orderID)` 只读内存（Mock `pkg/live/mock_trader.go:398`，XTP `pkg/live/broker/xtp/xtp.go:432`） | contracts | 按 `ClientOrderID` 查；未知 → `ErrOrderNotFound`；通道不可用 → `ErrChannelDown`；查不到但可能已送达 → `OrderState{Status: unknown}` + `nil`（调用方据此继续查/对账，**不重发**） | 新增 = MINOR |
| `QueryPositions` | `GetPositions(ctx)`（无 account；Mock `pkg/live/mock_trader.go:432`，XTP `pkg/live/broker/xtp/xtp.go:448`） | contracts | `accountID` 不匹配 → `ErrAccountMismatch`；通道不可用 → `ErrChannelDown`；空仓 → `[]`（不是 nil） | 新增 account 参数 = MINOR（新 port） |
| `QueryAccount` | `GetAccount(ctx)`（Mock `pkg/live/mock_trader.go:448`，XTP `pkg/live/broker/xtp/xtp.go:464`） | contracts | 同 `QueryPositions`；返回的现金/市值是**柜台口径**，与本地账的差异由对账处理，网关不做裁决 | 新增 = MINOR |
| `QueryFills` | **零实现**（`QueryFills` 在 Go 里零命中） | contracts | 只读；`since` **含**边界；按 `(FilledAt, FillID)` 升序；`FillID` 稳定，跨调用不重复；`accountID` 不匹配 → `ErrAccountMismatch`；不支持 → `ErrNotSupported` | 新增 = MINOR |
| `QuerySettlement` | `reconciliation.BrokerQuerier.QuerySettlement`（`pkg/live/reconciliation/reconciliation.go:250`），生产用同一 trader 自检（`cmd/analysis/setup_reconciliation.go:127`） | contracts | 返回**账户快照**（现金/持仓/费用），不是逐笔成交；`accountID` 不匹配 → `ErrAccountMismatch`；不支持 → `ErrNotSupported`；超时 → `ErrChannelDown`（对账不猜） | 新增 = MINOR；口径从快照改逐笔 = MAJOR |
| `EmergencyFlatten` | `LiveTrader.EmergencyFlatten`（Mock 真卖 `pkg/live/mock_trader.go:509`；XTP **伪造 Sold** `pkg/live/broker/xtp/xtp.go:508`） | contracts（`KillSwitch`） | 空仓 → 空 `Sold` + `nil`（幂等）；每个 `Sold` 必须对应**真实提交并确认**的订单，未确认的进 `Skipped`；**重放来的单不算本次清仓凭据**；清仓单 ID 必须区分轮次（同一笔持仓重入同 ID、卖掉后重建仓换 ID）；不支持 → `ErrNotSupported`（**禁止**伪造成功）；通道不可用 → `ErrChannelDown` | 新增 = MINOR；fake success 修复 = MAJOR（行为变更） |
| `HealthCheck` | `LiveTrader.HealthCheck`（Mock 恒 nil `pkg/live/mock_trader.go:192`；XTP offline/未就绪返回错误 `pkg/live/broker/xtp/xtp.go:295`） | contracts（`HealthChecker`） | 就绪 → `nil`；未连接/offline/断连 → `ErrChannelDown`；**不得**返回「健康」而实际不能下单 | 新增 = MINOR |
| `RiskPolicy.CheckOrder` | core/risk 的进程内裁决（`pkg/risk/interfaces.go:69`） | core（fusion 前）；**网关不调用、不 import** | 调用方在 `PlaceOrder` **之前**裁决；被拒 → `exec.order_rejected` 事件，**不进网关**；网关的错误集里没有「风控拒绝」 | 不改（QT-42 已对表） |
| `LiveTrader` / `Broker`（今天） | `pkg/live/trader.go:42` / `pkg/live/engine.go:39` | core-only（迁移期） | `LiveTrader` 的 `SubmitOrder`/`GetOrder`/`GetPositions`/`GetAccount`/`EmergencyFlatten` 语义被目标 port 吸收；`Broker`（`Connect`/`Disconnect`/`SubmitOrder(domain.Order)`/…）是 `LiveEngine`/`SimulatedBroker` 的旧面，生产零调用方（AUD-34） | 删除 = MAJOR（core 内部，随 X6a 迁移） |
| `OrderStore`（今天） | `pkg/live/order_store.go:35` | core-only | 订单真相的持久化面，**不属本仓**；幂等映射**不复用它**（R2 已裁：独立表 `broker.order_acks`，ADR 0002） | 不改 |

### 9.3 B 组 · DTO 与状态词表

| DTO / 字段 | 今天 | 契约位 | 形状与判据 | 版本策略 |
|---|---|---|---|---|
| `OrderRequest` | `LiveTrader.SubmitOrder` 的六个裸参数（`pkg/live/trader.go:42`） | contracts | `{ClientOrderID, AccountID, Symbol, Side(buy/sell), OrderType(market/limit), Quantity, LimitPrice}`；`ClientOrderID` 上游生成、重试不变；**不含** `BusinessType` / `ExecAlgo` / `StopPrice` / `Trail*` / `HighWaterMark` | 新 DTO = MINOR；字段改名 = MAJOR |
| `OrderAck` | `OrderResult`（`pkg/live/trader.go:141`）混了 ack + fill + status | contracts | `{ClientOrderID, BrokerOrderID, Status(accepted/rejected), IdempotentReplay, AcceptedAt, Code, Reason}`；**受理 ≠ 成交** | 新 DTO = MINOR |
| `CancelRequest` | `CancelOrder(orderID string)` | contracts | `{ClientOrderID, BrokerOrderID?}`；至少一个非空；优先按 `ClientOrderID` 幂等 | 新 DTO = MINOR |
| `OrderState` | `OrderResult`（`pkg/live/trader.go:141`） | contracts | `{ClientOrderID, BrokerOrderID, Status, FilledQuantity, AvgFillPrice, RemainingQuantity, LastUpdate, Code, Reason}`；`unknown` 是**未决**状态，不是拒绝 | 新 DTO = MINOR |
| `OrderStatus` | 裸字符串 `pending/filled/partial/cancelled/rejected/expired`（`pkg/live/trader.go:149`） | contracts | 冻结八值：`pending` / `accepted` / `partially_filled` / `filled` / `cancelled` / `rejected` / `expired` / `unknown`；**不是 K9**（K9 = run 的 `completed/invalid/failed`） | 加非终态 = MINOR；加终态/改语义 = MAJOR |
| `Fill` | `domain.Trade`（`pkg/domain/backtest.go:48`）是回测成交；实盘无归一化 Fill | contracts | `{FillID, ClientOrderID, BrokerOrderID, Symbol, Side, Quantity, Price, Commission, StampTax, TransferFee, FilledAt}`；`FillID` 稳定、跨调用不重复 | 新 DTO = MINOR |
| `Position` | `PositionInfo`（`pkg/live/trader.go:178`）含本地 T+1 字段 | contracts | `{AccountID, Symbol, Quantity, AvailableQuantity, AvgCost, MarketValue, AsOf}`；`AvailableQuantity` 是柜台口径；**不搬** `QuantityToday/QuantityYesterday`（本地记账） | 新 DTO = MINOR |
| `AccountSnapshot` | `reconciliation.AccountSnapshot`（`pkg/live/reconciliation/reconciliation.go:131`） | contracts | `{AccountID, AsOf, Cash(Total/Available), TotalAssets, Positions, Fees}`；`QueryAccount` 与 `QuerySettlement` **共用同一形状**（避免两套快照漂移） | 新 DTO = MINOR；字段/单位改 = MAJOR |
| `EmergencyFlattenResult` | `live.EmergencyFlattenResult`（`pkg/live/trader.go:91`） | contracts | `{Sold[], Skipped[], SoldTotal, StartedAt, CompletedAt, Reason}`；**Sold 与 Skipped 互斥**；`Sold[i]` 必须有真实成交依据；空仓 → 空 `Sold` + nil | 新 DTO = MINOR |
| `OrderReq`（XTP 专有） | `pkg/live/broker/xtp/xtp.go:213`，含 `BusinessType`（`pkg/live/broker/xtp/xtp.go:223`） | **XTP 适配器内部** | 不进 contracts；`BusinessType` 由账户/配置决定，不从 wire 透传 | 适配器内部，可独立升级 |
| `OrderRecord`（core 持久化） | `pkg/live/order_store.go:20` | core-only | 订单真相（quant）持久化；**不是**网关 DTO；broker 不写它 | 不改 |
| `Config`（XTP） | `pkg/live/broker/xtp/xtp.go:108`，`Password` 带 `yaml:"password"` | 适配器内部 | 密钥走环境/密钥管理；日志与序列化必须脱敏；**禁止**明文进库/进代码 | 内部；脱敏缺失 = 安全缺陷 |

### 9.4 C 组 · 调用面、错误语义与幂等矩阵

> 错误码的**唯一清单**在 `api-schema.md` §2；本表只引用，不重复定义。12 个 code 对应 12 个 Go 哨兵。

| 调用 | 今天 | 目标签名 | 错误码（本表冻结） | 幂等 / 重试 |
|---|---|---|---|---|
| `PlaceOrder` | `SubmitOrder` 六参，无幂等键（`pkg/live/mock_trader.go:207`） | `PlaceOrder(ctx, OrderRequest) (*OrderAck, error)` | `order_rejected` / `unknown_state` / `channel_down` / `duplicate_order` / `invalid_order` / `rate_limited` / `not_supported` / `insufficient_buying_power` / `idempotency_store_unavailable` | **发送权由 `Reserve` 原子判定**；同 `ClientOrderID` 同载荷 → 原 ack；不同载荷 → `duplicate_order`；`unknown_state` **只许查单**，不许重发；`rate_limited` 仅在**确定未送达**时退避重试 |
| `CancelOrder` | 已撤再撤报错（`pkg/live/mock_trader.go:380`） | `CancelOrder(ctx, CancelRequest) (*OrderState, error)` | `order_not_found` / `order_not_cancelable` / `unknown_state` / `channel_down` / `rate_limited` / `not_supported` | 已撤再撤 → 同终态；已成交 → `order_not_cancelable`；超时 → `unknown_state`（查单） |
| `QueryOrder` | `GetOrder` 只读内存（`pkg/live/broker/xtp/xtp.go:432`） | `QueryOrder(ctx, clientOrderID) (*OrderState, error)` | `order_not_found` / `channel_down` / `not_supported` | 只读；`unknown` 状态下继续查/对账，不推断 |
| `QueryPositions` | `GetPositions` 无 account（`pkg/live/mock_trader.go:432`） | `QueryPositions(ctx, accountID) ([]Position, error)` | `account_mismatch` / `channel_down` / `not_supported` | 只读 |
| `QueryAccount` | `GetAccount`（`pkg/live/mock_trader.go:448`） | `QueryAccount(ctx, accountID) (*AccountSnapshot, error)` | `account_mismatch` / `channel_down` / `not_supported` | 只读 |
| `QueryFills` | **零实现** | `QueryFills(ctx, accountID, since) ([]Fill, error)` | `account_mismatch` / `channel_down` / `not_supported` | 只读；`since` 含边界；`FillID` 稳定 |
| `QuerySettlement` | 自检 `traderAccountView`（`cmd/analysis/setup_reconciliation.go:127`） | `QuerySettlement(ctx, accountID, asOf) (*AccountSnapshot, error)` | `account_mismatch` / `channel_down` / `not_supported` | 只读；快照口径，**不是逐笔** |
| `EmergencyFlatten` | XTP 伪造 `Sold`（`pkg/live/broker/xtp/xtp.go:508`） | `EmergencyFlatten(ctx, reason) (*EmergencyFlattenResult, error)` | `channel_down` / `unknown_state` / `not_supported` / `account_mismatch` | 空仓 → 空结果；每个 sold 要有成交依据；未确认进 skipped；重入不重复卖 |
| `HealthCheck` | Mock 恒 nil（`pkg/live/mock_trader.go:192`），XTP offline 报错（`pkg/live/broker/xtp/xtp.go:295`） | `HealthCheck(ctx) error` | `channel_down` | 只读；失败即熔断 |

**fail-closed 四条**：① 风控拒绝**不进网关**（调用方先过 `RiskPolicy.CheckOrder`，被拒发 `exec.order_rejected`）；② 幂等映射不可用 → `idempotency_store_unavailable`，**不发送**；③ 未连接 → `channel_down`，不发送；④ 发出后超时 → `unknown_state`，**禁止重发**，只能查单/对账。

### 9.5 D 组 · 持久化面（本仓只持一张通道自有表）

| 数据 | 归属 / 落点 | 判据 |
|---|---|---|
| 订单 / 成交 / 持仓快照 | **现状 core `quant`**；最终归属待裁 **U2**（`tellurion:docs/design/qt-decoupling/repo-inventory.md` §3.1） | 网关只透传柜台状态，**不持有业务表**；要建 `exec` 镜像 schema 必须先立 ADR |
| `ClientOrderID → BrokerOrderID / ack` 幂等映射 | **本仓 `broker.order_acks`**（ADR 0002；结构见 `docs/db-schema.md` §1.1） | 写前日志，**不是订单真相**；`AckStore.Reserve` 原子判定发送权；`Durable()` 让装配闸拒绝内存实现 |
| 对账报告 / 差异 | `tellurion-risk`（`risk` schema，X3） | 网关只提供 `QuerySettlement` 快照；不写对账表 |
| 通道配置 / 密钥 | 网关自有配置（加密存储） | 环境变量 / 密钥管理；**禁止**明文进库、进代码、进日志；`Config.Password` 的 `yaml` tag 也要在序列化/日志路径脱敏（R8） |
| `OrderStore`（core） | core `quant` | 网关**不直连**这张表；幂等映射已裁为独立表（ADR 0002），不复用它 |

### 9.6 E 组 · 版本策略

| 面 | 稳定性 | 变更规则 |
|---|---|---|
| `BrokerGateway` / `SettlementQuerier` / `KillSwitch` / `HealthChecker` 方法集 | 新增（X6a 定稿后 frozen） | **加方法 = MAJOR**（所有实现全红）；新能力走**独立 port** = MINOR |
| `AckStore` 装配 SPI（`Load` / `Reserve` / `SaveAck` / `Durable`） | 新增（施工 ③ 定形） | 改发送权语义 = MAJOR（会造成重复下单）；加方法 = MAJOR（所有实现全红） |
| `OrderRequest` / `OrderAck` / `OrderState` 等 DTO 字段 | stable | 加字段 = MINOR（旧读者忽略未知字段）；改名 / 删除 / 改单位 = MAJOR |
| `OrderStatus` 八值 | stable | 加**非终态** = MINOR（旧客户端按未知处理）；加**终态** = MAJOR；`unknown` 的未决语义 = 冻结 |
| 错误哨兵（12 code） | stable | **加**新哨兵 = MINOR（调用方按 `errors.Is` 匹配，未知错误按 `channel_down` 兜底）；改某个 code 的可重试性/含义 = MAJOR |
| `ClientOrderID` 幂等语义 | frozen | 派生规则 / 重放语义改 = MAJOR（会造成重复下单或错配） |
| `QuerySettlement` 快照口径 | frozen | 从快照改逐笔 = MAJOR；字段/单位改 = MAJOR |
| `LiveTrader` / `Broker`（旧 port） | deprecated | 随 X6a 迁移删除 = MAJOR（core 内部）；`SimulatedBroker` 去留 = R4 |
| XTP 适配器 | 内部 | 可独立升级，不改变 port；新增柜台特性不得泄漏进 contracts |

### 9.7 R 组 · 待裁 / 已裁事项（不落地不阻塞对表，但阻塞实现）

| # | 事项 | 现状 / 冲突 | 建议裁决 |
|---|---|---|---|
| R1 | **订单/成交/持仓最终归属（U2）** | 现状登记 core `quant`；`run` 已排除；`exec` 镜像 schema 未裁 | 维持「现状 core `quant`」到 U2 裁决；裁决前不建表；建 `exec` 先立 ADR |
| R2 | **幂等映射的持久化** | ✅ **已裁（2026-10-23）**：落本仓 `broker.order_acks`（ADR 0002）；`AckStore` 改为 `Load` / `Reserve`（原子占位）/ `SaveAck` / `Durable`；柜台原生 `client-order-id` 只作第二道防线。否掉 core order store（无表 / 无列 / U2 未裁，X6d 后也不成立）与本地文件 WAL（单写者、真钱前仍要换 PG） | — |
| R3 | **对账口径与真实 broker 侧** | 生产是 `traderAccountView` 自检（local=broker，差异恒零）；XTP 未实现 `BrokerQuerier`；`FeeSnap` 恒零 | X6b 定「快照口径」；X6c 必须提供真实 broker 侧 `QuerySettlement`；验收腿要有人为注入差异（`driftedBrokerView` 已有测试样板），不能只跑零差异 |
| R4 | **port 归属与旧接口处置** | `BrokerGateway` 零实现；`LiveTrader`/`Broker` 两套旧面；`SimulatedBroker` 生产零调用方（AUD-34）；contracts 登记「`BrokerGateway` 未定形 / S1 不落」 | port + DTO 最终落 contracts（core 消费 + 本仓实现 = 两个使用方）；X6a 先让 Mock 同时满足旧 `LiveTrader`（core 内）与新 port（适配器薄层），再切消费方；`SimulatedBroker` 随 `LiveEngine` 一起退役或降为测试替身。「S1 不落」= 不随 QT-05 批量迁，不等于永远不落 |
| R5 | **申报规则的所有权** | `risk.ValidateOrderQuantity`（`pkg/risk/lot.go:136`）是交易所规则，却住在 risk 包；XTP 调它，Mock 不调 | 移到通道侧纯函数（contracts 或 gateway 内部的 `lot` 包），**两个实现共用**；`RiskPolicy` 不再导出该规则；迁移期保留 alias 以避免 core 改动扩散 |
| R6 | **XTP SDK / 许可 / 装配闸** | offline skeleton；`NewXTPTrader` 只校验配置；`EmergencyFlatten` 伪造成功 | X6c 从零实现；构造期强制注入 `SettlementQuerier` + 幂等映射 + `HealthChecker`，缺一返回 error；未实现能力返回 `not_supported`；**任何路径禁止 fake success** |
| R7 | **kill-switch 的授权与审计** | `EmergencyFlatten` 绕过 T+1；XTP 侧无真实成交；谁有权触发未定 | 独立 `KillSwitch` port；调用方必须带运维身份与 reason；逐标的落审计（`bypassed_t1`）；与 QT-50 ④ 的鉴权矩阵合流 |
| R8 | **密钥脱敏** | `Config.Password` `json:"-"` 但 `yaml:"password"`（`pkg/live/broker/xtp/xtp.go:119`） | 配置结构加 `String()`/日志脱敏；禁止把 `Config` 整体打进日志；密钥只从环境/密钥管理注入；加一条「日志里不出现 password」的测试 |

### 9.8 实现现状（2026-10-23，施工 ① + ② + ③）

**落地包**：`internal/gateway` + `internal/reconcile` + `internal/ackstore`（前两者只用 stdlib；`ackstore` 引 `pgx v5.7.3`）。

| 文件 | 内容 |
|---|---|
| `model.go` | `Side` / `OrderType` / `OrderStatus` 八值 + `Valid()` / `Terminal()`；`OrderRequest.Fingerprint()`；`OrderAck` / `CancelRequest` / `OrderState` / `Fill` / `Position` / `AccountSnapshot` / `EmergencyFlattenResult` |
| `errors.go` | 12 个契约哨兵（与 `api-schema.md` §2 一一对应） |
| `ports.go` | `BrokerGateway` / `SettlementQuerier` / `KillSwitch` / `HealthChecker` / `AckStore`（`Load` / `Reserve` / `SaveAck` / `Durable` + `IdempotencyRecord`） |
| `mock.go` | `Mock` 实现四个 port + `MemoryAckStore`（仅开发/测试；`Durable=false`） |
| `xtp.go`（施工 ②） | `XTPTrader` fail-closed 骨架 + 装配闸：`NewXTPTrader` 构造期强制注入 `AckStore` / `SettlementQuerier` / `HealthChecker`（缺一或 `Acks.Durable()==false` → `ErrAssemblyIncomplete`，构造路径哨兵、非 wire code）；已装配后**所有**方法（含 `EmergencyFlatten`）恒 `ErrChannelDown`；`XTPConfig` 无密钥字段（R8） |
| `ackstore/ddl.go`（施工 ③） | `broker.order_acks` 的唯一 DDL（幂等、无版本表）+ `Migrate` 的语句源 |
| `ackstore/pg.go`（施工 ③） | `AckStore` 的 PG 实现：原子 `Reserve`（`INSERT ... ON CONFLICT DO NOTHING RETURNING`）、`SaveAck`（只更新已占位行）、`Load`、`Durable=true`、`Migrate` |
| `reconcile/reconcile.go`（施工 ②） | 快照口径对账纯函数：两侧共用 `AccountSnapshot`（F6），七类差异 × 三级严重度，容差沿用 core 迁出口径（现金 0.01 / 数量 0 / 市值 0.5% 仅数量相符时比 / 费用 0.01 / 时间漂移 60s），nil / 账户不符 / 重复标的 fail-loud，固定发射顺序，零差异返回空非 nil |

**能跑 vs 只是接口就位**

| 能力 | 状态 |
|---|---|
| `PlaceOrder` 幂等（同载荷重放 → `IdempotentReplay=true` 不发送；异载荷 → `duplicate_order`） | ✅ 有测试 |
| **重放还原当初结论**（被拒的单重放仍返回 `ErrOrderRejected` / `ErrInsufficientBuyingPower`） | ✅ 有测试 |
| 写前日志顺序：`Load` → 占位 `Save` → 发送 → 回执 `Save` | ✅ 有测试 |
| 映射不可用（store nil / Load 失败 / 占位失败）→ `idempotency_store_unavailable` 且**不发送** | ✅ 有测试 |
| 断连 → `channel_down` 且**不发送** | ✅ 有测试 |
| 回执落盘失败 → `unknown_state`，映射留占位（重放继续 unknown，不重发） | ✅ 有测试 |
| 市价 + 有价格 + 资金够 → 全量成交；无价格 → 停在 `accepted`（不伪造成交） | ✅ 有测试 |
| 柜台拒单 → `OrderAck{rejected}` 与 error **同时非 nil**（留痕） | ✅ 有测试 |
| `CancelOrder`：已撤再撤 → 同终态 + nil；终态 → `order_not_cancelable` | ✅ 有测试 |
| `QueryOrder`：本地无但映射有占位 → `OrderUnknown` + nil；映射也没有 → `order_not_found` | ✅ 有测试 |
| 幂等域与本地订单索引**同口径**（都用本通道账户）→ 查单 / 撤单查得到 | ✅ 有测试 |
| `QueryPositions` / `QueryAccount` / `QueryFills` / `QuerySettlement`（内存快照） | ✅ 有测试 |
| `EmergencyFlatten`：只报真实成交，未确认进 `Skipped`；重入不重卖 | ✅ 有测试 |
| **清仓单 ID 区分轮次**（持仓代次参与 ID）→ 不重放上一轮回执伪造清仓成功 | ✅ 有测试 |
| `HealthCheck` / `Connect` / `Disconnect` | ✅ 有测试 |
| **持久**幂等映射（跨重启） | ✅ `internal/ackstore` 的 `PgStore`（`broker.order_acks`，ADR 0002）；回执跨连接池回读有集成测试（`BROKER_TEST_DSN`） |
| **并发同 ID 只出一个发送权** | ✅ `Reserve` 原子占位；内存 / PG 各有并发腿（破坏临界区后 16/16 重单，已做破坏验证） |
| `XTPTrader` **装配闸**（构造期缺幂等 / 对账 / 健康任一 → error，即「无幂等键不接真钱」护栏） | ✅ 有测试（X6c 施工 ②）；**并拒绝 `Durable()==false` 的内存实现**（施工 ③） |
| `XTPTrader` **全方法 fail-closed**（已装配后恒 `channel_down`，零 fake success） | ✅ 有测试；SDK 真实实现 ❌（X6c 全量：下单/撤单/查单/回报/心跳/重连/限流） |
| 对账纯函数（快照口径 + **差异注入验收**九腿） | ✅ 有测试（X6b 施工 ②）；真实 broker 侧 `QuerySettlement` ❌（归 X6c）；对账留痕进 risk 未接（X3） |
| core 消费方切换（旧 `LiveTrader` / `Broker` 退役） | ❌ 未做（R4） |
| 申报数量规则两实现共用同一份纯函数 | ❌ 未做（R5；Mock 内联了一份） |

**六条故意取舍（都写在代码注释里，改前先读）**

1. `AckStore` **注入式**：`nil` 就是 fail-closed，**不**提供"忘了注入就自动用内存"的兜底。
2. 回执落盘失败返回 `unknown_state` 而非"下单成功"——映射里留的是占位，任何重放都会被拦。
3. Mock 在没有价格时不撮合，**不**用 0 或猜的价格伪造成交。
4. `EmergencyFlatten` 的 `Sold` 必须对应真实提交并确认的订单，其余一律进 `Skipped`；清仓单 ID 带**持仓代次**，重放来的单**不算**本次清仓的凭据。
5. `ClientOrderID` 由调用方生成，网关**不改写、不派生**（`EmergencyFlatten` 是唯一例外：它内部自造 ID，用 `flatten:<symbol>:<代次>:<qty>` 区分轮次）。
6. **发送权由 `Reserve` 一步判定**：不提供「`Load` 再 `Save`」的先查后写形状——那正是并发同 ID 重单的来源（实测可复现）。`SaveAck` 只能写已占位的行，没有占位就报错。

**自检结论**：X6a 的 port / DTO / 错误语义 / 幂等顺序已定形可用；X6b 的口径已定稿为纯函数（差异注入验收在册）；X6c 的装配闸已把「无幂等键不接真钱」变成构造期可机械拒绝的护栏（并拒绝内存映射）；R2 已裁，持久幂等映射 `broker.order_acks` 已落。本仓仍**未接 core、无 XTP SDK 链路、未接真实柜台**——真钱通道现在只差 X6c 全量适配器（下单 / 撤单 / 查单 / 回报 / 心跳 / 重连 / 限流）。X6d 按 2026-10-23 裁决暂缓。
