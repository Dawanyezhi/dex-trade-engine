package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// SolanaRPCClient 是 Solana RPC 客户端的 mock 实现。
// 模拟 getAccountInfo、getRecentBlockhash、sendTransaction 等 RPC 响应。
// [链特定层] -- 生产中使用真实的 HTTP 客户端连接 Solana JSON-RPC 节点。
type SolanaRPCClient struct {
	mu          sync.RWMutex
	endpoint    string
	healthy     atomic.Bool
	blockHeight atomic.Uint64
	balances    map[string]*big.Int // 地址 -> 余额(lamports)

	// 模拟故障控制
	forceError atomic.Bool
}

// NewSolanaRPCClient 创建 Solana RPC 客户端 mock。
func NewSolanaRPCClient(endpoint string) *SolanaRPCClient {
	c := &SolanaRPCClient{
		endpoint: endpoint,
		balances: map[string]*big.Int{
			// 预设一些 mock 余额（单位: lamports, 1 SOL = 10^9 lamports）
			"SoLWallet1111111111111111111111111111111111": big.NewInt(5_000_000_000),  // 5 SOL
			"SoLWallet2222222222222222222222222222222222": big.NewInt(10_000_000_000), // 10 SOL
		},
	}
	c.healthy.Store(true)
	c.blockHeight.Store(280_000_000) // 模拟初始 slot 高度
	return c
}

// SendTransaction 模拟发送 Solana 交易。
// 生产中调用 sendTransaction JSON-RPC 方法，返回 base58 编码的交易签名。
func (c *SolanaRPCClient) SendTransaction(ctx context.Context, txData []byte) (string, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return "", fmt.Errorf("solana SendTransaction [%s]: %w", c.endpoint, err)
	}

	// 生成模拟交易签名（64 字节随机数的十六进制表示）
	sig := make([]byte, 32)
	if _, err := rand.Read(sig); err != nil {
		return "", fmt.Errorf("solana generate tx signature: %w", err)
	}
	txHash := hex.EncodeToString(sig)

	slog.Debug("solana 交易已发送",
		"endpoint", c.endpoint,
		"tx_hash", txHash,
		"tx_size", len(txData),
	)

	return txHash, nil
}

// GetBalance 模拟获取 Solana 地址余额。
// 生产中调用 getBalance JSON-RPC 方法，返回 lamports。
func (c *SolanaRPCClient) GetBalance(ctx context.Context, address string) (*big.Int, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return nil, fmt.Errorf("solana GetBalance [%s]: %w", c.endpoint, err)
	}

	c.mu.RLock()
	balance, ok := c.balances[address]
	c.mu.RUnlock()

	if !ok {
		// 未知地址返回 0 余额
		return big.NewInt(0), nil
	}

	return new(big.Int).Set(balance), nil
}

// GetBlockHeight 模拟获取 Solana 当前 slot 高度。
// 生产中调用 getBlockHeight 或 getSlot JSON-RPC 方法。
func (c *SolanaRPCClient) GetBlockHeight(ctx context.Context) (uint64, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return 0, fmt.Errorf("solana GetBlockHeight [%s]: %w", c.endpoint, err)
	}

	// 模拟 slot 增长
	height := c.blockHeight.Add(1)
	return height, nil
}

// IsHealthy 检查 Solana 节点健康状态。
// 生产中通过 getHealth 或 getVersion JSON-RPC 方法判断。
func (c *SolanaRPCClient) IsHealthy(_ context.Context) bool {
	return c.healthy.Load() && !c.forceError.Load()
}

// ChainID 返回链标识。
func (c *SolanaRPCClient) ChainID() coinset.ChainID {
	return coinset.ChainSolana
}

// --- Mock 控制方法（仅用于测试和演示） ---

// SetHealthy 设置节点健康状态。
func (c *SolanaRPCClient) SetHealthy(healthy bool) {
	c.healthy.Store(healthy)
}

// SetForceError 强制所有调用返回错误。
func (c *SolanaRPCClient) SetForceError(force bool) {
	c.forceError.Store(force)
}

// SetBalance 设置指定地址的余额。
func (c *SolanaRPCClient) SetBalance(address string, balance *big.Int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.balances[address] = new(big.Int).Set(balance)
}

// Endpoint 返回节点端点。
func (c *SolanaRPCClient) Endpoint() string {
	return c.endpoint
}

// checkAvailable 检查节点是否可用。
func (c *SolanaRPCClient) checkAvailable(ctx context.Context) error {
	if ctx.Err() != nil {
		return fmt.Errorf("context cancelled: %w", ctx.Err())
	}
	if c.forceError.Load() {
		return fmt.Errorf("node %s is forced to fail", c.endpoint)
	}
	if !c.healthy.Load() {
		return fmt.Errorf("node %s is unhealthy", c.endpoint)
	}
	return nil
}

// 编译期检查
var _ dexwallet.RPCClient = (*SolanaRPCClient)(nil)
