// Solana 事件解析器
// [链特定层] -- 按 ProgramID 匹配并解析 Solana 指令数据
//
// 包含 3 种解析器：
// - Raydium AMM Swap 解析器
// - PumpFun 交易解析器
// - SPL Token Transfer 解析器
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// Solana 常用 ProgramID（生产中是真实的 base58 编码公钥）
const (
	// RaydiumAMMProgramID Raydium AMM v4 程序地址
	RaydiumAMMProgramID = "675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8"

	// PumpFunProgramID PumpFun 程序地址
	PumpFunProgramID = "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P"

	// TokenProgramID SPL Token 程序地址
	TokenProgramID = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
)

// --- Raydium Swap 解析器 ---

// RaydiumSwapData 模拟 Raydium AMM Swap 指令的解码数据。
// 生产中这些字段是从 Borsh 编码的指令 Data 中反序列化的。
type RaydiumSwapData struct {
	Pool      string `json:"pool"`
	TokenIn   string `json:"token_in"`
	TokenOut  string `json:"token_out"`
	AmountIn  string `json:"amount_in"`  // 字符串表示的大整数
	AmountOut string `json:"amount_out"` // 字符串表示的大整数
}

// NewRaydiumSwapHandler 创建 Raydium AMM Swap 事件解析器。
// 按 ProgramID = RaydiumAMMProgramID 匹配。
func NewRaydiumSwapHandler() dexwallet.EventHandler {
	return func(ctx context.Context, rawData []byte) (*dexwallet.ChainEvent, error) {
		var data RaydiumSwapData
		if err := json.Unmarshal(rawData, &data); err != nil {
			return nil, fmt.Errorf("raydium swap unmarshal: %w", err)
		}

		amountIn, ok := new(big.Int).SetString(data.AmountIn, 10)
		if !ok {
			return nil, fmt.Errorf("raydium swap: invalid amount_in: %s", data.AmountIn)
		}

		amountOut, ok := new(big.Int).SetString(data.AmountOut, 10)
		if !ok {
			return nil, fmt.Errorf("raydium swap: invalid amount_out: %s", data.AmountOut)
		}

		return &dexwallet.ChainEvent{
			Type:      dexwallet.EventSwap,
			DexID:     dexwallet.DexRaydiumAMM,
			Pool:      data.Pool,
			TokenIn:   data.TokenIn,
			TokenOut:  data.TokenOut,
			AmountIn:  amountIn,
			AmountOut: amountOut,
		}, nil
	}
}

// --- PumpFun 交易解析器 ---

// PumpFunTradeData 模拟 PumpFun 交易指令的解码数据。
// PumpFun 使用联合曲线（Bonding Curve），交易模式与标准 AMM 不同。
type PumpFunTradeData struct {
	Mint      string `json:"mint"`       // 代币 Mint 地址
	IsBuy     bool   `json:"is_buy"`     // true=买入, false=卖出
	SolAmount string `json:"sol_amount"` // SOL 金额（lamports）
	TokenAmt  string `json:"token_amount"` // 代币金额（最小单位）
}

// NewPumpFunTradeHandler 创建 PumpFun 交易事件解析器。
// 按 ProgramID = PumpFunProgramID 匹配。
func NewPumpFunTradeHandler() dexwallet.EventHandler {
	return func(ctx context.Context, rawData []byte) (*dexwallet.ChainEvent, error) {
		var data PumpFunTradeData
		if err := json.Unmarshal(rawData, &data); err != nil {
			return nil, fmt.Errorf("pumpfun trade unmarshal: %w", err)
		}

		solAmount, ok := new(big.Int).SetString(data.SolAmount, 10)
		if !ok {
			return nil, fmt.Errorf("pumpfun trade: invalid sol_amount: %s", data.SolAmount)
		}

		tokenAmount, ok := new(big.Int).SetString(data.TokenAmt, 10)
		if !ok {
			return nil, fmt.Errorf("pumpfun trade: invalid token_amount: %s", data.TokenAmt)
		}

		event := &dexwallet.ChainEvent{
			Type:  dexwallet.EventSwap,
			DexID: dexwallet.DexPumpFun,
			Pool:  data.Mint, // PumpFun 的"池子"就是 Mint 地址
		}

		if data.IsBuy {
			// 买入：用 SOL 换 Token
			event.TokenIn = "SOL"
			event.TokenOut = data.Mint
			event.AmountIn = solAmount
			event.AmountOut = tokenAmount
		} else {
			// 卖出：用 Token 换 SOL
			event.TokenIn = data.Mint
			event.TokenOut = "SOL"
			event.AmountIn = tokenAmount
			event.AmountOut = solAmount
		}

		return event, nil
	}
}

// --- SPL Token Transfer 解析器 ---

// SPLTransferData 模拟 SPL Token Transfer 指令的解码数据。
type SPLTransferData struct {
	Mint   string `json:"mint"`
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"` // 字符串表示的大整数
}

// NewSPLTransferHandler 创建 SPL Token Transfer 事件解析器。
// 按 ProgramID = TokenProgramID 匹配。
func NewSPLTransferHandler() dexwallet.EventHandler {
	return func(ctx context.Context, rawData []byte) (*dexwallet.ChainEvent, error) {
		var data SPLTransferData
		if err := json.Unmarshal(rawData, &data); err != nil {
			return nil, fmt.Errorf("spl transfer unmarshal: %w", err)
		}

		amount, ok := new(big.Int).SetString(data.Amount, 10)
		if !ok {
			return nil, fmt.Errorf("spl transfer: invalid amount: %s", data.Amount)
		}

		return &dexwallet.ChainEvent{
			Type:      dexwallet.EventTransfer,
			TokenIn:   data.From,
			TokenOut:  data.To,
			AmountIn:  amount,
			AmountOut: amount, // Transfer 的输入输出金额相等
			Pool:      data.Mint,
		}, nil
	}
}
