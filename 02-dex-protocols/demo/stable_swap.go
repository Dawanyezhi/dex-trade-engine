package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// StableSwapProtocol 稳定币低滑点协议（Curve 简化模型）。
// 对应 Curve（EVM）、PancakeStable（BSC）。
//
// 核心思想：在恒定和（x + y = D）与恒定积（x * y = (D/2)^2）之间插值。
// A 参数（放大系数）控制插值权重：
//   - A -> 0：退化为恒定积（与 AMM 类似）
//   - A -> inf：退化为恒定和（1:1 恒定汇率）
//
// 简化不变量方程：
//
//	A * D * (x + y) + x * y = A * D^2 + (D/2)^2
type StableSwapProtocol struct {
	dexID dexwallet.DexID
}

// NewStableSwapProtocol 创建一个 StableSwap 协议实例。
func NewStableSwapProtocol(dexID dexwallet.DexID) *StableSwapProtocol {
	return &StableSwapProtocol{dexID: dexID}
}

// DexID 返回 DEX 标识。
func (s *StableSwapProtocol) DexID() dexwallet.DexID {
	return s.dexID
}

// ProtocolType 返回协议类型。
func (s *StableSwapProtocol) ProtocolType() dexwallet.ProtocolType {
	return dexwallet.ProtocolStableSwap
}

// GetPrice 获取当前价格。
// 对于稳定币对，价格接近 1:1。返回按 PricePrecision 缩放的价格。
func (s *StableSwapProtocol) GetPrice(_ context.Context, pool *dexwallet.Pool) (*big.Int, error) {
	data, err := extractStableSwapData(pool)
	if err != nil {
		return nil, fmt.Errorf("stable_swap GetPrice: %w", err)
	}

	if data.reserveBase.Sign() == 0 {
		return nil, fmt.Errorf("stable_swap GetPrice: base reserve is zero")
	}

	// 价格 = reserveQuote / reserveBase （按 PricePrecision 缩放）
	price := new(big.Int).Mul(data.reserveQuote, PricePrecision)
	price.Div(price, data.reserveBase)

	return price, nil
}

// Quote 获取报价。
// 使用牛顿迭代法解 StableSwap 不变量方程，计算输出金额。
func (s *StableSwapProtocol) Quote(_ context.Context, pool *dexwallet.Pool, amountIn *big.Int, direction dexwallet.SwapDirection) (*dexwallet.Quote, error) {
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("stable_swap Quote: amountIn must be positive")
	}

	data, err := extractStableSwapData(pool)
	if err != nil {
		return nil, fmt.Errorf("stable_swap Quote: %w", err)
	}

	if data.reserveBase.Sign() == 0 || data.reserveQuote.Sign() == 0 {
		return nil, fmt.Errorf("stable_swap Quote: pool has zero reserves")
	}

	var reserveIn, reserveOut *big.Int
	switch direction {
	case dexwallet.SwapDirectionSell:
		reserveIn = data.reserveBase
		reserveOut = data.reserveQuote
	case dexwallet.SwapDirectionBuy:
		reserveIn = data.reserveQuote
		reserveOut = data.reserveBase
	default:
		return nil, fmt.Errorf("stable_swap Quote: unknown direction: %s", direction)
	}

	// 扣除手续费
	effectiveIn := new(big.Int).Set(amountIn)
	if pool.FeeRate > 0 {
		feeFactor := big.NewInt(int64(10000 - pool.FeeRate))
		effectiveIn.Mul(effectiveIn, feeFactor)
		effectiveIn.Div(effectiveIn, big.NewInt(10000))
	}

	// 计算新的 reserveIn
	newReserveIn := new(big.Int).Add(reserveIn, effectiveIn)

	// 使用牛顿迭代法求解新的 reserveOut
	newReserveOut, err := solveStableSwap(newReserveIn, data.amplificationCoeff, data.invariantD)
	if err != nil {
		return nil, fmt.Errorf("stable_swap Quote: solve failed: %w", err)
	}

	// amountOut = reserveOut - newReserveOut
	amountOut := new(big.Int).Sub(reserveOut, newReserveOut)
	if amountOut.Sign() <= 0 {
		return nil, fmt.Errorf("stable_swap Quote: output amount is non-positive")
	}

	// 计算价格影响
	priceImpact := calcStableSwapPriceImpact(amountIn, amountOut, reserveIn, reserveOut)

	return &dexwallet.Quote{
		DexID:        s.dexID,
		ProtocolType: dexwallet.ProtocolStableSwap,
		Priority:     dexwallet.PriorityMedium,
		InputAmount:  new(big.Int).Set(amountIn),
		OutputAmount: amountOut,
		PriceImpact:  priceImpact,
		Pool:         pool,
		EstimatedGas: big.NewInt(250000),
	}, nil
}

// stableSwapData 从 Pool.Extra 中提取的 StableSwap 数据。
type stableSwapData struct {
	reserveBase        *big.Int
	reserveQuote       *big.Int
	amplificationCoeff int64
	invariantD         *big.Int
}

// extractStableSwapData 从 Pool.Extra 中提取 StableSwap 参数。
func extractStableSwapData(pool *dexwallet.Pool) (*stableSwapData, error) {
	if pool == nil {
		return nil, fmt.Errorf("pool is nil")
	}
	if pool.Extra == nil {
		return nil, fmt.Errorf("pool.Extra is nil")
	}

	data := &stableSwapData{}

	rb, ok := pool.Extra["ReserveBase"]
	if !ok {
		return nil, fmt.Errorf("pool.Extra missing ReserveBase")
	}
	data.reserveBase, ok = rb.(*big.Int)
	if !ok {
		return nil, fmt.Errorf("pool.Extra ReserveBase is not *big.Int")
	}

	rq, ok := pool.Extra["ReserveQuote"]
	if !ok {
		return nil, fmt.Errorf("pool.Extra missing ReserveQuote")
	}
	data.reserveQuote, ok = rq.(*big.Int)
	if !ok {
		return nil, fmt.Errorf("pool.Extra ReserveQuote is not *big.Int")
	}

	ac, ok := pool.Extra["AmplificationCoeff"]
	if !ok {
		return nil, fmt.Errorf("pool.Extra missing AmplificationCoeff")
	}
	data.amplificationCoeff, ok = ac.(int64)
	if !ok {
		return nil, fmt.Errorf("pool.Extra AmplificationCoeff is not int64")
	}

	id, ok := pool.Extra["InvariantD"]
	if !ok {
		return nil, fmt.Errorf("pool.Extra missing InvariantD")
	}
	data.invariantD, ok = id.(*big.Int)
	if !ok {
		return nil, fmt.Errorf("pool.Extra InvariantD is not *big.Int")
	}

	return data, nil
}

// solveStableSwap 使用牛顿迭代法求解 StableSwap 不变量方程中的 y。
//
// 不变量方程（两资产简化版）：
//
//	A * D * (x + y) + x * y = A * D^2 + (D/2)^2
//
// 已知 x（新的 reserveIn），求 y（新的 reserveOut）。
//
// 整理为关于 y 的方程：
//
//	f(y) = A*D*y + x*y + A*D*x - A*D^2 - D^2/4 = 0
//	     = y*(A*D + x) + A*D*x - A*D^2 - D^2/4 = 0
//	y = (A*D^2 + D^2/4 - A*D*x) / (A*D + x)
//
// 对于两资产的简化情况，这个方程是线性的，可以直接求解。
// 但为了演示牛顿迭代法（生产中多资产场景需要），我们仍然使用迭代方法。
func solveStableSwap(newReserveIn *big.Int, amplificationCoeff int64, invariantD *big.Int) (*big.Int, error) {
	A := big.NewInt(amplificationCoeff)
	D := new(big.Int).Set(invariantD)
	x := new(big.Int).Set(newReserveIn)

	// 直接求解（两资产情况）：
	// y = (A*D^2 + D^2/4 - A*D*x) / (A*D + x)
	AD := new(big.Int).Mul(A, D)     // A*D
	D2 := new(big.Int).Mul(D, D)     // D^2
	AD2 := new(big.Int).Mul(A, D2)   // A*D^2
	D2Div4 := new(big.Int).Div(D2, big.NewInt(4)) // D^2/4
	ADx := new(big.Int).Mul(AD, x)   // A*D*x

	// 分子 = A*D^2 + D^2/4 - A*D*x
	numerator := new(big.Int).Add(AD2, D2Div4)
	numerator.Sub(numerator, ADx)

	// 分母 = A*D + x
	denominator := new(big.Int).Add(AD, x)

	if denominator.Sign() <= 0 {
		return nil, fmt.Errorf("denominator is non-positive")
	}

	y := new(big.Int).Div(numerator, denominator)

	if y.Sign() < 0 {
		return nil, fmt.Errorf("solved y is negative, pool may be unbalanced beyond limit")
	}

	return y, nil
}

// CalcInvariantD 计算 StableSwap 的不变量 D。
// 给定储量 x, y 和放大系数 A，通过牛顿迭代法求解 D。
//
// 不变量方程：A * D * (x + y) + x * y = A * D^2 + (D/2)^2
// 整理为关于 D 的方程：
//
//	A*D^2 + D^2/4 - A*D*(x+y) - x*y = 0
//	D^2*(A + 1/4) - A*D*(x+y) - x*y = 0
//
// 使用求根公式或牛顿迭代法求解。
func CalcInvariantD(x, y *big.Int, amplificationCoeff int64) *big.Int {
	A := big.NewInt(amplificationCoeff)
	sum := new(big.Int).Add(x, y) // x + y

	// 初始估计 D = x + y（恒定和的情况）
	D := new(big.Int).Set(sum)

	// 牛顿迭代法
	// f(D) = A*D^2 + D^2/4 - A*D*(x+y) - x*y
	// f'(D) = 2*A*D + D/2 - A*(x+y)
	xy := new(big.Int).Mul(x, y)

	for i := 0; i < 256; i++ {
		D2 := new(big.Int).Mul(D, D) // D^2

		// f = A*D^2 + D^2/4 - A*D*sum - xy
		f := new(big.Int).Mul(A, D2)       // A*D^2
		D2Div4 := new(big.Int).Div(D2, big.NewInt(4)) // D^2/4
		f.Add(f, D2Div4)                   // + D^2/4
		ADsum := new(big.Int).Mul(A, D)
		ADsum.Mul(ADsum, sum)              // A*D*sum
		f.Sub(f, ADsum)                    // - A*D*sum
		f.Sub(f, xy)                       // - xy

		// f' = 2*A*D + D/2 - A*sum
		fp := new(big.Int).Mul(big.NewInt(2), A)
		fp.Mul(fp, D)                      // 2*A*D
		DDiv2 := new(big.Int).Div(D, big.NewInt(2)) // D/2
		fp.Add(fp, DDiv2)                  // + D/2
		Asum := new(big.Int).Mul(A, sum)
		fp.Sub(fp, Asum)                   // - A*sum

		if fp.Sign() == 0 {
			break
		}

		// D_new = D - f/f'
		adjustment := new(big.Int).Div(f, fp)
		newD := new(big.Int).Sub(D, adjustment)

		// 检查收敛
		diff := new(big.Int).Sub(newD, D)
		if diff.Sign() < 0 {
			diff.Neg(diff)
		}
		if diff.Cmp(big.NewInt(1)) <= 0 {
			return newD
		}

		D = newD
	}

	return D
}

// calcStableSwapPriceImpact 计算 StableSwap 的价格影响（基点）。
// 对于稳定币对，理想汇率是 1:1，因此理想输出 = amountIn。
func calcStableSwapPriceImpact(amountIn, amountOut, reserveIn, reserveOut *big.Int) uint64 {
	// 理想输出：按当前储量比例
	idealOutput := new(big.Int).Mul(amountIn, reserveOut)
	idealOutput.Div(idealOutput, reserveIn)

	if idealOutput.Sign() <= 0 {
		return 0
	}

	if amountOut.Cmp(idealOutput) >= 0 {
		return 0
	}

	// priceImpact = (idealOutput - amountOut) * 10000 / idealOutput
	diff := new(big.Int).Sub(idealOutput, amountOut)
	diff.Mul(diff, big.NewInt(10000))
	diff.Div(diff, idealOutput)

	return diff.Uint64()
}
