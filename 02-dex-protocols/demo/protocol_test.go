package main

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ----- AMM 测试 -----

func TestAMMQuote_BasicCorrectness(t *testing.T) {
	// 手动计算验证：
	// 储量 x=1000, y=2000000, 手续费 30bps
	// Sell 方向，amountIn=100
	// effectiveIn = 100 * (10000 - 30) = 100 * 9970 = 997000
	// amountOut = 2000000 * 997000 / (1000 * 10000 + 997000)
	//           = 1994000000000 / (10000000 + 997000)
	//           = 1994000000000 / 10997000
	//           = 181330（取整）
	amm := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	ctx := context.Background()

	pool := newAMMPool(big.NewInt(1000), big.NewInt(2000000), 30)
	amountIn := big.NewInt(100)

	quote, err := amm.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("Quote 失败: %v", err)
	}

	// 手动计算预期值
	expected := calcAMMOutput(amountIn, big.NewInt(1000), big.NewInt(2000000), 30)

	if quote.OutputAmount.Cmp(expected) != 0 {
		t.Errorf("输出金额不匹配: got %s, want %s", quote.OutputAmount.String(), expected.String())
	}

	// 验证价格影响为正
	if quote.PriceImpact == 0 {
		t.Error("价格影响应该大于 0（100 占 1000 储量的 10%）")
	}

	// 验证协议信息
	if quote.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("DexID 不匹配: got %s, want %s", quote.DexID, dexwallet.DexRaydiumAMM)
	}
	if quote.ProtocolType != dexwallet.ProtocolAMM {
		t.Errorf("ProtocolType 不匹配: got %s, want %s", quote.ProtocolType, dexwallet.ProtocolAMM)
	}
}

func TestAMMQuote_BuyDirection(t *testing.T) {
	amm := NewConstantProductAMM(dexwallet.DexUniswapV2)
	ctx := context.Background()

	pool := newAMMPool(big.NewInt(1000), big.NewInt(2000000), 30)
	amountIn := big.NewInt(10000) // 用 10000 quote 买 base

	quote, err := amm.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionBuy)
	if err != nil {
		t.Fatalf("Quote 失败: %v", err)
	}

	// 买入方向：reserveIn=2000000(quote), reserveOut=1000(base)
	expected := calcAMMOutput(amountIn, big.NewInt(2000000), big.NewInt(1000), 30)

	if quote.OutputAmount.Cmp(expected) != 0 {
		t.Errorf("输出金额不匹配: got %s, want %s", quote.OutputAmount.String(), expected.String())
	}

	// 输出应该小于 1000（全部储量），且为正数
	if quote.OutputAmount.Cmp(big.NewInt(1000)) >= 0 {
		t.Error("输出不应超过储量")
	}
	if quote.OutputAmount.Sign() <= 0 {
		t.Error("输出应为正数")
	}
}

func TestAMMQuote_ZeroReserves(t *testing.T) {
	amm := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	ctx := context.Background()

	// 储量为 0 的池子
	pool := newAMMPool(big.NewInt(0), big.NewInt(0), 30)

	_, err := amm.Quote(ctx, pool, big.NewInt(100), dexwallet.SwapDirectionSell)
	if err == nil {
		t.Error("零储量池子应该返回错误")
	}
}

func TestAMMQuote_ZeroAmountIn(t *testing.T) {
	amm := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	ctx := context.Background()

	pool := newAMMPool(big.NewInt(1000), big.NewInt(2000000), 30)

	_, err := amm.Quote(ctx, pool, big.NewInt(0), dexwallet.SwapDirectionSell)
	if err == nil {
		t.Error("零输入金额应该返回错误")
	}

	_, err = amm.Quote(ctx, pool, big.NewInt(-1), dexwallet.SwapDirectionSell)
	if err == nil {
		t.Error("负输入金额应该返回错误")
	}
}

func TestAMMQuote_LargeTradeExceedsReserve(t *testing.T) {
	amm := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	ctx := context.Background()

	pool := newAMMPool(big.NewInt(1000), big.NewInt(2000000), 30)

	// 非常大的交易量。注意 AMM 公式中输出永远小于储量，
	// 但我们仍然应该能正常计算。
	amountIn := big.NewInt(10000000) // 远大于 base 储量
	quote, err := amm.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("大额交易不应出错: %v", err)
	}

	// 输出应小于 quote 储量
	if quote.OutputAmount.Cmp(big.NewInt(2000000)) >= 0 {
		t.Error("输出不应超过储量")
	}
}

func TestAMM_SlippageIncreasesWithSize(t *testing.T) {
	amm := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	ctx := context.Background()

	pool := newAMMPool(big.NewInt(10000), big.NewInt(10000000), 30)

	amounts := []int64{1, 10, 100, 1000, 5000}
	var lastImpact uint64

	for _, amt := range amounts {
		quote, err := amm.Quote(ctx, pool, big.NewInt(amt), dexwallet.SwapDirectionSell)
		if err != nil {
			t.Fatalf("Quote 失败 (amount=%d): %v", amt, err)
		}

		if amt > 1 && quote.PriceImpact < lastImpact {
			t.Errorf("滑点应该随交易量递增: amount=%d impact=%d, 前一个 impact=%d",
				amt, quote.PriceImpact, lastImpact)
		}
		lastImpact = quote.PriceImpact
	}
}

func TestAMMGetPrice(t *testing.T) {
	amm := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	ctx := context.Background()

	pool := newAMMPool(big.NewInt(1000), big.NewInt(2000), 30)

	price, err := amm.GetPrice(ctx, pool)
	if err != nil {
		t.Fatalf("GetPrice 失败: %v", err)
	}

	// 价格应该是 2000/1000 * PricePrecision = 2 * 10^18
	expectedPrice := new(big.Int).Mul(big.NewInt(2), PricePrecision)
	if price.Cmp(expectedPrice) != 0 {
		t.Errorf("价格不匹配: got %s, want %s", price.String(), expectedPrice.String())
	}
}

func TestAMMNilPool(t *testing.T) {
	amm := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	ctx := context.Background()

	_, err := amm.Quote(ctx, nil, big.NewInt(100), dexwallet.SwapDirectionSell)
	if err == nil {
		t.Error("nil pool 应该返回错误")
	}

	_, err = amm.GetPrice(ctx, nil)
	if err == nil {
		t.Error("nil pool 应该返回错误")
	}
}

// ----- CLMM 测试 -----

func TestCLMMQuote_Basic(t *testing.T) {
	clmm := NewConcentratedLiquidityMM(dexwallet.DexRaydiumCLMM)
	ctx := context.Background()

	pool := newCLMMPool(100, []TickRange{
		{LowerTick: 50, UpperTick: 150, Liquidity: big.NewInt(1000000)},
	}, 25)

	quote, err := clmm.Quote(ctx, pool, big.NewInt(100), dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("Quote 失败: %v", err)
	}

	if quote.OutputAmount.Sign() <= 0 {
		t.Error("输出应为正数")
	}
	if quote.ProtocolType != dexwallet.ProtocolCLMM {
		t.Errorf("ProtocolType 不匹配: got %s", quote.ProtocolType)
	}
}

func TestCLMMQuote_NoActiveRange(t *testing.T) {
	clmm := NewConcentratedLiquidityMM(dexwallet.DexRaydiumCLMM)
	ctx := context.Background()

	// currentTick=200，但没有包含 200 的区间
	pool := newCLMMPool(200, []TickRange{
		{LowerTick: 50, UpperTick: 150, Liquidity: big.NewInt(1000000)},
	}, 25)

	_, err := clmm.Quote(ctx, pool, big.NewInt(100), dexwallet.SwapDirectionSell)
	if err == nil {
		t.Error("没有活跃区间时应该返回错误")
	}
}

func TestCLMM_HigherEfficiencyThanAMM(t *testing.T) {
	// CLMM 在窄范围内的等效流动性应该更大，
	// 因此同等底层流动性下滑点更低。
	ctx := context.Background()

	baseLiquidity := big.NewInt(1000000)

	// AMM 池子
	amm := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	ammPool := newAMMPool(new(big.Int).Set(baseLiquidity), new(big.Int).Set(baseLiquidity), 30)

	// CLMM 池子（窄范围，tickSpan=40）
	clmm := NewConcentratedLiquidityMM(dexwallet.DexRaydiumCLMM)
	clmmPool := newCLMMPool(100, []TickRange{
		{LowerTick: 80, UpperTick: 120, Liquidity: new(big.Int).Set(baseLiquidity)},
	}, 30)

	amountIn := big.NewInt(10000)

	ammQuote, err := amm.Quote(ctx, ammPool, amountIn, dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("AMM Quote 失败: %v", err)
	}

	clmmQuote, err := clmm.Quote(ctx, clmmPool, amountIn, dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("CLMM Quote 失败: %v", err)
	}

	// CLMM 应该给出更好的输出（因为集中流动性等效深度更大）
	if clmmQuote.OutputAmount.Cmp(ammQuote.OutputAmount) <= 0 {
		t.Errorf("CLMM 输出应该大于 AMM: CLMM=%s, AMM=%s",
			clmmQuote.OutputAmount.String(), ammQuote.OutputAmount.String())
	}

	t.Logf("AMM 输出: %s (影响: %d bps), CLMM 输出: %s (影响: %d bps)",
		ammQuote.OutputAmount.String(), ammQuote.PriceImpact,
		clmmQuote.OutputAmount.String(), clmmQuote.PriceImpact)
}

func TestCLMMQuote_CrossTick(t *testing.T) {
	clmm := NewConcentratedLiquidityMM(dexwallet.DexRaydiumCLMM)
	ctx := context.Background()

	// 创建多个 tick range 的池子
	// 当前 tick=100，活跃 range 为 [50, 150]
	// Sell 方向可用: [50,150], [0,50], [-50,0]（从 currentTick 向下）
	// Buy 方向可用: [50,150], [150,250], [250,350]（从 currentTick 向上）
	pool := newCLMMPool(100, []TickRange{
		{LowerTick: -50, UpperTick: 0, Liquidity: big.NewInt(500000)},
		{LowerTick: 0, UpperTick: 50, Liquidity: big.NewInt(500000)},
		{LowerTick: 50, UpperTick: 150, Liquidity: big.NewInt(500000)},
		{LowerTick: 150, UpperTick: 250, Liquidity: big.NewInt(500000)},
		{LowerTick: 250, UpperTick: 350, Liquidity: big.NewInt(500000)},
	}, 25)

	// 先用小额测试单个 range 能正常工作
	smallAmount := big.NewInt(10)
	smallQuote, err := clmm.Quote(ctx, pool, smallAmount, dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("小额 Quote 失败: %v", err)
	}
	if smallQuote.OutputAmount.Sign() <= 0 {
		t.Fatal("小额输出应为正数")
	}

	// 使用大额输入，需要跨越多个 tick range（Sell 方向）
	largeAmount := big.NewInt(1000000)
	largeSellQuote, err := clmm.Quote(ctx, pool, largeAmount, dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("大额 Sell Quote 失败: %v", err)
	}
	if largeSellQuote.OutputAmount.Sign() <= 0 {
		t.Error("大额 Sell 输出应为正数")
	}

	t.Logf("小额 Sell: 输入 %s, 输出 %s", smallAmount.String(), smallQuote.OutputAmount.String())
	t.Logf("大额 Sell: 输入 %s, 输出 %s", largeAmount.String(), largeSellQuote.OutputAmount.String())

	// 使用大额输入，Buy 方向
	largeBuyQuote, err := clmm.Quote(ctx, pool, largeAmount, dexwallet.SwapDirectionBuy)
	if err != nil {
		t.Fatalf("大额 Buy Quote 失败: %v", err)
	}
	if largeBuyQuote.OutputAmount.Sign() <= 0 {
		t.Error("大额 Buy 输出应为正数")
	}
	t.Logf("大额 Buy: 输入 %s, 输出 %s", largeAmount.String(), largeBuyQuote.OutputAmount.String())

	// 验证协议信息正确
	if largeSellQuote.ProtocolType != dexwallet.ProtocolCLMM {
		t.Errorf("ProtocolType 不匹配: got %s", largeSellQuote.ProtocolType)
	}
}

func TestCLMMQuote_CrossTick_ExhaustedAllRanges(t *testing.T) {
	clmm := NewConcentratedLiquidityMM(dexwallet.DexRaydiumCLMM)
	ctx := context.Background()

	// 创建流动性有限的池子
	pool := newCLMMPool(100, []TickRange{
		{LowerTick: 0, UpperTick: 50, Liquidity: big.NewInt(1000)},
		{LowerTick: 50, UpperTick: 150, Liquidity: big.NewInt(1000)},
	}, 25)

	// 超大额交易，应该耗尽所有 tick range（Sell 方向）
	hugeAmount := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil) // 10^18
	_, err := clmm.Quote(ctx, pool, hugeAmount, dexwallet.SwapDirectionSell)
	if err == nil {
		t.Error("超过所有 range 容量的交易应该返回错误")
	}
	expectedErr := "trade too large: exhausted all tick ranges"
	if err != nil && !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("错误信息不匹配: got %q, want 包含 %q", err.Error(), expectedErr)
	}

	// Buy 方向也测试
	poolBuy := newCLMMPool(100, []TickRange{
		{LowerTick: 50, UpperTick: 150, Liquidity: big.NewInt(1000)},
		{LowerTick: 150, UpperTick: 250, Liquidity: big.NewInt(1000)},
	}, 25)
	_, err = clmm.Quote(ctx, poolBuy, hugeAmount, dexwallet.SwapDirectionBuy)
	if err == nil {
		t.Error("超过所有 range 容量的 Buy 交易应该返回错误")
	}
	if err != nil && !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("Buy 错误信息不匹配: got %q, want 包含 %q", err.Error(), expectedErr)
	}
}

// ----- Bonding Curve 测试 -----

func TestBondingCurveQuote_LinearBuy(t *testing.T) {
	bc := NewBondingCurveProtocol(dexwallet.DexPumpFun)
	ctx := context.Background()

	pool := newBondingCurvePool(
		big.NewInt(1000),                    // currentSupply
		big.NewInt(1_000_000_000_000_000),   // a = 10^15 (即 0.001 缩放后)
		1,                                    // 线性
		big.NewInt(85_000000000),             // 毕业目标 85 SOL
		big.NewInt(0),                        // 已收集 0
		100,                                  // 1% 手续费
	)

	quote, err := bc.Quote(ctx, pool, big.NewInt(1000000), dexwallet.SwapDirectionBuy)
	if err != nil {
		t.Fatalf("Quote 失败: %v", err)
	}

	if quote.OutputAmount.Sign() <= 0 {
		t.Error("输出应为正数")
	}
	if quote.Priority != dexwallet.PriorityHigh {
		t.Errorf("Bonding Curve 应为高优先级: got %d", quote.Priority)
	}

	t.Logf("买入: 输入 1000000, 获得 %s 代币", quote.OutputAmount.String())
}

func TestBondingCurveQuote_SellExceedsSupply(t *testing.T) {
	bc := NewBondingCurveProtocol(dexwallet.DexPumpFun)
	ctx := context.Background()

	pool := newBondingCurvePool(
		big.NewInt(1000),
		big.NewInt(1_000_000_000_000_000),
		1,
		nil,
		big.NewInt(0),
		100,
	)

	// 卖出超过供应量
	_, err := bc.Quote(ctx, pool, big.NewInt(2000), dexwallet.SwapDirectionSell)
	if err == nil {
		t.Error("卖出超过供应量应该返回错误")
	}
}

func TestBondingCurve_GraduationTriggered(t *testing.T) {
	bc := NewBondingCurveProtocol(dexwallet.DexPumpFun)
	ctx := context.Background()

	pool := newBondingCurvePool(
		big.NewInt(1000000),
		big.NewInt(1_000_000_000_000),
		1,
		big.NewInt(100),  // 毕业目标 100
		big.NewInt(80),   // 已收集 80
		0,
	)

	// 输入 30，加上已收集的 80 = 110 >= 100，应触发毕业
	quote, err := bc.Quote(ctx, pool, big.NewInt(30), dexwallet.SwapDirectionBuy)
	if err != nil {
		t.Fatalf("Quote 失败: %v", err)
	}

	graduated, ok := quote.Pool.Extra["Graduated"]
	if !ok || !graduated.(bool) {
		t.Error("应该触发毕业")
	}

	t.Logf("毕业触发: 收集流动性 80 + 输入 30 = 110 >= 目标 100")
}

func TestBondingCurve_GraduationNotTriggered(t *testing.T) {
	bc := NewBondingCurveProtocol(dexwallet.DexPumpFun)
	ctx := context.Background()

	pool := newBondingCurvePool(
		big.NewInt(1000000),
		big.NewInt(1_000_000_000_000),
		1,
		big.NewInt(100),  // 毕业目标 100
		big.NewInt(10),   // 已收集 10
		0,
	)

	// 输入 5，加上已收集的 10 = 15 < 100，不应触发毕业
	quote, err := bc.Quote(ctx, pool, big.NewInt(5), dexwallet.SwapDirectionBuy)
	if err != nil {
		t.Fatalf("Quote 失败: %v", err)
	}

	graduated, ok := quote.Pool.Extra["Graduated"]
	if ok && graduated.(bool) {
		t.Error("不应触发毕业")
	}
}

func TestBondingCurve_PriceIncreasesWithSupply(t *testing.T) {
	bc := NewBondingCurveProtocol(dexwallet.DexPumpFun)
	ctx := context.Background()

	coeffA := big.NewInt(1_000_000_000_000_000)

	supplies := []int64{100, 1000, 10000, 100000}
	var lastPrice *big.Int

	for _, supply := range supplies {
		pool := newBondingCurvePool(
			big.NewInt(supply),
			coeffA,
			1,
			nil,
			big.NewInt(0),
			0,
		)

		price, err := bc.GetPrice(ctx, pool)
		if err != nil {
			t.Fatalf("GetPrice 失败 (supply=%d): %v", supply, err)
		}

		if lastPrice != nil && price.Cmp(lastPrice) <= 0 {
			t.Errorf("价格应该随供应量递增: supply=%d price=%s, 前一个 price=%s",
				supply, price.String(), lastPrice.String())
		}
		lastPrice = new(big.Int).Set(price)
	}
}

// ----- StableSwap 测试 -----

func TestStableSwapQuote_LowSlippage(t *testing.T) {
	ss := NewStableSwapProtocol(dexwallet.DexCurve)
	ctx := context.Background()

	pool := newStableSwapPool(
		big.NewInt(1000000),
		big.NewInt(1000000),
		100,
		4, // 0.04%
	)

	// 交易 1000 单位（0.1% 的储量），滑点应该非常小
	quote, err := ss.Quote(ctx, pool, big.NewInt(1000), dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("Quote 失败: %v", err)
	}

	if quote.OutputAmount.Sign() <= 0 {
		t.Error("输出应为正数")
	}

	// 对于稳定币，小额交易的价格影响应该非常小
	if quote.PriceImpact > 10 {
		t.Errorf("稳定币小额交易的价格影响应该很小: got %d bps", quote.PriceImpact)
	}

	t.Logf("输入: 1000, 输出: %s, 价格影响: %d bps", quote.OutputAmount.String(), quote.PriceImpact)
}

func TestStableSwap_LowerSlippageThanAMM(t *testing.T) {
	ctx := context.Background()

	reserveBase := big.NewInt(1000000)
	reserveQuote := big.NewInt(1000000)
	feeRate := uint64(4)

	// StableSwap
	ss := NewStableSwapProtocol(dexwallet.DexCurve)
	ssPool := newStableSwapPool(
		new(big.Int).Set(reserveBase),
		new(big.Int).Set(reserveQuote),
		100,
		feeRate,
	)

	// AMM
	amm := NewConstantProductAMM(dexwallet.DexUniswapV2)
	ammPool := newAMMPool(
		new(big.Int).Set(reserveBase),
		new(big.Int).Set(reserveQuote),
		feeRate,
	)

	amountIn := big.NewInt(100000) // 10% 的储量

	ssQuote, err := ss.Quote(ctx, ssPool, amountIn, dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("StableSwap Quote 失败: %v", err)
	}

	ammQuote, err := amm.Quote(ctx, ammPool, amountIn, dexwallet.SwapDirectionSell)
	if err != nil {
		t.Fatalf("AMM Quote 失败: %v", err)
	}

	// StableSwap 在等值资产交易中应该给出更好的输出
	if ssQuote.OutputAmount.Cmp(ammQuote.OutputAmount) <= 0 {
		t.Errorf("StableSwap 输出应该大于 AMM: SS=%s, AMM=%s",
			ssQuote.OutputAmount.String(), ammQuote.OutputAmount.String())
	}

	t.Logf("StableSwap: 输出 %s (影响 %d bps), AMM: 输出 %s (影响 %d bps)",
		ssQuote.OutputAmount.String(), ssQuote.PriceImpact,
		ammQuote.OutputAmount.String(), ammQuote.PriceImpact)
}

func TestStableSwap_HigherA_LowerSlippage(t *testing.T) {
	ss := NewStableSwapProtocol(dexwallet.DexCurve)
	ctx := context.Background()

	reserveBase := big.NewInt(1000000)
	reserveQuote := big.NewInt(1000000)
	amountIn := big.NewInt(50000) // 5% 的储量

	aValues := []int64{1, 10, 100, 1000}
	var lastOutput *big.Int

	for _, a := range aValues {
		pool := newStableSwapPool(
			new(big.Int).Set(reserveBase),
			new(big.Int).Set(reserveQuote),
			a,
			4,
		)

		quote, err := ss.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionSell)
		if err != nil {
			t.Fatalf("Quote 失败 (A=%d): %v", a, err)
		}

		if lastOutput != nil && quote.OutputAmount.Cmp(lastOutput) < 0 {
			t.Errorf("更大的 A 应该给出更好的输出: A=%d output=%s, 前一个 output=%s",
				a, quote.OutputAmount.String(), lastOutput.String())
		}
		lastOutput = new(big.Int).Set(quote.OutputAmount)

		t.Logf("A=%d: 输出 %s, 价格影响 %d bps", a, quote.OutputAmount.String(), quote.PriceImpact)
	}
}

func TestStableSwap_ZeroReserves(t *testing.T) {
	ss := NewStableSwapProtocol(dexwallet.DexCurve)
	ctx := context.Background()

	pool := newStableSwapPool(big.NewInt(0), big.NewInt(0), 100, 4)

	_, err := ss.Quote(ctx, pool, big.NewInt(100), dexwallet.SwapDirectionSell)
	if err == nil {
		t.Error("零储量池子应该返回错误")
	}
}

func TestCalcInvariantD(t *testing.T) {
	// 平衡池子：D 应该等于 x + y
	x := big.NewInt(1000000)
	y := big.NewInt(1000000)

	D := CalcInvariantD(x, y, 100)

	expectedD := new(big.Int).Add(x, y) // 2000000

	// D 应该接近 x + y（不完全相等，因为不变量方程不是纯粹的恒定和）
	diff := new(big.Int).Sub(D, expectedD)
	if diff.Sign() < 0 {
		diff.Neg(diff)
	}

	// 允许 1% 的误差
	tolerance := new(big.Int).Div(expectedD, big.NewInt(100))
	if diff.Cmp(tolerance) > 0 {
		t.Errorf("D 偏离 x+y 过多: D=%s, x+y=%s, diff=%s",
			D.String(), expectedD.String(), diff.String())
	}

	t.Logf("x=%s, y=%s, A=100, D=%s (期望接近 %s)", x.String(), y.String(), D.String(), expectedD.String())
}

// ----- 接口合规性测试 -----

func TestProtocolsImplementDexProtocol(t *testing.T) {
	// 编译时检查所有协议实现了 DexProtocol 接口
	var _ dexwallet.DexProtocol = NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	var _ dexwallet.DexProtocol = NewConcentratedLiquidityMM(dexwallet.DexRaydiumCLMM)
	var _ dexwallet.DexProtocol = NewBondingCurveProtocol(dexwallet.DexPumpFun)
	var _ dexwallet.DexProtocol = NewStableSwapProtocol(dexwallet.DexCurve)
}

// ----- 辅助函数 -----

func newAMMPool(reserveBase, reserveQuote *big.Int, feeRate uint64) *dexwallet.Pool {
	return &dexwallet.Pool{
		Address:      "TestAMMPool",
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseSymbol:   "BASE",
		QuoteSymbol:  "QUOTE",
		FeeRate:      feeRate,
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"ReserveBase":  new(big.Int).Set(reserveBase),
			"ReserveQuote": new(big.Int).Set(reserveQuote),
		},
	}
}

func newCLMMPool(currentTick int64, ranges []TickRange, feeRate uint64) *dexwallet.Pool {
	return &dexwallet.Pool{
		Address:      "TestCLMMPool",
		DexID:        dexwallet.DexRaydiumCLMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolCLMM,
		BaseSymbol:   "BASE",
		QuoteSymbol:  "QUOTE",
		FeeRate:      feeRate,
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"CurrentTick": currentTick,
			"TickRanges":  ranges,
		},
	}
}

func newBondingCurvePool(currentSupply, coefficientA *big.Int, exponent int64, graduationTarget, collectedLiquidity *big.Int, feeRate uint64) *dexwallet.Pool {
	extra := map[string]interface{}{
		"CurrentSupply":      new(big.Int).Set(currentSupply),
		"CoefficientA":       new(big.Int).Set(coefficientA),
		"Exponent":           exponent,
		"CollectedLiquidity": new(big.Int).Set(collectedLiquidity),
	}
	if graduationTarget != nil {
		extra["GraduationTarget"] = new(big.Int).Set(graduationTarget)
	}

	return &dexwallet.Pool{
		Address:      "TestBondingCurvePool",
		DexID:        dexwallet.DexPumpFun,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolBondingCurve,
		BaseSymbol:   "MEME",
		QuoteSymbol:  "SOL",
		FeeRate:      feeRate,
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra:        extra,
	}
}

func newStableSwapPool(reserveBase, reserveQuote *big.Int, amplificationCoeff int64, feeRate uint64) *dexwallet.Pool {
	invariantD := CalcInvariantD(reserveBase, reserveQuote, amplificationCoeff)

	return &dexwallet.Pool{
		Address:      "TestStableSwapPool",
		DexID:        dexwallet.DexCurve,
		ChainID:      coinset.ChainEthereum,
		ProtocolType: dexwallet.ProtocolStableSwap,
		BaseSymbol:   "USDC",
		QuoteSymbol:  "USDT",
		FeeRate:      feeRate,
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"ReserveBase":        new(big.Int).Set(reserveBase),
			"ReserveQuote":       new(big.Int).Set(reserveQuote),
			"AmplificationCoeff": amplificationCoeff,
			"InvariantD":         invariantD,
		},
	}
}

// ----- Benchmark 测试 -----

func BenchmarkAMMQuote(b *testing.B) {
	amm := NewConstantProductAMM(dexwallet.DexRaydiumAMM)
	ctx := context.Background()
	pool := newAMMPool(big.NewInt(1000000), big.NewInt(2000000000), 30)
	amountIn := big.NewInt(1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = amm.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionSell)
	}
}

func BenchmarkCLMMQuote(b *testing.B) {
	clmm := NewConcentratedLiquidityMM(dexwallet.DexRaydiumCLMM)
	ctx := context.Background()
	pool := newCLMMPool(100, []TickRange{
		{LowerTick: 50, UpperTick: 150, Liquidity: big.NewInt(1000000)},
	}, 25)
	amountIn := big.NewInt(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = clmm.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionSell)
	}
}

func BenchmarkStableSwapQuote(b *testing.B) {
	ss := NewStableSwapProtocol(dexwallet.DexCurve)
	ctx := context.Background()
	pool := newStableSwapPool(big.NewInt(1000000), big.NewInt(1000000), 100, 4)
	amountIn := big.NewInt(1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ss.Quote(ctx, pool, amountIn, dexwallet.SwapDirectionSell)
	}
}
