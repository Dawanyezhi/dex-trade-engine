// Package main 实现 DEX 协议数学模型的演示。
// [dexwallet 通用层] -- 纯数学计算，不依赖链上数据。
package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ConstantProductAMM 恒定乘积自动做市商（x * y = k）。
// 对应 Raydium AMM（Solana）和 Uniswap V2（EVM）。
//
// 核心公式：
//
//	amountOut = y * amountIn * (10000 - feeRate) / (x * 10000 + amountIn * (10000 - feeRate))
//
// 其中 x, y 为池子两种代币的储量，feeRate 以基点表示。
type ConstantProductAMM struct {
	dexID dexwallet.DexID
}

// NewConstantProductAMM 创建一个 AMM 协议实例。
func NewConstantProductAMM(dexID dexwallet.DexID) *ConstantProductAMM {
	return &ConstantProductAMM{dexID: dexID}
}

// DexID 返回 DEX 标识。
func (a *ConstantProductAMM) DexID() dexwallet.DexID {
	return a.dexID
}

// ProtocolType 返回协议类型。
func (a *ConstantProductAMM) ProtocolType() dexwallet.ProtocolType {
	return dexwallet.ProtocolAMM
}

// GetPrice 获取当前价格（1 个 base 代币值多少 quote 代币）。
// price = reserveQuote / reserveBase
// 为保留精度，返回值按 PRICE_PRECISION (10^18) 缩放。
func (a *ConstantProductAMM) GetPrice(_ context.Context, pool *dexwallet.Pool) (*big.Int, error) {
	reserveBase, reserveQuote, err := extractAMMReserves(pool)
	if err != nil {
		return nil, fmt.Errorf("amm GetPrice: %w", err)
	}

	if reserveBase.Sign() == 0 {
		return nil, fmt.Errorf("amm GetPrice: base reserve is zero")
	}

	// price = reserveQuote * PRICE_PRECISION / reserveBase
	price := new(big.Int).Mul(reserveQuote, PricePrecision)
	price.Div(price, reserveBase)

	return price, nil
}

// Quote 获取报价。
// Sell 方向：卖出 base 代币，得到 quote 代币。amountIn 是 base 数量。
// Buy 方向：用 quote 代币买入 base 代币。amountIn 是 quote 数量。
func (a *ConstantProductAMM) Quote(ctx context.Context, pool *dexwallet.Pool, amountIn *big.Int, direction dexwallet.SwapDirection) (*dexwallet.Quote, error) {
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("amm Quote: amountIn must be positive")
	}

	reserveBase, reserveQuote, err := extractAMMReserves(pool)
	if err != nil {
		return nil, fmt.Errorf("amm Quote: %w", err)
	}

	if reserveBase.Sign() == 0 || reserveQuote.Sign() == 0 {
		return nil, fmt.Errorf("amm Quote: pool has zero reserves")
	}

	var reserveIn, reserveOut *big.Int
	switch direction {
	case dexwallet.SwapDirectionSell:
		// 卖出 base，得到 quote
		reserveIn = reserveBase
		reserveOut = reserveQuote
	case dexwallet.SwapDirectionBuy:
		// 输入 quote，得到 base
		reserveIn = reserveQuote
		reserveOut = reserveBase
	default:
		return nil, fmt.Errorf("amm Quote: unknown direction: %s", direction)
	}

	// 计算输出金额
	amountOut := calcAMMOutput(amountIn, reserveIn, reserveOut, pool.FeeRate)

	if amountOut.Sign() <= 0 {
		return nil, fmt.Errorf("amm Quote: output amount is zero")
	}

	// 检查输出不超过储量
	if amountOut.Cmp(reserveOut) >= 0 {
		return nil, fmt.Errorf("amm Quote: output %s exceeds reserve %s", amountOut.String(), reserveOut.String())
	}

	// 计算价格影响
	priceImpact := calcPriceImpact(amountIn, amountOut, reserveIn, reserveOut)

	return &dexwallet.Quote{
		DexID:        a.dexID,
		ProtocolType: dexwallet.ProtocolAMM,
		Priority:     dexwallet.PriorityMedium,
		InputAmount:  new(big.Int).Set(amountIn),
		OutputAmount: amountOut,
		PriceImpact:  priceImpact,
		Pool:         pool,
		EstimatedGas: big.NewInt(200000), // 固定估算值
	}, nil
}

// calcAMMOutput 计算 AMM 的输出金额。
// amountOut = reserveOut * amountIn * (10000 - feeRate) / (reserveIn * 10000 + amountIn * (10000 - feeRate))
func calcAMMOutput(amountIn, reserveIn, reserveOut *big.Int, feeRate uint64) *big.Int {
	feeFactor := big.NewInt(int64(10000 - feeRate))
	bigBase := big.NewInt(10000)

	// effectiveIn = amountIn * (10000 - feeRate)
	effectiveIn := new(big.Int).Mul(amountIn, feeFactor)

	// numerator = reserveOut * effectiveIn
	numerator := new(big.Int).Mul(reserveOut, effectiveIn)

	// denominator = reserveIn * 10000 + effectiveIn
	denominator := new(big.Int).Mul(reserveIn, bigBase)
	denominator.Add(denominator, effectiveIn)

	// amountOut = numerator / denominator
	return new(big.Int).Div(numerator, denominator)
}

// calcPriceImpact 计算价格影响（基点）。
// 对比无滑点价格（reserveOut / reserveIn）和实际成交价格（amountOut / amountIn）。
func calcPriceImpact(amountIn, amountOut, reserveIn, reserveOut *big.Int) uint64 {
	// 理想输出 = amountIn * reserveOut / reserveIn （无滑点，不含手续费）
	idealOutput := new(big.Int).Mul(amountIn, reserveOut)
	idealOutput.Div(idealOutput, reserveIn)

	if idealOutput.Sign() <= 0 {
		return 0
	}

	// 如果实际输出大于等于理想输出，价格影响为 0
	if amountOut.Cmp(idealOutput) >= 0 {
		return 0
	}

	// priceImpact = (idealOutput - amountOut) * 10000 / idealOutput
	diff := new(big.Int).Sub(idealOutput, amountOut)
	diff.Mul(diff, big.NewInt(10000))
	diff.Div(diff, idealOutput)

	return diff.Uint64()
}

// extractAMMReserves 从 Pool.Extra 中提取 AMM 储量。
func extractAMMReserves(pool *dexwallet.Pool) (reserveBase, reserveQuote *big.Int, err error) {
	if pool == nil {
		return nil, nil, fmt.Errorf("pool is nil")
	}

	if pool.Extra == nil {
		return nil, nil, fmt.Errorf("pool.Extra is nil")
	}

	rb, ok := pool.Extra["ReserveBase"]
	if !ok {
		return nil, nil, fmt.Errorf("pool.Extra missing ReserveBase")
	}
	reserveBase, ok = rb.(*big.Int)
	if !ok {
		return nil, nil, fmt.Errorf("pool.Extra ReserveBase is not *big.Int")
	}

	rq, ok := pool.Extra["ReserveQuote"]
	if !ok {
		return nil, nil, fmt.Errorf("pool.Extra missing ReserveQuote")
	}
	reserveQuote, ok = rq.(*big.Int)
	if !ok {
		return nil, nil, fmt.Errorf("pool.Extra ReserveQuote is not *big.Int")
	}

	return reserveBase, reserveQuote, nil
}

// PricePrecision 价格精度缩放因子（10^18）。
// 用于在纯整数运算中表示价格，避免浮点数。
var PricePrecision = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
