package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// DexCompetition -- 全并发竞争选择器（生产模式）
// ============================================================
//
// 对标 solwallet 中 build_swap_tx.go 的实际生产逻辑。
//
// ---- 与 PrioritySelector 的核心区别 ----
//
// PrioritySelector（串行层级模式）：
//   按优先级层级依次运行，高层级全部超时/失败后才启动下一层。
//   流程: 启动高优先级 DEX → 等待结果 → 超时 → 启动中优先级 DEX → …
//   特点: 低优先级 DEX 的启动时间被高优先级层的超时阻塞。
//   适用: 对延迟要求不极端、希望严格隔离层级的场景。
//
// DexCompetition（全并发+竞争状态机模式）：
//   所有优先级的 DEX 同时启动，结果通过竞争状态机按优先级选择。
//   流程: 同时启动所有 DEX → 结果流入状态机 → 状态机按规则选出胜者。
//   特点: 低优先级 DEX 在高优先级 DEX 运行的同时就已经在执行，
//         如果高优先级全部失败，低优先级结果可能已经就绪，无需额外等待。
//   适用: 延迟极度敏感的生产环境（solwallet 实际使用此模式）。
//
// ---- 竞争状态机选择规则 ----
//
// 每收到一个 DEX 的结果，状态机尝试选择：
//   1. trySelectHigh():   highResult != nil → 立即选中（高优先级有成功结果，无需等待）
//   2. trySelectMiddle(): highRemaining == 0 && middleResult != nil → 选中
//      （高优先级全部完成且无成功结果，中优先级有成功结果）
//   3. trySelectLow():    highRemaining == 0 && middleRemaining == 0 && lowResult != nil → 选中
//      （高/中优先级全部完成且无成功结果，低优先级有成功结果）
//
// 关键：高优先级的成功结果可以"抢占"——即使中/低优先级先返回了成功结果，
// 只要高优先级随后也成功了，仍然选择高优先级的结果。
//
// ---- 快速失败规则 ----
//
// 并非所有错误都应该继续等待其他 DEX。某些错误表明请求本身有问题，
// 换 DEX 也不会成功，应该立即终止整个竞争：
//   - InsufficientBalanceError（余额不足）：用户钱包余额不够，所有 DEX 都会失败
//   - SlippageExceededError（滑点溢出）：市场条件导致，所有 DEX 类似
//   - 仅高/中优先级 DEX 的致命错误触发快速失败，低优先级（聚合器）不触发
//     原因：聚合器本身会做路由选择，其错误不代表全局状态
//
// ---- SplitMix64 灰度控制 ----
//
// 灰度控制用于金丝雀发布、新 DEX 灰度上线等场景。
// 使用 SplitMix64 风格的哈希函数对请求 ID 进行散列，确保：
//   - 同一个请求 ID 始终得到相同的灰度结果（确定性）
//   - 不同请求 ID 的分布均匀（公平性）
//   - 雪花 ID 等有规律的 ID 也能得到良好的散列效果

// ============================================================
// 配置
// ============================================================

// DexCompetitionConfig 全并发竞争选择器配置。
type DexCompetitionConfig struct {
	// TotalTimeout 总超时时间。
	// 从所有 DEX 启动开始计时，超过此时间后强制结束竞争。
	// 默认 5s，生产中通常设为 3-5s。
	TotalTimeout time.Duration
}

// DefaultDexCompetitionConfig 返回默认配置。
func DefaultDexCompetitionConfig() DexCompetitionConfig {
	return DexCompetitionConfig{
		TotalTimeout: 5 * time.Second,
	}
}

// ============================================================
// 参赛条目
// ============================================================

// DexCompetitionEntry 参赛 DEX 条目。
// 每个条目包含一个 SwapBuilder 及其优先级和灰度配置。
type DexCompetitionEntry struct {
	Builder   dexwallet.SwapBuilder // Swap 构建器
	Priority  dexwallet.DexPriority // DEX 优先级（1=高, 2=中, 3=低）
	DexID     dexwallet.DexID       // DEX 唯一标识
	Grayscale int                   // 灰度百分比 0-100，100=全量
}

// ============================================================
// 竞争结果
// ============================================================

// competitionResult 单个 DEX 的竞争结果。
// 从 goroutine 通过 channel 发送给主循环。
type competitionResult struct {
	Result   *dexwallet.SwapResult // 成功时的构建结果
	Err      error                 // 失败时的错误
	DexID    dexwallet.DexID       // DEX 标识
	Priority dexwallet.DexPriority // DEX 优先级
	Label    string                // 人类可读标签（用于日志）
}

// ============================================================
// 竞争状态机
// ============================================================
//
// competitionState 是全并发竞争的核心数据结构。
//
// 设计思路：
//   所有 DEX 同时启动后，结果以不确定的顺序到达。
//   状态机需要跟踪每个优先级层的"未完成计数"和"首个成功结果"，
//   以便在每个结果到达时判断是否可以做出最终选择。
//
//   关键不变式（invariant）：
//   - 高优先级有成功结果 → 立即可选（不等中/低优先级）
//   - 中优先级可选 ← 高优先级全部完成（remaining==0）且无成功结果
//   - 低优先级可选 ← 高+中优先级全部完成且无成功结果
//
//   使用 atomic 操作管理计数器，避免在 channel select 循环中持锁。

type competitionState struct {
	// 每个优先级的未完成 DEX 计数（使用 atomic 递减）
	highRemaining   int32
	middleRemaining int32
	lowRemaining    int32

	// 每个优先级的首个成功结果（只保存第一个，后续成功结果丢弃）
	mu           sync.Mutex // 保护 result 字段的写入
	highResult   *dexwallet.SwapResult
	middleResult *dexwallet.SwapResult
	lowResult    *dexwallet.SwapResult

	// 选中结果的 DexID（用于日志）
	highDexID   dexwallet.DexID
	middleDexID dexwallet.DexID
	lowDexID    dexwallet.DexID

	// 完成时间（性能分析用）
	startTime      time.Time
	highDoneTime   time.Time // 高优先级层全部完成的时间
	middleDoneTime time.Time // 中优先级层全部完成的时间
	lowDoneTime    time.Time // 低优先级层全部完成的时间

	// 快速失败
	criticalError  error // 触发快速失败的致命错误
	shouldFailFast bool  // 是否应该立即终止竞争

	// 错误收集
	errors []error // 所有 DEX 的错误（用于最终汇总）
}

// newCompetitionState 创建竞争状态机。
// 根据参赛条目统计每个优先级的 DEX 数量。
func newCompetitionState(entries []DexCompetitionEntry) *competitionState {
	state := &competitionState{
		startTime: time.Now(),
	}

	for _, e := range entries {
		switch e.Priority {
		case dexwallet.PriorityHigh:
			state.highRemaining++
		case dexwallet.PriorityMedium:
			state.middleRemaining++
		case dexwallet.PriorityLow:
			state.lowRemaining++
		}
	}

	return state
}

// recordSuccess 记录某个优先级的成功结果。
// 只保存第一个成功结果（先到先得），后续成功结果被忽略。
func (s *competitionState) recordSuccess(priority dexwallet.DexPriority, result *dexwallet.SwapResult, dexID dexwallet.DexID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch priority {
	case dexwallet.PriorityHigh:
		if s.highResult == nil {
			s.highResult = result
			s.highDexID = dexID
		}
	case dexwallet.PriorityMedium:
		if s.middleResult == nil {
			s.middleResult = result
			s.middleDexID = dexID
		}
	case dexwallet.PriorityLow:
		if s.lowResult == nil {
			s.lowResult = result
			s.lowDexID = dexID
		}
	}
}

// decrementRemaining 递减指定优先级的未完成计数，并在该层全部完成时记录时间。
func (s *competitionState) decrementRemaining(priority dexwallet.DexPriority) {
	switch priority {
	case dexwallet.PriorityHigh:
		if atomic.AddInt32(&s.highRemaining, -1) == 0 {
			s.highDoneTime = time.Now()
		}
	case dexwallet.PriorityMedium:
		if atomic.AddInt32(&s.middleRemaining, -1) == 0 {
			s.middleDoneTime = time.Now()
		}
	case dexwallet.PriorityLow:
		if atomic.AddInt32(&s.lowRemaining, -1) == 0 {
			s.lowDoneTime = time.Now()
		}
	}
}

// recordError 记录错误。
func (s *competitionState) recordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, err)
}

// setCriticalError 设置致命错误，触发快速失败。
func (s *competitionState) setCriticalError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.shouldFailFast {
		s.criticalError = err
		s.shouldFailFast = true
	}
}

// trySelectHigh 尝试选择高优先级结果。
// 规则：highResult != nil → 立即选中。
func (s *competitionState) trySelectHigh() (*dexwallet.SwapResult, dexwallet.DexID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.highResult != nil {
		return s.highResult, s.highDexID, true
	}
	return nil, "", false
}

// trySelectMiddle 尝试选择中优先级结果。
// 规则：高优先级全部完成（remaining==0）且无成功结果，中优先级有成功结果。
func (s *competitionState) trySelectMiddle() (*dexwallet.SwapResult, dexwallet.DexID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if atomic.LoadInt32(&s.highRemaining) == 0 && s.highResult == nil && s.middleResult != nil {
		return s.middleResult, s.middleDexID, true
	}
	return nil, "", false
}

// trySelectLow 尝试选择低优先级结果。
// 规则：高+中优先级全部完成且无成功结果，低优先级有成功结果。
func (s *competitionState) trySelectLow() (*dexwallet.SwapResult, dexwallet.DexID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if atomic.LoadInt32(&s.highRemaining) == 0 && s.highResult == nil &&
		atomic.LoadInt32(&s.middleRemaining) == 0 && s.middleResult == nil &&
		s.lowResult != nil {
		return s.lowResult, s.lowDexID, true
	}
	return nil, "", false
}

// trySelect 按优先级逐级尝试选择结果。
// 返回选中的结果、DexID、优先级标签和是否选中。
func (s *competitionState) trySelect() (*dexwallet.SwapResult, dexwallet.DexID, string, bool) {
	if result, dexID, ok := s.trySelectHigh(); ok {
		return result, dexID, "high", true
	}
	if result, dexID, ok := s.trySelectMiddle(); ok {
		return result, dexID, "medium", true
	}
	if result, dexID, ok := s.trySelectLow(); ok {
		return result, dexID, "low", true
	}
	return nil, "", "", false
}

// totalRemaining 返回所有优先级的总未完成计数。
func (s *competitionState) totalRemaining() int32 {
	return atomic.LoadInt32(&s.highRemaining) +
		atomic.LoadInt32(&s.middleRemaining) +
		atomic.LoadInt32(&s.lowRemaining)
}

// ============================================================
// SplitMix64 灰度散列
// ============================================================
//
// 为什么不用 math/rand？
//   灰度控制要求确定性：同一个请求 ID 必须始终得到相同的灰度判定结果。
//   math/rand 需要种子，且不同 goroutine 的全局 rand 会产生不同结果。
//   使用纯哈希函数可以保证确定性 + 线程安全 + 零分配。
//
// 为什么用 SplitMix64？
//   1. 算法简单：只有位移和乘法，无分支，编译器可以很好地优化
//   2. 分布均匀：通过了 BigCrush 等统计测试
//   3. 适合雪花 ID：雪花 ID 的低位是自增序列号（分布不均匀），
//      SplitMix64 的两次 xor-shift 可以充分混合高低位
//
// 算法详解：
//   h = qid ^ (qid >> 33)
//     第一次右移33位异或：将高33位的信息混入低31位。
//     雪花 ID 的高位包含时间戳，低位包含序列号，这一步让两者互相影响。
//
//   h = h * 0x9E3779B97F4A7C15
//     乘以黄金比例常数（2^64 / phi，phi = (1+sqrt(5))/2 ≈ 1.618）。
//     这个常数的二进制表示中 0 和 1 分布极其均匀，
//     乘法运算会让输入的每一位都影响到输出的多个位，实现"雪崩效应"。
//     为什么是黄金比例？因为黄金比例是"最无理"的无理数（连分数展开收敛最慢），
//     对应的乘法散列在模运算下分布最均匀（Knuth《TAOCP》卷3证明）。
//
//   h = h ^ (h >> 33)
//     第二次右移33位异或：乘法后高位的信息质量很高，
//     但低位可能仍有模式，这一步将高位混入低位，进一步消除规律。
//
//   h % 100
//     映射到 [0, 100) 范围，用于灰度百分比判定。

// hashQid 对请求 ID 进行 SplitMix64 风格的哈希，返回 [0, 100) 的值。
// 确保雪花 ID 的高位（时间戳）和低位（序列号）都充分参与散列。
func hashQid(qid uint64) uint64 {
	h := qid ^ (qid >> 33)
	h = h * 0x9E3779B97F4A7C15 // 2^64 / phi（黄金比例）
	h = h ^ (h >> 33)
	return h % 100
}

// shouldUseGrayscale 判断指定请求 ID 是否通过灰度控制。
//
// 应用场景：
//   - 金丝雀发布：新 DEX 集成后，先设 5% 灰度观察错误率和延迟
//   - 灰度上线：逐步 5% → 20% → 50% → 100% 放量
//   - A/B 测试：对比新旧 DEX 在相同流量下的表现
//   - 故障回滚：发现问题时将灰度降为 0%，立即停止使用
//
// 参数：
//   qid: 请求唯一标识（通常是雪花 ID 或用户 ID）
//   percentage: 灰度百分比，0 表示完全关闭，100 表示全量开放
func shouldUseGrayscale(qid uint64, percentage int) bool {
	if percentage <= 0 {
		return false
	}
	if percentage >= 100 {
		return true
	}
	return hashQid(qid) < uint64(percentage)
}

// extractQID 从 SwapRequest 的 Extra 字段提取请求 ID。
// 如果 Extra 中没有 "qid" 字段，返回 0（全量通过灰度检查的效果由 shouldUseGrayscale 的 percentage>=100 保证）。
// 生产中 qid 通常是请求链路的雪花 ID，由上游网关生成。
func extractQID(req dexwallet.SwapRequest) uint64 {
	if req.Extra == nil {
		return 0
	}
	v, ok := req.Extra["qid"]
	if !ok {
		return 0
	}
	switch id := v.(type) {
	case uint64:
		return id
	case int64:
		return uint64(id)
	case float64:
		return uint64(id)
	case int:
		return uint64(id)
	default:
		return 0
	}
}

// ============================================================
// 快速失败规则
// ============================================================
//
// 设计原则：
//   不是所有错误都值得等待其他 DEX。某些错误是"全局性"的，
//   意味着请求本身有问题，换 DEX 也解决不了：
//
//   1. InsufficientBalanceError（余额不足）
//      用户钱包里没有足够的代币，所有 DEX 都会遇到同样的问题。
//
//   2. SlippageExceededError（滑点溢出）
//      通常由市场极端波动导致，短时间内所有 DEX 的情况类似。
//
//   3. 仅高/中优先级 DEX 的致命错误触发快速失败
//      低优先级通常是聚合器（如 Jupiter），它们内部会做路由选择，
//      其错误可能是聚合器自身的问题，不代表全局状态。
//      例如：Jupiter API 超时不代表 Raydium 也会失败。

// isCriticalError 判断是否为致命错误（应触发快速失败）。
// 只有高/中优先级 DEX 返回特定错误类型时才视为致命错误。
func isCriticalError(err error, priority dexwallet.DexPriority) bool {
	// 低优先级 DEX（聚合器）的错误不触发快速失败
	if priority == dexwallet.PriorityLow {
		return false
	}

	// 检查是否为余额不足错误
	var balErr *dexwallet.InsufficientBalanceError
	if errors.As(err, &balErr) {
		return true
	}

	// 检查是否为滑点溢出错误
	var slipErr *dexwallet.SlippageExceededError
	if errors.As(err, &slipErr) {
		return true
	}

	// 检查是否为不可降级错误（通用判断）
	if dexwallet.IsNonDegradable(err) {
		return true
	}

	return false
}

// ============================================================
// DexCompetition 主结构
// ============================================================

// DexCompetition 全并发竞争选择器。
//
// 所有 DEX 同时启动，结果通过竞争状态机按优先级选择胜者。
// 这是 solwallet 实际生产中使用的 build_swap_tx 模式。
//
// 使用方式：
//
//	comp := NewDexCompetition(config, entries)
//	result, err := comp.Run(ctx, req)
type DexCompetition struct {
	config  DexCompetitionConfig
	entries []DexCompetitionEntry
}

// NewDexCompetition 创建全并发竞争选择器。
func NewDexCompetition(config DexCompetitionConfig, entries []DexCompetitionEntry) *DexCompetition {
	if config.TotalTimeout <= 0 {
		config.TotalTimeout = DefaultDexCompetitionConfig().TotalTimeout
	}
	return &DexCompetition{
		config:  config,
		entries: entries,
	}
}

// Run 执行全并发竞争，返回按优先级选出的最佳结果。
//
// 流程：
//  1. 灰度过滤：基于请求 ID（qid）和每个 DEX 的灰度百分比，过滤掉未通过灰度检查的 DEX
//  2. 初始化状态机：统计每个优先级的 DEX 数量，设置计数器
//  3. 全并发启动：所有通过灰度检查的 DEX 同时启动 goroutine，调用 builder.Build
//  4. 结果收集：通过 channel 收集每个 DEX 的结果
//  5. 状态机处理：每收到一个结果，更新状态机并尝试选择
//  6. 选择逻辑：按 high → middle → low 的优先级尝试选择
//  7. 快速失败：致命错误立即终止竞争
//  8. 全部失败：所有 DEX 都失败时返回汇总错误
func (dc *DexCompetition) Run(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	// ---- 步骤 1: 灰度过滤 ----
	qid := extractQID(req)
	var activeEntries []DexCompetitionEntry
	for _, entry := range dc.entries {
		if !shouldUseGrayscale(qid, entry.Grayscale) {
			slog.Debug("灰度过滤: DEX 未通过灰度检查",
				"dex", entry.DexID,
				"grayscale", entry.Grayscale,
				"qid", qid,
			)
			continue
		}
		activeEntries = append(activeEntries, entry)
	}

	if len(activeEntries) == 0 {
		return nil, fmt.Errorf("竞争失败: 所有 DEX 均未通过灰度检查 (qid=%d, 总计 %d 个 DEX)", qid, len(dc.entries))
	}

	slog.Info("DEX 竞争开始",
		"total_entries", len(dc.entries),
		"active_entries", len(activeEntries),
		"qid", qid,
		"timeout", dc.config.TotalTimeout,
	)

	// ---- 步骤 2: 初始化竞争状态机 ----
	state := newCompetitionState(activeEntries)

	// ---- 步骤 3: 创建总超时上下文 + 结果通道 ----
	competCtx, cancel := context.WithTimeout(ctx, dc.config.TotalTimeout)
	defer cancel()

	// 缓冲通道，大小等于活跃 DEX 数量，防止 goroutine 泄漏
	resultCh := make(chan competitionResult, len(activeEntries))

	// ---- 步骤 4: 全并发启动所有 DEX ----
	for _, entry := range activeEntries {
		go func(e DexCompetitionEntry) {
			startTime := time.Now()
			result, err := e.Builder.Build(competCtx, req)
			elapsed := time.Since(startTime)

			slog.Debug("DEX 构建完成",
				"dex", e.DexID,
				"priority", e.Priority,
				"elapsed", elapsed,
				"success", err == nil,
			)

			resultCh <- competitionResult{
				Result:   result,
				Err:      err,
				DexID:    e.DexID,
				Priority: e.Priority,
				Label:    e.Builder.Label(),
			}
		}(entry)
	}

	// ---- 步骤 5: 收集结果 + 状态机处理 ----
	totalActive := int32(len(activeEntries))
	received := int32(0)

	for received < totalActive {
		select {
		case r := <-resultCh:
			received++

			if r.Err != nil {
				// ---- 失败处理 ----
				slog.Debug("DEX 构建失败",
					"dex", r.DexID,
					"priority", r.Priority,
					"error", r.Err,
				)

				state.recordError(fmt.Errorf("[优先级=%s] %s: %w", priorityLabel(r.Priority), r.DexID, r.Err))

				// 检查是否为致命错误 → 快速失败
				if isCriticalError(r.Err, r.Priority) {
					slog.Warn("致命错误触发快速失败",
						"dex", r.DexID,
						"priority", r.Priority,
						"error", r.Err,
					)
					state.setCriticalError(r.Err)
					// 取消上下文，通知其他 goroutine 停止
					cancel()
					return nil, fmt.Errorf("快速失败: %s (优先级=%s) 返回致命错误: %w", r.DexID, priorityLabel(r.Priority), r.Err)
				}
			} else if r.Result != nil {
				// ---- 成功处理 ----
				slog.Debug("DEX 构建成功",
					"dex", r.DexID,
					"priority", r.Priority,
					"output", r.Result.OutputAmount.String(),
				)

				state.recordSuccess(r.Priority, r.Result, r.DexID)
			}

			// 递减对应优先级的计数
			state.decrementRemaining(r.Priority)

			// ---- 步骤 6: 尝试选择结果 ----
			if result, dexID, tier, ok := state.trySelect(); ok {
				elapsed := time.Since(state.startTime)
				slog.Info("竞争胜出",
					"dex", dexID,
					"tier", tier,
					"output", result.OutputAmount.String(),
					"elapsed", elapsed,
					"received", received,
					"total", totalActive,
				)
				// 取消上下文，让还在运行的 DEX 尽快退出
				cancel()
				return result, nil
			}

		case <-competCtx.Done():
			// ---- 总超时 ----
			slog.Warn("竞争总超时",
				"timeout", dc.config.TotalTimeout,
				"received", received,
				"total", totalActive,
				"high_remaining", atomic.LoadInt32(&state.highRemaining),
				"middle_remaining", atomic.LoadInt32(&state.middleRemaining),
				"low_remaining", atomic.LoadInt32(&state.lowRemaining),
			)

			// 超时后仍然尝试选择已有的结果
			if result, dexID, tier, ok := state.trySelect(); ok {
				slog.Info("超时后使用已有结果",
					"dex", dexID,
					"tier", tier,
				)
				return result, nil
			}

			state.mu.Lock()
			errCount := len(state.errors)
			state.mu.Unlock()

			return nil, fmt.Errorf("竞争超时: %v 内 %d/%d 个 DEX 返回结果，%d 个错误，无成功结果",
				dc.config.TotalTimeout, received, totalActive, errCount)
		}
	}

	// ---- 步骤 7: 所有 DEX 都已返回，做最后一次尝试选择 ----
	if result, dexID, tier, ok := state.trySelect(); ok {
		slog.Info("全部完成后选出结果",
			"dex", dexID,
			"tier", tier,
			"elapsed", time.Since(state.startTime),
		)
		return result, nil
	}

	// ---- 步骤 8: 全部失败 ----
	state.mu.Lock()
	allErrors := make([]error, len(state.errors))
	copy(allErrors, state.errors)
	state.mu.Unlock()

	slog.Error("竞争失败: 所有 DEX 均失败",
		"total", totalActive,
		"error_count", len(allErrors),
		"elapsed", time.Since(state.startTime),
	)

	return nil, fmt.Errorf("所有 %d 个 DEX 均失败: %v", totalActive, allErrors)
}

// ============================================================
// DexPriority 的 String 方法辅助（用于日志格式化）
// ============================================================

// priorityLabel 返回优先级的中文标签。
func priorityLabel(p dexwallet.DexPriority) string {
	switch p {
	case dexwallet.PriorityHigh:
		return "高"
	case dexwallet.PriorityMedium:
		return "中"
	case dexwallet.PriorityLow:
		return "低"
	default:
		return fmt.Sprintf("未知(%d)", p)
	}
}
