package main

// ============================================================
// CLMM 跨 tick 报价 & Bonding Curve 毕业检测
// ============================================================
//
// AMM (x*y=k) 报价：
//   outputAmount = (y * dx) / (x + dx) * (1 - fee)
//   - 整个价格范围内流动性均匀分布
//   - 一次计算即可得到输出金额
//   - 大单不会穿越价格区间
//
// CLMM (集中流动性) 报价：
//   - 流动性分布在不同的 tick 区间 [tickLower, tickUpper]
//   - 大单可能穿越多个 tick 区间，每穿越一个 tick 需要重新计算
//   - 不同区间的流动性不同，价格影响不同
//   - 价格 = 1.0001^tick
//
// 跨 tick 报价流程：
//   1. 从当前 tick 开始
//   2. 计算当前区间内能消化多少输入金额
//   3. 如果输入金额还有剩余，穿越到下一个 tick 区间
//   4. 重复直到输入金额消耗完毕或到达价格限制
//   5. 累加所有区间的输出金额
//
// 穿越 tick 的价格影响 > AMM，因为：
//   - AMM 的流动性均匀分布，价格沿光滑曲线变化
//   - CLMM 的流动性在 tick 边界处不连续，穿越时可能遇到"流动性悬崖"
//
// ============================================================
// CLMM 跨 tick 报价为什么比 AMM 复杂：
//
// AMM 的流动性在整个价格范围 (0, +inf) 内均匀分布，因此无论交易金额多大，
// 都只需要一次 x*y=k 公式即可计算输出。
//
// CLMM 将价格空间切分为离散的 tick，流动性提供者可以选择只在某个 tick
// 区间内提供流动性。这意味着：
//   - 不同价格区间的流动性可能完全不同
//   - 当一笔大单消耗完当前 tick 区间的所有流动性后，必须穿越到下一个
//     tick 区间，而下一个区间可能流动性很低甚至为零（"流动性悬崖"）
//   - 每穿越一个 tick 边界，需要重新加载该区间的流动性，再次计算
//   - 最终输出是所有被穿越区间的输出之和
//
// ============================================================
// tickSpacing 与 fee tier 的对应关系：
//
// Uniswap V3 定义了 fee tier 和 tickSpacing 的映射：
//   fee = 1 bps (0.01%)  → tickSpacing = 1   （稳定币对，极窄区间）
//   fee = 5 bps (0.05%)  → tickSpacing = 10  （稳定币/主流币对）
//   fee = 30 bps (0.30%) → tickSpacing = 60  （主流对，最常用）
//   fee = 100 bps (1.0%) → tickSpacing = 200 （长尾资产/高波动对）
//
// tickSpacing 越大，LP 的仓位粒度越粗，但链上存储和计算更省 Gas。
// tickSpacing 越小，LP 可以更精确地设置价格区间，但 Gas 成本更高。
// Raydium CLMM 也采用类似的 tickSpacing 设计。
// ============================================================

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"sync"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// Part 1: CLMM 跨 tick 报价
// ============================================================

// TickRange 表示一个 CLMM 的 tick 区间。
// 在 CLMM 中，流动性提供者选择一个 [tickLower, tickUpper] 区间来集中提供流动性。
// 只有当当前价格落在某个区间内时，该区间的流动性才会被激活参与交易。
type TickRange struct {
	TickLower int32    // 区间下界（对应较低的价格）
	TickUpper int32    // 区间上界（对应较高的价格）
	Liquidity *big.Int // 该区间的流动性（L 值，越大表示该区间内能承受越大的交易量）
}

// CLMMQuoteResult CLMM 报价结果。
// 与 AMM 的报价不同，CLMM 报价额外记录了穿越了多少个 tick 区间、最终 tick 位置、
// 以及实际消耗的输入金额（可能因为流动性耗尽而少于请求金额）。
type CLMMQuoteResult struct {
	AmountOut      *big.Int // 输出金额
	AmountInUsed   *big.Int // 实际消耗的输入金额（可能 < 请求的 amountIn，当流动性不足时）
	FeeAmount      *big.Int // 手续费总额
	TicksCrossed   int      // 穿越的 tick 区间数
	FinalTick      int32    // 报价结束时的最终 tick 位置
	PriceImpactBps uint64   // 价格影响（基点），衡量交易对价格的移动程度
}

// CLMMQuoter CLMM 跨 tick 报价器。
//
// 模拟 Uniswap V3 / Raydium CLMM 的报价逻辑（教学简化版）。
// 生产中使用精确的 sqrtPrice 数学:
//
//	deltaY = L * (sqrt(P_upper) - sqrt(P_lower))  // token1 变化量
//	deltaX = L * (1/sqrt(P_lower) - 1/sqrt(P_upper))  // token0 变化量
//
// 这里为了教学目的，使用简化的 AMM 公式在每个 tick 区间内计算，
// 但保留了跨 tick 穿越的核心逻辑。
type CLMMQuoter struct {
	tickSpacing int32       // tick 间距（如 Uniswap V3: fee=0.3% -> spacing=60）
	feeBps      uint64      // 手续费（基点，如 30 = 0.3%）
	ticks       []TickRange // 已初始化的 tick 区间（按 TickLower 排序）
	currentTick int32       // 当前 tick（表示当前价格位置）
}

// NewCLMMQuoter 创建 CLMM 跨 tick 报价器。
//
// 参数:
//   - tickSpacing: tick 间距，决定了最小的价格粒度
//   - feeBps: 手续费率（基点），例如 30 表示 0.3%
//   - currentTick: 当前 tick 位置，对应当前价格 = 1.0001^currentTick
func NewCLMMQuoter(tickSpacing int32, feeBps uint64, currentTick int32) *CLMMQuoter {
	return &CLMMQuoter{
		tickSpacing: tickSpacing,
		feeBps:      feeBps,
		ticks:       make([]TickRange, 0),
		currentTick: currentTick,
	}
}

// AddTickRange 添加一个 tick 区间。
//
// 注意: tickLower 和 tickUpper 应该是 tickSpacing 的整数倍。
// 例如 tickSpacing=60 时，合法的 tick 值为 ..., -120, -60, 0, 60, 120, ...
// 添加后会按 TickLower 升序排序，方便 Quote 时按方向遍历。
func (q *CLMMQuoter) AddTickRange(tickLower, tickUpper int32, liquidity *big.Int) {
	q.ticks = append(q.ticks, TickRange{
		TickLower: tickLower,
		TickUpper: tickUpper,
		Liquidity: new(big.Int).Set(liquidity),
	})
	// 按 TickLower 升序排序
	sort.Slice(q.ticks, func(i, j int) bool {
		return q.ticks[i].TickLower < q.ticks[j].TickLower
	})
}

// TickToPrice 将 tick 转换为价格。
//
// CLMM 中价格与 tick 的关系: price = 1.0001^tick
// 例如:
//   - tick = 0     -> price = 1.0
//   - tick = 6932  -> price ~ 2.0 (1.0001^6932 ≈ 2.0)
//   - tick = -6932 -> price ~ 0.5
//
// 注意: 这是 token1/token0 的价格。
// tick 越大，token0 越贵（需要更多 token1 来买 token0）。
func (q *CLMMQuoter) TickToPrice(tick int32) float64 {
	return math.Pow(1.0001, float64(tick))
}

// Quote 执行 CLMM 跨 tick 报价。
//
// 核心逻辑（教学简化版）：
//  1. 根据交易方向（zeroForOne），确定 tick 的遍历方向
//  2. 从当前 tick 开始，找到覆盖当前 tick 的活跃区间
//  3. 在当前活跃区间内，使用简化 AMM 公式计算能消化多少输入
//  4. 如果输入金额还有剩余，穿越到下一个 tick 区间
//  5. 重复直到输入金额消耗完毕或者没有更多流动性
//  6. 累加所有区间的输出金额
//
// 参数:
//   - amountIn: 输入金额
//   - zeroForOne: 交易方向
//   - true: token0 -> token1（卖出 token0，价格下跌，tick 递减）
//   - false: token1 -> token0（买入 token0，价格上涨，tick 递增）
//
// 注意: 这是教学简化版，生产环境中使用精确的 sqrtPrice 数学：
//
//	deltaY = L * (sqrt(P_upper) - sqrt(P_lower))
//	deltaX = L * (1/sqrt(P_lower) - 1/sqrt(P_upper))
func (q *CLMMQuoter) Quote(amountIn *big.Int, zeroForOne bool) (*CLMMQuoteResult, error) {
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("clmm quote: amountIn 必须大于 0")
	}

	if len(q.ticks) == 0 {
		return nil, fmt.Errorf("clmm quote: 没有已初始化的 tick 区间")
	}

	// 按交易方向排序 tick 区间
	// zeroForOne=true (token0->token1): tick 递减，从高到低遍历
	// zeroForOne=false (token1->token0): tick 递增，从低到高遍历
	orderedTicks := q.getOrderedTicks(zeroForOne)

	// 记录初始价格（用于计算价格影响）
	startPrice := q.TickToPrice(q.currentTick)

	remaining := new(big.Int).Set(amountIn) // 剩余待消耗的输入金额
	totalOut := new(big.Int)                 // 累计输出金额
	totalFee := new(big.Int)                 // 累计手续费
	ticksCrossed := 0                        // 穿越的 tick 区间数
	finalTick := q.currentTick               // 最终 tick

	// 遍历各 tick 区间
	for _, tr := range orderedTicks {
		if remaining.Sign() <= 0 {
			break
		}

		// 检查当前 tick 是否在这个区间范围内，或者是否已经穿越到这个区间
		// zeroForOne: tick 递减方向，需要当前 finalTick >= tr.TickLower
		// !zeroForOne: tick 递增方向，需要当前 finalTick <= tr.TickUpper
		if zeroForOne && finalTick < tr.TickLower {
			continue
		}
		if !zeroForOne && finalTick > tr.TickUpper {
			continue
		}

		// 计算当前区间可以消化的最大输入金额
		// 简化模型：maxInput = liquidity * tickSpacing / 10000
		// 实际 CLMM 中，这取决于 sqrtPrice 的变化范围
		maxInThisRange := q.calculateMaxInput(tr.Liquidity)

		// 实际在此区间消耗的输入
		actualIn := new(big.Int)
		if remaining.Cmp(maxInThisRange) <= 0 {
			actualIn.Set(remaining)
		} else {
			actualIn.Set(maxInThisRange)
		}

		// 扣除手续费
		// fee = actualIn * feeBps / 10000
		fee := new(big.Int).Mul(actualIn, big.NewInt(int64(q.feeBps)))
		fee.Div(fee, big.NewInt(10000))

		// 实际参与交易的金额（扣除手续费后）
		amountInAfterFee := new(big.Int).Sub(actualIn, fee)

		// 使用简化 AMM 公式计算此区间的输出:
		// actualOut = amountInAfterFee * L / (L + amountInAfterFee)
		// 其中 L 是该区间的流动性
		//
		// 这是简化版。生产中的精确计算:
		//   zeroForOne:  deltaY = L * (sqrt(P_current) - sqrt(P_next))
		//   !zeroForOne: deltaX = L * (1/sqrt(P_current) - 1/sqrt(P_next))
		numerator := new(big.Int).Mul(amountInAfterFee, tr.Liquidity)
		denominator := new(big.Int).Add(tr.Liquidity, amountInAfterFee)
		actualOut := new(big.Int).Div(numerator, denominator)

		// 累加
		totalOut.Add(totalOut, actualOut)
		totalFee.Add(totalFee, fee)
		remaining.Sub(remaining, actualIn)
		ticksCrossed++

		// 穿越到下一个 tick 区间
		// zeroForOne: tick 向下穿越（价格下跌）
		// !zeroForOne: tick 向上穿越（价格上涨）
		if zeroForOne {
			finalTick = tr.TickLower - 1
		} else {
			finalTick = tr.TickUpper + 1
		}
	}

	// 计算价格影响（基点）
	// priceImpact = |endPrice - startPrice| / startPrice * 10000
	endPrice := q.TickToPrice(finalTick)
	var priceImpactBps uint64
	if startPrice > 0 {
		impact := math.Abs(endPrice-startPrice) / startPrice * 10000
		priceImpactBps = uint64(impact)
	}

	// 计算实际消耗的输入金额
	amountInUsed := new(big.Int).Sub(amountIn, remaining)

	return &CLMMQuoteResult{
		AmountOut:      totalOut,
		AmountInUsed:   amountInUsed,
		FeeAmount:      totalFee,
		TicksCrossed:   ticksCrossed,
		FinalTick:      finalTick,
		PriceImpactBps: priceImpactBps,
	}, nil
}

// getOrderedTicks 根据交易方向返回排序后的 tick 区间。
//
// zeroForOne=true (token0->token1): 价格下跌，tick 递减
//   - 从当前 tick 开始向下遍历，先遍历 TickUpper 较大的区间
//   - 排序: 按 TickUpper 降序
//
// zeroForOne=false (token1->token0): 价格上涨，tick 递增
//   - 从当前 tick 开始向上遍历，先遍历 TickLower 较小的区间
//   - 排序: 按 TickLower 升序（默认已排序）
func (q *CLMMQuoter) getOrderedTicks(zeroForOne bool) []TickRange {
	ordered := make([]TickRange, len(q.ticks))
	copy(ordered, q.ticks)

	if zeroForOne {
		// token0->token1: tick 递减，按 TickUpper 降序排列
		// 先消耗价格高的区间，再向下穿越
		sort.Slice(ordered, func(i, j int) bool {
			return ordered[i].TickUpper > ordered[j].TickUpper
		})
	}
	// !zeroForOne: 保持 TickLower 升序（已经排好）

	return ordered
}

// calculateMaxInput 计算一个 tick 区间内可以消化的最大输入金额。
//
// 简化模型: maxInput = liquidity * tickSpacing / 10000
//
// 这是教学简化。实际 CLMM 中，最大输入取决于:
//   - 当前 sqrtPrice 与区间边界 sqrtPrice 的差值
//   - 该区间的流动性 L
//   - 精确公式: deltaX = L * (1/sqrt(P_lower) - 1/sqrt(P_upper))
//     或 deltaY = L * (sqrt(P_upper) - sqrt(P_lower))
func (q *CLMMQuoter) calculateMaxInput(liquidity *big.Int) *big.Int {
	maxIn := new(big.Int).Mul(liquidity, big.NewInt(int64(q.tickSpacing)))
	maxIn.Div(maxIn, big.NewInt(10000))
	return maxIn
}

// ============================================================
// Part 2: Bonding Curve 毕业检测
// ============================================================
//
// Bonding Curve 毕业后会怎样：
//
// PumpFun/Moonshot 等平台的代币使用 Bonding Curve（联合曲线）作为初始定价机制，
// 这被称为"内盘"交易。当满足毕业条件后，代币会迁移到外盘：
//
//   1. PumpFun 毕业 → 迁移到 Raydium AMM（旧版）或 PumpAMM（新版 2025.04 后）
//      - 毕业阈值: ~85 SOL (实际为 85 * 10^9 lamports)
//      - 迁移时创建 Raydium/PumpAMM 池子，注入初始流动性
//      - LP Token 被销毁（永久锁定流动性，防止 rug pull）
//
//   2. Moonshot 毕业 → 迁移到 Meteora AMM
//      - 毕业阈值: ~500 SOL（比 PumpFun 高很多）
//      - 迁移后流动性锁定在 Meteora
//
// PumpFun vs Moonshot 的毕业阈值差异：
//
//   平台        毕业阈值    毕业后去向          LP 处理
//   PumpFun     ~85 SOL     Raydium/PumpAMM     LP 销毁
//   Moonshot    ~500 SOL    Meteora AMM         LP 锁定
//
// Moonshot 阈值更高意味着：
//   - 代币需要更多买盘才能毕业
//   - 内盘阶段更长，发射方需要吸引更多资金
//   - 毕业后初始流动性更大，外盘交易滑点更小
//
// 为什么需要毕业检测：
//
// 这是交易机器人的核心风控逻辑之一。如果不做毕业检测，可能出现：
//   - 代币已经毕业到 Raydium，但机器人仍然尝试在 PumpFun 内盘买入
//   - 内盘已经关闭（complete=true），交易会失败
//   - 即使内盘还没完全关闭，内盘价格和外盘价格可能有巨大价差
//   - 用内盘的高价买入，到外盘发现实际价格低得多，造成亏损
//
// 正确的做法：
//   1. 检测到代币即将毕业或已毕业 → 切换到外盘 DEX
//   2. 如果 real_sol_reserves 接近阈值（>90%）→ 发出预警，可能暂停交易
//   3. 缓存已毕业的池子，避免重复查询链上数据
// ============================================================

// GraduationStatus 毕业状态检测结果。
type GraduationStatus struct {
	IsGraduated      bool     // 是否已毕业
	RealSOLReserves  *big.Int // 当前真实 SOL 储备量（lamports）
	Threshold        *big.Int // 毕业阈值（lamports）
	ProgressPercent  float64  // 毕业进度百分比（0-100）
	EstimatedSOLLeft *big.Int // 距离毕业还需要多少 SOL（lamports）
}

// BondingCurveGraduationChecker Bonding Curve 毕业检测器。
//
// 用于检测 PumpFun/Moonshot 等平台的代币是否已经达到毕业阈值。
// 一旦毕业，交易策略需要从内盘切换到外盘（Raydium/Meteora），
// 否则可能遇到交易失败或者严重的价差亏损。
type BondingCurveGraduationChecker struct {
	// 毕业阈值（真实 SOL 储备达到此值时毕业）
	// 默认 85 SOL (85e9 lamports)，对应 PumpFun
	// Moonshot 约为 500 SOL (500e9 lamports)
	graduationThreshold *big.Int

	// 已知已毕业的池子（缓存）
	// key: pool address, value: true
	// 一旦毕业就不会逆转，所以可以安全缓存
	graduated map[string]bool
	mu        sync.RWMutex
}

// NewBondingCurveGraduationChecker 创建毕业检测器。
//
// 参数:
//   - threshold: 毕业阈值（lamports）。
//     如果为 nil，使用默认值 85 SOL (85 * 10^9 lamports)，对应 PumpFun。
//     Moonshot 应传入 500 * 10^9。
func NewBondingCurveGraduationChecker(threshold *big.Int) *BondingCurveGraduationChecker {
	t := threshold
	if t == nil {
		// 默认 PumpFun 毕业阈值: 85 SOL = 85 * 10^9 lamports
		t = new(big.Int).Mul(big.NewInt(85), big.NewInt(1e9))
	}
	return &BondingCurveGraduationChecker{
		graduationThreshold: t,
		graduated:           make(map[string]bool),
	}
}

// CheckGraduation 检测池子的毕业状态。
//
// 检测逻辑:
//  1. 从 pool.Extra 中读取 "complete" 字段 — 如果为 true，代币已经完成毕业迁移
//  2. 从 pool.Extra 中读取 "real_sol_reserves" — 当前真实的 SOL 储备量
//  3. 如果 real_sol_reserves >= threshold，代币即将毕业（或已到阈值但还没迁移）
//  4. 计算进度百分比，帮助策略层判断是否需要提前切换
//
// pool.Extra 中的字段来自链上数据解析:
//   - "complete": bool — PumpFun 合约的 complete 标志位
//   - "real_sol_reserves": *big.Int — PumpFun Bonding Curve 账户中的实际 SOL 余额
func (c *BondingCurveGraduationChecker) CheckGraduation(pool *dexwallet.Pool) GraduationStatus {
	// 默认状态: 未毕业，无储备信息
	status := GraduationStatus{
		IsGraduated:      false,
		RealSOLReserves:  big.NewInt(0),
		Threshold:        new(big.Int).Set(c.graduationThreshold),
		ProgressPercent:  0,
		EstimatedSOLLeft: new(big.Int).Set(c.graduationThreshold),
	}

	if pool == nil || pool.Extra == nil {
		return status
	}

	// 检查 "complete" 标志（已完成毕业迁移）
	if complete, ok := pool.Extra["complete"]; ok {
		if b, ok := complete.(bool); ok && b {
			status.IsGraduated = true
			status.ProgressPercent = 100
			status.EstimatedSOLLeft = big.NewInt(0)

			// 缓存已毕业状态
			c.mu.Lock()
			c.graduated[pool.Address] = true
			c.mu.Unlock()

			return status
		}
	}

	// 读取 "real_sol_reserves"
	if reserves, ok := pool.Extra["real_sol_reserves"]; ok {
		var realReserves *big.Int

		// 支持多种类型（链上解析可能返回不同类型）
		switch v := reserves.(type) {
		case *big.Int:
			realReserves = v
		case int64:
			realReserves = big.NewInt(v)
		case float64:
			realReserves = big.NewInt(int64(v))
		default:
			// 无法解析，返回默认状态
			return status
		}

		status.RealSOLReserves = new(big.Int).Set(realReserves)

		// 计算进度: progress = realReserves / threshold * 100
		if c.graduationThreshold.Sign() > 0 {
			// 使用浮点数计算百分比（教学简化，生产用 big.Rat 或 decimal）
			reservesFloat := new(big.Float).SetInt(realReserves)
			thresholdFloat := new(big.Float).SetInt(c.graduationThreshold)
			ratio := new(big.Float).Quo(reservesFloat, thresholdFloat)
			ratioF64, _ := ratio.Float64()
			status.ProgressPercent = ratioF64 * 100

			// 限制在 0-100 范围内
			if status.ProgressPercent > 100 {
				status.ProgressPercent = 100
			}
			if status.ProgressPercent < 0 {
				status.ProgressPercent = 0
			}
		}

		// 计算剩余所需 SOL
		solLeft := new(big.Int).Sub(c.graduationThreshold, realReserves)
		if solLeft.Sign() < 0 {
			solLeft = big.NewInt(0)
		}
		status.EstimatedSOLLeft = solLeft

		// 判断是否已达到毕业阈值
		if realReserves.Cmp(c.graduationThreshold) >= 0 {
			status.IsGraduated = true

			// 缓存
			c.mu.Lock()
			c.graduated[pool.Address] = true
			c.mu.Unlock()
		}
	}

	return status
}

// IsGraduated 快速检查池子是否已毕业（从缓存读取）。
//
// 这是一个轻量级检查，直接从内存缓存读取，不需要解析 pool.Extra。
// 适用于高频调用场景（如每次报价前的快速过滤）。
//
// 注意: 只有调用过 CheckGraduation 且结果为已毕业的池子才会出现在缓存中。
// 如果池子从未被检查过，此方法返回 false（不代表未毕业）。
func (c *BondingCurveGraduationChecker) IsGraduated(poolAddress string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.graduated[poolAddress]
}
