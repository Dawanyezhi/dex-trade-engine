package dexwallet

import (
	"errors"
	"fmt"
)

// P1-2: 自定义错误类型，支持上层 errors.As 判断。

// PoolNotFoundError 池子不存在。
type PoolNotFoundError struct {
	Address   string
	BaseMint  string
	QuoteMint string
}

func (e *PoolNotFoundError) Error() string {
	if e.Address != "" {
		return fmt.Sprintf("pool not found: %s", e.Address)
	}
	return fmt.Sprintf("no pool found for %s/%s", e.BaseMint, e.QuoteMint)
}

// DexNotFoundError DEX 不存在。
type DexNotFoundError struct {
	DexID DexID
}

func (e *DexNotFoundError) Error() string {
	return fmt.Sprintf("DEX not found: %s", e.DexID)
}

// SlippageExceededError 滑点超过阈值。
type SlippageExceededError struct {
	Actual    uint64
	Threshold uint64
}

func (e *SlippageExceededError) Error() string {
	return fmt.Sprintf("slippage %d bps exceeds threshold %d bps", e.Actual, e.Threshold)
}

// InsufficientBalanceError 余额不足。
type InsufficientBalanceError struct {
	Required  string
	Available string
	Token     string
}

func (e *InsufficientBalanceError) Error() string {
	return fmt.Sprintf("insufficient %s balance: need %s, have %s", e.Token, e.Required, e.Available)
}

// AllDexFailedError 所有 DEX 均失败。
type AllDexFailedError struct {
	Chain string
	Count int
}

func (e *AllDexFailedError) Error() string {
	return fmt.Sprintf("all %d DEX failed on chain %s", e.Count, e.Chain)
}

// ----- 错误分类：可降级 vs 不可降级 -----
//
// 生产系统区分错误类型决定是否降级到下一个 DEX：
//   可降级（Degradable）：当前 DEX 暂时无法处理，换一个 DEX 可能成功
//   不可降级（NonDegradable）：问题出在请求本身，换 DEX 也不会成功

// DegradableError 可降级错误（应尝试下一个 DEX）。
// 常见场景：池子流动性不足、DEX 暂时不可用、网络超时、报价失败。
type DegradableError struct {
	DexID  DexID
	Reason string
	Cause  error
}

func (e *DegradableError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[degradable] %s: %s: %v", e.DexID, e.Reason, e.Cause)
	}
	return fmt.Sprintf("[degradable] %s: %s", e.DexID, e.Reason)
}

func (e *DegradableError) Unwrap() error { return e.Cause }

// IsDegradable 判断错误是否可降级。
func IsDegradable(err error) bool {
	var de *DegradableError
	return errors.As(err, &de)
}

// NonDegradableError 不可降级错误（直接返回，不尝试其他 DEX）。
// 常见场景：余额不足、滑点参数异常、池子不存在、参数校验失败。
type NonDegradableError struct {
	Reason string
	Cause  error
}

func (e *NonDegradableError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[non-degradable] %s: %v", e.Reason, e.Cause)
	}
	return fmt.Sprintf("[non-degradable] %s", e.Reason)
}

func (e *NonDegradableError) Unwrap() error { return e.Cause }

// IsNonDegradable 判断错误是否不可降级。
func IsNonDegradable(err error) bool {
	var nde *NonDegradableError
	return errors.As(err, &nde)
}
