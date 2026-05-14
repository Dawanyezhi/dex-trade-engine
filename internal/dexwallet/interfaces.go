package dexwallet

import (
	"context"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
)

// SwapBuilder 构建 Swap 交易的核心接口。
// Solana 和 EVM 各自实现：Solana 组装指令+ALT+ComputeBudget，EVM 编码 ABI+Gas+Approve。
type SwapBuilder interface {
	// Build 构建 Swap 交易。
	Build(ctx context.Context, req SwapRequest) (*SwapResult, error)

	// DexID 返回支持的 DEX 标识。
	DexID() DexID

	// ChainID 返回支持的链标识。
	ChainID() coinset.ChainID

	// ProtocolType 返回协议类型。
	ProtocolType() ProtocolType
}

// DexProtocol DEX 协议接口，提供报价和价格计算。
type DexProtocol interface {
	// Quote 获取报价（输入金额 → 预期输出金额）。
	Quote(ctx context.Context, pool *Pool, amountIn *big.Int, direction SwapDirection) (*Quote, error)

	// GetPrice 获取当前价格。
	GetPrice(ctx context.Context, pool *Pool) (*big.Int, error)

	// DexID 返回 DEX 标识。
	DexID() DexID

	// ProtocolType 返回协议类型。
	ProtocolType() ProtocolType
}

// PoolManager 池子管理接口。
// 通用的 LRU 缓存和状态管理在 dexwallet 层实现，
// 链特定的池子数据解析（Borsh / ABI）由 solana/evm 层实现。
type PoolManager interface {
	// GetPool 获取指定池子。
	GetPool(ctx context.Context, address string) (*Pool, error)

	// GetBestPool 获取某交易对的最优池子（按流动性、费率、滑点综合排序）。
	GetBestPool(ctx context.Context, baseMint, quoteMint string) (*Pool, error)

	// UpdatePool 更新池子数据。
	UpdatePool(ctx context.Context, pool *Pool) error

	// RefreshCache 刷新缓存中过期的池子。
	RefreshCache(ctx context.Context) error
}

// PoolParser 池子数据解析接口（链特定）。
type PoolParser interface {
	// Parse 从链上原始数据解析池子信息。
	Parse(ctx context.Context, rawData []byte) (*Pool, error)

	// SupportedDex 返回支持的 DEX 列表。
	SupportedDex() []DexID
}

// Aggregator 多 DEX 聚合接口。
// 核心聚合逻辑（并发 Quote → 比较 → 选最优 → 降级兜底）在 dexwallet 层实现，
// 各链只需注册自己的 DEX 列表和优先级配置。
type Aggregator interface {
	// FindBestQuote 并发获取所有 DEX 报价，返回最优结果。
	FindBestQuote(ctx context.Context, req SwapRequest) (*Quote, error)

	// BuildSwap 使用最优报价构建交易。
	BuildSwap(ctx context.Context, req SwapRequest) (*SwapResult, error)
}

// EventParser 链上事件解析接口。
// Solana 按 ProgramID 解析指令，EVM 按 topic 解析事件日志，
// 解析方式完全不同，但输出的事件模型是统一的 ChainEvent。
type EventParser interface {
	// Parse 解析链上原始交易数据为通用事件。
	Parse(ctx context.Context, rawTx []byte) ([]ChainEvent, error)

	// Register 注册特定事件的解析处理器。
	Register(identifier string, handler EventHandler)
}

// EventHandler 事件处理函数。
type EventHandler func(ctx context.Context, rawData []byte) (*ChainEvent, error)

// BribeService 贿赂/优先费服务接口。
// Solana 有 5 个贿赂服务商（NextBlock/Temporal/ZeroSlot 等），
// EVM 使用 Anti-MEV RPC + RBF。
type BribeService interface {
	// Send 通过贿赂服务发送交易。
	Send(ctx context.Context, txData []byte, fee *big.Int) (string, error)

	// GetRecommendedFee 获取推荐的优先费/贿赂费。
	GetRecommendedFee(ctx context.Context) (*big.Int, error)

	// Name 服务商名称。
	Name() string
}

// TxSender 交易发送与确认接口。
// 通用的多通道发送、超时重试在 dexwallet 层实现。
type TxSender interface {
	// Send 发送交易并返回交易哈希。
	Send(ctx context.Context, txData []byte) (string, error)

	// Confirm 等待交易确认。
	Confirm(ctx context.Context, txHash string) (TxStatus, error)

	// Retry 重试发送交易（可能使用更高的 Gas/优先费）。
	Retry(ctx context.Context, txHash string) (string, error)
}

// RPCClient 链 RPC 客户端接口。
// 通用方法在此定义，链特定方法由 solana/evm 各自扩展。
type RPCClient interface {
	// SendTransaction 发送已签名交易。
	SendTransaction(ctx context.Context, txData []byte) (string, error)

	// GetBalance 获取原生代币余额。
	GetBalance(ctx context.Context, address string) (*big.Int, error)

	// GetBlockHeight 获取当前区块高度。
	GetBlockHeight(ctx context.Context) (uint64, error)

	// IsHealthy 检查节点健康状态。
	IsHealthy(ctx context.Context) bool

	// ChainID 返回链标识。
	ChainID() coinset.ChainID
}

// Syncer 区块同步接口。
type Syncer interface {
	// Run 启动同步主循环。
	Run(ctx context.Context) error

	// GetCurrentHeight 获取当前同步高度。
	GetCurrentHeight() uint64
}

// Repository 数据存储接口。
type Repository interface {
	// SaveTxRecord 保存交易记录。
	SaveTxRecord(ctx context.Context, record *TxRecord) error

	// GetTxRecord 获取交易记录。
	GetTxRecord(ctx context.Context, txHash string) (*TxRecord, error)

	// UpdateTxStatus 更新交易状态。
	UpdateTxStatus(ctx context.Context, txHash string, status TxStatus) error

	// SavePool 保存池子数据。
	SavePool(ctx context.Context, pool *Pool) error

	// GetPool 获取池子数据。
	GetPool(ctx context.Context, address string) (*Pool, error)
}
