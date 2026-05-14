// 余额差异交叉验证（Balance Diff Cross-Validation）
// [dexwallet 通用层] -- Solana preTokenBalances/postTokenBalances 交叉验证
//
// 为什么需要余额差异验证？
// =============================
// 在生产环境中，仅靠解析指令数据（Instruction Data）来获取 swap 金额是不可靠的，
// 尤其在 CPI（跨程序调用）场景下：
//
//   1. 聚合器（如 Jupiter）的顶层指令金额可能是路由中间值，而非最终到账金额
//   2. CPI 链路中每一跳的 amountIn/amountOut 可能因滑点保护而与实际执行金额不同
//   3. 某些 DEX（如 PumpFun）的指令数据中包含的是"最大/最小"约束值，不是实际成交值
//   4. 内部指令的 data 字段可能被优化或省略（特别是 native program 调用）
//
// 因此，生产中采用双重验证策略：
//   - 方法 A：解析指令数据（Instruction Data parsing）→ 得到 ParsedIn / ParsedOut
//   - 方法 B：计算余额差异（Balance Diff）→ 得到 BalanceDiffIn / BalanceDiffOut
//   - 交叉验证：两者比较，如果不一致，以余额差异为准（因为它反映的是链上最终状态）
//
// 余额差异的本质是 postTokenBalances - preTokenBalances，
// 这是 Solana 验证节点在交易执行前后对所有涉及的 Token Account 做的快照，
// 是最终的、不可篡改的链上状态变化记录。
//
// 匹配算法说明：
// =============================
// 使用 (AccountIndex, Mint) 组合键进行匹配，而不是用 Owner：
//   - 一个 Owner 可能拥有多个相同 Mint 的 Token Account（虽然少见但合法）
//   - AccountIndex 是交易 accountKeys 数组中的位置索引，唯一标识一个账户
//   - (AccountIndex, Mint) 组合可以精确定位到具体的 Token Account
//
// 运行: go run ./07-event-parsing/demo/
package main

import (
	"fmt"
	"math/big"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// BalanceDiff 单个 Token Account 的余额变化。
// 记录某个代币账户在交易前后的余额差异。
//
// 生产中对应 Solana RPC 返回的 meta.preTokenBalances / meta.postTokenBalances 计算结果：
//   - Before: preTokenBalances 中该账户的 uiTokenAmount.amount（字符串大整数）
//   - After:  postTokenBalances 中该账户的 uiTokenAmount.amount
//   - Change: After - Before（正值=收入，负值=支出）
type BalanceDiff struct {
	Owner  string   // Token Account 的实际拥有者（即 owner 字段，通常是用户钱包地址）
	Mint   string   // 代币 Mint 地址（标识是哪种代币）
	Before *big.Int // 交易前余额（最小单位，如 lamports）
	After  *big.Int // 交易后余额（最小单位）
	Change *big.Int // 变化量 = After - Before（正=收入, 负=支出）
}

// BalanceValidation 余额差异交叉验证结果。
// 将指令解析得到的金额与余额差异计算的金额进行比较，
// 确保两种方式得出的结果一致。
//
// 在生产中，当 IsValid=false 时：
//   - 如果差异较小（<1%），可能是精度问题，通常可以忽略
//   - 如果差异较大，应以 BalanceDiff 为准，因为它是链上最终状态
//   - 记录 Discrepancy 到日志/监控系统，用于后续排查和优化解析逻辑
type BalanceValidation struct {
	IsValid        bool     // 两种方式的结果是否一致
	ParsedIn       *big.Int // 指令解析得到的 AmountIn（方法 A）
	ParsedOut      *big.Int // 指令解析得到的 AmountOut（方法 A）
	BalanceDiffIn  *big.Int // 余额变化计算的 AmountIn（取绝对值，方法 B）
	BalanceDiffOut *big.Int // 余额变化计算的 AmountOut（取绝对值，方法 B）
	Discrepancy    string   // 不一致原因（空字符串=完全一致）
}

// BalanceDiffCalculator 余额差异计算器。
// 封装了从 preTokenBalances/postTokenBalances 计算余额变化、
// 按 Owner/Mint 过滤、以及与解析事件交叉验证的完整流程。
type BalanceDiffCalculator struct{}

// NewBalanceDiffCalculator 创建余额差异计算器实例。
func NewBalanceDiffCalculator() *BalanceDiffCalculator {
	return &BalanceDiffCalculator{}
}

// Calculate 计算所有 Token Account 的余额变化。
//
// 算法流程：
//  1. 将 preTokenBalances 按 (AccountIndex, Mint) 建立索引 map
//  2. 遍历 postTokenBalances，用相同的 (AccountIndex, Mint) 查找 pre 记录
//  3. 计算 Change = PostAmount - PreAmount
//  4. 跳过 Change == 0 的记录（余额未变化的账户不关心）
//
// 为什么用 (AccountIndex, Mint) 而不是 Owner？
//   - 一个 Owner（用户钱包）可以拥有多个同一 Mint 的 Token Account
//   - AccountIndex 是交易 accountKeys 中的唯一位置，精确对应一个 Token Account
//   - pre 和 post 中相同 AccountIndex 保证指向同一个账户
//
// 参数：
//   - pre: 交易前的代币余额快照（meta.preTokenBalances）
//   - post: 交易后的代币余额快照（meta.postTokenBalances）
//
// 返回：
//   - 所有余额发生变化的 BalanceDiff 列表
func (c *BalanceDiffCalculator) Calculate(pre, post []MockTokenBalance) []BalanceDiff {
	// 构建索引键的辅助函数：使用 (AccountIndex, Mint) 组合作为唯一键
	makeKey := func(accountIndex int, mint string) string {
		return fmt.Sprintf("%d:%s", accountIndex, mint)
	}

	// 步骤 1: 将 preTokenBalances 建立索引
	// key = "accountIndex:mint" → value = MockTokenBalance
	preMap := make(map[string]MockTokenBalance, len(pre))
	for _, tb := range pre {
		key := makeKey(tb.AccountIndex, tb.Mint)
		preMap[key] = tb
	}

	// 步骤 2: 遍历 postTokenBalances，匹配 pre 记录并计算差值
	var diffs []BalanceDiff
	for _, postTB := range post {
		key := makeKey(postTB.AccountIndex, postTB.Mint)

		// 解析 post 金额
		postAmount := new(big.Int)
		postAmount.SetString(postTB.Amount, 10)

		// 查找匹配的 pre 记录
		preAmount := new(big.Int) // 默认为 0（如果 pre 中没有该账户，说明是新创建的）
		if preTB, ok := preMap[key]; ok {
			preAmount.SetString(preTB.Amount, 10)
		}

		// 计算变化量
		change := new(big.Int).Sub(postAmount, preAmount)

		// 步骤 3: 跳过零变化（余额未变动的账户对我们没有意义）
		if change.Sign() == 0 {
			continue
		}

		diffs = append(diffs, BalanceDiff{
			Owner:  postTB.Owner,
			Mint:   postTB.Mint,
			Before: preAmount,
			After:  postAmount,
			Change: change,
		})
	}

	// 步骤 4: 处理只在 pre 中存在但 post 中不存在的账户
	// 这种情况意味着 Token Account 在交易中被关闭（close account），
	// 余额从某个值变为 0
	postMap := make(map[string]struct{}, len(post))
	for _, postTB := range post {
		key := makeKey(postTB.AccountIndex, postTB.Mint)
		postMap[key] = struct{}{}
	}

	for _, preTB := range pre {
		key := makeKey(preTB.AccountIndex, preTB.Mint)
		if _, exists := postMap[key]; exists {
			continue // 已经在上面处理过了
		}

		preAmount := new(big.Int)
		preAmount.SetString(preTB.Amount, 10)

		// Token Account 被关闭，余额从 preAmount 变为 0
		if preAmount.Sign() == 0 {
			continue // 原本就是 0，跳过
		}

		change := new(big.Int).Neg(preAmount) // 变化量 = 0 - preAmount = -preAmount

		diffs = append(diffs, BalanceDiff{
			Owner:  preTB.Owner,
			Mint:   preTB.Mint,
			Before: preAmount,
			After:  new(big.Int), // 0
			Change: change,
		})
	}

	return diffs
}

// GetDiffsForOwner 按 Owner 过滤余额变化。
// 在 swap 验证中，我们通常只关心交易发起者（Sender）的余额变化，
// 因为 swap 的 amountIn/amountOut 是相对于发起者而言的。
//
// 参数：
//   - diffs: Calculate 返回的所有余额变化
//   - owner: 要过滤的 Owner 地址（通常是 tx.Sender）
//
// 返回：
//   - 该 Owner 名下所有 Token Account 的余额变化
func (c *BalanceDiffCalculator) GetDiffsForOwner(diffs []BalanceDiff, owner string) []BalanceDiff {
	var result []BalanceDiff
	for _, d := range diffs {
		if d.Owner == owner {
			result = append(result, d)
		}
	}
	return result
}

// GetDiffsForMint 按 Mint 过滤余额变化。
// 用于查看某种特定代币在交易中的所有余额变化，
// 包括所有相关 Owner 的变化（不限于 Sender）。
//
// 参数：
//   - diffs: Calculate 返回的所有余额变化
//   - mint: 要过滤的代币 Mint 地址
//
// 返回：
//   - 该 Mint 代币的所有余额变化
func (c *BalanceDiffCalculator) GetDiffsForMint(diffs []BalanceDiff, mint string) []BalanceDiff {
	var result []BalanceDiff
	for _, d := range diffs {
		if d.Mint == mint {
			result = append(result, d)
		}
	}
	return result
}

// Validate 交叉验证：将指令解析结果与余额差异计算结果进行比较。
//
// 这是生产中确保解析金额准确的核心方法。流程如下：
//  1. 从所有 diffs 中找到交易发起者（event.Sender）的余额变化
//  2. 找到负变化（支出）→ 对应 AmountIn（用户支付的代币）
//  3. 找到正变化（收入）→ 对应 AmountOut（用户收到的代币）
//  4. 将余额差异的绝对值与指令解析的 AmountIn/AmountOut 比较
//  5. 如果不一致，记录差异原因到 Discrepancy 字段
//
// 当两者不一致时的处理策略：
//   - 以余额差异（BalanceDiff）为准，因为它是链上最终状态的直接反映
//   - 指令数据中的金额可能是：请求金额（非成交金额）、含手续费金额、中间路由金额等
//   - 记录差异到监控系统，帮助发现和修复解析逻辑中的 bug
//
// 参数：
//   - diffs: Calculate 返回的所有余额变化
//   - event: 通过指令解析得到的 ChainEvent（含 AmountIn, AmountOut, Sender, TokenIn, TokenOut）
//
// 返回：
//   - BalanceValidation 验证结果
func (c *BalanceDiffCalculator) Validate(diffs []BalanceDiff, event *dexwallet.ChainEvent) *BalanceValidation {
	validation := &BalanceValidation{
		IsValid:   true,
		ParsedIn:  event.AmountIn,
		ParsedOut: event.AmountOut,
	}

	// 步骤 1: 找到交易发起者的余额变化
	senderDiffs := c.GetDiffsForOwner(diffs, event.Sender)
	if len(senderDiffs) == 0 {
		validation.IsValid = false
		validation.Discrepancy = fmt.Sprintf(
			"未找到 Sender(%s) 的余额变化记录，可能 Sender 地址不在 tokenBalances 中",
			event.Sender,
		)
		return validation
	}

	// 步骤 2: 从 Sender 的余额变化中，找到与 TokenIn/TokenOut 匹配的记录
	// TokenIn 对应负变化（支出），TokenOut 对应正变化（收入）
	var balanceDiffIn *big.Int  // Sender 支出的金额（取绝对值）
	var balanceDiffOut *big.Int // Sender 收到的金额

	for _, d := range senderDiffs {
		if d.Mint == event.TokenIn && d.Change.Sign() < 0 {
			// 负变化 = 支出，取绝对值作为 AmountIn
			balanceDiffIn = new(big.Int).Abs(d.Change)
		}
		if d.Mint == event.TokenOut && d.Change.Sign() > 0 {
			// 正变化 = 收入，直接作为 AmountOut
			balanceDiffOut = new(big.Int).Set(d.Change)
		}
	}

	validation.BalanceDiffIn = balanceDiffIn
	validation.BalanceDiffOut = balanceDiffOut

	// 步骤 3: 检查是否找到了对应的余额变化
	if balanceDiffIn == nil {
		validation.IsValid = false
		validation.Discrepancy = fmt.Sprintf(
			"未找到 Sender(%s) 对 TokenIn(%s) 的支出记录（负变化）",
			event.Sender, event.TokenIn,
		)
		return validation
	}

	if balanceDiffOut == nil {
		validation.IsValid = false
		validation.Discrepancy = fmt.Sprintf(
			"未找到 Sender(%s) 对 TokenOut(%s) 的收入记录（正变化）",
			event.Sender, event.TokenOut,
		)
		return validation
	}

	// 步骤 4: 交叉比较
	// 比较指令解析的 AmountIn 与余额差异计算的 AmountIn
	if event.AmountIn != nil && balanceDiffIn.Cmp(event.AmountIn) != 0 {
		validation.IsValid = false
		validation.Discrepancy = fmt.Sprintf(
			"AmountIn 不一致: 指令解析=%s, 余额差异=%s (差值=%s)",
			event.AmountIn.String(),
			balanceDiffIn.String(),
			new(big.Int).Sub(balanceDiffIn, event.AmountIn).String(),
		)
		return validation
	}

	// 比较指令解析的 AmountOut 与余额差异计算的 AmountOut
	if event.AmountOut != nil && balanceDiffOut.Cmp(event.AmountOut) != 0 {
		validation.IsValid = false
		validation.Discrepancy = fmt.Sprintf(
			"AmountOut 不一致: 指令解析=%s, 余额差异=%s (差值=%s)",
			event.AmountOut.String(),
			balanceDiffOut.String(),
			new(big.Int).Sub(balanceDiffOut, event.AmountOut).String(),
		)
		return validation
	}

	return validation
}
