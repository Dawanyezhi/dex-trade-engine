# 07-event-parsing: 深度技术笔记

> 本文档用真实交易 JSON 逐字段讲解 Solana 指令解析和 EVM 事件解析。
> 目标：看完后能从零实现一个链上事件解析器。

---

## 目录

1. [事件解析器注册机制](#1-事件解析器注册机制)
2. [Solana 交易结构与解析](#2-solana-交易结构与解析)
   - 2.1 getTransaction 完整返回
   - 2.2 逐字段解读
   - 2.3 Raydium AMM Swap 指令解析实战
   - 2.4 PumpFun 交易解析实战
   - 2.5 SPL Token Transfer 解析
   - 2.6 CPI 与 innerInstructions
   - 2.7 Borsh 编码格式详解
3. [EVM 交易结构与解析](#3-evm-交易结构与解析)
   - 3.1 eth_getTransactionReceipt 完整返回
   - 3.2 逐字段解读
   - 3.3 UniswapV2 Swap 事件解析实战
   - 3.4 ERC20 Transfer 事件解析实战
   - 3.5 UniswapV3 Swap 事件（与 V2 的区别）
   - 3.6 ABI 编码格式详解
   - 3.7 Topic 签名计算方法
4. [Syncer 同步设计](#4-syncer-同步设计)
5. [生产代码中的解析模式](#5-生产代码中的解析模式)
6. [金额处理与精度](#6-金额处理与精度)
7. [日志与监控设计](#7-日志与监控设计)

---

## 1. 事件解析器注册机制

> [dexwallet 通用层]

### 1.1 为什么用注册表模式

链上的 DEX 种类繁多且在不断增加。如果用 `switch-case` 硬编码：

```go
// 差的做法 -- 每新增一个 DEX 都要改这个文件
switch programID {
case "675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8":
    return parseRaydiumSwap(data)
case "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P":
    return parsePumpFun(data)
// ... 新增 DEX 时必须修改这里
}
```

注册表模式将"匹配"和"解析"解耦，新增 DEX 只需注册，不改核心代码：

```go
// 好的做法 -- 注册表模式
registry.Register("675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8", raydiumSwapHandler)
registry.Register("6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P", pumpfunHandler)
// 新增 DEX 只需一行注册
```

### 1.2 标识符的设计选择

| 链 | 标识符 | 示例 | 特点 |
|------|--------|------|------|
| Solana | ProgramID（程序公钥） | `675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8` | 同一 Program 可能有多种操作（swap/addLiquidity/...），解析器内部需要进一步判断指令类型 |
| EVM | Topic[0]（事件签名哈希） | `0xd78ad95f...` | 每种事件有唯一签名哈希，天然适合做 map key |

**关键区别**：Solana 是"一个 Program 多种操作"，EVM 是"一种事件一个签名"。这导致 Solana 解析器往往更复杂——注册时用 ProgramID 粗粒度匹配，解析时还要读 instruction discriminator 细粒度区分。

---

## 2. Solana 交易结构与解析

### 2.1 getTransaction 完整返回

以下是 Solana RPC `getTransaction` 返回的真实结构（以一笔 Raydium AMM Swap 为例）。理解这个 JSON 是写解析器的前提：

```json
{
  "jsonrpc": "2.0",
  "result": {
    "slot": 250000001,
    "blockTime": 1700000000,
    "transaction": {
      "signatures": [
        "5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tjF3ZpRzrFmBV6UjKdiSZkQUW"
      ],
      "message": {
        "accountKeys": [
          "UserWa11et1111111111111111111111111111111111",
          "675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8",
          "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
          "58oQChx4yWmvKdwLLZzBi4ChoCc2fqCUWBkwMihLYQo2",
          "DuMqJ62VdPBrdEFafo3cKPSwRZsQwmFHM4kNeMYg1Y3x",
          "9gGJAY2cHLsREjnfjy4aTqoP8gPFXBCg6ZaGJFBvJc8B",
          "HLmqeL62xR1QoZ1HKKbXRrdN1p3phKpxRMb2VVopvBBz",
          "ComputeBudget111111111111111111111111111111",
          "11111111111111111111111111111111"
        ],
        "instructions": [
          {
            "programIdIndex": 7,
            "accounts": [],
            "data": "3butUEijJrLf"
          },
          {
            "programIdIndex": 7,
            "accounts": [],
            "data": "Fhm2TM3Y1S1a"
          },
          {
            "programIdIndex": 1,
            "accounts": [2, 3, 4, 5, 6, 0, 2, 8],
            "data": "6Nst4FnmdKAKJ"
          }
        ],
        "recentBlockhash": "7GhFi2DRmKyZvBp3R1GJWRx9pQLAWR3KS4PYWmS8CG6T"
      }
    },
    "meta": {
      "err": null,
      "fee": 5001,
      "preBalances": [1000000000, 0, 0, 0, 0, 0, 0, 0, 0],
      "postBalances": [995000000, 0, 0, 0, 0, 0, 0, 0, 0],
      "preTokenBalances": [
        {
          "accountIndex": 5,
          "mint": "So11111111111111111111111111111111111111112",
          "uiTokenAmount": {"uiAmount": 100.0, "decimals": 9, "amount": "100000000000"}
        },
        {
          "accountIndex": 6,
          "mint": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
          "uiTokenAmount": {"uiAmount": 15000.0, "decimals": 6, "amount": "15000000000"}
        }
      ],
      "postTokenBalances": [
        {
          "accountIndex": 5,
          "mint": "So11111111111111111111111111111111111111112",
          "uiTokenAmount": {"uiAmount": 101.0, "decimals": 9, "amount": "101000000000"}
        },
        {
          "accountIndex": 6,
          "mint": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
          "uiTokenAmount": {"uiAmount": 14850.0, "decimals": 6, "amount": "14850000000"}
        }
      ],
      "innerInstructions": [
        {
          "index": 2,
          "instructions": [
            {
              "programIdIndex": 2,
              "accounts": [0, 5],
              "data": "3Bxs4h24hBtQy9rw"
            },
            {
              "programIdIndex": 2,
              "accounts": [6, 0],
              "data": "3Bxs3zwsaypMvxhX"
            }
          ]
        }
      ],
      "logMessages": [
        "Program ComputeBudget111111111111111111111111111111 invoke [1]",
        "Program ComputeBudget111111111111111111111111111111 success",
        "Program 675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8 invoke [1]",
        "Program log: ray_log: SwapBaseIn { amount_in: 1000000000, minimum_amount_out: 140000000 }",
        "Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA invoke [2]",
        "Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA success",
        "Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA invoke [2]",
        "Program TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA success",
        "Program 675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8 success"
      ]
    }
  }
}
```

### 2.2 逐字段解读

#### transaction.signatures

```json
"signatures": ["5VERv8NMvzbJMEkV8xnrLk..."]
```

- 交易的签名列表，**第一个签名同时也是交易的唯一 ID**（即 txHash）
- Solana 交易可以有多个签名者（multisig 场景），但对于普通 swap 交易只有一个

#### transaction.message.accountKeys

```json
"accountKeys": [
  "UserWa11et...",                           // index 0: 用户钱包（signer + fee payer）
  "675kPX9MHTjS2zt1qfr1NYH...",             // index 1: Raydium AMM Program
  "TokenkegQfeZyiNwAJbNbGKP...",             // index 2: SPL Token Program
  "58oQChx4yWmvKdwLLZzBi4C...",             // index 3: AMM ID（池子标识）
  "DuMqJ62VdPBrdEFafo3cKPS...",             // index 4: AMM Authority
  "9gGJAY2cHLsREjnfjy4aTqo...",             // index 5: Pool Coin Vault（tokenA 金库）
  "HLmqeL62xR1QoZ1HKKbXRrd...",             // index 6: Pool PC Vault（tokenB 金库）
  "ComputeBudget111111111111...",             // index 7: ComputeBudget Program
  "11111111111111111111111111..."             // index 8: System Program
]
```

**核心概念**：Solana 交易中所有涉及的账户都平铺在 `accountKeys` 数组里。指令中通过 **索引** 引用这些账户，而不是直接写地址。这是 Solana 的独特设计，目的是减少交易大小。

#### transaction.message.instructions

```json
"instructions": [
  {
    "programIdIndex": 7,    // 指向 accountKeys[7] = ComputeBudget Program
    "accounts": [],          // 无需额外账户
    "data": "3butUEijJrLf"  // base58 编码的指令数据（设置 CU 上限）
  },
  {
    "programIdIndex": 7,    // 又是 ComputeBudget（设置 CU price）
    "accounts": [],
    "data": "Fhm2TM3Y1S1a" // base58 编码的指令数据（设置优先费）
  },
  {
    "programIdIndex": 1,    // 指向 accountKeys[1] = Raydium AMM Program ← 这就是 Swap 指令
    "accounts": [2, 3, 4, 5, 6, 0, 2, 8],  // 引用的账户索引
    "data": "6Nst4FnmdKAKJ"                // base58 编码的 Swap 指令数据
  }
]
```

**一笔典型的 Swap 交易通常包含 3 条指令**：

| 序号 | Program | 作用 |
|------|---------|------|
| 0 | ComputeBudget | `SetComputeUnitLimit` — 设置这笔交易最多消耗多少 CU |
| 1 | ComputeBudget | `SetComputeUnitPrice` — 设置优先费（micro-lamports per CU） |
| 2 | Raydium AMM | 真正的 Swap 指令 |

**解析器只关心第 3 条指令**，因为前两条是交易参数设置，与业务无关。

#### instructions[2].accounts 详解（Raydium AMM Swap）

```
accounts: [2, 3, 4, 5, 6, 0, 2, 8]
```

每个位置对应 Raydium AMM Swap 的固定账户布局：

| 位置 | 索引 | 实际账户 | 角色 |
|------|------|----------|------|
| 0 | 2 | TokenProgram | SPL Token 程序（CPI 调用用） |
| 1 | 3 | AMM ID | 池子标识账户 |
| 2 | 4 | AMM Authority | 池子授权账户（PDA） |
| 3 | 5 | Pool Coin Vault | 池子 tokenA 金库 |
| 4 | 6 | Pool PC Vault | 池子 tokenB 金库 |
| 5 | 0 | User Wallet | 用户钱包（signer） |
| 6 | 2 | TokenProgram | 重复引用（Raydium 需要两次） |
| 7 | 8 | SystemProgram | 系统程序 |

**生产代码的解析方式**：按固定位置提取账户，`accounts[1]` 对应的 accountKey 就是池子地址。

#### instructions[2].data 详解

`data` 字段是 **base58 编码的字节数组**，解码后是 Borsh 格式的指令数据：

```
原始 base58: "6Nst4FnmdKAKJ"
解码后 hex:  09 00ca9a3b00000000 00ab5d86000000000
             ^^ ^^^^^^^^^^^^^^^^ ^^^^^^^^^^^^^^^^
             |  |                |
             |  amount_in        minimum_amount_out
             instruction discriminator (09 = SwapBaseIn)
```

具体解码（Borsh 小端序）：

| 偏移 | 长度 | 值 | 含义 |
|------|------|------|------|
| 0 | 1 | `0x09` | 指令类型判别符（Raydium AMM v4 的 swap = 9） |
| 1 | 8 | `0x00ca9a3b00000000` | amount_in = 1,000,000,000 lamports = 1 SOL |
| 9 | 8 | `0x00ab5d8600000000` | minimum_amount_out = 140,000,000 = 140 USDC（6位精度） |

#### meta.preTokenBalances / postTokenBalances

```json
"preTokenBalances": [
  {"accountIndex": 5, "mint": "So1111...2", "uiTokenAmount": {"amount": "100000000000", "decimals": 9}},
  {"accountIndex": 6, "mint": "EPjFWd...v", "uiTokenAmount": {"amount": "15000000000", "decimals": 6}}
],
"postTokenBalances": [
  {"accountIndex": 5, "mint": "So1111...2", "uiTokenAmount": {"amount": "101000000000", "decimals": 9}},
  {"accountIndex": 6, "mint": "EPjFWd...v", "uiTokenAmount": {"amount": "14850000000", "decimals": 6}}
]
```

**这是最可靠的解析方式之一**：通过比较 pre 和 post 的 token balance 变化来确定 swap 金额：

```
Pool Coin Vault (SOL):  pre=100 SOL  → post=101 SOL    → +1 SOL（用户付出）
Pool PC Vault (USDC):   pre=15000 USDC → post=14850 USDC → -150 USDC（用户获得）
```

结论：用户用 1 SOL 换了 150 USDC。

**生产中的最佳实践**：指令 data 解码 + balance diff 交叉验证，确保解析结果准确。

#### meta.innerInstructions

```json
"innerInstructions": [
  {
    "index": 2,
    "instructions": [
      {"programIdIndex": 2, "accounts": [0, 5], "data": "3Bxs4h24hBtQy9rw"},
      {"programIdIndex": 2, "accounts": [6, 0], "data": "3Bxs3zwsaypMvxhX"}
    ]
  }
]
```

`index: 2` 表示这些内部指令是由**顶层 instructions[2]**（即 Raydium Swap 指令）通过 CPI 触发的。

CPI（Cross-Program Invocation）是 Solana 的跨程序调用机制：

```
[顶层] Raydium AMM.swap()
  |
  |-- [CPI] TokenProgram.transfer(user → pool_coin_vault, 1 SOL)
  |         对应 innerInstructions[0]
  |
  |-- [CPI] TokenProgram.transfer(pool_pc_vault → user, 150 USDC)
              对应 innerInstructions[1]
```

**为什么要解析 innerInstructions**：

1. 顶层的 Raydium 指令只包含 `amount_in` 和 `minimum_amount_out`（滑点保护值），**真正的输出金额在 CPI 的 Transfer 中**
2. 聚合器（如 Jupiter）只有一条顶层指令，所有 DEX 的 Swap 都在 innerInstructions 里
3. 生产解析器必须递归解析 innerInstructions，否则会遗漏大量事件

#### meta.logMessages

```
"Program 675kPX9MHTjS2zt1qfr1NYH... invoke [1]"        ← Raydium 被调用（深度 1 = 顶层）
"Program log: ray_log: SwapBaseIn { amount_in: 1000000000, minimum_amount_out: 140000000 }"
"Program TokenkegQfeZyiNwAJbNbGKP... invoke [2]"        ← Token Program 被 CPI 调用（深度 2）
"Program TokenkegQfeZyiNwAJbNbGKP... success"
"Program TokenkegQfeZyiNwAJbNbGKP... invoke [2]"        ← 第二次 CPI
"Program TokenkegQfeZyiNwAJbNbGKP... success"
"Program 675kPX9MHTjS2zt1qfr1NYH... success"            ← Raydium 执行成功
```

日志中的 `invoke [N]` 表示调用深度：`[1]` 是顶层，`[2]` 是 CPI 第一层，`[3]` 是 CPI 嵌套调用。

**Raydium 特有**：`ray_log:` 前缀的日志包含了 swap 参数。这是 Raydium 自定义的日志格式，不同 DEX 的日志格式完全不同。PumpFun 没有结构化日志，Orca/Whirlpool 用二进制编码日志。

### 2.3 Raydium AMM Swap 指令解析实战

**步骤 1：匹配**

```go
// 遍历 transaction.message.instructions
for i, inst := range tx.Message.Instructions {
    programID := tx.Message.AccountKeys[inst.ProgramIdIndex]

    // 匹配 Raydium AMM ProgramID
    if programID == "675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8" {
        event, err := parseRaydiumSwap(tx, inst, i)
        // ...
    }
}
```

**步骤 2：解码 instruction data**

```go
func parseRaydiumSwap(tx *Transaction, inst Instruction, instIdx int) (*ChainEvent, error) {
    // 1. Base58 解码 data
    rawData := base58.Decode(inst.Data)

    // 2. 读取 instruction discriminator（第 1 个字节）
    discriminator := rawData[0]
    // Raydium AMM v4 的指令类型:
    //   9  = SwapBaseIn（指定输入金额）
    //   11 = SwapBaseOut（指定输出金额）
    if discriminator != 9 && discriminator != 11 {
        return nil, nil // 不是 Swap 指令，跳过（可能是 addLiquidity/removeLiquidity 等）
    }

    // 3. Borsh 解码 swap 参数（小端序）
    amountIn := binary.LittleEndian.Uint64(rawData[1:9])
    minAmountOut := binary.LittleEndian.Uint64(rawData[9:17])

    // 4. 从 accounts 提取池子地址
    ammIDIndex := inst.Accounts[1] // 位置 1 固定是 AMM ID
    poolAddress := tx.Message.AccountKeys[ammIDIndex]

    // ...
}
```

**步骤 3：确定实际输出金额**

指令中的 `minimum_amount_out` 只是滑点保护值，**不是实际输出金额**。要获取真实金额有两种方式：

方式 A — 解析 innerInstructions 中的 Transfer：

```go
// 找到 instIdx 对应的 innerInstructions
for _, inner := range tx.Meta.InnerInstructions {
    if inner.Index == instIdx {
        // inner.Instructions[1] 是第二个 CPI Transfer（池子 → 用户）
        transferData := base58.Decode(inner.Instructions[1].Data)
        // SPL Token Transfer 的 data: [1字节类型][8字节金额]
        actualAmountOut := binary.LittleEndian.Uint64(transferData[1:9])
    }
}
```

方式 B — 使用 preTokenBalances/postTokenBalances 差值：

```go
// 比较 vault 账户的 pre/post balance
func getActualAmounts(tx *Transaction, vaultCoinIdx, vaultPCIdx int) (amountIn, amountOut uint64) {
    preCoin := findTokenBalance(tx.Meta.PreTokenBalances, vaultCoinIdx)
    postCoin := findTokenBalance(tx.Meta.PostTokenBalances, vaultCoinIdx)
    prePC := findTokenBalance(tx.Meta.PreTokenBalances, vaultPCIdx)
    postPC := findTokenBalance(tx.Meta.PostTokenBalances, vaultPCIdx)

    // Vault 余额增加 = 用户付出的金额
    amountIn = postCoin.Amount - preCoin.Amount
    // Vault 余额减少 = 用户获得的金额
    amountOut = prePC.Amount - postPC.Amount
    return
}
```

**生产中两种方式都用**，交叉验证确保解析正确。

### 2.4 PumpFun 交易解析实战

PumpFun 的交易结构与 Raydium 完全不同。PumpFun 是 Bonding Curve（联合曲线），不是 AMM 池子。

**PumpFun 真实交易结构**：

```json
{
  "transaction": {
    "message": {
      "accountKeys": [
        "UserWallet...",
        "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P",
        "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
        "MintAddress1111111111111111111111111111111",
        "BondingCurveAccount11111111111111111111111",
        "AssociatedTokenAccount111111111111111111111"
      ],
      "instructions": [
        {
          "programIdIndex": 1,
          "accounts": [4, 3, 5, 0, 2],
          "data": "66063d1201daebea ..."
        }
      ]
    }
  }
}
```

**PumpFun instruction data 格式**：

```
偏移    长度    值                     含义
0       8       66063d1201daebea       instruction discriminator（buy 的固定 8 字节）
8       8       00e1f50500000000       token_amount: 100000000（买入的 token 数量）
16      8       00ca9a3b00000000       max_sol_cost: 1000000000（最多花 1 SOL）
```

PumpFun 的 discriminator 是 8 字节（而不是 Raydium 的 1 字节），这是因为 PumpFun 使用 Anchor 框架，Anchor 用 `sha256("global:<method_name>")` 的前 8 字节作为 discriminator。

```
buy 的 discriminator:  sha256("global:buy")[0:8]  = 66063d1201daebea
sell 的 discriminator: sha256("global:sell")[0:8] = 33e685a4017f83ad
```

**解析关键**：

```go
func parsePumpFunTrade(data []byte) {
    disc := data[0:8]

    isBuy := bytes.Equal(disc, pumpFunBuyDiscriminator)
    isSell := bytes.Equal(disc, pumpFunSellDiscriminator)

    if isBuy {
        tokenAmount := binary.LittleEndian.Uint64(data[8:16])
        maxSolCost := binary.LittleEndian.Uint64(data[16:24])
        // 注意: maxSolCost 是上限，实际花费需要从 balance diff 获取
    } else if isSell {
        tokenAmount := binary.LittleEndian.Uint64(data[8:16])
        minSolOutput := binary.LittleEndian.Uint64(data[16:24])
    }
}
```

### 2.5 SPL Token Transfer 解析

SPL Token Program 是 Solana 上所有代币转账的统一程序。

**instruction data 格式**：

```
偏移    长度    值               含义
0       1       03               instruction_type (3 = Transfer)
1       8       00e1f50500000000 amount: 100000000（小端序）
```

指令类型枚举：

| 值 | 类型 | 说明 |
|------|------|------|
| 0 | InitializeMint | 创建代币 |
| 1 | InitializeAccount | 创建 Token 账户 |
| 3 | Transfer | 转账 |
| 4 | Approve | 授权 |
| 7 | MintTo | 铸造 |
| 8 | Burn | 销毁 |
| 12 | TransferChecked | 带精度检查的转账（更常用） |

**注意 Token2022**：新版本的 Token Program（`TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb`），支持 transfer fee、confidential transfer 等新特性，instruction 格式略有不同。

### 2.6 CPI 与 innerInstructions 详解

Solana 的 CPI（Cross-Program Invocation）是理解事件解析的关键。

**三种常见的 CPI 模式**：

```
模式 1: 简单 Swap（Raydium AMM）
[顶层] Raydium.swap()
  ├── [CPI] Token.transfer(user → vault_a)     用户付 tokenA
  └── [CPI] Token.transfer(vault_b → user)     用户收 tokenB

模式 2: 聚合器多跳（Jupiter）
[顶层] Jupiter.route()
  ├── [CPI] Raydium.swap()
  │     ├── [CPI] Token.transfer(user → raydium_vault)
  │     └── [CPI] Token.transfer(raydium_vault → user)
  ├── [CPI] Orca.swap()
  │     ├── [CPI] Token.transfer(user → orca_vault)
  │     └── [CPI] Token.transfer(orca_vault → user)
  └── [CPI] Meteora.swap()
        ├── [CPI] Token.transfer(user → meteora_vault)
        └── [CPI] Token.transfer(meteora_vault → user)

模式 3: PumpFun 毕业（内盘 → AMM）
[顶层] PumpFun.trade()
  ├── [CPI] Token.transfer(...)    bonding curve 上的交易
  └── [CPI] Raydium.initialize()   如果池子毕业，CPI 调用 Raydium 创建 AMM 池子
```

**生产解析器必须递归处理 innerInstructions**：

```go
func parseTransaction(tx *Transaction) []ChainEvent {
    var events []ChainEvent

    // 1. 解析顶层指令
    for i, inst := range tx.Message.Instructions {
        programID := tx.Message.AccountKeys[inst.ProgramIdIndex]
        if handler, ok := registry[programID]; ok {
            event := handler(inst)
            events = append(events, event)
        }
    }

    // 2. 解析内部指令（CPI 触发的）
    for _, inner := range tx.Meta.InnerInstructions {
        for _, inst := range inner.Instructions {
            programID := tx.Message.AccountKeys[inst.ProgramIdIndex]
            if handler, ok := registry[programID]; ok {
                event := handler(inst)
                events = append(events, event)
            }
        }
    }

    return events
}
```

### 2.7 Borsh 编码格式详解

Borsh（Binary Object Representation Serializer for Hashing）是 Solana 上最常用的序列化格式。

**核心规则**：

| Go 类型 | Borsh 编码 | 大小 |
|---------|-----------|------|
| u8 | 1 字节 | 1 |
| u16 | 小端序 2 字节 | 2 |
| u32 | 小端序 4 字节 | 4 |
| u64 | 小端序 8 字节 | 8 |
| u128 | 小端序 16 字节 | 16 |
| bool | 1 字节（0=false, 1=true） | 1 |
| string | 4 字节长度(u32) + UTF-8 字节 | 4+N |
| Option\<T\> | 1 字节（0=None, 1=Some）+ T | 1 或 1+sizeof(T) |
| Vec\<T\> | 4 字节长度(u32) + N*sizeof(T) | 4+N*sizeof(T) |
| [u8; N] | N 字节（固定大小） | N |
| Pubkey | 32 字节 | 32 |

**Go 中的 Borsh 解码示例**：

```go
// 解码 Raydium SwapBaseIn 指令
type RaydiumSwapBaseIn struct {
    Discriminator uint8
    AmountIn      uint64
    MinAmountOut  uint64
}

func decodeRaydiumSwap(data []byte) RaydiumSwapBaseIn {
    return RaydiumSwapBaseIn{
        Discriminator: data[0],
        AmountIn:      binary.LittleEndian.Uint64(data[1:9]),
        MinAmountOut:  binary.LittleEndian.Uint64(data[9:17]),
    }
}
```

**Anchor 框架的 discriminator**：

使用 Anchor 框架的程序（PumpFun、Meteora、PumpAMM 等）用 8 字节 discriminator：

```go
// Anchor discriminator = sha256("global:<method_name>") 的前 8 字节
func anchorDiscriminator(methodName string) [8]byte {
    hash := sha256.Sum256([]byte("global:" + methodName))
    var disc [8]byte
    copy(disc[:], hash[:8])
    return disc
}
// 例：anchorDiscriminator("buy") → [102 6 61 18 1 218 235 234]
```

非 Anchor 程序（Raydium AMM v4、SPL Token）用 1 字节或 4 字节 discriminator，每个程序自定义格式。

---

## 3. EVM 交易结构与解析

### 3.1 eth_getTransactionReceipt 完整返回

以下是 EVM RPC `eth_getTransactionReceipt` 返回的真实结构（以一笔 UniswapV2 Swap 为例）：

```json
{
  "jsonrpc": "2.0",
  "result": {
    "transactionHash": "0xa1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456",
    "transactionIndex": "0x5",
    "blockHash": "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
    "blockNumber": "0x112a881",
    "from": "0xUserAddress1111111111111111111111111111",
    "to": "0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D",
    "cumulativeGasUsed": "0x7a120",
    "gasUsed": "0x28f5c",
    "effectiveGasPrice": "0x12a05f200",
    "status": "0x1",
    "logs": [
      {
        "address": "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",
        "topics": [
          "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
          "0x000000000000000000000000UserAddress1111111111111111111111111111",
          "0x000000000000000000000000B4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc"
        ],
        "data": "0x0000000000000000000000000000000000000000000000000de0b6b3a7640000",
        "blockNumber": "0x112a881",
        "transactionHash": "0xa1b2c3...123456",
        "transactionIndex": "0x5",
        "logIndex": "0x0",
        "removed": false
      },
      {
        "address": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
        "topics": [
          "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
          "0x000000000000000000000000B4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc",
          "0x000000000000000000000000UserAddress1111111111111111111111111111"
        ],
        "data": "0x00000000000000000000000000000000000000000000000000000000d09dc300",
        "blockNumber": "0x112a881",
        "transactionHash": "0xa1b2c3...123456",
        "transactionIndex": "0x5",
        "logIndex": "0x1",
        "removed": false
      },
      {
        "address": "0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc",
        "topics": [
          "0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822",
          "0x0000000000000000000000007a250d5630B4cF539739dF2C5dAcb4c659F2488D",
          "0x000000000000000000000000UserAddress1111111111111111111111111111"
        ],
        "data": "0x0000000000000000000000000000000000000000000000000de0b6b3a76400000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000d09dc300",
        "blockNumber": "0x112a881",
        "transactionHash": "0xa1b2c3...123456",
        "transactionIndex": "0x5",
        "logIndex": "0x2",
        "removed": false
      }
    ],
    "logsBloom": "0x00000000000000000000000080000..."
  }
}
```

### 3.2 逐字段解读

#### Receipt 顶层字段

```json
"status": "0x1"          // 1=成功, 0=失败。失败的交易 logs 为空。
"gasUsed": "0x28f5c"     // 实际消耗的 gas = 167772
"effectiveGasPrice": "0x12a05f200"  // 实际 gas price = 5 Gwei
// 交易费 = gasUsed * effectiveGasPrice = 167772 * 5 Gwei = 0.000839 ETH
"to": "0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D"  // UniswapV2 Router 合约
```

#### 一笔 Swap 交易的 3 个 Log（按执行顺序）

**Log[0] — ERC20 Transfer: 用户 → Pair 合约（WETH 转入）**

```json
{
  "address": "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",  // WETH 合约地址
  "topics": [
    "0xddf252ad...f523b3ef",   // Transfer 事件签名
    "0x...UserAddress...",      // from = 用户钱包（indexed）
    "0x...B4e16d01..."          // to = Pair 合约（indexed）
  ],
  "data": "0x...0de0b6b3a7640000"  // value = 1000000000000000000 = 1 WETH
}
```

**Log[1] — ERC20 Transfer: Pair 合约 → 用户（USDT 转出）**

```json
{
  "address": "0xdAC17F958D2ee523a2206206994597C13D831ec7",  // USDT 合约地址
  "topics": [
    "0xddf252ad...f523b3ef",   // Transfer 事件签名
    "0x...B4e16d01...",         // from = Pair 合约（indexed）
    "0x...UserAddress..."       // to = 用户钱包（indexed）
  ],
  "data": "0x...d09dc300"  // value = 3500000000 = 3500 USDT（6位精度）
}
```

**Log[2] — UniswapV2 Swap 事件**

```json
{
  "address": "0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc",  // Pair 合约地址
  "topics": [
    "0xd78ad95f...159d822",    // Swap 事件签名
    "0x...7a250d56...",         // sender = Router 合约（indexed）
    "0x...UserAddress..."       // to = 用户钱包（indexed）
  ],
  "data": "0x 0000...0de0b6b3a7640000  // amount0In  = 1 WETH
              0000...0000000000000000  // amount1In  = 0
              0000...0000000000000000  // amount0Out = 0
              0000...00000000d09dc300"  // amount1Out = 3500 USDT
}
```

**执行顺序解读**：
1. Router 合约先将用户的 1 WETH 转到 Pair 合约（Transfer log）
2. Pair 合约将 3500 USDT 转给用户（Transfer log）
3. Pair 合约触发 Swap 事件记录本次交换的所有金额（Swap log）

### 3.3 UniswapV2 Swap 事件解析实战

**步骤 1：匹配 Topic[0]**

```go
// UniswapV2 Swap 事件签名
const uniV2SwapTopic = "0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822"

for _, log := range receipt.Logs {
    if len(log.Topics) >= 3 && log.Topics[0] == uniV2SwapTopic {
        event := parseUniV2Swap(log)
        // ...
    }
}
```

**步骤 2：解码 indexed 参数（Topics）**

```go
// Topics[1] = sender（Router 地址，20 字节右对齐到 32 字节）
sender := common.BytesToAddress(log.Topics[1].Bytes())  // 取最后 20 字节

// Topics[2] = to（接收者地址）
to := common.BytesToAddress(log.Topics[2].Bytes())
```

**步骤 3：解码 Data 中的非 indexed 参数**

```go
// Data 是 4 个 uint256（各 32 字节）紧密排列
// | 32 bytes amount0In | 32 bytes amount1In | 32 bytes amount0Out | 32 bytes amount1Out |
data := log.Data
amount0In  := new(big.Int).SetBytes(data[0:32])
amount1In  := new(big.Int).SetBytes(data[32:64])
amount0Out := new(big.Int).SetBytes(data[64:96])
amount1Out := new(big.Int).SetBytes(data[96:128])
```

**步骤 4：判断 Swap 方向**

```go
zero := big.NewInt(0)
if amount0In.Cmp(zero) > 0 && amount1Out.Cmp(zero) > 0 {
    // 用 token0 换 token1
    // 本例：用 WETH (token0) 换 USDT (token1)
    tokenIn = token0Address   // 需要另外查 Pair 合约获取 token0/token1
    tokenOut = token1Address
    amountIn = amount0In      // 1 WETH
    amountOut = amount1Out    // 3500 USDT
} else if amount1In.Cmp(zero) > 0 && amount0Out.Cmp(zero) > 0 {
    // 用 token1 换 token0
    tokenIn = token1Address
    tokenOut = token0Address
    amountIn = amount1In
    amountOut = amount0Out
}
```

**步骤 5：获取 token0/token1 地址**

Swap 事件本身不包含 token 地址。需要从 Pair 合约查询：

```go
// 方式 1: 调用 Pair 合约的 token0() / token1() 方法
token0, _ := pairContract.Token0(nil) // 0xC02aaA39... (WETH)
token1, _ := pairContract.Token1(nil) // 0xdAC17F95... (USDT)

// 方式 2: 从同一笔交易的 Transfer log 中推断
// Transfer log 的 address 字段就是 token 地址
// Log[0].address = WETH 合约, Log[1].address = USDT 合约

// 方式 3: 维护一个 pair→(token0,token1) 的缓存（生产中最常用）
```

### 3.4 ERC20 Transfer 事件解析实战

```solidity
// Solidity 事件定义
event Transfer(address indexed from, address indexed to, uint256 value);
```

**解析代码**：

```go
const erc20TransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

func parseERC20Transfer(log *types.Log) {
    // log.Address = token 合约地址（知道是哪个代币）
    tokenAddress := log.Address

    // Topics[1] = from（indexed）
    from := common.BytesToAddress(log.Topics[1].Bytes())

    // Topics[2] = to（indexed）
    to := common.BytesToAddress(log.Topics[2].Bytes())

    // Data = value（非 indexed）
    value := new(big.Int).SetBytes(log.Data[0:32])

    // 特殊场景判断:
    // from == 0x0 → Mint（铸造）
    // to == 0x0   → Burn（销毁）
    // 否则         → Transfer（转账）
}
```

### 3.5 UniswapV3 Swap 事件（与 V2 的区别）

UniswapV3 的 Swap 事件签名和参数完全不同：

```solidity
// UniswapV3 的 Swap 事件
event Swap(
    address indexed sender,
    address indexed recipient,
    int256 amount0,        // 注意是 int256（有符号），正=流入池子，负=流出池子
    int256 amount1,        // 同上
    uint160 sqrtPriceX96,  // 交易后的价格（Q64.96 格式）
    uint128 liquidity,     // 当前流动性
    int24 tick             // 当前 tick
);
```

Topic 签名: `keccak256("Swap(address,address,int256,int256,uint160,uint128,int24)")` = `0xc42079f94a6350d7e6235f29174924f928cc2ac818eb64fed8004e115fbcca67`

**V2 vs V3 的关键差异**：

| 维度 | UniswapV2 | UniswapV3 |
|------|-----------|-----------|
| 金额表示 | 4 个 uint256（amount0In/1In/0Out/1Out） | 2 个 int256（amount0/amount1，正=流入负=流出） |
| 方向判断 | 看哪个 In > 0 且哪个 Out > 0 | 看 amount0/amount1 的正负号 |
| 额外信息 | 无 | sqrtPriceX96 + liquidity + tick |
| Topic 签名 | `0xd78ad95f...` | `0xc42079f9...` |

**V3 解析**：

```go
// V3 的 data 包含 5 个参数
amount0       := new(big.Int).SetBytes(data[0:32])    // int256，可能为负
amount1       := new(big.Int).SetBytes(data[32:64])   // int256，可能为负
sqrtPriceX96  := new(big.Int).SetBytes(data[64:96])
liquidity     := new(big.Int).SetBytes(data[96:128])
tick          := new(big.Int).SetBytes(data[128:160])

// int256 解码：如果最高位是 1，则是负数
if amount0.Bit(255) == 1 {
    amount0.Sub(amount0, new(big.Int).Lsh(big.NewInt(1), 256))
}

// 方向判断：amount0 > 0 表示 token0 流入池子（用户付出 token0）
if amount0.Sign() > 0 {
    // 用户用 token0 换 token1
    amountIn = amount0
    amountOut = new(big.Int).Neg(amount1)  // amount1 是负数，取绝对值
} else {
    // 用户用 token1 换 token0
    amountIn = amount1
    amountOut = new(big.Int).Neg(amount0)
}
```

### 3.6 ABI 编码格式详解

EVM 事件的 Data 字段使用 ABI（Application Binary Interface）编码。

**编码规则**：

| Solidity 类型 | ABI 编码 | 大小 |
|--------------|----------|------|
| uint256 | 32 字节，大端序，左填充 0 | 32 |
| int256 | 32 字节，大端序，负数用二进制补码 | 32 |
| address | 32 字节，左填充 12 字节 0 + 20 字节地址 | 32 |
| bool | 32 字节，值为 0 或 1 | 32 |
| bytes32 | 32 字节，右填充 0 | 32 |
| string | 偏移量(32) + 长度(32) + 数据(按32对齐) | 动态 |
| bytes | 同 string | 动态 |
| uint256[] | 偏移量(32) + 长度(32) + N*32 | 动态 |

**实际解码示例**：UniswapV2 Swap 的 Data

```
Data = 0x
  0000000000000000000000000000000000000000000000000de0b6b3a7640000  // amount0In
  0000000000000000000000000000000000000000000000000000000000000000  // amount1In
  0000000000000000000000000000000000000000000000000000000000000000  // amount0Out
  00000000000000000000000000000000000000000000000000000000d09dc300  // amount1Out
```

解码：
- `amount0In  = 0x0de0b6b3a7640000 = 1000000000000000000 = 1e18 wei = 1 WETH`
- `amount1In  = 0`
- `amount0Out = 0`
- `amount1Out = 0xd09dc300 = 3500000000 = 3500 USDT（USDT 用 6 位精度）`

**注意**：ABI 编码是大端序（最高有效字节在前），与 Solana 的 Borsh 小端序相反。

### 3.7 Topic 签名计算方法

每种 EVM 事件的 Topic[0] 是事件签名的 keccak256 哈希。

**计算过程**：

```go
import "golang.org/x/crypto/sha3"

// 1. 写出事件的规范化签名（类型名用全称，参数名省略，无空格）
signature := "Swap(address,uint256,uint256,uint256,uint256,address)"

// 2. 计算 keccak256
hash := sha3.NewLegacyKeccak256()
hash.Write([]byte(signature))
topic := hash.Sum(nil)
// topic = 0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822
```

**常见事件的 Topic 签名**：

| 事件 | 签名字符串 | Topic[0] 前缀 |
|------|-----------|--------------|
| ERC20 Transfer | `Transfer(address,address,uint256)` | `0xddf252ad...` |
| ERC20 Approval | `Approval(address,address,uint256)` | `0x8c5be1e5...` |
| UniV2 Swap | `Swap(address,uint256,uint256,uint256,uint256,address)` | `0xd78ad95f...` |
| UniV2 Sync | `Sync(uint112,uint112)` | `0x1c411e9a...` |
| UniV3 Swap | `Swap(address,address,int256,int256,uint160,uint128,int24)` | `0xc42079f9...` |
| UniV2 Mint | `Mint(address,uint256,uint256)` | `0x4c209b5f...` |
| UniV2 Burn | `Burn(address,uint256,uint256,address)` | `0xdccd412f...` |
| PancakeSwap V2 Swap | 与 UniV2 相同 | `0xd78ad95f...` |

**indexed 参数的规则**：
- indexed 参数放在 `Topics[1]`、`Topics[2]`、`Topics[3]`（最多 3 个 indexed）
- 非 indexed 参数按顺序 ABI 编码后拼在 Data 中
- `address` 和 `uint256` 的 indexed 参数直接放在 topic 中（32 字节零填充）
- `string` 和 `bytes` 的 indexed 参数放的是 keccak256 哈希（无法直接还原原值）

---

## 4. Syncer 同步设计

> [dexwallet 通用层]

### 4.1 主循环设计

```
启动 Syncer
    |
    v
获取链上最新高度 (latestBlock)
    |
    v
对比本地高度 (currentHeight)
    |
    ├── currentHeight >= latestBlock → 等待（轮询间隔）
    |
    └── currentHeight < latestBlock → 逐区块处理
          |
          v
        getBlock(currentHeight + 1)
          |
          v
        检测重组（parentHash 是否匹配）
          |
          ├── 匹配 → 解析区块中的所有交易 → 分发事件 → 推进高度
          |
          └── 不匹配 → 回滚到分叉点 → 重新同步
```

**轮询 vs WebSocket**：

| 方式 | 优点 | 缺点 | 适用 |
|------|------|------|------|
| 轮询 | 简单可靠，断线自动恢复 | 有延迟（等待下一次轮询） | 大多数生产系统 |
| WebSocket | 实时性好 | 断线需要重连+补数据逻辑 | 对延迟敏感的场景 |

生产中通常 **WebSocket 为主 + 轮询兜底**：正常用 WS 实时接收区块，WS 断开后切到轮询追赶。

### 4.2 重组检测

```go
// 维护已处理区块的哈希记录
blockHashes := map[uint64]string{
    100: "hash_100",
    101: "hash_101",
    102: "hash_102",  // currentHeight
}

// 新区块到来时检查
func detectReorg(block) bool {
    expectedParent := blockHashes[block.Height - 1]
    return block.ParentHash != expectedParent
}
```

**重组深度与确认数**：

| 链 | 典型重组深度 | 建议确认数 | 说明 |
|------|------------|-----------|------|
| Solana | 极少（PoS+Tower BFT） | 1 (finalized) | Solana 有 finalized 状态保证 |
| Ethereum | 1-2 区块 | 12-15 | PoS 后 finality 约 12 分钟 |
| BSC | 1-3 区块 | 15 | PoSA 共识 |
| Base/Arbitrum | 几乎不重组 | 1 | L2 Sequencer 单点排序 |

### 4.3 回滚策略

```
正常链: ... → Block 100 → Block 101 → Block 102
                                          ↑ currentHeight

重组后: ... → Block 100 → Block 101' → Block 102' → Block 103'
                   ↑ 分叉点

需要：
1. 找到分叉点（Block 100）
2. 撤销 101、102 的所有状态变更
3. 从 100 开始重新同步 101'、102'、103'
```

生产中回滚的复杂性：
- 池子状态已更新 → 需要用旧值覆盖
- 交易记录已确认 → 需要标记为 "reorged"
- 事件已推送给下游（Kafka）→ 需要发送 "撤销事件"
- 余额已刷新 → 需要重新计算

---

## 5. 生产代码中的解析模式

### 5.1 solwallet 的解析架构

```
solwallet 事件解析引擎（74 个文件，解析 70+ DEX）

核心文件:
  parser/engine.go         -- 解析引擎主入口
  parser/registry.go       -- ProgramID 注册表
  parser/raydium/amm.go    -- Raydium AMM v4 解析器
  parser/raydium/clmm.go   -- Raydium CLMM 解析器
  parser/pumpfun/trade.go  -- PumpFun 交易解析器
  parser/orca/whirlpool.go -- Orca Whirlpool 解析器
  parser/meteora/dlmm.go   -- Meteora DLMM 解析器
  ...
```

**解析流程**：

```go
// 1. 获取交易
tx := rpc.GetTransaction(signature)

// 2. 检查交易是否成功
if tx.Meta.Err != nil {
    return // 失败的交易不解析
}

// 3. 遍历所有指令（顶层 + 内部）
allInstructions := flattenInstructions(tx)

// 4. 按 ProgramID 分发到对应解析器
for _, inst := range allInstructions {
    programID := accountKeys[inst.ProgramIdIndex]
    handler := registry.Get(programID)
    if handler != nil {
        events = append(events, handler.Parse(tx, inst))
    }
}

// 5. 用 pre/postTokenBalances 校验金额
validateAmounts(events, tx.Meta.PreTokenBalances, tx.Meta.PostTokenBalances)
```

**flattenInstructions 的实现**：

```go
// 将顶层指令和内部指令展平为统一列表
func flattenInstructions(tx *Transaction) []FlatInstruction {
    var result []FlatInstruction

    for i, inst := range tx.Message.Instructions {
        result = append(result, FlatInstruction{
            Instruction: inst,
            Depth:       1,      // 顶层
            ParentIndex: -1,
        })

        // 找到对应的 innerInstructions
        for _, inner := range tx.Meta.InnerInstructions {
            if inner.Index == i {
                for _, innerInst := range inner.Instructions {
                    result = append(result, FlatInstruction{
                        Instruction: innerInst,
                        Depth:       2,   // CPI 第一层
                        ParentIndex: i,
                    })
                }
            }
        }
    }

    return result
}
```

### 5.2 evmwallet 的解析架构

```
evmwallet 事件解析（23+ 解析器，覆盖 6 条链）

核心文件:
  parser/engine.go              -- 解析引擎主入口
  parser/registry.go            -- Topic 注册表
  parser/uniswap/v2_swap.go     -- UniswapV2 Swap 解析器
  parser/uniswap/v3_swap.go     -- UniswapV3 Swap 解析器
  parser/pancake/v2_swap.go     -- PancakeSwap V2 解析器
  parser/erc20/transfer.go      -- ERC20 Transfer 解析器
  parser/erc20/approval.go      -- ERC20 Approval 解析器
  ...
```

**解析流程**：

```go
// 1. 获取交易回执
receipt := rpc.GetTransactionReceipt(txHash)

// 2. 检查交易是否成功
if receipt.Status != 1 {
    return // 失败的交易不解析
}

// 3. 遍历所有 log
for _, log := range receipt.Logs {
    if len(log.Topics) == 0 {
        continue
    }

    topic0 := log.Topics[0]
    handler := registry.Get(topic0)
    if handler != nil {
        event := handler.Parse(log)
        // 补充 pool 地址（log.Address 通常就是 Pair 合约地址）
        event.Pool = log.Address
        events = append(events, event)
    }
}
```

### 5.3 两种解析方式的对比总结

| 维度 | Solana | EVM |
|------|--------|-----|
| 数据来源 | getTransaction | eth_getTransactionReceipt |
| 匹配依据 | ProgramID（公钥地址） | Topics[0]（keccak256 哈希） |
| 数据编码 | Borsh（小端序） | ABI（大端序） |
| 事件位置 | instructions + innerInstructions | receipt.logs |
| 一笔交易多个事件 | 多条指令（顶层+CPI） | 多个 log entry |
| 金额校验 | preTokenBalances / postTokenBalances | 依赖 Transfer log 交叉验证 |
| 失败交易 | meta.err != nil | receipt.status == 0 |
| 代币地址获取 | mint 字段在 tokenBalances 中 | log.address = token 合约 |
| 池子地址获取 | 从 accounts 按固定位置提取 | log.address = Pair 合约 或从 Factory 查询 |

---

## 6. 金额处理与精度

### 6.1 为什么用 big.Int

链上金额都是最小单位的整数：

| 代币 | 精度 | 1 个单位 | 最小单位名称 |
|------|------|----------|------------|
| SOL | 9 | 10^9 lamports | lamport |
| ETH | 18 | 10^18 wei | wei |
| USDC (Solana) | 6 | 10^6 | — |
| USDT (EVM) | 6 | 10^6 | — |
| WBTC (EVM) | 8 | 10^8 | — |

`uint64` 最大值约 `1.8 * 10^19`，对于 wei 级别的大额转账（比如 100 ETH = `10^20 wei`）会溢出。`math/big.Int` 支持任意精度。

### 6.2 注意事项

```go
// 错误：永远不要用 float64
amount := float64(rawAmount) / math.Pow10(18) // 精度丢失！

// 正确：用 big.Int 做所有计算
amount := new(big.Int).SetBytes(data[0:32])

// 比较金额
if a.Cmp(b) > 0 { ... }  // 不要用 a > b

// 除法：先乘后除，避免精度丢失
// 计算 price = amountOut * 10^18 / amountIn
price := new(big.Int).Mul(amountOut, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
price.Div(price, amountIn)
```

---

## 7. 日志与监控设计

### 7.1 事件解析的日志分级

```go
// Debug -- 正常解析（量大，默认不输出）
slog.Debug("event parsed", "type", event.Type, "tx", event.TxHash)

// Debug -- 未知事件（不是错误，只是未注册的 ProgramID/Topic）
slog.Debug("unknown identifier, skipped", "id", id)

// Warn -- 单笔解析失败（不停止同步）
slog.Warn("parse failed", "tx", txHash, "error", err)

// Warn -- 检测到重组
slog.Warn("reorg detected", "height", height, "depth", depth)

// Error -- 同步停滞（需要人工干预）
slog.Error("syncer stalled", "height", height, "gap_seconds", gap)
```

### 7.2 生产监控指标

| 指标 | 类型 | 含义 |
|------|------|------|
| `syncer_current_height` | Gauge | 当前同步高度 |
| `syncer_latest_height` | Gauge | 链上最新高度 |
| `syncer_lag_blocks` | Gauge | 落后区块数 |
| `syncer_events_total` | Counter | 解析事件总数（按 type 分标签） |
| `syncer_parse_errors_total` | Counter | 解析失败次数 |
| `syncer_reorg_total` | Counter | 重组次数 |
| `syncer_block_process_duration` | Histogram | 区块处理延迟 |

**告警规则**：
- `syncer_lag_blocks > 100` → 同步严重落后
- `syncer_parse_errors_total` 增长率 > 10/min → 解析器异常
- `syncer_reorg_total` 增长率 > 5/hour → 节点质量问题或链不稳定
