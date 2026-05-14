// 07-event-parsing 演示程序
// 演示链上事件解析与区块同步机制。
//
// 运行: go run ./07-event-parsing/demo/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

func main() {
	// 设置日志级别为 Debug，方便观察详细信息
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	fmt.Println("========================================")
	fmt.Println("  07-event-parsing: 链上事件解析与同步演示")
	fmt.Println("========================================")
	fmt.Println()

	// 演示 1: Solana 事件解析
	demoSolanaEventParsing()

	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()

	// 演示 2: EVM 事件解析
	demoEVMEventParsing()

	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()

	// 演示 3: Syncer 区块同步（包含重组）
	demoSyncerWithReorg()
}

// demoSolanaEventParsing 演示 Solana 事件解析。
func demoSolanaEventParsing() {
	fmt.Println("[Solana] === 事件解析演示 ===")
	fmt.Println()

	// 创建 Solana 解析器注册表
	registry := NewParserRegistry(coinset.ChainSolana)

	// 注册 3 种 Solana 事件解析器
	registry.Register(RaydiumAMMProgramID, NewRaydiumSwapHandler())
	registry.Register(PumpFunProgramID, NewPumpFunTradeHandler())
	registry.Register(TokenProgramID, NewSPLTransferHandler())

	fmt.Printf("  已注册 %d 个 Solana 事件解析器\n", registry.HandlerCount())
	fmt.Println()

	// 构造模拟的 Solana 交易（包含 Raydium Swap 指令）
	raydiumSwapData, _ := json.Marshal(RaydiumSwapData{
		Pool:      "58oQChx4yWmvKdwLLZzBi4ChoCc2fqCUWBkwMihLYQo2",
		TokenIn:   "So11111111111111111111111111111111111111112",
		TokenOut:  "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		AmountIn:  "1000000000",  // 1 SOL
		AmountOut: "150000000",   // 150 USDC
	})

	solTx1 := MockRawTransaction{
		TxHash:    "5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tjF3ZpRzrFmBV6UjKdiSZkQUW",
		Block:     200000001,
		Timestamp: time.Now().Unix(),
		ChainID:   string(coinset.ChainSolana),
		Instructions: []MockInstruction{
			{ProgramID: RaydiumAMMProgramID, Data: raydiumSwapData},
		},
	}

	rawTx1, _ := json.Marshal(solTx1)
	events, err := registry.Parse(context.Background(), rawTx1)
	if err != nil {
		fmt.Printf("  解析失败: %v\n", err)
		return
	}
	fmt.Println("  [Raydium Swap] 解析结果:")
	printEvents(events)

	// 构造 PumpFun 买入交易
	pumpBuyData, _ := json.Marshal(PumpFunTradeData{
		Mint:      "7GCihgDB8fe6KNjn2MYtkzZcRjQy3t9GHdC8uHYmW2hr",
		IsBuy:     true,
		SolAmount: "500000000",   // 0.5 SOL
		TokenAmt:  "1000000000",  // 10亿 token
	})

	solTx2 := MockRawTransaction{
		TxHash:    "3AZi1HNHE5qMFnEJkPUMbPHTfgf6HJwARKRQUfxYrZ2SqCNpgzQ8nYUhHLdMbGCfJG7DPqEiE7YCNP6gGHUHgvZ",
		Block:     200000002,
		Timestamp: time.Now().Unix(),
		ChainID:   string(coinset.ChainSolana),
		Instructions: []MockInstruction{
			{ProgramID: PumpFunProgramID, Data: pumpBuyData},
		},
	}

	rawTx2, _ := json.Marshal(solTx2)
	events, err = registry.Parse(context.Background(), rawTx2)
	if err != nil {
		fmt.Printf("  解析失败: %v\n", err)
		return
	}
	fmt.Println("  [PumpFun Buy] 解析结果:")
	printEvents(events)

	// 构造 SPL Token Transfer 交易
	transferData, _ := json.Marshal(SPLTransferData{
		Mint:   "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		From:   "Wallet111111111111111111111111111111111111",
		To:     "Wallet222222222222222222222222222222222222",
		Amount: "50000000", // 50 USDC
	})

	solTx3 := MockRawTransaction{
		TxHash:    "4BwFc9cPKJXfmBcTAzCpzxLNKqMYLc5NMW3xJ8KGsGBqZpj7FUNqr8F5YMBbQHmNMWp9rFAzd9HqKCBqNRJvJmS",
		Block:     200000003,
		Timestamp: time.Now().Unix(),
		ChainID:   string(coinset.ChainSolana),
		Instructions: []MockInstruction{
			{ProgramID: TokenProgramID, Data: transferData},
		},
	}

	rawTx3, _ := json.Marshal(solTx3)
	events, err = registry.Parse(context.Background(), rawTx3)
	if err != nil {
		fmt.Printf("  解析失败: %v\n", err)
		return
	}
	fmt.Println("  [SPL Transfer] 解析结果:")
	printEvents(events)

	// 演示：包含未知 ProgramID 的交易（应被静默跳过）
	solTx4 := MockRawTransaction{
		TxHash:    "UnknownTx1111111111111111111111111111111111111111",
		Block:     200000004,
		Timestamp: time.Now().Unix(),
		ChainID:   string(coinset.ChainSolana),
		Instructions: []MockInstruction{
			{ProgramID: "UnknownProgram11111111111111111111111111111", Data: []byte(`{}`)},
		},
	}

	rawTx4, _ := json.Marshal(solTx4)
	events, err = registry.Parse(context.Background(), rawTx4)
	if err != nil {
		fmt.Printf("  解析失败: %v\n", err)
		return
	}
	fmt.Printf("  [未知 Program] 解析事件数: %d (预期为 0，未知事件被静默跳过)\n", len(events))
}

// demoEVMEventParsing 演示 EVM 事件解析。
func demoEVMEventParsing() {
	fmt.Println("[BSC] === 事件解析演示 ===")
	fmt.Println()

	// 创建 BSC 解析器注册表
	registry := NewParserRegistry(coinset.ChainBSC)

	// 注册 EVM 事件解析器
	registry.Register(UniV2SwapTopic, NewUniV2SwapHandler())
	registry.Register(ERC20TransferTopic, NewERC20TransferHandler())

	fmt.Printf("  已注册 %d 个 EVM 事件解析器\n", registry.HandlerCount())
	fmt.Println()

	// 构造 UniswapV2 Swap 事件
	uniSwapData, _ := json.Marshal(UniV2SwapData{
		Pair:       "0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc",
		Token0:     "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", // WETH
		Token1:     "0xdAC17F958D2ee523a2206206994597C13D831ec7", // USDT
		Amount0In:  "1000000000000000000", // 1 WETH
		Amount1In:  "0",
		Amount0Out: "0",
		Amount1Out: "3500000000", // 3500 USDT (6 decimals)
	})

	evmTx1 := MockRawTransaction{
		TxHash:    "0xa1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456",
		Block:     18000001,
		Timestamp: time.Now().Unix(),
		ChainID:   string(coinset.ChainBSC),
		Logs: []MockEventLog{
			{
				Address: "0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc",
				Topics:  []string{UniV2SwapTopic},
				Data:    uniSwapData,
			},
		},
	}

	rawTx1, _ := json.Marshal(evmTx1)
	events, err := registry.Parse(context.Background(), rawTx1)
	if err != nil {
		fmt.Printf("  解析失败: %v\n", err)
		return
	}
	fmt.Println("  [UniV2 Swap] 解析结果:")
	printEvents(events)

	// 构造 ERC20 Transfer 事件
	erc20Data, _ := json.Marshal(ERC20TransferData{
		Token:  "0xdAC17F958D2ee523a2206206994597C13D831ec7",
		From:   "0x1111111111111111111111111111111111111111",
		To:     "0x2222222222222222222222222222222222222222",
		Amount: "1000000000", // 1000 USDT (6 decimals)
	})

	evmTx2 := MockRawTransaction{
		TxHash:    "0xf1e2d3c4b5a6789012345678901234567890abcdef1234567890abcdef654321",
		Block:     18000002,
		Timestamp: time.Now().Unix(),
		ChainID:   string(coinset.ChainBSC),
		Logs: []MockEventLog{
			{
				Address: "0xdAC17F958D2ee523a2206206994597C13D831ec7",
				Topics:  []string{ERC20TransferTopic},
				Data:    erc20Data,
			},
		},
	}

	rawTx2, _ := json.Marshal(evmTx2)
	events, err = registry.Parse(context.Background(), rawTx2)
	if err != nil {
		fmt.Printf("  解析失败: %v\n", err)
		return
	}
	fmt.Println("  [ERC20 Transfer] 解析结果:")
	printEvents(events)
}

// demoSyncerWithReorg 演示 Syncer 区块同步，包含重组场景。
func demoSyncerWithReorg() {
	fmt.Println("[Syncer] === 区块同步与重组检测演示 ===")
	fmt.Println()

	// 创建 Solana 解析器（Syncer 使用）
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(RaydiumAMMProgramID, NewRaydiumSwapHandler())
	registry.Register(PumpFunProgramID, NewPumpFunTradeHandler())
	registry.Register(TokenProgramID, NewSPLTransferHandler())

	// 收集所有解析出的事件
	var collectedEvents []dexwallet.ChainEvent
	poolUpdater := func(event *dexwallet.ChainEvent) {
		collectedEvents = append(collectedEvents, *event)
		fmt.Printf("    [事件回调] type=%s dex=%s pool=%s amountIn=%s amountOut=%s\n",
			event.Type, event.DexID, truncateAddr(event.Pool),
			formatBigInt(event.AmountIn), formatBigInt(event.AmountOut),
		)
	}

	// 构造模拟区块序列（包含正常区块和一次重组）
	producer := newDemoBlockProducer()

	// 创建 Syncer
	syncer := NewBlockSyncer(registry, producer, poolUpdater)

	fmt.Println("  启动 Syncer，开始同步区块...")
	fmt.Println()

	// 运行 Syncer（producer 生成完所有区块后会返回 nil，Syncer 自动停止）
	err := syncer.Run(context.Background())
	if err != nil && err != context.Canceled {
		fmt.Printf("  Syncer 异常退出: %v\n", err)
	}

	fmt.Println()
	fmt.Printf("  Syncer 最终高度: %d\n", syncer.GetCurrentHeight())
	totalEvents, reorgCount, blocksHandled := syncer.GetStats()
	fmt.Printf("  统计: 处理区块=%d, 解析事件=%d, 重组次数=%d\n",
		blocksHandled, totalEvents, reorgCount,
	)
	fmt.Printf("  事件回调收到事件总数: %d\n", len(collectedEvents))
}

// demoBlockProducer 模拟区块生产者，生成预设的区块序列。
type demoBlockProducer struct {
	blocks []*MockBlock
	idx    int
}

// newDemoBlockProducer 创建演示用的区块生产者。
// 生成 5 个正常区块 + 1 次重组（第 4 个区块被替换）。
func newDemoBlockProducer() *demoBlockProducer {
	now := time.Now().Unix()

	// 构造交易数据
	raydiumSwap1, _ := json.Marshal(RaydiumSwapData{
		Pool: "Pool1111111111111111111111111111111111111",
		TokenIn: "SOL", TokenOut: "USDC",
		AmountIn: "2000000000", AmountOut: "300000000",
	})
	raydiumSwap2, _ := json.Marshal(RaydiumSwapData{
		Pool: "Pool2222222222222222222222222222222222222",
		TokenIn: "SOL", TokenOut: "RAY",
		AmountIn: "5000000000", AmountOut: "100000000000",
	})
	pumpBuy, _ := json.Marshal(PumpFunTradeData{
		Mint: "Pump1111111111111111111111111111111111111",
		IsBuy: true, SolAmount: "100000000", TokenAmt: "500000000",
	})
	pumpSell, _ := json.Marshal(PumpFunTradeData{
		Mint: "Pump2222222222222222222222222222222222222",
		IsBuy: false, SolAmount: "200000000", TokenAmt: "1000000000",
	})
	transfer1, _ := json.Marshal(SPLTransferData{
		Mint: "USDC1111111111111111111111111111111111111",
		From: "WalletA", To: "WalletB", Amount: "50000000",
	})

	blocks := []*MockBlock{
		// 区块 1: 包含一笔 Raydium Swap
		{
			Height: 1, ParentHash: "genesis", Hash: "hash_1",
			Transactions: []MockRawTransaction{
				{
					TxHash: "tx_block1_raydium", Block: 1, Timestamp: now,
					ChainID: string(coinset.ChainSolana),
					Instructions: []MockInstruction{
						{ProgramID: RaydiumAMMProgramID, Data: raydiumSwap1},
					},
				},
			},
		},
		// 区块 2: 包含 PumpFun 买入和 Transfer
		{
			Height: 2, ParentHash: "hash_1", Hash: "hash_2",
			Transactions: []MockRawTransaction{
				{
					TxHash: "tx_block2_pump", Block: 2, Timestamp: now + 1,
					ChainID: string(coinset.ChainSolana),
					Instructions: []MockInstruction{
						{ProgramID: PumpFunProgramID, Data: pumpBuy},
					},
				},
				{
					TxHash: "tx_block2_transfer", Block: 2, Timestamp: now + 1,
					ChainID: string(coinset.ChainSolana),
					Instructions: []MockInstruction{
						{ProgramID: TokenProgramID, Data: transfer1},
					},
				},
			},
		},
		// 区块 3: 包含另一笔 Raydium Swap
		{
			Height: 3, ParentHash: "hash_2", Hash: "hash_3",
			Transactions: []MockRawTransaction{
				{
					TxHash: "tx_block3_raydium", Block: 3, Timestamp: now + 2,
					ChainID: string(coinset.ChainSolana),
					Instructions: []MockInstruction{
						{ProgramID: RaydiumAMMProgramID, Data: raydiumSwap2},
					},
				},
			},
		},
		// 区块 4: 正常区块（后面会被重组替换）
		{
			Height: 4, ParentHash: "hash_3", Hash: "hash_4",
			Transactions: []MockRawTransaction{
				{
					TxHash: "tx_block4_pump_sell", Block: 4, Timestamp: now + 3,
					ChainID: string(coinset.ChainSolana),
					Instructions: []MockInstruction{
						{ProgramID: PumpFunProgramID, Data: pumpSell},
					},
				},
			},
		},
		// 区块 5: 正常区块
		{
			Height: 5, ParentHash: "hash_4", Hash: "hash_5",
			Transactions: []MockRawTransaction{
				{
					TxHash: "tx_block5_raydium", Block: 5, Timestamp: now + 4,
					ChainID: string(coinset.ChainSolana),
					Instructions: []MockInstruction{
						{ProgramID: RaydiumAMMProgramID, Data: raydiumSwap1},
					},
				},
			},
		},

		// --- 重组发生：区块 4 被替换 ---
		// 新的区块 4'：parentHash 指向 hash_3，但 hash 不同
		{
			Height: 4, ParentHash: "hash_3", Hash: "hash_4_reorg",
			Transactions: []MockRawTransaction{
				{
					TxHash: "tx_block4_reorg_raydium", Block: 4, Timestamp: now + 3,
					ChainID: string(coinset.ChainSolana),
					Instructions: []MockInstruction{
						{ProgramID: RaydiumAMMProgramID, Data: raydiumSwap2},
					},
				},
			},
		},
		// 新的区块 5'
		{
			Height: 5, ParentHash: "hash_4_reorg", Hash: "hash_5_reorg",
			Transactions: []MockRawTransaction{
				{
					TxHash: "tx_block5_reorg_transfer", Block: 5, Timestamp: now + 4,
					ChainID: string(coinset.ChainSolana),
					Instructions: []MockInstruction{
						{ProgramID: TokenProgramID, Data: transfer1},
					},
				},
			},
		},
		// 区块 6: 重组后的正常区块
		{
			Height: 6, ParentHash: "hash_5_reorg", Hash: "hash_6",
			Transactions: []MockRawTransaction{
				{
					TxHash: "tx_block6_pump", Block: 6, Timestamp: now + 5,
					ChainID: string(coinset.ChainSolana),
					Instructions: []MockInstruction{
						{ProgramID: PumpFunProgramID, Data: pumpBuy},
					},
				},
			},
		},
	}

	return &demoBlockProducer{blocks: blocks}
}

// NextBlock 返回下一个模拟区块。
func (p *demoBlockProducer) NextBlock() (*MockBlock, error) {
	if p.idx >= len(p.blocks) {
		return nil, nil // 所有区块已发完
	}
	block := p.blocks[p.idx]
	p.idx++
	return block, nil
}

// printEvents 打印解析出的事件列表。
func printEvents(events []dexwallet.ChainEvent) {
	if len(events) == 0 {
		fmt.Println("    (无事件)")
		return
	}
	for i, e := range events {
		fmt.Printf("    [%d] type=%s chain=%s dex=%s\n", i, e.Type, e.ChainID, e.DexID)
		fmt.Printf("        tx=%s block=%d\n", truncateHash(e.TxHash), e.Block)
		fmt.Printf("        pool=%s\n", truncateAddr(e.Pool))
		fmt.Printf("        tokenIn=%s tokenOut=%s\n", truncateAddr(e.TokenIn), truncateAddr(e.TokenOut))
		fmt.Printf("        amountIn=%s amountOut=%s\n",
			formatBigInt(e.AmountIn), formatBigInt(e.AmountOut))
	}
	fmt.Println()
}

// truncateAddr 截断地址显示。
func truncateAddr(addr string) string {
	if len(addr) > 16 {
		return addr[:8] + "..." + addr[len(addr)-4:]
	}
	return addr
}

// truncateHash 截断哈希显示。
func truncateHash(hash string) string {
	if len(hash) > 20 {
		return hash[:12] + "..." + hash[len(hash)-4:]
	}
	return hash
}

// formatBigInt 格式化 big.Int 指针。
func formatBigInt(n *big.Int) string {
	if n == nil {
		return "0"
	}
	return n.String()
}
