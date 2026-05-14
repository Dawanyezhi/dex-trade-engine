# 06-mev-protection: 深度技术笔记

## 1. 三明治攻击的数学模型 [L2 核心实现]

### 1.1 攻击原理

三明治攻击利用 AMM 恒定乘积公式（x * y = k）的确定性来提取利润。

设池子初始状态为 (x, y)，手续费率为 f（基点）：

```
步骤 1 -- 攻击者前置交易（买入 amountA 个 tokenIn）:
    effectiveA = amountA * (10000 - f) / 10000
    outputA = y * effectiveA / (x + effectiveA)
    新池子状态: (x + amountA, y - outputA)

步骤 2 -- 受害者交易（买入 amountV 个 tokenIn）:
    x' = x + amountA
    y' = y - outputA
    effectiveV = amountV * (10000 - f) / 10000
    outputV = y' * effectiveV / (x' + effectiveV)
    新池子状态: (x' + amountV, y' - outputV)

步骤 3 -- 攻击者后置交易（卖出 outputA 个 tokenOut）:
    x'' = x' + amountV  (此时 tokenOut 的储量)
    y'' = y' - outputV  (此时 tokenIn 的储量)
    注意：攻击者卖出的是 tokenOut，所以 reserveIn = y'' - outputV 对应的另一侧
    attackerReturn = 用 outputA 换回的 tokenIn 数量
    利润 = attackerReturn - amountA
```

### 1.2 关键洞察

**流动性深度决定攻击收益**：池子越浅，同样金额的前置交易推高价格的幅度越大。

```
浅池 (x=1000):  100 的输入 -> 约 9.1% 的价格变动
深池 (x=100000): 100 的输入 -> 约 0.1% 的价格变动
```

**受害者滑点容忍度是攻击者利润的上界**：攻击者最多让受害者的实际价格偏移到滑点边界。如果受害者设置 1% 滑点，攻击者理论上最多能提取接近 1% 的价值（扣除 Gas 后）。

**攻击者也需要支付手续费**：前置和后置交易各支付一次手续费，这是攻击成本的一部分。手续费率越高，三明治攻击的利润越薄。

---

## 2. Solana 贿赂服务生态 [L2 核心实现 / L3 进阶设计]

### 2.1 为什么 Solana 需要贿赂服务

Solana 的交易处理流程与 EVM 链有本质区别：

```
EVM (Ethereum):
    用户 -> mempool（公开） -> Builder 排序 -> Proposer 出块

Solana:
    用户 -> RPC 节点 -> Leader 节点（固定调度） -> 出块
```

Solana 没有全局 mempool，交易直接发送到当前 Leader 节点。Leader 按 Priority Fee 排序交易。贿赂服务本质上是：
1. 帮你更快地把交易送到 Leader 节点（网络优化）
2. 附加额外的 Tip 交易以激励 Leader 优先处理（经济激励）

### 2.2 五个服务商的定位差异

**NextBlock** -- 基础设施型
- 在多个地理区域部署转发节点
- 与大量验证者建立直连通道
- 覆盖率高，适合通用场景
- 费率中等

**Temporal** -- 延迟优化型
- 全球 CDN 加速网络
- 专注于最低延迟的交易提交
- 适合对时间敏感的套利交易
- 费率偏高

**ZeroSlot** -- 滑点优化型
- 优化交易在 Leader 队列中的位置
- 减少交易等待时间导致的额外滑点
- 适合大额交易
- 费率中等偏高

**BlockRazor** -- MEV 保护型
- 内置三明治攻击检测
- 将交易路由到安全的 Leader 通道
- 牺牲部分速度换取安全性
- 费率偏高

**BlockRush** -- 吞吐量型
- 优化批量交易的提交效率
- 支持同时提交大量交易
- 适合做市商和套利机器人
- 费率按批量折扣

### 2.3 多通道并发发送策略 [L2]

`BaseTxSender.Send` 的核心设计（已在 `internal/dexwallet/tx_sender.go` 实现）：

```
创建 N+1 个 goroutine（1 个 RPC + N 个贿赂服务）
    |
    v
每个 goroutine 独立尝试发送
    |
    v
使用 buffered channel 收集结果
    |
    v
第一个成功结果立即返回（其余 goroutine 被 context 取消）
    |
    v
如果所有通道失败，返回最后一个错误
```

**为什么取「第一个成功」而不是等所有结果？**

在 Solana 上，同一笔交易（相同签名）提交到多个通道是安全的 -- 链上会去重。但速度至关重要：Leader Slot 只有 400ms，越早到达 Leader 越好。所以第一个成功意味着交易已经被接受，其余通道的结果不影响交易的链上执行。

**故障降级模式**：

```
5 个贿赂服务都可用:   5 通道并发，成功率最高
3 个可用 + 2 个超时:  3 通道并发 + RPC 兜底
全部贿赂服务不可用:   仅 RPC 直发（无 MEV 保护）
```

---

## 3. EVM Anti-MEV 方案 [L2 核心实现 / L3 进阶设计]

### 3.1 Flashbots Protect

```
普通交易流:
    用户 -> 公开 mempool -> 搜索者可见 -> 被三明治攻击

Flashbots Protect 流:
    用户 -> Flashbots RPC -> Flashbots Builder -> 直接打包进区块
                             (不进入公开 mempool)
```

使用方式：将 RPC 端点替换为 `https://rpc.flashbots.net`。

优点：
- 交易对搜索者不可见
- 失败的交易不上链（不浪费 Gas）
- 无需修改交易逻辑

缺点：
- 可能延迟更高（需要等 Flashbots Builder 出块）
- 依赖 Flashbots 的中心化基础设施
- 仅适用于 Ethereum 主网

### 3.2 MEV Blocker (OFA)

OFA（Order Flow Auction）模式：

```
用户交易 -> MEV Blocker -> 搜索者竞价 -> 最高出价的搜索者执行回退
                                          |
                                          v
                                    用户获得部分 MEV 退款
```

用户的交易仍然可能被「善意利用」（back-run），但搜索者之间的竞争会把大部分利润返还给用户。

### 3.3 实现层面 [L2]

在代码中，Anti-MEV RPC 本质上就是一个换了端点的 RPCClient：

```go
type AntiMEVRPC struct {
    endpoint string   // "https://rpc.flashbots.net" 或 "https://mevblocker.io"
    provider string   // "flashbots_protect" 或 "mev_blocker"
}
```

它实现 `BribeService` 接口，但 `GetRecommendedFee` 返回 0（不需要额外费用，保护是免费的）。`Send` 方法将交易发送到私有 mempool 端点。

---

## 4. 优先费推荐算法 [L2 核心实现]

### 4.1 百分位数方法

基于最近 N 个区块的优先费样本，排序后按百分位取值：

```
样本: [100, 200, 300, 400, 500, 600, 700, 800, 900, 1000]
排序后: [100, 200, 300, 400, 500, 600, 700, 800, 900, 1000]

25th percentile (index = len*25/100 = 2):  300  -> 低优先级
50th percentile (index = len*50/100 = 5):  600  -> 中优先级
75th percentile (index = len*75/100 = 7):  800  -> 高优先级
```

**为什么用百分位而不用平均值？**

平均值容易被极端值拉偏。如果某个区块有一笔优先费 1000000 的交易（可能是套利机器人），平均值会被严重高估，但 50th 百分位不受影响。

### 4.2 Solana 的 Compute Unit Price [L3]

生产中通过 `getRecentPrioritizationFees` RPC 获取数据：

```json
// 请求
{"method": "getRecentPrioritizationFees", "params": [["程序地址"]]}

// 响应
[
    {"slot": 123, "prioritizationFee": 500},
    {"slot": 124, "prioritizationFee": 800},
    ...
]
```

返回最近 150 个 slot 的数据。需要注意：
- 返回的是针对特定程序地址的费用，不同程序（Raydium vs Jupiter）的费用分布不同
- 网络突然拥堵时，历史数据会低估当前需要的费用
- 建议在百分位基础上加一个安全边际（如 +20%）

### 4.3 EVM EIP-1559 [L3]

EIP-1559 模型下，交易费 = baseFee + priorityFee:

- **baseFee**: 由协议根据区块利用率自动调整，不可手动设置
- **priorityFee (maxPriorityFeePerGas)**: 给验证者的小费，可自由设置
- **maxFeePerGas**: 用户愿意支付的最大总费用

```
baseFee 调整规则:
    上一个区块 > 50% 满 -> baseFee 增加（最多 12.5%）
    上一个区块 < 50% 满 -> baseFee 减少（最多 12.5%）
    上一个区块 = 50% 满 -> baseFee 不变
```

---

## 5. RBF 交易替换 [L2 核心实现 / L3 进阶设计]

### 5.1 EVM RBF 机制

当一笔交易卡住（Gas 太低、网络拥堵）时，可以发送同 nonce 但更高 Gas 的新交易来替换它：

```
原始交易: nonce=5, gasPrice=20 Gwei, data=swap(...)
替换交易: nonce=5, gasPrice=26 Gwei, data=swap(...)  // 至少 +10% 或 +1 Gwei
```

节点会丢弃旧交易，保留 Gas 更高的新交易。

### 5.2 替换条件

不同客户端对 RBF 的最低 Gas 增幅要求不同：
- Geth: 新 gasPrice >= 旧 gasPrice * 110%（至少加 10%）
- Erigon: 类似要求
- 实际操作中建议至少加 30%（1.3x），以确保被接受

### 5.3 取消交易

特殊的 RBF 应用：发送同 nonce、value=0、to=自己的交易，用更高 Gas 替换原交易：

```
原始交易: nonce=5, to=UniswapRouter, value=0, data=swap(...)
取消交易: nonce=5, to=自己的地址,   value=0, data=0x      // 空数据
```

### 5.4 Solana 的差异 [L3]

Solana 没有 nonce 概念，每笔交易绑定一个 recent blockhash（有效期约 60 秒）。交易过期后自动失效，不需要 RBF。

Solana 的「重试」策略是：
1. 重新获取最新的 blockhash
2. 重新签名交易
3. 重新发送（可能提高优先费）

这不是真正的替换，而是发送一笔新的交易。

---

## 6. 并发安全设计 [L2 核心实现]

### 6.1 贿赂服务的并发发送

`BaseTxSender.Send` 中的关键并发模式：

```go
// 1. 拷贝贿赂服务列表（缩小锁范围）
s.mu.Lock()
bribes := make([]BribeService, len(s.bribes))
copy(bribes, s.bribes)
s.mu.Unlock()

// 2. 创建带超时的 context
sendCtx, cancel := context.WithTimeout(ctx, s.sendTimeout)
defer cancel()

// 3. 扇出：每个通道一个 goroutine
ch := make(chan result, 1+len(bribes)) // buffered，防止 goroutine 泄漏
for _, b := range bribes {
    go func(bribe BribeService) {
        hash, err := bribe.Send(sendCtx, txData, nil)
        ch <- result{txHash: hash, channel: bribe.Name(), err: err}
    }(b)
}

// 4. 收集结果
for i := 0; i < totalChannels; i++ {
    select {
    case r := <-ch:
        if r.err == nil {
            return r.txHash, nil // 第一个成功就返回
        }
    case <-sendCtx.Done():
        return "", fmt.Errorf("send timeout")
    }
}
```

**为什么 channel 要 buffered？**

如果 channel 是 unbuffered，当第一个成功结果返回后，其余 goroutine 向 channel 写入时会永远阻塞（没有接收者），导致 goroutine 泄漏。buffered channel 允许所有 goroutine 都能写入后退出。

### 6.2 优先费推荐器的并发安全

PriorityFeeRecommender 的样本数据可能被并发读写：
- 写：后台 goroutine 定期添加新区块的优先费样本
- 读：交易构建时查询推荐值

使用 `sync.RWMutex` 保护：读操作用 RLock（允许并发读），写操作用 Lock（独占写）。

---

## 7. 与生产系统的对接点 [L4 架构视角]

### 7.1 BribeService 接口在架构中的位置

```
SwapBuilder.Build()
    |
    v
SwapResult{TxData: []byte}
    |
    v
BaseTxSender.Send(ctx, txData)
    |
    +-- rpc.SendTransaction(ctx, txData)        // 直发
    +-- bribe1.Send(ctx, txData, fee)           // NextBlock
    +-- bribe2.Send(ctx, txData, fee)           // Temporal
    +-- bribe3.Send(ctx, txData, fee)           // ZeroSlot
    +-- bribe4.Send(ctx, txData, fee)           // BlockRazor
    +-- bribe5.Send(ctx, txData, fee)           // BlockRush
    |
    v
第一个成功的 txHash
    |
    v
BaseTxSender.Confirm(ctx, txHash)
    |
    +-- 成功 -> 结束
    +-- 超时 -> BaseTxSender.Retry(ctx, txHash)  // RBF 或重发
```

### 7.2 优先费在交易构建中的集成

```
// 生产中的流程
fee := priorityFeeRecommender.Recommend("high")

// Solana: 设置 Compute Unit Price
swapReq.Extra["computeUnitPrice"] = fee

// EVM: 设置 maxPriorityFeePerGas
swapReq.Extra["maxPriorityFeePerGas"] = fee

// SwapBuilder 使用这些参数构建交易
result, err := builder.Build(ctx, swapReq)
```

### 7.3 MEV 监控（事后分析）

生产中需要监控交易是否被三明治攻击：

```
交易确认后
    |
    v
分析同区块/同 slot 的相邻交易
    |
    +-- 前一笔交易与我交易的 token pair 相同？
    +-- 后一笔交易是前一笔的反向操作？
    +-- 如果是 -> 记录为「被三明治攻击」
    |
    v
统计被攻击频率和损失金额
    |
    v
告警 + 调整防护策略
```
