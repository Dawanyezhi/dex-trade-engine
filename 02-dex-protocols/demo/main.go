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
	// 初始化结构化日志
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx := context.Background()

	slog.Info("== DEX 协议数学模型演示 ==")
	fmt.Println()

	// 创建四种协议实例
	ammProtocol := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	clmmProtocol := NewConcentratedLiquidityMM(dexwallet.DexRaydiumCLMM)
	bondingProtocol := NewBondingCurveProtocol(dexwallet.DexPumpFun)
	stableProtocol := NewStableSwapProtocol(dexwallet.DexCurve)

	// 验证接口实现
	var _ dexwallet.DexProtocol = ammProtocol
	var _ dexwallet.DexProtocol = clmmProtocol
	var _ dexwallet.DexProtocol = bondingProtocol
	var _ dexwallet.DexProtocol = stableProtocol

	// ===== 1. AMM 演示 =====
	fmt.Println("============================================================")
	fmt.Println("1. AMM 恒定乘积做市商 (x * y = k)")
	fmt.Println("============================================================")
	demoAMM(ctx, ammProtocol)

	// ===== 2. CLMM 演示 =====
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("2. CLMM 集中流动性做市商")
	fmt.Println("============================================================")
	demoCLMM(ctx, clmmProtocol)

	// ===== 3. Bonding Curve 演示 =====
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("3. Bonding Curve 联合曲线（内盘）")
	fmt.Println("============================================================")
	demoBondingCurve(ctx, bondingProtocol)

	// ===== 4. StableSwap 演示 =====
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("4. StableSwap 稳定币低滑点")
	fmt.Println("============================================================")
	demoStableSwap(ctx, stableProtocol)

	// ===== 5. 协议对比 =====
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("5. 协议对比：同一笔交易在不同协议下的输出")
	fmt.Println("============================================================")
	demoComparison(ctx, ammProtocol, clmmProtocol, stableProtocol)
}

// demoAMM 演示 AMM 恒定乘积做市商。
func demoAMM(ctx context.Context, protocol *ConstantProductAMM) {
	// 创建 AMM 池子：1000 SOL / 200000 USDC，手续费 30 bps (0.3%)
	pool := &dexwallet.Pool{
		Address:      "AMMpool1111111111111111111111111111111111111",
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     "So11111111111111111111111111111111111111112",
		QuoteMint:    "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		BaseSymbol:   "SOL",
		QuoteSymbol:  "USDC",
		BaseDecimal:  9,
		QuoteDecimal: 6,
		Liquidity:    big.NewInt(200000_000000), // 200000 USDC
		FeeRate:      30,                        // 0.3%
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"ReserveBase":  big.NewInt(1000_000000000),  // 1000 SOL (9 decimals)
			"ReserveQuote": big.NewInt(200000_000000),   // 200000 USDC (6 decimals)
		},
	}

	// 获取当前价格
	price, err := protocol.GetPrice(ctx, pool)
	if err != nil {
		slog.Error("获取价格失败", "error", err)
		return
	}
	fmt.Printf("\n池子: SOL/USDC (储量: 1000 SOL / 200000 USDC)\n")
	fmt.Printf("当前价格: %s (x10^-18 USDC/SOL)\n", price.String())
	fmt.Printf("即: 1 SOL = %.2f USDC\n", float64(price.Int64())/1e18*1e3) // 简化展示

	// 不同金额的报价对比
	fmt.Println("\n--- 卖出不同数量的 SOL，获得 USDC ---")
	printTableHeader("输入(SOL)", "输出(USDC)", "价格影响(bps)", "有效价格(USDC/SOL)")

	testAmounts := []int64{1, 10, 50, 100, 200, 500}
	for _, amt := range testAmounts {
		amountIn := big.NewInt(amt * 1_000000000) // 转换为最小单位
		quote, err := protocol.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionSell)
		if err != nil {
			fmt.Printf("| %-12d | 错误: %-40s |\n", amt, err.Error())
			continue
		}
		effectivePrice := float64(quote.OutputAmount.Int64()) / float64(amt) / 1e6 * 1e0
		fmt.Printf("| %-12d | %-14s | %-18d | %-20.4f |\n",
			amt,
			formatAmount(quote.OutputAmount, 6),
			quote.PriceImpact,
			effectivePrice)
	}
	printTableFooter()
}

// demoCLMM 演示 CLMM 集中流动性。
func demoCLMM(ctx context.Context, protocol *ConcentratedLiquidityMM) {
	// 创建 CLMM 池子
	pool := &dexwallet.Pool{
		Address:      "CLMMpool111111111111111111111111111111111111",
		DexID:        dexwallet.DexRaydiumCLMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolCLMM,
		BaseMint:     "So11111111111111111111111111111111111111112",
		QuoteMint:    "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		BaseSymbol:   "SOL",
		QuoteSymbol:  "USDC",
		BaseDecimal:  9,
		QuoteDecimal: 6,
		Liquidity:    big.NewInt(500000_000000),
		FeeRate:      25, // 0.25%
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"CurrentTick": int64(100),
			"TickRanges": []TickRange{
				{
					LowerTick: 50,
					UpperTick: 150,
					Liquidity: big.NewInt(200000_000000000), // 大量集中流动性
				},
				{
					LowerTick: 0,
					UpperTick: 200,
					Liquidity: big.NewInt(50000_000000000), // 宽范围流动性较少
				},
			},
		},
	}

	price, err := protocol.GetPrice(ctx, pool)
	if err != nil {
		slog.Error("获取价格失败", "error", err)
		return
	}
	fmt.Printf("\n池子: SOL/USDC (CLMM, 集中流动性在 Tick [50, 150])\n")
	fmt.Printf("当前 Tick: 100, 价格: %s (x10^-18)\n", price.String())

	fmt.Println("\n--- CLMM vs 全范围 AMM 的滑点对比 ---")
	fmt.Println("（CLMM 在活跃区间内的等效流动性远大于全范围 AMM）")

	printTableHeader("输入(SOL)", "CLMM输出(USDC)", "价格影响(bps)", "备注")

	testAmounts := []int64{1, 10, 50, 100}
	for _, amt := range testAmounts {
		amountIn := big.NewInt(amt * 1_000000000)
		quote, err := protocol.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionSell)
		if err != nil {
			fmt.Printf("| %-12d | 错误: %-40s |\n", amt, err.Error())
			continue
		}
		note := ""
		if quote.PriceImpact < 10 {
			note = "极低滑点"
		} else if quote.PriceImpact < 50 {
			note = "低滑点"
		} else {
			note = "中等滑点"
		}
		fmt.Printf("| %-12d | %-18s | %-18d | %-20s |\n",
			amt,
			formatAmount(quote.OutputAmount, 6),
			quote.PriceImpact,
			note)
	}
	printTableFooter()
}

// demoBondingCurve 演示 Bonding Curve 联合曲线。
func demoBondingCurve(ctx context.Context, protocol *BondingCurveProtocol) {
	// 创建 Bonding Curve 池子（PumpFun 风格）
	// 线性曲线：price = a * supply, a = 10^12 (缩放后)
	// 当前供应量：1000000 个代币
	// 毕业目标：85 SOL
	pool := &dexwallet.Pool{
		Address:      "BCpool11111111111111111111111111111111111111",
		DexID:        dexwallet.DexPumpFun,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolBondingCurve,
		BaseMint:     "NewToken1111111111111111111111111111111111111",
		QuoteMint:    "So11111111111111111111111111111111111111112",
		BaseSymbol:   "MEME",
		QuoteSymbol:  "SOL",
		BaseDecimal:  6,
		QuoteDecimal: 9,
		FeeRate:      100, // 1%
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"CurrentSupply":      big.NewInt(1000000),        // 已售 1000000 个代币
			"CoefficientA":       big.NewInt(1_000_000_000_000), // a = 10^12 (缩放因子为 10^18)
			"Exponent":           int64(1),                   // 线性曲线
			"GraduationTarget":   big.NewInt(85_000000000),   // 85 SOL
			"CollectedLiquidity": big.NewInt(20_000000000),   // 已收集 20 SOL
		},
	}

	price, err := protocol.GetPrice(ctx, pool)
	if err != nil {
		slog.Error("获取价格失败", "error", err)
		return
	}
	fmt.Printf("\n池子: MEME/SOL (Bonding Curve, 线性曲线)\n")
	fmt.Printf("当前供应量: 1,000,000 MEME\n")
	fmt.Printf("当前价格: %s lamports/MEME\n", price.String())
	fmt.Printf("毕业目标: 85 SOL (已收集: 20 SOL)\n")

	fmt.Println("\n--- 用不同金额的 SOL 购买 MEME ---")
	printTableHeader("输入(SOL)", "获得(MEME)", "价格影响(bps)", "是否触发毕业")

	testAmounts := []int64{1, 5, 10, 50, 65} // 65 SOL 应触发毕业
	for _, amt := range testAmounts {
		amountIn := big.NewInt(amt * 1_000000000) // 转换为 lamports
		quote, err := protocol.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionBuy)
		if err != nil {
			fmt.Printf("| %-12d | 错误: %-48s |\n", amt, err.Error())
			continue
		}
		graduated := ""
		if g, ok := quote.Pool.Extra["Graduated"]; ok && g.(bool) {
			graduated = "是 (迁移到 AMM)"
		} else {
			graduated = "否"
		}
		fmt.Printf("| %-12d | %-14s | %-18d | %-20s |\n",
			amt,
			quote.OutputAmount.String(),
			quote.PriceImpact,
			graduated)
	}
	printTableFooter()

	// 演示价格随供应量变化
	fmt.Println("\n--- 价格随供应量变化（线性曲线 price = a * supply）---")
	printTableHeader("供应量", "价格(缩放后)", "相对初始价格", "")

	supplyPoints := []int64{100000, 500000, 1000000, 2000000, 5000000, 10000000}
	basePrice := calcBondingCurvePrice(big.NewInt(supplyPoints[0]),
		pool.Extra["CoefficientA"].(*big.Int), int64(1))

	for _, supply := range supplyPoints {
		p := calcBondingCurvePrice(big.NewInt(supply),
			pool.Extra["CoefficientA"].(*big.Int), int64(1))
		ratio := "1.0x"
		if basePrice.Sign() > 0 {
			r := new(big.Int).Mul(p, big.NewInt(10))
			r.Div(r, basePrice)
			ratio = fmt.Sprintf("%.1fx", float64(r.Int64())/10.0)
		}
		fmt.Printf("| %-12d | %-18s | %-18s | %-20s |\n",
			supply, p.String(), ratio, "")
	}
	printTableFooter()
}

// demoStableSwap 演示 StableSwap 稳定币低滑点。
func demoStableSwap(ctx context.Context, protocol *StableSwapProtocol) {
	// 创建 StableSwap 池子：1000000 USDC / 1000000 USDT，A=100
	reserveBase := big.NewInt(1000000_000000)  // 1M USDC
	reserveQuote := big.NewInt(1000000_000000) // 1M USDT
	amplificationCoeff := int64(100)

	// 计算不变量 D
	invariantD := CalcInvariantD(reserveBase, reserveQuote, amplificationCoeff)

	pool := &dexwallet.Pool{
		Address:      "StablePool1111111111111111111111111111111111",
		DexID:        dexwallet.DexCurve,
		ChainID:      coinset.ChainEthereum,
		ProtocolType: dexwallet.ProtocolStableSwap,
		BaseMint:     "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		QuoteMint:    "0xdAC17F958D2ee523a2206206994597C13D831ec7",
		BaseSymbol:   "USDC",
		QuoteSymbol:  "USDT",
		BaseDecimal:  6,
		QuoteDecimal: 6,
		Liquidity:    big.NewInt(2000000_000000),
		FeeRate:      4, // 0.04%（Curve 的手续费很低）
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"ReserveBase":        reserveBase,
			"ReserveQuote":       reserveQuote,
			"AmplificationCoeff": amplificationCoeff,
			"InvariantD":         invariantD,
		},
	}

	price, err := protocol.GetPrice(ctx, pool)
	if err != nil {
		slog.Error("获取价格失败", "error", err)
		return
	}
	fmt.Printf("\n池子: USDC/USDT (StableSwap, A=%d)\n", amplificationCoeff)
	fmt.Printf("储量: 1,000,000 USDC / 1,000,000 USDT\n")
	fmt.Printf("当前价格: %s (x10^-18, 接近 1.0)\n", price.String())
	fmt.Printf("不变量 D: %s\n", invariantD.String())

	fmt.Println("\n--- StableSwap vs AMM 滑点对比 ---")
	printTableHeader("输入(USDC)", "StableSwap输出(USDT)", "SS价格影响(bps)", "AMM价格影响(bps)")

	// 创建同储量的 AMM 池子做对比
	ammPool := &dexwallet.Pool{
		FeeRate: 4, // 与 StableSwap 相同的费率
		Extra: map[string]interface{}{
			"ReserveBase":  new(big.Int).Set(reserveBase),
			"ReserveQuote": new(big.Int).Set(reserveQuote),
		},
	}

	testAmounts := []int64{1000, 10000, 50000, 100000, 500000}
	for _, amt := range testAmounts {
		amountIn := big.NewInt(amt * 1_000000) // 转换为最小单位

		// StableSwap 报价
		ssQuote, ssErr := protocol.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionSell)

		// AMM 报价（对比用）
		ammAmountOut := calcAMMOutput(amountIn,
			ammPool.Extra["ReserveBase"].(*big.Int),
			ammPool.Extra["ReserveQuote"].(*big.Int),
			ammPool.FeeRate)
		ammImpact := calcPriceImpact(amountIn, ammAmountOut,
			ammPool.Extra["ReserveBase"].(*big.Int),
			ammPool.Extra["ReserveQuote"].(*big.Int))

		if ssErr != nil {
			fmt.Printf("| %-14d | 错误: %-28s | %-18s | %-18d |\n",
				amt, ssErr.Error(), "-", ammImpact)
			continue
		}

		fmt.Printf("| %-14d | %-24s | %-18d | %-18d |\n",
			amt,
			formatAmount(ssQuote.OutputAmount, 6),
			ssQuote.PriceImpact,
			ammImpact)
	}
	printTableFooter()

	// 演示 A 参数的影响
	fmt.Println("\n--- A 参数对 10 万 USDC 交易的影响 ---")
	printTableHeader("A 参数", "输出(USDT)", "价格影响(bps)", "说明")

	testAmplification := []int64{1, 10, 100, 500, 1000}
	for _, amp := range testAmplification {
		testD := CalcInvariantD(reserveBase, reserveQuote, amp)
		testPool := &dexwallet.Pool{
			DexID:        dexwallet.DexCurve,
			ProtocolType: dexwallet.ProtocolStableSwap,
			FeeRate:      4,
			Extra: map[string]interface{}{
				"ReserveBase":        new(big.Int).Set(reserveBase),
				"ReserveQuote":       new(big.Int).Set(reserveQuote),
				"AmplificationCoeff": amp,
				"InvariantD":         testD,
			},
		}

		amountIn := big.NewInt(100000_000000) // 10 万 USDC
		q, err := protocol.Quote(ctx, testPool, amountIn, dexwallet.SwapDirectionSell)

		desc := ""
		switch {
		case amp <= 1:
			desc = "接近恒定积"
		case amp <= 10:
			desc = "弱稳定"
		case amp <= 100:
			desc = "一般稳定"
		case amp <= 500:
			desc = "强稳定"
		default:
			desc = "极强稳定"
		}

		if err != nil {
			fmt.Printf("| %-12d | 错误: %-28s | %-18s | %-20s |\n",
				amp, err.Error(), "-", desc)
			continue
		}

		fmt.Printf("| %-12d | %-18s | %-18d | %-20s |\n",
			amp,
			formatAmount(q.OutputAmount, 6),
			q.PriceImpact,
			desc)
	}
	printTableFooter()
}

// demoComparison 对比同一笔交易在不同协议下的输出。
func demoComparison(ctx context.Context, amm *ConstantProductAMM, clmm *ConcentratedLiquidityMM, stable *StableSwapProtocol) {
	fmt.Println("\n场景：用 10000 单位 quote 代币换取 base 代币")
	fmt.Println("（三种协议使用相同储量和费率以进行公平对比）")

	commonReserveBase := big.NewInt(1000000)
	commonReserveQuote := big.NewInt(1000000)
	commonFeeRate := uint64(30)
	amountIn := big.NewInt(10000)

	// AMM 池子
	ammPool := &dexwallet.Pool{
		DexID:        dexwallet.DexRaydiumAMM,
		ProtocolType: dexwallet.ProtocolAMM,
		FeeRate:      commonFeeRate,
		Extra: map[string]interface{}{
			"ReserveBase":  new(big.Int).Set(commonReserveBase),
			"ReserveQuote": new(big.Int).Set(commonReserveQuote),
		},
	}

	// CLMM 池子（窄范围，高效率）
	clmmPool := &dexwallet.Pool{
		DexID:        dexwallet.DexRaydiumCLMM,
		ProtocolType: dexwallet.ProtocolCLMM,
		FeeRate:      commonFeeRate,
		Extra: map[string]interface{}{
			"CurrentTick": int64(100),
			"TickRanges": []TickRange{
				{
					LowerTick: 80,
					UpperTick: 120,
					Liquidity: new(big.Int).Set(commonReserveBase), // 与 AMM 相同的底层流动性
				},
			},
		},
	}

	// StableSwap 池子
	invariantD := CalcInvariantD(commonReserveBase, commonReserveQuote, 100)
	stablePool := &dexwallet.Pool{
		DexID:        dexwallet.DexCurve,
		ProtocolType: dexwallet.ProtocolStableSwap,
		FeeRate:      commonFeeRate,
		Extra: map[string]interface{}{
			"ReserveBase":        new(big.Int).Set(commonReserveBase),
			"ReserveQuote":       new(big.Int).Set(commonReserveQuote),
			"AmplificationCoeff": int64(100),
			"InvariantD":         invariantD,
		},
	}

	printTableHeader("协议", "DexID", "输出金额", "价格影响(bps)")

	// AMM
	ammQuote, err := amm.Quote(ctx, ammPool, amountIn, dexwallet.SwapDirectionBuy)
	if err != nil {
		fmt.Printf("| %-18s | %-18s | 错误: %-18s | %-18s |\n", "AMM", dexwallet.DexRaydiumAMM, err.Error(), "-")
	} else {
		fmt.Printf("| %-18s | %-18s | %-18s | %-18d |\n", "AMM", ammQuote.DexID, ammQuote.OutputAmount.String(), ammQuote.PriceImpact)
	}

	// CLMM
	clmmQuote, err := clmm.Quote(ctx, clmmPool, amountIn, dexwallet.SwapDirectionBuy)
	if err != nil {
		fmt.Printf("| %-18s | %-18s | 错误: %-18s | %-18s |\n", "CLMM", dexwallet.DexRaydiumCLMM, err.Error(), "-")
	} else {
		fmt.Printf("| %-18s | %-18s | %-18s | %-18d |\n", "CLMM", clmmQuote.DexID, clmmQuote.OutputAmount.String(), clmmQuote.PriceImpact)
	}

	// StableSwap
	stableQuote, err := stable.Quote(ctx, stablePool, amountIn, dexwallet.SwapDirectionBuy)
	if err != nil {
		fmt.Printf("| %-18s | %-18s | 错误: %-18s | %-18s |\n", "StableSwap", dexwallet.DexCurve, err.Error(), "-")
	} else {
		fmt.Printf("| %-18s | %-18s | %-18s | %-18d |\n", "StableSwap", stableQuote.DexID, stableQuote.OutputAmount.String(), stableQuote.PriceImpact)
	}

	printTableFooter()

	fmt.Println("\n说明:")
	fmt.Println("- AMM: 全范围流动性，标准恒定乘积滑点")
	fmt.Println("- CLMM: 集中流动性（窄范围），等效深度更大，滑点更低")
	fmt.Println("- StableSwap: 稳定币优化，在 1:1 附近滑点极低（适用于等值资产）")
}

// ----- 辅助函数 -----

// formatAmount 格式化金额显示（带小数点）。
func formatAmount(amount *big.Int, decimals int) string {
	if amount == nil {
		return "0"
	}
	s := amount.String()
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	intPart := s[:len(s)-decimals]
	decPart := s[len(s)-decimals:]
	// 截取前 4 位小数
	if len(decPart) > 4 {
		decPart = decPart[:4]
	}
	return intPart + "." + decPart
}

// printTableHeader 打印表格头。
func printTableHeader(cols ...string) {
	fmt.Println(strings.Repeat("-", 80))
	fmt.Print("|")
	for _, col := range cols {
		fmt.Printf(" %-18s |", col)
	}
	fmt.Println()
	fmt.Println(strings.Repeat("-", 80))
}

// printTableFooter 打印表格尾。
func printTableFooter() {
	fmt.Println(strings.Repeat("-", 80))
}
