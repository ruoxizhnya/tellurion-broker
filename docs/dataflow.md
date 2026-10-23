---
status: active
last-verified: 2026-10-23
verified-by: R2 裁决 + 持久幂等映射落地（ADR 0002 / X6c 幂等持久化）
---

# Dataflow — tellurion-broker

> 目的：把「数据从哪来、在谁手里变形、到哪去」写死。**任何跨仓数据交换都必须能在本图上找到一条边**；找不到 = 这段代码在偷偷建第二条路。

## 1. 主链路

```
core（订单意图 → RiskPolicy.CheckOrder 已裁决）
        │  PlaceOrder(OrderRequest{ClientOrderID, …}) —— 进程内 port
        ↓
  tellurion-broker（适配器）
        ├── MockTrader  ──► 内存 / 文件（开发、回放）
        └── XTPTrader   ──► 券商柜台 ──► 委托回报 / 成交回报
        ↓
  OrderAck / OrderState（受理 ≠ 成交）
        │
        └──► core 的 quant（实盘运行域：订单 / 成交 / 持仓）
                │
                ├── QueryFills ──► 成交 / 费用审计（不是对账输入）
                └── QuerySettlement（账户快照）──► 对账 ──► risk 的对账留痕
```

> 目标 port 见 [`api-schema.md`](api-schema.md) §1；今天的两套旧面（`LiveTrader` / `Broker`）与三个实现见 [`ARCHITECTURE.md`](ARCHITECTURE.md) §9.1 F1。**对账输入是账户快照，不是逐笔成交**（F6）：设计文档旧写的 `QueryFills → 对账` 与代码不符。

## 2. 关键时序

### 下单
1. 调用方先过 `RiskPolicy.CheckOrder`；被拒 → 发 `exec.order_rejected` 事件，**不进网关**。
2. 调用方生成 `ClientOrderID`（重试不变）→ `PlaceOrder`。
3. 网关**原子占位**（`AckStore.Reserve`）：同 ID 已有记录 → 原 `OrderAck`（`IdempotentReplay=true`），**不发送**；同 ID 不同载荷 → `duplicate_order`。并发同 ID 只有一个调用拿到发送权（PG 用 `INSERT ... ON CONFLICT DO NOTHING RETURNING`）。
   - **重放必须还原当初的结论**：当初被柜台拒过（`order_rejected` / `insufficient_buying_power`），这一单就还是被拒的——重放返回 `ack` **与同一个 error**。若重放只给 `ack` + `nil`，调用方会把"被拒"读成"受理成功"（`api-schema.md` §2 要求拒单时 ack 与 error 同时非 nil）。
4. 通道侧校验（最小单位 / 委托类型 / 申报数量 / 限流）→ 不合法 `invalid_order`，未发送。
5. 未连接 / 熔断中 → `channel_down`，未发送。
6. 适配器做柜台级换算 → 发柜台 → 返回 `OrderAck`（**受理 ≠ 成交**）。
7. 成交回报由柜台异步产生 → core 通过查单 / 对账更新运行域状态（端口**没有**推送面，网关不保存订单真相）。

### 超时（最危险的分支）
8. `PlaceOrder` 在**发出之后**超时 / 断连 → `ErrUnknownState`。
9. 调用方必须 `QueryOrder(ClientOrderID)` 确认，**不得重发**。
10. 查单返回 `unknown` → 继续查 / 等对账；查不到且网关能证明未发送 → `order_not_found`；仍不确定 → 告警 + 人工介入（真钱场景不做自动补偿）。

### 撤单
11. `CancelOrder(CancelRequest{ClientOrderID, …})`：已撤再撤 → 同终态 + `nil`；已成交 → `order_not_cancelable`；超时 → `unknown_state`（走查单，不重发撤单）。

### 对账（日终）
12. `QuerySettlement(accountID, asOf)` 取**账户快照**（现金 / 持仓 / 费用）→ 与 core 本地快照 `Reconcile` → 差异进 `risk` 的对账留痕。`Reconcile` 已落 `internal/reconcile`（纯函数，快照口径，F6；差异注入验收在册）——差异**分类与严重度判定**在本仓，**留痕与告警**归 risk（X3，未接）。
13. 生产装配今天（core 侧）仍是**自检形态**：local 与 broker 用同一个视图，差异恒为零；真实 broker 侧 `QuerySettlement` 归 X6c 全量——届时本仓的注入差异验收腿是它的验收样板。

### 熔断与紧急清仓
14. `HealthCheck` 失败 → `channel_down` → 熔断：停止下单 + 告警；恢复需显式重连，不自动重试。
15. `EmergencyFlatten(reason)`：逐标的真实提交并确认；每个 `Sold` 必须有成交依据，未确认进 `Skipped`；未实现 → `not_supported`，**禁止伪造成功**。

    - **清仓单 ID 必须区分轮次**：同一笔持仓反复清仓用同一个 `ClientOrderID`（重入幂等，不重复下单）；但持仓卖掉后**又建回来**要用新 ID——否则会重放上一轮的已成交回执，把一笔**根本没下单**的持仓报成清仓成功（伪造成功）。实现用**持仓代次**参与 ID。
    - **重放的那一单不能算作本次清仓的凭据**：它对应的是上一次的操作 → 只能进 `Skipped`。

## 3. 失败模式（每一处 fail-closed 都要能指名）

| 失败点 | 症状 | 兜底 |
|---|---|---|
| 超时后盲目重发 | 重复下单（真实亏损） | `unknown_state` + 强制查单，禁止重发 |
| 同 ID 不同载荷 | 幂等命中错单 | `duplicate_order` fail-loud，绝不发第二笔 |
| 幂等映射丢 / 不可用 | 重启后重复下单 | 持久映射（`broker.order_acks`）；不可用 → `idempotency_store_unavailable`，**不发送** |
| 并发同 ID 两个调用都读到未命中 | 重复下单 | 发送权由 `AckStore.Reserve` 原子判定；没拿到的一方只能重放 / unknown |
| 柜台断连 | 单发不出去 | `channel_down` 熔断 + 告警，不静默重试 |
| 未实现能力返回成功形状 | kill-switch 显示「已清仓」而没卖 | 未实现 → `not_supported`；`Sold` 必须有成交依据 |
| 对账跑成「自己对自己」 | 差异恒零，假绿 | 真实 broker 侧 `QuerySettlement` + 人为注入差异的验收腿（本仓 `internal/reconcile` 已按此口径建测试） |
| 申报规则只在一个实现里 | Mock 放行、实盘被拒 | 规则归通道侧纯函数，两实现共用（R5） |
| 归一化漏字段 | 上游拿到券商私有字段 | 契约禁 XTP 字段；适配器内消化 |
| 密钥进库 / 进代码 / 进日志 | 泄露 | 环境变量 / 密钥管理；`Config` 脱敏；提交前扫描 |

## 4. 不变量（可被测试钉住的）

1. 同一 `(AccountID, ClientOrderID)` 不会产生两笔柜台订单；同 ID 不同载荷直接 `duplicate_order`。发送权由 `AckStore.Reserve` 的**原子占位**保证（PG / 内存实现同一条判据）。
2. 超时一定返回 `ErrUnknownState`（不是 generic error，也不是 `ErrOrderRejected`）；`unknown` 是**未决**，不是拒绝。
3. 对外 DTO 不含任何券商特有字段（`BusinessType` 等留在适配器）。
4. 没有对账方案 / 持久幂等映射 / 健康检查时，`XTPTrader` 不可被装配（构造期返回 error）。**已由代码强制**：`NewXTPTrader` 缺任一依赖、或 `AckStore.Durable()==false` → `ErrAssemblyIncomplete`（构造路径哨兵，非 wire code），测试钉住。
5. `EmergencyFlatten` 的每个 `Sold` 都能指到一笔真实提交并确认的订单；未实现返回 `not_supported`。
6. 对账输入是 `QuerySettlement` 快照；`QueryFills` 只用于审计，不参与对账判定。
7. 网关不 import 风控裁决；风控拒绝不进网关（`exec.order_rejected` 在调用方侧）。
8. `OrderStatus` 八值以外不存在第九值；它不是 K9 的 run 三值。
9. 回执只能写到**已占位**的行（`SaveAck` 没有占位就报错）；占位未写回执 → 重放必须 `unknown_state`，禁止重发。

## 5. 与契约文档的关系

- 表结构（本仓只持一张通道自有表 `broker.order_acks`）→ `docs/db-schema.md`
- 接口形状 → `docs/api-schema.md`
- 本文件只讲**流向与顺序**，不重复字段定义。
