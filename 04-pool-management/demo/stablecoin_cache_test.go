package main

import (
	"math/big"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// StablecoinSet 单元测试
// ============================================================

// ----- IsStablecoin 判断 -----

// TestStablecoinSet_IsStablecoin 验证已添加的稳定币能被正确识别。
func TestStablecoinSet_IsStablecoin(t *testing.T) {
	s := NewStablecoinSet()
	s.Add("USDT_MINT", StablecoinInfo{Symbol: "USDT", Decimals: 6, Category: "usd_pegged"})
	s.Add("USDC_MINT", StablecoinInfo{Symbol: "USDC", Decimals: 6, Category: "usd_pegged"})

	if !s.IsStablecoin("USDT_MINT") {
		t.Error("USDT_MINT 应被识别为稳定币")
	}
	if !s.IsStablecoin("USDC_MINT") {
		t.Error("USDC_MINT 应被识别为稳定币")
	}
}

// TestStablecoinSet_IsNotStablecoin 验证非稳定币不会被误判。
func TestStablecoinSet_IsNotStablecoin(t *testing.T) {
	s := NewStablecoinSet()
	s.Add("USDT_MINT", StablecoinInfo{Symbol: "USDT", Decimals: 6, Category: "usd_pegged"})

	if s.IsStablecoin("SOL_MINT") {
		t.Error("SOL_MINT 不应被识别为稳定币")
	}
	if s.IsStablecoin("MEME_MINT") {
		t.Error("MEME_MINT 不应被识别为稳定币")
	}
}

// TestStablecoinSet_IsStablePair 验证两边都是稳定币时判定为稳定币对。
func TestStablecoinSet_IsStablePair(t *testing.T) {
	s := NewStablecoinSet()
	s.Add("USDT_MINT", StablecoinInfo{Symbol: "USDT", Decimals: 6, Category: "usd_pegged"})
	s.Add("USDC_MINT", StablecoinInfo{Symbol: "USDC", Decimals: 6, Category: "usd_pegged"})

	if !s.IsStablePair("USDT_MINT", "USDC_MINT") {
		t.Error("USDT/USDC 应被识别为稳定币对")
	}
	if s.IsStablePair("USDT_MINT", "SOL_MINT") {
		t.Error("USDT/SOL 不应被识别为稳定币对")
	}
	if s.IsStablePair("SOL_MINT", "MEME_MINT") {
		t.Error("SOL/MEME 不应被识别为稳定币对")
	}
}

// TestStablecoinSet_DefaultSet 验证默认稳定币集合包含预期的稳定币。
func TestStablecoinSet_DefaultSet(t *testing.T) {
	s := DefaultStablecoinSet()

	// Solana USDT
	if !s.IsStablecoin("Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB") {
		t.Error("默认集合应包含 Solana USDT")
	}
	// Solana USDC
	if !s.IsStablecoin("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v") {
		t.Error("默认集合应包含 Solana USDC")
	}
	// EVM USDT
	if !s.IsStablecoin("0xdAC17F958D2ee523a2206206994597C13D831ec7") {
		t.Error("默认集合应包含 EVM USDT")
	}

	// 非稳定币
	if s.IsStablecoin("SomeRandomMint123") {
		t.Error("随机地址不应被识别为稳定币")
	}
}

// ============================================================
// TieredPoolCache（StablecoinCache 分层缓存）单元测试
// ============================================================

// 辅助函数：创建测试用的稳定币集合和池子缓存
func setupTestTieredCache() (*StablecoinSet, *TieredPoolCache) {
	stablecoins := NewStablecoinSet()
	stablecoins.Add("USDT_MINT", StablecoinInfo{Symbol: "USDT", Decimals: 6, Category: "usd_pegged"})
	stablecoins.Add("USDC_MINT", StablecoinInfo{Symbol: "USDC", Decimals: 6, Category: "usd_pegged"})

	cache := NewTieredPoolCache(stablecoins, 5*time.Second, 30*time.Second)
	return stablecoins, cache
}

// 辅助函数：创建测试池子
func makeTestStablecoinPool(address, baseMint, quoteMint string) *dexwallet.Pool {
	return &dexwallet.Pool{
		Address:   address,
		DexID:     dexwallet.DexRaydiumAMM,
		BaseMint:  baseMint,
		QuoteMint: quoteMint,
		Liquidity: big.NewInt(1_000_000),
		State:     dexwallet.PoolStateActive,
		UpdatedAt: time.Now(),
	}
}

// ----- 不同 TTL 分层 -----

// TestStablecoinCache_StableStableGoesToStableCache 验证稳定币对进入 stableCache。
func TestStablecoinCache_StableStableGoesToStableCache(t *testing.T) {
	_, cache := setupTestTieredCache()

	pool := makeTestStablecoinPool("pool_usdt_usdc", "USDT_MINT", "USDC_MINT")
	cache.Put(pool)

	// 验证分类
	tier := cache.ClassifyPool(pool)
	if tier != string(PoolTierStableStable) {
		t.Errorf("USDT/USDC 应分类为 stable-stable，实际为 %s", tier)
	}

	// 应该能命中
	got, ok := cache.Get("pool_usdt_usdc")
	if !ok {
		t.Fatal("缓存应命中")
	}
	if got.Address != "pool_usdt_usdc" {
		t.Errorf("地址不匹配: 期望 pool_usdt_usdc，实际为 %s", got.Address)
	}
}

// TestStablecoinCache_StableNormalGoesToStableCache 验证一边是稳定币的池子进入 stableCache。
func TestStablecoinCache_StableNormalGoesToStableCache(t *testing.T) {
	_, cache := setupTestTieredCache()

	pool := makeTestStablecoinPool("pool_sol_usdt", "SOL_MINT", "USDT_MINT")
	cache.Put(pool)

	tier := cache.ClassifyPool(pool)
	if tier != string(PoolTierStableNormal) {
		t.Errorf("SOL/USDT 应分类为 stable-normal，实际为 %s", tier)
	}

	got, ok := cache.Get("pool_sol_usdt")
	if !ok {
		t.Fatal("缓存应命中")
	}
	if got.Address != "pool_sol_usdt" {
		t.Errorf("地址不匹配")
	}
}

// TestStablecoinCache_NormalNormalGoesToNormalCache 验证普通池子进入 normalCache。
func TestStablecoinCache_NormalNormalGoesToNormalCache(t *testing.T) {
	_, cache := setupTestTieredCache()

	pool := makeTestStablecoinPool("pool_sol_meme", "SOL_MINT", "MEME_MINT")
	cache.Put(pool)

	tier := cache.ClassifyPool(pool)
	if tier != string(PoolTierNormalNormal) {
		t.Errorf("SOL/MEME 应分类为 normal-normal，实际为 %s", tier)
	}

	got, ok := cache.Get("pool_sol_meme")
	if !ok {
		t.Fatal("缓存应命中")
	}
	if got.Address != "pool_sol_meme" {
		t.Errorf("地址不匹配")
	}
}

// TestStablecoinCache_NormalTTLExpires 验证普通池子的短 TTL 过期。
func TestStablecoinCache_NormalTTLExpires(t *testing.T) {
	stablecoins := NewStablecoinSet()
	stablecoins.Add("USDT_MINT", StablecoinInfo{Symbol: "USDT", Decimals: 6, Category: "usd_pegged"})

	// normalTTL = 100ms, stableTTL = 1s
	cache := NewTieredPoolCache(stablecoins, 100*time.Millisecond, 1*time.Second)

	// 普通池子
	normalPool := makeTestStablecoinPool("pool_normal", "SOL_MINT", "MEME_MINT")
	cache.Put(normalPool)

	// 稳定币池
	stablePool := makeTestStablecoinPool("pool_stable", "SOL_MINT", "USDT_MINT")
	cache.Put(stablePool)

	// 等待 normalTTL 过期但 stableTTL 未过期
	time.Sleep(200 * time.Millisecond)

	// 普通池子应该过期
	_, ok := cache.Get("pool_normal")
	if ok {
		t.Error("普通池子应在 100ms 后过期")
	}

	// 稳定币池应该仍然有效
	_, ok = cache.Get("pool_stable")
	if !ok {
		t.Error("稳定币池在 200ms 时不应过期（TTL 1s）")
	}
}

// TestStablecoinCache_Stats 验证统计信息的正确性。
func TestStablecoinCache_Stats(t *testing.T) {
	_, cache := setupTestTieredCache()

	// 添加池子
	cache.Put(makeTestStablecoinPool("pool_a", "USDT_MINT", "USDC_MINT"))
	cache.Put(makeTestStablecoinPool("pool_b", "SOL_MINT", "MEME_MINT"))

	// 命中一次
	cache.Get("pool_a")
	// 未命中一次
	cache.Get("nonexistent")

	stats := cache.Stats()

	if stats.StableSize != 1 {
		t.Errorf("稳定币缓存大小应为 1，实际为 %d", stats.StableSize)
	}
	if stats.NormalSize != 1 {
		t.Errorf("普通缓存大小应为 1，实际为 %d", stats.NormalSize)
	}
	if stats.TotalHits != 1 {
		t.Errorf("命中次数应为 1，实际为 %d", stats.TotalHits)
	}
	if stats.TotalMisses != 1 {
		t.Errorf("未命中次数应为 1，实际为 %d", stats.TotalMisses)
	}
}

// TestStablecoinCache_Invalidate 验证手动失效单个缓存条目。
func TestStablecoinCache_Invalidate(t *testing.T) {
	_, cache := setupTestTieredCache()

	pool := makeTestStablecoinPool("pool_to_invalidate", "USDT_MINT", "USDC_MINT")
	cache.Put(pool)

	// 失效前应命中
	_, ok := cache.Get("pool_to_invalidate")
	if !ok {
		t.Fatal("失效前应命中")
	}

	cache.Invalidate("pool_to_invalidate")

	// 失效后应未命中
	_, ok = cache.Get("pool_to_invalidate")
	if ok {
		t.Error("失效后不应命中")
	}
}

// TestStablecoinCache_PutNilPool 验证 Put nil 不会 panic。
func TestStablecoinCache_PutNilPool(t *testing.T) {
	_, cache := setupTestTieredCache()

	// 不应 panic
	cache.Put(nil)

	// 空地址也应跳过
	emptyPool := &dexwallet.Pool{Address: ""}
	cache.Put(emptyPool)

	stats := cache.Stats()
	if stats.NormalSize+stats.StableSize != 0 {
		t.Error("nil 或空地址池子不应被缓存")
	}
}

// TestStablecoinCache_ClassifyNilPool 验证 nil 池子分类为 normal-normal。
func TestStablecoinCache_ClassifyNilPool(t *testing.T) {
	_, cache := setupTestTieredCache()

	tier := cache.ClassifyPool(nil)
	if tier != string(PoolTierNormalNormal) {
		t.Errorf("nil 池子应分类为 normal-normal，实际为 %s", tier)
	}
}

// ----- CheckDepeg 脱锚检测 -----

// TestStablecoinCache_CheckDepeg 验证脱锚检测逻辑。
func TestStablecoinCache_CheckDepeg(t *testing.T) {
	_, cache := setupTestTieredCache()

	tests := []struct {
		name         string
		price        float64
		thresholdBps uint64
		expectDepeg  bool
	}{
		{
			name:         "正常价格 1.00",
			price:        1.00,
			thresholdBps: 500, // 5%
			expectDepeg:  false,
		},
		{
			name:         "轻微偏离 0.99（1% 偏离，阈值 5%）",
			price:        0.99,
			thresholdBps: 500,
			expectDepeg:  false,
		},
		{
			name:         "严重脱锚 0.90（10% 偏离，阈值 5%）",
			price:        0.90,
			thresholdBps: 500,
			expectDepeg:  true,
		},
		{
			name:         "向上脱锚 1.10（10% 偏离，阈值 5%）",
			price:        1.10,
			thresholdBps: 500,
			expectDepeg:  true,
		},
		{
			name:         "刚好在阈值边界（5% 偏离，阈值 5%）",
			price:        0.95,
			thresholdBps: 500,
			expectDepeg:  false,
		},
		{
			name:         "超过严格阈值（2% 偏离，阈值 1%）",
			price:        0.98,
			thresholdBps: 100, // 1%
			expectDepeg:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cache.CheckDepeg(tt.price, tt.thresholdBps)
			if result != tt.expectDepeg {
				t.Errorf("CheckDepeg(%.2f, %d) = %v，期望 %v",
					tt.price, tt.thresholdBps, result, tt.expectDepeg)
			}
		})
	}
}

// ----- InvalidateByMint 批量失效 -----

// TestStablecoinCache_InvalidateByMint 验证按 mint 地址批量失效缓存。
func TestStablecoinCache_InvalidateByMint(t *testing.T) {
	_, cache := setupTestTieredCache()

	// 添加多个池子，部分包含 USDT_MINT
	cache.Put(makeTestStablecoinPool("pool_usdt_usdc", "USDT_MINT", "USDC_MINT"))   // stableCache
	cache.Put(makeTestStablecoinPool("pool_sol_usdt", "SOL_MINT", "USDT_MINT"))     // stableCache
	cache.Put(makeTestStablecoinPool("pool_sol_meme", "SOL_MINT", "MEME_MINT"))     // normalCache
	cache.Put(makeTestStablecoinPool("pool_usdt_meme", "USDT_MINT", "MEME_MINT"))   // stableCache

	// 失效所有包含 USDT_MINT 的池子
	count := cache.InvalidateByMint("USDT_MINT")

	// 应该失效 3 个（pool_usdt_usdc, pool_sol_usdt, pool_usdt_meme）
	if count != 3 {
		t.Errorf("应失效 3 个条目，实际失效 %d 个", count)
	}

	// USDT 相关池子不应命中
	if _, ok := cache.Get("pool_usdt_usdc"); ok {
		t.Error("pool_usdt_usdc 应已失效")
	}
	if _, ok := cache.Get("pool_sol_usdt"); ok {
		t.Error("pool_sol_usdt 应已失效")
	}
	if _, ok := cache.Get("pool_usdt_meme"); ok {
		t.Error("pool_usdt_meme 应已失效")
	}

	// 不含 USDT 的池子应仍然命中
	if _, ok := cache.Get("pool_sol_meme"); !ok {
		t.Error("pool_sol_meme 不含 USDT，不应被失效")
	}
}

// TestStablecoinCache_InvalidateByMintNoMatch 验证无匹配时返回 0。
func TestStablecoinCache_InvalidateByMintNoMatch(t *testing.T) {
	_, cache := setupTestTieredCache()

	cache.Put(makeTestStablecoinPool("pool_sol_meme", "SOL_MINT", "MEME_MINT"))

	count := cache.InvalidateByMint("NONEXISTENT_MINT")
	if count != 0 {
		t.Errorf("无匹配时应返回 0，实际为 %d", count)
	}

	// 原有缓存不受影响
	if _, ok := cache.Get("pool_sol_meme"); !ok {
		t.Error("无匹配的 InvalidateByMint 不应影响其他缓存")
	}
}

// TestStablecoinCache_InvalidateByMintInNormalCache 验证 normalCache 中的条目也能被失效。
func TestStablecoinCache_InvalidateByMintInNormalCache(t *testing.T) {
	_, cache := setupTestTieredCache()

	// SOL/MEME 在 normalCache，但 BaseMint = SOL_MINT
	cache.Put(makeTestStablecoinPool("pool_sol_meme", "SOL_MINT", "MEME_MINT"))

	count := cache.InvalidateByMint("SOL_MINT")
	if count != 1 {
		t.Errorf("应失效 1 个条目，实际失效 %d 个", count)
	}

	if _, ok := cache.Get("pool_sol_meme"); ok {
		t.Error("pool_sol_meme 应已失效")
	}
}

// TestStablecoinCache_CopyIsolation 验证缓存返回的是副本，外部修改不影响缓存。
func TestStablecoinCache_CopyIsolation(t *testing.T) {
	_, cache := setupTestTieredCache()

	pool := makeTestStablecoinPool("pool_copy", "SOL_MINT", "MEME_MINT")
	pool.Liquidity = big.NewInt(1_000_000)
	cache.Put(pool)

	// 获取并修改
	got, ok := cache.Get("pool_copy")
	if !ok {
		t.Fatal("应命中")
	}
	got.Liquidity = big.NewInt(999)

	// 再次获取，应该是原始值
	got2, ok := cache.Get("pool_copy")
	if !ok {
		t.Fatal("应命中")
	}
	if got2.Liquidity.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Errorf("缓存值不应被外部修改影响，期望 1000000，实际为 %s", got2.Liquidity.String())
	}
}
