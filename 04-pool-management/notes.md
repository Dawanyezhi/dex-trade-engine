# 04-pool-management: 深度技术笔记

## 1. Pool 数据模型设计 [dexwallet 通用层] [L1]

Pool 是整个 DEX 交易系统中最核心的数据结构。每笔 Swap 的价格计算都依赖 Pool 中的储备量、费率等字段。

```go
type Pool struct {
    Address      string          // 唯一标识
    DexID        DexID           // 归属哪个 DEX
    ChainID      coinset.ChainID // 归属哪条链
    ProtocolType ProtocolType    // 采用什么协议（决定价格公式）
    BaseMint     string          // 基础代币地址
    QuoteMint    string          // 计价代币地址
    Liquidity    *big.Int        // 流动性总量
    FeeRate      uint64          // 手续费率（基点）
    State        PoolState       // 当前状态
    UpdatedAt    time.Time       // 最后更新时间
    Extra        map[string]interface{} // 链特定扩展
}
```

**为什么 Liquidity 用 *big.Int：**

以太坊上的流动性通常用 wei 计量（1 ETH = 10^18 wei）。一个拥有 100 ETH 流动性的池子：

```
100 * 10^18 = 100000000000000000000
uint64 最大值 = 18446744073709551615 (约 1.8 * 10^19)
```

100 ETH 已经接近 uint64 的上限。实际生产中，大型池子（如 Uniswap ETH/USDC）的流动性可以达到数千万美元，远超 uint64 范围。因此必须使用 big.Int。

**为什么 FeeRate 用 uint64 而非 big.Int：**

FeeRate 以基点（Basis Point）表示。1 基点 = 0.01%，最大费率不会超过 10000 基点（100%）。uint64 完全够用，无需引入 big.Int 的开销。

---

## 2. LRU 缓存实现原理 [dexwallet 通用层] [L2]

### 2.1 数据结构

LRUPoolCache 使用经典的「哈希表 + 双向链表」实现：

```
哈希表 items: map[address] -> *list.Element
                                    |
                                    v
双向链表 eviction: [最近使用] <-> ... <-> [最久未使用]
                    Front                  Back
```

- **Get 操作**: 哈希表 O(1) 查找 -> 检查 TTL -> 移到链表头部（标记最近使用）
- **Put 操作**: 已存在则更新并移到头部；不存在则检查容量，满了就淘汰尾部，然后插入头部
- **淘汰**: 移除链表尾部元素（最久未使用），同时从哈希表和交易对索引中删除

### 2.2 交易对索引

除了按地址查找，还需要按交易对查找所有相关池子。pairIndex 维护了这个映射：

```
pairIndex: map["SOL:USDC"] -> ["pool_addr_1", "pool_addr_2", "pool_addr_3"]
```

这样 GetByPair("SOL", "USDC") 可以快速返回所有 SOL/USDC 的池子，供最优选择算法使用。

**为什么要同时查反向交易对？**

用户查 GetByPair("SOL", "USDC")，但某些池子可能是以 USDC 为 BaseMint、SOL 为 QuoteMint 注册的。查反向确保不遗漏。

### 2.3 TTL 过期机制

每个缓存条目记录插入时间 addedAt。Get 时检查 `time.Since(addedAt) > ttl`：

```
Put 时记录: addedAt = time.Now()
Get 时检查: if time.Since(addedAt) > ttl -> 过期，删除并返回 miss
```

TTL 是被动检查的（Get 时才发现过期），不是主动定时清理。主动清理由 Evict 方法实现，需要外部定时调用。

### 2.4 并发安全

LRUPoolCache 使用 sync.RWMutex 实现并发安全：

```
Get:  RLock (读锁，允许多个并发读)
      -> 发现过期: RUnlock -> Lock (升级为写锁) -> 删除过期条目 -> Unlock
      -> 未过期: RUnlock -> Lock -> MoveToFront -> Unlock
Put:  Lock (写锁，独占)
```

注意 Get 方法中有一个锁升级的过程：先 RLock 读数据，再 Lock 写入（MoveToFront 或删除）。这里有一个微妙的问题——在 RUnlock 和 Lock 之间存在时间窗口，其他 goroutine 可能已经修改了数据。在 LRUPoolCache 的实现中，这不会导致数据损坏（最多是多次 MoveToFront），但在其他场景下需要注意。

---

## 3. BasePoolManager 三层查找 [dexwallet 通用层] [L2]

### 3.1 查找流程

```
GetPool(address)
    |
    [1] cache.Get(address)
    |-- 命中 --> 返回
    |-- 未命中
    |
    [2] repo.GetPool(address)
    |-- 找到 --> cache.Put(pool) --> 返回
    |-- 未找到 --> 返回错误
```

在生产系统中还有第三层——从链上解析：

```
    [3] 通过 RPC 查询链上账户数据
    |-- PoolParser.Parse(rawData) --> repo.SavePool(pool) --> cache.Put(pool) --> 返回
```

### 3.2 回填缓存

从 Repository 查到数据后，一定要 cache.Put 回填。否则：
1. 下次查同一个池子又要走 Repository（通常是数据库查询）
2. 在高频交易场景下，数据库会成为性能瓶颈
3. 缓存命中率下降，整体延迟上升

### 3.3 GetBestPool 的逻辑

BasePoolManager 中的 GetBestPool 实现比较简单——只按流动性降序选：

```go
// BasePoolManager 的简单实现
best := pools[0]
for _, p := range pools[1:] {
    if p.State != PoolStateActive { continue }
    if p.Liquidity.Cmp(best.Liquidity) > 0 { best = p }
}
```

本 demo 中的 PoolManagerDemo 增强了这个逻辑，使用综合评分：

```
score = 0.7 * normalized_liquidity + 0.3 * fee_score
```

---

## 4. 最优池选择算法 [dexwallet 通用层] [L2]

### 4.1 评分公式

对于一组候选池子，按以下公式计算综合评分：

```
score(pool) = liquidityWeight * (pool.Liquidity / maxLiquidity)
            + feeWeight * (minFeeRate / pool.FeeRate)
```

其中：
- `maxLiquidity`: 候选池中最大的流动性值，用于归一化
- `minFeeRate`: 候选池中最小的费率值，用于归一化
- `liquidityWeight = 0.7`: 流动性权重
- `feeWeight = 0.3`: 费率权重

### 4.2 为什么流动性权重更高

流动性直接决定滑点。一个流动性 100 SOL 的池子和一个流动性 10000 SOL 的池子：

```
输入 1 SOL swap:
- 100 SOL 池子: 价格影响约 1%
- 10000 SOL 池子: 价格影响约 0.01%
```

而费率的差异通常在 0.01%~1% 之间，相比大额交易的滑点差异小得多。

**例外情况**：如果交易金额很小（远小于池子流动性），滑点差异可以忽略，此时费率差异变得重要。生产系统可能根据交易金额动态调整权重。

### 4.3 归一化的必要性

不同池子的流动性量级可能差异巨大：

```
Raydium AMM 池子:    Liquidity = 50000 * 10^9 (50000 SOL)
PumpFun 内盘池子:    Liquidity = 100 * 10^9 (100 SOL)
Meteora DLMM 池子:   Liquidity = 5000 * 10^9 (5000 SOL)
```

如果不归一化，直接比较 big.Int 值，大池子总是碾压小池子。归一化到 [0, 1] 后可以和费率评分公平比较。

### 4.4 big.Int 做除法的精度问题

big.Int 除法是截断除法，直接 Div 会丢失小数部分。为了保留精度，乘以一个缩放因子：

```go
// 错误：直接除法丢失精度
score := pool.Liquidity / maxLiquidity  // 小池子/大池子 可能 = 0

// 正确：先乘以缩放因子
scaleFactor := big.NewInt(10000)
score := new(big.Int).Mul(pool.Liquidity, scaleFactor)
score.Div(score, maxLiquidity)
// 现在 score 的范围是 [0, 10000]，代表 [0.0000, 1.0000]
```

---

## 5. 池子状态管理 [dexwallet 通用层] [L2]

### 5.1 三种状态

```
PoolStateActive    = "active"      -- 数据有效，可正常交易
PoolStateInactive  = "inactive"    -- 池子不可用
PoolStateNeedUpdate = "need_update" -- 数据可能过期，需刷新
```

### 5.2 状态转换规则

**active -> need_update:**
- 数据超过一定时间未更新（如超过 30 秒没有链上确认）
- 收到链上事件提示池子数据可能变化
- 缓存 TTL 到期但数据还在使用中

**need_update -> active:**
- 重新从链上解析数据并更新成功
- UpdatePool 被调用且新数据有效

**active -> inactive:**
- 池子流动性降到 0
- 池子被关闭或迁移
- DEX 协议升级，旧池子不再可用

**inactive -> active:**
- 生产中极少发生（池子重新注入流动性）
- 通常是发现新池子而非旧池子恢复

### 5.3 need_update 的设计意义

为什么不是 active 直接变 inactive？因为「数据可能过期」不等于「池子不可用」：

1. 池子可能仍然有效，只是缓存的流动性数据不够新
2. 用旧数据计算的报价可能不够精确，但交易仍然可能成功（链上有滑点保护）
3. 标记为 need_update 后，后台刷新任务会优先更新这些池子

这是一种「乐观策略」——先用旧数据，后台异步更新，避免同步等待链上查询的延迟。

---

## 6. Repository 内存实现 [demo 层] [L1]

### 6.1 生产 vs Demo

生产系统的 Repository 通常基于 MySQL 或 PostgreSQL：

```sql
CREATE TABLE pools (
    address VARCHAR(64) PRIMARY KEY,
    dex_id VARCHAR(32) NOT NULL,
    chain_id VARCHAR(16) NOT NULL,
    protocol_type VARCHAR(16) NOT NULL,
    base_mint VARCHAR(64),
    quote_mint VARCHAR(64),
    liquidity DECIMAL(78,0),
    fee_rate BIGINT,
    state VARCHAR(16),
    updated_at TIMESTAMP,
    extra JSON
);
```

本 demo 使用内存 map 模拟，接口一致但数据不持久化。

### 6.2 MemoryPoolRepository 的线程安全

```go
type MemoryPoolRepository struct {
    mu    sync.RWMutex
    pools map[string]*Pool    // address -> Pool
    txs   map[string]*TxRecord // txHash -> TxRecord
}
```

所有读操作用 RLock，写操作用 Lock。关键点：存入 map 时应该存副本，避免外部修改影响存储数据。

---

## 7. 定时刷新机制 [demo 层] [L3]

### 7.1 后台刷新的作用

定时刷新有两个目的：
1. **清理过期条目**：调用 cache.Evict() 移除 TTL 过期的条目，释放内存
2. **更新 need_update 池子**：主动从 Repository（生产中是链上）拉取最新数据

### 7.2 刷新间隔的选择

```
刷新间隔应该 < TTL，但不能太频繁：

TTL = 30s, 刷新间隔 = 10s  -- 合理，每个 TTL 周期内刷新 3 次
TTL = 30s, 刷新间隔 = 1s   -- 太频繁，浪费 CPU
TTL = 30s, 刷新间隔 = 60s  -- 太慢，过期条目堆积
```

### 7.3 刷新与查询的锁竞争

Evict() 需要遍历整个链表并持有写锁。如果缓存很大（数千条目），遍历时间可能较长，阻塞其他 Get/Put 操作。

生产中的优化方案：
1. **分批淘汰**：每次只检查 N 个条目，不一次性遍历全部
2. **惰性淘汰**：不主动 Evict，Get 时才检查过期（LRUPoolCache 已经这样做了）
3. **后台淘汰 + 惰性淘汰结合**：后台低频清理堆积的过期条目，Get 时做即时检查

---

## 8. 并发安全的关键点 [dexwallet 通用层] [L2]

### 8.1 读写锁选择

```
场景                     操作    锁类型    原因
GetPool(查缓存)           读     RLock    多个查询可并发
GetBestPool(查缓存)       读     RLock    多个查询可并发
Put(添加到缓存)           写     Lock     修改链表和哈希表
UpdatePool(更新缓存+存储)  写     Lock     修改数据
Evict(清理过期)           写     Lock     删除条目
RefreshCache(刷新)        写     Lock     可能触发 Evict
```

读操作远多于写操作（查询频率 >> 更新频率），RWMutex 允许多个读并发，是正确的选择。

### 8.2 避免死锁

PoolManagerDemo 内部同时持有 cache 和 repo 两个资源。如果 cache.Get 和 repo.GetPool 各自有锁，需要注意加锁顺序：

```
正确: 总是先 cache 后 repo
错误: goroutine A 先锁 cache 再锁 repo, goroutine B 先锁 repo 再锁 cache
```

在本 demo 中，cache 和 repo 的锁是独立的（各自内部管理），不会形成循环等待，所以不会死锁。

### 8.3 值拷贝 vs 指针共享

cache 中存储的是 *Pool 指针。如果多个 goroutine 获取同一个 Pool 指针，其中一个修改了 Liquidity，所有持有者都会看到变化。这可能导致数据竞争。

安全做法是在 Get 时返回副本，或者确保返回的 Pool 是只读的。本 demo 中不做深拷贝以保持简单，但生产系统需要注意这个问题。

---

## 9. Solana 和 EVM 的池子差异 [链特定层] [L3]

### 9.1 数据来源

**Solana:**
- 池子数据存储在链上账户（Account）中
- 使用 Borsh 序列化格式
- 需要知道 ProgramID 和账户布局才能解析
- 例如 Raydium AMM 的池子账户结构约 700+ 字节

**EVM:**
- 池子数据存储在合约状态中
- 通过调用合约的 view 函数读取（如 getReserves()）
- 使用 ABI 编码/解码
- 可以用 multicall 一次批量查询多个池子

### 9.2 更新频率

**Solana (400ms 出块):**
- 池子数据每 400ms 可能变化
- 高频交易的 MEME 池子每个区块都有 Swap
- TTL 通常设 5-15 秒

**EVM (BSC 3s, ETH 12s 出块):**
- 变化频率相对较低
- TTL 可以设长一些（15-60 秒）

### 9.3 Extra 字段的链特定数据

Solana 池子的 Extra:
```json
{
    "program_id": "675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8",
    "open_time": 1700000000,
    "amm_target_orders": "...",
    "pool_coin_vault": "...",
    "pool_pc_vault": "..."
}
```

EVM 池子的 Extra:
```json
{
    "factory": "0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f",
    "router": "0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D",
    "token0": "0x...",
    "token1": "0x...",
    "block_timestamp_last": 1700000000
}
```

这些链特定字段放在 Extra map 中，避免 Pool 结构体膨胀。

---

## 10. 池子发现与生命周期 [链特定层] [L3]

### 10.1 池子的诞生

**创建方式:**
- 用户通过 DEX 工厂合约创建新池子
- Solana: 调用 DEX Program 的 initialize 指令
- EVM: 调用 Factory 合约的 createPair/createPool

**发现方式:**
- 监听链上事件：Factory 合约的 PairCreated/PoolCreated 事件
- 区块同步器(Syncer)解析每个区块的交易，发现新池子
- 定期扫描已知 Factory 的池子列表

### 10.2 池子的死亡

池子不会真的被「删除」，但会变得不可用：
- 流动性被完全撤出（Liquidity = 0）
- 代币合约被设置为禁止交易
- DEX 协议升级，旧合约停止服务
- PumpFun 内盘代币毕业后，内盘池子停止交易

### 10.3 池子管理的完整生命周期

```
链上发现新池子
    |
    v
PoolParser.Parse(rawData)  -- 解析原始数据
    |
    v
repo.SavePool(pool)        -- 持久化
    |
    v
cache.Put(pool)             -- 加入缓存
    |
    v
[正常服务期间]
    |-- GetPool / GetBestPool   -- 被查询
    |-- UpdatePool              -- 数据更新
    |-- 状态转换                 -- active <-> need_update
    |
    v
[池子失效]
    |-- 流动性归零 / 协议升级
    |-- State = inactive
    |-- 不再参与最优选择
    |-- 最终被 LRU 淘汰出缓存
```
