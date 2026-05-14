// 08-production-architecture 监控告警服务。
// 实现定时监控检查和告警触发。
//
// [dexwallet 通用层] -- 监控和告警对所有链一致。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/alarm"
	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// MonitorService 监控服务。
// 定期收集所有链的监控指标，检测异常并触发告警。
type MonitorService struct {
	mu       sync.RWMutex
	monitors map[coinset.ChainID]*dexwallet.Monitor
	alarm    *alarm.Manager
	interval time.Duration
}

// NewMonitorService 创建监控服务。
// interval 是检查间隔。
func NewMonitorService(alarmMgr *alarm.Manager, interval time.Duration) *MonitorService {
	return &MonitorService{
		monitors: make(map[coinset.ChainID]*dexwallet.Monitor),
		alarm:    alarmMgr,
		interval: interval,
	}
}

// RegisterChain 为某条链注册 Monitor。
func (s *MonitorService) RegisterChain(chainID coinset.ChainID) *dexwallet.Monitor {
	s.mu.Lock()
	defer s.mu.Unlock()

	monitor := dexwallet.NewMonitor(chainID, s.alarm)
	s.monitors[chainID] = monitor

	slog.Info("链监控器已注册", "chain", chainID)
	return monitor
}

// GetMonitor 获取某条链的 Monitor。
func (s *MonitorService) GetMonitor(chainID coinset.ChainID) (*dexwallet.Monitor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	monitor, ok := s.monitors[chainID]
	if !ok {
		return nil, fmt.Errorf("monitor not found for chain: %s", chainID)
	}
	return monitor, nil
}

// Start 启动监控服务的定时检查循环。
// 每隔 interval 时间执行一次所有链的监控检查。
func (s *MonitorService) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		slog.Info("监控服务已启动", "interval", s.interval)

		for {
			select {
			case <-ctx.Done():
				slog.Info("监控服务已停止")
				return
			case <-ticker.C:
				s.runChecks(ctx)
			}
		}
	}()
}

// runChecks 运行一轮监控检查。
func (s *MonitorService) runChecks(ctx context.Context) {
	s.mu.RLock()
	monitors := make(map[coinset.ChainID]*dexwallet.Monitor, len(s.monitors))
	for id, m := range s.monitors {
		monitors[id] = m
	}
	s.mu.RUnlock()

	for chainID, monitor := range monitors {
		// 运行 Monitor 的内置检查（区块停滞、报价失败率等）
		monitor.RunChecks(ctx)

		// 收集统计信息用于日志
		stats := monitor.GetStats()
		slog.Debug("监控检查完成",
			"chain", chainID,
			"swap_total", stats["swap_total"],
			"swap_fail", stats["swap_fail"],
			"quote_total", stats["quote_total"],
			"quote_fail", stats["quote_fail"],
			"block_height", stats["block_height"],
		)
	}
}

// RunOnce 手动运行一次监控检查（用于测试）。
func (s *MonitorService) RunOnce(ctx context.Context) {
	s.runChecks(ctx)
}

// CollectAllStats 收集所有链的监控统计。
func (s *MonitorService) CollectAllStats() map[coinset.ChainID]map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[coinset.ChainID]map[string]int64, len(s.monitors))
	for chainID, monitor := range s.monitors {
		result[chainID] = monitor.GetStats()
	}
	return result
}

// ---------------------------------------------------------------------------
// 模拟监控数据的辅助函数
// ---------------------------------------------------------------------------

// SimulateSwapActivity 模拟 Swap 活动，生成监控数据。
func SimulateSwapActivity(monitor *dexwallet.Monitor, totalSwaps int, failRate float64) {
	for i := 0; i < totalSwaps; i++ {
		// 按失败率决定本次 Swap 是否成功
		success := float64(i)/float64(totalSwaps) >= failRate
		monitor.RecordSwap(success)
	}
}

// SimulateQuoteActivity 模拟报价活动，生成监控数据。
func SimulateQuoteActivity(monitor *dexwallet.Monitor, totalQuotes int, failRate float64) {
	for i := 0; i < totalQuotes; i++ {
		success := float64(i)/float64(totalQuotes) >= failRate
		monitor.RecordQuote(success)
	}
}

// SimulateBlockUpdates 模拟区块高度更新。
func SimulateBlockUpdates(monitor *dexwallet.Monitor, startHeight uint64, count int) {
	for i := 0; i < count; i++ {
		monitor.UpdateBlockHeight(startHeight + uint64(i))
	}
}
