// Package bigint 提供精确金额处理，禁止 float64。
// [dexwallet 通用层] — 所有链共享的精确金额计算。
package bigint

import (
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"
)

// Amount 表示链上精确金额，内部使用 big.Int 存储最小单位（如 lamports / wei）。
type Amount struct {
	raw      *big.Int
	decimals uint8
}

// NewAmount 从 big.Int 原始值和精度创建 Amount。
func NewAmount(raw *big.Int, decimals uint8) Amount {
	if raw == nil {
		raw = new(big.Int)
	}
	return Amount{raw: new(big.Int).Set(raw), decimals: decimals}
}

// NewAmountFromString 从字符串创建 Amount（如 "1000000000"）。
func NewAmountFromString(s string, decimals uint8) (Amount, error) {
	raw, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return Amount{}, fmt.Errorf("invalid amount string: %s", s)
	}
	return NewAmount(raw, decimals), nil
}

// NewAmountFromHuman 从人类可读数量创建 Amount（如 "1.5" SOL, decimals=9 → 1500000000）。
func NewAmountFromHuman(human string, decimals uint8) (Amount, error) {
	d, err := decimal.NewFromString(human)
	if err != nil {
		return Amount{}, fmt.Errorf("invalid human amount: %s: %w", human, err)
	}
	// human * 10^decimals
	multiplier := decimal.NewFromInt(10).Pow(decimal.NewFromInt(int64(decimals)))
	raw := d.Mul(multiplier)
	if !raw.Equal(raw.Truncate(0)) {
		return Amount{}, fmt.Errorf("amount %s exceeds precision for %d decimals", human, decimals)
	}
	bigRaw := raw.BigInt()
	return NewAmount(bigRaw, decimals), nil
}

// Raw 返回原始 big.Int 值（最小单位）。
func (a Amount) Raw() *big.Int {
	if a.raw == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(a.raw)
}

// Decimals 返回精度。
func (a Amount) Decimals() uint8 {
	return a.decimals
}

// Human 返回人类可读字符串（如 "1.5"）。
func (a Amount) Human() string {
	if a.raw == nil {
		return "0"
	}
	d := decimal.NewFromBigInt(a.raw, -int32(a.decimals))
	return d.String()
}

// String 返回原始值字符串。
func (a Amount) String() string {
	if a.raw == nil {
		return "0"
	}
	return a.raw.String()
}

// IsZero 检查金额是否为零。
func (a Amount) IsZero() bool {
	return a.raw == nil || a.raw.Sign() == 0
}

// IsPositive 检查金额是否为正。
func (a Amount) IsPositive() bool {
	return a.raw != nil && a.raw.Sign() > 0
}

// Add 返回 a + b，精度必须相同。
func (a Amount) Add(b Amount) (Amount, error) {
	if a.decimals != b.decimals {
		return Amount{}, fmt.Errorf("decimal mismatch: %d vs %d", a.decimals, b.decimals)
	}
	result := new(big.Int).Add(a.Raw(), b.Raw())
	return NewAmount(result, a.decimals), nil
}

// Sub 返回 a - b，精度必须相同。
func (a Amount) Sub(b Amount) (Amount, error) {
	if a.decimals != b.decimals {
		return Amount{}, fmt.Errorf("decimal mismatch: %d vs %d", a.decimals, b.decimals)
	}
	result := new(big.Int).Sub(a.Raw(), b.Raw())
	return NewAmount(result, a.decimals), nil
}

// Cmp 比较两个金额：-1（a<b）、0（a==b）、1（a>b）。
func (a Amount) Cmp(b Amount) int {
	return a.Raw().Cmp(b.Raw())
}

// MulBigInt 乘以一个 big.Int 倍数。
func (a Amount) MulBigInt(n *big.Int) Amount {
	result := new(big.Int).Mul(a.Raw(), n)
	return NewAmount(result, a.decimals)
}

// DivBigInt 除以一个 big.Int（整数除法）。
func (a Amount) DivBigInt(n *big.Int) Amount {
	if n.Sign() == 0 {
		return NewAmount(new(big.Int), a.decimals)
	}
	result := new(big.Int).Div(a.Raw(), n)
	return NewAmount(result, a.decimals)
}

// ConvertDecimals 将金额转换到目标精度。
// 例如：SOL（9位）转 lamports（0位），或 EVM 18位代币转 6位 USDC。
// 当目标精度小于当前精度时，超出部分会被截断（整数除法）。
func (a Amount) ConvertDecimals(targetDecimals uint8) Amount {
	raw := a.Raw()
	if a.decimals == targetDecimals {
		return NewAmount(raw, targetDecimals)
	}
	if targetDecimals > a.decimals {
		// 放大：乘以 10^(target - current)
		diff := targetDecimals - a.decimals
		multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(diff)), nil)
		raw.Mul(raw, multiplier)
	} else {
		// 缩小：除以 10^(current - target)，截断
		diff := a.decimals - targetDecimals
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(diff)), nil)
		raw.Div(raw, divisor)
	}
	return NewAmount(raw, targetDecimals)
}

// CalcSlippageBps 计算 expected 和 actual 之间的滑点（基点，1 bps = 0.01%）。
// 返回值为正数表示 actual < expected（不利滑点）。
func CalcSlippageBps(expected, actual Amount) (int64, error) {
	if expected.decimals != actual.decimals {
		return 0, fmt.Errorf("decimal mismatch: %d vs %d", expected.decimals, actual.decimals)
	}
	if expected.IsZero() {
		return 0, fmt.Errorf("expected amount is zero")
	}
	// slippage_bps = (expected - actual) * 10000 / expected
	diff := new(big.Int).Sub(expected.Raw(), actual.Raw())
	diff.Mul(diff, big.NewInt(10000))
	diff.Div(diff, expected.Raw())
	return diff.Int64(), nil
}
