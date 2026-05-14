# 07-event-parsing: 链上事件解析与同步

## 模块概述

本模块实现链上事件的解析与区块同步机制。核心挑战在于 Solana 和 EVM 的事件模型完全不同，但最终需要输出统一的 `ChainEvent` 结构。

在生产环境中，事件解析是 DEX 钱包的"感知层" -- 通过实时解析链上交易，系统才能知道：
1. 哪些池子发生了 Swap（需要更新池子状态）
2. 哪些代币发生了 Transfer（需要更新余额）
3. 哪些池子有 Mint/Burn/Liquidity 变化（需要重新计算最优路由）

## 核心设计：Solana 指令解析 vs EVM 事件解析

### Solana：按 ProgramID 解析指令

Solana 交易由一组指令（Instruction）构成，每条指令指向一个 Program（合约）：

```
Transaction
  |-- Instruction[0]: ProgramID=RaydiumAMMv4, Data=[swap指令编码]
  |-- Instruction[1]: ProgramID=TokenProgram, Data=[transfer指令编码]
  |-- Instruction[2]: ProgramID=ComputeBudget, Data=[设置CU上限]
```

解析流程：
1. 遍历交易中的所有指令
2. 按 ProgramID 匹配到对应的解析器（如 Raydium 解析器、PumpFun 解析器）
3. 解码指令 Data（Borsh 序列化格式）
4. 提取出 Swap/Transfer 等事件信息

关键差异点：
- Solana 没有"事件日志"的标准概念（Program 可以通过 `msg!` 输出日志，但不像 EVM 那样结构化）
- CPI（跨程序调用）会产生"内部指令"，需要递归解析
- 同一笔交易可能包含多个 DEX 的 Swap（聚合器场景）

### EVM：按 Topic 签名解析事件日志

EVM 交易执行后产生 Receipt，其中包含结构化的事件日志（Event Log）：

```
TransactionReceipt
  |-- Log[0]: Address=UniswapV2Pair, Topics=[Swap签名,sender,to], Data=[amount0In,...]
  |-- Log[1]: Address=ERC20Token, Topics=[Transfer签名,from,to], Data=[value]
```

解析流程：
1. 获取交易 Receipt 中的所有 Log
2. 按 Topics[0]（事件签名哈希）匹配到对应的解析器
3. ABI 解码 Data 字段
4. 提取出 Swap/Transfer 等事件信息

关键差异点：
- Topics[0] 是事件签名的 keccak256 哈希（如 `Swap(address,uint256,uint256,uint256,uint256,address)` 的哈希）
- indexed 参数出现在 Topics[1..3]，非 indexed 参数 ABI 编码在 Data 中
- 不同 DEX 可能使用不同的 Swap 事件签名（UniV2 vs UniV3 vs Curve）

### 统一输出：ChainEvent

无论 Solana 还是 EVM，解析结果统一为 `dexwallet.ChainEvent`，这是 dexwallet 通用层的核心设计。

## Syncer 同步流程

BlockSyncer 实现了区块同步的主循环：

```
启动
  |
  v
轮询新区块 --[有新区块]--> 检查重组
  ^                           |
  |                       [无重组] --> 逐笔解析交易 --> 通过回调更新池子 --> 推进高度
  |                           |
  |                       [检测到重组] --> 回滚到分叉点 --> 重新同步
  |                           |
  +---------------------------+
```

### 重组检测

区块重组（Reorg）是指链上的区块被另一条更长的分叉替代。检测方式：

1. 记录已处理的每个区块的 Hash
2. 新区块到来时，检查其 `ParentHash` 是否等于上一个已处理区块的 Hash
3. 如果不相等，说明发生了重组，需要回滚到分叉点

```
正常情况:  Block[100].Hash == Block[101].ParentHash
重组情况:  Block[100].Hash != Block[101'].ParentHash  (101' 是新分叉的区块)
```

### 同步策略

- **轮询间隔**: 根据链的出块时间设置（Solana 400ms, BSC 3s, Ethereum 12s）
- **批量处理**: 如果落后多个区块，一次性追赶
- **幂等性**: 同一个区块重复处理不应产生副作用

## 文件结构

```
07-event-parsing/
  README.md                -- 模块概述（本文件）
  goals.md                 -- 学习目标与自检问题
  notes.md                 -- 深度技术笔记
  demo/
    parser_registry.go     -- 事件解析器注册机制（支持 CPI、复合键匹配）
    solana_parsers.go      -- Solana 事件解析器（Raydium/PumpFun/Transfer）
    evm_parsers.go         -- EVM 事件解析器（UniV2 Swap/ERC20 Transfer）
    uniswap_v3_parser.go   -- UniswapV3 Swap 解析器（有符号金额、sqrtPriceX96）
    inner_instructions.go  -- CPI 递归解析（FlattenInstructions 展平内部指令）
    balance_diff.go        -- 余额差异计算与交叉验证
    tx_classifier.go       -- 交易分类器（4×4 地址类型矩阵 → TxType）
    address_filter.go      -- 地址过滤器（Bloom Filter 原理 + map 实现）
    pipeline.go            -- Pipeline Stage 管道架构（5 阶段链式处理）
    syncer.go              -- BlockSyncer 区块同步主循环
    main.go                -- 可运行的演示程序
    event_test.go          -- 基础测试
    advanced_test.go       -- 新增组件测试（CPI/余额/分类/过滤/管道/V3）
```

## 与生产系统的差距

| 维度 | 本 demo | 生产系统 |
|------|---------|----------|
| 数据来源 | JSON 模拟的交易数据 | 真实链上 RPC 获取的区块和交易 |
| 指令解析 | 按字符串匹配 ProgramID/Topic | Borsh 反序列化（Solana）/ ABI 解码（EVM） |
| 重组处理 | 简单的 parentHash 检查 | 维护区块链的本地副本，支持多层回滚 |
| 同步性能 | 单线程串行处理 | 多 goroutine 并行解析 + 批量写入 |
| 存储 | 内存中的高度和事件 | 数据库持久化（PostgreSQL/Redis） |
| 事件分发 | 回调函数 | 消息队列（Kafka/NATS）+ 事件总线 |
| 错误恢复 | 无 | 断点续传（记录 checkpoint）+ 死信队列 |
| 事件类型 | 5 种基础类型 | 数十种（包括各 DEX 特有的事件） |
| 监控 | 日志输出 | Prometheus 指标（同步延迟、解析错误率、重组次数） |
| 过滤 | 全量解析 | 布隆过滤器 + 地址白名单，只关注相关交易 |
