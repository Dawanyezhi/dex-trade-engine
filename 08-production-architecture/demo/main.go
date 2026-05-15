// 08-production-architecture 演示程序
// 演示生产级架构特性：限流、多链配置、监控告警。
//
// 运行: go run ./08-production-architecture/demo/
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/alarm"
	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

func main() {
	// 设置日志级别为 Info
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	fmt.Println("========================================")
	fmt.Println("  08-production-architecture: 生产级架构特性演示")
	fmt.Println("========================================")
	fmt.Println()

	// 场景 1: 令牌桶限流
	demoTokenBucketRateLimiting()

	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()

	// 场景 2: Worker 池并发控制
	demoWorkerPoolConcurrency()

	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()

	// 场景 3: 多链配置热更新
	demoMultiChainConfigHotUpdate()

	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()

	// 场景 4: 监控告警触发
	demoMonitorAlarm()

	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()

	// 场景 5: 交易状态机
	demoTxStateMachine()

	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()

	// 场景 6: 卡住交易检测
	demoStuckTxDetector()

	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()

	// 场景 7: Nonce 管理
	demoNonceManager()
}

// demoTokenBucketRateLimiting 演示令牌桶限流。
// 发送 50 个请求，限制 20 TPS，展示通过/拒绝比例。
func demoTokenBucketRateLimiting() {
	fmt.Println("[场景 1] === 令牌桶限流 ===")
	fmt.Println()

	// 创建限流器：容量 20，填充速率 20/s
	limiter := NewTokenBucketLimiter(20, 20)

	available, capacity := limiter.Stats()
	fmt.Printf("  初始状态: 可用令牌=%.0f, 桶容量=%.0f\n", available, capacity)
	fmt.Println()

	// 同时发送 50 个请求
	totalRequests := 50
	allowed := 0
	rejected := 0

	fmt.Printf("  瞬间发送 %d 个请求（桶容量 20，超出部分将被拒绝）...\n", totalRequests)
	fmt.Println()

	for i := 0; i < totalRequests; i++ {
		if limiter.Allow() {
			allowed++
		} else {
			rejected++
		}
	}

	fmt.Printf("  结果: 通过=%d, 拒绝=%d (总计=%d)\n", allowed, rejected, totalRequests)
	fmt.Printf("  通过率: %.1f%%\n", float64(allowed)/float64(totalRequests)*100)

	available, _ = limiter.Stats()
	fmt.Printf("  剩余令牌: %.0f\n", available)
	fmt.Println()

	// 等待 1 秒让令牌恢复
	fmt.Println("  等待 1 秒让令牌恢复...")
	time.Sleep(1 * time.Second)

	available, _ = limiter.Stats()
	fmt.Printf("  1 秒后可用令牌: %.0f (填充速率 20/s)\n", available)

	// 再次发送 10 个请求
	allowed2 := 0
	for i := 0; i < 10; i++ {
		if limiter.Allow() {
			allowed2++
		}
	}
	fmt.Printf("  再次发送 10 个请求: 通过=%d\n", allowed2)
}

// demoWorkerPoolConcurrency 演示 Worker 池并发控制。
func demoWorkerPoolConcurrency() {
	fmt.Println("[场景 2] === Worker 池并发控制 ===")
	fmt.Println()

	// 创建 Worker 池，最大 3 个并发
	pool := NewWorkerPool(3)
	fmt.Printf("  Worker 池容量: %d\n", pool.MaxSize())
	fmt.Println()

	// 提交 8 个任务，每个任务耗时 200ms
	var wg sync.WaitGroup
	var mu sync.Mutex
	completedTasks := 0
	taskCount := 8

	fmt.Printf("  提交 %d 个任务（每个耗时 200ms，最多 3 个并发）...\n", taskCount)
	fmt.Println()

	startTime := time.Now()
	ctx := context.Background()

	for i := 1; i <= taskCount; i++ {
		wg.Add(1)
		taskID := i
		err := pool.Submit(ctx, func() {
			defer wg.Done()
			activeCount := pool.ActiveWorkers()
			fmt.Printf("    任务 %d 开始执行 (当前并发 Worker 数: %d)\n", taskID, activeCount)
			time.Sleep(200 * time.Millisecond)
			mu.Lock()
			completedTasks++
			mu.Unlock()
		})
		if err != nil {
			wg.Done()
			fmt.Printf("    任务 %d 提交失败: %v\n", taskID, err)
		}
	}

	wg.Wait()
	elapsed := time.Since(startTime)

	fmt.Println()
	fmt.Printf("  全部完成: %d/%d 个任务\n", completedTasks, taskCount)
	fmt.Printf("  总耗时: %v (串行需要 %v，并发加速 %.1fx)\n",
		elapsed.Round(time.Millisecond),
		time.Duration(taskCount)*200*time.Millisecond,
		float64(taskCount)*200/float64(elapsed.Milliseconds()),
	)

	// 演示 SwapQueue 三层防护
	fmt.Println()
	fmt.Println("  --- SwapQueue 三层防护演示 ---")
	fmt.Println()

	// 创建 SwapQueue: 10 TPS，突发容量 5，2 个 Worker，队列容量 4
	queue := NewSwapQueue(10, 5, 2, 4)

	ctx, cancel := context.WithCancel(context.Background())
	queue.Start(ctx)

	// 快速提交 12 个任务
	fmt.Printf("  快速提交 12 个任务到 SwapQueue（限流=5突发/10TPS，队列=4，Worker=2）...\n")
	for i := 1; i <= 12; i++ {
		taskID := fmt.Sprintf("swap-%d", i)
		err := queue.Submit(SwapTask{
			ID:      taskID,
			ChainID: "solana",
			DexID:   "raydium_amm",
			Handler: func() error {
				time.Sleep(100 * time.Millisecond)
				return nil
			},
		})
		if err != nil {
			// 被限流或队列满拒绝
			_ = err
		}
	}

	// 等待任务处理
	time.Sleep(500 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond) // 等待消费者退出

	submitted, accepted, rejected, completed, failed := queue.Stats()
	fmt.Println()
	fmt.Printf("  SwapQueue 统计:\n")
	fmt.Printf("    提交: %d, 接受: %d, 拒绝: %d\n", submitted, accepted, rejected)
	fmt.Printf("    完成: %d, 失败: %d\n", completed, failed)
}

// demoMultiChainConfigHotUpdate 演示多链配置热更新。
func demoMultiChainConfigHotUpdate() {
	fmt.Println("[场景 3] === 多链配置热更新 ===")
	fmt.Println()

	// 创建多链配置（从默认 Registry 初始化）
	registry := coinset.Global()
	mcc := NewDefaultMultiChainConfig(registry)

	// 列出所有链
	chains := mcc.ListChains()
	fmt.Printf("  已注册 %d 条链:\n", len(chains))
	for _, chainID := range chains {
		rt, _ := mcc.GetRuntime(chainID)
		fmt.Printf("    - %s (%s): TPS=%.0f, Workers=%d\n",
			rt.Config.Name, chainID, rt.MaxSwapTPS, rt.MaxWorkers)
	}
	fmt.Println()

	// 检查 Solana 上 PumpAMM 的灰度状态
	solRuntime, _ := mcc.GetRuntime(coinset.ChainSolana)
	fmt.Printf("  Solana PumpAMM 灰度百分比: %d%%\n", solRuntime.GrayscaleMap[dexwallet.DexPumpAMM])

	// 模拟 100 次灰度判断
	grayscaleHits := 0
	for i := 0; i < 100; i++ {
		if solRuntime.ShouldUseGrayscale(dexwallet.DexPumpAMM) {
			grayscaleHits++
		}
	}
	fmt.Printf("  100 次灰度判断命中: %d 次 (预期约 50 次)\n", grayscaleHits)
	fmt.Println()

	// 场景：动态禁用 BSC 上的 PancakeV2（假设发现该 DEX 有异常）
	fmt.Println("  --- 热更新：禁用 BSC 上的 PancakeV2 ---")

	bscRuntime, _ := mcc.GetRuntime(coinset.ChainBSC)
	fmt.Printf("  更新前: PancakeV2 禁用状态 = %v\n", bscRuntime.IsDexDisabled(dexwallet.DexPancakeV2))

	err := mcc.DisableDex(coinset.ChainBSC, dexwallet.DexPancakeV2)
	if err != nil {
		fmt.Printf("  禁用失败: %v\n", err)
		return
	}

	bscRuntime, _ = mcc.GetRuntime(coinset.ChainBSC)
	fmt.Printf("  更新后: PancakeV2 禁用状态 = %v\n", bscRuntime.IsDexDisabled(dexwallet.DexPancakeV2))
	fmt.Println()

	// 场景：调整 Ethereum 的限流参数
	fmt.Println("  --- 热更新：调整 Ethereum 限流参数 ---")

	ethRuntime, _ := mcc.GetRuntime(coinset.ChainEthereum)
	fmt.Printf("  更新前: Ethereum MaxTPS=%.0f, MaxWorkers=%d\n",
		ethRuntime.MaxSwapTPS, ethRuntime.MaxWorkers)

	_ = mcc.UpdateMaxTPS(coinset.ChainEthereum, 15)
	_ = mcc.UpdateChainConfig(coinset.ChainEthereum, func(r *ChainRuntime) {
		r.MaxWorkers = 5
	})

	ethRuntime, _ = mcc.GetRuntime(coinset.ChainEthereum)
	fmt.Printf("  更新后: Ethereum MaxTPS=%.0f, MaxWorkers=%d\n",
		ethRuntime.MaxSwapTPS, ethRuntime.MaxWorkers)
	fmt.Println()

	// 场景：重新启用 BSC 上的 PancakeV2
	fmt.Println("  --- 热更新：重新启用 BSC 上的 PancakeV2 ---")

	_ = mcc.EnableDex(coinset.ChainBSC, dexwallet.DexPancakeV2)
	bscRuntime, _ = mcc.GetRuntime(coinset.ChainBSC)
	fmt.Printf("  启用后: PancakeV2 禁用状态 = %v\n", bscRuntime.IsDexDisabled(dexwallet.DexPancakeV2))

	// 场景：参数校验
	fmt.Println()
	fmt.Println("  --- 参数校验 ---")

	err = mcc.UpdateMaxTPS(coinset.ChainBSC, -5)
	fmt.Printf("  设置负数 TPS: %v\n", err)

	err = mcc.SetGrayscale(coinset.ChainBSC, dexwallet.DexPancakeV2, 150)
	fmt.Printf("  设置非法灰度百分比: %v\n", err)
}

// demoMonitorAlarm 演示监控告警。
func demoMonitorAlarm() {
	fmt.Println("[场景 4] === 监控告警触发 ===")
	fmt.Println()

	// 创建告警管理器（使用 Console 输出）
	alarmMgr := alarm.NewManager(alarm.NewConsoleSender())

	// 创建监控服务（检查间隔 1 秒，演示用）
	monitorSvc := NewMonitorService(alarmMgr, 1*time.Second)

	// 为 Solana 和 BSC 注册 Monitor
	solMonitor := monitorSvc.RegisterChain(coinset.ChainSolana)
	bscMonitor := monitorSvc.RegisterChain(coinset.ChainBSC)

	// 场景 4a: 正常活动（不触发告警）
	fmt.Println("  [4a] 模拟正常活动（不应触发告警）")
	SimulateSwapActivity(solMonitor, 100, 0.05)  // 5% 失败率
	SimulateQuoteActivity(solMonitor, 200, 0.1)   // 10% 失败率
	SimulateBlockUpdates(solMonitor, 200000000, 10)

	SimulateSwapActivity(bscMonitor, 50, 0.02)    // 2% 失败率
	SimulateQuoteActivity(bscMonitor, 100, 0.05)   // 5% 失败率
	SimulateBlockUpdates(bscMonitor, 18000000, 5)

	// 手动运行一次检查
	ctx := context.Background()
	monitorSvc.RunOnce(ctx)

	// 打印统计
	allStats := monitorSvc.CollectAllStats()
	fmt.Println()
	fmt.Println("  所有链的监控统计:")
	for chainID, stats := range allStats {
		swapTotal := stats["swap_total"]
		swapFail := stats["swap_fail"]
		quoteTotal := stats["quote_total"]
		quoteFail := stats["quote_fail"]

		swapFailRate := float64(0)
		if swapTotal > 0 {
			swapFailRate = float64(swapFail) / float64(swapTotal) * 100
		}
		quoteFailRate := float64(0)
		if quoteTotal > 0 {
			quoteFailRate = float64(quoteFail) / float64(quoteTotal) * 100
		}

		fmt.Printf("    %s: swap=%d(fail %.1f%%), quote=%d(fail %.1f%%), block=%d\n",
			chainID, swapTotal, swapFailRate, quoteTotal, quoteFailRate, stats["block_height"])
	}
	fmt.Println()

	// 场景 4b: 触发区块停滞告警
	fmt.Println("  [4b] 模拟区块停滞（应触发 Critical 告警）")
	fmt.Println("  设置 BSC 区块更新时间为 60 秒前...")

	// 直接更新区块高度后等待使其过期（这里通过不更新模拟停滞）
	// Monitor 的 blockStaleThreshold 默认是 30s
	// 我们通过创建一个新的 Monitor 并设置过去的时间来模拟
	staleBscMonitor := dexwallet.NewMonitor(coinset.ChainBSC, alarmMgr)
	staleBscMonitor.UpdateBlockHeight(18000005)
	// 等待足够时间使区块"过期"（这里用 sleep 模拟）
	// 为了演示效率，我们将区块停滞阈值的检查重新做一次
	// 由于默认阈值是 30s，这里直接调用 RunChecks 会看到不过期
	// 实际生产中是长时间运行的服务

	fmt.Println("  （注：区块停滞需要 30 秒未更新才触发，此处跳过等待）")
	fmt.Println()

	// 场景 4c: 触发报价失败率告警
	fmt.Println("  [4c] 模拟高报价失败率（应触发 Warning 告警）")

	// 创建一个新的 Monitor，模拟 60% 报价失败率
	highFailMonitor := dexwallet.NewMonitor(coinset.ChainSolana, alarmMgr)
	SimulateQuoteActivity(highFailMonitor, 100, 0.6) // 60% 失败率
	SimulateBlockUpdates(highFailMonitor, 200000100, 1)

	fmt.Println("  运行检查（报价失败率 > 50% 应触发告警）...")
	highFailMonitor.RunChecks(ctx)

	fmt.Println()
	highFailStats := highFailMonitor.GetStats()
	quoteFail := highFailStats["quote_fail"]
	quoteTotal := highFailStats["quote_total"]
	fmt.Printf("  报价统计: total=%d, fail=%d, 失败率=%.1f%%\n",
		quoteTotal, quoteFail, float64(quoteFail)/float64(quoteTotal)*100)
	fmt.Println()

	// 场景 4d: 启动定时监控（短暂运行 2 秒）
	fmt.Println("  [4d] 启动定时监控服务（运行 2 秒）")

	timedCtx, timedCancel := context.WithTimeout(ctx, 2*time.Second)
	defer timedCancel()

	monitorSvc.Start(timedCtx)
	<-timedCtx.Done()
	time.Sleep(100 * time.Millisecond) // 等待最后一轮检查完成

	fmt.Println()
	fmt.Println("  监控服务已停止。")
}

// demoTxStateMachine 演示状态转换：New→Ready→Pending→Confirmed，非法转换被拒绝，RejectCode 使用。
func demoTxStateMachine() {
	fmt.Println("[场景 5] === 交易状态机 ===")
	fmt.Println()

	// ---- 1. 正常生命周期: New → Ready → Pending → Confirmed ----
	fmt.Println("  [1] 正常生命周期: New → Ready → Pending → Confirmed")

	record := &dexwallet.TxRecord{
		TxHash:    "0xabc123def456",
		ChainID:   coinset.ChainEthereum,
		DexID:     dexwallet.DexUniswapV2,
		Direction: dexwallet.SwapDirectionBuy,
		Status:    dexwallet.TxStatusNew,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	sm := NewTxStateMachine(record)
	fmt.Printf("      初始状态: %s\n", sm.Current())

	transitions := []struct {
		to     dexwallet.TxStatus
		reason string
	}{
		{dexwallet.TxStatusReady, "交易构建完成"},
		{dexwallet.TxStatusPending, "交易已广播到链上"},
		{dexwallet.TxStatusConfirmed, "链上确认成功（区块 #18000001）"},
	}

	for _, t := range transitions {
		err := sm.Transition(t.to, t.reason)
		if err != nil {
			fmt.Printf("      [错误] %v\n", err)
		} else {
			fmt.Printf("      转换成功: → %s（原因: %s）\n", sm.Current(), t.reason)
		}
	}

	// ---- 2. 非法状态转换 ----
	fmt.Println("\n  [2] 非法状态转换（应被拒绝）")

	// Confirmed → Pending（已确认的交易不能回退到 Pending）
	err := sm.Transition(dexwallet.TxStatusPending, "尝试回退")
	if err != nil {
		fmt.Printf("      Confirmed → Pending: [预期错误] %v\n", err)
	}

	// 创建新的状态机测试更多非法转换
	record2 := &dexwallet.TxRecord{Status: dexwallet.TxStatusNew, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	sm2 := NewTxStateMachine(record2)

	err = sm2.Transition(dexwallet.TxStatusPending, "跳过 Ready 直接到 Pending")
	if err != nil {
		fmt.Printf("      New → Pending:    [预期错误] %v\n", err)
	}

	err = sm2.Transition(dexwallet.TxStatusConfirmed, "跳过 Ready 和 Pending")
	if err != nil {
		fmt.Printf("      New → Confirmed:  [预期错误] %v\n", err)
	}

	// ---- 3. 状态变更历史 ----
	fmt.Println("\n  [3] 状态变更历史（审计日志）")
	history := sm.History()
	for i, h := range history {
		fmt.Printf("      [%d] %s → %s （原因: %s, 时间: %s）\n",
			i+1, h.From, h.To, h.Reason, h.Timestamp.Format("15:04:05.000"))
	}

	// ---- 4. RejectCode 错误码体系 ----
	fmt.Println("\n  [4] RejectCode 错误码体系")

	codes := []RejectCode{
		RejectOK,
		RejectInvalidOrderID,
		RejectInvalidAmount,
		RejectSlippageOverflow,
		RejectInvalidContract,
		RejectInsufficientBalance,
		RejectServerError,
	}

	fmt.Printf("      %-10s %-14s %-12s %s\n", "错误码", "分类", "是否成功", "描述")
	fmt.Println("      " + fmt.Sprintf("%s", strings.Repeat("-", 55)))
	for _, code := range codes {
		fmt.Printf("      %-10d %-14s %-12v %s\n",
			int(code), code.Category(), code.IsSuccess(), code.String())
	}
}

// demoStuckTxDetector 演示 StuckTxDetector.ScanOnce 检测超时交易。
func demoStuckTxDetector() {
	fmt.Println("[场景 6] === 卡住交易检测（StuckTxDetector）===")
	fmt.Println()

	// 模拟交易记录存储
	now := time.Now()
	records := []*dexwallet.TxRecord{
		{
			TxHash:    "0xTx_Normal_001",
			ChainID:   coinset.ChainEthereum,
			Status:    dexwallet.TxStatusPending,
			CreatedAt: now.Add(-30 * time.Second), // 30 秒前，未超时
			UpdatedAt: now,
		},
		{
			TxHash:    "0xTx_Timeout_002",
			ChainID:   coinset.ChainEthereum,
			Status:    dexwallet.TxStatusPending,
			CreatedAt: now.Add(-3 * time.Minute), // 3 分钟前，超时
			UpdatedAt: now,
		},
		{
			TxHash:    "0xTx_Confirmed_003",
			ChainID:   coinset.ChainEthereum,
			Status:    dexwallet.TxStatusPending,
			CreatedAt: now.Add(-2 * time.Minute),
			UpdatedAt: now,
		},
		{
			TxHash:    "0xTx_Failed_004",
			ChainID:   coinset.ChainEthereum,
			Status:    dexwallet.TxStatusPending,
			CreatedAt: now.Add(-2 * time.Minute),
			UpdatedAt: now,
		},
	}

	// 模拟链上状态查询
	onChainStatus := map[string]dexwallet.TxStatus{
		"0xTx_Normal_001":    dexwallet.TxStatusPending,   // 仍在 pending
		"0xTx_Timeout_002":   dexwallet.TxStatusPending,   // 仍在 pending（超时）
		"0xTx_Confirmed_003": dexwallet.TxStatusConfirmed, // 已确认
		"0xTx_Failed_004":    dexwallet.TxStatusFailed,    // 已失败
	}

	config := StuckTxConfig{
		ScanInterval:     10 * time.Second,
		PendingTimeout:   1 * time.Minute, // 1 分钟超时
		MaxRetries:       3,
		RBFGasMultiplier: 13000, // 1.3x
	}

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return onChainStatus[txHash]
		},
		func() []*dexwallet.TxRecord {
			return records
		},
		func(record *dexwallet.TxRecord) {
			// 模拟更新存储
		},
	)

	fmt.Printf("  配置: pendingTimeout=%v, maxRetries=%d, RBF=%.1fx\n",
		config.PendingTimeout, config.MaxRetries, float64(config.RBFGasMultiplier)/10000.0)

	fmt.Println("\n  Pending 交易列表:")
	for _, r := range records {
		elapsed := now.Sub(r.CreatedAt).Round(time.Second)
		fmt.Printf("    %s: 已等待 %v\n", r.TxHash, elapsed)
	}

	// ---- 第一次扫描 ----
	fmt.Println("\n  [扫描 1] 首次扫描:")
	actions := detector.ScanOnce()
	for _, a := range actions {
		fmt.Printf("    %s → action=%s (%s)\n", a.TxHash, a.Action, a.Reason)
	}

	// ---- 第二次扫描（模拟重试后仍超时）----
	fmt.Println("\n  [扫描 2] 第二次扫描（超时交易继续重试）:")
	actions = detector.ScanOnce()
	for _, a := range actions {
		fmt.Printf("    %s → action=%s (%s)\n", a.TxHash, a.Action, a.Reason)
	}
	fmt.Printf("    0xTx_Timeout_002 重试次数: %d/%d\n",
		detector.GetRetryCount("0xTx_Timeout_002"), config.MaxRetries)

	// ---- 第三次和第四次扫描（达到最大重试次数）----
	fmt.Println("\n  [扫描 3+4] 继续扫描直到重试耗尽:")
	for i := 3; i <= 4; i++ {
		actions = detector.ScanOnce()
		for _, a := range actions {
			fmt.Printf("    扫描 %d: %s → action=%s (%s)\n", i, a.TxHash, a.Action, a.Reason)
		}
	}

	fmt.Println("\n  最终状态:")
	for _, r := range records {
		fmt.Printf("    %s: status=%s\n", r.TxHash, r.Status)
	}
}

// demoNonceManager 演示 AcquireNonce/ResetNonce/PeekNonce。
func demoNonceManager() {
	fmt.Println("[场景 7] === EVM Nonce 管理 ===")
	fmt.Println()

	// 模拟链上 nonce 查询（初始 nonce = 42）
	chainNonces := map[string]uint64{
		"0xAlice": 42,
		"0xBob":   100,
	}

	nm := NewNonceManager(func(address string) (uint64, error) {
		nonce, ok := chainNonces[address]
		if !ok {
			return 0, fmt.Errorf("address not found: %s", address)
		}
		return nonce, nil
	})

	// ---- 1. 首次获取 nonce（从链上同步）----
	fmt.Println("  [1] 首次获取 nonce（从链上同步）")

	nonce1, release1, err := nm.AcquireNonce("0xAlice")
	if err != nil {
		fmt.Printf("      [错误] %v\n", err)
		return
	}
	fmt.Printf("      Alice 首次获取: nonce=%d（从链上同步，链上值=42）\n", nonce1)
	release1()

	// ---- 2. 连续获取 nonce（本地递增）----
	fmt.Println("\n  [2] 连续获取 nonce（本地递增，无 RPC 调用）")

	for i := 0; i < 3; i++ {
		nonce, release, err := nm.AcquireNonce("0xAlice")
		if err != nil {
			fmt.Printf("      [错误] %v\n", err)
			continue
		}
		fmt.Printf("      第 %d 次获取: nonce=%d\n", i+2, nonce)
		release()
	}

	// ---- 3. PeekNonce 查看当前值（不递增）----
	fmt.Println("\n  [3] PeekNonce 查看当前值（不消耗、不递增）")

	peeked, err := nm.PeekNonce("0xAlice")
	if err != nil {
		fmt.Printf("      [错误] %v\n", err)
	} else {
		fmt.Printf("      Alice 当前 nonce: %d（下一笔交易应使用此值）\n", peeked)
	}

	// 查看未知地址
	peekedBob, err := nm.PeekNonce("0xBob")
	if err != nil {
		fmt.Printf("      [错误] %v\n", err)
	} else {
		fmt.Printf("      Bob 当前 nonce: %d（未使用过，直接查链上）\n", peekedBob)
	}

	// ---- 4. ResetNonce 重置（模拟 nonce gap 修复）----
	fmt.Println("\n  [4] ResetNonce 重置（模拟 nonce gap 修复后重新同步）")

	fmt.Printf("      重置前 Alice nonce: %d\n", peeked)
	nm.ResetNonce("0xAlice")

	// 模拟链上 nonce 已更新（之前发的交易已确认）
	chainNonces["0xAlice"] = 46

	nonce5, release5, err := nm.AcquireNonce("0xAlice")
	if err != nil {
		fmt.Printf("      [错误] %v\n", err)
	} else {
		fmt.Printf("      重置后重新获取: nonce=%d（从链上重新同步，链上值=46）\n", nonce5)
		release5()
	}

	// ---- 5. 多地址独立管理 ----
	fmt.Println("\n  [5] 多地址独立管理（Alice 和 Bob 互不影响）")

	nonceAlice, releaseA, _ := nm.AcquireNonce("0xAlice")
	nonceBob, releaseB, _ := nm.AcquireNonce("0xBob")
	fmt.Printf("      Alice: nonce=%d\n", nonceAlice)
	fmt.Printf("      Bob:   nonce=%d\n", nonceBob)
	releaseA()
	releaseB()

	fmt.Println("\n  [总结]")
	fmt.Println("    - 首次使用某地址时从链上同步 nonce，之后本地递增")
	fmt.Println("    - 每个地址独立加锁，不同地址之间不互斥")
	fmt.Println("    - ResetNonce 用于 nonce gap 修复后重新对齐")
	fmt.Println("    - Solana 不需要 Nonce 管理（使用 recent blockhash 防重放）")
}
