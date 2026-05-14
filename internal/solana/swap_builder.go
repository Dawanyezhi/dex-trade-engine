// Package solana 实现 Solana 链特定的接口。
// [链特定层] -- 实现 dexwallet 层定义的 SwapBuilder 接口。
//
// 与 EVM 的核心差异：
//   - Solana 交易是"指令列表"，EVM 交易是"单个合约调用"
//   - Solana 用 Borsh 编码指令数据，EVM 用 ABI 编码
//   - Solana 需要预声明所有账户（AccountKeys），EVM 不需要
//   - Solana 有 ComputeBudget（计算预算），EVM 有 Gas
//   - Solana 有 ALT（地址查找表）减少交易大小，EVM 无此概念
package solana

import (
	"context"
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ----- 编译期接口检查 -----

var _ dexwallet.SwapBuilder = (*RaydiumSwapBuilder)(nil)

// ----- Solana 指令与交易结构 -----

// Instruction Solana 指令（简化）。
//
// Solana 交易由多条指令组成，每条指令包含：
//   - ProgramID: 要调用的程序地址（类比 EVM 的合约地址）
//   - AccountKeys: 指令涉及的所有账户列表（必须预声明，EVM 无此要求）
//   - Data: Borsh 编码的指令数据（类比 EVM 的 ABI 编码 calldata）
type Instruction struct {
	ProgramID   string   // 程序地址（如 RaydiumAMM ProgramID）
	AccountKeys []string // 涉及的账户列表（签名者、可写账户、只读账户）
	Data        []byte   // Borsh 编码的指令数据
}

// TransactionBuilder Solana 交易构建器。
//
// 与 EVM 交易的关键区别：
//   - EVM 交易 = 单个合约调用（to + calldata）
//   - Solana 交易 = 指令列表（多个程序调用打包在一笔交易中）
//   - Solana 需要 recentBlockhash 防止重放，EVM 用 nonce
//   - Solana 使用 ALT 压缩地址，EVM 无此需求
type TransactionBuilder struct {
	instructions    []Instruction // 指令列表（按顺序执行）
	signers         []string      // 签名者列表
	recentBlockhash string        // 最近的区块哈希（防重放，类比 EVM 的 nonce）
	altAddresses    []string      // 地址查找表地址（减少交易体积）
}

// NewTransactionBuilder 创建 Solana 交易构建器。
func NewTransactionBuilder() *TransactionBuilder {
	return &TransactionBuilder{
		instructions: make([]Instruction, 0, 4),
		signers:      make([]string, 0, 1),
		altAddresses: make([]string, 0),
	}
}

// AddInstruction 添加一条指令。
func (tb *TransactionBuilder) AddInstruction(ix Instruction) {
	tb.instructions = append(tb.instructions, ix)
}

// SetRecentBlockhash 设置最近区块哈希。
func (tb *TransactionBuilder) SetRecentBlockhash(hash string) {
	tb.recentBlockhash = hash
}

// AddSigner 添加签名者。
func (tb *TransactionBuilder) AddSigner(signer string) {
	tb.signers = append(tb.signers, signer)
}

// AddALT 添加地址查找表。
func (tb *TransactionBuilder) AddALT(altAddress string) {
	tb.altAddresses = append(tb.altAddresses, altAddress)
}

// InstructionCount 返回当前指令数量。
func (tb *TransactionBuilder) InstructionCount() int {
	return len(tb.instructions)
}

// AllAccountKeys 收集所有指令中涉及的账户地址（去重）。
func (tb *TransactionBuilder) AllAccountKeys() []string {
	seen := make(map[string]bool)
	var keys []string
	for _, ix := range tb.instructions {
		for _, key := range ix.AccountKeys {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// Serialize 序列化交易（模拟）。
// 生产中使用 Solana SDK 的 Transaction.Serialize()。
func (tb *TransactionBuilder) Serialize() ([]byte, error) {
	if tb.recentBlockhash == "" {
		return nil, fmt.Errorf("recentBlockhash is required")
	}
	if len(tb.instructions) == 0 {
		return nil, fmt.Errorf("transaction must contain at least one instruction")
	}

	// 模拟序列化：在生产中这里会执行真正的 Borsh 序列化
	data := fmt.Sprintf(
		"solana_tx{blockhash=%s,instructions=%d,signers=%d,accounts=%d,alt=%d}",
		tb.recentBlockhash,
		len(tb.instructions),
		len(tb.signers),
		len(tb.AllAccountKeys()),
		len(tb.altAddresses),
	)
	return []byte(data), nil
}

// ----- Raydium ComputeBudget 参数 -----

// ComputeBudgetParams ComputeBudget 指令参数。
//
// Solana 独有概念，类比 EVM 的 Gas：
//   - ComputeUnitLimit: 类比 EVM gasLimit（计算单元上限）
//   - ComputeUnitPrice: 类比 EVM gasPrice / priorityFee（每计算单元的价格，单位 microLamport）
//   - 优先费 = ComputeUnitLimit * ComputeUnitPrice / 1_000_000（lamports）
type ComputeBudgetParams struct {
	ComputeUnitLimit uint32 // 计算单元上限（默认 200_000，最大 1_400_000）
	ComputeUnitPrice uint64 // 每计算单元价格（microLamport）
}

// ----- RaydiumSwapBuilder 实现 -----

// RaydiumSwapBuilder 实现 Raydium AMM 的 Swap 交易构建。
// [链特定层 - Solana] 实现 dexwallet.SwapBuilder 接口。
//
// 核心流程展示了 Solana 交易构建与 EVM 的结构性差异：
//
//	EVM:  Approve → 编码 ABI calldata → 估算 Gas → 发送单笔交易
//	Solana: 构建 ComputeBudget 指令 → 构建 Swap 指令 → 组装指令列表 → ALT 压缩 → 序列化
//
// Solana 交易由多条指令组成，每条指令显式声明涉及的账户和程序。
// 这种"指令列表"模型使得一笔交易可以原子性地执行多个程序调用。
type RaydiumSwapBuilder struct {
	// Raydium AMM 程序地址
	programID string

	// 模拟的池子数据（生产中从链上读取）
	reserveIn  *big.Int // 输入代币储备
	reserveOut *big.Int // 输出代币储备
	feeRate    uint64   // 手续费率（基点，如 25 = 0.25%）

	// ComputeBudget 参数
	computeBudget ComputeBudgetParams

	// ALT 地址（生产中从配置或链上获取）
	altAddress string
}

// NewRaydiumSwapBuilder 创建 Raydium AMM SwapBuilder。
func NewRaydiumSwapBuilder() *RaydiumSwapBuilder {
	return &RaydiumSwapBuilder{
		programID:  "675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8", // Raydium AMM V4 Program
		reserveIn:  new(big.Int).Mul(big.NewInt(1000), big.NewInt(1e9)),    // 1000 SOL
		reserveOut: new(big.Int).Mul(big.NewInt(10000000), big.NewInt(1e6)), // 10_000_000 Token (6 decimals)
		feeRate:    25,                                                      // 0.25%
		computeBudget: ComputeBudgetParams{
			ComputeUnitLimit: 200_000,  // 200K CU
			ComputeUnitPrice: 50_000,   // 50_000 microLamport/CU → 优先费 = 200K * 50K / 1M = 10_000 lamports
		},
		altAddress: "ALTxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", // 模拟 ALT 地址
	}
}

// ----- SwapBuilder 接口实现 -----

// DexID 返回 Raydium AMM 的 DEX 标识。
func (b *RaydiumSwapBuilder) DexID() dexwallet.DexID {
	return dexwallet.DexRaydiumAMM
}

// ChainID 返回 Solana 链标识。
func (b *RaydiumSwapBuilder) ChainID() coinset.ChainID {
	return coinset.ChainSolana
}

// ProtocolType 返回 AMM 协议类型。
func (b *RaydiumSwapBuilder) ProtocolType() dexwallet.ProtocolType {
	return dexwallet.ProtocolAMM
}

// Label 返回此构建器的可读标签（用于日志和监控）。
func (b *RaydiumSwapBuilder) Label() string {
	return "raydium_amm_v4_solana"
}

// Simulate 模拟交易执行，验证交易是否会成功（不实际上链）。
//
// Solana 模拟调用 RPC 的 simulateTransaction 方法：
//   - 输入：序列化后的交易（Base64 编码），即 txData 参数
//   - 输出：模拟结果（消耗的 CU、日志、错误信息）
//
// 与 EVM 的差异：
//   - EVM 用 eth_call / eth_estimateGas 模拟，返回 returndata + gasUsed
//   - Solana 用 simulateTransaction 模拟，返回 logs + unitsConsumed + 错误
//   - Solana 模拟必须提供完整序列化交易（含 recentBlockhash），EVM 只需 from/to/data
func (b *RaydiumSwapBuilder) Simulate(ctx context.Context, txData []byte) error {
	if len(txData) == 0 {
		return fmt.Errorf("txData is empty, cannot simulate")
	}

	// 调用 simulateTransaction RPC（模拟）
	//
	// 生产中的调用方式：
	//   resp, err := rpcClient.Call(ctx, "simulateTransaction", base64.StdEncoding.EncodeToString(txData), map[string]interface{}{
	//       "encoding":               "base64",
	//       "replaceRecentBlockhash":  true,      // 自动替换为最新 blockhash
	//       "sigVerify":               false,      // 跳过签名验证（模拟不需要真签名）
	//   })
	//
	// 返回结果包含：
	//   - err: 如果模拟失败，返回错误类型和消息（如 InsufficientFunds、AccountNotFound 等）
	//   - logs: 程序日志（用于调试和解析事件）
	//   - unitsConsumed: 实际消耗的计算单元（可用于优化 ComputeUnitLimit）
	//   - returnData: 程序返回数据（如果有）
	//
	// 模拟失败的常见原因：
	//   - 余额不足（InsufficientFunds）
	//   - 账户不存在（AccountNotFound）—— 如用户没有目标代币的 ATA
	//   - 滑点超限（自定义 Program Error）
	//   - CU 超限（ComputationalBudgetExceeded）

	// 骨架实现：模拟成功，返回 nil
	// 生产中若 resp.Value.Err != nil，则返回 fmt.Errorf("simulation failed: %v", resp.Value.Err)
	_ = ctx
	return nil
}

// Build 构建 Raydium AMM Swap 交易。
//
// 展示 Solana 交易构建的 5 步流程（与 EVM 的结构性差异在每步注释中标注）：
//
//	步骤 1: 设置 ComputeBudget（Solana 独有，EVM 用 gasLimit + gasPrice）
//	步骤 2: 构建 DEX 特定的 Swap 指令（按固定账户布局填充 AccountKeys）
//	步骤 3: 如果账户数 > 30，使用 ALT 压缩（Solana 独有，EVM 无此概念）
//	步骤 4: 组装交易（所有指令 + recentBlockhash）
//	步骤 5: 序列化交易数据
func (b *RaydiumSwapBuilder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	// ========== 参数验证 ==========
	if req.Amount == nil || req.Amount.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be positive, got %v", req.Amount)
	}
	if req.Sender == "" {
		return nil, fmt.Errorf("sender address is required")
	}

	// ========== 步骤 1: 设置 ComputeBudget ==========
	//
	// Solana 独有概念。EVM 中对应的是 gasLimit + gasPrice/priorityFee。
	// ComputeBudget 程序提供两条指令：
	//   - SetComputeUnitLimit: 设置本交易的 CU 上限
	//   - SetComputeUnitPrice: 设置每 CU 的价格（影响交易优先级）
	//
	// ComputeBudget ProgramID: ComputeBudget111111111111111111111111111111

	txBuilder := NewTransactionBuilder()

	// 指令 1: SetComputeUnitLimit
	// Borsh 编码: [instruction_type(u8=2), compute_units(u32)]
	cuLimitIx := Instruction{
		ProgramID:   "ComputeBudget111111111111111111111111111111",
		AccountKeys: []string{}, // ComputeBudget 指令不需要额外账户
		Data:        encodeSetComputeUnitLimit(b.computeBudget.ComputeUnitLimit),
	}
	txBuilder.AddInstruction(cuLimitIx)

	// 指令 2: SetComputeUnitPrice
	// Borsh 编码: [instruction_type(u8=3), micro_lamports(u64)]
	cuPriceIx := Instruction{
		ProgramID:   "ComputeBudget111111111111111111111111111111",
		AccountKeys: []string{}, // ComputeBudget 指令不需要额外账户
		Data:        encodeSetComputeUnitPrice(b.computeBudget.ComputeUnitPrice),
	}
	txBuilder.AddInstruction(cuPriceIx)

	// ========== 步骤 2: 构建 Swap 指令 ==========
	//
	// 与 EVM 的核心差异：
	//   EVM:    编码函数选择器 + ABI 参数，合约内部自行读取状态
	//   Solana: 必须在指令中显式列出所有涉及的账户（AccountKeys），
	//           运行时会验证账户权限（签名者/可写/只读）
	//
	// Raydium AMM V4 的 Swap 指令 AccountKeys 布局（共 18 个账户）：
	//   [0]  Token Program
	//   [1]  AMM ID（池子地址）
	//   [2]  AMM Authority（PDA）
	//   [3]  AMM Open Orders
	//   [4]  AMM Target Orders
	//   [5]  Pool Coin Token Account（基础代币金库）
	//   [6]  Pool PC Token Account（计价代币金库）
	//   [7]  Serum Program ID
	//   [8]  Serum Market
	//   [9]  Serum Bids
	//   [10] Serum Asks
	//   [11] Serum Event Queue
	//   [12] Serum Coin Vault
	//   [13] Serum PC Vault
	//   [14] Serum Vault Signer
	//   [15] User Source Token Account
	//   [16] User Destination Token Account
	//   [17] User Owner（签名者）

	// 模拟填充 AccountKeys（生产中从池子数据和用户钱包地址获取）
	swapAccountKeys := []string{
		"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",  // [0] Token Program
		"58oQChx4yWmvKdwLLZzBi4ChoCc2fqCUWBkwMihLYQo2", // [1] AMM ID（模拟）
		"5Q544fKrFoe6tsEbD7S8EmxGTJYAKtTVhAW5Q5pge4j1", // [2] AMM Authority
		"HRk9CMrpq7Fo9CSbimwJ7ux3ZYip2MJq1K2gDMxNnWvq", // [3] AMM Open Orders
		"CZza3Ej4Mc58MnxWA385itCC9jCo3L1D7zc3LKy1bZMR", // [4] AMM Target Orders
		"DQyrAcCrDXQ7NeoqGgDCZwBvWDcYmFCjSb9JtteuvPuz", // [5] Pool Coin Token
		"HLmqeL62xR1QoZ1HKKbXRrdN1p3phKpxRMb2VVopvBBz", // [6] Pool PC Token
		"9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin", // [7] Serum Program
		"7GZfXSD95BEdNjyVWKMaC7YhePq7gkTCjVD2MHpouuRn", // [8] Serum Market（模拟）
		"Hf4jABBiCKxFRgJzMdKcSKSGoQaJGXCPjxC4yb4p1s4s", // [9] Serum Bids（模拟）
		"J2DzXM6VGoxXiRteGb3LLf49RRjMMW4yBh9MTQGGM2Kp", // [10] Serum Asks（模拟）
		"4QJwErNbi9NQoBn89sMjj1NL2e2rxW2Dz6E3vPy1gVAH", // [11] Serum Event Queue（模拟）
		"GGcdp4x2gXQLLxvrwMqHkGPr1BUCjr7r3gYj34AU7JUF", // [12] Serum Coin Vault（模拟）
		"22jHt5WmosAykp3LPGSAKgY45p7VGh4DFg37V7gmCjuH", // [13] Serum PC Vault（模拟）
		"B3PDzBYDKqmik9fVb6y3qEzZmSAt1q2QqXPUqcNDWbrv", // [14] Serum Vault Signer（模拟）
		req.FromToken.Address,                             // [15] User Source Token Account
		req.ToToken.Address,                               // [16] User Destination Token Account
		req.Sender,                                        // [17] User Owner（签名者）
	}

	// 计算输出金额（恒定乘积公式 x*y=k）
	outputAmount := calcAMMOutput(req.Amount, b.reserveIn, b.reserveOut, b.feeRate)
	if outputAmount.Sign() <= 0 {
		return nil, fmt.Errorf("calculated output amount is zero or negative")
	}

	// 计算最小输出（考虑滑点）
	minOutput := calcMinOutput(outputAmount, req.SlippageBps)

	// 构建 Swap 指令数据（Borsh 编码）
	// Raydium AMM Swap 指令数据: [instruction_type(u8=9), amount_in(u64), min_amount_out(u64)]
	swapIxData := encodeRaydiumSwap(req.Amount, minOutput)

	swapIx := Instruction{
		ProgramID:   b.programID,
		AccountKeys: swapAccountKeys,
		Data:        swapIxData,
	}
	txBuilder.AddInstruction(swapIx)

	// ========== 步骤 3: ALT 压缩判断 ==========
	//
	// Solana 独有概念。EVM 无此需求。
	//
	// Solana 交易大小上限 1232 字节。每个账户地址 32 字节。
	// 当账户数多时（如 Raydium 的 18 个 + ComputeBudget 的 0 个），交易可能接近上限。
	// ALT 将多个 32 字节地址压缩为 1 字节索引，大幅缩小交易体积。
	//
	// 判断逻辑：账户数 > 30 时启用 ALT（阈值可配置）

	allAccounts := txBuilder.AllAccountKeys()
	useALT := len(allAccounts) > 30

	if useALT {
		txBuilder.AddALT(b.altAddress)
	}

	// ========== 步骤 4: 组装交易 ==========
	//
	// 设置 recentBlockhash（Solana 用此防重放，类比 EVM 的 nonce）。
	// 生产中通过 getLatestBlockhash RPC 获取。
	txBuilder.SetRecentBlockhash("SimulatedBlockhash1111111111111111111111111")
	txBuilder.AddSigner(req.Sender)

	// ========== 步骤 5: 序列化交易 ==========
	txData, err := txBuilder.Serialize()
	if err != nil {
		return nil, fmt.Errorf("serialize transaction: %w", err)
	}

	// 计算优先费: CU_limit * CU_price / 1_000_000 (lamports)
	priorityFee := new(big.Int).SetUint64(
		uint64(b.computeBudget.ComputeUnitLimit) * b.computeBudget.ComputeUnitPrice / 1_000_000,
	)

	return &dexwallet.SwapResult{
		DexID:        dexwallet.DexRaydiumAMM,
		ChainID:      coinset.ChainSolana,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: outputAmount,
		MinOutput:    minOutput,
		SlippageBps:  req.SlippageBps,
		PriorityFee:  priorityFee,
		GasCost:      big.NewInt(5000), // Solana 固定基础费 5000 lamports
		TxData:       txData,
		Extra: map[string]interface{}{
			"program_id":         b.programID,
			"instructions_count": txBuilder.InstructionCount(),
			"account_keys_count": len(allAccounts),
			"compute_unit_limit": b.computeBudget.ComputeUnitLimit,
			"compute_unit_price": b.computeBudget.ComputeUnitPrice,
			"alt_used":           useALT,
			"alt_address":        b.altAddress,
		},
	}, nil
}

// ----- Borsh 编码辅助函数（模拟） -----

// encodeSetComputeUnitLimit 编码 SetComputeUnitLimit 指令数据。
// 真实 Borsh 编码: [u8(2)] + [u32(compute_units)] = 5 字节。
func encodeSetComputeUnitLimit(units uint32) []byte {
	// 模拟 Borsh 编码
	// 生产中: buf := make([]byte, 5); buf[0] = 2; binary.LittleEndian.PutUint32(buf[1:], units)
	return []byte(fmt.Sprintf("borsh{set_cu_limit:%d}", units))
}

// encodeSetComputeUnitPrice 编码 SetComputeUnitPrice 指令数据。
// 真实 Borsh 编码: [u8(3)] + [u64(micro_lamports)] = 9 字节。
func encodeSetComputeUnitPrice(microLamports uint64) []byte {
	// 模拟 Borsh 编码
	// 生产中: buf := make([]byte, 9); buf[0] = 3; binary.LittleEndian.PutUint64(buf[1:], microLamports)
	return []byte(fmt.Sprintf("borsh{set_cu_price:%d}", microLamports))
}

// encodeRaydiumSwap 编码 Raydium AMM Swap 指令数据。
// 真实 Borsh 编码: [u8(9)] + [u64(amount_in)] + [u64(min_amount_out)] = 17 字节。
func encodeRaydiumSwap(amountIn, minAmountOut *big.Int) []byte {
	// 模拟 Borsh 编码
	// 生产中:
	//   buf := make([]byte, 17)
	//   buf[0] = 9 // Raydium AMM swap instruction discriminator
	//   binary.LittleEndian.PutUint64(buf[1:9], amountIn.Uint64())
	//   binary.LittleEndian.PutUint64(buf[9:17], minAmountOut.Uint64())
	return []byte(fmt.Sprintf("borsh{raydium_swap:amount_in=%s,min_out=%s}", amountIn.String(), minAmountOut.String()))
}

// ----- 价格计算辅助函数 -----

// calcAMMOutput 用恒定乘积公式计算 AMM 输出金额。
// dy = reserveOut * effectiveInput / (reserveIn + effectiveInput)
// 其中 effectiveInput = amountIn * (10000 - feeRate) / 10000
func calcAMMOutput(amountIn, reserveIn, reserveOut *big.Int, feeRate uint64) *big.Int {
	// effectiveInput = amountIn * (10000 - feeRate) / 10000
	feeMultiplier := big.NewInt(int64(10000 - feeRate))
	effectiveInput := new(big.Int).Mul(amountIn, feeMultiplier)
	effectiveInput.Div(effectiveInput, big.NewInt(10000))

	// numerator = reserveOut * effectiveInput
	numerator := new(big.Int).Mul(reserveOut, effectiveInput)

	// denominator = reserveIn + effectiveInput
	denominator := new(big.Int).Add(reserveIn, effectiveInput)

	// amountOut = numerator / denominator
	return new(big.Int).Div(numerator, denominator)
}

// calcMinOutput 根据滑点容忍计算最小输出金额。
// minOutput = outputAmount * (10000 - slippageBps) / 10000
func calcMinOutput(outputAmount *big.Int, slippageBps uint64) *big.Int {
	if slippageBps == 0 {
		return new(big.Int).Set(outputAmount)
	}
	multiplier := big.NewInt(int64(10000 - slippageBps))
	result := new(big.Int).Mul(outputAmount, multiplier)
	result.Div(result, big.NewInt(10000))
	return result
}
