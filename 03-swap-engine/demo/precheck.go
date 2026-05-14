package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// PreChecker — 交易前检查器
// ============================================================

// PreChecker 交易前检查器。
// 在构建交易之前验证请求参数，防止构建注定失败的交易。
//
// 生产中对标 solwallet 的 precheck.go：
//   - 检查余额是否足够（包括 Gas 费）
//   - 检查滑点参数是否合理（不能太低也不能太高）
//   - 检查池子是否活跃
//   - 检查交易金额是否在合理范围内
//   - 检查 DEX 是否被禁用
type PreChecker struct {
	maxSlippageBps uint64   // 最大允许滑点（默认 5000 = 50%）
	minSlippageBps uint64   // 最小允许滑点（默认 10 = 0.1%）
	maxAmountUSD   *big.Int // 单笔最大金额（美元，防止手误）
	minAmountRaw   *big.Int // 最小交易金额（最小单位，过滤灰尘）
}

// NewPreChecker 创建默认配置的 PreChecker。
func NewPreChecker() *PreChecker {
	return &PreChecker{
		maxSlippageBps: 5000,                // 50%
		minSlippageBps: 10,                  // 0.1%
		maxAmountUSD:   big.NewInt(1000000000000), // 简化：原始单位上限（lamports/wei 量级）
		minAmountRaw:   big.NewInt(1000),    // 最小 1000 最小单位
	}
}

// Check 执行所有预检查。
// 如果任何检查失败，返回 *dexwallet.NonDegradableError（请求级别问题不应降级到其他 DEX）。
func (pc *PreChecker) Check(ctx context.Context, req dexwallet.SwapRequest, balance *big.Int, pool *dexwallet.Pool) error {
	// 估算 Gas 费（简化：使用固定值）
	estimatedFee := big.NewInt(50000) // 50000 最小单位作为估算 Gas 费

	if err := pc.checkBalance(ctx, req, balance, estimatedFee); err != nil {
		return err
	}
	if err := pc.checkSlippage(req); err != nil {
		return err
	}
	if err := pc.checkAmount(req); err != nil {
		return err
	}
	if err := pc.checkPoolActive(pool); err != nil {
		return err
	}

	return nil
}

// checkBalance 检查余额是否足够（包括 Gas 费）。
// 规则：balance >= amount + estimatedFee
func (pc *PreChecker) checkBalance(ctx context.Context, req dexwallet.SwapRequest, balance *big.Int, estimatedFee *big.Int) error {
	if balance == nil {
		return &dexwallet.NonDegradableError{
			Reason: "余额查询失败",
		}
	}

	totalRequired := new(big.Int).Add(req.Amount, estimatedFee)
	if balance.Cmp(totalRequired) < 0 {
		return &dexwallet.NonDegradableError{
			Reason: fmt.Sprintf("余额不足: 需要 %s（金额 %s + 预估费用 %s），可用 %s",
				totalRequired.String(), req.Amount.String(), estimatedFee.String(), balance.String()),
			Cause: &dexwallet.InsufficientBalanceError{
				Required:  totalRequired.String(),
				Available: balance.String(),
				Token:     req.FromToken.Symbol,
			},
		}
	}

	return nil
}

// checkSlippage 检查滑点参数是否在合理范围内。
// 规则：minSlippageBps <= slippageBps <= maxSlippageBps
func (pc *PreChecker) checkSlippage(req dexwallet.SwapRequest) error {
	if req.SlippageBps > pc.maxSlippageBps {
		return &dexwallet.NonDegradableError{
			Reason: fmt.Sprintf("滑点过高: %d BPS 超过最大允许值 %d BPS（%.1f%%）",
				req.SlippageBps, pc.maxSlippageBps, float64(pc.maxSlippageBps)/100),
			Cause: &dexwallet.SlippageExceededError{
				Actual:    req.SlippageBps,
				Threshold: pc.maxSlippageBps,
			},
		}
	}

	if req.SlippageBps < pc.minSlippageBps {
		return &dexwallet.NonDegradableError{
			Reason: fmt.Sprintf("滑点过低: %d BPS 低于最小允许值 %d BPS（%.1f%%），交易极可能失败",
				req.SlippageBps, pc.minSlippageBps, float64(pc.minSlippageBps)/100),
		}
	}

	return nil
}

// checkAmount 检查交易金额是否在合理范围内。
// 规则：amount > minAmountRaw 且 amount < maxAmountUSD（简化比较）
func (pc *PreChecker) checkAmount(req dexwallet.SwapRequest) error {
	if req.Amount == nil || req.Amount.Sign() <= 0 {
		return &dexwallet.NonDegradableError{
			Reason: "交易金额必须为正数",
		}
	}

	if req.Amount.Cmp(pc.minAmountRaw) < 0 {
		return &dexwallet.NonDegradableError{
			Reason: fmt.Sprintf("交易金额 %s 低于最小值 %s（灰尘过滤）",
				req.Amount.String(), pc.minAmountRaw.String()),
		}
	}

	// 简化：直接用原始金额与 maxAmountUSD 比较（生产中需要价格预言机转换）
	if req.Amount.Cmp(pc.maxAmountUSD) > 0 {
		return &dexwallet.NonDegradableError{
			Reason: fmt.Sprintf("交易金额 %s 超过单笔最大限额 %s（防止手误）",
				req.Amount.String(), pc.maxAmountUSD.String()),
		}
	}

	return nil
}

// checkPoolActive 检查池子是否处于活跃状态。
// 规则：pool.State == "active"
func (pc *PreChecker) checkPoolActive(pool *dexwallet.Pool) error {
	if pool == nil {
		return &dexwallet.NonDegradableError{
			Reason: "池子信息为空",
			Cause:  &dexwallet.PoolNotFoundError{},
		}
	}

	if pool.State != dexwallet.PoolStateActive {
		return &dexwallet.NonDegradableError{
			Reason: fmt.Sprintf("池子 %s 状态为 %s，非活跃状态无法交易",
				pool.Address, pool.State),
		}
	}

	return nil
}

// checkDexEnabled 检查 DEX 是否在禁用列表中。
// 规则：dexID 不在 disabledDexes 列表中
func (pc *PreChecker) checkDexEnabled(dexID dexwallet.DexID, disabledDexes []dexwallet.DexID) error {
	for _, disabled := range disabledDexes {
		if dexID == disabled {
			return &dexwallet.NonDegradableError{
				Reason: fmt.Sprintf("DEX %s 已被禁用", dexID),
			}
		}
	}
	return nil
}

// ============================================================
// PostChecker — 交易后检查器
// ============================================================

// PostChecker 交易后检查器。
// 在交易构建完成后、签名之前验证交易结果。
//
// 生产中对标 solwallet 的 postcheck.go：
//   - 检查输出金额是否满足滑点要求
//   - 检查优先费是否超过阈值（防止 fee 异常）
//   - 检查 Gas 消耗是否合理
//   - 模拟执行检查（SimulateTx）
type PostChecker struct {
	maxPriorityFee *big.Int // 最大优先费阈值
	maxGasCost     *big.Int // 最大 Gas 费阈值
}

// NewPostChecker 创建默认配置的 PostChecker。
func NewPostChecker() *PostChecker {
	return &PostChecker{
		maxPriorityFee: new(big.Int).Mul(big.NewInt(100), big.NewInt(1e9)), // 100 Gwei（或 0.1 SOL）
		maxGasCost:     new(big.Int).Mul(big.NewInt(1), big.NewInt(1e18)),  // 1 ETH（或 1e9 lamports = 1 SOL）
	}
}

// Check 执行所有后检查。
// 如果任何检查失败，返回 *dexwallet.NonDegradableError。
func (pc *PostChecker) Check(ctx context.Context, req dexwallet.SwapRequest, result *dexwallet.SwapResult) error {
	if err := pc.checkOutputAmount(req, result); err != nil {
		return err
	}
	if err := pc.checkPriorityFee(result); err != nil {
		return err
	}
	if err := pc.checkGasCost(result); err != nil {
		return err
	}
	if err := pc.checkTxDataNotEmpty(result); err != nil {
		return err
	}

	return nil
}

// checkOutputAmount 检查输出金额是否满足滑点要求。
// 规则：outputAmount >= minOutput
func (pc *PostChecker) checkOutputAmount(req dexwallet.SwapRequest, result *dexwallet.SwapResult) error {
	if result.OutputAmount == nil || result.OutputAmount.Sign() <= 0 {
		return &dexwallet.NonDegradableError{
			Reason: "输出金额为零或负数",
		}
	}

	if result.MinOutput == nil {
		return &dexwallet.NonDegradableError{
			Reason: "最小输出金额未设置",
		}
	}

	if result.OutputAmount.Cmp(result.MinOutput) < 0 {
		return &dexwallet.NonDegradableError{
			Reason: fmt.Sprintf("输出金额 %s 低于最小输出 %s（滑点保护触发）",
				result.OutputAmount.String(), result.MinOutput.String()),
			Cause: &dexwallet.SlippageExceededError{
				Actual:    result.SlippageBps,
				Threshold: req.SlippageBps,
			},
		}
	}

	return nil
}

// checkPriorityFee 检查优先费是否超过阈值。
// 规则：priorityFee <= maxPriorityFee
func (pc *PostChecker) checkPriorityFee(result *dexwallet.SwapResult) error {
	if result.PriorityFee == nil {
		// 没有设置优先费视为正常（某些链/场景不需要）
		return nil
	}

	if result.PriorityFee.Cmp(pc.maxPriorityFee) > 0 {
		return &dexwallet.NonDegradableError{
			Reason: fmt.Sprintf("优先费 %s 超过最大阈值 %s，可能存在异常",
				result.PriorityFee.String(), pc.maxPriorityFee.String()),
		}
	}

	return nil
}

// checkGasCost 检查 Gas 消耗是否合理。
// 规则：gasCost <= maxGasCost
func (pc *PostChecker) checkGasCost(result *dexwallet.SwapResult) error {
	if result.GasCost == nil {
		// 没有设置 Gas 费视为正常（某些链 Gas 费用在别处处理）
		return nil
	}

	if result.GasCost.Cmp(pc.maxGasCost) > 0 {
		return &dexwallet.NonDegradableError{
			Reason: fmt.Sprintf("Gas 费用 %s 超过最大阈值 %s，可能存在异常",
				result.GasCost.String(), pc.maxGasCost.String()),
		}
	}

	return nil
}

// checkTxDataNotEmpty 检查交易数据不为空。
// 规则：txData 不能为 nil 或空切片
func (pc *PostChecker) checkTxDataNotEmpty(result *dexwallet.SwapResult) error {
	if len(result.TxData) == 0 {
		return &dexwallet.NonDegradableError{
			Reason: "交易数据为空，构建可能失败",
		}
	}
	return nil
}

// ============================================================
// SwapValidator — 组合 PreCheck 和 PostCheck 的完整验证流程
// ============================================================

// SwapValidator 完整的 Swap 验证流程。
// 将 PreChecker 和 PostChecker 组合在一起，提供统一的验证入口。
//
// 使用方式：
//
//	validator := NewSwapValidator()
//	// 构建交易前
//	if err := validator.PreCheck(ctx, req, balance, pool); err != nil { ... }
//	// 构建交易
//	result, err := builder.Build(ctx, req)
//	// 签名前
//	if err := validator.PostCheck(ctx, req, result); err != nil { ... }
type SwapValidator struct {
	pre  *PreChecker
	post *PostChecker
}

// NewSwapValidator 创建带有默认配置的 SwapValidator。
func NewSwapValidator() *SwapValidator {
	return &SwapValidator{
		pre:  NewPreChecker(),
		post: NewPostChecker(),
	}
}

// PreCheck 执行交易前验证。
// 在构建交易之前调用，验证请求参数和前置条件。
func (v *SwapValidator) PreCheck(ctx context.Context, req dexwallet.SwapRequest, balance *big.Int, pool *dexwallet.Pool) error {
	return v.pre.Check(ctx, req, balance, pool)
}

// PostCheck 执行交易后验证。
// 在交易构建完成后、签名之前调用，验证构建结果。
func (v *SwapValidator) PostCheck(ctx context.Context, req dexwallet.SwapRequest, result *dexwallet.SwapResult) error {
	return v.post.Check(ctx, req, result)
}
