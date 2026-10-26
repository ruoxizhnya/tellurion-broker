---
status: active
last-verified: 2026-10-25
verified-by: G7（施工 ⑤）——X6c 开工条件判定为 ⛔ 阻塞 + 撤单判据自审补正；此前 R2 裁决 + 持久幂等映射落地 + R5 申报数量纯函数（施工 ④）+ 文档同步
---

# TASKS — tellurion-broker 未完成项

> 本仓**唯一**任务台账。跨仓任务只在 §二 留指针，**不镜像正文**（双写必然漂移）。
> 状态：`⬜ 待做` / `🔶 进行中` / `⛔ 阻塞（有未解除前置）` / `✅ 完成（附日期）`
> 每张单六字段（① 目标 ② 上下文 ③ 要求 ④ 约束与非目标 ⑤ 验收 ⑥ 边界），判据见 `tellurion:AGENTS.md` §8.5。
> 完成即标 ✅（附日期）并移入 `docs/archive/TASKS-YYYY-MM-DD-archived.md`；本文件只留未完成项。

> **第三轮裁决指针（2026-10-25）**：⑨ `broker.order_acks` 保留期 = **90 天**，只删 `ack IS NOT NULL`，先加 `(updated_at) WHERE ack IS NOT NULL` 部分索引，单批 ≤ 5000 行；90 天登记为暂定假设（柜台可查窗口未知）。㉒ **R10 现在修**（`internal/gateway/mock.go:358/415` 的映射读失败从 `ErrChannelDown` 改为 `ErrIdempotencyStoreUnavailable`，两处同口径 + 各一条用例）；**R9 暂缓到 X6c**，三条差异写进 `docs/x6c-xtp-readiness.md` §8。⑦ XTP 维持 ⛔。正文见 `tellurion:docs/design/qt-decoupling/cross-repo-decision-record-2026-10-25.md` §五。

---

## 一、本仓任务

> **约定变更（2026-10-21）**：原「占位仓不写正式六字段任务单」作废——X 线任务单已展开为本仓正式任务（见下）。M5/M8 开工前置不变；实现仍未开工，开工前仍须过前置两条。

### SVC-BROKER-G0 · 服务就绪门 G0：Mock 服务入口与自有部署（ADR-036）[P0] ✅（2026-10-26）

- **① 目标**：本仓作为交易网关**服务**能在本地独立启动；先暴露健康/版本面，订单 HTTP 面归 G1。
- **② 上下文**：ADR-036 定本仓为服务并**取代 X6d「独立进程暂缓」**；今天只有 `internal/gateway` 的 port + Mock，无 `cmd/`、无 `Dockerfile` / `deploy/`。
- **③ 要求**：新增 `cmd/brokergateway`（`/healthz`、`/version`；装配现有 `Mock`，`AckStore` 可 Memory 或 PG）；新增 `Dockerfile`（多阶段、非 root、`curl`、`HEALTHCHECK /healthz`）；新增 `deploy/compose.yaml`（`BROKER_DATABASE_DSN` 可选；端口 `:8091`）；README 写单仓启动命令。
- **④ 约束与非目标**：不接真实 XTP（X6c 仍 ⛔）；不做风控；不伪造订单成功；不在 G0 暴露下单 API（G1）。
- **⑤ 验收**：`docker compose -f deploy/compose.yaml config` 通过；单仓启动后 `GET /healthz` 200；`/version` 返回构建信息；无 `replace` / `go.work`。
- **⑥ 边界**：只改本仓 deployment 与最小服务入口；core 消费由 `SVC-BROKER-G3` 跟踪。

> **施工记录（2026-10-26）**：
> - **落点**：`cmd/brokergateway/main.go`（默认 `127.0.0.1:8091`、`/healthz`、`/version`，Mock + 内存 `AckStore`；路由用 `healthzPath` / `versionPath` 常量，便于文档 grep 锚定）、`Dockerfile`（多阶段、非 root、`curl`、`HEALTHCHECK curl -fsS http://127.0.0.1:8091/healthz`）、`deploy/compose.yaml`（服务名 `broker`、`:8091`）；README 补 `cmd/` 与直跑 / compose 命令。
> - **验收（真实输出）**：`docker compose -f deploy/compose.yaml config -q` → exit 0；`git ls-files cmd/` → 含 `cmd/brokergateway/main.go`；`rg -n '"/version"' cmd/brokergateway/main.go` → 1 行（`:46`）；`go build ./...` / `go vet ./...` / `go test ./... -count=1` 全绿（4 个测试包）；`gofmt -l .` 空。
> - **破坏验证**：`git rm --cached -r cmd` 后 `git ls-files cmd/` = 0 行（红）→ `git reset -- cmd` 逐字节还原 → 1 行（绿），工作区零改动。
> - **如实登记**：未引 SDK / C 工具链；未改 `pkg/live`（core 侧）；未自造 order intent DTO；compose 未注入 `BROKER_DATABASE_DSN`（G0 用内存 AckStore，接 PG 归 G1）；X6c 仍 ⛔ 永久。

### SVC-BROKER-G1 · 订单 HTTP 契约（ADR-036 G1）[P0] ⬜（2026-10-26 死锁裁定：**前置已解除，可立即开工**——不再等 contracts DTO）

- **① 目标**：把现有 Go port 的订单/查询/对账能力暴露成版本化 HTTP 面，供 core 远程调用。
- **② 上下文**：`docs/api-schema.md` 明写今天是「Go port（进程内），不是 HTTP」；ADR-036 把本仓定为服务，G1 必须补 HTTP 形状。
- **③ 要求**：定义 `/api/broker/orders`（Place/Cancel/Query）、`/accounts/{id}/positions|account|fills`、`/settlement`、`/emergency-flatten`；DTO 与 12 哨兵映射错误码；写侧 service token；幂等键仍 `(AccountID, ClientOrderID)`；更新 `api-schema.md`（从 port 改为 port + HTTP）。
- **④ 约束与非目标**：不改 Mock 语义；不接真钱；不做本地订单真相；不把风控塞进网关。
- **⑤ 验收**：core 能通过 HTTP 完成下单/查单/撤单/持仓/对账；12 错误码逐条可复现；`api-schema.md` 与实现一致。
- **⑥ 边界**：本仓只保证网关面；core 侧调用与旧实现退役由 `SVC-BROKER-G3` / `SVC-CORE-BROKER-RETIRE` 跟踪。

> **⛔ → ⬜ 开工前置裁定（2026-10-26，执行侧代裁，待主任追认）**：本单原记的 ⛔ 理由是「等 A 组 broker DTO（contracts 侧）」，
> 而 contracts 那份材料（`tellurion-contracts:docs/design/shape-packages-decision-material.md` §0 行 ③ 与 §3.1）写的正是
> 「③ 的前置 = `SVC-BROKER-G1` 先把订单 HTTP 面定形」——**两条并读就是互为前置的死锁**，谁都不许开工且不会自动解开。
> 裁定 **G1 先行**（判据、三条理由与代价见那份材料新增的 §3.1.1）：订单 DTO 的形状 owner 在 HTTP 面落地前**留在本仓**
> （`internal/gateway/model.go:80-186`，今天已经在那儿），contracts 的 `CT-05` 是**事后收编**，不是先立法。
> ⇒ 本单今天可开工的判据：`go.mod` 无需先引 contracts（kit 仍需在写侧 service token 时引入）；**不许**再拿「等 A 组」当不动手理由。
> **窗口期两条义务（缺一即 G1 不算落定，`CT-05` 不许开工）**：① `docs/api-schema.md` 写出订单 / 成交 / 账户三族的
> **逐字段报文表**（字段名 + 可空性 + 类型），并有 httpapi 用例断言序列化后的键集合；② 收编时**删本地形状改引契约** +
> 同一条报文快照**改前 / 改后逐字节一致**，不允许两份并存。
> 本轮仍未做：没自造 order intent DTO（本轮只裁顺序，不动代码）；`go.mod` 无 contracts / kit。

### SVC-BROKER-G3 · core 切读与旧 LiveTrader 退役（ADR-036 G3）[P0] ⛔（2026-10-26 复核：无消费面）

- **① 目标**：core 的交易通道调用走本仓；core `pkg/live` / `pkg/live/broker` 旧实现退役（R4 关闭）。
- **② 上下文**：core 仍跑 `MockTrader` / `LiveTrader`；本仓 X6c 全量适配器仍被 SDK 阻塞，但 Mock 路径可先切。
- **③ 要求**：core 通过 G1 HTTP 面调用本仓 Mock；按「第二实现 → 对拍 → 切读 → 停写」推进；切读后删 core `pkg/live` / `pkg/live/broker` 的通道适配；保留运行域（订单/持仓/结算）在 core；真实 XTP 另立 ADR。
- **④ 约束与非目标**：不在 X6c 前接真钱；不把订单真相搬进本仓；不改 T+1/撮合语义。
- **⑤ 验收**：core 侧旧通道适配零调用；同批回放的订单/成交/持仓与旧实现一致；`R4` 关闭；X6c 仍如实标 ⛔。
- **⑥ 边界**：本仓只保证网关；core 代码删除由 `SVC-CORE-BROKER-RETIRE` 跟踪。

> **⛔ 开工前置复核（2026-10-26，未开工）**：等 C 组 QT-51 合入（core 无执行 port 则无消费面）+ B 组 `SVC-CORE-BROKER-RETIRE` 立单；本仓 G1 HTTP 面未落，G3 无第二条实现可对拍。X6c 维持 ⛔ 永久（全仓无 SDK / C 工具链）。
>
> ⚠️ **同日实测订正两处**：① `SVC-CORE-BROKER-RETIRE` **已立单**——`tellurion:docs/TASKS.md:157`「六之二」，串行第 9（判据 = B 组材料 §4.6），不再是本单的前置；② G1 的死锁已裁（见上方 `SVC-BROKER-G1` 的裁定块：G1 先行、DTO 暂留本仓）。⇒ 本单真正的剩余前置只剩两条：**QT-51 合入**（2026-10-26 复测：`pkg/backtest/execution/port.go` 仍只在未合入分支 `task/qt51-execution-port`，main 上 `git cat-file -e main:pkg/backtest/execution/port.go` 失败 ⇒ 这条 ⛔ 属实）**+ G1 HTTP 面落地**。

### QT-45 · X6 交易通道网关（XTPTrader / MockTrader 两实现）[P2] 🔶

> **迁入说明（2026-10-21）**：本单自 core 仓 `tellurion:docs/TASKS.md`「五、X 线」迁入（任务台账批量重分布：X 线单展开到各服务仓为正式六字段单，「占位仓不写正式任务单」旧约定同时作废）。六字段正文逐字保留、不改写；core 侧同号条目已改为指针。
>
> **施工记录（2026-10-25，施工 ⑤ = 派工 G7）**：**本组不具备开工条件，交付的是决策材料**——
> 新建 [`x6c-xtp-readiness.md`](x6c-xtp-readiness.md)（四处取证 / 九项不可猜的柜台事实 / 剩余范围哪一半与 SDK 无关 /
> P1~P5 开工前置 / 三个选项与推荐 / 可判定的触发条件）。核对结论：**X6c 判 ⛔ 阻塞，不是 🔶 待做**。
> ① **前提核实（四处实测，都不在仓内）**：本仓 `find` 只有 `internal/gateway/xtp.go` + 其测试，无头文件 / 无 `.so`/`.dll`；
> core 侧 `git grep -l 'import "C"'` 零命中、注释里承诺的 `internal/cgo/xtp/` 目录不存在；本机 `go env CGO_ENABLED` = `0`
> 且 PATH 里没有 gcc（`go env CC` 写的是 `gcc`，但命令找不到）——`import "C"` 的包今天在这台机器上编不起来；
> CI 只 checkout 本仓、`ubuntu-latest`、四步 gofmt/build/vet/test，没有 C 工具链也没有柜台网络。
> ② **本波唯一一条代码改动 = 一条与 SDK 无关的真缺口**（自审发现，见 §7 的完整取证）：`Mock.CancelOrder` 在本地无订单状态时
> 直接报 `order_not_found`，而该 code 的判据是「网关**能证明**从未发送过该 ID」（`api-schema.md` §2 / `dataflow.md` §4-1）——
> 幂等映射里有占位（另一进程发的 / 本进程重启后本地状态丢了）时它证明不了，误报会让调用方换新 `ClientOrderID` 重发 ⇒ 柜台两笔。
> 改为先读映射（同一判重域）→ 有记录 = `unknown_state`，映射也没有才 `order_not_found`（哨兵取自 §9.4 已冻结的 `CancelOrder`
> 错误集，**不新增契约面**）。
> ③ **测试先行 + 破坏验证（真实退出码，未截断）**：新腿 `TestCancelOrderDoesNotClaimNeverSentForAReservedOrder`
> 先对**未修**代码红（`TEST_EXIT=1`：`err = broker: order not found: c-9, want ErrUnknownState`）；修后
> `BUILD_EXIT=0`、`VET_EXIT=0`、`gofmt -l .` 空输出、`TEST_EXIT=0`，**50 passed / 0 failed / 6 skipped**
> （六条是 PG 集成腿：本机 `127.0.0.1:5432` 无监听、Docker 引擎未起 ⇒ 无 DSN 按设计 skip；带 DSN 强跑 6 条 fail-loud）。
> 破坏腿把新分支哨兵换成**同样合法的** `ErrOrderNotFound` → `TEST_EXIT=1` 且红在该断言；逐字节还原 `sha256sum -c` → `OK`，
> 重跑绿。**无 race**（本机 `CGO_ENABLED=0`，`-race` 跑不了，如实登记）。
> 两笔提交落地后在**干净 checkout**（`git worktree add --detach` + 用完 `remove --force`）复取一遍：`gofmt -l .` 空输出、
> build / vet / test 全 `EXIT=0`，同样 **50 / 0 / 6**，新腿在列——工作树的绿不等于推得出去的绿。
> ④ **两条新待裁**（登记进 `ARCHITECTURE.md` §9.7，本轮不擅自改行为）：**R9** Mock 与真实柜台的三处行为差异
> （T+1 不进 Mock `mock.go:295`；卖出超可用量放行 `mock.go:300-305`，`PlaceOrder` 只校申报单位 `:663`；费用恒零）——
> 收紧属契约行为变更，要动 `api-schema.md` §2 并可能一条 ADR；**R10** 映射读失败被报成 `channel_down`
> （`QueryOrder` `mock.go:415`、本轮 `CancelOrder` `:358` 同口径），准确哨兵应是 `idempotency_store_unavailable`，
> 属诊断质量问题（现方向处置更保守，不会造成发送）。
> ⑤ **文档同步**：`api-schema.md` §3（含本文头部 last-verified）、`ARCHITECTURE.md`（§1 定位 / §9.4 `CancelOrder`
> 行补这条腿 / §9.7 R6 指向本文 + 新增 R9+R10 / §9.8 矩阵与自检结论 / frontmatter）、
> `docs/README.md` §1/§2 导航、`AGENTS.md` §1 状态。
> **未落项（如实，不写成已完成）**：X6c 全量适配器仍 ⛔（P1~P5 任一不满足即不动代码，触发条件见该文 §6）；
> R9 / R10 待裁；`broker.order_acks` 保留期未定（它要的是「柜台可查窗口」这个**事实**，不是代码）；
> 对账留痕进 risk（X3）未接；core 旧 `LiveTrader` / `Broker` 退役（R4，跨仓腿）；**PG 集成腿本轮未跑**（服务未起）。
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
> ⑥ ~~**X6d 裁决：暂缓**（2026-10-23）~~ → **已被 ADR-036（2026-10-26）取代**：本仓定位为服务，先落 `cmd/brokergateway` 的 Mock 健康/版本面（`SVC-BROKER-G0`），订单 HTTP 面归 `SVC-BROKER-G1`；真实 XTP 仍受 X6c 阻塞，真钱形态另立 ADR。
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

### SVC-BROKER-G2 · 服务就绪门 G2：`broker.order_acks` 唯一 writer · 保留期与部分索引（裁决 ⑨）[P1] ⬜

- **① 目标**：`broker` schema 满足 ADR-036 §3 的 G2（自有 schema、迁移可重放、无跨 schema 写），并把第三轮裁决 **⑨** 已拍定的保留期与索引**落进 DDL 与作业**——这条裁决今天**没有任何条目承载**。
- **② 上下文**：裁决 ⑨ 原文（本文件 `:14`）：「`broker.order_acks` 保留期 = **90 天**，只删 `ack IS NOT NULL`，先加 `(updated_at) WHERE ack IS NOT NULL` 部分索引，单批 ≤ 5000 行；90 天登记为**暂定假设**（柜台可查窗口未知）」；实测 `internal/ackstore/ddl.go` 的 `migrationStatements` 今天只有三条：`CREATE SCHEMA`、`CREATE TABLE broker.order_acks`（主键 `(account_id, client_order_id)`）、无其它索引 ⇒ **裁决的「先加部分索引」未落**；全仓生产代码无保留期删除语句（`DELETE FROM broker.order_acks` 只在 `internal/ackstore/pg_test.go:48` 的清理里出现）；台账 `:101` 与 `:109` 仍写「保留期未定（它要的是柜台可查窗口这个**事实**）」——这句与裁决 ⑨ 已被拍定为「90 天暂定」相冲突，属**账实不符**，本单第 ① 件事就是把它对齐。
- **③ 要求**：① 按裁决加部分索引 `CREATE INDEX IF NOT EXISTS … ON broker.order_acks (updated_at) WHERE ack IS NOT NULL`（加进 `migrationStatements()` 这条唯一路径，幂等）；② 新增保留期清理：只删 `ack IS NOT NULL` 且 `updated_at < now() - interval '90 days'` 的行，**单批 ≤ 5000**（裁决原值），批间可让出锁；③ 把「90 天是暂定假设」写进 `docs/db-schema.md` 与 `AGENTS.md` §1 状态行，并登记**触发复核的条件**（柜台可查窗口一旦已知即重算）；④ 迁移重放自证：同一测试库连跑两次 `Migrations()`，行数 / `pg_indexes` / 列集指纹逐位不变（`BROKER_TEST_DSN` 腿，无库 skip）；⑤ 禁跨 schema 写护栏：本仓 SQL 只允许 `broker.` 的写语句；⑥ 明确 fail-closed 边界：**未 ack 的行（`ack IS NULL`）永不被清理**——那是「可能已发出而不知结果」的唯一证据，删它等于制造重复下单（与 `api-schema.md` §2 的 `unknown_state` 语义同一口径）。
- **④ 约束与非目标**：不改 `Reserve` / `Load` / `SaveAck` / `Durable` 四方法语义（ADR 0002 的幂等映射契约）；不动主键形状；不在真钱路径上跑清理（X6c ⛔）；不引调度框架（清理可以是进程内周期任务或运维脚本，但要写明选哪个及为什么）；不 `DROP` 表。
- **⑤ 验收**：① `pg_indexes` 里出现该部分索引，且重放第二次执行后索引集与第一次逐位相同；② 清理用例三条：`ack IS NOT NULL` 且超 90 天 ⇒ 删；`ack IS NULL` 且超 90 天 ⇒ **不删**（这条是护栏，破坏验证：去掉 `ack IS NOT NULL` 谓词 ⇒ 红）；单批 > 5000 行时只删 5000 且不返回错误（批处理可续）；③ 破坏验证：往本仓任一 SQL 常量加 `UPDATE quant.orders` ⇒ 跨 schema 护栏红、还原复绿；④ `docs/db-schema.md` 的保留期一行与裁决一致并标注「暂定 + 复核条件」；⑤ 三条命令单仓绿。
- **⑥ 边界**：本仓 schema 生命周期；订单 HTTP 面归 `SVC-BROKER-G1`（⛔），DTO 进契约归 `tellurion-contracts:CT-05`（⛔，与本单互为死锁的裁点已在 CT-05 写明）。

### R10 · 幂等映射读失败的哨兵从 `ErrChannelDown` 改为 `ErrIdempotencyStoreUnavailable`（㉒ 已裁「现在修」）[P1] ⬜

- **① 目标**：兑现第三轮裁决 ㉒ 的另一半——「**R10 现在修**：`internal/gateway/mock.go:358/415` 的映射读失败从 `ErrChannelDown` 改为 `ErrIdempotencyStoreUnavailable`，两处同口径 + 各一条用例」；这条裁决今天**没有条目承载**，且台账 `:94-101` 仍把它写成「待裁」（账实不符，本单落完同批订正）。
- **② 上下文**：实测两处仍在——`internal/gateway/mock.go:358`（`CancelOrder` 路径里 `m.store.Load` 失败 → `fmt.Errorf("%w: %v", ErrChannelDown, err)`）与 `:415`（`QueryOrder` 同形）；正确哨兵 `ErrIdempotencyStoreUnavailable` 已存在（`internal/gateway/errors.go:36`）且在**发送前**的三处用对了（`mock.go:153/159/169`）⇒ 只有「读映射失败」这一类被错报成「通道断了」。为什么必须改：调用方看到 `channel_down` 会按「柜台侧不可达」处置（重试/等恢复），而真实情况是**本仓自己的幂等存储读不出来**，这一单可能已在柜台上；把「我存储坏了」报成「线路断了」是把诊断责任推给上游。
- **③ 要求**：① 两处同批改哨兵，报文保留底层 `err` 上下文（`%w: %v`）；② 各一条用例：注入一个 `Load` 返回错误的 store ⇒ 断言错误链里是 `ErrIdempotencyStoreUnavailable` 且**不是** `ErrChannelDown`；③ `api-schema.md` §2/§3 的错误语义表同步（这一条改变对外报文，属文档触发器）；④ 同批订正 `docs/TASKS.md` 头部与 `:94-101` 里「R10 待裁」的陈述（改为已裁已落，R9 维持「暂缓到 X6c」，㉒ 原文如此）。
- **④ 约束与非目标**：**不改** `found == true` 时的 `ErrUnknownState` 分支（那是正确行为，裁决没动它）；不改 R9 的三条 Mock/柜台行为差异（㉒ 明裁暂缓）；不动 `Disconnect()` 与真实断连路径的 `ErrChannelDown`（那些确实是通道断）；不改 DDL。
- **⑤ 验收**：① 两条新用例先红（实现未改时断言拿不到 `ErrIdempotencyStoreUnavailable`）后绿；② **破坏验证**：把其中一处改回 `ErrChannelDown` ⇒ 对应用例红、还原复绿；③ `git grep -n 'ErrChannelDown' -- internal/gateway/mock.go` 的结果里 `:358`/`:415` 两条**不再存在**，其余（真断连路径）保持；④ `api-schema.md` 错误表与本仓实现逐字一致；⑤ 三条命令绿（`BROKER_TEST_DSN` 有库时集成腿一并跑，无库时如实记 skip）。
- **⑥ 边界**：只改这一类错误归属；订单 HTTP 面（`SVC-BROKER-G1`）与 X6c 不在本单。

### SVC-BROKER-G4 · 服务就绪门 G4：网关失败语义 · 装配闸可观测 · runbook（ADR-036）[P1] ⬜

- **① 目标**：把「通道什么时候算不可用、不可用时对外说什么、谁来判断」写成文档并演练一次——本仓是全系统唯一**直接面对钱**的边界，失败语义含糊的代价与其它五仓不同量级。
- **② 上下文**：`cmd/brokergateway` 已有 `/healthz` + `/version`（G0 ✅，`:8091`）；`XTPTrader` 是 fail-closed 骨架（构造期缺 `AckStore` / `SettlementQuerier` / `HealthChecker` 任一、或 `Acks.Durable()==false` 即拒绝；装配后所有方法恒 `channel_down`，**零 fake success**）——这个姿势是对的，本单不改行为、只把它变成可核对的文档与演练；**`/readyz` 全 workspace 0 命中**（gateway 没有监听业务端口，「就绪」到底指什么必须先回答）；PG 只用于幂等映射，映射不可用与柜台不可用**必须是两个码**（R10 就是这条的具体化）；无 runbook（全 workspace `runbook` 只在 ADR-036 正文）；X6c 的四份 `docs/x6c-xtp-readiness.md` 有 P1~P5 前置，但「运维怎么判断现在能不能开」没有一页纸。
- **③ 要求**：① 失败语义表：`channel_down` / `unknown_state` / `idempotency_store_unavailable` / `order_not_found` / 幂等冲突 / 申报数量不合规（`internal/gateway/lot`）六类，各写「网关侧发生了什么、调用方**能做什么**（重试？换新 ID？必须查单？）」。其中「`unknown_state` 时严禁换 `client_order_id` 重发」是本仓最要命的一条，必须单独成行并有反证；② 就绪定义成文：gateway 无监听 ⇒ 显式声明「`/healthz` 即唯一探针，就绪 = 装配闸通过」，或引入 `/readyz` 检查 PG 池 + `Acks.Durable()`——**二选一在单内定死**，不许留给读者猜；③ 熔断（`KillSwitch`）触发与解除的运维姿势与留痕；④ `docs/runbook.md`：起停、看装配闸为什么拒、幂等映射怎么查（含 `BROKER_TEST_DSN` 集成腿姿势）、`unknown_state` 的排查顺序、与 core 对账（`internal/reconcile` 快照口径）怎么跑；⑤ 一次演练：关掉 PG ⇒ 断言网关**不 fake success** 且错误是 `idempotency_store_unavailable`（R10 落完后），恢复后可继续；⑥ 把「未接真钱」写成本仓的永久性 G4 前置（X6c ⛔），并说明**开真钱之前 G1/G2/G3/G4 必须全绿**这条闸门。
- **④ 约束与非目标**：不改 fail-closed 骨架的行为（它是设计，不是缺陷）；不在本单落订单 HTTP 面（`SVC-BROKER-G1` ⛔）；不接真实 XTP（X6c ⛔，四处无 SDK）；不引 APM；不做多副本。
- **⑤ 验收**：① 失败语义表六类齐全，且每类有「调用方动作」一列；② 反证用例：`unknown_state` 的报文里必须出现「不得重发/先查单」的语义（用例断言错误文本或 code），破坏验证：删掉该提示 ⇒ 红；③ 就绪口径在 `api-schema.md` 与 `runbook.md` 两处一致；④ 演练有真退出码（关 PG 前后各一次）；⑤ runbook 每条命令当场执行；⑥ 三条命令绿。
- **⑥ 边界**：本仓失败语义与运维面；订单 HTTP 契约归 `SVC-BROKER-G1`，DTO 进契约归 `tellurion-contracts:CT-05`，core 旧实现退役归 `tellurion:SVC-CORE-BROKER-RETIRE`。

## 施工序（候选单，开工前必须展开成六字段）

> 这张表是**施工顺序**，不是任务台账：每条在执行前要展开成六字段单（目标/上下文/要求/约束/验收/边界），并写明冻结契约的路径与节号。
> 顺序有依赖，**不要跳级**：跳序的代价是返工（例：X1a 的协议没冻结就写 aggregator，等于把协议写死在一个服务里）。
> 2026-10-21：候选单已展开为「一、本仓任务」的正式六字段条目（表中「上游单号」指向的 QT 单正文现在就在本仓）。

| 阶段 | 内容 | 上游单号 | 前置 |
|---|---|---|---|
| X6a | `BrokerGateway` port + `MockTrader`（`ClientOrderID` 幂等 / 超时语义跑通）🔶（施工 ① 已落 `internal/gateway`，未接 core） | `QT-45` | 本仓立项 + M8 对表 ✅ |
| X6b | 对账口径定稿（**账户快照** `QuerySettlement` + 真实 broker 侧 + 差异注入验收）🔶（施工 ② 口径纯函数 + 差异注入验收已落 `internal/reconcile`；真实 broker 侧归 X6c 全量） | `QT-45` | X6a |
| X6c | `XTPTrader` 适配器（从零实现；装配前置：对账 + 幂等 + 熔断，缺一构造期拒绝）**⛔ 阻塞（G7，2026-10-25）**——装配闸 + fail-closed 骨架 + 持久幂等映射已落，全量实现缺 P1~P5 五项外部条件（SDK / 测试账号 / 出网 / C 工具链 / 盯盘时间窗），本仓与 core 与本机与 CI **四处都没有 SDK**；取证、剩余范围切分与触发条件见 [`x6c-xtp-readiness.md`](x6c-xtp-readiness.md) | `QT-45` | X6b ＋ **P1+P2 到手且当天有 P5 时间窗** |
| X6d | 独立进程（回环监听 + 健康检查）——**ADR-036（2026-10-26）取代 2026-10-23「暂缓」**：本仓按服务定位，G0 先落 Mock 服务入口；真实 XTP 仍受 X6c 阻塞 | `SVC-BROKER-G0` / `QT-45` | 多账户共用通道 / 通道级高可用 |

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
