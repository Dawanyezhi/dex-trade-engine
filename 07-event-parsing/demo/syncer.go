// BlockSyncer 区块同步器
// [dexwallet 通用层] -- 同步主循环和重组检测是通用的，区块获取和事件解析是链特定的
//
// 实现 dexwallet.Syncer 接口，提供：
// - 轮询区块的主循环
// - 基于 parentHash 的重组检测
// - 事件解析后的池子更新回调
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// BlockProducer 模拟区块产生接口。
// 生产中对应 RPC 客户端的 getBlock / eth_getBlockByNumber 调用。
type BlockProducer interface {
	// NextBlock 获取下一个区块。
	// 返回 nil 表示当前没有新区块（已追上链上最新高度）。
	NextBlock() (*MockBlock, error)
}

// MockBlock 模拟区块数据。
type MockBlock struct {
	Height       uint64               `json:"height"`
	ParentHash   string               `json:"parent_hash"`
	Hash         string               `json:"hash"`
	Transactions []MockRawTransaction `json:"transactions"`
}

// BlockSyncer 区块同步器。
// 实现 dexwallet.Syncer 接口，逐区块同步并解析事件。
type BlockSyncer struct {
	mu            sync.RWMutex
	parser        *ParserRegistry
	currentHeight uint64
	blockProducer BlockProducer
	poolUpdater   func(event *dexwallet.ChainEvent) // 池子更新回调

	// 区块哈希链表，用于重组检测
	// key=区块高度, value=区块哈希
	blockHashes map[uint64]string

	// 统计信息
	totalEvents   int // 总解析事件数
	reorgCount    int // 重组次数
	blocksHandled int // 已处理区块数
}

// NewBlockSyncer 创建区块同步器。
func NewBlockSyncer(
	parser *ParserRegistry,
	producer BlockProducer,
	poolUpdater func(event *dexwallet.ChainEvent),
) *BlockSyncer {
	return &BlockSyncer{
		parser:        parser,
		blockProducer: producer,
		poolUpdater:   poolUpdater,
		blockHashes:   make(map[uint64]string),
	}
}

// Run 启动同步主循环。
// 流程：轮询新区块 -> 检查重组 -> 解析事件 -> 更新池子 -> 推进高度。
// 通过 context 取消信号实现优雅退出。
func (s *BlockSyncer) Run(ctx context.Context) error {
	slog.Info("syncer started", "start_height", s.currentHeight)

	for {
		select {
		case <-ctx.Done():
			slog.Info("syncer stopped",
				"final_height", s.GetCurrentHeight(),
				"total_events", s.totalEvents,
				"reorg_count", s.reorgCount,
				"blocks_handled", s.blocksHandled,
			)
			return ctx.Err()
		default:
		}

		// 获取下一个区块
		block, err := s.blockProducer.NextBlock()
		if err != nil {
			slog.Warn("failed to get next block, waiting...", "error", err)
			continue
		}
		if block == nil {
			// 没有新区块，已追上最新高度
			return nil
		}

		// 检测重组
		if s.detectReorg(block) {
			slog.Warn("reorg detected",
				"block_height", block.Height,
				"block_hash", block.Hash,
				"parent_hash", block.ParentHash,
			)
			s.rollback(block)
			continue
		}

		// 解析区块中的所有交易
		events, parseErrors := s.parseBlock(ctx, block)

		if parseErrors > 0 {
			slog.Warn("some transactions failed to parse",
				"block", block.Height,
				"parse_errors", parseErrors,
			)
		}

		// 通过回调分发事件，并统计事件数
		s.mu.Lock()
		s.totalEvents += len(events)
		s.mu.Unlock()

		for i := range events {
			if s.poolUpdater != nil {
				s.poolUpdater(&events[i])
			}
		}

		// 推进高度
		s.advanceHeight(block)

		slog.Info("block processed",
			"height", block.Height,
			"hash", block.Hash,
			"tx_count", len(block.Transactions),
			"event_count", len(events),
		)
	}
}

// GetCurrentHeight 获取当前同步高度。
func (s *BlockSyncer) GetCurrentHeight() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentHeight
}

// GetStats 获取统计信息。
func (s *BlockSyncer) GetStats() (totalEvents, reorgCount, blocksHandled int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalEvents, s.reorgCount, s.blocksHandled
}

// detectReorg 检测区块重组。
// 通过比较新区块的 ParentHash 与已记录的上一个区块 Hash 来判断。
// 如果不匹配，说明发生了重组（链上出现了分叉）。
func (s *BlockSyncer) detectReorg(block *MockBlock) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 第一个区块，无需检测
	if s.currentHeight == 0 && len(s.blockHashes) == 0 {
		return false
	}

	// 高度倒退，可能是重组
	if block.Height <= s.currentHeight {
		return true
	}

	// 检查 parentHash 是否匹配
	expectedHash, ok := s.blockHashes[block.Height-1]
	if !ok {
		// 没有记录前一个区块的哈希（可能是首次同步跳过了中间区块）
		return false
	}

	return block.ParentHash != expectedHash
}

// rollback 回滚到重组之前的状态。
// 找到分叉点，删除分叉点之后的所有区块记录，将高度重置到分叉点。
func (s *BlockSyncer) rollback(newBlock *MockBlock) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 从当前高度向前回溯，找到分叉点
	rollbackTo := s.currentHeight
	for rollbackTo > 0 {
		hash, ok := s.blockHashes[rollbackTo]
		if !ok {
			break
		}
		// 如果新区块声称的 parentHash 与某个历史区块匹配，那就是分叉点
		if newBlock.ParentHash == hash && newBlock.Height == rollbackTo+1 {
			break
		}
		// 删除已记录的区块哈希
		delete(s.blockHashes, rollbackTo)
		rollbackTo--
	}

	oldHeight := s.currentHeight
	s.currentHeight = rollbackTo
	s.reorgCount++

	slog.Warn("rollback completed",
		"from_height", oldHeight,
		"to_height", rollbackTo,
		"reorg_depth", oldHeight-rollbackTo,
		"total_reorgs", s.reorgCount,
	)
}

// parseBlock 解析区块中的所有交易。
// 返回解析出的事件列表和解析失败的交易数。
func (s *BlockSyncer) parseBlock(ctx context.Context, block *MockBlock) ([]dexwallet.ChainEvent, int) {
	var allEvents []dexwallet.ChainEvent
	parseErrors := 0

	for i := range block.Transactions {
		rawTx, err := json.Marshal(&block.Transactions[i])
		if err != nil {
			slog.Warn("failed to marshal transaction",
				"block", block.Height,
				"tx", block.Transactions[i].TxHash,
				"error", err,
			)
			parseErrors++
			continue
		}

		events, err := s.parser.Parse(ctx, rawTx)
		if err != nil {
			slog.Warn("failed to parse transaction",
				"block", block.Height,
				"tx", block.Transactions[i].TxHash,
				"error", err,
			)
			parseErrors++
			continue
		}

		allEvents = append(allEvents, events...)
	}

	return allEvents, parseErrors
}

// advanceHeight 推进同步高度并记录区块哈希。
func (s *BlockSyncer) advanceHeight(block *MockBlock) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentHeight = block.Height
	s.blockHashes[block.Height] = block.Hash
	s.blocksHandled++

	// 清理旧的区块哈希记录（只保留最近 100 个区块）
	// 防止内存无限增长
	const maxKeep = 100
	if block.Height > maxKeep {
		delete(s.blockHashes, block.Height-maxKeep)
	}
}

// 编译时检查：确保 BlockSyncer 实现了 dexwallet.Syncer 接口
var _ dexwallet.Syncer = (*BlockSyncer)(nil)

// 编译时检查：确保 ParserRegistry 实现了 dexwallet.EventParser 接口
var _ dexwallet.EventParser = (*ParserRegistry)(nil)
