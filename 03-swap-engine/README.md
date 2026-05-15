# 03-swap-engine: Swap 交易构建引擎

## 模块概述

本模块实现 DEX Swap 交易的构建引擎，是整个 dexwallet 系统的核心。SwapBuilder 接口将「报价计算」和「交易组装」统一到一个流程中，Solana 和 EVM 各自实现完全不同的交易构建逻辑，但对外暴露相同的接口。

核心设计：
1. **工厂模式** -- 通过 SwapBuilderFactory 注册和管理多个 DEX Builder
2. **策略模式** -- 每个 DEX 有独立的 SwapBuilder 实现，封装协议特定的交易构建逻辑
3. **交易模拟** -- 构建后可模拟执行，提前发现滑点过高、余额不足等问题

## Solana vs EVM 交易构建流程对比

### Solana 交易构建流程

```
SwapRequest
    |
    v
[1. 验证请求参数]
    |
    v
[2. 计算输出金额]  <-- 根据协议类型选择公式
    |                    AMM: x*y=k 恒定乘积
    |                    BondingCurve: 联合曲线定价
    |                    Aggregator: 聚合多 DEX 报价
    v
[3. 滑点检查]
    |
    v
[4. 组装 Solana 指令序列]
    |-- ComputeBudget 指令 (设置优先费和计算单元)
    |-- CreateATA 指令 (创建关联代币账户，如果不存在)
    |-- Swap 指令 (调用 DEX Program)
    v
[5. Address Lookup Table 压缩]
    |-- 查询 ALT 表
    |-- 将指令中的地址替换为 ALT 索引
    |-- 减少交易体积 (Solana 有 1232 字节限制)
    v
[6. 序列化交易] --> SwapResult
```

**Solana 特有概念：**
- **ComputeBudget**: 每笔交易需要声明消耗的计算单元（CU）和优先费
- **ATA (Associated Token Account)**: Solana 上代币余额存储在独立的关联账户中
- **ALT (Address Lookup Table)**: 地址查找表，将 32 字节的公钥压缩为 1 字节索引
- **指令序列**: 一笔 Solana 交易可以包含多个指令，原子性执行

### EVM 交易构建流程

```
SwapRequest
    |
    v
[1. 验证请求参数]
    |
    v
[2. 计算输出金额]  <-- 根据协议类型选择公式
    |                    AMM: x*y=k 恒定乘积
    |                    CLMM: tick 区间流动性
    |                    StableSwap: StableSwap 曲线
    v
[3. 滑点检查]
    |
    v
[4. 检查 Approve 状态]
    |-- 如果 allowance 不足，需要先发 Approve 交易
    |-- 这是独立的一笔交易（Solana 不需要这步）
    v
[5. ABI 编码 calldata]
    |-- 编码函数选择器 (4 bytes)
    |-- 编码参数 (每个参数 32 bytes 对齐)
    v
[6. Gas 估算]
    |-- EIP-1559: baseFee + maxPriorityFee
    |-- Legacy: gasPrice
    |-- estimateGas 获取 gasLimit
    v
[7. 构建 RawTransaction] --> SwapResult
```

**EVM 特有概念：**
- **Approve**: ERC20 代币需要先授权给 Router 合约才能转移
- **ABI Encoding**: 函数调用参数按 ABI 规范编码
- **EIP-1559 Gas**: baseFee (协议决定) + maxPriorityFee (用户设置)
- **calldata**: 合约调用的输入数据

### 关键差异总结

| 维度 | Solana | EVM |
|------|--------|-----|
| 交易结构 | 指令序列（多指令一笔交易） | 单次合约调用（Approve 需要独立交易） |
| 代币授权 | 不需要 Approve（ATA 机制） | 需要 Approve（allowance 机制） |
| 地址压缩 | ALT 查找表 | 无（地址固定 20 字节） |
| Gas/费用 | ComputeBudget（CU + 优先费） | EIP-1559（baseFee + priorityFee） |
| 交易大小限制 | 1232 字节硬限制 | 无硬限制（但 Gas 随数据增长） |
| 序列化格式 | Borsh + base58/base64 | RLP + hex |
| 原子性 | 指令序列原子执行 | 单笔交易原子，多笔不原子 |

## 文件结构

```
03-swap-engine/
    README.md                  -- 模块概述（本文件）
    goals.md                   -- 学习目标与自检问题
    notes.md                   -- 深度技术笔记
    demo/
        factory.go             -- SwapBuilder 工厂注册机制
        solana_builders.go     -- 3 个 Solana DEX Builder（mock）
        evm_builders.go        -- 3 个 EVM DEX Builder（mock）
        evm_approve.go         -- EVM ERC20 Approve 检查与交易构建（allowance 检查、approve(0) 边界）
        simulator.go           -- 交易模拟器
        precheck.go            -- 交易前检查器（余额、滑点、参数验证）
        preflight.go           -- Swap 预检验链（8 项检查 + RejectCode 拒绝码）
        main.go                -- 可运行演示
        swap_test.go           -- 测试
        precheck_test.go       -- PreCheck 测试
```

## 与生产系统的差距

| 维度 | 本 demo | 生产系统 |
|------|---------|----------|
| 价格计算 | 简化公式，固定参数 | 从链上读取实时池子数据计算 |
| 交易组装 | 模拟指令/calldata | 真实的 Borsh 序列化 / ABI 编码 |
| 余额检查 | 模拟余额 | RPC 查询真实余额 |
| Approve | 跳过 | 查询 allowance，不足则先发 Approve 交易 |
| ALT 压缩 | 模拟压缩 | 真实查询 ALT 表并替换地址 |
| Gas 估算 | 固定值 | eth_estimateGas / simulateTransaction |
| 签名 | 不涉及 | Ed25519 (Solana) / ECDSA (EVM) 真实签名 |
| 路由 | 直接交易 | 可能需要多跳路由（A->B->C） |
