package main

import (
	"context"
	"math/big"
	"testing"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// 辅助函数
// ============================================================

// newSolanaSwapRequest 创建一个标准的 Solana Swap 请求。
func newSolanaSwapRequest(dexID dexwallet.DexID, amount *big.Int, slippageBps uint64) dexwallet.SwapRequest {
	return dexwallet.SwapRequest{
		ChainID:   coinset.ChainSolana,
		DexID:     dexID,
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

// newEVMSwapRequest 创建一个标准的 EVM Swap 请求。
func newEVMSwapRequest(chainID coinset.ChainID, dexID dexwallet.DexID, amount *big.Int, slippageBps uint64) dexwallet.SwapRequest {
	return dexwallet.SwapRequest{
		ChainID:   chainID,
		DexID:     dexID,
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{
			Address:  "0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c",
			Symbol:   "BNB",
			Decimals: 18,
			ChainID:  chainID,
		},
		ToToken: dexwallet.Token{
			Address:  "0x1234567890abcdef1234567890abcdef12345678",
			Symbol:   "TOKEN",
			Decimals: 18,
			ChainID:  chainID,
		},
		Amount:      amount,
		SlippageBps: slippageBps,
		Sender:      "0xSenderAddress1234567890abcdef12345678",
		Recipient:   "0xSenderAddress1234567890abcdef12345678",
	}
}

// ============================================================
// 工厂注册和查找测试
// ============================================================

func TestFactoryRegisterAndGet(t *testing.T) {
	factory := NewSwapBuilderFactory()

	raydium := NewRaydiumAMMBuilder()
	factory.Register(raydium)

	// 查找已注册的 builder
	builder, err := factory.Get(dexwallet.DexRaydiumAMM)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if builder.DexID() != dexwallet.DexRaydiumAMM {
		t.Fatalf("expected DexID %s, got %s", dexwallet.DexRaydiumAMM, builder.DexID())
	}
	if builder.ChainID() != coinset.ChainSolana {
		t.Fatalf("expected ChainID %s, got %s", coinset.ChainSolana, builder.ChainID())
	}
	if builder.ProtocolType() != dexwallet.ProtocolAMM {
		t.Fatalf("expected ProtocolType %s, got %s", dexwallet.ProtocolAMM, builder.ProtocolType())
	}
}

func TestFactoryGetNotFound(t *testing.T) {
	factory := NewSwapBuilderFactory()

	_, err := factory.Get("nonexistent_dex")
	if err == nil {
		t.Fatal("expected error for nonexistent DEX, got nil")
	}
}

func TestFactoryListBuilders(t *testing.T) {
	factory := NewSwapBuilderFactory()

	factory.Register(NewRaydiumAMMBuilder())
	factory.Register(NewPumpFunBuilder())
	factory.Register(NewUniswapV2Builder())

	builders := factory.ListBuilders()
	if len(builders) != 3 {
		t.Fatalf("expected 3 builders, got %d", len(builders))
	}
}

func TestFactoryBuildChainMismatch(t *testing.T) {
	factory := NewSwapBuilderFactory()
	factory.Register(NewRaydiumAMMBuilder())

	// 用 BSC 链 ID 请求 Raydium（Solana DEX）
	req := dexwallet.SwapRequest{
		ChainID:   coinset.ChainBSC,
		DexID:     dexwallet.DexRaydiumAMM,
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{Symbol: "BNB", Decimals: 18, ChainID: coinset.ChainBSC},
		ToToken:   dexwallet.Token{Symbol: "TOKEN", Decimals: 18, ChainID: coinset.ChainBSC},
		Amount:    big.NewInt(1e18),
		Sender:    "0xSender",
		Recipient: "0xSender",
	}

	_, err := factory.Build(context.Background(), req)
	if err == nil {
		t.Fatal("expected chain mismatch error, got nil")
	}
}

func TestFactoryBuildNoDexID(t *testing.T) {
	factory := NewSwapBuilderFactory()
	factory.Register(NewRaydiumAMMBuilder())

	req := dexwallet.SwapRequest{
		ChainID:   coinset.ChainSolana,
		DexID:     "", // 未指定 DexID
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{Symbol: "SOL", Decimals: 9},
		ToToken:   dexwallet.Token{Symbol: "MEME", Decimals: 6},
		Amount:    big.NewInt(1e9),
		Sender:    "SenderPubkey",
	}

	_, err := factory.Build(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing DexID, got nil")
	}
}

// ============================================================
// Solana Builder 测试
// ============================================================

func TestRaydiumAMMBuildNormal(t *testing.T) {
	builder := NewRaydiumAMMBuilder()
	ctx := context.Background()
	amount := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9)) // 1 SOL

	req := newSolanaSwapRequest(dexwallet.DexRaydiumAMM, amount, 200)
	result, err := builder.Build(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// 验证基本字段
	if result.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("expected DexID %s, got %s", dexwallet.DexRaydiumAMM, result.DexID)
	}
	if result.ChainID != coinset.ChainSolana {
		t.Errorf("expected ChainID %s, got %s", coinset.ChainSolana, result.ChainID)
	}

	// 输出金额应该大于 0
	if result.OutputAmount.Sign() <= 0 {
		t.Error("expected positive output amount")
	}

	// 输入金额应该和请求一致
	if result.InputAmount.Cmp(amount) != 0 {
		t.Errorf("expected input amount %s, got %s", amount.String(), result.InputAmount.String())
	}

	// MinOutput 应该小于等于 OutputAmount
	if result.MinOutput.Cmp(result.OutputAmount) > 0 {
		t.Error("MinOutput should be <= OutputAmount")
	}

	// 交易数据不应为空
	if len(result.TxData) == 0 {
		t.Error("expected non-empty TxData")
	}
}

func TestRaydiumAMMBuildZeroAmount(t *testing.T) {
	builder := NewRaydiumAMMBuilder()
	ctx := context.Background()

	req := newSolanaSwapRequest(dexwallet.DexRaydiumAMM, big.NewInt(0), 200)
	_, err := builder.Build(ctx, req)
	if err == nil {
		t.Fatal("expected error for zero amount, got nil")
	}
}

func TestPumpFunBuildNormal(t *testing.T) {
	builder := NewPumpFunBuilder()
	ctx := context.Background()
	amount := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9)) // 1 SOL

	req := newSolanaSwapRequest(dexwallet.DexPumpFun, amount, 300)
	result, err := builder.Build(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if result.DexID != dexwallet.DexPumpFun {
		t.Errorf("expected DexID %s, got %s", dexwallet.DexPumpFun, result.DexID)
	}

	if result.OutputAmount.Sign() <= 0 {
		t.Error("expected positive output amount")
	}

	// 检查 Extra 中的 Bonding Curve 特有字段
	if result.Extra == nil {
		t.Fatal("expected non-nil Extra")
	}
	if _, ok := result.Extra["graduated"]; !ok {
		t.Error("expected 'graduated' field in Extra")
	}
	if _, ok := result.Extra["will_graduate"]; !ok {
		t.Error("expected 'will_graduate' field in Extra")
	}
}

func TestPumpFunBuildGraduated(t *testing.T) {
	builder := NewPumpFunBuilder()
	builder.graduated = true // 标记已毕业

	ctx := context.Background()
	amount := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9))

	req := newSolanaSwapRequest(dexwallet.DexPumpFun, amount, 300)
	_, err := builder.Build(ctx, req)
	if err == nil {
		t.Fatal("expected error for graduated token, got nil")
	}
}

func TestJupiterBuildNormal(t *testing.T) {
	raydium := NewRaydiumAMMBuilder()
	pumpFun := NewPumpFunBuilder()
	jupiter := NewJupiterBuilder(raydium, pumpFun)

	ctx := context.Background()
	amount := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9))

	req := newSolanaSwapRequest(dexwallet.DexJupiter, amount, 200)
	result, err := jupiter.Build(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if result.DexID != dexwallet.DexJupiter {
		t.Errorf("expected DexID %s, got %s", dexwallet.DexJupiter, result.DexID)
	}

	if result.OutputAmount.Sign() <= 0 {
		t.Error("expected positive output amount")
	}

	// Jupiter 作为聚合器会扣 0.1% 的费用，输出应略低于最优源 DEX 的直接输出
	// Jupiter 内部会选 PumpFun 或 Raydium 中输出更高的，然后扣手续费
	pumpResult, _ := pumpFun.Build(ctx, newSolanaSwapRequest(dexwallet.DexPumpFun, amount, 200))
	raydiumResult, _ := raydium.Build(ctx, newSolanaSwapRequest(dexwallet.DexRaydiumAMM, amount, 200))
	bestDirect := raydiumResult.OutputAmount
	if pumpResult.OutputAmount.Cmp(bestDirect) > 0 {
		bestDirect = pumpResult.OutputAmount
	}
	if result.OutputAmount.Cmp(bestDirect) >= 0 {
		t.Error("jupiter output should be slightly less than best direct DEX due to aggregator fee")
	}
}

// ============================================================
// EVM Builder 测试
// ============================================================

func TestUniswapV2BuildNormal(t *testing.T) {
	builder := NewUniswapV2Builder()
	ctx := context.Background()
	amount := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e18)) // 1 ETH

	req := newEVMSwapRequest(coinset.ChainEthereum, dexwallet.DexUniswapV2, amount, 100)
	result, err := builder.Build(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if result.DexID != dexwallet.DexUniswapV2 {
		t.Errorf("expected DexID %s, got %s", dexwallet.DexUniswapV2, result.DexID)
	}
	if result.ChainID != coinset.ChainEthereum {
		t.Errorf("expected ChainID %s, got %s", coinset.ChainEthereum, result.ChainID)
	}

	if result.OutputAmount.Sign() <= 0 {
		t.Error("expected positive output amount")
	}

	// Gas 费用应该大于 0
	if result.GasCost == nil || result.GasCost.Sign() <= 0 {
		t.Error("expected positive gas cost")
	}

	// EVM 交易应该包含 need_approve 标记
	if result.Extra == nil {
		t.Fatal("expected non-nil Extra")
	}
	if _, ok := result.Extra["need_approve"]; !ok {
		t.Error("expected 'need_approve' field in Extra")
	}
}

func TestPancakeV3BuildNormal(t *testing.T) {
	builder := NewPancakeV3Builder()
	ctx := context.Background()
	amount := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e18)) // 1 BNB

	req := newEVMSwapRequest(coinset.ChainBSC, dexwallet.DexPancakeV3, amount, 100)
	result, err := builder.Build(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if result.DexID != dexwallet.DexPancakeV3 {
		t.Errorf("expected DexID %s, got %s", dexwallet.DexPancakeV3, result.DexID)
	}

	// BSC 不支持 EIP-1559，Extra 中 eip1559 应为 false
	if eip1559, ok := result.Extra["eip1559"]; !ok || eip1559.(bool) {
		t.Error("expected eip1559=false for BSC")
	}
}

func TestCurveBuildNormal(t *testing.T) {
	builder := NewCurveBuilder()
	ctx := context.Background()
	amount := new(big.Int).Mul(big.NewInt(10000), big.NewInt(1e6)) // 10000 USDC (6 decimals)

	req := dexwallet.SwapRequest{
		ChainID:   coinset.ChainEthereum,
		DexID:     dexwallet.DexCurve,
		Direction: dexwallet.SwapDirectionSell,
		FromToken: dexwallet.Token{
			Address:  "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
			Symbol:   "USDC",
			Decimals: 6,
			ChainID:  coinset.ChainEthereum,
		},
		ToToken: dexwallet.Token{
			Address:  "0x6B175474E89094C44Da98b954EedeAC495271d0F",
			Symbol:   "DAI",
			Decimals: 18,
			ChainID:  coinset.ChainEthereum,
		},
		Amount:      amount,
		SlippageBps: 10, // 0.1% -- 稳定币交换可以设很低的滑点
		Sender:      "0xSenderAddress",
		Recipient:   "0xSenderAddress",
	}

	result, err := builder.Build(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Curve 稳定币交换，输出应该接近输入值（考虑精度转换后）
	// 输入 10000 USDC (10000 * 10^6 = 10^10)
	// 输出应该接近 10000 DAI (10000 * 10^18 = 10^22)
	if result.OutputAmount.Sign() <= 0 {
		t.Error("expected positive output amount")
	}

	// StableSwap 的协议类型检查
	if result.Extra["protocol"] != "stable_swap" {
		t.Errorf("expected protocol stable_swap, got %v", result.Extra["protocol"])
	}
}

// ============================================================
// 交易模拟测试
// ============================================================

func TestSimulateSwapSuccess(t *testing.T) {
	builder := NewRaydiumAMMBuilder()
	ctx := context.Background()
	amount := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9)) // 1 SOL

	req := newSolanaSwapRequest(dexwallet.DexRaydiumAMM, amount, 200)
	result, err := builder.Build(ctx, req)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	simErr := SimulateSwap(result)
	if simErr != nil {
		t.Fatalf("simulation should succeed, got: %v", simErr)
	}
}

func TestSimulateSwapSlippageTooHigh(t *testing.T) {
	result := &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		Direction:    dexwallet.SwapDirectionBuy,
		InputAmount:  new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9)),
		OutputAmount: big.NewInt(1000000),
		MinOutput:    big.NewInt(400000),
		SlippageBps:  6000, // 60% -- 超过 50% 上限
		GasCost:      big.NewInt(5000),
		TxData:       []byte("mock_tx_data"),
	}

	simErr := SimulateSwap(result)
	if simErr == nil {
		t.Fatal("simulation should fail for high slippage")
	}

	// 验证错误类型
	if sErr, ok := simErr.(*SimulationError); ok {
		if sErr.Code != ErrCodeSlippageTooHigh {
			t.Errorf("expected error code %s, got %s", ErrCodeSlippageTooHigh, sErr.Code)
		}
	} else {
		t.Error("expected SimulationError type")
	}
}

func TestSimulateSwapInsufficientBalance(t *testing.T) {
	result := &dexwallet.SwapResult{
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

	simErr := SimulateSwap(result)
	if simErr == nil {
		t.Fatal("simulation should fail for insufficient balance")
	}

	if sErr, ok := simErr.(*SimulationError); ok {
		if sErr.Code != ErrCodeInsufficientBalance {
			t.Errorf("expected error code %s, got %s", ErrCodeInsufficientBalance, sErr.Code)
		}
	} else {
		t.Error("expected SimulationError type")
	}
}

func TestSimulateSwapEmptyTxData(t *testing.T) {
	result := &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		Direction:    dexwallet.SwapDirectionBuy,
		InputAmount:  big.NewInt(1e9),
		OutputAmount: big.NewInt(1000000),
		MinOutput:    big.NewInt(980000),
		SlippageBps:  200,
		GasCost:      big.NewInt(5000),
		TxData:       []byte{}, // 空的交易数据
	}

	simErr := SimulateSwap(result)
	if simErr == nil {
		t.Fatal("simulation should fail for empty tx data")
	}

	if sErr, ok := simErr.(*SimulationError); ok {
		if sErr.Code != ErrCodeInvalidTxData {
			t.Errorf("expected error code %s, got %s", ErrCodeInvalidTxData, sErr.Code)
		}
	}
}

func TestSimulateSwapZeroOutput(t *testing.T) {
	result := &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		Direction:    dexwallet.SwapDirectionBuy,
		InputAmount:  big.NewInt(1e9),
		OutputAmount: big.NewInt(0), // 零输出
		MinOutput:    big.NewInt(0),
		SlippageBps:  200,
		GasCost:      big.NewInt(5000),
		TxData:       []byte("mock_tx_data"),
	}

	simErr := SimulateSwap(result)
	if simErr == nil {
		t.Fatal("simulation should fail for zero output")
	}

	if sErr, ok := simErr.(*SimulationError); ok {
		if sErr.Code != ErrCodeZeroOutput {
			t.Errorf("expected error code %s, got %s", ErrCodeZeroOutput, sErr.Code)
		}
	}
}

// ============================================================
// AMM 计算测试
// ============================================================

func TestCalcAMMOutput(t *testing.T) {
	// 池子: 100 SOL, 1000000 TOKEN, 手续费 0.3%
	reserveIn := new(big.Int).Mul(big.NewInt(100), big.NewInt(1e9))
	reserveOut := new(big.Int).Mul(big.NewInt(1000000), big.NewInt(1e6))

	// 输入 1 SOL
	amountIn := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9))
	output := calcAMMOutput(amountIn, reserveIn, reserveOut, 30)

	// 理论值: output = 1000000 * 0.997 / (100 + 0.997) * 10^6
	// 约等于 9871 * 10^6（约 9871 TOKEN）
	if output.Sign() <= 0 {
		t.Error("AMM output should be positive")
	}

	// 输出应该小于 10000 TOKEN (因为滑点和手续费)
	maxExpected := new(big.Int).Mul(big.NewInt(10000), big.NewInt(1e6))
	if output.Cmp(maxExpected) >= 0 {
		t.Errorf("AMM output %s should be less than %s due to slippage and fees",
			output.String(), maxExpected.String())
	}
}

func TestCalcAMMOutputZeroInput(t *testing.T) {
	reserveIn := big.NewInt(1000)
	reserveOut := big.NewInt(1000)
	output := calcAMMOutput(big.NewInt(0), reserveIn, reserveOut, 30)
	if output.Sign() != 0 {
		t.Error("zero input should produce zero output")
	}
}

func TestCalcMinOutput(t *testing.T) {
	outputAmount := big.NewInt(10000)

	// 1% 滑点
	minOutput := calcMinOutput(outputAmount, 100)
	expected := big.NewInt(9900)
	if minOutput.Cmp(expected) != 0 {
		t.Errorf("expected min output %s, got %s", expected.String(), minOutput.String())
	}

	// 0 滑点
	minOutput0 := calcMinOutput(outputAmount, 0)
	if minOutput0.Cmp(outputAmount) != 0 {
		t.Errorf("expected min output %s for 0 slippage, got %s",
			outputAmount.String(), minOutput0.String())
	}
}

// ============================================================
// 参数验证测试
// ============================================================

func TestValidateRequestMissingSender(t *testing.T) {
	req := dexwallet.SwapRequest{
		ChainID:   coinset.ChainSolana,
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{Symbol: "SOL"},
		ToToken:   dexwallet.Token{Symbol: "MEME"},
		Amount:    big.NewInt(1e9),
		Sender:    "", // 缺少 sender
	}

	err := validateRequest(req)
	if err == nil {
		t.Fatal("expected error for missing sender")
	}
}

func TestValidateRequestNilAmount(t *testing.T) {
	req := dexwallet.SwapRequest{
		ChainID:   coinset.ChainSolana,
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{Symbol: "SOL"},
		ToToken:   dexwallet.Token{Symbol: "MEME"},
		Amount:    nil, // nil 金额
		Sender:    "SenderPubkey",
	}

	err := validateRequest(req)
	if err == nil {
		t.Fatal("expected error for nil amount")
	}
}

func TestValidateRequestZeroAmount(t *testing.T) {
	req := dexwallet.SwapRequest{
		ChainID:   coinset.ChainSolana,
		Direction: dexwallet.SwapDirectionBuy,
		FromToken: dexwallet.Token{Symbol: "SOL"},
		ToToken:   dexwallet.Token{Symbol: "MEME"},
		Amount:    big.NewInt(0), // 零金额
		Sender:    "SenderPubkey",
	}

	err := validateRequest(req)
	if err == nil {
		t.Fatal("expected error for zero amount")
	}
}
