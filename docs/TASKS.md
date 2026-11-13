---
status: active
last-verified: 2026-11-13
verified-by: 2026-11-13 `BROKER-LIVE-LOOPBACK-GUARD` 按主任裁决结案（A 维持 Warn + 成文；实现落点订正到 core `pkg/brokerhttp/trader.go`，本仓无等价代码 ⇒ 活跃层改指针、原文与订正移入归档；ODR-074 D5）；2026-11-13 `BROKER-CI-GATES` 施工收口（CI 起 `postgres:16` + 迁移步 + 覆盖率门 73，实测 total 78.0%；见 `completed-2026-11-13`）；此前：2026-11-13 五角审查立单（只立单不施工）——档 3（实现）新立 `BROKER-LIVE-LOOPBACK-GUARD` ⏸（待主任裁决 fail-closed / 维持 Warn）；档 4（测试）新立 `BROKER-CI-GATES` ⬜；此前：2026-11-12 `X-CI-RACE-STATICCHECK` 补 CI 的 `-race` 与 `staticcheck` 门（staticcheck 本地 EXIT=0；`-race` 因本机无 cgo 未跑，交由 CI ubuntu 覆盖，已登记）；2026-11-12 `X-DEP-BUMP-CONSUMERS` 回补 `govulncheck` CI 门（BROKER-DEP-BUMP 首轮只跑本地验收、未进门；本次补 CI 步，降 pgx 复红/还原复绿）；2026-11-12 全面审查 W7 三单收口（`BROKER-DEP-BUMP`：pgx v5.11.0 + x/text v0.44.0 + `go` directive 1.26.11，`govulncheck` EXIT=0，破坏验证降版复红/还原复绿，ackstore PG 集成腿因本机无 `BROKER_TEST_DSN` 未跑并登记重开条件；`BROKER-AUTH-ADR`：新增 ADR-0004 固化鉴权姿态与 kit JWT 分叉例外 + runbook §2.1 轮换 + api-schema 指回，未改代码；`BROKER-RETRY-AFTER`：429 `rate_limited` 补 `Retry-After`（值来自 `rateLimitedRetryAfter` 常量）+ 2 条用例 + 破坏验证，全量 build/vet/test 绿）；2026-11-11 全面审查 W7 台账立单（A5 授权）——新立 3 条六字段单，证据坐标引 ODR-072 / ODR-069（`tellurion-docs:docs/archive/odr/`）；此前 QT-45 / X6c 终态裁决（2026-11-10：主任授权执行侧裁定 ⇒ `QT-45` 收口 ✅（Mock 全绿 + fail-closed 骨架 + 装配闸为终态）、`X6c` 转 ⏸ 挂起（外部依赖 P1~P5，重开条件与流程见 ADR-0003）；B 组债务结论与 C 组验收清单保留）；QT-45 收口复核（2026-11-10：⑤ 三条判据逐条点名用例全绿，③ ① 「两实现」因 XTP 仍是 fail-closed 骨架未完成 ⇒ 🔶 → ⛔，唯一硬前置 = X6c P1~P5；同轮 core R4 退役已确认完成）；SVC-BROKER-G3 前置复核（2026-11-10：QT-51 已合入 `a2e388a`、`main:pkg/backtest/execution/port.go` EXIT=0，叠加 G1 HTTP 面已落 ⇒ 两道前置全部解除，状态 ⛔ → ⬜；施工四步逐条列明，未做一步不勾；X6c 仍 ⛔）；G4 ✅（失败语义六类 / 就绪口径 / runbook / 内存演练 + 真 PG 停库→恢复演练与幂等映射核对，2026-11-07 晚补齐）+ G1 ✅（订单/查询/对账 HTTP 面 + 12 码映射 + 键集用例 + 真实二进制 smoke）+ G2 ✅（PG 集成腿真跑 9 条全 PASS）+ R10 施工；此前 G7（施工 ⑤）X6c ⛔ 判定、R2 裁决、R5 申报数量纯函数
completed-2026-11-13: `BROKER-CI-GATES` ✅（提交见 `git log --grep BROKER-CI-GATES`；实测库侧 9 条 SKIP→PASS、覆盖率 78.0% → 门 73%），原文与施工记录在 `docs/archive/tasks-archive-2026-11.md`
---

# TASKS — tellurion-broker 未完成项

> **归档说明（2026-11-11）**：本文件已瘦身为「只装未做的事」。2026-11-10 及以前已收口（✅）的任务原文、施工记录、验收输出与破坏验证，已逐字移入 [`docs/archive/tasks-archive-2026-11.md`](archive/tasks-archive-2026-11.md)；该文件顶部有「检索索引」表（ID / 标题 / 收口日期 / commit / 证据链接）。检索：`grep -n "<任务ID或commit>" docs/archive/tasks-archive-2026-11.md`。
>
> 状态：`⬜ 待做` / `🔶 进行中` / `⛔ 阻塞（有未解除前置）` / `⏸ 挂起（外部依赖或已裁决暂不实施；**不计入开放项**，重开须另立单并满足该条「重开条件」）` / `✅ 完成（附日期，随即移入归档）`

## 一、未做 / 挂起 / 外部依赖

> 本节 = 本轮（2026-11-11）台账重整后剩下的挂起项（`X6c` 及其派生项）+ 全面审查 W7 新立 3 单（`BROKER-DEP-BUMP` / `BROKER-AUTH-ADR` / `BROKER-RETRY-AFTER`）。此前已收口单的正文与施工记录全部在归档文件里。
>
> **2026-11-12 进展**：全面审查 W7 三单**全部收口** ✅ —— `BROKER-DEP-BUMP`（pgx v5.11.0 + x/text v0.44.0 + `go` directive 1.26.11，`govulncheck` EXIT=0；ackstore PG 集成腿当日补跑 **12/12 PASS、0 SKIP**，临时库验毕即删；工具链升级已由主任确认保留并登记在 `AGENTS.md` §2 与 `runbook.md` §11）、`BROKER-AUTH-ADR`（新增 ADR-0004 + runbook §2.1 轮换 + api-schema 指回，未改代码行为）、`BROKER-RETRY-AFTER`（429 `rate_limited` 补 `Retry-After` + 两条用例 + 破坏验证）。原文与落账见 [`archive/tasks-archive-2026-11.md`](archive/tasks-archive-2026-11.md#BROKER-DEP-BUMP)。本节回到「只剩 `X6c` 派生项」。

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

### BROKER-CI-GATES · CI 起 PG 并设覆盖率门（1 条 DB 用例不再 skip）[P2] ✅ 2026-11-13

> 已收口：原文与施工记录在归档「2026-11-13 收口」节；实测库侧 9 条 SKIP→PASS（旧口径「1 条」）、覆盖率 78.0% → 门 73%。

## 二、跨仓指针（不在本仓，勿双写）

| 事项 | 去哪 |
|---|---|
| core 侧通道核对与运行期复校验 | `tellurion:docs/TASKS.md` → `CORE-BROKER-RUNTIME-RECHECK` / `CORE-BROKER-STARTUP-PRECONDITION` |
| 报文契约形状 | `tellurion-contracts:docs/TASKS.md` |
| `live` + 回环地址的告警级别（`BROKER-LIVE-LOOPBACK-GUARD`，2026-11-13 裁决 A：维持 Warn + 成文） | **实现不在本仓**：`tellurion:pkg/brokerhttp/trader.go:78-99`（`resolveChannel` live 分支 + `isLoopbackHost`）。本仓无等价代码，仅有 `cmd/brokergateway/main.go:98-101` 关于 `/version.channel` 的注释；成文与 core 台账归 `tellurion`。原文、位置订正与裁决见 [`archive/tasks-archive-2026-11.md`](archive/tasks-archive-2026-11.md#BROKER-LIVE-LOOPBACK-GUARD) |

## 维护本文件

- **新增单**：写全六字段；六字段里必须显式引用**冻结契约的路径与节号**（契约仓的包路径、设计文档的节号），不许写"参考相关实现"。
- **派工前先证伪前提**：读码核对工单写的前提；不符就停下回报 + 给拆法，不硬做、不扩范围。
- **跨仓引用写 `repo:path`**（如 `tellurion:docs/TASKS.md`），不做可点击相对链接。
- **顺序纪律**：同一波次内撞同一批文件的单按单号串行，一单一 commit。
