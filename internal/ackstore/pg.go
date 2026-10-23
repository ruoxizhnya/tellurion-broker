package ackstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tellurion-broker/internal/gateway"
)

// PgStore 是 gateway.AckStore 的持久实现（docs/db-schema.md §1.1）。
//
// 并发同 `(AccountID, ClientOrderID)` 的发送权由一条 `INSERT ... ON CONFLICT
// DO NOTHING RETURNING` 判定：只有插入成功的那次调用可以发单。
type PgStore struct {
	pool *pgxpool.Pool
}

var _ gateway.AckStore = (*PgStore)(nil)

// NewPgStore 包装调用方持有的连接池（连接生命周期不归本包）。
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Durable 见 gateway.AckStore：PG 提交即持久。
func (s *PgStore) Durable() bool { return true }

// Load 见 gateway.AckStore。
func (s *PgStore) Load(ctx context.Context, accountID, clientOrderID string) (gateway.IdempotencyRecord, bool, error) {
	var fingerprint string
	var ackJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT request_fingerprint, ack
		FROM `+tableName+`
		WHERE account_id = $1 AND client_order_id = $2`,
		accountID, clientOrderID).Scan(&fingerprint, &ackJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return gateway.IdempotencyRecord{}, false, err
	}

	rec := gateway.IdempotencyRecord{RequestFingerprint: fingerprint}
	if len(ackJSON) > 0 {
		if err := json.Unmarshal(ackJSON, &rec.Ack); err != nil {
			return gateway.IdempotencyRecord{}, false, fmt.Errorf(
				"ack store: decode ack for account %q client order %q: %w", accountID, clientOrderID, err)
		}
	}
	return rec, true, nil
}

// Reserve 见 gateway.AckStore：`ON CONFLICT DO NOTHING` 是唯一的发送权判定。
//
// 冲突时 PG 会等对方事务结束再返回 0 行；随后用**下一条语句的新快照**回读
// （依赖默认的 READ COMMITTED）。把回读并进同一条 SQL 会在语句级快照下
// 看不到刚提交的行，所以刻意分成两步。
func (s *PgStore) Reserve(ctx context.Context, accountID, clientOrderID, fingerprint string) (gateway.IdempotencyRecord, bool, error) {
	var reservedAt time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO `+tableName+` (account_id, client_order_id, request_fingerprint)
		VALUES ($1, $2, $3)
		ON CONFLICT (account_id, client_order_id) DO NOTHING
		RETURNING reserved_at`,
		accountID, clientOrderID, fingerprint).Scan(&reservedAt)
	if err == nil {
		return gateway.IdempotencyRecord{
			RequestFingerprint: fingerprint,
			Ack:                gateway.OrderAck{ClientOrderID: clientOrderID},
		}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return gateway.IdempotencyRecord{}, false, err
	}

	rec, found, err := s.Load(ctx, accountID, clientOrderID)
	if err != nil {
		return gateway.IdempotencyRecord{}, false, err
	}
	if !found {
		return gateway.IdempotencyRecord{}, false, errors.New("ack store: reservation is not visible after conflict")
	}
	return rec, false, nil
}

// SaveAck 见 gateway.AckStore：只更新已占位的行；没有占位就是错误。
func (s *PgStore) SaveAck(ctx context.Context, accountID, clientOrderID string, rec gateway.IdempotencyRecord) error {
	ackJSON, err := json.Marshal(rec.Ack)
	if err != nil {
		return fmt.Errorf("ack store: encode ack: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE `+tableName+`
		SET ack = $3, updated_at = now()
		WHERE account_id = $1 AND client_order_id = $2`,
		accountID, clientOrderID, string(ackJSON))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("ack store: no reservation for account %q client order %q", accountID, clientOrderID)
	}
	return nil
}

// Migrate 按序执行本包的 DDL（幂等、无版本表）。装配方在启动时调用一次。
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	for i, stmt := range migrationStatements {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("ack store: migration statement %d failed: %w", i+1, err)
		}
	}
	return nil
}
