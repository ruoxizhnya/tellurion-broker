---
status: active
last-verified: 2026-10-20
verified-by: 功能边界固化（三份契约：db / api / dataflow）
---

# Dataflow — tellurion-broker

> 目的：把「数据从哪来、在谁手里变形、到哪去」写死。**任何跨仓数据交换都必须能在本图上找到一条边**；找不到 = 这段代码在偷偷建第二条路。

## 1. 主链路

```
core（下单意图 + 风控裁决已完成）
        │  PlaceOrder(OrderRequest) —— 进程内 port
        ↓
  tellurion-broker（适配器）
        ├── MockTrader  ──► 内存/文件（开发、回放）
        └── XTPTrader   ──► 柜台 ──► 成交回报
        ↓
  成交/持仓快照 ──► 实盘运行域（run / exec，待定）
        └── QueryFills ──► 对账（risk 持有留痕）
```

## 2. 关键时序

### 下单
1. core 生成 `OrderID`（幂等键）→ `PlaceOrder`
2. 适配器做柜台级换算（最小单位、委托类型）→ 发柜台
3. 返回 `OrderAck`（已受理 ≠ 已成交）
4. 成交回报异步到达 → 更新运行域状态

### 超时（最危险的分支）
5. `PlaceOrder` 超时 → 返回 `ErrUnknownState`
6. 调用方必须 `QueryOrder` 确认，**不得重发**
7. 查不到 → 告警 + 人工介入（真钱场景不做自动补偿）

### 对账（日终）
8. `QueryFills(since)` → 与运行域记录比对 → 差异进 `risk` 的对账留痕

## 3. 失败模式（每一处 fail-closed 都要能指名）

| 失败点 | 症状 | 兜底 |
|---|---|---|
| 超时后盲目重发 | 重复下单（真实亏损） | `ErrUnknownState` + 强制查单 |
| 柜台断连 | 单发不出去 | 熔断 + 告警，不静默重试 |
| 归一化漏字段 | 上游拿到券商私有字段 | 契约里禁 XTP 字段；适配器内消化 |
| 密钥进库/进代码 | 泄露 | 环境变量 / 密钥管理；提交前扫描 |

## 4. 不变量（可被测试钉住的）

1. 同一 `OrderID` 不会产生两笔柜台订单。
2. 超时一定返回 `ErrUnknownState`（不是 generic error）。
3. 对外 DTO 不含任何券商特有字段。
4. 无对账方案时，`XTPTrader` 不可被装配（可用构造期检查钉住）。

## 5. 与契约文档的关系

- 表结构 → `docs/db-schema.md`
- 接口形状 → `docs/api-schema.md`
- 本文件只讲**流向与顺序**，不重复字段定义。
