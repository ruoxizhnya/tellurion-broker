---
status: active
last-verified: 2026-10-25
verified-by: G7 取证（本仓 find / core git grep / 本机 go env / CI 配置）+ 官方下载页与 API 文档
---

# X6c 真钱通道 —— 开工条件与决策材料（G7）

> **这份文件不裁决，只把判断摊开。** ADR 0001 定了「先 Mock 再真钱」，ADR 0002 定了幂等映射落 `broker.order_acks`；
> 派工 G7 的判定是「本组不具备开工条件，只出决策材料」——**核对下来这条前提成立**（§1）。
> 所以本文不给「硬做」的方案，只给三样东西：挡住开工的事实清单、与 SDK 无关的那半条腿、可判定的触发条件。

## 0. 三句话结论

1. **X6c 现在开工不了，不是因为少写了代码**：本仓、core、本机工具链、CI 四个位点都没有 XTP SDK（§1）。
   照猜的形状写出来的适配器，「Mock 全绿」证明不了它对柜台正确——那比「没有适配器」更糟，因为它看起来像已经验证过。
2. **真正卡住的是九项不可猜的外部事实**（§2）。其中三项在官方材料里已经能看到影子，且每一项都要求照真实头文件写：
   交易 API 是**异步回调**而本仓四个 port 是同步返回；2.2.50.8 新增 `OnUnknownOrder`（报单未知状态通知）是 `unknown_state`
   的柜台原生来源；限流有**两个面**（QPS 与当日总报单量上限）。
3. **推荐 B**（§5）：本波只清与 SDK 无关的债（其中一条真缺口本轮已修，§7），`X6c` 继续挂 ⛔，
   以「SDK + 测试账号到手」为触发条件而**不以日期为触发**（§6）。至于**这间实验室到底要不要真钱通道**，
   是独立的一问（§5 D3），门槛是外部硬条件，要主任拍。

---

## 1. 工单前提核对（四处取证，命令与读数都是实测）

| 取证位点 | 命令 | 读数 | 结论 |
|---|---|---|---|
| 本仓 | `find . -not -path './.git/*' \( -iname '*xtp*' -o -iname '*.h' -o -iname '*.dll' -o -iname '*.so' -o -iname '*.lib' \)` | 只有 `./internal/gateway/xtp.go` 与 `./internal/gateway/xtp_test.go` | 无 SDK、无头文件、无库文件 |
| core（旧实现，只读取证） | `git grep -l 'import "C"'` ／ `ls internal/cgo` | 前者**零命中**；后者 `No such file or directory` | 老 skeleton 注释里承诺的 `internal/cgo/xtp/` **从未存在**；core 侧也没有任何绑定可搬 |
| 本机工具链 | `go env CGO_ENABLED` ／ `go env CC` ／ `command -v gcc` | `0` ／ `gcc` ／ 未找到 | `import "C"` 的包在这台机器上**现在编不起来**。与 ADR-024 拒 `plugin.Open` 同一条理由：本机验证不了的路径不配当主链路 |
| CI | `.github/workflows/ci.yml` | `ubuntu-latest`，只有 checkout 本仓 + gofmt/build/vet/test 四步 | 没有 C 工具链步骤、没有柜台网络；且 CI 头部与本仓 AGENTS 明写**不得 checkout 其他仓** |

⇒ 「仓内无 SDK 无法验证」不是"我没找到"，是四个位点都没有。

## 2. 「无 SDK」到底挡住了什么：不可猜清单

| # | 必须来自 SDK / 柜台的事实 | 猜错的后果 |
|---|---|---|
| 1 | **柜台错误码 → 本仓 12 哨兵的映射表**（`api-schema.md` §2 要求适配器私有错误必须在边界翻译） | 把「明确拒单」读成「状态未知」（或反过来）：一条**不重试**、一条**只能查单**——两条处置路径相反，猜错就是重复下单或漏撤 |
| 2 | **柜台原生幂等能力**：`client_id`（官方：一进程一个，可在 [1,24] 任选并固定下来）＋ `flow_id` 能否拒掉重复报单 | ADR 0002 把它登记为**第二道防线**，今天不可验证。若其实可用，`broker.order_acks` 的判重与保留期口径都要跟着改 |
| 3 | **回报模型**：交易 API 是 `TraderApi` 发起 + `TraderSpi` **异步回调**；本仓四个 port 是同步返回 | 适配器得在调用内等回调，而等待上限（超时值）必须来自真实柜台：拍小了假 `unknown_state`，拍大了挂死调用方 |
| 4 | **`OnUnknownOrder`**（API SDK 2.2.50.8 新增「报单未知状态通知」） | 这是 `unknown_state` 的柜台原生来源；接不接、怎么映射，只能照真实头文件写 |
| 5 | **限流的两个面**：QPS，以及「单个用户当日报单总量上限」（2.2.50.8 新增 `SetMaxOrderBufferQuantity`） | 只按 QPS 写令牌桶 = 漏掉一个真实事故来源；这两个数都不在仓内可知 |
| 6 | **心跳 / 重连 / 流恢复语义**（`SetHeartBeatInterval` 默认 15s 这类常数，登录恢复类型） | 熔断输入 `HealthCheck` 的判断口径就错——而熔断是真钱路径的刹车 |
| 7 | **`QuerySettlement` 的字段口径**：现金 / 冻结 / 可用 / 市值哪一列对得上 `gateway.AccountSnapshot` | 对账要么恒差异（噪声淹没真问题），要么恒零差（假绿）——就是 F6 那个「自己对自己」的翻版 |
| 8 | **申报数量的 XTP 入参**（R5 的 XTP 半腿） | 纯函数 `internal/gateway/lot` 已落，但接到真实字段名要靠 SDK |
| 9 | **平台与位数**：官方明写库**只支持 64 位**、要用 64 位编译器；社区封装另注明 mac 未做编译测试 | 构建矩阵（Windows 开发机 / ubuntu CI / 未来 Linux 部署）今天无法定型 |

## 3. X6c 剩余范围切分：哪一半与 SDK 无关

| 事项 | 要 SDK / 柜台吗 | 本波 | 落点 |
|---|---|---|---|
| 下单 / 撤单 / 查单 / 回报 / 心跳 / 重连 / 限流的翻译层 | 要 | ⛔ | X6c 全量 |
| CGo 绑定 + 构建矩阵 | 要（还要 C 工具链） | ⛔ | X6c 全量 |
| 柜台错误码映射表（§2-1） | 要（头文件 / 官方文档） | ⛔ | X6c 全量 |
| R5 的 XTP 半腿（`lot` 接进真实入参） | 要 | ⛔ | X6c 全量 |
| 真实 broker 侧 `QuerySettlement`（R3 的 X6c 部分） | 要 | ⛔ | X6c 全量 |
| **撤单不得把「映射里有占位」报成 `order_not_found`** | 不要 | ✅ **本轮已修**（§7） | `internal/gateway` |
| `broker.order_acks` 保留期 / 备份 | 不要代码，要一个**事实**（柜台可查窗口，§2-2） | ⬜ 待触发后定数 | `db-schema.md` §5 |
| 密钥注入形状（R8：环境 / 密钥管理 + 脱敏测试） | 半要（形状可先定，字段随 SDK） | ⬜ 可先立形状 | X6c |
| Mock 与真实柜台的行为差异清单（§8 F9） | 不要 | ✅ 本轮登记为待裁 **R9** | `ARCHITECTURE.md` §9.7（＋ 台账记录） |
| core 旧面退役（R4） | 不要 | ⛔ 跨仓腿（本波不改 core） | `tellurion:docs/TASKS.md` |

## 4. 开工前置清单（拿齐这五项才叫具备条件）

| # | 前置 | 从哪来 | 没有它会怎样 |
|---|---|---|---|
| P1 | **XTP SDK 本体**：C++ 头文件 + win64 / linux 的 `.dll`/`.so` + Demo 参考代码 | 官方下载中心（下表演示：API SDK 版本 2.2.50.8，2025-09-08 更新） | §2 的九项全是猜 |
| P2 | **测试账号与测试环境连接信息**（IP / 端口 / 账号 / 密码 / ClientID） | 官网注册后在个人中心填测试账户申请单；材料称约一个工作日出结果，股票类与算法类各一组，两融需另行申请 | 没有可连的柜台 = §2 的 3 / 4 / 6 条无法实测 |
| P3 | **出网与防火墙**：到测试环境地址的白名单（官方另提供 VPN / 堡垒机路径） | 本机与部署机的网络 | 连不上就只能拿到 `channel_down`，与今天没有任何区别 |
| P4 | **带 C 编译器、64 位库的构建环境** | 本机今天 `CGO_ENABLED=0` 且 PATH 无 gcc；CI 的 ubuntu 需新增工具链与库文件放置路径 | `import "C"` 编不过，本地与 CI 两套结论 |
| P5 | **一个愿意盯真实挂单的时间窗**（首批只下限额内的真单） | 人 | 「接好了但没人看过它跑」= 把首次运行留给生产（core AGENTS §14 的 AUD-34 教训同源） |

**外部事实出处**（本文 §2 / §4 引用，一律以官方头文件与文档为准，本文按公开材料记录）：

> ⚠️ **出处的成色要分开读**：第 1、2、5 行是中泰官方站点 / 官方仓库；**第 3、4 行（测试账号申请流程、实盘权限门槛）来自第三方公开专文，不是官方页**——
> 真要申请时以官网当场为准。这两行只用来回答「这件事有没有硬门槛、大概要多久」，不用来定数字。

| 事实 | 出处 |
|---|---|
| SDK 与算法总线 SDK 的下载入口、2.2.50.8 的更新项（含 `OnUnknownOrder`、`SetMaxOrderBufferQuantity`） | <https://xtp.zts.com.cn/service/download> |
| 交易 API 的两个接口类（`TraderApi` / `TraderSpi`）、异步回报、`client_id` 取值 [1,24]、心跳超时默认 15s、库仅 64 位 | <https://xtp.zts.com.cn/xtp-pro/API4/%E4%BA%A4%E6%98%93XTrader-API%E4%BD%BF%E7%94%A8%E6%8C%87%E5%8D%97QuickStart.html> |
| 测试账号申请流程与两组测试账号（股票 / 算法）、两融另开 | <https://zhuanlan.zhihu.com/p/614102313> |
| 实盘权限需第三类专业投资者认证（个人：金融资产≥300 万或近 3 年年均收入≥30 万，且 1 年以上投资经历） | 同上 |
| 多语言官方封装（win64 / linux，mac 未做编译测试） | <https://github.com/ztsec/xtp_api_java> |

## 5. 三个选项，与我的推荐

| 选项 | 做什么 | 代价 | 什么时候选它 |
|---|---|---|---|
| **A 维持现状** | 不动代码。`XTPTrader` 保持 fail-closed：装配闸缺一即构造期拒绝，已装配后全方法恒 `channel_down`，零 fake success | 纸交易之外没有实盘出口；§2 的九项永远是猜；R3 要求的**真实 broker 侧快照**永远是空位（对账就只能停在纯函数层） | 如果这间实验室的目标就到「研究 + 纸交易」为止——ADR-023 的单人自托管口径**允许**这个答案 |
| **B 只清 SDK 无关的债**（**推荐**） | 本轮已做：§7 的撤单判据 ＋ §8 的 F9 / 哨兵两条登记为 R9 / R10。**仍待做**（都在 B 里，不依赖 SDK）：保留期与备份口径（要 P2 提供「柜台可查窗口」这个事实）、R8 的密钥形状与脱敏测试、R9 收紧与否的裁决 | 看起来「没推进 X6c」；台账要多两三条待办 | **默认**。便宜、不引入猜测，且这些本来就是真钱路径的前置 |
| **C 一次最小闭环仿真接入** | P1+P2 到手后只做 `Connect` + `PlaceOrder` / `CancelOrder` + `QuerySettlement`；**不做** `EmergencyFlatten`，不碰两融 / 算法总线；验收腿用真实回报 + 注入差异 | 需要 P3/P4（C 工具链与出网）；一次性投入最大；错一个映射就是真钱语义错 | 出现「要用真实柜台成交差异来校准回测」的研究目标时 |

**明确不推荐**（写下来免得下次再议）：**先照猜的 SDK 形状把适配器写起来、等环境到手再改**。
它会在整个系统最难测的地方制造"看起来已验证"的假证据——与本仓「零 fake success」那条红线是同一类问题，
只是这次假的是适配器自己。

**D3 · 独立一问（要主任拍，不属于工程岔路）**：这间实验室**要不要**真钱通道。
实盘权限的门槛是外部硬条件（§4 出处表最后一条：第三类专业投资者认证 + 资产/收入门槛），不是代码能解决的。
如果答案是否，A+B 就是终态，X6c 应从「🔶 待做」改写成「不做的决定」并立一条 ADR——
今天它挂在 🔶 上，等于一笔永远在计息的隐性待办。

## 6. 触发条件（像 X6d 那样写成可判定的，不写「以后再说」）

X6c 从 ⛔ 转 🔶 的条件 = **P1 与 P2 同时到手，且当天有 P5 的时间窗**。

三条里任一不满足，本仓不动 XTP 适配器代码（fail-closed 骨架与装配闸保持现状，它们本来就是为这一天准备的）。
X6d（独立进程）另有自己的触发条件，见 ADR 0002 代价 4——两件事别互相等。

## 7. 本轮已做：一条与 SDK 无关的真缺口（G7 里唯一的代码改动）

- **缺陷**：`Mock.CancelOrder` 在本地没有订单状态时直接报 `order_not_found`。而该 code 的判据是
  「网关**能证明**从未发送过该 ID」（`api-schema.md` §2、`dataflow.md` §4-1）。幂等映射里有占位时
  （另一个进程发出去的、或本进程重启后本地状态丢了）它**证明不了**——调用方按 `order_not_found` 的既定处置
  （「可按新单处理」）换个新 `ClientOrderID` 重发，柜台就是两笔。与施工 ① 自审第 ③ 条同族（判重域与本地索引口径）。
- **修法**：撤单在本地无状态时先读映射（**同一判重域** `m.accountID + ClientOrderID`）→ 有记录即
  `unknown_state`（走查单，不重发撤单）；映射也没有才 `order_not_found`（`internal/gateway/mock.go:350-364`）。
  用的 code 在 `ARCHITECTURE.md` §9.4 已冻结的 `CancelOrder` 错误集内，**不是新增契约面**。
  新腿：`TestCancelOrderDoesNotClaimNeverSentForAReservedOrder`（`internal/gateway/mock_test.go:500`）。
- **测试先行与破坏验证**（真实退出码，不截断）：
  - 新腿先对着**未修**的代码跑 → 红：`TEST_EXIT=1`，`err = broker: order not found: c-9, want ErrUnknownState`
    （证明这条腿真在测东西，不是随大流绿）。
  - 修后全仓：`BUILD_EXIT=0`、`VET_EXIT=0`、`gofmt -l .` 空输出、`TEST_EXIT=0`，
    计数 **50 passed / 0 failed / 6 skipped**。六条 skip 是 PG 集成腿——本机 `127.0.0.1:5432` 无监听、
    Docker 引擎未起；带 `BROKER_TEST_DSN` 强跑则 6 条 fail-loud（`connectex: ... actively refused it`），不是假绿。
  - 破坏腿：把新分支的哨兵换成**同样合法的** `ErrOrderNotFound` → `TEST_EXIT=1`，红在
    `mock_test.go` 那条 `c-9` 断言上；逐字节还原后 `sha256sum -c` → `OK`，`go test ./internal/gateway` 绿。
- **文档同步**：`api-schema.md` §3 补撤单判据；`ARCHITECTURE.md` §9.4 表里 `CancelOrder` 行的「超时 →
  `unknown_state`」从此有了一条**本地无状态**的腿（§9.8 现状矩阵同步改一行）、§9.7 新增 **R9**（Mock 与
  柜台的三处行为差异）与 **R10**（映射读失败的哨兵）两条待裁；§1 定位补一句 X6c 的阻塞状态；
  本文登记进 `docs/README.md` §1/§2；台账见 `TASKS.md` QT-45 施工记录「G7（施工 ⑤）」。

## 8. 取证时看到、但**不属于** G7 范围的两组事实（登记，不擅自改）

1. **F9 · Mock 与真实柜台的三处行为差异**（ADR 0001「代价 3」的具体化，也是 C 组接入时的验收清单来源）：
   - ① 买入当日即 `AvailableQuantity = Quantity`——**T+1 不进 Mock**（`internal/gateway/mock.go:295`）；
   - ② 卖出数量超过可用数量时 **Mock 放行**，持仓减到 ≤0 直接删行（`internal/gateway/mock.go:300-305`）：
     `lot.Size` 有 `exceeds_available_quantity`，但 `PlaceOrder` 只用 `lot.ValidateQuantity` 校申报单位，
     没有校可用量（`internal/gateway/mock.go:663`）；
   - ③ `Fill` 的三项费用与 `AccountSnapshot.Fees` **恒零**（Mock 不建模费用，`fillLocked` 不带费用字段）——
     对账的费用桶今天只能比出零差。

   后果要如实读：Mock 全绿的用例在实盘可能是拒单或市值差，**今天这条「绿」的含金量就是这些**。
   要不要收紧（尤其 ②）是**契约行为变更**——要动 `api-schema.md` §2 的错误语义并可能要一条 ADR，
   不在 G7 里单方面改，留给主任定单。
2. **诊断质量问题（不是安全缺陷）**：映射读失败被报成 `channel_down`（`internal/gateway/mock.go:415` 的
   `QueryOrder` 早已如此，本轮 `mock.go:358` 的 `CancelOrder` 刻意与它同口径）。准确的哨兵应是
   `idempotency_store_unavailable`。
   今天这个方向调用方的处置是「熔断：停止下单 + 告警」，比真实原因更保守（不会造成发送），所以只登记待裁；
   本轮没有另造第三种行为。

## 9. 一句话给下一位读这份文件的人

X6c 不缺代码，缺的是**SDK、账号、网络、C 工具链和一个能盯盘的时间窗**（§4 的 P1~P5）；
在那之前本仓能做的只有 B 组（§5），而「要不要走到真钱」这件事本身（§5 D3）还没人算过收益/代价。

## 10. 本轮改了哪些文件（G7 的全部产出）

| 文件 | 改动 | 为什么 |
|---|---|---|
| `internal/gateway/mock.go` | `CancelOrder` 在本地无状态时先读幂等映射（`:350-364`） | §7 的判据缺陷：误报「从未发送」会导致调用方换新 ID 重发 |
| `internal/gateway/mock_test.go` | 新增 `TestCancelOrderDoesNotClaimNeverSentForAReservedOrder`（`:500`） | 测试先行：先对着未修的代码红，再绿 |
| `docs/api-schema.md` | §3 补「`CancelOrder` 的 `order_not_found` 判据与 `PlaceOrder` 同一条」 | 改接口语义 → 本文是真相源 |
| `docs/ARCHITECTURE.md` | §1 定位补 X6c 阻塞一句；§9.7 R6 指向本文 + 新增 **R9 / R10** 两条待裁；§9.8 现状矩阵与自检结论订正（含「不是忘了做，是缺外部条件」） | 改判定/登记待裁 → 对表与现状要同步 |
| `docs/README.md` | §1 与 §2 各登记本文一行 | 新增文档必须进导航（AGENTS §6 变更触发表） |
| `docs/TASKS.md` | QT-45 新增「G7（施工 ⑤）」施工记录 + 未落项改口径 + 施工序 X6c 行改判 ⛔ 阻塞 | 台账是唯一活口，未落项必须如实写 |
| `AGENTS.md` | §1 状态补一句 X6c ⛔ 与本文指针 | 单仓入口要能看出「真钱通道今天不可用是设定」 |
| 本文 | 新建 | G7 的交付物 |
