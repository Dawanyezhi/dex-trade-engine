// 07-event-parsing 事件解析器注册机制
// [dexwallet 通用层] -- 注册机制通用，解析逻辑链特定
//
// ParserRegistry 实现了 dexwallet.EventParser 接口，
// 通过 map[string]EventHandler 实现标识符到解析器的映射。
// Solana 用 ProgramID 作为标识符，EVM 用 Topic 签名哈希。
//
// 运行: go run ./07-event-parsing/demo/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// MockRawTransaction 模拟链上原始交易数据。
// 同时包含 Solana 风格的 Instructions 和 EVM 风格的 Logs，
// 实际使用中根据 ChainID 决定解析哪个字段。
type MockRawTransaction struct {
	TxHash       string            `json:"tx_hash"`
	Block        uint64            `json:"block"`
	Timestamp    int64             `json:"timestamp"`
	ChainID      string            `json:"chain_id"`
	Instructions []MockInstruction `json:"instructions"` // Solana 风格
	Logs         []MockEventLog    `json:"logs"`          // EVM 风格
}

// MockInstruction 模拟 Solana 指令。
// 生产中 Data 是 Borsh 编码的字节，这里用 JSON 模拟。
type MockInstruction struct {
	ProgramID string `json:"program_id"`
	Data      []byte `json:"data"`
}

// MockEventLog 模拟 EVM 事件日志。
// 生产中 Topics[0] 是事件签名的 keccak256 哈希，Data 是 ABI 编码。
type MockEventLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    []byte   `json:"data"`
}

// ParserRegistry 事件解析器注册表。
// [dexwallet 通用层] -- 注册机制通用，解析逻辑链特定。
//
// 线程安全：Register 使用写锁，Parse 使用读锁，
// 允许多个 goroutine 并发解析，同时支持运行时动态注册新的解析器。
type ParserRegistry struct {
	mu       sync.RWMutex
	handlers map[string]dexwallet.EventHandler // identifier -> handler
	chainID  coinset.ChainID
}

// NewParserRegistry 创建新的事件解析器注册表。
func NewParserRegistry(chainID coinset.ChainID) *ParserRegistry {
	return &ParserRegistry{
		handlers: make(map[string]dexwallet.EventHandler),
		chainID:  chainID,
	}
}

// Register 注册特定事件的解析处理器。
// identifier 对于 Solana 是 ProgramID，对于 EVM 是 Topic 签名哈希。
func (r *ParserRegistry) Register(identifier string, handler dexwallet.EventHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[identifier] = handler
	slog.Debug("event handler registered",
		"chain", r.chainID,
		"identifier", identifier,
	)
}

// Parse 解析链上原始交易数据为通用事件。
// 根据链类型，分别走 Solana 指令解析或 EVM 事件日志解析路径。
// 未注册的标识符会被静默跳过（不报错），因为链上交易中大部分事件与我们无关。
func (r *ParserRegistry) Parse(ctx context.Context, rawTx []byte) ([]dexwallet.ChainEvent, error) {
	var tx MockRawTransaction
	if err := json.Unmarshal(rawTx, &tx); err != nil {
		return nil, fmt.Errorf("unmarshal raw transaction: %w", err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var events []dexwallet.ChainEvent
	txTime := time.Unix(tx.Timestamp, 0)

	// 根据链类型选择解析路径
	chainCfg, err := coinset.Global().Get(coinset.ChainID(tx.ChainID))
	if err != nil {
		return nil, fmt.Errorf("unknown chain: %s: %w", tx.ChainID, err)
	}

	if chainCfg.IsSolana() {
		// Solana 路径：按 ProgramID 匹配指令
		for _, inst := range tx.Instructions {
			handler, ok := r.handlers[inst.ProgramID]
			if !ok {
				slog.Debug("unknown program, skipped",
					"chain", tx.ChainID,
					"program_id", inst.ProgramID,
					"tx", tx.TxHash,
				)
				continue
			}

			event, err := handler(ctx, inst.Data)
			if err != nil {
				slog.Warn("solana instruction parse failed",
					"chain", tx.ChainID,
					"program_id", inst.ProgramID,
					"tx", tx.TxHash,
					"error", err,
				)
				continue
			}
			if event != nil {
				// 填充交易级别的公共字段
				event.ChainID = coinset.ChainID(tx.ChainID)
				event.TxHash = tx.TxHash
				event.Block = tx.Block
				event.Timestamp = txTime
				events = append(events, *event)
			}
		}
	} else if chainCfg.IsEVM() {
		// EVM 路径：按 Topics[0]（事件签名哈希）匹配日志
		for _, log := range tx.Logs {
			if len(log.Topics) == 0 {
				continue
			}
			topic0 := log.Topics[0]
			handler, ok := r.handlers[topic0]
			if !ok {
				slog.Debug("unknown event topic, skipped",
					"chain", tx.ChainID,
					"topic", topic0,
					"address", log.Address,
					"tx", tx.TxHash,
				)
				continue
			}

			event, err := handler(ctx, log.Data)
			if err != nil {
				slog.Warn("evm event log parse failed",
					"chain", tx.ChainID,
					"topic", topic0,
					"address", log.Address,
					"tx", tx.TxHash,
					"error", err,
				)
				continue
			}
			if event != nil {
				event.ChainID = coinset.ChainID(tx.ChainID)
				event.TxHash = tx.TxHash
				event.Block = tx.Block
				event.Timestamp = txTime
				// EVM 事件日志的 Pool 通常就是合约地址
				if event.Pool == "" {
					event.Pool = log.Address
				}
				events = append(events, *event)
			}
		}
	}

	return events, nil
}

// HandlerCount 返回已注册的解析器数量（用于测试和监控）。
func (r *ParserRegistry) HandlerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handlers)
}
