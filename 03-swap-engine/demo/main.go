package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strings"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

func main() {
	// 配置结构化日志
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("03-swap-engine: Swap 交易构建引擎演示")
	fmt.Println(strings.Repeat("=", 70))

	// ---- 初始化工厂并注册所有 Builder ----
	factory := setupFactory()

	// ---- 演示 1: Solana Swap (SOL -> MEME) ----
	demoSolanaSwap(factory)

	// ---- 演示 2: EVM Swap (BNB -> TOKEN) ----
	demoEVMSwap(factory)

	// ---- 演示 3: 交易模拟（成功 / 滑点过高 / 余额不足） ----
	demoSimulation(factory)

	// ---- 演示 4: 工厂模式选 DEX ----
	demoFactorySelection(factory)

	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("演示完成")
	fmt.Println(strings.Repeat("=", 70))
}

// setupFactory 创建工厂并注册所有 6 个 DEX Builder。
func setupFactory() *SwapBuilderFactory {
	fmt.Println("\n--- 初始化 SwapBuilderFactory ---")

	factory := NewSwapBuilderFactory()

	// Solana Builders
	raydium := NewRaydiumAMMBuilder()
	pumpFun := NewPumpFunBuilder()
	jupiter := NewJupiterBuilder(raydium, pumpFun)

	factory.Register(raydium)
	factory.Register(pumpFun)
	factory.Register(jupiter)

	// EVM Builders
	factory.Register(NewUniswapV2Builder())
	factory.Register(NewPancakeV3Builder())
	factory.Register(NewCurveBuilder())

	fmt.Printf("已注册 %d 个 Builder: %v\n", len(factory.ListBuilders()), factory.ListBuilders())
	return factory
}

// demoSolanaSwap 演示 Solana 上的 Swap 交易构建。
func demoSolanaSwap(factory *SwapBuilderFactory) {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 1: Solana Swap (SOL -> MEME via Raydium AMM)")
	fmt.Println(strings.Repeat("-", 70))

	ctx := context.Background()

	req := dexwallet.SwapRequest{
		ChainID:   coinset.ChainSolana,
		DexID:     dexwallet.DexRaydiumAMM,
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

	result, err := factory.Build(ctx, req)
	if err != nil {
		fmt.Printf("  [错误] Solana swap 构建失败: %v\n", err)
		return
	}

	printSwapResult("Raydium AMM", result)
}

// demoEVMSwap 演示 EVM 上的 Swap 交易构建。
func demoEVMSwap(factory *SwapBuilderFactory) {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 2: EVM Swap (BNB -> TOKEN via PancakeSwap V3)")
	fmt.Println(strings.Repeat("-", 70))

	ctx := context.Background()

	req := dexwallet.SwapRequest{
		ChainID:   coinset.ChainBSC,
		DexID:     dexwallet.DexPancakeV3,
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{
			Address:  "0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c",
			Symbol:   "BNB",
			Decimals: 18,
			ChainID:  coinset.ChainBSC,
		},
		ToToken: dexwallet.Token{
			Address:  "0x1234567890abcdef1234567890abcdef12345678",
			Symbol:   "TOKEN",
			Decimals: 18,
			ChainID:  coinset.ChainBSC,
		},
		Amount:      new(big.Int).Mul(big.NewInt(1), big.NewInt(1e18)), // 1 BNB
		SlippageBps: 100, // 1%
		Sender:      "0xSenderAddress1234567890abcdef12345678",
		Recipient:   "0xSenderAddress1234567890abcdef12345678",
	}

	result, err := factory.Build(ctx, req)
	if err != nil {
		fmt.Printf("  [错误] EVM swap 构建失败: %v\n", err)
		return
	}

	printSwapResult("PancakeSwap V3", result)
}

// demoSimulation 演示交易模拟的三种场景。
func demoSimulation(factory *SwapBuilderFactory) {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 3: 交易模拟")
	fmt.Println(strings.Repeat("-", 70))

	ctx := context.Background()

	// 场景 1: 正常交易（模拟成功）
	fmt.Println("\n  [场景 1] 正常交易 -- 预期成功")
	normalReq := dexwallet.SwapRequest{
		ChainID:   coinset.ChainSolana,
		DexID:     dexwallet.DexRaydiumAMM,
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

	result, err := factory.Build(ctx, normalReq)
	if err != nil {
		fmt.Printf("    构建失败: %v\n", err)
	} else {
		simErr := SimulateSwap(result)
		if simErr != nil {
			fmt.Printf("    模拟失败: %v\n", simErr)
		} else {
			fmt.Println("    模拟成功: 交易可以安全执行")
		}
	}

	// 场景 2: 滑点过高
	fmt.Println("\n  [场景 2] 滑点过高 -- 预期被拒绝")
	highSlippageResult := &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		Direction:    dexwallet.SwapDirectionBuy,
		InputAmount:  new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9)),
		OutputAmount: big.NewInt(1000000),
		MinOutput:    big.NewInt(400000), // 60% 滑点
		SlippageBps:  6000,              // 60% -- 超过 50% 上限
		GasCost:      big.NewInt(5000),
		TxData:       []byte("mock_tx_data"),
	}
	simErr := SimulateSwap(highSlippageResult)
	if simErr != nil {
		fmt.Printf("    模拟失败（预期）: %v\n", simErr)
	} else {
		fmt.Println("    模拟成功（意外）")
	}

	// 场景 3: 余额不足
	fmt.Println("\n  [场景 3] 余额不足 -- 预期被拒绝")
	lowBalanceResult := &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		Direction:    dexwallet.SwapDirectionBuy,
		InputAmount:  new(big.Int).Mul(big.NewInt(100), big.NewInt(1e9)), // 100 SOL（余额只有 10 SOL）
		OutputAmount: big.NewInt(100000000),
		MinOutput:    big.NewInt(98000000),
		SlippageBps:  200,
		GasCost:      big.NewInt(5000),
		PriorityFee:  big.NewInt(5000),
		TxData:       []byte("mock_tx_data"),
	}
	simErr = SimulateSwap(lowBalanceResult)
	if simErr != nil {
		fmt.Printf("    模拟失败（预期）: %v\n", simErr)
	} else {
		fmt.Println("    模拟成功（意外）")
	}
}

// demoFactorySelection 演示工厂模式选 DEX。
func demoFactorySelection(factory *SwapBuilderFactory) {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 4: 工厂模式选 DEX")
	fmt.Println(strings.Repeat("-", 70))

	// 列出所有已注册的 Builder
	builders := factory.ListBuilders()
	fmt.Printf("\n  已注册的 Builder: %v\n", builders)

	// 逐个查找并展示信息
	for _, id := range builders {
		builder, err := factory.Get(id)
		if err != nil {
			fmt.Printf("  [错误] %s: %v\n", id, err)
			continue
		}
		fmt.Printf("  - %s: chain=%s, protocol=%s\n",
			builder.DexID(), builder.ChainID(), builder.ProtocolType())
	}

	// 尝试查找不存在的 DEX
	fmt.Println("\n  尝试查找不存在的 DEX:")
	_, err := factory.Get("nonexistent_dex")
	if err != nil {
		fmt.Printf("  [预期错误] %v\n", err)
	}

	// 尝试链不匹配的请求
	fmt.Println("\n  尝试链不匹配的请求（Solana Builder 处理 BSC 请求）:")
	ctx := context.Background()
	mismatchReq := dexwallet.SwapRequest{
		ChainID:   coinset.ChainBSC, // BSC 链
		DexID:     dexwallet.DexRaydiumAMM, // Raydium 是 Solana 的
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{Symbol: "BNB", Decimals: 18},
		ToToken:   dexwallet.Token{Symbol: "TOKEN", Decimals: 18},
		Amount:    big.NewInt(1e18),
		Sender:    "0xSender",
	}
	_, err = factory.Build(ctx, mismatchReq)
	if err != nil {
		fmt.Printf("  [预期错误] %v\n", err)
	}
}

// printSwapResult 格式化打印 SwapResult。
func printSwapResult(dexName string, result *dexwallet.SwapResult) {
	fmt.Printf("\n  %s Swap 结果:\n", dexName)
	fmt.Printf("    DEX:          %s\n", result.DexID)
	fmt.Printf("    链:           %s\n", result.ChainID)
	fmt.Printf("    方向:         %s\n", result.Direction)
	fmt.Printf("    输入金额:     %s\n", result.InputAmount.String())
	fmt.Printf("    输出金额:     %s\n", result.OutputAmount.String())
	fmt.Printf("    最小输出:     %s\n", result.MinOutput.String())
	fmt.Printf("    滑点(BPS):    %d\n", result.SlippageBps)
	if result.PriorityFee != nil {
		fmt.Printf("    优先费:       %s\n", result.PriorityFee.String())
	}
	if result.GasCost != nil {
		fmt.Printf("    Gas 费用:     %s\n", result.GasCost.String())
	}
	fmt.Printf("    交易数据长度: %d bytes\n", len(result.TxData))
	if result.Extra != nil {
		fmt.Printf("    扩展信息:     %v\n", result.Extra)
	}
}
