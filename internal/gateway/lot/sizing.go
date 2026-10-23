// Package lot 是交易所申报数量规则的纯函数面：无 I/O、无时钟、无全局状态。
//
// 归属（docs/ARCHITECTURE.md §9.1 F5 / §9.7 R5）：申报数量是**交易所规则**，
// 属通道侧；RiskPolicy 只管数量上限 / 集中度 / T+1 / 止损，网关不 import 风控裁决。
// 两个通道实现必须调用同一份规则，而不是各自内联。
//
// 两条刻意的口径（改前先读）：
//
//   - **只向下取整，不向上补到最小单位**。目标不足一个申报单位时返回 0 手并拒单。
//     把 50 股"补"成 100 股会放大仓位，把 0.4 股抬到 100 股更会放大两个数量级。
//   - **涨跌停带按闭区间校验**：价格在 [跌停, 涨停] 内合法，**等于**涨跌停也合法
//     （涨停买入 / 跌停卖出是真实可申报的委托，打板就靠它）；只有越界价才拒。
package lot

import (
	"fmt"
	"math"
)

// DefaultLotSize 是默认申报单位（A 股一手 = 100 股）。
const DefaultLotSize = 100

// RejectReason 是拒单原因码。零值表示通过；调用方按码分诊，不要解析 Detail。
type RejectReason string

const (
	// ReasonInvalidInput 是入参自身不合法（负数 / NaN / Inf / 涨跌停区间倒置）。
	ReasonInvalidInput RejectReason = "invalid_input"
	// ReasonPriceAboveLimitUp 是申报价高于涨停价（越界，不是"等于"）。
	ReasonPriceAboveLimitUp RejectReason = "price_above_limit_up"
	// ReasonPriceBelowLimitDown 是申报价低于跌停价（越界，不是"等于"）。
	ReasonPriceBelowLimitDown RejectReason = "price_below_limit_down"
	// ReasonZeroQuantity 是向下取整到申报单位后为 0 手。
	ReasonZeroQuantity RejectReason = "zero_quantity"
	// ReasonInsufficientCapital 是买入所需金额超过可用资金（不自动缩量）。
	ReasonInsufficientCapital RejectReason = "insufficient_capital"
	// ReasonExceedsAvailable 是卖出数量超过可用数量（不自动缩量）。
	ReasonExceedsAvailable RejectReason = "exceeds_available_quantity"
	// ReasonNotLotMultiple 是显式数量不是申报单位的整数倍。
	ReasonNotLotMultiple RejectReason = "not_lot_multiple"
)

// Input 是申报数量的计算输入。
//
//   - 买入看 Capital，卖出看 AvailableQuantity；另一项被忽略。
//   - LotSize 是申报单位（0 用 DefaultLotSize）。板块最低申报量（如科创板 200 股）
//     这类"地板"不在本函数内（它需要 symbol 与板块表）：调用方先按板块规则校验 / 折算，
//     再把申报单位传进来——本函数不解析 symbol，避免与 core 的板块分类表各存一份而漂移。
//   - LimitUp / LimitDown 为 0 表示"未提供"，跳过该侧校验；两者都提供时必须
//     LimitUp >= LimitDown。
type Input struct {
	Sell              bool
	TargetWeight      float64
	Price             float64
	Capital           float64
	AvailableQuantity int
	LotSize           int
	LimitUp           float64
	LimitDown         float64
}

// Result 是申报数量结果。拒单时 Quantity / Lots 恒为 0——被拒的结果不是订单，
// 不允许调用方"顺手"把它发出去。
type Result struct {
	Quantity int
	Lots     int
	Reject   RejectReason
	Detail   string
}

// OK 报告结果是否可下单。
func (r Result) OK() bool { return r.Reject == "" }

// Size 由目标权重、价格和资金 / 可用数量计算申报数量。
//
// 目标股数 = TargetWeight × Capital ÷ Price（买入）或 TargetWeight × AvailableQuantity
// （卖出），再向下取整到 LotSize 的整数倍。卖出不得超过可用数量，买入不得超过可用
// 资金——**拒单而不是静默缩量**，缩量会把尺寸错误藏起来。
func Size(in Input) Result {
	if !finite(in.TargetWeight) || in.TargetWeight < 0 {
		return reject(ReasonInvalidInput, "target weight must be a finite non-negative number, got %v", in.TargetWeight)
	}
	if !finite(in.Price) || in.Price <= 0 {
		return reject(ReasonInvalidInput, "price must be a finite positive number, got %v", in.Price)
	}
	if in.LotSize < 0 {
		return reject(ReasonInvalidInput, "lot size must not be negative, got %d", in.LotSize)
	}
	lotSize := in.LotSize
	if lotSize == 0 {
		lotSize = DefaultLotSize
	}

	if !finite(in.LimitUp) || !finite(in.LimitDown) || in.LimitUp < 0 || in.LimitDown < 0 {
		return reject(ReasonInvalidInput,
			"price limits must be finite non-negative numbers (0 = not supplied), got up=%v down=%v",
			in.LimitUp, in.LimitDown)
	}
	if in.LimitUp > 0 && in.LimitDown > 0 && in.LimitUp < in.LimitDown {
		return reject(ReasonInvalidInput, "limit up %v is below limit down %v", in.LimitUp, in.LimitDown)
	}
	if in.LimitUp > 0 && in.Price > in.LimitUp {
		return reject(ReasonPriceAboveLimitUp, "price %v is above limit up %v", in.Price, in.LimitUp)
	}
	if in.LimitDown > 0 && in.Price < in.LimitDown {
		return reject(ReasonPriceBelowLimitDown, "price %v is below limit down %v", in.Price, in.LimitDown)
	}

	var desired float64
	if in.Sell {
		if in.AvailableQuantity < 0 {
			return reject(ReasonInvalidInput, "available quantity must not be negative, got %d", in.AvailableQuantity)
		}
		desired = in.TargetWeight * float64(in.AvailableQuantity)
	} else {
		if !finite(in.Capital) || in.Capital < 0 {
			return reject(ReasonInvalidInput, "capital must be a finite non-negative number, got %v", in.Capital)
		}
		desired = in.TargetWeight * in.Capital / in.Price
	}
	if !finite(desired) || desired >= float64(maxInt) {
		return reject(ReasonInvalidInput, "target quantity %v is out of range", desired)
	}
	// 按**目标股数**判，而不是取整后的数量：1.09 × 1,000 股目标 1,090 股即便
	// floor 到 1,000 股也不该放行——那是"想卖超过持仓"，不是合法的整手折算。
	if in.Sell && desired > float64(in.AvailableQuantity) {
		return reject(ReasonExceedsAvailable, "target %.6g shares exceeds available %d", desired, in.AvailableQuantity)
	}

	quantity := floorToLot(desired, lotSize)
	if quantity == 0 {
		return reject(ReasonZeroQuantity, "target %.6g shares is below one lot of %d", desired, lotSize)
	}
	if !in.Sell {
		if cost := float64(quantity) * in.Price; cost > in.Capital {
			return reject(ReasonInsufficientCapital, "target %d shares costs %.2f, capital %.2f", quantity, cost, in.Capital)
		}
	}
	return Result{Quantity: quantity, Lots: quantity / lotSize}
}

// ValidateQuantity 校验一个**显式**申报数量是否为正的申报单位整数倍。
//
// 这是 Size 的校验对照面：Size 回答"该报多少"，ValidateQuantity 回答"这个数量能不能报"。
// 通道适配器收到调用方算好的数量时用后者；**绝不静默改写数量**——在最后一跳改尺寸
// 只会把上游的定仓错误藏起来。
//
// 它只做申报单位校验（卖出零股是否合法取决于持仓状态，本函数没有那个信息，
// 不在这里猜），也不认识板块：板块最低量由调用方传入合适的 LotSize。
func ValidateQuantity(quantity, lotSize int) Result {
	if lotSize < 0 {
		return reject(ReasonInvalidInput, "lot size must not be negative, got %d", lotSize)
	}
	if lotSize == 0 {
		lotSize = DefaultLotSize
	}
	if quantity < 0 {
		return reject(ReasonInvalidInput, "quantity must not be negative, got %d", quantity)
	}
	if quantity == 0 {
		return reject(ReasonZeroQuantity, "quantity must be positive, got 0")
	}
	if quantity%lotSize != 0 {
		return reject(ReasonNotLotMultiple, "quantity %d is not a multiple of lot size %d", quantity, lotSize)
	}
	return Result{Quantity: quantity, Lots: quantity / lotSize}
}

// maxInt 是当前平台 int 的上界；目标股数超过它就不是一张真实订单。
const maxInt = int(^uint(0) >> 1)

func floorToLot(shares float64, lotSize int) int {
	if shares <= 0 {
		return 0
	}
	lots := math.Floor(shares / float64(lotSize))
	if lots <= 0 {
		return 0
	}
	return int(lots) * lotSize
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func reject(reason RejectReason, format string, args ...any) Result {
	return Result{Reject: reason, Detail: fmt.Sprintf(format, args...)}
}
