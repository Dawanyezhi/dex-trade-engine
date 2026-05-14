package main

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// TickRange 表示一个流动性区间。
// CLMM 中，LP 将流动性集中在 [LowerTick, UpperTick] 的价格范围内。
type TickRange struct {
	LowerTick int64    // 下界 Tick（对应较低价格）
	UpperTick int64    // 上界 Tick（对应较高价格）
	Liquidity *big.Int // 该区间的流动性
}

// ConcentratedLiquidityMM 集中流动性做市商（简化模型）。
// 对应 Raydium CLMM（Solana）和 Uniswap V3（EVM）。
//
// 简化说明：
//   - 将活跃区间内的流动性转化为等效 AMM 储量进行计算
//   - 支持跨 Tick 范围交易：当单个区间容量不足时，自动跨到相邻区间继续计算
type ConcentratedLiquidityMM struct {
	dexID dexwallet.DexID
}

// NewConcentratedLiquidityMM 创建一个 CLMM 协议实例。
func NewConcentratedLiquidityMM(dexID dexwallet.DexID) *ConcentratedLiquidityMM {
	return &ConcentratedLiquidityMM{dexID: dexID}
}

// DexID 返回 DEX 标识。
func (c *ConcentratedLiquidityMM) DexID() dexwallet.DexID {
	return c.dexID
}

// ProtocolType 返回协议类型。
func (c *ConcentratedLiquidityMM) ProtocolType() dexwallet.ProtocolType {
	return dexwallet.ProtocolCLMM
}

// GetPrice 获取当前价格。
// 在 CLMM 中，当前价格由 currentTick 决定。
// 简化实现：使用活跃区间的虚拟储量计算价格。
func (c *ConcentratedLiquidityMM) GetPrice(_ context.Context, pool *dexwallet.Pool) (*big.Int, error) {
	_, activeRange, err := extractCLMMData(pool)
	if err != nil {
		return nil, fmt.Errorf("clmm GetPrice: %w", err)
	}
	if activeRange == nil {
		return nil, fmt.Errorf("clmm GetPrice: no active tick range for current price")
	}

	// 使用活跃区间的虚拟储量来表示价格
	// 虚拟储量 = liquidity（简化模型中直接使用流动性作为两边储量的基础）
	// 价格由 tick 决定：price = 1.0001^tick
	// 简化：使用等效 AMM 储量的价格 = reserveQuote / reserveBase
	reserveBase, reserveQuote := calcVirtualReserves(activeRange)
	if reserveBase.Sign() == 0 {
		return nil, fmt.Errorf("clmm GetPrice: virtual base reserve is zero")
	}

	price := new(big.Int).Mul(reserveQuote, PricePrecision)
	price.Div(price, reserveBase)

	return price, nil
}

// Quote 获取报价。
// 在活跃 Tick 范围内，使用等效 AMM 模型计算输出。
// 集中流动性意味着等效储量更大，因此同等交易量的滑点更低。
//
// 支持跨 tick 范围交易：当交易量超过当前活跃 tick range 的容量时，
// 会跨到相邻的 tick range 继续计算，直到全部输入被消耗或所有 range 耗尽。
func (c *ConcentratedLiquidityMM) Quote(_ context.Context, pool *dexwallet.Pool, amountIn *big.Int, direction dexwallet.SwapDirection) (*dexwallet.Quote, error) {
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("clmm Quote: amountIn must be positive")
	}

	if direction != dexwallet.SwapDirectionSell && direction != dexwallet.SwapDirectionBuy {
		return nil, fmt.Errorf("clmm Quote: unknown direction: %s", direction)
	}

	currentTick, _, err := extractCLMMData(pool)
	if err != nil {
		return nil, fmt.Errorf("clmm Quote: %w", err)
	}

	// 获取所有 tick ranges 并按交易方向排序
	orderedRanges, err := getOrderedTickRanges(pool, currentTick, direction)
	if err != nil {
		return nil, fmt.Errorf("clmm Quote: %w", err)
	}

	if len(orderedRanges) == 0 {
		return nil, fmt.Errorf("clmm Quote: no active tick range for current price")
	}

	// 跨 tick range 累加计算
	remainingIn := new(big.Int).Set(amountIn)
	totalOut := new(big.Int)
	// 用于价格影响计算的加权储量
	totalReserveIn := new(big.Int)
	totalReserveOut := new(big.Int)

	for _, tr := range orderedRanges {
		if remainingIn.Sign() <= 0 {
			break
		}

		reserveBase, reserveQuote := calcVirtualReserves(&tr)
		if reserveBase.Sign() == 0 || reserveQuote.Sign() == 0 {
			continue
		}

		var reserveIn, reserveOut *big.Int
		switch direction {
		case dexwallet.SwapDirectionSell:
			reserveIn = reserveBase
			reserveOut = reserveQuote
		case dexwallet.SwapDirectionBuy:
			reserveIn = reserveQuote
			reserveOut = reserveBase
		}

		// 计算本 range 能消耗的最大输入量
		// maxAmountIn 是使 amountOut 接近 reserveOut 的输入量
		// 使用 reserveOut 的 99% 作为安全上限来反推最大输入
		maxOut := new(big.Int).Mul(reserveOut, big.NewInt(99))
		maxOut.Div(maxOut, big.NewInt(100))
		maxAmountIn := calcMaxInput(maxOut, reserveIn, reserveOut, pool.FeeRate)

		var consumedIn *big.Int
		if remainingIn.Cmp(maxAmountIn) <= 0 {
			// 当前 range 能消耗全部剩余输入
			consumedIn = new(big.Int).Set(remainingIn)
		} else {
			// 当前 range 只能消耗部分输入
			consumedIn = maxAmountIn
		}

		if consumedIn.Sign() <= 0 {
			continue
		}

		out := calcAMMOutput(consumedIn, reserveIn, reserveOut, pool.FeeRate)
		if out.Sign() <= 0 {
			continue
		}

		totalOut.Add(totalOut, out)
		totalReserveIn.Add(totalReserveIn, reserveIn)
		totalReserveOut.Add(totalReserveOut, reserveOut)
		remainingIn.Sub(remainingIn, consumedIn)
	}

	if remainingIn.Sign() > 0 {
		return nil, fmt.Errorf("clmm Quote: trade too large: exhausted all tick ranges")
	}

	if totalOut.Sign() <= 0 {
		return nil, fmt.Errorf("clmm Quote: output amount is zero")
	}

	// 计算价格影响（使用累加的储量）
	var priceImpact uint64
	if totalReserveIn.Sign() > 0 && totalReserveOut.Sign() > 0 {
		priceImpact = calcPriceImpact(amountIn, totalOut, totalReserveIn, totalReserveOut)
	}

	return &dexwallet.Quote{
		DexID:        c.dexID,
		ProtocolType: dexwallet.ProtocolCLMM,
		Priority:     dexwallet.PriorityMedium,
		InputAmount:  new(big.Int).Set(amountIn),
		OutputAmount: totalOut,
		PriceImpact:  priceImpact,
		Pool:         pool,
		EstimatedGas: big.NewInt(300000), // CLMM 交易比 AMM 更贵
	}, nil
}

// getOrderedTickRanges 获取按交易方向排序的 tick ranges。
// 前提：currentTick 必须在某个 tick range 内（即存在活跃 range），否则返回空列表。
// Sell 方向（价格下降）：从包含 currentTick 的 range 开始，向下遍历（LowerTick 降序）
// Buy 方向（价格上升）：从包含 currentTick 的 range 开始，向上遍历（LowerTick 升序）
func getOrderedTickRanges(pool *dexwallet.Pool, currentTick int64, direction dexwallet.SwapDirection) ([]TickRange, error) {
	tr, ok := pool.Extra["TickRanges"]
	if !ok {
		return nil, fmt.Errorf("pool.Extra missing TickRanges")
	}
	tickRanges, ok := tr.([]TickRange)
	if !ok {
		return nil, fmt.Errorf("pool.Extra TickRanges is not []TickRange")
	}

	// 首先检查 currentTick 是否在某个 range 内（活跃 range 必须存在）
	hasActive := false
	for _, r := range tickRanges {
		if currentTick >= r.LowerTick && currentTick < r.UpperTick {
			hasActive = true
			break
		}
	}
	if !hasActive {
		return nil, nil
	}

	// 复制一份避免修改原始数据
	ranges := make([]TickRange, len(tickRanges))
	copy(ranges, tickRanges)

	switch direction {
	case dexwallet.SwapDirectionSell:
		// Sell: 价格下降，从当前 tick 向下遍历
		// 排序：LowerTick 降序（先处理高价区间）
		sort.Slice(ranges, func(i, j int) bool {
			return ranges[i].LowerTick > ranges[j].LowerTick
		})
		// 过滤：只保留 LowerTick <= currentTick 的 range（当前 tick 所在或其下方）
		var result []TickRange
		for _, r := range ranges {
			if currentTick >= r.LowerTick {
				result = append(result, r)
			}
		}
		return result, nil

	case dexwallet.SwapDirectionBuy:
		// Buy: 价格上升，从当前 tick 向上遍历
		// 排序：LowerTick 升序（先处理低价区间）
		sort.Slice(ranges, func(i, j int) bool {
			return ranges[i].LowerTick < ranges[j].LowerTick
		})
		// 过滤：只保留 UpperTick > currentTick 的 range（当前 tick 所在或其上方）
		var result []TickRange
		for _, r := range ranges {
			if currentTick < r.UpperTick {
				result = append(result, r)
			}
		}
		return result, nil
	}

	return nil, fmt.Errorf("unknown direction: %s", direction)
}

// calcMaxInput 根据目标输出量反推最大输入量。
// 由 AMM 公式 amountOut = reserveOut * effectiveIn / (reserveIn * 10000 + effectiveIn) 反推：
// effectiveIn = amountOut * reserveIn * 10000 / (reserveOut - amountOut)
// amountIn = effectiveIn / (10000 - feeRate)
func calcMaxInput(targetOut, reserveIn, reserveOut *big.Int, feeRate uint64) *big.Int {
	if targetOut.Sign() <= 0 || targetOut.Cmp(reserveOut) >= 0 {
		return new(big.Int)
	}

	bigBase := big.NewInt(10000)
	feeFactor := big.NewInt(int64(10000 - feeRate))

	// effectiveIn = targetOut * reserveIn * 10000 / (reserveOut - targetOut)
	numerator := new(big.Int).Mul(targetOut, reserveIn)
	numerator.Mul(numerator, bigBase)

	denominator := new(big.Int).Sub(reserveOut, targetOut)
	if denominator.Sign() <= 0 {
		return new(big.Int)
	}

	effectiveIn := new(big.Int).Div(numerator, denominator)

	// amountIn = effectiveIn * 10000 / (10000 - feeRate)
	amountIn := new(big.Int).Mul(effectiveIn, bigBase)
	amountIn.Div(amountIn, feeFactor)

	// 加 1 确保向上取整，避免因截断导致输出不足
	amountIn.Add(amountIn, big.NewInt(1))

	return amountIn
}

// calcVirtualReserves 计算活跃 Tick 范围的虚拟储量。
//
// 在 CLMM 中，集中在 [tickLower, tickUpper] 的流动性 L，
// 等效于一个拥有更大储量的全范围 AMM。
//
// 简化模型：
//   - 虚拟 reserveBase = liquidity * tickSpan（表示区间越窄，等效深度越大）
//   - 虚拟 reserveQuote = liquidity * tickSpan * midPrice
//
// 这是对真实 CLMM 数学的简化。真实实现中需要用 sqrtPrice 计算。
func calcVirtualReserves(tr *TickRange) (reserveBase, reserveQuote *big.Int) {
	tickSpan := tr.UpperTick - tr.LowerTick
	if tickSpan <= 0 {
		return new(big.Int), new(big.Int)
	}

	// 集中流动性的关键：价格范围越窄（tickSpan 越小），
	// 等效的单位流动性深度越大。
	// 这里使用一个简化的效率因子：efficiency = 10000 / tickSpan
	// tickSpan 越小，efficiency 越大，虚拟储量越大。
	// 基础效率乘数
	efficiencyBase := big.NewInt(10000)
	tickSpanBig := big.NewInt(tickSpan)

	// 为避免除以 0 和保持合理的储量，给 tickSpan 一个下限
	if tickSpanBig.Cmp(big.NewInt(1)) < 0 {
		tickSpanBig = big.NewInt(1)
	}

	// 虚拟 reserveBase = liquidity * efficiencyBase / tickSpan
	reserveBase = new(big.Int).Mul(tr.Liquidity, efficiencyBase)
	reserveBase.Div(reserveBase, tickSpanBig)

	// 虚拟 reserveQuote = reserveBase（简化为 1:1 等效，价格由 Tick 位置决定）
	// 在更精确的模型中，这里应该根据当前 Tick 在区间中的位置来分配两边的储量。
	// 简化处理：两边对称
	reserveQuote = new(big.Int).Set(reserveBase)

	return reserveBase, reserveQuote
}

// extractCLMMData 从 Pool.Extra 中提取 CLMM 数据。
// 返回当前 Tick 和活跃的 TickRange。
func extractCLMMData(pool *dexwallet.Pool) (currentTick int64, activeRange *TickRange, err error) {
	if pool == nil {
		return 0, nil, fmt.Errorf("pool is nil")
	}
	if pool.Extra == nil {
		return 0, nil, fmt.Errorf("pool.Extra is nil")
	}

	ct, ok := pool.Extra["CurrentTick"]
	if !ok {
		return 0, nil, fmt.Errorf("pool.Extra missing CurrentTick")
	}
	currentTick, ok = ct.(int64)
	if !ok {
		return 0, nil, fmt.Errorf("pool.Extra CurrentTick is not int64")
	}

	tr, ok := pool.Extra["TickRanges"]
	if !ok {
		return 0, nil, fmt.Errorf("pool.Extra missing TickRanges")
	}
	tickRanges, ok := tr.([]TickRange)
	if !ok {
		return 0, nil, fmt.Errorf("pool.Extra TickRanges is not []TickRange")
	}

	// 查找包含当前 Tick 的活跃范围
	for i := range tickRanges {
		if currentTick >= tickRanges[i].LowerTick && currentTick < tickRanges[i].UpperTick {
			activeRange = &tickRanges[i]
			break
		}
	}

	return currentTick, activeRange, nil
}
