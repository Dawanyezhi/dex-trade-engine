package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

func main() {
	// 配置结构化日志
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("05-aggregator-routing: 多 DEX 聚合与最优路由演示")
	fmt.Println(strings.Repeat("=", 70))

	// ---- 场景 1: 全部 DEX 报价成功，选最优 ----
	demoAllSuccess()

	// ---- 场景 2: 部分 DEX 失败，降级 ----
	demoPartialFailure()

	// ---- 场景 3: 全部失败，报错 ----
	demoAllFailure()

	// ---- 场景 4: 两跳路由 vs 直接路由对比 ----
	demoTwoHopRoute()

	// ---- 场景 5: 灰度发布 ----
	demoGrayscale()

	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("演示完成")
	fmt.Println(strings.Repeat("=", 70))
}

// ============================================================
// 场景 1: 全部 DEX 报价成功，选最优
// ============================================================

func demoAllSuccess() {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("场景 1: 全部 DEX 报价成功，选最优")
	fmt.Println(strings.Repeat("-", 70))

	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	aggregator := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	// 注册 3 个 DEX，不同优先级和输出比例
	// PumpFun: 高优先级，输出 92%
	pumpProtocol := NewMockDexProtocol(dexwallet.DexPumpFun, dexwallet.ProtocolBondingCurve, 92, 50*time.Millisecond, false)
	pumpBuilder := NewMockSwapBuilder(dexwallet.DexPumpFun, coinset.ChainSolana, dexwallet.ProtocolBondingCurve, false)
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:          pumpBuilder,
		Protocol:         pumpProtocol,
		Priority:         dexwallet.PriorityHigh,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	// Raydium: 中优先级，输出 97%
	raydiumProtocol := NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 97, 30*time.Millisecond, false)
	raydiumBuilder := NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false)
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:          raydiumBuilder,
		Protocol:         raydiumProtocol,
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	// Jupiter: 低优先级，输出 98%（聚合器通常输出更高，但优先级低）
	jupiterProtocol := NewMockDexProtocol(dexwallet.DexJupiter, dexwallet.ProtocolAggregator, 98, 100*time.Millisecond, false)
	jupiterBuilder := NewMockSwapBuilder(dexwallet.DexJupiter, coinset.ChainSolana, dexwallet.ProtocolAggregator, false)
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:          jupiterBuilder,
		Protocol:         jupiterProtocol,
		Priority:         dexwallet.PriorityLow,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	ctx := context.Background()
	req := newSwapRequest()

	fmt.Println("\n  注册的 DEX:")
	fmt.Printf("    %-15s 优先级=%-6s 输出比例=%s\n", "PumpFun", "High(1)", "92%")
	fmt.Printf("    %-15s 优先级=%-6s 输出比例=%s\n", "Raydium AMM", "Medium(2)", "97%")
	fmt.Printf("    %-15s 优先级=%-6s 输出比例=%s\n", "Jupiter", "Low(3)", "98%")

	fmt.Println("\n  报价中...")
	quote, err := aggregator.FindBestQuote(ctx, req)
	if err != nil {
		fmt.Printf("  [错误] %v\n", err)
		return
	}

	fmt.Println("\n  报价结果:")
	fmt.Printf("    选中 DEX:     %s\n", quote.DexID)
	fmt.Printf("    优先级:       %d\n", quote.Priority)
	fmt.Printf("    输入金额:     %s\n", quote.InputAmount.String())
	fmt.Printf("    输出金额:     %s\n", quote.OutputAmount.String())
	fmt.Printf("    价格影响:     %d bps\n", quote.PriceImpact)
	fmt.Printf("    预估 Gas:     %s\n", quote.EstimatedGas.String())

	fmt.Println("\n  [结论] PumpFun 被选中（优先级最高=1），即使 Jupiter 输出更多")
	fmt.Println("  原因: 优先级排序在输出金额排序之前")

	// 同时构建交易
	fmt.Println("\n  构建交易中...")
	result, err := aggregator.BuildSwap(ctx, req)
	if err != nil {
		fmt.Printf("  [错误] %v\n", err)
		return
	}
	fmt.Printf("    构建成功: DEX=%s, 输出=%s\n", result.DexID, result.OutputAmount.String())
}

// ============================================================
// 场景 2: 部分 DEX 失败，降级
// ============================================================

func demoPartialFailure() {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("场景 2: 部分 DEX 失败，降级兜底")
	fmt.Println(strings.Repeat("-", 70))

	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	aggregator := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	// PumpFun: 报价失败（模拟代币已毕业）
	pumpProtocol := NewMockDexProtocol(dexwallet.DexPumpFun, dexwallet.ProtocolBondingCurve, 92, 50*time.Millisecond, true)
	pumpBuilder := NewMockSwapBuilder(dexwallet.DexPumpFun, coinset.ChainSolana, dexwallet.ProtocolBondingCurve, false)
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:          pumpBuilder,
		Protocol:         pumpProtocol,
		Priority:         dexwallet.PriorityHigh,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	// Raydium: 正常
	raydiumProtocol := NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 97, 30*time.Millisecond, false)
	raydiumBuilder := NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false)
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:          raydiumBuilder,
		Protocol:         raydiumProtocol,
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	// Jupiter: 正常
	jupiterProtocol := NewMockDexProtocol(dexwallet.DexJupiter, dexwallet.ProtocolAggregator, 98, 100*time.Millisecond, false)
	jupiterBuilder := NewMockSwapBuilder(dexwallet.DexJupiter, coinset.ChainSolana, dexwallet.ProtocolAggregator, false)
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:          jupiterBuilder,
		Protocol:         jupiterProtocol,
		Priority:         dexwallet.PriorityLow,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	ctx := context.Background()
	req := newSwapRequest()

	fmt.Println("\n  DEX 状态:")
	fmt.Printf("    %-15s 状态=%s\n", "PumpFun", "报价失败(已毕业)")
	fmt.Printf("    %-15s 状态=%s\n", "Raydium AMM", "正常")
	fmt.Printf("    %-15s 状态=%s\n", "Jupiter", "正常")

	fmt.Println("\n  报价中...")
	quote, err := aggregator.FindBestQuote(ctx, req)
	if err != nil {
		fmt.Printf("  [错误] %v\n", err)
		return
	}

	fmt.Println("\n  报价结果:")
	fmt.Printf("    选中 DEX:     %s\n", quote.DexID)
	fmt.Printf("    优先级:       %d\n", quote.Priority)
	fmt.Printf("    输出金额:     %s\n", quote.OutputAmount.String())

	fmt.Println("\n  [结论] PumpFun 报价失败被跳过，Raydium AMM（中优先级）被选中")
	fmt.Println("  降级链: PumpFun(失败) -> Raydium(成功)")
}

// ============================================================
// 场景 3: 全部失败，报错
// ============================================================

func demoAllFailure() {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("场景 3: 全部 DEX 失败")
	fmt.Println(strings.Repeat("-", 70))

	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	aggregator := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	// 所有 DEX 都设置为失败
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:  NewMockSwapBuilder(dexwallet.DexPumpFun, coinset.ChainSolana, dexwallet.ProtocolBondingCurve, false),
		Protocol: NewMockDexProtocol(dexwallet.DexPumpFun, dexwallet.ProtocolBondingCurve, 0, 0, true),
		Priority: dexwallet.PriorityHigh, Enabled: true, GrayscalePercent: 100,
	})

	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:  NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false),
		Protocol: NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 0, 0, true),
		Priority: dexwallet.PriorityMedium, Enabled: true, GrayscalePercent: 100,
	})

	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:  NewMockSwapBuilder(dexwallet.DexJupiter, coinset.ChainSolana, dexwallet.ProtocolAggregator, false),
		Protocol: NewMockDexProtocol(dexwallet.DexJupiter, dexwallet.ProtocolAggregator, 0, 0, true),
		Priority: dexwallet.PriorityLow, Enabled: true, GrayscalePercent: 100,
	})

	ctx := context.Background()
	req := newSwapRequest()

	fmt.Println("\n  DEX 状态:")
	fmt.Printf("    %-15s 状态=%s\n", "PumpFun", "报价失败")
	fmt.Printf("    %-15s 状态=%s\n", "Raydium AMM", "报价失败")
	fmt.Printf("    %-15s 状态=%s\n", "Jupiter", "报价失败")

	fmt.Println("\n  报价中...")
	_, err := aggregator.FindBestQuote(ctx, req)
	if err != nil {
		fmt.Printf("\n  [预期错误] %v\n", err)
		fmt.Println("\n  [结论] 所有 DEX 报价失败，返回聚合错误")
	}

	fmt.Println("\n  构建交易中...")
	_, err = aggregator.BuildSwap(ctx, req)
	if err != nil {
		fmt.Printf("  [预期错误] %v\n", err)
	}
}

// ============================================================
// 场景 4: 两跳路由 vs 直接路由对比
// ============================================================

func demoTwoHopRoute() {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("场景 4: 两跳路由 vs 直接路由对比")
	fmt.Println(strings.Repeat("-", 70))

	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	aggregator := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	// 注册 DEX
	// 直接路由: 输出 85%（模拟低流动性池子）
	directProtocol := NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 85, 30*time.Millisecond, false)
	directBuilder := NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false)
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:          directBuilder,
		Protocol:         directProtocol,
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100,
	})

	// 创建两跳路由器
	middleAssets := []string{
		"So11111111111111111111111111111111111111112",  // SOL
		"USDCxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", // USDC
	}
	router := NewTwoHopRouter(aggregator, pm, middleAssets)

	// 请求: SOL -> RARE（有直接池但流动性低）
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
		Amount:      new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9)), // 10 SOL
		SlippageBps: 200,
		Sender:      "SenderPubkey111111111111111111111111111111",
		Recipient:   "SenderPubkey111111111111111111111111111111",
	}

	fmt.Println("\n  交易: 10 SOL -> RARE")
	fmt.Println("\n  可用路由:")
	fmt.Println("    直接路由: SOL -> RARE (低流动性池, 输出 85%)")
	fmt.Println("    两跳路由: SOL -> USDC -> RARE (高流动性)")

	route, err := router.FindBestRoute(ctx, req)
	if err != nil {
		fmt.Printf("  [错误] %v\n", err)
		return
	}

	fmt.Println("\n  最优路由结果:")
	if route.IsDirect {
		fmt.Println("    类型:         直接路由")
	} else {
		fmt.Printf("    类型:         两跳路由 (中间资产: %s)\n", route.MiddleAsset)
	}
	fmt.Printf("    总输出:       %s\n", route.TotalOutput.String())
	fmt.Printf("    总价格影响:   %d bps\n", route.TotalPriceImpact)
	fmt.Printf("    总 Gas:       %s\n", route.TotalGas.String())
	fmt.Printf("    跳数:         %d\n", len(route.Hops))

	fmt.Println("\n  路由明细:")
	for i, hop := range route.Hops {
		fmt.Printf("    跳 %d: %s -> %s (DEX: %s, 池: %s)\n",
			i+1, truncateAddr(hop.TokenIn), truncateAddr(hop.TokenOut),
			hop.DexID, hop.Pool)
		fmt.Printf("           输入: %s, 输出: %s\n", hop.AmountIn.String(), hop.AmountOut.String())
	}
}

// ============================================================
// 场景 5: 灰度发布
// ============================================================

func demoGrayscale() {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("场景 5: 灰度发布 (Meteora DLMM 30% 灰度)")
	fmt.Println(strings.Repeat("-", 70))

	pm := NewMockPoolManager()
	setupDefaultPools(pm)

	aggregator := dexwallet.NewBaseAggregator(coinset.ChainSolana, pm)

	// Raydium: 全量
	raydiumProtocol := NewMockDexProtocol(dexwallet.DexRaydiumAMM, dexwallet.ProtocolAMM, 95, 30*time.Millisecond, false)
	raydiumBuilder := NewMockSwapBuilder(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM, false)
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:          raydiumBuilder,
		Protocol:         raydiumProtocol,
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 100, // 全量
	})

	// Meteora DLMM: 30% 灰度（输出更高，但还在灰度阶段）
	meteoraProtocol := NewMockDexProtocol(dexwallet.DexMeteoraDLMM, dexwallet.ProtocolDLMM, 99, 20*time.Millisecond, false)
	meteoraBuilder := NewMockSwapBuilder(dexwallet.DexMeteoraDLMM, coinset.ChainSolana, dexwallet.ProtocolDLMM, false)
	aggregator.RegisterDex(&dexwallet.DexEntry{
		Builder:          meteoraBuilder,
		Protocol:         meteoraProtocol,
		Priority:         dexwallet.PriorityMedium,
		Enabled:          true,
		GrayscalePercent: 30, // 30% 灰度
	})

	ctx := context.Background()
	req := newSwapRequest()

	// 统计 100 次报价中 Meteora 被选中的次数
	totalRounds := 100
	meteoraSelected := 0
	raydiumSelected := 0
	errorCount := 0

	fmt.Println("\n  配置:")
	fmt.Printf("    %-15s 灰度=%3d%% 输出比例=%s\n", "Raydium AMM", 100, "95%")
	fmt.Printf("    %-15s 灰度=%3d%% 输出比例=%s\n", "Meteora DLMM", 30, "99%")

	fmt.Printf("\n  执行 %d 次报价统计...\n", totalRounds)

	for i := 0; i < totalRounds; i++ {
		quote, err := aggregator.FindBestQuote(ctx, req)
		if err != nil {
			errorCount++
			continue
		}
		switch quote.DexID {
		case dexwallet.DexMeteoraDLMM:
			meteoraSelected++
		case dexwallet.DexRaydiumAMM:
			raydiumSelected++
		}
	}

	fmt.Println("\n  统计结果:")
	fmt.Printf("    %-15s 选中次数: %3d/%d (%d%%)\n", "Raydium AMM", raydiumSelected, totalRounds, raydiumSelected*100/totalRounds)
	fmt.Printf("    %-15s 选中次数: %3d/%d (%d%%)\n", "Meteora DLMM", meteoraSelected, totalRounds, meteoraSelected*100/totalRounds)
	if errorCount > 0 {
		fmt.Printf("    %-15s 次数:     %3d/%d\n", "报价错误", errorCount, totalRounds)
	}

	fmt.Println("\n  [分析]")
	fmt.Println("    Meteora DLMM 设置 30% 灰度，约 30% 的请求它会参与报价")
	fmt.Println("    参与时因为输出更高(99%)会被优先选中（同优先级下输出大的优先）")
	fmt.Println("    不参与时 Raydium 作为唯一候选被选中")
	fmt.Printf("    理论上 Meteora 选中率约 30%%，实际: %d%%\n", meteoraSelected*100/totalRounds)

	// 演示动态调整灰度
	fmt.Println("\n  动态调整: 将 Meteora 灰度从 30% 提升到 80%")
	aggregator.SetGrayscale(dexwallet.DexMeteoraDLMM, 80)

	meteoraSelected2 := 0
	for i := 0; i < totalRounds; i++ {
		quote, err := aggregator.FindBestQuote(ctx, req)
		if err != nil {
			continue
		}
		if quote.DexID == dexwallet.DexMeteoraDLMM {
			meteoraSelected2++
		}
	}
	fmt.Printf("    调整后 Meteora 选中率: %d%% (理论约 80%%)\n", meteoraSelected2*100/totalRounds)
}

// ============================================================
// 辅助函数
// ============================================================

// newSwapRequest 创建标准的测试 Swap 请求。
func newSwapRequest() dexwallet.SwapRequest {
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
		SlippageBps: 200, // 2%
		Sender:      "SenderPubkey111111111111111111111111111111",
		Recipient:   "SenderPubkey111111111111111111111111111111",
	}
}

// truncateAddr 截断地址显示。
func truncateAddr(addr string) string {
	if len(addr) <= 12 {
		return addr
	}
	return addr[:6] + "..." + addr[len(addr)-4:]
}
