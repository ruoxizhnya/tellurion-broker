package reconcile

import (
	"reflect"
	"testing"
	"time"

	"tellurion-broker/internal/gateway"
)

var at = time.Date(2026, 10, 23, 16, 0, 0, 0, time.UTC)

func cfg() Config {
	c := DefaultConfig()
	c.Now = func() time.Time { return at }
	return c
}

func snapshot(account string, positions ...gateway.Position) *gateway.AccountSnapshot {
	return &gateway.AccountSnapshot{
		AccountID:   account,
		AsOf:        at,
		Cash:        gateway.Cash{Total: 100000, Available: 90000},
		TotalAssets: 150000,
		Positions:   positions,
		Fees:        gateway.Fees{Commission: 50, StampTax: 30, TransferFee: 2},
	}
}

func pos(symbol string, qty int, mv float64) gateway.Position {
	return gateway.Position{AccountID: "A1", Symbol: symbol, Quantity: qty, MarketValue: mv}
}

func kinds(list []Discrepancy) []Kind {
	out := make([]Kind, 0, len(list))
	for _, d := range list {
		out = append(out, d.Kind)
	}
	return out
}

func TestReconcile_ZeroDifferenceReportsNothing(t *testing.T) {
	local := snapshot("A1", pos("600000.SH", 100, 12000), pos("000001.SZ", 200, 3000))
	got, err := Reconcile(local, snapshot("A1", pos("000001.SZ", 200, 3000), pos("600000.SH", 100, 12000)), cfg())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// 零差异返回空切片（不是 nil），且两侧顺序无关。
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil, got %v", got)
	}
}

func TestReconcile_InjectedDriftFiresEachKind(t *testing.T) {
	base := func() (*gateway.AccountSnapshot, *gateway.AccountSnapshot) {
		return snapshot("A1", pos("600000.SH", 100, 12000)),
			snapshot("A1", pos("600000.SH", 100, 12000))
	}
	cases := []struct {
		name   string
		mutate func(local, broker *gateway.AccountSnapshot)
		want   Kind
	}{
		{
			name:   "cash drift",
			mutate: func(local, broker *gateway.AccountSnapshot) { broker.Cash.Total -= 100 },
			want:   KindCash,
		},
		{
			name: "missing local (疑似漏单)",
			mutate: func(local, broker *gateway.AccountSnapshot) {
				broker.Positions = append(broker.Positions, pos("000001.SZ", 200, 3000))
			},
			want: KindMissingLocal,
		},
		{
			name: "missing broker (疑似鬼单)",
			mutate: func(local, broker *gateway.AccountSnapshot) {
				local.Positions = append(local.Positions, pos("000001.SZ", 200, 3000))
			},
			want: KindMissingBroker,
		},
		{
			name:   "quantity mismatch",
			mutate: func(local, broker *gateway.AccountSnapshot) { broker.Positions[0].Quantity = 99 },
			want:   KindQuantity,
		},
		{
			name:   "market value drift",
			mutate: func(local, broker *gateway.AccountSnapshot) { broker.Positions[0].MarketValue *= 1.1 },
			want:   KindMarketValue,
		},
		{
			name:   "commission drift",
			mutate: func(local, broker *gateway.AccountSnapshot) { broker.Fees.Commission += 5 },
			want:   KindFee,
		},
		{
			name:   "stamp tax drift",
			mutate: func(local, broker *gateway.AccountSnapshot) { broker.Fees.StampTax += 5 },
			want:   KindFee,
		},
		{
			name:   "transfer fee drift",
			mutate: func(local, broker *gateway.AccountSnapshot) { broker.Fees.TransferFee += 5 },
			want:   KindFee,
		},
		{
			name:   "as-of drift",
			mutate: func(local, broker *gateway.AccountSnapshot) { broker.AsOf = broker.AsOf.Add(-5 * time.Minute) },
			want:   KindAsOfDrift,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local, broker := base()
			tc.mutate(local, broker)
			got, err := Reconcile(local, broker, cfg())
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if !reflect.DeepEqual(kinds(got), []Kind{tc.want}) {
				t.Fatalf("kinds = %v, want [%s]", kinds(got), tc.want)
			}
		})
	}
}

func TestReconcile_SeverityMapping(t *testing.T) {
	local := snapshot("A1", pos("600000.SH", 100, 12000))
	local.Cash.Total -= 500 // 资金偏差 → warning
	broker := snapshot("A1", pos("600000.SH", 100, 12000), pos("000001.SZ", 200, 3000))
	broker.AsOf = broker.AsOf.Add(-2 * time.Minute) // 时间漂移 → info
	got, err := Reconcile(local, broker, cfg())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	byKind := map[Kind]Severity{}
	for _, d := range got {
		byKind[d.Kind] = d.Severity
	}
	if byKind[KindMissingLocal] != SeverityCritical {
		t.Fatalf("missing_local = %v, want critical", byKind[KindMissingLocal])
	}
	if byKind[KindCash] != SeverityWarning {
		t.Fatalf("cash = %v, want warning", byKind[KindCash])
	}
	if byKind[KindAsOfDrift] != SeverityInfo {
		t.Fatalf("as_of_drift = %v, want info", byKind[KindAsOfDrift])
	}
}

func TestReconcile_ToleranceSuppressesNoise(t *testing.T) {
	local := snapshot("A1", pos("600000.SH", 100, 12000))
	broker := snapshot("A1", pos("600000.SH", 100, 12000))
	broker.Cash.Total += 0.005           // < CashTolerance 0.01
	broker.Positions[0].MarketValue += 3 // 0.025% < MarketValueTolerance 0.5%
	broker.Fees.Commission += 0.005      // < FeeTolerance 0.01
	got, err := Reconcile(local, broker, cfg())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("within-tolerance drift must not fire, got %v", got)
	}
}

func TestReconcile_FailsLoudOnBadInput(t *testing.T) {
	good := snapshot("A1", pos("600000.SH", 100, 12000))
	if _, err := Reconcile(nil, good, cfg()); err == nil {
		t.Fatal("nil local must fail")
	}
	if _, err := Reconcile(good, nil, cfg()); err == nil {
		t.Fatal("nil broker must fail")
	}
	other := snapshot("B2")
	other.Positions = nil
	if _, err := Reconcile(good, other, cfg()); err == nil {
		t.Fatal("account mismatch must fail")
	}
	dup := snapshot("A1", pos("600000.SH", 100, 12000), pos("600000.SH", 50, 6000))
	if _, err := Reconcile(dup, dup, cfg()); err == nil {
		t.Fatal("duplicate symbol must fail")
	}
}

func TestReconcile_IsPureAndDeterministic(t *testing.T) {
	local := snapshot("A1", pos("600000.SH", 100, 12000))
	broker := snapshot("A1", pos("600000.SH", 99, 12000))
	broker.AsOf = broker.AsOf.Add(-5 * time.Minute)
	first, err := Reconcile(local, broker, cfg())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	second, err := Reconcile(local, broker, cfg())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same input produced different output:\n%+v\n%+v", first, second)
	}
}
