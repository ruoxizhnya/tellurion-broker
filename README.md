# tellurion-broker

> module: `tellurion-broker` ｜ 状态：**预留（触发式）**——当前为占位仓，**未启动**。触发条件同 X 线通则；启动前先立单，且必须先有对账方案。

交易网关：实盘报单、成交回报、持仓同步、券商适配。来源为 core 的 `pkg/live` + `pkg/execution` 实盘侧。

⚠️ 本仓是**唯一涉及真钱的仓**：任何不确定都必须 fail-closed（拒单而不是猜）。

## 边界

**可以**
- 对接券商/柜台，管理连接与重连
- 持有报单/成交/持仓的落地表

**不可以**
- 被回测复用（回测撮合是模拟，不能和实盘共用同一份执行代码）
- 在鉴权/风控缺失时降级下单（必须 fail-closed）

## 依赖方向

```
tellurion-broker
```

- 契约与工具一律以 **tag** 依赖（`github.com/ruoxizhnya/tellurion-contracts` / `-kit`），
  **禁止 `replace` 指向仓外路径**（单仓不可构建）。
- `go.work` 一律不入库。

## 施工入口

本仓所有任务登记在 **`tellurion` 仓的 `docs/TASKS.md`**（单一台账，避免双写）。
在台账出现本仓的单号之前，**不要在此写实现**。

## 权威文档

| 主题 | 位置 |
|---|---|
| 仓清单与命名 | `tellurion:docs/design/qt-decoupling/repo-inventory.md` |
| 分仓拓扑与阶梯 | `tellurion:docs/design/qt-decoupling/repo-topology-and-naming.md` |
| P0 修复基线 | `tellurion:docs/design/qt-decoupling/p0-round2-review.md` |
