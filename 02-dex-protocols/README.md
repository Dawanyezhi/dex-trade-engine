# 02-dex-protocols: DEX 协议数学模型

## 模块概述

本模块用纯 Go 实现四种主流 DEX 协议的数学模型，不依赖链上数据，专注于价格计算和滑点模拟。每种协议都实现了 `dexwallet.DexProtocol` 接口，包括 `Quote`（报价）和 `GetPrice`（价格查询）。

DEX 协议是整个交易系统的核心：聚合器通过调用各协议的 `Quote` 方法获取报价，选出最优报价后构建交易。理解协议的数学模型是理解滑点、价格影响、流动性效率的基础。

## 四种协议说明

### 1. AMM -- 恒定乘积做市商（x * y = k）

代表项目：Raydium AMM、Uniswap V2、PancakeSwap V2

最经典的自动做市商模型。两种代币的储量乘积在交易前后保持不变。

- 优势：实现简单，全价格范围提供流动性
- 劣势：大额交易滑点大，流动性利用率低
- 对应 DexID：`DexRaydiumAMM`（Solana）、`DexUniswapV2`（EVM）

### 2. CLMM -- 集中流动性做市商

代表项目：Raydium CLMM、Uniswap V3

流动性提供者可以选择在特定价格范围内提供流动性，而非全范围。这使得相同资金量在活跃价格附近的流动性深度更大，滑点更低。

- 优势：资本效率高，活跃区间内滑点远小于 AMM
- 劣势：实现复杂，LP 需要主动管理头寸
- 对应 DexID：`DexRaydiumCLMM`（Solana）、`DexUniswapV3`（EVM）

### 3. Bonding Curve -- 联合曲线（内盘）

代表项目：PumpFun、Moonshot

新代币发行阶段使用的定价机制。价格沿着预定义的曲线随供应量变化。当流动性达到阈值（如 85 SOL）时，代币"毕业"，迁移到 AMM 池子。

- 优势：无需初始流动性，价格发现公平
- 劣势：早期买入者有价格优势，存在 rug pull 风险
- 对应 DexID：`DexPumpFun`（Solana）

### 4. StableSwap -- 稳定币低滑点

代表项目：Curve

专为等值资产（如 USDC/USDT）设计的做市模型。通过在恒定和（x + y = k）与恒定积（x * y = k）之间插值，在锚定价格附近实现极低滑点。

- 优势：等值资产交易滑点极低（比 AMM 低几个数量级）
- 劣势：仅适用于价格接近的资产对
- 对应 DexID：`DexCurve`（EVM）

## 文件结构

```
02-dex-protocols/
  README.md                -- 模块概述（本文件）
  goals.md                 -- 学习目标与自检问题
  notes.md                 -- 深度技术笔记（含数学推导）
  demo/
    amm.go                 -- AMM 恒定乘积实现
    clmm.go                -- CLMM 集中流动性简化实现
    bonding_curve.go        -- Bonding Curve 联合曲线实现
    stable_swap.go          -- StableSwap 稳定币低滑点实现
    main.go                -- 可运行演示程序
    protocol_test.go        -- 测试
```

## 与生产系统的差距

| 维度 | 本 demo | 生产系统 |
|------|---------|----------|
| 池子数据 | 手动构造 Pool + Extra 字段 | 从链上解析（Borsh / ABI），实时更新 |
| 储量精度 | 简化的 big.Int 计算 | 考虑 token decimals 对齐、溢出保护、精度损失补偿 |
| CLMM Tick | 简化的区间模型 | 完整的 Tick 位图、跨 Tick 交易、费率层级 |
| Bonding Curve | 通用数学模型 | 适配各平台特定曲线参数（PumpFun vs Moonshot） |
| StableSwap | 简化的两资产模型 | 支持多资产（3pool、4pool）、动态 A 参数 |
| 手续费 | 固定费率 | 动态费率、协议费分成、LP 费分配 |
| 路由 | 单池直接交易 | 多跳路由（A->B->C）、拆单（Split Route） |
| Gas 估算 | 固定值 | 根据交易复杂度动态估算 |
| 并发 | 顺序执行 | 并发获取多 DEX 报价 + 超时控制 |
