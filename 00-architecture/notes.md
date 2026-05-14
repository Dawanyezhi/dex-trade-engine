# 模块 00：架构设计 -- 深度技术笔记

> 本笔记记录架构设计背后的"为什么"，可直接用于面试复习。
> 每节标注所属层级：[dexwallet 通用层] 或 [链特定层]。

---

## 一、irwallet 抽取过程的真实故事

### 1.1 前情：两个独立项目 [dexwallet 通用层]

最初，solwallet 和 evmwallet 是两个完全独立的项目，各自从零开始开发。solwallet 先做，支持 Solana 上的 DEX 交易；evmwallet 后做，支持 BSC、Ethereum 等 EVM 链。

开发 evmwallet 的时候，工程师们很自然地"参考" solwallet 的设计——Syncer 怎么写的、数据模型长什么样、签名服务怎么对接。结果就是：**大量的 copy-paste 加微调**。

具体重复了什么：

| 重复模块 | solwallet 中 | evmwallet 中 | 重复程度 |
|----------|-------------|-------------|---------|
| 数据模型（swaptx 表） | 定义了 TxHash/Direction/SellSymbol/BuySymbol/SlippageBps/PriorityFee 等字段 | 几乎一样的字段，只是加了 GasLimit/Nonce | 90% |
| Syncer 主循环 | 轮询区块 -> 检查重组 -> 遍历交易 -> 分发解析 -> 更新高度 | 完全一样的流程框架 | 80% |
| 签名客户端（Ksrv） | TLS 双向认证 + Ed25519 签名请求 | TLS 双向认证 + ECDSA 签名请求 | 95%（只有签名算法不同） |
| 监控告警 | 区块停滞、交易超时、余额异常等 7 个指标 | 一模一样的 7 个指标 | 100% |
| 限流器 | 令牌桶 20 TPS | 令牌桶 20 TPS | 100% |
| Repository 接口 | SaveTxRecord / GetPool / UpdateTxStatus | 方法签名几乎一样 | 85% |

**痛点出现**：当 solwallet 修了一个 Syncer 的 bug（比如重组检测的边界条件），evmwallet 也需要手动同步这个修复。反过来也是。两个项目的维护成本线性增长。

### 1.2 抽取的动机 [dexwallet 通用层]

触发抽取的直接原因有三个：

1. **同一个 bug 修两次**：Syncer 的超时交易恢复逻辑有个竞态条件，solwallet 修了之后 evmwallet 忘了同步，线上出了事故。
2. **新增链的成本太高**：准备接入 Base 链时，发现要从 evmwallet 里 copy 出一套基础框架，然后改改 Gas 参数。这种"copy + 微调"模式不可持续。
3. **团队扩展**：新人来了之后，需要同时理解两套几乎一样但又有微妙差异的代码，学习成本很高。

### 1.3 抽取的四步过程 [dexwallet 通用层]

**第一步：标记重复**

拿着 solwallet 和 evmwallet 的代码一个文件一个文件对比，用三种颜色标记：
- 绿色：完全一样，可以直接提取
- 黄色：结构一样但有细节差异，需要定义接口
- 红色：底层机制不同，不应该强行统一

结果：约 60% 绿色，25% 黄色，15% 红色。

**第二步：定义接口**

为黄色部分定义接口。核心原则：**接口的方法签名要用通用数据模型，链特定的参数放 Extra**。

```
// 伪代码：接口定义的演进过程

// 第一版（太具体，Solana 味太浓）：
type SwapBuilder interface {
    Build(instructions []Instruction, alt Address) Transaction
}

// 第二版（太通用，什么信息都没有）：
type SwapBuilder interface {
    Build(data interface{}) interface{}
}

// 最终版（平衡点：通用的结构 + 扩展字段）：
type SwapBuilder interface {
    Build(ctx context.Context, req SwapRequest) (*SwapResult, error)
}
// SwapRequest 有通用字段（Amount/Slippage/Direction）+ Extra map
```

**第三步：移动代码**

把绿色部分直接移到 irwallet 仓库。把黄色部分拆成"接口定义（irwallet）+ 具体实现（sol/evm wallet）"。

移动顺序：
1. 先移数据模型（model.go）——这是最安全的，不涉及逻辑
2. 再移 Repository 接口定义——只是接口，不影响实现
3. 然后移通用实现（Aggregator / PoolCache / TxSender）
4. 最后移基础设施（签名客户端 / 告警 / 限流器）

**第四步：验证**

验证标准：
- solwallet 和 evmwallet 只 import irwallet，不互相引用
- 新增 Base 链时只添加了 ChainConfig，业务逻辑零修改（实际做到了）
- irwallet 的单元测试覆盖率 > 80%

### 1.4 抽取后的效果 [dexwallet 通用层]

| 指标 | 抽取前 | 抽取后 |
|------|--------|--------|
| 修一个通用 bug 的改动点 | 2 处（sol + evm） | 1 处（irwallet） |
| 新增 EVM 链的工作量 | 3-5 天 | 0.5 天（只加配置） |
| 新人理解核心框架的时间 | 2 周 | 1 周 |
| 通用代码的测试覆盖 | 各 50% | 集中 80%+ |
| 总代码量 | 增加（irwallet 是新仓库） | 略增，但维护成本降低 |

---

## 二、抽取时的取舍

### 2.1 该抽取的：Aggregator 聚合逻辑 [dexwallet 通用层]

**为什么可以抽取**：

Solana 和 EVM 的聚合流程本质上完全一样：
1. 拿到一个 SwapRequest
2. 遍历所有已注册的 DEX，并发调用 Quote()
3. 收集结果，按（优先级, 输出金额）排序
4. 选最优的 DEX 构建交易
5. 构建失败则降级到下一个 DEX

唯一的区别是**注册的 DEX 列表和优先级配置不同**。Solana 注册的是 Raydium/PumpFun/Jupiter，EVM 注册的是 UniSwap/PancakeSwap/1Inch。但聚合的算法是一样的。

```
// 伪代码：聚合器的使用方式

// Solana 侧
solAgg := NewBaseAggregator("solana", solPoolManager)
solAgg.RegisterDex(&DexEntry{Builder: raydiumBuilder, Priority: Medium})
solAgg.RegisterDex(&DexEntry{Builder: pumpfunBuilder, Priority: High})
solAgg.RegisterDex(&DexEntry{Builder: jupiterBuilder, Priority: Low})

// EVM 侧
evmAgg := NewBaseAggregator("bsc", evmPoolManager)
evmAgg.RegisterDex(&DexEntry{Builder: uniswapV2Builder, Priority: High})
evmAgg.RegisterDex(&DexEntry{Builder: pancakeV3Builder, Priority: High})
evmAgg.RegisterDex(&DexEntry{Builder: oneInchBuilder, Priority: Low})

// 调用方式完全一样
quote, err := solAgg.FindBestQuote(ctx, req)
quote, err := evmAgg.FindBestQuote(ctx, req)
```

### 2.2 该抽取的：TxSender 多通道发送 [dexwallet 通用层]

**为什么可以抽取**：

交易发送的流程对两条链也是一样的：
1. 并发通过多个通道发送（标准 RPC + 贿赂服务/Anti-MEV RPC）
2. 取第一个成功的结果
3. 超时未成功则报错
4. 成功后等待确认

区别只在于：
- Solana 的贿赂服务是 NextBlock/Temporal 等 5 个服务商
- EVM 的是 Flashbots Protect / MEV Blocker 等

但 BaseTxSender 不关心具体是什么服务商，它只调用 `BribeService.Send()` 接口。

### 2.3 不该抽取的：事件解析方式 [链特定层]

**为什么不该抽取**：

Solana 和 EVM 的事件解析**底层机制完全不同**：

**Solana 事件解析**：
- 交易里包含一个指令列表（含内部指令）
- 每个指令有 ProgramID（标识是哪个程序/合约）
- 解析方式：按 ProgramID 分发到对应的解析器
- 指令数据是 Borsh 编码的二进制
- 一笔交易可能包含 10+ 个指令，需要把它们关联起来理解

**EVM 事件解析**：
- 交易执行产生 Event Log
- 每个 Log 有 topic[0]（事件签名的 keccak256 哈希）
- 解析方式：按 topic 签名匹配到对应的解析器
- 数据是 ABI 编码的
- 一笔交易的 Log 是独立的，不像 Solana 指令那样有嵌套关系

如果强行统一成一个 `Parse(rawData []byte) -> Events` 的抽象，看起来接口签名一样，但内部实现完全不同，而且会丢失每种方式的特有信息。更糟糕的是，会让代码更难理解——看到一个 EventParser 接口不知道它背后是指令解析还是 Log 解析。

**正确做法**：
- dexwallet 层定义 `EventParser` 接口和统一的 `ChainEvent` 输出模型
- Solana 和 EVM 各自实现 Parse()，内部用完全不同的解析策略
- 统一的是**输出**（ChainEvent），不是**过程**

### 2.4 不该抽取的：交易构建细节 [链特定层]

**Solana 交易构建**：
```
// 伪代码
instructions := []Instruction{
    ComputeBudgetInstruction(unitLimit, unitPrice),
    CreateATAInstruction(owner, mint),          // 如果 ATA 不存在
    SwapInstruction(pool, amountIn, minOut),     // DEX 特定
}
tx := NewTransaction(instructions)
tx.SetAddressLookupTable(altAddress)             // ALT 压缩
tx.SetRecentBlockhash(blockhash)
```

**EVM 交易构建**：
```
// 伪代码
// 第一步：Approve（如果 allowance 不足）
approveTx := NewTransaction(
    to:   tokenContract,
    data: abi.Encode("approve", routerAddress, maxUint256),
    gas:  estimateGas(),
)

// 第二步：Swap
swapTx := NewTransaction(
    to:    routerContract,
    data:  abi.Encode("swapExactTokensForTokens", amountIn, minOut, path, recipient, deadline),
    value: 0,  // 或 msg.value（如果是 ETH）
    gas:   estimateGas(),
    maxFeePerGas:         baseFee + maxPriorityFee,
    maxPriorityFeePerGas: maxPriorityFee,
)
```

这两段代码**没有任何公共部分**。指令列表 vs ABI 编码、ALT vs Approve、ComputeBudget vs EIP-1559——概念和操作完全不同。强行抽象只会创造一个什么都不做的空壳接口。

正确做法：SwapBuilder 接口只规定 `Build(SwapRequest) -> SwapResult`，内部如何构建完全由各链自己决定。

### 2.5 边界案例：数据模型中的 Extra 字段

`Extra map[string]interface{}` 是一个**有意识的妥协**。

**优点**：
- 灵活：任何链特定数据都可以塞进去
- 通用层代码不需要知道链特定字段的存在
- 新增链特定字段不需要修改通用数据模型

**缺点**：
- 类型不安全：取值需要类型断言，容易运行时 panic
- 文档化差：不看代码不知道里面有什么 key
- 序列化问题：JSON 序列化后数值类型可能变化（int -> float64）

**生产中的应对措施**：
- 每个链特定层定义常量 key：`const ExtraKeyComputeUnitLimit = "compute_unit_limit"`
- 提供类型安全的 helper 函数：`func GetComputeUnitLimit(extra map[string]interface{}) (uint32, error)`
- 代码 review 时重点检查 Extra 的读写

**替代方案对比**：

| 方案 | 类型安全 | 灵活性 | 复杂度 |
|------|---------|--------|--------|
| Extra map | 低 | 高 | 低 |
| 嵌入链特定子结构 | 高 | 中 | 中 |
| Proto Oneof | 高 | 中 | 高 |
| 泛型参数 | 高 | 中 | 高（Go 泛型还不够成熟） |

最终选择 Extra map 是因为在 Go 生态中这是最实用的方案。如果用 Rust 做，可能会选 enum（类似 Oneof）。

---

## 三、接口设计的演进

### 3.1 SwapBuilder 接口的演进 [dexwallet 通用层]

**第一版：太具体**

```go
// 第一版（2022 年初）
// 问题：参数列表是 Solana 特有的
type SwapBuilder interface {
    BuildSwapTx(
        pool AccountInfo,
        userATA AccountInfo,
        amount uint64,
        slippage float64,
        instructions []Instruction,
    ) (*Transaction, error)
}
```

这个接口 EVM 完全无法实现——什么是 AccountInfo？什么是 Instruction？

**第二版：太抽象**

```go
// 第二版
// 问题：什么类型信息都没有，无法做任何通用处理
type SwapBuilder interface {
    Build(params interface{}) (interface{}, error)
}
```

这样虽然"通用"了，但通用层没法做任何有意义的事情（比如日志记录、监控统计）。

**第三版（最终版）：平衡点**

```go
// 最终版
type SwapBuilder interface {
    Build(ctx context.Context, req SwapRequest) (*SwapResult, error)
    DexID() DexID
    ChainID() coinset.ChainID
    ProtocolType() ProtocolType
}
```

- `SwapRequest` 有通用字段让通用层能读（Amount/Slippage/Direction）
- `Extra` map 让链特定层能写自己的参数
- `DexID()` 和 `ChainID()` 方法让通用层能做路由和监控
- `context.Context` 支持超时控制和取消传播

### 3.2 PoolManager 接口的演进 [dexwallet 通用层]

**关键决策：GetBestPool 的排序标准**

最初 GetBestPool 只按流动性排序。后来发现这不够——流动性大的池子手续费率可能也高，综合下来不一定是最优的。

排序标准的演进：
1. 只看流动性：`sort by liquidity DESC`
2. 加入费率：`sort by (liquidity * (10000 - feeRate)) DESC`
3. 加入池子状态：先过滤 `state == active`，再排序
4. 加入价格影响：对于大额交易，需要模拟计算价格影响

当前实现采用简化版（流动性 + 状态过滤），更复杂的评分机制留在 DEX 协议层的 Quote() 里处理。这是一个**有意识的分层决策**：PoolManager 做粗筛，DexProtocol.Quote() 做精确计算。

### 3.3 Aggregator 接口的演进 [dexwallet 通用层]

**关键决策：灰度发布机制**

灰度发布是在新 DEX 接入时避免全量流量打过去的安全措施。

```
// 灰度逻辑
if entry.GrayscalePercent < 100 {
    if rand.IntN(100) >= entry.GrayscalePercent {
        continue  // 跳过这个 DEX
    }
}
```

灰度上线一个新 DEX 的流程：
1. 代码合入但设 `GrayscalePercent = 0`（不接流量）
2. 测试环境验证通过后，设 `GrayscalePercent = 10`（10% 流量）
3. 观察 1-2 天无异常，逐步提升到 30% -> 50% -> 100%
4. 如果出问题，直接设为 0 立即止血

**关键决策：降级兜底策略**

降级策略的设计经历了几个阶段：

1. **无降级**（最初）：主 DEX 失败就直接返回错误。问题：用户体验差，一个 DEX 故障影响全部交易。
2. **随机降级**：随机选另一个 DEX 重试。问题：可能选到优先级更低的聚合器，增加延迟和费用。
3. **优先级降级**（当前）：按优先级顺序尝试下一个 DEX。这保证了尽量用直接路由，聚合器只作为最后兜底。

```
// 降级顺序示例（Solana）
// Raydium AMM 失败 -> Meteora AMM -> PumpAMM -> Jupiter（聚合器兜底）
```

### 3.4 EventParser 接口的统一输出 [dexwallet 通用层]

虽然 Solana 和 EVM 的解析过程完全不同，但输出的 `ChainEvent` 是统一的。这让下游消费方（Syncer 主循环、池子更新、Kafka 推送）完全不需要关心事件来自哪条链。

```go
// 统一的事件模型
type ChainEvent struct {
    Type      EventType  // swap / transfer / mint / burn / liquidity
    ChainID   ChainID
    TxHash    string
    Block     uint64
    DexID     DexID
    Pool      string
    TokenIn   string
    TokenOut  string
    AmountIn  *big.Int
    AmountOut *big.Int
    Timestamp time.Time
}
```

**设计要点**：
- `Type` 枚举把所有链的事件分为 5 大类，足够覆盖当前需求
- 金额统一用 `*big.Int`（最小单位），由消费方根据 Token 的 decimals 转换
- 如果某个字段对特定事件类型无意义，就留空（Go 的零值语义）

---

## 四、面试叙事建议

### 4.1 讲架构的思路

面试中被问"你们的系统架构是什么样的"时，建议用这个顺序讲：

1. **先说定位**："我们做的是交易所的 DEX 交易引擎，核心功能是在链上 DEX 自动完成代币交换。"
2. **再说双服务**："每条链部署两个服务——Wallet 负责构建和发送交易，Syncer 负责同步区块和解析事件。"
3. **重点讲三层抽象**："最核心的架构设计是三层代码抽象——通用框架层、链特定层、DEX 协议层。这个设计让我们能用 30K 行的通用代码支撑 Solana 和 EVM 两个平台、47+ 个 DEX。"
4. **举具体例子**："比如聚合器的并发报价逻辑，对 Solana 和 EVM 完全一致，只是注册的 DEX 列表不同。新增 Base 链时，我们只添加了链配置，业务代码零修改。"

### 4.2 讲抽取过程的思路

被问"irwallet 是怎么抽取出来的"时：

1. **先说痛点**："最初 solwallet 和 evmwallet 是独立项目，有大量重复代码。同一个 bug 要修两次，新增链要 copy 一整套框架。"
2. **再说过程**："我们做了四步：标记重复 -> 定义接口 -> 移动代码 -> 验证抽象质量。"
3. **重点讲取舍**："不是所有重复都该抽取。比如 Solana 的指令解析和 EVM 的事件解析虽然看起来都是解析交易，但底层机制完全不同。我们统一了输出模型（ChainEvent），但没有统一解析过程。"
4. **量化效果**："抽取后，新增 EVM 链从 3-5 天降到半天。通用代码的测试覆盖率从 50% 提升到 80%+。"

### 4.3 讲接口设计的思路

被问"接口设计最难的决策是什么"时：

1. **说困难**："最难的是 SwapRequest/SwapResult 的字段设计。Solana 需要 ComputeUnit、ALT 地址，EVM 需要 GasLimit、Nonce——这些是链特定的，但我们又希望有一个通用的结构。"
2. **说方案**："我们的方案是：通用字段（Amount/Slippage/Direction）直接放结构体，链特定字段用 Extra map。"
3. **说权衡**："Extra map 类型不安全，这是妥协。我们通过定义常量 key、提供 helper 函数、code review 来缓解。如果用 Rust 可能会选 enum，但在 Go 生态里这是最实用的方案。"
