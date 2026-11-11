---
service-card: 1
repo: tellurion-broker
kind: service
lifecycle: production
owner: ruoxizhnya
impact: blocking

requires:
  - tellurion-contracts
required-by: []
calls: []
called-by:
  - tellurion

truth:
  readme: README.md
  agents: AGENTS.md
  architecture: docs/ARCHITECTURE.md
  api: docs/api-schema.md
  db: docs/db-schema.md
  dataflow: docs/dataflow.md
  runbook: docs/runbook.md
  tasks: docs/TASKS.md

status: evergreen
last-verified: 2026-11-11
verified-by: 2026-11-11 实读 README / AGENTS / ARCHITECTURE / api-schema / db-schema / dataflow / runbook / go.mod / deploy/compose.yaml；端口、端点数量、表数量未写入
---

# 服务卡：tellurion-broker

> **一句话**：交易通道适配层——把订单 / 查询 / 对账转成柜台调用并保证幂等；订单真相不在这里。
>
> `service` · `production` · `blocking` · `ruoxizhnya`

## 1. 这是什么

它是 core 与外部柜台之间唯一的通道边界：接收已通过风控与资金检查的订单请求，用客户端订单号做幂等占位，转成柜台调用并把回执写回映射，再向 core 提供成交 / 持仓 / 结算查询与对账快照。今天常驻的是 `Mock` 实现；真实 XTP 适配器是 fail-closed 骨架，尚未接通。

**它拥有**：`broker.order_acks`（请求幂等映射）的 DDL、写权与保留期清理；`BrokerGateway` / `SettlementQuerier` / `KillSwitch` / `HealthChecker` 四个 port 的网关侧实现。
**它不拥有**：订单 / 成交 / 持仓的最终真相（在 core 的 `quant`），风控裁决，策略资产，任何行情数据。

## 2. 它不做什么

- 不做策略或风控裁决：网关只转发与确认，拒单理由不在这里生成。
- 不 import core 的实现包：跨仓只走 `tellurion-contracts/broker` 与 HTTP。
- 不在没有对账方案的情况下接真钱通道；装配闸不通过就拒起，不提供绕过开关。
- 不写 core 的订单 / 成交 / 持仓表：本仓只写 `broker.order_acks`。
- 不对外假装成功：未装配、通道未知或映射读失败一律报明确错误，不返回 fake success。

## 3. 与谁相连

### 调用它（入边）

| 来源 | 协议 | 用来做什么 | 本仓挂了会怎样 |
|---|---|---|---|
| `tellurion` | HTTP | 下单 / 撤单 / 查询 / 对账面（core 的 `pkg/brokerhttp`） | core 启动期会 `GET /version` 核对通道性质，本仓不可达或通道不符即**拒起**；已运行实例若本仓后挂，下单失败但 core 进程存活 |

### 它调用（出边）

| 目标 | 协议 | 用来做什么 | 目标挂了会怎样 |
|---|---|---|---|
| PostgreSQL | PG | `broker.order_acks` 的占位、回执与保留期清理 | 启动期装配闸不过即拒起；运行期幂等映射读写失败报 `channel_down`，不放行订单 |
| 券商柜台（XTP，未接通） | 专有 SDK | 真实报单 / 撤单 / 查询 | 今天装配的是 `Mock`；真实适配器未接通时所有方法恒 `channel_down`，零 fake success |

> 不写 URL、端口、凭证。地址见 `deploy/compose.yaml` 与 core 的 `config/`；接口形状见 `docs/api-schema.md`。
> 本仓**不调用任何内部服务仓**；`/version` 的 `channel` 字段是通道性质的唯一对外标识。

## 4. 运行时身份

- **常驻进程**：`cmd/brokergateway`（唯一 HTTP 监听）。
- **存活/就绪语义**：`GET /healthz` 是**唯一探针**；返回就绪即代表装配闸已通过（需要的 port 与持久映射都已就位），不是单纯的进程存活。
- **鉴权**：写侧需要服务 token；未配置时写面关闭而不是开放。
- **部署真相**：`deploy/compose.yaml`、`Dockerfile`、`README.md` 快速开始。
- **端口**：不在此卡；见上述部署真相。

## 5. 数据与契约归属

| 维度 | 归属 | 真相源 |
|---|---|---|
| DB schema | `broker.order_acks` 的唯一写者与 DDL | `docs/db-schema.md` |
| 跨仓 DTO / 契约 | 形状归 `tellurion-contracts/broker`；本仓只保留类型别名与指纹函数 | `tellurion-contracts:docs/API.md` |
| 对外 HTTP | 订单 / 查询 / 对账面 + 探活 / 版本 | `docs/api-schema.md` |

> 订单 / 成交 / 持仓的字段与状态词表归 core，不归本仓；本卡不复述。

## 6. 坏了怎么办

- **影响面**：`blocking`——本仓是默认部署的必填上游，启动期不可达会让 core 拒起；已运行实例后挂则交易链路断，回测与研究不受影响。装配闸 fail-closed：缺持久映射、缺结算查询、通道性质不符都拒起。
- **先确认（按顺序）**：
  1. `GET /healthz` 是否 200；不是则看装配闸拒绝原因（映射 / 结算 / 健康检查 / 通道性质）。
  2. `GET /version` 的 `channel` 是否与 core 的 `BROKER_CHANNEL` 声明一致；不一致时 core 会拒绝启动。
  3. 下单失败看错误类别（六类失败语义）与 `broker.order_acks` 的占位 / 回执状态；按 runbook §4~§6 走。
- **升级路径**：`owner` → 跨仓争议 → core `docs/TASKS.md`
- **runbook**：`docs/runbook.md`（起停与探针 §1~§3、失败语义 §4、真 PG 停库演练 §6）。

## 7. 5 分钟后去哪

| 我想…… | 读这个 |
|---|---|
| 知道订单 / 查询 / 对账面的形状与错误码 | `docs/api-schema.md` |
| 知道 `broker.order_acks` 的保留期与清理口径 | `docs/db-schema.md` |
| 知道失败语义与就绪口径 | `docs/dataflow.md`、`docs/runbook.md` §4 |
| 起停 / 排障 / 停库演练 | `docs/runbook.md` |
| 为什么今天不接真钱 | `docs/adr/0003-真钱通道暂不实施与重开条件.md` |
| 当前在做什么 | `docs/TASKS.md` |
| 架构与决策 | `docs/ARCHITECTURE.md` / `docs/adr/` |
