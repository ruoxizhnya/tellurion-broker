// Package broker 是 tellurion-broker 的根包。
//
// 本仓按能力拆子包：
//   - internal/gateway：BrokerGateway / SettlementQuerier / KillSwitch / HealthChecker 四个 port、
//     DTO、契约哨兵、Mock 与 XTP fail-closed 骨架（X6a / X6c 装配闸）。
//   - internal/gateway/lot：交易所申报数量规则纯函数（R5）：目标权重/价格/资金 → 手数 + 拒单原因；
//     无 I/O、无时钟、无全局状态。
//   - internal/reconcile：快照口径对账纯函数（X6b）。
//   - internal/ackstore：幂等映射的持久实现 `broker.order_acks`（X6c，ADR 0002）。
//
// 根包本身不放实现，只用来让 `go build ./...` 与 `go list ./...` 识别本 module。
// 新增能力时按同样规则开子包，不要把所有代码堆在根包。
package broker
