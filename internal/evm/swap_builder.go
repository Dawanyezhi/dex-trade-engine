// Package evm 实现 EVM 链特定的接口。
// [链特定层] -- 实现 dexwallet 层定义的 SwapBuilder 接口。
//
// 与 Solana 的核心差异：
//   - EVM 交易调用单个合约，Solana 交易是指令列表
//   - EVM 用 ABI 编码参数，Solana 用 Borsh 编码
//   - EVM 需要 Approve（授权）步骤，Solana 不需要（SPL Token 通过 owner 机制）
//   - EVM 有 Nonce（必须严格递增），Solana 用 recentBlockhash（有效期约 60s）
//   - EVM 有 Gas（EIP-1559: baseFee + priorityFee），Solana 有 ComputeUnit
package evm

import (
	"context"
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ----- 编译期接口检查 -----

var _ dexwallet.SwapBuilder = (*UniswapV2SwapBuilder)(nil)

// ----- EVM 交易数据结构 -----

// ABIEncoder ABI 编码器（简化）。
// 生产中使用 go-ethereum 的 abi 包，这里仅展示结构。
type ABIEncoder struct{}

// EncodeSwap 编码 swapExactTokensForTokens 调用数据。
// 实际生产中根据函数签名（selector = keccak256(sig)[:4]）+ ABI 编码参数。
func (e *ABIEncoder) EncodeSwap(amountIn, amountOutMin *big.Int, path []string, to string, deadline uint64) []byte {
	// 骨架实现：实际应使用 go-ethereum/accounts/abi 包
	// selector: swapExactTokensForTokens(uint256,uint256,address[],address,uint256) = 0x38ed1739
	return []byte{0x38, 0xed, 0x17, 0x39} // 仅返回函数选择器作为示意
}

// EncodeApprove 编码 ERC20 approve 调用数据。
// approve(address spender, uint256 amount)
func (e *ABIEncoder) EncodeApprove(spender string, amount *big.Int) []byte {
	// selector: approve(address,uint256) = 0x095ea7b3
	return []byte{0x09, 0x5e, 0xa7, 0xb3}
}

// TransactionData EVM 交易数据。
// 对比 Solana：Solana 的 Transaction 包含多条 Instruction，每条指令可以调用不同 Program；
// EVM 的 Transaction 只调用一个合约（To），通过 Data 字段传递 ABI 编码的调用数据。
type TransactionData struct {
	To       string   // 目标合约地址
	Value    *big.Int // ETH value（swap 非 ETH 时为 0）
	Data     []byte   // ABI 编码的调用数据
	GasLimit uint64
	Nonce    uint64

	// EIP-1559 Gas 模型（Ethereum / Base / Monad 等支持 EIP-1559 的链）
	// baseFee 由网络决定，priorityFee 由用户设置
	MaxFeePerGas         *big.Int // baseFee + priorityFee 的上限
	MaxPriorityFeePerGas *big.Int // 给矿工/验证者的小费

	// Legacy Gas 模型（BSC 等不支持 EIP-1559 的链）
	GasPrice *big.Int // 传统 Gas 价格（与 EIP-1559 互斥）
}

// ----- UniswapV2SwapBuilder -----

// UniswapV2SwapBuilder 实现 UniswapV2 风格 DEX 的 SwapBuilder。
// 适用于 UniswapV2、PancakeSwapV2 等恒定乘积 AMM。
//
// EVM Swap 的核心流程（与 Solana 对比）：
//
//	步骤          | EVM (UniswapV2)              | Solana (Raydium AMM)
//	-------------|------------------------------|----------------------------
//	1. 授权       | ERC20.approve（单独交易）      | 不需要（owner 机制）
//	2. 编码       | ABI 编码（4字节selector+参数）  | Borsh 序列化指令数据
//	3. Gas/费用   | estimateGas + EIP-1559       | ComputeBudget 指令
//	4. 序列号     | Nonce（严格递增，全局唯一）      | recentBlockhash（约60s有效）
//	5. 序列化     | RLP 编码                      | Borsh 序列化 Transaction
type UniswapV2SwapBuilder struct {
	chainID       coinset.ChainID     // 链标识（ethereum / bsc / base 等）
	chainConfig   *coinset.ChainConfig // 链配置（FeatureGate 判断 EIP-1559 等）
	routerAddress string              // Router 合约地址（UniswapV2Router02）
	encoder       *ABIEncoder         // ABI 编码器
}

// NewUniswapV2SwapBuilder 创建 UniswapV2 SwapBuilder。
func NewUniswapV2SwapBuilder(chainID coinset.ChainID, routerAddress string) (*UniswapV2SwapBuilder, error) {
	chainConfig, err := coinset.Global().Get(chainID)
	if err != nil {
		return nil, fmt.Errorf("获取链配置失败: %w", err)
	}
	if !chainConfig.IsEVM() {
		return nil, fmt.Errorf("链 %s 不是 EVM 类型", chainID)
	}

	return &UniswapV2SwapBuilder{
		chainID:       chainID,
		chainConfig:   chainConfig,
		routerAddress: routerAddress,
		encoder:       &ABIEncoder{},
	}, nil
}

// Build 构建 Swap 交易。
//
// EVM Swap 构建 5 步流程：
//  1. 检查 ERC20 Allowance，如果授权不足则先构建 Approve 交易
//  2. ABI 编码 Swap 调用数据（如 swapExactTokensForTokens）
//  3. 估算 Gas（eth_estimateGas）
//  4. 构建 EIP-1559 交易（baseFee + priorityFee）或 Legacy 交易（gasPrice）
//  5. 序列化交易数据（RLP 编码）
//
// 与 Solana 的关键差异：
//   - Solana 可以把多条指令打包进一个 Transaction（原子执行），
//     所以 approve + swap 可以在同一个 tx 中完成（实际上 Solana 不需要 approve）；
//   - EVM 的 approve 和 swap 是两个独立交易，approve 必须先上链确认后才能 swap。
func (b *UniswapV2SwapBuilder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {

	// ============================================================
	// Step 1: 检查 ERC20 Allowance
	// ============================================================
	// 对比 Solana：Solana 的 SPL Token 通过 owner 机制授权，
	// 只要 Token Account 的 owner 是当前钱包，就可以直接操作，无需 approve。
	//
	// EVM 的 ERC20 标准要求：在 DEX 合约代扣代币前，
	// 用户必须先调用 token.approve(router, amount) 授权。
	needApprove, err := b.checkAllowance(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("检查 allowance 失败: %w", err)
	}
	if needApprove {
		// 生产中：先发送 approve 交易并等待确认，再继续 swap
		// 常见优化：approve MaxUint256 一次性授权，避免每次 swap 都 approve
		_ = b.buildApproveTx(ctx, req)
		// 实际中这里需要等待 approve 交易确认后才能继续
	}

	// ============================================================
	// Step 2: ABI 编码 Swap 调用数据
	// ============================================================
	// 对比 Solana：Solana 用 Borsh 编码每条 Instruction 的 data 字段，
	// 每条 Instruction 还需要指定 ProgramID 和 AccountMeta 列表。
	//
	// EVM 只需编码一次函数调用：
	//   selector(4 bytes) + abi.encode(params)
	//   UniswapV2: swapExactTokensForTokens(amountIn, amountOutMin, path, to, deadline)
	minOutput := b.calculateMinOutput(req.Amount, req.SlippageBps)
	path := []string{req.FromToken.Address, req.ToToken.Address}
	deadline := uint64(0) // 简化：生产中使用 block.timestamp + N
	calldata := b.encoder.EncodeSwap(req.Amount, minOutput, path, req.Sender, deadline)

	// ============================================================
	// Step 3: 估算 Gas（eth_estimateGas）
	// ============================================================
	// 对比 Solana：Solana 通过 ComputeBudget 指令设置 Compute Unit 上限，
	// 默认 200K CU，如果指令复杂（如 CLMM、聚合器多跳）需要手动提高。
	//
	// EVM 通过 eth_estimateGas RPC 模拟执行，返回预估 Gas 用量。
	// 一般会在预估值基础上 * 1.2 留余量（Gas 用不完会退还）。
	gasLimit, err := b.estimateGas(ctx, req.Sender, b.routerAddress, calldata)
	if err != nil {
		return nil, fmt.Errorf("估算 Gas 失败: %w", err)
	}

	// ============================================================
	// Step 4: 构建交易（EIP-1559 或 Legacy）
	// ============================================================
	// 对比 Solana：Solana 没有 Gas 竞价机制（新增的优先费通过 ComputeBudget 指令设置），
	// 而 EVM 的 Gas 模型直接影响交易被打包的优先级。
	//
	// EIP-1559 (Ethereum / Base / Monad):
	//   总费用 = min(maxFeePerGas, baseFee + maxPriorityFeePerGas) * gasUsed
	//   baseFee: 每个区块根据利用率动态调整（燃烧）
	//   priorityFee: 给验证者的小费（决定排序优先级）
	//
	// Legacy (BSC):
	//   总费用 = gasPrice * gasUsed
	txData, err := b.buildTransaction(ctx, req.Sender, calldata, gasLimit)
	if err != nil {
		return nil, fmt.Errorf("构建交易失败: %w", err)
	}

	// ============================================================
	// Step 5: 序列化交易（RLP 编码）
	// ============================================================
	// 对比 Solana：Solana 使用 Borsh 序列化整个 Transaction（含 Message + Signatures）。
	// EVM 使用 RLP（Recursive Length Prefix）编码。
	// EIP-1559 交易类型前缀为 0x02。
	serialized := b.serializeTransaction(txData)

	return &dexwallet.SwapResult{
		DexID:        dexwallet.DexUniswapV2,
		ChainID:      b.chainID,
		Direction:    req.Direction,
		InputAmount:  req.Amount,
		OutputAmount: minOutput, // 简化：实际应从 Quote 获取精确输出
		MinOutput:    minOutput,
		SlippageBps:  req.SlippageBps,
		GasCost:      new(big.Int).SetUint64(gasLimit),
		TxData:       serialized,
		Extra: map[string]interface{}{
			"router":   b.routerAddress,
			"gasLimit": gasLimit,
			"nonce":    txData.Nonce,
		},
	}, nil
}

// DexID 返回支持的 DEX 标识。
func (b *UniswapV2SwapBuilder) DexID() dexwallet.DexID {
	return dexwallet.DexUniswapV2
}

// ChainID 返回支持的链标识。
func (b *UniswapV2SwapBuilder) ChainID() coinset.ChainID {
	return b.chainID
}

// ProtocolType 返回协议类型。
func (b *UniswapV2SwapBuilder) ProtocolType() dexwallet.ProtocolType {
	return dexwallet.ProtocolAMM
}

// Simulate 模拟执行交易，验证交易是否会成功。
//
// EVM 模拟使用两个 RPC 调用：
//   - eth_call: 在最新区块状态上执行调用，检查合约是否 revert
//   - eth_estimateGas: 估算 Gas 消耗（如果调用会 revert，estimateGas 也会失败）
//
// 对比 Solana：
//   - Solana 使用 simulateTransaction RPC，传入完整的序列化交易，
//     返回模拟执行结果（包括日志、CU 消耗、错误信息）。
//   - Solana 的模拟可以一次性验证所有指令（包括 CPI 调用），
//     EVM 的 eth_call 只能模拟单个合约调用。
//
// 常见 revert 原因：
//   - INSUFFICIENT_OUTPUT_AMOUNT: 滑点超出（输出低于 amountOutMin）
//   - EXPIRED: deadline 已过
//   - TRANSFER_FROM_FAILED: allowance 不足或余额不足
//   - INSUFFICIENT_LIQUIDITY: 池子流动性不足
func (b *UniswapV2SwapBuilder) Simulate(ctx context.Context, txData []byte) error {
	// 骨架实现：实际调用 eth_call + eth_estimateGas

	// Step 1: eth_call — 检查合约调用是否 revert
	// 如果 revert，返回 revert reason（如 "UniswapV2Router: INSUFFICIENT_OUTPUT_AMOUNT"）
	if err := b.ethCall(ctx, txData); err != nil {
		return fmt.Errorf("eth_call 模拟失败（合约 revert）: %w", err)
	}

	// Step 2: eth_estimateGas — 确认 Gas 预估正常
	// 如果 Step 1 通过但 estimateGas 失败，可能是状态变化（MEV、抢跑等）
	if _, err := b.ethEstimateGas(ctx, txData); err != nil {
		return fmt.Errorf("eth_estimateGas 失败: %w", err)
	}

	return nil
}

// Label 返回人类可读标签（用于日志和监控）。
func (b *UniswapV2SwapBuilder) Label() string {
	return fmt.Sprintf("UniswapV2[%s]", b.chainID)
}

// ----- 私有方法（骨架实现） -----

// checkAllowance 检查 ERC20 授权额度。
// 调用 token.allowance(owner, spender) 查询当前授权额度。
//
// 对比 Solana：Solana 完全不需要这一步。
// SPL Token 的 owner 字段直接决定谁有权操作该 Token Account，
// DEX Program 通过 CPI（Cross-Program Invocation）直接转移代币。
func (b *UniswapV2SwapBuilder) checkAllowance(_ context.Context, _ dexwallet.SwapRequest) (bool, error) {
	// 骨架：实际调用 ERC20.allowance(sender, routerAddress)
	// 如果 allowance < amount，返回 true（需要 approve）
	return false, nil
}

// buildApproveTx 构建 ERC20 Approve 交易。
// 授权 Router 合约代扣指定数量的代币。
//
// 常见策略：
//   - 精确授权：approve(router, exactAmount) — 更安全但每次 swap 都需要
//   - 最大授权：approve(router, MaxUint256) — 只需一次，但有安全风险
//
// 生产中一般使用最大授权 + 定期检查已知合约白名单。
func (b *UniswapV2SwapBuilder) buildApproveTx(_ context.Context, req dexwallet.SwapRequest) *TransactionData {
	// MaxUint256 = 2^256 - 1
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	calldata := b.encoder.EncodeApprove(b.routerAddress, maxUint256)

	return &TransactionData{
		To:    req.FromToken.Address, // approve 调用的是 Token 合约，不是 Router
		Value: big.NewInt(0),         // approve 不需要发送 ETH
		Data:  calldata,
	}
}

// calculateMinOutput 根据滑点计算最小输出。
func (b *UniswapV2SwapBuilder) calculateMinOutput(amount *big.Int, slippageBps uint64) *big.Int {
	// minOutput = amount * (10000 - slippageBps) / 10000
	// 简化实现：实际应使用 Quote 获取的精确输出金额作为基数
	factor := new(big.Int).SetUint64(10000 - slippageBps)
	result := new(big.Int).Mul(amount, factor)
	return result.Div(result, big.NewInt(10000))
}

// estimateGas 估算 Gas。
// 调用 eth_estimateGas RPC。
func (b *UniswapV2SwapBuilder) estimateGas(_ context.Context, _, _ string, _ []byte) (uint64, error) {
	// 骨架：实际调用 eth_estimateGas RPC
	// UniswapV2 swap 通常消耗 150K - 300K Gas
	baseGas := uint64(200000)
	// 安全余量 * 1.2
	return baseGas * 120 / 100, nil
}

// buildTransaction 构建 EVM 交易。
// 根据链配置选择 EIP-1559 或 Legacy Gas 模型。
//
// Nonce 管理（EVM 独有的复杂性）：
//   - Nonce 必须严格递增，每个地址独立计数
//   - 并发发送多笔交易时，必须自行管理 Nonce（不能依赖 eth_getTransactionCount）
//   - Nonce 冲突会导致交易替换（RBF）或失败
//   - 生产中使用本地 Nonce 缓存 + 定期与链上同步
//
// 对比 Solana：
//   - Solana 不需要 Nonce，使用 recentBlockhash 作为交易的"有效期"标记
//   - recentBlockhash 约 60s 过期，过期后交易自动失效
//   - 这意味着 Solana 可以安全地并发构建多笔交易，不会冲突
func (b *UniswapV2SwapBuilder) buildTransaction(_ context.Context, sender string, calldata []byte, gasLimit uint64) (*TransactionData, error) {
	tx := &TransactionData{
		To:       b.routerAddress,
		Value:    big.NewInt(0), // 非 ETH swap 时 value = 0
		Data:     calldata,
		GasLimit: gasLimit,
	}

	// 获取 Nonce（骨架：实际调用 eth_getTransactionCount 或本地缓存）
	nonce, err := b.getNonce(sender)
	if err != nil {
		return nil, fmt.Errorf("获取 nonce 失败: %w", err)
	}
	tx.Nonce = nonce

	// 根据链 FeatureGate 选择 Gas 模型
	if b.chainConfig.HasFeature(coinset.FeatureEIP1559) {
		// EIP-1559: baseFee + priorityFee
		// baseFee 从 eth_getBlockByNumber("latest") 获取
		// priorityFee 从 eth_maxPriorityFeePerGas 获取
		baseFee := big.NewInt(30_000_000_000)    // 30 Gwei（骨架值）
		priorityFee := big.NewInt(2_000_000_000) // 2 Gwei（骨架值）

		// maxFeePerGas = baseFee * 2 + priorityFee（留余量应对 baseFee 波动）
		tx.MaxFeePerGas = new(big.Int).Add(
			new(big.Int).Mul(baseFee, big.NewInt(2)),
			priorityFee,
		)
		tx.MaxPriorityFeePerGas = priorityFee
	} else {
		// Legacy: gasPrice（BSC 等链）
		// 从 eth_gasPrice 获取
		tx.GasPrice = big.NewInt(5_000_000_000) // 5 Gwei（骨架值）
	}

	return tx, nil
}

// serializeTransaction 序列化交易（RLP 编码）。
// EIP-1559 交易格式: 0x02 || RLP([chainId, nonce, maxPriorityFeePerGas, maxFeePerGas, gasLimit, to, value, data, accessList])
// Legacy 交易格式: RLP([nonce, gasPrice, gasLimit, to, value, data, v, r, s])
//
// 对比 Solana：Solana 使用 Borsh 序列化 Transaction 结构体，
// 格式为: [signatures_count, signatures, message(header, accounts, recentBlockhash, instructions)]
func (b *UniswapV2SwapBuilder) serializeTransaction(_ *TransactionData) []byte {
	// 骨架：实际使用 go-ethereum 的 types.Transaction.MarshalBinary()
	return []byte("mock-rlp-encoded-transaction")
}

// getNonce 获取当前 Nonce。
// 生产中的 Nonce 管理策略：
//  1. 启动时从链上 eth_getTransactionCount(address, "pending") 获取初始值
//  2. 每次发送交易后本地 +1
//  3. 如果交易失败（非 nonce 原因），不回退 nonce
//  4. 定期与链上同步，修正漂移
//  5. 并发发送时使用互斥锁保护 nonce 分配
func (b *UniswapV2SwapBuilder) getNonce(_ string) (uint64, error) {
	// 骨架：实际调用 eth_getTransactionCount 或从本地缓存获取
	return 0, nil
}

// ethCall 调用 eth_call 模拟执行。
func (b *UniswapV2SwapBuilder) ethCall(_ context.Context, _ []byte) error {
	// 骨架：实际调用 eth_call RPC
	// 如果合约 revert，解析 revert reason 并返回
	return nil
}

// ethEstimateGas 调用 eth_estimateGas 估算 Gas。
func (b *UniswapV2SwapBuilder) ethEstimateGas(_ context.Context, _ []byte) (uint64, error) {
	// 骨架：实际调用 eth_estimateGas RPC
	return 240000, nil
}
