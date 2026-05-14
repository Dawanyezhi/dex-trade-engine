package main

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// 辅助函数
// ============================================================

// setupTestAggregator 创建带有指定 DEX 配置的测试聚合器。
func setupTestAggregator(entries []*testDexConfig) (*dexwallet.BaseAggregator, *MockPoolManager) {
	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	agg := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)
	for _, e := range entries {
		agg.RegisterDex(&dexwallet.DexEntry{
			Builder:          NewMockSwapBuilder(e.dexID, coinset.ChainSolana, e.protocol, e.builderFail),
			Protocol:         NewMockDexProtocol(e.dexID, e.protocol, e.outputRatio, e.latency, e.quoteFail),
			Priority:         e.priority,
			Enabled:          e.enabled,
			GrayscalePercent: e.grayscale,
		})
	}
	return agg, pm
}

type testDexConfig struct {
	dexID       dexwallet.DexID
	protocol    dexwallet.ProtocolType
	priority    dexwallet.DexPriority
	outputRatio int64
	latency     time.Duration
	quoteFail   bool
	builderFail bool
	enabled     bool
	grayscale   int
}

// newTestSwapRequest 创建测试用 SwapRequest。
func newTestSwapRequest() dexwallet.SwapRequest {
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
// 测试: 全部 DEX 报价成功
// ============================================================

func TestAllDexQuoteSuccess(t *testing.T) {
	configs := []*testDexConfig{
		{
			dexID: dexwallet.DexPumpFun, protocol: dexwallet.ProtocolBondingCurve,
			priority: dexwallet.PriorityHigh, outputRatio: 92,
			latency: 10 * time.Millisecond, enabled: true, grayscale: 100,
		},
		{
			dexID: dexwallet.DexRaydiumAMM, protocol: dexwallet.ProtocolAMM,
			priority: dexwallet.PriorityMedium, outputRatio: 97,
			latency: 10 * time.Millisecond, enabled: true, grayscale: 100,
		},
		{
			dexID: dexwallet.DexJupiter, protocol: dexwallet.ProtocolAggregator,
			priority: dexwallet.PriorityLow, outputRatio: 98,
			latency: 10 * time.Millisecond, enabled: true, grayscale: 100,
		},
	}

	agg, _ := setupTestAggregator(configs)
	ctx := context.Background()
	req := newTestSwapRequest()

	quote, err := agg.FindBestQuote(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// 应该选中优先级最高的 PumpFun
	if quote.DexID != dexwallet.DexPumpFun {
		t.Errorf("expected PumpFun to be selected (highest priority), got: %s", quote.DexID)
	}

	if quote.Priority != dexwallet.PriorityHigh {
		t.Errorf("expected priority High(1), got: %d", quote.Priority)
	}

	if quote.OutputAmount == nil || quote.OutputAmount.Sign() <= 0 {
		t.Error("expected positive output amount")
	}
}

// ============================================================
// 测试: 部分 DEX 失败降级
// ============================================================

func TestPartialDexFailureFallback(t *testing.T) {
	configs := []*testDexConfig{
		{
			dexID: dexwallet.DexPumpFun, protocol: dexwallet.ProtocolBondingCurve,
			priority: dexwallet.PriorityHigh, outputRatio: 92,
			latency: 10 * time.Millisecond, quoteFail: true, // 报价失败
			enabled: true, grayscale: 100,
		},
		{
			dexID: dexwallet.DexRaydiumAMM, protocol: dexwallet.ProtocolAMM,
			priority: dexwallet.PriorityMedium, outputRatio: 97,
			latency: 10 * time.Millisecond, enabled: true, grayscale: 100,
		},
		{
			dexID: dexwallet.DexJupiter, protocol: dexwallet.ProtocolAggregator,
			priority: dexwallet.PriorityLow, outputRatio: 98,
			latency: 10 * time.Millisecond, enabled: true, grayscale: 100,
		},
	}

	agg, _ := setupTestAggregator(configs)
	ctx := context.Background()
	req := newTestSwapRequest()

	quote, err := agg.FindBestQuote(ctx, req)
	if err != nil {
		t.Fatalf("expected no error (should fallback), got: %v", err)
	}

	// PumpFun 失败，应该选中 Raydium（次高优先级）
	if quote.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("expected Raydium to be selected (fallback), got: %s", quote.DexID)
	}
}

// ============================================================
// 测试: 全部 DEX 失败
// ============================================================

func TestAllDexFailure(t *testing.T) {
	configs := []*testDexConfig{
		{
			dexID: dexwallet.DexPumpFun, protocol: dexwallet.ProtocolBondingCurve,
			priority: dexwallet.PriorityHigh, quoteFail: true,
			enabled: true, grayscale: 100,
		},
		{
			dexID: dexwallet.DexRaydiumAMM, protocol: dexwallet.ProtocolAMM,
			priority: dexwallet.PriorityMedium, quoteFail: true,
			enabled: true, grayscale: 100,
		},
		{
			dexID: dexwallet.DexJupiter, protocol: dexwallet.ProtocolAggregator,
			priority: dexwallet.PriorityLow, quoteFail: true,
			enabled: true, grayscale: 100,
		},
	}

	agg, _ := setupTestAggregator(configs)
	ctx := context.Background()
	req := newTestSwapRequest()

	_, err := agg.FindBestQuote(ctx, req)
	if err == nil {
		t.Fatal("expected error when all DEX fail, got nil")
	}
}

// ============================================================
// 测试: 无活跃 DEX（全部禁用）
// ============================================================

func TestNoActiveDex(t *testing.T) {
	configs := []*testDexConfig{
		{
			dexID: dexwallet.DexRaydiumAMM, protocol: dexwallet.ProtocolAMM,
			priority: dexwallet.PriorityMedium, outputRatio: 97,
			enabled: false, grayscale: 100, // 禁用
		},
	}

	agg, _ := setupTestAggregator(configs)
	ctx := context.Background()
	req := newTestSwapRequest()

	_, err := agg.FindBestQuote(ctx, req)
	if err == nil {
		t.Fatal("expected error when no active DEX, got nil")
	}
}

// ============================================================
// 测试: 优先级排序正确性
// ============================================================

func TestPrioritySortingCorrectness(t *testing.T) {
	// 高优先级 DEX 输出少，低优先级 DEX 输出多
	// 应该选择高优先级的
	configs := []*testDexConfig{
		{
			dexID: dexwallet.DexPumpFun, protocol: dexwallet.ProtocolBondingCurve,
			priority: dexwallet.PriorityHigh, outputRatio: 80, // 输出少
			latency: 10 * time.Millisecond, enabled: true, grayscale: 100,
		},
		{
			dexID: dexwallet.DexJupiter, protocol: dexwallet.ProtocolAggregator,
			priority: dexwallet.PriorityLow, outputRatio: 99, // 输出多
			latency: 10 * time.Millisecond, enabled: true, grayscale: 100,
		},
	}

	agg, _ := setupTestAggregator(configs)
	ctx := context.Background()
	req := newTestSwapRequest()

	quote, err := agg.FindBestQuote(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if quote.DexID != dexwallet.DexPumpFun {
		t.Errorf("expected PumpFun (high priority) despite lower output, got: %s", quote.DexID)
	}
}

// TestSamePrioritySortsByOutput 同优先级下按输出金额排序。
func TestSamePrioritySortsByOutput(t *testing.T) {
	configs := []*testDexConfig{
		{
			dexID: dexwallet.DexRaydiumAMM, protocol: dexwallet.ProtocolAMM,
			priority: dexwallet.PriorityMedium, outputRatio: 90,
			latency: 10 * time.Millisecond, enabled: true, grayscale: 100,
		},
		{
			dexID: dexwallet.DexMeteoraDLMM, protocol: dexwallet.ProtocolDLMM,
			priority: dexwallet.PriorityMedium, outputRatio: 98, // 同优先级但输出更多
			latency: 10 * time.Millisecond, enabled: true, grayscale: 100,
		},
	}

	agg, _ := setupTestAggregator(configs)
	ctx := context.Background()
	req := newTestSwapRequest()

	quote, err := agg.FindBestQuote(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 同优先级，应该选输出更多的 Meteora
	if quote.DexID != dexwallet.DexMeteoraDLMM {
		t.Errorf("expected Meteora (higher output at same priority), got: %s", quote.DexID)
	}
}

// ============================================================
// 测试: 灰度百分比控制
// ============================================================

func TestGrayscalePercentControl(t *testing.T) {
	// 只注册一个 DEX，灰度 0%
	configs := []*testDexConfig{
		{
			dexID: dexwallet.DexRaydiumAMM, protocol: dexwallet.ProtocolAMM,
			priority: dexwallet.PriorityMedium, outputRatio: 97,
			latency: 10 * time.Millisecond, enabled: true,
			grayscale: 0, // 0% 灰度 = 完全不参与
		},
	}

	agg, _ := setupTestAggregator(configs)
	ctx := context.Background()
	req := newTestSwapRequest()

	// 0% 灰度应该从不参与
	_, err := agg.FindBestQuote(ctx, req)
	if err == nil {
		t.Fatal("expected error with 0% grayscale (DEX should never participate), got nil")
	}
}

func TestGrayscalePercent100AlwaysParticipates(t *testing.T) {
	configs := []*testDexConfig{
		{
			dexID: dexwallet.DexRaydiumAMM, protocol: dexwallet.ProtocolAMM,
			priority: dexwallet.PriorityMedium, outputRatio: 97,
			latency: 10 * time.Millisecond, enabled: true,
			grayscale: 100, // 100% 灰度 = 全量
		},
	}

	agg, _ := setupTestAggregator(configs)
	ctx := context.Background()
	req := newTestSwapRequest()

	// 100% 灰度应该始终参与
	successCount := 0
	rounds := 20
	for i := 0; i < rounds; i++ {
		_, err := agg.FindBestQuote(ctx, req)
		if err == nil {
			successCount++
		}
	}

	if successCount != rounds {
		t.Errorf("expected all %d rounds to succeed with 100%% grayscale, got %d", rounds, successCount)
	}
}

func TestGrayscalePartialParticipation(t *testing.T) {
	// 两个 DEX，一个全量一个 30% 灰度
	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	agg := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false),
		Protocol:         NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 90, 10*time.Millisecond, false),
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100, // 全量
	})

	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          NewMockSwapBuilder(dexwallet.DexMeteoraDLMM, coinset.ChainSolana, dexwallet.ProtocolDLMM, false),
		Protocol:         NewMockDexProtocol(dexwallet.DexMeteoraDLMM, dexwallet.ProtocolDLMM, 99, 10*time.Millisecond, false),
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 30, // 30% 灰度
	})

	ctx := context.Background()
	req := newTestSwapRequest()

	meteoraCount := 0
	rounds := 500
	for i := 0; i < rounds; i++ {
		quote, err := agg.FindBestQuote(ctx, req)
		if err != nil {
			continue
		}
		if quote.DexID == dexwallet.DexMeteoraDLMM {
			meteoraCount++
		}
	}

	// Meteora 的参与率应该在 30% 附近（允许 +/- 15% 的偏差）
	participationRate := float64(meteoraCount) / float64(rounds) * 100
	if participationRate < 15 || participationRate > 45 {
		t.Errorf("expected Meteora participation rate around 30%%, got %.1f%% (%d/%d)",
			participationRate, meteoraCount, rounds)
	}
}

// ============================================================
// 测试: 灰度动态调整
// ============================================================

func TestGrayscaleDynamicAdjustment(t *testing.T) {
	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	agg := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false),
		Protocol:         NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 90, 5*time.Millisecond, false),
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          NewMockSwapBuilder(dexwallet.DexMeteoraDLMM, coinset.ChainSolana, dexwallet.ProtocolDLMM, false),
		Protocol:         NewMockDexProtocol(dexwallet.DexMeteoraDLMM, dexwallet.ProtocolDLMM, 99, 5*time.Millisecond, false),
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 0, // 初始 0%
	})

	ctx := context.Background()
	req := newTestSwapRequest()

	// 0% 灰度: Meteora 不应参与
	for i := 0; i < 10; i++ {
		quote, err := agg.FindBestQuote(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if quote.DexID == dexwallet.DexMeteoraDLMM {
			t.Fatal("Meteora should not participate at 0% grayscale")
		}
	}

	// 提升到 100%
	agg.SetGrayscale(dexwallet.DexMeteoraDLMM, 100)

	// 100% 灰度: Meteora 应该总是参与且被选中（输出更高）
	for i := 0; i < 10; i++ {
		quote, err := agg.FindBestQuote(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if quote.DexID != dexwallet.DexMeteoraDLMM {
			t.Errorf("Meteora should be selected at 100%% grayscale (higher output), got: %s", quote.DexID)
		}
	}
}

// ============================================================
// 测试: EnableDex 启用/禁用
// ============================================================

func TestEnableDex(t *testing.T) {
	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	agg := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false),
		Protocol:         NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 90, 5*time.Millisecond, false),
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	ctx := context.Background()
	req := newTestSwapRequest()

	// 启用状态: 应该成功
	_, err := agg.FindBestQuote(ctx, req)
	if err != nil {
		t.Fatalf("expected success when enabled, got: %v", err)
	}

	// 禁用
	agg.EnableDex(dexwallet.DexRaydiumAMM, false)

	// 禁用状态: 应该失败（无活跃 DEX）
	_, err = agg.FindBestQuote(ctx, req)
	if err == nil {
		t.Fatal("expected error when DEX disabled, got nil")
	}

	// 重新启用
	agg.EnableDex(dexwallet.DexRaydiumAMM, true)

	// 重新启用: 应该成功
	_, err = agg.FindBestQuote(ctx, req)
	if err != nil {
		t.Fatalf("expected success after re-enable, got: %v", err)
	}
}

// ============================================================
// 测试: BuildSwap 降级
// ============================================================

func TestBuildSwapFallback(t *testing.T) {
	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	agg := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	// PumpFun: 报价成功，构建失败
	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          NewMockSwapBuilder(dexwallet.DexPumpFun, coinset.ChainSolana, dexwallet.ProtocolBondingCurve, true), // Build 失败
		Protocol:         NewMockDexProtocol(dexwallet.DexPumpFun, dexwallet.ProtocolBondingCurve, 92, 10*time.Millisecond, false),
		Priority:         dexwallet.PriorityHigh,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	// Raydium: 正常
	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false),
		Protocol:         NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 97, 10*time.Millisecond, false),
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	ctx := context.Background()
	req := newTestSwapRequest()

	// PumpFun 报价成功但 Build 失败，应该降级到 Raydium
	result, err := agg.BuildSwap(ctx, req)
	if err != nil {
		t.Fatalf("expected successful fallback, got: %v", err)
	}

	if result.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("expected Raydium as fallback, got: %s", result.DexID)
	}
}

// ============================================================
// 测试: 两跳路由 vs 直接路由
// ============================================================

func TestTwoHopVsDirect(t *testing.T) {
	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	agg := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	// 注册 DEX（直接路由输出 85%，两跳路由每段 85%，即 85%*85%=72.25%）
	protocol := NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 85, 10*time.Millisecond, false)
	builder := NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false)
	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          builder,
		Protocol:         protocol,
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	middleAssets := []string{
		"So11111111111111111111111111111111111111112",
		"USDCxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
	}
	router := NewTwoHopRouter(agg, pm, middleAssets)

	ctx := context.Background()
	req := dexwallet.SwapRequest{
		ChainID:   coinset.ChainSolana,
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{
			Address:  "So11111111111111111111111111111111111111112",
			Symbol:   "SOL",
			Decimals: 9,
			ChainID:  coinset.ChainSolana,
		},
		ToToken: dexwallet.Token{
			Address:  "RARExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			Symbol:   "RARE",
			Decimals: 6,
			ChainID:  coinset.ChainSolana,
		},
		Amount:      new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9)),
		SlippageBps: 200,
		Sender:      "SenderPubkey111111111111111111111111111111",
		Recipient:   "SenderPubkey111111111111111111111111111111",
	}

	route, err := router.FindBestRoute(ctx, req)
	if err != nil {
		t.Fatalf("expected route, got: %v", err)
	}

	if route.TotalOutput == nil || route.TotalOutput.Sign() <= 0 {
		t.Error("expected positive total output")
	}

	// 路由应该有至少 1 跳
	if len(route.Hops) == 0 {
		t.Error("expected at least 1 hop")
	}

	// Gas 应该为正
	if route.TotalGas == nil || route.TotalGas.Sign() <= 0 {
		t.Error("expected positive total gas")
	}
}

// TestTwoHopRouterNoRoute 无路由可用的情况。
func TestTwoHopRouterNoRoute(t *testing.T) {
	pm := NewMockPoolManager()
	// 不添加任何池子

	agg := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	// 注册一个总是失败的 DEX
	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false),
		Protocol:         NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 0, 10*time.Millisecond, true),
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	middleAssets := []string{"SOL", "USDC"}
	router := NewTwoHopRouter(agg, pm, middleAssets)

	ctx := context.Background()
	req := newTestSwapRequest()

	_, err := router.FindBestRoute(ctx, req)
	if err == nil {
		t.Fatal("expected error when no route available, got nil")
	}
}

// ============================================================
// 测试: 并发安全
// ============================================================

func TestConcurrentFindBestQuote(t *testing.T) {
	configs := []*testDexConfig{
		{
			dexID: dexwallet.DexRaydiumAMM, protocol: dexwallet.ProtocolAMM,
			priority: dexwallet.PriorityMedium, outputRatio: 97,
			latency: 5 * time.Millisecond, enabled: true, grayscale: 100,
		},
		{
			dexID: dexwallet.DexJupiter, protocol: dexwallet.ProtocolAggregator,
			priority: dexwallet.PriorityLow, outputRatio: 98,
			latency: 5 * time.Millisecond, enabled: true, grayscale: 100,
		},
	}

	agg, _ := setupTestAggregator(configs)
	ctx := context.Background()
	req := newTestSwapRequest()

	// 并发执行 50 次 FindBestQuote
	var wg sync.WaitGroup
	var errCount atomic.Int64
	var successCount atomic.Int64

	concurrency := 50
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			quote, err := agg.FindBestQuote(ctx, req)
			if err != nil {
				errCount.Add(1)
				return
			}
			if quote != nil && quote.OutputAmount.Sign() > 0 {
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()

	if successCount.Load() != int64(concurrency) {
		t.Errorf("expected %d successful quotes, got %d (errors: %d)",
			concurrency, successCount.Load(), errCount.Load())
	}
}

func TestConcurrentRegisterAndQuery(t *testing.T) {
	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	agg := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	// 先注册一个基础 DEX
	agg.RegisterDex(&dexwallet.DexEntry{
		Builder:          NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false),
		Protocol:         NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 97, 5*time.Millisecond, false),
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	ctx := context.Background()
	req := newTestSwapRequest()

	var wg sync.WaitGroup

	// 并发查询
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = agg.FindBestQuote(ctx, req)
		}()
	}

	// 并发注册新 DEX
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			dexID := dexwallet.DexID(fmt.Sprintf("mock_dex_%d", idx))
			agg.RegisterDex(&dexwallet.DexEntry{
				Builder:          NewMockSwapBuilder(dexID, coinset.ChainSolana, dexwallet.ProtocolAMM, false),
				Protocol:         NewMockDexProtocol(dexID, dexwallet.ProtocolAMM, 95, 5*time.Millisecond, false),
				Priority:         dexwallet.PriorityMedium,
				Enabled:          true,
				GrayscalePercent: 100,
			})
		}(i)
	}

	// 并发调整灰度
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(pct int) {
			defer wg.Done()
			agg.SetGrayscale(dexwallet.DexRaydiumAMM, pct*10)
		}(i)
	}

	// 不应该 panic
	wg.Wait()
}

// ============================================================
// 测试: MockPoolManager
// ============================================================

func TestMockPoolManagerGetPool(t *testing.T) {
	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	ctx := context.Background()
	pool, err := pm.GetPool(ctx, "pool_sol_meme_raydium")
	if err != nil {
		t.Fatalf("expected pool, got: %v", err)
	}
	if pool.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("expected DexID %s, got %s", dexwallet.DexRaydiumAMM, pool.DexID)
	}
}

func TestMockPoolManagerGetPoolNotFound(t *testing.T) {
	pm := NewMockPoolManager()
	ctx := context.Background()

	_, err := pm.GetPool(ctx, "nonexistent_pool")
	if err == nil {
		t.Fatal("expected error for nonexistent pool, got nil")
	}
}

func TestMockPoolManagerGetBestPool(t *testing.T) {
	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	ctx := context.Background()
	pool, err := pm.GetBestPool(ctx,
		"So11111111111111111111111111111111111111112",
		"MEMExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	if err != nil {
		t.Fatalf("expected best pool, got: %v", err)
	}

	// 应该返回流动性最高的池子（Meteora 80000 SOL > Raydium 50000 SOL）
	if pool.Address != "pool_sol_meme_meteora" {
		t.Errorf("expected pool_sol_meme_meteora (highest liquidity), got: %s", pool.Address)
	}
}

func TestMockPoolManagerGetBestPoolNoMatch(t *testing.T) {
	pm := NewMockPoolManager()
	ctx := context.Background()

	_, err := pm.GetBestPool(ctx, "TOKEN_A", "TOKEN_B")
	if err == nil {
		t.Fatal("expected error for no matching pool, got nil")
	}
}
