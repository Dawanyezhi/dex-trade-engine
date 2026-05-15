package main

// ============================================================
// DexErrorCollector -- DEX 错误收集与分析系统
// ============================================================
//
// 对标 solwallet 的 dexerrorcollector.go，实现按优先级分组收集所有 DEX 错误，
// 并提供智能错误分析。
//
// 设计目标：
//   1. 按优先级分组收集（便于诊断：高优先级 DEX 错误最重要，因为它们是首选路径）
//   2. 业务错误优先策略（用户友好的错误信息 vs 技术错误）
//   3. 格式化错误摘要（用于日志和监控告警）
//
// 与 dexwallet 通用层 errors.go 的关系：
//   dexwallet/errors.go 定义了错误类型（InsufficientBalanceError, SlippageExceededError 等）。
//   本收集器使用 errors.As 识别这些错误类型，将技术错误分类为业务错误，
//   从而在最终返回给用户时，提供更有意义的错误信息（如"余额不足"而非"network timeout"）。
//
// 生产场景：
//   接入告警系统 — 当高优先级 DEX 连续失败时触发告警。
//   例如 Pump 内盘或 Raydium CPMM 连续报错，可能意味着链上拥堵或合约异常，
//   需要运维介入。FormatSummary 的输出可直接作为告警内容发送到 Slack/PagerDuty。

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ----- DexError 单个 DEX 的错误记录 -----

// DexError 记录单个 DEX 在报价或交易构建中产生的错误。
// 包含 DEX 标识、人类可读的标签、优先级和原始错误。
type DexError struct {
	DexID    dexwallet.DexID       // DEX 唯一标识（如 "pump_fun"）
	DexLabel string                // DEX 人类可读名称（用于日志展示）
	Priority dexwallet.DexPriority // DEX 优先级（高/中/低）
	Error    error                 // 原始错误
}

// ----- DexErrorCollector 错误收集器 -----

// DexErrorCollector 按优先级分组收集所有 DEX 的错误，并提供智能分析能力。
//
// 为什么按优先级分组收集：
//   在多 DEX 并发竞争的场景下，不同优先级的 DEX 错误重要性不同。
//   高优先级 DEX（如 Pump 内盘）的错误最值得关注，因为它们是首选路径。
//   如果高优先级全部失败，降级到中/低优先级，此时高优先级的错误信息
//   对于诊断问题根因至关重要。
//
//   分组收集还便于生成结构化告警：
//   "[High: 2 errors] pump_fun: InsufficientBalance; moonshot: Timeout"
//   运维一眼就能判断问题出在哪个层级。
type DexErrorCollector struct {
	mu                   sync.Mutex // 保护并发写入（多个 goroutine 同时 AddError）
	HighPriorityErrors   []DexError // 高优先级 DEX 的错误（内盘，如 Pump）
	MiddlePriorityErrors []DexError // 中优先级 DEX 的错误（AMM/CLMM，如 Raydium）
	LowPriorityErrors    []DexError // 低优先级 DEX 的错误（聚合器，如 Jupiter）
	NilResultDexes       []string   // 返回 nil 的 DEX（不支持该交易对）
	TotalDexCount        int        // 参与竞争的 DEX 总数
}

// NewDexErrorCollector 创建一个新的错误收集器。
// totalCount 是参与竞争的 DEX 总数，用于 FormatSummary 中展示全貌。
func NewDexErrorCollector(totalCount int) *DexErrorCollector {
	return &DexErrorCollector{
		HighPriorityErrors:   make([]DexError, 0),
		MiddlePriorityErrors: make([]DexError, 0),
		LowPriorityErrors:    make([]DexError, 0),
		NilResultDexes:       make([]string, 0),
		TotalDexCount:        totalCount,
	}
}

// AddError 按优先级分组收集一个 DEX 的错误。
//
// 根据 priority 将错误分配到对应的分组（高/中/低），
// 便于后续按重要性排序分析。
func (c *DexErrorCollector) AddError(dexID dexwallet.DexID, label string, priority dexwallet.DexPriority, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	de := DexError{
		DexID:    dexID,
		DexLabel: label,
		Priority: priority,
		Error:    err,
	}

	switch priority {
	case dexwallet.PriorityHigh:
		c.HighPriorityErrors = append(c.HighPriorityErrors, de)
	case dexwallet.PriorityMedium:
		c.MiddlePriorityErrors = append(c.MiddlePriorityErrors, de)
	case dexwallet.PriorityLow:
		c.LowPriorityErrors = append(c.LowPriorityErrors, de)
	default:
		// 未知优先级归入低优先级组
		c.LowPriorityErrors = append(c.LowPriorityErrors, de)
	}
}

// AddNilResult 记录返回 nil 结果的 DEX（不支持该交易对）。
//
// 有些 DEX 对特定交易对返回 nil（而非错误），表示该 DEX 不支持此交易对。
// 这不算错误，但需要记录，便于在摘要中展示完整信息。
func (c *DexErrorCollector) AddNilResult(label string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.NilResultDexes = append(c.NilResultDexes, label)
}

// GetAllErrors 返回所有错误，按优先级排序：高 -> 中 -> 低。
//
// 高优先级错误排在前面，因为它们最有诊断价值。
// 调用方可以遍历此列表做自定义分析或上报。
func (c *DexErrorCollector) GetAllErrors() []DexError {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getAllErrorsLocked()
}

// getAllErrorsLocked 内部方法，调用方需持有锁。
func (c *DexErrorCollector) getAllErrorsLocked() []DexError {
	all := make([]DexError, 0, len(c.HighPriorityErrors)+len(c.MiddlePriorityErrors)+len(c.LowPriorityErrors))
	all = append(all, c.HighPriorityErrors...)
	all = append(all, c.MiddlePriorityErrors...)
	all = append(all, c.LowPriorityErrors...)
	return all
}

// AnalyzeAndReturnError 智能分析所有收集到的错误，返回最合适的错误给调用方。
//
// 分析策略：
//   1. 如果没有任何错误且没有 nil 结果，返回 nil（不应该发生，防御性编程）
//   2. 优先查找业务错误（InsufficientBalanceError > SlippageExceededError > PoolNotFoundError）
//      业务错误对用户有明确意义，比技术错误更有价值
//   3. 如果没有业务错误，返回 AllDexFailedError 并附带格式化摘要
//
// 为什么业务错误优先：
//   当用户发起一笔交易，8 个 DEX 中 7 个返回 "network timeout"，
//   1 个返回 "余额不足"。此时用户真正需要知道的是"余额不足"，
//   因为即使网络恢复，这笔交易也不会成功。
//   业务错误能帮助用户采取正确的行动（如充值），而技术错误只会让用户困惑。
func (c *DexErrorCollector) AnalyzeAndReturnError() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	allErrors := c.getAllErrorsLocked()

	// 没有任何错误记录
	if len(allErrors) == 0 && len(c.NilResultDexes) == 0 {
		return nil
	}

	// 优先查找业务错误
	if bizErr := c.findFirstBusinessErrorLocked(); bizErr != nil {
		return bizErr
	}

	// 没有业务错误，返回 AllDexFailedError 并附带摘要
	return &dexwallet.AllDexFailedError{
		Chain: "solana",
		Count: c.TotalDexCount,
	}
}

// findFirstBusinessError 在所有错误中查找第一个业务错误。
//
// 业务错误优先级：InsufficientBalanceError > SlippageExceededError > PoolNotFoundError
//
// 原因：
//   - InsufficientBalanceError 最优先：余额不足是最明确的用户可操作错误，
//     用户需要充值后才能重试，换 DEX 也无法解决。
//   - SlippageExceededError 次之：滑点超限说明市场波动大或金额过大，
//     用户可以调整滑点容忍度或减小交易金额。
//   - PoolNotFoundError 最后：池子不存在可能是暂时的（池子刚创建还未被索引），
//     也可能是永久的（确实不支持该交易对）。
//
// 使用 errors.As 识别错误类型，与 dexwallet/errors.go 中定义的错误类型对应。
// 这样即使错误被 DegradableError/NonDegradableError 包装过，也能正确识别。
func (c *DexErrorCollector) findFirstBusinessErrorLocked() error {
	allErrors := c.getAllErrorsLocked()

	// 第一轮：查找 InsufficientBalanceError（最高优先级业务错误）
	for _, de := range allErrors {
		var insuffErr *dexwallet.InsufficientBalanceError
		if errors.As(de.Error, &insuffErr) {
			return insuffErr
		}
	}

	// 第二轮：查找 SlippageExceededError
	for _, de := range allErrors {
		var slipErr *dexwallet.SlippageExceededError
		if errors.As(de.Error, &slipErr) {
			return slipErr
		}
	}

	// 第三轮：查找 PoolNotFoundError
	for _, de := range allErrors {
		var poolErr *dexwallet.PoolNotFoundError
		if errors.As(de.Error, &poolErr) {
			return poolErr
		}
	}

	// 没有找到任何业务错误
	return nil
}

// FormatSummary 格式化错误摘要，用于日志和监控告警。
//
// 输出格式示例：
//
//	all dex(8) failed: [High: 2 errors] pump_fun: InsufficientBalance; moonshot: Timeout;
//	[Middle: 3 errors] raydium_amm: Slippage; ...; [Low: 1 errors] jupiter: Timeout;
//	[不支持交易对: moonshot, boop]
//
// 该输出可直接作为告警内容发送到 Slack/PagerDuty 等告警系统。
// 运维人员能一眼看出：哪个优先级层级出了什么问题、有多少 DEX 不支持该交易对。
func (c *DexErrorCollector) FormatSummary() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("all dex(%d) failed: ", c.TotalDexCount))

	// 格式化高优先级错误
	if len(c.HighPriorityErrors) > 0 {
		sb.WriteString(formatPriorityGroup("High", c.HighPriorityErrors))
	}

	// 格式化中优先级错误
	if len(c.MiddlePriorityErrors) > 0 {
		sb.WriteString(formatPriorityGroup("Middle", c.MiddlePriorityErrors))
	}

	// 格式化低优先级错误
	if len(c.LowPriorityErrors) > 0 {
		sb.WriteString(formatPriorityGroup("Low", c.LowPriorityErrors))
	}

	// 格式化不支持交易对的 DEX
	if len(c.NilResultDexes) > 0 {
		sb.WriteString(fmt.Sprintf("[不支持交易对: %s]", strings.Join(c.NilResultDexes, ", ")))
	}

	return sb.String()
}

// formatPriorityGroup 格式化某个优先级组的错误列表。
// 输出格式：[High: 2 errors] pump_fun: InsufficientBalance; moonshot: Timeout;
func formatPriorityGroup(level string, errs []DexError) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s: %d errors] ", level, len(errs)))

	for _, de := range errs {
		sb.WriteString(fmt.Sprintf("%s: %s; ", de.DexLabel, shortenError(de.Error)))
	}

	return sb.String()
}

// shortenError 将错误信息缩短为简洁的类型名称，用于摘要展示。
//
// 在摘要中使用完整错误信息太冗长，例如：
//   "insufficient SOL balance: need 1000000, have 500000"
// 缩短为：
//   "InsufficientBalance"
// 便于快速扫描和告警匹配。
func shortenError(err error) string {
	if err == nil {
		return "nil"
	}

	var insuffErr *dexwallet.InsufficientBalanceError
	if errors.As(err, &insuffErr) {
		return "InsufficientBalance"
	}

	var slipErr *dexwallet.SlippageExceededError
	if errors.As(err, &slipErr) {
		return "Slippage"
	}

	var poolErr *dexwallet.PoolNotFoundError
	if errors.As(err, &poolErr) {
		return "PoolNotFound"
	}

	var degradErr *dexwallet.DegradableError
	if errors.As(err, &degradErr) {
		return degradErr.Reason
	}

	var nonDegradErr *dexwallet.NonDegradableError
	if errors.As(err, &nonDegradErr) {
		return nonDegradErr.Reason
	}

	// 兜底：截取原始错误信息前 50 个字符
	msg := err.Error()
	if len(msg) > 50 {
		return msg[:50] + "..."
	}
	return msg
}

// LogHigherPriorityWarnings 当最终选中的 DEX 来自低优先级层级时，
// 生成一个警告信息，说明有哪些高优先级 DEX 失败了。
//
// 使用场景：
//   PrioritySelector 最终从 Jupiter（低优先级聚合器）获得了结果，
//   但之前 Pump 和 Raydium（高优先级）都失败了。
//   此时需要记录警告日志，便于排查为什么高优先级 DEX 不可用。
//
// 参数 selectedPriority：最终选中结果的优先级。
// 返回：比 selectedPriority 更高优先级的错误列表的格式化字符串。
// 如果没有更高优先级的错误，返回空字符串。
func (c *DexErrorCollector) LogHigherPriorityWarnings(selectedPriority dexwallet.DexPriority) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	var warnings []string

	// 如果选中的是中优先级，警告高优先级的错误
	// 如果选中的是低优先级，警告高优先级和中优先级的错误
	// 如果选中的是高优先级，不需要警告
	if selectedPriority >= dexwallet.PriorityMedium && len(c.HighPriorityErrors) > 0 {
		for _, de := range c.HighPriorityErrors {
			warnings = append(warnings, fmt.Sprintf("[High] %s: %s", de.DexLabel, shortenError(de.Error)))
		}
	}

	if selectedPriority >= dexwallet.PriorityLow && len(c.MiddlePriorityErrors) > 0 {
		for _, de := range c.MiddlePriorityErrors {
			warnings = append(warnings, fmt.Sprintf("[Middle] %s: %s", de.DexLabel, shortenError(de.Error)))
		}
	}

	if len(warnings) == 0 {
		return ""
	}

	return fmt.Sprintf("已降级到优先级 %d，以下高优先级 DEX 失败: %s",
		selectedPriority, strings.Join(warnings, "; "))
}
