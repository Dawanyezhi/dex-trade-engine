// Package dexwallet 定义跨链通用的接口和数据模型。
// [dexwallet 通用层] — 对标生产中的 irwallet 框架。
//
// 这是整个项目的灵魂：从 solwallet 和 evmwallet 两个具体项目中，
// 识别出通用逻辑并提取到此层。所有链特定实现只需实现此层定义的接口。
package dexwallet

import (
	"math/big"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
)

// ----- DEX 协议类型 -----

// ProtocolType DEX 协议类型。
type ProtocolType string

const (
	ProtocolAMM          ProtocolType = "amm"           // 恒定乘积 (x*y=k)
	ProtocolCLMM         ProtocolType = "clmm"          // 集中流动性
	ProtocolBondingCurve ProtocolType = "bonding_curve"  // 联合曲线（内盘）
	ProtocolStableSwap   ProtocolType = "stable_swap"    // 稳定币低滑点
	ProtocolDLMM         ProtocolType = "dlmm"           // 离散流动性
	ProtocolAggregator   ProtocolType = "aggregator"     // 聚合器
)

// DexID DEX 唯一标识。
type DexID string

// Solana DEX IDs
const (
	DexRaydiumAMM       DexID = "raydium_amm"
	DexRaydiumCPMM      DexID = "raydium_cpmm"
	DexRaydiumCLMM      DexID = "raydium_clmm"
	DexPumpFun          DexID = "pump_fun"
	DexPumpAMM          DexID = "pump_amm"
	DexMoonshot         DexID = "moonshot"
	DexMeteoraAMM       DexID = "meteora_amm"
	DexMeteoraDLMM      DexID = "meteora_dlmm"
	DexJupiter          DexID = "jupiter"
	DexAlphAggregator   DexID = "alph_aggregator"
)

// EVM DEX IDs
const (
	DexUniswapV2      DexID = "uniswap_v2"
	DexUniswapV3      DexID = "uniswap_v3"
	DexUniswapV4      DexID = "uniswap_v4"
	DexPancakeV2      DexID = "pancake_v2"
	DexPancakeV3      DexID = "pancake_v3"
	DexPancakeStable  DexID = "pancake_stable"
	DexCurve          DexID = "curve"
	DexOneInch        DexID = "1inch"
	DexParaSwap       DexID = "paraswap"
	DexOdos           DexID = "odos"
)

// DexPriority DEX 优先级（数值越小优先级越高）。
type DexPriority int

const (
	PriorityHigh   DexPriority = 1 // 内盘（Bonding Curve）
	PriorityMedium DexPriority = 2 // AMM/CLMM
	PriorityLow    DexPriority = 3 // 聚合器
)

// ----- Token 模型 -----

// Token 代币信息。
type Token struct {
	Address  string          `json:"address"`
	Symbol   string          `json:"symbol"`
	Decimals uint8           `json:"decimals"`
	ChainID  coinset.ChainID `json:"chain_id"`
}

// ----- Pool 模型 -----

// PoolState 池子状态。
type PoolState string

const (
	PoolStateActive   PoolState = "active"
	PoolStateInactive PoolState = "inactive"
	PoolStateNeedUpdate PoolState = "need_update"
)

// Pool 流动性池数据模型（通用字段，Solana 和 EVM 共享）。
type Pool struct {
	Address      string          `json:"address"`       // 池子合约地址
	DexID        DexID           `json:"dex_id"`
	ChainID      coinset.ChainID `json:"chain_id"`
	ProtocolType ProtocolType    `json:"protocol_type"`

	BaseMint     string `json:"base_mint"`     // 基础代币地址
	QuoteMint    string `json:"quote_mint"`    // 计价代币地址
	BaseSymbol   string `json:"base_symbol"`
	QuoteSymbol  string `json:"quote_symbol"`
	BaseDecimal  uint8  `json:"base_decimal"`
	QuoteDecimal uint8  `json:"quote_decimal"`

	Liquidity *big.Int `json:"liquidity"` // 流动性（最小单位）
	FeeRate   uint64   `json:"fee_rate"`  // 手续费率（基点，如 30 = 0.3%）

	State     PoolState `json:"state"`
	UpdatedAt time.Time `json:"updated_at"`

	// 链特定扩展字段
	Extra map[string]interface{} `json:"extra,omitempty"`
}

// ----- Swap 请求/响应模型 -----

// SwapDirection 交易方向（语义：相对于交易对的 base 代币）。
// P0-4: Direction 与 FromToken/ToToken 的关系——
//   - Buy:  FromToken 是 quote（如 USDT），ToToken 是 base（如 MEME），用 quote 买 base
//   - Sell: FromToken 是 base（如 MEME），ToToken 是 quote（如 USDT），卖 base 得 quote
//
// FromToken 始终是用户实际支付的代币，ToToken 是用户希望获得的代币。
// Direction 用于区分 DEX 内部的定价逻辑（内盘的买卖手续费可能不同）。
type SwapDirection string

const (
	SwapDirectionBuy  SwapDirection = "buy"  // FromToken=quote → ToToken=base
	SwapDirectionSell SwapDirection = "sell" // FromToken=base → ToToken=quote
)

// SwapRequest Swap 交易请求（通用字段）。
//
// 核心语义：用户支付 Amount 数量的 FromToken，期望获得 ToToken。
// Direction 是辅助信息，用于 DEX 内部区分买卖（影响手续费率等）。
type SwapRequest struct {
	ChainID   coinset.ChainID `json:"chain_id"`
	DexID     DexID           `json:"dex_id,omitempty"` // 为空则由聚合器选择
	Direction SwapDirection   `json:"direction"`         // 买/卖方向（影响 DEX 内部定价）

	FromToken Token    `json:"from_token"` // 用户支付的代币
	ToToken   Token    `json:"to_token"`   // 用户获得的代币
	Amount    *big.Int `json:"amount"`     // 输入金额（FromToken 的最小单位）

	SlippageBps uint64 `json:"slippage_bps"` // 滑点容忍（基点）
	Sender      string `json:"sender"`       // 发送方地址
	Recipient   string `json:"recipient"`    // 接收方地址（可选，默认同 sender）

	// 链特定字段
	Extra map[string]interface{} `json:"extra,omitempty"`
}

// SwapResult Swap 交易结果。
type SwapResult struct {
	DexID     DexID           `json:"dex_id"`
	ChainID   coinset.ChainID `json:"chain_id"`
	Direction SwapDirection   `json:"direction"`

	InputAmount  *big.Int `json:"input_amount"`
	OutputAmount *big.Int `json:"output_amount"`
	MinOutput    *big.Int `json:"min_output"`   // 考虑滑点后的最小输出
	SlippageBps  uint64   `json:"slippage_bps"` // 实际滑点

	PriorityFee *big.Int `json:"priority_fee,omitempty"` // 优先费
	GasCost     *big.Int `json:"gas_cost,omitempty"`     // Gas 成本

	// 交易数据（链特定）
	TxData []byte `json:"tx_data"`           // 序列化后的交易
	TxHash string `json:"tx_hash,omitempty"` // 签名后的交易哈希

	// 链特定字段
	Extra map[string]interface{} `json:"extra,omitempty"`
}

// ----- Quote 报价模型 -----

// Quote 报价结果。
type Quote struct {
	DexID        DexID        `json:"dex_id"`
	ProtocolType ProtocolType `json:"protocol_type"`
	Priority     DexPriority  `json:"priority"`

	InputAmount  *big.Int `json:"input_amount"`
	OutputAmount *big.Int `json:"output_amount"`
	PriceImpact  uint64   `json:"price_impact_bps"` // 价格影响（基点）

	Pool *Pool `json:"pool,omitempty"`

	// 路由信息（可能是多跳）
	Route []RouteHop `json:"route,omitempty"`

	EstimatedGas *big.Int `json:"estimated_gas,omitempty"`
}

// RouteHop 路由中的一跳。
type RouteHop struct {
	DexID     DexID  `json:"dex_id"`
	Pool      string `json:"pool"`
	TokenIn   string `json:"token_in"`
	TokenOut  string `json:"token_out"`
	AmountIn  *big.Int `json:"amount_in"`
	AmountOut *big.Int `json:"amount_out"`
}

// ----- 事件模型 -----

// EventType 链上事件类型。
type EventType string

const (
	EventSwap      EventType = "swap"
	EventTransfer  EventType = "transfer"
	EventMint      EventType = "mint"
	EventBurn      EventType = "burn"
	EventLiquidity EventType = "liquidity"
)

// ChainEvent 解析后的链上事件。
type ChainEvent struct {
	Type    EventType       `json:"type"`
	ChainID coinset.ChainID `json:"chain_id"`
	TxHash  string          `json:"tx_hash"`
	Block   uint64          `json:"block"`

	// Swap 事件字段
	DexID     DexID    `json:"dex_id,omitempty"`
	Pool      string   `json:"pool,omitempty"`
	TokenIn   string   `json:"token_in,omitempty"`
	TokenOut  string   `json:"token_out,omitempty"`
	AmountIn  *big.Int `json:"amount_in,omitempty"`
	AmountOut *big.Int `json:"amount_out,omitempty"`

	Timestamp time.Time `json:"timestamp"`
}

// ----- 交易状态 -----

// TxStatus 交易状态。
type TxStatus string

const (
	TxStatusPending   TxStatus = "pending"
	TxStatusConfirmed TxStatus = "confirmed"
	TxStatusFailed    TxStatus = "failed"
	TxStatusTimeout   TxStatus = "timeout"
)

// TxRecord 交易记录（对标生产中 swaptx 表）。
type TxRecord struct {
	TxHash    string          `json:"tx_hash"`
	ChainID   coinset.ChainID `json:"chain_id"`
	DexID     DexID           `json:"dex_id"`
	Direction SwapDirection   `json:"direction"`

	SellSymbol string   `json:"sell_symbol"`
	BuySymbol  string   `json:"buy_symbol"`
	SellAmount *big.Int `json:"sell_amount"`
	BuyAmount  *big.Int `json:"buy_amount"`

	SlippageBps uint64   `json:"slippage_bps"`
	PriorityFee *big.Int `json:"priority_fee"`

	Status    TxStatus  `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
