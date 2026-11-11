---
status: active
last-verified: 2026-11-11
verified-by: 2026-11-11 文档对齐审计 —— G3 已收口 / core 旧面已删，校准 §1 现状段与实现矩阵、§1.5.1 读侧鉴权口径、§4 旧面行；此前 G1 施工（§1.5）、G7（施工 ⑤）撤单 `order_not_found` 判据、R2 裁决（ADR 0002）
---

# API Schema — tellurion-broker

> **契约地位**：**Go port（进程内）+ 版本化 HTTP 面（`/api/broker`，2026-11-07 起）**。
> `BrokerGateway` 按"将来可远程"设计（DTO 序列化友好），port 形态与实盘运行域同进程（下单是延迟敏感路径）；HTTP 面是**服务间调用面**（ADR-036 G1），两者共用同一份实现与同一套 DTO，见 §1.5。
> **ADR-036（2026-10-26）已把本仓定位为服务**：G0 已落 `/healthz`、`/version`；**订单 HTTP 面已在 `SVC-BROKER-G1` 落地**；真实 XTP 的低延迟路径是否留在进程内，在 X6c 前另立 ADR 决定。
> **通道性质标识（2026-11-11）**：`GET /version` 额外返回只读字段 `channel`（当前恒 `"mock"`），供 core 启动期核对 `BROKER_CHANNEL` 声明（声明 `live` 却读到 `mock` 即拒绝启动）。它只描述通道性质，不改变撮合行为，也不是新的业务接口；接真实柜台时须同步改为 `"live"`。
> 本文件是**本仓对外接口的唯一真相源**。代码与它不一致时，先判断谁错：契约错 → 改本文 + 立 ADR；实现错 → 改代码。
> 跨仓消费方（core / agent / ui / 其他服务）按本文件编码，**不读本仓实现**。
> **今天 vs 目标**：本仓 `internal/gateway` 已落本文四个 port + DTO + 12 哨兵 + 内存 `Mock` + `XTPTrader` fail-closed 骨架（装配闸：缺持久幂等映射 / 对账 / 健康检查任一即构造期拒绝；已装配后全方法恒 `channel_down`，零 fake success），`internal/httpapi` 已落 HTTP 面（§1.5），`internal/reconcile` 已落快照口径对账纯函数，`internal/ackstore` 已落持久幂等映射 `broker.order_acks`（2026-10-23，ADR 0002）与保留期清理（G2），**无 XTP SDK 链路**；core 侧旧面（`LiveTrader` / `Broker` / `SimulatedBroker` 三个旧实现）已于 **2026-11-10** 随 `SVC-CORE-BROKER-RETIRE` 删除，core 默认通道 = 本仓 HTTP 面（`tellurion:pkg/brokerhttp`）。现状矩阵与取舍见 [`ARCHITECTURE.md`](ARCHITECTURE.md) §9.8，八条实测事实见 §9.1（**历史基线** core `dbe4c68`，删除前的取证留档）。
> **port 的落点**：wire 形状（订单 / 成交 / 持仓 / 账户 / 紧急清仓）已于 **2026-11-09** 收编 `tellurion-contracts/broker` v0.9.0（CT-05），本仓 `internal/gateway/model.go` 只留类型别名；`BrokerGateway` 等 Go port 方法集仍住本仓 `internal/gateway`。
> **ADR-036（2026-10-26）后本仓是服务**：G0 已落 `cmd/brokergateway`（`/healthz`、`/version`）+ Dockerfile/deploy（`:8091`）；G1 已落订单/查询/对账 HTTP 面（§1.5）。

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
| `Mock`（`internal/gateway`） | `BrokerGateway` + `SettlementQuerier` + `KillSwitch` + `HealthChecker` | 本仓的实现（`MockTrader` 是 core 旧名，已删） | **可用**（X6a）；注入式 `AckStore`，nil 即不发送；无价格不成交；申报数量走 `internal/gateway/lot` 纯函数（R5） |
| `XTPTrader`（`internal/gateway`，本仓） | 四个 port + `KillSwitch` | 真实柜台（X6c） | **fail-closed 骨架 + 装配闸**：构造期缺 `AckStore` / `SettlementQuerier` / `HealthChecker` 任一，或 `AckStore.Durable()==false` → `ErrAssemblyIncomplete`（构造路径哨兵，**不是第 13 个 wire code**）；已装配后所有方法恒 `channel_down`；SDK 从零实现未做 |
| `PgStore`（`internal/ackstore`） | `AckStore`（装配 SPI，非四个 wire port） | 持久幂等映射（R2 已裁） | **可用**：`broker.order_acks`，原子 `Reserve`（`ON CONFLICT DO NOTHING`）+ 回执写回 + `Durable=true`；PG 集成测试按 `BROKER_TEST_DSN` 运行 |
| `MemoryAckStore`（`internal/gateway`） | `AckStore` | 开发 / 测试 | 可用；**`Durable=false`**，进不了 XTP 装配闸 |
| `Reconcile`（`internal/reconcile`） | 纯函数（非 port） | 快照口径对账（X6b） | **可用**；输入两侧共用 `AccountSnapshot`，七类差异 × 三级严重度，差异注入验收在册；真实 broker 侧快照归 X6c 全量 |
| `MockTrader`（旧，core `pkg/live`） | `LiveTrader`（旧） | 开发 / 回放 / 压测 | **已删除**（2026-11-10，`SVC-CORE-BROKER-RETIRE`）；删除前无编译期断言、不做板块申报规则校验 |
| `XTPTrader`（旧，core `pkg/live/broker/xtp`） | `LiveTrader`（旧） | 真实柜台 | **已删除**（2026-11-10，`SVC-CORE-BROKER-RETIRE`）；删除前是 offline skeleton（`Connect`/`SubmitOrder`/`CancelOrder` 恒返回「SDK not linked」）且 `EmergencyFlatten` 伪造成功（F4 历史基线） |
| `SimulatedBroker`（旧，core） | `Broker`（旧，另一套面） | `LiveEngine` 测试替身 | **已删除**（2026-11-10，`SVC-CORE-BROKER-RETIRE`）；生产零调用方（AUD-34），去留曾登记为 R4 |

> 上表后三行是 **2026-11-10 前的 core 旧面**，只作历史基线留档；本仓今天的实现只有前四行（`Mock` / `XTPTrader` / `PgStore` / `MemoryAckStore`）+ `reconcile` 纯函数。

### 校验归属（必须分开，不能混）

| 校验 | 归属 |
|---|---|
| 最小单位、委托类型、柜台限流、**交易所申报数量规则**（100 股整数倍 / 科创板 200 股起等） | **网关内**（通道侧纯函数，两个实现共用） |
| 数量上限、集中度、T+1、止损 | **RiskPolicy**（进程内裁决，规则源在 risk 服务） |

> ✅ **本仓腿已落（2026-10-24，R5）**：申报数量规则抽为通道侧纯函数 `internal/gateway/lot`（无 I/O），Mock 已改调同一份；core 的 `risk.ValidateOrderQuantity` 尚未退役、XTP 全量适配器尚未接入（归 X6c 跨仓腿）。规则**归通道侧**，网关**不 import 风控裁决**。

## 1.5 HTTP 面（`SVC-BROKER-G1`，2026-11-07 落地）

> **实现**：`internal/httpapi`（只做报文与错误码翻译，不裁决、不改单、不持状态），挂载在 `cmd/brokergateway` 的 `/api/broker/` 下；port 形态（§1）保留，两者是**同一份实现的两个面**。
> **窗口期义务（CT-05 收编前置）**：本节就是订单 / 成交 / 账户三族的**逐字段报文表**；`internal/httpapi` 的用例**断言序列化后的键集合**（`TestOrderAndAccountPayloadShapes`）。收编契约时必须「删本地形状改引契约」，且在**同一份报文快照**上证明改前 / 改后逐字节一致——**不允许两份 DTO 并存**。✅ 已于 2026-11-09 收编完成：`internal/gateway/model.go` 只留 `tellurion-contracts/broker` 类型别名，逐字节 goldens 见 `internal/gateway/wire_shape_test.go`；本节字段表继续作为**契约的对外说明**，不再是本仓字段 owner。

### 1.5.1 路由

| 方法 | 路径 | 语义 | 需要 service token |
|---|---|---|---|
| `POST` | `/api/broker/orders` | 下单（`OrderRequest` → `OrderAck`） | 是 |
| `GET` | `/api/broker/orders/{clientOrderID}` | 查单（→ `OrderState`） | 否 |
| `DELETE` | `/api/broker/orders/{clientOrderID}` | 撤单（→ `OrderState`） | 是 |
| `GET` | `/api/broker/accounts/{accountID}/positions` | 持仓（→ `Position[]`） | 否 |
| `GET` | `/api/broker/accounts/{accountID}/account` | 账户快照（→ `AccountSnapshot`） | 否 |
| `GET` | `/api/broker/accounts/{accountID}/fills?since=RFC3339` | 成交（→ `Fill[]`，`since` 可省 = 全量） | 否 |
| `GET` | `/api/broker/accounts/{accountID}/settlement?as_of=RFC3339` | 对账快照（→ `AccountSnapshot`） | 否 |
| `POST` | `/api/broker/emergency-flatten` | 紧急清仓（body `{"reason":string}` → `EmergencyFlattenResult`） | 是 |

**鉴权**：写侧要求 `Authorization: Bearer $BROKER_SERVICE_TOKEN`（常量时间比较）。**环境变量未设 = 写侧关闭**（`503 writes_disabled`），不是"不校验"；token 不匹配 = `401 unauthorized`。读侧是本机服务面，**不鉴权**（G4 已收口：就绪只看装配闸，见 §1.6；本仓不引 kit 鉴权适配器，引入读侧鉴权须另立单）。
> **裁决依据**：写侧静态 token / 读侧只读无鉴权 / 与兄弟仓 kit JWT 体系的分叉是**显式接受的例外**（不是遗漏），见 [`adr/0004-鉴权姿态与静态token分叉.md`](adr/0004-鉴权姿态与静态token分叉.md)；token 轮换步骤（含兼容窗口）见 [`runbook.md`](runbook.md) §2.1。该 ADR 的 dev-only 边界一旦失效（端口外露 / 读侧出现写语义 / 报文新增凭证类字段 / X6c 真钱重开），必须重裁而不是沿用。
> **部署约束（硬）**：读侧 5 条路由**只读、无鉴权**，只允许绑定回环或服务内网，**不得暴露到公网**（持仓 / 账户 / 成交 / 对账快照对任何能连上端口的人都可见）；写侧的 `BROKER_SERVICE_TOKEN` 保护不了读侧。

**幂等**：键仍是 `(account_id, client_order_id)`，与 port 形态完全一致；重放返回**原回执**且 `idempotent_replay=true`。传输层不另造幂等键。

### 1.5.2 请求报文

| 字段 | 类型 | 可空 | 判据 |
|---|---|---|---|
| `client_order_id` | string | 否 | 调用方生成、重试不变；网关不改写不生成 |
| `account_id` | string | 否 | 必须与网关服务的账户一致（否则 `account_mismatch`） |
| `symbol` | string | 否 | 6 位代码，不带交易所后缀 |
| `side` | `"buy"` \| `"sell"` | 否 | 其余值 → `invalid_order` |
| `order_type` | `"market"` \| `"limit"` | 否 | 其余值 → `invalid_order` |
| `quantity` | integer | 否 | >0，且是申报单位的整数倍（`internal/gateway/lot`） |
| `limit_price` | number | 限价必填 / 市价省略 | 限价单缺失 → `invalid_order` |

### 1.5.3 响应报文（逐字段）

**`OrderAck`（下单回执）**

| 字段 | 类型 | 可空 | 说明 |
|---|---|---|---|
| `client_order_id` | string | 否 | 原样回显 |
| `broker_order_id` | string | 是（omitempty） | 柜台单号；未受理时缺省 |
| `status` | 八值枚举 | 否 | §1 的 `OrderStatus`；`unknown` = 未决，禁止重发 |
| `idempotent_replay` | bool | 否 | `true` = 本次是同载荷重放，返回的是原回执 |
| `accepted_at` | RFC3339 | 否 | UTC |
| `code` / `reason` | string | 是（omitempty） | 仅拒单/异常时出现 |

**`OrderState`（查单 / 撤单结果）**：`client_order_id`(string, 否)、`broker_order_id`(string, 可空)、`status`(枚举, 否)、`filled_quantity`(integer, 否)、`avg_fill_price`(number, 否，无成交为 0)、`remaining_quantity`(integer, 否)、`last_update`(RFC3339, 否)、`code`/`reason`(string, 可空)。

**`Fill`（成交回报）**：`fill_id`(string, 否，稳定不重复)、`client_order_id`(string, 否)、`broker_order_id`(string, 可空)、`symbol`(string, 否)、`side`(枚举, 否)、`quantity`(integer, 否)、`price`(number, 否)、`commission`(number, 否)、`stamp_tax`(number, 否)、`transfer_fee`(number, 否)、`filled_at`(RFC3339, 否)。

**`Position`（柜台口径持仓）**：`account_id`(string, 否)、`symbol`(string, 否)、`quantity`(integer, 否)、`available_quantity`(integer, 否，T+1 后可用)、`avg_cost`(number, 否)、`market_value`(number, 否)、`as_of`(RFC3339, 否)。本地 T+1 记账字段**不进报文**。

**`AccountSnapshot`（账户 / 对账共用）**：`account_id`(string, 否)、`as_of`(RFC3339, 否)、`cash`(对象, 否：`total` / `available` 均为 number)、`total_assets`(number, 否)、`positions`(`Position[]`, 否，空集合是 `[]` 不是 `null`)、`fees`(对象, 否：`commission` / `stamp_tax` / `transfer_fee` 均为 number)。

**`EmergencyFlattenResult`**：`sold`(`FlattenedPosition[]`, 否)、`skipped`(`SkippedPosition[]`, 否)、`sold_total`(number, 否)、`started_at`(RFC3339, 否)、`completed_at`(RFC3339, 可空)、`reason`(string, 否)。
- `sold[]` 元素：`account_id`、`symbol`、`quantity`、`client_order_id`、`broker_order_id`(可空)、`avg_fill_price`、`filled_at`。
- `skipped[]` 元素：`account_id`、`symbol`、`quantity`、`reason`。**`skipped` 非空不等于失败**：它是"没有确认成交"，逐条带原因。

**错误信封**：`{"code": string, "message": string, "ack"?: OrderAck}`。`code` 取自 §2 的 12 个契约码，或下列**非订单**码（鉴权 / 装配，不属契约哨兵）：

| code | HTTP | 场景 |
|---|---|---|
| `unauthorized` | 401 | 缺 token / token 不匹配（写侧） |
| `writes_disabled` | 503 | `BROKER_SERVICE_TOKEN` 未配置，写面关闭 |
| `invalid_request` | 400 | body 不是合法 JSON / 缺 `reason` / 时间参数不是 RFC3339 |
| `internal_error` | 500 | 未识别的错误（不猜成 4xx） |

### 1.5.4 12 个契约码的 HTTP 映射

| code | HTTP | code | HTTP |
|---|---|---|---|
| `order_rejected` | 409 | `invalid_order` | 400 |
| `unknown_state` | 409（未决，**禁止重发**） | `rate_limited` | 429 |
| `channel_down` | 503 | `not_supported` | 501 |
| `duplicate_order` | 409 | `account_mismatch` | 403 |
| `order_not_found` | 404 | `insufficient_buying_power` | 409 |
| `order_not_cancelable` | 409 | `idempotency_store_unavailable` | 503 |

> `order_rejected` 的响应**同时**带 `ack`（回执留痕，§2 的"ack 与 error 同时非 nil"在 HTTP 面翻译成 409 + `ack` 字段）。

### 1.6 就绪探针口径（G4 定死，二选一已选）

**选择：不引入 `/readyz`；`/healthz` 是唯一探针，就绪 = 装配闸通过。**

- 本仓只监听一个 HTTP 端口（默认 `:8091`），没有独立的业务监听面；再引入 `/readyz` 会让「进程活着 / 通道可用 / 幂等存储可用 / 可接单」四种状态争同一个探针，调用方与编排器反而无从判断该信谁。唯一探针同时消除了两处文档各写一套口径的漂移风险。
- **真钱态**：`NewXTPTrader` 的装配闸在**构造期**执行（持久 `AckStore` / `SettlementQuerier` / `HealthChecker` 缺一即 `ErrAssemblyIncomplete`）；构造失败进程不启动。因此「进程能监听」⇒「装配闸已通过」。
- **Mock 态**：`NewMock` + `httpapi.New` 在启动期完成装配；`httpapi.New` 缺 port 直接 panic，不会带半装配继续跑。Mock 允许 `Store=nil`（每笔下单 fail-closed 为 `idempotency_store_unavailable`），这仍然算「就绪」——**就绪回答的是「进程该不该被重启」，不是「这一笔能不能下」**。
- **`/healthz` 的语义**：只回答通道可用性（`HealthChecker.HealthCheck`），失败返回 `503 {"status":"down","reason":"channel_unavailable"}`。它**不**代表幂等映射可用：后者是 `idempotency_store_unavailable`（503）这条**业务错误**。把 PG 池塞进探针会让读侧（查单 / 持仓 / 对账）被整体摘掉，反而扩大事故面。
- `Acks.Durable()` 是构造期静态判据，不是运行期探针。
- 运维侧的同一口径与命令见 [`runbook.md`](runbook.md) §1。

## 2. 错误码与 fail-closed 语义

> **12 个 code = 12 个 Go 哨兵**。调用方只 `errors.Is` 契约哨兵；适配器私有错误（如 XTP `ErrOffline`）必须在边界翻译成本表之一。

| code | Go 哨兵 | 含义（判据） | 发送了吗 | 调用方行为 |
|---|---|---|---|---|
| `order_rejected` | `ErrOrderRejected` | 柜台**明确**拒单，回执已拿到 | 是 | **不重试**；`OrderAck{Status: rejected, Code, Reason}` 与 error **同时非 nil**（回执要留痕），其余情况 ack 为 nil |
| `unknown_state` | `ErrUnknownState` | 已发出，但**结果未知**（超时 / 发送后断连 / 回执落盘失败） | 是（可能） | **禁止重发（严禁换 `client_order_id` 重发）**；只能 `QueryOrder` / 对账；查不到 → 告警 + 人工介入（真钱不做自动补偿） |
| `channel_down` | `ErrChannelDown` | 未连接 / 熔断中 / 健康检查失败 | 否 | 熔断：停止下单 + 告警；**不静默重试** |
| `duplicate_order` | `ErrDuplicateOrder` | 同 `ClientOrderID`、**不同载荷** | 否 | fail-loud；绝不发第二笔；调用方查单或换 ID（换 ID 前先确认原单不存在） |
| `order_not_found` | `ErrOrderNotFound` | 网关**能证明**从未发送过该 ID | 否 | 可按新单处理（由调用方决定）；不得据此推断柜台侧不存在 |
| `order_not_cancelable` | `ErrOrderNotCancelable` | 已成交 / 已过期 / 已终态，不可撤 | 否 | 查单取终态；不重试撤单 |
| `invalid_order` | `ErrInvalidOrder` | 参数不合法 / 违反申报数量规则 | 否 | 修参数后重新提交（**新** `ClientOrderID`） |
| `rate_limited` | `ErrRateLimited` | 柜台限流，且**确定未送达** | 否 | 退避后可用**同一** `ClientOrderID` 重试；响应**必带 `Retry-After`（整数秒）**——本仓不持有柜台限流窗口，取本仓声明的最小退避单位 `1`（`internal/httpapi/httpapi.go` `rateLimitedRetryAfter`，与 risk 等兄弟仓同口径），调用方按该值退避而非立即重发 |
| `not_supported` | `ErrNotSupported` | 通道不支持该能力（如 XTP 未实现 `QueryFills`） | 否 | 降级或拒绝；**禁止**伪造成功形状 |
| `account_mismatch` | `ErrAccountMismatch` | `accountID` 不属于本通道 | 否 | 配置错误，fail-loud |
| `insufficient_buying_power` | `ErrInsufficientBuyingPower` | 柜台明确返回买力不足 | 是（已拒绝） | 不重试；告警 |
| `idempotency_store_unavailable` | `ErrIdempotencyStoreUnavailable` | 幂等映射（持久化）不可用（含下单发送前、以及查单/撤单读映射失败，R10） | 否 | **不发送**；这是可用性事故，不是下单失败 |

**最重要的一条**：超时 = 状态未知，不是"失败"。盲目重发是实盘事故的经典来源。
**第二重要的一条**：未实现的能力返回 `not_supported`，**绝不返回成功形状**（今天 XTP `EmergencyFlatten` 反例见 §9.1 F4）。

### 2.1 六类失败语义：网关侧发生了什么、调用方能做什么（G4）

> 「重试」「换新 ID」「必须查单」是三个互斥动作，不要凭直觉混用。
> `unknown_state` 单独成行——它是本仓最要命的一条。

| 类别（code） | 网关侧发生了什么 | 调用方能做什么 |
|---|---|---|
| `channel_down` | 未连接 / 熔断中 / `HealthCheck` 失败；**未发送**，也未占幂等位 | 不静默重试；熔断 + 告警，等显式恢复；恢复后可用**同一** `client_order_id` 重试（网关证明未发送）；**不要**换新 ID |
| **`unknown_state`** | **已发出**（或无法证明未发出），结果未知：发送后超时 / 断连、回执落盘失败、撤单时映射里有占位而本地无状态 | **必须查单**：用**同一个** `client_order_id` 调 `GET /api/broker/orders/{id}`；查不到就继续查 / 等对账 + 告警 + 人工介入。**严禁换 `client_order_id` 重发**（换 ID = 柜台第二笔）。网关的错误文本固定带「不得重发 / 先查单」提示，`internal/gateway/mock_test.go` 有反证用例 |
| `idempotency_store_unavailable` | 本仓自己的**幂等映射**不可用（PG 池断 / `Load` / `Reserve` / `SaveAck` 失败）。下单发送前失败 ⇒ **未发送**；查单 / 撤单读映射失败 ⇒ 这一单**可能已在柜台** | 这是**可用性事故**，不是下单失败。不要循环重试、不要换 ID；先修存储 / 查存储告警；恢复后用**同一** ID 重试下单。若是查单 / 撤单路径报出，恢复后先查单再决定，**不能推断「没发过」** |
| `order_not_found` | 网关**能证明**从未发送过该 ID（本地无状态且幂等映射无占位） | 可按新单处理（由调用方决定）；可复用或换 ID；**不得**据此推断柜台侧一定不存在 |
| 幂等冲突 `duplicate_order` | 同 `client_order_id`、**不同载荷**；**未发送**，绝不发第二笔 | 不重试；先查原单 / 核对载荷；只有确认原单不存在且确需新单时才用**新 ID**，并在事故记录里写清 |
| 申报数量不合规 `invalid_order`（`internal/gateway/lot`） | 通道侧校验失败：入参非法（`invalid_input`）、数量 ≤0、非申报单位整数倍（`not_lot_multiple`）、不足一手（`zero_quantity`）、超可用（`exceeds_available_quantity`）、超资金（`insufficient_capital`）、涨跌停越界（`price_above_limit_up` / `price_below_limit_down`）；**未发送** | 修参数后重新提交；**用新 `client_order_id`**（不要让同一 ID 携带不同载荷，否则会变成 `duplicate_order`）；不要靠网关缩量 / 抬量 |

> 六类里的后两类在本仓的落点：幂等冲突由 `AckStore.Reserve` + 指纹比对判定（`internal/gateway/mock.go`）；申报数量由 `internal/gateway/lot` 判定——`Mock.PlaceOrder` 直接校 `ValidateQuantity`（显式数量），`Size` 的超可用 / 超资金 / 涨跌停拒单原因发生在调用方算量阶段，同归 `invalid_order`。两者都在发送前，因此都不会产生柜台订单。

## 3. 幂等性与重试

- **`PlaceOrder` 幂等键 = `ClientOrderID`**（由调用方生成、重试不变；网关不改写、不生成）。判重范围 = `(AccountID, ClientOrderID)`。
- **发送权由 `AckStore.Reserve` 原子判定**：`Load` 再 `Save` 的两步形状会让并发同 ID 各自读到未命中、各发一笔（实测可复现）；PG 实现用 `INSERT ... ON CONFLICT DO NOTHING RETURNING`，内存实现用同一临界区。
- 同 ID **同载荷** → 返回原 `OrderAck`（`IdempotentReplay=true`），不发送；同 ID **不同载荷** → `duplicate_order`，不发送。
- **重放还原当初的结论**：当初被柜台拒过的单，重放时仍返回**同一个 error**（`order_rejected` / `insufficient_buying_power`）与那份 ack——被拒的单重放不能变成"受理成功"。
- 幂等映射必须**持久**（跨重启、跨进程）：不可用 → `idempotency_store_unavailable`，**不发送**。落点已裁为 `broker.order_acks`（[ADR 0002](adr/0002-幂等映射落-broker-写前日志.md)、[`db-schema.md`](db-schema.md) §1.1）；`AckStore.Durable()` 让 XTP 装配闸拒绝内存实现。
- `CancelOrder` 幂等：已撤再撤 → 同终态 `OrderState` + `nil`；已成交 → `order_not_cancelable`。
- **`CancelOrder` 的 `order_not_found` 判据与 `PlaceOrder` 同一条**：本地没有订单状态**不等于**从未发送——
  幂等映射里有占位（另一进程发的 / 本进程重启后本地状态丢了）时结论是 `unknown_state`（走查单，**不重发撤单**），
  映射里也没有才允许 `order_not_found`。没有注入幂等映射（`Store=nil`）时本实例**发不出任何单**
  （`PlaceOrder` 恒 `idempotency_store_unavailable`），那种装配下的 `order_not_found` 才是可证的。误报 `not_found` 会让调用方按「没发过」换新 `ClientOrderID` 重发 → 柜台两笔。
- `QueryOrder` / 查询面只读，天然幂等。
- **映射读失败 = `idempotency_store_unavailable`，不是 `channel_down`**（R10，2026-11-07）：`CancelOrder` / `QueryOrder` 读幂等映射失败时报的是**本仓存储不可用**，不是「柜台侧不可达」——这一单可能已在柜台上，调用方不能按线路故障处置。真正的断连路径（`Disconnect()` / 发送前连接判定）仍报 `channel_down`。
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
- 旧 `LiveTrader` / `Broker` / `SimulatedBroker` 已于 **2026-11-10** 随 `SVC-CORE-BROKER-RETIRE` 删除（core 内部；删除属 MAJOR，已在 core 仓落账）。

## 5. 不提供什么（边界）

- 不做风控裁决（`RiskPolicy.CheckOrder` 在 core / risk，网关不 import）
- 不做策略
- 不做订单簿 / 撮合（回测撮合在 core）
- 不持有订单真相（只透传柜台状态；订单持久化在 core `quant`，最终归属 U2）
- 不做资金划转
- 不持有对账结论（对账留痕归 `tellurion-risk`，网关只提供 `QuerySettlement` 快照）

## 6. 开真钱前置（当前阶段，G4）

> **本仓今天未接真钱。** 真钱通道（`X6c`）已于 2026-11-10 **裁决为暂不实施**（`⏸` 挂起，不是「待办」也不是「永久不做」）：缺 P1~P5（SDK / 测试账号 / 出网 / C 工具链 / 盯盘时间窗，见 [`x6c-xtp-readiness.md`](x6c-xtp-readiness.md) §4）。**重开条件与流程见 [`adr/0003-真钱通道暂不实施与重开条件.md`](adr/0003-真钱通道暂不实施与重开条件.md)**；本阶段的终态 = Mock 全绿 + `XTPTrader` fail-closed 骨架。

**当前阶段闸门**：开真钱之前，`SVC-BROKER-G1`（订单 / 查询 / 对账 HTTP 面）、`SVC-BROKER-G2`（幂等映射保留期）、`SVC-BROKER-G3`（core 切读与旧实现退役）、`SVC-BROKER-G4`（失败语义 / 就绪 / 熔断 / runbook / 演练）**必须全部 ✅**；任一 🔶 / ⛔ / ⬜ 都不得开真钱，也不得以「先跑小单」绕过。截至 2026-11-10：G1 ✅、G2 ✅、G3 ✅、G4 ✅。

机械判据：本仓没有 XTP SDK 链路、`XTPTrader` 恒 `channel_down`、`cmd/brokergateway` 只装配 Mock。任何「已经可以接真钱」的说法都必须先让上面四项全绿，并另立 ADR 记录切换判据。运维视角的同一闸门见 [`runbook.md`](runbook.md) §9。
