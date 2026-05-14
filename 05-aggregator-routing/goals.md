# 05-aggregator-routing: 学习目标与自检问题

## 知识点分层

### L1 -- 基础概念（必须掌握）

- 聚合器（Aggregator）的基本职责：向多个 DEX 询价，选最优，构建交易
- DEX 优先级的含义：为什么内盘优先级高于 AMM，AMM 高于聚合器
- 报价（Quote）的核心字段：输入金额、输出金额、价格影响、优先级
- 降级（Fallback）的概念：主 DEX 失败后尝试备选 DEX
- 滑点与价格影响（Price Impact）的区别
- 灰度发布的基本概念：按百分比控制流量

### L2 -- 核心实现（需要理解原理）

- 并发报价的实现模式
  - goroutine + channel 收集结果
  - context.WithTimeout 控制超时
  - sync.WaitGroup 等待所有 goroutine 完成
  - 部分失败不影响整体（错误隔离）
- 报价排序算法
  - 多键排序：先按优先级，再按输出金额
  - sort.Slice 自定义比较函数
  - 为什么不能只看输出金额（优先级的意义）
- 降级策略的实现
  - 排除已失败的 DEX
  - 按优先级排序候选 DEX
  - 依次尝试直到成功或全部失败
- DexEntry 的注册机制
  - RegisterDex: 注册 DEX 的 Builder、Protocol、优先级
  - EnableDex: 运行时启用/禁用
  - SetGrayscale: 运行时调整灰度百分比
- 灰度百分比的随机判定
  - rand.IntN(100) < GrayscalePercent 的概率含义
  - 为什么不是哈希取模（需要随机性，非确定性）

### L3 -- 进阶设计（生产中需要）

- 两跳路由的设计与实现
  - 中间资产的选择策略（SOL/WETH/USDC 等高流动性资产）
  - 两跳 vs 直接路由的权衡（手续费、价格影响、Gas）
  - 路由结果用 RouteHop 表示多跳信息
- 并发安全的设计
  - BaseAggregator 使用 sync.RWMutex 保护 dexList
  - 注册（写）和查询（读）的分离
  - getActiveDex 持有读锁遍历 map
  - 灰度随机判定在持有锁期间完成（避免 TOCTOU）
- 超时控制的层次
  - 外层 context 控制整体超时
  - quoteTimeout 控制单次报价超时
  - 报价超时后仍然使用已收到的有效报价
- 错误处理策略
  - 单个 DEX 报价失败只记录日志，不中断整体
  - 全部报价失败才返回错误
  - Build 失败触发降级链
  - 降级链全部失败才返回最终错误

### L4 -- 架构视角（通盘理解）

- BaseAggregator 在 dexwallet 三层架构中的位置
  - dexwallet 层: 定义 Aggregator 接口 + 实现 BaseAggregator
  - solana/evm 层: 注册各自的 DexEntry 列表
  - DEX 协议层: 实现各 DEX 的 DexProtocol 和 SwapBuilder
- Aggregator 与其他组件的协作关系
  - 依赖 PoolManager 获取交易对池子
  - 调用 DexProtocol.Quote 获取报价
  - 调用 SwapBuilder.Build 构建交易
  - 构建结果交给 TxSender 发送
- 生产中的扩展方向
  - 熔断器（Circuit Breaker）: DEX 连续失败 N 次后自动禁用
  - 健康检查: 定期探测 DEX 可用性，自动恢复
  - 报价缓存: 短期缓存避免重复 RPC 请求
  - 路由图搜索: 从两跳扩展到 N 跳 Dijkstra 搜索
  - 路由拆分: 大额交易拆分到多个 DEX 并行执行
  - 指标采集: 报价延迟、成功率、选中率等 Prometheus 指标
- Solana vs EVM 聚合的差异点
  - Solana: 交易体积限制（1232 字节）限制多跳路由的复杂度
  - EVM: 多跳路由可以在单个合约调用中完成（Router 合约内部循环）
  - Solana: 贿赂服务影响交易排序，聚合器需要考虑优先费
  - EVM: MEV 保护影响发送通道选择

## 自检问题

### L1 基础

1. BaseAggregator 的 FindBestQuote 方法做了哪三件事？为什么要并发而不是串行？
2. DexPriority 为什么用数值 1/2/3 而不是字符串 "high"/"medium"/"low"？这样设计有什么好处？
3. 如果所有 DEX 的报价都失败了，FindBestQuote 返回什么？如果部分成功呢？

### L2 核心

4. BaseAggregator 中的 goroutine 是怎么收集结果的？如果某个 goroutine 超时了会发生什么？channel 会泄漏吗？
5. 报价排序时，为什么优先级高的 DEX（值小）即使输出金额稍低也会被选中？给出一个具体场景说明这样做的合理性。
6. 灰度百分比设为 30 意味着什么？如果同一个请求连续调用 10 次 FindBestQuote，该 DEX 大约参与几次报价？
7. buildWithFallback 方法中，为什么需要排除第一个失败的 DEX？不排除会怎样？

### L3 进阶

8. 两跳路由 tokenA -> SOL -> tokenB 和直接路由 tokenA -> tokenB 相比，在哪些方面有劣势？什么场景下两跳路由反而更优？
9. BaseAggregator 的 getActiveDex 方法持有读锁时做了灰度随机判定，如果改为先释放锁再判定，会有什么问题？
10. 如果在报价过程中，另一个 goroutine 调用了 RegisterDex 注册新 DEX，会发生什么？是否安全？

### L4 架构

11. 如果要给 BaseAggregator 加入熔断器功能（连续失败 5 次自动禁用 DEX），需要修改哪些结构体和方法？
12. Jupiter/1inch 这类聚合器本身也会调用底层 DEX，如果 BaseAggregator 同时注册了 Jupiter 和 Raydium，Jupiter 内部又调了 Raydium，是否会导致重复报价？如何避免？
13. 从两跳路由扩展到任意 N 跳路由，数据结构和算法需要怎么改？为什么生产中通常限制最多 3 跳？
