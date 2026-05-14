# 模块 00：架构设计 -- 知识点分层与自检

## L3：白板能讲（核心原理，能从第一原理推导）

这一级的知识点必须能在白板上画出来、讲清楚，不依赖任何笔记。

### 1. 三层抽象设计

**知识点**：
- dexwallet 通用层（对标 irwallet）只定义接口和通用实现，不包含任何链特定逻辑
- 链特定层（solana / evm）实现 dexwallet 层的接口，处理链特有的交易构建、事件解析
- DEX 协议层（raydium / uniswap / ...）是最具体的一层，每个 DEX 有独立的报价和交易构建

**为什么这样分**：
- 从两个独立项目（solwallet / evmwallet）中发现大量重复代码：数据模型、Syncer 主循环、签名客户端、监控告警
- 抽取出 irwallet 后，新增一条 EVM 链只需添加配置；新增一条非 EVM 链需要实现接口但可复用所有通用逻辑
- 这是高级工程师的核心能力：从具体实现中识别通用模式并合理抽象

**白板画法**：
1. 画三个横向矩形，从下到上分别是 dexwallet / 链特定 / DEX 协议
2. 在 dexwallet 层写核心接口名：SwapBuilder / Aggregator / PoolManager / EventParser
3. 在链特定层画两列：Solana（指令组装/ALT/贿赂服务）和 EVM（ABI编码/Gas/Approve）
4. 用箭头标注"实现接口"的方向

### 2. Swap 完整流程

**知识点**（8 步）：
1. **预检查**：余额校验、参数校验、限流
2. **并行获取**：池子数据 + 推荐费用（并发，不串行等待）
3. **聚合报价**：并发调所有 DEX 的 Quote()，按优先级 + 输出金额排序
4. **构建交易**：工厂模式选择 SwapBuilder，链特定的交易构建
5. **模拟执行**：Solana simulateTransaction / EVM eth_estimateGas，检查滑点
6. **签名**：本地签名（demo）或远程 MPC 签名（生产）
7. **多通道发送**：RPC 标准通道 + 加速通道（Solana: 贿赂服务 / EVM: Anti-MEV RPC）并发发送，取第一个成功
8. **等待确认**：轮询交易状态，超时则标记 timeout

**白板画法**：画序列图，左边 Wallet，右边链上节点，中间标注每步操作

### 3. 聚合器工作原理

**知识点**：
- **并发 Quote**：goroutine 并发调用所有已注册且启用的 DEX
- **灰度过滤**：`GrayscalePercent < 100` 的 DEX 按概率参与
- **排序规则**：先按优先级（High > Medium > Low），再按输出金额（大 > 小）
- **降级兜底**：主 DEX 构建失败后，按优先级顺序尝试其他 DEX
- **超时控制**：单次 Quote 超时 3 秒，避免慢 DEX 拖慢整体

**Solana 优先级**：
```
High:   Pump.fun / Moonshot / Raydium Launchpad（内盘）
Medium: Raydium AMM/CPMM/CLMM, Meteora, PumpAMM（AMM/CLMM）
Low:    Jupiter, AlphAggregator（聚合器）
```

**EVM 优先级**：
```
High:   直接路由（UniSwap V2/V3, PancakeSwap）
Medium: 多跳路由
Low:    第三方聚合器（1Inch, ParaSwap, OKX DEX）
```

---

## L2：看到能识别（实现细节，看代码能理解）

这一级不需要默写，但看到代码要能立刻理解其作用和设计意图。

### 4. 核心接口的职责

| 接口 | 层级 | 职责 | 关键方法 |
|------|------|------|----------|
| SwapBuilder | dexwallet 定义，链特定实现 | 构建 Swap 交易 | `Build(ctx, SwapRequest) -> SwapResult` |
| DexProtocol | dexwallet 定义，DEX 层实现 | 提供报价和价格 | `Quote(ctx, Pool, amount, direction) -> Quote` |
| PoolManager | dexwallet 定义+通用实现 | 池子管理与缓存 | `GetBestPool / UpdatePool / RefreshCache` |
| Aggregator | dexwallet 通用实现 | 多 DEX 聚合选优 | `FindBestQuote / BuildSwap` |
| EventParser | dexwallet 定义，链特定实现 | 链上事件解析 | `Parse(ctx, rawTx) -> []ChainEvent` |
| BribeService | dexwallet 定义，链特定实现 | 交易加速（Solana 贿赂服务 / EVM Anti-MEV RPC） | `Send / GetRecommendedFee` |
| TxSender | dexwallet 定义+通用实现 | 交易发送与确认 | `Send / Confirm / Retry` |
| RPCClient | dexwallet 定义，链特定实现 | 链 RPC 客户端 | `SendTransaction / GetBalance / GetBlockHeight` |
| Repository | dexwallet 定义 | 数据持久化 | `SaveTxRecord / GetPool / UpdateTxStatus` |

### 5. 数据模型设计

**SwapRequest/SwapResult 的设计取舍**：
- 通用字段直接放结构体里：ChainID / DexID / Direction / Amount / SlippageBps
- 链特定字段用 `Extra map[string]interface{}` 承载：
  - Solana Extra: `compute_unit_limit`, `alt_addresses`, `use_token2022`
  - EVM Extra: `gas_limit`, `nonce`, `max_priority_fee`, `approval_tx`
- 这样做的好处：通用层代码可以读写所有通用字段，链特定逻辑只操作自己的 Extra

**Pool 模型的统一**：
- `Address / DexID / ChainID / ProtocolType` 所有链一样
- `BaseMint / QuoteMint / Liquidity / FeeRate` 语义对所有池子通用
- `State` 枚举（active / inactive / need_update）通用的状态机
- `Extra` 承载链特定数据：Solana 的 vault 地址、EVM 的 factory 地址

### 6. 双服务架构

**Wallet 服务**：
- API 层接收请求 -> 业务逻辑层构建交易 -> 签名 -> 发送
- 限流：令牌桶 20 TPS + 最大队列 500 + Worker 池 60

**Syncer 服务**：
- 主循环：轮询区块 -> 解析交易 -> 分类事件 -> 更新状态 -> 推送消息
- 重组检测：每个区块的 parentHash 必须匹配上一个区块
- 超时恢复：定期扫描 pending 交易，超时的标记为 timeout

---

## L1：知道有就行（具体参数值，记住即可）

### 7. 具体配置参数

- Solana 出块时间：约 400ms，确认数 1（finalized）
- EVM 出块时间：BSC 3s / ETH 12s / Base 2s，确认数 10-15
- 池子缓存 TTL：60 分钟
- 聚合器 Quote 超时：3 秒
- TxSender 发送超时：10 秒
- TxSender 确认超时：60 秒
- TxSender 最大重试：3 次
- 区块停滞告警阈值：30 秒

### 8. 监控指标名称

7 个核心监控指标：
1. `block_height` -- 区块高度（停滞告警）
2. `tx_timeout` -- 交易超时
3. `balance` -- 余额异常
4. `pool_state` -- 池子状态异常
5. `swap_latency` -- Swap 延迟
6. `quote_fail_rate` -- 报价失败率
7. `rpc_health` -- RPC 健康状态

### 9. 生产系统规模数据

- irwallet：30,800 行，233 个 Go 文件，被 2 个项目依赖
- solwallet：200K+ 行，支持 17+ DEX
- evmwallet：800+ 文件，支持 30+ DEX，覆盖 6 条链
- Solana 贿赂服务商：5 个（NextBlock / Temporal / ZeroSlot / BlockRazor / BlockRush）
- EVM 事件解析器：23+，按链和事件签名注册
- Solana 指令解析引擎：74 个文件，解析 70+ DEX

---

## L4：深度理论（接口设计原则、代码抽取方法论）

### 10. 接口设计原则

**接口隔离原则（ISP）的应用**：
- 没有把所有方法塞进一个 `Chain` 大接口
- 而是拆成 `RPCClient / SwapBuilder / PoolManager / EventParser / BribeService` 各自独立
- 每个链特定层只需实现它关心的接口

**依赖倒转原则（DIP）的应用**：
- dexwallet 层定义接口，链特定层实现接口
- dexwallet 层的通用代码（如 BaseAggregator）只依赖接口，不依赖具体实现
- 运行时通过注册机制注入具体实现

**开闭原则（OCP）的应用**：
- 新增一个 DEX：只需实现 `SwapBuilder + DexProtocol` 接口，然后注册到 Aggregator
- 新增一条链：实现所有接口 + 添加 ChainConfig，通用逻辑零修改
- 系统对扩展开放、对修改关闭

### 11. 代码抽取方法论

**从两个具体项目到一个通用框架的抽取过程**：

第一步：识别重复
- 对比 solwallet 和 evmwallet 的代码，标记哪些模块"长得很像"
- 数据模型（swaptx / pool / account 表结构）几乎一样
- Syncer 主循环（轮询 -> 分发 -> 持久化）框架一样
- 签名客户端、监控告警、限流逻辑完全一样

第二步：定义接口
- 把"长得很像"的模块抽象为接口
- 接口的方法签名要足够通用，输入输出用通用数据模型
- 链特定的参数用 Extra map 兜底

第三步：移动通用实现
- 把不依赖链特定逻辑的代码移到 irwallet
- 例如 BaseAggregator 的并发 Quote 逻辑对两条链完全一致
- 例如 LRUPoolCache 的缓存逻辑与链无关

第四步：验证抽象质量
- 新增 Base 链时，是否真的只需添加配置？（是）
- 新增 Monad 链时，需要改多少 irwallet 代码？（零行）
- Solana 新增一个 DEX 时，需要改 irwallet 吗？（不需要）

**抽取时的陷阱**：
- 不要为了"看起来一致"而过度抽象。Solana 指令解析和 EVM 事件解析虽然都是"解析链上交易"，但底层机制完全不同，强行统一只会增加复杂度
- 不要把链特定的优化放到通用层。Solana 的 ALT 优化是 Solana 特有的，放到 dexwallet 层会污染接口
- `Extra map[string]interface{}` 是妥协但实用的设计：类型安全性差，但灵活性高。生产中有 lint 规则检查 Extra 的使用

---

## 自检问题

### 白板级（L3）

1. **画出三层抽象架构图**，说明每层的职责和接口关系。
2. **讲述 Swap 的完整流程**（8 步），每步说明在哪一层执行。
3. **聚合器的并发报价是怎么工作的？** 灰度过滤、优先级排序、降级兜底分别怎么实现？
4. **如果你要新增一条 EVM 链（比如 Arbitrum），需要做什么？** 如果是非 EVM 链（比如 TRON）呢？

### 识别级（L2）

5. **SwapRequest 的通用字段和链特定字段是怎么分的？** Extra map 的设计有什么优缺点？
6. **Pool 数据模型为什么能同时表示 Solana 和 EVM 的池子？** 字段是怎么设计的？
7. **Wallet 服务和 Syncer 服务为什么要分开部署？** 合并有什么问题？
8. **irwallet 框架是怎么从 solwallet 和 evmwallet 中抽取出来的？** 抽取的四个步骤是什么？

### 记忆级（L1）

9. **Solana 的出块时间和确认数是多少？** BSC 和 ETH 呢？
10. **7 个监控指标分别监控什么？**

### 深度级（L4）

11. **在接口设计中，ISP（接口隔离）、DIP（依赖倒转）、OCP（开闭原则）分别体现在哪里？** 举具体代码为例。
12. **"看起来差不多但不该统一"的例子有哪些？** 为什么？强行统一会导致什么问题？
13. **Extra map 的替代方案有哪些？** 比较各方案的类型安全性、灵活性、维护成本。
