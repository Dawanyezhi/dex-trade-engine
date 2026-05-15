package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

func main() {
	// 配置结构化日志
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("04-pool-management: 流动性池解析与管理演示")
	fmt.Println(strings.Repeat("=", 70))

	ctx := context.Background()

	// 创建池子管理器
	cfg := DefaultPoolManagerConfig()
	cfg.CacheCapacity = 5 // 小容量，方便演示 LRU 淘汰
	cfg.CacheTTL = 10 * time.Second
	pm := NewPoolManagerDemo(cfg)

	// ---- 演示 1: 添加池子并查询 ----
	demoAddAndQuery(ctx, pm)

	// ---- 演示 2: GetBestPool 最优池选择 ----
	demoBestPoolSelection(ctx, pm)

	// ---- 演示 3: LRU 淘汰 ----
	demoLRUEviction(ctx, pm)

	// ---- 演示 4: 池子状态管理 ----
	demoStateManagement(ctx, pm)

	// ---- 演示 5: 缓存失效与回源 ----
	demoCacheInvalidation(ctx, pm)

	// ---- 演示 6: 稳定币分层缓存 ----
	demoStablecoinCache()

	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("演示完成")
	fmt.Println(strings.Repeat("=", 70))
}

// demoAddAndQuery 演示添加池子并查询。
func demoAddAndQuery(ctx context.Context, pm *PoolManagerDemo) {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 1: 添加池子并查询")
	fmt.Println(strings.Repeat("-", 70))

	pools := createSamplePools()

	// 添加前 3 个池子
	for i := 0; i < 3; i++ {
		if err := pm.AddPool(ctx, pools[i]); err != nil {
			fmt.Printf("  [错误] 添加池子失败: %v\n", err)
		}
	}

	fmt.Printf("\n  缓存大小: %d, 存储数量: %d\n", pm.CacheSize(), pm.RepoCount())

	// 查询池子（缓存命中）
	pool, err := pm.GetPool(ctx, pools[0].Address)
	if err != nil {
		fmt.Printf("  [错误] 查询失败: %v\n", err)
	} else {
		fmt.Println("\n  查询到的池子（缓存命中）:")
		printPool(pool)
	}
}

// demoBestPoolSelection 演示最优池选择。
func demoBestPoolSelection(ctx context.Context, pm *PoolManagerDemo) {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 2: GetBestPool 最优池选择")
	fmt.Println(strings.Repeat("-", 70))

	// 创建同一交易对（SOL/USDC）的多个池子
	solMint := "So11111111111111111111111111111111111111112"
	usdcMint := "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

	poolsForPair := []*dexwallet.Pool{
		{
			Address:      "RaydiumSOLUSDC_001",
			DexID:        dexwallet.DexRaydiumAMM,
			ChainID:      coinset.ChainSolana,
			ProtocolType: dexwallet.ProtocolAMM,
			BaseMint:     solMint,
			QuoteMint:    usdcMint,
			BaseSymbol:   "SOL",
			QuoteSymbol:  "USDC",
			BaseDecimal:  9,
			QuoteDecimal: 6,
			Liquidity:    new(big.Int).Mul(big.NewInt(50000), big.NewInt(1e9)), // 50000 SOL
			FeeRate:      25,                                                   // 0.25%
			State:        dexwallet.PoolStateActive,
		},
		{
			Address:      "RaydiumCLMM_SOLUSDC_002",
			DexID:        dexwallet.DexRaydiumCLMM,
			ChainID:      coinset.ChainSolana,
			ProtocolType: dexwallet.ProtocolCLMM,
			BaseMint:     solMint,
			QuoteMint:    usdcMint,
			BaseSymbol:   "SOL",
			QuoteSymbol:  "USDC",
			BaseDecimal:  9,
			QuoteDecimal: 6,
			Liquidity:    new(big.Int).Mul(big.NewInt(80000), big.NewInt(1e9)), // 80000 SOL
			FeeRate:      1,                                                    // 0.01%
			State:        dexwallet.PoolStateActive,
		},
		{
			Address:      "MeteoraSOLUSDC_003",
			DexID:        dexwallet.DexMeteoraAMM,
			ChainID:      coinset.ChainSolana,
			ProtocolType: dexwallet.ProtocolAMM,
			BaseMint:     solMint,
			QuoteMint:    usdcMint,
			BaseSymbol:   "SOL",
			QuoteSymbol:  "USDC",
			BaseDecimal:  9,
			QuoteDecimal: 6,
			Liquidity:    new(big.Int).Mul(big.NewInt(10000), big.NewInt(1e9)), // 10000 SOL
			FeeRate:      30,                                                   // 0.3%
			State:        dexwallet.PoolStateActive,
		},
	}

	// 重建管理器，确保缓存干净
	cfg := DefaultPoolManagerConfig()
	cfg.CacheCapacity = 10
	cfg.CacheTTL = 30 * time.Second
	pmBest := NewPoolManagerDemo(cfg)

	for _, p := range poolsForPair {
		if err := pmBest.AddPool(ctx, p); err != nil {
			fmt.Printf("  [错误] 添加池子失败: %v\n", err)
		}
	}

	// 显示候选池子
	fmt.Println("\n  SOL/USDC 候选池子:")
	fmt.Printf("  %-30s %-18s %-14s %-12s %-8s\n",
		"地址", "DEX", "协议", "流动性(SOL)", "费率(BPS)")
	fmt.Println("  " + strings.Repeat("-", 84))
	for _, p := range poolsForPair {
		liqSOL := new(big.Int).Div(p.Liquidity, big.NewInt(1e9))
		fmt.Printf("  %-30s %-18s %-14s %-12s %-8d\n",
			p.Address, p.DexID, p.ProtocolType, liqSOL.String(), p.FeeRate)
	}

	// 选择最优池
	best, err := pmBest.GetBestPool(ctx, solMint, usdcMint)
	if err != nil {
		fmt.Printf("\n  [错误] 选择最优池失败: %v\n", err)
	} else {
		liqSOL := new(big.Int).Div(best.Liquidity, big.NewInt(1e9))
		fmt.Printf("\n  最优池: %s (DEX=%s, 流动性=%s SOL, 费率=%d BPS)\n",
			best.Address, best.DexID, liqSOL.String(), best.FeeRate)
		fmt.Println("  原因: Raydium CLMM 流动性最大(80000 SOL)且费率最低(0.01%)")
	}
}

// demoLRUEviction 演示 LRU 缓存淘汰。
func demoLRUEviction(ctx context.Context, pm *PoolManagerDemo) {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 3: LRU 缓存淘汰")
	fmt.Println(strings.Repeat("-", 70))

	// 创建新的管理器，容量为 3
	cfg := DefaultPoolManagerConfig()
	cfg.CacheCapacity = 3
	cfg.CacheTTL = 1 * time.Minute
	pmLRU := NewPoolManagerDemo(cfg)

	pools := createSamplePools()

	// 添加 3 个池子（填满缓存）
	for i := 0; i < 3; i++ {
		_ = pmLRU.AddPool(ctx, pools[i])
		fmt.Printf("  添加池子 %d: %s (缓存大小: %d)\n",
			i+1, pools[i].Address, pmLRU.CacheSize())
	}

	fmt.Printf("\n  缓存已满: %d/%d\n", pmLRU.CacheSize(), 3)

	// 访问第一个池子，使其变为最近使用
	_, _ = pmLRU.GetPool(ctx, pools[0].Address)
	fmt.Printf("  访问了 %s（移到缓存头部）\n", pools[0].Address)

	// 添加第 4 个池子，触发淘汰
	fmt.Println("\n  添加第 4 个池子，触发 LRU 淘汰:")
	_ = pmLRU.AddPool(ctx, pools[3])
	fmt.Printf("  缓存大小: %d\n", pmLRU.CacheSize())

	// 检查哪个池子被淘汰
	for i := 0; i < 4; i++ {
		_, ok := pmLRU.Cache().Get(pools[i].Address)
		status := "在缓存中"
		if !ok {
			status = "已被淘汰"
		}
		fmt.Printf("  %s: %s\n", pools[i].Address, status)
	}
	fmt.Println("  说明: pools[1] 是最久未访问的（pools[0] 被访问过），所以被淘汰")
}

// demoStateManagement 演示池子状态管理。
func demoStateManagement(ctx context.Context, pm *PoolManagerDemo) {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 4: 池子状态管理")
	fmt.Println(strings.Repeat("-", 70))

	// 创建新的管理器
	cfg := DefaultPoolManagerConfig()
	cfg.CacheCapacity = 10
	cfg.CacheTTL = 1 * time.Minute
	pmState := NewPoolManagerDemo(cfg)

	pool := &dexwallet.Pool{
		Address:      "StateTestPool_001",
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     "SOL_MINT",
		QuoteMint:    "MEME_MINT",
		BaseSymbol:   "SOL",
		QuoteSymbol:  "MEME",
		BaseDecimal:  9,
		QuoteDecimal: 6,
		Liquidity:    new(big.Int).Mul(big.NewInt(1000), big.NewInt(1e9)),
		FeeRate:      25,
		State:        dexwallet.PoolStateActive,
	}

	_ = pmState.AddPool(ctx, pool)

	// 状态转换: active -> need_update
	fmt.Println("\n  [1] 状态: active -> need_update")
	err := pmState.SetPoolState(ctx, pool.Address, dexwallet.PoolStateNeedUpdate)
	if err != nil {
		fmt.Printf("      [错误] %v\n", err)
	}
	p, _ := pmState.GetPool(ctx, pool.Address)
	fmt.Printf("      当前状态: %s\n", p.State)

	// 状态转换: need_update -> active（模拟刷新成功）
	fmt.Println("\n  [2] 状态: need_update -> active（模拟数据刷新成功）")
	p.State = dexwallet.PoolStateActive
	p.Liquidity = new(big.Int).Mul(big.NewInt(1200), big.NewInt(1e9)) // 流动性更新
	_ = pmState.UpdatePool(ctx, p)
	p, _ = pmState.GetPool(ctx, pool.Address)
	liq := new(big.Int).Div(p.Liquidity, big.NewInt(1e9))
	fmt.Printf("      当前状态: %s, 流动性: %s SOL\n", p.State, liq.String())

	// 状态转换: active -> inactive
	fmt.Println("\n  [3] 状态: active -> inactive（池子关闭）")
	err = pmState.SetPoolState(ctx, pool.Address, dexwallet.PoolStateInactive)
	if err != nil {
		fmt.Printf("      [错误] %v\n", err)
	}
	fmt.Printf("      缓存大小: %d（inactive 的池子从缓存移除）\n", pmState.CacheSize())

	// 验证 inactive 的池子仍在存储中
	pFromRepo, err := pmState.repo.GetPool(ctx, pool.Address)
	if err != nil {
		fmt.Printf("      [错误] 从存储查询失败: %v\n", err)
	} else {
		fmt.Printf("      存储中仍然存在: %s (state=%s)\n", pFromRepo.Address, pFromRepo.State)
	}
}

// demoCacheInvalidation 演示缓存失效与回源。
func demoCacheInvalidation(ctx context.Context, pm *PoolManagerDemo) {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 5: 缓存失效与回源")
	fmt.Println(strings.Repeat("-", 70))

	// 创建一个 TTL 极短的管理器
	cfg := DefaultPoolManagerConfig()
	cfg.CacheCapacity = 10
	cfg.CacheTTL = 500 * time.Millisecond // 500ms 后过期
	pmExpire := NewPoolManagerDemo(cfg)

	pool := &dexwallet.Pool{
		Address:      "ExpireTestPool_001",
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     "SOL_MINT",
		QuoteMint:    "TOKEN_MINT",
		BaseSymbol:   "SOL",
		QuoteSymbol:  "TOKEN",
		BaseDecimal:  9,
		QuoteDecimal: 6,
		Liquidity:    new(big.Int).Mul(big.NewInt(5000), big.NewInt(1e9)),
		FeeRate:      25,
		State:        dexwallet.PoolStateActive,
	}

	_ = pmExpire.AddPool(ctx, pool)
	fmt.Printf("\n  池子已添加到缓存（TTL=500ms）\n")

	// 立即查询（缓存命中）
	_, err := pmExpire.GetPool(ctx, pool.Address)
	if err != nil {
		fmt.Printf("  [错误] 第一次查询: %v\n", err)
	} else {
		fmt.Println("  第一次查询: 缓存命中")
	}

	// 等待 TTL 过期
	fmt.Println("  等待 600ms（超过 TTL）...")
	time.Sleep(600 * time.Millisecond)

	// 再次查询（缓存未命中，回源到 Repository）
	p, err := pmExpire.GetPool(ctx, pool.Address)
	if err != nil {
		fmt.Printf("  [错误] 第二次查询: %v\n", err)
	} else {
		fmt.Printf("  第二次查询: 缓存未命中，从存储回源成功 (address=%s)\n", p.Address)
		fmt.Printf("  缓存大小: %d（回源后重新加入缓存）\n", pmExpire.CacheSize())
	}

	// 手动触发 Evict
	evicted := pmExpire.Cache().Evict()
	fmt.Printf("\n  手动 Evict: 清除了 %d 个过期条目\n", evicted)
	fmt.Printf("  Evict 后缓存大小: %d\n", pmExpire.CacheSize())
}

// createSamplePools 创建一组示例池子。
func createSamplePools() []*dexwallet.Pool {
	return []*dexwallet.Pool{
		{
			Address:      "RaydiumPool_SOL_MEME_001",
			DexID:        dexwallet.DexRaydiumAMM,
			ChainID:      coinset.ChainSolana,
			ProtocolType: dexwallet.ProtocolAMM,
			BaseMint:     "SOL_MINT_111",
			QuoteMint:    "MEME_MINT_222",
			BaseSymbol:   "SOL",
			QuoteSymbol:  "MEME",
			BaseDecimal:  9,
			QuoteDecimal: 6,
			Liquidity:    new(big.Int).Mul(big.NewInt(10000), big.NewInt(1e9)),
			FeeRate:      25,
			State:        dexwallet.PoolStateActive,
		},
		{
			Address:      "PumpFunPool_SOL_DOGE_002",
			DexID:        dexwallet.DexPumpFun,
			ChainID:      coinset.ChainSolana,
			ProtocolType: dexwallet.ProtocolBondingCurve,
			BaseMint:     "SOL_MINT_111",
			QuoteMint:    "DOGE_MINT_333",
			BaseSymbol:   "SOL",
			QuoteSymbol:  "DOGE",
			BaseDecimal:  9,
			QuoteDecimal: 6,
			Liquidity:    new(big.Int).Mul(big.NewInt(100), big.NewInt(1e9)),
			FeeRate:      100,
			State:        dexwallet.PoolStateActive,
		},
		{
			Address:      "UniswapPool_ETH_USDC_003",
			DexID:        dexwallet.DexUniswapV2,
			ChainID:      coinset.ChainEthereum,
			ProtocolType: dexwallet.ProtocolAMM,
			BaseMint:     "ETH_ADDR_444",
			QuoteMint:    "USDC_ADDR_555",
			BaseSymbol:   "ETH",
			QuoteSymbol:  "USDC",
			BaseDecimal:  18,
			QuoteDecimal: 6,
			Liquidity:    new(big.Int).Mul(big.NewInt(20000), big.NewInt(1e18)),
			FeeRate:      30,
			State:        dexwallet.PoolStateActive,
		},
		{
			Address:      "PancakePool_BNB_CAKE_004",
			DexID:        dexwallet.DexPancakeV2,
			ChainID:      coinset.ChainBSC,
			ProtocolType: dexwallet.ProtocolAMM,
			BaseMint:     "BNB_ADDR_666",
			QuoteMint:    "CAKE_ADDR_777",
			BaseSymbol:   "BNB",
			QuoteSymbol:  "CAKE",
			BaseDecimal:  18,
			QuoteDecimal: 18,
			Liquidity:    new(big.Int).Mul(big.NewInt(5000), big.NewInt(1e18)),
			FeeRate:      25,
			State:        dexwallet.PoolStateActive,
		},
		{
			Address:      "MeteoraPool_SOL_RAY_005",
			DexID:        dexwallet.DexMeteoraAMM,
			ChainID:      coinset.ChainSolana,
			ProtocolType: dexwallet.ProtocolAMM,
			BaseMint:     "SOL_MINT_111",
			QuoteMint:    "RAY_MINT_888",
			BaseSymbol:   "SOL",
			QuoteSymbol:  "RAY",
			BaseDecimal:  9,
			QuoteDecimal: 6,
			Liquidity:    new(big.Int).Mul(big.NewInt(3000), big.NewInt(1e9)),
			FeeRate:      30,
			State:        dexwallet.PoolStateActive,
		},
		{
			Address:      "CurvePool_USDC_DAI_006",
			DexID:        dexwallet.DexCurve,
			ChainID:      coinset.ChainEthereum,
			ProtocolType: dexwallet.ProtocolStableSwap,
			BaseMint:     "USDC_ADDR_999",
			QuoteMint:    "DAI_ADDR_000",
			BaseSymbol:   "USDC",
			QuoteSymbol:  "DAI",
			BaseDecimal:  6,
			QuoteDecimal: 18,
			Liquidity:    new(big.Int).Mul(big.NewInt(1000000), big.NewInt(1e6)),
			FeeRate:      4,
			State:        dexwallet.PoolStateActive,
		},
	}
}

// printPool 格式化打印池子信息。
func printPool(p *dexwallet.Pool) {
	fmt.Printf("    地址:     %s\n", p.Address)
	fmt.Printf("    DEX:      %s\n", p.DexID)
	fmt.Printf("    链:       %s\n", p.ChainID)
	fmt.Printf("    协议:     %s\n", p.ProtocolType)
	fmt.Printf("    交易对:   %s/%s\n", p.BaseSymbol, p.QuoteSymbol)
	if p.Liquidity != nil {
		fmt.Printf("    流动性:   %s\n", p.Liquidity.String())
	}
	fmt.Printf("    费率(BPS): %d\n", p.FeeRate)
	fmt.Printf("    状态:     %s\n", p.State)
	fmt.Printf("    更新时间: %s\n", p.UpdatedAt.Format(time.RFC3339))
}

// demoStablecoinCache 演示 TieredPoolCache 的三层 TTL、StablecoinSet 判断、脱锚检测。
func demoStablecoinCache() {
	fmt.Println("\n" + strings.Repeat("-", 70))
	fmt.Println("演示 6: 稳定币分层缓存（TieredPoolCache）")
	fmt.Println(strings.Repeat("-", 70))

	// ---- 初始化稳定币集合和分层缓存 ----
	stablecoins := DefaultStablecoinSet()
	normalTTL := 500 * time.Millisecond  // 普通池 TTL（演示用，生产中 3-5 秒）
	stableTTL := 2 * time.Second         // 稳定币池 TTL（演示用，生产中 30-60 秒）
	cache := NewTieredPoolCache(stablecoins, normalTTL, stableTTL)

	fmt.Printf("\n  缓存配置: normalTTL=%v, stableTTL=%v\n", normalTTL, stableTTL)

	// ---- 1. StablecoinSet 判断 ----
	fmt.Println("\n  [1] StablecoinSet 稳定币识别")

	testMints := []struct {
		mint   string
		symbol string
	}{
		{"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", "USDC(Solana)"},
		{"Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", "USDT(Solana)"},
		{"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "USDC(ETH)"},
		{"So11111111111111111111111111111111111111112", "SOL"},
		{"MEMExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "MEME"},
	}
	for _, t := range testMints {
		isStable := stablecoins.IsStablecoin(t.mint)
		fmt.Printf("    %s: 是否稳定币 = %v\n", t.symbol, isStable)
	}

	// ---- 2. 三层 TTL 分类 ----
	fmt.Println("\n  [2] 三层 TTL 分类")

	pools := []*dexwallet.Pool{
		{
			Address:     "Pool_USDT_USDC_001",
			DexID:       dexwallet.DexCurve,
			ChainID:     coinset.ChainEthereum,
			BaseMint:    "0xdAC17F958D2ee523a2206206994597C13D831ec7", // USDT
			QuoteMint:   "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", // USDC
			BaseSymbol:  "USDT",
			QuoteSymbol: "USDC",
			Liquidity:   new(big.Int).Mul(big.NewInt(1000000), big.NewInt(1e6)),
			FeeRate:     4,
			State:       dexwallet.PoolStateActive,
		},
		{
			Address:     "Pool_SOL_USDT_002",
			DexID:       dexwallet.DexRaydiumAMM,
			ChainID:     coinset.ChainSolana,
			BaseMint:    "So11111111111111111111111111111111111111112",  // SOL
			QuoteMint:   "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", // USDT
			BaseSymbol:  "SOL",
			QuoteSymbol: "USDT",
			Liquidity:   new(big.Int).Mul(big.NewInt(50000), big.NewInt(1e9)),
			FeeRate:     25,
			State:       dexwallet.PoolStateActive,
		},
		{
			Address:     "Pool_MEME_SOL_003",
			DexID:       dexwallet.DexRaydiumAMM,
			ChainID:     coinset.ChainSolana,
			BaseMint:    "MEMExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", // MEME
			QuoteMint:   "So11111111111111111111111111111111111111112",  // SOL
			BaseSymbol:  "MEME",
			QuoteSymbol: "SOL",
			Liquidity:   new(big.Int).Mul(big.NewInt(100), big.NewInt(1e9)),
			FeeRate:     25,
			State:       dexwallet.PoolStateActive,
		},
	}

	fmt.Printf("    %-25s %-12s %-16s %-10s\n", "池子地址", "交易对", "缓存层级", "TTL")
	fmt.Println("    " + strings.Repeat("-", 65))
	for _, p := range pools {
		tier := cache.ClassifyPool(p)
		ttl := normalTTL
		if tier == string(PoolTierStableStable) || tier == string(PoolTierStableNormal) {
			ttl = stableTTL
		}
		fmt.Printf("    %-25s %-12s %-16s %-10v\n",
			p.Address, p.BaseSymbol+"/"+p.QuoteSymbol, tier, ttl)
		cache.Put(p)
	}

	// ---- 3. 缓存命中与过期 ----
	fmt.Println("\n  [3] 缓存命中与过期")

	// 立即查询：全部命中
	for _, p := range pools {
		_, hit := cache.Get(p.Address)
		fmt.Printf("    %s: 缓存命中 = %v\n", p.Address, hit)
	}

	stats := cache.Stats()
	fmt.Printf("    统计: normalSize=%d, stableSize=%d, 命中率=%.1f%%\n",
		stats.NormalSize, stats.StableSize, stats.HitRate*100)

	// 等待 normalTTL 过期（600ms > 500ms），但 stableTTL 仍有效
	fmt.Printf("\n    等待 %v（超过 normalTTL=%v，但在 stableTTL=%v 内）...\n",
		600*time.Millisecond, normalTTL, stableTTL)
	time.Sleep(600 * time.Millisecond)

	for _, p := range pools {
		_, hit := cache.Get(p.Address)
		tier := cache.ClassifyPool(p)
		fmt.Printf("    %s (%s): 缓存命中 = %v\n", p.Address, tier, hit)
	}

	// ---- 4. 脱锚检测（通过 Invalidate 模拟） ----
	fmt.Println("\n  [4] 稳定币脱锚检测（模拟 USDT 脱锚）")

	// 先重新写入所有池子
	for _, p := range pools {
		cache.Put(p)
	}

	// 检查脱锚前：USDT/USDC 池缓存存在
	_, hitBefore := cache.Get("Pool_USDT_USDC_001")
	fmt.Printf("    脱锚前: USDT/USDC 池缓存命中 = %v\n", hitBefore)

	// 模拟 USDT 脱锚：手动失效相关池子的缓存
	fmt.Println("    检测到 USDT 价格跌破 0.95 USD，触发脱锚处理：")
	fmt.Println("      - Invalidate USDT/USDC 池缓存")
	fmt.Println("      - Invalidate SOL/USDT 池缓存")
	cache.Invalidate("Pool_USDT_USDC_001")
	cache.Invalidate("Pool_SOL_USDT_002")

	_, hitAfter := cache.Get("Pool_USDT_USDC_001")
	_, hitSOL := cache.Get("Pool_SOL_USDT_002")
	_, hitMEME := cache.Get("Pool_MEME_SOL_003")
	fmt.Printf("    脱锚后: USDT/USDC 池缓存命中 = %v\n", hitAfter)
	fmt.Printf("    脱锚后: SOL/USDT 池缓存命中 = %v\n", hitSOL)
	fmt.Printf("    脱锚后: MEME/SOL 池缓存命中 = %v（不受影响）\n", hitMEME)

	statsFinal := cache.Stats()
	fmt.Printf("    最终统计: normalSize=%d, stableSize=%d, 总命中=%d, 总未命中=%d\n",
		statsFinal.NormalSize, statsFinal.StableSize, statsFinal.TotalHits, statsFinal.TotalMisses)

	fmt.Println("\n  [总结]")
	fmt.Println("    - stable-stable 池（如 USDT/USDC）使用最长 TTL，减少 RPC 调用")
	fmt.Println("    - stable-normal 池（如 SOL/USDT）使用中等 TTL")
	fmt.Println("    - normal-normal 池（如 MEME/SOL）使用最短 TTL，保证数据新鲜度")
	fmt.Println("    - 脱锚时通过 Invalidate 强制失效缓存，下次查询从链上刷新")
}
