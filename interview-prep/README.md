# DEX 交易引擎 -- 面试准备材料

> 本文档以第一人称口语化风格编写，可直接用于面试中对面试官讲述。所有回答基于真实生产系统经验，包含具体数据和实际案例。

---

## 目录

- [一、DEX 交易引擎 Q&A](#一dex-交易引擎-qa)
- [二、架构设计 Q&A](#二架构设计-qa)
- [三、场景题](#三场景题)
- [四、深挖题](#四深挖题)

---

## 一、DEX 交易引擎 Q&A

### 1.1 "Swap 交易的完整流程是什么？"

我们的 Swap 交易流程分为七个阶段，每个阶段都有明确的职责边界。

第一步是**预检查**。收到 Swap 请求后，我们首先验证请求参数的合法性：检查发送方账户是否存在、卖出的 Token 是否支持、金额是否大于零、滑点设置是否在合理范围内（通常 0.5%-50%）。如果是 EVM 链还要检查 Approve 授权额度是否充足。这一步能拦掉大量无效请求，避免浪费后续的计算资源。

第二步是**并行获取基础数据**。我们用 goroutine 并发地获取四类信息：用户账户余额和 Nonce（确认资金充足）、当前网络费用信息（Solana 的 ComputeUnit 价格或 EVM 的 BaseFee + PriorityFee）、Token 元数据（精度、是否有税费、Token Program 类型）、以及相关流动性池子的实时数据。并行获取的好处是，这四个 RPC 调用互不依赖，可以把延迟从串行的 4 倍降到约 1 倍，在 Solana 上大约 200-400ms 就能全部拿到。

第三步是**选择最优 DEX**。我们的系统采用工厂模式，根据交易对自动匹配可用的 DEX 列表。然后并发调用所有候选 DEX 的 Quote 接口获取报价，按优先级和输出金额综合排序。在 Solana 上优先级是：内盘（Pump.fun/Moonshot/Raydium Launchpad）> AMM（Raydium/Meteora/PumpAMM/Boop）> 聚合器（Jupiter/AlphAggregator）。选择的核心逻辑是：如果 Token 还在内盘阶段（未毕业），直接走内盘 DEX，成本最低；如果已经毕业到 AMM，就比较各 AMM 的报价；只有直接路由都不行时才降级到聚合器。

第四步是**构建交易**。Solana 和 EVM 的构建方式差异很大。在 Solana 上，我们要组装一个指令列表：先放 ComputeBudget 指令设置计算单元上限和价格，然后是创建 ATA（Associated Token Account）的指令（如果用户没有目标 Token 的账户），最后是核心的 Swap 指令。如果指令太多导致交易体积超过 1232 字节，我们还要用 Address Lookup Table（ALT）来压缩地址引用。在 EVM 上，则是编码 ABI calldata，如果是第一次交易某个 Token，需要先发一笔 Approve 交易授权 Router 合约操作用户的 Token。

第五步是**模拟执行**。在 Solana 上调用 simulateTransaction RPC 方法，它会在节点本地执行交易但不上链，返回执行结果和消耗的 Compute Unit。我们用这个结果检查两件事：交易是否会执行成功（有没有指令错误），以及实际输出金额是否满足滑点阈值。在 EVM 上类似，用 eth_call 或 eth_estimateGas 预估 Gas 消耗，确保交易不会 revert。

第六步是**MPC 签名**。我们的生产系统不在本地存储私钥，而是通过 ksrv 远程签名服务完成签名。ksrv 使用 TLS 双向认证，客户端和服务端都要验证对方的证书，确保通信安全。签名请求发送到 ksrv 后，它在 HSM（硬件安全模块）中完成签名计算并返回签名结果。整个过程通常在 50-100ms 内完成。

第七步是**发送交易**。在 Solana 上，我们通过贿赂服务（NextBlock/Temporal/ZeroSlot/BlockRazor/BlockRush，共 5 个服务商）多通道并发发送，提高交易被打包的概率。在 EVM 上，通过 Anti-MEV RPC 节点发送，避免交易进入公开 mempool 被三明治攻击。发送后进入确认追踪阶段，轮询交易状态直到确认或超时。

### 1.2 "你们怎么选择最优 DEX？"

我们的最优 DEX 选择策略是一套"并发报价 + 优先级排序"的聚合机制。

具体来说，当一笔 Swap 请求进来，系统首先根据交易对（比如 SOL/MEME_TOKEN）查询所有可能有这个交易对池子的 DEX 列表。然后用 goroutine 并发调用每个 DEX 的 Quote 方法获取报价，设置一个统一的超时时间（通常 3 秒）。所有报价返回后（或者超时后），我们按两个维度排序：首先是优先级，其次是输出金额。

优先级策略是我们在生产中总结出来的经验。在 Solana 上，我们把 DEX 分为三档：High 优先级是内盘 DEX（Pump.fun、Moonshot、Raydium Launchpad），这些是 Token 还在 Bonding Curve 阶段的 DEX，价格由曲线公式直接决定，滑点可控，交易成本最低；Middle 优先级是 AMM 类 DEX（Raydium AMMv4/CPMM/CLMM、Meteora AMM/DAMM V2/DBC/DLMM、PumpAMM、Boop），这些是 Token 毕业后的主要交易场所；Low 优先级是聚合器（Jupiter、AlphAggregator），它们本身也会路由到上面的 AMM，但多一层中间调用，Gas 消耗更高，而且聚合器有时候 API 不稳定。

在同一优先级内，我们按"扣除 Gas 费用后的净输出金额"排序，选出净收益最高的那个。比如 Raydium AMM 报价输出 100 个 Token 需要 50000 Compute Unit，而 Meteora 报价输出 98 个 Token 但只需要 30000 Compute Unit，在 Compute Unit Price 较高的时段，Meteora 的净收益可能反而更好。

还有一个灰度机制。当我们新接入一个 DEX 时，不会立刻对 100% 的请求启用，而是通过配置一个 grayscale_percentage 参数（比如先设 10%），让只有 10% 的请求会尝试这个新 DEX 的报价。观察一段时间没有问题后，再逐步提高到 50%、100%。这样即使新 DEX 有 bug，影响面也是可控的。

### 1.3 "MEV 是什么？你们怎么防？"

MEV 是 Maximal Extractable Value（最大可提取价值）的缩写，简单来说就是矿工或验证者利用自己对交易排序的控制权来获取额外利润的行为。最常见的 MEV 攻击是三明治攻击（Sandwich Attack）。

三明治攻击的原理是这样的：假设用户要用 1000 USDT 买某个 Token，攻击者看到这笔交易后，在它之前插入一笔买入交易（Front-run），把价格抬高；然后让用户的交易以更高的价格执行；最后攻击者在用户交易之后插入一笔卖出交易（Back-run），把刚才买的 Token 卖掉赚取差价。用户最终拿到的 Token 数量比正常情况少了，差价就是攻击者的利润。在 EVM 链上，由于 mempool 是公开的，攻击者可以直接监听待打包的交易，特别容易被三明治攻击。

我们在 Solana 上的防护策略是使用贿赂服务。Solana 的架构和 EVM 不同，它没有公开的 mempool，交易直接发送给当前 slot 的 leader 节点。但问题是，如果你通过普通 RPC 发送，交易可能被转发到多个节点，仍然有被截获的风险。所以我们使用贿赂服务——通过给 leader 节点支付额外的小费（tip），让交易通过私有通道直接到达 leader，并获得优先打包。我们接入了 5 个贿赂服务商：NextBlock（在 Tokyo/Frankfurt/NY/London/SLC 五个地区有 CDN 节点）、Temporal（在 SGP/AMS/TYO/EWR/FRA2 五个地区有 CDN 节点）、ZeroSlot（低滑点专用）、BlockRazor（支持 fast 和 sandwichMitigation 两种模式）、BlockRush（高吞吐量）。我们多通道并发发送，只要有一个通道成功就行，这样既提高了成功率，也降低了对单一服务商的依赖。

在 EVM 链上，我们的防护策略是使用 Anti-MEV RPC 节点。比如 Flashbots Protect 和 MEV Blocker，它们的原理是把交易发送到私有的交易池，绕过公开的 mempool，直接与合作的区块构建者通信，这样攻击者就看不到你的待处理交易。此外，我们还使用了 RBF（Replace-By-Fee）交易替换机制，如果发现交易长时间未被打包，可以发送一笔相同 Nonce 但更高 Gas 的替换交易来加速确认，避免交易在 mempool 中暴露太久。

### 1.4 "Solana 和 EVM 链的 Swap 有什么区别？"

Solana 和 EVM 链在 Swap 实现上有几个根本性的差异，这些差异直接影响了我们的代码架构设计。

第一个差异是**账户模型**。Solana 使用账户模型（Account Model），每个 Token 余额存储在独立的 Token Account 中，用户必须先创建 Associated Token Account（ATA）才能接收某个 Token。这意味着 Swap 交易的指令列表中经常需要包含"创建 ATA"的指令。而 EVM 使用状态模型（State Model），Token 余额记录在合约内部的 mapping 里，不需要额外创建账户，但需要 Approve 授权合约操作你的 Token。所以 Solana 的额外步骤是创建 ATA，EVM 的额外步骤是 Approve 授权。

第二个差异是**交易结构**。Solana 的交易是一个指令列表（Instructions），每个指令指定一个 Program ID、涉及的账户列表和数据。一笔 Swap 交易通常包含 3-5 个指令：SetComputeUnitLimit、SetComputeUnitPrice、CreateATA（如果需要）、Swap 指令。交易的最大尺寸是 1232 字节，超出时需要用 Address Lookup Table（ALT）来压缩账户引用（从 32 字节压缩到 1 字节的索引）。EVM 的交易则是单一的合约调用，通过 ABI 编码把函数签名和参数打包成 calldata，没有大小限制但 Gas 消耗与 calldata 长度相关。

第三个差异是**费用模型**。Solana 的费用由三部分组成：基础费（固定 5000 lamports/签名）、优先费（ComputeUnitPrice 乘以 ComputeUnitLimit，单位是 micro-lamports）、以及贿赂费（通过贿赂服务支付给 leader）。优先费的推荐值我们每 2 秒从链上获取一次，取最近几个区块的中位数。EVM 的费用模型遵循 EIP-1559：Gas 费用 = (BaseFee + MaxPriorityFee) 乘以 GasUsed。BaseFee 由协议根据区块利用率动态调整，MaxPriorityFee 是给矿工的小费。我们用 GasOracle 缓存当前的 Gas 价格推荐值。

第四个差异是**MEV 防护机制**。Solana 没有公开的 mempool，我们通过贿赂服务（5 个服务商）获得优先打包权。EVM 有公开的 mempool，我们通过 Anti-MEV RPC 节点（Flashbots Protect 等）把交易发送到私有池，避免被攻击者监听。

第五个差异是**Token 标准**。Solana 有两套 Token Program：传统的 Token Program 和新的 Token2022（Token Extensions），后者支持转账费、利息等扩展功能，处理时需要区分。EVM 主要是 ERC-20 标准，但部分 Token 有特殊逻辑（比如转账税、重基等），我们通过白名单和模拟执行来处理。

### 1.5 "流动性池子怎么管理和缓存？"

流动性池子的管理是 DEX 交易系统的基础设施，我们设计了一套多层缓存加实时更新的方案。

首先是**池子数据模型**。我们在 irwallet 通用层定义了统一的 Pool 数据结构，核心字段包括：pool_contract（池子合约地址）、base_mint/quote_mint（交易对的两个 Token 地址）、liquidity（流动性深度）、fee_rate（手续费率）、dex_id（所属 DEX 标识）、state（池子状态：可用/不可用/需更新）。这个数据模型是跨链通用的，Solana 和 EVM 的池子都用同一个结构表示，链特定的额外字段通过 Extra 字段扩展。

缓存策略是**三层架构**。第一层是 LRU 本地内存缓存，容量根据链的活跃池子数量配置（Solana 约 10 万个活跃池子，我们缓存热点的 2 万个），命中率通常在 85% 以上。第二层是定时全量刷新，每 60 分钟从数据库加载最新的池子数据到内存缓存，保证即使实时推送有遗漏也不会用到太旧的数据。第三层是 gRPC 实时推送，在 Solana 上我们接入了 Helius 的 LaserStream 服务，当链上池子状态发生变化时（比如有人添加/移除流动性），LaserStream 会在 200-300ms 内推送更新事件，我们实时更新本地缓存。

**最优池选择**（GetBestPool）是一个多维度的排序算法。当用户要交易某个 Token 对时，可能有多个 DEX 都有这个交易对的池子。我们按以下维度综合评分：流动性深度（深度越大滑点越小）、手续费率（越低越好）、池子健康状态（最近是否有异常）、DEX 的优先级权重。最终返回评分最高的池子给 Swap 构建器使用。

池子状态管理也很重要。我们把池子分为三种状态：Active（正常可用）、Inactive（暂时不可用，比如流动性为零）、NeedUpdate（数据可能过期，需要刷新后再使用）。当 Syncer 检测到某个池子的链上数据发生变化但还没来得及完全解析时，会先把状态标记为 NeedUpdate，下次使用时触发一次同步刷新。这样既保证了数据的时效性，又不会因为频繁刷新占用太多 RPC 调用配额。

在 Solana 上，池子数据的解析需要反序列化 Account 数据（Borsh 编码格式），不同 DEX 的账户布局（Account Layout）完全不同——比如 Raydium AMM 的池子账户有 700+ 字节，Pump.fun 的 Bonding Curve 账户只有约 200 字节。在 EVM 上，池子数据通过读取合约 Storage 或解析 Factory 合约的 PairCreated/PoolCreated 事件日志来获取。

### 1.6 "聚合器降级策略是怎么设计的？"

我们的降级策略是一套"分级尝试、逐步兜底"的机制，确保在部分 DEX 不可用时交易仍然能完成。

第一级是**直接路由**。系统首先尝试在单个 AMM 池子中直接完成交换，比如用户要买 Token A，如果 Raydium 上有 SOL/Token_A 的池子且流动性充足，就直接在 Raydium 构建交易。直接路由的优势是 Gas 消耗最低、延迟最短、滑点可精确预测。在 Solana 上我们有 17+ 个 DEX 的直接路由支持，EVM 上有 30+ 个。

第二级是**多跳路由**。如果没有直接的交易对池子，我们尝试通过中间资产进行两跳交易。比如要从 Token_A 换到 Token_B，如果没有直接的 A/B 池子，但有 A/SOL 和 SOL/B 两个池子，就走 A -> SOL -> B 的两跳路径。中间资产的选择优先级是：SOL（Solana 原生资产）或 WETH/WBNB（EVM 链原生资产）> USDC > USDT。两跳路由的代价是 Gas 消耗翻倍、滑点叠加，但能覆盖更多的长尾交易对。我们还支持稳定币桥接，比如 USDT -> USDC 之间如果有低滑点的 StableSwap 池子，可以作为特殊的中间跳板。

第三级是**聚合器兜底**。如果直接路由和两跳路由都失败或者报价不佳，我们降级到第三方聚合器。在 Solana 上是 Jupiter 和 AlphAggregator，在 EVM 上是 1Inch、ParaSwap、OKX DEX、Odos、OpenOcean 等。聚合器的优势是它们有更复杂的路由算法，可能找到三跳甚至四跳的路径，覆盖几乎所有可交易的 Token 对。但代价是：调用延迟更高（通常 1-3 秒）、Gas 消耗更大、依赖第三方 API 的稳定性。

降级过程中还有**熔断机制**。如果某个 DEX 在短时间内连续失败超过阈值（比如 5 分钟内失败 10 次），我们会暂时将它标记为不可用，跳过报价阶段直接进入下一级。等到冷却期过后（比如 5 分钟），再用少量请求试探它是否恢复。这样避免了一个故障的 DEX 拖慢整体响应时间。

在 EVM 链上还有一个特殊的降级逻辑：如果所有链上 DEX（UniSwap、PancakeSwap 等）的直接路由都不可用，我们会优先尝试 1Inch 聚合器，因为它的路由能力最强；如果 1Inch 也超时或返回错误，再降级到 ParaSwap；最后兜底是 OpenOcean。每一级降级都会记录日志和监控指标，方便我们分析降级原因和频率。

### 1.7 "贿赂服务是什么？为什么需要？"

贿赂服务（Bribe Service）是 Solana 生态中一种特有的交易加速机制，本质上是通过给 validator（验证者）支付额外费用来获得交易的优先打包权。

要理解为什么需要贿赂服务，首先要了解 Solana 的交易处理机制。Solana 和以太坊不同，它没有一个公开的 mempool（交易待处理池）。在 Solana 上，交易直接发送给当前 slot 的 leader 节点，leader 按照一定规则将交易排入区块。虽然 Solana 有原生的优先费机制（通过 ComputeUnitPrice 设置），但在网络拥堵时，仅靠优先费可能还是不够——你的交易可能被 leader 丢弃，因为处理队列已满。

贿赂服务解决的是"可靠到达 + 优先处理"两个问题。它们维护着与大量 validator 的私有连接通道，当你通过贿赂服务发送交易时，交易会通过专用通道直接送达当前和下几个 slot 的 leader 节点，绕过了常规 RPC 的转发链路。同时，贿赂服务会在你的交易中附带一笔 tip 转账（通常是几千到几万 lamports），直接支付给 leader 的钱包地址，激励 leader 优先打包你的交易。

我们接入了 5 个贿赂服务商，每个有不同的特点和覆盖区域。**NextBlock** 在 Tokyo、Frankfurt、New York、London、SLC 五个地区部署了 CDN 节点，覆盖全球主要的 validator 集群地理位置，延迟低。**Temporal** 同样有全球 CDN（SGP/AMS/TYO/EWR/FRA2），它的特点是稳定性好，是我们的主力服务商之一。**ZeroSlot** 专注于低滑点场景，适合大额交易。**BlockRazor** 支持两种模式：fast 模式追求最快打包速度，sandwichMitigation 模式额外提供防三明治攻击保护。**BlockRush** 主打高吞吐量，适合批量发送交易。

我们的发送策略是**多通道并发**。每笔交易同时通过多个贿赂服务商发送，只要任意一个通道成功把交易打包进区块就算成功。这样做的好处有三个：提高成功率（某个服务商暂时不可用不影响交易）、降低延迟（不同服务商到不同 validator 的延迟不同，并发发送取最快的那个）、以及容灾（我们曾经遇到过某个贿赂服务商宕机 30 分钟的情况，多通道策略保证了业务完全不受影响）。

贿赂费用在整个交易费用中占比不大但效果显著。以一笔普通的 Solana Swap 交易为例，基础费约 5000 lamports（约 $0.001），优先费约 10000-100000 lamports（约 $0.002-$0.02），贿赂费约 10000-50000 lamports（约 $0.002-$0.01）。总费用在 $0.005-$0.03 之间，但换来的是交易在 1-2 个 slot（约 0.4-0.8 秒）内被确认，而不用担心被丢弃或等待很久。

---

## 二、架构设计 Q&A

### 2.1 "你们的跨链代码是怎么复用的？"

我们采用的是三层代码抽象架构，这是我在做了一年多的多链 DEX 开发后总结出来的核心设计模式。

最底层是 **irwallet 通用框架层**（我们项目中用 dexwallet 来对标）。它有 30800 行 Go 代码、233 个文件，定义了所有跨链通用的接口和数据模型。核心接口包括：SwapBuilder（构建 Swap 交易）、PoolManager（池子管理与缓存）、Aggregator（多 DEX 聚合与路由）、EventParser（链上事件解析）、BribeService（贿赂/优先费服务）、TxSender（交易发送与确认）、Alarm（监控告警）。通用实现包括：bigint（精确金额计算，禁止 float64）、coinset（链配置加 FeatureGate）、ratelimiter（令牌桶限流）、LRU 缓存、数据模型（交易/余额/池子，共 5578 行）。这一层完全不包含任何链特定逻辑。

中间层是**链特定实现层**。solwallet（200K+ 行代码）实现 irwallet 定义的所有接口，增加 Solana 特有的逻辑：指令组装、ALT（Address Lookup Table）管理、ComputeBudget 设置、Token2022 支持、Borsh 反序列化、5 个贿赂服务商集成。evmwallet（800+ 个文件）同样实现 irwallet 接口，增加 EVM 特有的逻辑：ABI 编码、Gas 估算（EIP-1559）、Approve 授权流程、Anti-MEV RPC 节点、RBF 交易替换。这两个项目的业务主流程是完全一致的（因为共享 irwallet 的接口定义），只是具体的链交互细节不同。

最上层是 **DEX 协议层**。每个具体的 DEX（Raydium、Pump.fun、UniSwap、PancakeSwap 等）都是一个独立的模块，实现链特定层定义的 DEX 接口。新增一个 DEX 只需要实现 Quote（获取报价）和 BuildSwapTx（构建交易）两个核心方法，然后在工厂中注册即可。

举一个具体的复用例子：Aggregator 的并发报价逻辑。在 irwallet 层，Aggregator 的实现是这样的——接收一个 SwapRequest，遍历注册的 DEX 列表，为每个 DEX 启动一个 goroutine 调用 Quote 方法，收集所有返回的报价，按优先级和输出金额排序，选出最优的一个。这套逻辑对 Solana 和 EVM 完全一致，唯一的区别是注册的 DEX 列表不同：Solana 注册了 Raydium/Pump.fun/Jupiter 等 17+ 个 DEX，EVM 注册了 UniSwap/PancakeSwap/Curve 等 30+ 个 DEX。通过这种方式，聚合逻辑只写了一份代码，但同时服务于所有链。

### 2.2 "irwallet 是怎么从 solwallet 和 evmwallet 中抽取出来的？"

irwallet 的诞生有一个真实的演进过程，不是一开始就设计好的，而是在实践中逐步提炼出来的。

最初，solwallet 和 evmwallet 是两个完全独立的项目，各自有自己的数据模型、Syncer 同步逻辑、签名客户端、监控告警实现。随着维护时间的增长，我们发现了越来越多的重复代码。最典型的例子是 Swap 交易的数据模型——solwallet 有一个 SolanaSwapTx 结构体，evmwallet 有一个 EvmSwapTx 结构体，但核心字段几乎一模一样：卖出币种（sell_symbol）、买入币种（buy_symbol）、滑点（slippage）、优先费（priority_fee）、交易状态（status）、时间戳等。两边各自维护一份，每次加新字段都要改两个地方，还容易遗漏。

Syncer 同步主循环也是一样的。两个项目的 Syncer 都遵循相同的流程：轮询最新区块高度 -> 获取区块数据 -> 遍历交易 -> 分发给解析器 -> 更新数据库 -> 推送 Kafka 消息。这个主循环逻辑完全一致，不同的只是"获取区块数据"和"解析交易"这两步的具体实现。

签名客户端更是完全相同——两边都调用同一个 ksrv 服务做远程签名，TLS 双向认证的配置、签名请求的序列化、重试逻辑完全一样，但代码各写了一份。监控告警也是如此，钉钉/Lark 的消息格式、发送逻辑、告警去重规则都是一样的。

抽取过程分三步走。第一步是**定义接口**。我们先分析两个项目的公共逻辑，定义出一组跨链通用的接口：Wallet 接口（NewSwap/GetBalance 等）、Syncer 接口（Run/DealBlockTx 等）、Repository 接口（Account/Transaction/Pool 的 CRUD）、RPCClient 接口（SendTransaction/GetBalance/GetBlockHeight）。接口的参数和返回值都使用通用数据模型，不包含任何链特定的类型。

第二步是**迁移通用实现**。把两个项目中完全相同的代码搬到 irwallet 中：数据模型（5578 行）、Syncer 主循环（31 个文件）、签名客户端（ksrv 包）、消息队列（kafka 包）、监控告警（alarm 包）、布隆过滤器（bloomfilter 包）、数据库迁移（10 个版本）。

第三步是**让链特定项目实现接口**。solwallet 和 evmwallet 各自实现 irwallet 定义的接口，只保留链特定的逻辑。比如 solwallet 实现 RPCClient 接口时，内部调用 Solana 的 getAccountInfo、sendTransaction 等 RPC 方法；evmwallet 实现同一接口时，内部调用 eth_call、eth_sendRawTransaction 等方法。

抽取过程中最关键的取舍是：**哪些东西看起来差不多但其实不该强行统一**。最典型的例子是 Solana 的指令解析和 EVM 的事件解析。表面上看，它们都是"从链上交易中提取业务信息"，功能类似。但底层机制完全不同：Solana 是解析 Transaction 中的 Instructions，按 Program ID 分发到不同的指令解析器，处理的是 Borsh 编码的二进制数据；EVM 是解析 Transaction Receipt 中的 Event Logs，按 topic[0]（事件签名的 keccak256 哈希）匹配解析器，处理的是 ABI 编码的数据。如果强行把这两种机制统一成一个抽象，接口会变得非常别扭，使用方也不方便。所以我们的选择是：在 irwallet 层只定义 EventParser 接口和事件类型枚举（Swap/Transfer/Mint/Burn/Liquidity），让输出的事件模型统一，但解析过程各自实现。

### 2.3 "新增一条 EVM 链要改什么？"

新增一条 EVM 链是成本最低的扩展场景，因为我们的架构已经为此做了充分的参数化设计。

核心改动只在 **coinset 链配置**中添加一条新链的配置项。配置内容包括：链 ID（Chain ID）、链名称、RPC 节点地址列表（主节点 + 备用节点）、区块确认数（比如 BSC 需要 15 个确认、Base 需要 1 个确认）、Gas 特性（是否支持 EIP-1559、BaseFee 系数）、原生资产信息（BNB/ETH/MATIC 等的精度和 Wrapped 地址）、区块间隔时间（用于超时计算）。这些全部是配置项，不需要写任何业务代码。

如果新链的 DEX 生态和现有 EVM 链一样（比如都是 UniSwap V2/V3 fork），那真的是零代码改动。因为 UniSwap V2 和 V3 的合约接口在所有 EVM 链上都是标准的，只是合约部署地址不同。我们只需要在配置中指定新链上各个 DEX Router 合约的地址，SwapBuilder 就能自动构建正确的交易。

如果新链有自己特有的 DEX（比如 BSC 上的 FourMeme、Monad 上的 NAD），那需要额外实现一个新的 DEX 模块。具体来说就是：实现 SwapTxBuilderSelector 接口的 Quote 和 BuildSwapTx 方法、在工厂中注册这个新 DEX、添加对应的 ABI 定义。但这个工作是增量的，不会影响已有的任何代码。

举一个真实的例子：我们接入 Base 链的时候，总共花了大约半天时间。其中大部分时间是在测试 RPC 节点的稳定性和确认区块确认数的最佳配置，真正的代码改动就是添加了一个 ChainConfig 配置项。Base 链上的 UniSwap V3 和 Aerodrome DEX 都可以直接复用 evmwallet 的现有逻辑，因为合约接口是标准的。后来我们额外接入了 Base 上的一个特有 DEX，那才需要写一个新的 DEX 模块，大约 500 行代码。

数据库层面也不需要改动。我们的 swaptx 表结构是链无关的，chain_id 字段区分不同链的数据。Syncer 同步服务只需要为新链启动一个新的实例，连接到新链的 RPC 节点，其他逻辑完全复用。

### 2.4 "新增一条非 EVM 链（比如 TRON）要改什么？"

新增一条非 EVM 链的成本显然比新增 EVM 链高很多，但我们的三层架构已经把这个成本降到了最低。

需要做的工作分为两大块。第一块是**实现 irwallet（dexwallet）层定义的所有接口**。这是主要工作量，具体包括：

RPCClient 接口——需要封装 TRON 的 RPC 调用方式。TRON 使用 gRPC 和 HTTP API，和 Solana/EVM 都不同，需要全新实现。包括获取账户信息、发送交易、查询交易状态、获取区块数据等基础方法。

SwapBuilder 接口——需要实现 TRON 上 DEX 的交易构建逻辑。TRON 上的主要 DEX 是 SunSwap（类似 UniSwap V2 和 V3），需要实现 TRC-20 Token 的 Approve 授权、Swap 合约调用的 ABI 编码（TRON 的 ABI 编码和 EVM 基本兼容但有细微差异）、能量（Energy）和带宽（Bandwidth）的费用计算。

PoolParser 接口——需要实现 TRON 上池子数据的解析。SunSwap 的池子合约接口和 UniSwap 类似，解析逻辑可以参考 evmwallet 的实现。

EventParser 接口——需要实现 TRON 的链上事件解析。TRON 的事件结构和 EVM 类似（也有 event logs 和 topics），但获取方式不同（通过 TRON 特有的事件 API）。

第二块是**复用 irwallet 层的通用逻辑**，这部分不需要写任何代码。Aggregator（并发报价 + 优先级排序 + 降级兜底）、PoolCache（LRU 缓存 + 定时刷新）、TxSender（多通道并发发送 + 超时重试 + 确认追踪）、Monitor（区块高度监控 + 交易超时监控 + 余额异常监控）、数据模型（swaptx/balance/pool 表结构）、签名客户端（ksrv 远程签名）、消息队列（Kafka 推送）、限流器（令牌桶）——这些全部直接复用，零改动。

按我们接入 Solana 和 EVM 两条链的经验估算，新增一条非 EVM 链的工作量大约是：RPC 客户端封装 2000-3000 行、SwapBuilder 3000-5000 行（取决于 DEX 数量）、PoolParser 1000-2000 行、EventParser 2000-4000 行。总计约 8000-15000 行代码。相比之下，irwallet 的 30800 行通用代码不需要任何改动。也就是说，新增一条链只需要编写约 30%-50% 的代码量，剩下的 50%-70% 全部复用。这就是三层抽象带来的实际收益。

另外还需要在 coinset 中添加新链的配置（如 TRON 使用 Tag-based 账户模型，确认数设置等），以及根据 TRON 的特性调整 FeatureGate 开关（比如 TRON 不需要 EnableBloomfilter，但需要 EnableEnergyEstimate）。

### 2.5 "接口设计时最难的决策是什么？"

最难的决策是 SwapRequest 和 SwapResult 这两个核心数据模型的字段设计——既要保证跨链通用性，又不能丢失链特定的关键信息。

难点在于，Solana 和 EVM 的 Swap 交易有非常不同的参数需求。Solana 的交易需要 ComputeUnitLimit（计算单元上限）、ComputeUnitPrice（优先费单价）、AddressLookupTable（ALT 地址列表）、是否需要创建 ATA 等信息。EVM 的交易需要 GasLimit、MaxFeePerGas、MaxPriorityFeePerGas、Nonce、是否需要 Approve、Approve 的 Spender 地址等信息。这些字段完全不重叠，如果全部放在一个扁平的结构体里，会导致每条链只用到一半的字段，另一半永远是零值，代码的可读性和维护性都很差。

我们最终的解决方案是**通用字段 + 链特定扩展**的组合模式。SwapRequest 的通用字段包括：FromAddress（发送方地址，string 类型兼容所有链的地址格式）、SellToken/BuyToken（卖出/买入的 Token 标识）、Amount（交易金额，使用 big.Int 保证精度）、Slippage（滑点百分比）、DexID（指定 DEX，可选）。SwapResult 的通用字段包括：TxData（待签名的交易原始数据，[]byte 类型）、EstimatedOutput（预估输出金额）、ActualDex（实际使用的 DEX）、Fee（交易费用，统一转换为 USD 计价方便比较）。

链特定字段通过两种方式扩展。第一种是 `Extra map[string]interface{}` 通用扩展字段，适合偶尔需要传递的非核心参数。第二种是定义链特定的子结构体，比如 SolanaSwapExtra 和 EvmSwapExtra，里面放各自链需要的字段，然后通过类型断言来使用。在实践中，我们发现第二种方式更好用，因为有编译时类型检查，不容易写错字段名。

另一个难决策是 Pool 数据模型。Solana 上的 AMM 池子（比如 Raydium AMM）用 base_vault/quote_vault 两个 Token Account 存储流动性，CLMM 池子（比如 Raydium CLMM）用 tick_array 存储离散的价格区间流动性。EVM 上的 UniSwap V2 池子用 reserve0/reserve1 两个 uint256 存储流动性，UniSwap V3 用 sqrtPriceX96 和 liquidity 表示当前价格和流动性。这些数据格式差异很大，但我们需要一个统一的 Pool 模型来支撑 Aggregator 的比价逻辑。最终我们把 Pool 模型设计为：核心字段用于比价和排序（pool_address、token_pair、total_liquidity、fee_rate、dex_id、state），链特定的原始数据存在 RawData []byte 字段中，需要时反序列化回链特定的格式。total_liquidity 字段统一用 USD 计价，这样不同链、不同 DEX 的池子可以直接比较流动性深度。

### 2.6 "irwallet 框架有多少行代码？维护成本如何？"

irwallet 框架总共 30800 行 Go 代码，分布在 233 个 Go 文件中。从模块划分来看：数据模型（datamodel）最大，有 5578 行，定义了交易、余额、池子等所有业务实体，使用 Protocol Buffers 做序列化以保证跨语言兼容性和高效传输；Repository 数据持久化接口有 11 个文件，定义了 Account、Transaction、Token、Pool 等实体的 CRUD 接口；Syncer 通用同步逻辑有 31 个文件，实现了区块轮询、交易分发、重组检测、超时恢复等完整的同步主循环；其余还有 coinset（链配置和 FeatureGate）、kafka（消息队列集成）、ksrv（签名服务客户端）、bloomfilter（地址过滤）、exchange（交易所 API）、alarm（告警接口）等模块。

维护成本我们从几个维度来衡量。首先是**代码质量保障**：我们使用 golangci-lint 做静态代码检查，配置了 40+ 种检查规则，包括 errcheck（确保所有 error 都被处理）、govet（检测常见的 Go 错误模式）、staticcheck（高级静态分析）、gosec（安全漏洞检测）等。每次 PR 合并前必须通过所有检查，确保代码质量不退化。

其次是**文档覆盖**：irwallet 项目有 9 个详细的技术文档，覆盖了架构设计、接口说明、数据模型说明、部署指南、运维手册等所有方面。新人入职后通过阅读这些文档，大约一周就能理解整体架构并开始提交代码。

然后是**变更频率**：由于 irwallet 是被 solwallet 和 evmwallet 两个项目共同依赖的，任何改动都要考虑向后兼容性。我们遵循的原则是"接口只增不改"——如果需要新功能，添加新的接口方法而不是修改已有方法的签名。数据库 schema 也是如此，已经有 10 个迁移版本，每次都是增量变更。实际上 irwallet 的变更频率不高，大约每月 2-3 个 PR，多数是新增字段或者优化通用逻辑的性能。

最后是**依赖管理**：solwallet 和 evmwallet 通过 Go module 依赖 irwallet 的特定版本。当 irwallet 有更新时，两个项目各自评估是否需要升级，不会被强制同步更新。这种松耦合的依赖关系让维护成本保持在可控范围内。

---

## 三、场景题

### 3.1 "用户 swap 1000 USDT 买某 meme 币，描述完整链路"

好的，我用一个具体的例子来走一遍完整链路。假设用户在 Solana 上要用 1000 USDT 买一个叫 PEPE2 的 meme 币。

首先，API 层收到 Swap 请求，参数包括：from_address（用户的 Solana 钱包地址）、sell_token（USDT 的 Mint 地址）、buy_token（PEPE2 的 Mint 地址）、amount（1000，单位是 USDT 的最小单位，即 1000 * 10^6 = 1_000_000_000）、slippage（比如 5%，meme 币通常需要较高的滑点）。

**预检查阶段**：系统验证用户账户是否存在且有效、USDT 余额是否 >= 1000、PEPE2 是否在我们的支持列表中（如果是全新的 Token，需要先通过安全检查确认不是诈骗合约）。预检查通过后，进入业务主流程。

**并行数据获取阶段**：系统并发发起 4 组 RPC 调用。(1) 获取用户的 USDT Token Account 余额，确认链上余额和数据库记录一致；(2) 获取当前的优先费推荐值（从最近几个区块的交易中统计 ComputeUnitPrice 的 P50 和 P75）；(3) 查询 PEPE2 的 Token 元数据（精度、是否 Token2022、是否有转账税）；(4) 查询所有有 USDT/PEPE2 或者 PEPE2/SOL 交易对的流动性池子。

**DEX 选择阶段**：这里有一个关键判断——PEPE2 这个 meme 币当前处于什么阶段？如果它还在 Pump.fun 的 Bonding Curve 阶段（未毕业），那就走 Pump.fun 的内盘交易，但注意 Pump.fun 只支持 SOL 计价，所以需要先把 USDT 换成 SOL（通过 Raydium 的 USDT/SOL 池子），再用 SOL 在 Pump.fun 买 PEPE2，这就是一个两跳路由。如果 PEPE2 已经毕业到了 Raydium AMM，系统并发调用 Raydium AMM、Raydium CPMM、Meteora 等所有有 PEPE2 池子的 DEX 获取报价。假设 Raydium AMM 给出最优报价：1000 USDT 可以获得 5_000_000 个 PEPE2（扣除 0.25% 手续费后）。如果所有直接路由和两跳路由都不行，降级到 Jupiter 聚合器兜底。

**构建交易阶段**：假设选中了 Raydium AMM。系统构建指令列表：(1) SetComputeUnitLimit 指令，设置为 200000 CU（Raydium AMM swap 通常消耗约 150000 CU，留一些余量）；(2) SetComputeUnitPrice 指令，设置优先费单价（假设当前推荐值为 50000 micro-lamports）；(3) 检查用户是否有 PEPE2 的 ATA，如果没有就加一个 CreateAssociatedTokenAccount 指令；(4) Raydium AMM 的 Swap 指令，包含池子地址、输入金额、最小输出金额（5_000_000 * 95% = 4_750_000，即扣除 5% 滑点）。如果所有指令加起来引用的 Account 太多导致交易超过 1232 字节，还需要使用 ALT 压缩。

**模拟执行阶段**：把构建好的交易发送到 Solana RPC 的 simulateTransaction 方法。它会模拟执行这笔交易并返回结果：如果成功，返回消耗的 CU 数量和执行后的账户状态变化；如果失败，返回错误信息（比如"滑点超限"、"流动性不足"）。我们检查模拟结果，确认输出的 PEPE2 数量 >= 4_750_000（满足 5% 滑点要求），然后用实际消耗的 CU 数量更新 ComputeUnitLimit（避免设置过高浪费费用）。

**签名阶段**：把最终的交易数据发送到 ksrv 签名服务，通过 TLS 双向认证的安全通道。ksrv 在内部使用 Ed25519 算法（Solana 使用的签名算法）完成签名，返回 64 字节的签名数据。我们把签名附加到交易中。

**发送阶段**：通过 5 个贿赂服务商（NextBlock/Temporal/ZeroSlot/BlockRazor/BlockRush）并发发送已签名的交易，附带贿赂费（比如 30000 lamports 的 tip）。同时也通过普通 RPC 节点发送一份作为备份。

**确认追踪阶段**：开始轮询 getSignatureStatuses，等待交易被确认。通常在 1-3 个 slot（0.4-1.2 秒）内就能确认。确认后，更新数据库中的 swaptx 记录状态为 confirmed，记录实际输出金额和费用消耗，推送交易结果到 Kafka 通知下游服务。

整条链路从收到请求到交易确认，正常情况下在 2-5 秒内完成。

### 3.2 "某 DEX 池子突然流动性归零，系统如何应对？"

这是一个在 meme 币交易中经常遇到的场景，我们有多层防护机制来应对。

**第一层：实时检测**。我们的 Syncer 服务持续同步链上区块数据。当检测到某个池子的 RemoveLiquidity 或 Withdraw 事件导致池子流动性降到阈值以下（比如 $100 以下）时，立即更新池子状态为 Inactive，并从 LRU 缓存中标记该池子不可用。在 Solana 上如果接入了 Helius LaserStream，可以在 200-300ms 内感知到链上状态变化；没有实时推送的情况下，依赖区块轮询的延迟大约在 1-3 个 slot（0.4-1.2 秒）。

**第二层：Swap 前置校验**。即使缓存中的池子状态还没来得及更新，Swap 构建流程中有两道防线。第一道是 Quote 阶段——获取报价时会读取池子的实时流动性数据，如果流动性为零，Quote 方法会返回错误或者一个滑点极大的报价，自然被排序到最后。第二道是模拟执行阶段——即使 Quote 报价通过了，simulateTransaction 会在链上状态上执行模拟，如果池子确实没有流动性，模拟会返回"insufficient liquidity"错误，交易会被拦截。

**第三层：降级路由**。如果用户要交易的 Token 在主力 DEX 的池子流动性归零了，系统的聚合器会自动尝试其他 DEX。比如 Raydium AMM 的池子没流动性了，但 Meteora 或 PumpAMM 上还有另一个池子有流动性，系统会自动路由到那里。如果所有直接路由的池子都没流动性，聚合器会尝试两跳路由或者降级到 Jupiter 等第三方聚合器，它们可能知道一些我们没有覆盖的池子。

**第四层：监控告警**。当某个池子的流动性发生剧烈变化（比如 1 分钟内下降超过 80%），监控系统会触发告警，通知运维团队。这种情况可能意味着：项目方跑路（Rug Pull）、大户撤出流动性、或者池子合约被攻击。运维人员收到告警后会评估风险，如果确认是 Rug Pull，会手动将该 Token 加入黑名单，阻止后续交易。告警渠道是钉钉/Lark 群消息，严重告警（比如涉及金额超过 $10000 的池子异常）会触发 PagerDuty 电话告警。

**第五层：用户反馈**。如果交易最终因为流动性不足而失败，系统返回给用户的错误信息会明确说明原因："交易失败：目标池子流动性不足，请稍后重试或降低交易金额"。不会返回含糊的错误信息。同时在 swaptx 记录中标记失败原因，方便后续数据分析。

### 3.3 "链上拥堵导致交易一直 pending，怎么处理？"

链上拥堵是我们在热点时段（比如某个 meme 币暴涨时）经常遇到的问题，我们有一套完整的超时处理和加速机制。

**Solana 上的处理方式**。Solana 的交易有一个天然的超时机制——每笔交易包含一个 recent_blockhash，这个 blockhash 的有效期大约是 150 个 slot（约 60 秒）。如果交易在 60 秒内没有被打包，它自动失效，不会永远 pending。所以 Solana 上的策略是：发送交易后启动一个超时计时器，每隔 2 秒轮询一次 getSignatureStatuses。如果 30 秒内未确认，我们认为可能需要加速，重新获取最新的 blockhash 和更高的优先费，构建一笔新的交易重新发送。如果 60 秒仍未确认，原交易自动失效，我们用新的 blockhash 重建交易并通过贿赂服务发送（这次会设置更高的 tip）。整个过程对用户是透明的，用户只需要等待最终结果。

**EVM 上的处理方式**。EVM 链上的交易不会自动过期，一旦进入 mempool 就会一直等待被打包（除非 Nonce 被其他交易覆盖）。所以我们使用 RBF（Replace-By-Fee）机制来加速。具体做法是：发送交易后启动超时监控（不同链的超时阈值不同，BSC 约 30 秒、Ethereum 约 120 秒）。如果超时未确认，系统会构建一笔相同 Nonce 的替换交易，Gas Price 提高 10%-20%（确保高于原交易的 Gas Price，否则替换交易不会被节点接受），然后重新发送。如果连续两次加速仍未确认，再次提高 Gas Price（比如提高 50%）。最多重试 3 次加速，如果仍未确认，标记交易为 timeout 状态并告警。

**超时监控系统**。irwallet 的 Syncer 模块有一个专门的 TimeoutMonitor 组件，它定期扫描所有状态为 pending 的交易，检查它们的等待时间是否超过了阈值。对于超时交易，它会触发两个动作：一是尝试自动加速（如上述），二是发送告警通知运维团队。如果超时交易的数量突增（比如 5 分钟内超过 20 笔），说明链上可能出现了全面拥堵，这时候监控系统会触发"链拥堵"级别的告警，运维团队可能需要临时提高全局的优先费/Gas Price 配置。

**限流背压机制**。当检测到链上拥堵时，我们的限流器会自动降低 Swap 请求的处理速率。正常情况下限流阈值是 20 TPS、60 个 Worker，拥堵时会动态降低到 10 TPS、30 个 Worker，避免大量交易堆积在链上。同时，等待队列超过 500 时，新请求会被直接拒绝并返回"系统繁忙，请稍后重试"。

**Nonce 管理**（EVM 特有）。EVM 上还有一个特殊的问题——如果一笔交易 pending 太久，后续交易（Nonce 更高的）也会被卡住。我们维护了一个 Nonce 管理器，实时追踪每个地址的 pending Nonce。如果检测到 Nonce 间隙（gap），会优先处理卡住的那笔交易（加速或取消），释放后续交易的通道。

### 3.4 "新上线一个 DEX 协议，如何灰度接入？"

灰度接入新 DEX 是我们在生产中频繁进行的操作，我们有一套标准化的流程来确保安全上线。

**第一步：协议研究和开发**（1-3 天）。首先研究新 DEX 的合约接口、文档、SDK。了解它的 Swap 方法签名、费率结构、池子数据格式。然后实现 DEX 模块的代码：Quote 方法（获取报价）和 BuildSwapTx 方法（构建交易）。同时实现对应的事件解析器（用于 Syncer 解析这个 DEX 的链上交易）。完成后在测试网或者 devnet 上做基本功能验证。

**第二步：配置灰度参数**。在链配置中为新 DEX 添加一个条目，设置 grayscale_percentage 为 0（初始关闭）。配置项还包括：DEX 的 Router 合约地址、费率、支持的交易对列表、优先级等级。由于 grayscale_percentage 是 0，即使部署了代码，新 DEX 也不会参与任何交易。

**第三步：内部测试**（1-2 天）。把 grayscale_percentage 设为 100，但只在测试环境生效。用测试账户执行各种场景的 Swap：正常交易、大额交易、小额交易、冷门交易对。检查报价准确性（和链上实际执行结果对比）、Gas 消耗合理性、错误处理是否正确。同时用 mock 数据跑聚合器逻辑，确认新 DEX 的报价能正确参与排序和比较。

**第四步：灰度发布**。在生产环境中逐步提高 grayscale_percentage。第一阶段设为 5%，观察 24 小时。这意味着只有 5% 的 Swap 请求会尝试新 DEX 的报价。我们监控以下指标：新 DEX 的 Quote 成功率（应该 > 95%）、交易成功率（应该 > 90%）、报价偏差（和其他 DEX 的报价对比，偏差不应超过 5%）、平均延迟（不应明显高于其他 DEX）。如果 24 小时内指标正常，提高到 20%，再观察 24 小时；然后提高到 50%，再观察；最后提高到 100%。

**第五步：正式上线**。灰度期间如果发现问题（比如报价异常、交易频繁失败），可以随时把 grayscale_percentage 设回 0，立即止血。问题修复后再重新灰度。如果一切正常，把灰度配置改为 enabled: true（永久启用），移除灰度逻辑，完成正式上线。

灰度的具体实现方式很简单：Aggregator 在遍历 DEX 列表获取报价时，对每个处于灰度阶段的 DEX，生成一个 0-100 的随机数，如果随机数 < grayscale_percentage 才调用它的 Quote 方法，否则跳过。这样可以精确控制新 DEX 被使用的比例。

### 3.5 "solwallet 和 evmwallet 的公共代码出现 bug，修复流程是什么？"

这个问题涉及我们的代码组织和多项目协作流程，是三层架构设计的一个实际运维场景。

**定位 bug 位置**。首先确认 bug 出在哪一层。如果 bug 影响了 solwallet 和 evmwallet 两个项目（比如两边都出现了相同的数据模型解析错误），那大概率是 irwallet 层的问题。如果只影响一个项目，那是链特定层的问题。具体的定位方法：查看两个项目的错误日志，如果错误栈（stack trace）指向了 irwallet 的包路径（比如 github.com/xxx/irwallet/datamodel/...），就确认了是 irwallet 层的 bug。

**在 irwallet 项目中修复**。在 irwallet 仓库中创建修复分支，修改 bug 代码，编写对应的单元测试确保修复有效且不引入新问题。由于 irwallet 有 golangci-lint 的 40+ 种检查规则，PR 必须通过所有检查。修复后发一个新版本（比如从 v1.3.5 升到 v1.3.6），遵循语义化版本规范——如果是 bug fix 且不改变接口，递增 patch 版本号。

**升级下游项目**。solwallet 和 evmwallet 各自更新 go.mod 中对 irwallet 的依赖版本（go get github.com/xxx/irwallet@v1.3.6）。然后在各自的项目中运行完整的测试套件，确保升级后所有功能正常。如果 bug 修复涉及数据库 schema 变更，还需要在各自项目中执行数据库迁移。

**部署流程**。先部署到 staging 环境，用真实流量做回归测试。确认没有问题后，灰度部署到生产环境——通常先部署一个实例，观察 30 分钟，然后滚动部署到所有实例。由于 solwallet 和 evmwallet 是独立部署的服务，它们可以各自按节奏升级，不需要同时部署。

**特殊情况处理**。如果 bug 很紧急（比如导致交易失败或资金安全问题），我们有 hotfix 流程：直接在 irwallet 的 main 分支上修复并打 tag，跳过常规的 code review 流程（但事后补充）。下游项目立即升级并热部署。如果是周末或深夜发现的问题，PagerDuty 会自动通知 on-call 人员。

**预防措施**。为了减少公共代码 bug 的影响面，我们有几个实践：(1) irwallet 的每次变更都要在 solwallet 和 evmwallet 的 CI 中跑集成测试（通过 CI 的 downstream test 触发）；(2) 核心模块（数据模型、Syncer 主循环、签名客户端）的变更需要两个人 review；(3) irwallet 的发布节奏是固定的（每两周一个 minor 版本），紧急修复除外。

---

## 四、深挖题

### 4.1 "AMM 的恒定乘积公式推导"

恒定乘积做市商（Constant Product Market Maker，CPMM）的核心公式是 `x * y = k`，其中 x 和 y 分别是池子中两种 Token 的储备量，k 是一个常数。这个公式最早由 Uniswap V2 提出，也被 Raydium AMMv4、PancakeSwap V2 等 DEX 采用。

**价格推导**。在任意时刻，Token A 相对于 Token B 的价格 P = y / x。这个很好理解——如果池子里有 100 个 Token A 和 200 个 Token B，那一个 Token A 的价格就是 2 个 Token B。

**交易计算**。假设用户要用 dx 个 Token A 换取 Token B。交易后池子的新状态是：(x + dx) * (y - dy) = k = x * y。解这个方程得到 dy = y * dx / (x + dx)。这就是用户实际得到的 Token B 数量。注意分母是 (x + dx) 而不是 x，这意味着随着交易量 dx 增大，每单位 dx 换到的 dy 越少——这就是滑点的数学来源。

**滑点计算**。理想情况下（无滑点），dx 个 Token A 应该换到 dx * (y/x) 个 Token B（按当前价格）。但实际只换到了 y * dx / (x + dx)。滑点 = 1 - 实际输出/理想输出 = 1 - [y * dx / (x + dx)] / [dx * y / x] = 1 - x / (x + dx) = dx / (x + dx)。所以滑点只取决于交易量 dx 和池子储备量 x 的比值。当 dx 远小于 x 时，滑点趋近于 0；当 dx = x 时，滑点 = 50%。这也解释了为什么大额交易需要深度更好（储备量更大）的池子。

**手续费影响**。实际的 AMM 在交易前会扣除一部分手续费。比如 Uniswap V2 的手续费是 0.3%。扣除手续费后，实际参与交易的金额是 dx_effective = dx * (1 - fee_rate) = dx * 0.997。然后用 dx_effective 代入上面的公式计算输出。手续费留在池子中，增加了 k 值，这就是流动性提供者（LP）的收益来源。所以准确的公式是：dy = y * dx * (1 - fee) / (x + dx * (1 - fee))。

**无常损失**。LP 把资金放入池子做市，如果价格发生变化，LP 持有的资产价值会低于简单持有（hold）的价值，这个差值就是无常损失（Impermanent Loss）。数学上，如果价格变化比率为 r（即新价格 / 原价格），无常损失 = 2 * sqrt(r) / (1 + r) - 1。当 r = 1（价格不变）时无常损失为 0；当 r = 4（价格翻倍）时无常损失约 5.7%；当 r = 0.25（价格跌 75%）时无常损失约 20%。这就是为什么 meme 币池子的 LP 风险极高——meme 币价格波动巨大，无常损失可能远超手续费收益。

### 4.2 "CLMM 的 Tick 机制和集中流动性原理"

CLMM（Concentrated Liquidity Market Maker，集中流动性做市商）是 Uniswap V3 提出的改进方案，也被 Raydium CLMM、PancakeSwap V3 等 DEX 采用。核心创新是让 LP 可以选择在特定的价格区间内提供流动性，而不是像 V2 那样在 0 到无穷大的全价格范围内平均分布。

**Tick 的定义**。价格空间被离散化为一系列 Tick，每个 Tick 对应一个价格。Tick 和价格的关系是：price = 1.0001^tick。也就是相邻两个 Tick 之间的价格差异约为 0.01%（1 个基点）。Tick 的范围通常从 -887272 到 +887272，覆盖了极端的价格范围。实际使用中，Tick 按 tickSpacing 分组（比如 tickSpacing = 60 表示每隔 60 个 Tick 才是一个有效的流动性边界），以减少 Gas 消耗。

**集中流动性的原理**。在传统 AMM 中，LP 的资金在整个价格范围 (0, +inf) 内均匀分布，但实际交易只发生在当前价格附近的一小段区间内，大部分资金是闲置的。CLMM 让 LP 选择一个价格区间 [Pa, Pb] 来提供流动性。如果当前价格在这个区间内，LP 的资金就在"工作"；如果价格移出这个区间，LP 的资金就变成了纯单一 Token（不再赚取手续费）。

**资金效率提升**。集中流动性的核心好处是资金效率大幅提升。假设 ETH/USDC 当前价格是 2000，在 V2 中 LP 需要在 (0, +inf) 提供流动性，但实际上 99% 的交易发生在 1800-2200 的范围内。在 CLMM 中，LP 可以只在 1800-2200 的范围内提供流动性，同样的资金量能获得约 10 倍的手续费收益。反过来说，提供同样的流动性深度，CLMM 只需要 V2 十分之一的资金。Uniswap V3 白皮书中给出的数据是，对于稳定币对（比如 USDC/USDT），集中流动性的资金效率可以提升 4000 倍。

**交易执行过程**。当用户发起一笔 Swap 时，交易在当前 Tick 对应的流动性上执行。如果交易量很大，会"穿越"多个 Tick。每穿越一个 Tick 边界，都需要更新活跃流动性（有些 LP 的区间在这个 Tick 开始或结束，需要加入或移除他们的流动性），这就是所谓的"Tick Crossing"。Tick Crossing 的 Gas 消耗比较高，这也是为什么 CLMM 的大额交易 Gas 成本通常高于传统 AMM。

**sqrtPriceX96 表示法**。在合约中，价格不是直接存储为浮点数（区块链不支持浮点运算），而是以 sqrtPriceX96 = sqrt(price) * 2^96 的形式存储为一个大整数。用平方根是为了简化流动性计算公式，乘以 2^96 是为了用定点数精确表示。我们在代码中需要正确处理这种高精度数学运算，全部使用 big.Int，禁止 float64。

**对我们系统的影响**。CLMM 池子的数据结构比 AMM 复杂得多。除了基本的池子信息外，还需要解析 TickArray 数据（在 Solana 上是独立的 Account，在 EVM 上是合约 Storage 中的 mapping），获取每个 Tick 的流动性分布。我们的 PoolParser 对 CLMM 池子要做更多的解析工作，GetBestPool 的评分算法也需要考虑当前价格附近的实际集中流动性，而不仅仅是总流动性。

### 4.3 "Solana 的 Compute Unit 和优先费计算"

Solana 的费用模型与 EVM 完全不同，理解它的细节对于优化交易成本和成功率至关重要。

**Compute Unit（CU）基础**。Solana 上每笔交易消耗一定数量的 Compute Unit，类似于 EVM 的 Gas。每笔交易默认的 CU 上限是 200,000，但可以通过 SetComputeUnitLimit 指令自定义（上限为 1,400,000）。如果交易实际消耗的 CU 超过设置的 Limit，交易会失败（类似 EVM 的 out of gas）。不同的操作消耗不同数量的 CU，比如一次简单转账约消耗 3000-5000 CU，一次 Raydium AMM Swap 约消耗 100,000-200,000 CU，一次 Jupiter 聚合路由可能消耗 300,000-800,000 CU。

**基础费**。Solana 的基础费是固定的：每个签名（signature）5000 lamports。一笔普通交易只有一个签名，所以基础费就是 5000 lamports（约 $0.001）。这个费用不管交易是否成功都要支付。

**优先费计算**。优先费通过两个 ComputeBudget 指令来设置：SetComputeUnitLimit（设置 CU 上限）和 SetComputeUnitPrice（设置每 CU 的价格，单位是 micro-lamports，即 10^-6 lamports）。优先费的计算公式是：priority_fee = ComputeUnitPrice * ComputeUnitLimit / 10^6（换算为 lamports）。比如 ComputeUnitPrice = 50000 micro-lamports，ComputeUnitLimit = 200000 CU，那优先费 = 50000 * 200000 / 10^6 = 10000 lamports（约 $0.002）。

**动态推荐**。我们不使用固定的优先费，而是根据链上实时情况动态推荐。具体做法是每 2 秒查询一次 getRecentPrioritizationFees RPC 方法，获取最近 150 个 slot 中交易的优先费分布。我们取 P50（中位数）作为"正常"推荐值，P75 作为"快速"推荐值。在链上拥堵时（检测到区块利用率超过 80%），自动切换到 P75 甚至 P90 的推荐值，确保交易能被优先打包。

**CU Limit 优化**。一个重要的优化点是不要把 CU Limit 设得太高。虽然未使用的 CU 不会被收费（Solana 的优先费是按 CU Limit 而非实际消耗来计算的——更新：从 2024 年起 Solana 会按实际消耗计费，但验证者调度仍参考 CU Limit），设置过高的 CU Limit 会降低交易被打包的优先级（验证者会考虑单位 CU 的费用）。所以我们的做法是先用 simulateTransaction 获取交易实际消耗的 CU 数量，然后设置 CU Limit 为实际消耗的 1.2 倍（留 20% 余量）。这样可以在保证成功率的同时，最大化单位 CU 的优先费，提高被打包的优先级。

**贿赂费与优先费的关系**。贿赂费（tip）是额外支付给 leader 的费用，与优先费是独立的。贿赂费通常通过一笔额外的 SOL 转账指令实现（转账到贿赂服务商指定的 tip 账户）。优先费由协议层处理，贿赂费由贿赂服务商转发给 leader。两者叠加使用可以获得最佳的打包速度。我们的经验是：优先费主要影响在标准交易队列中的排序，贿赂费则是走私有通道直达 leader，两者的作用机制不同但效果互补。

### 4.4 "EVM 的 EIP-1559 gas 模型"

EIP-1559 是 2021 年以太坊伦敦升级引入的新 Gas 费用机制，彻底改变了交易费用的计算方式。目前所有主要的 EVM 链（Ethereum、BSC、Base、Polygon 等）都已采用这个模型。

**旧模型的问题**。在 EIP-1559 之前，Gas 费用模型很简单：用户指定一个 GasPrice，矿工按 GasPrice 从高到低排序打包交易。问题是用户很难估准 GasPrice——出价太低交易会长时间 pending，出价太高又浪费钱。而且 GasPrice 在网络拥堵时会剧烈波动，用户体验很差。

**EIP-1559 的双组件模型**。EIP-1559 把 Gas 费用拆成两部分：BaseFee（基础费）和 MaxPriorityFee（小费/优先费）。用户在交易中指定两个参数：MaxFeePerGas（愿意支付的最高每单位 Gas 费用）和 MaxPriorityFeePerGas（愿意给矿工的小费上限）。实际支付的费用 = min(MaxFeePerGas, BaseFee + MaxPriorityFeePerGas) * GasUsed。

**BaseFee 的动态调整**。BaseFee 是协议层自动调整的，用户无法控制。调整规则是：如果上一个区块的 Gas 使用量超过目标值（区块 Gas Limit 的 50%），BaseFee 上升；如果低于目标值，BaseFee 下降。每个区块的 BaseFee 变化幅度最多为上一个区块的 12.5%。这个机制让 BaseFee 能平滑地跟踪网络拥堵程度。在以太坊上，正常时段 BaseFee 约 10-30 Gwei，拥堵时可以飙升到 100-500 Gwei。关键点：BaseFee 部分会被销毁（burn），不会给矿工，这是 EIP-1559 的另一个重要特性——通过销毁机制减少 ETH 的总供应量。

**MaxPriorityFee 的作用**。MaxPriorityFee 是真正给矿工/验证者的小费，矿工会按这个值排序，小费高的交易优先打包。通常 MaxPriorityFee 在 1-5 Gwei 之间就足够了（以太坊主网），但在拥堵时可能需要更高。在 BSC 上通常 1-3 Gwei 就够了，因为 BSC 的出块时间短（3 秒）、区块 Gas Limit 大。

**我们的 Gas 估算策略**。在 evmwallet 中，我们实现了一个 GasOracle 组件，定期（每 10 秒）从 RPC 节点获取 eth_gasPrice、eth_feeHistory 等数据，计算出三档推荐值：slow（BaseFee * 1.1 + P25 PriorityFee）、standard（BaseFee * 1.2 + P50 PriorityFee）、fast（BaseFee * 1.5 + P75 PriorityFee）。Swap 交易默认使用 standard 档位。当检测到用户的交易金额较大（超过 $10000）或者 Token 价格波动剧烈时，自动升级到 fast 档位，确保交易尽快确认，减少价格变动风险。

**Gas Limit 估算**。除了 Gas Price，还需要估算 GasLimit（交易最多消耗多少 Gas）。我们通过 eth_estimateGas RPC 方法模拟交易执行，获取预估的 Gas 消耗。然后设置 GasLimit 为预估值的 1.2 倍（留 20% 余量），因为某些 Token 的 transfer 回调可能在不同时刻消耗不同的 Gas。如果 eth_estimateGas 返回错误（说明交易会 revert），我们就不发送这笔交易，直接返回错误给用户。

**RBF（Replace-By-Fee）**。EIP-1559 原生支持交易替换：发送一笔新交易，使用相同的 Nonce 但更高的 MaxFeePerGas 和 MaxPriorityFeePerGas，节点会用新交易替换掉 mempool 中的旧交易。替换条件是新交易的 MaxPriorityFeePerGas 至少比旧交易高 10%。我们在交易超时未确认时使用这个机制来加速交易。

### 4.5 "为什么 irwallet 的 Pool 数据模型可以同时表示 Solana 和 EVM 的池子？"

这是我们在设计 irwallet 时花了最多时间讨论的数据模型之一，最终的设计体现了"通用字段做业务决策，链特定字段做链上交互"的分层思想。

**需求分析**。Pool 数据模型需要服务两个场景：一是在 Aggregator 中做跨 DEX 比价和最优池选择，这要求所有池子都有可比较的统一字段；二是在 SwapBuilder 中构建交易时提供链上交互所需的细节数据，这要求保留链特定的原始信息。

**通用字段设计**。我们定义了以下链无关的核心字段：
- `PoolContract`（string）：池子的合约/账户地址，Solana 是 base58 编码的 pubkey，EVM 是 0x 开头的 hex 地址，统一用 string 类型可以兼容所有格式。
- `BaseMint` / `QuoteMint`（string）：交易对的两个 Token 地址，同样用 string 兼容。
- `DexID`（int）：所属 DEX 的标识，全局唯一编号（比如 Raydium AMM = 1，UniSwap V2 = 101），用于工厂模式匹配 SwapBuilder。
- `ProtocolType`（enum）：协议类型——AMM、CLMM、BondingCurve、StableSwap、DLMM，用于 Aggregator 的报价策略选择。
- `FeeRate`（decimal）：手续费率，统一用 decimal 表示（比如 0.003 = 0.3%），不同 DEX 的费率结构不同但都可以归一化为单一数值。
- `TotalLiquidity`（big.Int）：总流动性深度，统一转换为 USD 计价。这是跨 DEX 比价的关键字段——不管是 Solana 的 AMM 池子还是 EVM 的 UniSwap V3 池子，USD 计价的流动性都是可直接比较的。
- `State`（enum）：池子状态——Active / Inactive / NeedUpdate。
- `LastUpdated`（timestamp）：最后更新时间，用于判断数据新鲜度。

**这些字段为什么够用**。Aggregator 做比价时只需要：ProtocolType（决定用什么报价算法）、TotalLiquidity（决定滑点大小）、FeeRate（决定手续费成本）、State（过滤不可用的池子）。这四个字段完全是链无关的，一个 Solana Raydium AMM 池子和一个 EVM UniSwap V2 池子，只要这四个字段都有值，Aggregator 就能统一比较和排序。

**链特定字段的处理**。但在实际构建交易时，需要更多链特定的细节。比如 Solana Raydium AMM 池子需要：amm_id、open_orders、target_orders、base_vault、quote_vault、market_program 等十几个 Account 地址。EVM UniSwap V2 池子需要：pair_address、token0、token1、reserve0、reserve1、factory_address、router_address 等。这些字段完全不重叠。

我们的解决方案是 `RawData []byte` 字段。它存储链特定的池子原始数据，序列化格式由各链自行决定（Solana 用 Borsh 编码，EVM 用 ABI 编码或 JSON）。当 SwapBuilder 需要构建交易时，通过各自链的 PoolParser 把 RawData 反序列化为链特定的结构体（比如 RaydiumAmmPoolData 或 UniswapV2PairData），获取所有需要的字段。

**数据库层面**。在 MySQL 的 pool 表中，通用字段是各自的列（pool_contract、base_mint、quote_mint、dex_id、protocol_type、fee_rate、total_liquidity、state、last_updated），链特定数据存在一个 raw_data BLOB 列中。这样既方便用通用字段做 SQL 查询（比如"查找所有 USDT/SOL 交易对、流动性 > $10000、状态为 Active 的池子"），又不丢失链特定的细节。chain_id 列区分不同链的数据。

**总结**。Pool 模型的设计原则是"用通用字段做业务决策，用原始数据做链上交互"。这个分层让 Aggregator 的比价逻辑完全不需要知道底层是 Solana 还是 EVM，实现了真正的跨链复用。而 SwapBuilder 在需要链特定细节时，通过反序列化 RawData 获取完整信息，不损失任何精度。这种设计在 irwallet 的 30800 行代码中被证明是有效的，两年多的生产运行中没有因为数据模型的抽象而导致过功能缺失或 bug。

---

> 本文档基于真实的生产系统经验编写，所有数据（代码行数、DEX 数量、服务商数量等）来自实际系统。核心设计思想在 Onchain-DEX-Lab 项目的 demo 代码中有对应的简化实现，可以结合代码一起复习。
