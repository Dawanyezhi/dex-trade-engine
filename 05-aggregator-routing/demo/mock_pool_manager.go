package main

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// MockPoolManager -- 模拟 PoolManager，实现 PoolManager 接口
// ============================================================

// MockPoolManager 内存中的池子管理器。
// 用于测试聚合器，不依赖真实的链上数据。
type MockPoolManager struct {
	mu    sync.RWMutex
	pools map[string]*dexwallet.Pool // key: pool address

	// 交易对索引: "baseMint:quoteMint" -> pool address
	pairIndex map[string][]string
}

// NewMockPoolManager 创建 Mock PoolManager。
func NewMockPoolManager() *MockPoolManager {
	return &MockPoolManager{
		pools:     make(map[string]*dexwallet.Pool),
		pairIndex: make(map[string][]string),
	}
}

// AddPool 添加池子（测试辅助方法）。
func (m *MockPoolManager) AddPool(pool *dexwallet.Pool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pools[pool.Address] = pool

	// 更新交易对索引（正向和反向）
	forwardKey := pool.BaseMint + ":" + pool.QuoteMint
	reverseKey := pool.QuoteMint + ":" + pool.BaseMint
	m.pairIndex[forwardKey] = appendUniqueStr(m.pairIndex[forwardKey], pool.Address)
	m.pairIndex[reverseKey] = appendUniqueStr(m.pairIndex[reverseKey], pool.Address)
}

// GetPool 获取指定池子。
func (m *MockPoolManager) GetPool(_ context.Context, address string) (*dexwallet.Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pool, ok := m.pools[address]
	if !ok {
		return nil, fmt.Errorf("pool not found: %s", address)
	}
	return pool, nil
}

// GetBestPool 获取某交易对的最优池子。
// 按流动性降序排列，返回流动性最高的活跃池子。
func (m *MockPoolManager) GetBestPool(_ context.Context, baseMint, quoteMint string) (*dexwallet.Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 查找正向和反向交易对
	pairKey := baseMint + ":" + quoteMint
	addresses := m.pairIndex[pairKey]

	if len(addresses) == 0 {
		return nil, fmt.Errorf("no pool found for %s/%s", baseMint, quoteMint)
	}

	// 按流动性选最优
	var best *dexwallet.Pool
	for _, addr := range addresses {
		pool, ok := m.pools[addr]
		if !ok || pool.State != dexwallet.PoolStateActive {
			continue
		}
		if best == nil {
			best = pool
			continue
		}
		if pool.Liquidity != nil && best.Liquidity != nil && pool.Liquidity.Cmp(best.Liquidity) > 0 {
			best = pool
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no active pool for %s/%s", baseMint, quoteMint)
	}

	return best, nil
}

// UpdatePool 更新池子数据。
func (m *MockPoolManager) UpdatePool(_ context.Context, pool *dexwallet.Pool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool.UpdatedAt = time.Now()
	m.pools[pool.Address] = pool
	return nil
}

// RefreshCache 刷新缓存（Mock 实现为空操作）。
func (m *MockPoolManager) RefreshCache(_ context.Context) error {
	return nil
}

// appendUniqueStr 去重追加字符串。
func appendUniqueStr(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

// ============================================================
// 预置池子数据
// ============================================================

// setupDefaultPools 创建预置的测试池子。
func setupDefaultPools(pm *MockPoolManager) {
	// SOL/MEME 池子（Raydium AMM）
	pm.AddPool(&dexwallet.Pool{
		Address:      "pool_sol_meme_raydium",
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     "So11111111111111111111111111111111111111112",
		QuoteMint:    "MEMExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		BaseSymbol:   "SOL",
		QuoteSymbol:  "MEME",
		BaseDecimal:  9,
		QuoteDecimal: 6,
		Liquidity:    new(big.Int).Mul(big.NewInt(50000), big.NewInt(1e9)), // 50000 SOL
		FeeRate:      25, // 0.25%
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
	})

	// SOL/MEME 池子（Meteora DLMM -- 更高流动性）
	pm.AddPool(&dexwallet.Pool{
		Address:      "pool_sol_meme_meteora",
		DexID:        dexwallet.DexMeteoraDLMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolDLMM,
		BaseMint:     "So11111111111111111111111111111111111111112",
		QuoteMint:    "MEMExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		BaseSymbol:   "SOL",
		QuoteSymbol:  "MEME",
		BaseDecimal:  9,
		QuoteDecimal: 6,
		Liquidity:    new(big.Int).Mul(big.NewInt(80000), big.NewInt(1e9)), // 80000 SOL
		FeeRate:      10, // 0.10%
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
	})

	// SOL/USDC 池子（用于两跳路由的中间环节）
	pm.AddPool(&dexwallet.Pool{
		Address:      "pool_sol_usdc",
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     "So11111111111111111111111111111111111111112",
		QuoteMint:    "USDCxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		BaseSymbol:   "SOL",
		QuoteSymbol:  "USDC",
		BaseDecimal:  9,
		QuoteDecimal: 6,
		Liquidity:    new(big.Int).Mul(big.NewInt(200000), big.NewInt(1e9)), // 200000 SOL
		FeeRate:      25, // 0.25%
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
	})

	// USDC/RARE 池子（用于两跳路由）
	pm.AddPool(&dexwallet.Pool{
		Address:      "pool_usdc_rare",
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     "USDCxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		QuoteMint:    "RARExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		BaseSymbol:   "USDC",
		QuoteSymbol:  "RARE",
		BaseDecimal:  6,
		QuoteDecimal: 6,
		Liquidity:    new(big.Int).Mul(big.NewInt(100000), big.NewInt(1e6)), // 100000 USDC
		FeeRate:      30, // 0.30%
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
	})

	// SOL/RARE 池子（直接路由，流动性较低）
	pm.AddPool(&dexwallet.Pool{
		Address:      "pool_sol_rare",
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     "So11111111111111111111111111111111111111112",
		QuoteMint:    "RARExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		BaseSymbol:   "SOL",
		QuoteSymbol:  "RARE",
		BaseDecimal:  9,
		QuoteDecimal: 6,
		Liquidity:    new(big.Int).Mul(big.NewInt(500), big.NewInt(1e9)), // 500 SOL（低流动性）
		FeeRate:      30, // 0.30%
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
	})
}
