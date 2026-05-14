package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"math/rand"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ----- Solana 贿赂服务实现（mock）-----

// baseBribeService 贿赂服务的通用基础结构。
type baseBribeService struct {
	mu             sync.Mutex
	name           string
	region         string
	recommendedFee *big.Int
	failRate       float64       // 模拟失败率（0.0 ~ 1.0）
	latency        time.Duration // 模拟网络延迟
}

// Send 通过贿赂服务发送交易（mock 实现）。
func (b *baseBribeService) Send(ctx context.Context, txData []byte, fee *big.Int) (string, error) {
	b.mu.Lock()
	failRate := b.failRate
	latency := b.latency
	name := b.name
	b.mu.Unlock()

	// 模拟网络延迟
	select {
	case <-time.After(latency):
	case <-ctx.Done():
		return "", fmt.Errorf("%s: %w", name, ctx.Err())
	}

	// 模拟随机失败
	if rand.Float64() < failRate {
		return "", fmt.Errorf("%s: 发送失败（模拟故障）", name)
	}

	// 生成模拟的交易哈希
	txHash := fmt.Sprintf("%s_%x_%d", name, txData[:min(4, len(txData))], time.Now().UnixNano()%100000)

	slog.Debug("贿赂服务发送成功",
		"service", name,
		"tx_hash", txHash,
		"fee", fee,
	)

	return txHash, nil
}

// GetRecommendedFee 获取推荐的贿赂费。
func (b *baseBribeService) GetRecommendedFee(_ context.Context) (*big.Int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return new(big.Int).Set(b.recommendedFee), nil
}

// Name 返回服务商名称。
func (b *baseBribeService) Name() string {
	return b.name
}

// SetFailRate 设置模拟失败率。
func (b *baseBribeService) SetFailRate(rate float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failRate = rate
}

// SetLatency 设置模拟延迟。
func (b *baseBribeService) SetLatency(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.latency = d
}

// ----- 5 个 Solana 贿赂服务商 -----

// NextBlockService NextBlock 贿赂服务 -- 多地区部署，覆盖面广。
type NextBlockService struct {
	baseBribeService
}

// NewNextBlockService 创建 NextBlock 服务。
func NewNextBlockService() *NextBlockService {
	return &NextBlockService{
		baseBribeService: baseBribeService{
			name:           "nextblock",
			region:         "multi-region",
			recommendedFee: big.NewInt(10000), // 10000 lamports
			failRate:       0.05,              // 5% 失败率
			latency:        50 * time.Millisecond,
		},
	}
}

// TemporalService Temporal 贿赂服务 -- 全球 CDN 加速，低延迟。
type TemporalService struct {
	baseBribeService
}

// NewTemporalService 创建 Temporal 服务。
func NewTemporalService() *TemporalService {
	return &TemporalService{
		baseBribeService: baseBribeService{
			name:           "temporal",
			region:         "global-cdn",
			recommendedFee: big.NewInt(15000), // 15000 lamports
			failRate:       0.08,              // 8% 失败率
			latency:        30 * time.Millisecond,
		},
	}
}

// ZeroSlotService ZeroSlot 贿赂服务 -- 低滑点优化。
type ZeroSlotService struct {
	baseBribeService
}

// NewZeroSlotService 创建 ZeroSlot 服务。
func NewZeroSlotService() *ZeroSlotService {
	return &ZeroSlotService{
		baseBribeService: baseBribeService{
			name:           "zeroslot",
			region:         "us-east",
			recommendedFee: big.NewInt(12000), // 12000 lamports
			failRate:       0.10,              // 10% 失败率
			latency:        60 * time.Millisecond,
		},
	}
}

// BlockRazorService BlockRazor 贿赂服务 -- MEV 保护模式。
type BlockRazorService struct {
	baseBribeService
}

// NewBlockRazorService 创建 BlockRazor 服务。
func NewBlockRazorService() *BlockRazorService {
	return &BlockRazorService{
		baseBribeService: baseBribeService{
			name:           "blockrazor",
			region:         "asia-pacific",
			recommendedFee: big.NewInt(20000), // 20000 lamports（MEV 保护费用更高）
			failRate:       0.12,              // 12% 失败率
			latency:        80 * time.Millisecond,
		},
	}
}

// BlockRushService BlockRush 贿赂服务 -- 高吞吐量批量提交。
type BlockRushService struct {
	baseBribeService
}

// NewBlockRushService 创建 BlockRush 服务。
func NewBlockRushService() *BlockRushService {
	return &BlockRushService{
		baseBribeService: baseBribeService{
			name:           "blockrush",
			region:         "europe",
			recommendedFee: big.NewInt(8000), // 8000 lamports（批量折扣）
			failRate:       0.15,             // 15% 失败率
			latency:        70 * time.Millisecond,
		},
	}
}

// ----- EVM Anti-MEV -----

// AntiMEVRPC EVM Anti-MEV RPC 服务（Flashbots Protect / MEV Blocker）。
// 通过私有交易池保护交易不被三明治攻击。
type AntiMEVRPC struct {
	mu       sync.Mutex
	name     string
	endpoint string
	provider string // "flashbots_protect" 或 "mev_blocker"
	failRate float64
	latency  time.Duration
}

// NewAntiMEVRPC 创建 Anti-MEV RPC 服务。
func NewAntiMEVRPC(provider string) *AntiMEVRPC {
	var endpoint string
	switch provider {
	case "flashbots_protect":
		endpoint = "https://rpc.flashbots.net"
	case "mev_blocker":
		endpoint = "https://rpc.mevblocker.io"
	default:
		endpoint = "https://rpc.flashbots.net"
		provider = "flashbots_protect"
	}

	return &AntiMEVRPC{
		name:     fmt.Sprintf("anti-mev-%s", provider),
		endpoint: endpoint,
		provider: provider,
		failRate: 0.03,
		latency:  100 * time.Millisecond,
	}
}

// Send 通过 Anti-MEV RPC 发送交易（mock 实现）。
func (a *AntiMEVRPC) Send(ctx context.Context, txData []byte, _ *big.Int) (string, error) {
	a.mu.Lock()
	failRate := a.failRate
	latency := a.latency
	name := a.name
	a.mu.Unlock()

	// 模拟网络延迟（Anti-MEV 通常延迟更高）
	select {
	case <-time.After(latency):
	case <-ctx.Done():
		return "", fmt.Errorf("%s: %w", name, ctx.Err())
	}

	// 模拟随机失败
	if rand.Float64() < failRate {
		return "", fmt.Errorf("%s: 提交到私有 mempool 失败", name)
	}

	txHash := fmt.Sprintf("0x%x_%s_%d", txData[:min(4, len(txData))], a.provider, time.Now().UnixNano()%100000)

	slog.Debug("Anti-MEV 发送成功",
		"provider", a.provider,
		"endpoint", a.endpoint,
		"tx_hash", txHash,
	)

	return txHash, nil
}

// GetRecommendedFee 返回推荐费用。
// Anti-MEV RPC 不需要额外费用（保护是免费的）。
func (a *AntiMEVRPC) GetRecommendedFee(_ context.Context) (*big.Int, error) {
	return big.NewInt(0), nil
}

// Name 返回服务名称。
func (a *AntiMEVRPC) Name() string {
	return a.name
}

// SetFailRate 设置模拟失败率。
func (a *AntiMEVRPC) SetFailRate(rate float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failRate = rate
}

// SetLatency 设置模拟延迟。
func (a *AntiMEVRPC) SetLatency(d time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.latency = d
}

// =====================================================================
// BribeServiceManager -- 贿赂服务健康管理器
// =====================================================================
//
// 设计对比（vs 01-rpc-client/StableClient）：
//
//   StableClient（RPC 客户端）：
//     模式：顺序故障转移（A 失败 → 尝试 B → 尝试 C）
//     原因：RPC 调用是幂等的读/写操作，只需要一个成功即可
//     特点：节省资源，按优先级逐个尝试
//
//   BribeServiceManager（贿赂服务）：
//     模式：并行广播（同时发 A+B+C+D+E，取最快成功的）
//     原因：贿赂服务的核心目标是"最快上链"，多发一份不会有副作用
//     特点：Solana 交易按签名去重，同一笔交易被多个服务商转发不会重复执行
//
// 错误处理策略：
//   1. 健康追踪：每个服务维护连续失败计数，超过阈值标记为不健康
//   2. 跳过不健康：并行广播时跳过不健康服务，避免浪费资源和增加无意义的错误日志
//   3. 冷却恢复：不健康服务过了冷却期后，自动纳入下次广播进行探测
//   4. 全挂告警：所有服务都不健康时，触发 onAllDown 回调（接入告警系统）
//   5. 统计记录：记录每个服务的成功/失败次数，用于监控和调优
//
// 注意：贿赂服务是 Solana 独有的概念。EVM 链使用 Anti-MEV RPC（私有 mempool），
// 机制完全不同，不适用此管理器。

// BribeServiceEntry 单个贿赂服务的健康状态和统计信息。
type BribeServiceEntry struct {
	service          dexwallet.BribeService
	healthy          bool
	consecutiveFails int
	lastFailTime     time.Time
	successCount     int64
	failCount        int64
}

// BribeServiceManagerConfig 管理器配置。
type BribeServiceManagerConfig struct {
	MaxConsecutiveFails int           // 连续失败多少次标记为不健康（默认 3）
	CooldownDuration   time.Duration // 不健康后多久尝试恢复探测（默认 30s）
	SendTimeout        time.Duration // 单次发送超时（默认 5s）
}

// DefaultBribeManagerConfig 返回默认配置。
func DefaultBribeManagerConfig() BribeServiceManagerConfig {
	return BribeServiceManagerConfig{
		MaxConsecutiveFails: 3,
		CooldownDuration:   30 * time.Second,
		SendTimeout:        5 * time.Second,
	}
}

// BribeServiceManager 贿赂服务健康管理器。
type BribeServiceManager struct {
	mu        sync.RWMutex
	entries   []*BribeServiceEntry
	config    BribeServiceManagerConfig
	onAllDown func() // 所有服务不可用时的回调
}

// NewBribeServiceManager 创建贿赂服务管理器。
func NewBribeServiceManager(services []dexwallet.BribeService, config BribeServiceManagerConfig, onAllDown func()) *BribeServiceManager {
	entries := make([]*BribeServiceEntry, len(services))
	for i, svc := range services {
		entries[i] = &BribeServiceEntry{
			service: svc,
			healthy: true, // 初始假定所有服务健康
		}
	}
	return &BribeServiceManager{
		entries:   entries,
		config:    config,
		onAllDown: onAllDown,
	}
}

// sendResult 并行发送的结果。
type sendResult struct {
	serviceName string
	txHash      string
	err         error
	entryIdx    int
}

// Send 并行广播交易到所有可用的贿赂服务，返回第一个成功的结果。
//
// 流程：
//  1. 筛选可用服务（健康的 + 过了冷却期的不健康服务）
//  2. 并发发送到所有可用服务
//  3. 取第一个成功结果立即返回
//  4. 后台收集剩余结果，更新各服务的健康状态
func (m *BribeServiceManager) Send(ctx context.Context, txData []byte, fee *big.Int) (string, error) {
	available := m.getAvailableEntries()

	if len(available) == 0 {
		slog.Error("所有贿赂服务不可用",
			"total", len(m.entries),
		)
		if m.onAllDown != nil {
			m.onAllDown()
		}
		return "", fmt.Errorf("all bribe services unavailable")
	}

	slog.Debug("并行广播贿赂服务",
		"available", len(available),
		"total", len(m.entries),
	)

	// 并发发送
	ch := make(chan sendResult, len(available))
	sendCtx, sendCancel := context.WithTimeout(ctx, m.config.SendTimeout)
	defer sendCancel()

	for _, ae := range available {
		go func(entry *BribeServiceEntry, idx int) {
			hash, err := entry.service.Send(sendCtx, txData, fee)
			ch <- sendResult{
				serviceName: entry.service.Name(),
				txHash:      hash,
				err:         err,
				entryIdx:    idx,
			}
		}(ae.entry, ae.idx)
	}

	// 收集结果：取第一个成功的
	var firstSuccess *sendResult
	var allErrors []string
	remaining := len(available)

	for remaining > 0 {
		result := <-ch
		remaining--

		if result.err != nil {
			m.recordFailure(result.entryIdx)
			allErrors = append(allErrors, fmt.Sprintf("%s: %v", result.serviceName, result.err))
			slog.Debug("贿赂服务发送失败",
				"service", result.serviceName,
				"error", result.err,
			)
		} else {
			m.recordSuccess(result.entryIdx)
			if firstSuccess == nil {
				firstSuccess = &result
				slog.Info("贿赂服务发送成功（首个）",
					"service", result.serviceName,
					"tx_hash", result.txHash,
				)
				// 不 break，继续收集剩余结果以更新健康状态
				// 但已有成功结果，后续失败不影响返回值
			}
		}
	}

	if firstSuccess != nil {
		return firstSuccess.txHash, nil
	}

	// 所有服务都失败了
	slog.Error("所有贿赂服务发送失败",
		"attempted", len(available),
		"errors", allErrors,
	)

	// 检查是否全部不健康，触发告警
	if m.allUnhealthy() && m.onAllDown != nil {
		m.onAllDown()
	}

	return "", fmt.Errorf("all %d bribe services failed: %s", len(available), allErrors[0])
}

// availableEntry 用于 getAvailableEntries 的返回值。
type availableEntry struct {
	entry *BribeServiceEntry
	idx   int
}

// getAvailableEntries 获取可用的服务列表。
// 包括：健康的服务 + 过了冷却期的不健康服务（用于探测恢复）。
func (m *BribeServiceManager) getAvailableEntries() []availableEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []availableEntry
	now := time.Now()

	for i, entry := range m.entries {
		if entry.healthy {
			result = append(result, availableEntry{entry: entry, idx: i})
		} else if now.Sub(entry.lastFailTime) >= m.config.CooldownDuration {
			// 过了冷却期，纳入广播进行探测
			slog.Debug("贿赂服务冷却期结束，尝试恢复探测",
				"service", entry.service.Name(),
				"cooldown", m.config.CooldownDuration,
			)
			result = append(result, availableEntry{entry: entry, idx: i})
		}
	}

	return result
}

// recordSuccess 记录发送成功，重置健康状态。
func (m *BribeServiceManager) recordSuccess(idx int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry := m.entries[idx]
	wasUnhealthy := !entry.healthy

	entry.healthy = true
	entry.consecutiveFails = 0
	entry.successCount++

	if wasUnhealthy {
		slog.Info("贿赂服务恢复健康",
			"service", entry.service.Name(),
		)
	}
}

// recordFailure 记录发送失败，可能标记为不健康。
func (m *BribeServiceManager) recordFailure(idx int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry := m.entries[idx]
	entry.consecutiveFails++
	entry.failCount++
	entry.lastFailTime = time.Now()

	if entry.healthy && entry.consecutiveFails >= m.config.MaxConsecutiveFails {
		entry.healthy = false
		slog.Warn("贿赂服务标记为不健康",
			"service", entry.service.Name(),
			"consecutive_fails", entry.consecutiveFails,
			"threshold", m.config.MaxConsecutiveFails,
		)
	}
}

// allUnhealthy 检查是否所有服务都不健康。
func (m *BribeServiceManager) allUnhealthy() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, entry := range m.entries {
		if entry.healthy {
			return false
		}
	}
	return true
}

// ServiceStats 单个服务的统计信息。
type ServiceStats struct {
	Name             string
	Healthy          bool
	ConsecutiveFails int
	SuccessCount     int64
	FailCount        int64
}

// Stats 返回所有服务的统计信息（用于监控）。
func (m *BribeServiceManager) Stats() []ServiceStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make([]ServiceStats, len(m.entries))
	for i, entry := range m.entries {
		stats[i] = ServiceStats{
			Name:             entry.service.Name(),
			Healthy:          entry.healthy,
			ConsecutiveFails: entry.consecutiveFails,
			SuccessCount:     entry.successCount,
			FailCount:        entry.failCount,
		}
	}
	return stats
}

// HealthyCount 返回健康服务数量。
func (m *BribeServiceManager) HealthyCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, entry := range m.entries {
		if entry.healthy {
			count++
		}
	}
	return count
}

// TotalCount 返回总服务数量。
func (m *BribeServiceManager) TotalCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

// ----- 接口合规性编译检查 -----

var (
	_ dexwallet.BribeService = (*NextBlockService)(nil)
	_ dexwallet.BribeService = (*TemporalService)(nil)
	_ dexwallet.BribeService = (*ZeroSlotService)(nil)
	_ dexwallet.BribeService = (*BlockRazorService)(nil)
	_ dexwallet.BribeService = (*BlockRushService)(nil)
	_ dexwallet.BribeService = (*AntiMEVRPC)(nil)
)

// min 返回两个整数中的较小值。
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
