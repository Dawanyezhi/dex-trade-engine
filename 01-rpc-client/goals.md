# 01-rpc-client: 学习目标与自检问题

## 知识点分层

### L1 -- 基础概念（必须掌握）

- RPC（Remote Procedure Call）是什么，JSON-RPC 协议格式
- Solana RPC 和 EVM RPC 的基本方法差异
- 为什么单一 RPC 节点不可靠（限速、宕机、区块高度落后）
- Go 接口（interface）的定义与实现
- `context.Context` 在 RPC 调用中的作用（超时控制、取消传播）

### L2 -- 核心实现（需要理解原理）

- 多节点故障转移的设计模式
  - 主备模式 vs 轮询模式 vs 自适应模式
  - 本 demo 采用「有序遍历 + 健康标记」
- `sync.RWMutex` 的读写锁语义
  - 为什么读操作用 RLock 而不是 Lock
  - 读写锁 vs 互斥锁的性能差异
- 后台健康检查 goroutine 的生命周期管理
  - 通过 `context.WithCancel` 控制退出
  - 避免 goroutine 泄漏
- 错误包装（`fmt.Errorf("xxx: %w", err)`）的意义
  - `errors.Is` 和 `errors.As` 的链式错误判断

### L3 -- 进阶设计（生产中需要）

- RPC 节点评分模型
  - 延迟权重、错误率权重、区块高度差权重
  - 滑动窗口统计 vs 指数衰减
- 限速器（Rate Limiter）设计
  - 每个 RPC 节点独立的令牌桶
  - 避免单个节点被打满
- WebSocket 长连接管理
  - 自动重连策略（指数退避 + 抖动）
  - 心跳检测
- 连接池与 HTTP 客户端复用
  - `http.Transport` 的 `MaxIdleConns` / `IdleConnTimeout`
  - HTTP/2 多路复用

### L4 -- 架构视角（通盘理解）

- RPCClient 接口在整个 dexwallet 架构中的位置
  - 它是所有链上操作的基础设施
  - TxSender / PoolManager / EventParser 都依赖它
- 通用层 vs 链特定层的分界
  - StableClient（故障转移）属于通用层
  - Solana/EVM 具体 RPC 方法属于链特定层
  - 通用层通过接口引用链特定层
- 链差异的参数化处理
  - coinset.ChainConfig 控制确认数、出块时间等
  - FeatureGate 控制链特有功能的开关
- 可观测性设计
  - 结构化日志（slog）
  - Metrics（Prometheus）
  - 分布式追踪（OpenTelemetry）
  - 告警（alarm.Manager）

## 自检问题

### L1 基础

1. Solana 的 `getRecentBlockhash` 和 EVM 的 `eth_blockNumber` 分别用于什么场景？
2. 为什么 `RPCClient.SendTransaction` 接收 `[]byte` 而不是结构化的交易对象？
3. `IsHealthy` 方法返回 `bool` 而不是 `error`，这样设计的理由是什么？

### L2 核心

4. 如果两个 goroutine 同时发现当前节点不健康，可能会出现什么问题？StableClient 如何解决？
5. 健康检查的间隔设为多少合适？太短和太长各有什么问题？
6. 为什么 `StableClient.callWithFailover` 在遍历所有节点后返回的是最后一个错误？有没有更好的做法？

### L3 进阶

7. 假设有 3 个 RPC 节点，延迟分别为 50ms/100ms/200ms，错误率分别为 0%/1%/5%，你会如何设计选择策略？
8. 一个 Solana RPC 节点的区块高度落后主网 100 个 slot，这个节点应该被标记为不健康吗？为什么？
9. 限速器应该放在 StableClient 层还是每个具体 RPCClient 内部？各有什么利弊？

### L4 架构

10. 如果要把 StableClient 从 demo 提升到 internal 包供全项目使用，需要做哪些改动？
11. StableClient 和 TxSender 的职责边界在哪里？如果 TxSender 也需要重试，和 StableClient 的故障转移如何协作？
12. 你的系统运行了一周后，发现某个 RPC 节点白天正常、半夜频繁超时。你会如何改进现有设计来自动应对这种情况？
