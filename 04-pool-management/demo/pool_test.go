package main

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// 辅助函数
// ============================================================

// newTestPool 创建一个测试用的池子。
func newTestPool(address string, dexID dexwallet.DexID, liquidity int64, feeRate uint64) *dexwallet.Pool {
	return &dexwallet.Pool{
		Address:      address,
		DexID:        dexID,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     "SOL_MINT",
		QuoteMint:    "USDC_MINT",
		BaseSymbol:   "SOL",
		QuoteSymbol:  "USDC",
		BaseDecimal:  9,
		QuoteDecimal: 6,
		Liquidity:    new(big.Int).Mul(big.NewInt(liquidity), big.NewInt(1e9)),
		FeeRate:      feeRate,
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
	}
}

// newTestManager 创建一个测试用的池子管理器。
func newTestManager(capacity int, ttl time.Duration) *PoolManagerDemo {
	cfg := DefaultPoolManagerConfig()
	cfg.CacheCapacity = capacity
	cfg.CacheTTL = ttl
	return NewPoolManagerDemo(cfg)
}

// ============================================================
// 缓存命中/失效测试
// ============================================================

func TestCacheHit(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()
	pool := newTestPool("pool_hit_001", dexwallet.DexRaydiumAMM, 1000, 25)

	// 添加到管理器
	if err := pm.AddPool(ctx, pool); err != nil {
		t.Fatalf("add pool failed: %v", err)
	}

	// 第一次查询应该命中缓存
	got, err := pm.GetPool(ctx, "pool_hit_001")
	if err != nil {
		t.Fatalf("get pool failed: %v", err)
	}
	if got.Address != "pool_hit_001" {
		t.Errorf("expected address pool_hit_001, got %s", got.Address)
	}
	if got.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("expected dex_id %s, got %s", dexwallet.DexRaydiumAMM, got.DexID)
	}
}

func TestCacheMissWithRepoFallback(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()
	pool := newTestPool("pool_miss_001", dexwallet.DexRaydiumAMM, 1000, 25)

	// 只保存到存储，不加入缓存
	if err := pm.repo.SavePool(ctx, pool); err != nil {
		t.Fatalf("save to repo failed: %v", err)
	}

	// 查询应该从存储回源
	got, err := pm.GetPool(ctx, "pool_miss_001")
	if err != nil {
		t.Fatalf("get pool failed: %v", err)
	}
	if got.Address != "pool_miss_001" {
		t.Errorf("expected address pool_miss_001, got %s", got.Address)
	}

	// 回源后应该在缓存中了
	if pm.CacheSize() != 1 {
		t.Errorf("expected cache size 1 after fallback, got %d", pm.CacheSize())
	}
}

func TestCacheMissAndRepoMiss(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	// 查询不存在的池子
	_, err := pm.GetPool(ctx, "nonexistent_pool")
	if err == nil {
		t.Fatal("expected error for nonexistent pool, got nil")
	}
}

// ============================================================
// LRU 淘汰测试（超容量）
// ============================================================

func TestLRUEvictionOnCapacity(t *testing.T) {
	pm := newTestManager(3, 1*time.Minute)
	ctx := context.Background()

	pools := []*dexwallet.Pool{
		newTestPool("lru_001", dexwallet.DexRaydiumAMM, 1000, 25),
		newTestPool("lru_002", dexwallet.DexPumpFun, 2000, 30),
		newTestPool("lru_003", dexwallet.DexMeteoraAMM, 3000, 20),
	}

	// 填满缓存（容量 3）
	for _, p := range pools {
		if err := pm.AddPool(ctx, p); err != nil {
			t.Fatalf("add pool failed: %v", err)
		}
	}

	if pm.CacheSize() != 3 {
		t.Fatalf("expected cache size 3, got %d", pm.CacheSize())
	}

	// 访问 lru_001，使其变为最近使用
	_, _ = pm.GetPool(ctx, "lru_001")

	// 添加第 4 个池子，应该淘汰最久未使用的（lru_002，因为 lru_001 刚被访问）
	newPool := newTestPool("lru_004", dexwallet.DexUniswapV2, 4000, 30)
	newPool.ChainID = coinset.ChainEthereum
	if err := pm.AddPool(ctx, newPool); err != nil {
		t.Fatalf("add pool failed: %v", err)
	}

	if pm.CacheSize() != 3 {
		t.Fatalf("expected cache size 3 after eviction, got %d", pm.CacheSize())
	}

	// lru_002 应该被淘汰（最久未使用）
	_, ok := pm.Cache().Get("lru_002")
	if ok {
		t.Error("expected lru_002 to be evicted from cache")
	}

	// lru_001 应该仍在缓存中（最近被访问）
	_, ok = pm.Cache().Get("lru_001")
	if !ok {
		t.Error("expected lru_001 to still be in cache")
	}

	// lru_004 应该在缓存中（刚添加）
	_, ok = pm.Cache().Get("lru_004")
	if !ok {
		t.Error("expected lru_004 to be in cache")
	}

	// 被淘汰的池子仍然在存储中
	_, err := pm.repo.GetPool(ctx, "lru_002")
	if err != nil {
		t.Errorf("expected lru_002 still in repo, got error: %v", err)
	}
}

// ============================================================
// TTL 过期测试
// ============================================================

func TestTTLExpiration(t *testing.T) {
	pm := newTestManager(10, 200*time.Millisecond) // TTL 200ms
	ctx := context.Background()
	pool := newTestPool("ttl_001", dexwallet.DexRaydiumAMM, 1000, 25)

	if err := pm.AddPool(ctx, pool); err != nil {
		t.Fatalf("add pool failed: %v", err)
	}

	// 立即查询应该命中
	_, ok := pm.Cache().Get("ttl_001")
	if !ok {
		t.Fatal("expected cache hit immediately after add")
	}

	// 等待 TTL 过期
	time.Sleep(300 * time.Millisecond)

	// 缓存应该未命中（TTL 过期）
	_, ok = pm.Cache().Get("ttl_001")
	if ok {
		t.Fatal("expected cache miss after TTL expiration")
	}

	// 但通过 GetPool 查询应该从存储回源
	got, err := pm.GetPool(ctx, "ttl_001")
	if err != nil {
		t.Fatalf("expected repo fallback to succeed, got error: %v", err)
	}
	if got.Address != "ttl_001" {
		t.Errorf("expected address ttl_001, got %s", got.Address)
	}
}

func TestEvictExpiredEntries(t *testing.T) {
	pm := newTestManager(10, 200*time.Millisecond)
	ctx := context.Background()

	// 添加 3 个池子
	for i := 0; i < 3; i++ {
		p := newTestPool(
			"evict_"+string(rune('a'+i)),
			dexwallet.DexRaydiumAMM, int64((i+1)*1000), 25,
		)
		_ = pm.AddPool(ctx, p)
	}

	if pm.CacheSize() != 3 {
		t.Fatalf("expected cache size 3, got %d", pm.CacheSize())
	}

	// 等待 TTL 过期
	time.Sleep(300 * time.Millisecond)

	// 手动调用 Evict
	evicted := pm.Cache().Evict()
	if evicted != 3 {
		t.Errorf("expected 3 entries evicted, got %d", evicted)
	}

	if pm.CacheSize() != 0 {
		t.Errorf("expected cache size 0 after eviction, got %d", pm.CacheSize())
	}
}

// ============================================================
// 池子状态切换测试
// ============================================================

func TestSetPoolStateToNeedUpdate(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()
	pool := newTestPool("state_001", dexwallet.DexRaydiumAMM, 1000, 25)

	_ = pm.AddPool(ctx, pool)

	// active -> need_update
	err := pm.SetPoolState(ctx, "state_001", dexwallet.PoolStateNeedUpdate)
	if err != nil {
		t.Fatalf("set pool state failed: %v", err)
	}

	got, _ := pm.GetPool(ctx, "state_001")
	if got.State != dexwallet.PoolStateNeedUpdate {
		t.Errorf("expected state need_update, got %s", got.State)
	}
}

func TestSetPoolStateToInactive(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()
	pool := newTestPool("state_002", dexwallet.DexRaydiumAMM, 1000, 25)

	_ = pm.AddPool(ctx, pool)

	// active -> inactive
	err := pm.SetPoolState(ctx, "state_002", dexwallet.PoolStateInactive)
	if err != nil {
		t.Fatalf("set pool state failed: %v", err)
	}

	// inactive 的池子应该从缓存中移除
	_, ok := pm.Cache().Get("state_002")
	if ok {
		t.Error("expected inactive pool to be removed from cache")
	}

	// 但仍在存储中
	got, err := pm.repo.GetPool(ctx, "state_002")
	if err != nil {
		t.Fatalf("expected pool still in repo, got error: %v", err)
	}
	if got.State != dexwallet.PoolStateInactive {
		t.Errorf("expected state inactive in repo, got %s", got.State)
	}
}

func TestSetPoolStateNonexistent(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	err := pm.SetPoolState(ctx, "nonexistent", dexwallet.PoolStateActive)
	if err == nil {
		t.Fatal("expected error for nonexistent pool, got nil")
	}
}

// ============================================================
// GetBestPool 选择正确性测试
// ============================================================

func TestGetBestPoolSingleCandidate(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	pool := newTestPool("best_single_001", dexwallet.DexRaydiumAMM, 1000, 25)
	_ = pm.AddPool(ctx, pool)

	best, err := pm.GetBestPool(ctx, "SOL_MINT", "USDC_MINT")
	if err != nil {
		t.Fatalf("get best pool failed: %v", err)
	}
	if best.Address != "best_single_001" {
		t.Errorf("expected address best_single_001, got %s", best.Address)
	}
}

func TestGetBestPoolHighLiquidityWins(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	// 两个池子，费率相同，流动性不同
	small := newTestPool("best_small", dexwallet.DexRaydiumAMM, 1000, 25)
	large := newTestPool("best_large", dexwallet.DexRaydiumCLMM, 10000, 25)

	_ = pm.AddPool(ctx, small)
	_ = pm.AddPool(ctx, large)

	best, err := pm.GetBestPool(ctx, "SOL_MINT", "USDC_MINT")
	if err != nil {
		t.Fatalf("get best pool failed: %v", err)
	}
	if best.Address != "best_large" {
		t.Errorf("expected best_large (higher liquidity), got %s", best.Address)
	}
}

func TestGetBestPoolLowFeeWins(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	// 两个池子，流动性相同，费率不同
	highFee := newTestPool("best_highfee", dexwallet.DexRaydiumAMM, 5000, 100) // 1%
	lowFee := newTestPool("best_lowfee", dexwallet.DexRaydiumCLMM, 5000, 1)    // 0.01%

	_ = pm.AddPool(ctx, highFee)
	_ = pm.AddPool(ctx, lowFee)

	best, err := pm.GetBestPool(ctx, "SOL_MINT", "USDC_MINT")
	if err != nil {
		t.Fatalf("get best pool failed: %v", err)
	}
	if best.Address != "best_lowfee" {
		t.Errorf("expected best_lowfee (lower fee), got %s", best.Address)
	}
}

func TestGetBestPoolComprehensiveScore(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	// 池子 A: 流动性大但费率高
	poolA := newTestPool("best_A", dexwallet.DexRaydiumAMM, 10000, 100) // 10000 SOL, 1%
	// 池子 B: 流动性中等，费率低
	poolB := newTestPool("best_B", dexwallet.DexRaydiumCLMM, 8000, 1) // 8000 SOL, 0.01%

	_ = pm.AddPool(ctx, poolA)
	_ = pm.AddPool(ctx, poolB)

	best, err := pm.GetBestPool(ctx, "SOL_MINT", "USDC_MINT")
	if err != nil {
		t.Fatalf("get best pool failed: %v", err)
	}

	// 池子 B 应该胜出:
	// A 的流动性评分: 10000/10000 * 10000 = 10000, 费率评分: 1/100 * 10000 = 100
	// A 综合: 70 * 10000 + 30 * 100 = 700000 + 3000 = 703000
	// B 的流动性评分: 8000/10000 * 10000 = 8000, 费率评分: 1/1 * 10000 = 10000
	// B 综合: 70 * 8000 + 30 * 10000 = 560000 + 300000 = 860000
	if best.Address != "best_B" {
		t.Errorf("expected best_B (better comprehensive score), got %s", best.Address)
	}
}

func TestGetBestPoolExcludesInactive(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	active := newTestPool("best_active", dexwallet.DexRaydiumAMM, 1000, 25)
	inactive := newTestPool("best_inactive", dexwallet.DexRaydiumCLMM, 50000, 1)
	inactive.State = dexwallet.PoolStateInactive

	_ = pm.AddPool(ctx, active)
	_ = pm.AddPool(ctx, inactive)

	best, err := pm.GetBestPool(ctx, "SOL_MINT", "USDC_MINT")
	if err != nil {
		t.Fatalf("get best pool failed: %v", err)
	}
	if best.Address != "best_active" {
		t.Errorf("expected best_active (inactive should be excluded), got %s", best.Address)
	}
}

func TestGetBestPoolExcludesZeroLiquidity(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	normal := newTestPool("best_normal", dexwallet.DexRaydiumAMM, 1000, 25)
	empty := newTestPool("best_empty", dexwallet.DexRaydiumCLMM, 0, 1)

	_ = pm.AddPool(ctx, normal)
	_ = pm.AddPool(ctx, empty)

	best, err := pm.GetBestPool(ctx, "SOL_MINT", "USDC_MINT")
	if err != nil {
		t.Fatalf("get best pool failed: %v", err)
	}
	if best.Address != "best_normal" {
		t.Errorf("expected best_normal (zero liquidity excluded), got %s", best.Address)
	}
}

func TestGetBestPoolNoPools(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	_, err := pm.GetBestPool(ctx, "NONEXISTENT_BASE", "NONEXISTENT_QUOTE")
	if err == nil {
		t.Fatal("expected error for no pools, got nil")
	}
}

func TestGetBestPoolAllInactive(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	p1 := newTestPool("all_inactive_1", dexwallet.DexRaydiumAMM, 1000, 25)
	p1.State = dexwallet.PoolStateInactive
	p2 := newTestPool("all_inactive_2", dexwallet.DexRaydiumCLMM, 2000, 30)
	p2.State = dexwallet.PoolStateInactive

	_ = pm.AddPool(ctx, p1)
	_ = pm.AddPool(ctx, p2)

	_, err := pm.GetBestPool(ctx, "SOL_MINT", "USDC_MINT")
	if err == nil {
		t.Fatal("expected error when all pools are inactive, got nil")
	}
}

// ============================================================
// 并发读写安全测试
// ============================================================

func TestConcurrentReadWrite(t *testing.T) {
	pm := newTestManager(200, 30*time.Second)
	ctx := context.Background()

	// 预先添加池子：读操作使用 read_ 前缀，写操作使用 write_ 前缀
	// 分开地址空间避免触发 LRUPoolCache 内部 Get/Put 之间的竞态
	// （这是库代码的已知限制，notes.md 中有说明）
	for i := 0; i < 20; i++ {
		p := newTestPool(
			"read_"+fmt.Sprintf("%03d", i),
			dexwallet.DexRaydiumAMM,
			int64((i+1)*1000),
			uint64(25+i),
		)
		_ = pm.AddPool(ctx, p)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 200)

	// 并发读取（只读已有池子）
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			addr := "read_" + fmt.Sprintf("%03d", idx%20)
			_, err := pm.GetPool(ctx, addr)
			if err != nil {
				errCh <- err
			}
		}(i)
	}

	// 并发写入新池子（不与读操作共享地址）
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := newTestPool(
				"write_"+fmt.Sprintf("%03d", idx),
				dexwallet.DexRaydiumAMM,
				int64((idx+1)*500),
				uint64(20+idx),
			)
			err := pm.AddPool(ctx, p)
			if err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	// 收集错误
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		t.Errorf("concurrent operations produced %d errors, first: %v", len(errs), errs[0])
	}

	// 验证所有写入的池子都在存储中
	for i := 0; i < 50; i++ {
		addr := "write_" + fmt.Sprintf("%03d", i)
		_, err := pm.repo.GetPool(ctx, addr)
		if err != nil {
			t.Errorf("expected pool %s in repo, got error: %v", addr, err)
		}
	}
}

func TestConcurrentGetBestPool(t *testing.T) {
	pm := newTestManager(100, 30*time.Second)
	ctx := context.Background()

	// 添加多个同交易对的池子
	for i := 0; i < 5; i++ {
		p := newTestPool(
			"concurrent_best_"+fmt.Sprintf("%03d", i),
			dexwallet.DexRaydiumAMM,
			int64((i+1)*1000),
			uint64(25+i*5),
		)
		_ = pm.AddPool(ctx, p)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 50)

	// 并发调用 GetBestPool
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := pm.GetBestPool(ctx, "SOL_MINT", "USDC_MINT")
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		t.Errorf("concurrent GetBestPool produced %d errors, first: %v", len(errs), errs[0])
	}
}

// ============================================================
// MemoryPoolRepository 测试
// ============================================================

func TestRepositorySaveAndGet(t *testing.T) {
	repo := NewMemoryPoolRepository()
	ctx := context.Background()

	pool := newTestPool("repo_001", dexwallet.DexRaydiumAMM, 1000, 25)
	if err := repo.SavePool(ctx, pool); err != nil {
		t.Fatalf("save pool failed: %v", err)
	}

	got, err := repo.GetPool(ctx, "repo_001")
	if err != nil {
		t.Fatalf("get pool failed: %v", err)
	}
	if got.Address != "repo_001" {
		t.Errorf("expected address repo_001, got %s", got.Address)
	}
}

func TestRepositoryGetNonexistent(t *testing.T) {
	repo := NewMemoryPoolRepository()
	ctx := context.Background()

	_, err := repo.GetPool(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent pool, got nil")
	}
}

func TestRepositorySaveNilPool(t *testing.T) {
	repo := NewMemoryPoolRepository()
	ctx := context.Background()

	err := repo.SavePool(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil pool, got nil")
	}
}

func TestRepositorySaveEmptyAddress(t *testing.T) {
	repo := NewMemoryPoolRepository()
	ctx := context.Background()

	pool := &dexwallet.Pool{Address: ""}
	err := repo.SavePool(ctx, pool)
	if err == nil {
		t.Fatal("expected error for empty address, got nil")
	}
}

func TestRepositoryOverwrite(t *testing.T) {
	repo := NewMemoryPoolRepository()
	ctx := context.Background()

	pool := newTestPool("repo_overwrite", dexwallet.DexRaydiumAMM, 1000, 25)
	_ = repo.SavePool(ctx, pool)

	// 覆盖更新
	pool.Liquidity = new(big.Int).Mul(big.NewInt(2000), big.NewInt(1e9))
	_ = repo.SavePool(ctx, pool)

	got, _ := repo.GetPool(ctx, "repo_overwrite")
	expected := new(big.Int).Mul(big.NewInt(2000), big.NewInt(1e9))
	if got.Liquidity.Cmp(expected) != 0 {
		t.Errorf("expected liquidity %s, got %s", expected.String(), got.Liquidity.String())
	}
}

func TestRepositoryCopyIsolation(t *testing.T) {
	repo := NewMemoryPoolRepository()
	ctx := context.Background()

	pool := newTestPool("repo_copy", dexwallet.DexRaydiumAMM, 1000, 25)
	_ = repo.SavePool(ctx, pool)

	// 修改原始对象，不应影响存储中的数据
	pool.Liquidity = big.NewInt(999)

	got, _ := repo.GetPool(ctx, "repo_copy")
	expected := new(big.Int).Mul(big.NewInt(1000), big.NewInt(1e9))
	if got.Liquidity.Cmp(expected) != 0 {
		t.Errorf("expected stored liquidity unchanged, got %s", got.Liquidity.String())
	}
}

func TestRepositoryTxRecord(t *testing.T) {
	repo := NewMemoryPoolRepository()
	ctx := context.Background()

	record := &dexwallet.TxRecord{
		TxHash:  "tx_001",
		ChainID: coinset.ChainSolana,
		DexID:   dexwallet.DexRaydiumAMM,
		Status:  dexwallet.TxStatusPending,
	}

	if err := repo.SaveTxRecord(ctx, record); err != nil {
		t.Fatalf("save tx record failed: %v", err)
	}

	got, err := repo.GetTxRecord(ctx, "tx_001")
	if err != nil {
		t.Fatalf("get tx record failed: %v", err)
	}
	if got.Status != dexwallet.TxStatusPending {
		t.Errorf("expected status pending, got %s", got.Status)
	}

	// 更新状态
	if err := repo.UpdateTxStatus(ctx, "tx_001", dexwallet.TxStatusConfirmed); err != nil {
		t.Fatalf("update tx status failed: %v", err)
	}

	got, _ = repo.GetTxRecord(ctx, "tx_001")
	if got.Status != dexwallet.TxStatusConfirmed {
		t.Errorf("expected status confirmed, got %s", got.Status)
	}
}

// ============================================================
// UpdatePool 测试
// ============================================================

func TestUpdatePool(t *testing.T) {
	pm := newTestManager(10, 30*time.Second)
	ctx := context.Background()

	pool := newTestPool("update_001", dexwallet.DexRaydiumAMM, 1000, 25)
	_ = pm.AddPool(ctx, pool)

	// 更新流动性
	pool.Liquidity = new(big.Int).Mul(big.NewInt(2000), big.NewInt(1e9))
	if err := pm.UpdatePool(ctx, pool); err != nil {
		t.Fatalf("update pool failed: %v", err)
	}

	got, _ := pm.GetPool(ctx, "update_001")
	expected := new(big.Int).Mul(big.NewInt(2000), big.NewInt(1e9))
	if got.Liquidity.Cmp(expected) != 0 {
		t.Errorf("expected updated liquidity %s, got %s", expected.String(), got.Liquidity.String())
	}

	// UpdatedAt 应该被更新
	if got.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

// ============================================================
// RefreshCache 测试
// ============================================================

func TestRefreshCache(t *testing.T) {
	pm := newTestManager(10, 200*time.Millisecond)
	ctx := context.Background()

	// 添加池子
	for i := 0; i < 5; i++ {
		p := newTestPool(
			"refresh_"+fmt.Sprintf("%03d", i),
			dexwallet.DexRaydiumAMM,
			int64((i+1)*1000),
			25,
		)
		_ = pm.AddPool(ctx, p)
	}

	if pm.CacheSize() != 5 {
		t.Fatalf("expected cache size 5, got %d", pm.CacheSize())
	}

	// 等待 TTL 过期
	time.Sleep(300 * time.Millisecond)

	// RefreshCache 应该清除过期条目
	if err := pm.RefreshCache(ctx); err != nil {
		t.Fatalf("refresh cache failed: %v", err)
	}

	if pm.CacheSize() != 0 {
		t.Errorf("expected cache size 0 after refresh, got %d", pm.CacheSize())
	}
}

// ============================================================
// 接口兼容性测试
// ============================================================

func TestPoolManagerDemoImplementsInterface(t *testing.T) {
	var _ dexwallet.PoolManager = (*PoolManagerDemo)(nil)
}

func TestMemoryPoolRepositoryImplementsInterface(t *testing.T) {
	var _ dexwallet.Repository = (*MemoryPoolRepository)(nil)
}

// ============================================================
// Benchmark 测试
// ============================================================

func BenchmarkPoolCacheGet(b *testing.B) {
	pm := newTestManager(1000, 30*time.Second)
	ctx := context.Background()

	// 预填充缓存
	for i := 0; i < 100; i++ {
		p := newTestPool(fmt.Sprintf("bench_get_%03d", i), dexwallet.DexRaydiumAMM, int64((i+1)*1000), 25)
		_ = pm.AddPool(ctx, p)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pm.Cache().Get(fmt.Sprintf("bench_get_%03d", i%100))
	}
}

func BenchmarkPoolCacheGetByPair(b *testing.B) {
	pm := newTestManager(1000, 30*time.Second)
	ctx := context.Background()

	// 预填充缓存，所有池子共享相同的交易对 (SOL_MINT / USDC_MINT)
	for i := 0; i < 100; i++ {
		p := newTestPool(fmt.Sprintf("bench_pair_%03d", i), dexwallet.DexRaydiumAMM, int64((i+1)*1000), 25)
		_ = pm.AddPool(ctx, p)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pm.Cache().GetByPair("SOL_MINT", "USDC_MINT")
	}
}

