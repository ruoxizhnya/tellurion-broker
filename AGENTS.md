# AGENTS — tellurion-broker

> 本文件是**本仓**的唯一入口。判据：一个 agent 只 checkout 本仓，就能知道「怎么构建、边界在哪、依赖哪版契约、活在哪、禁止什么」，**不需要读别的仓的源码**。

## 1. 这是什么仓

交易通道网关：`Mock` / `XTPTrader` = 同一 port 的两个实现（`MockTrader` 是 core 旧名，已随 2026-11-10 退役）。**落地前必须先有对账方案**。

**状态**：已启动（2026-10-24 施工 ④ ＋ 2026-10-25 施工 ⑤ = G7）——X6a 四 port + 内存 `Mock` 已落 `internal/gateway`；X6b 对账纯函数（快照口径 + 差异注入验收）已落 `internal/reconcile`；X6c 装配闸 + fail-closed 骨架 + **持久幂等映射 `broker.order_acks`**（`internal/ackstore`，ADR 0002）已落；R5 申报数量纯函数（`internal/gateway/lot`）已落并让 Mock 共用；**X6c 全量适配器 ⏸ 挂起**（2026-11-10 主任授权、执行侧裁定：**暂不投入真钱通道实施**，本阶段终态 = Mock 全绿 + fail-closed 骨架；不是永久不做，重开条件 = P1~P5 同时满足后另立施工单 + 真钱形态 ADR，见 `docs/adr/0003-真钱通道暂不实施与重开条件.md` 与 `docs/x6c-xtp-readiness.md` §12）；R10（映射读失败报成 `channel_down`）已落（2026-11-07），R9 已裁暂不收紧 + R8 密钥形状已定 + `order_acks` 保留期口径已定（2026-11-10，B 组债，见 `docs/x6c-xtp-readiness.md` §11）；`broker.order_acks` 的 G2（部分索引 + **90 天暂定**保留期，只删 `ack IS NOT NULL`，单批 ≤ 5000，进程内 24h 清理）代码已落、PG 集成腿已真跑（9 条全 PASS，2026-11-07）；G1 订单/查询/对账 HTTP 面已落（`/api/broker`，写侧 `BROKER_SERVICE_TOKEN`，未设即关闭写面），**core 侧消费已切到本仓 HTTP 面**（2026-11-10，`SVC-CORE-BROKER-RETIRE`：core 默认通道 = broker HTTP，`BROKER_URL` 缺失即装配失败，旧进程内 MockTrader 已删除）；G4 失败语义六类 + 就绪口径（`/healthz` 唯一探针，就绪 = 装配闸通过）+ `docs/runbook.md` + 内存演练 + **真 PG 停库→恢复演练**已落（2026-11-07 ✅）；**X6d 已由 ADR-036（2026-10-26）取代**——本仓定位为**服务**，G0 已落 `cmd/brokergateway`（`/healthz`、`/version`）+ `Dockerfile` + `deploy/compose.yaml`（`:8091`）；订单 HTTP 面已在 `SVC-BROKER-G1` 落地；真实 XTP 仍 ⛔（X6c）；**QT-45 已收口 ✅（2026-11-10）：终态 = Mock 全链路全绿 + `XTPTrader` fail-closed 骨架 + 装配闸 + DTO 契约测试；「XTP 全量适配器」转 X6c 挂起项（不计入开放项），逐条证据见 `docs/TASKS.md` QT-45 终态落账与 `docs/adr/0003-真钱通道暂不实施与重开条件.md`**

**今天的实现**（本仓，2026-10-24）：`internal/gateway` 的 `BrokerGateway` / `SettlementQuerier` / `KillSwitch` / `HealthChecker` 四个 port、DTO、12 个契约哨兵、`Mock`（`ClientOrderID` 幂等 + 写前日志 + `unknown_state`）、`MemoryAckStore`、`XTPTrader`（fail-closed 骨架：构造期缺 `AckStore` / `SettlementQuerier` / `HealthChecker` 任一、或 `Acks.Durable()==false` 即拒绝，已装配后所有方法恒 `channel_down`，零 fake success）；`internal/reconcile` 的快照口径对账纯函数；`internal/ackstore` 的持久幂等映射 `broker.order_acks`（原子 `Reserve` + 回执写回）；`internal/gateway/lot` 的申报数量纯函数（R5，Mock 共用）。**未接真实柜台、无 XTP SDK 链路**（core 已接本仓 HTTP 面；PG 只用于幂等映射）；细节见 `docs/ARCHITECTURE.md` §9.8。

**迁出前的老实现**（core，**已于 2026-11-10 退役删除**，`SVC-CORE-BROKER-RETIRE`）：`core:pkg/live`（`MockTrader` / order store / paper trading）+ `core:pkg/live/broker/xtp`（XTP 骨架）；运行时 core 保留 `pkg/trading` 的运行域（订单/持仓/结算类型、paper 会话、对账算法），通道侧 `pkg/brokerhttp` 只做 HTTP 客户端。本仓只搬**通道适配**，运行域留 core。

## 2. 构建 / 测试（必须单仓可跑）

> **构建前置：Go ≥ 1.26.11**（`go.mod` 的 `go` directive 已钉在 `1.26.11`）。旧工具链（1.25.x）能编过，但会让 `govulncheck ./...` 红——那 9 条 stdlib 告警只能靠工具链解，降版验收不成立。理由记录见 `docs/runbook.md` §11。

```bash
go build ./...
go vet ./...
go test ./...
```

- 这三条是 R1/R2 的判据：**单独 checkout 就能绿**。
- 可选集成测试（无外部条件时自动 skip）：`BROKER_TEST_DSN='postgres://postgres@127.0.0.1:5432/tellurion_broker_test?sslmode=disable' go test ./internal/ackstore -count=1`（测 `broker.order_acks` 的原子占位 / 跨实例回读）。
- 若某一步需要别的仓的源码目录才过，那是**边界画错了**——停下来修边界，**不要**加 `replace ../xxx` 绕过。
- 本机同时改多个仓时可用 `go.work` 联调，但**它不入库**（`.gitignore` 已挡）。
- CI（`.github/workflows/ci.yml`，单 job `go`）：`gofmt -l .` → `go build ./...` → `go vet ./...` → `govulncheck@latest ./...`（依赖漏洞门，X-DEP-BUMP-CONSUMERS）→ **Migrate（`ackstore.Migrate`，即 `TestPgStore_MigrateIsIdempotent`）** → `go test ./... -count=1 -coverprofile=coverage.out` + **覆盖率门**（BROKER-CI-GATES）→ `go test -race ./... -count=1`（竞态门，X-CI-RACE-STATICCHECK）→ `staticcheck@latest ./...`（`go install` 后跑，未钉版本）。
- **CI 的 DB 腿（BROKER-CI-GATES，2026-11-13）**：Test job 起 `postgres:16` service（`pg_isready`）+ job 级注入 `BROKER_TEST_DSN`（CI 内临时库 `tellurion_broker_test`，**绝不指向部署库**）。Test 前先跑 `go test ./internal/ackstore -run '^TestPgStore_MigrateIsIdempotent$' -count=1 -v` 应用迁移——入口就是服务启动同款 `ackstore.Migrate`（`ddl.go` 是 `broker` schema 唯一建表真相，幂等只前滚），不另造 `.sql` 镜像、不复制 core 脚本。效果：库侧 **9 条**用例由 SKIP 转 PASS（带 DSN 74 PASS / 0 SKIP，无 DSN 65 PASS / 9 SKIP）；**本地不设 DSN 仍按设计 skip**。
- **覆盖率门（BROKER-CI-GATES）**：阈值 **73%**（语句加权，`go tool cover -func` 的 total），来源 = 2026-11-13 在隔离 PG 16 上真跑全套件实测 **78.0%**（768 条语句），取整 78 减 5（跨仓口径见 `tellurion:docs/TASKS.md` → `X-COVERAGE-FLOOR`）。低于阈值 `exit 1`。本仓测试面小，**删单个可编译测试文件即可真红**：删 `internal/gateway/mock_test.go` ⇒ 41.3%（门红），删到破坏编译的用例则测试步 `EXIT=1`。如实登记边界：无 DSN 时实测 **73.4% > 73%**（DB 腿只贡献 4.6 点），漏配 DSN 这一项本仓**不**触发红，门的定位是防累计漂移 + 抓大块测试丢失。

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
| `github.com/ruoxizhnya/tellurion-contracts` | `v0.10.0`（2026-11-10 升，`c00d9b1`） | `broker` 包 = 订单 / 成交 / 持仓 / 账户 / 紧急清仓 wire 形状的唯一 owner；本仓 `internal/gateway/model.go` 只留类型别名（CT-05 收编） |
| `github.com/ruoxizhnya/tellurion-kit` | 未引入（远端最新 `v0.5.0`，2026-11-12 核实） | 日志 / HTTP |
| `github.com/jackc/pgx/v5` | `v5.11.0`（2026-11-12 升，`BROKER-DEP-BUMP`） | `internal/ackstore` 的 PG 连接池与查询（幂等映射 `broker.order_acks`，ADR 0002）；旧 `v5.7.3` 命中 GO-2026-5004（修复于 `v5.9.2`） |
| `golang.org/x/text`（indirect） | `v0.44.0`（2026-11-12 随 `go mod tidy` 升） | pgx 的传递依赖；旧 `v0.21.0` 命中 GO-2026-5970 / GO-2026-6629（`x/text/secure/precis` panic，修复于 `v0.41.0`） |
| Go 工具链（`go` directive） | `1.26.11`（2026-11-12 升） | 旧 `1.25` 使 stdlib 命中 9 条告警（`net/http` / `net/textproto` / `crypto/tls` / `crypto/x509`，最高要求修复版 `1.26.11`）；`govulncheck ./...` EXIT=0 的前提 |

> `go.mod` 的直接依赖 = `pgx v5.11.0` + `tellurion-contracts v0.10.0`（2026-11-09 CT-05 收编后）；`internal/gateway` / `internal/reconcile` 的其余依赖仍是 stdlib。DTO 字段定义住在 `tellurion-contracts/broker`，本仓只做类型别名与自由函数 `Fingerprint`。

> 跨仓依赖**只认版本号**（伪版本或 tag），不认源码路径。契约变更走 `tellurion-contracts` 的发布，不走"顺手改一下"。

## 5. 活在哪、文档在哪

- **本仓台账**：`docs/TASKS.md`（唯一）。**没有单号别开工。**
- 文档导航：`docs/README.md`；边界与依赖方向：`docs/ARCHITECTURE.md`；运维（起停 / 装配闸 / 幂等映射 / `unknown_state` / 对账 / KillSwitch）：`docs/runbook.md`；决策：`docs/adr/`。
- 跨仓背景（只读，不在本仓改）：`tellurion:docs/design/qt-decoupling/repo-topology-and-naming.md`、`tellurion:docs/TASKS.md`。

> 跨仓引用一律写 `repo:path` 纯文本，**不做可点击相对链接**——独立 checkout 时 `../../` 必断，R2 会永远红。

## 6. 文档真相源（改功能前先看这里）

本仓的「功能边界」由三份文件钉住，**它们是唯一真相源，不是参考读物**：

| 维度 | 文件 |
|---|---|
| 数据形状（我拥有哪些表、谁能写） | `docs/db-schema.md` |
| 接口形状（对外暴露什么、错误码、幂等） | `docs/api-schema.md` |
| 流向与顺序（数据怎么走、哪里 fail-closed） | `docs/dataflow.md` |

**变更触发是硬约束**（完整表见 `docs/README.md` §3）：改表 → 改 `db-schema.md`；改接口 → 改 `api-schema.md`（就绪 / 排查口径同时改 `runbook.md`）；改链路 → 改 `dataflow.md`；改分层 → 改 `ARCHITECTURE.md` + 一条 ADR；删档/迁移/换工具 → 一条 ODR。PR 里代码动了而对应文档没动，应被打回。
