---
status: active
last-verified: 2026-11-11
verified-by: 2026-11-11 文档对齐审计 —— 校准 §3-4 引用的 core 旧文件行号（`pkg/live/broker/xtp` 已于 2026-11-10 删除）与实现命名（`Mock` / core 旧名 `MockTrader`）；此前 G2 施工（部分索引 + 90 天保留期清理）、R2 裁决 + `broker.order_acks`（ADR 0002）
---

# DB Schema — tellurion-broker

> **所有权声明**：本仓只持有一张**通道自有**的表 `broker.order_acks`（幂等映射的写前日志，ADR 0002）；**不持有订单 / 成交 / 持仓**——那些是**目标表·未建**（`quant.orders` / `fills` / `positions` / `portfolio_snapshot`——`tellurion:docs/SPEC.md` 模块化内核目标形态明标「尚未实施」，内联 DDL 亦无此表），**最终归属待裁**（U2，见 §5）——现状登记为 **core 的 `quant`**（**实盘运行域就是 core 进程**）；对账留痕归 `tellurion-risk`；通道配置加密存储，**不入库明文**。
> 上级依据：`tellurion:docs/design/qt-decoupling/data-service-contract-and-ownership.md` §1 —— **DDL 只有一个 owner**，同一个 PG 实例、每服务一个 schema，将来拆库是"按 schema 搬走"而不是重写。
> 本文件是本仓表结构的**唯一真相源**：代码里的建表语句与它不一致时，**改代码**；要改表结构，先改本文件（结构级变更还要立 ADR）。

## 1. 归属表

本仓只持有一张**通道自有**的表（不是订单真相）：

| 数据 | 归属 | 说明 |
|---|---|---|
| `ClientOrderID → BrokerOrderID / ack` 幂等映射 | **本仓** `broker.order_acks`（ADR 0002，结构见 §1.1） | 发单前的写前日志：`(account_id, client_order_id)` 唯一 + 载荷指纹 + 当初的回执。跨重启 / 跨进程持久；不可用 → `idempotency_store_unavailable` 且**不发送** |
| 订单 / 成交 / 持仓快照 | **目标表·未建**；最终归属待裁（U2，见 §5）——现状登记 **core `quant`**（实盘运行域） | 网关**只透传**，不做本地真相；将来建 `exec` 镜像须先立 ADR |
| 通道配置（账户、连接参数） | 网关自有配置，**加密存储** | 密钥走环境 / 密钥管理，**禁止明文进库** |
| 对账留痕 | `tellurion-risk`（`risk` schema） | 网关只提供 `QuerySettlement` 快照；`QueryFills` 用于审计，不是对账输入 |

> ⚠️ **纠正（2026-10-20 审查）**：本文早期版本写「订单/成交/持仓归实盘运行域（`run` 或 `exec`，待定，倾向 `run`）」——**这是错的**。
> `run` 是 `tellurion-orchestration` 的**任务域**（job / batch / walkforward / artifact / worker / `strategy_state` 检查点），
> 把订单放进去等于让"只派活不裁决"的服务拥有交易真相。订单的真相落在 **core 的 `quant`**（**目标表·未建**，最终归属待裁 U2——见 §5）。
>
> 只有一种情况会变：将来要建**独立 `exec` schema** 做柜台状态的本地镜像 —— 那必须**先立 ADR**（数据归属变更属结构级），再改本文 §1，最后写迁移。

### 1.1 `broker.order_acks`（本仓唯一的表）

DDL 代码在 `internal/ackstore/ddl.go`，执行入口 `ackstore.Migrate`（启动时一次；幂等、无版本表）。

```sql
CREATE SCHEMA IF NOT EXISTS broker;

CREATE TABLE IF NOT EXISTS broker.order_acks (
    account_id          TEXT        NOT NULL,
    client_order_id     TEXT        NOT NULL,
    request_fingerprint TEXT        NOT NULL CHECK (request_fingerprint <> ''),
    ack                 JSONB,                  -- NULL = 已占位、结果未知
    reserved_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, client_order_id)
);

-- 保留期清理的部分索引（第三轮裁决 ⑨，G2）：只索引已回执的行
CREATE INDEX IF NOT EXISTS order_acks_updated_at_acked_idx
    ON broker.order_acks (updated_at) WHERE ack IS NOT NULL;
```

不变量：

1. **一行 = 一个 `(account_id, client_order_id)` 的发送权**。`INSERT ... ON CONFLICT DO NOTHING RETURNING` 决定谁拿到；没拿到的一方只能重放 / 报 unknown，不能发送。
2. `ack IS NULL` 表示**已占位但结果未知**（发出途中崩溃）→ 加载到这种行必须 `unknown_state`，禁止重发。
3. `request_fingerprint` 只由占位写入；写回执不得改写它（否则「同 ID 不同载荷」的判据会被洗掉）。
4. 这张表**不是订单账本**：不存成交 / 持仓 / 费用；删行 = 允许重复下单，保留期必须按「不早于柜台可查窗口」定（当前 **90 天暂定**，见 §5）。
5. **保留期清理只删 `ack IS NOT NULL` 的行**（`internal/ackstore/retention.go`）：`ack IS NULL` 是「可能已发出而不知结果」的唯一证据，**永不被清理**；单批 ≤ 5000 行，批间是独立语句（放锁）。清理作业形态 = 网关进程内 24h 周期任务（给了 `BROKER_DATABASE_DSN` 才启用）。

## 2. 只读输入（不属于本仓，禁止写入）

| 数据 | 真实 owner |
|---|---|
| 订单意图（`OrderIntent`） | core |
| 风控裁决 | `tellurion-risk` / core 进程内 |
| 持仓真相 | 券商柜台 |

本仓**不做**风控判断，也不改订单内容（除了柜台必需的最小单位换算）。

## 3. 红线

1. **真钱红线**：没有对账 + 幂等 + 熔断之前，只允许 Mock（本仓实现名 `Mock`；core 旧名 `MockTrader` 已随 2026-11-10 退役）；`XTPTrader` 的构造期必须**强制注入** `SettlementQuerier` + 持久幂等映射 + `HealthChecker`，缺一返回 error（不是 warning）。持久性由 `AckStore.Durable()` 机械判定——`MemoryAckStore` 会被拒绝。
2. **禁止明文密钥**：账户/密码/证书走环境或密钥管理；代码与库里都不能出现；`Config.Password` 在序列化 / 日志路径必须脱敏（R8）。
3. **归一化纪律**：券商特有字段在适配器内消化，**不许泄漏进契约**（`BusinessType` 等）。
4. **校验分界**：最小单位 / 委托类型 / 柜台限流 / **交易所申报数量规则**归网关（通道侧纯函数，两实现共用）；数量上限 / 集中度 / T+1 / 止损归 `RiskPolicy`。混用即是 bug（历史基线：core `pkg/live/broker/xtp/xtp.go:387` 调 `risk.ValidateOrderQuantity`，该文件已于 2026-11-10 删除；规则本体 `tellurion:pkg/risk/lot.go:136` **尚未退役**，R5 的跨仓腿仍在）。
5. **下单幂等**：`(AccountID, ClientOrderID)` 是幂等键；**发送权由 `AckStore.Reserve` 原子判定**（并发同 ID 只能出一个 winner）；同 ID 同载荷 → 原 ack 不发送；同 ID 不同载荷 → `duplicate_order`；重复提交不得重复下单。
6. **禁止伪造成功**：未实现的能力返回 `not_supported` / `channel_down`，任何路径不得返回「成功形状」（现状 `EmergencyFlatten` 反例见 `ARCHITECTURE.md` §9.1 F4）。

## 4. 数据治理

- **现状（2026-10-23，X6c 幂等持久化）**：`internal/gateway` 的 `Mock` 把订单 / 成交 / 持仓全放内存（`map` + `[]Fill`），这部分**没有 DDL**；本仓唯一的 DDL 是 `broker.order_acks`（`internal/ackstore/ddl.go`）。生产实现 `ackstore.PgStore`（pgx 连接池由调用方持有），`MemoryAckStore` 只用于开发 / 测试（`Durable=false`，进不了 XTP 装配闸）。
- 订单 / 成交 / 持仓仍是**目标表·未建**，最终归属待裁（U2，见 §5；`run` 选项已排除，现状登记 core `quant`）；对账留痕归 `risk`。
- `broker.order_acks` **只存幂等映射，不存订单真相**：不进成交 / 持仓 / 费用字段；必须进备份策略（丢失 = 重复下单风险）；保留期见 §5。
- 通道配置（账户、连接参数、证书）**加密存储**，密钥走环境 / 密钥管理：**代码与库里都不许出现明文**（安全红线，高于任何便利）。
- 若将来要建 `exec` schema（柜台状态的本地镜像）：**先立 ADR → 再改本文件 §1 → 最后写迁移**。顺序不能反，这是数据归属变更，属结构级。
- 本仓**不做**数据修复类迁移：柜台是真相源，本地镜像只能重建，不能"修"。

## 5. 待定

- 订单 / 成交 / 持仓快照的最终归属未裁决（U2，集中登记于 `tellurion:docs/design/qt-decoupling/repo-inventory.md` §3.1——单仓只留此指针）；裁决前不建表。
- `broker.order_acks` 的**保留期 = 90 天（暂定假设）**：只删 `ack IS NOT NULL` 且 `updated_at` 早于 90 天的行，先走部分索引 `order_acks_updated_at_acked_idx`，单批 ≤ 5000（第三轮裁决 ⑨，`internal/ackstore/retention.go`）。
  - **口径已定（2026-11-10，主任采纳 B）**：保留期判据 = **不早于柜台可查窗口**；90 天是 P2 到手前的占位值，本轮不改 `ackstore.DefaultRetention`。
  - **复核触发条件**：柜台可查窗口一旦已知（X6c 的 P2 测试账号 / 柜台文档到手）⇒ 按 `min(柜台可查窗口, 现行值)` 重算 `ackstore.DefaultRetention`，同笔改本节与 ADR 0002。
- 对账口径已从「逐笔成交」订正为「账户快照」（`QuerySettlement`）；真实 broker 侧与差异注入验收见 R3。
