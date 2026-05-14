package dexwallet

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// TxStatusChecker 交易状态查询函数（由链特定层注入）。
// 返回当前状态；生产中查询链上 getSignatureStatuses / eth_getTransactionReceipt。
type TxStatusChecker func(ctx context.Context, txHash string) (TxStatus, error)

// BaseTxSender 通用交易发送器。
// [dexwallet 通用层] — 多通道并发发送、超时重试对所有链一致。
type BaseTxSender struct {
	mu       sync.Mutex
	rpc      RPCClient
	bribes   []BribeService
	repo     Repository
	confirms int // 确认数要求

	// P1-1: 可注入的状态查询函数，替代虚假的直接返回 Confirmed
	statusChecker TxStatusChecker

	// 配置
	sendTimeout    time.Duration
	confirmTimeout time.Duration
	maxRetries     int
}

// NewBaseTxSender 创建通用交易发送器。
func NewBaseTxSender(rpc RPCClient, repo Repository, confirms int) *BaseTxSender {
	return &BaseTxSender{
		rpc:            rpc,
		repo:           repo,
		confirms:       confirms,
		sendTimeout:    10 * time.Second,
		confirmTimeout: 60 * time.Second,
		maxRetries:     3,
	}
}

// SetStatusChecker 注入交易状态查询函数（链特定实现）。
func (s *BaseTxSender) SetStatusChecker(checker TxStatusChecker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusChecker = checker
}

// AddBribeService 添加贿赂服务通道。
func (s *BaseTxSender) AddBribeService(b BribeService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bribes = append(s.bribes, b)
}

// Send 多通道并发发送交易（RPC + 贿赂服务），取第一个成功的结果。
func (s *BaseTxSender) Send(ctx context.Context, txData []byte) (string, error) {
	s.mu.Lock()
	bribes := make([]BribeService, len(s.bribes))
	copy(bribes, s.bribes)
	s.mu.Unlock()

	sendCtx, cancel := context.WithTimeout(ctx, s.sendTimeout)
	defer cancel()

	type result struct {
		txHash  string
		channel string
		err     error
	}

	// 通道数 = RPC + 贿赂服务数
	ch := make(chan result, 1+len(bribes))

	// 通道 1：标准 RPC 发送
	go func() {
		hash, err := s.rpc.SendTransaction(sendCtx, txData)
		ch <- result{txHash: hash, channel: "rpc", err: err}
	}()

	// 通道 2-N：贿赂服务发送
	// P0-3: 先获取每个贿赂服务的推荐费用，而非传 nil
	for _, b := range bribes {
		go func(bribe BribeService) {
			fee, feeErr := bribe.GetRecommendedFee(sendCtx)
			if feeErr != nil {
				slog.Debug("get recommended fee failed, using zero",
					"service", bribe.Name(), "error", feeErr)
			}
			hash, err := bribe.Send(sendCtx, txData, fee)
			ch <- result{txHash: hash, channel: bribe.Name(), err: err}
		}(b)
	}

	// 取第一个成功的结果
	totalChannels := 1 + len(bribes)
	var lastErr error
	for i := 0; i < totalChannels; i++ {
		select {
		case r := <-ch:
			if r.err == nil {
				slog.Info("tx sent successfully",
					"channel", r.channel,
					"tx_hash", r.txHash,
				)
				return r.txHash, nil
			}
			lastErr = r.err
			slog.Debug("send channel failed",
				"channel", r.channel,
				"error", r.err,
			)
		case <-sendCtx.Done():
			return "", fmt.Errorf("send timeout: %w", sendCtx.Err())
		}
	}

	return "", fmt.Errorf("all send channels failed, last error: %w", lastErr)
}

// Confirm 等待交易确认。
// P1-1: 使用注入的 statusChecker 查询实际状态，而非总是返回 Confirmed。
func (s *BaseTxSender) Confirm(ctx context.Context, txHash string) (TxStatus, error) {
	confirmCtx, cancel := context.WithTimeout(ctx, s.confirmTimeout)
	defer cancel()

	s.mu.Lock()
	checker := s.statusChecker
	s.mu.Unlock()

	// 若未注入 checker，默认返回 Confirmed（兼容 demo 场景）
	if checker == nil {
		slog.Debug("no status checker configured, assuming confirmed", "tx_hash", txHash)
		return TxStatusConfirmed, nil
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-confirmCtx.Done():
			if s.repo != nil {
				_ = s.repo.UpdateTxStatus(ctx, txHash, TxStatusTimeout)
			}
			return TxStatusTimeout, fmt.Errorf("confirm timeout for tx %s", txHash)

		case <-ticker.C:
			status, err := checker(confirmCtx, txHash)
			if err != nil {
				slog.Debug("status check failed, retrying", "tx_hash", txHash, "error", err)
				continue
			}
			if status == TxStatusConfirmed || status == TxStatusFailed {
				if s.repo != nil {
					_ = s.repo.UpdateTxStatus(ctx, txHash, status)
				}
				return status, nil
			}
			slog.Debug("tx still pending", "tx_hash", txHash, "status", status)
		}
	}
}

// Retry 重试发送交易。
func (s *BaseTxSender) Retry(ctx context.Context, txHash string) (string, error) {
	// 在实际实现中，这里会用更高的 Gas/优先费重新构建并发送交易
	// demo 简化为记录日志
	slog.Warn("tx retry requested", "tx_hash", txHash)
	return "", fmt.Errorf("retry not implemented in demo")
}
