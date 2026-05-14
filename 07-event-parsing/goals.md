# 07-event-parsing: 学习目标与自检问题

## 知识点分层

### L1 -- 基础概念（必须掌握）

- 链上事件的基本概念：什么是链上事件，为什么需要解析它们
- Solana 指令（Instruction）和 EVM 事件日志（Event Log）的区别
- ProgramID（Solana）和 Topic（EVM）的作用
- 区块同步的基本概念：为什么需要逐区块同步而不是直接查询最新状态
- 区块重组（Reorg）是什么，为什么会发生
- Go 中 `encoding/json` 的基本使用（Marshal/Unmarshal）
- `sync.RWMutex` 在注册表模式中的应用

### L2 -- 核心实现（需要理解原理）

- 注册表模式（Registry Pattern）的设计
  - 为什么使用 `map[string]EventHandler` 而不是 `switch-case`
  - 运行时动态注册 vs 编译时静态绑定的取舍
  - 标识符设计：Solana 用 ProgramID，EVM 用 Topic 签名
- Solana 事件解析的核心流程
  - ProgramID 匹配机制
  - 指令 Data 的解码方式（生产中是 Borsh，本 demo 用 JSON 模拟）
  - CPI（跨程序调用）产生的内部指令如何处理
- EVM 事件解析的核心流程
  - Topics[0] 是事件签名的 keccak256 哈希
  - indexed 参数在 Topics 中，非 indexed 参数 ABI 编码在 Data 中
  - 同一个合约地址可能触发多种事件
- Syncer 主循环的设计
  - 轮询模式 vs WebSocket 推送模式的取舍
  - 高度推进的正确性保证
  - 错误处理策略：跳过 vs 重试 vs 停止

### L3 -- 进阶设计（生产中需要）

- 重组检测与回滚机制
  - 为什么简单的 parentHash 检查不够（深度重组场景）
  - 生产中如何维护区块链的本地副本（区块哈希链表）
  - 回滚时如何撤销已经应用的状态变更
- 事件过滤与性能优化
  - 布隆过滤器（Bloom Filter）在 EVM 事件过滤中的应用
  - 地址白名单：只关注与自己相关的交易
  - 批量解析：一个区块内的交易可以并行解析
- 断点续传与容错
  - checkpoint 机制：定期持久化同步进度
  - 服务重启后从 checkpoint 恢复
  - 幂等性设计：重复处理同一个区块不产生副作用
- 事件分发架构
  - 回调函数（本 demo）vs 消息队列（生产）
  - 发布-订阅模式：不同下游消费不同类型的事件
  - 背压控制：当下游处理不过来时如何应对

### L4 -- 架构视角（通盘理解）

- EventParser 接口在 dexwallet 架构中的位置
  - 它连接了"链上世界"和"系统内部状态"
  - Syncer + EventParser = 系统的感知层
  - 解析出的事件驱动 PoolManager 更新、TxRecord 确认、余额刷新
- Solana 和 EVM 事件解析的统一抽象
  - 为什么统一为 ChainEvent 而不是各链独立的事件类型
  - 通用层（注册机制、事件模型）vs 链特定层（解析逻辑）的分界
  - 新增一条链（如 Aptos/Sui）时，只需实现新的解析器并注册
- 事件驱动架构的整体设计
  - 事件源（Syncer）-> 事件解析（EventParser）-> 事件分发 -> 事件消费
  - 与 CQRS/Event Sourcing 模式的关系
  - 最终一致性的保证
- 可观测性与运维
  - 同步延迟监控：当前高度 vs 链上最新高度
  - 解析错误率监控：未知事件 / 解析失败的比例
  - 重组次数监控：频繁重组可能意味着节点质量问题

## 自检问题

### L1 基础

1. Solana 的一笔交易可以包含多条指令，EVM 的一笔交易只调用一个合约。这个差异对事件解析有什么影响？
2. 为什么 EVM 的事件日志中 Topics[0] 是事件签名的哈希而不是原始签名字符串？
3. 区块重组为什么在 PoS 链（如以太坊合并后）上比 PoW 链上更少见？
4. `sync.RWMutex` 中，如果注册器的 Register 方法用 Lock，Parse 方法用 RLock，这样设计的理由是什么？

### L2 核心

5. ParserRegistry 为什么使用 `map[string]EventHandler` 而不是 `[]EventHandler`？当有 100 种事件类型时，两种方式的性能差异是什么？
6. 如果一笔 Solana 交易通过 Jupiter 聚合器执行了 3 跳路由（SOL->USDC->RAY->TOKEN），解析器需要如何处理？
7. Syncer 的主循环中，如果区块生产者返回错误（如 RPC 断连），应该直接 return 还是等待重试？为什么？
8. 为什么 Parse 方法返回 `[]ChainEvent` 而不是 `*ChainEvent`？在什么场景下一笔交易会解析出多个事件？

### L3 进阶

9. 假设发生了 3 个区块深度的重组（区块 100-102 被替换为 100'-102'），回滚时需要撤销哪些操作？如果某个事件已经触发了池子状态更新，如何恢复？
10. EVM 的 Receipt 中有一个 Bloom Filter 字段，它是如何帮助快速过滤相关事件的？为什么不直接遍历所有 Log？
11. 如果 Syncer 在处理区块 500 时崩溃重启，如何确保区块 500 不会被重复处理？如何确保区块 499 和 501 之间没有遗漏？
12. 当链上出块速度突然加快（如 Solana 的 burst），Syncer 来不及处理，积压了 1000 个区块。你会如何设计追赶策略？

### L4 架构

13. 如果要新增对 Aptos（Move 语言链）的事件解析支持，需要修改 dexwallet 通用层的哪些代码？理想情况下应该是零修改。
14. EventParser 解析出的 ChainEvent 可以驱动哪些下游操作？画出事件从链上到最终消费的完整流转链路。
15. 在极端情况下，Syncer 与链上最新高度差距超过 10000 个区块。此时应该切换到什么同步策略？为什么？
16. 你的系统同时运行 Solana Syncer 和 BSC Syncer，两者解析出的事件需要被同一个 PoolManager 消费。如何保证事件的有序性和一致性？
