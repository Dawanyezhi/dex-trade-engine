package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ScaleFactor Bonding Curve 价格系数的缩放因子（10^18）。
// 用于将小数系数转化为整数运算。
// 例如 a=0.001 表示为 CoefficientA = 10^15（即 0.001 * 10^18）。
var ScaleFactor = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

// BondingCurveProtocol 联合曲线协议（内盘）。
// 对应 PumpFun（Solana）等发射台项目。
//
// 定价公式：
//
//	price(supply) = a * supply^n
//
// 其中 a 是价格系数（按 ScaleFactor 缩放），n 是曲线指数。
// n=1 线性曲线：价格与供应量成正比
// n=2 二次曲线：价格与供应量的平方成正比
type BondingCurveProtocol struct {
	dexID dexwallet.DexID
}

// NewBondingCurveProtocol 创建一个 Bonding Curve 协议实例。
func NewBondingCurveProtocol(dexID dexwallet.DexID) *BondingCurveProtocol {
	return &BondingCurveProtocol{dexID: dexID}
}

// DexID 返回 DEX 标识。
func (b *BondingCurveProtocol) DexID() dexwallet.DexID {
	return b.dexID
}

// ProtocolType 返回协议类型。
func (b *BondingCurveProtocol) ProtocolType() dexwallet.ProtocolType {
	return dexwallet.ProtocolBondingCurve
}

// GetPrice 获取当前价格。
// price = a * currentSupply^n / ScaleFactor
func (b *BondingCurveProtocol) GetPrice(_ context.Context, pool *dexwallet.Pool) (*big.Int, error) {
	data, err := extractBondingCurveData(pool)
	if err != nil {
		return nil, fmt.Errorf("bonding_curve GetPrice: %w", err)
	}

	if data.currentSupply.Sign() == 0 {
		// 供应量为 0 时，起始价格 = a * 0^n = 0
		// 返回最低价格 a（第一个代币的价格）
		return new(big.Int).Set(data.coefficientA), nil
	}

	price := calcBondingCurvePrice(data.currentSupply, data.coefficientA, data.exponent)
	return price, nil
}

// Quote 获取报价。
// Buy 方向：用 quote 代币（如 SOL）购买 base 代币（新代币）。通过沿曲线积分计算能买到的数量。
// Sell 方向：卖出 base 代币，获得 quote 代币。通过沿曲线积分计算能获得的金额。
func (b *BondingCurveProtocol) Quote(_ context.Context, pool *dexwallet.Pool, amountIn *big.Int, direction dexwallet.SwapDirection) (*dexwallet.Quote, error) {
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("bonding_curve Quote: amountIn must be positive")
	}

	data, err := extractBondingCurveData(pool)
	if err != nil {
		return nil, fmt.Errorf("bonding_curve Quote: %w", err)
	}

	var amountOut *big.Int
	graduated := false

	switch direction {
	case dexwallet.SwapDirectionBuy:
		// 用 quote 买 base
		amountOut, err = calcBondingCurveBuy(amountIn, data)
		if err != nil {
			return nil, fmt.Errorf("bonding_curve Quote buy: %w", err)
		}

		// 检查毕业条件：收集的流动性 + 本次输入 >= 毕业目标
		if data.graduationTarget != nil && data.graduationTarget.Sign() > 0 {
			totalLiquidity := new(big.Int).Add(data.collectedLiquidity, amountIn)
			if totalLiquidity.Cmp(data.graduationTarget) >= 0 {
				graduated = true
			}
		}

	case dexwallet.SwapDirectionSell:
		// 卖出 base 换 quote
		if amountIn.Cmp(data.currentSupply) > 0 {
			return nil, fmt.Errorf("bonding_curve Quote sell: sell amount %s exceeds current supply %s",
				amountIn.String(), data.currentSupply.String())
		}
		amountOut, err = calcBondingCurveSell(amountIn, data)
		if err != nil {
			return nil, fmt.Errorf("bonding_curve Quote sell: %w", err)
		}

	default:
		return nil, fmt.Errorf("bonding_curve Quote: unknown direction: %s", direction)
	}

	if amountOut.Sign() <= 0 {
		return nil, fmt.Errorf("bonding_curve Quote: output amount is zero")
	}

	// 扣除手续费
	if pool.FeeRate > 0 {
		feeFactor := big.NewInt(int64(10000 - pool.FeeRate))
		amountOut.Mul(amountOut, feeFactor)
		amountOut.Div(amountOut, big.NewInt(10000))
	}

	// 计算价格影响
	// 对于 Bonding Curve，价格影响 = 交易后价格与交易前价格的差异
	priceImpact := calcBondingCurvePriceImpact(amountIn, data, direction)

	quote := &dexwallet.Quote{
		DexID:        b.dexID,
		ProtocolType: dexwallet.ProtocolBondingCurve,
		Priority:     dexwallet.PriorityHigh, // 内盘优先级最高
		InputAmount:  new(big.Int).Set(amountIn),
		OutputAmount: amountOut,
		PriceImpact:  priceImpact,
		Pool:         pool,
		EstimatedGas: big.NewInt(150000),
	}

	// 标记毕业状态
	if graduated {
		if quote.Pool.Extra == nil {
			quote.Pool.Extra = make(map[string]interface{})
		}
		quote.Pool.Extra["Graduated"] = true
	}

	return quote, nil
}

// bondingCurveData 从 Pool.Extra 中提取的 Bonding Curve 数据。
type bondingCurveData struct {
	currentSupply       *big.Int
	coefficientA        *big.Int
	exponent            int64
	graduationTarget    *big.Int
	collectedLiquidity  *big.Int
}

// extractBondingCurveData 从 Pool.Extra 中提取 Bonding Curve 参数。
func extractBondingCurveData(pool *dexwallet.Pool) (*bondingCurveData, error) {
	if pool == nil {
		return nil, fmt.Errorf("pool is nil")
	}
	if pool.Extra == nil {
		return nil, fmt.Errorf("pool.Extra is nil")
	}

	data := &bondingCurveData{}

	cs, ok := pool.Extra["CurrentSupply"]
	if !ok {
		return nil, fmt.Errorf("pool.Extra missing CurrentSupply")
	}
	data.currentSupply, ok = cs.(*big.Int)
	if !ok {
		return nil, fmt.Errorf("pool.Extra CurrentSupply is not *big.Int")
	}

	ca, ok := pool.Extra["CoefficientA"]
	if !ok {
		return nil, fmt.Errorf("pool.Extra missing CoefficientA")
	}
	data.coefficientA, ok = ca.(*big.Int)
	if !ok {
		return nil, fmt.Errorf("pool.Extra CoefficientA is not *big.Int")
	}

	exp, ok := pool.Extra["Exponent"]
	if !ok {
		return nil, fmt.Errorf("pool.Extra missing Exponent")
	}
	data.exponent, ok = exp.(int64)
	if !ok {
		return nil, fmt.Errorf("pool.Extra Exponent is not int64")
	}

	gt, ok := pool.Extra["GraduationTarget"]
	if ok {
		data.graduationTarget, _ = gt.(*big.Int)
	}

	cl, ok := pool.Extra["CollectedLiquidity"]
	if ok {
		data.collectedLiquidity, _ = cl.(*big.Int)
	}
	if data.collectedLiquidity == nil {
		data.collectedLiquidity = new(big.Int)
	}

	return data, nil
}

// calcBondingCurvePrice 计算指定供应量处的价格。
// price = a * supply^n / ScaleFactor
func calcBondingCurvePrice(supply, coefficientA *big.Int, exponent int64) *big.Int {
	if supply.Sign() == 0 {
		return new(big.Int)
	}

	// supply^n
	supplyPow := new(big.Int).Exp(supply, big.NewInt(exponent), nil)

	// price = a * supply^n / ScaleFactor
	price := new(big.Int).Mul(coefficientA, supplyPow)
	price.Div(price, ScaleFactor)

	return price
}

// calcBondingCurveBuy 计算给定 quote 输入能买到多少 base 代币。
//
// 对于线性曲线（n=1）：
//
//	cost = a * [(S+x)^2 - S^2] / 2 = a * x * (2S + x) / 2
//	给定 cost（即 amountIn），解出 x：
//	a * x^2 + 2*a*S*x - 2*cost*ScaleFactor = 0
//	x = (-2*a*S + sqrt(4*a^2*S^2 + 8*a*cost*ScaleFactor)) / (2*a)
//	  = (-S + sqrt(S^2 + 2*cost*ScaleFactor/a)) （简化后）
//
// 对于其他指数，使用数值逼近。
func calcBondingCurveBuy(amountIn *big.Int, data *bondingCurveData) (*big.Int, error) {
	if data.exponent == 1 {
		return calcLinearBondingCurveBuy(amountIn, data)
	}
	// 非线性曲线使用数值逼近（二分搜索）
	return calcGenericBondingCurveBuy(amountIn, data)
}

// calcLinearBondingCurveBuy 线性曲线（n=1）的买入计算。
// 解二次方程：a * x^2 + 2*a*S*x - 2*cost*ScaleFactor = 0
func calcLinearBondingCurveBuy(amountIn *big.Int, data *bondingCurveData) (*big.Int, error) {
	a := data.coefficientA
	s := data.currentSupply

	// discriminant = 4*a^2*S^2 + 8*a*amountIn*ScaleFactor
	// = 4*a*(a*S^2 + 2*amountIn*ScaleFactor)
	aS2 := new(big.Int).Mul(s, s)
	aS2.Mul(aS2, a)

	twoAmountScale := new(big.Int).Mul(amountIn, ScaleFactor)
	twoAmountScale.Mul(twoAmountScale, big.NewInt(2))

	inner := new(big.Int).Add(aS2, twoAmountScale)
	inner.Mul(inner, big.NewInt(4))
	inner.Mul(inner, a)

	// sqrt(discriminant)
	sqrtDisc := new(big.Int).Sqrt(inner)

	// x = (-2*a*S + sqrtDisc) / (2*a)
	twoAS := new(big.Int).Mul(big.NewInt(2), a)
	twoAS.Mul(twoAS, s)

	numerator := new(big.Int).Sub(sqrtDisc, twoAS)
	denominator := new(big.Int).Mul(big.NewInt(2), a)

	if denominator.Sign() == 0 {
		return nil, fmt.Errorf("coefficient a is zero")
	}

	result := new(big.Int).Div(numerator, denominator)
	if result.Sign() < 0 {
		result = new(big.Int)
	}

	return result, nil
}

// calcGenericBondingCurveBuy 通用曲线的买入计算（二分搜索）。
func calcGenericBondingCurveBuy(amountIn *big.Int, data *bondingCurveData) (*big.Int, error) {
	// 二分搜索：找到 x 使得 calcCostForTokens(x) 尽量接近 amountIn
	lo := big.NewInt(0)
	// 上界估计：假设全部按起始价格购买，最多能买 amountIn / 起始价格
	startPrice := calcBondingCurvePrice(
		new(big.Int).Add(data.currentSupply, big.NewInt(1)),
		data.coefficientA,
		data.exponent,
	)
	if startPrice.Sign() == 0 {
		startPrice = big.NewInt(1)
	}
	hi := new(big.Int).Mul(amountIn, big.NewInt(10))
	hi.Div(hi, startPrice)
	if hi.Cmp(big.NewInt(1)) < 0 {
		hi = big.NewInt(1)
	}
	hi.Mul(hi, big.NewInt(10)) // 给一些余量

	// 确保上界足够大
	for {
		cost := calcCostForTokens(data.currentSupply, hi, data.coefficientA, data.exponent)
		if cost.Cmp(amountIn) >= 0 {
			break
		}
		hi.Mul(hi, big.NewInt(2))
		// 防止无限循环
		if hi.BitLen() > 256 {
			return nil, fmt.Errorf("unable to find upper bound for buy calculation")
		}
	}

	// 二分搜索
	for new(big.Int).Sub(hi, lo).Cmp(big.NewInt(1)) > 0 {
		mid := new(big.Int).Add(lo, hi)
		mid.Div(mid, big.NewInt(2))

		cost := calcCostForTokens(data.currentSupply, mid, data.coefficientA, data.exponent)
		if cost.Cmp(amountIn) <= 0 {
			lo = mid
		} else {
			hi = mid
		}
	}

	return new(big.Int).Set(lo), nil
}

// calcCostForTokens 计算购买 amount 个代币的总成本。
// cost = a * [(S+amount)^(n+1) - S^(n+1)] / ((n+1) * ScaleFactor)
func calcCostForTokens(currentSupply, amount, coefficientA *big.Int, exponent int64) *big.Int {
	nPlus1 := big.NewInt(exponent + 1)

	// (S + amount)^(n+1)
	newSupply := new(big.Int).Add(currentSupply, amount)
	newPow := new(big.Int).Exp(newSupply, nPlus1, nil)

	// S^(n+1)
	oldPow := new(big.Int).Exp(currentSupply, nPlus1, nil)

	// diff = newPow - oldPow
	diff := new(big.Int).Sub(newPow, oldPow)

	// cost = a * diff / ((n+1) * ScaleFactor)
	cost := new(big.Int).Mul(coefficientA, diff)
	divisor := new(big.Int).Mul(nPlus1, ScaleFactor)
	cost.Div(cost, divisor)

	return cost
}

// calcBondingCurveSell 计算卖出指定数量 base 代币能获得的 quote 数量。
// revenue = a * [S^(n+1) - (S-amount)^(n+1)] / ((n+1) * ScaleFactor)
func calcBondingCurveSell(amountIn *big.Int, data *bondingCurveData) (*big.Int, error) {
	if amountIn.Cmp(data.currentSupply) > 0 {
		return nil, fmt.Errorf("sell amount exceeds current supply")
	}

	nPlus1 := big.NewInt(data.exponent + 1)

	// S^(n+1)
	oldPow := new(big.Int).Exp(data.currentSupply, nPlus1, nil)

	// (S - amount)^(n+1)
	newSupply := new(big.Int).Sub(data.currentSupply, amountIn)
	newPow := new(big.Int).Exp(newSupply, nPlus1, nil)

	// diff = oldPow - newPow
	diff := new(big.Int).Sub(oldPow, newPow)

	// revenue = a * diff / ((n+1) * ScaleFactor)
	revenue := new(big.Int).Mul(data.coefficientA, diff)
	divisor := new(big.Int).Mul(nPlus1, ScaleFactor)
	revenue.Div(revenue, divisor)

	return revenue, nil
}

// calcBondingCurvePriceImpact 计算 Bonding Curve 的价格影响（基点）。
func calcBondingCurvePriceImpact(amountIn *big.Int, data *bondingCurveData, direction dexwallet.SwapDirection) uint64 {
	priceBefore := calcBondingCurvePrice(data.currentSupply, data.coefficientA, data.exponent)
	if priceBefore.Sign() == 0 {
		return 0
	}

	var supplyAfter *big.Int
	switch direction {
	case dexwallet.SwapDirectionBuy:
		// 买入后供应量增加（近似用 amountIn / currentPrice 估算）
		if priceBefore.Sign() > 0 {
			tokensBought := new(big.Int).Mul(amountIn, ScaleFactor)
			tokensBought.Div(tokensBought, priceBefore)
			supplyAfter = new(big.Int).Add(data.currentSupply, tokensBought)
		} else {
			return 0
		}
	case dexwallet.SwapDirectionSell:
		supplyAfter = new(big.Int).Sub(data.currentSupply, amountIn)
		if supplyAfter.Sign() < 0 {
			supplyAfter = new(big.Int)
		}
	default:
		return 0
	}

	priceAfter := calcBondingCurvePrice(supplyAfter, data.coefficientA, data.exponent)

	// priceImpact = |priceAfter - priceBefore| * 10000 / priceBefore
	diff := new(big.Int).Sub(priceAfter, priceBefore)
	if diff.Sign() < 0 {
		diff.Neg(diff)
	}
	diff.Mul(diff, big.NewInt(10000))
	diff.Div(diff, priceBefore)

	return diff.Uint64()
}
