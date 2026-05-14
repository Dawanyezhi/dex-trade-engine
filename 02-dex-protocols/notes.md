# 02-dex-protocols: 深度技术笔记

## 1. AMM 恒定乘积做市商 [dexwallet 通用层]

### 1.1 核心公式推导

恒定乘积不变量：

```
x * y = k
```

其中 x 是 base 代币储量，y 是 quote 代币储量，k 是常数。

**卖出 base 代币（Sell 方向）**：

用户输入 dx 个 base 代币，期望得到 dy 个 quote 代币。交易后：

```
(x + dx_effective) * (y - dy) = k = x * y
```

其中 dx_effective = dx * (10000 - feeRate) / 10000，手续费在输入端扣除。

解出 dy：

```
dy = y * dx_effective / (x + dx_effective)
   = y * dx * (10000 - feeRate) / (x * 10000 + dx * (10000 - feeRate))
```

**买入 base 代币（Buy 方向）**：

用户输入 dy 个 quote 代币，期望得到 dx 个 base 代币。类似推导：

```
dx = x * dy_effective / (y + dy_effective)
   = x * dy * (10000 - feeRate) / (y * 10000 + dy * (10000 - feeRate))
```

### 1.2 价格与滑点

**当前价格**（边际价格）：

```
price = y / x  （1 个 base 代币值多少 quote 代币）
```

**无滑点输出**（理想输出，按当前价格线性计算）：

```
idealOutput = amountIn * price  （Sell 方向）
idealOutput = amountIn / price  （Buy 方向）
```

**价格影响**（Price Impact）：

```
priceImpact = (idealOutput - actualOutput) / idealOutput * 10000  （基点）
```

价格影响的物理含义：交易量相对于池子储量越大，价格偏移越大。这是 AMM 曲线非线性的直接体现。

### 1.3 为什么大额交易滑点更大

考虑 x=1000, y=1000000 的池子：

- 交易 1 个 base：output = 1000000 * 1 / (1000 + 1) = 999.0 （约 999 quote，几乎无滑点）
- 交易 100 个 base：output = 1000000 * 100 / (1000 + 100) = 90909 （约 909 quote/base，9.1% 滑点）
- 交易 500 个 base：output = 1000000 * 500 / (1000 + 500) = 333333 （约 667 quote/base，33.3% 滑点）

交易量占池子储量比例越大，价格偏移越剧烈。

---

## 2. CLMM 集中流动性 [dexwallet 通用层]

### 2.1 核心思想

传统 AMM 的流动性均匀分布在 (0, +inf) 的全价格范围，但实际上大部分交易集中在当前价格附近。CLMM 允许 LP 将流动性集中在 [priceLower, priceUpper] 的价格范围内。

### 2.2 Tick 机制

价格空间被离散化为 Tick，每个 Tick 对应一个价格：

```
price(tick) = 1.0001^tick
```

LP 选择一个 Tick 范围 [tickLower, tickUpper] 来提供流动性。当前价格所在的 Tick 范围内的活跃流动性决定了交易的滑点。

### 2.3 集中流动性的效率优势

假设 LP 在传统 AMM 中提供了 L 的流动性，覆盖全价格范围。如果将同样的资金集中到 [priceLower, priceUpper]，等效流动性为：

```
L_effective = L * sqrt(priceUpper * priceLower) / (sqrt(priceUpper) - sqrt(priceLower))
```

当价格范围越窄，等效流动性越大。例如，将流动性集中在当前价格正负 5% 的范围内，资本效率约提升 10-20 倍。

### 2.4 简化模型

本 demo 简化处理：
- 用 TickRange 结构表示每个流动性区间
- 交易时只在当前价格所在的 Tick 范围内计算
- 不处理跨多个 Tick 范围的复杂场景（生产中需要遍历 Tick 位图）

在单个 Tick 范围内，计算逻辑与 AMM 类似，但使用该范围内的"虚拟储量"而非全局储量。

---

## 3. Bonding Curve 联合曲线 [dexwallet 通用层]

### 3.1 定价公式

```
price(supply) = a * supply^n
```

其中：
- supply 是已售出的代币数量
- a 是基础价格系数
- n 是曲线指数（n=1 线性，n=2 二次）

### 3.2 买入计算（沿曲线积分）

买入 amount 个代币的总成本：

```
cost = integral(a * s^n, s=currentSupply, s=currentSupply+amount)
     = a * [(currentSupply+amount)^(n+1) - currentSupply^(n+1)] / (n+1)
```

反过来，给定一笔资金 amountIn，能买到多少代币，需要解方程：

```
amountIn = a * [(currentSupply+x)^(n+1) - currentSupply^(n+1)] / (n+1)
```

对于 n=1（线性曲线）：

```
amountIn = a * [(currentSupply+x)^2 - currentSupply^2] / 2
         = a * [2*currentSupply*x + x^2] / 2
         = a * x * (2*currentSupply + x) / 2
```

解二次方程得到 x（买到的代币数量）。

### 3.3 卖出计算

卖出 amount 个代币获得的收入：

```
revenue = integral(a * s^n, s=currentSupply-amount, s=currentSupply)
        = a * [currentSupply^(n+1) - (currentSupply-amount)^(n+1)] / (n+1)
```

### 3.4 毕业机制

Bonding Curve 是"内盘"阶段的定价机制。当满足以下条件之一时触发"毕业"：

- 累计流动性达到阈值（如 PumpFun 的 85 SOL）
- 代币供应量达到预设上限
- 市值达到目标

毕业后：
1. 销毁 Bonding Curve 中剩余的未售代币
2. 将已筹集的资金和对应代币转移到 AMM 池子
3. 后续交易通过 AMM 协议定价

### 3.5 生产差异

PumpFun 和 Moonshot 的具体曲线参数不同，且有各自的合约实现细节：
- PumpFun: 固定曲线参数，毕业后迁移到 Raydium AMM
- Moonshot: 可配置曲线，毕业后迁移到指定 DEX

本 demo 实现通用的线性和二次曲线模型。

---

## 4. StableSwap 稳定币低滑点 [dexwallet 通用层]

### 4.1 设计动机

稳定币对（如 USDC/USDT）的价格应该接近 1:1。在恒定乘积 AMM 中，即使是小额交易也会产生不必要的滑点。StableSwap 通过特殊的曲线设计，在 1:1 附近大幅降低滑点。

### 4.2 核心公式（Curve 的不变量）

Curve 的两资产不变量：

```
A * (x + y) + D = A * D + D^3 / (4 * x * y)
```

简化版（本 demo 采用的插值模型）：

StableSwap 的思路是在恒定和（x + y = D）和恒定积（x * y = (D/2)^2）之间做加权插值：

```
A * D * (x + y) + x * y = A * D^2 + (D/2)^2
```

其中 A 是放大系数（Amplification Coefficient）。

- 当 A -> 0 时，退化为恒定积（与普通 AMM 类似）
- 当 A -> inf 时，退化为恒定和（1:1 恒定汇率，零滑点）

### 4.3 A 参数的影响

| A 值 | 特性 | 适用场景 |
|------|------|----------|
| 1 | 接近恒定积，滑点较大 | 不推荐用于稳定币 |
| 10 | 在 1:1 附近有一定优化 | 弱锚定资产 |
| 100 | 1:1 附近滑点很低 | 一般稳定币对 |
| 1000 | 1:1 附近几乎零滑点 | 高度锚定的稳定币对（USDC/USDT） |

### 4.4 简化实现

本 demo 采用简化的迭代方法计算输出金额：
1. 已知 x, y, A, D 和输入 dx
2. 交易后新的 x' = x + dx
3. 通过牛顿迭代法解不变量方程得到新的 y'
4. 输出 dy = y - y'

### 4.5 生产差异

生产中的 Curve 实现：
- 支持 2-4 种资产的池子（3pool: USDT/USDC/DAI）
- A 参数可以动态调整（治理投票）
- 考虑管理费和 LP 费分配
- 有精心优化的定点数学库

---

## 5. Pool.Extra 字段的使用约定 [dexwallet 通用层]

Pool 结构体的 Extra 字段（map[string]interface{}）用于存储协议特定的数据，避免在通用结构体中添加每种协议的字段。

### 5.1 AMM 协议

```go
Extra["ReserveBase"]  = *big.Int  // base 代币储量
Extra["ReserveQuote"] = *big.Int  // quote 代币储量
```

### 5.2 CLMM 协议

```go
Extra["CurrentTick"]  = int64        // 当前 Tick 索引
Extra["TickRanges"]   = []TickRange  // 流动性区间列表
Extra["SqrtPriceX64"] = *big.Int     // 当前价格的平方根（Q64.64 定点数，生产用）
```

### 5.3 Bonding Curve 协议

```go
Extra["CurrentSupply"]     = *big.Int  // 当前已售出的代币供应量
Extra["CoefficientA"]      = *big.Int  // 价格系数 a（缩放后的整数）
Extra["Exponent"]          = int64     // 曲线指数 n
Extra["GraduationTarget"]  = *big.Int  // 毕业目标（累计流动性）
Extra["CollectedLiquidity"]= *big.Int  // 已收集的流动性
```

### 5.4 StableSwap 协议

```go
Extra["ReserveBase"]         = *big.Int  // base 代币储量
Extra["ReserveQuote"]        = *big.Int  // quote 代币储量
Extra["AmplificationCoeff"]  = int64     // 放大系数 A
Extra["InvariantD"]          = *big.Int  // 不变量 D
```

---

## 6. 整数运算与精度 [dexwallet 通用层]

### 6.1 为什么禁止 float64

浮点数不精确：

```
0.1 + 0.2 = 0.30000000000000004  （float64）
```

在金融计算中，这种误差会导致：
- 套利者利用精度差异获利
- 累计计算后金额不一致
- 无法与链上合约的整数运算结果对齐

### 6.2 先乘后除原则

错误：
```go
// 会丢失精度：100 * 30 / 10000 = 0（整数除法）
result = amountIn.Mul(feeRate).Div(bigInt10000)
```

正确：
```go
// 先乘大数，最后除：100 * y * (10000-30) / (x * 10000 + 100 * (10000-30))
// 分子足够大，除法的精度损失相对很小
numerator := new(big.Int).Mul(y, effectiveIn)
denominator := new(big.Int).Add(xScaled, effectiveIn)
result := new(big.Int).Div(numerator, denominator)
```

### 6.3 缩放技巧

Bonding Curve 中 price = a * supply^n 涉及小数。处理方式：
- 将系数 a 缩放为整数（乘以 SCALE_FACTOR = 10^18）
- 计算结果最后除以 SCALE_FACTOR
- 保持中间计算全部为整数运算

---

## 7. DexProtocol 接口与 Aggregator 的关系 [dexwallet 通用层]

### 7.1 数据流

```
用户请求 SwapRequest
    |
    v
Aggregator.FindBestQuote
    |-- 并发调用各 DexProtocol.Quote
    |   |-- AMM.Quote(pool_raydium, amount, sell)     --> Quote{output: 998}
    |   |-- CLMM.Quote(pool_raydium_clmm, amount, sell) --> Quote{output: 999}
    |   |-- BondingCurve.Quote(pool_pump, amount, sell)  --> Quote{output: 995}
    |
    |-- 比较所有 Quote，选最优
    |-- 考虑优先级（内盘 > AMM/CLMM > 聚合器）
    v
最优 Quote
    |
    v
SwapBuilder.Build(SwapRequest)  --> SwapResult{txData: [...]}
```

### 7.2 DexProtocol 是纯计算层

DexProtocol.Quote 接收 *Pool 参数而不是自己获取链上数据，这个设计决策的好处：

1. **可测试性**：可以用构造好的 Pool 数据进行单元测试，不需要链上连接
2. **关注点分离**：协议层只做数学计算，数据获取由 PoolManager 负责
3. **缓存友好**：Pool 数据可以被缓存和复用，不需要每次 Quote 都查链
4. **跨链通用**：同一个 AMM 数学模型可以用于 Raydium（Solana）和 Uniswap V2（EVM）

---

## 8. 协议选择策略 [dexwallet 通用层]

### 8.1 优先级规则

```
PriorityHigh (1): 内盘（Bonding Curve）
  -- 新代币在毕业前只能通过内盘交易
  -- 必须优先检查是否在内盘阶段

PriorityMedium (2): AMM / CLMM / StableSwap
  -- 已毕业代币的常规交易
  -- 从多个 DEX 中选最优报价

PriorityLow (3): 聚合器（Jupiter / 1inch）
  -- 聚合器会做更复杂的路由优化
  -- 但延迟较高，且有额外手续费
  -- 作为 AMM/CLMM 不可用时的兜底
```

### 8.2 报价比较

选最优报价不仅看 OutputAmount，还要考虑：

```
score = outputAmount
      - estimatedGas * gasPrice   // 扣除 Gas 成本
      - (priceImpact 是否可接受)  // 价格影响过大则放弃
```

生产中还有可靠性因子：某些 DEX 报价好但成交率低（如交易失败率高），需要加入历史成交率权重。
