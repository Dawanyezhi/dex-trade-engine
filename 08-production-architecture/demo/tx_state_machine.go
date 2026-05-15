// 交易状态机演示。
// 展示完整的交易生命周期管理：8 态状态机 + 状态转移验证 + RejectCode 错误码体系。
//
// 对标 irwallet walletmodel/status.go 和 rejects.go 的设计思路。
package main

import (
	"fmt"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ----- 交易状态机 -----

// StateTransition 记录一次状态变更。
type StateTransition struct {
	From      dexwallet.TxStatus // 变更前状态
	To        dexwallet.TxStatus // 变更后状态
	Reason    string             // 变更原因
	Timestamp time.Time          // 变更时间
}

// TxStateMachine 交易状态机，管理交易的完整生命周期。
//
// 核心职责：
//   - 维护当前交易状态
//   - 校验状态转移的合法性（基于 dexwallet.ValidTransitions）
//   - 记录所有状态变更历史，便于排查和审计
type TxStateMachine struct {
	record  *dexwallet.TxRecord // 关联的交易记录
	history []StateTransition   // 状态变更历史
}

// NewTxStateMachine 创建交易状态机。
// 传入的 TxRecord 的 Status 字段将作为初始状态。
func NewTxStateMachine(record *dexwallet.TxRecord) *TxStateMachine {
	return &TxStateMachine{
		record:  record,
		history: make([]StateTransition, 0),
	}
}

// Transition 执行状态转移。
// 会先检查转移的合法性，不合法则返回错误且不变更状态。
// reason 用于记录此次转移的原因（如 "交易已上链" / "确认超时" 等）。
func (sm *TxStateMachine) Transition(to dexwallet.TxStatus, reason string) error {
	from := sm.record.Status

	if !dexwallet.CanTransition(from, to) {
		return fmt.Errorf("非法状态转移: %s -> %s (原因: %s)", from, to, reason)
	}

	// 记录变更历史
	sm.history = append(sm.history, StateTransition{
		From:      from,
		To:        to,
		Reason:    reason,
		Timestamp: time.Now(),
	})

	// 更新状态
	sm.record.Status = to
	sm.record.UpdatedAt = time.Now()

	return nil
}

// Current 返回当前状态。
func (sm *TxStateMachine) Current() dexwallet.TxStatus {
	return sm.record.Status
}

// History 返回完整的状态变更历史。
func (sm *TxStateMachine) History() []StateTransition {
	result := make([]StateTransition, len(sm.history))
	copy(result, sm.history)
	return result
}

// ----- RejectCode 错误码体系 -----

// RejectCode 拒绝码，对标 irwallet rejects.go。
// 按段（segment）分类，便于快速定位问题类别：
//   - 段 1 (1000-1999): 订单参数错误
//   - 段 2 (2000-2999): 金额和费用错误
//   - 段 3 (3000-3999): 比率和滑点错误
//   - 段 4 (4000-4999): 合约和 DEX 错误
//   - 段 8 (8000-8999): 余额和流动性错误
//   - 段 99 (99000-99999): 内部错误
type RejectCode int

const (
	RejectOK RejectCode = 0 // 成功

	// 段 1: 订单参数 (1000-1999)
	RejectInvalidOrderID RejectCode = 1001 // 无效的订单 ID
	RejectInvalidUserID  RejectCode = 1002 // 无效的用户 ID

	// 段 2: 金额和费用 (2000-2999)
	RejectInvalidAmount RejectCode = 2001 // 无效的交易金额
	RejectInvalidFee    RejectCode = 2002 // 无效的手续费

	// 段 3: 比率和滑点 (3000-3999)
	RejectInvalidSlippage  RejectCode = 3001 // 无效的滑点设置
	RejectSlippageOverflow RejectCode = 3002 // 滑点超出允许范围

	// 段 4: 合约和 DEX (4000-4999)
	RejectInvalidContract RejectCode = 4001 // 无效的合约地址
	RejectDexNotFound     RejectCode = 4002 // 找不到目标 DEX

	// 段 8: 余额和流动性 (8000-8999)
	RejectInsufficientBalance RejectCode = 8001 // 余额不足
	RejectGasTooHigh          RejectCode = 8002 // Gas 费用过高
	RejectLiquidityTooLow     RejectCode = 8003 // 流动性不足

	// 段 99: 内部错误 (99000-99999)
	RejectServerError RejectCode = 99001 // 服务器内部错误
)

// rejectDescriptions 错误码到可读描述的映射。
var rejectDescriptions = map[RejectCode]string{
	RejectOK:                  "成功",
	RejectInvalidOrderID:      "无效的订单 ID",
	RejectInvalidUserID:       "无效的用户 ID",
	RejectInvalidAmount:       "无效的交易金额",
	RejectInvalidFee:          "无效的手续费",
	RejectInvalidSlippage:     "无效的滑点设置",
	RejectSlippageOverflow:    "滑点超出允许范围",
	RejectInvalidContract:     "无效的合约地址",
	RejectDexNotFound:         "找不到目标 DEX",
	RejectInsufficientBalance: "余额不足",
	RejectGasTooHigh:          "Gas 费用过高",
	RejectLiquidityTooLow:     "流动性不足",
	RejectServerError:         "服务器内部错误",
}

// String 返回错误码的可读描述。
func (r RejectCode) String() string {
	if desc, ok := rejectDescriptions[r]; ok {
		return fmt.Sprintf("[%d] %s", int(r), desc)
	}
	return fmt.Sprintf("[%d] 未知错误", int(r))
}

// IsSuccess 检查是否为成功状态。
func (r RejectCode) IsSuccess() bool {
	return r == RejectOK
}

// Category 返回错误码所属的分类名。
func (r RejectCode) Category() string {
	code := int(r)
	switch {
	case code == 0:
		return "成功"
	case code >= 1000 && code < 2000:
		return "订单参数"
	case code >= 2000 && code < 3000:
		return "金额费用"
	case code >= 3000 && code < 4000:
		return "比率滑点"
	case code >= 4000 && code < 5000:
		return "合约DEX"
	case code >= 8000 && code < 9000:
		return "余额流动性"
	case code >= 99000 && code < 100000:
		return "内部错误"
	default:
		return "未知分类"
	}
}

