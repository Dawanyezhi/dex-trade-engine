# 08-production-architecture: 深度技术笔记

## 1. 令牌桶限流器 [dexwallet 通用层]

令牌桶算法是生产级限流的基础组件。限流逻辑对所有链完全一致（都是控制请求速率），只有参数不同（Solana TPS 高于 EVM）。

### 1.1 算法原理

```
桶容量 = 20（允许的最大突发量）
填充速率 = 10/s（长期平均速率）

时间线：
  t=0s: 桶中 20 个令牌（满桶），突发消耗 20 个 -> 桶空
  t=1s: 填充 10 个令牌（桶中 10 个）
  t=2s: 填充 10 个令牌（桶中 20 个，已满不再填充）
```

### 1.2 惰性填充（Lazy Refill）

关键优化：不使用定时器周期性填充令牌，而是在每次 `Allow()` 或 `Wait()` 调用时，根据距上次填充的时间差计算应补充的令牌数。

```go
func (l *TokenBucketLimiter) refill() {
    now := time.Now()
    elapsed := now.Sub(l.lastRefill).Seconds()
    tokensToAdd := elapsed * l.refillRate
    l.tokens = min(l.tokens + tokensToAdd, l.capacity)
    l.lastRefill = now
}
```

优势：
- 零后台 goroutine，零定时器开销
- 在无请求时不消耗任何 CPU
- 令牌精度取决于调用频率，高频场景下精度很高

### 1.3 阻塞等待 vs 非阻塞拒绝

两种使用模式的选择：

- `Allow() bool`：非阻塞，立即返回。适合快速失败场景（如 API 网关直接拒绝超限请求）。
- `Wait(ctx context.Context) error`：阻塞等待直到有令牌可用或 ctx 取消。适合内部队列消费者（愿意等待但不想超时太久）。

生产中常见的组合：API 入口用 `Allow()` 快速拒绝，内部任务队列用 `Wait()` 排队处理。

### 1.4 并发安全设计

为什么用 `sync.Mutex` 而不是 `sync/atomic`？

令牌桶涉及多个字段的联合更新（tokens、lastRefill），这些字段之间有因果关系（先计算时间差，再更新令牌数，最后更新时间戳）。`sync/atomic` 只能保证单个变量的原子性，无法保证多个变量的一致性。

```go
// 错误示范：用 atomic 无法保证一致性
// goroutine A 读取 lastRefill，计算 tokensToAdd
// goroutine B 在 A 更新 tokens 之前也读取了 lastRefill，导致重复填充
```

---

## 2. Worker 池 [dexwallet 通用层]

### 2.1 信号量模式

Go 中最轻量的并发限制方式是使用缓冲 channel 作为信号量：

```go
type WorkerPool struct {
    sem     chan struct{} // 缓冲大小 = 最大并发数
    maxSize int
}

// 获取许可（如果满了则阻塞）
pool.sem <- struct{}{}
// 执行任务...
// 释放许可
<-pool.sem
```

为什么用 `chan struct{}` 而不是 `chan bool`：`struct{}` 是零大小类型，不占用内存。当 channel 缓冲区很大时（如 1000），`chan bool` 会多消耗 1000 字节。

### 2.2 与 WaitGroup 的配合

Worker 池控制"同时有多少个任务在执行"，WaitGroup 控制"等待所有已提交的任务完成"。两者通常配合使用：

```go
var wg sync.WaitGroup
for _, task := range tasks {
    wg.Add(1)
    pool.Submit(func() {
        defer wg.Done()
        // 处理任务
    })
}
wg.Wait() // 等待所有任务完成
```

### 2.3 生产中的增强

基础 Worker 池在生产中通常需要增强：
- 优先级队列：紧急交易优先执行
- 任务超时：单个任务执行超时自动取消
- 动态伸缩：根据队列积压动态调整 Worker 数
- 优雅关闭：停止接受新任务，等待正在执行的任务完成

---

## 3. SwapQueue 请求队列 [dexwallet 通用层]

### 3.1 三层防护

SwapQueue 将限流器、Worker 池和有界队列组合成三层防护：

```
请求到达
  |
  v
[限流检查] -- Allow() 返回 false --> 拒绝请求（快速失败）
  |
  v (通过)
[入队列] -- 队列已满 --> 拒绝请求（背压）
  |
  v (入队成功)
[Worker 消费] -- 获取 Worker 许可 --> 执行 Swap
```

- 第一层（限流）：控制请求速率，防止 RPC 节点过载
- 第二层（队列）：缓冲突发流量，平滑处理速度
- 第三层（Worker 池）：控制并发度，防止 goroutine 爆炸

### 3.2 有界队列的容量设计

队列容量的设计需要权衡：
- **过小**：突发流量时大量请求被拒绝，用户体验差
- **过大**：积压的请求等待时间过长，Swap 的报价可能已经过期（链上价格变化快）

经验值：队列容量 = Worker 数 * 2~5。例如 5 个 Worker，队列容量 10~25。

### 3.3 背压信号

当队列使用率超过 80% 时，应该发出背压信号：
- 降低上游的请求接受速率
- 告警通知运维团队
- 考虑临时增加 Worker 数

---

## 4. 多链配置管理 [dexwallet 通用层]

### 4.1 静态配置 vs 运行时配置

`coinset.ChainConfig` 定义了链的静态属性（链类型、原生代币、出块时间等），这些在链创建后不会改变。

`ChainRuntime` 扩展了运行时可变的配置：
- `DisabledDex`：禁用的 DEX 列表（故障时手动或自动禁用）
- `GrayscaleMap`：灰度发布比例（新 DEX 逐步放量）
- `MaxSwapTPS`：Swap 限流参数（根据负载动态调整）
- `MaxWorkers`：Worker 池大小

### 4.2 热更新设计

热更新的核心挑战是并发安全：

```go
// 读操作（高频）：获取配置执行 Swap
func (m *MultiChainConfig) GetRuntime(chainID) *ChainRuntime {
    m.mu.RLock()         // 读锁
    defer m.mu.RUnlock()
    return m.configs[chainID]
}

// 写操作（低频）：更新配置
func (m *MultiChainConfig) UpdateChainConfig(chainID, updater func(*ChainRuntime)) {
    m.mu.Lock()          // 写锁
    defer m.mu.Unlock()
    updater(m.configs[chainID])
}
```

使用 `sync.RWMutex` 是因为读多写少：配置读取每秒可能数千次（每个 Swap 请求都要读配置），而配置更新可能几分钟甚至几小时才一次。

### 4.3 灰度发布实现

灰度发布的核心是按比例分流：

```go
func shouldUseGrayscale(percentage int) bool {
    return rand.Intn(100) < percentage
}
```

生产中灰度的维度更细：
- 按用户地址哈希分流（保证同一用户始终走同一组）
- 按交易对分流（某些交易对先上灰度）
- 按金额分流（小额先上灰度）

### 4.4 新增链的流程

新增一条 EVM 链（如 Monad）只需要：

1. 在 `coinset` 中添加 `ChainMonad ChainID = "monad"` 和默认配置
2. 在 `MultiChainConfig` 中注册 `ChainRuntime`，配置 TPS、Worker 数等
3. 灰度发布：从 10% 流量开始，逐步放量到 100%

零代码修改：因为所有 EVM 链共享相同的 Swap 构建器、事件解析器、限流逻辑。

---

## 5. 监控告警 [dexwallet 通用层]

### 5.1 MonitorService 设计

MonitorService 是一个定时运行的后台服务，职责：
1. 周期性收集所有链的 Monitor 指标
2. 汇总指标、检测异常
3. 通过 alarm.Manager 发送告警

```
MonitorService
  |-- monitors: map[ChainID]*Monitor  （每条链一个 Monitor）
  |-- alarm: *Manager                  （告警管理器，可有多个发送通道）
  |-- interval: time.Duration          （检查间隔）
```

### 5.2 监控指标类型

参考 `dexwallet.Monitor` 中定义的 7 个指标：

| 指标 | 类型 | 告警条件 |
|------|------|----------|
| block_height | 区块高度 | 超过 30s 未更新 -> Critical |
| tx_timeout | 交易超时 | 超时率 > 10% -> Warning |
| balance | 余额 | 低于阈值 -> Critical |
| pool_state | 池子状态 | 异常池子 > 20% -> Warning |
| swap_latency | Swap 延迟 | P99 > 3s -> Warning |
| quote_fail_rate | 报价失败率 | > 50% -> Warning |
| rpc_health | RPC 健康 | 不健康 -> Critical |

### 5.3 告警设计原则

- **分级告警**：不同级别通知不同的人，避免"狼来了"效应
- **告警抑制**：同一类告警在 5 分钟内只发送一次
- **告警升级**：Warning 持续 10 分钟自动升级为 Critical
- **恢复通知**：异常恢复后发送恢复通知（"区块高度已恢复正常"）

### 5.4 与 Prometheus 的对比

本 demo 使用内存计数器 + slog 日志，生产中使用 Prometheus：

```go
// 本 demo
monitor.RecordSwap(true)
stats := monitor.GetStats()

// 生产中（Prometheus）
swapCounter.WithLabelValues(chainID, dexID, "success").Inc()
swapLatency.WithLabelValues(chainID, dexID).Observe(elapsed.Seconds())
```

Prometheus 的优势：
- 多维度标签（chain, dex, direction 等）
- 内置聚合函数（rate, histogram_quantile 等）
- 与 Grafana 无缝集成，实时可视化
- AlertManager 提供完整的告警路由和抑制功能

---

## 6. 日志设计 [dexwallet 通用层]

### 6.1 限流相关日志

```go
// 请求被限流 -- Info 级别（正常行为，限流器在工作）
slog.Info("request rate limited", "chain", chainID, "queue_size", queueLen)

// 队列满拒绝 -- Warn 级别（需要关注，可能需要扩容）
slog.Warn("swap queue full, request rejected", "chain", chainID, "max_queue", maxQueue)

// Worker 池耗尽 -- Warn 级别
slog.Warn("worker pool exhausted", "chain", chainID, "max_workers", maxWorkers)
```

### 6.2 配置变更日志

```go
// 配置更新 -- Info 级别（运维操作，需要审计）
slog.Info("chain config updated",
    "chain", chainID,
    "field", "max_swap_tps",
    "old_value", oldTPS,
    "new_value", newTPS,
)

// DEX 禁用 -- Warn 级别（可能影响交易）
slog.Warn("dex disabled",
    "chain", chainID,
    "dex", dexID,
    "reason", "high_failure_rate",
)
```

### 6.3 监控告警日志

```go
// 告警触发 -- 由 alarm.Manager 内部记录
slog.Warn("[ALARM][critical] Block Height Stale: ...")

// 检查完成 -- Debug 级别
slog.Debug("monitor checks completed", "chain", chainID, "stats", stats)
```

---

## 7. 并发模型总结 [dexwallet 通用层]

本模块涉及的并发原语：

| 组件 | 并发原语 | 用途 |
|------|----------|------|
| TokenBucketLimiter | sync.Mutex | 保护 tokens + lastRefill 的联合更新 |
| WorkerPool | chan struct{} | 信号量，限制并发 goroutine 数 |
| SwapQueue | chan SwapTask | 有界缓冲队列，背压机制 |
| MultiChainConfig | sync.RWMutex | 读多写少的配置访问 |
| Monitor | sync/atomic | 高频计数器（Swap 计数、报价计数） |
| MonitorService | context + ticker | 定时检查 + 优雅退出 |

设计原则：
- 优先使用 channel 而非共享内存（Go 的哲学）
- 读多写少场景用 RWMutex
- 单个变量的高频更新用 atomic
- 多个变量的联合更新用 Mutex
- 后台任务用 context 控制生命周期
