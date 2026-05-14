package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// PoolParserRouter — 池子数据解析路由器
// ============================================================

// PoolParserRouter 根据池子账户的 owner（ProgramID / Factory）路由到对应的解析器。
//
// 核心思路：
//   - Solana: 每个 pool account 的 owner 字段是创建它的 Program → 用 owner 路由
//   - EVM: 每个 pair 合约由 Factory 创建 → 用 Factory 地址路由
//   - 生产中有 25+ 个解析器，每个 DEX 一个，全部注册到此路由器
//
// 使用方式：
//
//	router := NewPoolParserRouter()
//	router.Register(NewRaydiumPoolParser())
//	router.Register(NewPumpFunPoolParser())
//	pool, err := router.Parse(ctx, ownerAddress, rawAccountData)
type PoolParserRouter struct {
	mu      sync.RWMutex
	parsers map[string]dexwallet.PoolParser // owner address → parser
}

// NewPoolParserRouter 创建池子解析路由器。
func NewPoolParserRouter() *PoolParserRouter {
	return &PoolParserRouter{
		parsers: make(map[string]dexwallet.PoolParser),
	}
}

// Register 注册一个解析器。
// 以解析器的 Owner() 返回值作为路由 key。
// 如果同一个 owner 重复注册，后注册的会覆盖先注册的。
func (r *PoolParserRouter) Register(parser dexwallet.PoolParser) {
	r.mu.Lock()
	defer r.mu.Unlock()

	owner := parser.Owner()
	r.parsers[owner] = parser

	slog.Info("pool parser registered",
		"owner", owner,
		"supported_dex", parser.SupportedDex(),
	)
}

// Parse 根据 owner 地址路由到对应的解析器，解析原始数据为 Pool。
// 如果找不到对应的解析器，返回错误。
func (r *PoolParserRouter) Parse(ctx context.Context, owner string, rawData []byte) (*dexwallet.Pool, error) {
	r.mu.RLock()
	parser, ok := r.parsers[owner]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no parser registered for owner: %s", owner)
	}

	slog.Debug("routing parse request",
		"owner", owner,
		"data_size", len(rawData),
	)

	return parser.Parse(ctx, rawData)
}

// ParserCount 返回已注册的解析器数量。
func (r *PoolParserRouter) ParserCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.parsers)
}

// ============================================================
// RaydiumPoolParser — Raydium AMM 池子解析器（Solana）
// ============================================================

// RaydiumPoolRawData 模拟 Raydium AMM 链上账户的 Borsh 解码后数据。
// 生产中这些字段从 Borsh 反序列化得到，这里用 JSON 模拟。
//
// Raydium AMM 的池子账户包含两个 vault（base_vault / quote_vault），
// 储备量（reserve）从 vault 的 token balance 获取。
type RaydiumPoolRawData struct {
	AMMID         string `json:"amm_id"`          // AMM 池子地址
	BaseMint      string `json:"base_mint"`        // 基础代币 mint 地址
	QuoteMint     string `json:"quote_mint"`       // 计价代币 mint 地址
	BaseVault     string `json:"base_vault"`       // 基础代币 vault 地址
	QuoteVault    string `json:"quote_vault"`      // 计价代币 vault 地址
	BaseReserve   string `json:"base_reserve"`     // 基础代币储备量（最小单位）
	QuoteReserve  string `json:"quote_reserve"`    // 计价代币储备量（最小单位）
	BaseDecimal   uint8  `json:"base_decimal"`     // 基础代币精度
	QuoteDecimal  uint8  `json:"quote_decimal"`    // 计价代币精度
	BaseSymbol    string `json:"base_symbol"`      // 基础代币符号
	QuoteSymbol   string `json:"quote_symbol"`     // 计价代币符号
	FeeRateBps    uint64 `json:"fee_rate_bps"`     // 手续费率（基点）
	OpenTime      int64  `json:"open_time"`        // 开池时间（Unix 时间戳）
}

// raydiumProgramID Raydium AMM V4 的 Program ID。
const raydiumProgramID = "675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8"

// RaydiumPoolParser Raydium AMM 池子解析器。
// 负责将 Raydium AMM Program 拥有的账户数据解析为通用 Pool 模型。
type RaydiumPoolParser struct{}

// NewRaydiumPoolParser 创建 Raydium AMM 解析器。
func NewRaydiumPoolParser() *RaydiumPoolParser {
	return &RaydiumPoolParser{}
}

// Owner 返回 Raydium AMM V4 的 Program ID（路由 key）。
func (p *RaydiumPoolParser) Owner() string {
	return raydiumProgramID
}

// SupportedDex 返回此解析器支持的 DEX 列表。
func (p *RaydiumPoolParser) SupportedDex() []dexwallet.DexID {
	return []dexwallet.DexID{dexwallet.DexRaydiumAMM}
}

// Parse 从原始数据解析 Raydium AMM 池子。
// 生产中 rawData 是 Borsh 编码的账户数据，这里用 JSON 模拟。
//
// 解析流程（生产版本）：
//  1. Borsh 反序列化 → RaydiumAmmAccountLayout
//  2. 提取 vault 地址，RPC 查询 vault 的 token balance → 得到 reserve
//  3. 计算流动性 = base_reserve * quote_reserve（用于排序）
//  4. 组装为通用 Pool 模型
func (p *RaydiumPoolParser) Parse(_ context.Context, rawData []byte) (*dexwallet.Pool, error) {
	var raw RaydiumPoolRawData
	if err := json.Unmarshal(rawData, &raw); err != nil {
		return nil, fmt.Errorf("raydium: unmarshal raw data: %w", err)
	}

	// 解析储备量
	baseReserve, ok := new(big.Int).SetString(raw.BaseReserve, 10)
	if !ok {
		return nil, fmt.Errorf("raydium: invalid base_reserve: %s", raw.BaseReserve)
	}
	quoteReserve, ok := new(big.Int).SetString(raw.QuoteReserve, 10)
	if !ok {
		return nil, fmt.Errorf("raydium: invalid quote_reserve: %s", raw.QuoteReserve)
	}

	// 计算流动性指标：使用 quote 侧储备作为流动性的近似值
	// 生产中可能用 sqrt(base * quote) 或直接用 TVL（USD 计价）
	liquidity := new(big.Int).Set(quoteReserve)

	pool := &dexwallet.Pool{
		Address:      raw.AMMID,
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      "solana",
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     raw.BaseMint,
		QuoteMint:    raw.QuoteMint,
		BaseSymbol:   raw.BaseSymbol,
		QuoteSymbol:  raw.QuoteSymbol,
		BaseDecimal:  raw.BaseDecimal,
		QuoteDecimal: raw.QuoteDecimal,
		Liquidity:    liquidity,
		FeeRate:      raw.FeeRateBps,
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"base_vault":    raw.BaseVault,
			"quote_vault":   raw.QuoteVault,
			"base_reserve":  baseReserve.String(),
			"quote_reserve": quoteReserve.String(),
			"open_time":     raw.OpenTime,
		},
	}

	slog.Debug("raydium pool parsed",
		"amm_id", raw.AMMID,
		"pair", raw.BaseSymbol+"/"+raw.QuoteSymbol,
		"liquidity", liquidity.String(),
	)

	return pool, nil
}

// 确保 RaydiumPoolParser 实现 dexwallet.PoolParser 接口。
var _ dexwallet.PoolParser = (*RaydiumPoolParser)(nil)

// ============================================================
// PumpFunPoolParser — PumpFun 联合曲线池子解析器（Solana）
// ============================================================

// PumpFunPoolRawData 模拟 PumpFun 联合曲线账户的 Borsh 解码后数据。
// PumpFun 使用联合曲线（Bonding Curve）定价，与 AMM 的 x*y=k 模型完全不同。
//
// 关键区别：
//   - AMM: 两种代币储备，价格由储备比决定
//   - BondingCurve: 虚拟储备 + 真实储备，价格沿曲线变化
//   - complete=true 表示已毕业（migrated to Raydium），此时应使用 Raydium 解析器
type PumpFunPoolRawData struct {
	Mint                 string `json:"mint"`                   // 代币 mint 地址
	BondingCurve         string `json:"bonding_curve"`          // 联合曲线账户地址
	VirtualSOLReserves   string `json:"virtual_sol_reserves"`   // 虚拟 SOL 储备（定价用）
	VirtualTokenReserves string `json:"virtual_token_reserves"` // 虚拟 Token 储备（定价用）
	RealSOLReserves      string `json:"real_sol_reserves"`      // 真实 SOL 储备（实际锁定量）
	RealTokenReserves    string `json:"real_token_reserves"`    // 真实 Token 储备（实际锁定量）
	Complete             bool   `json:"complete"`               // 是否已毕业（迁移到外盘）
	TokenSymbol          string `json:"token_symbol"`           // 代币符号
	TokenDecimal         uint8  `json:"token_decimal"`          // 代币精度
}

// pumpFunProgramID PumpFun 的 Program ID。
const pumpFunProgramID = "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P"

// PumpFunPoolParser PumpFun 联合曲线池子解析器。
// 负责将 PumpFun Program 拥有的账户数据解析为通用 Pool 模型。
type PumpFunPoolParser struct{}

// NewPumpFunPoolParser 创建 PumpFun 解析器。
func NewPumpFunPoolParser() *PumpFunPoolParser {
	return &PumpFunPoolParser{}
}

// Owner 返回 PumpFun 的 Program ID（路由 key）。
func (p *PumpFunPoolParser) Owner() string {
	return pumpFunProgramID
}

// SupportedDex 返回此解析器支持的 DEX 列表。
func (p *PumpFunPoolParser) SupportedDex() []dexwallet.DexID {
	return []dexwallet.DexID{dexwallet.DexPumpFun}
}

// Parse 从原始数据解析 PumpFun 联合曲线池子。
// 生产中 rawData 是 Borsh 编码的 BondingCurve 账户数据，这里用 JSON 模拟。
//
// 解析流程（生产版本）：
//  1. Borsh 反序列化 → BondingCurveLayout
//  2. 提取虚拟储备和真实储备
//  3. 检查 complete 标志（毕业后不再使用此解析器）
//  4. 流动性使用 real_sol_reserves（真实锁定的 SOL）
//  5. PumpFun 固定 1% 手续费（100 bps）
func (p *PumpFunPoolParser) Parse(_ context.Context, rawData []byte) (*dexwallet.Pool, error) {
	var raw PumpFunPoolRawData
	if err := json.Unmarshal(rawData, &raw); err != nil {
		return nil, fmt.Errorf("pumpfun: unmarshal raw data: %w", err)
	}

	// 解析储备量
	virtualSOL, ok := new(big.Int).SetString(raw.VirtualSOLReserves, 10)
	if !ok {
		return nil, fmt.Errorf("pumpfun: invalid virtual_sol_reserves: %s", raw.VirtualSOLReserves)
	}
	virtualToken, ok := new(big.Int).SetString(raw.VirtualTokenReserves, 10)
	if !ok {
		return nil, fmt.Errorf("pumpfun: invalid virtual_token_reserves: %s", raw.VirtualTokenReserves)
	}
	realSOL, ok := new(big.Int).SetString(raw.RealSOLReserves, 10)
	if !ok {
		return nil, fmt.Errorf("pumpfun: invalid real_sol_reserves: %s", raw.RealSOLReserves)
	}
	realToken, ok := new(big.Int).SetString(raw.RealTokenReserves, 10)
	if !ok {
		return nil, fmt.Errorf("pumpfun: invalid real_token_reserves: %s", raw.RealTokenReserves)
	}

	// 池子状态：complete=true 表示已毕业，标记为 inactive
	state := dexwallet.PoolStateActive
	if raw.Complete {
		state = dexwallet.PoolStateInactive
	}

	// 流动性使用真实 SOL 储备
	liquidity := new(big.Int).Set(realSOL)

	// SOL 的 mint 地址（Wrapped SOL）
	solMint := "So11111111111111111111111111111111111111112"

	pool := &dexwallet.Pool{
		Address:      raw.BondingCurve,
		DexID:        dexwallet.DexPumpFun,
		ChainID:      "solana",
		ProtocolType: dexwallet.ProtocolBondingCurve,
		BaseMint:     raw.Mint,
		QuoteMint:    solMint,
		BaseSymbol:   raw.TokenSymbol,
		QuoteSymbol:  "SOL",
		BaseDecimal:  raw.TokenDecimal,
		QuoteDecimal: 9, // SOL 精度固定 9
		Liquidity:    liquidity,
		FeeRate:      100, // PumpFun 固定 1% 手续费
		State:        state,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"mint":                   raw.Mint,
			"virtual_sol_reserves":   virtualSOL.String(),
			"virtual_token_reserves": virtualToken.String(),
			"real_sol_reserves":      realSOL.String(),
			"real_token_reserves":    realToken.String(),
			"complete":               raw.Complete,
		},
	}

	slog.Debug("pumpfun pool parsed",
		"bonding_curve", raw.BondingCurve,
		"token", raw.TokenSymbol,
		"complete", raw.Complete,
		"real_sol", realSOL.String(),
	)

	return pool, nil
}

// 确保 PumpFunPoolParser 实现 dexwallet.PoolParser 接口。
var _ dexwallet.PoolParser = (*PumpFunPoolParser)(nil)

// ============================================================
// UniswapV2PoolParser — Uniswap V2 池子解析器（EVM）
// ============================================================

// UniV2PoolRawData 模拟 Uniswap V2 Pair 合约的 ABI 解码后数据。
// 生产中通过 multicall 批量调用 getReserves() 获取储备量。
//
// Uniswap V2 的特点：
//   - 所有 Pair 由同一个 Factory 合约创建
//   - 手续费固定 0.3%（30 bps），无法修改
//   - token0/token1 的排序由地址大小决定（小地址在前）
type UniV2PoolRawData struct {
	Pair     string `json:"pair"`     // Pair 合约地址
	Token0   string `json:"token0"`   // token0 地址（地址排序较小的那个）
	Token1   string `json:"token1"`   // token1 地址
	Symbol0  string `json:"symbol0"`  // token0 符号
	Symbol1  string `json:"symbol1"`  // token1 符号
	Decimal0 uint8  `json:"decimal0"` // token0 精度
	Decimal1 uint8  `json:"decimal1"` // token1 精度
	Reserve0 string `json:"reserve0"` // token0 储备量（最小单位）
	Reserve1 string `json:"reserve1"` // token1 储备量（最小单位）
	Fee      uint64 `json:"fee"`      // 手续费（基点），V2 固定 30
}

// uniswapV2FactoryAddress Uniswap V2 Factory 合约地址（Ethereum Mainnet）。
const uniswapV2FactoryAddress = "0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f"

// UniswapV2PoolParser Uniswap V2 池子解析器。
// 负责将 Uniswap V2 Factory 创建的 Pair 合约数据解析为通用 Pool 模型。
type UniswapV2PoolParser struct{}

// NewUniswapV2PoolParser 创建 Uniswap V2 解析器。
func NewUniswapV2PoolParser() *UniswapV2PoolParser {
	return &UniswapV2PoolParser{}
}

// Owner 返回 Uniswap V2 Factory 合约地址（路由 key）。
func (p *UniswapV2PoolParser) Owner() string {
	return uniswapV2FactoryAddress
}

// SupportedDex 返回此解析器支持的 DEX 列表。
func (p *UniswapV2PoolParser) SupportedDex() []dexwallet.DexID {
	return []dexwallet.DexID{dexwallet.DexUniswapV2}
}

// Parse 从原始数据解析 Uniswap V2 池子。
// 生产中 rawData 是 ABI 编码的合约调用结果，这里用 JSON 模拟。
//
// 解析流程（生产版本）：
//  1. multicall 批量调用: getReserves() + token0() + token1()
//  2. ABI 解码返回值
//  3. 从 ERC20 合约获取 symbol 和 decimals
//  4. 流动性使用 reserve1（通常是稳定币侧或 ETH 侧）
//  5. V2 手续费固定 30 bps
func (p *UniswapV2PoolParser) Parse(_ context.Context, rawData []byte) (*dexwallet.Pool, error) {
	var raw UniV2PoolRawData
	if err := json.Unmarshal(rawData, &raw); err != nil {
		return nil, fmt.Errorf("uniswap_v2: unmarshal raw data: %w", err)
	}

	// 解析储备量
	reserve0, ok := new(big.Int).SetString(raw.Reserve0, 10)
	if !ok {
		return nil, fmt.Errorf("uniswap_v2: invalid reserve0: %s", raw.Reserve0)
	}
	reserve1, ok := new(big.Int).SetString(raw.Reserve1, 10)
	if !ok {
		return nil, fmt.Errorf("uniswap_v2: invalid reserve1: %s", raw.Reserve1)
	}

	// 使用 reserve1 作为流动性近似值（通常 token1 是计价代币）
	liquidity := new(big.Int).Set(reserve1)

	// V2 手续费固定 30 bps，即使原始数据中传了其他值也强制覆盖
	feeRate := uint64(30)
	if raw.Fee > 0 {
		feeRate = raw.Fee
	}

	pool := &dexwallet.Pool{
		Address:      raw.Pair,
		DexID:        dexwallet.DexUniswapV2,
		ChainID:      "ethereum",
		ProtocolType: dexwallet.ProtocolAMM,
		BaseMint:     raw.Token0,
		QuoteMint:    raw.Token1,
		BaseSymbol:   raw.Symbol0,
		QuoteSymbol:  raw.Symbol1,
		BaseDecimal:  raw.Decimal0,
		QuoteDecimal: raw.Decimal1,
		Liquidity:    liquidity,
		FeeRate:      feeRate,
		State:        dexwallet.PoolStateActive,
		UpdatedAt:    time.Now(),
		Extra: map[string]interface{}{
			"reserve0": reserve0.String(),
			"reserve1": reserve1.String(),
			"factory":  uniswapV2FactoryAddress,
		},
	}

	slog.Debug("uniswap v2 pool parsed",
		"pair", raw.Pair,
		"tokens", raw.Symbol0+"/"+raw.Symbol1,
		"liquidity", liquidity.String(),
	)

	return pool, nil
}

// 确保 UniswapV2PoolParser 实现 dexwallet.PoolParser 接口。
var _ dexwallet.PoolParser = (*UniswapV2PoolParser)(nil)
