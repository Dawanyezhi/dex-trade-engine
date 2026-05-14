package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// UniswapV2Builder -- Uniswap V2 (恒定乘积 x*y=k) 构建器
// ============================================================

// UniswapV2Builder 模拟 Uniswap V2 的 Swap 交易构建。
// [链特定层 - EVM]
//
// 交易流程：
// 1. 检查 Approve 状态（allowance 是否足够）
// 2. 如果不足，先发 Approve 交易
// 3. 计算输出金额（x*y=k）
// 4. ABI 编码 swapExactTokensForTokens calldata
// 5. 估算 Gas（BaseFee + PriorityFee）
type UniswapV2Builder struct {
	// 模拟的池子储备量
	reserveIn  *big.Int
	reserveOut *big.Int
	feeRate    uint64 // 手续费率（基点）

	// 模拟的 Gas 参数
	baseFee     *big.Int // 当前 baseFee (wei)
	priorityFee *big.Int // 推荐 priorityFee (wei)
	gasLimit    uint64   // 估算的 gasLimit

	// 模拟的 Router 合约地址
	routerAddress string
}

// NewUniswapV2Builder 创建 Uniswap V2 构建器。
func NewUniswapV2Builder() *UniswapV2Builder {
	return &UniswapV2Builder{
		reserveIn:     new(big.Int).Mul(big.NewInt(500), big.NewInt(1e18)),    // 500 ETH
		reserveOut:    new(big.Int).Mul(big.NewInt(1000000), big.NewInt(1e18)), // 1000000 TOKEN
		feeRate:       30,                                                      // 0.3%
		baseFee:       big.NewInt(30e9),                                        // 30 Gwei
		priorityFee:   big.NewInt(2e9),                                         // 2 Gwei
		gasLimit:      200000,
		routerAddress: "0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D",
	}
}

func (b *UniswapV2Builder) DexID() dexwallet.DexID         { return dexwallet.DexUniswapV2 }
func (b *UniswapV2Builder) ChainID() coinset.ChainID       { return coinset.ChainEthereum }
func (b *UniswapV2Builder) ProtocolType() dexwallet.ProtocolType { return dexwallet.ProtocolAMM }
func (b *UniswapV2Builder) Label() string                        { return "uniswap_v2_ethereum" }
func (b *UniswapV2Builder) Simulate(_ context.Context, _ []byte) error { return nil }

func (b *UniswapV2Builder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	// 步骤 1: 验证请求参数
	if err := validateRequest(req); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	// 步骤 2: 模拟 Approve 检查
	needApprove := simulateApproveCheck(req.FromToken.Address, b.routerAddress, req.Amount)
	if needApprove {
		slog.Info("uniswap v2: approve needed",
			"token", req.FromToken.Symbol,
			"router", b.routerAddress,
			"amount", req.Amount.String(),
		)
	}

	// 步骤 3: 计算输出金额（恒定乘积公式，和 Raydium 一样）
	outputAmount := calcAMMOutput(req.Amount, b.reserveIn, b.reserveOut, b.feeRate)
	if outputAmount.Sign() <= 0 {
		return nil, fmt.Errorf("calculated output amount is zero or negative")
	}

	// 步骤 4: 滑点检查
	minOutput := calcMinOutput(outputAmount, req.SlippageBps)
	if err := checkSlippage(outputAmount, minOutput); err != nil {
		return nil, fmt.Errorf("slippage check: %w", err)
	}

	// 步骤 5: 模拟 ABI 编码
	txData := encodeEVMCalldata(req, outputAmount, minOutput, "swapExactTokensForTokens")

	// 步骤 6: 计算 Gas 费用
	gasCost := calcEVMGas(b.baseFee, b.priorityFee, b.gasLimit)

	slog.Info("uniswap v2 swap built",
		"input", req.Amount.String(),
		"output", outputAmount.String(),
		"min_output", minOutput.String(),
		"gas_cost_wei", gasCost.String(),
		"need_approve", needApprove,
	)

	return &dexwallet.SwapResult{
		DexID:        dexwallet.DexUniswapV2,
		ChainID:      coinset.ChainEthereum,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: outputAmount,
		MinOutput:    minOutput,
		SlippageBps:  req.SlippageBps,
		PriorityFee:  new(big.Int).Set(b.priorityFee),
		GasCost:      gasCost,
		TxData:       txData,
		Extra: map[string]interface{}{
			"router":       b.routerAddress,
			"need_approve": needApprove,
			"gas_limit":    b.gasLimit,
			"base_fee":     b.baseFee.String(),
			"priority_fee": b.priorityFee.String(),
			"eip1559":      true,
		},
	}, nil
}

// ============================================================
// PancakeV3Builder -- PancakeSwap V3 (CLMM) 构建器
// ============================================================

// PancakeV3Builder 模拟 PancakeSwap V3 的 CLMM 交易构建。
// [链特定层 - EVM (BSC)]
//
// PancakeSwap V3 使用集中流动性（Concentrated Liquidity Market Maker）：
// - 流动性集中在指定的 tick 区间
// - 在活跃区间内资本效率远高于 V2
// - 交易可能跨越多个 tick 区间
type PancakeV3Builder struct {
	// 模拟的 CLMM 参数
	liquidity *big.Int // 当前 tick 区间的流动性
	sqrtPrice *big.Int // 当前 sqrtPriceX96
	tickLower int64    // 活跃区间下界
	tickUpper int64    // 活跃区间上界
	feeRate   uint64   // 手续费率（基点）

	// Gas 参数（BSC 不支持 EIP-1559）
	gasPrice *big.Int
	gasLimit uint64

	routerAddress string
}

// NewPancakeV3Builder 创建 PancakeSwap V3 构建器。
func NewPancakeV3Builder() *PancakeV3Builder {
	return &PancakeV3Builder{
		liquidity:     new(big.Int).Mul(big.NewInt(1000000), big.NewInt(1e18)),
		sqrtPrice:     new(big.Int).Mul(big.NewInt(79228162514264337), big.NewInt(1e3)), // 约 1:1
		tickLower:     -887220,
		tickUpper:     887220,
		feeRate:       25, // 0.25%
		gasPrice:      big.NewInt(3e9),  // 3 Gwei (BSC)
		gasLimit:      250000,
		routerAddress: "0x13f4EA83D0bd40E75C8222255bc855a974568Dd4",
	}
}

func (b *PancakeV3Builder) DexID() dexwallet.DexID         { return dexwallet.DexPancakeV3 }
func (b *PancakeV3Builder) ChainID() coinset.ChainID       { return coinset.ChainBSC }
func (b *PancakeV3Builder) ProtocolType() dexwallet.ProtocolType { return dexwallet.ProtocolCLMM }
func (b *PancakeV3Builder) Label() string                        { return "pancake_v3_bsc" }
func (b *PancakeV3Builder) Simulate(_ context.Context, _ []byte) error { return nil }

func (b *PancakeV3Builder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	// 步骤 1: 验证请求参数
	if err := validateRequest(req); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	// 步骤 2: 模拟 Approve 检查
	needApprove := simulateApproveCheck(req.FromToken.Address, b.routerAddress, req.Amount)

	// 步骤 3: 计算输出金额（CLMM 简化模型）
	// 在活跃 tick 区间内，等效于恒定乘积，但资本效率更高
	// demo 简化：使用 AMM 公式 + 资本效率倍数
	outputAmount := calcCLMMOutput(req.Amount, b.liquidity, b.feeRate)
	if outputAmount.Sign() <= 0 {
		return nil, fmt.Errorf("calculated output amount is zero or negative")
	}

	// 步骤 4: 滑点检查
	minOutput := calcMinOutput(outputAmount, req.SlippageBps)
	if err := checkSlippage(outputAmount, minOutput); err != nil {
		return nil, fmt.Errorf("slippage check: %w", err)
	}

	// 步骤 5: ABI 编码
	txData := encodeEVMCalldata(req, outputAmount, minOutput, "exactInputSingle")

	// 步骤 6: Gas 计算（BSC 使用 Legacy Gas 模型）
	gasCost := new(big.Int).Mul(b.gasPrice, big.NewInt(int64(b.gasLimit)))

	slog.Info("pancake v3 clmm swap built",
		"input", req.Amount.String(),
		"output", outputAmount.String(),
		"gas_cost_wei", gasCost.String(),
		"tick_range", fmt.Sprintf("[%d, %d]", b.tickLower, b.tickUpper),
		"need_approve", needApprove,
	)

	return &dexwallet.SwapResult{
		DexID:        dexwallet.DexPancakeV3,
		ChainID:      coinset.ChainBSC,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: outputAmount,
		MinOutput:    minOutput,
		SlippageBps:  req.SlippageBps,
		PriorityFee:  big.NewInt(0), // BSC 没有 priorityFee
		GasCost:      gasCost,
		TxData:       txData,
		Extra: map[string]interface{}{
			"router":       b.routerAddress,
			"need_approve": needApprove,
			"gas_limit":    b.gasLimit,
			"gas_price":    b.gasPrice.String(),
			"eip1559":      false, // BSC 不支持 EIP-1559
			"tick_lower":   b.tickLower,
			"tick_upper":   b.tickUpper,
			"protocol":     "clmm",
		},
	}, nil
}

// ============================================================
// CurveBuilder -- Curve StableSwap 构建器
// ============================================================

// CurveBuilder 模拟 Curve 的稳定币交换构建。
// [链特定层 - EVM (Ethereum)]
//
// Curve 的核心特点：
// - 专为稳定币设计的低滑点曲线
// - 在 1:1 附近几乎零滑点
// - 放大系数 A 控制曲线平坦度
type CurveBuilder struct {
	// 模拟的 StableSwap 参数
	amplification uint64   // 放大系数 A
	reserves      []*big.Int // 各代币的储备量
	feeRate       uint64     // 手续费率（基点）

	// Gas 参数（Ethereum EIP-1559）
	baseFee     *big.Int
	priorityFee *big.Int
	gasLimit    uint64

	poolAddress string
}

// NewCurveBuilder 创建 Curve 构建器。
func NewCurveBuilder() *CurveBuilder {
	return &CurveBuilder{
		amplification: 200, // 高放大系数，稳定币池典型值
		reserves: []*big.Int{
			new(big.Int).Mul(big.NewInt(10000000), big.NewInt(1e6)),  // 1000万 USDC (6 decimals)
			new(big.Int).Mul(big.NewInt(10000000), big.NewInt(1e18)), // 1000万 DAI (18 decimals)
		},
		feeRate:     4,   // 0.04% (Curve 手续费很低)
		baseFee:     big.NewInt(25e9), // 25 Gwei
		priorityFee: big.NewInt(1e9),  // 1 Gwei
		gasLimit:    300000,
		poolAddress: "0xbEbc44782C7dB0a1A60Cb6fe97d0b483032FF1C7",
	}
}

func (b *CurveBuilder) DexID() dexwallet.DexID         { return dexwallet.DexCurve }
func (b *CurveBuilder) ChainID() coinset.ChainID       { return coinset.ChainEthereum }
func (b *CurveBuilder) ProtocolType() dexwallet.ProtocolType { return dexwallet.ProtocolStableSwap }
func (b *CurveBuilder) Label() string                        { return "curve_ethereum" }
func (b *CurveBuilder) Simulate(_ context.Context, _ []byte) error { return nil }

func (b *CurveBuilder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	// 步骤 1: 验证请求参数
	if err := validateRequest(req); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	// 步骤 2: 模拟 Approve 检查
	needApprove := simulateApproveCheck(req.FromToken.Address, b.poolAddress, req.Amount)

	// 步骤 3: 计算输出金额（StableSwap 简化模型）
	outputAmount := calcStableSwapOutput(req.Amount, req.FromToken.Decimals, req.ToToken.Decimals, b.feeRate, b.amplification)
	if outputAmount.Sign() <= 0 {
		return nil, fmt.Errorf("calculated output amount is zero or negative")
	}

	// 步骤 4: 滑点检查
	minOutput := calcMinOutput(outputAmount, req.SlippageBps)
	if err := checkSlippage(outputAmount, minOutput); err != nil {
		return nil, fmt.Errorf("slippage check: %w", err)
	}

	// 步骤 5: ABI 编码
	txData := encodeEVMCalldata(req, outputAmount, minOutput, "exchange")

	// 步骤 6: Gas 计算（EIP-1559）
	gasCost := calcEVMGas(b.baseFee, b.priorityFee, b.gasLimit)

	slog.Info("curve stableswap built",
		"input", req.Amount.String(),
		"output", outputAmount.String(),
		"amplification", b.amplification,
		"fee_rate_bps", b.feeRate,
		"gas_cost_wei", gasCost.String(),
	)

	return &dexwallet.SwapResult{
		DexID:        dexwallet.DexCurve,
		ChainID:      coinset.ChainEthereum,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: outputAmount,
		MinOutput:    minOutput,
		SlippageBps:  req.SlippageBps,
		PriorityFee:  new(big.Int).Set(b.priorityFee),
		GasCost:      gasCost,
		TxData:       txData,
		Extra: map[string]interface{}{
			"pool":          b.poolAddress,
			"need_approve":  needApprove,
			"amplification": b.amplification,
			"gas_limit":     b.gasLimit,
			"base_fee":      b.baseFee.String(),
			"priority_fee":  b.priorityFee.String(),
			"eip1559":       true,
			"protocol":      "stable_swap",
		},
	}, nil
}

// ============================================================
// EVM 辅助函数
// ============================================================

// calcCLMMOutput 用简化的 CLMM 模型计算输出金额。
// 在活跃 tick 区间内，资本效率是传统 AMM 的数倍。
// demo 简化模型：output = input * (10000 - feeRate) / 10000 * efficiencyFactor
func calcCLMMOutput(amountIn, liquidity *big.Int, feeRate uint64) *big.Int {
	// 扣除手续费
	feeMultiplier := big.NewInt(int64(10000 - feeRate))
	effectiveInput := new(big.Int).Mul(amountIn, feeMultiplier)
	effectiveInput.Div(effectiveInput, big.NewInt(10000))

	// CLMM 的简化计算：使用流动性深度估算输出
	// 在实际中，需要根据当前 tick、流动性分布、以及价格移动来精确计算
	// demo 简化为近似 1:2000 的比率（模拟 BNB -> TOKEN）
	ratio := big.NewInt(2000)
	output := new(big.Int).Mul(effectiveInput, ratio)

	return output
}

// calcStableSwapOutput 用简化的 StableSwap 模型计算输出金额。
// 对于稳定币交换，在 1:1 附近几乎零滑点。
func calcStableSwapOutput(amountIn *big.Int, fromDecimals, toDecimals uint8, feeRate uint64, amplification uint64) *big.Int {
	// 首先做精度转换（不同稳定币精度不同）
	// 例如 USDC (6 decimals) -> DAI (18 decimals)
	var normalized *big.Int

	if fromDecimals < toDecimals {
		diff := toDecimals - fromDecimals
		multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(diff)), nil)
		normalized = new(big.Int).Mul(amountIn, multiplier)
	} else if fromDecimals > toDecimals {
		diff := fromDecimals - toDecimals
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(diff)), nil)
		normalized = new(big.Int).Div(amountIn, divisor)
	} else {
		normalized = new(big.Int).Set(amountIn)
	}

	// StableSwap 在 1:1 附近几乎零滑点
	// 简化模型：output = normalized * (10000 - feeRate) / 10000
	// 高放大系数使得滑点接近于零
	feeMultiplier := big.NewInt(int64(10000 - feeRate))
	output := new(big.Int).Mul(normalized, feeMultiplier)
	output.Div(output, big.NewInt(10000))

	return output
}

// simulateApproveCheck 模拟检查 ERC20 Approve 状态。
// 在生产中，这会调用 ERC20.allowance(owner, spender) 查询链上状态。
func simulateApproveCheck(tokenAddress, spenderAddress string, amount *big.Int) bool {
	// demo 简化：假设都需要 approve
	// 生产中查询 allowance 后与 amount 比较
	return true
}

// encodeEVMCalldata 模拟 ABI 编码。
// 生产中使用 go-ethereum 的 abi.Pack 方法。
func encodeEVMCalldata(req dexwallet.SwapRequest, outputAmount, minOutput *big.Int, funcName string) []byte {
	// 模拟 ABI 编码结构：
	// [4 bytes 函数选择器] [32 bytes amountIn] [32 bytes amountOutMin] [32 bytes path...] [32 bytes to] [32 bytes deadline]
	//
	// 实际生产中：
	// funcSelector := crypto.Keccak256([]byte("swapExactTokensForTokens(uint256,uint256,address[],address,uint256)"))[:4]
	// encodedArgs := abi.Arguments{...}.Pack(amountIn, amountOutMin, path, to, deadline)

	data := fmt.Sprintf(
		"evm_tx{func=%s,from_token=%s,to_token=%s,amount_in=%s,amount_out_min=%s,recipient=%s}",
		funcName,
		req.FromToken.Address,
		req.ToToken.Address,
		req.Amount.String(),
		minOutput.String(),
		req.Recipient,
	)
	return []byte(data)
}

// calcEVMGas 计算 EVM Gas 费用。
// EIP-1559: gasCost = gasLimit * (baseFee + priorityFee)
func calcEVMGas(baseFee, priorityFee *big.Int, gasLimit uint64) *big.Int {
	effectiveGasPrice := new(big.Int).Add(baseFee, priorityFee)
	return new(big.Int).Mul(effectiveGasPrice, big.NewInt(int64(gasLimit)))
}
