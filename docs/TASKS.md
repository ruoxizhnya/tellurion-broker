---
status: active
last-verified: 2026-10-24
verified-by: R2 裁决 + 持久幂等映射落地 + R5 申报数量纯函数 + 文档同步（QT-45 施工 ④）
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
> **施工记录（2026-10-24，施工 ④）**：**R5 申报数量纯函数**落地，`go build/vet/test -count=1` 全绿（新增 `internal/gateway/lot`；用例覆盖 0 手 / 最小单位取整 / 超可用数量 / 涨跌停 / 越界价 / 非法入参）——
> ① `Size(Input)`：目标权重 × 资金或可用数量 ÷ 价格，**向下取整**到申报单位（`LotSize`，0 = 100）；输出股数 / 手数与拒单原因码（`zero_quantity` / `insufficient_capital` / `exceeds_available_quantity` / `price_above_limit_up` / `price_below_limit_down` / `invalid_input`）。**不缩量、不抬量**：超可用 / 超资金 / 不足一手一律拒单，把定仓错误暴露在调用方；被拒结果不带数量（防止"顺手"把拒单当订单发出去）。
> ② 涨跌停按**闭区间**：价格 ∈ [跌停, 涨停] 合法（涨停买入 / 跌停卖出是真实可申报委托，打板依赖它），越界才拒；单侧为 0 表示未提供、跳过该侧，两侧倒置判非法入参。
> ③ `ValidateQuantity`：显式数量的申报单位校验；Mock 的 `PlaceOrder` 从内联 `quantity % lotSize` 改为调它，`gateway.DefaultLotSize` 收敛为 `lot.DefaultLotSize`。
> ④ **破坏验证**：`floorToLot` 的 `math.Floor` 改 `math.Round` → `TestSizeBuyRoundsDownToLot` 红（300 股 / 3 手 vs 期望 200 / 2），逐字节还原后绿。
> **仍未做（如实登记）**：XTP 全量适配器接入这份规则（归 X6c）、core `risk.ValidateOrderQuantity` 退役与 alias（跨仓腿，本仓不改 core）——「两个实现共用」在本仓只落了 Mock 这一半。
>
> **施工记录（2026-10-23，施工 ③）**：**R2 裁决落账 + 持久幂等映射落地**，`go build/vet/test` 全绿（gateway / ackstore / reconcile；PG 集成腿 6/6，DSN 在案）——
> ① **R2 裁决**（[ADR 0002](adr/0002-幂等映射落-broker-写前日志.md)）：幂等映射落本仓 `broker.order_acks`（写前日志，**不是订单真相**）；柜台原生 `client-order-id` 只作第二道防线（重放不出当初 ack、比不了指纹，且仓内无 SDK 无法验证）；否掉 core order store（无表 / 无列 / U2 未裁，X6d 后也不成立）与本地文件 WAL。
> ② **端口形状修正**：旧 `AckStore` 的 `Load` + `Save` 两步没有原子性——实测并发同 ID 两边都读到未命中会各发一笔（**16/16 复现**）。改为 `Load` / `Reserve`（原子占位）/ `SaveAck` / `Durable`；PG 用 `INSERT ... ON CONFLICT DO NOTHING RETURNING` 判定发送权，内存实现用同一临界区。
> ③ **持久实现**：`internal/ackstore`（`ddl.go` + `pg.go`），`broker.order_acks`：主键 `(account_id, client_order_id)`、载荷指纹、`ack` 可为 NULL（占位未回执）、时间戳；`ackstore.Migrate` 启动时执行（幂等、无版本表）。新增 `pgx v5.7.3` 依赖（AGENTS §4 已登记）。
> ④ **装配闸收紧**：`NewXTPTrader` 现在要求 `Acks.Durable()==true`——`MemoryAckStore` 接真钱在构造期被拒，「无幂等键不接真钱」不再靠装配方记得。
> ⑤ **破坏验证**：拆开内存 `Reserve` 的临界区 → 并发腿 16 笔成交 / 32 winners（红）；逐字节还原 → 绿。PG 的 `ON CONFLICT DO NOTHING` 改成 `DO UPDATE` → 16 winners（红）；还原 → 6/6 绿。
> ⑥ **X6d 裁决：暂缓**（2026-10-23）——仓内文档明写它是触发式选项（多账户共用通道 / 通道级高可用），当前单账户单实例，触发条件未到；本轮不建 `cmd/brokergateway`，文档口径不变。
> **仍是 🔶**：X6c 全量适配器（下单 / 撤单 / 查单 / 回报 / 心跳 / 重连 / 限流）需要真实 XTP SDK 与柜台环境，仍从零未做；对账留痕进 risk（X3）未接；core 侧旧 `LiveTrader` / `Broker` 未退役（R4）；`broker.order_acks` 的保留期未定（当前不自动删）。R5 已落（施工 ④，本仓腿）。
>
> **施工记录（2026-10-23，施工 ②）**：X6b 的**口径定稿**与 X6c 的**装配闸**已落，`go build/vet/test` 全绿（38 测试）——
> ① **对账纯函数**（`internal/reconcile`，X6b）：输入两侧共用 `gateway.AccountSnapshot`（快照口径，F6；`QueryFills` 不参与对账判定），容差沿用 core 迁出时口径（现金 0.01 绝对 / 数量 0 / 市值 0.5% 相对且仅在数量相符时比 / 费用 0.01 / 快照时间漂移 60s），差异七类（现金 / 本地缺失 / 柜台缺失 / 数量 / 市值 / 费用 / 时间漂移）× 三级严重度，nil / 账户不符 / 重复标的 fail-loud，零差异返回空非 nil；**差异注入验收**（R3）=`TestReconcile_InjectedDriftFiresEachKind` 九条注入腿逐一命中，另有零差异 / 容差吞噪 / 纯函数确定性腿。**真实 broker 侧 `QuerySettlement` 仍 ❌**（归 X6c 全量）。
> ② **XTPTrader fail-closed 骨架 + 装配闸**（`internal/gateway/xtp.go`，X6c 前半）：`NewXTPTrader` 构造期强制注入 `AckStore` / `SettlementQuerier` / `HealthChecker`，缺一返回 `ErrAssemblyIncomplete`（含缺哪个依赖的说明），坏配置同哨兵——**「无幂等键不接真钱」从文字变成可机械判定的护栏**（`TestNewXTPTrader_GateRejectsIncompleteAssembly` / `_GateRejectsBadConfig`）；已装配后**所有**方法（含 `EmergencyFlatten`）恒 `ErrChannelDown`，零 fake success（F4 反例不再重演），不接 SDK、不接网络；`XTPConfig` 无任何密钥字段（R8）。`ErrAssemblyIncomplete` 是**构造路径哨兵，不是第 13 个 wire code**（`api-schema.md` §2 十二码不变）。
> **仍是 🔶**：X6c 全量适配器（下单 / 撤单 / 查单 / 回报 / 心跳 / 重连 / 限流，从零实现）未做；X6d（独立进程）施工 ③ 裁决暂缓；对账留痕进 risk（X3）未接；幂等映射持久落点（R2）施工 ③ 已裁为 `broker.order_acks`；core 侧旧 `LiveTrader` / `Broker` 未退役（R4）。
>
> **施工记录（2026-10-23，施工 ①）**：X6a 已落 `internal/gateway`——四个 port（`BrokerGateway` / `SettlementQuerier` / `KillSwitch` / `HealthChecker`）+ DTO + 12 哨兵 + 内存 `Mock`（`ClientOrderID` 幂等、写前日志、`unknown_state` 语义），接口契约与 `api-schema.md` 逐条对齐；现状矩阵与取舍见 `ARCHITECTURE.md` §9.8。
> 自审补正（2026-10-23，施工 ①）——**三处都会产生错误结论的缺陷**：
> ① **清仓单 ID 区分轮次**：原 ID 只由 `(symbol, qty)` 组成 → 持仓卖掉后**又建回来**时，flatten 会重放上一轮的"已成交"回执，把一笔**根本没下单**的持仓报成"清仓成功"（伪造成功）。改用**持仓代次**参与 ID（`flatten:<symbol>:<代次>:<qty>`），并把**重放来的单**判为 `Skipped`（它不是本次清仓的凭据）。
> ② **重放还原当初结论**：原先被柜台拒过的单，重放时返回 `ack` + `nil` error → 调用方会把"被拒"读成"受理成功"。改为重放仍返回同一个 `ErrOrderRejected` / `ErrInsufficientBuyingPower`（`api-schema.md` §2 要求拒单时 ack 与 error 同时非 nil）。
> ③ **判重域与本地索引同口径**：原先幂等映射按**请求的** `AccountID` 存、本地订单状态按**通道的**账户索引 → 单账户开发态下两者不一致，查单 / 撤单查不到自己下的那一单。统一为通道账户（配置了账户时二者本就相等）。
> 另加：四个 port 的**编译期接口断言**（方法集是冻结面，少一个方法就是实现方全红）。
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
| X6b | 对账口径定稿（**账户快照** `QuerySettlement` + 真实 broker 侧 + 差异注入验收）🔶（施工 ② 口径纯函数 + 差异注入验收已落 `internal/reconcile`；真实 broker 侧归 X6c 全量） | `QT-45` | X6a |
| X6c | `XTPTrader` 适配器（从零实现；装配前置：对账 + 幂等 + 熔断，缺一构造期拒绝）🔶（施工 ② 装配闸 + fail-closed 骨架；施工 ③ 持久幂等 `broker.order_acks` + `Durable` 闸；**SDK 从零实现未做**） | `QT-45` | X6b |
| X6d | 独立进程（回环监听 + 健康检查）——**2026-10-23 裁决：暂缓**（触发条件未到；见 ADR 0002 代价 4 与 `ARCHITECTURE.md` §8） | `QT-45` | 多账户共用通道 / 通道级高可用 |

## 二、跨仓指针（不在本仓，勿双写）

| 单号 | 主题 | 在哪 |
|---|---|---|
| QT-45 | X6 交易通道网关（XTPTrader / MockTrader 两实现） | 已迁入本仓「一、本仓任务」（2026-10-21；core `tellurion:docs/TASKS.md` 留同号指针） |
| QT-08 | 本仓 CI / AGENTS / 台账由该单一并补齐 | `tellurion:docs/TASKS.md` |
| R4（QT-45 子项） | core 旧 `LiveTrader` / `Broker` 退役（切读后停写） | `tellurion:docs/TASKS.md`；**本仓只登记跨仓腿，不改 core**（派工约束） |

---

## 维护本文件

- **新增单**：写全六字段；六字段里必须显式引用**冻结契约的路径与节号**（契约仓的包路径、设计文档的节号），不许写"参考相关实现"。
- **派工前先证伪前提**：读码核对工单写的前提；不符就停下回报 + 给拆法，不硬做、不扩范围。
- **跨仓引用写 `repo:path`**（如 `tellurion:docs/TASKS.md`），不做可点击相对链接。
- **顺序纪律**：同一波次内撞同一批文件的单按单号串行，一单一 commit。
