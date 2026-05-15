package main

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// StablecoinSet -- 稳定币识别集合
// ============================================================
//
// 为什么需要单独识别稳定币？
//
// 在 DEX 交易系统中，不同资产类型具有截然不同的"数据新鲜度"需求：
//   - meme 币：价格一分钟可以涨跌 50%+，流动性频繁变化 --> 缓存 TTL 需要极短（3-5 秒）
//   - 稳定币：价格几乎恒定（1 USDT ≈ 1 USDC ≈ 1 USD），流动性深且稳定 --> 缓存 TTL 可以很长（30-60 秒）
//
// 通过识别池子中的代币是否为稳定币，可以将池子分配到不同的缓存层，
// 从而在数据准确性和 RPC 调用成本之间取得最佳平衡。

// StablecoinInfo 描述一个稳定币的基本信息。
type StablecoinInfo struct {
	Symbol   string // 代币符号，如 USDT, USDC, DAI, BUSD, USD1
	Decimals uint8  // 代币精度
	Category string // 稳定币分类："usd_pegged"（法币锚定）, "algo_stable"（算法稳定）, "wrapped"（跨链包装）
}

// StablecoinSet 管理已知稳定币的 mint 地址集合。
// 用于快速判断某个代币是否为稳定币，以及某个交易对是否为稳定币对。
//
// 并发安全：内部使用 sync.RWMutex 保护，支持运行时动态添加新的稳定币。
// 生产中可通过配置中心热更新稳定币列表（例如新上线的稳定币、或移除脱锚的稳定币）。
type StablecoinSet struct {
	mu    sync.RWMutex
	mints map[string]StablecoinInfo // mint 地址 -> 稳定币信息
}

// NewStablecoinSet 创建一个空的稳定币集合。
func NewStablecoinSet() *StablecoinSet {
	return &StablecoinSet{
		mints: make(map[string]StablecoinInfo),
	}
}

// Add 添加一个稳定币到集合。
// 如果 mint 地址已存在，会覆盖旧的信息。
func (s *StablecoinSet) Add(mint string, info StablecoinInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mints[mint] = info
}

// IsStablecoin 判断给定的 mint 地址是否为已知稳定币。
func (s *StablecoinSet) IsStablecoin(mint string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.mints[mint]
	return ok
}

// IsStablePair 判断交易对的两边是否都是稳定币（例如 USDT/USDC）。
// 稳定币-稳定币对的价格几乎不变，可以使用最长的缓存 TTL。
func (s *StablecoinSet) IsStablePair(baseMint, quoteMint string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, baseOK := s.mints[baseMint]
	_, quoteOK := s.mints[quoteMint]
	return baseOK && quoteOK
}

// IsStableQuote 判断报价代币（quote）是否为稳定币。
// 这类池子（如 MEME/USDT）的报价侧稳定，可以适当延长 TTL。
func (s *StablecoinSet) IsStableQuote(quoteMint string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.mints[quoteMint]
	return ok
}

// DefaultStablecoinSet 返回预置的常见稳定币集合。
//
// 包含 Solana 和 EVM 主网上的主要稳定币：
//   - 法币锚定型（usd_pegged）：由法币储备或国债支撑的中心化稳定币
//   - 算法稳定型（algo_stable）：通过算法机制维持锚定的去中心化稳定币
//   - 跨链包装型（wrapped）：从其他链桥接过来的稳定币
//
// 生产中应通过配置中心管理此列表，支持热更新。
// 当发生脱锚事件（如 USDT < 0.95 USD）时，应动态移除对应稳定币，
// 使其池子回退到普通缓存策略（短 TTL），确保价格数据的时效性。
func DefaultStablecoinSet() *StablecoinSet {
	s := NewStablecoinSet()

	// ---- Solana 主网稳定币 ----

	// USDT (Tether USD) - Solana SPL Token
	s.Add("Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", StablecoinInfo{
		Symbol:   "USDT",
		Decimals: 6,
		Category: "usd_pegged",
	})

	// USDC (USD Coin) - Solana SPL Token
	s.Add("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", StablecoinInfo{
		Symbol:   "USDC",
		Decimals: 6,
		Category: "usd_pegged",
	})

	// USD1 (WLFI USD Stablecoin) - Solana SPL Token
	s.Add("GSSi4BSbqEpNTYMbLF1hWHwFwCbo9Ge3fBF2Jbr7bZiz", StablecoinInfo{
		Symbol:   "USD1",
		Decimals: 6,
		Category: "usd_pegged",
	})

	// DAI (Wormhole 桥接) - Solana SPL Token
	s.Add("EjmyN6qEC1Tf1JxiG1ae7UTJhUxSwk1TCWNWqxWV4J6o", StablecoinInfo{
		Symbol:   "DAI",
		Decimals: 8,
		Category: "wrapped",
	})

	// ---- EVM 主网（Ethereum）稳定币 ----

	// USDT (Tether USD) - ERC-20
	s.Add("0xdAC17F958D2ee523a2206206994597C13D831ec7", StablecoinInfo{
		Symbol:   "USDT",
		Decimals: 6,
		Category: "usd_pegged",
	})

	// USDC (USD Coin) - ERC-20
	s.Add("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", StablecoinInfo{
		Symbol:   "USDC",
		Decimals: 6,
		Category: "usd_pegged",
	})

	// DAI (Maker DAI) - ERC-20
	s.Add("0x6B175474E89094C44Da98b954EedeAC495271d0F", StablecoinInfo{
		Symbol:   "DAI",
		Decimals: 18,
		Category: "algo_stable",
	})

	// BUSD (Binance USD) - ERC-20
	s.Add("0x4Fabb145d64652a948d72533023f6E7A623C7C53", StablecoinInfo{
		Symbol:   "BUSD",
		Decimals: 18,
		Category: "usd_pegged",
	})

	return s
}

// ============================================================
// PoolTier -- 池子缓存层级
// ============================================================

// PoolTier 定义池子的缓存层级。
// 不同层级对应不同的 TTL 和刷新策略。
type PoolTier string

const (
	// PoolTierStableStable 两边都是稳定币（如 USDT/USDC）。
	// 价格波动极小，使用最长的缓存 TTL。
	// StableSwap 协议（如 Curve）的手续费通常只有 0.01%-0.04%。
	PoolTierStableStable PoolTier = "stable-stable"

	// PoolTierStableNormal 一边是稳定币，一边是普通代币（如 SOL/USDT）。
	// 价格波动取决于普通代币侧，使用中等 TTL。
	PoolTierStableNormal PoolTier = "stable-normal"

	// PoolTierNormalNormal 两边都是普通代币（如 MEME/SOL）。
	// 价格波动最大，使用最短的 TTL。
	PoolTierNormalNormal PoolTier = "normal-normal"
)

// ============================================================
// TieredPoolCache -- 分层池子缓存
// ============================================================
//
// 为什么要分层缓存？
//
// 核心洞察：不同资产类型有不同的"数据新鲜度"需求。
//
// 1. 减少 RPC 调用成本
//    稳定币池占总交易量的 30%+，但价格几乎不变。
//    将这些池子的缓存 TTL 从 5 秒延长到 30 秒，可减少约 30% 的 RPC 查询。
//
// 2. 降低延迟
//    命中缓存直接返回，无需等待 RPC 响应（通常 50-200ms）。
//    对高频交易场景，每次节省的延迟都是竞争优势。
//
// 3. 减轻 RPC 节点压力
//    Solana RPC 节点有请求速率限制（如 Helius 免费版 10 req/s）。
//    稳定币池使用长 TTL 可以把宝贵的请求配额留给价格变化快的 meme 币池。
//
// 缓存预热策略（生产建议）：
//   - 启动时批量加载 Top 100 交易量最大的池子（通过 RPC getMultipleAccounts 批量查询）
//   - 按 TVL 排序优先加载稳定币池（命中率最高）
//   - 使用 WebSocket 订阅池子账户变更，实现增量更新（替代轮询）
//   - 对于 stable-stable 池子，可以在预热时就设置较长 TTL，减少后续刷新

// cachedPool 缓存条目，记录池子数据及缓存元信息。
type cachedPool struct {
	pool        *dexwallet.Pool // 缓存的池子数据
	cachedAt    time.Time       // 写入缓存的时间
	accessCount int64           // 访问计数，用于统计热点池子
}

// CacheStats 缓存统计信息。
// 用于监控缓存效率和容量规划。
type CacheStats struct {
	NormalSize  int     // 普通缓存中的池子数量
	StableSize  int     // 稳定币缓存中的池子数量
	HitRate     float64 // 总体缓存命中率（0.0 ~ 1.0）
	TotalHits   int64   // 总命中次数
	TotalMisses int64   // 总未命中次数
}

// TieredPoolCache 分层池子缓存。
//
// 根据池子中代币的类型，将缓存分为两层：
//   - normalCache：普通池子（短 TTL，默认 5 秒），适用于 meme 币等价格波动大的池子
//   - stableCache：稳定币池（长 TTL，默认 30 秒），适用于 USDT/USDC 等价格稳定的池子
//
// 写入时自动根据池子的 BaseMint/QuoteMint 判断应该放入哪一层。
// 读取时自动根据条目所在层使用对应的 TTL 判断是否过期。
//
// 稳定币脱锚检测：
//   当监测到稳定币价格严重偏离锚定值（如 USDT < 0.95 USD）时，
//   应调用 Invalidate 方法手动失效该池子的缓存，使下次查询强制从链上刷新。
//   同时应考虑将该稳定币从 StablecoinSet 中移除，
//   让相关池子在后续写入时自动归入 normalCache（使用短 TTL）。
//
// 并发安全：使用 sync.RWMutex 保护 map 操作，hits/misses 使用 atomic 操作。
type TieredPoolCache struct {
	mu sync.RWMutex

	stablecoins *StablecoinSet

	// 普通池子缓存（短 TTL）
	normalCache map[string]*cachedPool
	normalTTL   time.Duration // 默认 5s

	// 稳定币池缓存（长 TTL）
	stableCache map[string]*cachedPool
	stableTTL   time.Duration // 默认 30s

	// 统计计数器（使用 atomic 操作，避免在读路径上加写锁）
	hits   int64
	misses int64
}

// NewTieredPoolCache 创建分层池子缓存。
//
// 参数说明：
//   - stablecoins: 稳定币识别集合，用于判断池子应放入哪一层
//   - normalTTL: 普通池子的缓存有效期（建议 3-5 秒）
//   - stableTTL: 稳定币池的缓存有效期（建议 30-60 秒）
func NewTieredPoolCache(stablecoins *StablecoinSet, normalTTL, stableTTL time.Duration) *TieredPoolCache {
	return &TieredPoolCache{
		stablecoins: stablecoins,
		normalCache: make(map[string]*cachedPool),
		normalTTL:   normalTTL,
		stableCache: make(map[string]*cachedPool),
		stableTTL:   stableTTL,
	}
}

// Get 按池子地址查询缓存。
//
// 查找逻辑：
//  1. 先在 stableCache 中查找，若命中则使用 stableTTL 判断是否过期
//  2. 再在 normalCache 中查找，若命中则使用 normalTTL 判断是否过期
//  3. 未找到或已过期则返回 (nil, false)，调用方应从链上/存储层获取最新数据
//
// 返回的是 Pool 的副本，调用方修改不会影响缓存中的数据。
func (c *TieredPoolCache) Get(address string) (*dexwallet.Pool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 先查 stableCache
	if entry, ok := c.stableCache[address]; ok {
		if time.Since(entry.cachedAt) <= c.stableTTL {
			atomic.AddInt64(&c.hits, 1)
			atomic.AddInt64(&entry.accessCount, 1)
			return copyPool(entry.pool), true
		}
		// 已过期，视为未命中（过期条目在 Put 或清理时移除）
	}

	// 再查 normalCache
	if entry, ok := c.normalCache[address]; ok {
		if time.Since(entry.cachedAt) <= c.normalTTL {
			atomic.AddInt64(&c.hits, 1)
			atomic.AddInt64(&entry.accessCount, 1)
			return copyPool(entry.pool), true
		}
	}

	atomic.AddInt64(&c.misses, 1)
	return nil, false
}

// Put 将池子存入缓存。
//
// 根据池子的 BaseMint 和 QuoteMint 自动判断缓存层级：
//   - 两边都是稳定币（stable-stable）-> stableCache
//   - 一边是稳定币（stable-normal）  -> stableCache（报价侧稳定，价格变化主要来自普通代币侧，但整体波动仍可控）
//   - 两边都不是稳定币              -> normalCache
//
// 存入的是 Pool 的副本，外部后续修改原始 Pool 不会影响缓存数据。
func (c *TieredPoolCache) Put(pool *dexwallet.Pool) {
	if pool == nil || pool.Address == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := &cachedPool{
		pool:     copyPool(pool),
		cachedAt: time.Now(),
	}

	tier := c.classifyPoolLocked(pool)
	switch tier {
	case PoolTierStableStable, PoolTierStableNormal:
		// 如果之前在 normalCache 中，先移除
		delete(c.normalCache, pool.Address)
		c.stableCache[pool.Address] = entry
	default:
		// 如果之前在 stableCache 中，先移除（可能稳定币被移除集合后降级）
		delete(c.stableCache, pool.Address)
		c.normalCache[pool.Address] = entry
	}
}

// Invalidate 手动失效指定池子的缓存。
//
// 使用场景：
//   - 检测到稳定币脱锚事件（如 USDT 价格跌破 0.95 USD）
//   - 检测到池子发生大额流动性变动（LP 大规模撤出）
//   - 池子状态变更（如从 active 变为 inactive）
//   - 管理员手动触发强制刷新
//
// 调用后，下次 Get 将返回 miss，迫使调用方从链上重新获取最新数据。
func (c *TieredPoolCache) Invalidate(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.stableCache, address)
	delete(c.normalCache, address)
}

// Stats 返回缓存统计信息。
//
// 统计信息包括：
//   - 各层缓存的当前大小
//   - 总体命中率
//   - 累计命中/未命中次数
//
// 生产中可定期采集这些指标并上报到监控系统（如 Prometheus），
// 用于评估缓存策略效果和调优 TTL 参数。
func (c *TieredPoolCache) Stats() CacheStats {
	c.mu.RLock()
	normalSize := len(c.normalCache)
	stableSize := len(c.stableCache)
	c.mu.RUnlock()

	totalHits := atomic.LoadInt64(&c.hits)
	totalMisses := atomic.LoadInt64(&c.misses)

	var hitRate float64
	total := totalHits + totalMisses
	if total > 0 {
		hitRate = float64(totalHits) / float64(total)
	}

	return CacheStats{
		NormalSize:  normalSize,
		StableSize:  stableSize,
		HitRate:     hitRate,
		TotalHits:   totalHits,
		TotalMisses: totalMisses,
	}
}

// ClassifyPool 根据池子的代币对判断缓存层级。
//
// 分类规则：
//   - "stable-stable": BaseMint 和 QuoteMint 都是稳定币（如 USDT/USDC），使用最长 TTL
//   - "stable-normal": 其中一个是稳定币（如 SOL/USDT），使用中等 TTL
//   - "normal-normal": 两边都不是稳定币（如 MEME/SOL），使用最短 TTL
//
// 此方法是线程安全的，内部通过 RLock 读取 StablecoinSet。
func (c *TieredPoolCache) ClassifyPool(pool *dexwallet.Pool) string {
	if pool == nil {
		return string(PoolTierNormalNormal)
	}

	baseIsStable := c.stablecoins.IsStablecoin(pool.BaseMint)
	quoteIsStable := c.stablecoins.IsStablecoin(pool.QuoteMint)

	switch {
	case baseIsStable && quoteIsStable:
		return string(PoolTierStableStable)
	case baseIsStable || quoteIsStable:
		return string(PoolTierStableNormal)
	default:
		return string(PoolTierNormalNormal)
	}
}

// classifyPoolLocked 内部分类方法（调用方须已持有 c.mu 锁）。
// 直接访问 stablecoins 的底层 map 以避免重复加锁。
func (c *TieredPoolCache) classifyPoolLocked(pool *dexwallet.Pool) PoolTier {
	if pool == nil {
		return PoolTierNormalNormal
	}

	// 直接访问 stablecoins 的方法（StablecoinSet 内部有自己的 RWMutex，不会死锁）
	baseIsStable := c.stablecoins.IsStablecoin(pool.BaseMint)
	quoteIsStable := c.stablecoins.IsStablecoin(pool.QuoteMint)

	switch {
	case baseIsStable && quoteIsStable:
		return PoolTierStableStable
	case baseIsStable || quoteIsStable:
		return PoolTierStableNormal
	default:
		return PoolTierNormalNormal
	}
}
