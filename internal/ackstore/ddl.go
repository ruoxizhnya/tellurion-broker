// Package ackstore 是 `ClientOrderID → ack` 幂等映射的持久实现。
//
// 它只做通道适配器的写前日志：记录「这个 ClientOrderID 发过没有、柜台当时
// 回了什么」，**不是订单真相**（成交 / 持仓 / 费用不在这里，那些归 core 的
// `quant`，最终归属待裁 U2）。真相源见 docs/db-schema.md §1.1。
package ackstore

// 表名是包常量，不接受任何外部输入；SQL 里直接拼接它没有注入面。
const tableName = "broker.order_acks"

// Migrations 返回建表语句副本（调用方拿到的是拷贝，改不动本包常量）。
//
// 纪律与 core 的 migrationStatements 一致：只前滚、每条可重入（IF NOT EXISTS）、
// 无版本表；结构变了本文件与 docs/db-schema.md 必须同一次提交。
func Migrations() []string {
	out := make([]string, len(migrationStatements))
	copy(out, migrationStatements)
	return out
}

var migrationStatements = []string{
	`CREATE SCHEMA IF NOT EXISTS broker`,

	`CREATE TABLE IF NOT EXISTS broker.order_acks (
		account_id          TEXT        NOT NULL,
		client_order_id     TEXT        NOT NULL,
		request_fingerprint TEXT        NOT NULL CHECK (request_fingerprint <> ''),
		ack                 JSONB,
		reserved_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (account_id, client_order_id)
	)`,
}
