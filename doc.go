// Package broker 是 tellurion-broker 的根包。
//
// 本仓按能力拆子包，目前只有：
//   - internal/gateway：BrokerGateway / SettlementQuerier / KillSwitch / HealthChecker 四个 port、
//     DTO、契约哨兵与内存 Mock 实现（X6a）。
//
// 根包本身不放实现，只用来让 `go build ./...` 与 `go list ./...` 识别本 module。
// 新增能力时按同样规则开子包，不要把所有代码堆在根包。
package broker
