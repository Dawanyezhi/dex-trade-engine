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
// RaydiumAMMBuilder -- Raydium AMM (恒定乘积 x*y=k) 构建器
// ============================================================

// RaydiumAMMBuilder 模拟 Raydium AMM 的 Swap 交易构建。
// [链特定层 - Solana]
//
// 交易指令序列：
// 1. ComputeBudget.SetComputeUnitLimit
// 2. ComputeBudget.SetComputeUnitPrice
// 3. CreateATA（如果代币账户不存在）
// 4. Raydium.Swap
//
// 构建完成后还会模拟 ALT 压缩，减少交易体积。
type RaydiumAMMBuilder struct {
	// 模拟的池子储备量
	reserveIn  *big.Int // 输入代币储备
	reserveOut *big.Int // 输出代币储备
	feeRate    uint64   // 手续费率（基点）
}

// NewRaydiumAMMBuilder 创建 Raydium AMM 构建器。
func NewRaydiumAMMBuilder() *RaydiumAMMBuilder {
	return &RaydiumAMMBuilder{
		reserveIn:  new(big.Int).Mul(big.NewInt(1000), big.NewInt(1e9)),  // 1000 SOL
		reserveOut: new(big.Int).Mul(big.NewInt(10000000), big.NewInt(1e6)), // 10000000 MEME (6 decimals)
		feeRate:    25, // 0.25%
	}
}

func (b *RaydiumAMMBuilder) DexID() dexwallet.DexID              { return dexwallet.DexRaydiumAMM }
func (b *RaydiumAMMBuilder) ChainID() coinset.ChainID            { return coinset.ChainSolana }
func (b *RaydiumAMMBuilder) ProtocolType() dexwallet.ProtocolType { return dexwallet.ProtocolAMM }
func (b *RaydiumAMMBuilder) Label() string                       { return "raydium_amm_solana" }
func (b *RaydiumAMMBuilder) Simulate(_ context.Context, _ []byte) error { return nil }

func (b *RaydiumAMMBuilder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	// 步骤 1: 验证请求参数
	if err := validateRequest(req); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	// 步骤 2: 计算输出金额（恒定乘积公式）
	outputAmount := calcAMMOutput(req.Amount, b.reserveIn, b.reserveOut, b.feeRate)
	if outputAmount.Sign() <= 0 {
		return nil, fmt.Errorf("calculated output amount is zero or negative")
	}

	// 步骤 3: 滑点检查
	minOutput := calcMinOutput(outputAmount, req.SlippageBps)
	if err := checkSlippage(outputAmount, minOutput); err != nil {
		return nil, fmt.Errorf("slippage check: %w", err)
	}

	// 步骤 4: 模拟 Solana 指令组装
	txData := assembleSolanaInstructions(req, outputAmount, "raydium_amm_swap")

	// 步骤 5: 模拟 ALT 压缩
	compressedSize := simulateALTCompression(len(txData))

	// 计算优先费（模拟 ComputeBudget）
	priorityFee := big.NewInt(5000) // 5000 lamports

	slog.Info("raydium amm swap built",
		"input", req.Amount.String(),
		"output", outputAmount.String(),
		"min_output", minOutput.String(),
		"tx_size_before_alt", len(txData),
		"tx_size_after_alt", compressedSize,
		"priority_fee", priorityFee.String(),
	)

	return &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: outputAmount,
		MinOutput:    minOutput,
		SlippageBps:  req.SlippageBps,
		PriorityFee:  priorityFee,
		GasCost:      big.NewInt(5000), // Solana 固定基础费 5000 lamports
		TxData:       txData,
		Extra: map[string]interface{}{
			"instructions_count":   4,
			"compute_units":        200000,
			"alt_compressed":       true,
			"compressed_tx_size":   compressedSize,
			"uncompressed_tx_size": len(txData),
		},
	}, nil
}

// ============================================================
// PumpFunBuilder -- PumpFun Bonding Curve 构建器
// ============================================================

// PumpFunBuilder 模拟 PumpFun 的 Bonding Curve 交易构建。
// [链特定层 - Solana]
//
// PumpFun 使用联合曲线定价，特点：
// - 价格随购买量递增
// - 有毕业阈值（市值达标后迁移到 AMM）
// - 不同于 AMM 的价格计算逻辑
type PumpFunBuilder struct {
	// 模拟的 Bonding Curve 参数
	virtualSolReserve  *big.Int // 虚拟 SOL 储备
	virtualTokenReserve *big.Int // 虚拟 Token 储备
	currentMarketCap   *big.Int // 当前市值 (lamports)
	graduationCap      *big.Int // 毕业阈值 (lamports)
	feeRate            uint64   // 手续费率（基点）
	graduated          bool     // 是否已毕业
}

// NewPumpFunBuilder 创建 PumpFun 构建器。
func NewPumpFunBuilder() *PumpFunBuilder {
	return &PumpFunBuilder{
		virtualSolReserve:   new(big.Int).Mul(big.NewInt(30), big.NewInt(1e9)),     // 30 SOL
		virtualTokenReserve: new(big.Int).Mul(big.NewInt(1000000000), big.NewInt(1e6)), // 10 亿 token
		currentMarketCap:    new(big.Int).Mul(big.NewInt(50000), big.NewInt(1e9)),  // 50000 SOL (~$5M)
		graduationCap:       new(big.Int).Mul(big.NewInt(85000), big.NewInt(1e9)),  // 85000 SOL (~$8.5M)
		feeRate:             100, // 1%
		graduated:           false,
	}
}

func (b *PumpFunBuilder) DexID() dexwallet.DexID              { return dexwallet.DexPumpFun }
func (b *PumpFunBuilder) ChainID() coinset.ChainID            { return coinset.ChainSolana }
func (b *PumpFunBuilder) ProtocolType() dexwallet.ProtocolType { return dexwallet.ProtocolBondingCurve }
func (b *PumpFunBuilder) Label() string                        { return "pumpfun_solana" }
func (b *PumpFunBuilder) Simulate(_ context.Context, _ []byte) error { return nil }

func (b *PumpFunBuilder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	// 步骤 1: 验证请求参数
	if err := validateRequest(req); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	// 步骤 2: 毕业检查
	if b.graduated {
		return nil, fmt.Errorf("token has graduated from bonding curve, use Raydium AMM instead")
	}

	// 步骤 3: 计算输出金额（Bonding Curve）
	// PumpFun 使用虚拟储备曲线，本质上还是 x*y=k，但初始储备是虚拟的
	outputAmount := calcBondingCurveOutput(req.Amount, b.virtualSolReserve, b.virtualTokenReserve, b.feeRate, req.Direction)
	if outputAmount.Sign() <= 0 {
		return nil, fmt.Errorf("calculated output amount is zero or negative")
	}

	// 步骤 4: 检查交易后是否触发毕业
	newMarketCap := calcNewMarketCap(b.currentMarketCap, req.Amount, req.Direction)
	willGraduate := newMarketCap.Cmp(b.graduationCap) >= 0

	// 步骤 5: 滑点检查
	minOutput := calcMinOutput(outputAmount, req.SlippageBps)
	if err := checkSlippage(outputAmount, minOutput); err != nil {
		return nil, fmt.Errorf("slippage check: %w", err)
	}

	// 步骤 6: 组装交易指令
	txData := assembleSolanaInstructions(req, outputAmount, "pump_fun_swap")

	priorityFee := big.NewInt(10000) // PumpFun 交易通常需要更高优先费

	slog.Info("pumpfun bonding curve swap built",
		"input", req.Amount.String(),
		"output", outputAmount.String(),
		"direction", req.Direction,
		"current_market_cap", b.currentMarketCap.String(),
		"will_graduate", willGraduate,
	)

	return &dexwallet.SwapResult{
		DexID:        dexwallet.DexPumpFun,
		ChainID:      coinset.ChainSolana,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: outputAmount,
		MinOutput:    minOutput,
		SlippageBps:  req.SlippageBps,
		PriorityFee:  priorityFee,
		GasCost:      big.NewInt(5000),
		TxData:       txData,
		Extra: map[string]interface{}{
			"protocol":        "bonding_curve",
			"graduated":       b.graduated,
			"will_graduate":   willGraduate,
			"market_cap":      b.currentMarketCap.String(),
			"graduation_cap":  b.graduationCap.String(),
			"instructions_count": 3,
			"compute_units":     150000,
		},
	}, nil
}

// ============================================================
// JupiterBuilder -- Jupiter 聚合器构建器
// ============================================================

// JupiterBuilder 模拟 Jupiter 聚合器的 Swap 交易构建。
// [链特定层 - Solana]
//
// Jupiter 是 Solana 上最大的 DEX 聚合器，特点：
// - 从多个 DEX 获取报价，选择最优路由
// - 支持多跳路由（A -> B -> C）
// - 支持路由拆分（一笔大单拆到多个 DEX）
// - 作为其他 DEX 都失败时的降级兜底
type JupiterBuilder struct {
	// 内部包装的 builder（模拟聚合逻辑）
	raydiumBuilder *RaydiumAMMBuilder
	pumpFunBuilder *PumpFunBuilder
}

// NewJupiterBuilder 创建 Jupiter 聚合器构建器。
func NewJupiterBuilder(raydium *RaydiumAMMBuilder, pumpFun *PumpFunBuilder) *JupiterBuilder {
	return &JupiterBuilder{
		raydiumBuilder: raydium,
		pumpFunBuilder: pumpFun,
	}
}

func (b *JupiterBuilder) DexID() dexwallet.DexID         { return dexwallet.DexJupiter }
func (b *JupiterBuilder) ChainID() coinset.ChainID            { return coinset.ChainSolana }
func (b *JupiterBuilder) ProtocolType() dexwallet.ProtocolType { return dexwallet.ProtocolAggregator }
func (b *JupiterBuilder) Label() string                        { return "jupiter_solana" }
func (b *JupiterBuilder) Simulate(_ context.Context, _ []byte) error { return nil }

func (b *JupiterBuilder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	// 步骤 1: 验证请求参数
	if err := validateRequest(req); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	// 步骤 2: 模拟聚合路由 -- 尝试从多个 DEX 获取最优结果
	slog.Info("jupiter aggregator: finding best route",
		"from", req.FromToken.Symbol,
		"to", req.ToToken.Symbol,
		"amount", req.Amount.String(),
	)

	// 先尝试 PumpFun（如果是内盘代币）
	pumpReq := req
	pumpReq.DexID = dexwallet.DexPumpFun
	pumpResult, pumpErr := b.pumpFunBuilder.Build(ctx, pumpReq)

	// 再尝试 Raydium
	raydiumReq := req
	raydiumReq.DexID = dexwallet.DexRaydiumAMM
	raydiumResult, raydiumErr := b.raydiumBuilder.Build(ctx, raydiumReq)

	// 选择最优结果
	var bestResult *dexwallet.SwapResult
	if pumpErr == nil && raydiumErr == nil {
		// 两者都成功，选输出更多的
		if pumpResult.OutputAmount.Cmp(raydiumResult.OutputAmount) >= 0 {
			bestResult = pumpResult
		} else {
			bestResult = raydiumResult
		}
	} else if pumpErr == nil {
		bestResult = pumpResult
	} else if raydiumErr == nil {
		bestResult = raydiumResult
	} else {
		return nil, fmt.Errorf("jupiter: all routes failed, pump: %v, raydium: %v", pumpErr, raydiumErr)
	}

	// 步骤 3: 包装为 Jupiter 结果
	// 聚合器可能会额外收取少量费用（demo 中简化为扣减 0.1% 输出）
	jupiterFee := new(big.Int).Div(bestResult.OutputAmount, big.NewInt(1000)) // 0.1%
	adjustedOutput := new(big.Int).Sub(bestResult.OutputAmount, jupiterFee)
	minOutput := calcMinOutput(adjustedOutput, req.SlippageBps)

	slog.Info("jupiter aggregator: best route selected",
		"source_dex", bestResult.DexID,
		"output_before_fee", bestResult.OutputAmount.String(),
		"output_after_fee", adjustedOutput.String(),
		"jupiter_fee", jupiterFee.String(),
	)

	return &dexwallet.SwapResult{
		DexID:        dexwallet.DexJupiter,
		ChainID:      coinset.ChainSolana,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: adjustedOutput,
		MinOutput:    minOutput,
		SlippageBps:  req.SlippageBps,
		PriorityFee:  bestResult.PriorityFee,
		GasCost:      bestResult.GasCost,
		TxData:       bestResult.TxData,
		Extra: map[string]interface{}{
			"aggregator":    "jupiter",
			"source_dex":    string(bestResult.DexID),
			"jupiter_fee":   jupiterFee.String(),
			"routes_tried":  2,
			"routes_success": countNonNil(pumpErr, raydiumErr),
		},
	}, nil
}

// ============================================================
// Solana 辅助函数
// ============================================================

// calcAMMOutput 用恒定乘积公式计算 AMM 输出金额。
// dy = reserveOut * effectiveInput / (reserveIn + effectiveInput)
// 其中 effectiveInput = amountIn * (10000 - feeRate) / 10000
func calcAMMOutput(amountIn, reserveIn, reserveOut *big.Int, feeRate uint64) *big.Int {
	// effectiveInput = amountIn * (10000 - feeRate) / 10000
	feeMultiplier := big.NewInt(int64(10000 - feeRate))
	effectiveInput := new(big.Int).Mul(amountIn, feeMultiplier)
	effectiveInput.Div(effectiveInput, big.NewInt(10000))

	// numerator = reserveOut * effectiveInput
	numerator := new(big.Int).Mul(reserveOut, effectiveInput)

	// denominator = reserveIn + effectiveInput
	denominator := new(big.Int).Add(reserveIn, effectiveInput)

	// amountOut = numerator / denominator
	amountOut := new(big.Int).Div(numerator, denominator)
	return amountOut
}

// calcBondingCurveOutput 用虚拟储备曲线计算 Bonding Curve 输出金额。
// 本质上还是 x*y=k，但使用虚拟储备，使得价格随购买量上升。
func calcBondingCurveOutput(amountIn, virtualSolReserve, virtualTokenReserve *big.Int, feeRate uint64, direction dexwallet.SwapDirection) *big.Int {
	// 扣除手续费
	feeMultiplier := big.NewInt(int64(10000 - feeRate))
	effectiveInput := new(big.Int).Mul(amountIn, feeMultiplier)
	effectiveInput.Div(effectiveInput, big.NewInt(10000))

	var numerator, denominator *big.Int

	if direction == dexwallet.SwapDirectionBuy {
		// 买入: SOL -> Token
		numerator = new(big.Int).Mul(virtualTokenReserve, effectiveInput)
		denominator = new(big.Int).Add(virtualSolReserve, effectiveInput)
	} else {
		// 卖出: Token -> SOL
		numerator = new(big.Int).Mul(virtualSolReserve, effectiveInput)
		denominator = new(big.Int).Add(virtualTokenReserve, effectiveInput)
	}

	return new(big.Int).Div(numerator, denominator)
}

// calcNewMarketCap 计算交易后的市值变化。
func calcNewMarketCap(currentCap, amount *big.Int, direction dexwallet.SwapDirection) *big.Int {
	newCap := new(big.Int).Set(currentCap)
	if direction == dexwallet.SwapDirectionBuy {
		// 买入增加市值
		newCap.Add(newCap, new(big.Int).Mul(amount, big.NewInt(10)))
	} else {
		// 卖出减少市值
		decrease := new(big.Int).Mul(amount, big.NewInt(10))
		if newCap.Cmp(decrease) > 0 {
			newCap.Sub(newCap, decrease)
		} else {
			newCap.SetInt64(0)
		}
	}
	return newCap
}

// assembleSolanaInstructions 模拟组装 Solana 交易指令。
// 返回模拟的序列化交易数据。
func assembleSolanaInstructions(req dexwallet.SwapRequest, outputAmount *big.Int, programName string) []byte {
	// 模拟指令序列:
	// [ComputeBudget SetCULimit] [ComputeBudget SetCUPrice] [CreateATA] [Swap]
	//
	// 实际生产中这里会：
	// 1. 构造 solana.Instruction 列表
	// 2. 通过 Borsh 序列化指令数据
	// 3. 收集所有账户地址
	// 4. 组装 solana.Transaction
	// 5. Base58/Base64 编码

	data := fmt.Sprintf(
		"solana_tx{program=%s,from=%s,to=%s,amount=%s,output=%s,slippage=%d}",
		programName,
		req.FromToken.Address,
		req.ToToken.Address,
		req.Amount.String(),
		outputAmount.String(),
		req.SlippageBps,
	)
	return []byte(data)
}

// simulateALTCompression 模拟 Address Lookup Table 压缩。
// 返回压缩后的预计大小。
func simulateALTCompression(originalSize int) int {
	// ALT 可以将 10 个 32 字节地址压缩为 1 个 32 字节 ALT 引用 + 10 个 1 字节索引
	// 节省约 87% 的地址空间
	// 假设地址占交易的 60%，ALT 能将这部分缩减到 13%
	addressPortion := originalSize * 60 / 100
	otherPortion := originalSize - addressPortion
	compressedAddresses := addressPortion * 13 / 100
	return otherPortion + compressedAddresses
}

// countNonNil 统计非 nil 错误数量（用于计算成功路由数）。
func countNonNil(errs ...error) int {
	count := len(errs)
	for _, err := range errs {
		if err != nil {
			count--
		}
	}
	return count
}

// ============================================================
// 通用辅助函数（Solana 和 EVM 共享）
// ============================================================

// validateRequest 验证 SwapRequest 的基本字段。
func validateRequest(req dexwallet.SwapRequest) error {
	if req.Amount == nil || req.Amount.Sign() <= 0 {
		return fmt.Errorf("amount must be positive, got %v", req.Amount)
	}
	if req.Sender == "" {
		return fmt.Errorf("sender address is required")
	}
	if req.Direction != dexwallet.SwapDirectionBuy && req.Direction != dexwallet.SwapDirectionSell {
		return fmt.Errorf("invalid direction: %s, must be 'buy' or 'sell'", req.Direction)
	}
	return nil
}

// calcMinOutput 根据滑点容忍计算最小输出金额。
// minOutput = outputAmount * (10000 - slippageBps) / 10000
func calcMinOutput(outputAmount *big.Int, slippageBps uint64) *big.Int {
	if slippageBps == 0 {
		return new(big.Int).Set(outputAmount)
	}
	multiplier := big.NewInt(int64(10000 - slippageBps))
	result := new(big.Int).Mul(outputAmount, multiplier)
	result.Div(result, big.NewInt(10000))
	return result
}

// checkSlippage 检查滑点是否在合理范围内。
func checkSlippage(outputAmount, minOutput *big.Int) error {
	if outputAmount.Sign() <= 0 {
		return fmt.Errorf("output amount must be positive")
	}
	if minOutput.Sign() < 0 {
		return fmt.Errorf("min output must be non-negative")
	}
	return nil
}
