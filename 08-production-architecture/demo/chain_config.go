// 08-production-architecture 多链配置管理。
// 实现多链运行时配置、热更新和灰度发布。
//
// [dexwallet 通用层] -- 配置管理对所有链一致，链差异通过参数化表达。
package main

import (
	"fmt"
	"log/slog"
	"math/rand"
	"sync"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ChainRuntime 链运行时配置（支持热更新）。
// 在 coinset.ChainConfig 的静态配置基础上，扩展了运行时可变参数。
type ChainRuntime struct {
	Config       *coinset.ChainConfig           // 链的静态配置
	DisabledDex  map[dexwallet.DexID]bool       // 禁用的 DEX 列表
	GrayscaleMap map[dexwallet.DexID]int        // 灰度百分比（0-100）
	MaxSwapTPS   float64                        // 最大 Swap TPS
	MaxWorkers   int                            // 最大 Worker 数
}

// IsDexDisabled 检查某个 DEX 是否被禁用。
func (r *ChainRuntime) IsDexDisabled(dexID dexwallet.DexID) bool {
	if r.DisabledDex == nil {
		return false
	}
	return r.DisabledDex[dexID]
}

// ShouldUseGrayscale 判断某个 DEX 的请求是否应该走灰度。
// 基于百分比随机分流：灰度百分比为 30 表示 30% 的请求走灰度路径。
func (r *ChainRuntime) ShouldUseGrayscale(dexID dexwallet.DexID) bool {
	if r.GrayscaleMap == nil {
		return false
	}
	percentage, exists := r.GrayscaleMap[dexID]
	if !exists {
		return false
	}
	return rand.Intn(100) < percentage
}

// MultiChainConfig 多链配置管理。
// 使用 RWMutex 保护读多写少的配置访问。
type MultiChainConfig struct {
	mu      sync.RWMutex
	configs map[coinset.ChainID]*ChainRuntime
}

// NewMultiChainConfig 创建多链配置管理器。
func NewMultiChainConfig() *MultiChainConfig {
	return &MultiChainConfig{
		configs: make(map[coinset.ChainID]*ChainRuntime),
	}
}

// RegisterChain 注册一条链的运行时配置。
func (m *MultiChainConfig) RegisterChain(runtime *ChainRuntime) {
	m.mu.Lock()
	defer m.mu.Unlock()

	chainID := runtime.Config.ChainID
	m.configs[chainID] = runtime

	slog.Info("链运行时配置已注册",
		"chain", chainID,
		"name", runtime.Config.Name,
		"max_tps", runtime.MaxSwapTPS,
		"max_workers", runtime.MaxWorkers,
	)
}

// GetRuntime 获取链的运行时配置。
func (m *MultiChainConfig) GetRuntime(chainID coinset.ChainID) (*ChainRuntime, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	runtime, ok := m.configs[chainID]
	if !ok {
		return nil, fmt.Errorf("chain runtime not found: %s", chainID)
	}
	return runtime, nil
}

// ListChains 列出所有已注册的链。
func (m *MultiChainConfig) ListChains() []coinset.ChainID {
	m.mu.RLock()
	defer m.mu.RUnlock()

	chains := make([]coinset.ChainID, 0, len(m.configs))
	for id := range m.configs {
		chains = append(chains, id)
	}
	return chains
}

// UpdateChainConfig 热更新链运行时配置。
// updater 函数在持有写锁的情况下执行，直接修改 ChainRuntime。
func (m *MultiChainConfig) UpdateChainConfig(chainID coinset.ChainID, updater func(*ChainRuntime)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	runtime, ok := m.configs[chainID]
	if !ok {
		return fmt.Errorf("chain runtime not found: %s", chainID)
	}

	updater(runtime)

	slog.Info("链运行时配置已更新", "chain", chainID)
	return nil
}

// DisableDex 禁用某条链上的某个 DEX。
func (m *MultiChainConfig) DisableDex(chainID coinset.ChainID, dexID dexwallet.DexID) error {
	return m.UpdateChainConfig(chainID, func(r *ChainRuntime) {
		if r.DisabledDex == nil {
			r.DisabledDex = make(map[dexwallet.DexID]bool)
		}
		r.DisabledDex[dexID] = true
		slog.Warn("DEX 已被禁用", "chain", chainID, "dex", dexID)
	})
}

// EnableDex 启用某条链上的某个 DEX。
func (m *MultiChainConfig) EnableDex(chainID coinset.ChainID, dexID dexwallet.DexID) error {
	return m.UpdateChainConfig(chainID, func(r *ChainRuntime) {
		if r.DisabledDex != nil {
			delete(r.DisabledDex, dexID)
			slog.Info("DEX 已被启用", "chain", chainID, "dex", dexID)
		}
	})
}

// SetGrayscale 设置某个 DEX 的灰度百分比。
func (m *MultiChainConfig) SetGrayscale(chainID coinset.ChainID, dexID dexwallet.DexID, percentage int) error {
	if percentage < 0 || percentage > 100 {
		return fmt.Errorf("invalid grayscale percentage: %d (must be 0-100)", percentage)
	}
	return m.UpdateChainConfig(chainID, func(r *ChainRuntime) {
		if r.GrayscaleMap == nil {
			r.GrayscaleMap = make(map[dexwallet.DexID]int)
		}
		r.GrayscaleMap[dexID] = percentage
		slog.Info("灰度百分比已更新", "chain", chainID, "dex", dexID, "percentage", percentage)
	})
}

// UpdateMaxTPS 更新链的最大 Swap TPS。
func (m *MultiChainConfig) UpdateMaxTPS(chainID coinset.ChainID, maxTPS float64) error {
	if maxTPS <= 0 {
		return fmt.Errorf("invalid max TPS: %f (must be > 0)", maxTPS)
	}
	return m.UpdateChainConfig(chainID, func(r *ChainRuntime) {
		oldTPS := r.MaxSwapTPS
		r.MaxSwapTPS = maxTPS
		slog.Info("最大 TPS 已更新", "chain", chainID, "old_tps", oldTPS, "new_tps", maxTPS)
	})
}

// ---------------------------------------------------------------------------
// 默认配置工厂
// ---------------------------------------------------------------------------

// NewDefaultMultiChainConfig 创建包含默认链配置的多链管理器。
// 从 coinset.Registry 获取静态配置，附加默认运行时参数。
func NewDefaultMultiChainConfig(registry *coinset.Registry) *MultiChainConfig {
	mcc := NewMultiChainConfig()

	// Solana: 高 TPS，多 DEX
	if cfg, err := registry.Get(coinset.ChainSolana); err == nil {
		mcc.RegisterChain(&ChainRuntime{
			Config:      cfg,
			DisabledDex: make(map[dexwallet.DexID]bool),
			GrayscaleMap: map[dexwallet.DexID]int{
				dexwallet.DexPumpAMM: 50, // PumpAMM 灰度 50%
			},
			MaxSwapTPS: 50,
			MaxWorkers: 10,
		})
	}

	// BSC: 中等 TPS
	if cfg, err := registry.Get(coinset.ChainBSC); err == nil {
		mcc.RegisterChain(&ChainRuntime{
			Config:       cfg,
			DisabledDex:  make(map[dexwallet.DexID]bool),
			GrayscaleMap: make(map[dexwallet.DexID]int),
			MaxSwapTPS:   20,
			MaxWorkers:   5,
		})
	}

	// Ethereum: 低 TPS，Gas 昂贵
	if cfg, err := registry.Get(coinset.ChainEthereum); err == nil {
		mcc.RegisterChain(&ChainRuntime{
			Config:       cfg,
			DisabledDex:  make(map[dexwallet.DexID]bool),
			GrayscaleMap: make(map[dexwallet.DexID]int),
			MaxSwapTPS:   10,
			MaxWorkers:   3,
		})
	}

	// Base: 中等 TPS
	if cfg, err := registry.Get(coinset.ChainBase); err == nil {
		mcc.RegisterChain(&ChainRuntime{
			Config:       cfg,
			DisabledDex:  make(map[dexwallet.DexID]bool),
			GrayscaleMap: make(map[dexwallet.DexID]int),
			MaxSwapTPS:   30,
			MaxWorkers:   5,
		})
	}

	return mcc
}
