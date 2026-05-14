# 06-mev-protection: MEV 防护与交易优化

## 模块概述

本模块聚焦链上交易中的 MEV（Maximal Extractable Value）攻击防护和交易发送优化。MEV 是验证者/搜索者通过重排、插入或审查交易来提取的额外价值，对普通用户构成直接经济损失。本模块通过三明治攻击模拟、Solana 贿赂服务多通道发送、EVM Anti-MEV RPC、优先费推荐和 RBF 加速等维度，展示 MEV 防护的核心思路。

> **重要区分**：贿赂服务（Bribe Service）是 **Solana 独有** 的机制，通过向 Leader 节点支付 Tip 获得优先打包权。EVM 链没有贿赂机制，而是使用 Anti-MEV RPC（如 Flashbots Protect）将交易发送到私有 mempool 防止被攻击者监听。

## MEV 攻击类型

### 三明治攻击（Sandwich Attack）

最常见的 MEV 攻击形式，针对 DEX Swap 交易：

```
时间线:
  [1] 攻击者监测到受害者的 Swap 交易（在 mempool/pending 中）
  [2] 攻击者抢先买入（Front-run），推高价格
  [3] 受害者交易执行，以更高的价格成交
  [4] 攻击者立刻卖出（Back-run），赚取差价

受害者的损失 = 无攻击时的输出 - 被攻击后的输出
攻击者的利润 = 卖出收入 - 买入成本 - Gas/贿赂费用
```

攻击者的利润取决于：
- 受害者交易的金额（越大越有利可图）
- 受害者设置的滑点容忍度（越高，攻击空间越大）
- 池子的流动性深度（越浅，价格越容易被操纵）

### 抢跑攻击（Front-running）

攻击者检测到有利可图的交易，抢在其前面执行相同操作：
- 套利交易：发现价差后抢先套利
- 清算交易：抢先执行借贷协议的清算
- NFT 铸造：抢先 mint 稀缺 NFT

### 尾随攻击（Back-running）

在目标交易之后紧跟执行，利用交易造成的价格变化进行套利。

### 时间强盗攻击（Time-bandit Attack）

验证者重组区块以提取历史 MEV，仅在理论中讨论较多，实际发生较少。

## Solana 贿赂服务

Solana 由于其交易排序机制（Leader Schedule + Priority Fee），形成了独特的贿赂服务生态。通过向 Leader 节点支付额外费用（贿赂/Tip），可以获得更优的交易排序位置。

### 5 个主流服务商

| 服务商 | 特点 | 适用场景 |
|--------|------|----------|
| NextBlock | 多地区部署，覆盖主要 Leader 节点 | 通用交易加速，覆盖面广 |
| Temporal | 全球 CDN 加速，低延迟提交 | 对延迟敏感的高频交易 |
| ZeroSlot | 专注低滑点优化 | 大额交易，需要最小滑点 |
| BlockRazor | 内置 MEV 保护模式 | 需要防三明治攻击的场景 |
| BlockRush | 高吞吐量批量提交 | 批量发送大量交易 |

### 生产中的使用模式

```
交易构建完成
    |
    v
[多通道并发发送]
    |-- RPC 直发（最基础）
    |-- NextBlock（主贿赂通道）
    |-- Temporal（备用贿赂通道）
    |-- BlockRazor（MEV 保护通道）
    |
    v
取第一个成功的结果（BaseTxSender 已实现此逻辑）
```

### BribeServiceManager 健康管理

并行广播模式下，需要对各服务的健康状态进行管理（对比 `01-rpc-client` 的 `StableClient`）：

| 维度 | StableClient（RPC） | BribeServiceManager（贿赂服务） |
|------|---------------------|-------------------------------|
| 发送模式 | 顺序故障转移（A→B→C） | 并行广播（A+B+C 同时发） |
| 设计目标 | 节省资源，一个成功即可 | 最快上链，多发不会重复 |
| 去重机制 | 不需要 | Solana 按交易签名去重 |

错误处理策略：

```
                  健康追踪
                     |
    服务连续失败 N 次 → 标记为不健康
                     |
         并行广播时跳过不健康服务
                     |
    冷却期结束 → 纳入下次广播探测恢复
                     |
    探测成功 → 恢复健康    探测失败 → 继续冷却
                     |
    所有服务不可用 → 触发 onAllDown 告警回调
```

配置参数：
- `MaxConsecutiveFails`：连续失败阈值（默认 3 次），达到后标记不健康
- `CooldownDuration`：冷却时间（默认 30s），过后自动探测恢复
- `SendTimeout`：单次发送超时（默认 5s）

## EVM Anti-MEV

EVM 链上的 MEV 防护主要依赖私有交易池（Private Transaction Pool），交易不进入公开 mempool，攻击者无法观测到：

| 方案 | 链 | 原理 |
|------|------|------|
| Flashbots Protect | Ethereum | 交易直接发送到 Flashbots Builder，不经过公开 mempool |
| MEV Blocker | Ethereum | OFA（Order Flow Auction），搜索者竞价获取订单流 |
| 48 Club | BSC | BSC 上的私有交易池 |
| 链原生方案 | Base/Arbitrum | L2 的 Sequencer 单点排序，天然无公开 mempool |

## 优先费计算

### Solana 优先费

Solana 的优先费通过 Compute Unit Price 设置，影响交易在 Leader 队列中的排序位置：

```
优先费 = Compute Unit Price * Compute Units
```

推荐策略：基于最近 N 个区块的优先费分布，按百分位推荐：
- 低（25th percentile）：省钱但可能排队
- 中（50th percentile）：平衡选择
- 高（75th percentile）：优先处理

### EVM Gas Price / EIP-1559

EVM 链的交易费模型：
- Legacy: gasPrice * gasUsed
- EIP-1559: (baseFee + priorityFee) * gasUsed

RBF（Replace-By-Fee）：用更高的 Gas 重新发送同 nonce 的交易，替换卡住的交易。

## 文件结构

```
06-mev-protection/
    README.md                  -- 模块概述（本文件）
    goals.md                   -- 学习目标与自检问题
    notes.md                   -- 深度技术笔记
    demo/
        sandwich.go            -- 三明治攻击模拟器
        bribe_service.go       -- 贿赂服务实现（mock）+ BribeServiceManager 健康管理
        priority_fee.go        -- 优先费推荐算法
        rbf.go                 -- RBF 加速逻辑
        main.go                -- 可运行演示
        mev_test.go            -- 测试
```

## 与生产系统的差距

| 维度 | 本 demo | 生产系统 |
|------|---------|----------|
| 三明治攻击 | 纯数学模拟，固定储量池 | 实时监控 mempool，分析 pending 交易 |
| 贿赂服务 | mock 实现，模拟延迟和失败 | 真实 HTTP/gRPC 调用，TLS 认证 |
| 优先费 | 手动填充样本数据 | RPC 查询 getRecentPrioritizationFees |
| RBF | 简单 Gas 乘数 | 监控 nonce、动态计算替换 Gas、签名替换 |
| MEV 保护 | 演示概念 | Flashbots Bundle、Jito Bundle、私有 mempool |
| 攻击检测 | 不涉及 | 监控链上交易，检测被三明治攻击的模式 |
| 利润计算 | 不含 Gas 成本 | 精确计算 Gas + 贿赂费 + 滑点的净利润 |
