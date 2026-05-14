package main

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// --- 辅助函数 ---

// newTestSolanaNodes 创建指定数量的 Solana mock 节点。
func newTestSolanaNodes(count int) []*SolanaRPCClient {
	nodes := make([]*SolanaRPCClient, count)
	for i := 0; i < count; i++ {
		nodes[i] = NewSolanaRPCClient("test-solana-node-" + string(rune('A'+i)))
	}
	return nodes
}

// toRPCClients 将 SolanaRPCClient 列表转为 RPCClient 接口列表。
func toRPCClients(nodes []*SolanaRPCClient) []dexwallet.RPCClient {
	clients := make([]dexwallet.RPCClient, len(nodes))
	for i, n := range nodes {
		clients[i] = n
	}
	return clients
}

// newTestStableClient 创建用于测试的 StableClient。
func newTestStableClient(t *testing.T, nodes []*SolanaRPCClient) *StableClient {
	t.Helper()
	sc, err := NewStableClient(
		coinset.ChainSolana,
		toRPCClients(nodes),
		WithHealthInterval(100*time.Millisecond),
		WithPerNodeTimeout(1*time.Second),
	)
	if err != nil {
		t.Fatalf("创建 StableClient 失败: %v", err)
	}
	return sc
}

// --- 测试用例 ---

// TestNewStableClient_NoNodes 测试没有节点时创建失败。
func TestNewStableClient_NoNodes(t *testing.T) {
	_, err := NewStableClient(coinset.ChainSolana, nil)
	if err != ErrNoNodes {
		t.Errorf("期望 ErrNoNodes, 得到 %v", err)
	}

	_, err = NewStableClient(coinset.ChainSolana, []dexwallet.RPCClient{})
	if err != ErrNoNodes {
		t.Errorf("期望 ErrNoNodes, 得到 %v", err)
	}
}

// TestNewStableClient_BasicCreation 测试基本创建。
func TestNewStableClient_BasicCreation(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)

	if sc.NodeCount() != 3 {
		t.Errorf("期望 3 个节点, 得到 %d", sc.NodeCount())
	}

	if sc.ChainID() != coinset.ChainSolana {
		t.Errorf("期望 ChainID=solana, 得到 %s", sc.ChainID())
	}

	if sc.ActiveNodeIndex() != 0 {
		t.Errorf("期望活跃节点索引=0, 得到 %d", sc.ActiveNodeIndex())
	}
}

// TestNormalCall 测试正常调用（无故障）。
func TestNormalCall(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)
	ctx := context.Background()

	// 测试 GetBalance
	balance, err := sc.GetBalance(ctx, "SoLWallet1111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("GetBalance 不应失败: %v", err)
	}
	if balance.Int64() != 5_000_000_000 {
		t.Errorf("期望余额 5000000000, 得到 %s", balance.String())
	}

	// 测试 GetBlockHeight
	height, err := sc.GetBlockHeight(ctx)
	if err != nil {
		t.Fatalf("GetBlockHeight 不应失败: %v", err)
	}
	if height == 0 {
		t.Error("GetBlockHeight 不应返回 0")
	}

	// 测试 SendTransaction
	txHash, err := sc.SendTransaction(ctx, []byte("test-tx"))
	if err != nil {
		t.Fatalf("SendTransaction 不应失败: %v", err)
	}
	if txHash == "" {
		t.Error("SendTransaction 不应返回空哈希")
	}

	// 确认仍在使用第一个节点
	if sc.ActiveNodeIndex() != 0 {
		t.Errorf("正常调用后活跃节点应为 0, 得到 %d", sc.ActiveNodeIndex())
	}
}

// TestFailover_SingleNodeDown 测试单个节点故障时自动切换。
func TestFailover_SingleNodeDown(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)
	ctx := context.Background()

	// 让主节点故障
	nodes[0].SetForceError(true)

	// 调用应该成功（自动切换到 node-1）
	balance, err := sc.GetBalance(ctx, "SoLWallet1111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("故障转移后 GetBalance 不应失败: %v", err)
	}
	if balance.Int64() != 5_000_000_000 {
		t.Errorf("期望余额 5000000000, 得到 %s", balance.String())
	}

	// 活跃节点应该切换到 1
	if sc.ActiveNodeIndex() != 1 {
		t.Errorf("故障转移后活跃节点应为 1, 得到 %d", sc.ActiveNodeIndex())
	}
}

// TestFailover_MultipleNodesDown 测试多个节点故障时跳过不健康节点。
func TestFailover_MultipleNodesDown(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)
	ctx := context.Background()

	// 前两个节点都故障
	nodes[0].SetForceError(true)
	nodes[1].SetForceError(true)

	// 调用应该成功（切换到 node-2）
	_, err := sc.GetBalance(ctx, "SoLWallet1111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("故障转移后 GetBalance 不应失败: %v", err)
	}

	// 活跃节点应该切换到 2
	if sc.ActiveNodeIndex() != 2 {
		t.Errorf("故障转移后活跃节点应为 2, 得到 %d", sc.ActiveNodeIndex())
	}
}

// TestFailover_AllNodesDown 测试所有节点不可用。
func TestFailover_AllNodesDown(t *testing.T) {
	nodes := newTestSolanaNodes(3)

	allFailedCalled := false
	sc, err := NewStableClient(
		coinset.ChainSolana,
		toRPCClients(nodes),
		WithPerNodeTimeout(1*time.Second),
		WithOnAllNodesFailed(func() {
			allFailedCalled = true
		}),
	)
	if err != nil {
		t.Fatalf("创建 StableClient 失败: %v", err)
	}

	// 所有节点故障
	nodes[0].SetForceError(true)
	nodes[1].SetForceError(true)
	nodes[2].SetForceError(true)

	ctx := context.Background()
	_, err = sc.GetBalance(ctx, "SoLWallet1111111111111111111111111111111111")
	if err == nil {
		t.Fatal("所有节点故障时 GetBalance 应返回错误")
	}

	// IsHealthy 应返回 false
	if sc.IsHealthy(ctx) {
		t.Error("所有节点故障时 IsHealthy 应返回 false")
	}

	// 触发健康检查以验证回调
	sc.checkAllNodes(ctx)
	if !allFailedCalled {
		t.Error("所有节点不可用时应触发 onAllNodesFailed 回调")
	}
}

// TestFailover_NodeRecovery 测试节点恢复后重新可用。
func TestFailover_NodeRecovery(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)
	ctx := context.Background()

	// 主节点故障 -> 切换到 node-1
	nodes[0].SetForceError(true)
	_, err := sc.GetBalance(ctx, "SoLWallet1111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("故障转移后不应失败: %v", err)
	}
	if sc.ActiveNodeIndex() != 1 {
		t.Fatalf("期望活跃节点为 1, 得到 %d", sc.ActiveNodeIndex())
	}

	// 主节点恢复
	nodes[0].SetForceError(false)

	// 触发健康检查
	sc.checkAllNodes(ctx)

	// node-0 应该恢复健康（但活跃节点不会自动回退到 0，除非当前节点故障）
	if !sc.IsHealthy(ctx) {
		t.Error("至少有一个节点健康时 IsHealthy 应返回 true")
	}
}

// TestContextCancellation 测试 context 取消时的行为。
func TestContextCancellation(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)

	// 创建已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// 所有调用应该快速失败
	_, err := sc.GetBalance(ctx, "test-address")
	if err == nil {
		t.Error("已取消的 context 应导致调用失败")
	}

	_, err = sc.GetBlockHeight(ctx)
	if err == nil {
		t.Error("已取消的 context 应导致调用失败")
	}

	_, err = sc.SendTransaction(ctx, []byte("test"))
	if err == nil {
		t.Error("已取消的 context 应导致调用失败")
	}
}

// TestContextTimeout 测试超时处理。
func TestContextTimeout(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)

	// 使用极短的超时
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// 等待超时生效
	time.Sleep(1 * time.Millisecond)

	_, err := sc.GetBalance(ctx, "test-address")
	if err == nil {
		t.Error("超时后的调用应失败")
	}
}

// TestConcurrentSafety 测试并发安全。
func TestConcurrentSafety(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)
	ctx := context.Background()

	// 启动健康检查
	healthCtx, healthCancel := context.WithCancel(ctx)
	defer healthCancel()
	go sc.StartHealthCheck(healthCtx)

	// 并发调用所有方法
	const goroutines = 50
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*3)

	for i := 0; i < goroutines; i++ {
		wg.Add(3)

		// 并发 GetBalance
		go func() {
			defer wg.Done()
			_, err := sc.GetBalance(ctx, "SoLWallet1111111111111111111111111111111111")
			if err != nil {
				errCh <- err
			}
		}()

		// 并发 GetBlockHeight
		go func() {
			defer wg.Done()
			_, err := sc.GetBlockHeight(ctx)
			if err != nil {
				errCh <- err
			}
		}()

		// 并发 SendTransaction
		go func() {
			defer wg.Done()
			_, err := sc.SendTransaction(ctx, []byte("concurrent-tx"))
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("并发调用失败: %v", err)
	}
}

// TestConcurrentSafety_WithFailover 测试并发调用中的故障转移。
func TestConcurrentSafety_WithFailover(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)
	ctx := context.Background()

	// 在并发调用过程中制造故障
	const goroutines = 30
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// 第 10 次调用时让主节点故障
			if idx == 10 {
				nodes[0].SetForceError(true)
			}
			_, err := sc.GetBalance(ctx, "SoLWallet1111111111111111111111111111111111")
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// 大部分调用应该成功（通过故障转移）
	if successCount < goroutines/2 {
		t.Errorf("并发故障转移中成功次数过少: %d/%d", successCount, goroutines)
	}
}

// TestHealthCheck 测试健康检查功能。
func TestHealthCheck(t *testing.T) {
	nodes := newTestSolanaNodes(3)
	sc := newTestStableClient(t, nodes)
	ctx := context.Background()

	// 初始所有节点健康
	if !sc.IsHealthy(ctx) {
		t.Error("初始状态应为健康")
	}

	// 让 node-1 不健康
	nodes[1].SetHealthy(false)
	sc.checkAllNodes(ctx)

	// StableClient 仍应报告健康（还有其他节点可用）
	if !sc.IsHealthy(ctx) {
		t.Error("有可用节点时 IsHealthy 应返回 true")
	}

	// 让所有节点不健康
	nodes[0].SetHealthy(false)
	nodes[2].SetHealthy(false)
	sc.checkAllNodes(ctx)

	if sc.IsHealthy(ctx) {
		t.Error("所有节点不健康时 IsHealthy 应返回 false")
	}

	// 恢复一个节点
	nodes[2].SetHealthy(true)
	sc.checkAllNodes(ctx)

	if !sc.IsHealthy(ctx) {
		t.Error("恢复一个节点后 IsHealthy 应返回 true")
	}
}

// TestHealthCheckGoroutine 测试健康检查 goroutine 的启停。
func TestHealthCheckGoroutine(t *testing.T) {
	nodes := newTestSolanaNodes(2)
	sc, err := NewStableClient(
		coinset.ChainSolana,
		toRPCClients(nodes),
		WithHealthInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// 启动健康检查
	done := make(chan struct{})
	go func() {
		sc.StartHealthCheck(ctx)
		close(done)
	}()

	// 让节点变为不健康
	nodes[0].SetHealthy(false)

	// 等待健康检查执行
	time.Sleep(200 * time.Millisecond)

	// 停止健康检查
	cancel()

	// 等待 goroutine 退出
	select {
	case <-done:
		// 正常退出
	case <-time.After(2 * time.Second):
		t.Fatal("健康检查 goroutine 未在 2 秒内退出")
	}
}

// TestEVMRPCClient 测试 EVM RPC 客户端基本功能。
func TestEVMRPCClient(t *testing.T) {
	client := NewEVMRPCClient("test-bsc-node", coinset.ChainBSC)
	ctx := context.Background()

	// 测试 ChainID
	if client.ChainID() != coinset.ChainBSC {
		t.Errorf("期望 ChainID=bsc, 得到 %s", client.ChainID())
	}

	// 测试 IsHealthy
	if !client.IsHealthy(ctx) {
		t.Error("新创建的节点应为健康")
	}

	// 测试 GetBalance
	balance, err := client.GetBalance(ctx, "0x1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("GetBalance 不应失败: %v", err)
	}
	if balance.Sign() <= 0 {
		t.Error("预设地址余额应为正数")
	}

	// 测试未知地址
	balance, err = client.GetBalance(ctx, "0xunknown")
	if err != nil {
		t.Fatalf("未知地址 GetBalance 不应失败: %v", err)
	}
	if balance.Sign() != 0 {
		t.Error("未知地址余额应为 0")
	}

	// 测试 GetBlockHeight
	height, err := client.GetBlockHeight(ctx)
	if err != nil {
		t.Fatalf("GetBlockHeight 不应失败: %v", err)
	}
	if height == 0 {
		t.Error("区块高度不应为 0")
	}

	// 测试 SendTransaction
	txHash, err := client.SendTransaction(ctx, []byte("test-evm-tx"))
	if err != nil {
		t.Fatalf("SendTransaction 不应失败: %v", err)
	}
	if len(txHash) < 3 || txHash[:2] != "0x" {
		t.Errorf("EVM 交易哈希应以 0x 开头, 得到 %s", txHash)
	}

	// 测试强制错误
	client.SetForceError(true)
	_, err = client.GetBalance(ctx, "0x1111111111111111111111111111111111111111")
	if err == nil {
		t.Error("强制错误时 GetBalance 应失败")
	}
	if client.IsHealthy(ctx) {
		t.Error("强制错误时 IsHealthy 应返回 false")
	}
}

// TestSolanaRPCClient 测试 Solana RPC 客户端基本功能。
func TestSolanaRPCClient(t *testing.T) {
	client := NewSolanaRPCClient("test-solana-node")
	ctx := context.Background()

	// 测试 ChainID
	if client.ChainID() != coinset.ChainSolana {
		t.Errorf("期望 ChainID=solana, 得到 %s", client.ChainID())
	}

	// 测试 IsHealthy
	if !client.IsHealthy(ctx) {
		t.Error("新创建的节点应为健康")
	}

	// 测试 GetBalance（预设地址）
	balance, err := client.GetBalance(ctx, "SoLWallet1111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("GetBalance 不应失败: %v", err)
	}
	if balance.Int64() != 5_000_000_000 {
		t.Errorf("期望余额 5000000000, 得到 %s", balance.String())
	}

	// 测试 SetBalance
	client.SetBalance("custom-addr", newBigInt(999))
	balance, err = client.GetBalance(ctx, "custom-addr")
	if err != nil {
		t.Fatalf("GetBalance 不应失败: %v", err)
	}
	if balance.Int64() != 999 {
		t.Errorf("期望余额 999, 得到 %s", balance.String())
	}

	// 测试 SendTransaction
	txHash, err := client.SendTransaction(ctx, []byte("test-sol-tx"))
	if err != nil {
		t.Fatalf("SendTransaction 不应失败: %v", err)
	}
	if txHash == "" {
		t.Error("交易哈希不应为空")
	}

	// 测试不健康状态
	client.SetHealthy(false)
	if client.IsHealthy(ctx) {
		t.Error("设置为不健康后 IsHealthy 应返回 false")
	}
	_, err = client.GetBalance(ctx, "any-addr")
	if err == nil {
		t.Error("不健康节点 GetBalance 应失败")
	}
}

// TestStableClient_WithEVMNodes 测试 StableClient 与 EVM 节点的集成。
func TestStableClient_WithEVMNodes(t *testing.T) {
	node0 := NewEVMRPCClient("evm-node-0", coinset.ChainBSC)
	node1 := NewEVMRPCClient("evm-node-1", coinset.ChainBSC)

	sc, err := NewStableClient(
		coinset.ChainBSC,
		[]dexwallet.RPCClient{node0, node1},
	)
	if err != nil {
		t.Fatalf("创建 StableClient 失败: %v", err)
	}

	ctx := context.Background()

	// 正常调用
	_, err = sc.GetBlockHeight(ctx)
	if err != nil {
		t.Fatalf("GetBlockHeight 不应失败: %v", err)
	}

	// node-0 故障
	node0.SetForceError(true)
	_, err = sc.GetBlockHeight(ctx)
	if err != nil {
		t.Fatalf("故障转移后 GetBlockHeight 不应失败: %v", err)
	}

	if sc.ActiveNodeIndex() != 1 {
		t.Errorf("故障转移后活跃节点应为 1, 得到 %d", sc.ActiveNodeIndex())
	}
}

// newBigInt 辅助函数，创建 big.Int。
func newBigInt(v int64) *big.Int {
	return big.NewInt(v)
}
