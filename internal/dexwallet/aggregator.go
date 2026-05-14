package dexwallet

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
)

// DexEntry 注册的 DEX 信息。
type DexEntry struct {
	Builder  SwapBuilder
	Protocol DexProtocol
	Priority DexPriority
	Enabled  bool

	// 灰度发布：0-100，100 表示全量
	GrayscalePercent int
}

// BaseAggregator 通用聚合器实现。
// [dexwallet 通用层] — 并发 Quote → 按优先级排序 → 选最优 → 降级兜底。
// Solana 和 EVM 的聚合流程完全一致，只是注册的 DEX 列表不同。
type BaseAggregator struct {
	mu      sync.RWMutex
	dexList map[DexID]*DexEntry
	chainID coinset.ChainID

	poolManager PoolManager

	// 聚合配置
	quoteTimeout   time.Duration // 单个 DEX 报价超时
	maxConcurrency int           // 最大并发报价数，防止 100+ DEX 创建过多 goroutine
}

// NewBaseAggregator 创建通用聚合器。
func NewBaseAggregator(chainID coinset.ChainID, pm PoolManager) *BaseAggregator {
	return &BaseAggregator{
		dexList:        make(map[DexID]*DexEntry),
		chainID:        chainID,
		poolManager:    pm,
		quoteTimeout:   3 * time.Second,
		maxConcurrency: 20, // P1-3: 默认最多 20 个并发报价
	}
}

// SetMaxConcurrency 设置最大并发报价数。
func (a *BaseAggregator) SetMaxConcurrency(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n > 0 {
		a.maxConcurrency = n
	}
}

// RegisterDex 注册 DEX。
func (a *BaseAggregator) RegisterDex(entry *DexEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dexList[entry.Builder.DexID()] = entry
}

// SetGrayscale 设置 DEX 灰度百分比。
func (a *BaseAggregator) SetGrayscale(dexID DexID, percent int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if entry, ok := a.dexList[dexID]; ok {
		entry.GrayscalePercent = percent
	}
}

// EnableDex 启用/禁用 DEX。
func (a *BaseAggregator) EnableDex(dexID DexID, enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if entry, ok := a.dexList[dexID]; ok {
		entry.Enabled = enabled
	}
}

// FindBestQuote 并发获取所有 DEX 报价，返回最优结果。
func (a *BaseAggregator) FindBestQuote(ctx context.Context, req SwapRequest) (*Quote, error) {
	entries := a.getActiveDex()
	if len(entries) == 0 {
		return nil, fmt.Errorf("no active DEX available for chain %s", a.chainID)
	}

	// 获取交易对的池子
	pool, err := a.poolManager.GetBestPool(ctx, req.FromToken.Address, req.ToToken.Address)
	if err != nil {
		slog.Warn("no direct pool found, will try each DEX", "error", err)
	}

	// 并发获取报价（P1-3: 通过信号量限制并发数）
	type quoteResult struct {
		quote *Quote
		err   error
	}

	results := make(chan quoteResult, len(entries))
	quoteCtx, cancel := context.WithTimeout(ctx, a.quoteTimeout)
	defer cancel()

	a.mu.RLock()
	concurrency := a.maxConcurrency
	a.mu.RUnlock()
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	for _, entry := range entries {
		wg.Add(1)
		go func(e *DexEntry) {
			defer wg.Done()
			sem <- struct{}{}        // 获取信号量
			defer func() { <-sem }() // 释放信号量
			q, qErr := e.Protocol.Quote(quoteCtx, pool, req.Amount, req.Direction)
			if qErr != nil {
				slog.Debug("quote failed", "dex", e.Builder.DexID(), "error", qErr)
				results <- quoteResult{err: qErr}
				return
			}
			q.Priority = e.Priority
			results <- quoteResult{quote: q}
		}(entry)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集有效报价
	var quotes []*Quote
	for r := range results {
		if r.err == nil && r.quote != nil && r.quote.OutputAmount != nil && r.quote.OutputAmount.Sign() > 0 {
			quotes = append(quotes, r.quote)
		}
	}

	if len(quotes) == 0 {
		return nil, fmt.Errorf("all DEX quotes failed for %s → %s", req.FromToken.Symbol, req.ToToken.Symbol)
	}

	// 按优先级 + 输出金额排序
	sort.Slice(quotes, func(i, j int) bool {
		if quotes[i].Priority != quotes[j].Priority {
			return quotes[i].Priority < quotes[j].Priority // 优先级高的优先
		}
		return quotes[i].OutputAmount.Cmp(quotes[j].OutputAmount) > 0 // 输出多的优先
	})

	best := quotes[0]
	slog.Info("best quote selected",
		"dex", best.DexID,
		"output", best.OutputAmount.String(),
		"priority", best.Priority,
		"total_quotes", len(quotes),
	)

	return best, nil
}

// BuildSwap 使用最优报价构建交易。
func (a *BaseAggregator) BuildSwap(ctx context.Context, req SwapRequest) (*SwapResult, error) {
	// 先找最优报价
	quote, err := a.FindBestQuote(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("find best quote: %w", err)
	}

	// P0-1: 在读锁内快照 builder 引用，避免 TOCTOU 竞态。
	// 之前的实现在 RUnlock 之后使用 entry.Builder，期间 EnableDex 可能禁用该 DEX。
	a.mu.RLock()
	entry, ok := a.dexList[quote.DexID]
	var builder SwapBuilder
	if ok && entry.Enabled {
		builder = entry.Builder
	}
	a.mu.RUnlock()

	if builder == nil {
		return nil, fmt.Errorf("DEX builder not found or disabled: %s", quote.DexID)
	}

	req.DexID = quote.DexID
	result, err := builder.Build(ctx, req)
	if err != nil {
		slog.Warn("primary DEX build failed, trying fallback",
			"dex", quote.DexID, "error", err)
		return a.buildWithFallback(ctx, req, quote.DexID)
	}

	return result, nil
}

// buildWithFallback 降级策略：主 DEX 失败后尝试其他 DEX。
func (a *BaseAggregator) buildWithFallback(ctx context.Context, req SwapRequest, excludeDex DexID) (*SwapResult, error) {
	// P0-1: 在锁内快照 builder 列表，锁外执行 Build，避免长时间持锁。
	type candidate struct {
		builder  SwapBuilder
		dexID    DexID
		priority DexPriority
	}

	a.mu.RLock()
	var candidates []candidate
	for id, entry := range a.dexList {
		if id == excludeDex || !entry.Enabled {
			continue
		}
		candidates = append(candidates, candidate{
			builder:  entry.Builder,
			dexID:    id,
			priority: entry.Priority,
		})
	}
	a.mu.RUnlock()

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].priority < candidates[j].priority
	})

	for _, c := range candidates {
		req.DexID = c.dexID
		result, err := c.builder.Build(ctx, req)
		if err == nil {
			slog.Info("fallback DEX succeeded", "dex", c.dexID)
			return result, nil
		}
		slog.Debug("fallback DEX failed", "dex", c.dexID, "error", err)
	}

	return nil, fmt.Errorf("all DEX builders failed (including fallbacks)")
}

// getActiveDex 获取当前激活的 DEX 列表（考虑灰度）。
func (a *BaseAggregator) getActiveDex() []*DexEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var active []*DexEntry
	for _, entry := range a.dexList {
		if !entry.Enabled {
			continue
		}
		// 灰度检查
		if entry.GrayscalePercent < 100 {
			if rand.IntN(100) >= entry.GrayscalePercent {
				continue
			}
		}
		active = append(active, entry)
	}
	return active
}
