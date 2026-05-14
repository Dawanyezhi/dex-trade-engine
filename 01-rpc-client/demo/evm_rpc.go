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

// EVMRPCClient 是 EVM RPC 客户端的 mock 实现。
// 模拟 eth_call、eth_estimateGas、eth_sendRawTransaction 等 RPC 响应。
// [链特定层] -- 生产中使用 go-ethereum 的 ethclient 或自定义 HTTP 客户端。
type EVMRPCClient struct {
	mu          sync.RWMutex
	endpoint    string
	chainID     coinset.ChainID
	healthy     atomic.Bool
	blockHeight atomic.Uint64
	balances    map[string]*big.Int // 地址 -> 余额(wei)

	// 模拟故障控制
	forceError atomic.Bool
}

// NewEVMRPCClient 创建 EVM RPC 客户端 mock。
func NewEVMRPCClient(endpoint string, chainID coinset.ChainID) *EVMRPCClient {
	c := &EVMRPCClient{
		endpoint: endpoint,
		chainID:  chainID,
		balances: map[string]*big.Int{
			// 预设一些 mock 余额（单位: wei, 1 ETH/BNB = 10^18 wei）
			"0x1111111111111111111111111111111111111111": new(big.Int).Mul(big.NewInt(5), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)),   // 5 ETH/BNB
			"0x2222222222222222222222222222222222222222": new(big.Int).Mul(big.NewInt(10), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)),  // 10 ETH/BNB
		},
	}
	c.healthy.Store(true)

	// 根据链类型设置不同的初始区块高度
	switch chainID {
	case coinset.ChainEthereum:
		c.blockHeight.Store(20_000_000)
	case coinset.ChainBSC:
		c.blockHeight.Store(40_000_000)
	case coinset.ChainBase:
		c.blockHeight.Store(15_000_000)
	default:
		c.blockHeight.Store(10_000_000)
	}

	return c
}

// SendTransaction 模拟发送 EVM 交易。
// 生产中调用 eth_sendRawTransaction JSON-RPC 方法，返回 0x 前缀的交易哈希。
func (c *EVMRPCClient) SendTransaction(ctx context.Context, txData []byte) (string, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return "", fmt.Errorf("evm SendTransaction [%s][%s]: %w", c.chainID, c.endpoint, err)
	}

	// 生成模拟交易哈希（32 字节随机数的十六进制表示，带 0x 前缀）
	hash := make([]byte, 32)
	if _, err := rand.Read(hash); err != nil {
		return "", fmt.Errorf("evm generate tx hash: %w", err)
	}
	txHash := "0x" + hex.EncodeToString(hash)

	slog.Debug("evm 交易已发送",
		"chain", c.chainID,
		"endpoint", c.endpoint,
		"tx_hash", txHash,
		"tx_size", len(txData),
	)

	return txHash, nil
}

// GetBalance 模拟获取 EVM 地址余额。
// 生产中调用 eth_getBalance JSON-RPC 方法，返回 wei。
func (c *EVMRPCClient) GetBalance(ctx context.Context, address string) (*big.Int, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return nil, fmt.Errorf("evm GetBalance [%s][%s]: %w", c.chainID, c.endpoint, err)
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

// GetBlockHeight 模拟获取 EVM 当前区块高度。
// 生产中调用 eth_blockNumber JSON-RPC 方法。
func (c *EVMRPCClient) GetBlockHeight(ctx context.Context) (uint64, error) {
	if err := c.checkAvailable(ctx); err != nil {
		return 0, fmt.Errorf("evm GetBlockHeight [%s][%s]: %w", c.chainID, c.endpoint, err)
	}

	// 模拟区块增长
	height := c.blockHeight.Add(1)
	return height, nil
}

// IsHealthy 检查 EVM 节点健康状态。
// 生产中通过 net_version 或 eth_chainId JSON-RPC 方法判断。
func (c *EVMRPCClient) IsHealthy(_ context.Context) bool {
	return c.healthy.Load() && !c.forceError.Load()
}

// ChainID 返回链标识。
func (c *EVMRPCClient) ChainID() coinset.ChainID {
	return c.chainID
}

// --- Mock 控制方法（仅用于测试和演示） ---

// SetHealthy 设置节点健康状态。
func (c *EVMRPCClient) SetHealthy(healthy bool) {
	c.healthy.Store(healthy)
}

// SetForceError 强制所有调用返回错误。
func (c *EVMRPCClient) SetForceError(force bool) {
	c.forceError.Store(force)
}

// SetBalance 设置指定地址的余额。
func (c *EVMRPCClient) SetBalance(address string, balance *big.Int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.balances[address] = new(big.Int).Set(balance)
}

// Endpoint 返回节点端点。
func (c *EVMRPCClient) Endpoint() string {
	return c.endpoint
}

// checkAvailable 检查节点是否可用。
func (c *EVMRPCClient) checkAvailable(ctx context.Context) error {
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
var _ dexwallet.RPCClient = (*EVMRPCClient)(nil)
