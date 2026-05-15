# 05-aggregator-routing: 多 DEX 聚合与最优路由

## 模块概述

本模块实现多 DEX 聚合器的核心功能：并发报价、最优选择、降级兜底和两跳路由。BaseAggregator 是 `internal/dexwallet/aggregator.go` 中已实现的通用聚合器，Solana 和 EVM 共享完全一致的聚合流程，只是注册的 DEX 列表不同。

核心设计：
1. **并发报价** -- 向所有活跃 DEX 并发请求报价，带超时控制
2. **优先级排序** -- 按 DEX 优先级 + 输出金额综合排序，选出最优报价
3. **降级策略** -- 主 DEX 构建失败后，按优先级依次尝试其他 DEX
4. **两跳路由** -- 当 tokenA 和 tokenB 之间没有直接池子时，通过中间资产（SOL/WETH/USDC）进行两跳路由
5. **灰度发布** -- 按百分比控制某个 DEX 对请求的覆盖率，用于新 DEX 上线或故障隔离

## 并发报价流程图

```
SwapRequest
    |
    v
[1. 获取活跃 DEX 列表]
    |-- 遍历所有已注册 DEX
    |-- 过滤: Enabled == true
    |-- 灰度检查: rand(100) < GrayscalePercent
    |
    v
[2. 获取最优池子]
    |-- PoolManager.GetBestPool(fromToken, toToken)
    |-- 按流动性和费率排序
    |
    v
[3. 并发获取报价] (goroutine * N, 超时 3s)
    |-- DEX-A: Protocol.Quote(pool, amount, direction)
    |-- DEX-B: Protocol.Quote(pool, amount, direction)
    |-- DEX-C: Protocol.Quote(pool, amount, direction)
    |   (任何一个失败不影响其他)
    |
    v
[4. 收集有效报价]
    |-- 过滤: err == nil && OutputAmount > 0
    |
    v
[5. 按优先级 + 输出金额排序]
    |-- 第一排序键: Priority (小的优先, 1 > 2 > 3)
    |-- 第二排序键: OutputAmount (大的优先)
    |
    v
[6. 选出最优报价] --> Quote
    |
    v
[7. 构建交易]
    |-- 使用最优 Quote 对应的 SwapBuilder.Build()
    |-- 成功 --> SwapResult
    |-- 失败 --> 降级兜底
              |-- 排除失败的 DEX
              |-- 按优先级依次尝试其他 DEX 的 Builder
              |-- 全部失败 --> 返回错误
```

## 优先级策略

| 优先级 | 值 | DEX 类型 | 典型代表 | 适用场景 |
|--------|-----|----------|----------|----------|
| PriorityHigh | 1 | 内盘 (Bonding Curve) | PumpFun, Moonshot | 代币未毕业，只有内盘能交易 |
| PriorityMedium | 2 | AMM/CLMM | Raydium, Uniswap, PancakeSwap | 有直接交易对，最优价格 |
| PriorityLow | 3 | 聚合器 | Jupiter, 1inch | 兜底，路由拆分，多跳路由 |

**排序规则：**
- 优先级相同时，输出金额更大的报价排在前面
- 相同优先级的 DEX 之间形成价格竞争
- 高优先级的 DEX 即使输出稍低也会被优先选择（因为延迟更低、确定性更高）

## 降级策略

```
Build(req) --> FindBestQuote() --> 获取最优 Quote
    |
    v
使用 Quote.DexID 对应的 Builder.Build()
    |-- 成功 --> 返回 SwapResult
    |-- 失败 --> buildWithFallback()
                    |
                    v
                排除已失败的 DEX
                按优先级排序剩余候选
                    |
                    v
                依次尝试每个候选 DEX 的 Builder
                    |-- DEX-B.Build() 成功 --> 返回 SwapResult
                    |-- DEX-B.Build() 失败 --> 继续尝试 DEX-C
                    |-- 全部失败 --> 返回 "all DEX builders failed"
```

**降级的典型场景：**
1. 内盘代币已毕业，PumpFun 返回错误 --> 降级到 Raydium AMM
2. AMM 池子流动性不足 --> 降级到 Jupiter 聚合器
3. 链上拥堵导致某 DEX RPC 超时 --> 降级到其他 DEX

## 两跳路由

当 tokenA 和 tokenB 之间没有直接交易池时，通过中间资产进行两跳路由：

```
直接路由:
  tokenA --[pool_AB]--> tokenB

两跳路由:
  tokenA --[pool_AM]--> middleAsset --[pool_MB]--> tokenB

中间资产候选（按优先级）:
  Solana: SOL, USDC, USDT
  EVM:    WETH, USDC, USDT, WBNB
```

**两跳路由的选择逻辑：**
1. 对每个中间资产，查询 tokenA->middle 和 middle->tokenB 两段的报价
2. 计算两段的组合输出金额
3. 与直接路由（如果存在）比较
4. 选择输出最大的方案

**两跳路由的代价：**
- 两次 swap 手续费
- 两次价格影响
- 更高的 Gas 费用
- 更大的交易体积（Solana 1232 字节限制更紧张）

## 灰度发布

```go
type DexEntry struct {
    // ...
    GrayscalePercent int // 0-100, 100 表示全量
}
```

**灰度发布的工作原理：**
- 每次获取活跃 DEX 列表时，对每个 DEX 生成随机数 [0, 100)
- 如果随机数 < GrayscalePercent，则该 DEX 参与本次报价
- GrayscalePercent = 100 表示全量启用
- GrayscalePercent = 30 表示约 30% 的请求会使用该 DEX

**灰度发布的典型场景：**
1. 新 DEX 上线：先设 10%，观察成功率，逐步放量
2. DEX 故障恢复：从 0 逐步放量到 100%
3. A/B 测试：对比新旧 DEX 的表现

## 文件结构

```
05-aggregator-routing/
    README.md                      -- 模块概述（本文件）
    goals.md                       -- 学习目标与自检问题
    notes.md                       -- 深度技术笔记
    demo/
        mock_protocol.go           -- Mock DEX 协议和 SwapBuilder 实现
        mock_pool_manager.go       -- Mock PoolManager 实现
        two_hop_router.go          -- 两跳路由器
        priority_selector.go       -- 优先级竞争选择器（高/中/低三层降级）
        dex_competition.go         -- 多 DEX 并发竞争（三层优先级状态机 + SplitMix64 灰度 + fast-fail）
        dex_error_collector.go     -- DEX 错误收集与分析（优先级分组 + 业务错误优先）
        clmm_quote.go              -- CLMM 跨 tick 报价 + Bonding Curve 毕业检测
        main.go                    -- 可运行演示（5 个场景）
        aggregator_test.go         -- 测试
        priority_selector_test.go  -- PrioritySelector 测试
```

## 与生产系统的差距

| 维度 | 本 demo | 生产系统 |
|------|---------|----------|
| 报价来源 | Mock 固定比例 | 真实链上池子数据计算 |
| 池子数据 | 内存 Mock | LRU 缓存 + 链上实时刷新 |
| 路由算法 | 两跳穷举 | Dijkstra/BFS 多跳图搜索 |
| 灰度控制 | 随机数 | 配置中心 + 动态调整 |
| 降级策略 | 简单依次尝试 | 熔断器 + 健康检查 + 自动恢复 |
| 并发控制 | context.WithTimeout | rate limiter + semaphore + 背压 |
| 报价缓存 | 无 | 短期报价缓存（避免重复 RPC） |
| 路由拆分 | 不支持 | 大单拆到多个 DEX 并行执行 |
