// Package coinset 提供链配置和 FeatureGate 机制。
// [dexwallet 通用层] — 链差异通过配置参数化，业务逻辑零修改。
package coinset

import (
	"fmt"
	"sync"
)

// ChainType 链类型。
type ChainType string

const (
	ChainTypeSolana ChainType = "solana"
	ChainTypeEVM    ChainType = "evm"
)

// ChainID 链唯一标识。
type ChainID string

const (
	ChainSolana   ChainID = "solana"
	ChainBSC      ChainID = "bsc"
	ChainEthereum ChainID = "ethereum"
	ChainBase     ChainID = "base"
	ChainXLayer   ChainID = "xlayer"
	ChainMonad    ChainID = "monad"
)

// SignAlgorithm 签名算法。
type SignAlgorithm string

const (
	SignAlgorithmEd25519 SignAlgorithm = "ed25519"
	SignAlgorithmECDSA   SignAlgorithm = "ecdsa"
)

// Feature 可选特性标识。
type Feature string

const (
	FeatureBloomFilter   Feature = "bloom_filter"   // 布隆过滤器地址过滤
	FeatureRangeSync     Feature = "range_sync"     // 区间同步
	FeatureToken2022     Feature = "token_2022"     // Solana Token2022 支持
	FeatureEIP1559       Feature = "eip_1559"       // EVM EIP-1559 Gas 模型
	FeatureAntiMEV       Feature = "anti_mev"       // Anti-MEV RPC 节点
	FeatureRBF           Feature = "rbf"            // Replace-By-Fee 加速
	FeatureBribeService  Feature = "bribe_service"  // 贿赂服务（Solana）
	FeatureALT           Feature = "alt"            // Address Lookup Table（Solana）
	FeatureComputeBudget Feature = "compute_budget" // Compute Budget 指令（Solana）
)

// ChainConfig 链配置，参数化链差异。
type ChainConfig struct {
	ChainID       ChainID       `json:"chain_id"`
	ChainType     ChainType     `json:"chain_type"`
	Name          string        `json:"name"`
	NativeCoin    string        `json:"native_coin"`    // SOL / ETH / BNB
	NativeDecimal uint8         `json:"native_decimal"` // 9 (Solana) / 18 (EVM)
	SignAlgorithm SignAlgorithm `json:"sign_algorithm"`
	Confirmations int           `json:"confirmations"` // 确认数
	BlockTime     int           `json:"block_time_ms"` // 出块时间（毫秒）

	// RPC 配置
	RPCEndpoints []string `json:"rpc_endpoints"`

	// FeatureGate 控制链特有功能
	Features map[Feature]bool `json:"features"`
}

// HasFeature 检查链是否启用某特性。
func (c *ChainConfig) HasFeature(f Feature) bool {
	if c.Features == nil {
		return false
	}
	return c.Features[f]
}

// IsSolana 返回是否为 Solana 链。
func (c *ChainConfig) IsSolana() bool {
	return c.ChainType == ChainTypeSolana
}

// IsEVM 返回是否为 EVM 链。
func (c *ChainConfig) IsEVM() bool {
	return c.ChainType == ChainTypeEVM
}

// Registry 链配置注册表（全局单例）。
type Registry struct {
	mu     sync.RWMutex
	chains map[ChainID]*ChainConfig
}

var (
	globalRegistry *Registry
	once           sync.Once
)

// Global 返回全局注册表。
func Global() *Registry {
	once.Do(func() {
		globalRegistry = NewRegistry()
		globalRegistry.registerDefaults()
	})
	return globalRegistry
}

// NewRegistry 创建新的注册表。
func NewRegistry() *Registry {
	return &Registry{
		chains: make(map[ChainID]*ChainConfig),
	}
}

// Register 注册一条链。
func (r *Registry) Register(cfg *ChainConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chains[cfg.ChainID] = cfg
}

// Get 获取链配置。
func (r *Registry) Get(id ChainID) (*ChainConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.chains[id]
	if !ok {
		return nil, fmt.Errorf("chain not found: %s", id)
	}
	return cfg, nil
}

// ListByType 列出某类型的所有链。
func (r *Registry) ListByType(t ChainType) []*ChainConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*ChainConfig
	for _, cfg := range r.chains {
		if cfg.ChainType == t {
			result = append(result, cfg)
		}
	}
	return result
}

// registerDefaults 注册默认链配置。
func (r *Registry) registerDefaults() {
	r.Register(&ChainConfig{
		ChainID:       ChainSolana,
		ChainType:     ChainTypeSolana,
		Name:          "Solana",
		NativeCoin:    "SOL",
		NativeDecimal: 9,
		SignAlgorithm: SignAlgorithmEd25519,
		Confirmations: 1,
		BlockTime:     400,
		Features: map[Feature]bool{
			FeatureToken2022:     true,
			FeatureBribeService:  true,
			FeatureALT:           true,
			FeatureComputeBudget: true,
		},
	})

	r.Register(&ChainConfig{
		ChainID:       ChainBSC,
		ChainType:     ChainTypeEVM,
		Name:          "BNB Smart Chain",
		NativeCoin:    "BNB",
		NativeDecimal: 18,
		SignAlgorithm: SignAlgorithmECDSA,
		Confirmations: 15,
		BlockTime:     3000,
		Features: map[Feature]bool{
			FeatureEIP1559: false,
			FeatureAntiMEV: true,
		},
	})

	r.Register(&ChainConfig{
		ChainID:       ChainEthereum,
		ChainType:     ChainTypeEVM,
		Name:          "Ethereum",
		NativeCoin:    "ETH",
		NativeDecimal: 18,
		SignAlgorithm: SignAlgorithmECDSA,
		Confirmations: 12,
		BlockTime:     12000,
		Features: map[Feature]bool{
			FeatureEIP1559: true,
			FeatureAntiMEV: true,
			FeatureRBF:     true,
		},
	})

	r.Register(&ChainConfig{
		ChainID:       ChainBase,
		ChainType:     ChainTypeEVM,
		Name:          "Base",
		NativeCoin:    "ETH",
		NativeDecimal: 18,
		SignAlgorithm: SignAlgorithmECDSA,
		Confirmations: 10,
		BlockTime:     2000,
		Features: map[Feature]bool{
			FeatureEIP1559: true,
			FeatureAntiMEV: false,
		},
	})

	r.Register(&ChainConfig{
		ChainID:       ChainMonad,
		ChainType:     ChainTypeEVM,
		Name:          "Monad",
		NativeCoin:    "MON",
		NativeDecimal: 18,
		SignAlgorithm: SignAlgorithmECDSA,
		Confirmations: 1,
		BlockTime:     500,
		Features: map[Feature]bool{
			FeatureEIP1559: true,
			FeatureAntiMEV: false,
		},
	})

	r.Register(&ChainConfig{
		ChainID:       ChainXLayer,
		ChainType:     ChainTypeEVM,
		Name:          "XLayer",
		NativeCoin:    "OKB",
		NativeDecimal: 18,
		SignAlgorithm: SignAlgorithmECDSA,
		Confirmations: 10,
		BlockTime:     3000,
		Features: map[Feature]bool{
			FeatureEIP1559: true,
			FeatureAntiMEV: false,
		},
	})
}
