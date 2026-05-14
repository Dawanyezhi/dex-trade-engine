# 02-dex-protocols: 学习目标与自检问题

## 知识点分层

### L1 -- 基础概念（必须掌握）

- AMM 恒定乘积公式 x * y = k 的含义
  - 为什么乘积不变就能自动定价
  - 什么是储量（Reserve）、什么是流动性（Liquidity）
- 滑点（Slippage）和价格影响（Price Impact）的区别
  - 滑点：预期价格与实际成交价格的偏差
  - 价格影响：单笔交易对池子价格的改变程度
- 手续费率的基点（Basis Points）表示
  - 1 bps = 0.01%，30 bps = 0.3%
  - 为什么用整数基点而不是浮点百分比
- DexProtocol 接口的设计意图
  - Quote 和 GetPrice 的区别
  - 为什么 Quote 需要 Pool 参数
- SwapDirection 的两个方向
  - Buy：用 quote 代币购买 base 代币（amountIn 是 quote）
  - Sell：卖出 base 代币换取 quote 代币（amountIn 是 base）

### L2 -- 核心实现（需要理解原理）

- AMM 报价公式的推导
  - 从 x * y = k 约束推导出 amountOut 的闭合公式
  - 手续费如何影响有效输入金额
  - 为什么大额交易的滑点更大（价格曲线的非线性）
- CLMM 集中流动性的核心思想
  - Tick 和价格范围的对应关系
  - 为什么集中流动性在活跃区间内等效于更大的 AMM 池子
  - 跨 Tick 交易的处理逻辑
- Bonding Curve 联合曲线定价
  - price = a * supply^n 的经济含义
  - 线性曲线（n=1）vs 二次曲线（n=2）的价格增长差异
  - 沿曲线积分计算 amountOut
  - 毕业机制：为什么需要、何时触发
- StableSwap 的数学原理
  - 恒定和（x + y = D）vs 恒定积（x * y = (D/2)^2）
  - A 参数（放大系数）如何在两者之间插值
  - 为什么 A 越大，锚定价格附近的滑点越低
- math/big 整数运算的注意事项
  - 除法精度损失（先乘后除的原则）
  - 溢出风险与处理

### L3 -- 进阶设计（生产中需要）

- 多 DEX 报价聚合
  - 并发获取报价的超时控制
  - 最优报价选择策略（不只是看 outputAmount）
  - 降级策略：首选 DEX 不可用时回退到聚合器
- 实时池子数据的获取与缓存
  - Solana: getAccountInfo 解析 Borsh 编码的 AMM 状态
  - EVM: eth_call 调用 getReserves() 获取池子储量
  - 缓存失效策略：TTL vs 事件驱动
- 路由优化
  - 多跳路由：A->B->C 可能比 A->C 直接交易更优
  - 拆单路由：将大单拆分到多个池子以减少滑点
  - 路由图的构建与最短路径搜索
- 价格预言机与套利
  - 链上价格与 AMM 价格的偏差
  - 套利者如何维持价格平衡

### L4 -- 架构视角（通盘理解）

- DexProtocol 接口在 dexwallet 架构中的位置
  - Aggregator 调用 DexProtocol.Quote 获取报价
  - SwapBuilder 根据最优报价构建交易
  - 协议层是纯计算，不涉及链上交互
- 通用层 vs 链特定层的边界
  - 数学模型（本模块）属于通用层
  - 池子数据解析（Borsh/ABI）属于链特定层
  - Pool.Extra 字段是桥接两层的关键设计
- 四种协议与各 DEX 平台的对应关系
  - Solana: Raydium AMM（AMM）、Raydium CLMM（CLMM）、PumpFun（Bonding Curve）
  - EVM: Uniswap V2（AMM）、Uniswap V3（CLMM）、Curve（StableSwap）
- 协议演进趋势
  - V1 AMM -> V2 手续费分层 -> V3 集中流动性 -> V4 Hook 可编程
  - 从被动做市到主动做市策略

## 自检问题

### L1 基础

1. 一个 AMM 池子有 100 ETH 和 200000 USDC，当前 ETH 价格是多少 USDC？
2. 你在这个池子里用 10000 USDC 买 ETH，能买到多少？为什么不是 5 ETH？
3. 手续费率 30 bps 和 25 bps 分别对应百分之多少？

### L2 核心

4. 用 AMM 公式手算：池子储量 x=1000, y=2000000, 手续费 30bps，输入 amountIn=100（base方向），输出 amountOut 是多少？
5. 为什么 CLMM 在活跃价格范围内的滑点比同等流动性的全范围 AMM 低？请用具体数字说明。
6. Bonding Curve 的线性曲线（n=1）中，如果第 1000 个代币的价格是 0.001 SOL，第 2000 个代币的价格是多少？
7. StableSwap 的 A 参数设为 1 和设为 1000 时，1:1 稳定币对的交易滑点有什么区别？

### L3 进阶

8. 如果同时拿到 Raydium AMM 和 Jupiter 聚合器的报价，Raydium 给出更好的价格但 Jupiter 更可靠，你会如何选择？
9. 池子缓存的 TTL 设为 1 秒和 10 秒各有什么利弊？Solana 和 EVM 的合理值是否不同？
10. 一笔 10 ETH 的卖单，池子 A 有 1000 ETH 流动性、池子 B 有 500 ETH 流动性，如何拆单能获得最优输出？

### L4 架构

11. 为什么 DexProtocol.Quote 接收 *Pool 参数而不是自己去链上获取池子数据？这个设计决策的好处是什么？
12. 如果要新增一个 DEX 协议（比如 Meteora DLMM），需要修改哪些文件？不需要修改哪些文件？
13. Pool.Extra 字段设计为 map[string]interface{}，生产中有没有更好的方案？有什么利弊权衡？
