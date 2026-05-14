// Package main 实现多节点故障转移的 StableClient。
// [dexwallet 通用层] -- StableClient 不关心底层是 Solana 还是 EVM，
// 只通过 RPCClient 接口与具体实现交互。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// 哨兵错误，调用方可以用 errors.Is 判断。
var (
	ErrAllNodesFailed = errors.New("all rpc nodes are unavailable")
	ErrNoNodes        = errors.New("no rpc nodes configured")
)

// NodeEntry 记录单个 RPC 节点的状态。
type NodeEntry struct {
	client   dexwallet.RPCClient // 底层 RPC 客户端
	endpoint string              // 节点端点标识（用于日志）
	healthy  bool                // 当前健康状态
}

// StableClient 多节点故障转移的 RPC 客户端。
// 实现 dexwallet.RPCClient 接口，内部维护多个节点并自动切换。
type StableClient struct {
	mu              sync.RWMutex
	nodes           []*NodeEntry
	activeIdx       int
	chainID         coinset.ChainID
	healthInterval  time.Duration
	perNodeTimeout  time.Duration
	onAllNodesFailed func() // 所有节点不可用时的回调（如告警）
}

// StableClientOption 配置选项函数。
type StableClientOption func(*StableClient)

// WithHealthInterval 设置健康检查间隔。
func WithHealthInterval(d time.Duration) StableClientOption {
	return func(s *StableClient) {
		s.healthInterval = d
	}
}

// WithPerNodeTimeout 设置每个节点的单次调用超时。
func WithPerNodeTimeout(d time.Duration) StableClientOption {
	return func(s *StableClient) {
		s.perNodeTimeout = d
	}
}

// WithOnAllNodesFailed 设置所有节点不可用时的回调。
func WithOnAllNodesFailed(fn func()) StableClientOption {
	return func(s *StableClient) {
		s.onAllNodesFailed = fn
	}
}

// NewStableClient 创建 StableClient。
// clients 是按优先级排序的 RPC 客户端列表（索引 0 优先级最高）。
func NewStableClient(chainID coinset.ChainID, clients []dexwallet.RPCClient, opts ...StableClientOption) (*StableClient, error) {
	if len(clients) == 0 {
		return nil, ErrNoNodes
	}

	nodes := make([]*NodeEntry, len(clients))
	for i, c := range clients {
		nodes[i] = &NodeEntry{
			client:   c,
			endpoint: fmt.Sprintf("node-%d(%s)", i, chainID),
			healthy:  true, // 初始假定所有节点健康
		}
	}

	sc := &StableClient{
		nodes:          nodes,
		activeIdx:      0,
		chainID:        chainID,
		healthInterval: 10 * time.Second,  // 默认 10 秒检查一次
		perNodeTimeout: 5 * time.Second,   // 默认单节点超时 5 秒
	}

	for _, opt := range opts {
		opt(sc)
	}

	return sc, nil
}

// SetNodeEndpoint 设置节点端点名称（用于日志）。
func (s *StableClient) SetNodeEndpoint(idx int, endpoint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx >= 0 && idx < len(s.nodes) {
		s.nodes[idx].endpoint = endpoint
	}
}

// StartHealthCheck 启动后台健康检查 goroutine。
// 通过 ctx 控制退出。
func (s *StableClient) StartHealthCheck(ctx context.Context) {
	ticker := time.NewTicker(s.healthInterval)
	defer ticker.Stop()

	slog.Info("健康检查已启动",
		"chain", s.chainID,
		"interval", s.healthInterval,
		"node_count", len(s.nodes),
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("健康检查已停止", "chain", s.chainID)
			return
		case <-ticker.C:
			s.checkAllNodes(ctx)
		}
	}
}

// checkAllNodes 检查所有节点的健康状态。
func (s *StableClient) checkAllNodes(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	allUnhealthy := true
	for _, node := range s.nodes {
		// 使用独立的超时 context 检查每个节点
		checkCtx, cancel := context.WithTimeout(ctx, s.perNodeTimeout)
		healthy := node.client.IsHealthy(checkCtx)
		cancel()

		oldHealthy := node.healthy
		node.healthy = healthy

		if healthy {
			allUnhealthy = false
		}

		// 状态变化时记录日志
		if oldHealthy != healthy {
			if healthy {
				slog.Info("节点恢复健康",
					"chain", s.chainID,
					"node", node.endpoint,
				)
			} else {
				slog.Warn("节点变为不健康",
					"chain", s.chainID,
					"node", node.endpoint,
				)
			}
		}
	}

	if allUnhealthy {
		slog.Error("所有节点不可用",
			"chain", s.chainID,
			"node_count", len(s.nodes),
		)
		if s.onAllNodesFailed != nil {
			s.onAllNodesFailed()
		}
	}
}

// getActiveNode 获取当前活跃节点（读锁）。
func (s *StableClient) getActiveNode() *NodeEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nodes[s.activeIdx]
}

// switchToNextHealthy 切换到下一个健康节点（写锁）。
// 返回是否切换成功。
func (s *StableClient) switchToNextHealthy(failedIdx int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 标记失败节点为不健康
	if failedIdx >= 0 && failedIdx < len(s.nodes) {
		s.nodes[failedIdx].healthy = false
	}

	// 从当前位置开始遍历，找到下一个健康节点
	for i := 1; i <= len(s.nodes); i++ {
		nextIdx := (failedIdx + i) % len(s.nodes)
		if s.nodes[nextIdx].healthy {
			oldEndpoint := s.nodes[s.activeIdx].endpoint
			s.activeIdx = nextIdx
			slog.Warn("节点切换",
				"chain", s.chainID,
				"from", oldEndpoint,
				"to", s.nodes[nextIdx].endpoint,
			)
			return true
		}
	}

	return false
}

// callWithFailover 带故障转移的通用调用方法。
// fn 是实际的 RPC 调用逻辑，接收一个带超时的 context 和 RPCClient，返回错误。
func (s *StableClient) callWithFailover(ctx context.Context, methodName string, fn func(context.Context, dexwallet.RPCClient) error) error {
	s.mu.RLock()
	nodeCount := len(s.nodes)
	s.mu.RUnlock()

	var lastErr error

	// 最多尝试所有节点
	for attempt := 0; attempt < nodeCount; attempt++ {
		// 检查上层 context 是否已取消
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled during %s failover: %w", methodName, ctx.Err())
		}

		s.mu.RLock()
		currentIdx := s.activeIdx
		node := s.nodes[currentIdx]
		s.mu.RUnlock()

		// 跳过已知不健康的节点（包括第一次尝试）
		if !node.healthy {
			if !s.switchToNextHealthy(currentIdx) {
				// 没有健康节点可用
				break
			}
			continue
		}

		// 使用单节点超时执行调用
		callCtx, callCancel := context.WithTimeout(ctx, s.perNodeTimeout)
		err := fn(callCtx, node.client)
		callCancel()

		if err == nil {
			return nil
		}

		lastErr = err
		slog.Warn("节点调用失败，尝试切换",
			"chain", s.chainID,
			"node", node.endpoint,
			"method", methodName,
			"attempt", attempt+1,
			"error", err,
		)

		// 切换到下一个健康节点
		if !s.switchToNextHealthy(currentIdx) {
			// 没有健康节点可用
			break
		}
	}

	return fmt.Errorf("all %d nodes failed for %s: %w", nodeCount, methodName, lastErr)
}

// --- 实现 dexwallet.RPCClient 接口 ---

// SendTransaction 发送交易，带故障转移。
func (s *StableClient) SendTransaction(ctx context.Context, txData []byte) (string, error) {
	var txHash string
	err := s.callWithFailover(ctx, "SendTransaction", func(nodeCtx context.Context, c dexwallet.RPCClient) error {
		var e error
		txHash, e = c.SendTransaction(nodeCtx, txData)
		return e
	})
	return txHash, err
}

// GetBalance 获取余额，带故障转移。
func (s *StableClient) GetBalance(ctx context.Context, address string) (*big.Int, error) {
	var balance *big.Int
	err := s.callWithFailover(ctx, "GetBalance", func(nodeCtx context.Context, c dexwallet.RPCClient) error {
		var e error
		balance, e = c.GetBalance(nodeCtx, address)
		return e
	})
	return balance, err
}

// GetBlockHeight 获取区块高度，带故障转移。
func (s *StableClient) GetBlockHeight(ctx context.Context) (uint64, error) {
	var height uint64
	err := s.callWithFailover(ctx, "GetBlockHeight", func(nodeCtx context.Context, c dexwallet.RPCClient) error {
		var e error
		height, e = c.GetBlockHeight(nodeCtx)
		return e
	})
	return height, err
}

// IsHealthy 检查是否有任一节点健康。
func (s *StableClient) IsHealthy(ctx context.Context) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, node := range s.nodes {
		if node.healthy {
			return true
		}
	}
	return false
}

// ChainID 返回链标识。
func (s *StableClient) ChainID() coinset.ChainID {
	return s.chainID
}

// NodeCount 返回节点数量（用于测试和监控）。
func (s *StableClient) NodeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodes)
}

// ActiveNodeIndex 返回当前活跃节点索引（用于测试和监控）。
func (s *StableClient) ActiveNodeIndex() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeIdx
}

// 编译期检查：StableClient 必须实现 RPCClient 接口。
var _ dexwallet.RPCClient = (*StableClient)(nil)
