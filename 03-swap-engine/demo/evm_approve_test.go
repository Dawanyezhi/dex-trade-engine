package main

import (
	"context"
	"math/big"
	"strings"
	"testing"
)

// ============================================================
// EVM Approve 测试
// ============================================================

// ---------------------------------------------------------------------------
// MaxUint256 常量正确性
// ---------------------------------------------------------------------------

// TestMaxUint256_Value 测试 MaxUint256 常量值的正确性。
func TestMaxUint256_Value(t *testing.T) {
	// MaxUint256 应该等于 2^256 - 1
	expected := new(big.Int).Sub(
		new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil),
		big.NewInt(1),
	)

	if MaxUint256.Cmp(expected) != 0 {
		t.Errorf("MaxUint256 值不正确，期望=%s, 实际=%s", expected.String(), MaxUint256.String())
	}
}

// TestMaxUint256_HexRepresentation 测试 MaxUint256 的十六进制表示。
func TestMaxUint256_HexRepresentation(t *testing.T) {
	// 2^256 - 1 的十六进制表示应该是 64 个 f
	hexStr := MaxUint256.Text(16)
	expectedHex := strings.Repeat("f", 64)
	if hexStr != expectedHex {
		t.Errorf("MaxUint256 十六进制表示不正确，期望=%s, 实际=%s", expectedHex, hexStr)
	}
}

// TestMaxUint256_BitLen 测试 MaxUint256 的位长度。
func TestMaxUint256_BitLen(t *testing.T) {
	if MaxUint256.BitLen() != 256 {
		t.Errorf("MaxUint256 位长度期望=256, 实际=%d", MaxUint256.BitLen())
	}
}

// ---------------------------------------------------------------------------
// ApproveChecker — 无需 approve 的情况（allowance 足够）
// ---------------------------------------------------------------------------

// TestApproveChecker_NoApproveNeeded_ExactEqual 测试 allowance 恰好等于 amountIn 时无需 approve。
func TestApproveChecker_NoApproveNeeded_ExactEqual(t *testing.T) {
	amountIn := big.NewInt(1000000)

	// mock: allowance 恰好等于 amountIn
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return new(big.Int).Set(amountIn)
		},
		true,
	)

	need := checker.NeedApprove("0xToken", "0xOwner", "0xSpender", amountIn)
	if need {
		t.Error("allowance == amountIn 时不应需要 approve")
	}
}

// TestApproveChecker_NoApproveNeeded_MoreThanEnough 测试 allowance 大于 amountIn 时无需 approve。
func TestApproveChecker_NoApproveNeeded_MoreThanEnough(t *testing.T) {
	amountIn := big.NewInt(1000000)

	// mock: allowance 远大于 amountIn
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return new(big.Int).Set(MaxUint256) // 无限授权
		},
		true,
	)

	need := checker.NeedApprove("0xToken", "0xOwner", "0xSpender", amountIn)
	if need {
		t.Error("allowance >> amountIn 时不应需要 approve")
	}
}

// TestApproveChecker_CheckAndApprove_NoApproveNeeded 测试 CheckAndApprove 在 allowance 足够时返回 false。
func TestApproveChecker_CheckAndApprove_NoApproveNeeded(t *testing.T) {
	amountIn := big.NewInt(1000000)

	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return new(big.Int).Set(amountIn)
		},
		true,
	)

	ctx := context.Background()
	approved, tx, err := checker.CheckAndApprove(ctx, "0xToken", "0xOwner", "0xSpender", amountIn)
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if approved {
		t.Error("allowance 足够时 approved 应为 false")
	}
	if tx != nil {
		t.Error("allowance 足够时 tx 应为 nil")
	}
}

// ---------------------------------------------------------------------------
// ApproveChecker — 需要 exact approve 的情况
// ---------------------------------------------------------------------------

// TestApproveChecker_ExactApprove 测试精确授权模式。
func TestApproveChecker_ExactApprove(t *testing.T) {
	amountIn := big.NewInt(5000000)

	// mock: allowance 为 0，需要 approve
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return big.NewInt(0)
		},
		true, // useExactApprove = true
	)

	need := checker.NeedApprove("0xToken", "0xOwner", "0xSpender", amountIn)
	if !need {
		t.Error("allowance=0 时应需要 approve")
	}

	// 构建 approve 交易
	tx := checker.BuildApproveTx("0xToken", "0xSpender", amountIn)
	if tx == nil {
		t.Fatal("BuildApproveTx 不应返回 nil")
	}
	if tx.Amount.Cmp(amountIn) != 0 {
		t.Errorf("精确授权时 Amount 应等于 amountIn, 期望=%s, 实际=%s",
			amountIn.String(), tx.Amount.String())
	}
	if tx.Token != "0xToken" {
		t.Errorf("期望 Token=0xToken, 实际=%s", tx.Token)
	}
	if tx.Spender != "0xSpender" {
		t.Errorf("期望 Spender=0xSpender, 实际=%s", tx.Spender)
	}
}

// TestApproveChecker_ExactApprove_CheckAndApprove 测试精确授权模式的完整流程。
func TestApproveChecker_ExactApprove_CheckAndApprove(t *testing.T) {
	amountIn := big.NewInt(5000000)

	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return big.NewInt(0)
		},
		true,
	)

	ctx := context.Background()
	approved, tx, err := checker.CheckAndApprove(ctx, "0xToken", "0xOwner", "0xSpender", amountIn)
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if !approved {
		t.Error("allowance 不足时 approved 应为 true")
	}
	if tx == nil {
		t.Fatal("approved=true 时 tx 不应为 nil")
	}
	if tx.Amount.Cmp(amountIn) != 0 {
		t.Errorf("精确授权 Amount 期望=%s, 实际=%s", amountIn.String(), tx.Amount.String())
	}
}

// TestApproveChecker_ExactApprove_AllowanceInsufficient 测试 allowance 不足但不为零。
func TestApproveChecker_ExactApprove_AllowanceInsufficient(t *testing.T) {
	amountIn := big.NewInt(5000000)

	// mock: allowance 有一些但不够
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return big.NewInt(1000000) // 少于 amountIn
		},
		true,
	)

	need := checker.NeedApprove("0xToken", "0xOwner", "0xSpender", amountIn)
	if !need {
		t.Error("allowance < amountIn 时应需要 approve")
	}
}

// ---------------------------------------------------------------------------
// ApproveChecker — 需要 unlimited approve 的情况
// ---------------------------------------------------------------------------

// TestApproveChecker_UnlimitedApprove 测试无限授权模式。
func TestApproveChecker_UnlimitedApprove(t *testing.T) {
	amountIn := big.NewInt(5000000)

	// mock: allowance 为 0
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return big.NewInt(0)
		},
		false, // useExactApprove = false (unlimited)
	)

	need := checker.NeedApprove("0xToken", "0xOwner", "0xSpender", amountIn)
	if !need {
		t.Error("allowance=0 时应需要 approve")
	}

	// 构建 approve 交易
	tx := checker.BuildApproveTx("0xToken", "0xSpender", amountIn)
	if tx == nil {
		t.Fatal("BuildApproveTx 不应返回 nil")
	}

	// 无限授权时 Amount 应等于 MaxUint256
	if tx.Amount.Cmp(MaxUint256) != 0 {
		t.Errorf("无限授权时 Amount 应等于 MaxUint256, 期望=%s, 实际=%s",
			MaxUint256.String(), tx.Amount.String())
	}
}

// TestApproveChecker_UnlimitedApprove_CheckAndApprove 测试无限授权模式的完整流程。
func TestApproveChecker_UnlimitedApprove_CheckAndApprove(t *testing.T) {
	amountIn := big.NewInt(5000000)

	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return big.NewInt(0)
		},
		false,
	)

	ctx := context.Background()
	approved, tx, err := checker.CheckAndApprove(ctx, "0xToken", "0xOwner", "0xSpender", amountIn)
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if !approved {
		t.Error("allowance 不足时 approved 应为 true")
	}
	if tx == nil {
		t.Fatal("approved=true 时 tx 不应为 nil")
	}
	if tx.Amount.Cmp(MaxUint256) != 0 {
		t.Errorf("无限授权 Amount 期望=%s, 实际=%s", MaxUint256.String(), tx.Amount.String())
	}
}

// ---------------------------------------------------------------------------
// ApproveChecker — GetApproveResult 方法
// ---------------------------------------------------------------------------

// TestApproveChecker_GetApproveResult_NoApproveNeeded 测试无需 approve 时的 GetApproveResult。
func TestApproveChecker_GetApproveResult_NoApproveNeeded(t *testing.T) {
	amountIn := big.NewInt(1000000)

	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return new(big.Int).Set(amountIn)
		},
		true,
	)

	ctx := context.Background()
	result, err := checker.GetApproveResult(ctx, "0xToken", "0xOwner", "0xSpender", amountIn)
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if result.Needed {
		t.Error("allowance 足够时 Needed 应为 false")
	}
	if result.Tx != nil {
		t.Error("不需要 approve 时 Tx 应为 nil")
	}
	if result.Strategy != "exact" {
		t.Errorf("useExactApprove=true 时 Strategy 应为 exact, 实际=%s", result.Strategy)
	}
}

// TestApproveChecker_GetApproveResult_ExactStrategy 测试精确授权策略的 GetApproveResult。
func TestApproveChecker_GetApproveResult_ExactStrategy(t *testing.T) {
	amountIn := big.NewInt(1000000)

	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return big.NewInt(0)
		},
		true,
	)

	ctx := context.Background()
	result, err := checker.GetApproveResult(ctx, "0xToken", "0xOwner", "0xSpender", amountIn)
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if !result.Needed {
		t.Error("allowance 不足时 Needed 应为 true")
	}
	if result.Strategy != "exact" {
		t.Errorf("期望 Strategy=exact, 实际=%s", result.Strategy)
	}
	if result.Tx == nil {
		t.Fatal("需要 approve 时 Tx 不应为 nil")
	}
}

// TestApproveChecker_GetApproveResult_UnlimitedStrategy 测试无限授权策略的 GetApproveResult。
func TestApproveChecker_GetApproveResult_UnlimitedStrategy(t *testing.T) {
	amountIn := big.NewInt(1000000)

	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int {
			return big.NewInt(0)
		},
		false,
	)

	ctx := context.Background()
	result, err := checker.GetApproveResult(ctx, "0xToken", "0xOwner", "0xSpender", amountIn)
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if !result.Needed {
		t.Error("allowance 不足时 Needed 应为 true")
	}
	if result.Strategy != "unlimited" {
		t.Errorf("期望 Strategy=unlimited, 实际=%s", result.Strategy)
	}
}

// ---------------------------------------------------------------------------
// ApproveChecker — 参数校验
// ---------------------------------------------------------------------------

// TestApproveChecker_CheckAndApprove_EmptyParams 测试空参数校验。
func TestApproveChecker_CheckAndApprove_EmptyParams(t *testing.T) {
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int { return big.NewInt(0) },
		true,
	)

	ctx := context.Background()
	amountIn := big.NewInt(1000000)

	tests := []struct {
		name    string
		token   string
		owner   string
		spender string
	}{
		{"token 为空", "", "0xOwner", "0xSpender"},
		{"owner 为空", "0xToken", "", "0xSpender"},
		{"spender 为空", "0xToken", "0xOwner", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := checker.CheckAndApprove(ctx, tt.token, tt.owner, tt.spender, amountIn)
			if err == nil {
				t.Error("空参数应返回错误")
			}
		})
	}
}

// TestApproveChecker_CheckAndApprove_InvalidAmountIn 测试无效 amountIn 参数。
func TestApproveChecker_CheckAndApprove_InvalidAmountIn(t *testing.T) {
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int { return big.NewInt(0) },
		true,
	)

	ctx := context.Background()

	// nil amountIn
	_, _, err := checker.CheckAndApprove(ctx, "0xToken", "0xOwner", "0xSpender", nil)
	if err == nil {
		t.Error("nil amountIn 应返回错误")
	}

	// 零 amountIn
	_, _, err = checker.CheckAndApprove(ctx, "0xToken", "0xOwner", "0xSpender", big.NewInt(0))
	if err == nil {
		t.Error("零 amountIn 应返回错误")
	}

	// 负 amountIn
	_, _, err = checker.CheckAndApprove(ctx, "0xToken", "0xOwner", "0xSpender", big.NewInt(-1))
	if err == nil {
		t.Error("负 amountIn 应返回错误")
	}
}

// TestApproveChecker_CheckAndApprove_ContextCanceled 测试上下文取消。
func TestApproveChecker_CheckAndApprove_ContextCanceled(t *testing.T) {
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int { return big.NewInt(0) },
		true,
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	_, _, err := checker.CheckAndApprove(ctx, "0xToken", "0xOwner", "0xSpender", big.NewInt(1000))
	if err == nil {
		t.Error("上下文取消后应返回错误")
	}
}

// ---------------------------------------------------------------------------
// ApproveTx — Calldata 和 Gas 参数
// ---------------------------------------------------------------------------

// TestBuildApproveTx_CalldataFormat 测试 Calldata 格式。
func TestBuildApproveTx_CalldataFormat(t *testing.T) {
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int { return big.NewInt(0) },
		false,
	)

	tx := checker.BuildApproveTx("0xToken", "0xSpender", big.NewInt(1000))

	// Calldata 应以 "0x" 开头
	if !strings.HasPrefix(tx.Calldata, "0x") {
		t.Error("Calldata 应以 '0x' 开头")
	}

	// Calldata 去掉 0x 后应包含 selector(8 hex) + spender(64 hex) + amount(64 hex) = 136 hex chars
	calldataBody := tx.Calldata[2:]
	if len(calldataBody) != 136 {
		t.Errorf("Calldata body 长度期望=136, 实际=%d", len(calldataBody))
	}

	// 前 8 个字符应是 approve selector
	selector := calldataBody[:8]
	expectedSelector := ApproveSelector[2:] // 去掉 "0x"
	if selector != expectedSelector {
		t.Errorf("Calldata selector 期望=%s, 实际=%s", expectedSelector, selector)
	}
}

// TestBuildApproveTx_GasParams 测试 Gas 参数设置。
func TestBuildApproveTx_GasParams(t *testing.T) {
	checker := NewApproveChecker(
		func(token, owner, spender string) *big.Int { return big.NewInt(0) },
		true,
	)

	tx := checker.BuildApproveTx("0xToken", "0xSpender", big.NewInt(1000))

	if tx.GasLimit == 0 {
		t.Error("GasLimit 不应为 0")
	}
	if tx.BaseFee == nil || tx.BaseFee.Sign() <= 0 {
		t.Error("BaseFee 应为正数")
	}
	if tx.PriorityFee == nil || tx.PriorityFee.Sign() <= 0 {
		t.Error("PriorityFee 应为正数")
	}
}

// ---------------------------------------------------------------------------
// ABI 常量
// ---------------------------------------------------------------------------

// TestABISelectors 测试 ABI function selector 常量。
func TestABISelectors(t *testing.T) {
	if ApproveSelector != "0x095ea7b3" {
		t.Errorf("ApproveSelector 期望=0x095ea7b3, 实际=%s", ApproveSelector)
	}
	if AllowanceSelector != "0xdd62ed3e" {
		t.Errorf("AllowanceSelector 期望=0xdd62ed3e, 实际=%s", AllowanceSelector)
	}
}
