# AGENTS — tellurion-broker

> 本文件是**本仓**的唯一入口。判据：一个 agent 只 checkout 本仓，就能知道「怎么构建、边界在哪、依赖哪版契约、活在哪、禁止什么」，**不需要读别的仓的源码**。

## 1. 这是什么仓

交易通道网关：`XTPTrader` / `MockTrader` = 同一 port 的两个实现。**落地前必须先有对账方案**。

**状态**：已启动（2026-10-23 施工 ③）——X6a 四 port + 内存 `Mock` 已落 `internal/gateway`；X6b 对账纯函数（快照口径 + 差异注入验收）已落 `internal/reconcile`；X6c 装配闸 + fail-closed 骨架 + **持久幂等映射 `broker.order_acks`**（`internal/ackstore`，ADR 0002）已落；X6c 全量适配器（SDK）未做；X6d 按同日裁决**暂缓**

**今天的实现**（本仓，2026-10-23）：`internal/gateway` 的 `BrokerGateway` / `SettlementQuerier` / `KillSwitch` / `HealthChecker` 四个 port、DTO、12 个契约哨兵、`Mock`（`ClientOrderID` 幂等 + 写前日志 + `unknown_state`）、`MemoryAckStore`、`XTPTrader`（fail-closed 骨架：构造期缺 `AckStore` / `SettlementQuerier` / `HealthChecker` 任一、或 `Acks.Durable()==false` 即拒绝，已装配后所有方法恒 `channel_down`，零 fake success）；`internal/reconcile` 的快照口径对账纯函数；`internal/ackstore` 的持久幂等映射 `broker.order_acks`（原子 `Reserve` + 回执写回）。**未接 core、无 XTP SDK 链路、未接真实柜台**（PG 只用于幂等映射）；细节见 `docs/ARCHITECTURE.md` §9.8。

**迁出前的老实现**（core，待切读后退役）：`core:pkg/live`（`MockTrader` / order store / paper trading）+ `core:pkg/live/broker/xtp`（XTP 骨架）。本仓只搬**通道适配**，运行域留 core。

## 2. 构建 / 测试（必须单仓可跑）

```bash
go build ./...
go vet ./...
go test ./...
```

- 这三条是 R1/R2 的判据：**单独 checkout 就能绿**。
- 可选集成测试（无外部条件时自动 skip）：`BROKER_TEST_DSN='postgres://postgres@127.0.0.1:5432/tellurion_broker_test?sslmode=disable' go test ./internal/ackstore -count=1`（测 `broker.order_acks` 的原子占位 / 跨实例回读）。
- 若某一步需要别的仓的源码目录才过，那是**边界画错了**——停下来修边界，**不要**加 `replace ../xxx` 绕过。
- 本机同时改多个仓时可用 `go.work` 联调，但**它不入库**（`.gitignore` 已挡）。

## 3. 边界（硬约束）

**允许**

- 券商柜台适配（XTP 等）与 Mock 实现
- 订单/成交回报的可靠投递与幂等
- 与 core 的对账（日终）

**禁止**

- 在这里做策略或风控裁决（网关只转发与确认）
- import core 的实现包
- 在没有对账方案的情况下接真钱通道

下面三条对所有仓都成立：

- **禁止** `replace` 指向仓外路径（单仓就不可构建了）。
- **禁止**提交 `go.work` / `go.work.sum`（本地产物；已在 `.gitignore`）。
- 新增第三方依赖前，先在第 4 节登记，再改 `go.mod`；没登记就加依赖 = 绕过评审。

## 4. 依赖的契约版本

| 依赖 | 版本 | 说明 |
|---|---|---|
| `github.com/ruoxizhnya/tellurion-contracts` | 未引入（最新 `v0.3.0`，2026-10-23 发版） | `BrokerGateway` 契约 |
| `github.com/ruoxizhnya/tellurion-kit` | 未引入（`v0.1.0` 已发版） | 日志 / HTTP |
| `github.com/jackc/pgx/v5` | `v5.7.3` | `internal/ackstore` 的 PG 连接池与查询（幂等映射 `broker.order_acks`，ADR 0002）；版本与 core / kit / strategy 对齐 |

> 今日 `go.mod` 的**唯一直接依赖**是 `pgx v5.7.3`（其余是它的间接依赖）：`internal/gateway` / `internal/reconcile` 仍只用 stdlib；port / DTO 暂由本仓定义，`BrokerGateway` 随 X6a 定稿后落 contracts（R4）。

> 跨仓依赖**只认版本号**（伪版本或 tag），不认源码路径。契约变更走 `tellurion-contracts` 的发布，不走"顺手改一下"。

## 5. 活在哪、文档在哪

- **本仓台账**：`docs/TASKS.md`（唯一）。**没有单号别开工。**
- 文档导航：`docs/README.md`；边界与依赖方向：`docs/ARCHITECTURE.md`；决策：`docs/adr/`。
- 跨仓背景（只读，不在本仓改）：`tellurion:docs/design/qt-decoupling/repo-topology-and-naming.md`、`tellurion:docs/TASKS.md`。

> 跨仓引用一律写 `repo:path` 纯文本，**不做可点击相对链接**——独立 checkout 时 `../../` 必断，R2 会永远红。

## 6. 文档真相源（改功能前先看这里）

本仓的「功能边界」由三份文件钉住，**它们是唯一真相源，不是参考读物**：

| 维度 | 文件 |
|---|---|
| 数据形状（我拥有哪些表、谁能写） | `docs/db-schema.md` |
| 接口形状（对外暴露什么、错误码、幂等） | `docs/api-schema.md` |
| 流向与顺序（数据怎么走、哪里 fail-closed） | `docs/dataflow.md` |

**变更触发是硬约束**（完整表见 `docs/README.md` §3）：改表 → 改 `db-schema.md`；改接口 → 改 `api-schema.md`；改链路 → 改 `dataflow.md`；改分层 → 改 `ARCHITECTURE.md` + 一条 ADR；删档/迁移/换工具 → 一条 ODR。PR 里代码动了而对应文档没动，应被打回。
