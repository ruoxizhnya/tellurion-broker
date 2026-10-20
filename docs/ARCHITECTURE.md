---
status: active
last-verified: 2026-10-20
verified-by: 功能边界固化（三份契约：db / api / dataflow）
---

# 架构 — tellurion-broker

## 1. 定位

交易通道网关：`XTPTrader` / `MockTrader` = **同一个 port 的两个实现**。

它出仓的时机排在最后（X6）：单账户单实例阶段，独立进程的收益（多策略共用通道、通道独立重启）兑现不了，而下单是延迟敏感路径。所以**先把 port 定好，实现先放进程内**。

## 2. 分层

```
core（订单意图）
      ↓  BrokerGateway（port，进程内）
适配器层
  ├── MockTrader  → 内存 / 文件
  └── XTPTrader   → 券商柜台（最小单位/委托类型/限流在适配器内）
      ↓
成交回报 / 持仓 ──► 实盘运行域（run / exec，待定）
      └── QueryFills ──► 对账（risk 留痕）
```

## 3. 组件职责

| 组件 | 职责 | 不做 |
|---|---|---|
| `BrokerGateway` port | 下单 / 撤单 / 查单 / 持仓 / 成交 | 不做风控 |
| Mock 实现 | 开发与回放 | 不模拟复杂柜台规则 |
| XTP 适配器 | 柜台协议 + 归一化 | 不决定"能不能下" |
| 对账输入 | `QueryFills` | 不做对账判断（归 risk） |

## 4. 三份契约（功能的边界就在这三份文件里）

| 维度 | 文件 | 回答什么问题 |
|---|---|---|
| 数据形状 | [`db-schema.md`](db-schema.md) | 本仓拥有哪些表？谁能写？ |
| 接口形状 | [`api-schema.md`](api-schema.md) | 对外暴露什么？错误码与幂等怎么做？ |
| 流向与顺序 | [`dataflow.md`](dataflow.md) | 数据怎么流？哪里 fail-closed？ |

## 5. 依赖方向

```
contracts（BrokerPort / OrderRequest / Fill）+ kit（logging/http）
        ↑
 tellurion-broker ◄──(进程内 port)── core
        └──► 券商柜台
```

## 6. 关键决策

- [`adr/0001-先-mock-再真钱.md`](adr/0001-先-mock-再真钱.md)

## 7. 非目标（明确不做）

- 不做风控、不做策略、不做撮合
- 不持有订单真相
- 不做资金划转
- 现在不做独立进程（触发条件：多账户共用通道 / 通道级独立高可用）

## 8. 分期与状态

| 阶段 | 内容 | 前置 |
|---|---|---|
| X6a | port 定义 + MockTrader | 无 |
| X6b | 对账口径定稿 | risk 的 `QueryFills` 契约 |
| X6c | XTPTrader | 对账 + 幂等 + 熔断三项就位 |
| X6d | 独立进程（可选） | 多账户 / 多策略共用通道 |
