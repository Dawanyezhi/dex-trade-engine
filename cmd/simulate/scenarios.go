package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/alarm"
	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// 场景 01：RPC 客户端 -- 多节点故障转移
// ============================================================

func runScenario01() error {
	// 模拟多节点 RPC 客户端
	nodes := []mockRPCNode{
		{name: "solana-node-1", healthy: true, chainID: coinset.ChainSolana, blockHeight: 250000000},
		{name: "solana-node-2", healthy: true, chainID: coinset.ChainSolana, blockHeight: 250000001},
		{name: "solana-node-3", healthy: false, chainID: coinset.ChainSolana, blockHeight: 0},
	}

	// 测试正常调用
	balance, err := nodes[0].GetBalance(context.Background(), "So11111111111111111111111111111112")
	if err != nil {
		return fmt.Errorf("正常调用失败: %w", err)
	}
	if balance.Sign() <= 0 {
		return fmt.Errorf("余额应为正数")
	}

	// 测试故障转移：节点 1 不可用，切换到节点 2
	nodes[0].healthy = false
	activeNode := -1
	for i, n := range nodes {
		if n.healthy {
			activeNode = i
			break
		}
	}
	if activeNode != 1 {
		return fmt.Errorf("故障转移应切换到节点 1")
	}

	height, err := nodes[activeNode].GetBlockHeight(context.Background())
	if err != nil {
		return fmt.Errorf("故障转移后调用失败: %w", err)
	}
	if height != 250000001 {
		return fmt.Errorf("区块高度不匹配")
	}

	// EVM 节点测试
	evmNode := mockRPCNode{name: "bsc-node-1", healthy: true, chainID: coinset.ChainBSC, blockHeight: 40000000}
	evmBalance, err := evmNode.GetBalance(context.Background(), "0x1234567890abcdef")
	if err != nil || evmBalance.Sign() <= 0 {
		return fmt.Errorf("EVM RPC 调用失败")
	}

	return nil
}

// ============================================================
// 场景 02：DEX 协议解析 -- 价格计算与滑点模拟
// ============================================================

func runScenario02() error {
	// AMM 恒定乘积
	reserveA := new(big.Int).Mul(big.NewInt(1000000), big.NewInt(1e9))  // 1M SOL
	reserveB := new(big.Int).Mul(big.NewInt(10000000), big.NewInt(1e9)) // 10M MEME
	feeRate := uint64(30)                                                // 0.3%

	amountIn := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e9)) // 1 SOL
	ammOut := calcAMMOutput(reserveA, reserveB, amountIn, feeRate)
	if ammOut.Sign() <= 0 {
		return fmt.Errorf("AMM 输出应为正数")
	}

	// 大额交易滑点更高
	bigAmountIn := new(big.Int).Mul(big.NewInt(100), big.NewInt(1e9)) // 100 SOL
	bigAmmOut := calcAMMOutput(reserveA, reserveB, bigAmountIn, feeRate)
	// 100x 输入应该得到 < 100x 输出（滑点）
	scaledSmallOut := new(big.Int).Mul(ammOut, big.NewInt(100))
	if bigAmmOut.Cmp(scaledSmallOut) >= 0 {
		return fmt.Errorf("大额交易应有更高滑点")
	}

	// Bonding Curve（简化）
	bondingOut := calcBondingCurveOutput(big.NewInt(1e9), big.NewInt(50000), big.NewInt(1e15))
	if bondingOut.Sign() <= 0 {
		return fmt.Errorf("Bonding Curve 输出应为正数")
	}

	return nil
}

// ============================================================
// 场景 03：Swap 交易引擎 -- 工厂模式 + 交易构建 + 模拟
// ============================================================

func runScenario03() error {
	factory := newSimpleFactory()

	// 注册 Solana + EVM builder
	factory.register(dexwallet.DexRaydiumAMM, coinset.ChainSolana, dexwallet.ProtocolAMM)
	factory.register(dexwallet.DexPumpFun, coinset.ChainSolana, dexwallet.ProtocolBondingCurve)
	factory.register(dexwallet.DexUniswapV2, coinset.ChainEthereum, dexwallet.ProtocolAMM)

	// 通过工厂获取 builder
	_, err := factory.get(dexwallet.DexRaydiumAMM)
	if err != nil {
		return fmt.Errorf("工厂获取 Raydium 失败: %w", err)
	}

	// 不存在的 DEX
	_, err = factory.get("nonexistent_dex")
	if err == nil {
		return fmt.Errorf("应返回不存在的 DEX 错误")
	}

	// 构建 Solana Swap
	result := &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		InputAmount:  big.NewInt(1e9),
		OutputAmount: big.NewInt(9965059852),
		MinOutput:    big.NewInt(9765758654),
		SlippageBps:  200,
		TxData:       []byte("mock_solana_tx"),
	}

	// 交易模拟：正常通过
	if err := simulateSwap(result, 500); err != nil {
		return fmt.Errorf("模拟应通过: %w", err)
	}

	// 交易模拟：滑点过高
	badResult := &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		InputAmount:  big.NewInt(1e9),
		OutputAmount: big.NewInt(100), // 极低输出
		MinOutput:    big.NewInt(50),
		SlippageBps:  9000, // 90% 滑点
		TxData:       []byte("mock_tx"),
	}
	if err := simulateSwap(badResult, 500); err == nil {
		return fmt.Errorf("高滑点应被拒绝")
	}

	return nil
}

// ============================================================
// 场景 04：池子管理 -- LRU 缓存 + 最优池选择
// ============================================================

func runScenario04() error {
	cache := dexwallet.NewLRUPoolCache(3, 60*time.Minute) // 容量 3

	pools := []*dexwallet.Pool{
		{Address: "pool_1", BaseMint: "SOL", QuoteMint: "MEME", Liquidity: big.NewInt(1000000), FeeRate: 30, State: dexwallet.PoolStateActive, DexID: dexwallet.DexRaydiumAMM},
		{Address: "pool_2", BaseMint: "SOL", QuoteMint: "MEME", Liquidity: big.NewInt(5000000), FeeRate: 25, State: dexwallet.PoolStateActive, DexID: dexwallet.DexMeteoraAMM},
		{Address: "pool_3", BaseMint: "SOL", QuoteMint: "MEME", Liquidity: big.NewInt(2000000), FeeRate: 50, State: dexwallet.PoolStateActive, DexID: dexwallet.DexPumpAMM},
	}

	for _, p := range pools {
		cache.Put(p)
	}

	// 缓存命中
	p, ok := cache.Get("pool_1")
	if !ok || p.Address != "pool_1" {
		return fmt.Errorf("缓存命中失败")
	}

	// LRU 淘汰：添加第 4 个，应淘汰最久未访问的
	cache.Put(&dexwallet.Pool{Address: "pool_4", BaseMint: "ETH", QuoteMint: "USDC", Liquidity: big.NewInt(100), State: dexwallet.PoolStateActive})
	if cache.Size() != 3 {
		return fmt.Errorf("LRU 淘汰后容量应为 3，得到 %d", cache.Size())
	}

	// 最优池选择（按流动性）
	bestPools := cache.GetByPair("SOL", "MEME")
	if len(bestPools) == 0 {
		return fmt.Errorf("应找到 SOL/MEME 池子")
	}

	return nil
}

// ============================================================
// 场景 05：聚合路由 -- 并发报价 + 优先级 + 降级
// ============================================================

func runScenario05() error {
	// 模拟 3 个 DEX 的报价
	quotes := []struct {
		dexID    dexwallet.DexID
		priority dexwallet.DexPriority
		output   *big.Int
		fail     bool
	}{
		{dexwallet.DexPumpFun, dexwallet.PriorityHigh, big.NewInt(31000000000), false},
		{dexwallet.DexRaydiumAMM, dexwallet.PriorityMedium, big.NewInt(9965000000), false},
		{dexwallet.DexJupiter, dexwallet.PriorityLow, big.NewInt(9900000000), false},
	}

	// 并发获取报价
	type quoteResult struct {
		dexID  dexwallet.DexID
		output *big.Int
		prio   dexwallet.DexPriority
		err    error
	}

	results := make(chan quoteResult, len(quotes))
	for _, q := range quotes {
		go func(q struct {
			dexID    dexwallet.DexID
			priority dexwallet.DexPriority
			output   *big.Int
			fail     bool
		}) {
			if q.fail {
				results <- quoteResult{dexID: q.dexID, err: fmt.Errorf("DEX %s 报价失败", q.dexID)}
			} else {
				results <- quoteResult{dexID: q.dexID, output: q.output, prio: q.priority}
			}
		}(q)
	}

	var validQuotes []quoteResult
	for i := 0; i < len(quotes); i++ {
		r := <-results
		if r.err == nil {
			validQuotes = append(validQuotes, r)
		}
	}

	if len(validQuotes) == 0 {
		return fmt.Errorf("所有报价失败")
	}

	// 按优先级 + 输出金额排序
	sort.Slice(validQuotes, func(i, j int) bool {
		if validQuotes[i].prio != validQuotes[j].prio {
			return validQuotes[i].prio < validQuotes[j].prio
		}
		return validQuotes[i].output.Cmp(validQuotes[j].output) > 0
	})

	best := validQuotes[0]
	if best.dexID != dexwallet.DexPumpFun {
		return fmt.Errorf("最优报价应来自 PumpFun（高优先级），得到 %s", best.dexID)
	}

	return nil
}

// ============================================================
// 场景 06：MEV 防护 -- 三明治攻击 + Solana 贿赂服务 + EVM Anti-MEV + 优先费
// ============================================================

func runScenario06() error {
	// 三明治攻击模拟
	reserveA := new(big.Int).Mul(big.NewInt(100000), big.NewInt(1e9))
	reserveB := new(big.Int).Mul(big.NewInt(1000000), big.NewInt(1e9))
	victimIn := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e9))
	attackerIn := new(big.Int).Mul(big.NewInt(50), big.NewInt(1e9))

	// 无攻击时受害者获得的输出
	victimOutClean := calcAMMOutput(reserveA, reserveB, victimIn, 30)

	// 攻击者前置交易
	attackerOut1 := calcAMMOutput(reserveA, reserveB, attackerIn, 30)
	newReserveA := new(big.Int).Add(reserveA, attackerIn)
	newReserveB := new(big.Int).Sub(reserveB, attackerOut1)

	// 受害者在被操纵的池子中交易
	victimOutAttacked := calcAMMOutput(newReserveA, newReserveB, victimIn, 30)

	// 受害者损失
	victimLoss := new(big.Int).Sub(victimOutClean, victimOutAttacked)
	if victimLoss.Sign() <= 0 {
		return fmt.Errorf("三明治攻击应导致受害者损失")
	}

	// 贿赂服务模拟
	services := []string{"NextBlock", "Temporal", "ZeroSlot", "BlockRazor", "BlockRush"}
	var successCount int32
	var wg sync.WaitGroup
	for _, name := range services {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			// 模拟 80% 成功率
			if n != "ZeroSlot" { // ZeroSlot 模拟失败
				atomic.AddInt32(&successCount, 1)
			}
		}(name)
	}
	wg.Wait()
	if successCount < 1 {
		return fmt.Errorf("至少一个贿赂服务应成功")
	}

	// 优先费推荐
	samples := []uint64{1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000, 9000, 10000}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p25 := samples[len(samples)/4]      // 25th percentile
	p50 := samples[len(samples)/2]      // 50th percentile
	p75 := samples[len(samples)*3/4]    // 75th percentile
	if p25 >= p50 || p50 >= p75 {
		return fmt.Errorf("优先费百分位应递增: p25=%d p50=%d p75=%d", p25, p50, p75)
	}

	return nil
}

// ============================================================
// 场景 07：事件解析 -- 解析器注册 + Syncer 主循环
// ============================================================

func runScenario07() error {
	// 解析器注册
	parsers := map[string]string{
		"RaydiumV4":   "solana_program_raydium",
		"PumpFun":     "solana_program_pumpfun",
		"UniswapV2":   "0xd78ad95f...",
		"ERC20":       "0xddf252ad...",
	}
	if len(parsers) != 4 {
		return fmt.Errorf("应注册 4 个解析器")
	}

	// 模拟 Swap 事件解析
	mockEvent := dexwallet.ChainEvent{
		Type:      dexwallet.EventSwap,
		ChainID:   coinset.ChainSolana,
		TxHash:    "5abc...def",
		Block:     250000001,
		DexID:     dexwallet.DexRaydiumAMM,
		Pool:      "pool_raydium_sol_meme",
		TokenIn:   "So11111111111111111111111111111112",
		TokenOut:  "MEME_MINT_ADDRESS",
		AmountIn:  big.NewInt(1e9),
		AmountOut: big.NewInt(9965059852),
		Timestamp: time.Now(),
	}
	if mockEvent.Type != dexwallet.EventSwap {
		return fmt.Errorf("事件类型应为 swap")
	}

	// 模拟 Syncer 同步 3 个区块
	heights := []uint64{100, 101, 102}
	currentHeight := uint64(99)
	for _, h := range heights {
		if h != currentHeight+1 {
			return fmt.Errorf("区块高度不连续")
		}
		currentHeight = h
	}
	if currentHeight != 102 {
		return fmt.Errorf("最终高度应为 102")
	}

	return nil
}

// ============================================================
// 场景 08：生产级架构 -- 限流 + 多链配置 + 监控
// ============================================================

func runScenario08() error {
	// 令牌桶限流
	capacity := 10
	refillRate := 5 // 每秒 5 个
	tokens := capacity
	passed := 0
	rejected := 0
	for i := 0; i < 20; i++ {
		if tokens > 0 {
			tokens--
			passed++
		} else {
			rejected++
		}
	}
	if passed != 10 || rejected != 10 {
		return fmt.Errorf("限流结果应为 10 通过 10 拒绝")
	}
	_ = refillRate

	// 多链配置
	registry := coinset.Global()
	solCfg, err := registry.Get(coinset.ChainSolana)
	if err != nil || !solCfg.IsSolana() {
		return fmt.Errorf("Solana 配置获取失败")
	}
	bscCfg, err := registry.Get(coinset.ChainBSC)
	if err != nil || !bscCfg.IsEVM() {
		return fmt.Errorf("BSC 配置获取失败")
	}

	// 新增链只需添加配置
	registry.Register(&coinset.ChainConfig{
		ChainID:       "monad",
		ChainType:     coinset.ChainTypeEVM,
		Name:          "Monad",
		NativeCoin:    "MON",
		NativeDecimal: 18,
		Confirmations: 1,
		BlockTime:     500,
		Features: map[coinset.Feature]bool{
			coinset.FeatureEIP1559: true,
		},
	})
	monadCfg, err := registry.Get("monad")
	if err != nil || monadCfg.Name != "Monad" {
		return fmt.Errorf("Monad 配置注册失败")
	}

	// 监控告警
	alarmMgr := alarm.NewManager(alarm.NewConsoleSender())
	monitor := dexwallet.NewMonitor(coinset.ChainSolana, alarmMgr)
	monitor.UpdateBlockHeight(250000001)
	monitor.RecordSwap(true)
	monitor.RecordSwap(true)
	monitor.RecordSwap(false)
	monitor.RecordQuote(true)
	monitor.RecordQuote(false)

	stats := monitor.GetStats()
	if stats["swap_total"] != 3 || stats["swap_fail"] != 1 {
		return fmt.Errorf("监控统计不正确")
	}

	return nil
}

// ============================================================
// 辅助函数
// ============================================================

// calcAMMOutput 恒定乘积 AMM 价格计算
func calcAMMOutput(reserveIn, reserveOut, amountIn *big.Int, feeBps uint64) *big.Int {
	// amountOut = reserveOut * amountIn * (10000 - fee) / (reserveIn * 10000 + amountIn * (10000 - fee))
	feeMultiplier := big.NewInt(int64(10000 - feeBps))
	tenK := big.NewInt(10000)

	amountInWithFee := new(big.Int).Mul(amountIn, feeMultiplier)
	numerator := new(big.Int).Mul(reserveOut, amountInWithFee)
	denominator := new(big.Int).Add(
		new(big.Int).Mul(reserveIn, tenK),
		amountInWithFee,
	)
	return new(big.Int).Div(numerator, denominator)
}

// calcBondingCurveOutput 简化的 Bonding Curve 输出
func calcBondingCurveOutput(amountIn, currentSupply, totalSupply *big.Int) *big.Int {
	// 简化：输出 = amountIn * (totalSupply - currentSupply) / totalSupply
	remaining := new(big.Int).Sub(totalSupply, currentSupply)
	numerator := new(big.Int).Mul(amountIn, remaining)
	return new(big.Int).Div(numerator, totalSupply)
}

// simulateSwap 交易模拟验证
func simulateSwap(result *dexwallet.SwapResult, maxSlippageBps uint64) error {
	if len(result.TxData) == 0 {
		return fmt.Errorf("交易数据为空")
	}
	if result.OutputAmount.Sign() <= 0 {
		return fmt.Errorf("输出金额为零")
	}
	if result.SlippageBps > maxSlippageBps {
		return fmt.Errorf("滑点 %d bps 超过阈值 %d bps", result.SlippageBps, maxSlippageBps)
	}
	return nil
}

// mockRPCNode 模拟 RPC 节点
type mockRPCNode struct {
	name        string
	healthy     bool
	chainID     coinset.ChainID
	blockHeight uint64
}

func (n *mockRPCNode) GetBalance(_ context.Context, _ string) (*big.Int, error) {
	if !n.healthy {
		return nil, fmt.Errorf("node %s unhealthy", n.name)
	}
	return big.NewInt(5000000000), nil // 5 SOL / 5 BNB
}

func (n *mockRPCNode) GetBlockHeight(_ context.Context) (uint64, error) {
	if !n.healthy {
		return 0, fmt.Errorf("node %s unhealthy", n.name)
	}
	return n.blockHeight, nil
}

// simpleFactory 简化的工厂
type simpleFactory struct {
	builders map[dexwallet.DexID]dexEntry
}

type dexEntry struct {
	chainID  coinset.ChainID
	protocol dexwallet.ProtocolType
}

func newSimpleFactory() *simpleFactory {
	return &simpleFactory{builders: make(map[dexwallet.DexID]dexEntry)}
}

func (f *simpleFactory) register(id dexwallet.DexID, chain coinset.ChainID, proto dexwallet.ProtocolType) {
	f.builders[id] = dexEntry{chainID: chain, protocol: proto}
}

func (f *simpleFactory) get(id dexwallet.DexID) (*dexEntry, error) {
	e, ok := f.builders[id]
	if !ok {
		return nil, fmt.Errorf("DEX not found: %s", id)
	}
	return &e, nil
}

// 确保 json 包被使用（事件序列化需要）
var _ = json.Marshal
