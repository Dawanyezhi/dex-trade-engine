// UniswapV3 Swap 事件解析器
// [链特定层] -- 按 Topic 签名哈希匹配并解析 UniswapV3 Swap 事件日志
//
// ============================================================================
// V3 与 V2 的核心区别
// ============================================================================
//
// UniswapV2 Swap 事件：
//   event Swap(address indexed sender, uint256 amount0In, uint256 amount1In,
//              uint256 amount0Out, uint256 amount1Out, address indexed to)
//   - 4 个 uint256 值（全部 >= 0）
//   - 方向判断：amount0In > 0 && amount1Out > 0 → token0 换 token1
//   - 简单直观，但无法表达精确的价格和流动性信息
//
// UniswapV3 Swap 事件：
//   event Swap(address indexed sender, address indexed recipient,
//              int256 amount0, int256 amount1,
//              uint160 sqrtPriceX96, uint128 liquidity, int24 tick)
//   - 2 个 int256 值（有符号！可以为负数）
//   - 从池子（pool）的视角描述资金流动：
//       正数 = 池子收到的代币（用户支付）
//       负数 = 池子支出的代币（用户收到）
//   - 额外携带价格和流动性信息（sqrtPriceX96, liquidity, tick）
//
// 为什么 V3 使用有符号整数？
//   V2 需要 4 个字段来表达一次 swap（amount0In/amount1In/amount0Out/amount1Out），
//   其中总有 2 个字段为 0。V3 用 2 个有符号字段就能表达相同信息：
//     - amount0 > 0, amount1 < 0: 池子收到 token0、支出 token1
//     - amount0 < 0, amount1 > 0: 池子支出 token0、收到 token1
//   这种设计更简洁，也更容易做数学运算（如累加多次 swap 的净头寸）。
//
// ============================================================================
// V3 价格相关字段说明
// ============================================================================
//
// sqrtPriceX96:
//   交易后的池子价格，使用定点数表示：sqrtPriceX96 = sqrt(price) * 2^96
//   其中 price = token1 / token0（即 1 个 token0 值多少 token1）。
//   使用 sqrt 是因为 V3 的数学公式中大量使用 sqrt(price)，避免重复计算。
//   使用 2^96 的定点数是因为 EVM 没有浮点数，需要用整数模拟小数精度。
//   还原价格: price = (sqrtPriceX96 / 2^96)^2
//
// tick:
//   离散化的价格点，tick = log_{1.0001}(price)
//   V3 将连续的价格空间离散化为 tick，每个 tick 对应一个价格：
//     price(tick) = 1.0001^tick
//   tick 的间距由 tickSpacing 决定（如 fee=0.3% 对应 tickSpacing=60）。
//   tick 的作用：确定当前活跃的流动性区间，LP 只在其设定的 [tickLower, tickUpper] 区间内提供流动性。
//
// liquidity:
//   交易后的活跃流动性，即当前 tick 所在区间内的总流动性。
//   V3 的集中流动性模型：LP 可以选择只在某个价格区间内提供流动性，
//   因此不同价格区间的流动性不同，liquidity 表示当前生效的那部分。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// UniV3SwapTopic UniswapV3 Swap 事件签名的 keccak256 哈希。
// keccak256("Swap(address,address,int256,int256,uint160,uint128,int24)")
const UniV3SwapTopic = "0xc42079f94a6350d7e6235f29174924f928cc2ac818eb64fed8004e115fbcca67"

// UniV3SwapData 模拟 UniswapV3 Swap 事件的 ABI 解码数据。
//
// 与 V2 的关键区别：
//   - Amount0/Amount1 是有符号整数（int256），可以为负数
//   - 正值 = 池子收到（用户支付），负值 = 池子支出（用户收到）
//   - 额外包含 sqrtPriceX96、liquidity、tick 等价格/流动性信息
type UniV3SwapData struct {
	Pair         string `json:"pair"`
	Token0       string `json:"token0"`
	Token1       string `json:"token1"`
	Amount0      string `json:"amount0"`                     // int256: 正=池子收到, 负=池子支出
	Amount1      string `json:"amount1"`                     // int256: 正=池子收到, 负=池子支出
	SqrtPriceX96 string `json:"sqrt_price_x96,omitempty"`    // 交易后的价格
	Liquidity    string `json:"liquidity,omitempty"`
	Tick         int    `json:"tick,omitempty"`
}

// NewUniV3SwapHandler 创建 UniswapV3 Swap 事件解析器。
// 按 Topic = UniV3SwapTopic 匹配。
//
// 解析逻辑（从池子视角转换为用户视角）：
//
//  1. amount0 > 0 且 amount1 < 0:
//     池子收到 token0，支出 token1
//     → 用户支付 token0，获得 token1
//     → TokenIn = token0, AmountIn = amount0
//     → TokenOut = token1, AmountOut = abs(amount1)
//
//  2. amount0 < 0 且 amount1 > 0:
//     池子支出 token0，收到 token1
//     → 用户支付 token1，获得 token0
//     → TokenIn = token1, AmountIn = amount1
//     → TokenOut = token0, AmountOut = abs(amount0)
//
// 注意：与 V2 不同，V3 的金额是从池子视角描述的，需要"翻转"才能得到用户视角。
// V2 的 amountXIn/amountXOut 直接从用户视角描述（虽然字段名暗示了方向），
// 但 V3 的正/负号明确区分了池子的收/支。
func NewUniV3SwapHandler() dexwallet.EventHandler {
	return func(ctx context.Context, rawData []byte) (*dexwallet.ChainEvent, error) {
		var data UniV3SwapData
		if err := json.Unmarshal(rawData, &data); err != nil {
			return nil, fmt.Errorf("univ3 swap unmarshal: %w", err)
		}

		// 解析 amount0 为有符号大整数（int256，可以为负数）
		// big.Int.SetString 支持负数（如 "-1234567890"），无需额外处理
		amount0, ok := new(big.Int).SetString(data.Amount0, 10)
		if !ok {
			return nil, fmt.Errorf("univ3 swap: invalid amount0: %s", data.Amount0)
		}

		// 解析 amount1 为有符号大整数
		amount1, ok := new(big.Int).SetString(data.Amount1, 10)
		if !ok {
			return nil, fmt.Errorf("univ3 swap: invalid amount1: %s", data.Amount1)
		}

		event := &dexwallet.ChainEvent{
			Type:  dexwallet.EventSwap,
			DexID: dexwallet.DexUniswapV3,
			Pool:  data.Pair,
		}

		zero := big.NewInt(0)

		switch {
		case amount0.Cmp(zero) > 0 && amount1.Cmp(zero) < 0:
			// 池子收到 token0（amount0 > 0），支出 token1（amount1 < 0）
			// → 用户卖出 token0 换取 token1
			event.TokenIn = data.Token0
			event.AmountIn = amount0
			event.TokenOut = data.Token1
			event.AmountOut = new(big.Int).Abs(amount1) // 取绝对值：用户收到的数量

		case amount0.Cmp(zero) < 0 && amount1.Cmp(zero) > 0:
			// 池子支出 token0（amount0 < 0），收到 token1（amount1 > 0）
			// → 用户卖出 token1 换取 token0
			event.TokenIn = data.Token1
			event.AmountIn = amount1
			event.TokenOut = data.Token0
			event.AmountOut = new(big.Int).Abs(amount0) // 取绝对值：用户收到的数量

		default:
			// 异常情况：两个金额同号或有零值
			// 正常的 swap 不应出现这种情况（池子必然一进一出）
			return nil, fmt.Errorf("univ3 swap: unexpected amounts pattern: amount0=%s amount1=%s (expected opposite signs)",
				amount0, amount1)
		}

		return event, nil
	}
}
