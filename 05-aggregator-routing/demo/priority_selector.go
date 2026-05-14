package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// PrioritySelector -- 优先级竞争选择器
// ============================================================
//
// 对标 solwallet 中 build_swap_tx.go 的核心逻辑：
//
//   高优先级 DEX（Pump 内盘、Raydium CPMM）→ 50ms 并发竞争，最快成功返回
//       ↓ 全部超时或失败
//   中优先级 DEX（Raydium CLMM、Orca、Meteora）→ 并发竞争
//       ↓ 全部超时或失败
//   低优先级 DEX（Jupiter 聚合器）→ 兜底
//       ↓
//   快速失败规则：特定错误 → 立即返回不降级（如余额不足）
//
// 与 BaseAggregator 的区别：
//   BaseAggregator 是"所有 DEX 并发 Quote，全部返回后排序选最优"。
//   PrioritySelector 是"按优先级分层竞争，高优先级快速返回即可，不等低优先级"。
//   生产中 PrioritySelector 用于 build_swap_tx（延迟敏感），
//   BaseAggregator 用于 find_best_quote（价格敏感）。

// ----- 配置 -----

// PrioritySelectorConfig 优先级选择器配置。
type PrioritySelectorConfig struct {
	HighTierTimeout   time.Duration // 高优先级层超时（默认 50ms）
	MediumTierTimeout time.Duration // 中优先级层超时（默认 200ms）
	LowTierTimeout    time.Duration // 低优先级层超时（默认 3s）
}

// DefaultPrioritySelectorConfig 返回默认配置。
func DefaultPrioritySelectorConfig() PrioritySelectorConfig {
	return PrioritySelectorConfig{
		HighTierTimeout:   50 * time.Millisecond,
		MediumTierTimeout: 200 * time.Millisecond,
		LowTierTimeout:    3 * time.Second,
	}
}

// ----- 条目 -----

// PrioritySelectorEntry 注册到 PrioritySelector 的 DEX 条目。
type PrioritySelectorEntry struct {
	Builder  dexwallet.SwapBuilder  // Swap 构建器
	Priority dexwallet.DexPriority  // DEX 优先级
}

// ----- 统计 -----

// SelectStats 优先级选择器的运行统计。
// 用于监控和调优：哪个 DEX 胜出最多、降级频率如何。
type SelectStats struct {
	mu sync.Mutex

	// 每个 DEX 胜出的次数
	WinsByDex map[dexwallet.DexID]int64

	// 每个层级被访问的次数（高/中/低）
	TierReached map[dexwallet.DexPriority]int64

	// 不可降级错误导致的快速失败次数
	NonDegradableFails int64

	// 总调用次数
	TotalCalls int64

	// 总成功次数
	TotalSuccess int64
}

// NewSelectStats 创建空的统计实例。
func NewSelectStats() *SelectStats {
	return &SelectStats{
		WinsByDex:   make(map[dexwallet.DexID]int64),
		TierReached: make(map[dexwallet.DexPriority]int64),
	}
}

// recordWin 记录某个 DEX 的胜出。
func (s *SelectStats) recordWin(dexID dexwallet.DexID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WinsByDex[dexID]++
	s.TotalSuccess++
}

// recordTierReached 记录某个层级被访问。
func (s *SelectStats) recordTierReached(tier dexwallet.DexPriority) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TierReached[tier]++
}

// recordNonDegradableFail 记录不可降级错误。
func (s *SelectStats) recordNonDegradableFail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NonDegradableFails++
}

// recordCall 记录一次调用。
func (s *SelectStats) recordCall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalCalls++
}

// SelectStatsSnapshot 统计快照（不含 mutex，可安全拷贝）。
type SelectStatsSnapshot struct {
	WinsByDex          map[dexwallet.DexID]int64
	TierReached        map[dexwallet.DexPriority]int64
	NonDegradableFails int64
	TotalCalls         int64
	TotalSuccess       int64
}

// Snapshot 返回统计快照（线程安全）。
func (s *SelectStats) Snapshot() SelectStatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := SelectStatsSnapshot{
		TotalCalls:         s.TotalCalls,
		TotalSuccess:       s.TotalSuccess,
		NonDegradableFails: s.NonDegradableFails,
		WinsByDex:          make(map[dexwallet.DexID]int64, len(s.WinsByDex)),
		TierReached:        make(map[dexwallet.DexPriority]int64, len(s.TierReached)),
	}
	for k, v := range s.WinsByDex {
		snap.WinsByDex[k] = v
	}
	for k, v := range s.TierReached {
		snap.TierReached[k] = v
	}
	return snap
}

// ----- PrioritySelector -----

// PrioritySelector 优先级竞争选择器。
// 按层级分组运行 DEX，高优先级先跑，成功即返回，失败才降级。
type PrioritySelector struct {
	config PrioritySelectorConfig
	stats  *SelectStats

	// 按优先级分组的 DEX 条目
	tiers map[dexwallet.DexPriority][]PrioritySelectorEntry
}

// NewPrioritySelector 创建优先级选择器。
// builders: 所有参与竞争的 DEX 条目，会自动按 Priority 分组。
func NewPrioritySelector(config PrioritySelectorConfig, builders []PrioritySelectorEntry) *PrioritySelector {
	tiers := make(map[dexwallet.DexPriority][]PrioritySelectorEntry)
	for _, entry := range builders {
		tiers[entry.Priority] = append(tiers[entry.Priority], entry)
	}

	return &PrioritySelector{
		config: config,
		stats:  NewSelectStats(),
		tiers:  tiers,
	}
}

// Stats 返回统计实例（可调用 Snapshot 获取快照）。
func (ps *PrioritySelector) Stats() *SelectStats {
	return ps.stats
}

// Select 执行优先级竞争选择，返回第一个成功的 SwapResult。
//
// 流程：
//  1. 按优先级从高到低遍历层级（High → Medium → Low）
//  2. 每个层级内，所有 DEX 并发运行，取第一个成功结果
//  3. 如果某个 DEX 返回不可降级错误（如余额不足），立即终止，不再尝试下一层
//  4. 如果当前层级全部超时或返回可降级错误，继续下一层
//  5. 所有层级都失败则返回错误
func (ps *PrioritySelector) Select(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	ps.stats.recordCall()

	// 按优先级从高到低排列的层级顺序
	tierOrder := []dexwallet.DexPriority{
		dexwallet.PriorityHigh,
		dexwallet.PriorityMedium,
		dexwallet.PriorityLow,
	}

	// 层级对应的超时
	tierTimeout := map[dexwallet.DexPriority]time.Duration{
		dexwallet.PriorityHigh:   ps.config.HighTierTimeout,
		dexwallet.PriorityMedium: ps.config.MediumTierTimeout,
		dexwallet.PriorityLow:    ps.config.LowTierTimeout,
	}

	// 收集所有层级的错误，用于最终错误报告
	var allErrors []error

	for _, tier := range tierOrder {
		entries, ok := ps.tiers[tier]
		if !ok || len(entries) == 0 {
			continue // 该层级无 DEX，跳过
		}

		ps.stats.recordTierReached(tier)

		timeout := tierTimeout[tier]
		slog.Debug("开始竞争层级",
			"tier", tier,
			"timeout", timeout,
			"dex_count", len(entries),
		)

		result, err := ps.runTier(ctx, req, entries, timeout)

		// 情况 1: 成功 — 记录统计并返回
		if err == nil && result != nil {
			ps.stats.recordWin(result.DexID)
			slog.Info("优先级选择成功",
				"dex", result.DexID,
				"tier", tier,
				"output", result.OutputAmount.String(),
			)
			return result, nil
		}

		// 情况 2: 不可降级错误 — 立即返回，不再尝试下一层
		if err != nil && dexwallet.IsNonDegradable(err) {
			ps.stats.recordNonDegradableFail()
			slog.Warn("不可降级错误，停止降级",
				"tier", tier,
				"error", err,
			)
			return nil, err
		}

		// 情况 3: 可降级错误或超时 — 继续下一层
		if err != nil {
			allErrors = append(allErrors, err)
			slog.Debug("层级失败，降级到下一层",
				"tier", tier,
				"error", err,
			)
		}
	}

	// 所有层级都失败
	return nil, fmt.Errorf("所有层级均失败 (%d 个错误): %v", len(allErrors), allErrors)
}

// tierResult 层级内单个 DEX 的执行结果。
type tierResult struct {
	result *dexwallet.SwapResult
	err    error
}

// runTier 在指定超时内并发运行一个层级的所有 DEX，返回第一个成功的结果。
//
// 语义：
//   - 任何一个 DEX 成功 → 立即返回该结果
//   - 任何一个 DEX 返回不可降级错误 → 立即返回该错误
//   - 全部超时或可降级失败 → 返回汇总的可降级错误
func (ps *PrioritySelector) runTier(
	ctx context.Context,
	req dexwallet.SwapRequest,
	entries []PrioritySelectorEntry,
	timeout time.Duration,
) (*dexwallet.SwapResult, error) {

	// 创建层级专属的超时 context
	tierCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 结果通道：缓冲大小 = DEX 数量，防止 goroutine 泄漏
	results := make(chan tierResult, len(entries))

	// 并发启动所有 DEX
	for _, entry := range entries {
		go func(e PrioritySelectorEntry) {
			result, err := e.Builder.Build(tierCtx, req)
			results <- tierResult{result: result, err: err}
		}(entry)
	}

	// 收集结果：等待第一个成功或全部完成
	var degradableErrors []error
	received := 0

	for received < len(entries) {
		select {
		case r := <-results:
			received++

			// 成功 — 立即返回
			if r.err == nil && r.result != nil {
				return r.result, nil
			}

			// 不可降级错误 — 立即返回，不等其他 DEX
			if r.err != nil && dexwallet.IsNonDegradable(r.err) {
				return nil, r.err
			}

			// 可降级错误或其他错误 — 记录，继续等待
			if r.err != nil {
				degradableErrors = append(degradableErrors, r.err)
			}

		case <-tierCtx.Done():
			// 层级超时 — 不再等待剩余 DEX
			degradableErrors = append(degradableErrors, &dexwallet.DegradableError{
				Reason: fmt.Sprintf("层级超时 (%v)", timeout),
				Cause:  tierCtx.Err(),
			})
			return nil, &dexwallet.DegradableError{
				Reason: fmt.Sprintf("层级超时，%d/%d 个 DEX 未返回", len(entries)-received, len(entries)),
				Cause:  tierCtx.Err(),
			}
		}
	}

	// 所有 DEX 都返回了但没有成功的
	if len(degradableErrors) > 0 {
		return nil, &dexwallet.DegradableError{
			Reason: fmt.Sprintf("层级内 %d 个 DEX 全部失败", len(entries)),
			Cause:  degradableErrors[0], // 携带第一个错误作为根因
		}
	}

	return nil, &dexwallet.DegradableError{
		Reason: "层级内无可用 DEX",
	}
}
