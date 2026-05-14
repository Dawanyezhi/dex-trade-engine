package dexwallet

import (
	"container/list"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// LRUPoolCache 基于 LRU 的池子缓存。
// [dexwallet 通用层] — Solana 和 EVM 共享相同的缓存逻辑。
type LRUPoolCache struct {
	mu       sync.RWMutex
	capacity int
	ttl      time.Duration

	items    map[string]*list.Element // key → element
	eviction *list.List               // LRU 链表

	// 交易对索引：baseMint:quoteMint → []address
	pairIndex map[string][]string
}

type cacheEntry struct {
	key     string
	pool    *Pool
	addedAt time.Time
}

// NewLRUPoolCache 创建 LRU 池子缓存。
func NewLRUPoolCache(capacity int, ttl time.Duration) *LRUPoolCache {
	return &LRUPoolCache{
		capacity:  capacity,
		ttl:       ttl,
		items:     make(map[string]*list.Element),
		eviction:  list.New(),
		pairIndex: make(map[string][]string),
	}
}

// Get 获取缓存中的池子。
func (c *LRUPoolCache) Get(address string) (*Pool, bool) {
	c.mu.RLock()
	elem, ok := c.items[address]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*cacheEntry)

	// 检查 TTL
	if time.Since(entry.addedAt) > c.ttl {
		c.mu.Lock()
		c.removeElement(elem)
		c.mu.Unlock()
		return nil, false
	}

	// 移到链表头部（最近使用）
	c.mu.Lock()
	c.eviction.MoveToFront(elem)
	c.mu.Unlock()

	return entry.pool, true
}

// Put 添加池子到缓存。
func (c *LRUPoolCache) Put(pool *Pool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 已存在则更新
	if elem, ok := c.items[pool.Address]; ok {
		c.eviction.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		entry.pool = pool
		entry.addedAt = time.Now()
		return
	}

	// 超过容量则淘汰
	if c.eviction.Len() >= c.capacity {
		c.removeLRU()
	}

	entry := &cacheEntry{
		key:     pool.Address,
		pool:    pool,
		addedAt: time.Now(),
	}
	elem := c.eviction.PushFront(entry)
	c.items[pool.Address] = elem

	// 更新交易对索引
	pairKey := pool.BaseMint + ":" + pool.QuoteMint
	c.pairIndex[pairKey] = appendUnique(c.pairIndex[pairKey], pool.Address)
}

// GetByPair 根据交易对获取所有池子。
func (c *LRUPoolCache) GetByPair(baseMint, quoteMint string) []*Pool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	pairKey := baseMint + ":" + quoteMint
	addresses := c.pairIndex[pairKey]

	// 也查反向交易对
	reversePairKey := quoteMint + ":" + baseMint
	addresses = append(addresses, c.pairIndex[reversePairKey]...)

	var pools []*Pool
	for _, addr := range addresses {
		if elem, ok := c.items[addr]; ok {
			entry := elem.Value.(*cacheEntry)
			if time.Since(entry.addedAt) <= c.ttl {
				pools = append(pools, entry.pool)
			}
		}
	}
	return pools
}

// Remove 从缓存中移除池子。
func (c *LRUPoolCache) Remove(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[address]; ok {
		c.removeElement(elem)
	}
}

// Size 返回缓存大小。
func (c *LRUPoolCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.eviction.Len()
}

// Evict 清除所有过期条目。
func (c *LRUPoolCache) Evict() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	var next *list.Element
	for e := c.eviction.Back(); e != nil; e = next {
		next = e.Prev()
		entry := e.Value.(*cacheEntry)
		if time.Since(entry.addedAt) > c.ttl {
			c.removeElement(e)
			count++
		}
	}
	return count
}

func (c *LRUPoolCache) removeLRU() {
	if elem := c.eviction.Back(); elem != nil {
		c.removeElement(elem)
	}
}

func (c *LRUPoolCache) removeElement(elem *list.Element) {
	c.eviction.Remove(elem)
	entry := elem.Value.(*cacheEntry)
	delete(c.items, entry.key)
}

func appendUnique(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

// BasePoolManager 通用池子管理器。
// [dexwallet 通用层] — 缓存和状态管理通用，链特定的池子数据解析由 PoolParser 实现。
type BasePoolManager struct {
	cache  *LRUPoolCache
	parser PoolParser
	repo   Repository

	// P0-2: singleflight 防止缓存穿透（多个 goroutine 同时请求同一个不存在的池子）
	sfGroup singleflightGroup
}

// singleflightGroup 简化版 singleflight，避免引入 golang.org/x/sync 依赖。
type singleflightGroup struct {
	mu sync.Mutex
	m  map[string]*sfCall
}

type sfCall struct {
	wg  sync.WaitGroup
	val interface{}
	err error
}

func (g *singleflightGroup) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*sfCall)
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &sfCall{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err
}

// NewBasePoolManager 创建通用池子管理器。
func NewBasePoolManager(cache *LRUPoolCache, parser PoolParser, repo Repository) *BasePoolManager {
	return &BasePoolManager{
		cache:  cache,
		parser: parser,
		repo:   repo,
	}
}

func (m *BasePoolManager) GetPool(ctx context.Context, address string) (*Pool, error) {
	// 先查缓存
	if pool, ok := m.cache.Get(address); ok {
		return pool, nil
	}

	// P0-2: singleflight 防止并发穿透——多个 goroutine 请求同一个池子只查一次存储
	val, err := m.sfGroup.Do(address, func() (interface{}, error) {
		// double check: 可能在等待期间另一个请求已经填充了缓存
		if pool, ok := m.cache.Get(address); ok {
			return pool, nil
		}
		pool, err := m.repo.GetPool(ctx, address)
		if err != nil {
			return nil, fmt.Errorf("get pool %s: %w", address, err)
		}
		m.cache.Put(pool)
		return pool, nil
	})
	if err != nil {
		return nil, err
	}
	return val.(*Pool), nil
}

// poolScore 计算池子综合评分（P1-4: 同时考虑流动性和费率）。
// score = liquidity_normalized * 0.7 + fee_rate_inverted * 0.3
// 分数越高越优。
func poolScore(p *Pool) int64 {
	if p.Liquidity == nil {
		return 0
	}
	// 流动性分数：直接用流动性值（大的更优）
	liqScore := p.Liquidity.Int64()
	// 费率分数：费率越低越好，用 10000 - feeRate 作为分数
	feeScore := int64(10000 - p.FeeRate)
	if feeScore < 0 {
		feeScore = 0
	}
	// 综合评分（简化：流动性权重高，费率权重低）
	return liqScore + feeScore*1000
}

func (m *BasePoolManager) GetBestPool(ctx context.Context, baseMint, quoteMint string) (*Pool, error) {
	pools := m.cache.GetByPair(baseMint, quoteMint)
	if len(pools) == 0 {
		return nil, &PoolNotFoundError{BaseMint: baseMint, QuoteMint: quoteMint}
	}

	// P1-4: 按综合评分排序（流动性 + 费率），不再只看流动性
	var best *Pool
	var bestScore int64 = -1
	for _, p := range pools {
		if p.State != PoolStateActive {
			continue
		}
		score := poolScore(p)
		if score > bestScore {
			bestScore = score
			best = p
		}
	}

	if best == nil {
		return nil, &PoolNotFoundError{BaseMint: baseMint, QuoteMint: quoteMint}
	}

	return best, nil
}

func (m *BasePoolManager) UpdatePool(ctx context.Context, pool *Pool) error {
	pool.UpdatedAt = time.Now()
	m.cache.Put(pool)
	return m.repo.SavePool(ctx, pool)
}

func (m *BasePoolManager) RefreshCache(ctx context.Context) error {
	evicted := m.cache.Evict()
	if evicted > 0 {
		slog.Info("pool cache refreshed", "evicted", evicted)
	}
	return nil
}
