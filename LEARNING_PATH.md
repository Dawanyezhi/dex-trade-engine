# Onchain-Dex-Lab 学习路径

本文档提供推荐的学习顺序，帮助你系统性地掌握链上 DEX 交易系统的核心知识。

---

## 第一阶段：全局认知

### 00-architecture -- 系统架构总览

- 建议时间：1-2 小时
- 关键文件：`00-architecture/README.md`、`00-architecture/notes.md`
- 学习目标：理解 dexwallet 的整体架构设计，掌握各模块之间的关系和数据流向。建立全局视野后再深入各模块，学习效率会大幅提升。
- 通用层代码：`internal/dexwallet/interfaces.go`（核心接口定义）、`internal/coinset/coinset.go`（链配置）、`internal/bigint/bigint.go`（精确金额处理）

---

## 第二阶段：核心交易链路

按 RPC 通信 -> 协议计算 -> 交易构建 的顺序，掌握一笔交易从发起到上链的完整流程。

### 01-rpc-client -- RPC 客户端与故障转移

- 建议时间：2-3 小时
- 关键文件：
  - `01-rpc-client/demo/stable_client.go` -- 多节点故障转移核心实现
  - `01-rpc-client/demo/solana_rpc.go` -- Solana RPC 适配
  - `01-rpc-client/demo/evm_rpc.go` -- EVM RPC 适配
  - `01-rpc-client/demo/stable_client_test.go` -- 测试用例
- 学习目标：理解多节点健康检查、自动故障转移机制，以及 Solana/EVM 双链 RPC 抽象。

### 02-dex-protocols -- DEX 协议实现

- 建议时间：3-4 小时
- 关键文件：
  - `02-dex-protocols/demo/amm.go` -- 恒定乘积 AMM（如 Raydium/Uniswap V2）
  - `02-dex-protocols/demo/clmm.go` -- 集中流动性（如 Uniswap V3）
  - `02-dex-protocols/demo/bonding_curve.go` -- 联合曲线（如 Pump.fun）
  - `02-dex-protocols/demo/stable_swap.go` -- 稳定币交换（如 Curve）
  - `02-dex-protocols/demo/protocol_test.go` -- 协议测试
- 学习目标：掌握四种主流 DEX 定价模型的数学原理和代码实现。这是理解后续模块的数学基础。

### 03-swap-engine -- 交易引擎

- 建议时间：2-3 小时
- 关键文件：
  - `03-swap-engine/demo/factory.go` -- 交易构建工厂
  - `03-swap-engine/demo/solana_builders.go` -- Solana 交易构建
  - `03-swap-engine/demo/evm_builders.go` -- EVM 交易构建
  - `03-swap-engine/demo/simulator.go` -- 交易模拟
  - `03-swap-engine/demo/swap_test.go` -- 交易测试
- 学习目标：理解如何将协议计算结果转化为链上交易指令，掌握 Solana 和 EVM 交易构建的差异。

---

## 第三阶段：聚合能力

在掌握单协议交易后，学习如何管理池子数据并实现多协议聚合路由。

### 04-pool-management -- 池管理

- 建议时间：2-3 小时
- 关键文件：
  - `04-pool-management/demo/pool_manager.go` -- 池管理器核心逻辑
  - `04-pool-management/demo/repository.go` -- 池数据存储层
  - `04-pool-management/demo/pool_test.go` -- 池管理测试
- 学习目标：理解链上池数据的获取、缓存、更新策略，这是聚合路由的数据基础。

### 05-aggregator-routing -- 聚合路由

- 建议时间：3-4 小时
- 关键文件：
  - `05-aggregator-routing/demo/two_hop_router.go` -- 两跳路由算法
  - `05-aggregator-routing/demo/mock_protocol.go` -- 协议模拟
  - `05-aggregator-routing/demo/mock_pool_manager.go` -- 池管理模拟
  - `05-aggregator-routing/demo/aggregator_test.go` -- 聚合测试
- 学习目标：掌握多协议聚合报价和最优路径选择算法，理解 1inch/Jupiter 类聚合器的核心思路。

---

## 第四阶段：高级特性

这些模块相对独立，可根据兴趣调整学习顺序。

### 06-mev-protection -- MEV 防护

- 建议时间：2-3 小时
- 关键文件：
  - `06-mev-protection/demo/sandwich.go` -- 三明治攻击检测
  - `06-mev-protection/demo/priority_fee.go` -- 优先费用策略
  - `06-mev-protection/demo/bribe_service.go` -- Solana 贿赂服务（Jito）
  - `06-mev-protection/demo/rbf.go` -- EVM Replace-By-Fee 加速
  - `06-mev-protection/demo/mev_test.go` -- MEV 测试
- 学习目标：理解 MEV 攻击原理和防护手段，掌握 Solana/EVM 各自的交易加速机制。

### 07-event-parsing -- 事件解析

- 建议时间：2-3 小时
- 关键文件：
  - `07-event-parsing/demo/parser_registry.go` -- 解析器注册表
  - `07-event-parsing/demo/solana_parsers.go` -- Solana 日志解析
  - `07-event-parsing/demo/evm_parsers.go` -- EVM 事件解析
  - `07-event-parsing/demo/syncer.go` -- 区块同步器
  - `07-event-parsing/demo/event_test.go` -- 事件解析测试
- 学习目标：掌握链上事件/日志的解析方法，理解如何从交易中提取 swap、transfer 等业务数据。

### 08-production-architecture -- 生产架构

- 建议时间：2-3 小时
- 关键文件：
  - `08-production-architecture/demo/rate_limiter.go` -- 限流器
  - `08-production-architecture/demo/chain_config.go` -- 链配置管理
  - `08-production-architecture/demo/monitor_demo.go` -- 监控演示
  - `08-production-architecture/demo/production_test.go` -- 生产架构测试
- 学习目标：学习将 demo 级代码演进为生产级系统所需的工程实践，包括限流、监控、配置管理等。

---

## 总学习时间估算

| 阶段 | 模块 | 预计时间 |
|------|------|----------|
| 第一阶段 | 00-architecture | 1-2 小时 |
| 第二阶段 | 01 + 02 + 03 | 7-10 小时 |
| 第三阶段 | 04 + 05 | 5-7 小时 |
| 第四阶段 | 06 + 07 + 08 | 6-9 小时 |
| **合计** | | **19-28 小时** |

## 辅助资源

- `internal/dexwallet/` -- 通用接口和模型定义，贯穿所有模块
- `cmd/simulate/` -- 端到端模拟场景，适合在学完核心模块后运行体验
- `interview-prep/` -- 面试准备资料，适合学完全部模块后复习巩固
