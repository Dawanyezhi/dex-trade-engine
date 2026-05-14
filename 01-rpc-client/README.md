# 01-rpc-client: RPC 客户端与多节点故障转移

## 模块概述

本模块实现 Solana 和 EVM 链的 RPC 客户端封装，核心是 `StableClient` -- 一个支持多节点故障转移的稳定 RPC 客户端。

在生产环境中，单一 RPC 节点不可靠（限速、宕机、网络分区），因此需要：
1. 维护多个 RPC 节点端点
2. 周期性健康检查
3. 故障时自动切换到健康节点
4. 全部不可用时触发告警

## 核心实现

### StableClient（多节点故障转移）

`StableClient` 是 `dexwallet.RPCClient` 接口的包装层，内部维护一个有序的节点列表：

```
StableClient
  |-- nodes: []*NodeEntry      // 所有候选节点
  |-- activeIdx: int           // 当前活跃节点索引
  |-- mu: sync.RWMutex         // 并发保护
  |-- healthInterval: Duration // 健康检查间隔
```

**故障转移流程：**

1. 调用方调用 StableClient 的任意 RPCClient 方法
2. StableClient 将请求转发给当前活跃节点
3. 如果调用失败，标记该节点为不健康
4. 遍历节点列表，找到下一个健康节点
5. 如果所有节点都不健康，返回错误并触发告警

**健康检查机制：**

- 后台 goroutine 按固定间隔对所有节点执行 `IsHealthy` 检查
- 不健康节点在恢复后自动重新加入候选列表
- 检查结果通过 `NodeEntry.healthy` 字段记录

### SolanaRPCClient（Solana Mock）

模拟 Solana JSON-RPC 响应：
- `SendTransaction` -- 模拟交易发送，返回 base58 格式的交易签名
- `GetBalance` -- 模拟余额查询，返回 lamports
- `GetBlockHeight` -- 模拟区块高度查询

### EVMRPCClient（EVM Mock）

模拟 EVM JSON-RPC 响应：
- `SendTransaction` -- 模拟交易发送，返回 0x 前缀的交易哈希
- `GetBalance` -- 模拟余额查询，返回 wei
- `GetBlockHeight` -- 模拟区块高度查询

## 文件结构

```
01-rpc-client/
  README.md              -- 模块概述（本文件）
  goals.md               -- 学习目标与自检问题
  notes.md               -- 深度技术笔记
  demo/
    stable_client.go     -- StableClient 多节点故障转移实现
    solana_rpc.go        -- Solana RPC 客户端 mock
    evm_rpc.go           -- EVM RPC 客户端 mock
    main.go              -- 可运行的演示程序
    stable_client_test.go -- 测试
```

## 与生产系统的差距

| 维度 | 本 demo | 生产系统 |
|------|---------|----------|
| RPC 调用 | 内存 mock，无网络 IO | 真实 HTTP/WebSocket 连接，带超时和重试 |
| 健康检查 | 简单布尔标记 | 多维度指标（延迟、错误率、区块高度差） |
| 节点选择 | 顺序遍历 | 加权轮询 / 最小延迟 / 自适应选择 |
| 限速保护 | 无 | 每节点独立限速器（令牌桶） |
| WebSocket | 不支持 | 支持 WSS 订阅（新区块、账户变更） |
| 连接池 | 不涉及 | http.Client 复用 + 连接池管理 |
| 指标上报 | 仅日志 | Prometheus metrics（延迟分布、错误率、节点状态） |
| 告警 | 控制台日志 | 钉钉/Lark/PagerDuty 多通道告警 |
| 链特定方法 | 仅通用接口 | Solana: getAccountInfo/simulateTransaction 等；EVM: eth_call/eth_estimateGas/debug_traceCall 等 |
