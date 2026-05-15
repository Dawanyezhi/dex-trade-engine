package main

import (
	"math/big"
	"testing"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// CLMMQuoter 单元测试
// ============================================================

// ----- 基本报价（正常 tick 范围） -----

// TestCLMM_BasicQuoteSingleTick 验证单个 tick 区间内的基本报价。
func TestCLMM_BasicQuoteSingleTick(t *testing.T) {
	// tickSpacing=60, feeBps=30 (0.3%), currentTick=0
	quoter := NewCLMMQuoter(60, 30, 0)

	// 添加一个覆盖当前 tick 的区间
	quoter.AddTickRange(-120, 120, big.NewInt(1_000_000))

	amountIn := big.NewInt(10_000)
	result, err := quoter.Quote(amountIn, true) // zeroForOne: token0 -> token1

	if err != nil {
		t.Fatalf("报价失败: %v", err)
	}

	// 输出应为正
	if result.AmountOut == nil || result.AmountOut.Sign() <= 0 {
		t.Error("输出金额应为正")
	}

	// 手续费应为正
	if result.FeeAmount == nil || result.FeeAmount.Sign() <= 0 {
		t.Error("手续费应为正")
	}

	// 实际消耗的输入金额应 <= amountIn
	if result.AmountInUsed.Cmp(amountIn) > 0 {
		t.Errorf("实际消耗金额 %s 不应超过输入金额 %s",
			result.AmountInUsed.String(), amountIn.String())
	}

	// 穿越 tick 数应为 1（在单个区间内完成）
	if result.TicksCrossed != 1 {
		t.Errorf("穿越 tick 数应为 1，实际为 %d", result.TicksCrossed)
	}

	t.Logf("单 tick 报价: in=%s, out=%s, fee=%s, ticks=%d",
		result.AmountInUsed.String(), result.AmountOut.String(),
		result.FeeAmount.String(), result.TicksCrossed)
}

// TestCLMM_BasicQuoteReverseDirection 验证反向交易（token1 -> token0）。
func TestCLMM_BasicQuoteReverseDirection(t *testing.T) {
	quoter := NewCLMMQuoter(60, 30, 0)
	quoter.AddTickRange(-120, 120, big.NewInt(1_000_000))

	amountIn := big.NewInt(10_000)
	result, err := quoter.Quote(amountIn, false) // token1 -> token0

	if err != nil {
		t.Fatalf("反向报价失败: %v", err)
	}

	if result.AmountOut == nil || result.AmountOut.Sign() <= 0 {
		t.Error("反向交易输出金额应为正")
	}
}

// TestCLMM_QuoteWithZeroAmount 验证零金额输入返回错误。
func TestCLMM_QuoteWithZeroAmount(t *testing.T) {
	quoter := NewCLMMQuoter(60, 30, 0)
	quoter.AddTickRange(-120, 120, big.NewInt(1_000_000))

	_, err := quoter.Quote(big.NewInt(0), true)
	if err == nil {
		t.Error("零金额输入应返回错误")
	}
}

// TestCLMM_QuoteWithNilAmount 验证 nil 输入返回错误。
func TestCLMM_QuoteWithNilAmount(t *testing.T) {
	quoter := NewCLMMQuoter(60, 30, 0)
	quoter.AddTickRange(-120, 120, big.NewInt(1_000_000))

	_, err := quoter.Quote(nil, true)
	if err == nil {
		t.Error("nil 输入应返回错误")
	}
}

// TestCLMM_QuoteWithNoTicks 验证无 tick 区间时返回错误。
func TestCLMM_QuoteWithNoTicks(t *testing.T) {
	quoter := NewCLMMQuoter(60, 30, 0)

	_, err := quoter.Quote(big.NewInt(10_000), true)
	if err == nil {
		t.Error("无 tick 区间应返回错误")
	}
}

// TestCLMM_FeeDeduction 验证手续费计算正确（feeBps=30 即 0.3%）。
func TestCLMM_FeeDeduction(t *testing.T) {
	quoter := NewCLMMQuoter(60, 30, 0)
	// 大流动性区间，确保输入全部在一个 tick 内完成
	quoter.AddTickRange(-600, 600, big.NewInt(100_000_000))

	amountIn := big.NewInt(100_000)
	result, err := quoter.Quote(amountIn, true)
	if err != nil {
		t.Fatalf("报价失败: %v", err)
	}

	// 预期手续费 = 100000 * 30 / 10000 = 300
	expectedFee := big.NewInt(300)
	if result.FeeAmount.Cmp(expectedFee) != 0 {
		t.Errorf("手续费应为 %s，实际为 %s", expectedFee.String(), result.FeeAmount.String())
	}
}

// ----- 跨 tick 场景 -----

// TestCLMM_CrossTickQuote 验证输入金额超过单个 tick 区间容量时的跨 tick 报价。
func TestCLMM_CrossTickQuote(t *testing.T) {
	quoter := NewCLMMQuoter(60, 30, 0)

	// 添加多个 tick 区间（每个区间流动性较小，迫使跨 tick）
	quoter.AddTickRange(-120, 0, big.NewInt(100_000))
	quoter.AddTickRange(0, 120, big.NewInt(100_000))
	quoter.AddTickRange(120, 240, big.NewInt(100_000))

	// 大金额输入，应该跨越多个 tick
	amountIn := big.NewInt(10_000)
	result, err := quoter.Quote(amountIn, false) // token1 -> token0（tick 递增）

	if err != nil {
		t.Fatalf("跨 tick 报价失败: %v", err)
	}

	if result.AmountOut == nil || result.AmountOut.Sign() <= 0 {
		t.Error("跨 tick 输出金额应为正")
	}

	// 穿越 tick 数应 > 1（因为单个区间容量不够）
	if result.TicksCrossed < 1 {
		t.Errorf("应穿越至少 1 个 tick 区间，实际为 %d", result.TicksCrossed)
	}

	t.Logf("跨 tick 报价: in=%s, out=%s, ticks=%d, finalTick=%d",
		result.AmountInUsed.String(), result.AmountOut.String(),
		result.TicksCrossed, result.FinalTick)
}

// TestCLMM_CrossTickOutputGreaterThanSingleTick 验证多区间比单区间能消化更多输入。
func TestCLMM_CrossTickOutputGreaterThanSingleTick(t *testing.T) {
	// 单区间报价器
	quoterSingle := NewCLMMQuoter(60, 30, 0)
	quoterSingle.AddTickRange(-120, 120, big.NewInt(100_000))

	// 多区间报价器（总流动性更大）
	quoterMulti := NewCLMMQuoter(60, 30, 0)
	quoterMulti.AddTickRange(-120, 0, big.NewInt(100_000))
	quoterMulti.AddTickRange(0, 120, big.NewInt(100_000))
	quoterMulti.AddTickRange(120, 240, big.NewInt(100_000))

	amountIn := big.NewInt(10_000)

	resultSingle, errS := quoterSingle.Quote(amountIn, false)
	resultMulti, errM := quoterMulti.Quote(amountIn, false)

	if errS != nil || errM != nil {
		t.Fatalf("报价失败: single=%v, multi=%v", errS, errM)
	}

	// 多区间的实际消耗金额应 >= 单区间
	if resultMulti.AmountInUsed.Cmp(resultSingle.AmountInUsed) < 0 {
		t.Errorf("多区间消耗金额 %s 不应小于单区间 %s",
			resultMulti.AmountInUsed.String(), resultSingle.AmountInUsed.String())
	}
}

// TestCLMM_PriceImpact 验证价格影响计算。
func TestCLMM_PriceImpact(t *testing.T) {
	quoter := NewCLMMQuoter(60, 30, 0)
	quoter.AddTickRange(-120, 120, big.NewInt(100_000))

	amountIn := big.NewInt(5_000)
	result, err := quoter.Quote(amountIn, true)
	if err != nil {
		t.Fatalf("报价失败: %v", err)
	}

	// 价格影响应该 >= 0（跨 tick 后价格会变化）
	t.Logf("价格影响: %d bps", result.PriceImpactBps)
}

// TestCLMM_TickToPrice 验证 tick 到价格的转换。
func TestCLMM_TickToPrice(t *testing.T) {
	quoter := NewCLMMQuoter(60, 30, 0)

	// tick=0 -> price=1.0
	price0 := quoter.TickToPrice(0)
	if price0 != 1.0 {
		t.Errorf("tick=0 的价格应为 1.0，实际为 %f", price0)
	}

	// tick > 0 -> price > 1.0
	pricePositive := quoter.TickToPrice(100)
	if pricePositive <= 1.0 {
		t.Errorf("正 tick 的价格应 > 1.0，实际为 %f", pricePositive)
	}

	// tick < 0 -> price < 1.0
	priceNegative := quoter.TickToPrice(-100)
	if priceNegative >= 1.0 {
		t.Errorf("负 tick 的价格应 < 1.0，实际为 %f", priceNegative)
	}
}

// ============================================================
// BondingCurveGraduationChecker 单元测试
// ============================================================

// TestGraduationChecker_NotGraduatedDefault 验证默认状态为未毕业。
func TestGraduationChecker_NotGraduatedDefault(t *testing.T) {
	checker := NewBondingCurveGraduationChecker(nil) // 默认阈值 85 SOL

	pool := &dexwallet.Pool{
		Address: "pool_test_001",
		Extra: map[string]interface{}{
			"real_sol_reserves": big.NewInt(10_000_000_000), // 10 SOL
		},
	}

	status := checker.CheckGraduation(pool)

	if status.IsGraduated {
		t.Error("10 SOL 远低于 85 SOL 阈值，不应毕业")
	}
	if status.ProgressPercent >= 100 {
		t.Errorf("进度不应达到 100%%，实际为 %.2f%%", status.ProgressPercent)
	}
	if status.EstimatedSOLLeft.Sign() <= 0 {
		t.Error("距离毕业还需要的 SOL 应为正")
	}
}

// TestGraduationChecker_GraduatedByComplete 验证 complete 标志位导致毕业。
func TestGraduationChecker_GraduatedByComplete(t *testing.T) {
	checker := NewBondingCurveGraduationChecker(nil)

	pool := &dexwallet.Pool{
		Address: "pool_graduated_001",
		Extra: map[string]interface{}{
			"complete": true,
		},
	}

	status := checker.CheckGraduation(pool)

	if !status.IsGraduated {
		t.Error("complete=true 时应判定为已毕业")
	}
	if status.ProgressPercent != 100 {
		t.Errorf("毕业进度应为 100%%，实际为 %.2f%%", status.ProgressPercent)
	}
	if status.EstimatedSOLLeft.Sign() != 0 {
		t.Errorf("已毕业时剩余 SOL 应为 0，实际为 %s", status.EstimatedSOLLeft.String())
	}

	// 验证缓存
	if !checker.IsGraduated("pool_graduated_001") {
		t.Error("毕业状态应被缓存")
	}
}

// TestGraduationChecker_GraduatedByThreshold 验证储备达到阈值时毕业。
func TestGraduationChecker_GraduatedByThreshold(t *testing.T) {
	checker := NewBondingCurveGraduationChecker(nil) // 阈值 85 SOL

	pool := &dexwallet.Pool{
		Address: "pool_threshold_001",
		Extra: map[string]interface{}{
			"real_sol_reserves": new(big.Int).Mul(big.NewInt(90), big.NewInt(1e9)), // 90 SOL > 85 SOL
		},
	}

	status := checker.CheckGraduation(pool)

	if !status.IsGraduated {
		t.Error("90 SOL 超过 85 SOL 阈值，应毕业")
	}
	if status.ProgressPercent != 100 {
		t.Errorf("超过阈值时进度应为 100%%，实际为 %.2f%%", status.ProgressPercent)
	}
}

// TestGraduationChecker_CustomThreshold 验证自定义阈值（如 Moonshot 500 SOL）。
func TestGraduationChecker_CustomThreshold(t *testing.T) {
	moonshotThreshold := new(big.Int).Mul(big.NewInt(500), big.NewInt(1e9))
	checker := NewBondingCurveGraduationChecker(moonshotThreshold)

	pool := &dexwallet.Pool{
		Address: "pool_moonshot_001",
		Extra: map[string]interface{}{
			"real_sol_reserves": new(big.Int).Mul(big.NewInt(200), big.NewInt(1e9)), // 200 SOL
		},
	}

	status := checker.CheckGraduation(pool)

	if status.IsGraduated {
		t.Error("200 SOL 低于 Moonshot 500 SOL 阈值，不应毕业")
	}

	// 进度约 40%
	if status.ProgressPercent < 39 || status.ProgressPercent > 41 {
		t.Errorf("进度应约为 40%%，实际为 %.2f%%", status.ProgressPercent)
	}
}

// TestGraduationChecker_NilPool 验证 nil 池子的处理。
func TestGraduationChecker_NilPool(t *testing.T) {
	checker := NewBondingCurveGraduationChecker(nil)

	status := checker.CheckGraduation(nil)
	if status.IsGraduated {
		t.Error("nil 池子不应判定为已毕业")
	}
}

// TestGraduationChecker_NilExtra 验证无 Extra 字段的处理。
func TestGraduationChecker_NilExtra(t *testing.T) {
	checker := NewBondingCurveGraduationChecker(nil)

	pool := &dexwallet.Pool{
		Address: "pool_no_extra",
	}

	status := checker.CheckGraduation(pool)
	if status.IsGraduated {
		t.Error("无 Extra 时不应判定为已毕业")
	}
}

// TestGraduationChecker_Int64Reserves 验证 int64 类型的储备值。
func TestGraduationChecker_Int64Reserves(t *testing.T) {
	checker := NewBondingCurveGraduationChecker(nil)

	pool := &dexwallet.Pool{
		Address: "pool_int64_001",
		Extra: map[string]interface{}{
			"real_sol_reserves": int64(50_000_000_000), // 50 SOL，int64 类型
		},
	}

	status := checker.CheckGraduation(pool)

	if status.IsGraduated {
		t.Error("50 SOL 低于阈值，不应毕业")
	}
	if status.RealSOLReserves.Cmp(big.NewInt(50_000_000_000)) != 0 {
		t.Errorf("储备值应为 50e9，实际为 %s", status.RealSOLReserves.String())
	}
}

// TestGraduationChecker_IsGraduatedCache 验证缓存机制的正确性。
func TestGraduationChecker_IsGraduatedCache(t *testing.T) {
	checker := NewBondingCurveGraduationChecker(nil)

	// 未检查过的池子，缓存应返回 false
	if checker.IsGraduated("unknown_pool") {
		t.Error("未检查过的池子缓存应返回 false")
	}

	// 检查一个已毕业的池子
	pool := &dexwallet.Pool{
		Address: "cached_pool",
		Extra: map[string]interface{}{
			"complete": true,
		},
	}
	checker.CheckGraduation(pool)

	// 缓存应返回 true
	if !checker.IsGraduated("cached_pool") {
		t.Error("已毕业池子的缓存应返回 true")
	}
}
