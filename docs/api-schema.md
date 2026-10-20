---
status: active
last-verified: 2026-10-20
verified-by: 功能边界固化（三份契约：db / api / dataflow）
---

# API Schema — tellurion-broker

> **契约地位**：**Go port（进程内）**，不是 HTTP。

`BrokerGateway` 按"将来可远程"设计（DTO 序列化友好），但当前与实盘运行域同进程 —— 下单是延迟敏感路径，独立进程 = 每笔多一次网络跳。
> 本文件是**本仓对外接口的唯一真相源**。代码与它不一致时，先判断谁错：契约错 → 改本文 + 立 ADR；实现错 → 改代码。
> 跨仓消费方（core / agent / ui / 其他服务）按本文件编码，**不读本仓实现**。

## 1. 接口面

```go
// contracts —— 券商通道统一协议（归一化后）
type BrokerGateway interface {
    PlaceOrder(ctx, req OrderRequest) (OrderAck, error)   // 幂等键 OrderID
    CancelOrder(ctx, orderID string) error
    QueryOrder(ctx, orderID string) (OrderStatus, error)
    QueryPositions(ctx, account string) ([]Position, error)
    QueryFills(ctx, since time.Time) ([]Fill, error)      // 对账输入
}
// OrderRequest / OrderAck / OrderStatus / Fill = 归一化 DTO（与 domain.Order/Fill 对齐）
```

### 校验归属（必须分开，不能混）

| 校验 | 归属 |
|---|---|
| 最小单位、柜台委托类型、柜台限流 | **网关内**（适配器） |
| 数量上限、集中度、T+1、止损 | **RiskPolicy**（进程内裁决，规则源在 risk 服务） |

### 两个实现

| 实现 | 用途 | 前置 |
|---|---|---|
| `MockTrader` | 开发 / 回放 / 压测 | 无 |
| `XTPTrader` | 真实柜台 | 对账 + 幂等 + 熔断三项就位 |

## 2. 错误码与 fail-closed 语义

| 情况 | 返回 | 调用方行为 |
|---|---|---|
| 柜台拒单 | `OrderRejected{code, reason}` | 不重试（除非 reason 明确可重试） |
| 网络超时（结果未知） | `ErrUnknownState` | **查单**再决定，禁止盲目重发 |
| 重复 `OrderID` | 返回原 `OrderAck` | 幂等成功 |
| 通道未连接 | `ErrChannelDown` | 熔断：停止下单 + 告警 |

**最重要的一条**：超时 = 状态未知，不是"失败"。盲目重发是实盘事故的经典来源。

## 3. 幂等性与重试

- `PlaceOrder` 幂等键 = `OrderID`（由上游生成，网关不改写）。
- `CancelOrder` 幂等（已撤再撤返回同状态）。
- 重试只允许在"确定未送达"时进行；超时先 `QueryOrder`。

## 4. 版本与兼容

- port 只增方法；删/改方法 = 所有实现与调用方一起改（0.x 允许，bump MINOR）。
- DTO 字段只增不删；语义变更走 ADR。
- 柜台适配器可独立升级（不影响 port）。

## 5. 不提供什么（边界）

- 不做风控裁决
- 不做策略
- 不做订单簿 / 撮合（回测撮合在 core）
- 不持有订单真相（只透传柜台状态）
- 不做资金划转
