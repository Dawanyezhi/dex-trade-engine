package main

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/alarm"
	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ---------------------------------------------------------------------------
// 限流精度测试
// ---------------------------------------------------------------------------

// TestTokenBucketLimiter_BasicAllow 测试令牌桶的基本 Allow 行为。
func TestTokenBucketLimiter_BasicAllow(t *testing.T) {
	// 桶容量 10，填充速率 10/s
	limiter := NewTokenBucketLimiter(10, 10)

	// 初始状态应该有 10 个令牌
	available, capacity := limiter.Stats()
	if capacity != 10 {
		t.Errorf("期望桶容量=10, 实际=%f", capacity)
	}
	if math.Abs(available-10) > 0.5 {
		t.Errorf("期望可用令牌约为 10, 实际=%f", available)
	}

	// 连续消费 10 个令牌，应该全部通过
	allowed := 0
	for i := 0; i < 10; i++ {
		if limiter.Allow() {
			allowed++
		}
	}
	if allowed != 10 {
		t.Errorf("期望通过 10 个请求, 实际=%d", allowed)
	}

	// 桶空了，下一个请求应该被拒绝
	if limiter.Allow() {
		t.Error("桶空后应该拒绝请求")
	}
}

// TestTokenBucketLimiter_Refill 测试令牌桶的填充行为。
func TestTokenBucketLimiter_Refill(t *testing.T) {
	// 桶容量 5，填充速率 10/s
	limiter := NewTokenBucketLimiter(5, 10)

	// 消耗所有令牌
	for i := 0; i < 5; i++ {
		limiter.Allow()
	}

	// 确认桶空
	available, _ := limiter.Stats()
	if available > 0.5 {
		t.Errorf("期望桶空, 实际可用=%f", available)
	}

	// 等待 500ms，应该填充约 5 个令牌
	time.Sleep(500 * time.Millisecond)

	available, _ = limiter.Stats()
	// 允许一定误差（时间精度）
	if available < 4 || available > 5.5 {
		t.Errorf("等待 500ms 后期望约 5 个令牌, 实际=%f", available)
	}
}

// TestTokenBucketLimiter_CapacityLimit 测试令牌桶不超过容量上限。
func TestTokenBucketLimiter_CapacityLimit(t *testing.T) {
	// 桶容量 10，填充速率 100/s
	limiter := NewTokenBucketLimiter(10, 100)

	// 等待 1 秒，理论上填充 100 个，但桶容量只有 10
	time.Sleep(100 * time.Millisecond)

	available, capacity := limiter.Stats()
	if available > capacity+0.1 {
		t.Errorf("可用令牌(%f)不应超过桶容量(%f)", available, capacity)
	}
}

// TestTokenBucketLimiter_BurstAndSteady 测试突发+稳态模式。
func TestTokenBucketLimiter_BurstAndSteady(t *testing.T) {
	// 桶容量 20，填充速率 20/s
	limiter := NewTokenBucketLimiter(20, 20)

	// 瞬间消耗 50 个请求
	allowed := 0
	for i := 0; i < 50; i++ {
		if limiter.Allow() {
			allowed++
		}
	}

	// 桶容量 20，应该通过约 20 个
	if allowed != 20 {
		t.Errorf("突发消耗期望通过 20 个, 实际=%d", allowed)
	}

	// 等待 1 秒填充
	time.Sleep(1 * time.Second)

	// 再消耗，应该通过约 20 个（填充了 20 个）
	allowed = 0
	for i := 0; i < 30; i++ {
		if limiter.Allow() {
			allowed++
		}
	}

	if allowed < 18 || allowed > 22 {
		t.Errorf("1 秒后突发消耗期望通过约 20 个, 实际=%d", allowed)
	}
}

// TestTokenBucketLimiter_Wait 测试 Wait 阻塞等待。
func TestTokenBucketLimiter_Wait(t *testing.T) {
	// 桶容量 1，填充速率 10/s（每 100ms 一个令牌）
	limiter := NewTokenBucketLimiter(1, 10)

	// 消耗唯一的令牌
	limiter.Allow()

	// Wait 应该在约 100ms 后获得令牌
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := limiter.Wait(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Wait 不应返回错误: %v", err)
	}
	if elapsed < 50*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("Wait 耗时期望约 100ms, 实际=%v", elapsed)
	}
}

// TestTokenBucketLimiter_WaitCancel 测试 Wait 的 context 取消。
func TestTokenBucketLimiter_WaitCancel(t *testing.T) {
	// 桶容量 1，填充速率极低
	limiter := NewTokenBucketLimiter(1, 0.1) // 每 10 秒一个令牌

	// 消耗唯一的令牌
	limiter.Allow()

	// 用很短的超时取消
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := limiter.Wait(ctx)
	if err == nil {
		t.Error("Wait 应该返回 context 取消错误")
	}
}

// TestTokenBucketLimiter_ConcurrentSafety 测试令牌桶的并发安全。
func TestTokenBucketLimiter_ConcurrentSafety(t *testing.T) {
	limiter := NewTokenBucketLimiter(100, 100)

	var wg sync.WaitGroup
	var totalAllowed atomic.Int64

	// 100 个 goroutine 同时竞争
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.Allow() {
				totalAllowed.Add(1)
			}
		}()
	}
	wg.Wait()

	allowed := totalAllowed.Load()
	// 桶容量 100，100 个并发请求应该全部通过
	if allowed != 100 {
		t.Errorf("100 个并发请求期望全部通过, 实际=%d", allowed)
	}
}

// ---------------------------------------------------------------------------
// Worker 池并发限制测试
// ---------------------------------------------------------------------------

// TestWorkerPool_ConcurrencyLimit 测试 Worker 池的并发限制。
func TestWorkerPool_ConcurrencyLimit(t *testing.T) {
	maxWorkers := 3
	pool := NewWorkerPool(maxWorkers)

	var maxConcurrent atomic.Int32
	var currentConcurrent atomic.Int32
	var wg sync.WaitGroup

	taskCount := 10
	for i := 0; i < taskCount; i++ {
		wg.Add(1)
		err := pool.Submit(context.Background(), func() {
			defer wg.Done()

			// 记录当前并发数
			current := currentConcurrent.Add(1)
			// 更新最大并发数
			for {
				old := maxConcurrent.Load()
				if current <= old || maxConcurrent.CompareAndSwap(old, current) {
					break
				}
			}

			time.Sleep(50 * time.Millisecond)
			currentConcurrent.Add(-1)
		})
		if err != nil {
			wg.Done()
			t.Errorf("任务提交失败: %v", err)
		}
	}

	wg.Wait()

	maxSeen := maxConcurrent.Load()
	if maxSeen > int32(maxWorkers) {
		t.Errorf("最大并发数 %d 超过了 Worker 池限制 %d", maxSeen, maxWorkers)
	}
	if maxSeen < 1 {
		t.Error("最大并发数应至少为 1")
	}
}

// TestWorkerPool_TrySubmit 测试非阻塞提交。
func TestWorkerPool_TrySubmit(t *testing.T) {
	pool := NewWorkerPool(1)

	// 占满 Worker 池
	blocker := make(chan struct{})
	ok := pool.TrySubmit(func() {
		<-blocker // 阻塞直到外部通知
	})
	if !ok {
		t.Error("第一个任务应该提交成功")
	}

	// 等待 goroutine 启动
	time.Sleep(10 * time.Millisecond)

	// Worker 池已满，TrySubmit 应该立即返回 false
	ok = pool.TrySubmit(func() {})
	if ok {
		t.Error("Worker 池满时 TrySubmit 应该返回 false")
	}

	// 释放 blocker
	close(blocker)
	time.Sleep(20 * time.Millisecond)

	// 现在应该可以提交了
	done := make(chan struct{})
	ok = pool.TrySubmit(func() {
		close(done)
	})
	if !ok {
		t.Error("Worker 释放后 TrySubmit 应该返回 true")
	}
	<-done
}

// TestWorkerPool_SubmitContextCancel 测试提交时的 context 取消。
func TestWorkerPool_SubmitContextCancel(t *testing.T) {
	pool := NewWorkerPool(1)

	// 占满 Worker 池
	blocker := make(chan struct{})
	_ = pool.Submit(context.Background(), func() {
		<-blocker
	})
	time.Sleep(10 * time.Millisecond)

	// 使用短超时 context 提交
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := pool.Submit(ctx, func() {})
	if err == nil {
		t.Error("Worker 池满且 context 超时时，Submit 应该返回错误")
	}

	close(blocker)
}

// ---------------------------------------------------------------------------
// 配置热更新测试
// ---------------------------------------------------------------------------

// TestMultiChainConfig_RegisterAndGet 测试配置注册和获取。
func TestMultiChainConfig_RegisterAndGet(t *testing.T) {
	mcc := NewMultiChainConfig()

	runtime := &ChainRuntime{
		Config: &coinset.ChainConfig{
			ChainID:   coinset.ChainSolana,
			ChainType: coinset.ChainTypeSolana,
			Name:      "Solana",
		},
		DisabledDex:  make(map[dexwallet.DexID]bool),
		GrayscaleMap: make(map[dexwallet.DexID]int),
		MaxSwapTPS:   50,
		MaxWorkers:   10,
	}

	mcc.RegisterChain(runtime)

	// 获取成功
	got, err := mcc.GetRuntime(coinset.ChainSolana)
	if err != nil {
		t.Fatalf("获取配置失败: %v", err)
	}
	if got.MaxSwapTPS != 50 {
		t.Errorf("期望 MaxSwapTPS=50, 实际=%f", got.MaxSwapTPS)
	}

	// 获取不存在的链
	_, err = mcc.GetRuntime(coinset.ChainBSC)
	if err == nil {
		t.Error("获取不存在的链应该返回错误")
	}
}

// TestMultiChainConfig_DisableEnableDex 测试 DEX 禁用和启用。
func TestMultiChainConfig_DisableEnableDex(t *testing.T) {
	mcc := NewMultiChainConfig()
	mcc.RegisterChain(&ChainRuntime{
		Config: &coinset.ChainConfig{
			ChainID: coinset.ChainBSC,
			Name:    "BSC",
		},
		DisabledDex:  make(map[dexwallet.DexID]bool),
		GrayscaleMap: make(map[dexwallet.DexID]int),
		MaxSwapTPS:   20,
		MaxWorkers:   5,
	})

	// 初始未禁用
	rt, _ := mcc.GetRuntime(coinset.ChainBSC)
	if rt.IsDexDisabled(dexwallet.DexPancakeV2) {
		t.Error("初始状态 PancakeV2 不应被禁用")
	}

	// 禁用
	err := mcc.DisableDex(coinset.ChainBSC, dexwallet.DexPancakeV2)
	if err != nil {
		t.Fatalf("禁用失败: %v", err)
	}
	rt, _ = mcc.GetRuntime(coinset.ChainBSC)
	if !rt.IsDexDisabled(dexwallet.DexPancakeV2) {
		t.Error("禁用后 PancakeV2 应被禁用")
	}

	// 启用
	err = mcc.EnableDex(coinset.ChainBSC, dexwallet.DexPancakeV2)
	if err != nil {
		t.Fatalf("启用失败: %v", err)
	}
	rt, _ = mcc.GetRuntime(coinset.ChainBSC)
	if rt.IsDexDisabled(dexwallet.DexPancakeV2) {
		t.Error("启用后 PancakeV2 不应被禁用")
	}
}

// TestMultiChainConfig_UpdateTPS 测试 TPS 热更新。
func TestMultiChainConfig_UpdateTPS(t *testing.T) {
	mcc := NewMultiChainConfig()
	mcc.RegisterChain(&ChainRuntime{
		Config: &coinset.ChainConfig{
			ChainID: coinset.ChainEthereum,
			Name:    "Ethereum",
		},
		DisabledDex:  make(map[dexwallet.DexID]bool),
		GrayscaleMap: make(map[dexwallet.DexID]int),
		MaxSwapTPS:   10,
		MaxWorkers:   3,
	})

	// 更新 TPS
	err := mcc.UpdateMaxTPS(coinset.ChainEthereum, 25)
	if err != nil {
		t.Fatalf("更新 TPS 失败: %v", err)
	}

	rt, _ := mcc.GetRuntime(coinset.ChainEthereum)
	if rt.MaxSwapTPS != 25 {
		t.Errorf("期望 MaxSwapTPS=25, 实际=%f", rt.MaxSwapTPS)
	}

	// 非法值
	err = mcc.UpdateMaxTPS(coinset.ChainEthereum, -1)
	if err == nil {
		t.Error("负数 TPS 应该返回错误")
	}

	err = mcc.UpdateMaxTPS(coinset.ChainEthereum, 0)
	if err == nil {
		t.Error("零 TPS 应该返回错误")
	}
}

// TestMultiChainConfig_Grayscale 测试灰度发布。
func TestMultiChainConfig_Grayscale(t *testing.T) {
	mcc := NewMultiChainConfig()
	mcc.RegisterChain(&ChainRuntime{
		Config: &coinset.ChainConfig{
			ChainID: coinset.ChainSolana,
			Name:    "Solana",
		},
		DisabledDex:  make(map[dexwallet.DexID]bool),
		GrayscaleMap: make(map[dexwallet.DexID]int),
		MaxSwapTPS:   50,
		MaxWorkers:   10,
	})

	// 设置灰度百分比
	err := mcc.SetGrayscale(coinset.ChainSolana, dexwallet.DexPumpAMM, 30)
	if err != nil {
		t.Fatalf("设置灰度失败: %v", err)
	}

	// 统计命中率
	rt, _ := mcc.GetRuntime(coinset.ChainSolana)
	hits := 0
	trials := 10000
	for i := 0; i < trials; i++ {
		if rt.ShouldUseGrayscale(dexwallet.DexPumpAMM) {
			hits++
		}
	}

	// 30% 灰度，允许 5% 误差
	hitRate := float64(hits) / float64(trials) * 100
	if hitRate < 25 || hitRate > 35 {
		t.Errorf("灰度命中率期望约 30%%, 实际=%.1f%%", hitRate)
	}

	// 非法灰度百分比
	err = mcc.SetGrayscale(coinset.ChainSolana, dexwallet.DexPumpAMM, -1)
	if err == nil {
		t.Error("负数灰度百分比应该返回错误")
	}
	err = mcc.SetGrayscale(coinset.ChainSolana, dexwallet.DexPumpAMM, 101)
	if err == nil {
		t.Error("超过 100 的灰度百分比应该返回错误")
	}
}

// TestMultiChainConfig_ConcurrentAccess 测试并发访问安全。
func TestMultiChainConfig_ConcurrentAccess(t *testing.T) {
	mcc := NewMultiChainConfig()
	mcc.RegisterChain(&ChainRuntime{
		Config: &coinset.ChainConfig{
			ChainID: coinset.ChainSolana,
			Name:    "Solana",
		},
		DisabledDex:  make(map[dexwallet.DexID]bool),
		GrayscaleMap: make(map[dexwallet.DexID]int),
		MaxSwapTPS:   50,
		MaxWorkers:   10,
	})

	var wg sync.WaitGroup

	// 并发读
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mcc.GetRuntime(coinset.ChainSolana)
		}()
	}

	// 并发写
	for i := 0; i < 10; i++ {
		wg.Add(1)
		newTPS := float64(i + 10)
		go func() {
			defer wg.Done()
			_ = mcc.UpdateMaxTPS(coinset.ChainSolana, newTPS)
		}()
	}

	wg.Wait()
	// 能跑到这里不 panic 就说明并发安全
}

// ---------------------------------------------------------------------------
// 告警触发条件测试
// ---------------------------------------------------------------------------

// testAlarmSender 测试用的告警发送器，记录收到的告警。
type testAlarmSender struct {
	mu     sync.Mutex
	alerts []alarm.Alert
}

func newTestAlarmSender() *testAlarmSender {
	return &testAlarmSender{}
}

func (s *testAlarmSender) Name() string { return "test" }

func (s *testAlarmSender) Send(_ context.Context, alert alarm.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = append(s.alerts, alert)
	return nil
}

func (s *testAlarmSender) getAlerts() []alarm.Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]alarm.Alert, len(s.alerts))
	copy(result, s.alerts)
	return result
}

// TestMonitorService_NoAlarmOnNormalActivity 测试正常活动不触发告警。
func TestMonitorService_NoAlarmOnNormalActivity(t *testing.T) {
	sender := newTestAlarmSender()
	alarmMgr := alarm.NewManager(sender)
	monitorSvc := NewMonitorService(alarmMgr, 1*time.Second)

	monitor := monitorSvc.RegisterChain(coinset.ChainSolana)

	// 正常活动：低失败率
	SimulateSwapActivity(monitor, 100, 0.05)
	SimulateQuoteActivity(monitor, 100, 0.1) // 10% 失败率，低于 50% 阈值
	SimulateBlockUpdates(monitor, 200000000, 5)

	monitorSvc.RunOnce(context.Background())

	alerts := sender.getAlerts()
	if len(alerts) != 0 {
		t.Errorf("正常活动不应触发告警, 但收到了 %d 条告警", len(alerts))
		for _, a := range alerts {
			t.Logf("  告警: level=%s title=%s", a.Level, a.Title)
		}
	}
}

// TestMonitorService_HighQuoteFailRate 测试高报价失败率触发告警。
func TestMonitorService_HighQuoteFailRate(t *testing.T) {
	sender := newTestAlarmSender()
	alarmMgr := alarm.NewManager(sender)

	monitor := dexwallet.NewMonitor(coinset.ChainSolana, alarmMgr)

	// 高失败率：60% 报价失败（超过 50% 阈值）
	SimulateQuoteActivity(monitor, 100, 0.6)
	SimulateBlockUpdates(monitor, 200000000, 1) // 避免区块停滞告警

	monitor.RunChecks(context.Background())

	alerts := sender.getAlerts()
	found := false
	for _, a := range alerts {
		if a.Title == "High Quote Failure Rate" {
			found = true
			if a.Level != alarm.LevelWarning {
				t.Errorf("期望告警级别=warning, 实际=%s", a.Level)
			}
		}
	}
	if !found {
		t.Error("高报价失败率应触发 'High Quote Failure Rate' 告警")
	}
}

// TestMonitorService_CollectAllStats 测试统计收集。
func TestMonitorService_CollectAllStats(t *testing.T) {
	sender := newTestAlarmSender()
	alarmMgr := alarm.NewManager(sender)
	monitorSvc := NewMonitorService(alarmMgr, 1*time.Second)

	solMonitor := monitorSvc.RegisterChain(coinset.ChainSolana)
	bscMonitor := monitorSvc.RegisterChain(coinset.ChainBSC)

	SimulateSwapActivity(solMonitor, 50, 0.1)
	SimulateSwapActivity(bscMonitor, 30, 0.2)

	allStats := monitorSvc.CollectAllStats()

	if len(allStats) != 2 {
		t.Errorf("期望 2 条链的统计, 实际=%d", len(allStats))
	}

	solStats, ok := allStats[coinset.ChainSolana]
	if !ok {
		t.Fatal("缺少 Solana 的统计")
	}
	if solStats["swap_total"] != 50 {
		t.Errorf("Solana swap_total 期望=50, 实际=%d", solStats["swap_total"])
	}

	bscStats, ok := allStats[coinset.ChainBSC]
	if !ok {
		t.Fatal("缺少 BSC 的统计")
	}
	if bscStats["swap_total"] != 30 {
		t.Errorf("BSC swap_total 期望=30, 实际=%d", bscStats["swap_total"])
	}
}

// ---------------------------------------------------------------------------
// SwapQueue 集成测试
// ---------------------------------------------------------------------------

// TestSwapQueue_RateLimitAndBackpressure 测试 SwapQueue 的限流和背压。
func TestSwapQueue_RateLimitAndBackpressure(t *testing.T) {
	// 突发容量 5，TPS 10，2 个 Worker，队列容量 3
	queue := NewSwapQueue(10, 5, 2, 3)

	ctx, cancel := context.WithCancel(context.Background())
	queue.Start(ctx)

	// 快速提交 15 个任务
	for i := 0; i < 15; i++ {
		_ = queue.Submit(SwapTask{
			ID:      fmt.Sprintf("task-%d", i),
			ChainID: "solana",
			DexID:   "raydium_amm",
			Handler: func() error {
				time.Sleep(50 * time.Millisecond)
				return nil
			},
		})
	}

	// 等待处理
	time.Sleep(500 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	submitted, accepted, rejected, _, _ := queue.Stats()

	// 提交了 15 个
	if submitted != 15 {
		t.Errorf("期望提交 15 个, 实际=%d", submitted)
	}

	// 应该有一部分被拒绝（限流或队列满）
	if rejected == 0 {
		t.Error("15 个请求中应该有部分被拒绝")
	}

	// 接受 + 拒绝 = 提交
	if accepted+rejected != submitted {
		t.Errorf("accepted(%d) + rejected(%d) != submitted(%d)", accepted, rejected, submitted)
	}
}

// ---------------------------------------------------------------------------
// CircuitBreaker 熔断器测试
// ---------------------------------------------------------------------------

// TestCircuitBreaker_NormalOperationStaysClosed 连续成功保持 Closed。
func TestCircuitBreaker_NormalOperationStaysClosed(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold:    5,
		RecoveryTimeout:     30 * time.Second,
		HalfOpenMaxRequests: 1,
	})

	// 初始状态应为 closed
	if cb.State() != "closed" {
		t.Fatalf("初始状态期望 closed, 实际=%s", cb.State())
	}

	// 连续成功 20 次，状态始终保持 closed
	for i := 0; i < 20; i++ {
		if !cb.Allow() {
			t.Fatalf("第 %d 次请求应该被允许", i+1)
		}
		cb.RecordSuccess()

		if cb.State() != "closed" {
			t.Fatalf("第 %d 次成功后状态期望 closed, 实际=%s", i+1, cb.State())
		}
	}
}

// TestCircuitBreaker_TripsOnConsecutiveFailures 连续失败触发 Open。
func TestCircuitBreaker_TripsOnConsecutiveFailures(t *testing.T) {
	threshold := 3
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold:    threshold,
		RecoveryTimeout:     30 * time.Second,
		HalfOpenMaxRequests: 1,
	})

	// 连续失败 threshold-1 次，状态还是 closed
	for i := 0; i < threshold-1; i++ {
		if !cb.Allow() {
			t.Fatalf("第 %d 次请求应该被允许", i+1)
		}
		cb.RecordFailure()

		if cb.State() != "closed" {
			t.Fatalf("第 %d 次失败后状态期望 closed, 实际=%s", i+1, cb.State())
		}
	}

	// 第 threshold 次失败触发 Open
	if !cb.Allow() {
		t.Fatal("第 threshold 次请求应该被允许")
	}
	cb.RecordFailure()

	if cb.State() != "open" {
		t.Fatalf("达到失败阈值后状态期望 open, 实际=%s", cb.State())
	}

	// Open 状态下请求应该被拒绝
	if cb.Allow() {
		t.Fatal("Open 状态下请求应该被拒绝")
	}
}

// TestCircuitBreaker_RecoveryFlow 测试 Open → HalfOpen → Closed 恢复流程。
func TestCircuitBreaker_RecoveryFlow(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold:    2,
		RecoveryTimeout:     100 * time.Millisecond, // 短超时便于测试
		HalfOpenMaxRequests: 1,
	})

	// 触发熔断
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != "open" {
		t.Fatalf("期望 open, 实际=%s", cb.State())
	}

	// Open 状态下请求被拒绝
	if cb.Allow() {
		t.Fatal("Open 状态下请求应被拒绝")
	}

	// 等待超过 RecoveryTimeout
	time.Sleep(150 * time.Millisecond)

	// 此时 Allow() 应触发 Open → HalfOpen 转换并放行
	if !cb.Allow() {
		t.Fatal("恢复超时后请求应该被允许（HalfOpen 探测）")
	}
	if cb.State() != "half_open" {
		t.Fatalf("期望 half_open, 实际=%s", cb.State())
	}

	// 探测成功，回到 Closed
	cb.RecordSuccess()

	if cb.State() != "closed" {
		t.Fatalf("探测成功后期望 closed, 实际=%s", cb.State())
	}

	// 恢复后正常使用
	if !cb.Allow() {
		t.Fatal("恢复后请求应该被允许")
	}
}

// TestCircuitBreaker_HalfOpenFailureReturnsToOpen HalfOpen 时失败回到 Open。
func TestCircuitBreaker_HalfOpenFailureReturnsToOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold:    2,
		RecoveryTimeout:     100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	})

	// 触发熔断
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != "open" {
		t.Fatalf("期望 open, 实际=%s", cb.State())
	}

	// 等待超过 RecoveryTimeout
	time.Sleep(150 * time.Millisecond)

	// 进入 HalfOpen，放行探测请求
	if !cb.Allow() {
		t.Fatal("恢复超时后请求应该被允许（HalfOpen 探测）")
	}
	if cb.State() != "half_open" {
		t.Fatalf("期望 half_open, 实际=%s", cb.State())
	}

	// 探测失败，回到 Open
	cb.RecordFailure()

	if cb.State() != "open" {
		t.Fatalf("HalfOpen 失败后期望 open, 实际=%s", cb.State())
	}

	// 回到 Open 后请求再次被拒绝
	if cb.Allow() {
		t.Fatal("回到 Open 后请求应该被拒绝")
	}
}

// ---------------------------------------------------------------------------
// Benchmark 测试
// ---------------------------------------------------------------------------

func BenchmarkTokenBucketAllow(b *testing.B) {
	limiter := NewTokenBucketLimiter(float64(b.N), float64(b.N))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.Allow()
	}
}

func BenchmarkWorkerPoolSubmit(b *testing.B) {
	pool := NewWorkerPool(8)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.Submit(ctx, func() {})
	}
}

// ---------------------------------------------------------------------------
// StuckTxDetector 卡住交易检测测试
// ---------------------------------------------------------------------------

// TestStuckTxDetector_NoPendingTx 测试无 pending 交易时 ScanOnce 无输出。
func TestStuckTxDetector_NoPendingTx(t *testing.T) {
	config := DefaultStuckTxConfig()

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return dexwallet.TxStatusPending
		},
		func() []*dexwallet.TxRecord {
			return nil // 无 pending 记录
		},
		func(record *dexwallet.TxRecord) {},
	)

	actions := detector.ScanOnce()
	if len(actions) != 0 {
		t.Errorf("无 pending 交易时 ScanOnce 应返回空列表，实际=%d", len(actions))
	}
}

// TestStuckTxDetector_NoPendingTx_EmptySlice 测试空列表的情况。
func TestStuckTxDetector_NoPendingTx_EmptySlice(t *testing.T) {
	config := DefaultStuckTxConfig()

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return dexwallet.TxStatusPending
		},
		func() []*dexwallet.TxRecord {
			return []*dexwallet.TxRecord{} // 空列表
		},
		func(record *dexwallet.TxRecord) {},
	)

	actions := detector.ScanOnce()
	if len(actions) != 0 {
		t.Errorf("空记录列表时 ScanOnce 应返回空列表，实际=%d", len(actions))
	}
}

// TestStuckTxDetector_NonPendingSkipped 测试非 Pending 状态的记录被跳过。
func TestStuckTxDetector_NonPendingSkipped(t *testing.T) {
	config := DefaultStuckTxConfig()

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return dexwallet.TxStatusPending
		},
		func() []*dexwallet.TxRecord {
			return []*dexwallet.TxRecord{
				{
					TxHash:    "0xConfirmed",
					Status:    dexwallet.TxStatusConfirmed, // 非 Pending
					CreatedAt: time.Now().Add(-5 * time.Minute),
				},
				{
					TxHash:    "0xFailed",
					Status:    dexwallet.TxStatusFailed, // 非 Pending
					CreatedAt: time.Now().Add(-5 * time.Minute),
				},
			}
		},
		func(record *dexwallet.TxRecord) {},
	)

	actions := detector.ScanOnce()
	if len(actions) != 0 {
		t.Errorf("非 Pending 状态的记录应被跳过，期望 0 个 action，实际=%d", len(actions))
	}
}

// TestStuckTxDetector_TimeoutDetected 测试超时交易被检测到并触发 retry。
func TestStuckTxDetector_TimeoutDetected(t *testing.T) {
	config := DefaultStuckTxConfig()
	config.PendingTimeout = 1 * time.Second // 缩短超时用于测试
	config.MaxRetries = 3

	var updatedRecords []*dexwallet.TxRecord

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return dexwallet.TxStatusPending // 链上仍是 Pending
		},
		func() []*dexwallet.TxRecord {
			return []*dexwallet.TxRecord{
				{
					TxHash:    "0xStuck1",
					Status:    dexwallet.TxStatusPending,
					CreatedAt: time.Now().Add(-5 * time.Second), // 超过 1s 超时
				},
			}
		},
		func(record *dexwallet.TxRecord) {
			updatedRecords = append(updatedRecords, record)
		},
	)

	actions := detector.ScanOnce()
	if len(actions) != 1 {
		t.Fatalf("期望 1 个 action，实际=%d", len(actions))
	}

	action := actions[0]
	if action.TxHash != "0xStuck1" {
		t.Errorf("期望 TxHash=0xStuck1, 实际=%s", action.TxHash)
	}
	if action.Action != "retry" {
		t.Errorf("首次超时期望 Action=retry, 实际=%s", action.Action)
	}
	if action.Record == nil {
		t.Error("Action.Record 不应为 nil")
	}

	// 验证重试计数增加
	retryCount := detector.GetRetryCount("0xStuck1")
	if retryCount != 1 {
		t.Errorf("首次超时后重试计数期望=1, 实际=%d", retryCount)
	}
}

// TestStuckTxDetector_TimeoutDropAfterMaxRetries 测试超时且重试次数达上限时标记为 drop。
func TestStuckTxDetector_TimeoutDropAfterMaxRetries(t *testing.T) {
	config := DefaultStuckTxConfig()
	config.PendingTimeout = 1 * time.Second
	config.MaxRetries = 2

	var updatedRecords []*dexwallet.TxRecord

	stuckRecord := &dexwallet.TxRecord{
		TxHash:    "0xStuckDrop",
		Status:    dexwallet.TxStatusPending,
		CreatedAt: time.Now().Add(-10 * time.Second),
	}

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return dexwallet.TxStatusPending
		},
		func() []*dexwallet.TxRecord {
			return []*dexwallet.TxRecord{stuckRecord}
		},
		func(record *dexwallet.TxRecord) {
			updatedRecords = append(updatedRecords, record)
		},
	)

	// 第 1 次扫描 → retry (第 1 次重试)
	actions := detector.ScanOnce()
	if len(actions) != 1 || actions[0].Action != "retry" {
		t.Fatalf("第 1 次扫描期望 retry, 实际=%v", actions)
	}

	// 第 2 次扫描 → retry (第 2 次重试)
	actions = detector.ScanOnce()
	if len(actions) != 1 || actions[0].Action != "retry" {
		t.Fatalf("第 2 次扫描期望 retry, 实际=%v", actions)
	}

	// 第 3 次扫描 → drop (重试次数已达上限)
	actions = detector.ScanOnce()
	if len(actions) != 1 {
		t.Fatalf("第 3 次扫描期望 1 个 action, 实际=%d", len(actions))
	}
	if actions[0].Action != "drop" {
		t.Errorf("重试次数达上限后期望 Action=drop, 实际=%s", actions[0].Action)
	}

	// 验证记录被更新为 Error 状态
	if stuckRecord.Status != dexwallet.TxStatusError {
		t.Errorf("drop 后记录状态期望=error, 实际=%s", stuckRecord.Status)
	}
	if stuckRecord.ErrorMsg == "" {
		t.Error("drop 后记录的 ErrorMsg 不应为空")
	}
}

// TestStuckTxDetector_ConfirmedOnChain 测试链上已确认成功的交易。
func TestStuckTxDetector_ConfirmedOnChain(t *testing.T) {
	config := DefaultStuckTxConfig()

	var updatedRecords []*dexwallet.TxRecord

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return dexwallet.TxStatusConfirmed // 链上已确认
		},
		func() []*dexwallet.TxRecord {
			return []*dexwallet.TxRecord{
				{
					TxHash:    "0xSuccess",
					Status:    dexwallet.TxStatusPending,
					CreatedAt: time.Now().Add(-30 * time.Second),
				},
			}
		},
		func(record *dexwallet.TxRecord) {
			updatedRecords = append(updatedRecords, record)
		},
	)

	actions := detector.ScanOnce()
	if len(actions) != 1 {
		t.Fatalf("期望 1 个 action，实际=%d", len(actions))
	}
	if actions[0].Action != "confirm" {
		t.Errorf("链上已确认期望 Action=confirm, 实际=%s", actions[0].Action)
	}
	if len(updatedRecords) != 1 {
		t.Fatalf("期望 1 条记录被更新，实际=%d", len(updatedRecords))
	}
	if updatedRecords[0].Status != dexwallet.TxStatusConfirmed {
		t.Errorf("确认后记录状态期望=confirmed, 实际=%s", updatedRecords[0].Status)
	}
}

// TestStuckTxDetector_FailedOnChain 测试链上执行失败的交易。
func TestStuckTxDetector_FailedOnChain(t *testing.T) {
	config := DefaultStuckTxConfig()

	var updatedRecords []*dexwallet.TxRecord

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return dexwallet.TxStatusFailed // 链上执行失败
		},
		func() []*dexwallet.TxRecord {
			return []*dexwallet.TxRecord{
				{
					TxHash:    "0xFail",
					Status:    dexwallet.TxStatusPending,
					CreatedAt: time.Now().Add(-30 * time.Second),
				},
			}
		},
		func(record *dexwallet.TxRecord) {
			updatedRecords = append(updatedRecords, record)
		},
	)

	actions := detector.ScanOnce()
	if len(actions) != 1 {
		t.Fatalf("期望 1 个 action，实际=%d", len(actions))
	}
	if actions[0].Action != "confirm_failed" {
		t.Errorf("链上失败期望 Action=confirm_failed, 实际=%s", actions[0].Action)
	}
	if len(updatedRecords) != 1 {
		t.Fatalf("期望 1 条记录被更新，实际=%d", len(updatedRecords))
	}
	if updatedRecords[0].Status != dexwallet.TxStatusFailed {
		t.Errorf("失败后记录状态期望=failed, 实际=%s", updatedRecords[0].Status)
	}
}

// TestStuckTxDetector_PendingNotTimeout 测试 Pending 但未超时的交易不产生 action。
func TestStuckTxDetector_PendingNotTimeout(t *testing.T) {
	config := DefaultStuckTxConfig()
	config.PendingTimeout = 5 * time.Minute

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return dexwallet.TxStatusPending
		},
		func() []*dexwallet.TxRecord {
			return []*dexwallet.TxRecord{
				{
					TxHash:    "0xRecent",
					Status:    dexwallet.TxStatusPending,
					CreatedAt: time.Now().Add(-10 * time.Second), // 远未超时
				},
			}
		},
		func(record *dexwallet.TxRecord) {},
	)

	actions := detector.ScanOnce()
	if len(actions) != 0 {
		t.Errorf("未超时的 Pending 交易不应产生 action，实际=%d", len(actions))
	}
}

// TestStuckTxDetector_RetryCountManagement 测试重试计数的管理。
func TestStuckTxDetector_RetryCountManagement(t *testing.T) {
	config := DefaultStuckTxConfig()
	config.PendingTimeout = 1 * time.Second
	config.MaxRetries = 5

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus {
			return dexwallet.TxStatusPending
		},
		func() []*dexwallet.TxRecord {
			return []*dexwallet.TxRecord{
				{
					TxHash:    "0xRetryTest",
					Status:    dexwallet.TxStatusPending,
					CreatedAt: time.Now().Add(-10 * time.Second),
				},
			}
		},
		func(record *dexwallet.TxRecord) {},
	)

	// 初始重试计数为 0
	if detector.GetRetryCount("0xRetryTest") != 0 {
		t.Error("初始重试计数应为 0")
	}

	// 扫描一次，重试计数增加
	detector.ScanOnce()
	if detector.GetRetryCount("0xRetryTest") != 1 {
		t.Errorf("第 1 次扫描后重试计数期望=1, 实际=%d", detector.GetRetryCount("0xRetryTest"))
	}

	// 重置重试计数
	detector.ResetRetryCount("0xRetryTest")
	if detector.GetRetryCount("0xRetryTest") != 0 {
		t.Errorf("重置后重试计数期望=0, 实际=%d", detector.GetRetryCount("0xRetryTest"))
	}
}

// ---------------------------------------------------------------------------
// StuckTxAction 类型正确性
// ---------------------------------------------------------------------------

// TestStuckTxAction_Types 测试 StuckTxAction 的 Action 类型取值正确性。
func TestStuckTxAction_Types(t *testing.T) {
	// 定义所有合法的 Action 类型
	validActions := map[string]bool{
		"confirm":        true,
		"confirm_failed": true,
		"retry":          true,
		"drop":           true,
	}

	config := DefaultStuckTxConfig()
	config.PendingTimeout = 1 * time.Second
	config.MaxRetries = 1

	// 测试 confirm 类型
	t.Run("confirm", func(t *testing.T) {
		detector := NewStuckTxDetector(
			config,
			func(txHash string) dexwallet.TxStatus { return dexwallet.TxStatusConfirmed },
			func() []*dexwallet.TxRecord {
				return []*dexwallet.TxRecord{{
					TxHash: "0x1", Status: dexwallet.TxStatusPending,
					CreatedAt: time.Now(),
				}}
			},
			func(record *dexwallet.TxRecord) {},
		)
		actions := detector.ScanOnce()
		if len(actions) != 1 || !validActions[actions[0].Action] {
			t.Errorf("Action 类型不合法: %v", actions)
		}
		if actions[0].Action != "confirm" {
			t.Errorf("期望 Action=confirm, 实际=%s", actions[0].Action)
		}
	})

	// 测试 confirm_failed 类型
	t.Run("confirm_failed", func(t *testing.T) {
		detector := NewStuckTxDetector(
			config,
			func(txHash string) dexwallet.TxStatus { return dexwallet.TxStatusFailed },
			func() []*dexwallet.TxRecord {
				return []*dexwallet.TxRecord{{
					TxHash: "0x2", Status: dexwallet.TxStatusPending,
					CreatedAt: time.Now(),
				}}
			},
			func(record *dexwallet.TxRecord) {},
		)
		actions := detector.ScanOnce()
		if len(actions) != 1 || actions[0].Action != "confirm_failed" {
			t.Errorf("期望 Action=confirm_failed, 实际=%v", actions)
		}
	})

	// 测试 retry 类型
	t.Run("retry", func(t *testing.T) {
		detector := NewStuckTxDetector(
			config,
			func(txHash string) dexwallet.TxStatus { return dexwallet.TxStatusPending },
			func() []*dexwallet.TxRecord {
				return []*dexwallet.TxRecord{{
					TxHash: "0x3", Status: dexwallet.TxStatusPending,
					CreatedAt: time.Now().Add(-10 * time.Second),
				}}
			},
			func(record *dexwallet.TxRecord) {},
		)
		actions := detector.ScanOnce()
		if len(actions) != 1 || actions[0].Action != "retry" {
			t.Errorf("期望 Action=retry, 实际=%v", actions)
		}
	})

	// 测试 drop 类型
	t.Run("drop", func(t *testing.T) {
		record := &dexwallet.TxRecord{
			TxHash: "0x4", Status: dexwallet.TxStatusPending,
			CreatedAt: time.Now().Add(-10 * time.Second),
		}
		detector := NewStuckTxDetector(
			config,
			func(txHash string) dexwallet.TxStatus { return dexwallet.TxStatusPending },
			func() []*dexwallet.TxRecord { return []*dexwallet.TxRecord{record} },
			func(r *dexwallet.TxRecord) {},
		)
		// 第 1 次 → retry（消耗唯一的重试机会，MaxRetries=1）
		detector.ScanOnce()
		// 第 2 次 → drop
		actions := detector.ScanOnce()
		if len(actions) != 1 || actions[0].Action != "drop" {
			t.Errorf("期望 Action=drop, 实际=%v", actions)
		}
	})
}

// TestStuckTxAction_StructFields 测试 StuckTxAction 结构体字段完整性。
func TestStuckTxAction_StructFields(t *testing.T) {
	config := DefaultStuckTxConfig()
	config.PendingTimeout = 1 * time.Second

	detector := NewStuckTxDetector(
		config,
		func(txHash string) dexwallet.TxStatus { return dexwallet.TxStatusConfirmed },
		func() []*dexwallet.TxRecord {
			return []*dexwallet.TxRecord{{
				TxHash: "0xFields", Status: dexwallet.TxStatusPending,
				CreatedAt: time.Now(),
			}}
		},
		func(record *dexwallet.TxRecord) {},
	)

	actions := detector.ScanOnce()
	if len(actions) != 1 {
		t.Fatalf("期望 1 个 action, 实际=%d", len(actions))
	}

	action := actions[0]
	if action.TxHash == "" {
		t.Error("StuckTxAction.TxHash 不应为空")
	}
	if action.Record == nil {
		t.Error("StuckTxAction.Record 不应为 nil")
	}
	if action.Action == "" {
		t.Error("StuckTxAction.Action 不应为空")
	}
	if action.Reason == "" {
		t.Error("StuckTxAction.Reason 不应为空")
	}
}

// ---------------------------------------------------------------------------
// TxStateMachine 状态机测试
// ---------------------------------------------------------------------------

// TestStateMachine_LegalTransition_NewToConfirmed 合法状态转换：New -> Ready -> Pending -> Confirmed。
func TestStateMachine_LegalTransition_NewToConfirmed(t *testing.T) {
	record := &dexwallet.TxRecord{
		TxHash:  "0xabc123",
		ChainID: coinset.ChainSolana,
		Status:  dexwallet.TxStatusNew,
	}
	sm := NewTxStateMachine(record)

	// 初始状态应为 New
	if sm.Current() != dexwallet.TxStatusNew {
		t.Fatalf("初始状态期望=new, 实际=%s", sm.Current())
	}

	// New -> Ready
	if err := sm.Transition(dexwallet.TxStatusReady, "交易已构建"); err != nil {
		t.Fatalf("New->Ready 应合法, 错误: %v", err)
	}
	if sm.Current() != dexwallet.TxStatusReady {
		t.Fatalf("转换后期望=ready, 实际=%s", sm.Current())
	}

	// Ready -> Pending
	if err := sm.Transition(dexwallet.TxStatusPending, "交易已广播"); err != nil {
		t.Fatalf("Ready->Pending 应合法, 错误: %v", err)
	}
	if sm.Current() != dexwallet.TxStatusPending {
		t.Fatalf("转换后期望=pending, 实际=%s", sm.Current())
	}

	// Pending -> Confirmed
	if err := sm.Transition(dexwallet.TxStatusConfirmed, "交易已确认"); err != nil {
		t.Fatalf("Pending->Confirmed 应合法, 错误: %v", err)
	}
	if sm.Current() != dexwallet.TxStatusConfirmed {
		t.Fatalf("转换后期望=confirmed, 实际=%s", sm.Current())
	}
}

// TestStateMachine_IllegalTransition 非法状态转换应被拒绝。
func TestStateMachine_IllegalTransition(t *testing.T) {
	tests := []struct {
		name string
		from dexwallet.TxStatus
		to   dexwallet.TxStatus
	}{
		{"Confirmed->Pending", dexwallet.TxStatusConfirmed, dexwallet.TxStatusPending},
		{"Confirmed->Ready", dexwallet.TxStatusConfirmed, dexwallet.TxStatusReady},
		{"Confirmed->New", dexwallet.TxStatusConfirmed, dexwallet.TxStatusNew},
		{"Failed->Pending", dexwallet.TxStatusFailed, dexwallet.TxStatusPending},
		{"Failed->Confirmed", dexwallet.TxStatusFailed, dexwallet.TxStatusConfirmed},
		{"New->Confirmed", dexwallet.TxStatusNew, dexwallet.TxStatusConfirmed},
		{"New->Pending", dexwallet.TxStatusNew, dexwallet.TxStatusPending},
		{"Ready->Confirmed", dexwallet.TxStatusReady, dexwallet.TxStatusConfirmed},
		{"Replace->New", dexwallet.TxStatusReplace, dexwallet.TxStatusNew},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &dexwallet.TxRecord{
				TxHash: "0xtest",
				Status: tt.from,
			}
			sm := NewTxStateMachine(record)
			err := sm.Transition(tt.to, "测试非法转换")
			if err == nil {
				t.Errorf("期望 %s->%s 被拒绝, 实际无错误", tt.from, tt.to)
			}
			// 状态应保持不变
			if sm.Current() != tt.from {
				t.Errorf("非法转换后状态不应改变: 期望=%s, 实际=%s", tt.from, sm.Current())
			}
		})
	}
}

// TestStateMachine_HistoryRecord 验证状态变更历史记录。
func TestStateMachine_HistoryRecord(t *testing.T) {
	record := &dexwallet.TxRecord{
		TxHash: "0xhist",
		Status: dexwallet.TxStatusNew,
	}
	sm := NewTxStateMachine(record)

	// 初始历史为空
	if len(sm.History()) != 0 {
		t.Fatalf("初始历史应为空, 实际长度=%d", len(sm.History()))
	}

	// 执行一系列合法转换
	transitions := []struct {
		to     dexwallet.TxStatus
		reason string
	}{
		{dexwallet.TxStatusReady, "构建完成"},
		{dexwallet.TxStatusPending, "已广播"},
		{dexwallet.TxStatusConfirmed, "已确认"},
	}

	for _, tr := range transitions {
		if err := sm.Transition(tr.to, tr.reason); err != nil {
			t.Fatalf("转换到 %s 失败: %v", tr.to, err)
		}
	}

	history := sm.History()
	if len(history) != 3 {
		t.Fatalf("期望 3 条历史记录, 实际=%d", len(history))
	}

	// 验证第一条记录
	if history[0].From != dexwallet.TxStatusNew || history[0].To != dexwallet.TxStatusReady {
		t.Errorf("第 1 条记录: 期望 new->ready, 实际 %s->%s", history[0].From, history[0].To)
	}
	if history[0].Reason != "构建完成" {
		t.Errorf("第 1 条记录原因: 期望='构建完成', 实际='%s'", history[0].Reason)
	}

	// 验证第二条记录
	if history[1].From != dexwallet.TxStatusReady || history[1].To != dexwallet.TxStatusPending {
		t.Errorf("第 2 条记录: 期望 ready->pending, 实际 %s->%s", history[1].From, history[1].To)
	}

	// 验证第三条记录
	if history[2].From != dexwallet.TxStatusPending || history[2].To != dexwallet.TxStatusConfirmed {
		t.Errorf("第 3 条记录: 期望 pending->confirmed, 实际 %s->%s", history[2].From, history[2].To)
	}

	// 验证时间戳单调递增
	for i := 1; i < len(history); i++ {
		if history[i].Timestamp.Before(history[i-1].Timestamp) {
			t.Errorf("第 %d 条记录的时间戳早于第 %d 条", i+1, i)
		}
	}

	// 验证 History 返回的是副本（修改不影响原始数据）
	historyCopy := sm.History()
	historyCopy[0].Reason = "被修改了"
	if sm.History()[0].Reason == "被修改了" {
		t.Error("History() 应返回副本, 修改副本不应影响原始数据")
	}
}

// TestStateMachine_ErrorRecovery 测试 Error 状态可以恢复到 New。
func TestStateMachine_ErrorRecovery(t *testing.T) {
	record := &dexwallet.TxRecord{
		TxHash: "0xerr",
		Status: dexwallet.TxStatusNew,
	}
	sm := NewTxStateMachine(record)

	// New -> Error
	if err := sm.Transition(dexwallet.TxStatusError, "签名失败"); err != nil {
		t.Fatalf("New->Error 应合法: %v", err)
	}

	// Error -> New（重试）
	if err := sm.Transition(dexwallet.TxStatusNew, "重试"); err != nil {
		t.Fatalf("Error->New 应合法: %v", err)
	}

	if sm.Current() != dexwallet.TxStatusNew {
		t.Errorf("期望恢复到 new, 实际=%s", sm.Current())
	}
}

// TestStateMachine_RevertFlow 测试 Confirmed -> Revert -> Pending 重组流程。
func TestStateMachine_RevertFlow(t *testing.T) {
	record := &dexwallet.TxRecord{
		TxHash: "0xrev",
		Status: dexwallet.TxStatusPending,
	}
	sm := NewTxStateMachine(record)

	// Pending -> Confirmed
	if err := sm.Transition(dexwallet.TxStatusConfirmed, "确认"); err != nil {
		t.Fatalf("Pending->Confirmed: %v", err)
	}

	// Confirmed -> Revert（分叉回滚）
	if err := sm.Transition(dexwallet.TxStatusRevert, "区块重组"); err != nil {
		t.Fatalf("Confirmed->Revert: %v", err)
	}

	// Revert -> Pending（重新广播）
	if err := sm.Transition(dexwallet.TxStatusPending, "重新广播"); err != nil {
		t.Fatalf("Revert->Pending: %v", err)
	}

	if sm.Current() != dexwallet.TxStatusPending {
		t.Errorf("期望状态=pending, 实际=%s", sm.Current())
	}
}

// ---------------------------------------------------------------------------
// RejectCode 错误码测试
// ---------------------------------------------------------------------------

// TestStateMachine_RejectCodeString 测试 RejectCode 的 String() 方法。
func TestStateMachine_RejectCodeString(t *testing.T) {
	tests := []struct {
		code     RejectCode
		contains string // 期望 String() 包含的子串
	}{
		{RejectOK, "成功"},
		{RejectInvalidOrderID, "无效的订单 ID"},
		{RejectInvalidAmount, "无效的交易金额"},
		{RejectSlippageOverflow, "滑点超出允许范围"},
		{RejectInsufficientBalance, "余额不足"},
		{RejectServerError, "服务器内部错误"},
		{RejectCode(99999), "未知错误"}, // 未定义的错误码
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("code_%d", int(tt.code)), func(t *testing.T) {
			s := tt.code.String()
			if s == "" {
				t.Error("String() 不应返回空字符串")
			}
			// 验证包含期望子串
			found := false
			for i := 0; i <= len(s)-len(tt.contains); i++ {
				if s[i:i+len(tt.contains)] == tt.contains {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("String()='%s' 应包含 '%s'", s, tt.contains)
			}
		})
	}
}

// TestStateMachine_RejectCodeIsSuccess 测试 IsSuccess() 方法。
func TestStateMachine_RejectCodeIsSuccess(t *testing.T) {
	if !RejectOK.IsSuccess() {
		t.Error("RejectOK.IsSuccess() 应返回 true")
	}

	failureCodes := []RejectCode{
		RejectInvalidOrderID, RejectInvalidAmount, RejectSlippageOverflow,
		RejectInsufficientBalance, RejectServerError,
	}
	for _, code := range failureCodes {
		if code.IsSuccess() {
			t.Errorf("RejectCode(%d).IsSuccess() 应返回 false", int(code))
		}
	}
}

// TestStateMachine_RejectCodeCategory 测试 Category() 方法。
func TestStateMachine_RejectCodeCategory(t *testing.T) {
	tests := []struct {
		code     RejectCode
		category string
	}{
		{RejectOK, "成功"},
		{RejectInvalidOrderID, "订单参数"},
		{RejectInvalidUserID, "订单参数"},
		{RejectInvalidAmount, "金额费用"},
		{RejectInvalidFee, "金额费用"},
		{RejectInvalidSlippage, "比率滑点"},
		{RejectSlippageOverflow, "比率滑点"},
		{RejectInvalidContract, "合约DEX"},
		{RejectDexNotFound, "合约DEX"},
		{RejectInsufficientBalance, "余额流动性"},
		{RejectGasTooHigh, "余额流动性"},
		{RejectLiquidityTooLow, "余额流动性"},
		{RejectServerError, "内部错误"},
		{RejectCode(50000), "未知分类"}, // 未定义的段
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("code_%d", int(tt.code)), func(t *testing.T) {
			got := tt.code.Category()
			if got != tt.category {
				t.Errorf("RejectCode(%d).Category() = '%s', 期望='%s'", int(tt.code), got, tt.category)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NonceManager 测试
// ---------------------------------------------------------------------------

// mockGetTransactionCount 模拟链上 nonce 查询，返回固定值。
func mockGetTransactionCount(startNonce uint64) func(string) (uint64, error) {
	return func(_ string) (uint64, error) {
		return startNonce, nil
	}
}

// TestNonceManager_AcquireFirstSync 首次 AcquireNonce 应从链上同步。
func TestNonceManager_AcquireFirstSync(t *testing.T) {
	var syncCalled atomic.Int64
	nm := NewNonceManager(func(address string) (uint64, error) {
		syncCalled.Add(1)
		return 42, nil
	})

	nonce, release, err := nm.AcquireNonce("0xABC")
	if err != nil {
		t.Fatalf("AcquireNonce 失败: %v", err)
	}
	release()

	if nonce != 42 {
		t.Errorf("首次 nonce 期望=42 (链上值), 实际=%d", nonce)
	}
	if syncCalled.Load() != 1 {
		t.Errorf("期望调用 1 次链上查询, 实际=%d", syncCalled.Load())
	}
}

// TestNonceManager_AcquireIncrement 多次 AcquireNonce 应递增。
func TestNonceManager_AcquireIncrement(t *testing.T) {
	nm := NewNonceManager(mockGetTransactionCount(10))

	// 获取首次 nonce（同步后值为 10）
	nonce1, release1, err := nm.AcquireNonce("0xABC")
	if err != nil {
		t.Fatalf("第 1 次 AcquireNonce 失败: %v", err)
	}
	release1()

	if nonce1 != 10 {
		t.Errorf("第 1 次 nonce 期望=10, 实际=%d", nonce1)
	}

	// 第二次应递增
	nonce2, release2, err := nm.AcquireNonce("0xABC")
	if err != nil {
		t.Fatalf("第 2 次 AcquireNonce 失败: %v", err)
	}
	release2()

	if nonce2 != 11 {
		t.Errorf("第 2 次 nonce 期望=11, 实际=%d", nonce2)
	}

	// 第三次继续递增
	nonce3, release3, err := nm.AcquireNonce("0xABC")
	if err != nil {
		t.Fatalf("第 3 次 AcquireNonce 失败: %v", err)
	}
	release3()

	if nonce3 != 12 {
		t.Errorf("第 3 次 nonce 期望=12, 实际=%d", nonce3)
	}
}

// TestNonceManager_ResetNonce 测试 ResetNonce 后重新同步。
func TestNonceManager_ResetNonce(t *testing.T) {
	callCount := atomic.Int64{}
	nm := NewNonceManager(func(address string) (uint64, error) {
		count := callCount.Add(1)
		if count <= 1 {
			return 10, nil // 首次同步返回 10
		}
		return 20, nil // 重置后同步返回 20
	})

	// 首次获取
	nonce1, release1, err := nm.AcquireNonce("0xABC")
	if err != nil {
		t.Fatalf("首次 AcquireNonce 失败: %v", err)
	}
	release1()
	if nonce1 != 10 {
		t.Errorf("首次 nonce 期望=10, 实际=%d", nonce1)
	}

	// 第二次获取（本地递增到 11）
	nonce2, release2, err := nm.AcquireNonce("0xABC")
	if err != nil {
		t.Fatalf("第二次 AcquireNonce 失败: %v", err)
	}
	release2()
	if nonce2 != 11 {
		t.Errorf("第二次 nonce 期望=11, 实际=%d", nonce2)
	}

	// 重置 nonce
	nm.ResetNonce("0xABC")

	// 重置后应重新从链上同步（返回 20）
	nonce3, release3, err := nm.AcquireNonce("0xABC")
	if err != nil {
		t.Fatalf("重置后 AcquireNonce 失败: %v", err)
	}
	release3()
	if nonce3 != 20 {
		t.Errorf("重置后 nonce 期望=20 (新的链上值), 实际=%d", nonce3)
	}
}

// TestNonceManager_ResetNonce_NonExistentAddress 重置不存在的地址应无副作用。
func TestNonceManager_ResetNonce_NonExistentAddress(t *testing.T) {
	nm := NewNonceManager(mockGetTransactionCount(0))

	// 重置一个从未使用过的地址，不应 panic
	nm.ResetNonce("0xNonExistent")
}

// TestNonceManager_PeekNonce 测试 PeekNonce 返回正确值。
func TestNonceManager_PeekNonce(t *testing.T) {
	nm := NewNonceManager(mockGetTransactionCount(15))

	// 地址不存在时，PeekNonce 应直接查链上
	nonce, err := nm.PeekNonce("0xABC")
	if err != nil {
		t.Fatalf("PeekNonce 失败: %v", err)
	}
	if nonce != 15 {
		t.Errorf("PeekNonce (未同步) 期望=15, 实际=%d", nonce)
	}

	// 先 AcquireNonce 同步并递增
	n, release, err := nm.AcquireNonce("0xABC")
	if err != nil {
		t.Fatalf("AcquireNonce 失败: %v", err)
	}
	release()
	if n != 15 {
		t.Errorf("AcquireNonce 期望=15, 实际=%d", n)
	}

	// PeekNonce 应返回递增后的值（16）
	nonce, err = nm.PeekNonce("0xABC")
	if err != nil {
		t.Fatalf("PeekNonce 失败: %v", err)
	}
	if nonce != 16 {
		t.Errorf("PeekNonce (已同步) 期望=16, 实际=%d", nonce)
	}
}

// TestNonceManager_PeekNonce_RPCError 测试 PeekNonce 在 RPC 失败时返回错误。
func TestNonceManager_PeekNonce_RPCError(t *testing.T) {
	nm := NewNonceManager(func(_ string) (uint64, error) {
		return 0, fmt.Errorf("RPC 不可用")
	})

	_, err := nm.PeekNonce("0xABC")
	if err == nil {
		t.Fatal("RPC 失败时 PeekNonce 应返回错误")
	}
}

// TestNonceManager_AcquireNonce_RPCError 测试首次同步 RPC 失败。
func TestNonceManager_AcquireNonce_RPCError(t *testing.T) {
	nm := NewNonceManager(func(_ string) (uint64, error) {
		return 0, fmt.Errorf("RPC 连接失败")
	})

	_, _, err := nm.AcquireNonce("0xABC")
	if err == nil {
		t.Fatal("RPC 失败时 AcquireNonce 应返回错误")
	}
}

// TestNonceManager_MultipleAddresses 不同地址应独立管理 nonce。
func TestNonceManager_MultipleAddresses(t *testing.T) {
	nm := NewNonceManager(func(address string) (uint64, error) {
		switch address {
		case "0xAAA":
			return 100, nil
		case "0xBBB":
			return 200, nil
		default:
			return 0, nil
		}
	})

	nonceA, releaseA, err := nm.AcquireNonce("0xAAA")
	if err != nil {
		t.Fatalf("AcquireNonce(0xAAA) 失败: %v", err)
	}
	releaseA()

	nonceB, releaseB, err := nm.AcquireNonce("0xBBB")
	if err != nil {
		t.Fatalf("AcquireNonce(0xBBB) 失败: %v", err)
	}
	releaseB()

	if nonceA != 100 {
		t.Errorf("地址 0xAAA 的 nonce 期望=100, 实际=%d", nonceA)
	}
	if nonceB != 200 {
		t.Errorf("地址 0xBBB 的 nonce 期望=200, 实际=%d", nonceB)
	}
}

// TestNonceManager_ConcurrentAcquire 并发安全测试：多个 goroutine 同时 AcquireNonce。
func TestNonceManager_ConcurrentAcquire(t *testing.T) {
	nm := NewNonceManager(mockGetTransactionCount(0))

	const goroutines = 50
	var wg sync.WaitGroup
	nonces := make([]uint64, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			nonce, release, err := nm.AcquireNonce("0xConcurrent")
			if err != nil {
				t.Errorf("goroutine %d: AcquireNonce 失败: %v", idx, err)
				return
			}
			nonces[idx] = nonce
			// 模拟使用 nonce 的时间
			time.Sleep(1 * time.Millisecond)
			release()
		}(i)
	}

	wg.Wait()

	// 验证所有 nonce 都是唯一的（无重复）
	seen := make(map[uint64]bool)
	for i, n := range nonces {
		if seen[n] {
			t.Errorf("nonce %d 被重复分配 (goroutine %d)", n, i)
		}
		seen[n] = true
	}

	// 验证 nonce 范围：从 0 开始，连续分配到 goroutines-1
	if len(seen) != goroutines {
		t.Errorf("期望 %d 个唯一 nonce, 实际=%d", goroutines, len(seen))
	}

	for n := uint64(0); n < goroutines; n++ {
		if !seen[n] {
			t.Errorf("缺少 nonce %d", n)
		}
	}
}

// TestNonceManager_ConcurrentDifferentAddresses 不同地址的并发操作互不阻塞。
func TestNonceManager_ConcurrentDifferentAddresses(t *testing.T) {
	nm := NewNonceManager(func(address string) (uint64, error) {
		return 0, nil
	})

	var wg sync.WaitGroup
	addresses := []string{"0xAddr1", "0xAddr2", "0xAddr3", "0xAddr4", "0xAddr5"}

	for _, addr := range addresses {
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(a string) {
				defer wg.Done()
				nonce, release, err := nm.AcquireNonce(a)
				if err != nil {
					t.Errorf("AcquireNonce(%s) 失败: %v", a, err)
					return
				}
				_ = nonce
				time.Sleep(1 * time.Millisecond)
				release()
			}(addr)
		}
	}

	wg.Wait()
	// 能跑到这里不 panic/deadlock 就说明不同地址的并发操作是安全的
}
