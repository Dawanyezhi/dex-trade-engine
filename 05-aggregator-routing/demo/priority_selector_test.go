package main

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// MockPriorityBuilder -- 用于 PrioritySelector 测试的 Mock Builder
// ============================================================
//
// 与 MockSwapBuilder 不同，MockPriorityBuilder 支持：
//   - 可配置延迟（模拟不同 DEX 的响应时间）
//   - 可配置错误类型（DegradableError / NonDegradableError）
//   - 不依赖 Simulate/Label（PrioritySelector 只调用 Build）

// MockPriorityBuilder 用于优先级选择器测试的 Mock 构建器。
type MockPriorityBuilder struct {
	dexID    dexwallet.DexID
	chainID  coinset.ChainID
	protocol dexwallet.ProtocolType
	latency  time.Duration // 模拟构建延迟
	err      error         // 返回的错误（nil 表示成功）
}

// NewMockPriorityBuilder 创建 Mock 构建器。
func NewMockPriorityBuilder(
	dexID dexwallet.DexID,
	latency time.Duration,
	err error,
) *MockPriorityBuilder {
	return &MockPriorityBuilder{
		dexID:    dexID,
		chainID:  coinset.ChainSolana,
		protocol: dexwallet.ProtocolAMM,
		latency:  latency,
		err:      err,
	}
}

// Build 构建 Swap 交易（模拟延迟和错误）。
func (b *MockPriorityBuilder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	// 模拟延迟，同时尊重 context 取消
	select {
	case <-time.After(b.latency):
	case <-ctx.Done():
		return nil, &dexwallet.DegradableError{
			DexID:  b.dexID,
			Reason: "context cancelled",
			Cause:  ctx.Err(),
		}
	}

	if b.err != nil {
		return nil, b.err
	}

	// 模拟成功结果
	outputAmount := new(big.Int).Mul(req.Amount, big.NewInt(95))
	outputAmount.Div(outputAmount, big.NewInt(100))

	return &dexwallet.SwapResult{
		DexID:        b.dexID,
		ChainID:      b.chainID,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: outputAmount,
		MinOutput:    new(big.Int).Set(outputAmount),
		SlippageBps:  req.SlippageBps,
		TxData:       []byte(fmt.Sprintf("mock_priority_tx_%s", b.dexID)),
	}, nil
}

func (b *MockPriorityBuilder) DexID() dexwallet.DexID        { return b.dexID }
func (b *MockPriorityBuilder) ChainID() coinset.ChainID      { return b.chainID }
func (b *MockPriorityBuilder) ProtocolType() dexwallet.ProtocolType { return b.protocol }
func (b *MockPriorityBuilder) Simulate(_ context.Context, _ []byte) error { return nil }
func (b *MockPriorityBuilder) Label() string                  { return fmt.Sprintf("mock_%s", b.dexID) }

// ============================================================
// 辅助函数
// ============================================================

// newPrioritySelectorTestReq 创建测试用 SwapRequest。
func newPrioritySelectorTestReq() dexwallet.SwapRequest {
	return dexwallet.SwapRequest{
		ChainID:   coinset.ChainSolana,
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{
			Address:  "So11111111111111111111111111111111111111112",
			Symbol:   "SOL",
			Decimals: 9,
			ChainID:  coinset.ChainSolana,
		},
		ToToken: dexwallet.Token{
			Address:  "MEMExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			Symbol:   "MEME",
			Decimals: 6,
			ChainID:  coinset.ChainSolana,
		},
		Amount:      new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9)), // 1 SOL
		SlippageBps: 200,
		Sender:      "SenderPubkey111111111111111111111111111111",
		Recipient:   "SenderPubkey111111111111111111111111111111",
	}
}

// ============================================================
// 测试: 高优先级层胜出
// ============================================================

// TestPrioritySelector_HighTierWins 验证高优先级 DEX 在超时内返回时直接胜出，
// 不会等待中/低优先级层。
func TestPrioritySelector_HighTierWins(t *testing.T) {
	// 高优先级: PumpFun 10ms 成功，Raydium CPMM 20ms 成功
	// 中优先级: Raydium CLMM 5ms 成功（更快，但优先级低，不应被选中）
	// 低优先级: Jupiter 1ms 成功（最快，但优先级最低）
	entries := []PrioritySelectorEntry{
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexPumpFun, 10*time.Millisecond, nil),
			Priority: dexwallet.PriorityHigh,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCPMM, 20*time.Millisecond, nil),
			Priority: dexwallet.PriorityHigh,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCLMM, 5*time.Millisecond, nil),
			Priority: dexwallet.PriorityMedium,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexJupiter, 1*time.Millisecond, nil),
			Priority: dexwallet.PriorityLow,
		},
	}

	config := PrioritySelectorConfig{
		HighTierTimeout:   100 * time.Millisecond, // 充足的超时
		MediumTierTimeout: 200 * time.Millisecond,
		LowTierTimeout:    3 * time.Second,
	}

	selector := NewPrioritySelector(config, entries)
	ctx := context.Background()
	req := newPrioritySelectorTestReq()

	result, err := selector.Select(ctx, req)
	if err != nil {
		t.Fatalf("期望成功，实际错误: %v", err)
	}

	// 应该是高优先级层中最快的 PumpFun（10ms < 20ms）
	if result.DexID != dexwallet.DexPumpFun {
		t.Errorf("期望 PumpFun 胜出（高优先级层最快），实际: %s", result.DexID)
	}

	// 验证统计
	stats := selector.Stats().Snapshot()
	if stats.TotalCalls != 1 {
		t.Errorf("期望 1 次调用，实际: %d", stats.TotalCalls)
	}
	if stats.TotalSuccess != 1 {
		t.Errorf("期望 1 次成功，实际: %d", stats.TotalSuccess)
	}
	if stats.TierReached[dexwallet.PriorityHigh] != 1 {
		t.Errorf("期望高优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityHigh])
	}
	// 中/低优先级层不应被访问
	if stats.TierReached[dexwallet.PriorityMedium] != 0 {
		t.Errorf("中优先级层不应被访问，实际: %d", stats.TierReached[dexwallet.PriorityMedium])
	}
	if stats.TierReached[dexwallet.PriorityLow] != 0 {
		t.Errorf("低优先级层不应被访问，实际: %d", stats.TierReached[dexwallet.PriorityLow])
	}
	if stats.WinsByDex[dexwallet.DexPumpFun] != 1 {
		t.Errorf("期望 PumpFun 胜出 1 次，实际: %d", stats.WinsByDex[dexwallet.DexPumpFun])
	}
}

// ============================================================
// 测试: 高优先级失败，降级到中优先级
// ============================================================

// TestPrioritySelector_FallbackToMedium 验证高优先级层全部失败（可降级错误）时，
// 自动降级到中优先级层。
func TestPrioritySelector_FallbackToMedium(t *testing.T) {
	// 高优先级: 两个都返回可降级错误
	highErr := &dexwallet.DegradableError{
		DexID:  dexwallet.DexPumpFun,
		Reason: "代币已毕业，内盘不可用",
	}
	highErr2 := &dexwallet.DegradableError{
		DexID:  dexwallet.DexRaydiumCPMM,
		Reason: "池子未找到",
	}

	// 中优先级: Orca 15ms 成功
	entries := []PrioritySelectorEntry{
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexPumpFun, 5*time.Millisecond, highErr),
			Priority: dexwallet.PriorityHigh,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCPMM, 5*time.Millisecond, highErr2),
			Priority: dexwallet.PriorityHigh,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCLMM, 15*time.Millisecond, nil),
			Priority: dexwallet.PriorityMedium,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexJupiter, 10*time.Millisecond, nil),
			Priority: dexwallet.PriorityLow,
		},
	}

	config := PrioritySelectorConfig{
		HighTierTimeout:   100 * time.Millisecond,
		MediumTierTimeout: 200 * time.Millisecond,
		LowTierTimeout:    3 * time.Second,
	}

	selector := NewPrioritySelector(config, entries)
	ctx := context.Background()
	req := newPrioritySelectorTestReq()

	result, err := selector.Select(ctx, req)
	if err != nil {
		t.Fatalf("期望降级成功，实际错误: %v", err)
	}

	// 应该选中中优先级的 Raydium CLMM
	if result.DexID != dexwallet.DexRaydiumCLMM {
		t.Errorf("期望降级到 Raydium CLMM，实际: %s", result.DexID)
	}

	// 验证统计：高优先级和中优先级层都被访问
	stats := selector.Stats().Snapshot()
	if stats.TierReached[dexwallet.PriorityHigh] != 1 {
		t.Errorf("期望高优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityHigh])
	}
	if stats.TierReached[dexwallet.PriorityMedium] != 1 {
		t.Errorf("期望中优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityMedium])
	}
	// 低优先级层不应被访问（中优先级已成功）
	if stats.TierReached[dexwallet.PriorityLow] != 0 {
		t.Errorf("低优先级层不应被访问，实际: %d", stats.TierReached[dexwallet.PriorityLow])
	}
}

// ============================================================
// 测试: 不可降级错误立即终止
// ============================================================

// TestPrioritySelector_NonDegradableStops 验证余额不足等不可降级错误
// 导致立即返回，不尝试下一层。
func TestPrioritySelector_NonDegradableStops(t *testing.T) {
	// 高优先级: PumpFun 返回不可降级错误（余额不足）
	nonDegErr := &dexwallet.NonDegradableError{
		Reason: "余额不足",
		Cause: &dexwallet.InsufficientBalanceError{
			Required:  "1000000000",
			Available: "500000",
			Token:     "SOL",
		},
	}

	// 中优先级: 可以成功（但不应该被执行到）
	entries := []PrioritySelectorEntry{
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexPumpFun, 5*time.Millisecond, nonDegErr),
			Priority: dexwallet.PriorityHigh,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCLMM, 5*time.Millisecond, nil),
			Priority: dexwallet.PriorityMedium,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexJupiter, 5*time.Millisecond, nil),
			Priority: dexwallet.PriorityLow,
		},
	}

	config := PrioritySelectorConfig{
		HighTierTimeout:   100 * time.Millisecond,
		MediumTierTimeout: 200 * time.Millisecond,
		LowTierTimeout:    3 * time.Second,
	}

	selector := NewPrioritySelector(config, entries)
	ctx := context.Background()
	req := newPrioritySelectorTestReq()

	result, err := selector.Select(ctx, req)

	// 应该返回错误
	if err == nil {
		t.Fatalf("期望不可降级错误，实际成功: %+v", result)
	}

	// 错误应该是不可降级的
	if !dexwallet.IsNonDegradable(err) {
		t.Errorf("期望 NonDegradableError，实际: %T: %v", err, err)
	}

	// 验证统计：只有高优先级被访问
	stats := selector.Stats().Snapshot()
	if stats.NonDegradableFails != 1 {
		t.Errorf("期望 1 次不可降级失败，实际: %d", stats.NonDegradableFails)
	}
	if stats.TierReached[dexwallet.PriorityHigh] != 1 {
		t.Errorf("期望高优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityHigh])
	}
	if stats.TierReached[dexwallet.PriorityMedium] != 0 {
		t.Errorf("中优先级层不应被访问（不可降级错误应终止），实际: %d", stats.TierReached[dexwallet.PriorityMedium])
	}
	if stats.TierReached[dexwallet.PriorityLow] != 0 {
		t.Errorf("低优先级层不应被访问，实际: %d", stats.TierReached[dexwallet.PriorityLow])
	}
	if stats.TotalSuccess != 0 {
		t.Errorf("期望 0 次成功，实际: %d", stats.TotalSuccess)
	}
}

// ============================================================
// 测试: 高优先级超时，降级到中优先级
// ============================================================

// TestPrioritySelector_HighTierTimeout 验证高优先级层超时后降级到中优先级。
func TestPrioritySelector_HighTierTimeout(t *testing.T) {
	// 高优先级: 200ms 延迟（超时 50ms）
	// 中优先级: 10ms 成功
	entries := []PrioritySelectorEntry{
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexPumpFun, 200*time.Millisecond, nil),
			Priority: dexwallet.PriorityHigh,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCLMM, 10*time.Millisecond, nil),
			Priority: dexwallet.PriorityMedium,
		},
	}

	config := PrioritySelectorConfig{
		HighTierTimeout:   50 * time.Millisecond, // PumpFun 会超时
		MediumTierTimeout: 200 * time.Millisecond,
		LowTierTimeout:    3 * time.Second,
	}

	selector := NewPrioritySelector(config, entries)
	ctx := context.Background()
	req := newPrioritySelectorTestReq()

	result, err := selector.Select(ctx, req)
	if err != nil {
		t.Fatalf("期望降级到中优先级成功，实际错误: %v", err)
	}

	if result.DexID != dexwallet.DexRaydiumCLMM {
		t.Errorf("期望 Raydium CLMM（中优先级），实际: %s", result.DexID)
	}

	// 验证统计
	stats := selector.Stats().Snapshot()
	if stats.TierReached[dexwallet.PriorityHigh] != 1 {
		t.Errorf("期望高优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityHigh])
	}
	if stats.TierReached[dexwallet.PriorityMedium] != 1 {
		t.Errorf("期望中优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityMedium])
	}
}

// ============================================================
// 测试: 全部层级失败
// ============================================================

// TestPrioritySelector_AllTiersFail 验证所有层级都失败时返回错误。
func TestPrioritySelector_AllTiersFail(t *testing.T) {
	degErr := func(dexID dexwallet.DexID) error {
		return &dexwallet.DegradableError{
			DexID:  dexID,
			Reason: "模拟失败",
		}
	}

	entries := []PrioritySelectorEntry{
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexPumpFun, 5*time.Millisecond, degErr(dexwallet.DexPumpFun)),
			Priority: dexwallet.PriorityHigh,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCLMM, 5*time.Millisecond, degErr(dexwallet.DexRaydiumCLMM)),
			Priority: dexwallet.PriorityMedium,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexJupiter, 5*time.Millisecond, degErr(dexwallet.DexJupiter)),
			Priority: dexwallet.PriorityLow,
		},
	}

	config := DefaultPrioritySelectorConfig()
	selector := NewPrioritySelector(config, entries)
	ctx := context.Background()
	req := newPrioritySelectorTestReq()

	_, err := selector.Select(ctx, req)
	if err == nil {
		t.Fatal("期望所有层级失败时返回错误，实际成功")
	}

	// 验证所有层级都被访问
	stats := selector.Stats().Snapshot()
	if stats.TierReached[dexwallet.PriorityHigh] != 1 {
		t.Errorf("期望高优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityHigh])
	}
	if stats.TierReached[dexwallet.PriorityMedium] != 1 {
		t.Errorf("期望中优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityMedium])
	}
	if stats.TierReached[dexwallet.PriorityLow] != 1 {
		t.Errorf("期望低优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityLow])
	}
	if stats.TotalSuccess != 0 {
		t.Errorf("期望 0 次成功，实际: %d", stats.TotalSuccess)
	}
}

// ============================================================
// 测试: 中优先级层内多个 DEX 竞争，最快者胜出
// ============================================================

// TestPrioritySelector_FastestInTierWins 验证同一层级内最快成功的 DEX 胜出。
func TestPrioritySelector_FastestInTierWins(t *testing.T) {
	// 无高优先级 DEX
	// 中优先级: 3 个 DEX，延迟分别为 50ms、10ms、30ms
	entries := []PrioritySelectorEntry{
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCLMM, 50*time.Millisecond, nil),
			Priority: dexwallet.PriorityMedium,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexMeteoraAMM, 10*time.Millisecond, nil),
			Priority: dexwallet.PriorityMedium,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexMeteoraDLMM, 30*time.Millisecond, nil),
			Priority: dexwallet.PriorityMedium,
		},
	}

	config := DefaultPrioritySelectorConfig()
	selector := NewPrioritySelector(config, entries)
	ctx := context.Background()
	req := newPrioritySelectorTestReq()

	result, err := selector.Select(ctx, req)
	if err != nil {
		t.Fatalf("期望成功，实际错误: %v", err)
	}

	// Meteora AMM 最快（10ms），应该胜出
	if result.DexID != dexwallet.DexMeteoraAMM {
		t.Errorf("期望 Meteora AMM（最快），实际: %s", result.DexID)
	}
}

// ============================================================
// 测试: 不可降级错误在层级内传播
// ============================================================

// TestPrioritySelector_NonDegradableInMiddleTier 验证中优先级层内的
// 不可降级错误也会立即终止，不继续到低优先级层。
func TestPrioritySelector_NonDegradableInMiddleTier(t *testing.T) {
	// 高优先级: 可降级失败
	highErr := &dexwallet.DegradableError{
		DexID:  dexwallet.DexPumpFun,
		Reason: "内盘不可用",
	}

	// 中优先级: 不可降级错误
	midErr := &dexwallet.NonDegradableError{
		Reason: "滑点参数异常",
	}

	entries := []PrioritySelectorEntry{
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexPumpFun, 5*time.Millisecond, highErr),
			Priority: dexwallet.PriorityHigh,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCLMM, 5*time.Millisecond, midErr),
			Priority: dexwallet.PriorityMedium,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexJupiter, 5*time.Millisecond, nil),
			Priority: dexwallet.PriorityLow,
		},
	}

	config := DefaultPrioritySelectorConfig()
	selector := NewPrioritySelector(config, entries)
	ctx := context.Background()
	req := newPrioritySelectorTestReq()

	_, err := selector.Select(ctx, req)
	if err == nil {
		t.Fatal("期望不可降级错误终止，实际成功")
	}

	if !dexwallet.IsNonDegradable(err) {
		t.Errorf("期望 NonDegradableError，实际: %T: %v", err, err)
	}

	// 低优先级层不应被访问
	stats := selector.Stats().Snapshot()
	if stats.TierReached[dexwallet.PriorityLow] != 0 {
		t.Errorf("低优先级层不应被访问（中优先级不可降级错误应终止），实际: %d",
			stats.TierReached[dexwallet.PriorityLow])
	}
}

// ============================================================
// 测试: 降级链完整流程 — 高 → 中 → 低
// ============================================================

// TestPrioritySelector_FullDegradation 验证完整的三级降级链：
// 高优先级失败 → 中优先级失败 → 低优先级（兜底）成功。
func TestPrioritySelector_FullDegradation(t *testing.T) {
	degErr := func(dexID dexwallet.DexID) error {
		return &dexwallet.DegradableError{
			DexID:  dexID,
			Reason: "模拟失败",
		}
	}

	entries := []PrioritySelectorEntry{
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexPumpFun, 5*time.Millisecond, degErr(dexwallet.DexPumpFun)),
			Priority: dexwallet.PriorityHigh,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexRaydiumCLMM, 5*time.Millisecond, degErr(dexwallet.DexRaydiumCLMM)),
			Priority: dexwallet.PriorityMedium,
		},
		{
			Builder:  NewMockPriorityBuilder(dexwallet.DexJupiter, 10*time.Millisecond, nil), // 兜底成功
			Priority: dexwallet.PriorityLow,
		},
	}

	config := DefaultPrioritySelectorConfig()
	selector := NewPrioritySelector(config, entries)
	ctx := context.Background()
	req := newPrioritySelectorTestReq()

	result, err := selector.Select(ctx, req)
	if err != nil {
		t.Fatalf("期望兜底成功，实际错误: %v", err)
	}

	if result.DexID != dexwallet.DexJupiter {
		t.Errorf("期望 Jupiter 兜底成功，实际: %s", result.DexID)
	}

	// 验证所有层级都被访问
	stats := selector.Stats().Snapshot()
	if stats.TierReached[dexwallet.PriorityHigh] != 1 {
		t.Errorf("期望高优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityHigh])
	}
	if stats.TierReached[dexwallet.PriorityMedium] != 1 {
		t.Errorf("期望中优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityMedium])
	}
	if stats.TierReached[dexwallet.PriorityLow] != 1 {
		t.Errorf("期望低优先级层被访问 1 次，实际: %d", stats.TierReached[dexwallet.PriorityLow])
	}
	if stats.WinsByDex[dexwallet.DexJupiter] != 1 {
		t.Errorf("期望 Jupiter 胜出 1 次，实际: %d", stats.WinsByDex[dexwallet.DexJupiter])
	}
}
