// Package main 实现 Swap 交易构建引擎的演示。
// [dexwallet 通用层] -- SwapBuilderFactory 是链无关的工厂注册机制。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// SwapBuilderFactory SwapBuilder 的工厂注册表。
// 通过 DexID 注册和查找 SwapBuilder，支持运行时动态注册。
// 使用 sync.RWMutex 保护 builders map，实现并发安全的读写分离。
type SwapBuilderFactory struct {
	mu       sync.RWMutex
	builders map[dexwallet.DexID]dexwallet.SwapBuilder
}

// NewSwapBuilderFactory 创建新的 SwapBuilderFactory。
func NewSwapBuilderFactory() *SwapBuilderFactory {
	return &SwapBuilderFactory{
		builders: make(map[dexwallet.DexID]dexwallet.SwapBuilder),
	}
}

// Register 注册一个 SwapBuilder。
// 如果已存在相同 DexID 的 builder，会覆盖。
func (f *SwapBuilderFactory) Register(builder dexwallet.SwapBuilder) {
	f.mu.Lock()
	defer f.mu.Unlock()

	dexID := builder.DexID()
	f.builders[dexID] = builder
	slog.Info("swap builder registered",
		"dex_id", dexID,
		"chain_id", builder.ChainID(),
		"protocol", builder.ProtocolType(),
	)
}

// Get 根据 DexID 获取对应的 SwapBuilder。
// 如果不存在，返回错误。
func (f *SwapBuilderFactory) Get(dexID dexwallet.DexID) (dexwallet.SwapBuilder, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	builder, ok := f.builders[dexID]
	if !ok {
		return nil, fmt.Errorf("swap builder not found for dex: %s", dexID)
	}
	return builder, nil
}

// Build 根据请求中的 DexID 选择对应的 SwapBuilder 并构建交易。
// 如果请求中未指定 DexID，返回错误（DexID 的选择应由 Aggregator 完成）。
func (f *SwapBuilderFactory) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	if req.DexID == "" {
		return nil, fmt.Errorf("dex_id is required, use Aggregator to select DEX")
	}

	builder, err := f.Get(req.DexID)
	if err != nil {
		return nil, fmt.Errorf("get builder: %w", err)
	}

	// 验证链 ID 匹配
	if builder.ChainID() != req.ChainID {
		return nil, fmt.Errorf("chain mismatch: builder supports %s, request is for %s",
			builder.ChainID(), req.ChainID)
	}

	slog.Info("building swap",
		"dex_id", req.DexID,
		"chain_id", req.ChainID,
		"direction", req.Direction,
		"from", req.FromToken.Symbol,
		"to", req.ToToken.Symbol,
		"amount", req.Amount.String(),
	)

	result, err := builder.Build(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("build swap on %s: %w", req.DexID, err)
	}

	return result, nil
}

// ListBuilders 列出所有已注册的 builder 信息（用于调试）。
func (f *SwapBuilderFactory) ListBuilders() []dexwallet.DexID {
	f.mu.RLock()
	defer f.mu.RUnlock()

	ids := make([]dexwallet.DexID, 0, len(f.builders))
	for id := range f.builders {
		ids = append(ids, id)
	}
	return ids
}
