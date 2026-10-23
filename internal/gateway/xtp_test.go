package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// durableStore 让内存映射在测试里冒充持久实现（生产实现是 ackstore.PgStore）。
type durableStore struct{ *MemoryAckStore }

func (durableStore) Durable() bool { return true }

func xtpDeps() XTPDeps {
	m := NewMock(MockOptions{})
	return XTPDeps{Acks: durableStore{NewMemoryAckStore()}, Settlement: m, Health: m}
}

func TestNewXTPTrader_GateRejectsIncompleteAssembly(t *testing.T) {
	cases := []struct {
		name string
		deps XTPDeps
		want string
	}{
		{"missing acks", XTPDeps{Settlement: NewMock(MockOptions{}), Health: NewMock(MockOptions{})}, "ack store"},
		{"non-durable acks", XTPDeps{Acks: NewMemoryAckStore(), Settlement: NewMock(MockOptions{}), Health: NewMock(MockOptions{})}, "durable"},
		{"missing settlement", XTPDeps{Acks: durableStore{NewMemoryAckStore()}, Health: NewMock(MockOptions{})}, "settlement"},
		{"missing health", XTPDeps{Acks: durableStore{NewMemoryAckStore()}, Settlement: NewMock(MockOptions{})}, "health"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x, err := NewXTPTrader(XTPConfig{AccountID: "A1", Host: "xtp.example.com", Port: 6002}, tc.deps)
			if !errors.Is(err, ErrAssemblyIncomplete) {
				t.Fatalf("err = %v, want ErrAssemblyIncomplete", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name the missing dep %q", err, tc.want)
			}
			if x != nil {
				t.Fatal("rejected assembly must not yield an instance")
			}
		})
	}
}

func TestNewXTPTrader_GateRejectsBadConfig(t *testing.T) {
	cases := []XTPConfig{
		{Host: "xtp.example.com", Port: 6002},
		{AccountID: "A1", Port: 6002},
		{AccountID: "A1", Host: "xtp.example.com"},
		{AccountID: "A1", Host: "xtp.example.com", Port: 70000},
	}
	for _, cfg := range cases {
		if _, err := NewXTPTrader(cfg, xtpDeps()); !errors.Is(err, ErrAssemblyIncomplete) {
			t.Fatalf("config %+v: err = %v, want ErrAssemblyIncomplete", cfg, err)
		}
	}
}

func TestNewXTPTrader_CompleteAssemblySucceeds(t *testing.T) {
	x, err := NewXTPTrader(XTPConfig{AccountID: "A1", Host: "xtp.example.com", Port: 6002}, xtpDeps())
	if err != nil {
		t.Fatalf("valid assembly rejected: %v", err)
	}
	if x.AccountID() != "A1" {
		t.Fatalf("account = %q", x.AccountID())
	}
}

// 骨架的统一语义：没有 SDK link = 通道不可用。任何调用都拿不到成功形状
// （F4 的教训：伪成功比失败更危险），也不产生副作用。
func TestXTPTrader_AllCallsFailClosedWithChannelDown(t *testing.T) {
	x, err := NewXTPTrader(XTPConfig{AccountID: "A1", Host: "xtp.example.com", Port: 6002}, xtpDeps())
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	ctx := context.Background()

	if _, err := x.PlaceOrder(ctx, OrderRequest{ClientOrderID: "c1", AccountID: "A1", Symbol: "600000.SH", Side: SideBuy, OrderType: OrderLimit, Quantity: 100, LimitPrice: 10}); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("PlaceOrder err = %v, want ErrChannelDown", err)
	}
	if _, err := x.CancelOrder(ctx, CancelRequest{ClientOrderID: "c1"}); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("CancelOrder err = %v, want ErrChannelDown", err)
	}
	if _, err := x.QueryOrder(ctx, "c1"); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("QueryOrder err = %v, want ErrChannelDown", err)
	}
	if _, err := x.QueryPositions(ctx, "A1"); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("QueryPositions err = %v, want ErrChannelDown", err)
	}
	if _, err := x.QueryAccount(ctx, "A1"); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("QueryAccount err = %v, want ErrChannelDown", err)
	}
	if _, err := x.QueryFills(ctx, "A1", time.Time{}); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("QueryFills err = %v, want ErrChannelDown", err)
	}
	if _, err := x.QuerySettlement(ctx, "A1", time.Time{}); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("QuerySettlement err = %v, want ErrChannelDown", err)
	}
	// kill-switch 骨架绝不能返回「已清仓」形状。
	if res, err := x.EmergencyFlatten(ctx, "drill"); res != nil || !errors.Is(err, ErrChannelDown) {
		t.Fatalf("EmergencyFlatten = (%v, %v), want (nil, ErrChannelDown)", res, err)
	}
	if err := x.HealthCheck(ctx); !errors.Is(err, ErrChannelDown) {
		t.Fatalf("HealthCheck err = %v, want ErrChannelDown", err)
	}
}

func TestXTPTrader_AccountMismatchIsConfigErrorNotChannelState(t *testing.T) {
	x, err := NewXTPTrader(XTPConfig{AccountID: "A1", Host: "xtp.example.com", Port: 6002}, xtpDeps())
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	ctx := context.Background()
	// 账户不匹配是离线可判的配置错误，优先于「通道不可用」报出。
	if _, err := x.QueryPositions(ctx, "B2"); !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("QueryPositions err = %v, want ErrAccountMismatch", err)
	}
	if _, err := x.QuerySettlement(ctx, "B2", time.Time{}); !errors.Is(err, ErrAccountMismatch) {
		t.Fatalf("QuerySettlement err = %v, want ErrAccountMismatch", err)
	}
}
