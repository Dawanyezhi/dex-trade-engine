# 03-swap-engine: 学习目标与自检问题

## 知识点分层

### L1 -- 基础概念（必须掌握）

- Swap 交易的基本流程：输入金额 -> 计算输出 -> 检查滑点 -> 构建交易
- AMM 恒定乘积公式 x*y=k 的含义
- 滑点（Slippage）的定义和基点（BPS）的换算（1 BPS = 0.01%）
- SwapBuilder 接口的职责：接收 SwapRequest，返回 SwapResult
- 工厂模式的基本概念：注册 -> 查找 -> 使用
- Solana 和 EVM 交易结构的基本差异（指令序列 vs 合约调用）

### L2 -- 核心实现（需要理解原理）

- 不同 DEX 协议的价格计算差异
  - AMM（Raydium/Uniswap）: dy = y * dx / (x + dx)，手续费在输入端扣除
  - Bonding Curve（PumpFun）: 联合曲线，越买越贵，有毕业阈值
  - CLMM（PancakeV3）: 集中流动性，tick 区间内的恒定乘积
  - StableSwap（Curve）: 在 1:1 附近几乎零滑点的特殊曲线
- Solana 交易指令的组装顺序和依赖关系
  - ComputeBudget 必须是第一条指令
  - CreateATA 必须在 Swap 之前
  - ALT 压缩减少交易体积
- EVM 交易的 Approve 机制
  - 为什么需要两步（Approve + Swap）
  - Approve 是独立交易，有失败风险
  - MaxUint256 授权 vs 精确授权的安全性权衡
- math/big 的正确使用
  - 为什么禁止 float64（精度丢失会导致资金损失）
  - big.Int 的常用操作：Mul, Div, Sub, Cmp
  - 滑点计算的整数公式

### L3 -- 进阶设计（生产中需要）

- 交易模拟的价值
  - Solana simulateTransaction：不上链但会完整执行
  - EVM eth_call：模拟合约调用
  - 模拟可以提前发现：余额不足、滑点超限、合约限制
- Gas/优先费策略
  - Solana: ComputeBudget 中的优先费影响交易排序
  - EVM: EIP-1559 的 baseFee + maxPriorityFeePerGas
  - 动态调整：根据网络拥堵程度调整费用
- 聚合器（Jupiter/1inch）的角色
  - 路由拆分：大单拆到多个 DEX 执行
  - 多跳路由：A -> B -> C（中间代币桥接）
  - 作为降级兜底的设计
- 工厂模式的并发安全
  - sync.RWMutex 保护 builder map
  - 注册和查找的读写分离
  - 运行时动态注册/注销 builder

### L4 -- 架构视角（通盘理解）

- SwapBuilder 在 dexwallet 三层架构中的位置
  - dexwallet 层定义接口
  - solana/evm 层实现链特定的交易构建
  - DEX 协议层处理各 DEX 的具体逻辑
- SwapBuilder 与其他组件的协作
  - 依赖 PoolManager 获取池子数据
  - 依赖 RPCClient 查询链上状态和模拟交易
  - 构建结果交给 TxSender 发送
  - 全流程由 Aggregator 编排
- Solana 和 EVM 的交易生命周期差异
  - Solana: Build -> Sign -> Simulate -> Send -> Confirm (400ms slot)
  - EVM: Build -> Sign -> Send -> WaitReceipt (12s block)
  - 这些差异如何影响重试和超时策略
- DEX 优先级和降级策略
  - 内盘（BondingCurve）优先级最高
  - AMM/CLMM 次之
  - 聚合器作为兜底
  - 灰度发布机制的意义

## 自检问题

### L1 基础

1. 如果用户输入 1 SOL（1000000000 lamports）在一个 x*y=k 的池子里 swap，池子中有 100 SOL 和 1000000 MEME，手续费 0.3%，输出多少 MEME？
2. SlippageBps = 100 表示多少百分比的滑点容忍？如果期望输出 1000 个 token，MinOutput 应该是多少？
3. SwapBuilder 的 Build 方法为什么返回 SwapResult 而不是直接发送交易？这种设计有什么好处？

### L2 核心

4. Solana 交易为什么需要 ComputeBudget 指令？如果不设置会发生什么？
5. EVM 的 Approve + Swap 两步操作中，如果 Approve 成功但 Swap 失败，会有什么后果？如何处理？
6. PumpFun 的 Bonding Curve 和 Raydium AMM 的 x*y=k 有什么本质区别？为什么 PumpFun 有「毕业」概念？
7. 为什么金额计算禁止使用 float64？给出一个具体的精度丢失场景。

### L3 进阶

8. Jupiter 作为聚合器，为什么在优先级设计中排在最后而不是最前面？什么场景下应该优先使用聚合器？
9. 如果一个 Solana 交易超过 1232 字节限制，ALT 压缩如何解决这个问题？ALT 本身有什么限制？
10. 交易模拟（simulateTransaction / eth_call）返回成功，但实际执行时仍然失败，可能的原因是什么？

### L4 架构

11. 如果要新增一个 DEX（比如 Meteora DLMM），需要修改哪些文件？工厂模式如何保证新增 DEX 不影响已有逻辑？
12. SwapBuilder 和 DexProtocol 两个接口有什么区别？为什么不合并成一个？
13. 在高并发场景下（每秒处理 100+ swap 请求），SwapBuilderFactory 的锁设计是否足够？如何优化？
