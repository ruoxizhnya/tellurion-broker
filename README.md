# tellurion-broker

> module: `tellurion-broker` ｜ 状态：**服务已启动**——X6a 四 port + 内存 `Mock` + HTTP 面（G1）+ 持久幂等映射；`QT-45` ✅ 收口（终态 = Mock 全绿 + `XTPTrader` fail-closed 骨架）；`X6c`（真实 XTP 全量适配器）**⏸ 挂起**，重开条件见 [`docs/adr/0003-真钱通道暂不实施与重开条件.md`](docs/adr/0003-真钱通道暂不实施与重开条件.md)

交易通道网关：`Mock` / `XTPTrader` = 同一 port 的两个实现（`MockTrader` 是 core 旧名，已随 2026-11-10 退役）。**落地前必须先有对账方案**。

## 目录

| 路径 | 内容 |
|---|---|
| `cmd/brokergateway/` | 网关服务进程入口（`127.0.0.1:8091`；`/healthz` / `/version` + `/api/broker/*` 订单/查询/对账面，G0/G1 均已落） |
| `internal/gateway/` | `BrokerGateway` / `SettlementQuerier` / `KillSwitch` / `HealthChecker` port + DTO + `Mock`（X6a） |
| `docs/` | 本仓文档：台账 / 边界 / 决策 / 归档 —— 从这里进：`docs/README.md` |
| `AGENTS.md` | 本仓入口（构建、边界、禁止、依赖版本） |
| `doc.go` | 根包说明（实现按能力拆子包） |

## 快速开始

```bash
go build ./...
go vet ./...
go test ./...
```

本仓**单独 checkout 就能跑通**上面三条。需要别的仓源码才过 → 边界错了，不是加 `replace` 能解决的问题。

跑起来（Mock 网关：探活 / 版本 + `/api/broker` 订单面）：

```bash
# 本机直跑（默认 127.0.0.1:8091）
go run ./cmd/brokergateway

# 或容器（单仓 compose）
docker compose -f deploy/compose.yaml up --build
```

`GET /version` 返回构建信息 + 只读的 `"channel": "mock"`——这是通道性质的
唯一对外标识，core 启动期据此核对 `BROKER_CHANNEL` 声明（声明 `live` 却读到
`mock` 会被 core 拒绝启动）。接线真实柜台时必须把该字段同步改成 `"live"`，
否则 core 不会把它当真实柜台用。

## 边界

**可以**：券商柜台适配（XTP 等）与 Mock 实现；订单/成交回报的可靠投递与幂等；与 core 的对账（日终）

**不可以**：在这里做策略或风控裁决（网关只转发与确认）；import core 的实现包；在没有对账方案的情况下接真钱通道

## 文档

- 文档导航：`docs/README.md`
- 本仓台账：`docs/TASKS.md`（唯一；跨仓任务只留指针）
- 边界与依赖方向：`docs/ARCHITECTURE.md`
- 决策记录：`docs/adr/`

## 许可

MIT（见 `LICENSE`）。
