package ackstore

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tellurion-broker/internal/gateway"
)

// PG 集成测试只在显式给出 DSN 时运行（CI / 本机没库就该 skip，而不是假绿）。
// 例：BROKER_TEST_DSN='postgres://postgres:postgres@127.0.0.1:5432/tellurion_broker_test?sslmode=disable' go test ./internal/ackstore -count=1
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("BROKER_TEST_DSN")
	if dsn == "" {
		t.Skip("BROKER_TEST_DSN is not set; skipping PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func testAccount(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
}

func cleanupAccount(t *testing.T, pool *pgxpool.Pool, accountID string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM broker.order_acks WHERE account_id = $1`, accountID); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
}

func TestPgStore_MigrateIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestPgStore_ReserveIsAtomic(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	account := testAccount(t)
	cleanupAccount(t, pool, account)
	store := NewPgStore(pool)

	const n = 16
	start := make(chan struct{})
	created := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, ok, err := store.Reserve(ctx, account, "same-id", "fp")
			created[i], errs[i] = ok, err
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, ok := range created {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("Reserve winners = %d, want exactly 1", winners)
	}
}

func TestPgStore_AckSurvivesAnotherStoreInstance(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	account := testAccount(t)
	cleanupAccount(t, pool, account)

	store := NewPgStore(pool)
	if _, created, err := store.Reserve(ctx, account, "c-1", "fp"); err != nil || !created {
		t.Fatalf("Reserve = (created=%v, err=%v)", created, err)
	}
	ack := gateway.OrderAck{
		ClientOrderID: "c-1",
		BrokerOrderID: "XTP-1",
		Status:        gateway.OrderFilled,
		AcceptedAt:    time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := store.SaveAck(ctx, account, "c-1", gateway.IdempotencyRecord{
		RequestFingerprint: "fp",
		Ack:                ack,
	}); err != nil {
		t.Fatalf("SaveAck: %v", err)
	}

	// 另开一个连接池 = 模拟进程重启后只靠 PG 恢复。
	second, err := pgxpool.New(ctx, os.Getenv("BROKER_TEST_DSN"))
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	defer second.Close()

	rec, found, err := NewPgStore(second).Load(ctx, account, "c-1")
	if err != nil || !found {
		t.Fatalf("Load = (found=%v, err=%v)", found, err)
	}
	if rec.RequestFingerprint != "fp" || rec.Ack.Status != gateway.OrderFilled || rec.Ack.BrokerOrderID != "XTP-1" {
		t.Fatalf("restored record = %+v", rec)
	}
}

func TestPgStore_SaveAckRequiresReservation(t *testing.T) {
	pool := testPool(t)
	account := testAccount(t)
	cleanupAccount(t, pool, account)

	if err := NewPgStore(pool).SaveAck(context.Background(), account, "unreserved", gateway.IdempotencyRecord{
		Ack: gateway.OrderAck{Status: gateway.OrderFilled},
	}); err == nil {
		t.Fatal("SaveAck without a reservation must fail")
	}
}

func TestPgStore_ReservationWithoutAckStaysUnknown(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	account := testAccount(t)
	cleanupAccount(t, pool, account)
	store := NewPgStore(pool)

	if _, created, err := store.Reserve(ctx, account, "c-1", "fp"); err != nil || !created {
		t.Fatalf("Reserve = (created=%v, err=%v)", created, err)
	}
	rec, found, err := store.Load(ctx, account, "c-1")
	if err != nil || !found {
		t.Fatalf("Load = (found=%v, err=%v)", found, err)
	}
	if rec.Ack.Status != "" {
		t.Fatalf("placeholder must have an empty status, got %q", rec.Ack.Status)
	}
}

func TestPgStore_ConflictReturnsOriginalFingerprint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	account := testAccount(t)
	cleanupAccount(t, pool, account)
	store := NewPgStore(pool)

	if _, created, err := store.Reserve(ctx, account, "c-1", "fp-original"); err != nil || !created {
		t.Fatalf("first Reserve = (created=%v, err=%v)", created, err)
	}
	rec, created, err := store.Reserve(ctx, account, "c-1", "fp-different")
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if created {
		t.Fatal("second Reserve must not win the send right")
	}
	if rec.RequestFingerprint != "fp-original" {
		t.Fatalf("fingerprint = %q, want the original one", rec.RequestFingerprint)
	}
}
