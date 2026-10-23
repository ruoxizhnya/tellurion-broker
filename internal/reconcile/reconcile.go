// Package reconcile 是日终对账的纯函数核心（X6b，docs/ARCHITECTURE.md §9.1 F6）。
//
// 对账口径是**账户快照**（现金 / 持仓 / 费用），不是逐笔成交——`QueryFills`
// 只用于成交/费用审计，不参与对账判定。两侧共用 `gateway.AccountSnapshot`
// 同一形状（§9.3：避免两套快照漂移）；本地侧由 core 组装后传入。
//
// 本包零 I/O、零跨调用状态：Reconcile 不读时钟（DetectedAt 取注入的 Now）、
// 不写任何表——差异报告的消费方是 risk 的对账留痕（X3），落表不归本仓。
package reconcile

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"tellurion-broker/internal/gateway"
)

// Kind 是差异类别。
type Kind string

const (
	// KindCash 是资金余额（Total）超容差。
	KindCash Kind = "cash"
	// KindMissingLocal 是券商有持仓、本地缺失（疑似漏单）。
	KindMissingLocal Kind = "missing_local"
	// KindMissingBroker 是本地有持仓、券商缺失（疑似鬼单）。
	KindMissingBroker Kind = "missing_broker"
	// KindQuantity 是持仓数量偏差超容差（整股口径，出现偏差即疑似丢单/重复成交）。
	KindQuantity Kind = "quantity"
	// KindMarketValue 是持仓市值相对偏差超容差（主要覆盖行情刷新延迟）。
	KindMarketValue Kind = "market_value"
	// KindFee 是费用分桶超容差。
	KindFee Kind = "fee"
	// KindAsOfDrift 是两侧快照时间戳漂移超容差（不判差异，只提示）。
	KindAsOfDrift Kind = "as_of_drift"
)

// Severity 是差异等级。对账报告落 risk 后的处置 SLA 按它分档。
type Severity string

const (
	SeverityCritical Severity = "critical" // 疑似丢单 / 鬼单：立即人工核查
	SeverityWarning  Severity = "warning"  // 数值超容差：当日预警
	SeverityInfo     Severity = "info"     // 不构成差异，只留痕
)

// Discrepancy 是一条差异。Local / Broker / Delta / Tolerance 统一用 float64
// 承载（数量是整股，进来也是精确值），Note 是给人看的定性说明。
type Discrepancy struct {
	Kind       Kind      `json:"kind"`
	Severity   Severity  `json:"severity"`
	Symbol     string    `json:"symbol,omitempty"`
	Field      string    `json:"field,omitempty"`
	Local      float64   `json:"local"`
	Broker     float64   `json:"broker"`
	Delta      float64   `json:"delta"`
	Tolerance  float64   `json:"tolerance"`
	Note       string    `json:"note"`
	DetectedAt time.Time `json:"detected_at"`
}

// Config 是对账容差与注入时钟。零值 = 全部零容差（严格模式，任何非零偏差都报）；
// 监管口径的默认值用 DefaultConfig。
type Config struct {
	// CashTolerance 是资金余额绝对偏差容忍度（默认 0.01 元 = 1 分）。
	CashTolerance float64
	// QuantityTolerance 是持仓数量容忍度（整股；默认 0——账务应严格相等）。
	QuantityTolerance int
	// MarketValueTolerance 是持仓市值**相对**偏差容忍度（默认 0.005 = 0.5%）。
	MarketValueTolerance float64
	// FeeTolerance 是费用各分桶绝对偏差容忍度（默认 0.01 元）。
	FeeTolerance float64
	// AsOfDriftTolerance 是两侧快照时间戳漂移容忍度（零值按 60s 处理——
	// 快照时间几乎不可能逐秒相等，严格零容差会让该检查常驻报噪声）。
	AsOfDriftTolerance time.Duration
	// Now 返回 DetectedAt（nil → time.Now）。
	Now func() time.Time
}

// DefaultConfig 返回监管建议的默认容差
// （口径沿用 core `pkg/live/reconciliation` 的生产配置，迁出时不变）。
func DefaultConfig() Config {
	return Config{
		CashTolerance:        0.01,
		QuantityTolerance:    0,
		MarketValueTolerance: 0.005,
		FeeTolerance:         0.01,
		AsOfDriftTolerance:   60 * time.Second,
	}
}

// Reconcile 对比本地与券商的账户快照，返回全部差异。
//
// 纯函数：不碰 I/O、不读时钟（DetectedAt 取 cfg.Now）、可并发调用。
// 输出顺序固定：cash → 持仓（symbol 字典序）→ 费用（commission / stamp_tax /
// transfer_fee）→ as_of 漂移；零差异返回空切片（不是 nil，JSON 稳定）。
//
// fail-loud：两侧 nil、账户不一致、同一侧出现重复 symbol 都是调用方装配错误，
// 返回 error 而不是静默跳过——对账跑在错误的输入上等于没跑。
func Reconcile(local, broker *gateway.AccountSnapshot, cfg Config) ([]Discrepancy, error) {
	if local == nil || broker == nil {
		return nil, errors.New("reconcile: local and broker snapshots are required")
	}
	if local.AccountID != broker.AccountID {
		return nil, fmt.Errorf("reconcile: account mismatch: local %q vs broker %q", local.AccountID, broker.AccountID)
	}
	localPos, err := indexPositions(local)
	if err != nil {
		return nil, err
	}
	brokerPos, err := indexPositions(broker)
	if err != nil {
		return nil, err
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	out := make([]Discrepancy, 0, 8)

	// 1) 资金余额（只对 Total；Available 的口径两侧随时可变，不进判定）。
	cashDelta := local.Cash.Total - broker.Cash.Total
	if abs(cashDelta) > cfg.CashTolerance {
		out = append(out, Discrepancy{
			Kind: KindCash, Severity: SeverityWarning, Field: "cash.total",
			Local: local.Cash.Total, Broker: broker.Cash.Total,
			Delta: cashDelta, Tolerance: cfg.CashTolerance,
			Note: "资金余额偏差超容差", DetectedAt: at,
		})
	}

	// 2) 持仓：两侧 symbol 的并集，字典序逐个比。
	symbols := make([]string, 0, len(localPos)+len(brokerPos))
	for sym := range localPos {
		symbols = append(symbols, sym)
	}
	for sym := range brokerPos {
		if _, ok := localPos[sym]; !ok {
			symbols = append(symbols, sym)
		}
	}
	sort.Strings(symbols)
	for _, sym := range symbols {
		lp, lok := localPos[sym]
		bp, bok := brokerPos[sym]
		switch {
		case !lok:
			out = append(out, Discrepancy{
				Kind: KindMissingLocal, Severity: SeverityCritical, Symbol: sym,
				Broker: float64(bp.Quantity), Delta: -float64(bp.Quantity),
				Tolerance: float64(cfg.QuantityTolerance),
				Note:      "券商有持仓，本地缺失（疑似漏单）", DetectedAt: at,
			})
			continue
		case !bok:
			out = append(out, Discrepancy{
				Kind: KindMissingBroker, Severity: SeverityCritical, Symbol: sym,
				Local: float64(lp.Quantity), Delta: float64(lp.Quantity),
				Tolerance: float64(cfg.QuantityTolerance),
				Note:      "本地有持仓，券商缺失（疑似鬼单）", DetectedAt: at,
			})
			continue
		}
		if d := lp.Quantity - bp.Quantity; d != 0 {
			sev := SeverityCritical
			note := "持仓数量偏差超容差"
			if absInt(d) <= cfg.QuantityTolerance {
				sev = SeverityInfo
				note = "持仓数量在容差内，但不严格相等"
			}
			out = append(out, Discrepancy{
				Kind: KindQuantity, Severity: sev, Symbol: sym, Field: "quantity",
				Local: float64(lp.Quantity), Broker: float64(bp.Quantity),
				Delta: float64(d), Tolerance: float64(cfg.QuantityTolerance),
				Note: note, DetectedAt: at,
			})
		}
		// 市值只在数量一致（含容差内）时比——数量都对不上时市值差没有意义。
		if absInt(lp.Quantity-bp.Quantity) <= cfg.QuantityTolerance {
			mvDelta := lp.MarketValue - bp.MarketValue
			base := lp.MarketValue
			if bp.MarketValue > base {
				base = bp.MarketValue
			}
			if base > 0 {
				if rel := abs(mvDelta) / base; rel > cfg.MarketValueTolerance {
					out = append(out, Discrepancy{
						Kind: KindMarketValue, Severity: SeverityWarning, Symbol: sym, Field: "market_value",
						Local: lp.MarketValue, Broker: bp.MarketValue,
						Delta: mvDelta, Tolerance: cfg.MarketValueTolerance,
						Note: "持仓市值相对偏差超容差（行情刷新延迟?）", DetectedAt: at,
					})
				}
			}
		}
	}

	// 3) 费用分桶（本仓 Fees 是三桶；core 的 other 桶不进新口径）。
	for _, b := range []struct {
		field  string
		l, bkt float64
	}{
		{"commission", local.Fees.Commission, broker.Fees.Commission},
		{"stamp_tax", local.Fees.StampTax, broker.Fees.StampTax},
		{"transfer_fee", local.Fees.TransferFee, broker.Fees.TransferFee},
	} {
		if d := b.l - b.bkt; abs(d) > cfg.FeeTolerance {
			out = append(out, Discrepancy{
				Kind: KindFee, Severity: SeverityWarning, Field: b.field,
				Local: b.l, Broker: b.bkt,
				Delta: d, Tolerance: cfg.FeeTolerance,
				Note: "费用累计偏差超容差", DetectedAt: at,
			})
		}
	}

	// 4) 快照时间戳漂移：只提示，不判差异。
	drift := local.AsOf.Sub(broker.AsOf)
	if drift < 0 {
		drift = -drift
	}
	limit := cfg.AsOfDriftTolerance
	if limit == 0 {
		limit = 60 * time.Second
	}
	if drift > limit {
		out = append(out, Discrepancy{
			Kind: KindAsOfDrift, Severity: SeverityInfo, Field: "as_of",
			Local: float64(local.AsOf.Unix()), Broker: float64(broker.AsOf.Unix()),
			Delta: drift.Seconds(), Tolerance: limit.Seconds(),
			Note: "本地/券商快照时间戳漂移超容差", DetectedAt: at,
		})
	}

	return out, nil
}

// indexPositions 按 symbol 建索引；重复 symbol 是快照装配错误，fail-loud。
func indexPositions(s *gateway.AccountSnapshot) (map[string]gateway.Position, error) {
	m := make(map[string]gateway.Position, len(s.Positions))
	for _, p := range s.Positions {
		if _, dup := m[p.Symbol]; dup {
			return nil, fmt.Errorf("reconcile: duplicate symbol %q in snapshot of %q", p.Symbol, s.AccountID)
		}
		m[p.Symbol] = p
	}
	return m, nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func absInt(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
