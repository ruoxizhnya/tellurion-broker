---
status: active
last-verified: 2026-11-07
verified-by: G4 施工（失败语义 / 就绪口径 / KillSwitch 姿态 / 演练；PG 腿未执行）
---

# Runbook — tellurion-broker 网关运维

> 适用对象：`cmd/brokergateway`（今天只装配 **Mock**；真实 XTP 仍被 X6c ⛔ 阻塞）。
> 本文的所有命令都在**仓库根目录**、Windows PowerShell 5 下执行；每条命令前先做一次环境准备：

```powershell
$env:Path = [System.Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [System.Environment]::GetEnvironmentVariable('Path','User'); $env:CGO_ENABLED='0'
```

> 端口约定：文档里的常驻实例用 `127.0.0.1:8091`；演练用 `127.0.0.1:18091`（避免撞到本机正在跑的实例）。
> **本仓今天不接真钱**（见 §9）；本文不会给出任何“绕过装配闸”的步骤。

## 1. 就绪口径（与 `api-schema.md` §1.6 一致，已定死）

**选择：不引入 `/readyz`；`/healthz` 是唯一探针，就绪 = 装配闸通过。**

- 本仓只监听一个 HTTP 端口（默认 `:8091`），没有独立的业务监听面；再加 `/readyz` 会让“进程活着 / 通道可用 / 幂等存储可用 / 可接单”四种状态争一个探针，排障时反而不知道信谁。
- **真钱态**：`NewXTPTrader` 的装配闸在**构造期**执行（持久 `AckStore` / `SettlementQuerier` / `HealthChecker` 缺一即 `ErrAssemblyIncomplete`）；构造失败进程直接退出。所以“进程能监听” ⇒ “装配闸已通过”。
- **Mock 态**：`NewMock` + `httpapi.New` 在启动期完成装配；`httpapi.New` 缺 port 直接 panic，不会带半装配继续跑。Mock 允许 `Store=nil`（每笔下单 fail-closed 为 `idempotency_store_unavailable`），这仍然算“就绪”——**就绪回答的是“进程该不该被重启”，不是“这一笔能不能下”**。
- `/healthz` 只回答通道可用性；幂等映射故障是**业务错误** `idempotency_store_unavailable`（503），不是探针失败。把 PG 池塞进探针会让读侧（查单 / 持仓 / 对账）被整体摘掉，扩大事故面。
- `Acks.Durable()` 是构造期静态判据，不是运行期探针。

现场判定：

```powershell
Invoke-RestMethod -Uri http://127.0.0.1:8091/healthz
Invoke-RestMethod -Uri http://127.0.0.1:8091/version
```

`/healthz` → `200 {"status":"ok"}` 且进程已启动 = 就绪；`503 {"status":"down","reason":"channel_unavailable"}` = 通道不可用（不是“重试下单”的信号）。

## 2. 起停

构建并启动一个演练实例：

```powershell
$tmp = Join-Path $env:TEMP 'tellurion-broker-g4'
New-Item -ItemType Directory -Force $tmp | Out-Null
go build -o (Join-Path $tmp 'brokergateway.exe') ./cmd/brokergateway
$env:BROKER_SERVICE_TOKEN = 'g4-token'
$p = Start-Process -FilePath (Join-Path $tmp 'brokergateway.exe') -ArgumentList '-addr','127.0.0.1:18091' -PassThru
Start-Sleep -Seconds 1
Invoke-RestMethod -Uri http://127.0.0.1:18091/healthz
```

停止：

```powershell
Stop-Process -Id $p.Id
```

接 PG（持久幂等映射）时加 DSN；**连不上就启动失败**，这是 fail-closed，不是“降级用内存”：

```powershell
$env:BROKER_DATABASE_DSN = 'postgres://postgres@127.0.0.1:5446/tellurion_broker_test?sslmode=disable'
& (Join-Path $tmp 'brokergateway.exe') -addr 127.0.0.1:18092
```

预期：连接失败时进程以非 0 退出，日志首行为 `brokergateway: idempotency store: ...`。**没有 PG 就不要起持久实例**，也不要把 DSN 换成内存实现来“先跑起来”——那会把幂等事故藏到真钱路径上。

写侧 token：`BROKER_SERVICE_TOKEN` 未设 = 写面关闭（`503 writes_disabled`），不是“不校验”。读侧无鉴权（G1 的现状）。

## 3. 看装配闸为什么拒

今天的 `cmd/brokergateway` 只装配 Mock，XTP 装配闸**不在服务启动路径上**；离线核对装配闸：

```powershell
go test ./internal/gateway -run 'TestNewXTPTrader_GateRejects(IncompleteAssembly|BadConfig)' -count=1 -v
```

输出里的四条子用例就是四种拒绝原因（`-v` 会打印实际错误）：

```
gate rejected: broker: xtp assembly rejected: persistent ack store is required (无幂等映射不接真钱)
gate rejected: broker: xtp assembly rejected: ack store is not durable (无持久幂等映射不接真钱)
gate rejected: broker: xtp assembly rejected: settlement querier is required (无对账方案不接真钱)
gate rejected: broker: xtp assembly rejected: health checker is required (无熔断输入不接真钱)
```

- 装配闸拒绝是**构造期**错误（`ErrAssemblyIncomplete`），不是 12 个 wire code；不要把它当成某笔订单的返回码。
- 将来接入 XTP 时，启动期构造失败必须让进程退出，并在日志里保留 `xtp assembly rejected` 原文；**禁止**捕获后继续监听。
- 启动期 PG 失败（§2 的 DSN 示例）是另一类拒绝：`brokergateway: idempotency store: ...`；同样必须退出，不得回退内存 store。

## 4. 幂等映射怎么查

表：`broker.order_acks`（`docs/db-schema.md` §1.1；主键 `(account_id, client_order_id)`）。

```sql
SELECT account_id,
       client_order_id,
       ack IS NOT NULL                       AS has_ack,
       left(request_fingerprint, 12)         AS fingerprint_prefix,
       created_at,
       updated_at
FROM broker.order_acks
WHERE account_id = '<account>'
  AND client_order_id = '<client_order_id>';
```

读法：

| 查询结果 | 含义 | 处置 |
|---|---|---|
| 无行 | 存储里没有占位 | 与网关本地状态一起看：两者都没有且存储健康，才可能证明“从未发送”；单看本表不行 |
| `has_ack = false` | 只有占位、没有回执 | 这一单**可能已经发出**；重放会得到 `unknown_state`，先查单，**严禁换 ID 重发** |
| `has_ack = true` | 已有回执 | 重放返回原回执（`idempotent_replay=true`）；被拒过的单重放仍返回同一个 error |

PG 集成腿的姿势（**本机未执行**：无 PG、Docker 不可用、`5446` 无监听）：

```powershell
$env:BROKER_TEST_DSN = 'postgres://postgres@127.0.0.1:5446/tellurion_broker_test?sslmode=disable'
go test ./internal/ackstore -count=1 -v
```

- `BROKER_TEST_DSN` **未设**：PG 用例按设计 `SKIP`（默认 CI / 无库本机）。
- 设了但连不上：用例 **fail-loud**（不是 skip），这是刻意的——有 DSN 就代表“应当能跑”。
- 查询命令 `psql "$env:BROKER_TEST_DSN" -c "SELECT ..."` 本轮**未执行**（本机无 `psql`/无 PG）；上表 SQL 是给现场用的。

## 5. `unknown_state` 排查顺序（严格按序，不要跳）

> `unknown_state` 的意思是“已发出但结果未知”，**不是失败**。本仓最要命的一条：**严禁换 `client_order_id` 重发**。换 ID 会在柜台上留下第二笔。

1. **看通道**：`GET /healthz`。若 503，先按 `channel_down` 处置；但已经拿到的 `unknown_state` 仍然必须查单，不能因为通道断了就假定没发出去。
2. **用同一个 ID 查单**：`GET /api/broker/orders/{client_order_id}`。**不要**生成新 ID 去“重试下单”。
3. 按查单结果分诊：
   - `200` 且 `status = filled / cancelled / rejected / expired`：终态，按终态处理，结束。
   - `200` 且 `status = unknown`：继续查 / 等对账；记录时间戳；不要自动补偿。
   - `404 order_not_found`：只有**存储健康**且本地状态也没有时，才说明网关能证明“从未发送”；是否按新单处理由调用方决定。
   - `503 idempotency_store_unavailable`：先修存储（查 PG / 连接池），恢复后**重新执行第 2 步**；此时不能推断“没发过”。
4. 仍无法判定：告警 + 人工介入；用 §6 的对账（账户快照）兜底。**真钱不做自动补偿**。
5. 留痕：`client_order_id`、`broker_order_id`（若拿到）、每次查单的时间与状态、最终结论。禁止把“换新 ID 重发成功”写成“重试成功”。

现场命令（`404` 也会打印 body，不抛异常）：

```powershell
curl.exe -s -w ' HTTP=%{http_code}' http://127.0.0.1:8091/api/broker/orders/<client_order_id>
```

## 6. 与 core 对账（快照口径）

口径是**账户快照**（现金 / 持仓 / 费用），不是逐笔成交；实现是纯函数 `internal/reconcile.Reconcile(local, broker *gateway.AccountSnapshot, cfg)`：

- `broker` 侧：`QuerySettlement(ctx, accountID, asOf)`（HTTP：`GET /api/broker/accounts/{id}/settlement?as_of=RFC3339`）。
- `local` 侧：core 组装后传入（今天 core 消费未接，归 `SVC-BROKER-G3` ⛔）。
- 输出七类差异 × 三级严重度：`cash` / `missing_local` / `missing_broker` / `quantity` / `market_value` / `fee` / `as_of_drift`；`critical` 立即人工核查，`warning` 当日预警，`info` 只留痕。
- 容差默认（`reconcile.DefaultConfig()`）：现金 0.01、数量 0、市值相对 0.5%（仅数量相符时比）、费用 0.01、快照时间漂移 60s。
- nil 快照 / 账户不一致 / 同一侧重复 symbol 都是 fail-loud，返回 error，不静默跳过。

今天可当场执行的只有纯函数腿：

```powershell
go test ./internal/reconcile -count=1 -v
```

**未执行 / 未接通**：真实 broker 侧 `QuerySettlement`（X6c ⛔）；core 本地快照组装与差异投递 risk（X3 / G3 ⛔）。所以今天不要声称“对账已上线”；这条命令只证明口径与差异分类可用。

## 7. KillSwitch（紧急清仓）触发、解除与留痕

> 本仓的 `KillSwitch` 只有 `EmergencyFlatten`。它绕过 T+1、逐标的真实提交并确认；**没有“自动恢复”**，也不是可反复按的“重试按钮”。

触发权限与前置：

- 只有事故指挥 / 被授权运维能触发；必须带 `BROKER_SERVICE_TOKEN`。
- 先确认 `/healthz` 200、当前持仓与事故范围；`reason` 必填，写清事故单号、操作者、触发原因（例：`INC-20261107-01 oncall=ruoxi trigger=runaway-strategy`）。
- 今天 Mock 没有真实成交，命令只验证链路；真钱态（X6c ⛔）会返回 `channel_down`，**不会伪造清仓成功**。

触发：

```powershell
$h = @{ Authorization = 'Bearer ' + $env:BROKER_SERVICE_TOKEN }
$body = @{ reason = 'INC-20261107-01 oncall=ruoxi trigger=runaway-strategy' } | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8091/api/broker/emergency-flatten `
  -Headers $h -ContentType 'application/json' -Body $body | ConvertTo-Json -Depth 6
```

读结果：

- `sold[]`：每一项都必须有 `broker_order_id` 与真实成交依据；`sold_total` 是已确认卖出的股数。
- `skipped[]`：**非空不等于失败**，但每条 `reason` 都要分诊（未确认 / 重放 / 未实现 / 通道错误）。未确认的持仓必须人工跟进，不能当成“已清仓”。
- `completed_at` 为空表示尚未完成；不要在这个状态下恢复交易。

解除（恢复正常交易）的条件：

1. 持仓已核实为 0，或剩余持仓都有明确的 `skipped` 原因和人工处置记录；
2. `/healthz` 200，且没有未决的 `unknown_state`（§5 已闭环）；
3. 事故指挥确认后，才允许恢复正常下单。**不要**用再次 `EmergencyFlatten` 当“重试”；重入靠持仓代次幂等，重放单不算本次清仓凭据。

留痕：

- 触发命令的 `reason`、完整响应 JSON、每次查单结果、事故单链接，由**调用方**写入事故记录。
- 本仓**不持久化 KillSwitch 审计**（授权与审计归 R7 / risk 的 X3 对账留痕）；服务端今天也没有独立的访问审计日志。不要声称“网关已留痕”——留痕义务在调用方，这条是已知缺口。

## 8. G4 演练：幂等存储不可用 ⇒ 不 fake success ⇒ 恢复后可继续

### 8.1 内存侧（已执行）

```powershell
go test ./internal/httpapi -run TestDrillIdempotencyStoreOutageDoesNotFakeSuccess -count=1 -v
```

真实输出（2026-11-07，本机）：

```
=== RUN   TestDrillIdempotencyStoreOutageDoesNotFakeSuccess
    g4_drill_test.go:94: outage: status=503 code=idempotency_store_unavailable message="broker: idempotency store unavailable: pg: connection refused"
    g4_drill_test.go:111: after recovery: status=200 body={"client_order_id":"g4-drill-1","broker_order_id":"MOCK-000001","status":"filled","idempotent_replay":false,"accepted_at":"2026-11-07T04:32:02.7717465Z"}
--- PASS: TestDrillIdempotencyStoreOutageDoesNotFakeSuccess (0.00s)
PASS
ok  	tellurion-broker/internal/httpapi	0.050s
```

演练断言：故障期 `503` + `idempotency_store_unavailable`；响应里没有 `broker_order_id` / `status=accepted` 这种成功形状；`QueryFills` 为 0（**没有发送**）；存储恢复后用**同一个** `client_order_id` 下单成功。这是 `httptest` 真跑，不是 mock 断言。

### 8.2 真 PG 腿（未执行）

- **未执行**：本机无 PG、Docker 不可用、`BROKER_TEST_DSN` 未设/`127.0.0.1:5446` 无监听；`CGO_ENABLED=0` 也跑不了 `-race`。
- 有库后的动作：起临时 PG（约定 `5446`）→ `BROKER_TEST_DSN=... go test ./internal/ackstore -count=1` → 停 PG（或切断连接）→ 通过 HTTP 下单，断言 `503 idempotency_store_unavailable` 且无成交 → 恢复 PG → 用**同一个** `client_order_id` 重试成功。预期与前一条一致，但输入换成真实 `PgStore`。

## 9. 永久前置：未接真钱（G4 的前置，不是待办）

- **本仓今天不接真钱。** `X6c` 是 ⛔，不是“漏做的一步”：缺 P1~P5（SDK / 测试账号 / 出网 / C 工具链 / 盯盘时间窗，见 `x6c-xtp-readiness.md` §4）。这条不随 G4 完成而解除。
- **开真钱之前，`SVC-BROKER-G1` / `G2` / `G3` / `G4` 必须全部 ✅**；任一 🔶 / ⛔ / ⬜ 都不得开真钱，也不得以“先跑小单”绕过。
- 截至 2026-11-07：G1 ✅、G2 🔶（PG 集成腿未执行）、G3 ⛔（等 QT-51）、G4 🔶（本文与演练已落，真 PG 演练腿未执行）。
- 机械判据：没有 XTP SDK 链路、`XTPTrader` 恒 `channel_down`、`cmd/brokergateway` 只装配 Mock。任何“已经可以接真钱”的说法都必须先让四项全绿并另立 ADR。

## 10. 本次执行覆盖（2026-11-07）

| § | 命令 / 动作 | 结果 |
|---|---|---|
| 2 | `go build ./cmd/brokergateway` + 起停 `:18091` | `BUILD_EXIT=0`；`/healthz` 200；已停止 |
| 2 | 不存在的 PG DSN 启动 | `PG_START_EXIT=1`（fail-closed，拒绝降级） |
| 3 | `go test ./internal/gateway -run TestNewXTPTrader_GateRejects... -v` | `EXIT=0`，四条拒绝原因已打印 |
| 4 | `go test ./internal/ackstore -v`（无 DSN） | `EXIT=0`，PG 9 条 `SKIP`；**真库查询未执行** |
| 5 | `/healthz`、`GET /orders/...`、缺失 ID 404 | 200 / 200 / 404，输出见 §5 与提交记录 |
| 6 | `go test ./internal/reconcile -v` | `EXIT=0`，七类差异注入全 PASS |
| 7 | `POST /api/broker/emergency-flatten`（Mock 空仓） | 200，`sold=[] skipped=[] sold_total=0` |
| 8.1 | `go test ./internal/httpapi -run TestDrill... -v` | `EXIT=0`，`503 idempotency_store_unavailable` → 恢复后 200 |
| 8.2 | 真 PG 停库演练 | **未执行** |
