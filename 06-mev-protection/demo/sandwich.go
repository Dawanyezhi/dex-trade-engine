// Package main 实现 MEV 防护与交易优化的演示。
// [dexwallet 通用层] -- MEV 攻击模拟、贿赂服务、优先费推荐。
package main

import (
	"fmt"
	"math/big"
)

// SandwichSimulator 模拟三明治攻击场景。
// 使用 AMM 恒定乘积模型（x * y = k），模拟攻击者如何通过前置和后置交易提取利润。
type SandwichSimulator struct {
	poolReserveA *big.Int // 池子代币 A 储备
	poolReserveB *big.Int // 池子代币 B 储备
	feeRate      uint64   // 手续费（基点，30 = 0.3%）
}

// SandwichResult 三明治攻击模拟结果。
type SandwichResult struct {
	VictimPriceWithout *big.Int // 无攻击时受害者获得的输出
	VictimPriceWith    *big.Int // 被攻击后受害者获得的输出
	VictimLoss         *big.Int // 受害者损失
	AttackerProfit     *big.Int // 攻击者利润（扣除手续费，不含 Gas）
	PriceImpactBps     uint64   // 价格影响（基点）
}

// NewSandwichSimulator 创建三明治攻击模拟器。
func NewSandwichSimulator(reserveA, reserveB *big.Int, feeRate uint64) *SandwichSimulator {
	if reserveA == nil || reserveB == nil {
		panic("reserves must not be nil")
	}
	if reserveA.Sign() <= 0 || reserveB.Sign() <= 0 {
		panic("reserves must be positive")
	}
	return &SandwichSimulator{
		poolReserveA: new(big.Int).Set(reserveA),
		poolReserveB: new(big.Int).Set(reserveB),
		feeRate:      feeRate,
	}
}

// SimulateAttack 模拟完整的三明治攻击。
//
// 交易方向：用 tokenA 买 tokenB（攻击者和受害者都是这个方向）。
//
// 步骤：
//  1. 攻击者前置交易（抢买 tokenB，推高 tokenB 的价格）
//  2. 受害者交易（以更高价格买入 tokenB，获得更少的 tokenB）
//  3. 攻击者后置交易（卖出 tokenB，换回 tokenA，赚取差价）
func (s *SandwichSimulator) SimulateAttack(victimAmountIn *big.Int, attackerAmountIn *big.Int) *SandwichResult {
	if victimAmountIn == nil || victimAmountIn.Sign() <= 0 {
		panic("victimAmountIn must be positive")
	}
	if attackerAmountIn == nil || attackerAmountIn.Sign() <= 0 {
		panic("attackerAmountIn must be positive")
	}

	// ====== 基线：无攻击时受害者的输出 ======
	victimOutputWithout := calcOutput(
		victimAmountIn,
		s.poolReserveA,
		s.poolReserveB,
		s.feeRate,
	)

	// ====== 步骤 1：攻击者前置交易（用 tokenA 买 tokenB）======
	// 当前池子: (reserveA, reserveB)
	reserveA := new(big.Int).Set(s.poolReserveA)
	reserveB := new(big.Int).Set(s.poolReserveB)

	attackerOutputB := calcOutput(attackerAmountIn, reserveA, reserveB, s.feeRate)

	// 更新池子状态
	reserveA.Add(reserveA, attackerAmountIn)
	reserveB.Sub(reserveB, attackerOutputB)

	// ====== 步骤 2：受害者交易（用 tokenA 买 tokenB）======
	// 池子已被攻击者推高价格
	victimOutputWith := calcOutput(victimAmountIn, reserveA, reserveB, s.feeRate)

	// 更新池子状态
	reserveA.Add(reserveA, victimAmountIn)
	reserveB.Sub(reserveB, victimOutputWith)

	// ====== 步骤 3：攻击者后置交易（卖出 tokenB 换回 tokenA）======
	// 攻击者用步骤 1 获得的 tokenB 换回 tokenA
	// 注意方向反转：输入是 tokenB，输出是 tokenA
	attackerReturnA := calcOutput(attackerOutputB, reserveB, reserveA, s.feeRate)

	// ====== 计算结果 ======
	victimLoss := new(big.Int).Sub(victimOutputWithout, victimOutputWith)
	if victimLoss.Sign() < 0 {
		victimLoss.SetInt64(0)
	}

	attackerProfit := new(big.Int).Sub(attackerReturnA, attackerAmountIn)
	if attackerProfit.Sign() < 0 {
		attackerProfit.SetInt64(0)
	}

	// 价格影响：受害者损失占无攻击输出的比例（基点）
	var priceImpactBps uint64
	if victimOutputWithout.Sign() > 0 {
		impact := new(big.Int).Mul(victimLoss, big.NewInt(10000))
		impact.Div(impact, victimOutputWithout)
		priceImpactBps = impact.Uint64()
	}

	return &SandwichResult{
		VictimPriceWithout: victimOutputWithout,
		VictimPriceWith:    victimOutputWith,
		VictimLoss:         victimLoss,
		AttackerProfit:     attackerProfit,
		PriceImpactBps:     priceImpactBps,
	}
}

// calcOutput 计算 AMM 恒定乘积公式的输出。
// amountOut = reserveOut * amountIn * (10000 - feeRate) / (reserveIn * 10000 + amountIn * (10000 - feeRate))
func calcOutput(amountIn, reserveIn, reserveOut *big.Int, feeRate uint64) *big.Int {
	if amountIn.Sign() <= 0 || reserveIn.Sign() <= 0 || reserveOut.Sign() <= 0 {
		return big.NewInt(0)
	}

	feeFactor := big.NewInt(int64(10000 - feeRate))
	bigBase := big.NewInt(10000)

	// effectiveIn = amountIn * (10000 - feeRate)
	effectiveIn := new(big.Int).Mul(amountIn, feeFactor)

	// numerator = reserveOut * effectiveIn
	numerator := new(big.Int).Mul(reserveOut, effectiveIn)

	// denominator = reserveIn * 10000 + effectiveIn
	denominator := new(big.Int).Mul(reserveIn, bigBase)
	denominator.Add(denominator, effectiveIn)

	if denominator.Sign() <= 0 {
		return big.NewInt(0)
	}

	return new(big.Int).Div(numerator, denominator)
}

// String 格式化 SandwichResult 为可读字符串。
func (r *SandwichResult) String() string {
	return fmt.Sprintf(
		"无攻击输出: %s, 被攻击输出: %s, 受害者损失: %s, 攻击者利润: %s, 价格影响: %d bps",
		r.VictimPriceWithout.String(),
		r.VictimPriceWith.String(),
		r.VictimLoss.String(),
		r.AttackerProfit.String(),
		r.PriceImpactBps,
	)
}
