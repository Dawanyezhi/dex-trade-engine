# Solana ALT 说明

## 1. 核心定义

ALT 是 `Address Lookup Table`，即地址查找表。它是 Solana 为 `versioned transaction v0` 提供的地址压缩机制。

Solana 交易执行前必须声明所有会读写的账户。普通交易会把每个账户的完整 public key 写进 message，每个地址占 `32 bytes`。当 DEX Swap 涉及很多账户时，交易体积很容易超过限制。

ALT 的作用是：

```text
把一批常用地址预先存到链上的 lookup table account，
交易里不再重复写完整 32 字节地址，
而是引用 lookup table + 地址索引。
```

一句话概括：

```text
ALT = 用链上地址表压缩交易账户列表，让复杂 Solana 交易能放进单笔交易大小限制内。
```

## 2. 为什么 Solana 需要 ALT

Solana 的交易模型要求每笔交易提前声明全部账户，包括：

- 用户主账户。
- 用户输入 / 输出 ATA。
- 池子 vault。
- AMM / CLMM config。
- authority。
- tick array / bin array。
- oracle account。
- token program。
- associated token program。
- system program。
- DEX program。
- tip account。

普通单池 AMM Swap 的账户数量通常还能控制，但复杂路由很容易超过交易大小限制。

典型高风险场景：

- Jupiter 多跳路由。
- Raydium CLMM。
- Meteora DLMM。
- 多池拆单。
- 跨多个 DEX 的路由。
- Token2022 带扩展账户。
- 同一笔交易中包含 create ATA、wrap SOL、swap、close WSOL。
- 交易中还要加入 ComputeBudget 和 tip 指令。

没有 ALT 时，复杂交易可能无法序列化、无法模拟或无法发送。

## 3. ALT 的压缩原理

没有 ALT：

```text
10 个账户地址 = 10 * 32 bytes = 320 bytes
```

使用 ALT：

```text
lookup table 地址 = 32 bytes
10 个账户索引 = 10 * 1 byte
合计约 42 bytes
```

实际交易格式还有额外元数据，但核心收益是把大量重复的 `32 bytes public key` 替换为短索引。

注意：

```text
ALT 减少的是 message 里的地址体积，不是减少账户本身，也不是减少 Compute Unit。
```

复杂路由该消耗多少 CU 仍然会消耗多少 CU。

## 4. 与 v0 transaction 的关系

ALT 主要配合 `versioned transaction v0` 使用。

对比：

| 交易类型 | 账户地址处理方式 |
|----------|------------------|
| legacy transaction | 所有账户地址直接写入 message |
| versioned transaction v0 | message 可以引用 address lookup table |

生产系统中使用 ALT 通常意味着需要构建：

```text
VersionedTransaction
MessageV0
AddressLookupTableAccount
```

如果还是 legacy transaction，就无法使用 ALT 的地址查找能力。

## 5. DEX Swap 中的作用

在 DEX Swap 中，ALT 主要解决复杂交易的体积问题。

例如一笔多跳交易：

```text
SOL -> USDC -> MEME
```

可能涉及：

- SOL/USDC 池子账户。
- USDC/MEME 池子账户。
- 两个 DEX program。
- 多个 vault。
- 多个 tick array / bin array。
- 用户多个 ATA。
- token program / token2022 program。
- associated token program。
- compute budget program。
- tip account。

如果所有账户都直接写入交易 message，很容易超出限制。使用 ALT 后，可以把固定 program、池子、vault、config、oracle 等地址放进 lookup table，交易里只引用索引。

对交易所 DEX 系统来说，ALT 能让以下能力更稳定：

- 支持 Jupiter 复杂路由。
- 支持 CLMM / DLMM 多账户交易。
- 支持多跳和拆单。
- 支持带 tip / bribe 的复杂 Swap。
- 支持更多协议账户和 Token2022 扩展账户。

## 6. 生产使用流程

典型流程：

```text
1. 构建 Swap 指令列表。
2. 收集所有 account metas。
3. 估算 legacy message 体积。
4. 如果账户过多或 message 过大，查询可用 ALT。
5. 把能命中的账户放入 address lookup table 引用。
6. 构建 MessageV0。
7. 签名 VersionedTransaction。
8. simulateTransaction。
9. 发送交易。
```

生产中不建议只用“账户数量”判断是否启用 ALT，更稳的是同时看：

- 账户数量。
- 序列化 message 大小。
- 是否包含 CLMM / DLMM。
- 是否包含聚合器多跳。
- 是否包含额外 tip / create ATA / wrap SOL 指令。
- 交易是否已经是外部聚合器返回的 v0 transaction。

## 7. 使用外部聚合器返回的 ALT

Jupiter 等聚合器返回的 swap transaction 可能已经是 v0 transaction，并且包含 ALT 引用。

接入方需要做的不是重新发明 route，而是正确校验和发送：

```text
1. 反序列化 versioned transaction。
2. 读取 message 中引用的 lookup table address。
3. 通过 RPC 拉取 AddressLookupTableAccount。
4. 校验 ALT 是否存在、是否 active。
5. 解析完整账户列表。
6. 检查 fee payer、ComputeBudget、slippage、minOut、账户数量。
7. 补签或重新签名。
8. simulateTransaction。
9. 发送。
```

常见风险：

- ALT account 不存在。
- ALT 已 deactivate。
- RPC 没返回完整 lookup table。
- ALT 内容和交易索引不匹配。
- 外部交易体积仍然过大。
- 交易中 ALT 依赖过多，导致模拟失败或发送失败。
- 修改交易后 message hash 变化，原签名失效。

## 8. 自建 ALT

交易所自建 Solana DEX 引擎可以维护自己的 ALT，适合高频、稳定、可预测的地址集合。

适合放入 ALT 的地址：

- 常用 DEX program。
- 高频池子的 vault。
- pool config。
- authority。
- oracle。
- 常用 tick array / bin array。
- token program / associated token program。
- 平台 tip account。

不适合放入 ALT 的地址：

- 一次性用户地址。
- 低频长尾池子。
- 临时中间账户。
- 高频变化、生命周期短的账户。

自建 ALT 的成本：

- 创建 lookup table。
- 扩展地址列表。
- 等待地址可用。
- 权限管理。
- 多环境隔离。
- 失效和关闭管理。
- 配置同步和缓存刷新。

生产里一般不会为了单笔用户交易临时创建 ALT，因为创建和扩展 ALT 本身也需要链上交易和等待时间。

## 9. 关键限制和坑

### 9.1 ALT 不是实时可用

刚加入 lookup table 的地址通常不能在同一个 slot 立刻安全用于交易。生产系统要考虑 warm-up 和 slot 时序。

### 9.2 ALT 有容量限制

单个 lookup table 不能无限存地址。需要规划表结构，比如按 DEX、按池子类型、按高频 token 分表。

### 9.3 ALT 失效会导致交易失败

如果 lookup table 被 deactivate 或 close，依赖它的交易无法解析完整账户。

### 9.4 ALT 不能降低 CU

ALT 只压缩地址，不减少 AMM/CLMM 计算逻辑，也不减少跨程序调用本身的 Compute Unit。

### 9.5 修改账户列表会改变签名

交易 message 里引用的 ALT 和索引是签名内容的一部分。任何账户列表、lookup table、指令顺序变化都会导致 message hash 变化，需要重新签名。

### 9.6 外部 ALT 要做信任边界校验

如果使用聚合器返回的 ALT，必须校验最终解析出的账户是否符合预期，避免交易中混入异常 program、异常 token account 或不可接受的 writable account。

## 10. 与 EVM 的差异

EVM 没有 ALT 这种机制。

| 维度 | Solana | EVM |
|------|--------|-----|
| 交易结构 | 指令列表 + 显式账户列表 | 调用合约地址 + calldata |
| 账户声明 | 必须提前声明读写账户 | 合约执行时访问 storage |
| 体积限制 | 账户多会撑大 message | calldata 越大 gas 越高 |
| 优化方式 | ALT 压缩地址 | 减少 calldata / 优化 ABI / 合约逻辑 |
| DEX 影响 | 多账户路由需要 ALT | 多跳主要体现为 gas 增加 |

这也是 Solana SwapBuilder 和 EVM SwapBuilder 很难强行共用同一套底层构建逻辑的原因之一。

## 11. 生产监控指标

ALT 相关能力上线后，建议监控：

- 交易是否使用 ALT 的比例。
- 使用 ALT 后的 message size。
- 未使用 ALT 导致交易过大的失败数。
- ALT lookup 失败率。
- ALT deactivate / close 相关错误。
- 外部聚合器 ALT 解析失败率。
- v0 transaction 反序列化失败率。
- simulateTransaction 中账户解析失败数。
- 因修改交易导致签名失效的错误数。
- 各 lookup table 的命中率。

告警建议：

- 高频 lookup table 突然不可用。
- Jupiter 返回的 ALT 解析失败率升高。
- 复杂路由交易过大失败率升高。
- 某个 DEX 的 CLMM / DLMM 交易 ALT 命中率下降。

## 12. 面试表达

可以这样说明：

```text
ALT 是 Solana 的 Address Lookup Table，主要用于压缩交易里的账户地址。Solana 交易必须提前声明所有读写账户，复杂 DEX Swap，尤其是 Jupiter 多跳、Raydium CLMM、Meteora DLMM、Token2022 或带 tip 的交易，会涉及很多账户，容易超过交易大小限制。ALT 把常用账户预先存到链上 lookup table，v0 transaction 里只引用 table 和 index，从而减少 message 体积，让复杂路由能放进单笔交易。
```

生产视角可以继续补充：

```text
生产里我们会在构建交易时判断账户数量和 message size，必要时使用 v0 transaction + ALT。对 Jupiter 这类外部聚合器返回的交易，要校验 ALT 是否存在、是否 active、lookup table 内容是否能正确解析；自建 ALT 则适合高频池子和固定路由，但要处理创建、扩展、激活、失效和权限管理。
```

## 13. 总结

ALT 的本质是交易体积优化，不是费用优化、MEV 防护或交易加速。

它在 Solana DEX 生产系统里的价值是：

- 支撑复杂 Swap 路由。
- 支撑 CLMM / DLMM 多账户交易。
- 支撑聚合器 v0 transaction。
- 降低交易因 message 过大而失败的概率。
- 让自建 SwapBuilder 能覆盖更复杂的协议组合。

最终原则：

```text
账户少、交易简单：legacy transaction 即可。
账户多、路由复杂：使用 v0 transaction + ALT。
外部聚合器交易：必须解析并校验其 ALT 依赖。
自建高频路由：可以维护自己的 ALT，但要治理生命周期。
```
