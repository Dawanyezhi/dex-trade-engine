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
	"math/big"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// MockRawTransaction 模拟链上原始交易数据。
// 同时包含 Solana 风格的 Instructions 和 EVM 风格的 Logs，
// 实际使用中根据 ChainID 决定解析哪个字段。
//
// 生产中对标：
//   - Solana: getTransaction RPC 返回的完整 JSON（含 meta.innerInstructions, meta.preTokenBalances 等）
//   - EVM: eth_getTransactionReceipt 返回的 receipt（含 logs, status, gasUsed 等）
type MockRawTransaction struct {
	TxHash       string            `json:"tx_hash"`
	Block        uint64            `json:"block"`
	Timestamp    int64             `json:"timestamp"`
	ChainID      string            `json:"chain_id"`
	Instructions []MockInstruction `json:"instructions"` // Solana: 顶层指令
	Logs         []MockEventLog    `json:"logs"`          // EVM: 事件日志

	// === Solana 特有字段 ===
	// InnerInstructions 内部指令（CPI 调用链）。
	// 生产中对应 meta.innerInstructions[]，每个元素关联一个顶层指令。
	// 70%+ 的 Solana 交易通过聚合器（如 Jupiter），所有 DEX swap 都在 inner 里。
	InnerInstructions []MockInnerInstruction `json:"inner_instructions,omitempty"`

	// PreTokenBalances / PostTokenBalances 代币余额快照。
	// 生产中对应 meta.preTokenBalances / meta.postTokenBalances。
	// 交叉验证：AmountOut = PostBalance - PreBalance（确保解析金额准确）。
	PreTokenBalances  []MockTokenBalance `json:"pre_token_balances,omitempty"`
	PostTokenBalances []MockTokenBalance `json:"post_token_balances,omitempty"`

	// === 交易元信息（Solana + EVM 通用）===
	Sender  string `json:"sender,omitempty"`  // 发起者地址
	Fee     string `json:"fee,omitempty"`     // 手续费（lamports/wei 字符串）
	Success *bool  `json:"success,omitempty"` // nil=未知, true=成功, false=失败

	// === EVM 特有字段 ===
	GasUsed  string `json:"gas_used,omitempty"`  // 实际 Gas 消耗
	GasPrice string `json:"gas_price,omitempty"` // Gas 单价
}

// MockInstruction 模拟 Solana 指令。
// 生产中 Data 是 Borsh 编码的字节，这里用 JSON 模拟。
//
// 关键概念：Instruction Discriminator（指令判别器）
//   - Anchor 框架: 前 8 字节是 sha256("global:<method_name>")[:8]
//   - 原生程序: 前 1-4 字节是指令类型枚举值
//   - 同一个 ProgramID 可能有 swap/addLiquidity/removeLiquidity 等多种操作，
//     需要通过 Discriminator 区分，不能只靠 ProgramID 粒度匹配。
type MockInstruction struct {
	ProgramID     string `json:"program_id"`
	Data          []byte `json:"data"`
	Discriminator string `json:"discriminator,omitempty"` // 指令判别器（简化为字符串标识）
}

// MockInnerInstruction 模拟 Solana 内部指令（CPI 调用）。
// 生产中对应 meta.innerInstructions[i].instructions[]。
//
// CPI（Cross-Program Invocation）是 Solana 的核心机制：
//   - 一个 Program 可以调用另一个 Program（如 Jupiter → Raydium → Token Program）
//   - 内部指令记录了完整的 CPI 调用链
//   - 关联到顶层指令的索引（InstructionIndex），表示"这些内部指令是由第 N 条顶层指令触发的"
type MockInnerInstruction struct {
	InstructionIndex int              `json:"instruction_index"` // 关联的顶层指令索引（从 0 开始）
	Instructions     []MockInstruction `json:"instructions"`      // 此顶层指令触发的所有 CPI 调用
}

// MockTokenBalance 模拟代币余额快照。
// 生产中对应 meta.preTokenBalances[] / meta.postTokenBalances[]。
//
// 用途：
//   - 计算 balance diff = postBalance - preBalance 得到实际金额变化
//   - 与指令解析的金额交叉验证（如果不一致，说明解析可能有误）
//   - 在 CPI 场景下，指令数据中的金额可能是中间值，balance diff 才是最终结果
type MockTokenBalance struct {
	AccountIndex int    `json:"account_index"` // 账户在交易 accountKeys 中的索引
	Owner        string `json:"owner"`         // Token Account 的 owner（即实际拥有者）
	Mint         string `json:"mint"`          // 代币 Mint 地址
	Amount       string `json:"amount"`        // 余额（字符串大整数）
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
//
// 关键改进（相比简化版）：
//  1. 失败交易处理：先检查 tx.Success，失败交易仍解析但标记 Success=false
//  2. Solana innerInstructions：展平顶层+内部指令后统一解析，覆盖聚合器场景
//  3. 交易元信息填充：Sender、Fee、ProgramID 等追踪信息
func (r *ParserRegistry) Parse(ctx context.Context, rawTx []byte) ([]dexwallet.ChainEvent, error) {
	var tx MockRawTransaction
	if err := json.Unmarshal(rawTx, &tx); err != nil {
		return nil, fmt.Errorf("unmarshal raw transaction: %w", err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	// 步骤 0: 判断交易是否成功
	// 生产中：Solana 检查 meta.err == nil，EVM 检查 receipt.status == 1
	txSuccess := true
	if tx.Success != nil {
		txSuccess = *tx.Success
	}
	if !txSuccess {
		slog.Debug("failed transaction, parsing for record only",
			"tx", tx.TxHash,
			"chain", tx.ChainID,
		)
		// 失败交易也要解析（用于状态追踪），但所有事件标记 Success=false
	}

	var events []dexwallet.ChainEvent
	txTime := time.Unix(tx.Timestamp, 0)

	// 解析手续费
	var fee *big.Int
	if tx.Fee != "" {
		fee, _ = new(big.Int).SetString(tx.Fee, 10)
	}

	// 根据链类型选择解析路径
	chainCfg, err := coinset.Global().Get(coinset.ChainID(tx.ChainID))
	if err != nil {
		return nil, fmt.Errorf("unknown chain: %s: %w", tx.ChainID, err)
	}

	if chainCfg.IsSolana() {
		// Solana 路径：展平顶层+内部指令后统一解析
		//
		// 为什么需要展平？
		// Jupiter 聚合器交易只有 1 条顶层指令（Jupiter Program），
		// 所有 DEX swap（Raydium/Orca/PumpFun）都在 innerInstructions 里。
		// 如果只解析顶层，70%+ 的交易解析不出来。
		flatInsts := FlattenInstructions(tx.Instructions, tx.InnerInstructions)

		for _, fi := range flatInsts {
			// 优先用 discriminator 拼接的 key 匹配（更精确）
			// 例如："675kPX...#swap" 而不是 "675kPX..."
			// 这样同一 Program 的 swap/addLiquidity 可以用不同 handler
			var handler dexwallet.EventHandler
			var ok bool

			if fi.Discriminator != "" {
				compositeKey := fi.ProgramID + "#" + fi.Discriminator
				handler, ok = r.handlers[compositeKey]
			}
			if !ok {
				// 降级到 ProgramID 粒度匹配
				handler, ok = r.handlers[fi.ProgramID]
			}
			if !ok {
				slog.Debug("unknown program, skipped",
					"chain", tx.ChainID,
					"program_id", fi.ProgramID,
					"is_inner", fi.IsInner,
					"tx", tx.TxHash,
				)
				continue
			}

			event, err := handler(ctx, fi.Data)
			if err != nil {
				slog.Warn("solana instruction parse failed",
					"chain", tx.ChainID,
					"program_id", fi.ProgramID,
					"is_inner", fi.IsInner,
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
				event.Sender = tx.Sender
				event.Fee = fee
				event.Success = txSuccess

				// CPI 追踪信息
				event.ProgramID = fi.ProgramID
				event.InstructionIndex = fi.Index
				event.IsInnerInst = fi.IsInner
				event.ParentProgramID = fi.ParentProgramID

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
				event.Sender = tx.Sender
				event.Fee = fee
				event.Success = txSuccess

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
