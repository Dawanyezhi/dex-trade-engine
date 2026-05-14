package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// --- 辅助函数 ---

// mustMarshal JSON 序列化，失败则 panic（仅测试中使用）。
func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	return data
}

// buildSolanaTx 构造一个 Solana 交易的 JSON 字节。
func buildSolanaTx(t *testing.T, txHash string, block uint64, instructions []MockInstruction) []byte {
	t.Helper()
	tx := MockRawTransaction{
		TxHash:       txHash,
		Block:        block,
		Timestamp:    time.Now().Unix(),
		ChainID:      string(coinset.ChainSolana),
		Instructions: instructions,
	}
	return mustMarshal(t, tx)
}

// buildEVMTx 构造一个 EVM 交易的 JSON 字节。
func buildEVMTx(t *testing.T, txHash string, block uint64, logs []MockEventLog) []byte {
	t.Helper()
	tx := MockRawTransaction{
		TxHash:    txHash,
		Block:     block,
		Timestamp: time.Now().Unix(),
		ChainID:   string(coinset.ChainBSC),
		Logs:      logs,
	}
	return mustMarshal(t, tx)
}

// --- 测试：正常解析各种事件类型 ---

func TestParseRaydiumSwap(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(RaydiumAMMProgramID, NewRaydiumSwapHandler())

	swapData := mustMarshal(t, RaydiumSwapData{
		Pool:      "PoolAddress123",
		TokenIn:   "SOL",
		TokenOut:  "USDC",
		AmountIn:  "1000000000",
		AmountOut: "150000000",
	})

	rawTx := buildSolanaTx(t, "tx_raydium_1", 100, []MockInstruction{
		{ProgramID: RaydiumAMMProgramID, Data: swapData},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Type != dexwallet.EventSwap {
		t.Errorf("expected event type %s, got %s", dexwallet.EventSwap, e.Type)
	}
	if e.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("expected dex %s, got %s", dexwallet.DexRaydiumAMM, e.DexID)
	}
	if e.ChainID != coinset.ChainSolana {
		t.Errorf("expected chain %s, got %s", coinset.ChainSolana, e.ChainID)
	}
	if e.TxHash != "tx_raydium_1" {
		t.Errorf("expected tx hash tx_raydium_1, got %s", e.TxHash)
	}
	if e.Block != 100 {
		t.Errorf("expected block 100, got %d", e.Block)
	}
	if e.AmountIn.Cmp(big.NewInt(1000000000)) != 0 {
		t.Errorf("expected amountIn 1000000000, got %s", e.AmountIn)
	}
	if e.AmountOut.Cmp(big.NewInt(150000000)) != 0 {
		t.Errorf("expected amountOut 150000000, got %s", e.AmountOut)
	}
}

func TestParsePumpFunBuy(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(PumpFunProgramID, NewPumpFunTradeHandler())

	buyData := mustMarshal(t, PumpFunTradeData{
		Mint:      "MintAddress456",
		IsBuy:     true,
		SolAmount: "500000000",
		TokenAmt:  "1000000000",
	})

	rawTx := buildSolanaTx(t, "tx_pump_buy", 101, []MockInstruction{
		{ProgramID: PumpFunProgramID, Data: buyData},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Type != dexwallet.EventSwap {
		t.Errorf("expected event type %s, got %s", dexwallet.EventSwap, e.Type)
	}
	if e.DexID != dexwallet.DexPumpFun {
		t.Errorf("expected dex %s, got %s", dexwallet.DexPumpFun, e.DexID)
	}
	// 买入：TokenIn=SOL, TokenOut=Mint
	if e.TokenIn != "SOL" {
		t.Errorf("expected tokenIn SOL, got %s", e.TokenIn)
	}
	if e.TokenOut != "MintAddress456" {
		t.Errorf("expected tokenOut MintAddress456, got %s", e.TokenOut)
	}
	if e.AmountIn.Cmp(big.NewInt(500000000)) != 0 {
		t.Errorf("expected amountIn 500000000, got %s", e.AmountIn)
	}
}

func TestParsePumpFunSell(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(PumpFunProgramID, NewPumpFunTradeHandler())

	sellData := mustMarshal(t, PumpFunTradeData{
		Mint:      "MintAddress789",
		IsBuy:     false,
		SolAmount: "200000000",
		TokenAmt:  "5000000000",
	})

	rawTx := buildSolanaTx(t, "tx_pump_sell", 102, []MockInstruction{
		{ProgramID: PumpFunProgramID, Data: sellData},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	// 卖出：TokenIn=Mint, TokenOut=SOL
	if e.TokenIn != "MintAddress789" {
		t.Errorf("expected tokenIn MintAddress789, got %s", e.TokenIn)
	}
	if e.TokenOut != "SOL" {
		t.Errorf("expected tokenOut SOL, got %s", e.TokenOut)
	}
}

func TestParseSPLTransfer(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(TokenProgramID, NewSPLTransferHandler())

	transferData := mustMarshal(t, SPLTransferData{
		Mint:   "USDC_Mint",
		From:   "WalletA",
		To:     "WalletB",
		Amount: "100000000",
	})

	rawTx := buildSolanaTx(t, "tx_transfer", 103, []MockInstruction{
		{ProgramID: TokenProgramID, Data: transferData},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Type != dexwallet.EventTransfer {
		t.Errorf("expected event type %s, got %s", dexwallet.EventTransfer, e.Type)
	}
	if e.AmountIn.Cmp(big.NewInt(100000000)) != 0 {
		t.Errorf("expected amount 100000000, got %s", e.AmountIn)
	}
	// Transfer 的输入输出金额应相等
	if e.AmountIn.Cmp(e.AmountOut) != 0 {
		t.Errorf("transfer amountIn (%s) should equal amountOut (%s)", e.AmountIn, e.AmountOut)
	}
}

func TestParseUniV2Swap(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainBSC)
	registry.Register(UniV2SwapTopic, NewUniV2SwapHandler())

	// token0 -> token1 方向
	swapData := mustMarshal(t, UniV2SwapData{
		Pair:       "0xPairAddress",
		Token0:     "WETH",
		Token1:     "USDT",
		Amount0In:  "1000000000000000000",
		Amount1In:  "0",
		Amount0Out: "0",
		Amount1Out: "3500000000",
	})

	rawTx := buildEVMTx(t, "0xtx_univ2_1", 200, []MockEventLog{
		{
			Address: "0xPairAddress",
			Topics:  []string{UniV2SwapTopic},
			Data:    swapData,
		},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Type != dexwallet.EventSwap {
		t.Errorf("expected event type %s, got %s", dexwallet.EventSwap, e.Type)
	}
	if e.DexID != dexwallet.DexUniswapV2 {
		t.Errorf("expected dex %s, got %s", dexwallet.DexUniswapV2, e.DexID)
	}
	if e.TokenIn != "WETH" {
		t.Errorf("expected tokenIn WETH, got %s", e.TokenIn)
	}
	if e.TokenOut != "USDT" {
		t.Errorf("expected tokenOut USDT, got %s", e.TokenOut)
	}

	expectedIn, _ := new(big.Int).SetString("1000000000000000000", 10)
	if e.AmountIn.Cmp(expectedIn) != 0 {
		t.Errorf("expected amountIn %s, got %s", expectedIn, e.AmountIn)
	}
}

func TestParseUniV2SwapReverse(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainBSC)
	registry.Register(UniV2SwapTopic, NewUniV2SwapHandler())

	// token1 -> token0 方向（反向 swap）
	swapData := mustMarshal(t, UniV2SwapData{
		Pair:       "0xPairAddress",
		Token0:     "WETH",
		Token1:     "USDT",
		Amount0In:  "0",
		Amount1In:  "3500000000",
		Amount0Out: "1000000000000000000",
		Amount1Out: "0",
	})

	rawTx := buildEVMTx(t, "0xtx_univ2_reverse", 201, []MockEventLog{
		{
			Address: "0xPairAddress",
			Topics:  []string{UniV2SwapTopic},
			Data:    swapData,
		},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.TokenIn != "USDT" {
		t.Errorf("expected tokenIn USDT, got %s", e.TokenIn)
	}
	if e.TokenOut != "WETH" {
		t.Errorf("expected tokenOut WETH, got %s", e.TokenOut)
	}
}

func TestParseERC20Transfer(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainBSC)
	registry.Register(ERC20TransferTopic, NewERC20TransferHandler())

	transferData := mustMarshal(t, ERC20TransferData{
		Token:  "0xUSDT",
		From:   "0xAlice",
		To:     "0xBob",
		Amount: "1000000000",
	})

	rawTx := buildEVMTx(t, "0xtx_transfer", 202, []MockEventLog{
		{
			Address: "0xUSDT",
			Topics:  []string{ERC20TransferTopic},
			Data:    transferData,
		},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Type != dexwallet.EventTransfer {
		t.Errorf("expected event type %s, got %s", dexwallet.EventTransfer, e.Type)
	}
	if e.TokenIn != "0xAlice" {
		t.Errorf("expected from 0xAlice, got %s", e.TokenIn)
	}
	if e.TokenOut != "0xBob" {
		t.Errorf("expected to 0xBob, got %s", e.TokenOut)
	}
}

// --- 测试：未知事件跳过（不报错）---

func TestUnknownSolanaProgram(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(RaydiumAMMProgramID, NewRaydiumSwapHandler())

	rawTx := buildSolanaTx(t, "tx_unknown", 104, []MockInstruction{
		{ProgramID: "UnknownProgramID", Data: []byte(`{}`)},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse should not fail for unknown program: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown program, got %d", len(events))
	}
}

func TestUnknownEVMTopic(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainBSC)
	registry.Register(UniV2SwapTopic, NewUniV2SwapHandler())

	rawTx := buildEVMTx(t, "0xtx_unknown_topic", 203, []MockEventLog{
		{
			Address: "0xSomeContract",
			Topics:  []string{"0xunknown_topic_hash"},
			Data:    []byte(`{}`),
		},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse should not fail for unknown topic: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown topic, got %d", len(events))
	}
}

func TestEmptyTopicsSkipped(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainBSC)
	registry.Register(UniV2SwapTopic, NewUniV2SwapHandler())

	rawTx := buildEVMTx(t, "0xtx_empty_topics", 204, []MockEventLog{
		{
			Address: "0xSomeContract",
			Topics:  []string{}, // 空 topics
			Data:    []byte(`{}`),
		},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse should not fail for empty topics: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty topics, got %d", len(events))
	}
}

// --- 测试：多事件交易 ---

func TestMultipleEventsInOneTx(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(RaydiumAMMProgramID, NewRaydiumSwapHandler())
	registry.Register(TokenProgramID, NewSPLTransferHandler())

	swapData := mustMarshal(t, RaydiumSwapData{
		Pool: "Pool1", TokenIn: "SOL", TokenOut: "USDC",
		AmountIn: "1000000000", AmountOut: "150000000",
	})
	transferData := mustMarshal(t, SPLTransferData{
		Mint: "USDC", From: "WalletA", To: "WalletB", Amount: "150000000",
	})

	rawTx := buildSolanaTx(t, "tx_multi", 105, []MockInstruction{
		{ProgramID: RaydiumAMMProgramID, Data: swapData},
		{ProgramID: TokenProgramID, Data: transferData},
	})

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != dexwallet.EventSwap {
		t.Errorf("first event should be swap, got %s", events[0].Type)
	}
	if events[1].Type != dexwallet.EventTransfer {
		t.Errorf("second event should be transfer, got %s", events[1].Type)
	}
}

// --- 测试：重组回滚正确性 ---

func TestReorgDetection(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)

	// 不注册任何 handler，Syncer 只关注重组检测
	producer := &testBlockProducer{
		blocks: []*MockBlock{
			{Height: 1, ParentHash: "genesis", Hash: "h1"},
			{Height: 2, ParentHash: "h1", Hash: "h2"},
			{Height: 3, ParentHash: "h2", Hash: "h3"},
			// 重组：新的区块 3，parentHash 指向 h2 但 hash 不同
			// detectReorg 检测到 height <= currentHeight，触发回滚
			{Height: 3, ParentHash: "h2", Hash: "h3_reorg"},
			// 回滚后重新提供区块 3（重组后的版本）和后续区块
			{Height: 3, ParentHash: "h2", Hash: "h3_reorg"},
			{Height: 4, ParentHash: "h3_reorg", Hash: "h4"},
		},
	}

	syncer := NewBlockSyncer(registry, producer, nil)
	err := syncer.Run(context.Background())
	if err != nil {
		t.Fatalf("syncer run failed: %v", err)
	}

	if syncer.GetCurrentHeight() != 4 {
		t.Errorf("expected final height 4, got %d", syncer.GetCurrentHeight())
	}

	_, reorgCount, blocksHandled := syncer.GetStats()
	if reorgCount != 1 {
		t.Errorf("expected 1 reorg, got %d", reorgCount)
	}
	// 处理了区块 1,2,3 + 重组回滚 + 重组后的 3_reorg,4 = 5 个区块
	if blocksHandled != 5 {
		t.Errorf("expected 5 blocks handled, got %d", blocksHandled)
	}
}

func TestReorgRollbackDepth(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)

	producer := &testBlockProducer{
		blocks: []*MockBlock{
			{Height: 1, ParentHash: "genesis", Hash: "h1"},
			{Height: 2, ParentHash: "h1", Hash: "h2"},
			{Height: 3, ParentHash: "h2", Hash: "h3"},
			{Height: 4, ParentHash: "h3", Hash: "h4"},
			// 深度重组：回到区块 2 之后分叉，新的区块 3 parentHash=h2
			{Height: 3, ParentHash: "h2", Hash: "h3_new"},
			{Height: 4, ParentHash: "h3_new", Hash: "h4_new"},
			{Height: 5, ParentHash: "h4_new", Hash: "h5_new"},
		},
	}

	syncer := NewBlockSyncer(registry, producer, nil)
	err := syncer.Run(context.Background())
	if err != nil {
		t.Fatalf("syncer run failed: %v", err)
	}

	if syncer.GetCurrentHeight() != 5 {
		t.Errorf("expected final height 5, got %d", syncer.GetCurrentHeight())
	}

	_, reorgCount, _ := syncer.GetStats()
	if reorgCount < 1 {
		t.Errorf("expected at least 1 reorg, got %d", reorgCount)
	}
}

// --- 测试：并发注册安全 ---

func TestConcurrentRegister(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)

	// 10 个 goroutine 同时注册不同的 handler
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("program_%d", idx)
			registry.Register(id, func(ctx context.Context, rawData []byte) (*dexwallet.ChainEvent, error) {
				return &dexwallet.ChainEvent{Type: dexwallet.EventSwap}, nil
			})
		}(i)
	}
	wg.Wait()

	if registry.HandlerCount() != 10 {
		t.Errorf("expected 10 handlers, got %d", registry.HandlerCount())
	}
}

func TestConcurrentRegisterAndParse(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(RaydiumAMMProgramID, NewRaydiumSwapHandler())

	swapData := mustMarshal(t, RaydiumSwapData{
		Pool: "Pool1", TokenIn: "SOL", TokenOut: "USDC",
		AmountIn: "1000000000", AmountOut: "150000000",
	})
	rawTx := buildSolanaTx(t, "tx_concurrent", 100, []MockInstruction{
		{ProgramID: RaydiumAMMProgramID, Data: swapData},
	})

	// 同时进行注册和解析
	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	// 10 个 goroutine 解析
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events, err := registry.Parse(context.Background(), rawTx)
			if err != nil {
				errCh <- err
				return
			}
			if len(events) != 1 {
				errCh <- fmt.Errorf("expected 1 event, got %d", len(events))
			}
		}()
	}

	// 同时 10 个 goroutine 注册新 handler
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("new_program_%d", idx)
			registry.Register(id, func(ctx context.Context, rawData []byte) (*dexwallet.ChainEvent, error) {
				return nil, nil
			})
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent operation error: %v", err)
	}
}

// --- 测试：Syncer 高度推进 ---

func TestSyncerHeightAdvance(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)

	producer := &testBlockProducer{
		blocks: []*MockBlock{
			{Height: 1, ParentHash: "genesis", Hash: "h1"},
			{Height: 2, ParentHash: "h1", Hash: "h2"},
			{Height: 3, ParentHash: "h2", Hash: "h3"},
			{Height: 4, ParentHash: "h3", Hash: "h4"},
			{Height: 5, ParentHash: "h4", Hash: "h5"},
		},
	}

	syncer := NewBlockSyncer(registry, producer, nil)

	// 初始高度为 0
	if syncer.GetCurrentHeight() != 0 {
		t.Errorf("expected initial height 0, got %d", syncer.GetCurrentHeight())
	}

	err := syncer.Run(context.Background())
	if err != nil {
		t.Fatalf("syncer run failed: %v", err)
	}

	// 最终高度应该是 5
	if syncer.GetCurrentHeight() != 5 {
		t.Errorf("expected final height 5, got %d", syncer.GetCurrentHeight())
	}

	_, _, blocksHandled := syncer.GetStats()
	if blocksHandled != 5 {
		t.Errorf("expected 5 blocks handled, got %d", blocksHandled)
	}
}

func TestSyncerWithEvents(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(RaydiumAMMProgramID, NewRaydiumSwapHandler())

	swapData := mustMarshal(t, RaydiumSwapData{
		Pool: "Pool1", TokenIn: "SOL", TokenOut: "USDC",
		AmountIn: "1000000000", AmountOut: "150000000",
	})

	producer := &testBlockProducer{
		blocks: []*MockBlock{
			{
				Height: 1, ParentHash: "genesis", Hash: "h1",
				Transactions: []MockRawTransaction{
					{
						TxHash: "tx1", Block: 1, Timestamp: time.Now().Unix(),
						ChainID: string(coinset.ChainSolana),
						Instructions: []MockInstruction{
							{ProgramID: RaydiumAMMProgramID, Data: swapData},
						},
					},
				},
			},
			{
				Height: 2, ParentHash: "h1", Hash: "h2",
				Transactions: []MockRawTransaction{
					{
						TxHash: "tx2", Block: 2, Timestamp: time.Now().Unix(),
						ChainID: string(coinset.ChainSolana),
						Instructions: []MockInstruction{
							{ProgramID: RaydiumAMMProgramID, Data: swapData},
						},
					},
					{
						TxHash: "tx3", Block: 2, Timestamp: time.Now().Unix(),
						ChainID: string(coinset.ChainSolana),
						Instructions: []MockInstruction{
							{ProgramID: RaydiumAMMProgramID, Data: swapData},
						},
					},
				},
			},
		},
	}

	var eventCount int
	syncer := NewBlockSyncer(registry, producer, func(event *dexwallet.ChainEvent) {
		eventCount++
	})

	err := syncer.Run(context.Background())
	if err != nil {
		t.Fatalf("syncer run failed: %v", err)
	}

	// 区块 1 有 1 个事件，区块 2 有 2 个事件，共 3 个
	if eventCount != 3 {
		t.Errorf("expected 3 events dispatched, got %d", eventCount)
	}

	totalEvents, _, _ := syncer.GetStats()
	if totalEvents != 3 {
		t.Errorf("expected totalEvents 3, got %d", totalEvents)
	}
}

func TestSyncerEmptyBlocks(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)

	producer := &testBlockProducer{
		blocks: []*MockBlock{
			{Height: 1, ParentHash: "genesis", Hash: "h1"},
			{Height: 2, ParentHash: "h1", Hash: "h2"},
			{Height: 3, ParentHash: "h2", Hash: "h3"},
		},
	}

	syncer := NewBlockSyncer(registry, producer, nil)
	err := syncer.Run(context.Background())
	if err != nil {
		t.Fatalf("syncer run failed: %v", err)
	}

	if syncer.GetCurrentHeight() != 3 {
		t.Errorf("expected height 3, got %d", syncer.GetCurrentHeight())
	}

	totalEvents, _, _ := syncer.GetStats()
	if totalEvents != 0 {
		t.Errorf("expected 0 events for empty blocks, got %d", totalEvents)
	}
}

func TestSyncerContextCancel(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)

	// 无限区块生产者
	producer := &infiniteBlockProducer{startHeight: 1}

	syncer := NewBlockSyncer(registry, producer, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := syncer.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}

	// 应该处理了一些区块
	if syncer.GetCurrentHeight() == 0 {
		t.Error("expected some blocks to be processed before timeout")
	}
}

// --- 辅助类型 ---

// testBlockProducer 测试用区块生产者。
type testBlockProducer struct {
	blocks []*MockBlock
	idx    int
}

func (p *testBlockProducer) NextBlock() (*MockBlock, error) {
	if p.idx >= len(p.blocks) {
		return nil, nil
	}
	block := p.blocks[p.idx]
	p.idx++
	return block, nil
}

// infiniteBlockProducer 无限区块生产者（用于测试 context 取消）。
type infiniteBlockProducer struct {
	startHeight uint64
	current     uint64
	lastHash    string
}

func (p *infiniteBlockProducer) NextBlock() (*MockBlock, error) {
	if p.current == 0 {
		p.current = p.startHeight
	}
	parentHash := p.lastHash
	if parentHash == "" {
		parentHash = "genesis"
	}
	hash := fmt.Sprintf("hash_%d", p.current)
	block := &MockBlock{
		Height:     p.current,
		ParentHash: parentHash,
		Hash:       hash,
	}
	p.lastHash = hash
	p.current++
	return block, nil
}
