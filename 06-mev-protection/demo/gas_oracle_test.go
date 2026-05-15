package main

import (
	"testing"
)

// ============================================================
// GasOracle 单元测试
// ============================================================

// ----- UpdateBlock 和 EstimateBaseFee -----

// TestGasOracle_EstimateBaseFeeRisingTrend 验证上升趋势时预测 baseFee * 1.125。
func TestGasOracle_EstimateBaseFeeRisingTrend(t *testing.T) {
	oracle := NewGasOracle(20)

	// 添加上升趋势数据：前半段低，后半段高
	// 前半段平均约 100，后半段平均约 200，200/100 = 2.0 > 1.05 -> rising
	oracle.UpdateBlock(1, 80, nil)
	oracle.UpdateBlock(2, 100, nil)
	oracle.UpdateBlock(3, 120, nil)
	oracle.UpdateBlock(4, 100, nil)
	oracle.UpdateBlock(5, 180, nil)
	oracle.UpdateBlock(6, 200, nil)
	oracle.UpdateBlock(7, 220, nil)
	oracle.UpdateBlock(8, 200, nil)

	trend := oracle.Trend()
	if trend != "rising" {
		t.Errorf("趋势应为 rising，实际为 %s", trend)
	}

	estimated := oracle.EstimateBaseFee()
	// 当前 baseFee = 200（最后一个区块）
	// rising: 200 + 200/8 = 225
	expected := uint64(200 + 200/8)
	if estimated != expected {
		t.Errorf("上升趋势预测 baseFee 应为 %d，实际为 %d", expected, estimated)
	}
}

// TestGasOracle_EstimateBaseFeeFallingTrend 验证下降趋势时预测 baseFee * 0.875。
func TestGasOracle_EstimateBaseFeeFallingTrend(t *testing.T) {
	oracle := NewGasOracle(20)

	// 前半段高，后半段低
	oracle.UpdateBlock(1, 200, nil)
	oracle.UpdateBlock(2, 220, nil)
	oracle.UpdateBlock(3, 200, nil)
	oracle.UpdateBlock(4, 180, nil)
	oracle.UpdateBlock(5, 100, nil)
	oracle.UpdateBlock(6, 80, nil)
	oracle.UpdateBlock(7, 100, nil)
	oracle.UpdateBlock(8, 80, nil)

	trend := oracle.Trend()
	if trend != "falling" {
		t.Errorf("趋势应为 falling，实际为 %s", trend)
	}

	estimated := oracle.EstimateBaseFee()
	// 当前 baseFee = 80（最后一个区块）
	// falling: 80 - 80/8 = 70
	expected := uint64(80 - 80/8)
	if estimated != expected {
		t.Errorf("下降趋势预测 baseFee 应为 %d，实际为 %d", expected, estimated)
	}
}

// TestGasOracle_EstimateBaseFeeStableTrend 验证稳定趋势时预测等于当前值。
func TestGasOracle_EstimateBaseFeeStableTrend(t *testing.T) {
	oracle := NewGasOracle(20)

	// 前后均值基本相同（差距 < 5%）
	oracle.UpdateBlock(1, 100, nil)
	oracle.UpdateBlock(2, 102, nil)
	oracle.UpdateBlock(3, 98, nil)
	oracle.UpdateBlock(4, 100, nil)
	oracle.UpdateBlock(5, 101, nil)
	oracle.UpdateBlock(6, 99, nil)
	oracle.UpdateBlock(7, 100, nil)
	oracle.UpdateBlock(8, 100, nil)

	trend := oracle.Trend()
	if trend != "stable" {
		t.Errorf("趋势应为 stable，实际为 %s", trend)
	}

	estimated := oracle.EstimateBaseFee()
	// stable: 返回当前值 100
	if estimated != 100 {
		t.Errorf("稳定趋势预测 baseFee 应为 100，实际为 %d", estimated)
	}
}

// ----- RecommendPriorityFee 百分位推荐 -----

// TestGasOracle_RecommendPriorityFeePercentiles 验证百分位推荐的正确性。
func TestGasOracle_RecommendPriorityFeePercentiles(t *testing.T) {
	oracle := NewGasOracle(100)

	// 添加有序 tip 样本：100, 200, 300, ..., 1000
	tips := make([]uint64, 10)
	for i := 0; i < 10; i++ {
		tips[i] = uint64((i + 1) * 100)
	}
	oracle.UpdateBlock(1, 100, tips)

	// 25th percentile: index = 10 * 25 / 100 = 2 -> sorted[2] = 300
	low := oracle.RecommendPriorityFee("low")
	if low != 300 {
		t.Errorf("low (25th percentile) 应为 300，实际为 %d", low)
	}

	// 50th percentile: index = 10 * 50 / 100 = 5 -> sorted[5] = 600
	medium := oracle.RecommendPriorityFee("medium")
	if medium != 600 {
		t.Errorf("medium (50th percentile) 应为 600，实际为 %d", medium)
	}

	// 75th percentile: index = 10 * 75 / 100 = 7 -> sorted[7] = 800
	high := oracle.RecommendPriorityFee("high")
	if high != 800 {
		t.Errorf("high (75th percentile) 应为 800，实际为 %d", high)
	}

	t.Logf("推荐 priorityFee: low=%d, medium=%d, high=%d", low, medium, high)
}

// TestGasOracle_RecommendPriorityFeeOrdering 验证 low < medium < high。
func TestGasOracle_RecommendPriorityFeeOrdering(t *testing.T) {
	oracle := NewGasOracle(100)

	tips := make([]uint64, 20)
	for i := 0; i < 20; i++ {
		tips[i] = uint64((i + 1) * 50)
	}
	oracle.UpdateBlock(1, 100, tips)

	low := oracle.RecommendPriorityFee("low")
	medium := oracle.RecommendPriorityFee("medium")
	high := oracle.RecommendPriorityFee("high")

	if !(low <= medium && medium <= high) {
		t.Errorf("应满足 low <= medium <= high，实际 low=%d, medium=%d, high=%d",
			low, medium, high)
	}
}

// TestGasOracle_RecommendPriorityFeeUnknownLevel 验证未知级别默认使用 50th percentile。
func TestGasOracle_RecommendPriorityFeeUnknownLevel(t *testing.T) {
	oracle := NewGasOracle(100)

	tips := []uint64{100, 200, 300, 400, 500}
	oracle.UpdateBlock(1, 100, tips)

	unknown := oracle.RecommendPriorityFee("unknown")
	medium := oracle.RecommendPriorityFee("medium")

	if unknown != medium {
		t.Errorf("未知级别应等于 medium: unknown=%d, medium=%d", unknown, medium)
	}
}

// ----- RecommendMaxFee 计算 -----

// TestGasOracle_RecommendMaxFee 验证 maxFee = estimatedBaseFee * 2 + tip。
func TestGasOracle_RecommendMaxFee(t *testing.T) {
	oracle := NewGasOracle(20)

	// 稳定趋势，baseFee = 100
	oracle.UpdateBlock(1, 100, nil)
	oracle.UpdateBlock(2, 100, nil)
	oracle.UpdateBlock(3, 100, nil)
	oracle.UpdateBlock(4, 100, nil)

	// 添加 tip 样本
	tips := []uint64{10, 20, 30, 40, 50}
	oracle.UpdateBlock(5, 100, tips)

	estimatedBase := oracle.EstimateBaseFee() // stable: 100
	tip := oracle.RecommendPriorityFee("medium")
	maxFee := oracle.RecommendMaxFee("medium")

	expectedMaxFee := estimatedBase*2 + tip
	if maxFee != expectedMaxFee {
		t.Errorf("maxFee 应为 %d (base*2 + tip)，实际为 %d", expectedMaxFee, maxFee)
	}

	t.Logf("RecommendMaxFee: estimatedBase=%d, tip=%d, maxFee=%d",
		estimatedBase, tip, maxFee)
}

// ----- Trend 判断 -----

// TestGasOracle_TrendWithTwoDataPoints 验证只有两个数据点时的趋势判断。
func TestGasOracle_TrendWithTwoDataPoints(t *testing.T) {
	oracle := NewGasOracle(20)

	// 两个数据点：100, 200（后半段 200 > 前半段 100 * 1.05 = 105）
	oracle.UpdateBlock(1, 100, nil)
	oracle.UpdateBlock(2, 200, nil)

	trend := oracle.Trend()
	if trend != "rising" {
		t.Errorf("100 -> 200 应为 rising，实际为 %s", trend)
	}
}

// TestGasOracle_TrendReversesWithNewData 验证新数据可以改变趋势。
func TestGasOracle_TrendReversesWithNewData(t *testing.T) {
	oracle := NewGasOracle(4)

	// 初始上升趋势
	oracle.UpdateBlock(1, 100, nil)
	oracle.UpdateBlock(2, 100, nil)
	oracle.UpdateBlock(3, 200, nil)
	oracle.UpdateBlock(4, 200, nil)

	trend := oracle.Trend()
	if trend != "rising" {
		t.Errorf("初始趋势应为 rising，实际为 %s", trend)
	}

	// 添加下降数据，窗口大小为 4，旧数据会被淘汰
	oracle.UpdateBlock(5, 50, nil)
	oracle.UpdateBlock(6, 50, nil)
	oracle.UpdateBlock(7, 50, nil)
	oracle.UpdateBlock(8, 50, nil)

	trend = oracle.Trend()
	if trend != "stable" {
		t.Errorf("全部 50 时趋势应为 stable，实际为 %s", trend)
	}
}

// ----- 空数据/边界情况 -----

// TestGasOracle_EmptyEstimateBaseFee 验证无数据时返回 0。
func TestGasOracle_EmptyEstimateBaseFee(t *testing.T) {
	oracle := NewGasOracle(20)

	estimated := oracle.EstimateBaseFee()
	if estimated != 0 {
		t.Errorf("无数据时 EstimateBaseFee 应返回 0，实际为 %d", estimated)
	}
}

// TestGasOracle_EmptyRecommendPriorityFee 验证无 tip 样本时返回 0。
func TestGasOracle_EmptyRecommendPriorityFee(t *testing.T) {
	oracle := NewGasOracle(20)

	fee := oracle.RecommendPriorityFee("medium")
	if fee != 0 {
		t.Errorf("无样本时 RecommendPriorityFee 应返回 0，实际为 %d", fee)
	}
}

// TestGasOracle_EmptyTrend 验证无数据时趋势为 stable。
func TestGasOracle_EmptyTrend(t *testing.T) {
	oracle := NewGasOracle(20)

	trend := oracle.Trend()
	if trend != "stable" {
		t.Errorf("无数据时趋势应为 stable，实际为 %s", trend)
	}
}

// TestGasOracle_SingleDataPointTrend 验证只有一个数据点时趋势为 stable。
func TestGasOracle_SingleDataPointTrend(t *testing.T) {
	oracle := NewGasOracle(20)
	oracle.UpdateBlock(1, 100, nil)

	trend := oracle.Trend()
	if trend != "stable" {
		t.Errorf("单数据点时趋势应为 stable，实际为 %s", trend)
	}
}

// TestGasOracle_EstimateBaseFeeOnlyOneBlock 验证只有一个区块时返回当前值。
func TestGasOracle_EstimateBaseFeeOnlyOneBlock(t *testing.T) {
	oracle := NewGasOracle(20)
	oracle.UpdateBlock(1, 150, nil)

	estimated := oracle.EstimateBaseFee()
	// 只有一个数据点 -> stable -> 返回当前值
	if estimated != 150 {
		t.Errorf("单区块时 EstimateBaseFee 应返回 150，实际为 %d", estimated)
	}
}

// TestGasOracle_HistoryWindowLimit 验证历史窗口超限时旧数据被淘汰。
func TestGasOracle_HistoryWindowLimit(t *testing.T) {
	maxHistory := 4
	oracle := NewGasOracle(maxHistory)

	// 添加 6 个区块，超过窗口大小
	oracle.UpdateBlock(1, 100, []uint64{10})
	oracle.UpdateBlock(2, 200, []uint64{20})
	oracle.UpdateBlock(3, 300, []uint64{30})
	oracle.UpdateBlock(4, 400, []uint64{40})
	oracle.UpdateBlock(5, 500, []uint64{50})
	oracle.UpdateBlock(6, 600, []uint64{60})

	// baseFee 历史应只保留最近 4 个: 300, 400, 500, 600
	// 当前 baseFee = 600
	estimated := oracle.EstimateBaseFee()

	// 前半段 [300, 400] 均值 350
	// 后半段 [500, 600] 均值 550
	// 550*100 = 55000 > 350*105 = 36750 -> rising
	// rising: 600 + 600/8 = 675
	expectedEstimate := uint64(600 + 600/8)
	if estimated != expectedEstimate {
		t.Errorf("窗口限制后 EstimateBaseFee 应为 %d，实际为 %d", expectedEstimate, estimated)
	}
}

// TestGasOracle_DefaultMaxHistory 验证 maxHistory <= 0 时使用默认值 20。
func TestGasOracle_DefaultMaxHistory(t *testing.T) {
	oracle := NewGasOracle(0) // 应使用默认值 20

	// 添加 25 个区块
	for i := 1; i <= 25; i++ {
		oracle.UpdateBlock(uint64(i), uint64(i*100), nil)
	}

	// 应该不会 panic，且 baseFee 历史长度不超过 20
	estimated := oracle.EstimateBaseFee()
	if estimated == 0 {
		t.Error("添加数据后 EstimateBaseFee 不应为 0")
	}
}

// TestGasOracle_RecommendMaxFeeNoTips 验证无 tip 样本时 maxFee = estimatedBase * 2。
func TestGasOracle_RecommendMaxFeeNoTips(t *testing.T) {
	oracle := NewGasOracle(20)

	oracle.UpdateBlock(1, 100, nil)
	oracle.UpdateBlock(2, 100, nil)

	maxFee := oracle.RecommendMaxFee("medium")
	estimatedBase := oracle.EstimateBaseFee()

	// tip = 0（无样本）
	expectedMaxFee := estimatedBase * 2
	if maxFee != expectedMaxFee {
		t.Errorf("无 tip 时 maxFee 应为 %d，实际为 %d", expectedMaxFee, maxFee)
	}
}

// TestGasOracle_EstimateGasCost 验证 gas 费用估算。
func TestGasOracle_EstimateGasCost(t *testing.T) {
	oracle := NewGasOracle(20)

	// 稳定趋势
	for i := 1; i <= 4; i++ {
		oracle.UpdateBlock(uint64(i), 100, []uint64{10, 20, 30})
	}

	gasLimit := uint64(21000)
	cost := oracle.EstimateGasCost(gasLimit, "medium")

	estimatedBase := oracle.EstimateBaseFee()
	tip := oracle.RecommendPriorityFee("medium")
	expectedCost := gasLimit * (estimatedBase + tip)

	if cost != expectedCost {
		t.Errorf("EstimateGasCost 应为 %d，实际为 %d", expectedCost, cost)
	}
}

// TestGasOracle_String 验证 String() 输出不为空。
func TestGasOracle_String(t *testing.T) {
	oracle := NewGasOracle(20)
	oracle.UpdateBlock(1, 100, []uint64{10, 20})

	s := oracle.String()
	if s == "" {
		t.Error("String() 不应返回空字符串")
	}
	t.Logf("GasOracle.String(): %s", s)
}

// TestGasOracle_TrendOverflowFixed 验证溢出修复后的趋势判断正确性。
// trendLocked 使用 avgSecond*100 > avgFirst*105 的比较方式，避免溢出。
func TestGasOracle_TrendOverflowFixed(t *testing.T) {
	oracle := NewGasOracle(20)

	// 使用较大的 baseFee 值测试不会溢出
	// 前半段均值 = 10_000_000_000, 后半段均值 = 12_000_000_000
	// 12e9/10e9 = 1.2 > 1.05 -> rising
	oracle.UpdateBlock(1, 10_000_000_000, nil)
	oracle.UpdateBlock(2, 10_000_000_000, nil)
	oracle.UpdateBlock(3, 12_000_000_000, nil)
	oracle.UpdateBlock(4, 12_000_000_000, nil)

	trend := oracle.Trend()
	if trend != "rising" {
		t.Errorf("大数值下趋势应为 rising，实际为 %s", trend)
	}

	// 反向测试：下降
	oracle2 := NewGasOracle(20)
	oracle2.UpdateBlock(1, 12_000_000_000, nil)
	oracle2.UpdateBlock(2, 12_000_000_000, nil)
	oracle2.UpdateBlock(3, 10_000_000_000, nil)
	oracle2.UpdateBlock(4, 10_000_000_000, nil)

	trend2 := oracle2.Trend()
	if trend2 != "falling" {
		t.Errorf("大数值下趋势应为 falling，实际为 %s", trend2)
	}
}
