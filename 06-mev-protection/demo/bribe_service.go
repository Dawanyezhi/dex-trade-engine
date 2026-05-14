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
