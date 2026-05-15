// EVM 地址级 Nonce 管理器。
// 对标 evmwallet nonce_manager.go 的设计思路。
//
// ---- 为什么需要 Nonce 管理 ----
//
// EVM 链上每个地址（EOA）的交易必须按 nonce 严格递增：
//   - 第一笔交易 nonce=0，第二笔 nonce=1，第三笔 nonce=2 ...
//   - 节点只会打包 nonce == 当前已确认 nonce 的交易
//   - 如果 nonce 不连续（如跳过了 5 直接发 6），交易会卡在 mempool 中等待 nonce=5 的交易
//
// nonce 的核心作用：
//   1. 防重放攻击：每个 nonce 只能使用一次，防止交易被重复执行
//   2. 保证交易有序：同一地址的交易按 nonce 严格排序执行
//   3. 支持 RBF 替换：用相同 nonce + 更高 Gas 的新交易替换旧交易
//
// ---- 并发问题 ----
//
// 如果不做 nonce 管理，多个 goroutine 同时为同一个地址发送交易：
//
//   goroutine A: getTransactionCount("0xABC") → nonce=5
//   goroutine B: getTransactionCount("0xABC") → nonce=5  (同一瞬间查询)
//   goroutine A: 发送 tx(nonce=5, ...)  → 成功
//   goroutine B: 发送 tx(nonce=5, ...)  → 失败! nonce 已被使用
//
// 更糟糕的是，如果 B 的交易 Gas 更高，可能反而替换掉 A 的交易（意外 RBF）。
//
// ---- 为什么不能每次都查链上 ----
//
//   1. 网络延迟：每次 RPC 调用需要几十到几百毫秒，高频场景不可接受
//   2. pending 交易不可见：eth_getTransactionCount("latest") 只返回已确认的 nonce，
//      不包含 mempool 中的 pending 交易。如果刚发了 nonce=5 但未确认，
//      查询仍然返回 5，导致重复使用
//   3. RPC 限流：频繁查询可能触发节点的速率限制
//
// 解决方案：per-address mutex + 本地 nonce 追踪
//   - 每个地址独立加锁，不同地址之间不互斥
//   - 首次使用时从链上同步 nonce，之后在本地递增
//   - 当检测到 nonce gap 或错误时，强制重新同步
//
// ---- Nonce gap 的处理 ----
//
// 如果 nonce=5 的交易因为某种原因失败（Gas 不足、合约 revert 等），
// 那么 nonce=6、7、8... 的交易都会卡住，因为节点要求 nonce 严格递增。
//
// 处理策略：
//   1. 检测：监控 pending 交易，如果低 nonce 交易长时间未确认，可能存在 gap
//   2. 填补：重新发送 nonce=5 的交易（可以是空交易：to=自己，value=0）
//   3. 重置：调用 ResetNonce 强制从链上重新同步 nonce
//
// ---- 与 Solana 的对比 ----
//
// Solana 不需要 nonce 管理，因为它使用完全不同的防重放机制：
//   - Solana 交易包含 recent blockhash（最近的区块哈希）
//   - 每个 blockhash 约 60 秒后过期，过期交易自动作废
//   - 同一笔交易（相同签名）不会被执行两次
//   - 多笔交易可以同时发送，不需要排序，彼此独立
//
// 这意味着 Solana 可以真正并行发送交易，而 EVM 需要串行管理 nonce。
// 这也是为什么 Solana 钱包不需要 NonceManager，而 EVM 钱包必须有。
package main

import (
	"fmt"
	"log/slog"
	"sync"
)

// ---------------------------------------------------------------------------
// NonceManager 结构
// ---------------------------------------------------------------------------

// addressNonce 单个地址的 nonce 状态。
// 每个地址有独立的互斥锁，不同地址之间的操作互不阻塞。
type addressNonce struct {
	mu      sync.Mutex // 地址级互斥锁（保证同一地址的 nonce 分配是串行的）
	current uint64     // 本地追踪的当前 nonce（下一笔交易应使用的值）
	synced  bool       // 是否已从链上同步过（false 表示还需要首次同步）
}

// NonceManager EVM 地址级 Nonce 管理器。
//
// 核心设计：
//   - 使用两层锁：外层 mu 保护 locks map 的并发访问，
//     内层 addressNonce.mu 保护单个地址的 nonce 分配
//   - 首次为某地址分配 nonce 时，通过 RPC 查询链上 nonce 并缓存
//   - 后续分配直接在本地递增，避免频繁 RPC 调用
//   - 返回释放函数（release），调用方在交易发送完成后释放地址锁
//
// 使用模式：
//
//	nonce, release, err := nm.AcquireNonce("0xABC...")
//	if err != nil { ... }
//	defer release()
//	// 使用 nonce 构建和发送交易
type NonceManager struct {
	mu    sync.Mutex
	locks map[string]*addressNonce // address → nonce 状态

	// 模拟 RPC 查询（生产中是 eth_getTransactionCount 调用）
	// 返回指定地址的已确认交易数（即下一笔交易应使用的 nonce）
	getTransactionCount func(address string) (uint64, error)
}

// NewNonceManager 创建 Nonce 管理器。
//
// getTransactionCount 是链上 nonce 查询函数，对应 EVM 的 eth_getTransactionCount(address, "latest")。
// 生产中由 RPC 客户端提供具体实现。
func NewNonceManager(getTransactionCount func(address string) (uint64, error)) *NonceManager {
	return &NonceManager{
		locks:               make(map[string]*addressNonce),
		getTransactionCount: getTransactionCount,
	}
}

// getOrCreateAddressNonce 获取或创建地址的 nonce 状态。
// 调用方必须持有 nm.mu 锁。
func (nm *NonceManager) getOrCreateAddressNonce(address string) *addressNonce {
	an, exists := nm.locks[address]
	if !exists {
		an = &addressNonce{}
		nm.locks[address] = an
	}
	return an
}

// AcquireNonce 获取下一个可用的 nonce。
//
// 返回值：
//   - nonce: 下一笔交易应使用的 nonce 值
//   - release: 释放函数，调用方在交易发送完成后必须调用（通常 defer release()）
//   - err: 错误（首次同步 RPC 失败等）
//
// 工作流程：
//  1. 获取地址级锁（保证同一地址串行分配）
//  2. 如果是首次使用（synced=false），从链上查询当前 nonce
//  3. 分配当前 nonce 值，然后本地递增
//  4. 返回 nonce 和释放函数
//
// 注意：release 函数释放的是地址级锁。在释放之前，同一地址的其他
// AcquireNonce 调用会阻塞等待。这保证了 nonce 的严格递增分配。
func (nm *NonceManager) AcquireNonce(address string) (uint64, func(), error) {
	// 获取地址的 nonce 状态（需要短暂持有外层锁）
	nm.mu.Lock()
	an := nm.getOrCreateAddressNonce(address)
	nm.mu.Unlock()

	// 获取地址级锁
	an.mu.Lock()

	// 首次使用：从链上同步 nonce
	if !an.synced {
		onChainNonce, err := nm.getTransactionCount(address)
		if err != nil {
			an.mu.Unlock()
			return 0, func() {}, fmt.Errorf("nonce manager: 查询链上 nonce 失败 (address=%s): %w", address, err)
		}
		an.current = onChainNonce
		an.synced = true

		slog.Info("Nonce 已从链上同步",
			"address", address,
			"nonce", onChainNonce,
		)
	}

	// 分配 nonce 并递增
	nonce := an.current
	an.current++

	slog.Debug("Nonce 已分配",
		"address", address,
		"nonce", nonce,
		"next", an.current,
	)

	// 返回释放函数（释放地址级锁）
	release := func() {
		an.mu.Unlock()
	}

	return nonce, release, nil
}

// ResetNonce 强制重新同步某个地址的 nonce。
//
// 使用场景：
//   - 检测到 nonce gap（某笔交易失败导致后续交易卡住）
//   - 手动修复后需要重新对齐本地状态和链上状态
//   - 长时间未使用某地址，需要确保 nonce 最新
//
// 注意：此方法会获取地址级锁，如果有正在进行的 AcquireNonce 调用，
// 会等待其完成后再执行重置。
func (nm *NonceManager) ResetNonce(address string) {
	nm.mu.Lock()
	an, exists := nm.locks[address]
	nm.mu.Unlock()

	if !exists {
		// 地址不存在，无需重置
		return
	}

	// 获取地址级锁，确保没有正在进行的 nonce 分配
	an.mu.Lock()
	an.synced = false // 标记为未同步，下次 AcquireNonce 时会重新查询链上
	an.mu.Unlock()

	slog.Info("Nonce 已重置，下次使用时将重新同步",
		"address", address,
	)
}

// PeekNonce 查看某个地址的当前 nonce（不加锁，不递增）。
//
// 注意：这是一个"快照"值，可能在读取后立即被其他 goroutine 修改。
// 仅用于监控和调试，不应用于构建交易。
//
// 如果地址尚未同步过 nonce，会从链上查询（但不缓存，不影响内部状态）。
func (nm *NonceManager) PeekNonce(address string) (uint64, error) {
	nm.mu.Lock()
	an, exists := nm.locks[address]
	nm.mu.Unlock()

	if !exists || !an.synced {
		// 地址不存在或未同步 → 直接查链上
		nonce, err := nm.getTransactionCount(address)
		if err != nil {
			return 0, fmt.Errorf("nonce manager: 查询链上 nonce 失败 (address=%s): %w", address, err)
		}
		return nonce, nil
	}

	// 已有缓存，直接返回（注意：不加锁，可能读到正在被修改的值）
	return an.current, nil
}
