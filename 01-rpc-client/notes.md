# 01-rpc-client: 深度技术笔记

## 1. RPCClient 接口设计哲学 [dexwallet 通用层]

RPCClient 接口定义在 `internal/dexwallet/interfaces.go`，是整个系统访问链上数据的最底层抽象。

**为什么只有 4 个通用方法？**

```go
type RPCClient interface {
    SendTransaction(ctx context.Context, txData []byte) (string, error)
    GetBalance(ctx context.Context, address string) (*big.Int, error)
    GetBlockHeight(ctx context.Context) (uint64, error)
    IsHealthy(ctx context.Context) bool
    ChainID() coinset.ChainID
}
```

这 4 个方法是 Solana 和 EVM **都需要**的最小公共子集。生产中 Solana 需要 `getAccountInfo`、`getMultipleAccounts`、`simulateTransaction` 等，EVM 需要 `eth_call`、`eth_estimateGas`、`eth_getLogs` 等，这些链特定方法通过各自的扩展接口定义（如 `SolanaRPCClient`、`EVMRPCClient`），不污染通用接口。

**txData 为什么是 []byte？**

交易的序列化格式在两条链上完全不同：
- Solana: 经过 Borsh 序列化 + base58/base64 编码的交易
- EVM: RLP 编码 + 签名的原始交易字节

用 `[]byte` 可以避免在通用层引入链特定的交易结构体。构建交易是 SwapBuilder 的职责，RPCClient 只负责「发出去」。

---

## 2. 多节点故障转移 [dexwallet 通用层]

### 2.1 为什么需要多节点

生产环境中，RPC 节点的不可用原因：
- **限速（Rate Limiting）**: 公共节点（如 Solana 的 api.mainnet-beta.solana.com）有严格 QPS 限制
- **网络分区**: 节点与主网断连，返回过时数据
- **区块高度落后**: 节点同步慢，查询到的状态不是最新的
- **宕机维护**: 节点提供商的计划内/外停机

生产中的 RPC 配置示例（通过 coinset.ChainConfig.RPCEndpoints）:
```
Solana:
  - https://mainnet.helius-rpc.com/?api-key=xxx  (主节点，付费)
  - https://rpc.shyft.to/?api_key=xxx            (备用)
  - https://api.mainnet-beta.solana.com           (公共，兜底)

BSC:
  - https://bsc-dataseed.bnbchain.org             (官方)
  - https://bsc-dataseed1.defibit.io              (第三方)
  - https://bsc-dataseed2.ninicoin.io             (第三方)
```

### 2.2 StableClient 的故障转移策略

本 demo 采用最简单的「有序遍历」策略：

```
请求进入 --> 尝试 activeNode
                |
            成功? --> 返回结果
                |
            失败 --> 标记不健康，尝试下一个节点
                        |
                    遍历完所有节点都失败? --> 返回错误
```

**并发安全的关键点：**

1. `activeIdx` 的读写需要锁保护，防止多个 goroutine 同时切换导致混乱
2. 健康状态使用 `atomic.Bool` 或在锁内读写
3. 故障转移过程中，已经发出的请求不应被中断

### 2.3 生产中的改进方向

**加权评分模型**：不是简单的健康/不健康二分，而是给每个节点打分：

```
score = w1 * (1/latency_ms) + w2 * (1-error_rate) + w3 * (1/(height_diff+1))
```

- `latency_ms`: 最近 N 次请求的平均延迟
- `error_rate`: 最近 N 次请求的错误率
- `height_diff`: 节点区块高度与已知最大高度的差值

**熔断器模式（Circuit Breaker）**：

```
CLOSED (正常) --[连续 N 次失败]--> OPEN (熔断，直接跳过)
OPEN --[等待冷却期]--> HALF-OPEN (允许一次试探)
HALF-OPEN --[成功]--> CLOSED
HALF-OPEN --[失败]--> OPEN
```

---

## 3. Solana RPC 特点 [链特定层]

### 3.1 JSON-RPC 协议

Solana 使用标准 JSON-RPC 2.0，但有自己的方法命名和参数约定：

```json
// 请求
{"jsonrpc":"2.0","id":1,"method":"getBalance","params":["address..."]}

// 响应
{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":123},"value":1000000000}}
```

注意 Solana 的响应通常包含 `context.slot`，表示数据对应的 slot 号。这对判断数据新鲜度很重要。

### 3.2 Commitment Level

Solana 独有的确认级别概念：
- `processed`: 节点本地处理完毕（可能回滚）
- `confirmed`: 超过 2/3 验证者确认（大概率不回滚）
- `finalized`: 完全确定（不可回滚）

生产中做 Swap 用 `confirmed`，查余额通常也用 `confirmed`，只有关键的出入金用 `finalized`。

### 3.3 Solana 特有 RPC 方法

生产中常用但本 demo 未实现的方法：
- `getAccountInfo`: 获取账户数据（池子状态、代币余额都靠这个）
- `getMultipleAccounts`: 批量获取（减少 RPC 调用次数）
- `simulateTransaction`: 模拟执行（预判交易是否会失败）
- `getRecentBlockhash` / `getLatestBlockhash`: 获取最新区块哈希（交易需要带这个）
- `getSignaturesForAddress`: 查询地址的历史交易签名

---

## 4. EVM RPC 特点 [链特定层]

### 4.1 Ethereum JSON-RPC

EVM 链的 RPC 更加标准化，几乎所有 EVM 链都遵循以太坊的 JSON-RPC 规范：

```json
// 请求
{"jsonrpc":"2.0","id":1,"method":"eth_getBalance","params":["0xaddr...","latest"]}

// 响应
{"jsonrpc":"2.0","id":1,"result":"0xde0b6b3a7640000"}
```

注意返回值是十六进制字符串。

### 4.2 区块标签

EVM 的区块参数：
- `latest`: 最新已确认区块
- `pending`: 待打包的交易池状态
- `earliest`: 创世区块
- `safe`: 安全区块（不太可能重组）
- `finalized`: 最终确认区块

对应 Solana 的 commitment level，但语义不完全相同。

### 4.3 EVM 特有 RPC 方法

生产中常用但本 demo 未实现的方法：
- `eth_call`: 模拟执行合约调用（不上链，用于读取合约状态、模拟 Swap）
- `eth_estimateGas`: 估算 Gas 消耗
- `eth_sendRawTransaction`: 发送已签名的原始交易
- `eth_getLogs`: 查询事件日志（按合约地址、topic 过滤）
- `eth_getTransactionReceipt`: 获取交易回执（确认状态、Gas 实际消耗、事件日志）
- `eth_getCode`: 获取合约字节码（判断地址是否为合约）

### 4.4 EVM 链之间的差异

虽然都是 EVM，但各链有细微差别：
- **Gas 模型**: Ethereum/Base 支持 EIP-1559（baseFee + priorityFee），BSC 不支持
- **出块时间**: Ethereum 12s, BSC 3s, Base 2s
- **确认数**: Ethereum 12, BSC 15, Base 10
- **Anti-MEV**: 部分链支持 Anti-MEV RPC（如 Flashbots Protect），防止交易被三明治攻击

这些差异通过 `coinset.ChainConfig` 的 `Features` 和 `BlockTime`、`Confirmations` 参数化。

---

## 5. 并发安全设计 [dexwallet 通用层]

### 5.1 sync.RWMutex 的使用场景

StableClient 中的典型读写分离：

- **读操作**（RLock）: 获取当前活跃节点、读取健康状态。多个 goroutine 可以同时读。
- **写操作**（Lock）: 切换活跃节点、更新健康状态。必须独占。

```go
// 读路径 -- 高频，允许并发
func (s *StableClient) getActiveNode() *NodeEntry {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.nodes[s.activeIdx]
}

// 写路径 -- 低频，独占
func (s *StableClient) switchToNext() {
    s.mu.Lock()
    defer s.mu.Unlock()
    // ... 切换逻辑
}
```

### 5.2 避免锁竞争的技巧

1. **缩小锁的范围**: 只在访问共享状态时加锁，RPC 调用本身不需要持锁
2. **避免持锁执行 IO**: 先在锁内拷贝需要的状态，释放锁后再执行 RPC 调用
3. **考虑 atomic 替代**: 对于简单的布尔值或整数，`sync/atomic` 比 mutex 更轻量

---

## 6. 错误处理模式 [dexwallet 通用层]

### 6.1 错误包装链

```go
// 底层
func (c *SolanaRPCClient) GetBalance(ctx context.Context, addr string) (*big.Int, error) {
    return nil, fmt.Errorf("rpc call getBalance: %w", err)
}

// StableClient 层
func (s *StableClient) GetBalance(ctx context.Context, addr string) (*big.Int, error) {
    // ... 故障转移逻辑 ...
    return nil, fmt.Errorf("all %d nodes failed for GetBalance: %w", len(s.nodes), lastErr)
}
```

调用方可以用 `errors.Is` 或 `errors.As` 判断底层错误类型，而不丢失上下文信息。

### 6.2 生产中的错误分类

不是所有错误都应该触发节点切换：
- **应该切换**: 连接超时、连接拒绝、HTTP 5xx、节点返回 -32000 系列内部错误
- **不应该切换**: 交易签名无效（客户端问题）、余额不足（业务错误）、参数格式错误

本 demo 简化处理，所有错误都触发切换。生产中需要更细粒度的错误分类。

---

## 7. Context 的传播与超时 [dexwallet 通用层]

### 7.1 超时控制层次

```
用户请求 context (30s 总超时)
  |-- StableClient (每个节点尝试 5s)
        |-- HTTP 请求 (3s 连接超时)
```

每一层都应该尊重上层的 context，同时可以施加更紧的超时：

```go
func (s *StableClient) callWithFailover(ctx context.Context, fn func(RPCClient) error) error {
    for _, node := range s.nodes {
        nodeCtx, cancel := context.WithTimeout(ctx, s.perNodeTimeout)
        err := fn(node.client)
        cancel()
        if err == nil {
            return nil
        }
        // 如果是上层 context 取消，不再重试
        if ctx.Err() != nil {
            return ctx.Err()
        }
    }
    return ErrAllNodesFailed
}
```

### 7.2 健康检查 goroutine 的退出

```go
func (s *StableClient) StartHealthCheck(ctx context.Context) {
    ticker := time.NewTicker(s.healthInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return // 优雅退出
        case <-ticker.C:
            s.checkAllNodes(ctx)
        }
    }
}
```

通过传入可取消的 context，调用方（通常是 main 函数或服务管理器）可以控制健康检查的生命周期。

---

## 8. 日志设计 [dexwallet 通用层]

### 8.1 结构化日志

使用 `log/slog` 而非 `fmt.Printf` 或 `log.Printf`，因为：
1. 结构化字段方便日志系统索引和查询
2. 级别控制（Debug/Info/Warn/Error）可以按环境配置
3. 与 OpenTelemetry 等可观测性工具集成更好

```go
slog.Info("rpc node switched",
    "chain", s.chainID,
    "from_node", oldNode.endpoint,
    "to_node", newNode.endpoint,
    "reason", "health_check_failed",
)
```

### 8.2 日志级别约定

- `Debug`: 每次 RPC 请求的详情（开发时开启）
- `Info`: 节点切换、健康检查结果
- `Warn`: 单个节点不健康、超时
- `Error`: 所有节点不可用、无法恢复的错误
