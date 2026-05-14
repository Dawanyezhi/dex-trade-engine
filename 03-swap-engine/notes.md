# 03-swap-engine: 深度技术笔记

## 1. SwapBuilder 接口设计 [dexwallet 通用层] [L1]

SwapBuilder 是 `internal/dexwallet/interfaces.go` 中定义的核心接口：

```go
type SwapBuilder interface {
    Build(ctx context.Context, req SwapRequest) (*SwapResult, error)
    DexID() DexID
    ChainID() coinset.ChainID
    ProtocolType() ProtocolType
}
```

**为什么 Build 方法只返回结果而不直接发送？**

这是「构建-发送」分离原则。SwapBuilder 只负责：
1. 计算输出金额
2. 检查滑点
3. 组装交易数据

发送交易是 TxSender 的职责。分离的好处：
- 可以在发送前进行模拟（simulateTransaction / eth_call）
- 可以在发送前让用户确认
- 可以对同一笔交易尝试不同的发送通道（RPC / 贿赂服务）
- 测试时只需测试构建逻辑，不需要连接链

---

## 2. AMM 恒定乘积公式 [链特定层] [L2]

### 2.1 基本公式 x * y = k

池子中两种代币的数量乘积保持恒定：

```
x * y = k

交易前：x0 * y0 = k
交易后：(x0 + dx) * (y0 - dy) = k

解出 dy = y0 * dx / (x0 + dx)
```

其中：
- x0: 池子中输入代币的储备量
- y0: 池子中输出代币的储备量
- dx: 用户输入的代币数量（扣除手续费后）
- dy: 用户获得的输出代币数量

### 2.2 手续费处理

手续费在输入端扣除（大部分 AMM 的做法）：

```
effectiveInput = dx * (10000 - feeRate) / 10000
dy = y0 * effectiveInput / (x0 + effectiveInput)
```

其中 feeRate 以基点表示（30 = 0.3%）。

### 2.3 整数除法的影响

用 big.Int 做除法时，结果向零取整（截断）。这意味着：
- 计算出的输出金额总是略小于理论值
- 这对用户是保守的（不会多给），对池子是安全的

```go
// 正确的整数计算方式
numerator := new(big.Int).Mul(reserveOut, effectiveInput)
denominator := new(big.Int).Add(reserveIn, effectiveInput)
amountOut := new(big.Int).Div(numerator, denominator)
```

**禁止 float64 的原因：**

```
float64 能精确表示的整数范围: 2^53 = 9007199254740992 (~9 * 10^15)
1 ETH = 10^18 wei
100 ETH = 10^20 wei > 2^53

用 float64 计算 100 ETH 的 swap 金额，可能丢失 10^4 级别的精度
对于高价值代币，这可能意味着几美元的损失
```

---

## 3. Bonding Curve 联合曲线 [链特定层] [L2]

### 3.1 PumpFun 的定价机制

PumpFun 使用联合曲线（Bonding Curve）进行内盘定价。核心特点：
- 价格随购买量单调递增（越买越贵）
- 没有外部流动性提供者，由曲线本身定价
- 有毕业阈值：当市值达到阈值时，代币从内盘迁移到 AMM（如 Raydium）

### 3.2 简化的线性 Bonding Curve

```
price(supply) = basePrice + slope * supply

购买 amount 个代币的总成本：
cost = sum_{i=0}^{amount-1} price(currentSupply + i)
     = amount * basePrice + slope * (currentSupply * amount + amount * (amount-1) / 2)
```

实际 PumpFun 使用的是更复杂的虚拟储备曲线，但核心思想一致。

### 3.3 毕业检查

```
当前市值 = totalSupply * currentPrice
毕业阈值 = 69000 * 10^6 (以 USDC 计)

if 当前市值 >= 毕业阈值:
    停止内盘交易
    将资金注入 Raydium AMM 池子
    代币正式上市
```

毕业后不能再用 PumpFunBuilder，需要切换到 RaydiumAMMBuilder。这就是 Aggregator 的 DEX 优先级和降级机制存在的意义。

---

## 4. CLMM 集中流动性 [链特定层] [L2]

### 4.1 与传统 AMM 的区别

传统 AMM（Uniswap V2 / Raydium AMM）：
- 流动性均匀分布在 (0, +inf) 价格范围
- 大部分流动性在当前价格附近未被使用
- 资本效率低

CLMM（Uniswap V3 / PancakeSwap V3）：
- 流动性集中在用户指定的价格范围 [tickLower, tickUpper]
- 在活跃范围内的资本效率远高于传统 AMM
- 价格空间被划分为离散的 tick

### 4.2 Tick 和价格的关系

```
price = 1.0001^tick

tick = 0   => price = 1.0
tick = 100 => price = 1.01005...
tick = -100 => price = 0.99005...
```

交易时如果价格移动跨越了 tick 边界，需要切换到下一个 tick 区间的流动性，计算更加复杂。

### 4.3 本 demo 的简化

demo 中将 CLMM 简化为在 tick 范围内的恒定乘积计算，并额外乘以一个资本效率系数。生产中需要遍历跨越的每个 tick 区间分段计算。

---

## 5. StableSwap 稳定币曲线 [链特定层] [L2]

### 5.1 Curve 的 StableSwap 不变量

Curve 的数学核心是在恒定和（x + y = D）和恒定积（x * y = (D/2)^2）之间的插值：

```
A * n^n * sum(x_i) + D = A * D * n^n + D^(n+1) / (n^n * prod(x_i))
```

其中 A 是放大系数（Amplification factor），控制曲线在 1:1 附近的平坦程度：
- A 很大时，曲线接近恒定和（x + y = D），几乎零滑点
- A = 0 时，退化为恒定积（x * y = k），和普通 AMM 一样

### 5.2 本 demo 的简化

demo 中用一个简化的模型：对于接近 1:1 的稳定币交换，滑点极小（乘以 0.999 系数），对于偏离 1:1 的情况适当增加滑点。

---

## 6. Solana 交易指令组装 [链特定层] [L2]

### 6.1 指令序列

一笔完整的 Solana Swap 交易包含多条指令：

```
Transaction:
  Instruction[0]: ComputeBudget.SetComputeUnitLimit(200000)
  Instruction[1]: ComputeBudget.SetComputeUnitPrice(1000)  // 优先费
  Instruction[2]: AssociatedTokenAccount.CreateIdempotent(...)  // 创建 ATA
  Instruction[3]: DexProgram.Swap(...)  // 执行 Swap
```

**为什么 ComputeBudget 要放第一个？**
- Solana 验证器在处理交易前就需要知道 CU 限制来排序交易
- 优先费 = CU_price * CU_limit，影响交易在队列中的优先级

### 6.2 Address Lookup Table (ALT)

Solana 交易有 1232 字节的硬限制。一个 Raydium AMM Swap 可能涉及 10+ 个账户地址，每个 32 字节，很容易超限。

ALT 的工作原理：
1. 链上预先创建一个 ALT 账户，存储常用地址列表
2. 交易中引用 ALT 的地址，用 1 字节的索引替代 32 字节的地址
3. 验证器在执行时展开这些索引

```
未压缩: 10 个地址 * 32 字节 = 320 字节
ALT 压缩: ALT 引用 32 字节 + 10 个索引 * 1 字节 = 42 字节
节省: 278 字节 (87%)
```

### 6.3 ATA (Associated Token Account)

Solana 的代币余额不存储在用户钱包账户中，而是存储在一个「关联代币账户」中：

```
ATA 地址 = PDA(用户地址, TokenProgram, 代币 Mint 地址)
```

如果用户第一次接收某个代币，需要先创建这个 ATA。CreateIdempotent 指令是幂等的，已存在也不会失败。

---

## 7. EVM 交易构建 [链特定层] [L2]

### 7.1 Approve 机制

ERC20 代币的转移需要两步：
1. **Approve**: 代币持有者授权 Router 合约可以转移自己的代币
2. **transferFrom**: Router 合约在 Swap 过程中调用 transferFrom 转移代币

```
用户 --approve(router, amount)--> ERC20 合约
用户 --swap(...)--> Router 合约 --transferFrom(用户, pool, amount)--> ERC20 合约
```

**Solana 不需要 Approve 的原因：**
Solana 使用账户所有权模型，交易中直接提供签名，无需额外授权步骤。

### 7.2 ABI 编码

EVM 合约调用的输入数据（calldata）按 ABI 规范编码：

```
calldata = 函数选择器(4 bytes) + 参数编码

函数选择器 = keccak256("swapExactTokensForTokens(uint256,uint256,address[],address,uint256)")[:4]

参数按 32 字节对齐，左侧补零
```

### 7.3 EIP-1559 Gas 模型

```
总费用 = gasUsed * (baseFee + maxPriorityFeePerGas)

其中:
- baseFee: 由协议根据上一个区块的 Gas 使用率决定
- maxPriorityFeePerGas: 用户设置的矿工小费
- maxFeePerGas: 用户愿意支付的最高 Gas 价格
- gasLimit: 交易允许消耗的最大 Gas 数量
```

BSC 不支持 EIP-1559，使用 Legacy Gas 模型：

```
总费用 = gasUsed * gasPrice
```

coinset.ChainConfig.Features[FeatureEIP1559] 控制使用哪种模型。

---

## 8. 工厂模式与并发安全 [dexwallet 通用层] [L2]

### 8.1 SwapBuilderFactory 设计

```go
type SwapBuilderFactory struct {
    mu       sync.RWMutex
    builders map[DexID]SwapBuilder
}
```

使用读写锁而非互斥锁的原因：
- 注册（写操作）只在初始化阶段发生，频率极低
- 查找和构建（读操作）在运行时高频发生
- RWMutex 允许多个读操作并发执行

### 8.2 Build 方法的路由逻辑

```
Build(req)
  |
  req.DexID 是否指定?
  |-- 是 --> 直接使用对应 builder
  |-- 否 --> 返回错误（DexID 的选择是 Aggregator 的职责）
```

SwapBuilderFactory 不做 DEX 选择，它是纯粹的「注册表」。DEX 选择由 Aggregator 在更上层完成。

---

## 9. 交易模拟的意义 [dexwallet 通用层] [L3]

### 9.1 为什么需要模拟

发送一笔链上交易需要消耗真实的 Gas 费用。如果交易注定会失败（余额不足、滑点过高、合约限制），白白浪费 Gas。模拟可以在发送前以零成本检测这些问题。

### 9.2 Solana simulateTransaction

```json
// 请求
{"method": "simulateTransaction", "params": ["base64_tx", {"sigVerify": false}]}

// 响应
{
  "value": {
    "err": null,        // null 表示成功
    "logs": [...],      // 执行日志
    "unitsConsumed": 50000  // 消耗的计算单元
  }
}
```

`sigVerify: false` 允许不签名就模拟，方便在签名前检查。

### 9.3 EVM eth_call

```json
// 请求
{"method": "eth_call", "params": [{"to": "0x...", "data": "0x..."}, "latest"]}

// 响应
{"result": "0x000...000a"}  // 返回值（ABI 编码）
```

eth_call 模拟执行但不上链，不需要 Gas 但需要 from 地址有足够余额（某些实现）。

### 9.4 模拟与实际执行的时间差

模拟成功不保证实际执行成功。在模拟和执行之间：
- 池子状态可能变化（其他人先交易了）
- 区块的 baseFee 可能上涨
- 代币合约可能被修改（添加黑名单等）

这就是为什么滑点保护至关重要。

---

## 10. DEX 优先级与降级策略 [dexwallet 通用层] [L3]

### 10.1 优先级设计

```
PriorityHigh(1):   内盘（BondingCurve） -- PumpFun 等
PriorityMedium(2): AMM/CLMM -- Raydium, Uniswap, PancakeSwap
PriorityLow(3):    聚合器 -- Jupiter, 1inch
```

**为什么内盘优先？**
如果代币还在内盘（未毕业），只有通过内盘合约才能交易。使用聚合器可能找不到路由或价格更差。

**为什么聚合器优先级最低？**
聚合器本身会调用底层 DEX，多一层中间商意味着：
- 可能有额外费用
- 延迟更高
- 在直接可用的 DEX 上直接交易更高效

聚合器的价值在于：
- 处理大额交易时的路由拆分
- 处理没有直接交易对的情况（多跳路由）
- 作为所有直接 DEX 都失败时的兜底

### 10.2 降级流程

```
尝试 PumpFun (内盘)
  |-- 毕业了/失败 --> 尝试 Raydium AMM
                        |-- 池子不存在/失败 --> 尝试 Jupiter (聚合器)
                                                  |-- 失败 --> 返回错误
```

这个降级链在 BaseAggregator.buildWithFallback 中实现。

---

## 11. 滑点保护机制 [dexwallet 通用层] [L2]

### 11.1 滑点的来源

1. **价格影响（Price Impact）**: 交易金额越大，对池子价格的影响越大
2. **前端运行（Front-running）**: MEV 机器人在你的交易前插入交易，推高价格
3. **池子状态变化**: 从计算到上链的时间差内，其他交易改变了池子

### 11.2 MinOutput 的计算

```
MinOutput = OutputAmount * (10000 - SlippageBps) / 10000

例：OutputAmount = 1000, SlippageBps = 100 (1%)
MinOutput = 1000 * 9900 / 10000 = 990
```

链上合约会检查实际输出是否 >= MinOutput，如果不满足则回滚交易。

### 11.3 滑点设置的权衡

- 太小（如 10 BPS = 0.1%）：交易经常因为正常波动而失败
- 太大（如 5000 BPS = 50%）：容易被 MEV 攻击，三明治夹子利润空间大
- 生产中常用：100-300 BPS (1%-3%)，对于波动大的 MEME 币可能需要更高
