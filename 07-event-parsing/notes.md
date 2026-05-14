# 07-event-parsing: 深度技术笔记

## 1. 事件解析器注册机制 [dexwallet 通用层]

EventParser 接口和 EventHandler 类型定义在 `internal/dexwallet/interfaces.go`，是通用层的核心抽象。

**为什么使用注册表模式？**

链上事件的种类繁多且不断增加（新 DEX 上线、新合约部署），如果用 `switch-case` 硬编码，每新增一种事件都要修改解析器核心代码。注册表模式将"匹配"和"解析"解耦：

```go
// 注册机制是通用的
registry.Register("RaydiumAMMv4ProgramID", raydiumSwapHandler)
registry.Register("0xd78ad95f...", uniV2SwapHandler)

// 解析逻辑是链特定的
func raydiumSwapHandler(ctx context.Context, rawData []byte) (*ChainEvent, error) {
    // Solana 特定的 Borsh 反序列化...
}
```

**标识符的设计选择：**

- Solana: 使用 ProgramID（程序的公钥地址）作为标识符。同一个 Program 可能处理多种操作，解析器内部需要进一步判断指令类型。
- EVM: 使用 Topic 签名哈希作为标识符。每种事件有唯一的签名哈希，天然适合做 map key。

---

## 2. Solana 事件解析 [链特定层]

### 2.1 交易结构

Solana 交易的核心结构（简化）：

```
Transaction
  |-- Message
  |     |-- AccountKeys: [Pubkey...]     // 所有涉及的账户
  |     |-- Instructions: [Instruction...] // 顶层指令
  |     |     |-- ProgramIdIndex: u8     // 指向 AccountKeys 中的 Program 地址
  |     |     |-- Accounts: [u8...]      // 指向 AccountKeys 中的账户
  |     |     |-- Data: [u8...]          // Borsh 编码的指令数据
  |     |-- RecentBlockhash: Hash        // 交易有效期关联的区块哈希
  |-- Signatures: [Signature...]
```

### 2.2 Raydium AMM Swap 解析

Raydium AMM v4 的 ProgramID: `675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8`

Swap 指令的 Data 解码（Borsh 格式）:
```
[0..8]   instruction_discriminator (swap = 特定 8 字节)
[8..16]  amount_in: u64
[16..24] minimum_amount_out: u64
```

指令中的 Accounts 列表包含了池子地址、Token Vault、用户 Token 账户等，通过索引位置确定各账户的角色。

### 2.3 PumpFun 交易解析

PumpFun 的 ProgramID: `6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P`

PumpFun 使用联合曲线（Bonding Curve），交易模式与标准 AMM 不同：
- 内盘阶段：在联合曲线上直接买卖
- 毕业后：迁移到 Raydium，变成标准 AMM

### 2.4 SPL Token Transfer

Token Program 的 ProgramID: `TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA`

Transfer 指令解析：
```
[0]      instruction_type (3 = transfer)
[1..9]   amount: u64 (小端序)
```

注意 Token2022 Program 有不同的 ProgramID: `TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb`

---

## 3. EVM 事件解析 [链特定层]

### 3.1 事件日志结构

EVM 交易执行后，通过 Receipt 获取事件日志：

```
Receipt
  |-- Status: 1 (成功) / 0 (失败)
  |-- GasUsed: uint64
  |-- Logs: [Log...]
        |-- Address: 合约地址
        |-- Topics: [Hash...]  // 最多 4 个 topic
        |     |-- [0]: 事件签名哈希 (keccak256)
        |     |-- [1..3]: indexed 参数
        |-- Data: []byte       // 非 indexed 参数的 ABI 编码
        |-- BlockNumber: uint64
        |-- TxHash: Hash
        |-- LogIndex: uint
```

### 3.2 UniswapV2 Swap 事件

事件定义：
```solidity
event Swap(
    address indexed sender,
    uint amount0In,
    uint amount1In,
    uint amount0Out,
    uint amount1Out,
    address indexed to
);
```

Topic 签名: `keccak256("Swap(address,uint256,uint256,uint256,uint256,address)")` = `0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822`

解析方式：
- Topics[0]: 匹配 Swap 事件签名
- Topics[1]: sender 地址（indexed）
- Topics[2]: to 地址（indexed）
- Data: ABI 解码得到 amount0In, amount1In, amount0Out, amount1Out

### 3.3 ERC20 Transfer 事件

事件定义：
```solidity
event Transfer(address indexed from, address indexed to, uint256 value);
```

Topic 签名: `keccak256("Transfer(address,address,uint256)")` = `0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef`

解析方式：
- Topics[0]: 匹配 Transfer 事件签名
- Topics[1]: from 地址
- Topics[2]: to 地址
- Data: ABI 解码得到 value

---

## 4. Syncer 同步设计 [dexwallet 通用层]

### 4.1 主循环设计

Syncer 的主循环是一个典型的"生产者-消费者"模式：

```go
func (s *BlockSyncer) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            block, err := s.blockProducer.NextBlock()
            if err != nil {
                // 等待后重试
                continue
            }
            if s.detectReorg(block) {
                s.rollback()
                continue
            }
            events := s.parseBlock(block)
            s.dispatchEvents(events)
            s.advanceHeight(block)
        }
    }
}
```

关键设计决策：
- **轮询 vs 推送**: 本 demo 使用轮询。生产中 Solana 用 WebSocket 订阅 slot，EVM 用 `newHeads` 订阅。
- **错误恢复**: 解析单笔交易失败不应停止整个同步，应该记录错误并跳过。
- **优雅退出**: 通过 context 取消信号实现，确保当前区块处理完毕后再退出。

### 4.2 重组检测

维护一个最近区块的哈希链表：

```
blockHashes: map[uint64]string
  100 -> "hash_100"
  101 -> "hash_101"
  102 -> "hash_102"  <-- currentHeight
```

新区块到来时：
```go
func (s *BlockSyncer) detectReorg(block *MockBlock) bool {
    if block.Height <= s.currentHeight {
        return true // 高度倒退，可能是重组
    }
    if expectedHash, ok := s.blockHashes[block.Height-1]; ok {
        return block.ParentHash != expectedHash
    }
    return false
}
```

### 4.3 回滚策略

检测到重组后的处理：
1. 从当前高度向前回溯，找到分叉点（最后一个 hash 匹配的区块）
2. 撤销分叉点之后的所有状态变更
3. 从分叉点重新开始同步

生产中的复杂性：
- 状态变更可能已经写入数据库，需要用补偿事务撤销
- 如果事件已经通过消息队列分发给下游，需要发送"撤销事件"
- 深度重组（超过确认数）在正常情况下不应该发生，需要触发告警

---

## 5. 并发安全 [dexwallet 通用层]

### 5.1 注册表的读写分离

ParserRegistry 使用 `sync.RWMutex` 实现读写分离：

- **Register（写操作）**: 通常在启动阶段调用，频率低，使用 `Lock`
- **Parse（读操作）**: 在运行时高频调用，使用 `RLock`，允许多个 goroutine 并发解析

```go
func (r *ParserRegistry) Register(identifier string, handler EventHandler) {
    r.mu.Lock()         // 写锁 -- 独占
    defer r.mu.Unlock()
    r.handlers[identifier] = handler
}

func (r *ParserRegistry) Parse(ctx context.Context, rawTx []byte) ([]ChainEvent, error) {
    r.mu.RLock()        // 读锁 -- 共享
    defer r.mu.RUnlock()
    // ... 查找 handler 并解析
}
```

### 5.2 Syncer 的线程安全

BlockSyncer 的 `currentHeight` 需要原子访问：
- 主循环写入（推进高度）
- 外部读取（`GetCurrentHeight()` 用于监控）

可以使用 `sync/atomic` 或 `sync.RWMutex`。本 demo 使用 mutex 与其他状态一起保护。

---

## 6. 金额处理 [dexwallet 通用层]

### 6.1 为什么用 big.Int

链上金额都是整数（最小单位）：
- Solana SOL: 1 SOL = 10^9 lamports
- EVM ETH: 1 ETH = 10^18 wei

`uint64` 的最大值约 1.8 * 10^19，对于 wei 级别的大额转账会溢出。`math/big.Int` 支持任意精度整数运算。

### 6.2 注意事项

- 永远不要用 float64 表示金额（精度丢失）
- 比较金额用 `Cmp` 方法而不是 `==`（big.Int 是指针类型）
- JSON 序列化 big.Int 需要自定义 MarshalJSON/UnmarshalJSON

---

## 7. 日志设计 [dexwallet 通用层]

### 7.1 事件解析的日志策略

```go
// 正常解析 -- Debug 级别（量大，按需开启）
slog.Debug("event parsed", "type", event.Type, "tx", event.TxHash, "dex", event.DexID)

// 未知事件 -- Debug 级别（不是错误，只是未注册的事件类型）
slog.Debug("unknown event identifier, skipped", "identifier", id)

// 解析失败 -- Warn 级别（单笔失败不影响整体）
slog.Warn("event parse failed", "tx", txHash, "error", err)

// 重组检测 -- Warn 级别（需要关注但不是异常）
slog.Warn("reorg detected", "height", block.Height, "expected_parent", expected, "actual_parent", block.ParentHash)

// 同步停滞 -- Error 级别（需要干预）
slog.Error("syncer stalled", "current_height", s.currentHeight, "gap", gap)
```

### 7.2 结构化字段规范

事件相关日志建议包含的字段：
- `chain`: 链标识（solana/bsc/ethereum）
- `height`: 区块高度
- `tx`: 交易哈希
- `event_type`: 事件类型（swap/transfer/mint/burn）
- `dex`: DEX 标识
- `latency_ms`: 处理延迟（毫秒）
