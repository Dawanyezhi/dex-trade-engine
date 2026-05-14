// EVM 事件解析器
// [链特定层] -- 按 Topic 签名哈希匹配并解析 EVM 事件日志
//
// 包含 2 种解析器：
// - UniswapV2 Swap 事件解析器
// - ERC20 Transfer 事件解析器
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// EVM 事件 Topic 签名哈希
// 生产中这些是 keccak256 哈希值
const (
	// UniV2SwapTopic UniswapV2 Swap 事件签名
	// keccak256("Swap(address,uint256,uint256,uint256,uint256,address)")
	UniV2SwapTopic = "0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822"

	// ERC20TransferTopic ERC20 Transfer 事件签名
	// keccak256("Transfer(address,address,uint256)")
	ERC20TransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
)

// --- UniswapV2 Swap 解析器 ---

// UniV2SwapData 模拟 UniswapV2 Swap 事件的 ABI 解码数据。
// 生产中从 Log.Data 通过 ABI 解码得到 amount0In/amount1In/amount0Out/amount1Out。
type UniV2SwapData struct {
	Pair       string `json:"pair"`        // 交易对合约地址
	Token0     string `json:"token0"`      // 池子中的 token0
	Token1     string `json:"token1"`      // 池子中的 token1
	Amount0In  string `json:"amount0_in"`  // token0 输入量
	Amount1In  string `json:"amount1_in"`  // token1 输入量
	Amount0Out string `json:"amount0_out"` // token0 输出量
	Amount1Out string `json:"amount1_out"` // token1 输出量
}

// NewUniV2SwapHandler 创建 UniswapV2 Swap 事件解析器。
// 按 Topic = UniV2SwapTopic 匹配。
//
// UniswapV2 的 Swap 事件中，amountXIn 和 amountXOut 成对出现：
// - 如果 amount0In > 0 且 amount1Out > 0，表示用 token0 换 token1
// - 如果 amount1In > 0 且 amount0Out > 0，表示用 token1 换 token0
func NewUniV2SwapHandler() dexwallet.EventHandler {
	return func(ctx context.Context, rawData []byte) (*dexwallet.ChainEvent, error) {
		var data UniV2SwapData
		if err := json.Unmarshal(rawData, &data); err != nil {
			return nil, fmt.Errorf("univ2 swap unmarshal: %w", err)
		}

		amount0In, ok := new(big.Int).SetString(data.Amount0In, 10)
		if !ok {
			return nil, fmt.Errorf("univ2 swap: invalid amount0_in: %s", data.Amount0In)
		}

		amount1In, ok := new(big.Int).SetString(data.Amount1In, 10)
		if !ok {
			return nil, fmt.Errorf("univ2 swap: invalid amount1_in: %s", data.Amount1In)
		}

		amount0Out, ok := new(big.Int).SetString(data.Amount0Out, 10)
		if !ok {
			return nil, fmt.Errorf("univ2 swap: invalid amount0_out: %s", data.Amount0Out)
		}

		amount1Out, ok := new(big.Int).SetString(data.Amount1Out, 10)
		if !ok {
			return nil, fmt.Errorf("univ2 swap: invalid amount1_out: %s", data.Amount1Out)
		}

		event := &dexwallet.ChainEvent{
			Type:  dexwallet.EventSwap,
			DexID: dexwallet.DexUniswapV2,
			Pool:  data.Pair,
		}

		// 判断 Swap 方向
		zero := big.NewInt(0)
		if amount0In.Cmp(zero) > 0 && amount1Out.Cmp(zero) > 0 {
			// 用 token0 换 token1
			event.TokenIn = data.Token0
			event.TokenOut = data.Token1
			event.AmountIn = amount0In
			event.AmountOut = amount1Out
		} else if amount1In.Cmp(zero) > 0 && amount0Out.Cmp(zero) > 0 {
			// 用 token1 换 token0
			event.TokenIn = data.Token1
			event.TokenOut = data.Token0
			event.AmountIn = amount1In
			event.AmountOut = amount0Out
		} else {
			return nil, fmt.Errorf("univ2 swap: unexpected amounts pattern: a0in=%s a1in=%s a0out=%s a1out=%s",
				amount0In, amount1In, amount0Out, amount1Out)
		}

		return event, nil
	}
}

// --- ERC20 Transfer 解析器 ---

// ERC20TransferData 模拟 ERC20 Transfer 事件的 ABI 解码数据。
// 生产中 from 和 to 从 Topics[1] 和 Topics[2] 解码（indexed 参数），
// value 从 Log.Data ABI 解码。
type ERC20TransferData struct {
	Token  string `json:"token"` // ERC20 合约地址
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"` // 字符串表示的大整数（wei）
}

// NewERC20TransferHandler 创建 ERC20 Transfer 事件解析器。
// 按 Topic = ERC20TransferTopic 匹配。
func NewERC20TransferHandler() dexwallet.EventHandler {
	return func(ctx context.Context, rawData []byte) (*dexwallet.ChainEvent, error) {
		var data ERC20TransferData
		if err := json.Unmarshal(rawData, &data); err != nil {
			return nil, fmt.Errorf("erc20 transfer unmarshal: %w", err)
		}

		amount, ok := new(big.Int).SetString(data.Amount, 10)
		if !ok {
			return nil, fmt.Errorf("erc20 transfer: invalid amount: %s", data.Amount)
		}

		return &dexwallet.ChainEvent{
			Type:      dexwallet.EventTransfer,
			Pool:      data.Token, // Transfer 事件的"池子"是 Token 合约地址
			TokenIn:   data.From,
			TokenOut:  data.To,
			AmountIn:  amount,
			AmountOut: amount, // Transfer 的输入输出金额相等
		}, nil
	}
}
