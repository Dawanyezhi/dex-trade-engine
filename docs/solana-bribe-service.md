# Solana 贿赂服务说明

## 1. 核心定义

Solana 贿赂服务（Bribe Service）是一类交易加速和私有发送服务。它通过更短的私有传播路径把交易发送到当前或未来 slot 的 leader / block engine，并附带额外 tip，提高交易被优先处理和成功上链的概率。

在生产系统里，它也常被称为：

- `bribe service`
- `tip service`
- `block engine`
- `private relay`
- `transaction landing service`

一句话概括：

```text
Solana 贿赂服务 = 私有发送通道 + leader/block engine 直连能力 + tip 激励。
```

## 2. 为什么 Solana 需要贿赂服务

Solana 没有 EVM 那种公开 mempool。交易通常通过 RPC、TPU 或其它转发路径到达当前或即将出块的 leader。普通 RPC 发送在生产 DEX 场景下会遇到几个问题：

- 传播链路长，延迟不稳定。
- 网络拥堵时交易容易丢包、排队或过期。
- 高价值 Swap 对成交时效敏感，越晚落地越容易滑点超限。
- 普通传播路径可能增加交易提前暴露的概率。
- 仅依赖 `ComputeUnitPrice` 不一定能保证交易顺利 landing。

贿赂服务主要解决两个问题：

```text
可靠到达：提高交易抵达 leader / block engine 的概率。
优先处理：通过 tip 激励，提高交易在拥堵环境下被处理的优先级。
```

## 3. 与优先费的关系

Solana 一笔 DEX Swap 交易通常包含三类费用：

| 费用类型 | 说明 | 常见实现 |
|----------|------|----------|
| 基础费 | Solana 协议基础签名费 | `5000 lamports / signature` |
| 优先费 | 提高交易在标准队列中的排序权重 | `ComputeBudget::SetComputeUnitPrice` |
| 贿赂费 / Tip | 通过私有通道给指定 tip account 的额外激励 | `SystemProgram::Transfer` 或 bundle tip |

优先费和贿赂费不是同一个东西。

```text
优先费：协议层费用，影响标准交易调度。
贿赂费：私有通道激励，影响服务商 / leader / block engine 的处理优先级。
```

生产系统里两者通常叠加使用：优先费保证协议层排序不太低，tip 保证私有通道里有足够激励。

## 4. DEX Swap 中的典型流程

生产级 Solana DEX Swap 发送链路通常如下：

```text
1. 预检查用户余额、ATA、mint、Token Program、池子状态
2. 构建 Swap 指令
3. 加入 ComputeBudget 指令
4. 根据策略加入 tip 指令，或准备 bundle tip
5. simulateTransaction 校验交易可执行、输出满足 minOut
6. 调用签名服务完成签名
7. 并发发送到多个通道：
   - 普通 RPC
   - NextBlock
   - Temporal
   - ZeroSlot
   - BlockRazor / BlockRush / Jito 类服务
8. 任一通道返回成功 signature 后进入确认追踪
9. 持续查询 getSignatureStatuses / block inclusion
10. 超时后按策略重建交易、提高费用并重发
```

本项目中的抽象位置：

- `internal/dexwallet/interfaces.go` 定义 `BribeService` 接口。
- `internal/dexwallet/tx_sender.go` 实现 RPC + 多个贿赂服务并发发送。
- `06-mev-protection/demo/bribe_service.go` 模拟 NextBlock、Temporal、ZeroSlot 等服务商。

## 5. 多通道发送策略

常见生产策略是同一笔已签名交易并发发往多个通道，取第一个成功结果：

```text
signed tx
  |-- RPC
  |-- NextBlock
  |-- Temporal
  |-- ZeroSlot
  |-- Jito / block engine
```

这样做的收益：

- 提高交易 landing 成功率。
- 降低单个服务商故障对业务的影响。
- 降低尾延迟，取最快通道的结果。
- 在拥堵时提升高价值交易的成交概率。

但这里有一个生产级关键前提：

```text
最好多通道发送同一笔 signed transaction，而不是构建多笔不同交易。
```

如果所有通道发送的是同一笔交易，signature 相同，Solana 会按交易签名去重，通常只会执行一次。

如果不同服务商要求不同 tip account，系统可能构建出多笔不同交易：

```text
tx A = swap + tip to NextBlock
tx B = swap + tip to Temporal
tx C = swap + tip to ZeroSlot
```

这三笔交易的签名不同，理论上可能全部上链，导致用户 Swap 被重复执行。这是 DEX 资金安全场景里的高风险问题。

生产建议：

- 优先设计统一 tip account 或 bundle 方案，尽量复用同一笔 signed tx。
- 如果必须多版本交易并发，要引入订单状态机和 single-flight 控制。
- 确认任一版本 landed 后，立即停止其它版本发送和重试。
- 大额交易不要盲目多版本并发。
- 对用户余额、订单幂等号、交易 nonce/blockhash 生命周期做强约束。

## 6. 贿赂费策略

贿赂费不是越高越好。生产系统需要按交易价值、链拥堵、池子深度、滑点风险和用户成本动态计算。

一个实用分层策略：

| 场景 | 建议策略 |
|------|----------|
| 小额普通 Swap | 低优先费，默认 RPC 或低 tip |
| 中等金额 Swap | 中等优先费，选择 1-2 个稳定私有通道 |
| 大额 Swap | 较高优先费，多通道私有发送，严格 minOut |
| 抢新池 / 高频交易 | 高优先费 + 高 tip + 最短通道 |
| 低收益套利 | tip 必须小于预期净收益，避免费用吃掉利润 |

费用上限必须受控：

```text
max_total_fee = base_fee + priority_fee + bribe_tip
max_total_fee <= 用户可接受费用上限
max_total_fee <= 交易预期收益或滑点收益保护阈值
```

## 7. 与 MEV 防护的关系

贿赂服务能降低部分 MEV 暴露风险，但它不是绝对 MEV 防护。

它能改善：

- 交易更快抵达 leader。
- 减少普通 RPC 转发路径中的泄露窗口。
- 提高拥堵时交易 landing 概率。
- 降低因延迟造成的滑点风险。

它不能保证：

- 一定上链。
- 一定按预期顺序执行。
- 一定不被夹子攻击。
- 一定不会被审查。
- 一定不会滑点超限。

因此 DEX 生产系统仍必须保留：

- `minOut` 滑点保护。
- 交易模拟。
- 池子状态刷新。
- 大额交易风控。
- 成交后到账校验。
- 异常成交和 MEV 模式检测。

## 8. 与 EVM Anti-MEV RPC 的区别

Solana 贿赂服务和 EVM Anti-MEV RPC 的目标类似，都是改善交易执行质量，但底层机制不同。

| 维度 | Solana 贿赂服务 | EVM Anti-MEV RPC |
|------|------------------|------------------|
| 链上环境 | 无传统公开 mempool | 通常有公开 mempool |
| 核心目标 | 快速抵达 leader，提高 landing 概率 | 绕开公开 mempool，避免 pending 交易泄露 |
| 费用机制 | 优先费 + 额外 tip | 通常是 gas fee，部分私有 RPC 免费 |
| 常见实现 | tip account、bundle、block engine | Flashbots Protect、MEV Blocker、私有 RPC |
| 重试方式 | blockhash 过期后重建交易 | RBF 替换同 nonce 交易 |

代码抽象上可以统一成 `BribeService` 或 `PrivateSendService`，但实现层必须区分 Solana 和 EVM。

## 9. 生产监控指标

接入贿赂服务后，至少需要监控以下指标：

- 各服务商发送成功率。
- 各服务商首个成功占比。
- 各服务商平均耗时和 P95/P99 耗时。
- 交易 landing 成功率。
- 交易从签名到 confirmed/finalized 的耗时。
- blockhash 过期率。
- dropped transaction 比例。
- 贿赂费平均值、P95、P99。
- 每笔 Swap 的总费用占成交额比例。
- 滑点失败率和 minOut 失败率。
- 同订单多 signature 风险事件。
- 服务商连续失败和恢复情况。

告警建议：

- 单服务商连续失败超过阈值，自动熔断并告警。
- 所有私有通道不可用时，降级到 RPC 并打高优先级告警。
- 贿赂费异常升高时，触发费用保护。
- 同一业务订单出现多个不同 signature 时，触发资金安全告警。

## 10. 面试表达

可以这样简洁解释：

```text
Solana 贿赂服务是交易加速和私有发送机制。它通过私有通道把交易更快送到 leader 或 block engine，并附带 tip 激励优先处理。对 DEX 来说，它能提高 Swap 成功率、降低拥堵时的掉单和滑点风险，也能减少普通 RPC 传播路径中的交易暴露。

生产上不能只说多通道并发发送，还要处理重复成交风险。最佳实践是尽量对同一笔 signed transaction 多通道广播；如果不同服务商需要不同 tip account，导致交易签名不同，就必须用订单状态机、single-flight、费用上限和确认追踪来避免重复执行和资损。
```

## 11. 生产落地原则

- 不要把贿赂服务当作交易成功保证，它只是提升 landing probability。
- 小额交易控制费用，大额交易优先保证成交质量和资金安全。
- 多通道发送优先复用同一笔 signed transaction。
- 多版本交易并发必须有严格幂等和状态机。
- 模拟、滑点保护、确认追踪、超时重建不能省略。
- 服务商健康管理和费用监控必须前置建设。
- EVM 的 Anti-MEV RPC 和 Solana 贿赂服务只能抽象接口统一，不能混淆底层机制。
