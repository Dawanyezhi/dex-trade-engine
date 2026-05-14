# 05-aggregator-routing: 深度技术笔记

## 1. BaseAggregator 核心设计 [dexwallet 通用层] [L1]

BaseAggregator 是 `internal/dexwallet/aggregator.go` 中实现的通用聚合器。它的核心职责是：

```
输入: SwapRequest (我要用 X 数量的 tokenA 换 tokenB)
输出: SwapResult (构建好的交易数据)

中间过程:
  1. 获取所有活跃 DEX
  2. 并发获取报价
  3. 排序选最优
  4. 构建交易（失败则降级）
```

**为什么是通用层？**

Solana 上有 Raydium、PumpFun、Jupiter 等 DEX，EVM 上有 Uniswap、PancakeSwap、1inch 等 DEX。虽然底层协议完全不同，但聚合逻辑是一样的：
- 并发询价的模式一样
- 优先级排序的规则一样
- 降级兜底的策略一样
- 灰度发布的机制一样

差异只在于注册的 DexEntry 列表不同。这就是 dexwallet 通用层的设计原则：**共性逻辑上提，差异通过注册配置**。

---

## 2. 并发报价模式 [dexwallet 通用层] [L2]

### 2.1 goroutine + channel 模式

```go
results := make(chan quoteResult, len(entries))  // 缓冲 channel
quoteCtx, cancel := context.WithTimeout(ctx, a.quoteTimeout)
defer cancel()

var wg sync.WaitGroup
for _, entry := range entries {
    wg.Add(1)
    go func(e *DexEntry) {
        defer wg.Done()
        q, qErr := e.Protocol.Quote(quoteCtx, pool, req.Amount, req.Direction)
        results <- quoteResult{quote: q, err: qErr}
    }(entry)
}

go func() {
    wg.Wait()
    close(results)  // 所有 goroutine 完成后关闭 channel
}()

for r := range results {
    // 收集有效报价
}
```

**关键设计点：**

1. **缓冲 channel**: `make(chan quoteResult, len(entries))` 容量等于 goroutine 数量，任何 goroutine 都不会阻塞在发送上
2. **WaitGroup + close**: 确保所有 goroutine 完成后才关闭 channel，`for range` 才能正确退出
3. **context 传播**: quoteCtx 超时后，所有 goroutine 中的 Quote 调用会收到 context 取消信号
4. **闭包陷阱**: `go func(e *DexEntry)` 通过参数传递 entry，避免闭包变量捕获问题

### 2.2 超时控制

```
外层 context (比如 10s)
  |
  v
quoteCtx = WithTimeout(ctx, 3s)
  |
  v
各 goroutine 使用 quoteCtx 调用 Quote
  |-- DEX-A 响应 100ms --> 成功
  |-- DEX-B 响应 500ms --> 成功
  |-- DEX-C 响应 3100ms --> 超时（context.DeadlineExceeded）
```

超时的 DEX 不会阻塞整体流程：
- goroutine 会收到 context 取消信号
- Quote 方法应该检查 ctx.Err() 并提前返回
- 即使 goroutine 超时，它仍然会向 channel 发送错误结果
- 收集阶段过滤掉错误结果，只使用有效报价

### 2.3 部分失败的容错

这是聚合器最重要的特性之一：**部分 DEX 失败不影响整体报价**。

```
假设注册了 5 个 DEX:
  DEX-A: 报价成功，输出 1000 tokens
  DEX-B: 报价失败（RPC 超时）
  DEX-C: 报价成功，输出 950 tokens
  DEX-D: 报价失败（池子不存在）
  DEX-E: 报价成功，输出 980 tokens

结果: 从 3 个有效报价中选最优 --> DEX-A (1000 tokens)
```

如果是串行报价，DEX-B 的超时会延迟后续所有 DEX 的报价，总耗时可能超过 15s。并发报价时总耗时等于最慢的那个（超时 3s），或者所有成功的最慢那个。

---

## 3. 优先级排序算法 [dexwallet 通用层] [L2]

### 3.1 排序规则

```go
sort.Slice(quotes, func(i, j int) bool {
    if quotes[i].Priority != quotes[j].Priority {
        return quotes[i].Priority < quotes[j].Priority  // 优先级数值小的排前面
    }
    return quotes[i].OutputAmount.Cmp(quotes[j].OutputAmount) > 0  // 输出大的排前面
})
```

两级排序：
1. **第一级: 优先级** -- Priority 值越小越优先（1 > 2 > 3）
2. **第二级: 输出金额** -- 同优先级下，输出越多越优先

### 3.2 为什么不只看输出金额

假设场景：
```
PumpFun (优先级 1, 内盘): 输出 980 tokens
Raydium (优先级 2, AMM):  输出 1000 tokens
Jupiter (优先级 3, 聚合器): 输出 1010 tokens
```

如果只看输出金额，会选 Jupiter。但这可能不是最优选择：
- Jupiter 有额外的聚合器费用（约 0.1%），实际到手可能更少
- Jupiter 的延迟更高（需要先查询路由再构建交易）
- Jupiter 的交易可能更大（多跳路由占用更多指令空间）
- PumpFun 是内盘，如果代币还没毕业，只有内盘能交易

优先级排序确保：
- 能在内盘交易就在内盘交易（确定性最高）
- 有直接 DEX 池子就直接交易（成本最低）
- 聚合器作为最后的兜底（覆盖面最广）

### 3.3 同优先级的价格竞争

同一优先级内，输出金额决定排序。例如：
```
Raydium AMM (优先级 2):   输出 1000 tokens, 手续费 0.25%
Raydium CLMM (优先级 2):  输出 1020 tokens, 手续费 0.01%
Meteora DLMM (优先级 2):  输出 1015 tokens, 手续费 0.05%
```

CLMM 因为集中流动性的资本效率更高，提供了更好的价格，所以排在 AMM 前面。

---

## 4. 降级策略详解 [dexwallet 通用层] [L2]

### 4.1 两阶段降级

BaseAggregator 的降级分为两个阶段：

**阶段 1: 报价降级（在 FindBestQuote 中）**
- 如果某个 DEX 报价失败，直接跳过，不影响其他 DEX
- 只有全部报价失败才返回错误

**阶段 2: 构建降级（在 BuildSwap 中）**
- 使用最优报价对应的 Builder 构建交易
- 如果构建失败，进入 buildWithFallback
- 按优先级排序其他 DEX，依次尝试

### 4.2 buildWithFallback 的实现逻辑

```go
func (a *BaseAggregator) buildWithFallback(ctx context.Context, req SwapRequest, excludeDex DexID) (*SwapResult, error) {
    // 1. 收集候选（排除已失败的 DEX）
    // 2. 按优先级排序
    // 3. 依次尝试 Build
    // 4. 任何一个成功即返回
    // 5. 全部失败才返回错误
}
```

**为什么要排除已失败的 DEX？**
- 如果 DEX-A 的 Build 刚刚失败（比如毕业了），在降级中再试一次大概率还是失败
- 排除它可以避免不必要的延迟和错误日志

### 4.3 降级的典型实战场景

```
场景 1: PumpFun 代币已毕业
  PumpFun.Build() --> 错误: "token graduated, use AMM"
  --> fallback to Raydium AMM --> 成功

场景 2: Raydium 池子流动性耗尽
  Raydium.Build() --> 错误: "insufficient liquidity"
  --> fallback to Jupiter --> Jupiter 找到多跳路由 --> 成功

场景 3: 链上拥堵
  PumpFun.Build() --> 超时
  --> fallback to Raydium --> 超时
  --> fallback to Jupiter --> 超时
  --> 返回 "all DEX builders failed"
```

---

## 5. 灰度发布机制 [dexwallet 通用层] [L2]

### 5.1 实现原理

```go
func (a *BaseAggregator) getActiveDex() []*DexEntry {
    a.mu.RLock()
    defer a.mu.RUnlock()

    var active []*DexEntry
    for _, entry := range a.dexList {
        if !entry.Enabled {
            continue
        }
        if entry.GrayscalePercent < 100 {
            if rand.IntN(100) >= entry.GrayscalePercent {
                continue  // 本次请求不使用该 DEX
            }
        }
        active = append(active, entry)
    }
    return active
}
```

### 5.2 灰度百分比的含义

| GrayscalePercent | 含义 | 每 100 次请求约参与次数 |
|-----------------|------|----------------------|
| 0 | 完全不参与 | 0 次 |
| 10 | 10% 流量 | 10 次 |
| 30 | 30% 流量 | 30 次 |
| 50 | 半量 | 50 次 |
| 100 | 全量 | 100 次 |

### 5.3 灰度发布的实战流程

```
新 DEX "Meteora DLMM" 上线流程:

Day 1: SetGrayscale("meteora_dlmm", 5)   // 5% 流量
        监控: 报价成功率、延迟、输出金额偏差

Day 2: SetGrayscale("meteora_dlmm", 20)  // 放量到 20%
        监控: 同上 + 是否影响其他 DEX 的选中率

Day 3: SetGrayscale("meteora_dlmm", 50)  // 半量
        监控: 综合表现

Day 5: SetGrayscale("meteora_dlmm", 100) // 全量
```

如果在任何阶段发现问题：
```
SetGrayscale("meteora_dlmm", 0)   // 立即下线
EnableDex("meteora_dlmm", false)  // 或直接禁用
```

### 5.4 为什么用随机数而非哈希

灰度判定使用 `rand.IntN(100)` 而不是对请求 hash 取模。区别：
- **随机数**: 每次请求独立判定，同一笔交易重试时可能结果不同
- **哈希取模**: 相同请求总是得到相同结果（确定性灰度）

DEX 聚合场景中随机更合适，因为：
- 不需要对同一用户保持一致（不是 UI 灰度）
- 需要让流量均匀分布
- 避免某些特定请求总是命中/总是不命中

---

## 6. 两跳路由设计 [dexwallet 通用层] [L3]

### 6.1 为什么需要两跳路由

很多代币之间没有直接交易对。例如：
```
MEME-A 和 MEME-B 之间没有直接池子
但是:
  MEME-A / SOL 有池子 (Raydium)
  SOL / MEME-B 有池子 (Raydium)

两跳路由: MEME-A --> SOL --> MEME-B
```

### 6.2 中间资产的选择

中间资产需要满足：
1. **高流动性**: 确保两段路由都有足够的流动性
2. **低手续费**: 稳定的交易对通常手续费更低
3. **广泛的配对**: 大多数代币都有与之配对的池子

典型的中间资产：
- **Solana**: SOL（原生资产），USDC，USDT
- **EVM**: WETH/WBNB（原生资产包装版），USDC，USDT

### 6.3 两跳路由的金额计算

```
tokenA --[amount_in]--> middle_asset --[middle_amount]--> tokenB

第一跳: middle_amount = Quote(tokenA/middle, amount_in)
第二跳: final_output  = Quote(middle/tokenB, middle_amount)

总价格影响 = 第一跳影响 + 第二跳影响
总手续费 = 第一跳手续费 + 第二跳手续费
总 Gas = 第一跳 Gas + 第二跳 Gas
```

两跳路由的输出通常低于理论的直接路由（如果存在的话），因为：
- 两次手续费叠加
- 两次价格影响叠加
- 中间资产的买卖价差

### 6.4 RouteHop 数据结构

```go
type RouteHop struct {
    DexID     DexID    `json:"dex_id"`
    Pool      string   `json:"pool"`
    TokenIn   string   `json:"token_in"`
    TokenOut  string   `json:"token_out"`
    AmountIn  *big.Int `json:"amount_in"`
    AmountOut *big.Int `json:"amount_out"`
}
```

两跳路由生成两个 RouteHop：
```go
route := []RouteHop{
    {DexID: "raydium_amm", Pool: "pool_A_SOL", TokenIn: "MEME_A", TokenOut: "SOL",
     AmountIn: inputAmount, AmountOut: middleAmount},
    {DexID: "raydium_amm", Pool: "pool_SOL_B", TokenIn: "SOL", TokenOut: "MEME_B",
     AmountIn: middleAmount, AmountOut: finalOutput},
}
```

---

## 7. 并发安全设计 [dexwallet 通用层] [L3]

### 7.1 BaseAggregator 的锁策略

```go
type BaseAggregator struct {
    mu      sync.RWMutex      // 保护 dexList
    dexList map[DexID]*DexEntry
    // ...
}
```

**读操作（RLock）:**
- `getActiveDex()`: 遍历 dexList，收集活跃 DEX
- `BuildSwap()` 中查找 DexEntry

**写操作（Lock）:**
- `RegisterDex()`: 添加 DEX
- `SetGrayscale()`: 修改灰度百分比
- `EnableDex()`: 启用/禁用 DEX

### 7.2 为什么用 RWMutex 而不是 Mutex

运行时的访问模式：
```
初始化阶段: RegisterDex() * N 次  (写操作，低频)
运行阶段:   FindBestQuote() * 1000/s (读操作，高频)
            SetGrayscale() * 偶尔    (写操作，低频)
```

RWMutex 允许多个读操作并发，非常适合「读多写少」的场景。如果用 Mutex，1000/s 的 FindBestQuote 会串行化，性能急剧下降。

### 7.3 getActiveDex 的锁范围

```go
func (a *BaseAggregator) getActiveDex() []*DexEntry {
    a.mu.RLock()
    defer a.mu.RUnlock()
    // 遍历 + 灰度判定 + 收集结果
    // 全部在锁保护下完成
}
```

灰度判定在锁内完成是有意为之。如果先释放锁再判定：
```
// 错误做法（有 TOCTOU 竞态）
entries := a.getAllEntries()  // 持有锁
a.mu.RUnlock()
// 此时另一个 goroutine 可能 EnableDex(id, false)
for _, e := range entries {
    if e.Enabled {  // 已经过时的状态
        // ...
    }
}
```

锁内完成保证了读取状态和判定的原子性。

### 7.4 并发报价的安全性

并发报价的 goroutine 不需要锁 BaseAggregator，因为：
- goroutine 拿到的是 `*DexEntry` 指针的副本
- Quote 方法只读取 DexProtocol 和 Pool 数据
- 每个 goroutine 通过 channel 发送结果，channel 本身是并发安全的

唯一的竞态风险：如果在报价过程中修改了 DexEntry（比如 SetGrayscale），goroutine 可能看到中间状态。但这在实际中无害，因为灰度百分比的微小偏差不影响正确性。

---

## 8. Quote 超时与 Context 传播 [dexwallet 通用层] [L3]

### 8.1 超时层次

```
用户请求 context (可能带 10s 超时)
    |
    v
BaseAggregator.FindBestQuote()
    |
    v
quoteCtx = context.WithTimeout(ctx, 3s)  // 报价超时
    |
    v
goroutine-A: DexProtocol.Quote(quoteCtx, ...)
goroutine-B: DexProtocol.Quote(quoteCtx, ...)
goroutine-C: DexProtocol.Quote(quoteCtx, ...)
```

超时传播链：
1. quoteCtx 3s 超时到期
2. 所有 goroutine 中的 quoteCtx.Done() 被关闭
3. Quote 方法内部检查 ctx.Err()，返回 context.DeadlineExceeded
4. goroutine 发送错误结果到 channel
5. 收集阶段过滤掉错误结果

### 8.2 cancel 的调用时机

```go
quoteCtx, cancel := context.WithTimeout(ctx, a.quoteTimeout)
defer cancel()  // FindBestQuote 返回时取消
```

即使所有 goroutine 在超时前完成，cancel() 也必须调用，否则 quoteCtx 关联的定时器不会被释放（内存泄漏）。

---

## 9. DexEntry 注册机制 [dexwallet 通用层] [L2]

### 9.1 DexEntry 结构

```go
type DexEntry struct {
    Builder          SwapBuilder   // 构建交易
    Protocol         DexProtocol   // 获取报价和价格
    Priority         DexPriority   // 优先级
    Enabled          bool          // 是否启用
    GrayscalePercent int           // 灰度百分比
}
```

一个 DexEntry 包含两个核心接口实现：
- **DexProtocol**: 负责报价（Quote）和价格查询（GetPrice）
- **SwapBuilder**: 负责构建交易（Build）

它们是分开的，因为：
- 报价可以不构建交易（只做价格比较）
- 构建交易可以不报价（已经通过其他方式获得报价）
- 某些 DEX 的报价和交易构建是不同的 API 端点

### 9.2 注册流程

```go
// Solana 的注册示例
aggregator := NewBaseAggregator(coinset.ChainSolana, poolManager)

aggregator.RegisterDex(&DexEntry{
    Builder:          NewPumpFunBuilder(),
    Protocol:         NewPumpFunProtocol(),
    Priority:         PriorityHigh,    // 内盘优先
    Enabled:          true,
    GrayscalePercent: 100,             // 全量
})

aggregator.RegisterDex(&DexEntry{
    Builder:          NewRaydiumAMMBuilder(),
    Protocol:         NewRaydiumAMMProtocol(),
    Priority:         PriorityMedium,  // AMM 次之
    Enabled:          true,
    GrayscalePercent: 100,
})

aggregator.RegisterDex(&DexEntry{
    Builder:          NewJupiterBuilder(),
    Protocol:         NewJupiterProtocol(),
    Priority:         PriorityLow,     // 聚合器兜底
    Enabled:          true,
    GrayscalePercent: 100,
})
```

### 9.3 运行时动态调整

```go
// 故障隔离: 禁用有问题的 DEX
aggregator.EnableDex("pump_fun", false)

// 灰度放量: 新 DEX 逐步放量
aggregator.SetGrayscale("meteora_dlmm", 30)

// 恢复全量
aggregator.SetGrayscale("meteora_dlmm", 100)
```

所有这些操作都是线程安全的（持有写锁），可以在运行时安全调用。

---

## 10. 聚合器在交易全链路中的位置 [dexwallet 通用层] [L4]

### 10.1 全链路流程

```
用户请求 (tokenA, tokenB, amount)
    |
    v
[Aggregator] FindBestQuote()
    |-- 并发调用各 DexProtocol.Quote()
    |-- 排序选最优
    v
[Aggregator] BuildSwap()
    |-- 调用最优 DEX 的 SwapBuilder.Build()
    |-- 失败则降级
    v
[TxSender] Send()
    |-- Solana: 通过贿赂服务发送
    |-- EVM: 通过 Anti-MEV RPC 发送
    v
[TxSender] Confirm()
    |-- Solana: 等待 1 次确认 (~400ms)
    |-- EVM: 等待 N 次确认 (12s * N)
    v
[Monitor] 记录交易结果
```

### 10.2 组件依赖关系

```
Aggregator
  |-- 依赖 PoolManager (获取池子数据)
  |     |-- 依赖 LRUPoolCache (缓存)
  |     |-- 依赖 Repository (持久化)
  |
  |-- 依赖 DexProtocol[] (各 DEX 报价)
  |     |-- 依赖 RPCClient (查询链上状态)
  |
  |-- 依赖 SwapBuilder[] (各 DEX 交易构建)
  |     |-- 依赖 RPCClient (查询余额、模拟交易)
  |
  |-- 被 TxSender 消费 (发送构建好的交易)
```

### 10.3 生产中的扩展

**熔断器 (Circuit Breaker)**:
```
DexEntry 增加字段:
  consecutiveFailures int
  lastFailureTime     time.Time
  circuitOpen         bool

判断逻辑:
  if consecutiveFailures >= 5 && time.Since(lastFailureTime) < 30s:
      circuitOpen = true  // 熔断，不再尝试
  if circuitOpen && time.Since(lastFailureTime) >= 30s:
      circuitOpen = false  // 半开，允许一个请求探测
```

**路由图搜索**:
```
从两跳穷举 --> N 跳 Dijkstra:

graph[tokenA] = {SOL: pool1, USDC: pool2}
graph[SOL]    = {tokenB: pool3, USDC: pool4}
graph[USDC]   = {tokenB: pool5}

Dijkstra 以 "输出金额最大化" 为权重搜索:
  tokenA --> SOL --> tokenB (手续费 0.3% + 0.3%)
  tokenA --> USDC --> tokenB (手续费 0.3% + 0.1%)
  tokenA --> SOL --> USDC --> tokenB (三跳, 手续费 0.3% + 0.3% + 0.1%)

选输出最大的路径。
```

生产中通常限制最多 3 跳，因为：
- 每增加一跳，手续费和价格影响叠加
- Solana 交易体积限制（1232 字节）制约指令数量
- EVM Gas 费用随跳数线性增长
- 搜索空间指数增长（O(DEX^N)），超过 3 跳搜索成本过高
