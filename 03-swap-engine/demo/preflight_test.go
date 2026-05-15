package main

import (
	"testing"
)

// ============================================================
// Preflight 校验测试
// ============================================================

// validPreflightRequest 返回一个全部字段合法的 SwapPreflightRequest，用于测试基准。
func validPreflightRequest() *SwapPreflightRequest {
	return &SwapPreflightRequest{
		OrderID:      100001,
		UID:          10000,
		SellContract: "So11111111111111111111111111111111111111112",
		BuyContract:  "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		SellAmount:   "1000000000",
		PriorityFee:  "5000",
		FeeRate:      "30",
		Slippage:     200,
	}
}

// ---------------------------------------------------------------------------
// 全部检查通过
// ---------------------------------------------------------------------------

// TestPreflight_AllPass 测试全部 8 项校验通过的情况。
func TestPreflight_AllPass(t *testing.T) {
	pf := NewSwapPreflight()
	req := validPreflightRequest()

	result := pf.Check(req)
	if !result.IsOK() {
		t.Fatalf("期望全部校验通过，但失败了: Code=%d, Message=%s", result.Code, result.Message)
	}
	if result.Code != PreflightOK {
		t.Errorf("期望 Code=0 (PreflightOK), 实际=%d", result.Code)
	}
}

// TestPreflight_AllPassOptionalFieldsEmpty 测试可选字段为空时全部校验通过。
func TestPreflight_AllPassOptionalFieldsEmpty(t *testing.T) {
	pf := NewSwapPreflight()
	req := validPreflightRequest()
	req.PriorityFee = "" // 可选字段为空，跳过校验
	req.FeeRate = ""     // 可选字段为空，跳过校验

	result := pf.Check(req)
	if !result.IsOK() {
		t.Fatalf("可选字段为空时期望通过，但失败了: Code=%d, Message=%s", result.Code, result.Message)
	}
}

// ---------------------------------------------------------------------------
// 各项检查失败
// ---------------------------------------------------------------------------

// TestPreflight_InvalidOrderID 测试 OrderID 不合法的情况。
func TestPreflight_InvalidOrderID(t *testing.T) {
	pf := NewSwapPreflight()

	tests := []struct {
		name    string
		orderID int64
	}{
		{"零 OrderID", 0},
		{"负 OrderID", -1},
		{"极小负值", -999999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validPreflightRequest()
			req.OrderID = tt.orderID

			result := pf.Check(req)
			if result.IsOK() {
				t.Errorf("OrderID=%d 期望被拒绝", tt.orderID)
			}
			if result.Code != PreflightInvalidOrderID {
				t.Errorf("期望 Code=%d (PreflightInvalidOrderID), 实际=%d", PreflightInvalidOrderID, result.Code)
			}
		})
	}
}

// TestPreflight_InvalidUID 测试 UID 不合法的情况。
func TestPreflight_InvalidUID(t *testing.T) {
	pf := NewSwapPreflight()

	tests := []struct {
		name string
		uid  int64
	}{
		{"低于最小 UID", 9999},
		{"零 UID", 0},
		{"负 UID", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validPreflightRequest()
			req.UID = tt.uid

			result := pf.Check(req)
			if result.IsOK() {
				t.Errorf("UID=%d 期望被拒绝", tt.uid)
			}
			if result.Code != PreflightInvalidUserID {
				t.Errorf("期望 Code=%d (PreflightInvalidUserID), 实际=%d", PreflightInvalidUserID, result.Code)
			}
		})
	}
}

// TestPreflight_InvalidSellAmount 测试 SellAmount 不合法的情况。
func TestPreflight_InvalidSellAmount(t *testing.T) {
	pf := NewSwapPreflight()

	tests := []struct {
		name       string
		sellAmount string
	}{
		{"空字符串", ""},
		{"零金额", "0"},
		{"负金额", "-100"},
		{"非数字", "abc"},
		{"浮点数", "1.5"},
		{"只有空格", "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validPreflightRequest()
			req.SellAmount = tt.sellAmount

			result := pf.Check(req)
			if result.IsOK() {
				t.Errorf("SellAmount=%q 期望被拒绝", tt.sellAmount)
			}
			if result.Code != PreflightInvalidAmount {
				t.Errorf("期望 Code=%d (PreflightInvalidAmount), 实际=%d", PreflightInvalidAmount, result.Code)
			}
		})
	}
}

// TestPreflight_InvalidPriorityFee 测试 PriorityFee 不合法的情况。
func TestPreflight_InvalidPriorityFee(t *testing.T) {
	pf := NewSwapPreflight()

	tests := []struct {
		name        string
		priorityFee string
	}{
		{"负数", "-100"},
		{"非数字", "abc"},
		{"浮点数", "1.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validPreflightRequest()
			req.PriorityFee = tt.priorityFee

			result := pf.Check(req)
			if result.IsOK() {
				t.Errorf("PriorityFee=%q 期望被拒绝", tt.priorityFee)
			}
			if result.Code != PreflightInvalidFee {
				t.Errorf("期望 Code=%d (PreflightInvalidFee), 实际=%d", PreflightInvalidFee, result.Code)
			}
		})
	}
}

// TestPreflight_InvalidFeeRate 测试 FeeRate 不合法的情况。
func TestPreflight_InvalidFeeRate(t *testing.T) {
	pf := NewSwapPreflight()

	tests := []struct {
		name    string
		feeRate string
	}{
		{"负数", "-1"},
		{"非数字", "xyz"},
		{"浮点数", "0.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validPreflightRequest()
			req.FeeRate = tt.feeRate

			result := pf.Check(req)
			if result.IsOK() {
				t.Errorf("FeeRate=%q 期望被拒绝", tt.feeRate)
			}
			if result.Code != PreflightInvalidFeeRate {
				t.Errorf("期望 Code=%d (PreflightInvalidFeeRate), 实际=%d", PreflightInvalidFeeRate, result.Code)
			}
		})
	}
}

// TestPreflight_InvalidSlippage 测试滑点不合法的情况。
func TestPreflight_InvalidSlippage(t *testing.T) {
	pf := NewSwapPreflight()

	tests := []struct {
		name     string
		slippage uint64
		wantCode PreflightRejectCode
	}{
		{"零滑点", 0, PreflightInvalidSlippage},
		{"低于最小值 (9 BPS)", 9, PreflightInvalidSlippage},
		{"低于最小值 (1 BPS)", 1, PreflightInvalidSlippage},
		{"超出最大值 (5001 BPS)", 5001, PreflightSlippageOverflow},
		{"超出最大值 (10000 BPS)", 10000, PreflightSlippageOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validPreflightRequest()
			req.Slippage = tt.slippage

			result := pf.Check(req)
			if result.IsOK() {
				t.Errorf("Slippage=%d 期望被拒绝", tt.slippage)
			}
			if result.Code != tt.wantCode {
				t.Errorf("期望 Code=%d, 实际=%d", tt.wantCode, result.Code)
			}
		})
	}
}

// TestPreflight_SlippageBoundary 测试滑点边界值。
func TestPreflight_SlippageBoundary(t *testing.T) {
	pf := NewSwapPreflight()

	// 最小合法值
	req := validPreflightRequest()
	req.Slippage = 10
	result := pf.Check(req)
	if !result.IsOK() {
		t.Errorf("Slippage=10 应该通过, 但失败了: Code=%d", result.Code)
	}

	// 最大合法值
	req.Slippage = 5000
	result = pf.Check(req)
	if !result.IsOK() {
		t.Errorf("Slippage=5000 应该通过, 但失败了: Code=%d", result.Code)
	}
}

// TestPreflight_InvalidContract 测试合约地址不合法的情况。
func TestPreflight_InvalidContract(t *testing.T) {
	pf := NewSwapPreflight()

	tests := []struct {
		name         string
		sellContract string
		buyContract  string
	}{
		{"SellContract 为空", "", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"},
		{"BuyContract 为空", "So11111111111111111111111111111111111111112", ""},
		{"SellContract 太短", "short", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"},
		{"BuyContract 太短", "So11111111111111111111111111111111111111112", "short"},
		{"两个都为空", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validPreflightRequest()
			req.SellContract = tt.sellContract
			req.BuyContract = tt.buyContract

			result := pf.Check(req)
			if result.IsOK() {
				t.Errorf("SellContract=%q, BuyContract=%q 期望被拒绝", tt.sellContract, tt.buyContract)
			}
			if result.Code != PreflightInvalidContract {
				t.Errorf("期望 Code=%d (PreflightInvalidContract), 实际=%d", PreflightInvalidContract, result.Code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PreflightRejectCode 正确性
// ---------------------------------------------------------------------------

// TestPreflightRejectCode_Values 测试 PreflightRejectCode 的值域正确性。
func TestPreflightRejectCode_Values(t *testing.T) {
	tests := []struct {
		name string
		code PreflightRejectCode
		want int
	}{
		{"PreflightOK", PreflightOK, 0},
		{"PreflightInvalidOrderID", PreflightInvalidOrderID, 1001},
		{"PreflightInvalidUserID", PreflightInvalidUserID, 1002},
		{"PreflightInvalidAmount", PreflightInvalidAmount, 2001},
		{"PreflightInvalidFee", PreflightInvalidFee, 2002},
		{"PreflightInvalidFeeRate", PreflightInvalidFeeRate, 3001},
		{"PreflightInvalidSlippage", PreflightInvalidSlippage, 3001},
		{"PreflightSlippageOverflow", PreflightSlippageOverflow, 3002},
		{"PreflightInvalidContract", PreflightInvalidContract, 4001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if int(tt.code) != tt.want {
				t.Errorf("期望 %s=%d, 实际=%d", tt.name, tt.want, int(tt.code))
			}
		})
	}
}

// TestPreflightRejectCode_Segments 测试错误码分段正确性。
func TestPreflightRejectCode_Segments(t *testing.T) {
	// 段 1: 订单参数 (1000-1999)
	if PreflightInvalidOrderID < 1000 || PreflightInvalidOrderID > 1999 {
		t.Errorf("PreflightInvalidOrderID(%d) 不在段 1 (1000-1999)", PreflightInvalidOrderID)
	}
	if PreflightInvalidUserID < 1000 || PreflightInvalidUserID > 1999 {
		t.Errorf("PreflightInvalidUserID(%d) 不在段 1 (1000-1999)", PreflightInvalidUserID)
	}

	// 段 2: 金额和费用 (2000-2999)
	if PreflightInvalidAmount < 2000 || PreflightInvalidAmount > 2999 {
		t.Errorf("PreflightInvalidAmount(%d) 不在段 2 (2000-2999)", PreflightInvalidAmount)
	}
	if PreflightInvalidFee < 2000 || PreflightInvalidFee > 2999 {
		t.Errorf("PreflightInvalidFee(%d) 不在段 2 (2000-2999)", PreflightInvalidFee)
	}

	// 段 3: 比率和滑点 (3000-3999)
	if PreflightInvalidSlippage < 3000 || PreflightInvalidSlippage > 3999 {
		t.Errorf("PreflightInvalidSlippage(%d) 不在段 3 (3000-3999)", PreflightInvalidSlippage)
	}
	if PreflightSlippageOverflow < 3000 || PreflightSlippageOverflow > 3999 {
		t.Errorf("PreflightSlippageOverflow(%d) 不在段 3 (3000-3999)", PreflightSlippageOverflow)
	}

	// 段 4: 合约和 DEX (4000-4999)
	if PreflightInvalidContract < 4000 || PreflightInvalidContract > 4999 {
		t.Errorf("PreflightInvalidContract(%d) 不在段 4 (4000-4999)", PreflightInvalidContract)
	}
}

// TestPreflightResult_IsOK 测试 PreflightResult.IsOK 方法。
func TestPreflightResult_IsOK(t *testing.T) {
	okResult := PreflightResult{Code: PreflightOK, Message: "OK"}
	if !okResult.IsOK() {
		t.Error("Code=0 时 IsOK 应返回 true")
	}

	failResult := PreflightResult{Code: PreflightInvalidAmount, Message: "fail"}
	if failResult.IsOK() {
		t.Error("Code!=0 时 IsOK 应返回 false")
	}
}

// ---------------------------------------------------------------------------
// 校验顺序测试（验证先检测到的错误优先返回）
// ---------------------------------------------------------------------------

// TestPreflight_CheckOrder 测试校验顺序：OrderID 在 Amount 之前检查。
func TestPreflight_CheckOrder(t *testing.T) {
	pf := NewSwapPreflight()

	// 同时设置 OrderID 和 SellAmount 不合法，应先返回 OrderID 错误
	req := validPreflightRequest()
	req.OrderID = 0
	req.SellAmount = "0"

	result := pf.Check(req)
	if result.Code != PreflightInvalidOrderID {
		t.Errorf("期望先检测到 OrderID 错误 (Code=%d), 实际 Code=%d",
			PreflightInvalidOrderID, result.Code)
	}
}

// TestPreflight_ValidAmounts 测试合法金额能通过校验。
func TestPreflight_ValidAmounts(t *testing.T) {
	pf := NewSwapPreflight()

	validAmounts := []string{
		"1",
		"1000000000",
		"999999999999999999999999999999", // 极大值
	}

	for _, amount := range validAmounts {
		req := validPreflightRequest()
		req.SellAmount = amount

		result := pf.Check(req)
		if !result.IsOK() {
			t.Errorf("SellAmount=%q 应该通过校验, 但失败了: Code=%d, Message=%s",
				amount, result.Code, result.Message)
		}
	}
}

// TestPreflight_ValidPriorityFee 测试合法的 PriorityFee。
func TestPreflight_ValidPriorityFee(t *testing.T) {
	pf := NewSwapPreflight()

	// PriorityFee 允许为 0（非负数）
	req := validPreflightRequest()
	req.PriorityFee = "0"

	result := pf.Check(req)
	if !result.IsOK() {
		t.Errorf("PriorityFee=0 应该通过校验, 但失败了: Code=%d", result.Code)
	}
}
