package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// DexErrorCollector 单元测试
// ============================================================

// ----- AddError 按优先级分组 -----

// TestErrorCollector_AddErrorGroupsByPriority 验证 AddError 按优先级正确分组。
func TestErrorCollector_AddErrorGroupsByPriority(t *testing.T) {
	ec := NewDexErrorCollector(5)

	// 添加高优先级错误
	ec.AddError(dexwallet.DexPumpFun, "PumpFun", dexwallet.PriorityHigh,
		&dexwallet.InsufficientBalanceError{Token: "SOL", Required: "1000", Available: "500"})

	// 添加中优先级错误
	ec.AddError(dexwallet.DexRaydiumAMM, "Raydium", dexwallet.PriorityMedium,
		&dexwallet.SlippageExceededError{Actual: 500, Threshold: 200})

	// 添加低优先级错误
	ec.AddError(dexwallet.DexJupiter, "Jupiter", dexwallet.PriorityLow,
		fmt.Errorf("network timeout"))

	if len(ec.HighPriorityErrors) != 1 {
		t.Errorf("高优先级错误数量应为 1，实际为 %d", len(ec.HighPriorityErrors))
	}
	if len(ec.MiddlePriorityErrors) != 1 {
		t.Errorf("中优先级错误数量应为 1，实际为 %d", len(ec.MiddlePriorityErrors))
	}
	if len(ec.LowPriorityErrors) != 1 {
		t.Errorf("低优先级错误数量应为 1，实际为 %d", len(ec.LowPriorityErrors))
	}

	// 验证高优先级错误的内容
	if ec.HighPriorityErrors[0].DexID != dexwallet.DexPumpFun {
		t.Errorf("高优先级错误 DexID 应为 pump_fun，实际为 %s", ec.HighPriorityErrors[0].DexID)
	}
}

// TestErrorCollector_AddErrorUnknownPriorityGoesToLow 验证未知优先级归入低优先级组。
func TestErrorCollector_AddErrorUnknownPriorityGoesToLow(t *testing.T) {
	ec := NewDexErrorCollector(3)

	// 使用未知优先级值
	ec.AddError("unknown_dex", "Unknown", dexwallet.DexPriority(99),
		fmt.Errorf("some error"))

	if len(ec.LowPriorityErrors) != 1 {
		t.Errorf("未知优先级应归入低优先级组，低优先级数量应为 1，实际为 %d", len(ec.LowPriorityErrors))
	}
}

// ----- AddNilResult 记录 -----

// TestErrorCollector_AddNilResult 验证 AddNilResult 正确记录不支持交易对的 DEX。
func TestErrorCollector_AddNilResult(t *testing.T) {
	ec := NewDexErrorCollector(5)

	ec.AddNilResult("Moonshot")
	ec.AddNilResult("Boop")

	if len(ec.NilResultDexes) != 2 {
		t.Errorf("NilResult 数量应为 2，实际为 %d", len(ec.NilResultDexes))
	}
	if ec.NilResultDexes[0] != "Moonshot" {
		t.Errorf("第一个 NilResult 应为 Moonshot，实际为 %s", ec.NilResultDexes[0])
	}
	if ec.NilResultDexes[1] != "Boop" {
		t.Errorf("第二个 NilResult 应为 Boop，实际为 %s", ec.NilResultDexes[1])
	}
}

// ----- GetAllErrors 排序 -----

// TestErrorCollector_GetAllErrorsOrdering 验证 GetAllErrors 按 高->中->低 排序返回。
func TestErrorCollector_GetAllErrorsOrdering(t *testing.T) {
	ec := NewDexErrorCollector(6)

	// 故意乱序添加
	ec.AddError(dexwallet.DexJupiter, "Jupiter", dexwallet.PriorityLow,
		fmt.Errorf("low error"))
	ec.AddError(dexwallet.DexPumpFun, "PumpFun", dexwallet.PriorityHigh,
		fmt.Errorf("high error"))
	ec.AddError(dexwallet.DexRaydiumAMM, "Raydium", dexwallet.PriorityMedium,
		fmt.Errorf("medium error"))

	all := ec.GetAllErrors()

	if len(all) != 3 {
		t.Fatalf("错误总数应为 3，实际为 %d", len(all))
	}

	// 第一个应该是高优先级
	if all[0].Priority != dexwallet.PriorityHigh {
		t.Errorf("第一个错误应为高优先级，实际优先级为 %d", all[0].Priority)
	}
	// 第二个应该是中优先级
	if all[1].Priority != dexwallet.PriorityMedium {
		t.Errorf("第二个错误应为中优先级，实际优先级为 %d", all[1].Priority)
	}
	// 第三个应该是低优先级
	if all[2].Priority != dexwallet.PriorityLow {
		t.Errorf("第三个错误应为低优先级，实际优先级为 %d", all[2].Priority)
	}
}

// TestErrorCollector_GetAllErrorsEmpty 验证空收集器返回空列表。
func TestErrorCollector_GetAllErrorsEmpty(t *testing.T) {
	ec := NewDexErrorCollector(3)

	all := ec.GetAllErrors()
	if len(all) != 0 {
		t.Errorf("空收集器应返回空列表，实际长度为 %d", len(all))
	}
}

// ----- AnalyzeAndReturnError 业务错误优先策略 -----

// TestErrorCollector_AnalyzeReturnsNilWhenEmpty 验证无错误时返回 nil。
func TestErrorCollector_AnalyzeReturnsNilWhenEmpty(t *testing.T) {
	ec := NewDexErrorCollector(3)

	err := ec.AnalyzeAndReturnError()
	if err != nil {
		t.Errorf("无错误时应返回 nil，实际为 %v", err)
	}
}

// TestErrorCollector_AnalyzePrefersInsufficientBalance 验证余额不足错误优先级最高。
func TestErrorCollector_AnalyzePrefersInsufficientBalance(t *testing.T) {
	ec := NewDexErrorCollector(5)

	// 添加多种业务错误（故意把 InsufficientBalance 放在后面）
	ec.AddError(dexwallet.DexRaydiumAMM, "Raydium", dexwallet.PriorityMedium,
		&dexwallet.SlippageExceededError{Actual: 500, Threshold: 200})
	ec.AddError(dexwallet.DexJupiter, "Jupiter", dexwallet.PriorityLow,
		&dexwallet.PoolNotFoundError{BaseMint: "SOL", QuoteMint: "MEME"})
	ec.AddError(dexwallet.DexPumpFun, "PumpFun", dexwallet.PriorityHigh,
		&dexwallet.InsufficientBalanceError{Token: "SOL", Required: "1000", Available: "500"})

	err := ec.AnalyzeAndReturnError()
	if err == nil {
		t.Fatal("应返回错误")
	}

	var insuffErr *dexwallet.InsufficientBalanceError
	if !errors.As(err, &insuffErr) {
		t.Errorf("应优先返回 InsufficientBalanceError，实际类型为 %T: %v", err, err)
	}
}

// TestErrorCollector_AnalyzePrefersSlippageOverPoolNotFound 验证滑点错误优先于池子不存在。
func TestErrorCollector_AnalyzePrefersSlippageOverPoolNotFound(t *testing.T) {
	ec := NewDexErrorCollector(3)

	ec.AddError(dexwallet.DexJupiter, "Jupiter", dexwallet.PriorityLow,
		&dexwallet.PoolNotFoundError{BaseMint: "SOL", QuoteMint: "MEME"})
	ec.AddError(dexwallet.DexRaydiumAMM, "Raydium", dexwallet.PriorityMedium,
		&dexwallet.SlippageExceededError{Actual: 500, Threshold: 200})

	err := ec.AnalyzeAndReturnError()
	if err == nil {
		t.Fatal("应返回错误")
	}

	var slipErr *dexwallet.SlippageExceededError
	if !errors.As(err, &slipErr) {
		t.Errorf("应优先返回 SlippageExceededError，实际类型为 %T: %v", err, err)
	}
}

// TestErrorCollector_AnalyzeReturnsPoolNotFound 验证只有 PoolNotFound 时正确返回。
func TestErrorCollector_AnalyzeReturnsPoolNotFound(t *testing.T) {
	ec := NewDexErrorCollector(3)

	ec.AddError(dexwallet.DexRaydiumAMM, "Raydium", dexwallet.PriorityMedium,
		&dexwallet.PoolNotFoundError{BaseMint: "SOL", QuoteMint: "RARE"})
	ec.AddError(dexwallet.DexJupiter, "Jupiter", dexwallet.PriorityLow,
		fmt.Errorf("network timeout"))

	err := ec.AnalyzeAndReturnError()
	if err == nil {
		t.Fatal("应返回错误")
	}

	var poolErr *dexwallet.PoolNotFoundError
	if !errors.As(err, &poolErr) {
		t.Errorf("应返回 PoolNotFoundError，实际类型为 %T: %v", err, err)
	}
}

// TestErrorCollector_AnalyzeReturnsAllDexFailedWhenNoBizError 验证无业务错误时返回 AllDexFailedError。
func TestErrorCollector_AnalyzeReturnsAllDexFailedWhenNoBizError(t *testing.T) {
	ec := NewDexErrorCollector(3)

	ec.AddError(dexwallet.DexPumpFun, "PumpFun", dexwallet.PriorityHigh,
		fmt.Errorf("network timeout"))
	ec.AddError(dexwallet.DexRaydiumAMM, "Raydium", dexwallet.PriorityMedium,
		fmt.Errorf("rpc error"))

	err := ec.AnalyzeAndReturnError()
	if err == nil {
		t.Fatal("应返回错误")
	}

	var allFailedErr *dexwallet.AllDexFailedError
	if !errors.As(err, &allFailedErr) {
		t.Errorf("无业务错误时应返回 AllDexFailedError，实际类型为 %T: %v", err, err)
	}
}

// TestErrorCollector_AnalyzeWithOnlyNilResults 验证只有 nil 结果时返回 AllDexFailedError。
func TestErrorCollector_AnalyzeWithOnlyNilResults(t *testing.T) {
	ec := NewDexErrorCollector(2)

	ec.AddNilResult("Moonshot")

	err := ec.AnalyzeAndReturnError()
	if err == nil {
		t.Fatal("有 NilResult 时应返回错误")
	}

	var allFailedErr *dexwallet.AllDexFailedError
	if !errors.As(err, &allFailedErr) {
		t.Errorf("应返回 AllDexFailedError，实际类型为 %T: %v", err, err)
	}
}

// ----- FormatSummary 输出格式 -----

// TestErrorCollector_FormatSummaryBasic 验证 FormatSummary 的基本输出格式。
func TestErrorCollector_FormatSummaryBasic(t *testing.T) {
	ec := NewDexErrorCollector(5)

	ec.AddError(dexwallet.DexPumpFun, "PumpFun", dexwallet.PriorityHigh,
		&dexwallet.InsufficientBalanceError{Token: "SOL", Required: "1000", Available: "500"})
	ec.AddError(dexwallet.DexRaydiumAMM, "Raydium", dexwallet.PriorityMedium,
		&dexwallet.SlippageExceededError{Actual: 500, Threshold: 200})
	ec.AddError(dexwallet.DexJupiter, "Jupiter", dexwallet.PriorityLow,
		fmt.Errorf("network timeout"))
	ec.AddNilResult("Moonshot")

	summary := ec.FormatSummary()

	// 验证包含关键内容
	if !strings.Contains(summary, "all dex(5) failed:") {
		t.Errorf("摘要应包含 'all dex(5) failed:'，实际为: %s", summary)
	}
	if !strings.Contains(summary, "[High: 1 errors]") {
		t.Errorf("摘要应包含 '[High: 1 errors]'，实际为: %s", summary)
	}
	if !strings.Contains(summary, "PumpFun: InsufficientBalance") {
		t.Errorf("摘要应包含 'PumpFun: InsufficientBalance'，实际为: %s", summary)
	}
	if !strings.Contains(summary, "[Middle: 1 errors]") {
		t.Errorf("摘要应包含 '[Middle: 1 errors]'，实际为: %s", summary)
	}
	if !strings.Contains(summary, "Raydium: Slippage") {
		t.Errorf("摘要应包含 'Raydium: Slippage'，实际为: %s", summary)
	}
	if !strings.Contains(summary, "[Low: 1 errors]") {
		t.Errorf("摘要应包含 '[Low: 1 errors]'，实际为: %s", summary)
	}
	if !strings.Contains(summary, "[不支持交易对: Moonshot]") {
		t.Errorf("摘要应包含 '[不支持交易对: Moonshot]'，实际为: %s", summary)
	}
}

// TestErrorCollector_FormatSummaryEmpty 验证空收集器的摘要格式。
func TestErrorCollector_FormatSummaryEmpty(t *testing.T) {
	ec := NewDexErrorCollector(3)

	summary := ec.FormatSummary()
	if !strings.Contains(summary, "all dex(3) failed:") {
		t.Errorf("空收集器摘要应包含 dex 总数，实际为: %s", summary)
	}
}

// TestErrorCollector_FormatSummaryMultipleNilResults 验证多个 NilResult 的格式。
func TestErrorCollector_FormatSummaryMultipleNilResults(t *testing.T) {
	ec := NewDexErrorCollector(4)

	ec.AddNilResult("Moonshot")
	ec.AddNilResult("Boop")

	summary := ec.FormatSummary()
	if !strings.Contains(summary, "[不支持交易对: Moonshot, Boop]") {
		t.Errorf("摘要应包含 '[不支持交易对: Moonshot, Boop]'，实际为: %s", summary)
	}
}

// ----- 并发安全 -----

// TestErrorCollector_ConcurrentAddError 验证并发 AddError 不会 panic 或数据竞争。
func TestErrorCollector_ConcurrentAddError(t *testing.T) {
	ec := NewDexErrorCollector(100)

	var wg sync.WaitGroup
	concurrency := 100

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// 交替添加不同优先级的错误
			switch idx % 3 {
			case 0:
				ec.AddError(dexwallet.DexPumpFun, "PumpFun", dexwallet.PriorityHigh,
					fmt.Errorf("error %d", idx))
			case 1:
				ec.AddError(dexwallet.DexRaydiumAMM, "Raydium", dexwallet.PriorityMedium,
					fmt.Errorf("error %d", idx))
			case 2:
				ec.AddError(dexwallet.DexJupiter, "Jupiter", dexwallet.PriorityLow,
					fmt.Errorf("error %d", idx))
			}
		}(i)
	}

	wg.Wait()

	// 验证总数正确
	all := ec.GetAllErrors()
	if len(all) != concurrency {
		t.Errorf("并发添加后错误总数应为 %d，实际为 %d", concurrency, len(all))
	}

	// 验证各优先级组的数量之和等于总数
	total := len(ec.HighPriorityErrors) + len(ec.MiddlePriorityErrors) + len(ec.LowPriorityErrors)
	if total != concurrency {
		t.Errorf("各优先级组数量之和应为 %d，实际为 %d", concurrency, total)
	}
}

// TestErrorCollector_ConcurrentMixedOperations 验证并发混合操作的安全性。
func TestErrorCollector_ConcurrentMixedOperations(t *testing.T) {
	ec := NewDexErrorCollector(50)

	var wg sync.WaitGroup

	// 并发 AddError
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ec.AddError(dexwallet.DexPumpFun, "PumpFun", dexwallet.PriorityHigh,
				fmt.Errorf("error %d", idx))
		}(i)
	}

	// 并发 AddNilResult
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ec.AddNilResult(fmt.Sprintf("dex_%d", idx))
		}(i)
	}

	// 并发 GetAllErrors
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ec.GetAllErrors()
		}()
	}

	// 并发 FormatSummary
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ec.FormatSummary()
		}()
	}

	// 并发 AnalyzeAndReturnError
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ec.AnalyzeAndReturnError()
		}()
	}

	// 不应该 panic
	wg.Wait()
}
