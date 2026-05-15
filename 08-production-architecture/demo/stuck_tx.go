// StuckTx 处理器 — 交易卡住检测和恢复。
// 对标 solwallet stucktx/ 模块的设计思路。
//
// ---- 为什么需要 StuckTx ----
//
// 交易广播到链上后，并不保证一定会被打包。以下情况会导致交易"卡住"：
//   - Gas/Priority Fee 太低：矿工/验证者优先打包高费率交易，低费率交易可能永远排队
//   - Nonce gap（EVM 特有）：如果 nonce=5 的交易失败，nonce=6、7、8... 全部卡住
//   - 网络拥堵：区块空间不足，低优先级交易被挤出 mempool
//   - Mempool 淘汰：节点 mempool 满时会丢弃最低价格的交易
//
// 如果不主动检测和处理，这些交易会一直停留在 Pending 状态，
// 占用系统资源（nonce 槽位、余额锁定），影响后续交易的发送。
//
// ---- Solana vs EVM 的超时差异 ----
//
// Solana:
//   - 交易包含 recent blockhash，约 60 秒（150 个 slot）后自动过期
//   - 过期后交易彻底无效，不会被打包
//   - 恢复策略：用新的 blockhash 重新构建并广播原交易（不需要修改内容）
//
// EVM:
//   - 交易没有内置过期机制，理论上可以在 mempool 中待无限久
//   - 只要 nonce 未被使用，交易始终有效
//   - 恢复策略：RBF（Replace-By-Fee），用相同 nonce + 更高 Gas 的新交易替换
//   - 或者发送一笔相同 nonce 的空交易（value=0，to=自己）来"取消"
//
// ---- 重试策略 ----
//
// Solana: 重新签名广播原交易（因为 blockhash 过期后原交易无效，需要新 blockhash）
// EVM:    通过 RBF 替换，每次提高 Gas（例如 1.3x），直到达到最大重试次数
//
// ---- 生产中的扫描策略 ----
//
// 不同交易类型有不同的超时阈值（因为紧急程度不同）：
//   - 提现交易：2 分钟（用户在等待，需要快速响应）
//   - Swap 交易：1 分钟（自动化交易，对时效性要求更高）
//   - 归集交易：5 分钟（内部操作，不那么紧急）
//   - 跨链桥交易：10 分钟（跨链本身就慢，给更多时间）
//
// 扫描间隔也应该根据链的出块速度调整：
//   - Solana: 10-15 秒（slot 时间 ~400ms，blockhash 有效期 ~60s）
//   - EVM L1: 30-60 秒（出块 ~12s，交易无自动过期）
//   - EVM L2: 15-30 秒（出块更快，但仍无自动过期）
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ---------------------------------------------------------------------------
// StuckTxConfig 配置
// ---------------------------------------------------------------------------

// StuckTxConfig 卡住交易检测器的配置参数。
type StuckTxConfig struct {
	ScanInterval     time.Duration // 扫描间隔（默认 30s）
	PendingTimeout   time.Duration // Pending 超时阈值（默认 2 分钟）
	MaxRetries       int           // 最大重试次数（默认 3）
	RBFGasMultiplier int64         // RBF Gas 乘数（基点，默认 13000 = 1.3x）
}

// DefaultStuckTxConfig 返回默认配置。
func DefaultStuckTxConfig() StuckTxConfig {
	return StuckTxConfig{
		ScanInterval:     30 * time.Second,
		PendingTimeout:   2 * time.Minute,
		MaxRetries:       3,
		RBFGasMultiplier: 13000, // 1.3x
	}
}

// ---------------------------------------------------------------------------
// StuckTxAction 处理动作
// ---------------------------------------------------------------------------

// StuckTxAction 描述对一笔卡住交易应采取的处理动作。
//
// Action 取值：
//   - "confirm"        — 链上已确认成功，更新状态为 Confirmed
//   - "confirm_failed" — 链上已确认失败，更新状态为 Failed
//   - "retry"          — 超时且重试次数未达上限，需要重发/RBF
//   - "drop"           — 超时且重试次数已达上限，标记为 Error 放弃处理
type StuckTxAction struct {
	TxHash    string              // 原始交易哈希
	Record    *dexwallet.TxRecord // 关联的交易记录
	Action    string              // 处理动作: "confirm" / "confirm_failed" / "retry" / "drop"
	Reason    string              // 动作原因（人可读描述）
	NewTxHash string              // RBF 替换后的新 tx hash（仅 "retry" 时可能有值）
}

// ---------------------------------------------------------------------------
// StuckTxDetector 核心检测器
// ---------------------------------------------------------------------------

// StuckTxDetector 卡住交易检测器。
// 定期扫描所有 Pending 状态的交易记录，检测是否已被链上确认或已超时，
// 并返回对应的处理动作列表，由调用方决定如何执行。
//
// 设计理念：检测器只负责"发现问题+给出建议"，不直接执行链上操作。
// 这样便于测试、便于不同链复用相同的检测逻辑。
type StuckTxDetector struct {
	config StuckTxConfig

	// 模拟依赖（生产中由具体链实现注入）
	getTxStatus  func(txHash string) dexwallet.TxStatus // 查询链上交易状态
	getRecords   func() []*dexwallet.TxRecord           // 获取所有 Pending 记录
	updateRecord func(record *dexwallet.TxRecord)       // 更新记录到存储

	// 重试计数器（因为 TxRecord 没有 RetryCount 字段，这里在内存中追踪）
	// 生产中应该持久化到数据库
	retryCounts map[string]int // txHash → 已重试次数
}

// NewStuckTxDetector 创建卡住交易检测器。
//
// 参数说明：
//   - config: 检测器配置
//   - getTxStatus: 链上状态查询函数（模拟 RPC 调用，如 eth_getTransactionReceipt）
//   - getRecords: 获取所有需要检查的 Pending 交易记录
//   - updateRecord: 更新交易记录到持久化存储
func NewStuckTxDetector(
	config StuckTxConfig,
	getTxStatus func(txHash string) dexwallet.TxStatus,
	getRecords func() []*dexwallet.TxRecord,
	updateRecord func(record *dexwallet.TxRecord),
) *StuckTxDetector {
	return &StuckTxDetector{
		config:       config,
		getTxStatus:  getTxStatus,
		getRecords:   getRecords,
		updateRecord: updateRecord,
		retryCounts:  make(map[string]int),
	}
}

// ScanOnce 执行一次完整扫描，返回需要处理的动作列表。
//
// 扫描逻辑：
//
//	遍历所有 Pending 记录：
//	  1. 先查链上状态（getTxStatus）
//	     - 如果链上已 Confirmed → Action="confirm"，更新状态
//	     - 如果链上已 Failed → Action="confirm_failed"，更新状态
//	  2. 如果链上仍 Pending，检查超时
//	     - 未超时 → 跳过（交易还在正常等待中）
//	     - 已超时 + 重试次数未达上限 → Action="retry"
//	     - 已超时 + 重试次数达上限 → Action="drop"，标记为 Error
func (d *StuckTxDetector) ScanOnce() []StuckTxAction {
	records := d.getRecords()
	if len(records) == 0 {
		return nil
	}

	var actions []StuckTxAction

	for _, record := range records {
		// 只处理 Pending 状态的交易
		if record.Status != dexwallet.TxStatusPending {
			continue
		}

		txHash := record.TxHash

		// 步骤 1: 查询链上实际状态
		onChainStatus := d.getTxStatus(txHash)

		switch onChainStatus {
		case dexwallet.TxStatusConfirmed:
			// 链上已确认成功 → 同步状态
			record.Status = dexwallet.TxStatusConfirmed
			record.UpdatedAt = time.Now()
			d.updateRecord(record)

			actions = append(actions, StuckTxAction{
				TxHash: txHash,
				Record: record,
				Action: "confirm",
				Reason: "链上已确认成功",
			})

			slog.Info("交易已在链上确认",
				"tx_hash", txHash,
				"status", "confirmed",
			)

		case dexwallet.TxStatusFailed:
			// 链上已确认失败 → 同步状态
			record.Status = dexwallet.TxStatusFailed
			record.UpdatedAt = time.Now()
			d.updateRecord(record)

			actions = append(actions, StuckTxAction{
				TxHash: txHash,
				Record: record,
				Action: "confirm_failed",
				Reason: "链上执行失败",
			})

			slog.Info("交易在链上执行失败",
				"tx_hash", txHash,
				"status", "failed",
			)

		default:
			// 链上仍然是 Pending → 检查是否超时
			action := d.checkTimeout(record)
			if action != nil {
				actions = append(actions, *action)
			}
		}
	}

	if len(actions) > 0 {
		slog.Info("StuckTx 扫描完成",
			"pending_count", len(records),
			"action_count", len(actions),
		)
	}

	return actions
}

// checkTimeout 检查一笔 Pending 交易是否超时，返回对应的处理动作。
// 如果未超时，返回 nil（无需处理）。
func (d *StuckTxDetector) checkTimeout(record *dexwallet.TxRecord) *StuckTxAction {
	txHash := record.TxHash
	elapsed := time.Since(record.CreatedAt)

	// 使用交易自身的 Timeout 设置（如果有），否则使用全局默认值
	timeout := d.config.PendingTimeout
	if record.Timeout > 0 {
		timeout = record.Timeout
	}

	// 未超时 → 跳过
	if elapsed < timeout {
		return nil
	}

	// 已超时 → 检查重试次数
	retryCount := d.retryCounts[txHash]

	if retryCount >= d.config.MaxRetries {
		// 重试次数已达上限 → 放弃，标记为 Error
		record.Status = dexwallet.TxStatusError
		record.ErrorMsg = fmt.Sprintf("Pending 超时（已等待 %v），重试 %d 次后仍未确认，放弃处理",
			elapsed.Round(time.Second), retryCount)
		record.UpdatedAt = time.Now()
		d.updateRecord(record)

		slog.Warn("交易超时且重试耗尽，标记为 Error",
			"tx_hash", txHash,
			"elapsed", elapsed.Round(time.Second),
			"retries", retryCount,
			"max_retries", d.config.MaxRetries,
		)

		return &StuckTxAction{
			TxHash: txHash,
			Record: record,
			Action: "drop",
			Reason: fmt.Sprintf("超时 %v，重试 %d/%d 次后放弃",
				elapsed.Round(time.Second), retryCount, d.config.MaxRetries),
		}
	}

	// 重试次数未达上限 → 重试
	d.retryCounts[txHash] = retryCount + 1

	slog.Info("交易超时，准备重试",
		"tx_hash", txHash,
		"elapsed", elapsed.Round(time.Second),
		"timeout", timeout,
		"retry", retryCount+1,
		"max_retries", d.config.MaxRetries,
		"rbf_multiplier", fmt.Sprintf("%.2fx", float64(d.config.RBFGasMultiplier)/10000.0),
	)

	return &StuckTxAction{
		TxHash: txHash,
		Record: record,
		Action: "retry",
		Reason: fmt.Sprintf("超时 %v，第 %d/%d 次重试（RBF %.2fx）",
			elapsed.Round(time.Second), retryCount+1, d.config.MaxRetries,
			float64(d.config.RBFGasMultiplier)/10000.0),
	}
}

// Run 启动定期扫描循环。
// 每隔 config.ScanInterval 执行一次 ScanOnce。
// 通过 ctx 控制生命周期，ctx 取消时退出循环。
//
// 生产中的使用模式：
//
//	detector := NewStuckTxDetector(config, ...)
//	go detector.Run(ctx)
func (d *StuckTxDetector) Run(ctx context.Context) {
	ticker := time.NewTicker(d.config.ScanInterval)
	defer ticker.Stop()

	slog.Info("StuckTx 检测器已启动",
		"scan_interval", d.config.ScanInterval,
		"pending_timeout", d.config.PendingTimeout,
		"max_retries", d.config.MaxRetries,
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("StuckTx 检测器已停止")
			return
		case <-ticker.C:
			actions := d.ScanOnce()
			// 生产中这里可以将 actions 发送到处理队列，
			// 或者直接调用对应的恢复逻辑（RBF/重新广播）。
			_ = actions
		}
	}
}

// GetRetryCount 返回某笔交易的当前重试次数。
// 用于外部查询和监控。
func (d *StuckTxDetector) GetRetryCount(txHash string) int {
	return d.retryCounts[txHash]
}

// ResetRetryCount 重置某笔交易的重试计数。
// 当交易被手动处理或 RBF 成功后，应重置计数。
func (d *StuckTxDetector) ResetRetryCount(txHash string) {
	delete(d.retryCounts, txHash)
}

