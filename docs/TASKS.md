---
status: active
last-verified: 2026-11-11
verified-by: 2026-11-11 全面审查 W7 台账立单（A5 授权）——新立 3 条六字段单（`BROKER-DEP-BUMP` / `BROKER-AUTH-ADR` / `BROKER-RETRY-AFTER`），证据坐标引 ODR-072 / ODR-069（`tellurion-docs:docs/archive/odr/`）；本轮只立单不修复。此前 QT-45 / X6c 终态裁决（2026-11-10：主任授权执行侧裁定 ⇒ `QT-45` 收口 ✅（Mock 全绿 + fail-closed 骨架 + 装配闸为终态）、`X6c` 转 ⏸ 挂起（外部依赖 P1~P5，重开条件与流程见 ADR-0003）；B 组债务结论与 C 组验收清单保留）；QT-45 收口复核（2026-11-10：⑤ 三条判据逐条点名用例全绿，③ ① 「两实现」因 XTP 仍是 fail-closed 骨架未完成 ⇒ 🔶 → ⛔，唯一硬前置 = X6c P1~P5；同轮 core R4 退役已确认完成）；SVC-BROKER-G3 前置复核（2026-11-10：QT-51 已合入 `a2e388a`、`main:pkg/backtest/execution/port.go` EXIT=0，叠加 G1 HTTP 面已落 ⇒ 两道前置全部解除，状态 ⛔ → ⬜；施工四步逐条列明，未做一步不勾；X6c 仍 ⛔）；G4 ✅（失败语义六类 / 就绪口径 / runbook / 内存演练 + 真 PG 停库→恢复演练与幂等映射核对，2026-11-07 晚补齐）+ G1 ✅（订单/查询/对账 HTTP 面 + 12 码映射 + 键集用例 + 真实二进制 smoke）+ G2 ✅（PG 集成腿真跑 9 条全 PASS）+ R10 施工；此前 G7（施工 ⑤）X6c ⛔ 判定、R2 裁决、R5 申报数量纯函数
---

# TASKS — tellurion-broker 未完成项

> **归档说明（2026-11-11）**：本文件已瘦身为「只装未做的事」。2026-11-10 及以前已收口（✅）的任务原文、施工记录、验收输出与破坏验证，已逐字移入 [`docs/archive/tasks-archive-2026-11.md`](archive/tasks-archive-2026-11.md)；该文件顶部有「检索索引」表（ID / 标题 / 收口日期 / commit / 证据链接）。检索：`grep -n "<任务ID或commit>" docs/archive/tasks-archive-2026-11.md`。
>
> 状态：`⬜ 待做` / `🔶 进行中` / `⛔ 阻塞（有未解除前置）` / `⏸ 挂起（外部依赖或已裁决暂不实施；**不计入开放项**，重开须另立单并满足该条「重开条件」）` / `✅ 完成（附日期，随即移入归档）`

## 一、未做 / 挂起 / 外部依赖

> 本节 = 本轮（2026-11-11）台账重整后剩下的挂起项（`X6c` 及其派生项）+ 全面审查 W7 新立 3 单（`BROKER-DEP-BUMP` / `BROKER-AUTH-ADR` / `BROKER-RETRY-AFTER`）。此前已收口单的正文与施工记录全部在归档文件里。

### X6c · 真钱通道（XTP 全量适配器）[P1] ⏸（外部依赖）

- **① 目标**：把 `XTPTrader` 从 fail-closed 骨架做成可上真钱的适配器。
- **② 上下文**：`docs/adr/0003-真钱通道暂不实施与重开条件.md`（2026-11-10 裁决）定本阶段终态 = Mock 全链路全绿 + `XTPTrader` fail-closed 骨架（零 fake success）+ 装配闸；阻塞条件见 `docs/x6c-xtp-readiness.md` §4 的 P1~P5（SDK / C 工具链 / 柜台网络 / 测试账号 / 可盯盘时间窗）。
- **③ 要求**：重开时按 ADR 写死的流程：① P1~P5 逐项取证；② 立 X6c 施工单（下单/撤单/查单/回报/心跳/重连/限流 + 柜台错误码映射 + 真实 `QuerySettlement`）；③ 立真钱形态 ADR；④ ADR-0003 标 `Superseded by 000N`。
- **④ 约束与非目标**：重开前禁止任何「已支持实盘」表述；B 组债务（保留期 90 天占位、R8 密钥形状、R9 暂不收紧）与 C 组接入验收清单**原样作为准入清单**，不得丢失。
- **⑤ 验收**：ADR-0003 的重开流程四步全部完成。
- **⑥ 边界**：本仓通道实现；真钱形态的进程拓扑另立 ADR。
- **重开条件**：`docs/x6c-xtp-readiness.md` §4 的 P1~P5 **同时满足**。

### X6c-B · `/version` 的 `channel` 改为由实际装配的通道派生 [P1] ⏸

- **① 目标**：`/version.channel` 反映**真正装配**的通道，不再是手写常量。
- **② 上下文**：`cmd/brokergateway/main.go` 的 `/version` 当前返回写死的 `"channel": "mock"`（注释也写死「供 core 启动期核对」）。一旦存在多于一种通道实现或引入配置切换，这个常量就会骗过 core 的启动期核对（core 侧按 `channel` 决定是否接受，见 `tellurion:pkg/brokerhttp/trader.go`）。
- **③ 要求**：① `channel` 从装配点（实际构造的 `BrokerGateway` 实现）取值，保留单一真相；② 未知通道不得回落到 `mock`；③ 补一条「装配 X ⇒ `/version` 报 X」的用例。
- **④ 约束与非目标**：不改 `/version` 的其余字段与 core 的核对契约；不新增通道实现。
- **⑤ 验收**：换装配实现后 `/version.channel` 随之变化；破坏验证 = 改回常量 ⇒ 用例红。
- **⑥ 边界**：本仓 server 装配；core 侧消费归 `tellurion`（`CORE-BROKER-RUNTIME-RECHECK`）。
- **重开条件**：`X6c` 重开时**必须同批完成**（属 X6c 的准入项，不可单独跳过）。

### BROKER-DEP-BUMP · pgx / x-text 落在已知漏洞版本（govulncheck EXIT=1）[P0] ⬜

- **① 目标**：把本仓 `go.mod` 的 pgx 与 x/text 升到无已知漏洞的版本，`govulncheck ./...` 退出码归 0，消除供应链卫生债。
- **② 上下文（2026-11-11 全面审查 W5-F1，证据 ODR-072 `tellurion-docs:docs/archive/odr/odr-072-audit-wave5-security-reliability-deps.md`；裁决 D1=b：pgx 一步收敛到 v5.11.0）**：`go.mod:6` `github.com/jackc/pgx/v5 v5.7.3`、`:16` `golang.org/x/text v0.21.0 // indirect`（go.sum 复核同版）。W5 govulncheck E1 实测 10/12 仓 EXIT=1，本仓在列。pgx 的 SQLi 告警**实际暴露面低**——`internal/ackstore/pg.go:66-73` `Reserve` 用 `$1/$2/$3` 参数化、`tableName` 是常量拼接非用户输入（ODR-072 Lessons Learned ①「难利用不是不修的理由」）；修法是依赖 bump，不是改查询。跨仓 pgx 三档并存（core/kit/data/strategy/agent/orch/broker=v5.7.3，risk=v5.7.5，analytics=v5.11.0），本单只收敛**本仓**到 v5.11.0，不做全组 tidy。
- **③ 要求**：① `go get github.com/jackc/pgx/v5@v5.11.0` + `go mod tidy`，x/text 随 tidy 升到无漏洞版（或显式 `go get golang.org/x/text@latest` 取补丁版）；② `govulncheck ./...` EXIT=0；③ `internal/ackstore` 的 PG 集成腿（`BROKER_TEST_DSN`，`tellurion_broker_test` 库）真跑全 PASS，确认 v5.7.3→v5.11.0 无行为回归（`pgxpool`/`pgx.ErrNoRows`/`ON CONFLICT` 语义不变）；④ AGENTS.md §4 依赖表同步新版本。
- **④ 约束与非目标**：不改 `Reserve`/`Load`/`SaveAck` 的 SQL 与幂等语义；不引入 kit（本仓今天不 require kit）；不动 contracts pin（已 v0.10.0）；不在本单顺手统一别仓 pgx；不提交 `go.work`。
- **⑤ 验收**：`govulncheck ./...` EXIT=0（真退出码，非管道 tail）；`go build/vet/test ./... -count=1` 全绿；`BROKER_TEST_DSN=... go test ./internal/ackstore -count=1` 全 PASS（数 PASS 行，非「no tests to run」）；破坏验证 = 把 pgx 降回 v5.7.3 ⇒ govulncheck 复红，还原复绿。
- **⑥ 边界**：本仓 `go.mod`/`go.sum` + AGENTS.md §4；kit pin 的 7 消费仓 bump 归 `tellurion:docs/TASKS.md` → `X-DEP-BUMP-CONSUMERS`（本仓不 require kit，不在该批内）。
- **owner**：待定

### BROKER-AUTH-ADR · 写侧静态 token / 读侧开放的鉴权姿态无 ADR，与兄弟仓 kit JWT 分叉 [P2] ⬜

- **① 目标**：把本仓「写侧静态 `BROKER_SERVICE_TOKEN` + 读侧开放靠端口隔离」的鉴权姿态固化成一条 ADR，显式登记它与兄弟仓 kit JWT service-token 的**分叉例外**，并在 runbook 写清 token 轮换——而不是让这套决定只散在代码注释里。
- **② 上下文（2026-11-11 全面审查 W2-F4，证据 ODR-069 `tellurion-docs:docs/archive/odr/odr-069-audit-wave2-core-architecture.md`；裁决 D11=b：保留 dev-only 静态 token + 登记例外 + runbook 轮换，本轮不迁 kit JWT）**：`internal/httpapi/httpapi.go:74-89` `requireToken`——空 `WriteToken` ⇒ 503 `writes_disabled`（fail-closed ✅）、`subtle.ConstantTimeCompare` 常量时间比较 ✅、不匹配 ⇒ 401 `unauthorized` ✅；挂在 3 条写端点（POST orders / DELETE orders / POST emergency-flatten）。**5 条 GET 读端点不鉴权**（`:63` orders/{id}、`:65` positions、`:66` account、`:67` fills、`:68` settlement），靠默认回环端口隔离。兄弟仓（data/risk/analytics/strategy/orch）走 kit `auth/httpadapter` 的 JWT service-token + 策略表，本仓是**静态 token 分叉**。`docs/adr/` 现有 0001/0002/0003，无鉴权姿态 ADR。
- **③ 要求**：① 新增 `docs/adr/0004-鉴权姿态与静态token分叉.md`（编号续 0003），记录：为何写侧用静态 token（Mock MVP + 单人自托管）、为何读侧开放（端口隔离）、与 kit JWT 兄弟仓的分叉是**显式接受的例外**、dev-only 边界（上真钱前必须重裁，挂 X6c 准入）；② `docs/runbook.md` 补 `BROKER_SERVICE_TOKEN` 轮换步骤与「空 token = 写面关闭」语义；③ `docs/api-schema.md` 鉴权节加指回 ADR；④ `docs/adr/README.md` 索引登记。
- **④ 约束与非目标**：**本轮不改鉴权代码**（不迁 kit JWT、不动 `requireToken`、不给读端点加闸）——纯决策固化 + 文档；不放宽 fail-closed；不把静态 token 表述成「生产级鉴权」；真钱形态的鉴权重裁归 X6c。
- **⑤ 验收**：ADR-0004 存在且 Status=Accepted、含 Context/Decision/Consequences 与「dev-only / X6c 重开重裁」边界；runbook 有轮换步骤；api-schema 有指回链接；链接巡检活跃层 0 断链。
- **⑥ 边界**：本仓 `docs/adr/` + `docs/runbook.md` + `docs/api-schema.md` 鉴权节；鉴权实现升级（若将来迁 kit）另立单挂 X6c。
- **owner**：待定

### BROKER-RETRY-AFTER · 429 `rate_limited` 不带 Retry-After [P3] ⬜

- **① 目标**：让本仓唯一的 429（`rate_limited`）带 `Retry-After` 头，调用方知道何时重试，与 risk 等兄弟仓口径一致。
- **② 上下文（2026-11-11 全面审查 W2-F6，证据 ODR-069 `tellurion-docs:docs/archive/odr/odr-069-audit-wave2-core-architecture.md`）**：`internal/httpapi/httpapi.go:253-254` `case errors.Is(err, gateway.ErrRateLimited): return http.StatusTooManyRequests, "rate_limited"`，经 `writeError`/`writeJSON`（`:227-228`）输出，**不设 `Retry-After`**（grep 全 httpapi 零命中）。对照 risk `internal/httpapi/auth.go:96` 限流 429 带 `Retry-After: 1`。注意本仓 429 是 `rate_limited`（网关 `ErrRateLimited` 哨兵），**不是** orchestration 的 `queue_full`。
- **③ 要求**：① 429 `rate_limited` 响应带 `Retry-After`（值与兄弟仓同口径，建议 `1`，或按网关限流窗口定）；② `docs/api-schema.md` 登记该码的 `Retry-After` 口径；③ 补一条用例断言 429 响应头含非空 `Retry-After`。
- **④ 约束与非目标**：不改 `ErrRateLimited` 的触发条件与网关限流逻辑；不改其余 11 个哨兵的 status/code 映射；不引入身份令牌桶（本仓 HTTP 层限流是另一回事，若要做另立单，不在本单）；不动写侧鉴权。
- **⑤ 验收**：用例断言 `rate_limited` 429 + `Retry-After` 非空；破坏验证 = 去掉头设置 ⇒ 用例红，还原复绿；`go build/vet/test ./... -count=1` 全绿。
- **⑥ 边界**：本仓 `internal/httpapi/httpapi.go` 的 429 写头 + `docs/api-schema.md` + 一条用例；orchestration 的限流与 queue_full Retry-After 归 `tellurion-orchestration:docs/TASKS.md` → `ORCH-RATELIMIT`。
- **owner**：待定

## 二、跨仓指针（不在本仓，勿双写）

| 事项 | 去哪 |
|---|---|
| core 侧通道核对与运行期复校验 | `tellurion:docs/TASKS.md` → `CORE-BROKER-RUNTIME-RECHECK` / `CORE-BROKER-STARTUP-PRECONDITION` |
| 报文契约形状 | `tellurion-contracts:docs/TASKS.md` |

## 维护本文件

- **新增单**：写全六字段；六字段里必须显式引用**冻结契约的路径与节号**（契约仓的包路径、设计文档的节号），不许写"参考相关实现"。
- **派工前先证伪前提**：读码核对工单写的前提；不符就停下回报 + 给拆法，不硬做、不扩范围。
- **跨仓引用写 `repo:path`**（如 `tellurion:docs/TASKS.md`），不做可点击相对链接。
- **顺序纪律**：同一波次内撞同一批文件的单按单号串行，一单一 commit。
