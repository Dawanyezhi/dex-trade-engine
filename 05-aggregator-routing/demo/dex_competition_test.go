package main

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// SplitMix64 灰度哈希测试
// ============================================================

// TestCompetition_HashQidDeterministic 验证 hashQid 的确定性：同一输入始终产生同一输出。
func TestCompetition_HashQidDeterministic(t *testing.T) {
	testCases := []uint64{0, 1, 42, 12345678, 1<<63 - 1, ^uint64(0)}

	for _, qid := range testCases {
		first := hashQid(qid)
		for i := 0; i < 100; i++ {
			got := hashQid(qid)
			if got != first {
				t.Errorf("hashQid(%d) 不确定: 第一次=%d, 第 %d 次=%d", qid, first, i+1, got)
			}
		}
	}
}

// TestCompetition_HashQidRange 验证 hashQid 输出范围在 [0, 100)。
func TestCompetition_HashQidRange(t *testing.T) {
	for qid := uint64(0); qid < 10000; qid++ {
		h := hashQid(qid)
		if h >= 100 {
			t.Errorf("hashQid(%d)=%d, 超出 [0,100) 范围", qid, h)
		}
	}
}

// TestCompetition_HashQidDistribution 验证 hashQid 在 [0,100) 上的分布均匀性。
// 使用卡方检验，期望每个桶约 N/100 个样本。
func TestCompetition_HashQidDistribution(t *testing.T) {
	const N = 100000
	var buckets [100]int

	for i := uint64(0); i < N; i++ {
		buckets[hashQid(i)]++
	}

	expected := float64(N) / 100.0
	var chiSquare float64
	for _, count := range buckets {
		diff := float64(count) - expected
		chiSquare += (diff * diff) / expected
	}

	// 卡方检验临界值（自由度 99，显著性水平 0.01）约 135.8
	// 使用更宽松的阈值 200 以避免 CI 中的偶发失败
	if chiSquare > 200 {
		t.Errorf("分布不均匀: 卡方统计量=%.2f (阈值=200), 说明 hashQid 分布有偏差", chiSquare)
	}
}

// TestCompetition_HashQidSnowflakeIDs 验证雪花 ID 风格的输入（高位时间戳+低位序列号）也能良好散列。
func TestCompetition_HashQidSnowflakeIDs(t *testing.T) {
	const N = 10000
	var buckets [100]int

	// 模拟雪花 ID：高位固定时间戳，低位递增序列号
	baseTimestamp := uint64(1700000000000) << 22 // 41 位时间戳左移
	for i := uint64(0); i < N; i++ {
		snowflakeID := baseTimestamp | (i & 0x3FFFFF) // 低 22 位序列号
		buckets[hashQid(snowflakeID)]++
	}

	expected := float64(N) / 100.0
	var chiSquare float64
	for _, count := range buckets {
		diff := float64(count) - expected
		chiSquare += (diff * diff) / expected
	}

	if chiSquare > 200 {
		t.Errorf("雪花 ID 分布不均匀: 卡方统计量=%.2f", chiSquare)
	}
}

// TestCompetition_ShouldUseGrayscaleBoundary 验证 shouldUseGrayscale 的边界条件。
func TestCompetition_ShouldUseGrayscaleBoundary(t *testing.T) {
	// percentage <= 0 始终返回 false
	for qid := uint64(0); qid < 100; qid++ {
		if shouldUseGrayscale(qid, 0) {
			t.Errorf("shouldUseGrayscale(%d, 0) 应返回 false", qid)
		}
		if shouldUseGrayscale(qid, -10) {
			t.Errorf("shouldUseGrayscale(%d, -10) 应返回 false", qid)
		}
	}

	// percentage >= 100 始终返回 true
	for qid := uint64(0); qid < 100; qid++ {
		if !shouldUseGrayscale(qid, 100) {
			t.Errorf("shouldUseGrayscale(%d, 100) 应返回 true", qid)
		}
		if !shouldUseGrayscale(qid, 200) {
			t.Errorf("shouldUseGrayscale(%d, 200) 应返回 true", qid)
		}
	}
}

// TestCompetition_ShouldUseGrayscaleRate 验证中间灰度百分比的通过率。
func TestCompetition_ShouldUseGrayscaleRate(t *testing.T) {
	testCases := []struct {
		percentage int
		tolerance  float64 // 允许误差百分比
	}{
		{10, 5},
		{30, 5},
		{50, 5},
		{70, 5},
		{90, 5},
	}

	const N = 100000
	for _, tc := range testCases {
		passCount := 0
		for i := uint64(0); i < N; i++ {
			if shouldUseGrayscale(i, tc.percentage) {
				passCount++
			}
		}
		actualRate := float64(passCount) / float64(N) * 100
		if math.Abs(actualRate-float64(tc.percentage)) > tc.tolerance {
			t.Errorf("灰度 %d%%: 实际通过率=%.1f%%, 偏差超过 %.1f%%",
				tc.percentage, actualRate, tc.tolerance)
		}
	}
}

// ============================================================
// 竞争测试辅助
// ============================================================

// competitionMockBuilder 用于 DexCompetition 测试的 mock SwapBuilder。
// 支持配置延迟、输出金额和自定义错误。
type competitionMockBuilder struct {
	dexID      dexwallet.DexID
	latency    time.Duration
	output     *big.Int
	err        error
	buildCount atomic.Int64
}

func (b *competitionMockBuilder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	b.buildCount.Add(1)
	select {
	case <-time.After(b.latency):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if b.err != nil {
		return nil, b.err
	}
	return &dexwallet.SwapResult{
		DexID:        b.dexID,
		ChainID:      coinset.ChainSolana,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: new(big.Int).Set(b.output),
		MinOutput:    new(big.Int).Set(b.output),
		GasCost:      big.NewInt(50000),
		TxData:       []byte(fmt.Sprintf("mock_tx_%s", b.dexID)),
	}, nil
}

func (b *competitionMockBuilder) DexID() dexwallet.DexID       { return b.dexID }
func (b *competitionMockBuilder) ChainID() coinset.ChainID     { return coinset.ChainSolana }
func (b *competitionMockBuilder) ProtocolType() dexwallet.ProtocolType { return dexwallet.ProtocolAMM }
func (b *competitionMockBuilder) Simulate(_ context.Context, _ []byte) error { return nil }
func (b *competitionMockBuilder) Label() string { return fmt.Sprintf("mock_%s", b.dexID) }

// newCompetitionTestRequest 创建带 qid 的测试请求。
func newCompetitionTestRequest(qid uint64) dexwallet.SwapRequest {
	req := newTestSwapRequest()
	req.Extra = map[string]interface{}{"qid": qid}
	return req
}

// ============================================================
// DexCompetition 完整竞争流程测试
// ============================================================

// TestCompetition_AllSuccess 所有 DEX 成功，应选择高优先级。
func TestCompetition_AllSuccess(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexPumpFun,
				latency: 10 * time.Millisecond,
				output:  big.NewInt(900),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 10 * time.Millisecond,
				output:  big.NewInt(950),
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexJupiter,
				latency: 10 * time.Millisecond,
				output:  big.NewInt(980),
			},
			Priority:  dexwallet.PriorityLow,
			DexID:     dexwallet.DexJupiter,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	req := newCompetitionTestRequest(12345)
	result, err := comp.Run(context.Background(), req)

	if err != nil {
		t.Fatalf("期望成功, 实际错误: %v", err)
	}
	if result == nil {
		t.Fatal("期望非空结果")
	}
	// 高优先级 PumpFun 应被选中（即使输出金额不是最高）
	if result.DexID != dexwallet.DexPumpFun {
		t.Errorf("期望选中 PumpFun (高优先级), 实际=%s", result.DexID)
	}
	if result.OutputAmount.Cmp(big.NewInt(900)) != 0 {
		t.Errorf("期望输出=900, 实际=%s", result.OutputAmount.String())
	}
}

// TestCompetition_PartialFailure_HighFails 高优先级失败，中优先级成功时选中中优先级。
func TestCompetition_PartialFailure_HighFails(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexPumpFun,
				latency: 5 * time.Millisecond,
				err:     fmt.Errorf("mock: PumpFun 不可用"),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 10 * time.Millisecond,
				output:  big.NewInt(950),
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexJupiter,
				latency: 10 * time.Millisecond,
				output:  big.NewInt(980),
			},
			Priority:  dexwallet.PriorityLow,
			DexID:     dexwallet.DexJupiter,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	result, err := comp.Run(context.Background(), newCompetitionTestRequest(100))
	if err != nil {
		t.Fatalf("期望降级成功, 实际错误: %v", err)
	}
	if result.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("高优先级失败后期望选中 Raydium (中优先级), 实际=%s", result.DexID)
	}
}

// TestCompetition_PartialFailure_HighAndMiddleFail 高/中优先级都失败，低优先级成功。
func TestCompetition_PartialFailure_HighAndMiddleFail(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexPumpFun,
				latency: 5 * time.Millisecond,
				err:     fmt.Errorf("mock: PumpFun 不可用"),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 5 * time.Millisecond,
				err:     fmt.Errorf("mock: Raydium 不可用"),
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexJupiter,
				latency: 10 * time.Millisecond,
				output:  big.NewInt(980),
			},
			Priority:  dexwallet.PriorityLow,
			DexID:     dexwallet.DexJupiter,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	result, err := comp.Run(context.Background(), newCompetitionTestRequest(200))
	if err != nil {
		t.Fatalf("期望降级到低优先级成功, 实际错误: %v", err)
	}
	if result.DexID != dexwallet.DexJupiter {
		t.Errorf("高/中优先级都失败后期望选中 Jupiter (低优先级), 实际=%s", result.DexID)
	}
}

// TestCompetition_AllFailure 所有 DEX 失败，应返回错误。
func TestCompetition_AllFailure(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexPumpFun,
				latency: 5 * time.Millisecond,
				err:     fmt.Errorf("mock: PumpFun 失败"),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 5 * time.Millisecond,
				err:     fmt.Errorf("mock: Raydium 失败"),
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexJupiter,
				latency: 5 * time.Millisecond,
				err:     fmt.Errorf("mock: Jupiter 失败"),
			},
			Priority:  dexwallet.PriorityLow,
			DexID:     dexwallet.DexJupiter,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	_, err := comp.Run(context.Background(), newCompetitionTestRequest(300))
	if err == nil {
		t.Fatal("期望所有 DEX 失败时返回错误, 实际返回 nil")
	}
}

// ============================================================
// 优先级选择逻辑测试
// ============================================================

// TestCompetition_HighPriorityPreempts 高优先级即使后返回也应被优先选中。
// 场景：低优先级先返回成功结果，高优先级随后也成功，应选高优先级。
func TestCompetition_HighPriorityPreempts(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexPumpFun,
				latency: 50 * time.Millisecond, // 高优先级延迟高
				output:  big.NewInt(900),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexJupiter,
				latency: 5 * time.Millisecond, // 低优先级很快返回
				output:  big.NewInt(980),
			},
			Priority:  dexwallet.PriorityLow,
			DexID:     dexwallet.DexJupiter,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	result, err := comp.Run(context.Background(), newCompetitionTestRequest(400))
	if err != nil {
		t.Fatalf("期望成功, 实际错误: %v", err)
	}
	// 即使 Jupiter 先返回，高优先级 PumpFun 成功后应被选中
	if result.DexID != dexwallet.DexPumpFun {
		t.Errorf("期望高优先级 PumpFun 抢占选中, 实际=%s", result.DexID)
	}
}

// TestCompetition_MiddleSelectedWhenHighAllDone 高优先级全部完成且失败时选中中优先级。
func TestCompetition_MiddleSelectedWhenHighAllDone(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   "pump_fun_1",
				latency: 5 * time.Millisecond,
				err:     fmt.Errorf("失败1"),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     "pump_fun_1",
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   "pump_fun_2",
				latency: 5 * time.Millisecond,
				err:     fmt.Errorf("失败2"),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     "pump_fun_2",
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 3 * time.Millisecond, // 中优先级可能先返回
				output:  big.NewInt(950),
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	result, err := comp.Run(context.Background(), newCompetitionTestRequest(500))
	if err != nil {
		t.Fatalf("期望中优先级降级成功, 实际错误: %v", err)
	}
	if result.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("所有高优先级完成且失败后期望选中 Raydium, 实际=%s", result.DexID)
	}
}

// ============================================================
// 快速失败（Fast-fail）规则测试
// ============================================================

// TestCompetition_FastFail_InsufficientBalance 余额不足错误触发快速失败。
func TestCompetition_FastFail_InsufficientBalance(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexPumpFun,
				latency: 5 * time.Millisecond,
				err: &dexwallet.InsufficientBalanceError{
					Required:  "1000000000",
					Available: "500000000",
					Token:     "SOL",
				},
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 100 * time.Millisecond, // 较慢
				output:  big.NewInt(950),
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	start := time.Now()
	_, err := comp.Run(context.Background(), newCompetitionTestRequest(600))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("期望快速失败返回错误, 实际返回 nil")
	}

	// 快速失败应该在很短时间内返回，不应等待 Raydium
	if elapsed > 500*time.Millisecond {
		t.Errorf("快速失败耗时过长: %v, 说明未及时终止竞争", elapsed)
	}
}

// TestCompetition_FastFail_SlippageExceeded 滑点溢出错误触发快速失败。
func TestCompetition_FastFail_SlippageExceeded(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 5 * time.Millisecond,
				err: &dexwallet.SlippageExceededError{
					Actual:    500,
					Threshold: 200,
				},
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexJupiter,
				latency: 100 * time.Millisecond,
				output:  big.NewInt(980),
			},
			Priority:  dexwallet.PriorityLow,
			DexID:     dexwallet.DexJupiter,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	_, err := comp.Run(context.Background(), newCompetitionTestRequest(700))
	if err == nil {
		t.Fatal("滑点溢出应触发快速失败")
	}
}

// TestCompetition_FastFail_LowPriorityNotTrigger 低优先级的致命错误不触发快速失败。
func TestCompetition_FastFail_LowPriorityNotTrigger(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexJupiter,
				latency: 5 * time.Millisecond,
				err: &dexwallet.InsufficientBalanceError{
					Required:  "1000000000",
					Available: "500000000",
					Token:     "SOL",
				},
			},
			Priority:  dexwallet.PriorityLow, // 低优先级
			DexID:     dexwallet.DexJupiter,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 20 * time.Millisecond,
				output:  big.NewInt(950),
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	result, err := comp.Run(context.Background(), newCompetitionTestRequest(800))
	if err != nil {
		t.Fatalf("低优先级致命错误不应触发快速失败, 实际错误: %v", err)
	}
	if result.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("期望 Raydium 成功, 实际=%s", result.DexID)
	}
}

// TestCompetition_IsCriticalError 验证 isCriticalError 函数的判断逻辑。
func TestCompetition_IsCriticalError(t *testing.T) {
	balErr := &dexwallet.InsufficientBalanceError{Required: "100", Available: "50", Token: "SOL"}
	slipErr := &dexwallet.SlippageExceededError{Actual: 500, Threshold: 200}
	nonDegErr := &dexwallet.NonDegradableError{Reason: "参数无效"}
	normalErr := fmt.Errorf("普通网络错误")

	tests := []struct {
		name     string
		err      error
		priority dexwallet.DexPriority
		want     bool
	}{
		{"高优先级+余额不足", balErr, dexwallet.PriorityHigh, true},
		{"中优先级+余额不足", balErr, dexwallet.PriorityMedium, true},
		{"低优先级+余额不足", balErr, dexwallet.PriorityLow, false}, // 低优先级不触发
		{"高优先级+滑点溢出", slipErr, dexwallet.PriorityHigh, true},
		{"中优先级+滑点溢出", slipErr, dexwallet.PriorityMedium, true},
		{"低优先级+滑点溢出", slipErr, dexwallet.PriorityLow, false},
		{"高优先级+不可降级", nonDegErr, dexwallet.PriorityHigh, true},
		{"低优先级+不可降级", nonDegErr, dexwallet.PriorityLow, false},
		{"高优先级+普通错误", normalErr, dexwallet.PriorityHigh, false},
		{"中优先级+普通错误", normalErr, dexwallet.PriorityMedium, false},
		{"低优先级+普通错误", normalErr, dexwallet.PriorityLow, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCriticalError(tt.err, tt.priority)
			if got != tt.want {
				t.Errorf("isCriticalError(%v, %d) = %v, 期望 %v", tt.err, tt.priority, got, tt.want)
			}
		})
	}
}

// ============================================================
// 灰度过滤测试
// ============================================================

// TestCompetition_GrayscaleFilter 灰度为 0 的 DEX 不参与竞争。
func TestCompetition_GrayscaleFilter(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexPumpFun,
				latency: 5 * time.Millisecond,
				output:  big.NewInt(900),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 0, // 灰度为 0，不参与
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 5 * time.Millisecond,
				output:  big.NewInt(950),
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	result, err := comp.Run(context.Background(), newCompetitionTestRequest(900))
	if err != nil {
		t.Fatalf("期望成功, 实际错误: %v", err)
	}
	// PumpFun 被灰度过滤，应选中 Raydium
	if result.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("灰度为 0 的 DEX 不应参与, 期望选中 Raydium, 实际=%s", result.DexID)
	}
}

// TestCompetition_AllGrayscaleZero 所有 DEX 灰度为 0 应返回错误。
func TestCompetition_AllGrayscaleZero(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:  dexwallet.DexPumpFun,
				output: big.NewInt(900),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 0,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	_, err := comp.Run(context.Background(), newCompetitionTestRequest(1000))
	if err == nil {
		t.Fatal("所有 DEX 灰度为 0 应返回错误")
	}
}

// ============================================================
// 并发安全性测试
// ============================================================

// TestCompetition_ConcurrentRun 多个 goroutine 同时执行竞争，不应 panic。
func TestCompetition_ConcurrentRun(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexPumpFun,
				latency: 5 * time.Millisecond,
				output:  big.NewInt(900),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexRaydiumAMM,
				latency: 5 * time.Millisecond,
				output:  big.NewInt(950),
			},
			Priority:  dexwallet.PriorityMedium,
			DexID:     dexwallet.DexRaydiumAMM,
			Grayscale: 100,
		},
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexJupiter,
				latency: 5 * time.Millisecond,
				output:  big.NewInt(980),
			},
			Priority:  dexwallet.PriorityLow,
			DexID:     dexwallet.DexJupiter,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 2 * time.Second}
	comp := NewDexCompetition(config, entries)

	var wg sync.WaitGroup
	var successCount atomic.Int64
	var errorCount atomic.Int64

	concurrency := 50
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := newCompetitionTestRequest(uint64(idx))
			result, err := comp.Run(context.Background(), req)
			if err != nil {
				errorCount.Add(1)
				return
			}
			if result != nil && result.OutputAmount.Sign() > 0 {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if successCount.Load() != int64(concurrency) {
		t.Errorf("期望 %d 次全部成功, 实际成功=%d, 失败=%d",
			concurrency, successCount.Load(), errorCount.Load())
	}
}

// TestCompetition_CompetitionStateConcurrent 并发操作 competitionState 不应 data race。
func TestCompetition_CompetitionStateConcurrent(t *testing.T) {
	entries := []DexCompetitionEntry{
		{Priority: dexwallet.PriorityHigh, DexID: "h1"},
		{Priority: dexwallet.PriorityHigh, DexID: "h2"},
		{Priority: dexwallet.PriorityMedium, DexID: "m1"},
		{Priority: dexwallet.PriorityLow, DexID: "l1"},
	}
	state := newCompetitionState(entries)

	var wg sync.WaitGroup

	// 并发记录成功
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result := &dexwallet.SwapResult{
				OutputAmount: big.NewInt(int64(idx * 100)),
				DexID:        dexwallet.DexID(fmt.Sprintf("dex_%d", idx)),
			}
			state.recordSuccess(dexwallet.PriorityHigh, result, result.DexID)
		}(i)
	}

	// 并发记录错误
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			state.recordError(fmt.Errorf("error_%d", idx))
		}(i)
	}

	// 并发尝试选择
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state.trySelect()
		}()
	}

	// 并发读取 remaining
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = state.totalRemaining()
		}()
	}

	wg.Wait()
	// 能跑到这里不 panic/data race 就说明并发安全
}

// ============================================================
// extractQID 测试
// ============================================================

// TestCompetition_ExtractQID 测试从请求中提取 qid。
func TestCompetition_ExtractQID(t *testing.T) {
	tests := []struct {
		name     string
		extra    map[string]interface{}
		expected uint64
	}{
		{"nil Extra", nil, 0},
		{"无 qid 字段", map[string]interface{}{"other": 123}, 0},
		{"uint64 类型", map[string]interface{}{"qid": uint64(42)}, 42},
		{"int64 类型", map[string]interface{}{"qid": int64(42)}, 42},
		{"float64 类型", map[string]interface{}{"qid": float64(42)}, 42},
		{"int 类型", map[string]interface{}{"qid": int(42)}, 42},
		{"string 类型(不支持)", map[string]interface{}{"qid": "42"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := dexwallet.SwapRequest{Extra: tt.extra}
			got := extractQID(req)
			if got != tt.expected {
				t.Errorf("extractQID() = %d, 期望 %d", got, tt.expected)
			}
		})
	}
}

// ============================================================
// 超时测试
// ============================================================

// TestCompetition_Timeout 总超时到达时应返回错误或已有结果。
func TestCompetition_Timeout(t *testing.T) {
	entries := []DexCompetitionEntry{
		{
			Builder: &competitionMockBuilder{
				dexID:   dexwallet.DexPumpFun,
				latency: 5 * time.Second, // 远超超时
				output:  big.NewInt(900),
			},
			Priority:  dexwallet.PriorityHigh,
			DexID:     dexwallet.DexPumpFun,
			Grayscale: 100,
		},
	}

	config := DexCompetitionConfig{TotalTimeout: 100 * time.Millisecond}
	comp := NewDexCompetition(config, entries)

	start := time.Now()
	_, err := comp.Run(context.Background(), newCompetitionTestRequest(1100))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("超时后应返回错误")
	}

	// 应在 100ms 左右返回，允许合理偏差
	if elapsed > 500*time.Millisecond {
		t.Errorf("超时未生效, 耗时 %v", elapsed)
	}
}

// ============================================================
// 默认配置测试
// ============================================================

// TestCompetition_DefaultConfig 验证默认配置的正确性。
func TestCompetition_DefaultConfig(t *testing.T) {
	config := DefaultDexCompetitionConfig()
	if config.TotalTimeout != 5*time.Second {
		t.Errorf("默认超时期望=5s, 实际=%v", config.TotalTimeout)
	}
}

// TestCompetition_InvalidTimeoutUsesDefault 无效超时应使用默认值。
func TestCompetition_InvalidTimeoutUsesDefault(t *testing.T) {
	comp := NewDexCompetition(DexCompetitionConfig{TotalTimeout: 0}, nil)
	if comp.config.TotalTimeout != 5*time.Second {
		t.Errorf("超时为 0 时应使用默认 5s, 实际=%v", comp.config.TotalTimeout)
	}

	comp2 := NewDexCompetition(DexCompetitionConfig{TotalTimeout: -1 * time.Second}, nil)
	if comp2.config.TotalTimeout != 5*time.Second {
		t.Errorf("超时为负数时应使用默认 5s, 实际=%v", comp2.config.TotalTimeout)
	}
}
