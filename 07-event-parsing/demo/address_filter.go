// 地址过滤器 (AddressFilter)
// [dexwallet 通用层] -- 快速过滤无关交易，减少 99% 的解析开销
//
// 为什么需要地址过滤？
//
// 区块链上每个区块包含成百上千笔交易，但只有极少数与我们的钱包系统相关。
// 以 Solana 为例：
//   - 每个 slot（约 400ms）可能有 1000+ 笔交易
//   - 我们可能只管理 10 万个用户地址
//   - 99%+ 的交易与我们完全无关
//
// 如果对每笔交易都进行完整的事件解析（反序列化 JSON、查询数据库、分类），
// 会浪费大量 CPU 和数据库资源。地址过滤器的作用就是在解析之前，
// 用极低的成本快速判断"这笔交易是否可能涉及我们的地址"。
//
// 生产架构中的位置：
//
//   区块数据 → [地址过滤器] → 可能相关的交易 → [事件解析器] → [交易分类器] → 下游服务
//              ↓ (99% 丢弃)
//          无关交易（跳过）
//
// 对标 irwallet 的 AddressFilter / BloomFilter 模块。
package main

import (
	"sync"
	"sync/atomic"
)

// ==================== Bloom Filter 原理说明 ====================

// BloomFilterNote 说明为什么生产环境使用 Bloom Filter。
//
// 本 demo 使用 map[string]struct{} 实现精确匹配，生产环境应使用 Bloom Filter。
// 以下是两者的对比：
//
// ┌─────────────┬──────────────────────────┬──────────────────────────────┐
// │             │ map（本 demo）            │ Bloom Filter（生产）          │
// ├─────────────┼──────────────────────────┼──────────────────────────────┤
// │ 查找时间    │ O(1) 平均，O(n) 最坏     │ O(k)，k 为哈希函数个数       │
// │ 内存占用    │ 高（存储完整地址字符串）  │ 极低（只需位数组）            │
// │ 误判率      │ 0%（精确匹配）           │ 可控的假阳性率（如 0.1%）     │
// │ 漏判率      │ 0%                       │ 0%（绝对无假阴性）            │
// │ 10万地址    │ ~8MB 内存               │ ~120KB 内存（0.1% 误判率时）  │
// │ 100万地址   │ ~80MB 内存              │ ~1.2MB 内存                   │
// │ 删除支持    │ 支持                     │ 标准 BF 不支持                │
// └─────────────┴──────────────────────────┴──────────────────────────────┘
//
// Bloom Filter 的核心思想：
//
// 1. 插入：将地址通过 k 个不同的哈希函数，映射到一个 m 位的位数组上，
//    将对应的 k 个位置设为 1。
//
//    例如 k=3, m=16:
//    地址 "0xABC..." → hash1=3, hash2=7, hash3=11
//    位数组: 0001000100010000
//
// 2. 查询：同样计算 k 个哈希位置，检查是否全为 1。
//    - 如果有任一位为 0 → "一定不是钱包地址" → 直接跳过（高速否定过滤）
//    - 如果全为 1 → "可能是钱包地址" → 需要进一步查数据库确认
//
// 3. 关键特性：
//    - 假阴性率 = 0%：如果地址在集合中，BF 一定会说"可能在"
//    - 假阳性率 > 0%：BF 说"可能在"的地址，实际可能不在（假阳性）
//    - 对于过滤场景，假阳性是可以接受的（最多多查一次数据库），
//      假阴性是绝对不能接受的（会导致漏处理交易 → 用户充值不到账）
//
// irwallet 的实际实现：
//   1. 启动时从数据库加载所有用户地址，构建 Bloom Filter
//   2. 收到新交易时，先用 BF 快速过滤
//   3. BF 说"一定不是" → 跳过（99% 的交易在此步被过滤）
//   4. BF 说"可能是" → 查 Redis 缓存 → 查 MySQL 确认
//   5. 定期重建 BF（新用户注册后地址需要加入）
//
// 为什么不直接用 Redis？
//   - Redis 查询需要网络往返（~1ms），BF 查询在本地内存完成（~100ns）
//   - 每秒处理 10000 笔交易时，1ms × 10000 = 10 秒的延迟是不可接受的
//   - BF 先过滤掉 99%，只有 1% 需要查 Redis，延迟降低 100 倍
const BloomFilterNote = `
生产环境应使用 Bloom Filter 替代 map，以获得：
- 极低的内存占用（10万地址仅需 ~120KB）
- O(k) 的常数时间查找（k 通常为 3-7）
- 零假阴性保证（不会漏掉任何钱包地址）

本 demo 使用 map 是为了代码简洁和教学目的。
`

// ==================== 地址过滤器实现 ====================

// AddressFilter 地址过滤器。
//
// 维护一个"关注地址集合"，快速判断交易是否涉及我们管理的地址。
// 线程安全：支持并发的 Add 和 Contains 操作。
//
// 内部使用 map[string]struct{} 实现精确匹配。
// 生产中应替换为 Bloom Filter 以降低内存占用（参见 BloomFilterNote）。
type AddressFilter struct {
	mu sync.RWMutex

	// 精确匹配集合（小规模时直接用 map，大规模时用 Bloom Filter）
	addresses map[string]struct{}

	// 统计
	checked  int64 // 总检查次数（使用 atomic 操作保证并发安全）
	filtered int64 // 被过滤（跳过）的次数
}

// NewAddressFilter 创建新的地址过滤器。
func NewAddressFilter() *AddressFilter {
	return &AddressFilter{
		addresses: make(map[string]struct{}),
	}
}

// Add 添加一个关注地址。
//
// 调用时机：
//   - 系统启动时，从数据库批量加载所有用户地址
//   - 新用户注册时，将分配的充值地址加入
//   - 新建内部钱包时（如新增热钱包）
func (f *AddressFilter) Add(address string) {
	if address == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addresses[address] = struct{}{}
}

// AddBatch 批量添加关注地址。
//
// 适用于系统启动时从数据库加载大量地址的场景。
// 一次性获取写锁，避免逐个 Add 时频繁加锁的开销。
func (f *AddressFilter) AddBatch(addresses []string) {
	if len(addresses) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, addr := range addresses {
		if addr != "" {
			f.addresses[addr] = struct{}{}
		}
	}
}

// Contains 检查地址是否在关注集合中。
//
// 时间复杂度：
//   - 本 demo (map): O(1) 平均
//   - 生产 (Bloom Filter): O(k)，k 为哈希函数个数
//
// 返回值语义：
//   - true:  地址在集合中（本 demo）/ 地址可能在集合中（Bloom Filter）
//   - false: 地址不在集合中（两种实现都保证：false 意味着绝对不在）
func (f *AddressFilter) Contains(address string) bool {
	if address == "" {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.addresses[address]
	return ok
}

// ShouldProcess 判断一笔交易是否需要处理。
//
// 遍历交易中所有可能涉及的地址，只要有一个在关注集合中就返回 true。
// 这是过滤链的第一道关卡，在事件解析之前调用。
//
// 检查策略（简化版）：
//   1. 检查交易发起者（tx.Sender）
//   2. 检查 PreTokenBalances / PostTokenBalances 中的 Owner 地址
//      - 这些 Owner 是 Token Account 的实际拥有者
//      - 如果某个 Owner 是我们的用户，说明这笔交易涉及该用户的代币变动
//   3. Solana 特有：还可以检查 Instructions 中的 accounts 列表
//   4. EVM 特有：还可以检查 Logs 中的 indexed 地址参数
//
// 生产优化：
//   - 先检查 Sender（最快，只需一次查找）
//   - 再检查 TokenBalance Owners（Solana）或 Log addresses（EVM）
//   - 使用 Bloom Filter 时，上述每次 Contains 调用只需 ~100ns
func (f *AddressFilter) ShouldProcess(tx *MockRawTransaction) bool {
	atomic.AddInt64(&f.checked, 1)

	// 检查 1: 交易发起者
	// 如果 Sender 是我们的用户，这笔交易一定与我们相关（可能是提现或 Swap）
	if tx.Sender != "" && f.Contains(tx.Sender) {
		return true
	}

	// 检查 2: PreTokenBalances 中的 Owner 地址
	// Solana 交易包含交易前后的代币余额快照，Owner 是 Token Account 的拥有者。
	// 如果某个 Owner 是我们的用户，说明该用户的代币余额发生了变化。
	for _, balance := range tx.PreTokenBalances {
		if f.Contains(balance.Owner) {
			return true
		}
	}

	// 检查 3: PostTokenBalances 中的 Owner 地址
	// 与 PreTokenBalances 类似，但覆盖交易后新创建的 Token Account。
	// 例如：用户首次收到某代币时，PostTokenBalances 中会出现新的 Owner。
	for _, balance := range tx.PostTokenBalances {
		if f.Contains(balance.Owner) {
			return true
		}
	}

	// 检查 4: EVM Logs 中的合约地址
	// EVM 交易的事件日志中，Address 是产生事件的合约地址。
	// 虽然合约地址通常不是用户地址，但某些场景下（如代理合约）可能是。
	// 更重要的是 Topics 中的 indexed 地址参数（如 Transfer 的 from/to），
	// 但简化版暂不解析 Topics。
	for _, log := range tx.Logs {
		if f.Contains(log.Address) {
			return true
		}
	}

	// 没有找到任何关联地址，跳过这笔交易
	atomic.AddInt64(&f.filtered, 1)
	return false
}

// Stats 返回过滤统计信息。
//
// 返回值：
//   - checked: 总共检查了多少笔交易
//   - filtered: 其中有多少笔被过滤掉（与我们无关）
//
// 用途：
//   - 监控过滤效率（filtered/checked 应该 > 99%）
//   - 如果过滤率异常下降，可能是 Bloom Filter 需要重建（地址集过大导致假阳性率上升）
//   - 如果 checked 为 0 但区块在推进，说明过滤器没有被调用（流程配置错误）
func (f *AddressFilter) Stats() (checked, filtered int64) {
	return atomic.LoadInt64(&f.checked), atomic.LoadInt64(&f.filtered)
}

// Size 返回当前关注地址的数量。
//
// 用途：
//   - 监控地址集大小，评估是否需要升级为 Bloom Filter
//   - 当 Size > 10000 时，建议切换到 Bloom Filter 以节省内存
//   - 当 Size > 100000 时，map 的内存占用会显著影响 GC 性能
func (f *AddressFilter) Size() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.addresses)
}
