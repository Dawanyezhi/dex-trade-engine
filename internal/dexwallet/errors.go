package dexwallet

import "fmt"

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
