package main

// ============================================================
// EVM ERC20 Approve 机制 — 授权检查与构建
// ============================================================
//
// 【为什么 EVM 需要 Approve】
//
// ERC20 标准定义了两种转账方式：
//   - transfer(to, amount)      — 用户直接转账，msg.sender 就是代币持有者
//   - transferFrom(from, to, amount) — 第三方代为转账，需要预先获得授权
//
// DEX Router 合约（如 Uniswap Router）在执行 swap 时，需要从用户地址拉取代币，
// 因此必须调用 transferFrom。而 transferFrom 的前提是用户先调用 approve(spender, amount)，
// 将 Router 合约地址设为 spender，授权其操作指定数量的代币。
//
// 完整流程：
//   1. 用户调用 ERC20.approve(routerAddress, amount)，授权 Router 可以操作 amount 数量的代币
//   2. Router 合约内部调用 ERC20.transferFrom(user, pool, amount)，将代币从用户转入池子
//   3. 池子计算输出，将目标代币转给用户
//
// 【Approve 的安全陷阱】
//
// 1. USDT 的 approve(0) 问题：
//    旧版 USDT（Ethereum 主网上的 Tether）的 approve 实现不符合标准 ERC20 规范。
//    标准 ERC20 的 approve 可以直接设置新值覆盖旧值，但旧版 USDT 要求：
//    如果当前 allowance 不为 0，必须先 approve(spender, 0) 将授权清零，
//    然后再 approve(spender, newAmount) 设置新值。否则交易会 revert。
//    这意味着对 USDT 的授权可能需要两笔交易，增加了 Gas 成本和时间。
//
//    代码层面的原因：旧版 USDT 的 approve 函数包含如下检查：
//      require(!((_value != 0) && (allowed[msg.sender][_spender] != 0)));
//    即：新值和旧值不能同时非零。
//
// 2. 无限授权（MaxUint256）vs 精确授权的安全取舍：
//    - 无限授权（approve MaxUint256）：只需授权一次，后续所有交易无需再 approve，
//      节省 Gas。但如果 Router 合约有漏洞或被攻击，攻击者可以转走用户所有代币。
//      适用场景：信任度高的合约（如 Uniswap 官方 Router）、频繁交易的账户。
//    - 精确授权（approve 精确的 amountIn）：每次交易只授权需要的金额，
//      安全性更高，但每次交易前都需要发一笔 approve 交易，Gas 开销翻倍。
//      适用场景：安全要求高的账户（如交易所热钱包）、不频繁交易的场景。
//    生产中的策略：Bitrue 等交易所账户使用精确授权（安全优先），
//    外部用户账户使用无限授权（用户体验优先）。
//
// 3. 授权竞态（Approve 和 TransferFrom 之间的时间窗口）：
//    在 approve 交易上链确认和 swap 交易发送之间存在时间窗口。
//    在这个窗口内，如果有恶意交易（如 MEV 机器人）监听到 approve 事件，
//    理论上可以在用户 swap 之前调用 transferFrom 转走代币（前提是 spender 合约有漏洞）。
//    此外，ERC20 的 approve 还有一个经典的 "前端运行攻击"（front-running attack）：
//      - 用户先 approve(spender, 100)
//      - 用户想改为 approve(spender, 200)
//      - 攻击者在新 approve 上链前用旧授权 transferFrom 100
//      - 新 approve(200) 上链后，攻击者再 transferFrom 200
//      - 攻击者总共转走 300，而用户只想授权 200
//    防御方式：先 approve(0)，再 approve(newAmount)（increaseAllowance/decreaseAllowance 也可以）。
//
// 【与 Solana 的对比】
//
// Solana 使用完全不同的 Token Account 模型，不需要 Approve 机制：
//   - Solana 的 SPL Token 使用关联代币账户（Associated Token Account, ATA）
//   - 每个代币在用户名下有独立的 ATA，由 Token Program 管理
//   - DEX 程序通过 CPI（Cross-Program Invocation）直接指令 Token Program 转账
//   - 用户签名交易时已经授权了整个交易的所有指令，不需要单独的 approve 步骤
//   - 这使得 Solana 上的 swap 只需要一笔交易，而 EVM 可能需要两笔（approve + swap）
//
// 【生产中的完整 Approve 流程】
//
//   ┌──────────────────────────────────────────────────────────────┐
//   │ 1. 查询 allowance                                           │
//   │    eth_call → ERC20.allowance(owner, spender)               │
//   │    返回当前授权额度                                            │
//   ├──────────────────────────────────────────────────────────────┤
//   │ 2. 判断是否需要 approve                                      │
//   │    if allowance >= amountIn → 跳过 approve，直接 swap        │
//   │    if allowance < amountIn → 需要 approve                    │
//   ├──────────────────────────────────────────────────────────────┤
//   │ 3. 构建 approve 交易                                         │
//   │    - 编码 calldata: approve(address,uint256) 的 ABI 编码      │
//   │    - 估算 Gas: eth_estimateGas                               │
//   │    - 获取 Gas 价格: eth_gasPrice / eth_maxFeePerGas          │
//   │    - 设置 nonce: eth_getTransactionCount                     │
//   ├──────────────────────────────────────────────────────────────┤
//   │ 4. 签名 approve 交易                                         │
//   │    - EIP-1559: 使用 baseFee + priorityFee                    │
//   │    - Legacy: 使用 gasPrice                                   │
//   ├──────────────────────────────────────────────────────────────┤
//   │ 5. 发送 approve 交易                                         │
//   │    eth_sendRawTransaction                                    │
//   ├──────────────────────────────────────────────────────────────┤
//   │ 6. 轮询 approve 交易回执                                      │
//   │    eth_getTransactionReceipt（轮询直到确认或超时）               │
//   │    检查 receipt.status == 1（成功）                            │
//   ├──────────────────────────────────────────────────────────────┤
//   │ 7. approve 确认后，构建并发送 swap 交易                         │
//   │    此时 allowance 已足够，swap 中的 transferFrom 不会 revert    │
//   └──────────────────────────────────────────────────────────────┘

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
)

// ============================================================
// ABI 常量
// ============================================================
//
// EVM 合约调用使用 ABI（Application Binary Interface）编码。
// 每个函数调用的前 4 字节是 function selector，由函数签名的 keccak256 哈希的前 4 字节得到。
//
// 计算方式：
//   ApproveSelector   = keccak256("approve(address,uint256)")[:4]   = 0x095ea7b3
//   AllowanceSelector = keccak256("allowance(address,address)")[:4] = 0xdd62ed3e
//
// approve 函数的完整 ABI：
//   function approve(address spender, uint256 amount) external returns (bool)
//   - spender: 被授权的地址（如 Router 合约），ABI 编码为 32 字节（左补零）
//   - amount:  授权金额，ABI 编码为 32 字节（大端序）
//   - 返回值:  bool，表示是否成功
//
// allowance 函数的完整 ABI：
//   function allowance(address owner, address spender) external view returns (uint256)
//   - owner:   代币持有者地址
//   - spender: 被授权的地址
//   - 返回值:  uint256，当前授权额度
//
// 完整的 approve calldata 结构（共 68 字节）：
//   [0:4]   function selector  (4 bytes)  = 0x095ea7b3
//   [4:36]  spender address    (32 bytes) = 地址左补零到 32 字节
//   [36:68] amount             (32 bytes) = uint256 大端序编码

const (
	// ApproveSelector 是 approve(address,uint256) 的 function selector。
	// 计算过程：keccak256("approve(address,uint256)") 的前 4 字节 = 0x095ea7b3
	ApproveSelector = "0x095ea7b3"

	// AllowanceSelector 是 allowance(address,address) 的 function selector。
	// 计算过程：keccak256("allowance(address,address)") 的前 4 字节 = 0xdd62ed3e
	AllowanceSelector = "0xdd62ed3e"
)

// ============================================================
// MaxUint256 常量
// ============================================================

// MaxUint256 是 uint256 的最大值：2^256 - 1。
// 用于 "无限授权" 场景：approve(spender, MaxUint256) 表示授权对方可以操作无限数量的代币。
// 因为 uint256 最大值极大（约 1.16 * 10^77），实际中永远不会用尽，等效于无限授权。
//
// 以 16 进制表示：0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
var MaxUint256 *big.Int

func init() {
	// 计算 2^256 - 1
	MaxUint256 = new(big.Int).Sub(
		new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), // 2^256
		big.NewInt(1), // -1
	)
}

// ============================================================
// ApproveTx — Approve 交易数据
// ============================================================

// ApproveTx 表示一笔 ERC20 approve 交易的完整数据。
// 在生产中，这些数据会被用于构建 EIP-1559 或 Legacy 交易，然后签名发送。
type ApproveTx struct {
	// Token 是 ERC20 合约地址。
	// approve 交易的 to 字段就是这个地址（调用的是 ERC20 合约的 approve 方法）。
	Token string

	// Spender 是被授权的地址（通常是 DEX Router 合约地址）。
	// 作为 approve(address spender, uint256 amount) 的第一个参数。
	Spender string

	// Amount 是授权金额。
	// 精确授权时等于 amountIn，无限授权时等于 MaxUint256。
	Amount *big.Int

	// Gas 参数（EIP-1559 模型）
	GasLimit    uint64   // approve 交易的 Gas 上限（通常 50000-80000）
	BaseFee     *big.Int // 当前区块的 baseFee (wei)
	PriorityFee *big.Int // 矿工小费 / 优先费 (wei)

	// Calldata 是 ABI 编码后的调用数据。
	// 格式：selector(4 bytes) + spender(32 bytes) + amount(32 bytes)
	//
	// 示例（approve 0x7a25...88D 无限授权）：
	//   0x095ea7b3
	//   0000000000000000000000007a250d5630b4cf539739df2c5dacb4c659f2488d
	//   ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
	//
	// 生产中使用 go-ethereum 的 abi.Pack：
	//   parsedABI, _ := abi.JSON(strings.NewReader(erc20ABI))
	//   calldata, _ := parsedABI.Pack("approve", spenderAddr, amount)
	Calldata string
}

// ============================================================
// ApproveResult — Approve 检查结果
// ============================================================

// ApproveResult 封装了 approve 检查的完整结果。
// 调用方可以根据 Needed 字段判断是否需要发送 approve 交易，
// 根据 Strategy 字段了解使用的授权策略。
type ApproveResult struct {
	// Needed 表示是否需要发送 approve 交易。
	// false: 当前 allowance 已经足够，可以直接发送 swap 交易。
	// true:  需要先发送 approve 交易，等待确认后再发 swap。
	Needed bool

	// Tx 是 approve 交易的详细数据（仅当 Needed=true 时有值）。
	// 调用方拿到这个数据后，需要签名并发送到链上。
	Tx *ApproveTx

	// Strategy 是本次使用的授权策略。
	// "exact":     精确授权，Amount 等于本次 swap 的 amountIn
	// "unlimited": 无限授权，Amount 等于 MaxUint256
	Strategy string
}

// ============================================================
// ApproveChecker — Approve 检查器
// ============================================================

// ApproveChecker 负责检查和构建 ERC20 Approve 交易。
// 这是 EVM 链上 swap 流程中的关键步骤：在发送 swap 交易之前，
// 必须确保 Router 合约有足够的 allowance 来调用 transferFrom。
//
// 对标生产中 evmwallet 的 approvechecker.go：
//   - 查询链上 allowance 状态
//   - 根据策略决定授权金额（精确 vs 无限）
//   - 处理 USDT 等特殊代币的 approve(0) 问题
//   - 构建 approve 交易的 ABI calldata
type ApproveChecker struct {
	// getAllowance 模拟查询当前授权额度。
	// 参数：token（ERC20 合约地址）、owner（代币持有者）、spender（被授权者）
	// 返回：当前 allowance 值。
	//
	// 生产中的实现：
	//   通过 eth_call 调用 ERC20.allowance(owner, spender)
	//   calldata = AllowanceSelector + abi.encode(owner, spender)
	//   result = ethClient.CallContract(ctx, ethereum.CallMsg{
	//       To:   &tokenAddr,
	//       Data: calldata,
	//   }, nil)
	//   allowance = new(big.Int).SetBytes(result)
	getAllowance func(token, owner, spender string) *big.Int

	// useExactApprove 控制授权策略。
	// true:  精确授权 — 每次只授权 amountIn 的金额。
	//        优点：安全性最高，即使合约被攻击，损失仅限于本次交易金额。
	//        缺点：每次 swap 前都需要一笔额外的 approve 交易（多消耗约 46000 Gas）。
	//        适用：交易所热钱包（Bitrue 账户）、大额交易。
	// false: 无限授权 — 授权 MaxUint256，后续交易无需再 approve。
	//        优点：只需授权一次，后续 swap 无额外 Gas 开销。
	//        缺点：如果 Router 合约有漏洞，攻击者可转走所有代币。
	//        适用：外部用户账户、频繁交易场景、信任度高的 Router 合约。
	useExactApprove bool
}

// NewApproveChecker 创建 ApproveChecker。
//
// 参数：
//   - getAllowance: 查询 allowance 的函数（生产中调用 eth_call）
//   - useExactApprove: 是否使用精确授权策略
func NewApproveChecker(getAllowance func(token, owner, spender string) *big.Int, useExactApprove bool) *ApproveChecker {
	return &ApproveChecker{
		getAllowance:    getAllowance,
		useExactApprove: useExactApprove,
	}
}

// NeedApprove 检查是否需要授权。
//
// 判断逻辑：对比当前 allowance 和 amountIn。
// 如果 allowance >= amountIn，说明之前已经授权过且额度充足，无需再次 approve。
// 如果 allowance < amountIn，说明额度不足，需要发送 approve 交易。
//
// 参数：
//   - token:   ERC20 合约地址
//   - owner:   代币持有者地址（通常是用户钱包地址）
//   - spender: 被授权地址（通常是 Router 合约地址）
//   - amountIn: 本次 swap 需要的代币数量
//
// 返回：true 表示需要 approve，false 表示 allowance 充足。
func (ac *ApproveChecker) NeedApprove(token, owner, spender string, amountIn *big.Int) bool {
	// 查询当前链上 allowance
	currentAllowance := ac.getAllowance(token, owner, spender)

	slog.Debug("approve check: 查询当前 allowance",
		"token", token,
		"owner", owner,
		"spender", spender,
		"current_allowance", currentAllowance.String(),
		"required_amount", amountIn.String(),
	)

	// allowance >= amountIn 时无需 approve
	// Cmp 返回：-1 (小于), 0 (等于), 1 (大于)
	return currentAllowance.Cmp(amountIn) < 0
}

// BuildApproveTx 构建 Approve 交易数据。
//
// 根据 useExactApprove 策略决定授权金额：
//   - 精确授权：amount = amountIn
//   - 无限授权：amount = MaxUint256 (2^256 - 1)
//
// 返回的 ApproveTx 包含完整的交易信息，调用方可直接用于签名和发送。
//
// 【USDT 特殊处理说明】
// 对于 USDT 等旧版代币，如果当前 allowance 不为 0 且需要设置新的 allowance，
// 需要先发送 approve(spender, 0) 交易将授权清零，等该交易确认后，
// 再发送 approve(spender, newAmount)。
// 本 demo 简化处理，不模拟两步 approve。生产中需要：
//   1. 检查当前 allowance 是否为 0
//   2. 如果不为 0 且代币是 USDT（或其他非标准实现），先 approve(0)
//   3. approve(0) 确认后，再 approve(newAmount)
//
// 参数：
//   - token:   ERC20 合约地址
//   - spender: Router 合约地址
//   - amountIn: 本次 swap 需要的代币数量
//
// 返回：完整的 ApproveTx 交易数据。
func (ac *ApproveChecker) BuildApproveTx(token, spender string, amountIn *big.Int) *ApproveTx {
	// 根据策略确定授权金额
	var approveAmount *big.Int
	if ac.useExactApprove {
		approveAmount = new(big.Int).Set(amountIn)
	} else {
		approveAmount = new(big.Int).Set(MaxUint256)
	}

	// 构建 ABI 编码的 calldata
	// 格式：selector(4 bytes) + spender(32 bytes, 左补零) + amount(32 bytes, 大端序)
	//
	// 示例：
	//   selector: 095ea7b3
	//   spender:  0000000000000000000000007a250d5630b4cf539739df2c5dacb4c659f2488d
	//   amount:   ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff (MaxUint256)
	//
	// 生产中使用 go-ethereum/accounts/abi 包：
	//   erc20ABI, _ := abi.JSON(strings.NewReader(`[{"name":"approve","type":"function",
	//     "inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],
	//     "outputs":[{"name":"","type":"bool"}]}]`))
	//   calldata, _ := erc20ABI.Pack("approve", common.HexToAddress(spender), approveAmount)
	calldata := fmt.Sprintf("%s%064x%064x",
		ApproveSelector[2:], // 去掉 "0x" 前缀
		addressToUint(spender),
		approveAmount,
	)

	// Gas 参数说明：
	// approve 交易的 Gas 消耗通常在 46000-65000 之间，取决于：
	//   - 是否是首次授权（SSTORE 从 0 到非 0 消耗 20000 Gas）
	//   - 还是更新授权（SSTORE 从非 0 到非 0 消耗 5000 Gas）
	// 这里使用保守估计 65000
	tx := &ApproveTx{
		Token:       token,
		Spender:     spender,
		Amount:      approveAmount,
		GasLimit:    65000,
		BaseFee:     big.NewInt(30e9), // 模拟 30 Gwei baseFee
		PriorityFee: big.NewInt(2e9),  // 模拟 2 Gwei priorityFee
		Calldata:    "0x" + calldata,
	}

	strategy := "unlimited"
	if ac.useExactApprove {
		strategy = "exact"
	}

	slog.Info("approve tx 构建完成",
		"token", token,
		"spender", spender,
		"amount", approveAmount.String(),
		"strategy", strategy,
		"gas_limit", tx.GasLimit,
		"calldata_length", len(tx.Calldata),
	)

	return tx
}

// CheckAndApprove 执行完整的 "检查 + 授权" 流程。
//
// 这是调用方最常用的方法，封装了完整的 approve 逻辑：
//   1. 查询当前 allowance
//   2. 判断是否需要 approve
//   3. 如果需要，构建 approve 交易数据
//
// 生产中的完整流程（本 demo 只模拟到构建 tx 数据）：
//   1. CheckAndApprove → 获得 ApproveTx
//   2. 签名 ApproveTx → 得到 signedTx
//   3. eth_sendRawTransaction(signedTx) → 得到 txHash
//   4. 轮询 eth_getTransactionReceipt(txHash) → 等待确认
//   5. 确认 receipt.status == 1 → approve 成功
//   6. 继续构建和发送 swap 交易
//
// 参数：
//   - ctx:     上下文（用于超时控制）
//   - token:   ERC20 合约地址
//   - owner:   代币持有者地址
//   - spender: Router 合约地址
//   - amountIn: 本次 swap 需要的代币数量
//
// 返回：
//   - approved: 是否执行了 approve（true 表示构建了 approve 交易）
//   - tx:       approve 交易详情（仅当 approved=true 时有值）
//   - err:      错误信息
func (ac *ApproveChecker) CheckAndApprove(ctx context.Context, token, owner, spender string, amountIn *big.Int) (approved bool, tx *ApproveTx, err error) {
	// 参数校验
	if token == "" || owner == "" || spender == "" {
		return false, nil, fmt.Errorf("approve check: 地址参数不能为空 (token=%q, owner=%q, spender=%q)", token, owner, spender)
	}
	if amountIn == nil || amountIn.Sign() <= 0 {
		return false, nil, fmt.Errorf("approve check: amountIn 必须为正数，got %v", amountIn)
	}

	// 检查上下文是否已取消
	select {
	case <-ctx.Done():
		return false, nil, fmt.Errorf("approve check: 上下文已取消: %w", ctx.Err())
	default:
	}

	slog.Info("approve check: 开始检查授权状态",
		"token", token,
		"owner", owner,
		"spender", spender,
		"amount_in", amountIn.String(),
	)

	// 步骤 1: 检查是否需要 approve
	needApprove := ac.NeedApprove(token, owner, spender, amountIn)

	if !needApprove {
		slog.Info("approve check: allowance 充足，无需 approve",
			"token", token,
			"spender", spender,
		)
		return false, nil, nil
	}

	// 步骤 2: 需要 approve，构建 approve 交易
	slog.Info("approve check: allowance 不足，构建 approve 交易",
		"token", token,
		"spender", spender,
		"amount_in", amountIn.String(),
	)

	approveTx := ac.BuildApproveTx(token, spender, amountIn)

	// 步骤 3: 在生产中，这里还需要：
	// - 签名交易（使用私钥或 KMS）
	// - 发送交易（eth_sendRawTransaction）
	// - 轮询回执（eth_getTransactionReceipt）
	// - 确认成功（receipt.status == 1）
	// 本 demo 只模拟到构建交易数据

	slog.Info("approve check: approve 交易构建完成（demo 模拟，未实际发送）",
		"token", token,
		"spender", spender,
		"approve_amount", approveTx.Amount.String(),
		"gas_limit", approveTx.GasLimit,
	)

	return true, approveTx, nil
}

// GetApproveResult 返回结构化的 ApproveResult。
// 这是 CheckAndApprove 的包装方法，返回更完整的结果信息。
//
// 参数：
//   - ctx:     上下文
//   - token:   ERC20 合约地址
//   - owner:   代币持有者地址
//   - spender: Router 合约地址
//   - amountIn: 本次 swap 需要的代币数量
//
// 返回：ApproveResult 结构体和可能的错误。
func (ac *ApproveChecker) GetApproveResult(ctx context.Context, token, owner, spender string, amountIn *big.Int) (*ApproveResult, error) {
	approved, tx, err := ac.CheckAndApprove(ctx, token, owner, spender, amountIn)
	if err != nil {
		return nil, err
	}

	strategy := "unlimited"
	if ac.useExactApprove {
		strategy = "exact"
	}

	return &ApproveResult{
		Needed:   approved,
		Tx:       tx,
		Strategy: strategy,
	}, nil
}

// ============================================================
// 辅助函数
// ============================================================

// addressToUint 将十六进制地址字符串转换为 *big.Int。
// 用于 ABI 编码时将 address 类型填充到 32 字节。
//
// EVM 地址是 20 字节（160 位），在 ABI 编码中左补零到 32 字节（256 位）。
// 例如：0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D
// 编码为：0000000000000000000000007a250d5630b4cf539739df2c5dacb4c659f2488d
func addressToUint(addr string) *big.Int {
	// 去掉 "0x" 前缀
	cleanAddr := addr
	if len(addr) > 2 && addr[:2] == "0x" {
		cleanAddr = addr[2:]
	}

	result := new(big.Int)
	result.SetString(cleanAddr, 16)
	return result
}
