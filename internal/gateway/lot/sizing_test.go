package lot

import (
	"math"
	"testing"
)

func TestSizeBuyRoundsDownToLot(t *testing.T) {
	// 25% × 10,000 = 2,500 元；10 元/股 = 250 股；申报单位 100 → 2 手（200 股）。
	// 这条是 floor 的钉子：改成 round 会得到 300 股 / 3 手。
	got := Size(Input{TargetWeight: 0.25, Price: 10, Capital: 10000, LotSize: 100})
	if !got.OK() {
		t.Fatalf("unexpected rejection: %s (%s)", got.Reject, got.Detail)
	}
	if got.Quantity != 200 || got.Lots != 2 {
		t.Fatalf("size = %d shares / %d lots, want 200 / 2", got.Quantity, got.Lots)
	}
}

func TestSizeBuyWholeLots(t *testing.T) {
	got := Size(Input{TargetWeight: 0.5, Price: 10, Capital: 10000, LotSize: 100})
	if !got.OK() || got.Quantity != 500 || got.Lots != 5 {
		t.Fatalf("got %+v, want 500 shares / 5 lots", got)
	}
}

func TestSizeSell(t *testing.T) {
	cases := []struct {
		name  string
		in    Input
		wantQ int
		wantL int
	}{
		{
			name:  "full position",
			in:    Input{Sell: true, TargetWeight: 1, Price: 20, AvailableQuantity: 300, LotSize: 100},
			wantQ: 300, wantL: 3,
		},
		{
			name:  "half of odd position rounds down",
			in:    Input{Sell: true, TargetWeight: 0.5, Price: 20, AvailableQuantity: 350, LotSize: 100},
			wantQ: 100, wantL: 1,
		},
		{
			name:  "single-share lot (STAR/BSE style)",
			in:    Input{Sell: true, TargetWeight: 1, Price: 20, AvailableQuantity: 250, LotSize: 1},
			wantQ: 250, wantL: 250,
		},
		{
			name:  "default lot when unspecified",
			in:    Input{Sell: true, TargetWeight: 1, Price: 20, AvailableQuantity: 400},
			wantQ: 400, wantL: 4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Size(tc.in)
			if !got.OK() || got.Quantity != tc.wantQ || got.Lots != tc.wantL {
				t.Fatalf("got %+v, want %d shares / %d lots", got, tc.wantQ, tc.wantL)
			}
		})
	}
}

func TestSizeRejects(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want RejectReason
	}{
		{
			name: "zero weight yields zero lots",
			in:   Input{TargetWeight: 0, Price: 10, Capital: 10000, LotSize: 100},
			want: ReasonZeroQuantity,
		},
		{
			name: "target below one lot yields zero lots",
			in:   Input{TargetWeight: 0.01, Price: 100, Capital: 10000, LotSize: 100}, // 1 share
			want: ReasonZeroQuantity,
		},
		{
			name: "sell target exceeds available",
			in:   Input{Sell: true, TargetWeight: 1.5, Price: 20, AvailableQuantity: 1000, LotSize: 100},
			want: ReasonExceedsAvailable,
		},
		{
			// 1.09 × 1,000 = 1,090 股；floor 会落到 1,000 股（= 可用量），
			// 但"想卖超过持仓"必须在取整前就拒——否则取整成了静默缩量。
			name: "sell target just above available is rejected before lot flooring",
			in:   Input{Sell: true, TargetWeight: 1.09, Price: 20, AvailableQuantity: 1000, LotSize: 100},
			want: ReasonExceedsAvailable,
		},
		{
			name: "buy target costs more than capital",
			in:   Input{TargetWeight: 1.2, Price: 10, Capital: 10000, LotSize: 100},
			want: ReasonInsufficientCapital,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertReject(t, Size(tc.in), tc.want)
		})
	}
}

func TestSizePriceLimits(t *testing.T) {
	cases := []struct {
		name   string
		in     Input
		want   RejectReason
		wantOK bool
	}{
		{
			name: "buy above limit up",
			in:   Input{TargetWeight: 1, Price: 10.51, Capital: 100000, LotSize: 100, LimitUp: 10.5, LimitDown: 9.5},
			want: ReasonPriceAboveLimitUp,
		},
		{
			name: "buy below limit down",
			in:   Input{TargetWeight: 1, Price: 9.49, Capital: 100000, LotSize: 100, LimitUp: 10.5, LimitDown: 9.5},
			want: ReasonPriceBelowLimitDown,
		},
		{
			name: "sell above limit up",
			in:   Input{Sell: true, TargetWeight: 1, Price: 10.51, AvailableQuantity: 1000, LotSize: 100, LimitUp: 10.5, LimitDown: 9.5},
			want: ReasonPriceAboveLimitUp,
		},
		{
			name: "sell below limit down",
			in:   Input{Sell: true, TargetWeight: 1, Price: 9.49, AvailableQuantity: 1000, LotSize: 100, LimitUp: 10.5, LimitDown: 9.5},
			want: ReasonPriceBelowLimitDown,
		},
		{
			// 涨停买入（打板）是合法可申报委托：闭区间校验，只有越界才拒。
			name:   "buy at limit up is allowed",
			in:     Input{TargetWeight: 1, Price: 10.5, Capital: 100000, LotSize: 100, LimitUp: 10.5, LimitDown: 9.5},
			wantOK: true,
		},
		{
			name:   "sell at limit down is allowed",
			in:     Input{Sell: true, TargetWeight: 1, Price: 9.5, AvailableQuantity: 1000, LotSize: 100, LimitUp: 10.5, LimitDown: 9.5},
			wantOK: true,
		},
		{
			name:   "limits not supplied skips the band check",
			in:     Input{TargetWeight: 0.5, Price: 999, Capital: 1_000_000, LotSize: 100},
			wantOK: true,
		},
		{
			name: "inverted band",
			in:   Input{TargetWeight: 1, Price: 10, Capital: 100000, LotSize: 100, LimitUp: 9, LimitDown: 10},
			want: ReasonInvalidInput,
		},
		{
			name: "negative limit",
			in:   Input{TargetWeight: 1, Price: 10, Capital: 100000, LotSize: 100, LimitUp: -1},
			want: ReasonInvalidInput,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Size(tc.in)
			if tc.wantOK {
				if !got.OK() {
					t.Fatalf("unexpected rejection: %s (%s)", got.Reject, got.Detail)
				}
				return
			}
			assertReject(t, got, tc.want)
		})
	}
}

func TestSizeRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		in   Input
	}{
		{"negative weight", Input{TargetWeight: -0.1, Price: 10, Capital: 10000}},
		{"NaN weight", Input{TargetWeight: math.NaN(), Price: 10, Capital: 10000}},
		{"zero price", Input{TargetWeight: 1, Price: 0, Capital: 10000}},
		{"infinite price", Input{TargetWeight: 1, Price: math.Inf(1), Capital: 10000}},
		{"negative capital", Input{TargetWeight: 1, Price: 10, Capital: -1}},
		{"NaN capital", Input{TargetWeight: 1, Price: 10, Capital: math.NaN()}},
		{"negative available", Input{Sell: true, TargetWeight: 1, Price: 10, AvailableQuantity: -1}},
		{"negative lot size", Input{TargetWeight: 1, Price: 10, Capital: 10000, LotSize: -100}},
		{"overflowing target", Input{TargetWeight: 1e300, Price: 1e-300, Capital: 1e300}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertReject(t, Size(tc.in), ReasonInvalidInput)
		})
	}
}

func TestValidateQuantity(t *testing.T) {
	cases := []struct {
		name     string
		quantity int
		lotSize  int
		wantQ    int
		wantL    int
		want     RejectReason
	}{
		{"one lot", 100, 100, 100, 1, ""},
		{"whole lots", 300, 100, 300, 3, ""},
		{"default lot", 200, 0, 200, 2, ""},
		{"single-share lot", 250, 1, 250, 250, ""},
		{"not a multiple", 150, 100, 0, 0, ReasonNotLotMultiple},
		{"zero", 0, 100, 0, 0, ReasonZeroQuantity},
		{"negative", -100, 100, 0, 0, ReasonInvalidInput},
		{"negative lot size", 100, -100, 0, 0, ReasonInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateQuantity(tc.quantity, tc.lotSize)
			if got.Reject != tc.want {
				t.Fatalf("reject = %q (%s), want %q", got.Reject, got.Detail, tc.want)
			}
			if tc.want == "" && (got.Quantity != tc.wantQ || got.Lots != tc.wantL) {
				t.Fatalf("got %d shares / %d lots, want %d / %d", got.Quantity, got.Lots, tc.wantQ, tc.wantL)
			}
			if tc.want != "" && (got.Quantity != 0 || got.Lots != 0) {
				t.Fatalf("rejected result must not carry a quantity: %+v", got)
			}
		})
	}
}

func assertReject(t *testing.T, got Result, want RejectReason) {
	t.Helper()
	if got.Reject != want {
		t.Fatalf("reject = %q (%s), want %q", got.Reject, got.Detail, want)
	}
	if got.Detail == "" {
		t.Fatal("rejection must carry a detail message")
	}
	if got.Quantity != 0 || got.Lots != 0 {
		t.Fatalf("rejected result must not carry a quantity: %+v", got)
	}
}
