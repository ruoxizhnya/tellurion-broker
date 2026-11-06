package ackstore

import (
	"regexp"
	"strings"
	"testing"
)

// qualifiedName 抓 SQL 里的 `schema.object` 限定名。
var qualifiedName = regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\.[a-z_]`)

// TestSQLStatementsOnlyTouchBrokerSchema 是本仓的跨 schema 写护栏（G2 验收 ③）：
// 本仓的 DDL/DML 只允许出现 `broker.` 限定名，任何 `quant.` / `risk.` / … 都是
// 越界写别人的表。破坏验证：往任一语句里加一句 `UPDATE quant.orders ...` ⇒ 红。
func TestSQLStatementsOnlyTouchBrokerSchema(t *testing.T) {
	stmts := append(Migrations(), cleanupAckedStatement)
	for _, stmt := range stmts {
		for _, m := range qualifiedName.FindAllStringSubmatch(stmt, -1) {
			if m[1] != "broker" {
				t.Fatalf("statement touches schema %q, want only %q:\n%s", m[1], "broker", stmt)
			}
		}
	}
}

// TestCleanupStatementKeepsUnackedRows 钉住护栏本身：清理语句必须带
// `ack IS NOT NULL` 谓词（去掉它 = 连「可能已发出」的证据一起删）。
func TestCleanupStatementKeepsUnackedRows(t *testing.T) {
	if !strings.Contains(cleanupAckedStatement, "ack IS NOT NULL") {
		t.Fatalf("cleanup statement must only delete acknowledged rows:\n%s", cleanupAckedStatement)
	}
}

// TestMigrationsIncludePartialIndex 钉住裁决 ⑨ 的部分索引必须走迁移这一条路径。
func TestMigrationsIncludePartialIndex(t *testing.T) {
	joined := strings.Join(Migrations(), "\n")
	if !strings.Contains(joined, "order_acks_updated_at_acked_idx") {
		t.Fatal("migrations must create order_acks_updated_at_acked_idx")
	}
	if !strings.Contains(joined, "WHERE ack IS NOT NULL") {
		t.Fatal("the retention index must be partial (WHERE ack IS NOT NULL)")
	}
}
