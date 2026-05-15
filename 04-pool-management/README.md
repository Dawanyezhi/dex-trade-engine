# 04-pool-management: 流动性池解析与管理

## 模块概述

本模块实现流动性池(Pool)的缓存管理、最优池选择和状态维护。池子是 DEX 交易的核心数据结构，每一笔 Swap 都依赖池子中的储备量和费率来计算输出金额。PoolManager 作为池子数据的统一入口，在缓存层、持久层和链上数据之间协调。

核心设计：
1. **LRU 缓存** -- 高频访问的池子数据通过 LRU 缓存加速，避免每次都查询存储或链上
2. **最优池选择** -- 同一交易对可能存在多个池子（不同 DEX、不同费率），需要综合评分选出最优
3. **状态管理** -- 池子有 active/inactive/need_update 三种状态，影响是否参与交易和何时刷新

## 池子数据结构

```
Pool
  |-- Address        池子合约地址（唯一标识）
  |-- DexID          所属 DEX（raydium_amm, uniswap_v2 等）
  |-- ChainID        所属链（solana, bsc 等）
  |-- ProtocolType   协议类型（amm, clmm, bonding_curve 等）
  |-- BaseMint       基础代币地址
  |-- QuoteMint      计价代币地址
  |-- Liquidity      流动性（最小单位，big.Int）
  |-- FeeRate        手续费率（基点，30 = 0.3%）
  |-- State          状态（active / inactive / need_update）
  |-- UpdatedAt      最后更新时间
  |-- Extra          链特定扩展字段
```

## 缓存策略

```
请求池子数据
    |
    v
[1. 查 LRU 缓存]
    |-- 命中且未过期 --> 返回（最快路径）
    |-- 命中但已过期 --> 从缓存删除，走下一步
    |-- 未命中 --> 走下一步
    |
    v
[2. 查持久存储（Repository）]
    |-- 找到 --> 放入缓存 --> 返回
    |-- 未找到 --> 返回错误
    |
    v
[3. 生产中还会查链上（PoolParser）]
    |-- 解析链上原始数据 --> 保存到存储 --> 放入缓存 --> 返回
```

LRU 缓存的关键参数：
- **capacity**: 最大缓存条目数，超出时淘汰最久未使用的条目
- **ttl**: 条目存活时间，过期后视为无效需要重新加载

## 最优池选择算法

同一交易对（如 SOL/USDC）可能有多个池子：
- Raydium AMM 池子（流动性大、费率 0.25%）
- Raydium CLMM 池子（流动性集中、费率 0.01%）
- Meteora DLMM 池子（流动性中等、费率 0.3%）

最优池选择的综合评分公式：

```
score = liquidity_weight * normalized_liquidity + fee_weight * (1 / fee_rate)
```

评分维度：
| 维度 | 权重 | 说明 |
|------|------|------|
| 流动性 | 0.7 | 流动性越大，滑点越小 |
| 费率倒数 | 0.3 | 费率越低，交易成本越低 |

额外约束：
- 只考虑 state == active 的池子
- 流动性为 0 的池子直接排除
- 同等评分下优先选择更近期更新的池子

## 状态管理

```
                      数据过期/链上变化
active ─────────────────────────────> need_update
  ^                                      |
  |          重新解析成功                  |
  +--------------------------------------+
  |
  |          池子被关闭/流动性归零
  +--------------------------------------> inactive
```

- **active**: 数据有效，可以正常使用
- **need_update**: 数据可能过期，需要刷新（可以临时使用旧数据，但应尽快更新）
- **inactive**: 池子不可用（流动性为 0、被关闭等），不参与最优选择

## 文件结构

```
04-pool-management/
    README.md                  -- 模块概述（本文件）
    goals.md                   -- 学习目标与自检问题
    notes.md                   -- 深度技术笔记
    demo/
        repository.go          -- 内存池子存储实现
        pool_manager.go        -- 池子管理器演示实现
        pool_parser.go         -- 池子数据解析路由器（按 ProgramID/Factory 分发解析）
        stablecoin_cache.go    -- 稳定币分层缓存（stable-stable/stable-normal/normal-normal 三档 TTL）
        main.go                -- 可运行演示
        pool_test.go           -- 测试
        pool_parser_test.go    -- PoolParser 测试
```

## 与生产系统的差距

| 维度 | 本 demo | 生产系统 |
|------|---------|----------|
| 数据来源 | 手动创建 Pool 对象 | RPC 查询链上账户数据，PoolParser 解析 |
| 持久存储 | 内存 map（进程退出丢失） | MySQL/PostgreSQL + Redis 缓存 |
| 池子发现 | 预定义地址 | 监听链上事件（Swap/Mint/Burn）自动发现新池子 |
| 价格更新 | 手动调用 UpdatePool | 区块同步器(Syncer)每个区块自动更新 |
| 最优选择 | 流动性+费率简单评分 | 还考虑价格影响、历史滑点、Gas 成本、路由深度 |
| 并发规模 | 单进程演示 | 数千个池子、每秒数百次查询 |
| 池子解析 | 不涉及 | Solana 用 Borsh 反序列化、EVM 用 ABI 解码 |
