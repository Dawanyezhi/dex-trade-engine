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
