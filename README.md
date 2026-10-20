# tellurion-broker

> module: `tellurion-broker` ｜ 状态：**预留（S0 占位，触发式启动）——尚未启动，无实现**

交易通道网关：`XTPTrader` / `MockTrader` = 同一 port 的两个实现。**落地前必须先有对账方案**。

## 目录

| 路径 | 内容 |
|---|---|
| `docs/` | 本仓文档：台账 / 边界 / 决策 / 归档 —— 从这里进：`docs/README.md` |
| `AGENTS.md` | 本仓入口（构建、边界、禁止、依赖版本） |
| `doc.go` | 占位包（无实现） |

## 快速开始

```bash
go build ./...
go vet ./...
go test ./...
```

本仓**单独 checkout 就能跑通**上面三条。需要别的仓源码才过 → 边界错了，不是加 `replace` 能解决的问题。

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
