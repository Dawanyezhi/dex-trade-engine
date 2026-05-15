// Preflight 验证链 — 最早阶段的参数校验。
//
// ============================================================
// Preflight vs PreCheck 的职责边界
// ============================================================
//
// 请求到达后的验证分为三个阶段：
//
//	请求到达
//	   ↓
//	[Preflight] ← 最早的参数校验（格式、范围、签名）→ 返回 PreflightRejectCode
//	   ↓ 通过
//	[PreCheck]  ← 业务逻辑校验（余额、滑点、池子活跃）→ 返回 error
//	   ↓ 通过
//	[Build]     ← 构建交易
//	   ↓
//	[PostCheck] ← 构建后检查（输出金额、Gas）→ 返回 error
//
// Preflight 的特点：
//   - 纯参数校验，不涉及任何外部依赖（不查数据库、不查链上状态）
//   - 成本极低，可以在 API 网关层执行
//   - 返回结构化错误码（PreflightRejectCode），而非 error
//   - 校验顺序：成本低的先检查，避免无意义的后续计算
//
// PreCheck 的特点：
//   - 需要查询余额、池子状态等外部数据
//   - 返回 error（包装为 NonDegradableError）
//   - 在 Preflight 通过后执行
//
// ============================================================
// 为什么用错误码而不是 error
// ============================================================
//
// Preflight 返回 PreflightRejectCode 而非 error，原因：
//   1. API 级别的拦截，需要结构化错误码返回给上游（HTTP/gRPC 响应体）
//   2. 上游系统（如订单系统）需要根据错误码做分类处理（重试/拒绝/告警）
//   3. 错误码可聚合统计，用于监控和报警（如 4001 突增说明有批量非法合约地址）
//   4. 国际化：前端可根据错误码展示对应语言的提示
//
// ============================================================
// 生产扩展
// ============================================================
//
// 生产中 Preflight 还会验证上游签名（ed25519 签名校验，防止伪造请求）。
// 典型流程：
//   1. 上游系统用私钥对请求 body 签名
//   2. Preflight 用对应公钥验证签名，防止中间人篡改
//   3. 签名校验失败直接拒绝，不进入后续流程
//
// 校验顺序的设计原则（成本低的先检查，避免无意义的数据库查询）：
//   1. OrderID/UID — 整数比较，O(1)
//   2. Amount/Fee — 字符串解析 + 数值比较，O(n) n=字符串长度
//   3. Slippage — 整数范围比较，O(1)
//   4. Contract — 外部验证函数（可能涉及正则/checksum），成本相对最高
//
// 对标 irwallet preflight.go 的设计思路。
package main

import (
	"math/big"
	"strings"
)

// ============================================================
// PreflightRejectCode — 本地错误码定义
// ============================================================

// PreflightRejectCode Preflight 阶段的拒绝码。
//
// 注意：这是教学项目的简化做法。RejectCode 的完整定义在 08-production-architecture
// 模块中（tx_state_machine.go），但因为 03 和 08 都在各自的 package main 中，
// 不能直接引用。
//
// 生产中 RejectCode 应定义在共享的 internal 包中（如 internal/rejectcode/code.go），
// 所有模块通过 import 统一引用，避免重复定义和值域不一致的风险。
type PreflightRejectCode int

const (
	PreflightOK PreflightRejectCode = 0 // 成功

	// 段 1: 订单参数 (1000-1999)
	PreflightInvalidOrderID PreflightRejectCode = 1001 // 无效的订单 ID
	PreflightInvalidUserID  PreflightRejectCode = 1002 // 无效的用户 ID

	// 段 2: 金额和费用 (2000-2999)
	PreflightInvalidAmount PreflightRejectCode = 2001 // 无效的交易金额
	PreflightInvalidFee    PreflightRejectCode = 2002 // 无效的手续费

	// 段 3: 比率和滑点 (3000-3999)
	PreflightInvalidFeeRate    PreflightRejectCode = 3001 // 无效的费率
	PreflightInvalidSlippage   PreflightRejectCode = 3001 // 无效的滑点设置（滑点为 0 或低于下限）
	PreflightSlippageOverflow  PreflightRejectCode = 3002 // 滑点超出允许范围

	// 段 4: 合约和 DEX (4000-4999)
	PreflightInvalidContract PreflightRejectCode = 4001 // 无效的合约地址
)

// ============================================================
// SwapPreflightRequest — Preflight 校验请求
// ============================================================

// SwapPreflightRequest Preflight 阶段的请求参数。
// 与 dexwallet.SwapRequest 不同，这里的字段都是原始字符串形式，
// 因为 Preflight 的职责就是验证这些原始输入是否可以安全地解析为业务类型。
type SwapPreflightRequest struct {
	OrderID      int64  // 订单 ID（必须 > 0）
	UID          int64  // 用户 ID（必须 >= minUID）
	SellContract string // 卖出代币合约地址
	BuyContract  string // 买入代币合约地址
	SellAmount   string // 字符串形式的金额（需要验证可解析为正整数）
	PriorityFee  string // 字符串形式的优先费（可选字段，为空时跳过校验）
	FeeRate      string // 字符串形式的费率（可选字段，为空时跳过校验）
	Slippage     uint64 // 滑点，单位 BPS（基点，1 BPS = 0.01%）
}

// ============================================================
// PreflightResult — 校验结果
// ============================================================

// PreflightResult Preflight 校验结果。
// Code 为 0 表示所有校验通过，非 0 表示被拒绝，Message 描述拒绝原因。
type PreflightResult struct {
	Code    PreflightRejectCode // 0 = OK
	Message string              // 人类可读描述
}

// IsOK 检查 Preflight 校验是否通过。
func (r PreflightResult) IsOK() bool { return r.Code == PreflightOK }

// ============================================================
// SwapPreflight — Preflight 验证器
// ============================================================

// SwapPreflight Swap 交易的 Preflight 验证器。
// 在请求进入业务逻辑之前，对原始参数做格式和范围校验。
//
// 使用方式：
//
//	pf := NewSwapPreflight()
//	result := pf.Check(&SwapPreflightRequest{...})
//	if !result.IsOK() {
//	    // 返回错误码给上游
//	    return result.Code, result.Message
//	}
//	// 继续进入 PreCheck -> Build -> PostCheck 流程
type SwapPreflight struct {
	// validateContract 合约地址验证函数。
	// 检查地址格式是否合法（如 Solana 的 base58 格式、EVM 的 0x 前缀 + 40 位 hex）。
	// 生产中根据链类型注入不同的验证逻辑。
	validateContract func(address string) bool

	// baseTokens 已知的 base token 列表。
	// 包括 SOL、WSOL、ETH、BNB、USDT、USDC 等主流代币的合约地址。
	// 用于快速判断某个地址是否为已知的基础代币（某些校验规则对 base token 可能不同）。
	baseTokens map[string]bool

	// minUID UID 范围下限。
	// 系统启动时的初始 UID，低于此值的请求视为非法。
	minUID int64
}

// NewSwapPreflight 创建带有默认配置的 SwapPreflight。
//
// 默认配置：
//   - validateContract: 简单的非空 + 长度检查（生产中应替换为链特定的地址校验）
//   - baseTokens: 包含常见的 SOL、WSOL、ETH、WETH、BNB、WBNB、USDT、USDC
//   - minUID: 10000（假设系统从此 UID 开始分配）
func NewSwapPreflight() *SwapPreflight {
	return &SwapPreflight{
		validateContract: defaultValidateContract,
		baseTokens: map[string]bool{
			// Solana
			"So11111111111111111111111111111111111111112":  true, // SOL (Native Mint)
			"So11111111111111111111111111111111111111111":  true, // WSOL
			// EVM 通用
			"0x0000000000000000000000000000000000000000":   true, // ETH (零地址表示原生代币)
			"0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2": true, // WETH
			"0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c": true, // WBNB (BSC)
			// 稳定币
			"0xdAC17F958D2ee523a2206206994597C13D831ec7": true, // USDT (ETH)
			"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48": true, // USDC (ETH)
			"Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB": true, // USDT (Solana)
			"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v": true, // USDC (Solana)
		},
		minUID: 10000,
	}
}

// defaultValidateContract 默认的合约地址验证函数。
// 简单检查：非空、去除空白后长度 >= 20。
// 生产中应替换为链特定的验证：
//   - Solana: base58 解码 + 长度 32 字节
//   - EVM: 0x 前缀 + 40 位 hex + EIP-55 checksum
func defaultValidateContract(address string) bool {
	trimmed := strings.TrimSpace(address)
	return len(trimmed) >= 20
}

// ============================================================
// Check — 核心校验链
// ============================================================

// Check 执行 Preflight 校验链。
// 按照成本从低到高的顺序逐项检查，一旦发现不合法立即返回对应的错误码。
//
// 校验顺序（对标 irwallet preflight.go）：
//  1. OrderID > 0
//  2. UID >= minUID
//  3. SellAmount 可解析为正整数
//  4. PriorityFee 有效（可选字段）
//  5. FeeRate 有效（可选字段）
//  6. Slippage 范围 [10, 5000]
//  7. SellContract 格式合法
//  8. BuyContract 格式合法
func (p *SwapPreflight) Check(req *SwapPreflightRequest) PreflightResult {
	// 1. 校验 OrderID：必须为正整数
	if req.OrderID <= 0 {
		return PreflightResult{
			Code:    PreflightInvalidOrderID,
			Message: "OrderID 必须大于 0",
		}
	}

	// 2. 校验 UID：必须 >= minUID
	if req.UID < p.minUID {
		return PreflightResult{
			Code:    PreflightInvalidUserID,
			Message: "UID 不在合法范围内",
		}
	}

	// 3. 校验 SellAmount：必须可解析为正整数
	if !isValidAmount(req.SellAmount) {
		return PreflightResult{
			Code:    PreflightInvalidAmount,
			Message: "SellAmount 无法解析为正整数",
		}
	}

	// 4. 校验 PriorityFee（可选字段）：非空时必须可解析为非负数
	if req.PriorityFee != "" && !isValidReadable(req.PriorityFee) {
		return PreflightResult{
			Code:    PreflightInvalidFee,
			Message: "PriorityFee 无法解析为有效的非负数值",
		}
	}

	// 5. 校验 FeeRate（可选字段）：非空时必须可解析为非负数
	if req.FeeRate != "" && !isValidReadable(req.FeeRate) {
		return PreflightResult{
			Code:    PreflightInvalidFeeRate,
			Message: "FeeRate 无法解析为有效的非负数值",
		}
	}

	// 6. 校验 Slippage：范围 [10, 5000] BPS（即 0.1% ~ 50%）
	if req.Slippage < 10 {
		return PreflightResult{
			Code:    PreflightInvalidSlippage,
			Message: "Slippage 低于最小值 10 BPS（0.1%）",
		}
	}
	if req.Slippage > 5000 {
		return PreflightResult{
			Code:    PreflightSlippageOverflow,
			Message: "Slippage 超过最大值 5000 BPS（50%）",
		}
	}

	// 7. 校验 SellContract：合约地址格式合法
	if !p.validateContract(req.SellContract) {
		return PreflightResult{
			Code:    PreflightInvalidContract,
			Message: "SellContract 地址格式不合法",
		}
	}

	// 8. 校验 BuyContract：合约地址格式合法
	if !p.validateContract(req.BuyContract) {
		return PreflightResult{
			Code:    PreflightInvalidContract,
			Message: "BuyContract 地址格式不合法",
		}
	}

	// 全部校验通过
	return PreflightResult{
		Code:    PreflightOK,
		Message: "OK",
	}
}

// ============================================================
// 辅助方法
// ============================================================

// isValidAmount 检查字符串是否可解析为正整数。
// 用于验证 SellAmount 等必须为正整数的字段。
//
// 规则：
//   - 不能为空
//   - 必须可被 big.Int 解析（支持任意精度）
//   - 必须为正数（> 0）
func isValidAmount(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	n := new(big.Int)
	_, ok := n.SetString(s, 10)
	if !ok {
		return false
	}

	return n.Sign() > 0
}

// isValidReadable 检查字符串是否可解析为非负数。
// 用于验证 PriorityFee、FeeRate 等可选字段（允许为 0，但不能为负数）。
//
// 规则：
//   - 不能为空（调用方应在调用前检查是否为空字符串来决定是否跳过）
//   - 必须可被 big.Int 解析（支持任意精度整数）
//   - 必须为非负数（>= 0）
func isValidReadable(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	n := new(big.Int)
	_, ok := n.SetString(s, 10)
	if !ok {
		return false
	}

	return n.Sign() >= 0
}
