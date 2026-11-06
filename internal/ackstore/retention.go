package ackstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultRetention 是 `broker.order_acks` 中**已回执**行（`ack IS NOT NULL`）的保留期。
//
// **90 天是暂定假设**（第三轮裁决 ⑨）：真正的判据是「柜台侧可查窗口」，那个事实今天
// 未知。一旦已知即按同一判据重算本常量（见 docs/db-schema.md §5 的复核条件）。
const DefaultRetention = 90 * 24 * time.Hour

// MaxCleanupBatch 是单批删除上限（裁决 ⑨：单批 ≤ 5000 行）。批间是独立语句，
// 每批结束即释放行锁，所以调用方可以安全地分批续删。
const MaxCleanupBatch = 5000

// cleanupAckedStatement 只删「已回执且超过保留期」的行。
//
// **`ack IS NOT NULL` 是护栏，不是优化**：`ack IS NULL` 表示已占位但结果未知，
// 它是「这一单可能已经在柜台上」的唯一证据；删它 = 允许同一个 ClientOrderID
// 再发一笔 = 柜台两笔。与 `api-schema.md` §2 的 `unknown_state` 同一口径。
const cleanupAckedStatement = `
DELETE FROM broker.order_acks
WHERE (account_id, client_order_id) IN (
	SELECT account_id, client_order_id
	FROM broker.order_acks
	WHERE ack IS NOT NULL AND updated_at < $1
	ORDER BY updated_at
	LIMIT $2
)`

// CleanupAckedBatch 删除一批（≤ MaxCleanupBatch）已回执且早于 olderThan 的行，
// 返回本批删除行数。`limit <= 0` 或超过上限时按 MaxCleanupBatch 执行。
func CleanupAckedBatch(ctx context.Context, pool *pgxpool.Pool, olderThan time.Time, limit int) (int64, error) {
	if pool == nil {
		return 0, fmt.Errorf("ack store: cleanup needs a connection pool")
	}
	if limit <= 0 || limit > MaxCleanupBatch {
		limit = MaxCleanupBatch
	}
	tag, err := pool.Exec(ctx, cleanupAckedStatement, olderThan.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("ack store: cleanup acked rows: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CleanupAcked 按 retention 分批清理已回执行，直到删空或某批不足 MaxCleanupBatch。
// 返回累计删除行数。
func CleanupAcked(ctx context.Context, pool *pgxpool.Pool, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, fmt.Errorf("ack store: retention must be positive, got %v", retention)
	}
	olderThan := time.Now().UTC().Add(-retention)
	var total int64
	for {
		n, err := CleanupAckedBatch(ctx, pool, olderThan, MaxCleanupBatch)
		if err != nil {
			return total, err
		}
		total += n
		if n < MaxCleanupBatch {
			return total, nil
		}
	}
}
