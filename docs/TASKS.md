---
status: active
last-verified: 2026-10-23
verified-by: X6a 落地 + 文档同步（QT-45 施工 ①）
---

# TASKS — tellurion-broker 未完成项

> 本仓**唯一**任务台账。跨仓任务只在 §二 留指针，**不镜像正文**（双写必然漂移）。
> 状态：`⬜ 待做` / `🔶 进行中` / `⛔ 阻塞（有未解除前置）` / `✅ 完成（附日期）`
> 每张单六字段（① 目标 ② 上下文 ③ 要求 ④ 约束与非目标 ⑤ 验收 ⑥ 边界），判据见 `tellurion:AGENTS.md` §8.5。
> 完成即标 ✅（附日期）并移入 `docs/archive/TASKS-YYYY-MM-DD-archived.md`；本文件只留未完成项。

---

## 一、本仓任务

> **约定变更（2026-10-21）**：原「占位仓不写正式六字段任务单」作废——X 线任务单已展开为本仓正式任务（见下）。M5/M8 开工前置不变；实现仍未开工，开工前仍须过前置两条。

### QT-45 · X6 交易通道网关（XTPTrader / MockTrader 两实现）[P2] 🔶

> **迁入说明（2026-10-21）**：本单自 core 仓 `tellurion:docs/TASKS.md`「五、X 线」迁入（任务台账批量重分布：X 线单展开到各服务仓为正式六字段单，「占位仓不写正式任务单」旧约定同时作废）。六字段正文逐字保留、不改写；core 侧同号条目已改为指针。
>
> **施工记录（2026-10-23，施工 ①）**：X6a 已落 `internal/gateway`——四个 port（`BrokerGateway` / `SettlementQuerier` / `KillSwitch` / `HealthChecker`）+ DTO + 12 哨兵 + 内存 `Mock`（`ClientOrderID` 幂等、写前日志、`unknown_state` 语义），接口契约与 `api-schema.md` 逐条对齐；现状矩阵与取舍见 `ARCHITECTURE.md` §9.8。**仍是 🔶**：X6b（对账口径 / 真实 broker 侧）、X6c（`XTPTrader`）、X6d（独立进程）未做；幂等映射只有内存实现，持久落点待裁（R2）；core 侧旧 `LiveTrader` / `Broker` 未退役（R4）。
>
> **M8 前置完成（2026-10-22）**：接口对表见 [`ARCHITECTURE.md`](ARCHITECTURE.md) §9。开工前先读 §9.1 的八条前置事实——今天不是「一个 port 两实现」而是**两个 port、三个实现**（F1）、没有上游幂等键（F2）、没有可区分的错误分类（F3）、XTP 是 offline skeleton 且 `EmergencyFlatten` 伪造成功（F4）、申报规则归属混用且 Mock 不校验（F5）、对账真实输入是 `QuerySettlement` 快照而非 `QueryFills` 且生产自检恒零差异（F6）、DTO 全在 core 且混入执行层语义（F7）、XTP 装配前置无检查（F8）。本单当时**仍 ⬜**（对表是开工前置，不是实现）；X6a 从「先定 `ClientOrderID` 幂等」开始，X6b 口径订正为快照，X6c 是从零实现而非接 SDK。
> 对表自检（2026-10-22，基线 core `dbe4c68`）：core `path:line` 引用逐条命中；4 个目标 port / 9 个方法 / 12 个错误码与 `api-schema.md` 双向一致；破坏验证三腿（改错行号 / 删一个方法 / 删一个错误码）各自红，还原后绿。

- **① 目标**：交易通道收敛为 port 两实现，归一化 DTO 进 contracts；通道级校验与风控校验分界清晰。
- **② 上下文**：`broker-gateway-design.md`（D23）；`xtp.go:387` 通道校验与风控校验混用需拆。
- **③ 要求**：① `BrokerPort` + 两实现；② 归一化 DTO 进 contracts；③ 拆开通道规则校验与 RiskPolicy 裁决；④ paper/实盘共用同一调用面。
- **④ 约束与非目标**：延迟敏感项保持进程内；不接新柜台；不改实盘下单语义。
- **⑤ 验收**：MockTrader 全链路测试；通道拒绝与风控拒绝的错误码可区分；DTO 有契约测试。
- **⑥ 边界**：只做交易通道；数据源 connector 在 QT-40。


---

> **⚠️ 开工前置（refactor-plan-review M5/M8，2026-10-20 登记）**：本表任何单开工前，必须先过 core `tellurion:docs/TASKS.md`「五、X 线」的**公共前置**两条：① 建仓/升格前先与 ADR-027 §2 对账（12 仓是上限/当前规划，不是承诺终态）；② 本仓设计文档先写「**接口对表**」小节（签名 + 错误语义 + 版本策略，模板见 `tellurion:docs/design/qt-decoupling/data-service-contract-and-ownership.md` §4.1），**未对表不开工**。正文以彼处为准，此处只留指针、不镜像。

## 施工序（候选单，开工前必须展开成六字段）

> 这张表是**施工顺序**，不是任务台账：每条在执行前要展开成六字段单（目标/上下文/要求/约束/验收/边界），并写明冻结契约的路径与节号。
> 顺序有依赖，**不要跳级**：跳序的代价是返工（例：X1a 的协议没冻结就写 aggregator，等于把协议写死在一个服务里）。
> 2026-10-21：候选单已展开为「一、本仓任务」的正式六字段条目（表中「上游单号」指向的 QT 单正文现在就在本仓）。

| 阶段 | 内容 | 上游单号 | 前置 |
|---|---|---|---|
| X6a | `BrokerGateway` port + `MockTrader`（`ClientOrderID` 幂等 / 超时语义跑通）🔶（施工 ① 已落 `internal/gateway`，未接 core） | `QT-45` | 本仓立项 + M8 对表 ✅ |
| X6b | 对账口径定稿（**账户快照** `QuerySettlement` + 真实 broker 侧 + 差异注入验收） | `QT-45` | X6a |
| X6c | `XTPTrader` 适配器（从零实现；装配前置：对账 + 幂等 + 熔断，缺一构造期拒绝） | `QT-45` | X6b |

## 二、跨仓指针（不在本仓，勿双写）

| 单号 | 主题 | 在哪 |
|---|---|---|
| QT-45 | X6 交易通道网关（XTPTrader / MockTrader 两实现） | 已迁入本仓「一、本仓任务」（2026-10-21；core `tellurion:docs/TASKS.md` 留同号指针） |
| QT-08 | 本仓 CI / AGENTS / 台账由该单一并补齐 | `tellurion:docs/TASKS.md` |

---

## 维护本文件

- **新增单**：写全六字段；六字段里必须显式引用**冻结契约的路径与节号**（契约仓的包路径、设计文档的节号），不许写"参考相关实现"。
- **派工前先证伪前提**：读码核对工单写的前提；不符就停下回报 + 给拆法，不硬做、不扩范围。
- **跨仓引用写 `repo:path`**（如 `tellurion:docs/TASKS.md`），不做可点击相对链接。
- **顺序纪律**：同一波次内撞同一批文件的单按单号串行，一单一 commit。
