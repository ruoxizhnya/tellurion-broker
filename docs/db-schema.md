---
status: active
last-verified: 2026-10-23
verified-by: X6a 落地（无表，DTO 在 internal/gateway）+ 文档同步
---

# DB Schema — tellurion-broker

> **所有权声明**：本仓**当前不持有任何业务表**：它是通道适配器，只透传柜台状态。
> 订单 / 成交 / 持仓快照是**目标表·未建**（`quant.orders` / `fills` / `positions` / `portfolio_snapshot`——`tellurion:docs/SPEC.md` 模块化内核目标形态明标「尚未实施」，内联 DDL 亦无此表），**最终归属待裁**（U2，见 §5）——现状登记为 **core 的 `quant`**（**实盘运行域就是 core 进程**）；对账留痕归 `tellurion-risk`；通道配置加密存储，**不入库明文**。
> 上级依据：`tellurion:docs/design/qt-decoupling/data-service-contract-and-ownership.md` §1 —— **DDL 只有一个 owner**，同一个 PG 实例、每服务一个 schema，将来拆库是"按 schema 搬走"而不是重写。
> 本文件是本仓表结构的**唯一真相源**：代码里的建表语句与它不一致时，**改代码**；要改表结构，先改本文件（结构级变更还要立 ADR）。

## 1. 归属表

**无业务表**（当前）。

> ⚠️ **纠正（2026-10-20 审查）**：本文早期版本写「订单/成交/持仓归实盘运行域（`run` 或 `exec`，待定，倾向 `run`）」——**这是错的**。
> `run` 是 `tellurion-orchestration` 的**任务域**（job / batch / walkforward / artifact / worker / `strategy_state` 检查点），
> 把订单放进去等于让"只派活不裁决"的服务拥有交易真相。订单的真相落在 **core 的 `quant`**（**目标表·未建**，最终归属待裁 U2——见 §5）。
>
> 只有一种情况会变：将来要建**独立 `exec` schema** 做柜台状态的本地镜像 —— 那必须**先立 ADR**（数据归属变更属结构级），再改本文 §1，最后写迁移。

| 数据 | 归属 | 说明 |
|---|---|---|
| 订单 / 成交 / 持仓快照 | **目标表·未建**；最终归属待裁（U2，见 §5）——现状登记 **core `quant`**（实盘运行域） | 网关**只透传**，不做本地真相；将来建 `exec` 镜像须先立 ADR |
| `ClientOrderID → BrokerOrderID / ack` 幂等映射 | **待裁（R2）**；本仓不建表、不直连 | 必须**持久**（跨重启、跨进程）；不可用 → `idempotency_store_unavailable` 且**不发送**；候选：柜台原生 client-order-id（优先）或注入 core order store 的 port |
| 通道配置（账户、连接参数） | 网关自有配置，**加密存储** | 密钥走环境 / 密钥管理，**禁止明文进库** |
| 对账留痕 | `tellurion-risk`（`risk` schema） | 网关只提供 `QuerySettlement` 快照；`QueryFills` 用于审计，不是对账输入 |

## 2. 只读输入（不属于本仓，禁止写入）

| 数据 | 真实 owner |
|---|---|
| 订单意图（`OrderIntent`） | core |
| 风控裁决 | `tellurion-risk` / core 进程内 |
| 持仓真相 | 券商柜台 |

本仓**不做**风控判断，也不改订单内容（除了柜台必需的最小单位换算）。

## 3. 红线

1. **真钱红线**：没有对账 + 幂等 + 熔断之前，只允许 `MockTrader`；`XTPTrader` 的构造期必须**强制注入** `SettlementQuerier` + 持久幂等映射 + `HealthChecker`，缺一返回 error（不是 warning）。
2. **禁止明文密钥**：账户/密码/证书走环境或密钥管理；代码与库里都不能出现；`Config.Password` 在序列化 / 日志路径必须脱敏（R8）。
3. **归一化纪律**：券商特有字段在适配器内消化，**不许泄漏进契约**（`BusinessType` 等）。
4. **校验分界**：最小单位 / 委托类型 / 柜台限流 / **交易所申报数量规则**归网关（通道侧纯函数，两实现共用）；数量上限 / 集中度 / T+1 / 止损归 `RiskPolicy`。混用即是 bug（现状 `pkg/live/broker/xtp/xtp.go:387`：规则住在 risk 包、只有 XTP 调用，R5）。
5. **下单幂等**：`(AccountID, ClientOrderID)` 是幂等键；同 ID 同载荷 → 原 ack 不发送；同 ID 不同载荷 → `duplicate_order`；重复提交不得重复下单。
6. **禁止伪造成功**：未实现的能力返回 `not_supported` / `channel_down`，任何路径不得返回「成功形状」（现状 `EmergencyFlatten` 反例见 `ARCHITECTURE.md` §9.1 F4）。

## 4. 数据治理（本仓无业务表）

- **现状（2026-10-23，X6a）**：`internal/gateway` 的 `Mock` 把订单 / 成交 / 持仓全放内存（`map` + `[]Fill`），**没有一条 DDL**，也没有 `database/sql` 依赖；`IdempotencyRecord` 只有内存实现 `MemoryAckStore`，生产必须换成持久 `AckStore`（R2 未裁）。
- 本仓**不建表**：订单 / 成交 / 持仓是**目标表·未建**，最终归属待裁（U2，见 §5；`run` 选项已排除，现状登记 core `quant`）；对账留痕归 `risk`。
- 幂等映射**不建在本仓 schema**：它要么由柜台原生保证，要么走注入 port（R2）；本仓不直连任何表。
- 通道配置（账户、连接参数、证书）**加密存储**，密钥走环境 / 密钥管理：**代码与库里都不许出现明文**（安全红线，高于任何便利）。
- 若将来要建 `exec` schema（柜台状态的本地镜像）：**先立 ADR → 再改本文件 §1 → 最后写迁移**。顺序不能反，这是数据归属变更，属结构级。
- 本仓**不做**数据修复类迁移：柜台是真相源，本地镜像只能重建，不能"修"。

## 5. 待定

- 订单 / 成交 / 持仓快照的最终归属未裁决（U2，集中登记于 `tellurion:docs/design/qt-decoupling/repo-inventory.md` §3.1——单仓只留此指针）；裁决前不建表。
- 幂等映射的持久化落点未裁决（R2：柜台原生 / core order store 注入 port）。
- 对账口径已从「逐笔成交」订正为「账户快照」（`QuerySettlement`）；真实 broker 侧与差异注入验收见 R3。
