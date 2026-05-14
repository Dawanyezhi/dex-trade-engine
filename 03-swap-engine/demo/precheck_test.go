package main

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// 测试辅助函数
// ============================================================

// newTestSwapRequest 创建用于测试的标准 SwapRequest。
func newTestSwapRequest(amount *big.Int, slippageBps uint64) dexwallet.SwapRequest {
	return dexwallet.SwapRequest{
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
		Amount:      amount,
		SlippageBps: slippageBps,
		Sender:      "SenderPubkey111111111111111111111111111111",
		Recipient:   "SenderPubkey111111111111111111111111111111",
	}
}

// newTestPool 创建用于测试的标准 Pool。
func newTestPool(state dexwallet.PoolState) *dexwallet.Pool {
	return &dexwallet.Pool{
		Address:      "PoolAddress111111111111111111111111111111",
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     "MEMExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		QuoteMint:    "So11111111111111111111111111111111111111112",
		BaseSymbol:   "MEME",
		QuoteSymbol:  "SOL",
		BaseDecimal:  6,
		QuoteDecimal: 9,
		Liquidity:    new(big.Int).Mul(big.NewInt(1000), big.NewInt(1e9)),
		FeeRate:      25,
		State:        state,
	}
}

// newTestSwapResult 创建用于测试的标准 SwapResult。
func newTestSwapResult(outputAmount, minOutput, priorityFee, gasCost *big.Int, txData []byte) *dexwallet.SwapResult {
	return &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		Direction:    dexwallet.SwapDirectionBuy,
		InputAmount:  new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9)),
		OutputAmount: outputAmount,
		MinOutput:    minOutput,
		SlippageBps:  200,
		PriorityFee:  priorityFee,
		GasCost:      gasCost,
		TxData:       txData,
	}
}

// ============================================================
// PreChecker 测试
// ============================================================

// TestPreCheck_InsufficientBalance 测试余额不足的场景。
// 当用户余额不够支付交易金额 + 预估 Gas 费时应返回 NonDegradableError。
func TestPreCheck_InsufficientBalance(t *testing.T) {
	pc := NewPreChecker()
	ctx := context.Background()

	// 请求 500000 最小单位，但余额只有 100000
	req := newTestSwapRequest(big.NewInt(500000), 200)
	balance := big.NewInt(100000) // 远小于 amount + estimatedFee
	pool := newTestPool(dexwallet.PoolStateActive)

	err := pc.Check(ctx, req, balance, pool)
	if err == nil {
		t.Fatal("预期余额不足时返回错误，实际返回 nil")
	}

	// 验证错误类型为 NonDegradableError
	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}

	// 验证内部包含 InsufficientBalanceError
	var ibe *dexwallet.InsufficientBalanceError
	if !errors.As(err, &ibe) {
		t.Fatalf("预期内部包含 InsufficientBalanceError，实际得到: %v", err)
	}
}

// TestPreCheck_SlippageTooHigh 测试滑点过高的场景。
// 当滑点设置超过 maxSlippageBps（默认 5000 = 50%）时应被拒绝。
func TestPreCheck_SlippageTooHigh(t *testing.T) {
	pc := NewPreChecker()
	ctx := context.Background()

	// 滑点设置为 6000 BPS (60%)，超过默认上限 5000 BPS (50%)
	req := newTestSwapRequest(big.NewInt(100000), 6000)
	balance := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9)) // 充足的余额
	pool := newTestPool(dexwallet.PoolStateActive)

	err := pc.Check(ctx, req, balance, pool)
	if err == nil {
		t.Fatal("预期滑点过高时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}

	// 验证内部包含 SlippageExceededError
	var see *dexwallet.SlippageExceededError
	if !errors.As(err, &see) {
		t.Fatalf("预期内部包含 SlippageExceededError，实际得到: %v", err)
	}
	if see.Actual != 6000 || see.Threshold != 5000 {
		t.Errorf("预期 SlippageExceededError{Actual: 6000, Threshold: 5000}，得到 {Actual: %d, Threshold: %d}",
			see.Actual, see.Threshold)
	}
}

// TestPreCheck_SlippageTooLow 测试滑点过低的场景。
// 当滑点设置低于 minSlippageBps（默认 10 = 0.1%）时应被拒绝，因为交易极可能失败。
func TestPreCheck_SlippageTooLow(t *testing.T) {
	pc := NewPreChecker()
	ctx := context.Background()

	// 滑点设置为 5 BPS (0.05%)，低于默认下限 10 BPS (0.1%)
	req := newTestSwapRequest(big.NewInt(100000), 5)
	balance := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9))
	pool := newTestPool(dexwallet.PoolStateActive)

	err := pc.Check(ctx, req, balance, pool)
	if err == nil {
		t.Fatal("预期滑点过低时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}

// TestPreCheck_PoolInactive 测试池子非活跃状态的场景。
// 当池子状态不是 "active" 时应被拒绝。
func TestPreCheck_PoolInactive(t *testing.T) {
	pc := NewPreChecker()
	ctx := context.Background()

	req := newTestSwapRequest(big.NewInt(100000), 200)
	balance := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9))
	pool := newTestPool(dexwallet.PoolStateInactive) // 非活跃池子

	err := pc.Check(ctx, req, balance, pool)
	if err == nil {
		t.Fatal("预期池子非活跃时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}

// TestPreCheck_AmountTooSmall 测试交易金额过小的场景（灰尘过滤）。
func TestPreCheck_AmountTooSmall(t *testing.T) {
	pc := NewPreChecker()
	ctx := context.Background()

	// 金额为 100，低于默认最小值 1000
	req := newTestSwapRequest(big.NewInt(100), 200)
	balance := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9))
	pool := newTestPool(dexwallet.PoolStateActive)

	err := pc.Check(ctx, req, balance, pool)
	if err == nil {
		t.Fatal("预期金额过小时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}

// TestPreCheck_DexDisabled 测试 DEX 被禁用的场景。
func TestPreCheck_DexDisabled(t *testing.T) {
	pc := NewPreChecker()
	disabledDexes := []dexwallet.DexID{dexwallet.DexRaydiumAMM, dexwallet.DexPumpFun}

	// Raydium 在禁用列表中
	err := pc.checkDexEnabled(dexwallet.DexRaydiumAMM, disabledDexes)
	if err == nil {
		t.Fatal("预期 DEX 被禁用时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}

	// Jupiter 不在禁用列表中
	err = pc.checkDexEnabled(dexwallet.DexJupiter, disabledDexes)
	if err != nil {
		t.Fatalf("Jupiter 未被禁用，不应返回错误: %v", err)
	}
}

// TestPreCheck_PoolNil 测试池子为 nil 的场景。
func TestPreCheck_PoolNil(t *testing.T) {
	pc := NewPreChecker()
	ctx := context.Background()

	req := newTestSwapRequest(big.NewInt(100000), 200)
	balance := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9))

	err := pc.Check(ctx, req, balance, nil) // nil 池子
	if err == nil {
		t.Fatal("预期池子为 nil 时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}

// TestPreCheck_AllPassed 测试所有检查均通过的场景。
func TestPreCheck_AllPassed(t *testing.T) {
	pc := NewPreChecker()
	ctx := context.Background()

	req := newTestSwapRequest(big.NewInt(100000), 200)
	balance := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9)) // 10 SOL，充足
	pool := newTestPool(dexwallet.PoolStateActive)

	err := pc.Check(ctx, req, balance, pool)
	if err != nil {
		t.Fatalf("预期所有检查通过，实际得到错误: %v", err)
	}
}

// ============================================================
// PostChecker 测试
// ============================================================

// TestPostCheck_OutputBelowMinimum 测试输出金额低于最小输出的场景。
// 当 outputAmount < minOutput 时表示滑点保护被触发。
func TestPostCheck_OutputBelowMinimum(t *testing.T) {
	pc := NewPostChecker()
	ctx := context.Background()
	req := newTestSwapRequest(big.NewInt(100000), 200)

	// outputAmount (800) < minOutput (1000)
	result := newTestSwapResult(
		big.NewInt(800),        // outputAmount
		big.NewInt(1000),       // minOutput（大于 output）
		big.NewInt(5000),       // priorityFee
		big.NewInt(5000),       // gasCost
		[]byte("mock_tx_data"), // txData
	)

	err := pc.Check(ctx, req, result)
	if err == nil {
		t.Fatal("预期输出低于最小值时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}

// TestPostCheck_PriorityFeeTooHigh 测试优先费过高的场景。
// 防止异常高的优先费导致资金浪费。
func TestPostCheck_PriorityFeeTooHigh(t *testing.T) {
	pc := NewPostChecker()
	ctx := context.Background()
	req := newTestSwapRequest(big.NewInt(100000), 200)

	// 优先费远超阈值（默认 100 Gwei = 100 * 1e9）
	hugePriorityFee := new(big.Int).Mul(big.NewInt(1000), big.NewInt(1e9)) // 1000 Gwei
	result := newTestSwapResult(
		big.NewInt(100000),
		big.NewInt(98000),
		hugePriorityFee,
		big.NewInt(5000),
		[]byte("mock_tx_data"),
	)

	err := pc.Check(ctx, req, result)
	if err == nil {
		t.Fatal("预期优先费过高时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}

// TestPostCheck_GasCostTooHigh 测试 Gas 费用过高的场景。
func TestPostCheck_GasCostTooHigh(t *testing.T) {
	pc := NewPostChecker()
	ctx := context.Background()
	req := newTestSwapRequest(big.NewInt(100000), 200)

	// Gas 费用远超阈值（默认 1 ETH = 1e18）
	hugeGas := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18)) // 10 ETH
	result := newTestSwapResult(
		big.NewInt(100000),
		big.NewInt(98000),
		big.NewInt(5000),
		hugeGas,
		[]byte("mock_tx_data"),
	)

	err := pc.Check(ctx, req, result)
	if err == nil {
		t.Fatal("预期 Gas 费过高时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}

// TestPostCheck_EmptyTxData 测试交易数据为空的场景。
func TestPostCheck_EmptyTxData(t *testing.T) {
	pc := NewPostChecker()
	ctx := context.Background()
	req := newTestSwapRequest(big.NewInt(100000), 200)

	result := newTestSwapResult(
		big.NewInt(100000),
		big.NewInt(98000),
		big.NewInt(5000),
		big.NewInt(5000),
		[]byte{}, // 空的交易数据
	)

	err := pc.Check(ctx, req, result)
	if err == nil {
		t.Fatal("预期交易数据为空时返回错误，实际返回 nil")
	}

	var nde *dexwallet.NonDegradableError
	if !errors.As(err, &nde) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}

// TestPostCheck_AllPassed 测试所有后检查均通过的场景。
func TestPostCheck_AllPassed(t *testing.T) {
	pc := NewPostChecker()
	ctx := context.Background()
	req := newTestSwapRequest(big.NewInt(100000), 200)

	result := newTestSwapResult(
		big.NewInt(100000),     // outputAmount
		big.NewInt(98000),      // minOutput（合理范围内）
		big.NewInt(5000),       // priorityFee（远低于阈值）
		big.NewInt(5000),       // gasCost（远低于阈值）
		[]byte("mock_tx_data"), // 非空 txData
	)

	err := pc.Check(ctx, req, result)
	if err != nil {
		t.Fatalf("预期所有后检查通过，实际得到错误: %v", err)
	}
}

// ============================================================
// SwapValidator 完整流程测试
// ============================================================

// TestSwapValidator_FullFlow 测试 SwapValidator 的完整 PreCheck + PostCheck 流程。
// 模拟一个正常交易的完整验证周期。
func TestSwapValidator_FullFlow(t *testing.T) {
	validator := NewSwapValidator()
	ctx := context.Background()

	// 构造合理的请求
	req := newTestSwapRequest(
		new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9)), // 1 SOL
		200, // 2% 滑点
	)
	balance := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9)) // 10 SOL
	pool := newTestPool(dexwallet.PoolStateActive)

	// 步骤 1: PreCheck 应该通过
	err := validator.PreCheck(ctx, req, balance, pool)
	if err != nil {
		t.Fatalf("PreCheck 应该通过，实际得到错误: %v", err)
	}

	// 步骤 2: 模拟构建交易（使用 RaydiumAMMBuilder）
	builder := NewRaydiumAMMBuilder()
	result, err := builder.Build(ctx, req)
	if err != nil {
		t.Fatalf("Build 应该成功，实际得到错误: %v", err)
	}

	// 步骤 3: PostCheck 应该通过
	err = validator.PostCheck(ctx, req, result)
	if err != nil {
		t.Fatalf("PostCheck 应该通过，实际得到错误: %v", err)
	}
}

// TestSwapValidator_PreCheckFail 测试 PreCheck 失败后不应继续构建的场景。
func TestSwapValidator_PreCheckFail(t *testing.T) {
	validator := NewSwapValidator()
	ctx := context.Background()

	// 余额不足的请求
	req := newTestSwapRequest(
		new(big.Int).Mul(big.NewInt(100), big.NewInt(1e9)), // 100 SOL
		200,
	)
	balance := big.NewInt(1000) // 远不够
	pool := newTestPool(dexwallet.PoolStateActive)

	err := validator.PreCheck(ctx, req, balance, pool)
	if err == nil {
		t.Fatal("PreCheck 应该因余额不足而失败")
	}

	// 验证是 NonDegradable 错误（不应降级到其他 DEX）
	if !dexwallet.IsNonDegradable(err) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}

// TestSwapValidator_PostCheckFail 测试 PostCheck 失败的场景。
func TestSwapValidator_PostCheckFail(t *testing.T) {
	validator := NewSwapValidator()
	ctx := context.Background()

	req := newTestSwapRequest(big.NewInt(100000), 200)

	// 构造一个输出异常的结果（output < minOutput）
	badResult := &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		Direction:    dexwallet.SwapDirectionBuy,
		InputAmount:  big.NewInt(100000),
		OutputAmount: big.NewInt(500),  // 极低的输出
		MinOutput:    big.NewInt(1000), // 大于 output
		SlippageBps:  200,
		PriorityFee:  big.NewInt(5000),
		GasCost:      big.NewInt(5000),
		TxData:       []byte("mock_tx_data"),
	}

	err := validator.PostCheck(ctx, req, badResult)
	if err == nil {
		t.Fatal("PostCheck 应该因输出不足而失败")
	}

	if !dexwallet.IsNonDegradable(err) {
		t.Fatalf("预期 NonDegradableError，实际得到: %T: %v", err, err)
	}
}
