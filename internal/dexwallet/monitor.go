package dexwallet

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/alarm"
	"github.com/yys9517/onchain-dex-lab/internal/coinset"
)

// MetricType 监控指标类型。
type MetricType string

const (
	MetricBlockHeight   MetricType = "block_height"    // 区块高度（停滞告警）
	MetricTxTimeout     MetricType = "tx_timeout"      // 交易超时
	MetricBalance       MetricType = "balance"         // 余额异常
	MetricPoolState     MetricType = "pool_state"      // 池子状态异常
	MetricSwapLatency   MetricType = "swap_latency"    // Swap 延迟
	MetricQuoteFailRate MetricType = "quote_fail_rate" // 报价失败率
	MetricRPCHealth     MetricType = "rpc_health"      // RPC 健康状态
)

// MonitorConfig 监控配置（P1-5: 阈值可配置，不再硬编码）。
type MonitorConfig struct {
	BlockStaleThreshold   time.Duration // 区块停滞阈值
	QuoteFailRateThreshold float64      // 报价失败率阈值（0.0-1.0）
	QuoteMinSamples       int64         // 触发失败率告警的最小样本数
}

// DefaultMonitorConfig 默认监控配置。
func DefaultMonitorConfig() MonitorConfig {
	return MonitorConfig{
		BlockStaleThreshold:    30 * time.Second,
		QuoteFailRateThreshold: 0.5,
		QuoteMinSamples:        10,
	}
}

// Monitor 通用监控指标收集器。
// [dexwallet 通用层] — 7 个监控指标对 Solana 和 EVM 完全一致。
type Monitor struct {
	mu      sync.RWMutex
	chainID coinset.ChainID
	alarm   *alarm.Manager
	config  MonitorConfig

	// 原子计数器
	swapCount      atomic.Int64
	swapFailCount  atomic.Int64
	quoteCount     atomic.Int64
	quoteFailCount atomic.Int64

	// 区块高度监控
	lastBlockHeight atomic.Uint64
	lastBlockTime   atomic.Int64 // unix timestamp
}

// NewMonitor 创建监控器（使用默认配置）。
func NewMonitor(chainID coinset.ChainID, alarmMgr *alarm.Manager) *Monitor {
	return NewMonitorWithConfig(chainID, alarmMgr, DefaultMonitorConfig())
}

// NewMonitorWithConfig 使用自定义配置创建监控器。
func NewMonitorWithConfig(chainID coinset.ChainID, alarmMgr *alarm.Manager, cfg MonitorConfig) *Monitor {
	return &Monitor{
		chainID: chainID,
		alarm:   alarmMgr,
		config:  cfg,
	}
}

// RecordSwap 记录 Swap 交易。
func (m *Monitor) RecordSwap(success bool) {
	m.swapCount.Add(1)
	if !success {
		m.swapFailCount.Add(1)
	}
}

// RecordQuote 记录报价请求。
func (m *Monitor) RecordQuote(success bool) {
	m.quoteCount.Add(1)
	if !success {
		m.quoteFailCount.Add(1)
	}
}

// UpdateBlockHeight 更新区块高度。
func (m *Monitor) UpdateBlockHeight(height uint64) {
	m.lastBlockHeight.Store(height)
	m.lastBlockTime.Store(time.Now().Unix())
}

// CheckBlockStale 检查区块是否停滞。
func (m *Monitor) CheckBlockStale() bool {
	lastTime := m.lastBlockTime.Load()
	if lastTime == 0 {
		return false
	}
	elapsed := time.Since(time.Unix(lastTime, 0))
	return elapsed > m.config.BlockStaleThreshold
}

// GetStats 获取监控统计。
func (m *Monitor) GetStats() map[string]int64 {
	return map[string]int64{
		"swap_total":       m.swapCount.Load(),
		"swap_fail":        m.swapFailCount.Load(),
		"quote_total":      m.quoteCount.Load(),
		"quote_fail":       m.quoteFailCount.Load(),
		"block_height":     int64(m.lastBlockHeight.Load()),
		"block_last_time":  m.lastBlockTime.Load(),
	}
}

// RunChecks 运行所有监控检查，发送告警。
func (m *Monitor) RunChecks(ctx context.Context) {
	// 检查区块停滞
	if m.CheckBlockStale() {
		m.alarm.Send(ctx, alarm.Alert{
			Level:   alarm.LevelCritical,
			Title:   "Block Height Stale",
			Message: "block height not updated for " + m.config.BlockStaleThreshold.String(),
			Chain:   string(m.chainID),
			Module:  "monitor",
		})
	}

	// 检查报价失败率（P1-5: 使用可配置阈值）
	total := m.quoteCount.Load()
	fail := m.quoteFailCount.Load()
	if total > m.config.QuoteMinSamples && float64(fail)/float64(total) > m.config.QuoteFailRateThreshold {
		m.alarm.Send(ctx, alarm.Alert{
			Level:   alarm.LevelWarning,
			Title:   "High Quote Failure Rate",
			Message: "quote failure rate > 50%",
			Chain:   string(m.chainID),
			Module:  "aggregator",
		})
	}

	slog.Debug("monitor checks completed",
		"chain", m.chainID,
		"stats", m.GetStats(),
	)
}
