package main

// ============================================================================
// EVM Gas Oracle -- EVM Gas 预测器
// ============================================================================
//
// EVM Gas 模型（EIP-1559 后）：
//   totalFee = gasUsed * (baseFee + priorityFee)
//   - baseFee: 协议自动调节（区块 gas 使用率 > 50% -> baseFee 上升，反之下降）
//   - priorityFee: 用户设置的小费（给矿工/验证者的激励）
//   - maxFeePerGas: 用户愿意支付的最高单价（baseFee + priorityFee <= maxFeePerGas）
//
// Solana Gas 模型：
//   totalFee = baseFee(固定 5000 lamports) + priorityFee(按 CU 计价)
//   - baseFee 固定不变，priorityFee 完全由市场竞争决定
//   - priorityFee = pricePerCU * requestedCU
//
// 核心差异：
//   - EVM baseFee 动态变化 -> 需要预测下个区块的 baseFee
//   - Solana baseFee 固定 -> 只需要关注 priorityFee 竞争
//
// ============================================================================
// BaseFee 预测算法详解
// ============================================================================
//
// EIP-1559 baseFee 调整公式：
//   如果上个区块 gasUsed > target（15M）:
//     baseFee_new = baseFee_old * (1 + (gasUsed - target) / target / 8)
//     最大涨幅 = 12.5%
//   如果上个区块 gasUsed < target:
//     baseFee_new = baseFee_old * (1 - (target - gasUsed) / target / 8)
//     最大跌幅 = 12.5%
//
// 本 demo 简化为趋势判断 + 线性外推。
// 生产中使用更复杂的时间序列预测（如 EMA、ARIMA）。
//
// ============================================================================
// BSC 非 EIP-1559 兼容说明
// ============================================================================
//
// BSC 使用 Legacy Gas 模型（非 EIP-1559）：
//   totalFee = gasUsed * gasPrice
//   - 没有 baseFee/priorityFee 的概念
//   - gasPrice 由矿工/验证者设置最低值（通常 3 Gwei）
//   - 实际 gasPrice = max(minGasPrice, 用户设置的 gasPrice)
//
// 本 demo 的 GasOracle 主要面向 EIP-1559 链（Ethereum），
// BSC 场景可以直接使用固定 gasPrice 或 PriorityFeeRecommender。
// ============================================================================

import (
	"fmt"
	"sort"
	"sync"
)

// GasOracle EVM Gas 预测器。
// 基于历史区块数据预测下一个区块的 baseFee，并推荐合适的 priorityFee 和 maxFeePerGas。
type GasOracle struct {
	mu sync.RWMutex

	// baseFee 历史（最近 N 个区块）
	baseFeeHistory []uint64
	maxHistory     int

	// priorityFee 样本（最近 N 个区块的交易 tip）
	tipHistory []uint64

	// 当前区块信息
	currentBaseFee uint64
	currentBlock   uint64
}

// NewGasOracle 创建 EVM Gas 预测器。
// maxHistory 控制历史窗口大小，建议 20-128。
func NewGasOracle(maxHistory int) *GasOracle {
	if maxHistory <= 0 {
		maxHistory = 20
	}
	return &GasOracle{
		baseFeeHistory: make([]uint64, 0, maxHistory),
		tipHistory:     make([]uint64, 0, maxHistory),
		maxHistory:     maxHistory,
	}
}

// UpdateBlock 更新区块数据。
// blockNumber: 区块号
// baseFee: 该区块的 baseFee（单位：wei）
// tips: 该区块中所有交易的 priorityFee 样本
func (g *GasOracle) UpdateBlock(blockNumber uint64, baseFee uint64, tips []uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 更新当前区块信息
	g.currentBlock = blockNumber
	g.currentBaseFee = baseFee

	// 追加 baseFee 历史
	g.baseFeeHistory = append(g.baseFeeHistory, baseFee)
	if len(g.baseFeeHistory) > g.maxHistory {
		g.baseFeeHistory = g.baseFeeHistory[len(g.baseFeeHistory)-g.maxHistory:]
	}

	// 追加 tip 样本
	g.tipHistory = append(g.tipHistory, tips...)
	if len(g.tipHistory) > g.maxHistory {
		g.tipHistory = g.tipHistory[len(g.tipHistory)-g.maxHistory:]
	}
}

// EstimateBaseFee 预测下一个区块的 baseFee。
//
// 算法：
//   - 如果最近 baseFee 呈上升趋势 -> 预测 baseFee * 1.125（最大涨幅 12.5%）
//   - 如果最近 baseFee 呈下降趋势 -> 预测 baseFee * 0.875（最大跌幅 12.5%）
//   - 如果稳定 -> 使用当前值
func (g *GasOracle) EstimateBaseFee() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.currentBaseFee == 0 {
		return 0
	}

	trend := g.trendLocked()
	switch trend {
	case "rising":
		// baseFee * 1.125 = baseFee + baseFee/8
		return g.currentBaseFee + g.currentBaseFee/8
	case "falling":
		// baseFee * 0.875 = baseFee - baseFee/8
		return g.currentBaseFee - g.currentBaseFee/8
	default:
		// stable: 使用当前值
		return g.currentBaseFee
	}
}

// RecommendPriorityFee 推荐 priorityFee（tip）。
// 基于历史 tip 样本的百分位数：
//   - "low"    = 25th percentile（省钱，可能排队较久）
//   - "medium" = 50th percentile（平衡选择）
//   - "high"   = 75th percentile（优先处理）
//
// 如果没有样本数据，返回 0。
func (g *GasOracle) RecommendPriorityFee(level string) uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.tipHistory) == 0 {
		return 0
	}

	// 复制并排序 tip 样本
	sorted := make([]uint64, len(g.tipHistory))
	copy(sorted, g.tipHistory)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	var percentile int
	switch level {
	case "low":
		percentile = 25
	case "medium":
		percentile = 50
	case "high":
		percentile = 75
	default:
		percentile = 50
	}

	return getPercentileValue(sorted, percentile)
}

// RecommendMaxFee 推荐 maxFeePerGas。
//
// 计算公式：
//   maxFee = estimatedBaseFee * 2 + recommendedTip
//
// 乘以 2 是为了给 2 个区块的 baseFee 上涨留出余量。
// 如果 baseFee 连续 2 个区块以最大幅度上涨（12.5%），
// 实际 baseFee 最多为 currentBaseFee * 1.125 * 1.125 ≈ currentBaseFee * 1.266，
// 而 estimatedBaseFee * 2 提供了更宽裕的安全边际。
func (g *GasOracle) RecommendMaxFee(level string) uint64 {
	estimatedBase := g.EstimateBaseFee()
	tip := g.RecommendPriorityFee(level)
	return estimatedBase*2 + tip
}

// EstimateGasCost 估算总 gas 费用（单位：wei）。
//
// 计算公式：
//   gasCost = gasLimit * (estimatedBaseFee + recommendedTip)
//
// gasLimit: 交易的 gas 上限（如普通转账 21000，Uniswap swap 约 150000-300000）
func (g *GasOracle) EstimateGasCost(gasLimit uint64, level string) uint64 {
	estimatedBase := g.EstimateBaseFee()
	tip := g.RecommendPriorityFee(level)
	return gasLimit * (estimatedBase + tip)
}

// Trend 返回当前 baseFee 的趋势。
// 返回值：
//   - "rising"  -- baseFee 呈上升趋势
//   - "falling" -- baseFee 呈下降趋势
//   - "stable"  -- baseFee 相对稳定
func (g *GasOracle) Trend() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.trendLocked()
}

// trendLocked 内部趋势判断（调用方需持有读锁）。
//
// 算法：
//   取最近的 baseFee 历史，比较后半段均值与前半段均值。
//   - 后半段均值 > 前半段均值 * 1.05 -> "rising"
//   - 后半段均值 < 前半段均值 * 0.95 -> "falling"
//   - 否则 -> "stable"
func (g *GasOracle) trendLocked() string {
	n := len(g.baseFeeHistory)
	if n < 2 {
		return "stable"
	}

	// 将历史分为前半和后半
	mid := n / 2
	firstHalf := g.baseFeeHistory[:mid]
	secondHalf := g.baseFeeHistory[mid:]

	avgFirst := average(firstHalf)
	avgSecond := average(secondHalf)

	if avgFirst == 0 {
		return "stable"
	}

	// 后半段均值 > 前半段均值 * 1.05 -> 上升趋势
	// 后半段均值 < 前半段均值 * 0.95 -> 下降趋势
	// 使用整数运算避免浮点精度问题：
	//   avgSecond > avgFirst * 105 / 100
	//   avgSecond < avgFirst * 95 / 100
	if avgSecond > avgFirst*105/100 {
		return "rising"
	}
	if avgSecond < avgFirst*95/100 {
		return "falling"
	}
	return "stable"
}

// average 计算 uint64 切片的平均值。
func average(values []uint64) uint64 {
	if len(values) == 0 {
		return 0
	}
	var sum uint64
	for _, v := range values {
		sum += v
	}
	return sum / uint64(len(values))
}

// getPercentileValue 从已排序的切片中获取指定百分位的值。
func getPercentileValue(sorted []uint64, percentile int) uint64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := len(sorted) * percentile / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// String 格式化 GasOracle 当前状态为可读字符串。
func (g *GasOracle) String() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	estimatedBase := uint64(0)
	if g.currentBaseFee > 0 {
		// 手动调用不加锁版本的逻辑来避免死锁
		trend := g.trendLocked()
		switch trend {
		case "rising":
			estimatedBase = g.currentBaseFee + g.currentBaseFee/8
		case "falling":
			estimatedBase = g.currentBaseFee - g.currentBaseFee/8
		default:
			estimatedBase = g.currentBaseFee
		}
	}

	return fmt.Sprintf(
		"GasOracle [区块: %d] 当前baseFee: %d wei, 预测baseFee: %d wei, 趋势: %s, 样本数: %d",
		g.currentBlock,
		g.currentBaseFee,
		estimatedBase,
		g.trendLocked(),
		len(g.tipHistory),
	)
}
