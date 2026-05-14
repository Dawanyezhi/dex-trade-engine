# Onchain-Dex-Lab 项目生成提示词

> 将以下完整内容作为提示词发给 AI，让它在本目录下生成项目。
> 建议分模块执行（一次让 AI 完成 1-2 个模块），避免上下文过长导致质量下降。

---

## 提示词正文

你是一位资深区块链钱包工程师，精通 Go 语言、Solana 和 EVM 链的 DEX 交易系统开发。现在请你在当前目录下，帮我创建一个**链上 DEX 交易引擎**的完整学习项目。

### 一、项目背景

我在交易所做了几年 DEX 钱包开发，日常维护 Solana（17+ DEX）和多条 EVM 链（30+ DEX）的 Swap 交易系统。这个项目把生产中最核心的设计提炼出来，用教学友好的方式重新实现——**不是 copy 生产代码，而是理解后重写**，每个模块都要有 notes.md 记录设计决策背后的"为什么"。

**项目定位**：与我的另一个项目 [Exchange-Wallet](https://github.com/yys9517/exchange-wallet)（充提归集系统）形成姊妹篇。Exchange-Wallet 解决"钱怎么进出交易所"，本项目解决"钱怎么在链上 DEX 交易"。

**面试价值**：面试中被问最多的三个 DEX 相关问题——Swap 交易怎么构建、多 DEX 怎么聚合选最优、MEV 怎么防护——在这个项目里都要有可运行的代码和测试来回答。

### 二、核心架构亮点：三层抽象（重点展示）

本项目最重要的架构特色是**三层代码抽象**，对标生产系统中的真实设计：

```
┌─────────────────────────────────────────────────────────────────┐
│                    DEX 协议层（最具体）                           │
│  Raydium / Pump.fun / Jupiter / UniSwap / PancakeSwap / Curve   │
│  每个 DEX 有独立的交易构建、报价、指令解析                        │
└────────────────────────┬────────────────────────────────────────┘
                         │ 实现接口
┌────────────────────────┴────────────────────────────────────────┐
│               链特定层（solana / evm）                           │
│  solana: RPC封装、指令组装、ALT、ComputeBudget、Token2022       │
│  evm:    RPC封装、ABI编码、Gas估算、EIP-1559、Approve流程       │
│  各自实现 dexwallet 层定义的接口                                 │
└────────────────────────┬────────────────────────────────────────┘
                         │ 实现接口
┌────────────────────────┴────────────────────────────────────────┐
│            dexwallet 通用层（跨链公共抽象）                       │
│  对标生产中的 irwallet 框架                                      │
│                                                                  │
│  定义接口:                                                       │
│    SwapBuilder      — 构建 Swap 交易                             │
│    PoolManager      — 池子管理与缓存                              │
│    Aggregator       — 多 DEX 聚合与路由                           │
│    EventParser      — 链上事件解析                                │
│    BribeService     — 贿赂/优先费服务                             │
│    TxSender         — 交易发送与确认                              │
│    Alarm            — 监控告警                                    │
│                                                                  │
│  通用实现:                                                       │
│    bigint           — 精确金额（禁止float64）                     │
│    coinset          — 链配置 + FeatureGate                       │
│    ratelimiter      — 令牌桶限流                                  │
│    cache            — LRU 缓存                                   │
│    repository       — 数据存储接口                                │
│    datamodel        — 交易/余额/池子数据模型                      │
└──────────────────────────────────────────────────────────────────┘
```

**生产中的真实情况**：
- **irwallet**（30,800 行）：跨链通用框架，被 solwallet 和 evmwallet 共同依赖。定义了 Wallet/Syncer/Repository 等核心接口，包含数据模型、消息队列、签名服务客户端、监控告警等通用实现。
- **solwallet**（200K+ 行）：实现 irwallet 接口，增加 Solana 特有逻辑（指令解析、ALT、贿赂服务、Token2022）。
- **evmwallet**（800+ 文件）：实现 irwallet 接口，增加 EVM 特有逻辑（ABI 编码、Gas 估算、Approve、Anti-MEV RPC）。

**本项目要体现这个抽象过程**：从具体的 Solana/EVM 代码中，识别出哪些是通用逻辑、哪些是链特定逻辑，然后合理分层。每个模块的 notes.md 都要标注："这部分属于 dexwallet 通用层" vs "这部分属于链特定层"。

### 三、参考的生产系统概述

以下是我在生产中维护的三个系统的核心设计，作为本项目的知识来源。**不要照搬生产代码，要提炼核心设计思想重新实现。**

#### 3.1 irwallet — 跨链钱包框架（Go, 30,800 行）

通用钱包框架，被 solwallet 和 evmwallet 依赖：
- **双服务架构**：Wallet 服务（签名+交易）+ Syncer 服务（区块同步+事件解析）
- **分层设计**：API层 → 业务逻辑层(mywallet/syncer) → 数据层(repository) → 基础设施层(MySQL/Redis/Kafka)
- **核心接口定义**：
  - `Wallet` 接口：NewWithdraw / NewSwap / NewTransfer / GetBalance
  - `Syncer` 接口：Run / Try / DealBlockTx / HeightMonitor / TimeoutMonitor
  - `Repository` 接口：Account / Transaction / Token / Pool 的 CRUD
  - `RPCClient` 接口：链特定的 RPC 调用（由 solwallet/evmwallet 各自实现）
  - `Alarm` 接口：告警（钉钉/Lark/PagerDuty 可插拔）
- **通用实现**：
  - `datamodel/`：交易、余额、池子的数据模型（5,578 行），Protocol Buffers 序列化
  - `repository/`：数据持久化接口定义（11 个文件）
  - `coinset/`：多币种模型（Account-based / UTXO / Tag-based）+ FeatureGate
  - `bloomfilter/`：布隆过滤器地址过滤
  - `kafka/`：消息队列集成
  - `ksrv/`：密钥签名服务客户端（TLS 双向认证）
  - `exchange/`：交易所 API 交互
- **数据库表**：account、outboundtx、swaptx、balance、header、pool（10 个迁移版本）
- **安全**：TLS 双向认证、ED25519 签名验证、KMS 密钥管理
- **监控**：7 个监控指标（高度/超时/余额/异常交易/池子状态）
- **文档**：9 个详细文档覆盖所有方面

**抽取过程的关键决策**（面试重点）：
1. **接口而非实现**：irwallet 只定义接口，不包含任何链特定逻辑
2. **数据模型统一**：swaptx 表结构对 Solana 和 EVM 通用（sellsymbol/buysymbol/slippage/priorityfee）
3. **配置驱动差异**：链的差异通过 `coinset.Chain` 配置参数化（确认数、签名算法、精度等）
4. **可选特性**：FeatureGate 机制控制链特有功能（EnableBloomfilter / EnableRangeSync 等）

#### 3.2 solwallet — Solana DEX 交易引擎（Go, 200K+ 行）

Solana 链上的完整 DEX 交易系统：

**支持的 DEX（17+）**：
| 优先级 | DEX | 类型 |
|--------|-----|------|
| High | Pump.fun / Moonshot / Raydium Launchpad | 内盘（Bonding Curve） |
| Middle | Raydium AMMv4/CPMM/CLMM、Meteora AMM/DAMM V2/DBC/DLMM、PumpAMM、Boop | AMM/CLMM |
| Low | Jupiter、AlphAggregator | 聚合器 |

**核心架构**：
- **Swap 流程**：预检查 → 并行获取(账户/费用/Token/池子) → 选择DEX(工厂模式) → 构建交易(指令+费用+ALT) → 模拟执行(滑点检查) → MPC签名 → 发送(带贿赂服务)
- **费用体系**：基础费(5000 lamports/sig) + 优先费(ComputeUnitPrice × Limit) + 贿赂费(NextBlock/Temporal/ZeroSlot/BlockRazor/BlockRush，5个服务商，多地区CDN)
- **指令解析引擎**：74 个文件，解析 70+ DEX 的链上交易指令，提取池子信息和税费数据
- **池子管理**：链上池子数据解析、LRU 缓存、定时刷新（60分钟）、gRPC LaserStream 实时推送（200-300ms 延迟）
- **交易优化**：Address Lookup Table(ALT) 压缩交易体积、交易模拟验证滑点、限流（20 TPS，60 workers）
- **稳定币支持**：USDC/USDT/USD1，支持稳定币作为主币的两跳聚合路径

#### 3.3 evmwallet — 多链 EVM DEX 交易引擎（Go, 800+ 文件）

多条 EVM 链的 DEX 聚合交易系统：

**支持链**：BSC、Ethereum、Base、XLayer、Monad、Plasma

**支持的 DEX（30+）**：
- UniSwap V2/V3/V4、PancakeSwap V2/V3/V4/Stable
- Curve、Balancer、DODO、KyberSwap
- 第三方聚合：1Inch、ParaSwap、OKX DEX、Odos、OpenOcean
- 链特定：FourMeme、NAD、KasFun

**核心架构**：
- **聚合器模式**：SwapTxBuilderSelector 接口 → 并发调用所有 DEX → 比较报价选最优 → 构建交易
- **交易构建**：Gas 估算(动态 BaseFee + PriorityFee) → Approval + Swap → 远程签名(Ksrv) → 广播
- **Anti-MEV**：配置专用防抢跑 RPC 节点
- **RBF 加速**：Replace-By-Fee 交易替换机制
- **事件解析**：23+ 解析器，按链和事件签名注册，解析 Swap/Transfer/Liquidity/TokenCreate 事件
- **合约交互**：30+ ABI 定义，go-ethereum 生成 Go 绑定

### 四、项目结构要求

严格仿照 Exchange-Wallet 的组织方式，采用编号目录 + 循序渐进的模块设计：

```
onchain-dex-lab/
├── 00-architecture/              系统架构设计（纯文档，重点展示三层抽象）
├── 01-rpc-client/                Solana & EVM RPC 客户端封装
│   └── demo/
├── 02-dex-protocols/             DEX 协议深度解析（AMM/CLMM/Bonding Curve）
│   └── demo/
├── 03-swap-engine/               Swap 交易构建引擎  ★
│   └── demo/
├── 04-pool-management/           流动性池解析与管理
│   └── demo/
├── 05-aggregator-routing/        多 DEX 聚合与最优路由  ★
│   └── demo/
├── 06-mev-protection/            MEV 防护与交易优化  ★
│   └── demo/
├── 07-event-parsing/             链上事件解析与同步
│   └── demo/
├── 08-production-architecture/   生产级架构（多链扩展/限流/监控）
│   └── demo/
│
├── cmd/simulate/                 端到端演示（串联 8 个场景）
├── cmd/dex-wallet/               DEX 钱包服务主入口（stub）
├── interview-prep/               面试准备（口语化 Q&A + 场景题 + 深挖题）
├── config/                       配置文件（dev.yaml）
│
├── internal/                     跨链通用基础库（对标 irwallet）
│   ├── dexwallet/                ★ 核心抽象层（接口定义 + 通用实现）
│   │   ├── interfaces.go         SwapBuilder / PoolManager / Aggregator / EventParser 等接口
│   │   ├── model.go              SwapRequest / SwapResult / Pool / Quote 等数据模型
│   │   ├── aggregator.go         通用聚合逻辑（并发 Quote → 比较 → 选最优）
│   │   ├── pool_cache.go         通用 LRU 池子缓存
│   │   ├── tx_sender.go          通用交易发送与确认追踪
│   │   └── monitor.go            通用监控指标收集
│   ├── solana/                   Solana 链特定实现（实现 dexwallet 接口）
│   │   ├── rpc.go                Solana RPC 客户端
│   │   ├── swap_builder.go       Solana 交易构建（指令组装/ALT/ComputeBudget）
│   │   ├── pool_parser.go        Solana 池子解析（Borsh 反序列化）
│   │   ├── event_parser.go       Solana 事件解析（指令分发）
│   │   ├── bribe.go              贿赂服务（NextBlock/Temporal/ZeroSlot 等）
│   │   └── dex/                  各 DEX 协议实现
│   │       ├── raydium.go
│   │       ├── pumpfun.go
│   │       └── jupiter.go
│   ├── evm/                      EVM 链特定实现（实现 dexwallet 接口）
│   │   ├── rpc.go                EVM RPC 客户端
│   │   ├── swap_builder.go       EVM 交易构建（ABI编码/Gas/Approve）
│   │   ├── pool_parser.go        EVM 池子解析（Factory 事件）
│   │   ├── event_parser.go       EVM 事件解析（topic 签名匹配）
│   │   ├── antimev.go            Anti-MEV RPC 节点
│   │   └── dex/                  各 DEX 协议实现
│   │       ├── uniswapv2.go
│   │       ├── pancakev3.go
│   │       └── curve.go
│   ├── bigint/                   精确金额（禁止 float64）
│   ├── coinset/                  链配置 + FeatureGate
│   └── alarm/                    告警接口（可插拔）
│
├── docs/                         数据库 schema + API 设计
├── README.md                     主文档
├── CLAUDE.md                     项目指令
├── Makefile                      构建命令
└── go.mod
```

**关键点**：`internal/dexwallet/` 是整个项目的灵魂——它定义了所有跨链通用的接口和数据模型。`internal/solana/` 和 `internal/evm/` 只实现这些接口，不引入新的公共概念。这就是生产中 irwallet 的抽象思路。

### 五、每个模块的详细要求

#### 模块 00：系统架构设计（重点：三层抽象）

**内容**：
- DEX 钱包系统三层架构：签名服务 → DEX 交易引擎(Wallet+Syncer) → 多链节点
- **重点**：三层代码抽象的设计过程和决策
  - 哪些代码是 Solana 和 EVM 共有的？→ 提取到 dexwallet 层
  - 哪些代码是链特定的？→ 留在 solana/evm 层
  - 接口怎么设计才能让新增一条链成本最低？
- 数据流：Swap 请求流 / 事件同步流 / 池子更新流
- Solana vs EVM 架构差异对比
- 与 Exchange-Wallet 充提系统的关系图

**notes.md 重点**：
- irwallet 抽取过程的真实故事：最初 solwallet 和 evmwallet 是独立项目，后来发现大量重复代码（数据模型、Syncer 流程、签名客户端、告警），所以抽取出 irwallet
- 抽取时的取舍：哪些东西看起来"差不多"但其实不该抽取（例如 Solana 的指令解析 vs EVM 的事件解析，形式完全不同）
- 接口设计的演进：从具体实现到抽象接口的重构过程

**文件**：
- `README.md`：架构概述 + Mermaid 流程图 + 三层抽象图
- `goals.md`：知识点 L1-L3 分层 + 自检问题
- `notes.md`：架构决策笔记

#### 模块 01：RPC 客户端封装

**核心知识点**：
- Solana RPC：getAccountInfo、getRecentBlockhash、sendTransaction、getSignatureStatuses、getPriorityFee
- EVM RPC：eth_call、eth_estimateGas、eth_sendRawTransaction、eth_getLogs、debug_traceTransaction
- 多节点故障转移（StableClient 模式）
- gRPC 流式订阅（Helius LaserStream）

**抽象层设计**（notes.md 重点标注）：
- **dexwallet 层**：定义 `RPCClient` 接口（SendTransaction / GetBalance / GetBlockHeight），定义 `StableClient` 通用故障转移逻辑
- **链特定层**：Solana 实现 getAccountInfo 等、EVM 实现 eth_call 等

**demo 要求**：
- 实现一个多节点 RPC 客户端，支持自动故障转移
- 实现 Solana 和 EVM 的基础 RPC 调用（用 mock 替代真实节点）
- 测试：节点故障切换、超时处理、并发安全

**生产差距**：demo 用内存 mock 替代真实 RPC 节点，缺少 WebSocket 长连接和 gRPC 流式订阅。

#### 模块 02：DEX 协议深度解析

**核心知识点**：
- **AMM（恒定乘积）**：Raydium AMMv4、UniSwap V2、PancakeSwap V2 — `x * y = k` 数学原理、滑点计算、无常损失
- **CLMM（集中流动性）**：Raydium CLMM、UniSwap V3、PancakeSwap V3 — Tick 机制、价格区间、流动性分布
- **Bonding Curve（内盘）**：Pump.fun、Moonshot — 联合曲线定价、毕业机制（迁移到 AMM）
- **稳定币 AMM**：Curve StableSwap — 稳定币低滑点交换原理
- **DLMM（离散流动性）**：Meteora DLMM — Bin 机制、零滑点交易

**抽象层设计**（notes.md 重点标注）：
- **dexwallet 层**：定义 `DexProtocol` 接口（Quote / GetPrice / CalcSlippage），定义 `ProtocolType` 枚举（AMM/CLMM/BondingCurve/StableSwap）
- **链特定层**：每个具体 DEX 实现 DexProtocol 接口

**demo 要求**：
- 用纯 Go 实现各协议的**价格计算和滑点模拟**（不依赖链上数据）
- 对比同一笔交易在不同协议下的报价差异
- 可视化输出价格曲线和滑点关系
- 测试：边界条件（极端价格、流动性为 0、大额交易）

**生产差距**：demo 不包含链上合约调用，数学模型简化了手续费层级。

#### 模块 03：Swap 交易构建引擎（核心模块 ★）

**核心知识点**：
- **Solana 交易构建**：
  - 指令列表组装（ComputeBudget + ATA 创建 + Swap 指令）
  - Address Lookup Table（ALT）压缩交易体积
  - 交易模拟（simulateTransaction）+ 滑点阈值检查
  - Token Program vs Token2022 区别处理
- **EVM 交易构建**：
  - Approve + Swap 两步交易
  - Gas 估算（动态 BaseFee + PriorityFee）
  - EIP-1559 交易类型
  - Calldata 编码（ABI encoding）
- **工厂模式**：根据 DEX ID 选择对应的交易构建器
- **签名集成**：本地签名（demo）vs 远程 MPC 签名（生产）

**抽象层设计**（notes.md 重点标注）：
- **dexwallet 层**：定义 `SwapBuilder` 接口 `Build(ctx, SwapRequest) → SwapResult`，定义 `SwapRequest/SwapResult` 通用数据模型，实现通用的工厂模式 DEX 选择逻辑
- **链特定层**：Solana 实现指令组装+ALT+ComputeBudget，EVM 实现 ABI 编码+Gas+Approve

**demo 要求**：
- 定义 `SwapBuilder` 接口和工厂注册机制
- 实现 3 个 Solana DEX 构建器：Raydium AMM、Pump.fun、Jupiter（mock 版）
- 实现 3 个 EVM DEX 构建器：UniSwap V2、PancakeSwap V3、Curve（mock 版）
- 交易模拟：验证交易是否会成功、检查滑点是否超阈值
- 测试：正常 swap、滑点过高拒绝、余额不足、Token 不存在

**生产差距**：demo 用本地签名替代远程 MPC 签名，交易模拟用内存状态替代链上 simulateTransaction。

#### 模块 04：流动性池解析与管理

**核心知识点**：
- **池子数据结构**：pool_contract、base_mint/quote_mint、liquidity、fee_rate、dex_id、state
- **链上池子解析**：
  - Solana：Account 数据反序列化（Borsh 编码）
  - EVM：合约 Storage 读取 + Factory 事件日志
- **池子缓存策略**：
  - LRU 本地缓存 + 定时刷新（60 分钟）
  - gRPC 实时推送更新（Solana LaserStream）
  - 布隆过滤器快速判断池子是否存在
- **池子状态管理**：可用/不可用/需更新、流动性监控
- **最优池选择**：GetBestPool — 按流动性、费率、滑点综合排序

**抽象层设计**（notes.md 重点标注）：
- **dexwallet 层**：定义 `Pool` 数据模型（通用字段），定义 `PoolManager` 接口（GetBestPool / UpdatePool / RefreshCache），实现通用 LRU 缓存和状态管理
- **链特定层**：Solana 实现 Borsh 解析池子数据，EVM 实现 Factory 事件解析池子数据

**demo 要求**：
- 定义 Pool 数据模型和 PoolManager 接口
- 实现内存池子存储 + LRU 缓存
- 实现 GetBestPool 最优池选择算法
- 模拟池子状态变化和缓存失效
- 测试：缓存命中/失效、池子状态切换、并发读写安全

**生产差距**：demo 缺少真实的链上池子解析（Borsh 反序列化、ABI 解码），缺少 gRPC 流式推送。

#### 模块 05：多 DEX 聚合与最优路由（核心模块 ★）

**核心知识点**：
- **并发报价**：goroutine 并发调用所有 DEX 的 `Quote()` 方法
- **报价比较**：按输出金额排序，考虑 Gas 成本后的净收益
- **优先级策略**：
  - Solana：内盘(Pump.fun) > AMM(Raydium/Meteora) > 聚合器(Jupiter)
  - EVM：直接路由 > 多跳路由 > 第三方聚合器(1Inch/ParaSwap)
- **两跳路由**：tokenA → 中间资产(SOL/WETH) → tokenB
- **稳定币桥接**：USDT → USDC 直接桥接 vs 两跳路由
- **灰度发布**：新 DEX 按百分比灰度接入（grayscale_percentage: 0-100）
- **降级策略**：直接路由失败 → 聚合器兜底

**抽象层设计**（notes.md 重点标注）：
- **dexwallet 层**：实现通用的 `Aggregator`——并发 Quote → 按优先级排序 → 选最优 → 降级兜底。这是 irwallet 层最核心的复用逻辑，Solana 和 EVM 的聚合流程完全一致，只是注册的 DEX 列表不同
- **链特定层**：各链注册自己的 DEX 列表和优先级配置

**demo 要求**：
- 实现 `Aggregator` 核心逻辑：并发 Quote → 比较 → 选最优 → 构建交易
- 实现优先级排序和降级策略
- 实现两跳路由算法
- 灰度发布模拟（某 DEX 只对 30% 请求启用）
- 测试：全部 DEX 报价成功、部分失败降级、全部失败兜底、两跳 vs 直接路由对比

**生产差距**：demo 的报价数据是 mock 的。生产系统 Solana 支持 17+ DEX、EVM 支持 30+ DEX。

#### 模块 06：MEV 防护与交易优化（核心模块 ★）

**核心知识点**：
- **MEV 攻击类型**：三明治攻击（Sandwich）、抢跑（Front-running）、尾随（Back-running）
- **Solana 贿赂服务**（5个）：
  - NextBlock：多地区（Tokyo/Frankfurt/NY/London/SLC）
  - Temporal：全球 CDN（SGP/AMS/TYO/EWR/FRA2）
  - ZeroSlot：低滑点专用
  - BlockRazor：MEV 保护模式（fast/sandwichMitigation）
  - BlockRush：高吞吐量
- **EVM Anti-MEV**：
  - 私有 mempool RPC 节点（Flashbots Protect、MEV Blocker）
  - RBF 交易替换加速
- **优先费计算**：
  - Solana：ComputeUnitPrice × ComputeUnitLimit，动态推荐（2 秒更新）
  - EVM：BaseFee + MaxPriorityFee，GasOracle 价格缓存
- **交易发送策略**：多通道并发发送、超时监控、自动重试、确认状态追踪

**抽象层设计**（notes.md 重点标注）：
- **dexwallet 层**：定义 `BribeService` 接口（Send / GetFee）和 `TxSender` 接口（Send / Confirm / Retry），实现通用的多通道并发发送逻辑和超时重试
- **链特定层**：Solana 实现 5 个贿赂服务商集成，EVM 实现 Anti-MEV RPC + RBF

**demo 要求**：
- 模拟三明治攻击场景：展示攻击前后的价格变化
- 实现贿赂服务抽象层：BribeService 接口 + 多服务商并发发送
- 实现优先费推荐算法（基于历史数据的百分位计算）
- 实现 RBF 加速逻辑（EVM）
- 测试：正常发送、贿赂服务故障降级、优先费计算边界

**生产差距**：demo 的贿赂服务是 mock 的，不连接真实的 NextBlock/Temporal 等服务。

#### 模块 07：链上事件解析与同步

**核心知识点**：
- **Solana 交易解析**：
  - 指令解析引擎（按 ProgramID 分发）
  - 交易分类：Transfer / Swap / TokenMint / AmmLiquidity / PumpFunAgent
  - 内部指令（Inner Instructions）解析
- **EVM 事件解析**：
  - Event Log 解析（topic 签名匹配）
  - 23+ 解析器注册机制（按链和事件签名）
  - Trace API 内部交易追踪
- **Syncer 同步流程**：
  - 区块轮询 → 交易分类 → 事件解析 → 池子更新 → Kafka 推送
  - 重组检测（parentHash 校验）
  - 超时交易恢复

**抽象层设计**（notes.md 重点标注）：
- **dexwallet 层**：定义 `EventParser` 接口（Parse / Register），定义事件类型枚举（Swap/Transfer/Mint/Burn/Liquidity），定义 `Syncer` 通用主循环（轮询+分发+持久化），irwallet 的 syncer 模块有 31 个文件实现通用同步逻辑
- **链特定层**：Solana 按 ProgramID 解析指令，EVM 按 topic 解析事件日志——解析方式完全不同，但输出的事件模型是统一的

**demo 要求**：
- 实现事件解析器注册机制：`parser.Register(chainId, identifier, handler)`
- 实现 3 种 Swap 事件解析器：UniSwap V2 Swap、Raydium Swap、Pump.fun 交易
- 实现 Syncer 主循环：模拟区块产生 → 解析事件 → 更新池子状态
- 测试：正常解析、未知事件跳过、重组回滚

**生产差距**：demo 解析 3 种事件，生产系统解析 70+ 种。缺少 Kafka 推送和 Bloom Filter 优化。

#### 模块 08：生产级架构

**核心知识点**：
- **多链扩展**：
  - EVM：链差异通过 ChainConfig 参数化（Gas 估算方式、确认数、RPC 特性）
  - Solana：Token Program vs Token2022、Compute Unit 配置
  - 新增一条 EVM 链只需添加配置，业务逻辑零修改
- **限流与背压**：
  - Swap 限流：令牌桶（20 TPS）+ 最大队列（500）+ Worker 池（60）
  - RPC 调用限流
- **监控与告警**：
  - 区块高度监控（停滞告警）
  - 交易超时监控
  - 余额异常监控
  - 池子状态异常
  - 告警通道：钉钉/Lark/PagerDuty（可插拔接口）
- **配置管理**：
  - 多环境配置（dev/stage/prod）
  - 运行时动态配置（灰度百分比、禁用 DEX 列表）

**抽象层设计**（notes.md 重点标注）：
- **dexwallet 层**：监控、限流、配置管理全部是通用的——irwallet 的 7 个监控指标对 Solana 和 EVM 完全一致
- **链特定层**：只有告警消息内容和监控阈值不同

**demo 要求**：
- 实现令牌桶限流器
- 实现可插拔的多链配置系统
- 实现监控指标收集 + 告警发送（mock）
- 端到端演示：模拟高并发 Swap 请求 → 限流 → 处理 → 监控
- 测试：限流精度、配置热更新、告警触发

**生产差距**：demo 缺少 Kafka/Redis 集成、K8s 部署配置、真实的钉钉/Lark Webhook。

### 六、每个模块的文件结构（强制）

```
XX-module-name/
├── README.md       模块概述 + 核心实现说明 + 与生产系统的差距
├── goals.md        知识点 L1~L4 分层 + 自检问题
│                     L3: 白板能讲（核心原理，能从第一原理推导）
│                     L2: 看到能识别（实现细节，看代码能理解）
│                     L1: 知道有就行（具体参数值，记住即可）
│                     L4: 深度理论（数学/密码学/底层原理）
├── notes.md        深度技术笔记（可直接用于面试复习）
│                     包含：伪代码、流程图、设计决策的"为什么"、真实案例
│                     ★ 每节标注：[dexwallet 通用层] 或 [链特定层]
└── demo/           可运行 Go 代码 + 测试
    ├── main.go           可运行的演示程序
    ├── xxx.go            核心实现代码
    └── xxx_test.go       测试文件
```

### 七、主 README.md 要求

仿照 Exchange-Wallet 的 README 风格：

1. **项目背景**：一段话说清项目定位和面试价值
2. **30 秒看懂核心亮点**：ASCII 树形图展示 3 个核心亮点（Swap引擎、聚合路由、MEV防护）+ 三层抽象设计
3. **`make simulate` 效果预览**：展示 8 个场景的通过/失败输出
4. **项目状态表**：每个模块的阶段、测试数、是否可用于生产
5. **系统架构图**：Mermaid flowchart 展示三层架构和数据流
6. **三层抽象设计图**：展示 dexwallet 通用层 → solana/evm 链特定层 → DEX 协议层
7. **各章节导航表**：每个模块的核心话题、代码亮点、文档亮点
8. **代码复用架构**：对标 irwallet 的抽取过程说明
9. **目录结构树**
10. **快速开始**：make test / make simulate / make demo
11. **与 Exchange-Wallet 的关系**：
    ```
    | 项目 | 内容 | 关注点 |
    |------|------|--------|
    | Exchange-Wallet | 充提归集系统 | 钱怎么进出交易所 |
    | Onchain-DEX-Lab | DEX 交易引擎 | 钱怎么在链上交易 |
    | 共同基础 | irwallet 跨链框架 | 双服务架构 + 接口抽象 |
    ```
12. **生产差距总表**
13. **技术栈表**
14. **License: MIT**

### 八、interview-prep 要求

创建 `interview-prep/README.md`，包含：

1. **DEX 交易引擎 Q&A**（第一人称口语化，可直接对面试官讲）：
   - "Swap 交易的完整流程是什么？"
   - "你们怎么选择最优 DEX？"
   - "MEV 是什么？你们怎么防？"
   - "Solana 和 EVM 链的 Swap 有什么区别？"
   - "流动性池子怎么管理和缓存？"
   - "聚合器降级策略是怎么设计的？"
   - "贿赂服务是什么？为什么需要？"

2. **架构设计 Q&A**（★ 重点，展示抽象能力）：
   - "你们的跨链代码是怎么复用的？"
     → 讲 irwallet 三层抽象：通用框架层 → 链特定层 → DEX 协议层
     → 举例：Aggregator 的并发报价逻辑对 Solana 和 EVM 完全一致，只是注册的 DEX 列表不同
   - "irwallet 是怎么从 solwallet 和 evmwallet 中抽取出来的？"
     → 最初两个项目独立开发，后来发现大量重复：数据模型（swaptx 表结构）、Syncer 主循环、签名客户端、监控告警
     → 抽取过程：先定义接口 → 把通用实现移到 irwallet → 让 solwallet/evmwallet 实现链特定接口
     → 取舍：Solana 的指令解析和 EVM 的事件解析看起来"差不多"，但底层机制完全不同，不应该强行统一
   - "新增一条 EVM 链要改什么？"
     → 只需在 coinset 添加链配置（确认数、Gas 特性、RPC 地址），业务逻辑零修改
     → 如果链有特殊 DEX，只需注册新的 SwapTxBuilderSelector
   - "新增一条非 EVM 链（比如 TRON）要改什么？"
     → 实现 dexwallet 层定义的所有接口（RPCClient、SwapBuilder、PoolParser、EventParser）
     → 复用 dexwallet 层的通用逻辑（Aggregator、PoolCache、TxSender、Monitor）
   - "接口设计时最难的决策是什么？"
     → SwapRequest/SwapResult 的字段设计：Solana 需要 ComputeUnit、ALT，EVM 需要 GasLimit、Nonce
     → 解决：通用字段在结构体里，链特定字段用 `Extra map[string]interface{}` 或链特定子结构
   - "irwallet 框架有多少行代码？维护成本如何？"
     → 30,800 行，233 个 Go 文件，被 solwallet 和 evmwallet 两个项目共同依赖
     → 使用 golangci-lint 进行 40+ 种代码检查
     → 有 9 个详细文档覆盖所有方面

3. **场景题**：
   - "用户 swap 1000 USDT 买某 meme 币，描述完整链路"
   - "某 DEX 池子突然流动性归零，系统如何应对？"
   - "链上拥堵导致交易一直 pending，怎么处理？"
   - "新上线一个 DEX 协议，如何灰度接入？"
   - "solwallet 和 evmwallet 的公共代码出现 bug，修复流程是什么？"

4. **深挖题**：
   - "AMM 的恒定乘积公式推导"
   - "CLMM 的 Tick 机制和集中流动性原理"
   - "Solana 的 Compute Unit 和优先费计算"
   - "EVM 的 EIP-1559 gas 模型"
   - "为什么 irwallet 的 Pool 数据模型可以同时表示 Solana 和 EVM 的池子？字段是怎么设计的？"

### 九、cmd/simulate 要求

创建端到端演示程序，`make simulate` 可串联运行所有场景：

```
╔══════════════════════════════════════════════════════════════╗
║         Onchain DEX Lab — 端到端全场景模拟                   ║
╚══════════════════════════════════════════════════════════════╝

【场景 01：RPC 客户端】
  多节点故障转移 / Solana & EVM RPC 封装
  [PASS] (0.xx s)

【场景 02：DEX 协议解析】
  AMM 价格计算 / CLMM Tick 机制 / Bonding Curve 定价
  [PASS] (0.xx s)

【场景 03：Swap 交易引擎】
  工厂模式选 DEX / Solana 指令组装 / EVM ABI 编码 / 交易模拟
  [PASS] (0.xx s)

【场景 04：池子管理】
  LRU 缓存 / 最优池选择 / 状态更新
  [PASS] (0.xx s)

【场景 05：聚合路由】
  并发报价 / 优先级排序 / 降级兜底 / 两跳路由
  [PASS] (0.xx s)

【场景 06：MEV 防护】
  三明治攻击模拟 / 贿赂服务 / 优先费推荐
  [PASS] (0.xx s)

【场景 07：事件解析】
  解析器注册 / Swap事件解析 / Syncer 主循环
  [PASS] (0.xx s)

【场景 08：生产级架构】
  限流 / 多链配置 / 监控告警
  [PASS] (0.xx s)

╔══════════════════════════════════════════════════════════════╗
║  模拟结果：8 通过 / 0 失败                                  ║
╚══════════════════════════════════════════════════════════════╝
```

### 十、技术约束

- **语言**：Go 1.22+
- **核心依赖**：go-ethereum v1.14.x（EVM 类型/ABI）、shopspring/decimal（精确计算）
- **测试**：所有 demo 使用内存 mock，不依赖外部服务（MySQL/Redis/Kafka/RPC 节点）
- **金额处理**：禁止 float64，全部使用 big.Int 或 decimal.Decimal
- **并发安全**：所有共享数据结构必须有并发保护（sync.Mutex / sync.RWMutex / sync.Map）
- **错误处理**：使用 `fmt.Errorf("xxx: %w", err)` 链式传递
- **日志**：demo 代码使用 `log/slog`（标准库），cmd 使用 `logrus`

### 十一、生产差距总体说明

在主 README.md 中必须有一节说明本项目与生产系统的总体差距：

| 维度 | 本项目 | 生产系统 |
|------|--------|----------|
| DEX 数量 | Solana 3 + EVM 3 = 6 | Solana 17+ / EVM 30+ |
| 链支持 | 模拟（mock） | Solana + BSC + ETH + Base + XLayer + Monad |
| 签名 | 本地 Ed25519/ECDSA | 远程 MPC 签名（ksrv，TLS 双向认证） |
| 数据存储 | 内存 map | MySQL + Redis + Kafka |
| 池子数据 | 静态 mock | 链上实时解析 + gRPC 推送 |
| 贿赂服务 | mock 接口 | 5 个服务商真实集成 |
| 事件解析 | 3 种 | 70+ 种（74 个解析文件） |
| 部署 | 本地运行 | K8s + Docker + 多环境 |
| 监控 | 控制台输出 | 钉钉/Lark + 7 个监控指标 |
| 框架复用 | internal/dexwallet | irwallet（30,800 行，被 2 个项目共同依赖） |

**但核心设计思想完全一致**：三层代码抽象、工厂模式选 DEX、并发聚合比价、优先级降级、交易模拟验证、限流背压——这些架构决策在 demo 和生产系统中是相同的。

### 十二、执行顺序建议

建议按以下顺序逐模块实现：

1. **第一批**：先创建项目骨架 + go.mod + Makefile + 主 README.md + internal/dexwallet/（核心接口定义）+ internal/bigint + internal/coinset
2. **第二批**：00-architecture + 01-rpc-client（基础设施）
3. **第三批**：02-dex-protocols + 03-swap-engine（核心交易）
4. **第四批**：04-pool-management + 05-aggregator-routing（聚合能力）
5. **第五批**：06-mev-protection + 07-event-parsing（高级特性）
6. **第六批**：08-production-architecture + cmd/simulate（生产级 + 串联演示）
7. **第七批**：interview-prep（面试准备材料）

每完成一批，运行 `make test` 确保所有测试通过。

### 十三、关键提醒

1. **不要照搬生产代码**：提炼核心设计思想，用教学友好的方式重新实现
2. **每个 demo 必须可独立运行**：`go run ./XX-module/demo/` 或 `go test ./XX-module/demo/`
3. **生产差距必须诚实标注**：知道哪些是 demo、哪些能上线，本身是工程师的核心能力
4. **notes.md 要写设计决策的"为什么"**：不只是"怎么做"，更重要的是"为什么这样做"
5. **notes.md 每节标注 [dexwallet 通用层] 或 [链特定层]**：体现抽象思维
6. **面试叙事线**：整个项目要支撑一个连贯的面试故事——"我在交易所做 DEX 钱包开发，设计了 irwallet 跨链框架实现代码复用，这个项目提炼了核心设计"
7. **internal/dexwallet/ 是项目灵魂**：它要体现"从两个具体项目中抽取公共代码"的工程能力，这是高级工程师的核心素质
