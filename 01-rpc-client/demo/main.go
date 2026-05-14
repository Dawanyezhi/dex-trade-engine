// 01-rpc-client 演示程序
// 演示多节点 RPC 客户端的故障转移能力。
//
// 运行: go run ./01-rpc-client/demo/
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

func main() {
	// 设置日志级别为 Debug，方便观察详细信息
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	fmt.Println("========================================")
	fmt.Println("  01-rpc-client: RPC 客户端故障转移演示")
	fmt.Println("========================================")
	fmt.Println()

	// 演示 Solana RPC 客户端
	demoSolana()

	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()

	// 演示 EVM RPC 客户端
	demoEVM()
}

// demoSolana 演示 Solana 多节点 RPC 客户端。
func demoSolana() {
	fmt.Println("[Solana] 创建 3 个 RPC 节点...")
	fmt.Println()

	// 创建 3 个 Solana RPC mock 节点
	node0 := NewSolanaRPCClient("https://mainnet.helius-rpc.com")
	node1 := NewSolanaRPCClient("https://rpc.shyft.to")
	node2 := NewSolanaRPCClient("https://api.mainnet-beta.solana.com")

	// 创建 StableClient
	stableClient, err := NewStableClient(
		coinset.ChainSolana,
		[]dexwallet.RPCClient{node0, node1, node2},
		WithHealthInterval(2*time.Second),
		WithPerNodeTimeout(3*time.Second),
		WithOnAllNodesFailed(func() {
			fmt.Println("  [告警] Solana 所有节点不可用!")
		}),
	)
	if err != nil {
		slog.Error("创建 StableClient 失败", "error", err)
		return
	}

	// 设置可读的端点名称
	stableClient.SetNodeEndpoint(0, "helius(主节点)")
	stableClient.SetNodeEndpoint(1, "shyft(备用)")
	stableClient.SetNodeEndpoint(2, "solana-public(兜底)")

	// 启动健康检查
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go stableClient.StartHealthCheck(ctx)

	// --- 演示 1: 正常调用 ---
	fmt.Println("[Solana] === 演示 1: 正常调用 ===")
	solAddr := "SoLWallet1111111111111111111111111111111111"
	demoNormalCall(stableClient, solAddr)

	// --- 演示 2: 主节点故障，自动切换 ---
	fmt.Println()
	fmt.Println("[Solana] === 演示 2: 主节点故障，自动切换 ===")
	fmt.Println("  模拟: helius 节点宕机...")
	node0.SetForceError(true)

	demoFailoverCall(stableClient, solAddr)

	fmt.Printf("  当前活跃节点索引: %d\n", stableClient.ActiveNodeIndex())

	// --- 演示 3: 主节点恢复 ---
	fmt.Println()
	fmt.Println("[Solana] === 演示 3: 主节点恢复 ===")
	fmt.Println("  模拟: helius 节点恢复...")
	node0.SetForceError(false)

	// 等待健康检查发现恢复
	time.Sleep(3 * time.Second)
	demoNormalCall(stableClient, solAddr)

	// --- 演示 4: 所有节点故障 ---
	fmt.Println()
	fmt.Println("[Solana] === 演示 4: 所有节点故障 ===")
	fmt.Println("  模拟: 所有节点宕机...")
	node0.SetForceError(true)
	node1.SetForceError(true)
	node2.SetForceError(true)

	demoAllNodesFailed(stableClient, solAddr)

	// 恢复
	node0.SetForceError(false)
	node1.SetForceError(false)
	node2.SetForceError(false)
}

// demoEVM 演示 EVM 多节点 RPC 客户端。
func demoEVM() {
	fmt.Println("[BSC] 创建 3 个 RPC 节点...")
	fmt.Println()

	// 创建 3 个 BSC RPC mock 节点
	node0 := NewEVMRPCClient("https://bsc-dataseed.bnbchain.org", coinset.ChainBSC)
	node1 := NewEVMRPCClient("https://bsc-dataseed1.defibit.io", coinset.ChainBSC)
	node2 := NewEVMRPCClient("https://bsc-dataseed2.ninicoin.io", coinset.ChainBSC)

	// 创建 StableClient
	stableClient, err := NewStableClient(
		coinset.ChainBSC,
		[]dexwallet.RPCClient{node0, node1, node2},
		WithHealthInterval(2*time.Second),
		WithPerNodeTimeout(3*time.Second),
		WithOnAllNodesFailed(func() {
			fmt.Println("  [告警] BSC 所有节点不可用!")
		}),
	)
	if err != nil {
		slog.Error("创建 StableClient 失败", "error", err)
		return
	}

	stableClient.SetNodeEndpoint(0, "bnbchain(主节点)")
	stableClient.SetNodeEndpoint(1, "defibit(备用)")
	stableClient.SetNodeEndpoint(2, "ninicoin(兜底)")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go stableClient.StartHealthCheck(ctx)

	evmAddr := "0x1111111111111111111111111111111111111111"

	// --- 演示 1: 正常调用 ---
	fmt.Println("[BSC] === 演示 1: 正常调用 ===")
	demoNormalCall(stableClient, evmAddr)

	// --- 演示 2: 主节点故障，自动切换 ---
	fmt.Println()
	fmt.Println("[BSC] === 演示 2: 主节点故障，自动切换 ===")
	fmt.Println("  模拟: bnbchain 节点宕机...")
	node0.SetForceError(true)

	demoFailoverCall(stableClient, evmAddr)

	fmt.Printf("  当前活跃节点索引: %d\n", stableClient.ActiveNodeIndex())

	// 恢复
	node0.SetForceError(false)
}

// demoNormalCall 演示正常 RPC 调用。
func demoNormalCall(client dexwallet.RPCClient, address string) {
	ctx := context.Background()

	// 查询余额
	balance, err := client.GetBalance(ctx, address)
	if err != nil {
		fmt.Printf("  GetBalance 失败: %v\n", err)
	} else {
		fmt.Printf("  GetBalance(%s) = %s\n", truncateAddr(address), balance.String())
	}

	// 查询区块高度
	height, err := client.GetBlockHeight(ctx)
	if err != nil {
		fmt.Printf("  GetBlockHeight 失败: %v\n", err)
	} else {
		fmt.Printf("  GetBlockHeight() = %d\n", height)
	}

	// 发送交易
	txHash, err := client.SendTransaction(ctx, []byte("mock-transaction-data"))
	if err != nil {
		fmt.Printf("  SendTransaction 失败: %v\n", err)
	} else {
		fmt.Printf("  SendTransaction() = %s\n", truncateHash(txHash))
	}
}

// demoFailoverCall 演示故障转移调用。
func demoFailoverCall(client dexwallet.RPCClient, address string) {
	ctx := context.Background()

	fmt.Println("  尝试调用 GetBalance（应该自动切换到备用节点）...")
	balance, err := client.GetBalance(ctx, address)
	if err != nil {
		fmt.Printf("  GetBalance 失败: %v\n", err)
	} else {
		fmt.Printf("  GetBalance 成功（故障转移后）: %s\n", balance.String())
	}
}

// demoAllNodesFailed 演示所有节点不可用。
func demoAllNodesFailed(client dexwallet.RPCClient, address string) {
	ctx := context.Background()

	fmt.Println("  尝试调用 GetBalance（预期失败）...")
	_, err := client.GetBalance(ctx, address)
	if err != nil {
		fmt.Printf("  GetBalance 预期失败: %v\n", err)
	}
}

// truncateAddr 截断地址显示。
func truncateAddr(addr string) string {
	if len(addr) > 12 {
		return addr[:6] + "..." + addr[len(addr)-4:]
	}
	return addr
}

// truncateHash 截断哈希显示。
func truncateHash(hash string) string {
	if len(hash) > 16 {
		return hash[:10] + "..." + hash[len(hash)-4:]
	}
	return hash
}
