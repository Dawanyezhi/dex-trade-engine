// CPI（Cross-Program Invocation）递归解析与指令展平
//
// 这是 Solana 交易解析中最关键的一环。没有 CPI 解析，70%+ 的 Solana 交易无法正确解读。
//
// ============================================================================
// 为什么 CPI 解析如此重要？
// ============================================================================
//
// 以 Jupiter 聚合器为例（Solana 上最大的 DEX 聚合器）：
//   - 用户发起一笔 swap 交易，顶层只有 1 条指令（调用 Jupiter Program）
//   - Jupiter 内部通过 CPI 调用 Raydium/Orca/PumpFun 等 DEX 完成实际的 swap
//   - 所有真正的 swap 操作都藏在 innerInstructions 里
//   - 如果只解析顶层指令，你只能看到"调用了 Jupiter"，但看不到任何 swap 细节
//
// 真实场景举例：
//   顶层指令: [Jupiter Program]
//   内部指令: [
//     Raydium.swap(SOL → USDC),      // Jupiter CPI 调用 Raydium
//     TokenProgram.transfer(...),      // Raydium CPI 调用 Token Program 完成转账
//     Orca.swap(USDC → RAY),          // Jupiter CPI 调用 Orca
//     TokenProgram.transfer(...),      // Orca CPI 调用 Token Program 完成转账
//   ]
//
// ============================================================================
// CPI 的三种典型模式
// ============================================================================
//
// 模式 A: 简单 CPI（Simple CPI）
//   Program A → Program B
//   例: Raydium → Token Program（DEX 调用 Token Program 完成代币转账）
//   特点: 只有一层 CPI，最常见的基础模式
//
// 模式 B: 嵌套 CPI（Nested / Aggregator CPI）
//   Jupiter → Raydium → Token Program
//   例: 聚合器 → DEX → Token Program（三层调用链）
//   特点: CPI 可以嵌套，形成调用树。Solana 限制最大 CPI 深度为 4 层
//
// 模式 C: 扇出 CPI（Fan-out / Multi-hop CPI）
//   Jupiter → [Raydium + Orca]（同一顶层指令触发多个并行的 CPI）
//   例: Jupiter 多跳路由 SOL → USDC → RAY，先走 Raydium 再走 Orca
//   特点: 同一个 InstructionIndex 下有多条内部指令，对应不同的 DEX 调用
//
// ============================================================================
// 生产代码（solwallet）如何使用展平后的指令
// ============================================================================
//
// 流程: flattenInstructions → 按 ProgramID 过滤 → 逐条解析
//
//   flatInsts := FlattenInstructions(tx.Instructions, tx.InnerInstructions)
//   for _, fi := range flatInsts {
//       handler, ok := registry[fi.ProgramID]
//       if !ok { continue }  // 跳过不关心的 Program
//       event := handler.Parse(fi.Data)
//       event.IsInner = fi.IsInner
//       event.ParentProgramID = fi.ParentProgramID
//       events = append(events, event)
//   }
//
// 这样无论交易是直接调用 DEX，还是通过 Jupiter 等聚合器间接调用，
// 都能统一解析出所有 swap 事件。
package main

import (
	"encoding/hex"
	"fmt"
	"log/slog"
)

// FlatInstruction 展平后的指令，包含 CPI 追踪元数据。
//
// 将 Solana 交易中的顶层指令（top-level）和内部指令（inner/CPI）
// 统一展平为一个有序列表，按实际执行顺序排列。
//
// 为什么需要这个结构？
//   - 原始交易数据中，顶层指令和内部指令是分开存储的（两个独立数组）
//   - 解析时需要按执行顺序遍历所有指令，不区分顶层/内部
//   - 但同时需要保留 CPI 关系信息（谁调用了谁），用于事件归因和调试
type FlatInstruction struct {
	// ProgramID 执行此指令的 Program 地址。
	// 例: "675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8"（Raydium AMM）
	ProgramID string

	// Data 指令数据（原始字节）。
	// Anchor 程序: 前 8 字节是 discriminator，后面是 Borsh 编码的参数
	// 原生程序: 前 1-4 字节是指令类型枚举，后面是参数
	Data []byte

	// Discriminator 指令判别器，用于区分同一 Program 的不同操作。
	// 例: Raydium 的 swap 和 addLiquidity 是不同的 discriminator。
	// 在 demo 中简化为字符串标识；生产中是从 Data 前 8 字节提取的 hex。
	Discriminator string

	// Index 在展平后列表中的位置（从 0 开始）。
	// 用于在事件中记录"这个事件是由第几条展平指令产生的"，方便调试和回溯。
	Index int

	// IsInner 是否是内部指令（CPI 调用产生的）。
	// true = 这条指令是某个顶层指令通过 CPI 间接触发的
	// false = 这是用户直接发起的顶层指令
	IsInner bool

	// ParentProgramID CPI 调用者的 ProgramID。
	// 顶层指令: 空字符串（没有父级）
	// 内部指令: 触发此 CPI 的顶层指令的 ProgramID
	//
	// 例: Jupiter 交易中，Raydium swap 的 ParentProgramID 是 Jupiter Program
	// 这个信息用于：
	//   1. 判断事件来源（"这笔 swap 是 Jupiter 路由过来的"）
	//   2. 聚合器归因（统计 Jupiter 带来了多少交易量）
	//   3. 调试（追踪 CPI 调用链）
	ParentProgramID string

	// Depth CPI 调用深度。
	// 0 = 顶层指令（用户直接调用）
	// 1 = 第一层 CPI（顶层指令调用的）
	// 2 = 第二层 CPI（嵌套 CPI，如 Jupiter → Raydium → Token Program）
	//
	// Solana 运行时限制最大 CPI 深度为 4 层。
	// 在本 demo 中简化处理，inner instructions 统一设为 depth=1。
	// 生产环境中需要根据 innerInstructions 的 stackHeight 字段精确计算。
	Depth int
}

// FlattenInstructions 将顶层指令和内部指令展平为统一的有序列表。
//
// 这是整个 CPI 解析的核心函数。
//
// 输入:
//   - topLevel: 交易的顶层指令列表（用户直接发起的指令）
//   - inner: 内部指令列表（每个元素关联一个顶层指令，包含该指令触发的所有 CPI 调用）
//
// 输出:
//   - 展平后的指令列表，按执行顺序排列
//
// 展平规则:
//  1. 遍历每条顶层指令（索引 i）
//  2. 将顶层指令加入结果列表（IsInner=false, Depth=0）
//  3. 在 inner 中查找 InstructionIndex == i 的条目
//  4. 将该条目下的所有内部指令依次加入结果列表（IsInner=true, Depth=1）
//  5. 重复直到所有顶层指令处理完毕
//
// 示例:
//
//	topLevel = [Jupiter指令, ComputeBudget指令]
//	inner = [{InstructionIndex: 0, Instructions: [Raydium.swap, Token.transfer, Orca.swap, Token.transfer]}]
//
//	展平结果:
//	  [0] Jupiter指令        (IsInner=false, Depth=0)
//	  [1] Raydium.swap       (IsInner=true,  Depth=1, Parent=Jupiter)
//	  [2] Token.transfer     (IsInner=true,  Depth=1, Parent=Jupiter)
//	  [3] Orca.swap          (IsInner=true,  Depth=1, Parent=Jupiter)
//	  [4] Token.transfer     (IsInner=true,  Depth=1, Parent=Jupiter)
//	  [5] ComputeBudget指令  (IsInner=false, Depth=0)
func FlattenInstructions(topLevel []MockInstruction, inner []MockInnerInstruction) []FlatInstruction {
	// 预构建 inner instructions 的索引映射，避免对每条顶层指令都遍历整个 inner 列表。
	// key: 顶层指令索引, value: 该顶层指令触发的内部指令列表
	//
	// 为什么用 map 而不是直接按索引访问？
	//   - inner 列表不一定包含所有顶层指令的条目（没有 CPI 的顶层指令不会出现在 inner 中）
	//   - inner 列表的顺序不一定与顶层指令索引一致
	//   - 可能存在多个条目对应同一个 InstructionIndex（虽然不常见，但防御性编程）
	innerMap := make(map[int][]MockInstruction, len(inner))
	for _, ii := range inner {
		innerMap[ii.InstructionIndex] = ii.Instructions
	}

	// 预估容量：顶层指令数 + 所有内部指令数（减少切片扩容开销）
	totalInner := 0
	for _, ii := range inner {
		totalInner += len(ii.Instructions)
	}
	result := make([]FlatInstruction, 0, len(topLevel)+totalInner)

	flatIndex := 0 // 全局展平索引，按执行顺序递增

	for i, inst := range topLevel {
		// 提取 discriminator（如果指令数据中包含的话）
		// 优先使用显式设置的 Discriminator 字段，否则从 Data 中自动提取
		disc := inst.Discriminator
		if disc == "" && len(inst.Data) > 0 {
			disc = ExtractDiscriminator(inst.Data)
		}

		// 步骤 1: 添加顶层指令
		result = append(result, FlatInstruction{
			ProgramID:       inst.ProgramID,
			Data:            inst.Data,
			Discriminator:   disc,
			Index:           flatIndex,
			IsInner:         false,           // 顶层指令，不是 CPI
			ParentProgramID: "",              // 顶层指令没有父级
			Depth:           0,               // 顶层 = 深度 0
		})
		flatIndex++

		slog.Debug("flatten: top-level instruction",
			"index", i,
			"flat_index", flatIndex-1,
			"program_id", inst.ProgramID,
			"discriminator", disc,
		)

		// 步骤 2: 查找并添加该顶层指令触发的所有内部指令（CPI 调用）
		innerInsts, hasInner := innerMap[i]
		if !hasInner {
			continue // 该顶层指令没有触发任何 CPI，跳过
		}

		for j, innerInst := range innerInsts {
			innerDisc := innerInst.Discriminator
			if innerDisc == "" && len(innerInst.Data) > 0 {
				innerDisc = ExtractDiscriminator(innerInst.Data)
			}

			result = append(result, FlatInstruction{
				ProgramID:       innerInst.ProgramID,
				Data:            innerInst.Data,
				Discriminator:   innerDisc,
				Index:           flatIndex,
				IsInner:         true,            // 这是 CPI 产生的内部指令
				ParentProgramID: inst.ProgramID,  // 父级是当前顶层指令的 Program
				Depth:           1,               // 简化处理：内部指令统一为深度 1
			})
			flatIndex++

			slog.Debug("flatten: inner instruction (CPI)",
				"top_index", i,
				"inner_index", j,
				"flat_index", flatIndex-1,
				"program_id", innerInst.ProgramID,
				"parent_program_id", inst.ProgramID,
				"discriminator", innerDisc,
			)
		}
	}

	slog.Debug("flatten complete",
		"top_level_count", len(topLevel),
		"inner_count", totalInner,
		"flat_total", len(result),
	)

	return result
}

// ExtractDiscriminator 从指令数据中提取判别器（discriminator）。
//
// Solana 程序通过指令数据的前几个字节来区分不同的操作类型：
//
// ============================================================================
// Anchor 框架（大多数现代 Solana 程序使用）
// ============================================================================
//   - 前 8 字节是 discriminator
//   - 计算方式: sha256("global:<method_name>")[:8]
//   - 例: sha256("global:swap")[:8] = [0xf8, 0xc6, 0x9e, 0x91, ...]
//   - 同一个 Program 的不同方法（swap / addLiquidity / removeLiquidity）有不同的 discriminator
//   - 这就是为什么 parser_registry.go 中支持 "ProgramID#discriminator" 的复合 key 匹配
//
// ============================================================================
// 原生程序（System Program / Token Program 等）
// ============================================================================
//   - 前 1 字节（或 1-4 字节）是指令类型枚举值
//   - 例: Token Program 的 Transfer 指令类型 = 3
//   - 比 Anchor 的 8 字节 discriminator 更紧凑
//
// ============================================================================
// 本函数的简化实现
// ============================================================================
//   - 如果数据 >= 8 字节: 返回前 8 字节的 hex 编码（假设是 Anchor discriminator）
//   - 如果数据 >= 1 字节但 < 8 字节: 返回前 1 字节的 hex 编码（假设是原生程序指令类型）
//   - 否则返回空字符串
//
// 生产环境中的改进:
//   - 维护已知 Program 的 discriminator → method name 映射表
//   - 对 Anchor 程序，可以从 IDL 文件自动生成映射
//   - 对原生程序，硬编码枚举值到操作名的映射
func ExtractDiscriminator(data []byte) string {
	if len(data) >= 8 {
		// Anchor 风格: 取前 8 字节作为 discriminator
		// 例: data = [0xf8, 0xc6, 0x9e, ...] → "f8c69e91..."（16 位 hex 字符串）
		return hex.EncodeToString(data[:8])
	}
	if len(data) >= 1 {
		// 原生程序风格: 取前 1 字节作为指令类型
		// 例: data = [0x03, ...] → "03"（Token Program Transfer）
		return hex.EncodeToString(data[:1])
	}
	return ""
}

// String 返回 FlatInstruction 的可读字符串表示，用于调试和日志输出。
func (fi FlatInstruction) String() string {
	innerLabel := "TOP"
	if fi.IsInner {
		innerLabel = fmt.Sprintf("CPI(depth=%d, parent=%s)", fi.Depth, truncateProgramID(fi.ParentProgramID))
	}
	return fmt.Sprintf("[%d] %s program=%s disc=%s",
		fi.Index, innerLabel, truncateProgramID(fi.ProgramID), fi.Discriminator)
}

// truncateProgramID 截断 ProgramID 用于日志显示，避免过长的 base58 地址影响可读性。
func truncateProgramID(pid string) string {
	if len(pid) > 12 {
		return pid[:6] + "..." + pid[len(pid)-4:]
	}
	return pid
}
