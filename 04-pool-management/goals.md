# 04-pool-management: 学习目标与自检问题

## 知识点分层

### L1 -- 基础概念（必须掌握）

- Pool 数据结构的含义：每个字段代表什么，为什么 Liquidity 用 big.Int
- 缓存的基本概念：为什么要缓存池子数据而不是每次从链上读取
- LRU（Least Recently Used）淘汰策略的原理
- TTL（Time To Live）过期机制的作用
- Pool 的三种状态（active / inactive / need_update）各自的含义
- Repository 接口的职责：持久化存储池子数据和交易记录

### L2 -- 核心实现（需要理解原理）

- LRUPoolCache 的实现细节
  - 双向链表 + 哈希表实现 O(1) 的 Get/Put
  - 交易对索引（pairIndex）的设计：快速按交易对查找所有相关池子
  - 正向和反向交易对都要查（SOL/USDC 和 USDC/SOL）
- 最优池选择算法
  - 流动性和费率的综合评分
  - 为什么权重分配是 0.7 流动性 + 0.3 费率，而不是各占一半
  - 归一化处理：不同池子的流动性量级可能差几个数量级
- 并发安全
  - sync.RWMutex 的读写分离策略
  - 为什么 Get 用 RLock 而 Put 用 Lock
  - 缓存操作中锁的粒度控制
- BasePoolManager 的三层查找逻辑
  - 缓存 -> 存储 -> 链上（本 demo 只涉及前两层）
  - 查到后回填缓存的意义

### L3 -- 进阶设计（生产中需要）

- 缓存容量和 TTL 的调优
  - 容量太小：频繁淘汰，缓存命中率低
  - 容量太大：内存占用高，过期数据多
  - TTL 太短：频繁刷新，增加存储/链上查询压力
  - TTL 太长：数据过期，交易可能用旧的池子数据导致滑点过高
  - 生产中的经验值：Solana 400ms 出块，TTL 一般设 5-30 秒
- 池子数据的一致性问题
  - 缓存中的数据和链上真实状态之间的时间差
  - 在高频交易场景下，这个时间差如何影响 Swap 的成功率
  - 乐观更新 vs 悲观更新策略
- 定时刷新机制
  - 后台 goroutine 定期清理过期条目
  - 主动刷新 need_update 状态的池子
  - 刷新和正常查询之间的锁竞争问题
- 链上池子数据解析（PoolParser）
  - Solana: Borsh 序列化格式，按 ProgramID 判断池子类型
  - EVM: ABI 解码，multicall 批量查询储备量
  - 不同 DEX 的数据结构完全不同，但输出统一的 Pool 模型

### L4 -- 架构视角（通盘理解）

- PoolManager 在 dexwallet 三层架构中的位置
  - dexwallet 层：定义 PoolManager/PoolParser 接口，实现 LRUPoolCache 和 BasePoolManager
  - solana/evm 层：实现链特定的 PoolParser（Borsh/ABI 解析）
  - 调用方：Aggregator 通过 PoolManager 获取池子数据进行报价
- PoolManager 与其他组件的协作
  - Aggregator 调用 GetBestPool 获取最优池子
  - DexProtocol 使用 Pool 数据计算报价
  - Syncer 通过 UpdatePool 更新链上最新数据
  - Monitor 监控池子状态变化
- 大规模池子管理的挑战
  - Solana 上有数万个活跃池子（Raydium + Meteora + PumpFun）
  - 不可能全部缓存，需要按交易频率和流动性分级
  - 热池（高频交易的池子）常驻缓存，冷池按需加载
- 缓存失效策略与事件驱动
  - 被动失效：TTL 到期自动失效
  - 主动失效：收到链上事件（Swap/Mint/Burn）时主动更新
  - 两者结合是生产系统的常见做法

## 自检问题

### L1 基础

1. 为什么 Pool 的 Liquidity 字段使用 *big.Int 而不是 uint64 或 float64？给出一个 uint64 不够用的具体场景。
2. LRU 缓存中，当缓存已满时新插入一个条目，会发生什么？被淘汰的是哪个条目？
3. Pool 状态从 active 变为 need_update 的触发条件是什么？为什么不是直接变为 inactive？

### L2 核心

4. LRUPoolCache.GetByPair 为什么要同时查正向和反向交易对？给出一个必须查反向的场景。
5. 最优池选择中，为什么流动性权重（0.7）大于费率权重（0.3）？如果一个池子流动性很大但费率也很高（如 1%），另一个池子流动性较小但费率很低（如 0.01%），应该选哪个？
6. BasePoolManager.GetPool 中，从 Repository 查到数据后为什么要 cache.Put？如果不 Put 会有什么性能问题？
7. 如果两个 goroutine 同时调用 cache.Get，一个在 RLock 读数据，另一个在等待 Lock 写入新数据，会发生什么？

### L3 进阶

8. 如果 TTL 设为 1 秒，Solana 每 400ms 出一个块，会产生什么问题？如果设为 60 秒呢？
9. 假设缓存中的池子流动性是 100 SOL，但链上真实流动性已经因为一笔大额交易变成了 10 SOL。用户基于缓存数据发起 50 SOL 的 Swap 会怎样？
10. 后台刷新 goroutine 每 10 秒运行一次 Evict，但某个高频查询的池子刚好在 Evict 时被清除。下一次查询需要回源到 Repository，这段时间的延迟如何优化？

### L4 架构

11. 如果要新增一条链（比如 Base 链），PoolManager 层需要修改什么？PoolParser 层呢？
12. PoolManager 和 Aggregator 之间是什么关系？如果 PoolManager 返回的「最优池」和 Aggregator 通过报价选出的「最优 DEX」不一致，以谁为准？
13. 在一个管理 10000+ 池子的生产系统中，单一的 LRU 缓存会遇到什么瓶颈？如何用分片（sharding）改进？
