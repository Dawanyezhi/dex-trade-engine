# Anti-MEV 说明

## 1. 核心定义

Anti-MEV 是一组降低交易被 MEV 攻击风险的机制。它的目标不是让交易一定成功，而是减少交易在执行前被搜索者、验证者、builder 或其它参与方利用的概率。

在 DEX Swap 场景中，Anti-MEV 主要防护：

- 抢跑攻击（front-running）。
- 三明治攻击（sandwich attack）。
- 尾随套利（back-running）。
- 交易在公开传播路径中过早暴露。
- 交易延迟导致的滑点扩大。

一句话概括：

```text
Anti-MEV = 用私有发送、排序保护、滑点保护和交易模拟等机制，降低 DEX 交易被抢跑、夹子和尾随攻击的风险。
```

## 2. MEV 对 DEX Swap 的影响

最典型的是三明治攻击：

```text
1. 攻击者看到用户的大额 Swap。
2. 攻击者抢先买入，推高池子价格。
3. 用户 Swap 以更差价格成交。
4. 攻击者立刻卖出，赚取价差。
```

结果是：

```text
用户实际收到的 token 变少。
攻击者利润来自用户滑点空间。
```

攻击空间主要取决于：

- 用户交易金额。
- 池子流动性深度。
- 用户设置的滑点容忍度。
- 交易暴露时间。
- 链上排序机制。
- 搜索者竞争强度。

因此，DEX 生产系统不能只关注 quote，还必须关注交易如何发送、如何排序、是否暴露、是否能按预期落地。

## 3. EVM 上的 Anti-MEV

EVM 链通常有公开 mempool。普通交易发送路径是：

```text
用户交易 -> 公共 RPC -> 公开 mempool -> 搜索者可见 -> validator / builder 打包
```

攻击者可以监听 pending transaction，然后构造抢跑或三明治交易。

EVM Anti-MEV 的核心方案是使用私有交易通道：

```text
用户交易 -> 私有 RPC / relay / builder -> 不进入公开 mempool -> 打包上链
```

常见服务包括：

- Flashbots Protect。
- MEV Blocker。
- bloXroute Protect。
- 48 Club。
- Titan / Beaver / Builder 私有通道。
- 交易所或钱包自建 private relay。

EVM Anti-MEV 的核心价值：

- 避免交易进入公开 mempool。
- 降低被搜索者提前看到的概率。
- 减少三明治攻击。
- 支持 bundle 或 builder 直连。
- 某些方案可以返回 MEV rebate 或 order flow auction 收益。

生产注意点：

- 私有 RPC 不保证一定上链。
- builder 可能不覆盖所有 validator。
- 私有通道失败后是否降级公共 RPC，需要按交易风险决定。
- 如果降级到公共 RPC，交易会重新暴露。
- EVM 卡单通常需要 RBF，用同 nonce 更高 gas 的交易替换。

## 4. Solana 上的 Anti-MEV

Solana 没有 EVM 那种传统公开 mempool，因此 Anti-MEV 的技术路径不同。

Solana 更常见的手段是：

- Jito bundle。
- block engine。
- 私有发送通道。
- 贿赂服务 / tip service。
- 优先费 `ComputeUnitPrice`。
- 更短路径直达 leader。

Solana 的重点不是“绕开公开 mempool”，而是：

```text
更快、更私密地把交易送到 leader / block engine，
减少传播路径中的暴露，
提高交易 landing 成功率。
```

典型发送路径：

```text
signed tx
  |-- 普通 RPC
  |-- Jito / block engine
  |-- NextBlock
  |-- Temporal
  |-- ZeroSlot
```

Solana 生产注意点：

- 优先费和贿赂费是两套机制。
- 贿赂服务只能提高 landing probability，不能保证成交。
- 多通道发送应尽量复用同一笔 signed transaction。
- 如果不同服务商要求不同 tip account，可能产生多笔不同签名交易，必须防止重复成交。
- blockhash 有有效期，超时后需要重建交易。

更详细的 Solana 贿赂服务说明见 `docs/solana-bribe-service.md`。

## 5. Anti-MEV 不能保证什么

生产系统必须明确：Anti-MEV 不是交易成功保证。

它不能保证：

- 一定上链。
- 一定成交。
- 一定不失败。
- 一定不滑点。
- 一定不被所有 MEV 形式影响。
- 一定比普通 RPC 更便宜。
- 一定比普通 RPC 更快。
- 一定不会被 builder / validator 审查。

因此，Anti-MEV 必须和其它交易保护一起使用：

- `minOut` 滑点保护。
- 交易模拟。
- 报价新鲜度校验。
- 费用上限控制。
- 私有通道健康检查。
- 确认追踪。
- 超时重试。
- 异常成交检测。

## 6. DEX Swap 使用策略

不是所有交易都必须走 Anti-MEV。生产系统应该按风险分层。

建议默认启用 Anti-MEV 的场景：

- 大额 Swap。
- 低流动性池子。
- 高滑点交易。
- 热门 token。
- 新币抢购。
- 用户选择 MEV protection 模式。
- 历史上被夹子攻击频繁的池子或 token。
- 套利、清算、强时效交易。

可以不强制启用的场景：

- 小额交易。
- 稳定币深池交易。
- 低滑点、低价格影响交易。
- 私有通道当前不可用且用户允许普通发送。

推荐决策逻辑：

```text
1. 计算交易金额、价格影响和池子深度。
2. 判断 token 是否热门、新币或高风险。
3. 判断用户滑点是否过高。
4. 如果风险高，优先私有通道。
5. 如果私有通道超时，按用户策略决定是否降级公共 RPC。
6. 发送后追踪 confirmed/finalized 和实际到账。
```

## 7. 降级策略

Anti-MEV 通道失败后，是否降级普通 RPC 是生产系统里的关键决策。

可以降级的情况：

- 小额交易。
- 用户明确允许普通发送。
- 交易价格影响很低。
- 池子深度足够。
- 当前 token 不属于高风险标的。

不建议降级的情况：

- 大额 Swap。
- 高滑点交易。
- 低流动性池子。
- 新币抢购。
- 用户明确要求 MEV protection only。
- 降级后可能明显暴露套利空间。

推荐在请求参数中区分：

```text
mevProtectionMode:
  off      普通发送
  auto     系统按风险自动选择
  strict   只走 Anti-MEV，失败不降级公共 RPC
```

## 8. 与滑点保护的关系

Anti-MEV 和滑点保护不是替代关系。

```text
Anti-MEV:
减少交易被看到和被排序攻击的概率。

minOut:
保证最差成交结果不会低于用户接受值。
```

即使使用 Anti-MEV，也必须设置 `minOut`。原因是：

- 池子状态可能自然变化。
- 其它交易可能先成交。
- 私有通道不保证排序。
- quote 可能过期。
- bundle 可能没有被预期 builder 打包。

生产建议：

- 根据池子深度、交易金额和 token 风险动态推荐滑点。
- 对高风险 token 限制最大滑点。
- 对 quote 到 build 到 send 的耗时设置上限。
- 模拟结果必须验证 output 不低于 `minOut`。

## 9. 与费用策略的关系

Anti-MEV 通常会影响费用模型。

EVM：

- 主要是 gas fee。
- EIP-1559 下需要设置 `maxFeePerGas` 和 `maxPriorityFeePerGas`。
- RBF 替换需要更高 gas。
- 某些私有 RPC 不额外收费，但 builder 是否打包仍受 gas 激励影响。

Solana：

- 基础签名费固定。
- 优先费通过 `ComputeBudget::SetComputeUnitPrice` 设置。
- 私有通道可能需要额外 tip。
- 贿赂费必须纳入用户总成本和收益校验。

费用控制原则：

```text
total_fee <= 用户费用上限
total_fee <= 交易预期收益
total_fee 不应吃掉大部分滑点收益
```

## 10. 生产监控指标

Anti-MEV 上线后至少需要监控：

- Anti-MEV 通道发送成功率。
- 私有通道首个成功占比。
- 私有通道平均耗时和 P95/P99。
- 私有通道失败后的降级比例。
- strict 模式失败率。
- 普通 RPC 与 Anti-MEV 的成交滑点对比。
- suspected sandwich attack 数量。
- 用户实际到账与 quote 偏差。
- EVM RBF 次数和替换成功率。
- Solana blockhash 过期率。
- Solana tip 平均值和 P95/P99。
- 私有服务商连续失败和恢复情况。

告警建议：

- Anti-MEV 通道整体失败率升高。
- 某个服务商连续失败。
- 降级普通 RPC 比例异常升高。
- 高风险交易被错误降级。
- 疑似夹子攻击数量异常升高。
- 用户实际到账偏差超过阈值。

## 11. 代码抽象建议

可以在通用层抽象为：

```go
type PrivateSendService interface {
    Send(ctx context.Context, txData []byte, fee *big.Int) (string, error)
    GetRecommendedFee(ctx context.Context) (*big.Int, error)
    Name() string
}
```

但实现层必须区分链：

```text
Solana:
  BribeService / Jito Bundle / private relay / tip

EVM:
  Anti-MEV RPC / private mempool / builder relay / RBF
```

不要因为接口统一，就把底层机制混为一谈。

## 12. 面试表达

可以这样说明：

```text
Anti-MEV 是为了降低交易被抢跑、三明治和尾随攻击的风险。EVM 上主要通过私有 RPC 或 builder relay 绕开公开 mempool，比如 Flashbots Protect；Solana 没有传统公开 mempool，所以更多依赖 Jito bundle、私有发送通道、贿赂服务和优先费来提高交易 landing，并减少传播路径暴露。

但 Anti-MEV 不是交易成功保证。生产上还必须配合 minOut、交易模拟、费用上限、确认追踪和失败降级。尤其是 DEX Swap，需要根据交易金额、池子深度、滑点和 token 风险决定是否启用 Anti-MEV，以及私有通道失败后能不能降级普通 RPC。
```

## 13. 总结

Anti-MEV 在生产 DEX 系统中的定位是交易保护层，而不是单独的发送 API。

它需要和以下模块一起工作：

- Aggregator：判断交易路径和价格影响。
- SwapBuilder：构建带滑点保护的交易。
- FeeOracle：推荐 gas、priority fee 和 tip。
- TxSender：选择普通 RPC、私有通道或多通道发送。
- ConfirmTracker：追踪交易确认和超时重试。
- RiskEngine：判断是否允许降级和是否限制滑点。
- Monitor：检测成交偏差、夹子攻击和服务商故障。

最终目标不是完全消灭 MEV，而是在可控成本下显著降低用户被攻击的概率，并让每笔 Swap 的失败、降级和成交结果都可解释、可观测、可追责。
