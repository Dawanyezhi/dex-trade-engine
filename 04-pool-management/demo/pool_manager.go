package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// PoolManagerDemo 池子管理器演示实现。
// 包装 LRUPoolCache + MemoryPoolRepository，提供增强的最优池选择算法。
//
// 与 BasePoolManager 的区别：
// - GetBestPool 使用综合评分（流动性权重 + 费率权重），而非仅按流动性排序
// - 支持定时刷新模拟
// - 增加了池子状态管理方法
type PoolManagerDemo struct {
	mu    sync.RWMutex
	cache *dexwallet.LRUPoolCache
	repo  *MemoryPoolRepository

	// 评分权重配置
	liquidityWeight float64 // 流动性权重，默认 0.7
	feeWeight       float64 // 费率权重，默认 0.3

	// 刷新控制
	refreshInterval time.Duration
	stopCh          chan struct{}
}

// PoolManagerConfig 池子管理器配置。
type PoolManagerConfig struct {
	CacheCapacity   int
	CacheTTL        time.Duration
	LiquidityWeight float64
	FeeWeight       float64
	RefreshInterval time.Duration
}

// DefaultPoolManagerConfig 返回默认配置。
func DefaultPoolManagerConfig() PoolManagerConfig {
	return PoolManagerConfig{
		CacheCapacity:   100,
		CacheTTL:        30 * time.Second,
		LiquidityWeight: 0.7,
		FeeWeight:       0.3,
		RefreshInterval: 10 * time.Second,
	}
}

// NewPoolManagerDemo 创建池子管理器。
func NewPoolManagerDemo(cfg PoolManagerConfig) *PoolManagerDemo {
	cache := dexwallet.NewLRUPoolCache(cfg.CacheCapacity, cfg.CacheTTL)
	repo := NewMemoryPoolRepository()

	return &PoolManagerDemo{
		cache:           cache,
		repo:            repo,
		liquidityWeight: cfg.LiquidityWeight,
		feeWeight:       cfg.FeeWeight,
		refreshInterval: cfg.RefreshInterval,
		stopCh:          make(chan struct{}),
	}
}

// GetPool 获取指定池子。
// 查找顺序：缓存 -> 存储。
// 返回的是 Pool 的副本，调用方修改不会影响缓存中的数据。
func (pm *PoolManagerDemo) GetPool(ctx context.Context, address string) (*dexwallet.Pool, error) {
	// 先查缓存
	if pool, ok := pm.cache.Get(address); ok {
		slog.Debug("pool cache hit", "address", address)
		return copyPool(pool), nil
	}

	slog.Debug("pool cache miss, querying repository", "address", address)

	// 查存储（repo.GetPool 已经返回副本）
	pool, err := pm.repo.GetPool(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("get pool %s: %w", address, err)
	}

	// 回填缓存
	pm.cache.Put(pool)
	return copyPool(pool), nil
}

// GetBestPool 获取某交易对的最优池子。
// 使用综合评分算法：score = liquidityWeight * normalizedLiquidity + feeWeight * feeScore
//
// 评分逻辑：
// 1. 只考虑 state == active 的池子
// 2. 流动性为 0 或 nil 的池子直接排除
// 3. 流动性归一化到 [0, 10000]（以候选池中最大流动性为基准）
// 4. 费率评分 = minFeeRate / pool.FeeRate * 10000（费率越低得分越高）
// 5. 综合评分 = liquidityWeight * liquidityScore + feeWeight * feeScore
func (pm *PoolManagerDemo) GetBestPool(ctx context.Context, baseMint, quoteMint string) (*dexwallet.Pool, error) {
	_ = ctx

	// 从缓存中获取该交易对的所有池子
	pools := pm.cache.GetByPair(baseMint, quoteMint)
	if len(pools) == 0 {
		return nil, fmt.Errorf("no pool found for %s/%s", baseMint, quoteMint)
	}

	// 过滤：只保留 active 且流动性 > 0 的池子
	var candidates []*dexwallet.Pool
	for _, p := range pools {
		if p.State != dexwallet.PoolStateActive {
			continue
		}
		if p.Liquidity == nil || p.Liquidity.Sign() <= 0 {
			continue
		}
		candidates = append(candidates, p)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no active pool with liquidity for %s/%s", baseMint, quoteMint)
	}

	// 如果只有一个候选池，直接返回
	if len(candidates) == 1 {
		return candidates[0], nil
	}

	// 计算评分
	type scoredPool struct {
		pool  *dexwallet.Pool
		score int64
	}

	// 找出最大流动性和最小费率（用于归一化）
	maxLiquidity := new(big.Int).Set(candidates[0].Liquidity)
	minFeeRate := candidates[0].FeeRate
	for _, p := range candidates[1:] {
		if p.Liquidity.Cmp(maxLiquidity) > 0 {
			maxLiquidity.Set(p.Liquidity)
		}
		if p.FeeRate < minFeeRate {
			minFeeRate = p.FeeRate
		}
	}

	// 避免除零
	if maxLiquidity.Sign() == 0 {
		maxLiquidity = big.NewInt(1)
	}
	if minFeeRate == 0 {
		minFeeRate = 1
	}

	// 缩放因子，用于保留整数计算精度
	scaleFactor := big.NewInt(10000)

	scored := make([]scoredPool, 0, len(candidates))
	for _, p := range candidates {
		// 流动性评分: pool.Liquidity / maxLiquidity * 10000
		liquidityScore := new(big.Int).Mul(p.Liquidity, scaleFactor)
		liquidityScore.Div(liquidityScore, maxLiquidity)

		// 费率评分: minFeeRate / pool.FeeRate * 10000
		var feeScore int64
		if p.FeeRate > 0 {
			feeScore = int64(minFeeRate) * 10000 / int64(p.FeeRate)
		}

		// 综合评分（使用整数计算，权重乘以 100 变成整数）
		// score = liquidityWeight(70) * liquidityScore + feeWeight(30) * feeScore
		lw := int64(pm.liquidityWeight * 100)
		fw := int64(pm.feeWeight * 100)
		totalScore := lw*liquidityScore.Int64() + fw*feeScore

		scored = append(scored, scoredPool{
			pool:  p,
			score: totalScore,
		})

		slog.Debug("pool score calculated",
			"address", p.Address,
			"dex_id", p.DexID,
			"liquidity_score", liquidityScore.Int64(),
			"fee_score", feeScore,
			"total_score", totalScore,
		)
	}

	// 按评分降序排序，评分相同则按更新时间降序
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].pool.UpdatedAt.After(scored[j].pool.UpdatedAt)
	})

	best := scored[0]
	slog.Info("best pool selected",
		"address", best.pool.Address,
		"dex_id", best.pool.DexID,
		"score", best.score,
		"liquidity", best.pool.Liquidity.String(),
		"fee_rate", best.pool.FeeRate,
	)

	return best.pool, nil
}

// UpdatePool 更新池子数据。
// 同时更新缓存和存储。
func (pm *PoolManagerDemo) UpdatePool(ctx context.Context, pool *dexwallet.Pool) error {
	pool.UpdatedAt = time.Now()

	// 更新缓存
	pm.cache.Put(pool)

	// 更新存储
	if err := pm.repo.SavePool(ctx, pool); err != nil {
		return fmt.Errorf("save pool to repo: %w", err)
	}

	slog.Info("pool updated",
		"address", pool.Address,
		"state", pool.State,
	)
	return nil
}

// RefreshCache 刷新缓存，清除过期条目。
func (pm *PoolManagerDemo) RefreshCache(_ context.Context) error {
	evicted := pm.cache.Evict()
	if evicted > 0 {
		slog.Info("pool cache refreshed", "evicted", evicted)
	}
	return nil
}

// AddPool 添加池子到管理器（同时保存到缓存和存储）。
// 这是演示用的便捷方法，生产中池子通过 PoolParser 从链上数据解析后添加。
func (pm *PoolManagerDemo) AddPool(ctx context.Context, pool *dexwallet.Pool) error {
	if pool.UpdatedAt.IsZero() {
		pool.UpdatedAt = time.Now()
	}

	// 保存到存储
	if err := pm.repo.SavePool(ctx, pool); err != nil {
		return fmt.Errorf("save pool: %w", err)
	}

	// 加入缓存
	pm.cache.Put(pool)

	slog.Info("pool added",
		"address", pool.Address,
		"dex_id", pool.DexID,
		"pair", pool.BaseSymbol+"/"+pool.QuoteSymbol,
	)
	return nil
}

// SetPoolState 设置池子状态。
// 实现 active/inactive/need_update 之间的状态转换。
func (pm *PoolManagerDemo) SetPoolState(ctx context.Context, address string, state dexwallet.PoolState) error {
	pool, err := pm.GetPool(ctx, address)
	if err != nil {
		return fmt.Errorf("get pool for state change: %w", err)
	}

	oldState := pool.State
	pool.State = state
	pool.UpdatedAt = time.Now()

	// 更新缓存和存储
	pm.cache.Put(pool)
	if err := pm.repo.SavePool(ctx, pool); err != nil {
		return fmt.Errorf("save pool state: %w", err)
	}

	slog.Info("pool state changed",
		"address", address,
		"old_state", oldState,
		"new_state", state,
	)

	// 如果标记为 inactive，从缓存中移除（不参与后续查询）
	if state == dexwallet.PoolStateInactive {
		pm.cache.Remove(address)
		slog.Info("inactive pool removed from cache", "address", address)
	}

	return nil
}

// StartRefresh 启动后台定时刷新。
// 定期调用 Evict 清理过期条目。
func (pm *PoolManagerDemo) StartRefresh() {
	go func() {
		ticker := time.NewTicker(pm.refreshInterval)
		defer ticker.Stop()

		slog.Info("pool cache refresh started",
			"interval", pm.refreshInterval.String(),
		)

		for {
			select {
			case <-ticker.C:
				evicted := pm.cache.Evict()
				slog.Debug("periodic cache eviction",
					"evicted", evicted,
					"cache_size", pm.cache.Size(),
				)
			case <-pm.stopCh:
				slog.Info("pool cache refresh stopped")
				return
			}
		}
	}()
}

// StopRefresh 停止后台刷新。
func (pm *PoolManagerDemo) StopRefresh() {
	close(pm.stopCh)
}

// CacheSize 返回当前缓存大小。
func (pm *PoolManagerDemo) CacheSize() int {
	return pm.cache.Size()
}

// RepoCount 返回存储中的池子数量。
func (pm *PoolManagerDemo) RepoCount() int {
	return pm.repo.PoolCount()
}

// Cache 返回底层缓存（测试用）。
func (pm *PoolManagerDemo) Cache() *dexwallet.LRUPoolCache {
	return pm.cache
}

// 确保 PoolManagerDemo 实现 dexwallet.PoolManager 接口。
var _ dexwallet.PoolManager = (*PoolManagerDemo)(nil)
