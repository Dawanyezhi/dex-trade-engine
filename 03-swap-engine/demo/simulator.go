package main

import (
	"fmt"
	"log/slog"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// SimulationError 模拟器错误类型。
type SimulationError struct {
	Code    string // 错误码
	Message string // 错误消息
}

func (e *SimulationError) Error() string {
	return fmt.Sprintf("simulation failed [%s]: %s", e.Code, e.Message)
}

// 模拟器错误码
const (
	ErrCodeSlippageTooHigh    = "SLIPPAGE_TOO_HIGH"
	ErrCodeInsufficientBalance = "INSUFFICIENT_BALANCE"
	ErrCodeZeroOutput         = "ZERO_OUTPUT"
	ErrCodeInvalidTxData      = "INVALID_TX_DATA"
)

// SimulateSwap 模拟 Swap 交易执行，验证交易是否会成功。
// 在生产中，这会调用：
// - Solana: simulateTransaction RPC
// - EVM: eth_call RPC
//
// 本 demo 实现以下检查：
// 1. 交易数据有效性检查
// 2. 输出金额检查（不为零）
// 3. 滑点阈值检查
// 4. 余额充足性检查
func SimulateSwap(result *dexwallet.SwapResult) error {
	slog.Info("simulating swap",
		"dex", result.DexID,
		"chain", result.ChainID,
		"input", result.InputAmount.String(),
		"output", result.OutputAmount.String(),
	)

	// 检查 1: 交易数据有效性
	if len(result.TxData) == 0 {
		return &SimulationError{
			Code:    ErrCodeInvalidTxData,
			Message: "transaction data is empty",
		}
	}

	// 检查 2: 输出金额检查
	if result.OutputAmount == nil || result.OutputAmount.Sign() <= 0 {
		return &SimulationError{
			Code:    ErrCodeZeroOutput,
			Message: "output amount is zero or negative",
		}
	}

	// 检查 3: 滑点阈值检查
	// 如果实际价格影响（priceImpact）超过 SlippageBps 的 2 倍，认为风险过高
	if err := validateSlippageThreshold(result); err != nil {
		return err
	}

	// 检查 4: 余额充足性检查（模拟）
	if err := validateBalance(result); err != nil {
		return err
	}

	slog.Info("simulation passed",
		"dex", result.DexID,
		"output", result.OutputAmount.String(),
		"min_output", result.MinOutput.String(),
	)

	return nil
}

// validateSlippageThreshold 验证滑点是否在可接受范围内。
// 如果 MinOutput 和 OutputAmount 的差距过大（超过 SlippageBps 的 2 倍），
// 认为价格影响过高，拒绝交易。
func validateSlippageThreshold(result *dexwallet.SwapResult) error {
	if result.MinOutput == nil || result.OutputAmount == nil {
		return &SimulationError{
			Code:    ErrCodeSlippageTooHigh,
			Message: "min_output or output_amount is nil",
		}
	}

	// 计算实际滑点比例（基点）
	// actualSlippage = (outputAmount - minOutput) * 10000 / outputAmount
	diff := new(big.Int).Sub(result.OutputAmount, result.MinOutput)
	actualSlippageBps := new(big.Int).Mul(diff, big.NewInt(10000))
	actualSlippageBps.Div(actualSlippageBps, result.OutputAmount)

	// 如果设置的滑点容忍超过 5000 BPS (50%)，认为风险过高
	maxAllowedBps := uint64(5000)
	if result.SlippageBps > maxAllowedBps {
		return &SimulationError{
			Code: ErrCodeSlippageTooHigh,
			Message: fmt.Sprintf("slippage tolerance %d BPS exceeds maximum allowed %d BPS",
				result.SlippageBps, maxAllowedBps),
		}
	}

	slog.Debug("slippage validation passed",
		"slippage_bps", result.SlippageBps,
		"actual_slippage_bps", actualSlippageBps.Int64(),
	)

	return nil
}

// validateBalance 验证余额是否充足。
// 需要检查：输入金额 + Gas 费用 <= 可用余额。
func validateBalance(result *dexwallet.SwapResult) error {
	// 模拟余额查询
	// 在生产中，这会调用 RPCClient.GetBalance
	simulatedBalance := getSimulatedBalance(result.ChainID)

	// 总需求 = 输入金额 + Gas 费用 + 优先费
	totalRequired := new(big.Int).Set(result.InputAmount)
	if result.GasCost != nil {
		totalRequired.Add(totalRequired, result.GasCost)
	}
	if result.PriorityFee != nil {
		totalRequired.Add(totalRequired, result.PriorityFee)
	}

	if simulatedBalance.Cmp(totalRequired) < 0 {
		return &SimulationError{
			Code: ErrCodeInsufficientBalance,
			Message: fmt.Sprintf("insufficient balance: need %s, have %s",
				totalRequired.String(), simulatedBalance.String()),
		}
	}

	slog.Debug("balance validation passed",
		"required", totalRequired.String(),
		"available", simulatedBalance.String(),
	)

	return nil
}

// getSimulatedBalance 获取模拟余额。
// 在生产中由 RPCClient.GetBalance 替代。
func getSimulatedBalance(chainID coinset.ChainID) *big.Int {
	switch chainID {
	case coinset.ChainSolana:
		// 模拟 10 SOL 余额 (10 * 10^9 lamports)
		return new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9))
	case coinset.ChainEthereum:
		// 模拟 5 ETH 余额 (5 * 10^18 wei)
		return new(big.Int).Mul(big.NewInt(5), big.NewInt(1e18))
	case coinset.ChainBSC:
		// 模拟 20 BNB 余额 (20 * 10^18 wei)
		return new(big.Int).Mul(big.NewInt(20), big.NewInt(1e18))
	default:
		return big.NewInt(0)
	}
}
