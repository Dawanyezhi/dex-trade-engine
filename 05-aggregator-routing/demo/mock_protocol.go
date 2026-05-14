package main

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// MockDexProtocol -- 模拟 DEX 协议，实现 DexProtocol 接口
// ============================================================

// MockDexProtocol 模拟 DEX 协议。
// 通过配置输出比例、延迟、是否失败来模拟不同 DEX 的行为。
type MockDexProtocol struct {
	dexID       dexwallet.DexID
	protocol    dexwallet.ProtocolType
	outputRatio *big.Int      // 输出比例（输入金额 * outputRatio / 100）
	latency     time.Duration // 模拟延迟
	shouldFail  bool          // 是否模拟失败
}

// NewMockDexProtocol 创建 Mock DEX 协议。
// outputRatioPercent: 输出比例百分比，例如 95 表示输出 = 输入 * 95 / 100。
func NewMockDexProtocol(dexID dexwallet.DexID, protocol dexwallet.ProtocolType, outputRatioPercent int64, latency time.Duration, shouldFail bool) *MockDexProtocol {
	return &MockDexProtocol{
		dexID:       dexID,
		protocol:    protocol,
		outputRatio: big.NewInt(outputRatioPercent),
		latency:     latency,
		shouldFail:  shouldFail,
	}
}

// Quote 获取报价。
func (m *MockDexProtocol) Quote(ctx context.Context, pool *dexwallet.Pool, amountIn *big.Int, direction dexwallet.SwapDirection) (*dexwallet.Quote, error) {
	// 模拟延迟
	select {
	case <-time.After(m.latency):
	case <-ctx.Done():
		return nil, fmt.Errorf("quote timeout for %s: %w", m.dexID, ctx.Err())
	}

	// 模拟失败
	if m.shouldFail {
		return nil, fmt.Errorf("mock quote failed for %s", m.dexID)
	}

	// 计算输出金额: amountIn * outputRatio / 100
	outputAmount := new(big.Int).Mul(amountIn, m.outputRatio)
	outputAmount.Div(outputAmount, big.NewInt(100))

	// 计算价格影响（模拟：越大的交易影响越大）
	// 简化为固定值：100 - outputRatio 个基点
	priceImpact := uint64(100) - uint64(m.outputRatio.Int64())

	// 构建 Gas 估算
	estimatedGas := big.NewInt(50000) // 固定模拟值

	quote := &dexwallet.Quote{
		DexID:        m.dexID,
		ProtocolType: m.protocol,
		InputAmount:  new(big.Int).Set(amountIn),
		OutputAmount: outputAmount,
		PriceImpact:  priceImpact * 100, // 转换为基点
		Pool:         pool,
		EstimatedGas: estimatedGas,
	}

	return quote, nil
}

// GetPrice 获取当前价格。
func (m *MockDexProtocol) GetPrice(ctx context.Context, pool *dexwallet.Pool) (*big.Int, error) {
	if m.shouldFail {
		return nil, fmt.Errorf("mock get price failed for %s", m.dexID)
	}
	// 模拟价格: outputRatio / 100 * 10^9 (以 lamports 精度表示)
	price := new(big.Int).Mul(m.outputRatio, big.NewInt(1e7)) // outputRatio * 10^7 = 价格（10^9 精度）
	return price, nil
}

// DexID 返回 DEX 标识。
func (m *MockDexProtocol) DexID() dexwallet.DexID {
	return m.dexID
}

// ProtocolType 返回协议类型。
func (m *MockDexProtocol) ProtocolType() dexwallet.ProtocolType {
	return m.protocol
}

// SetShouldFail 设置是否模拟失败（用于测试场景切换）。
func (m *MockDexProtocol) SetShouldFail(fail bool) {
	m.shouldFail = fail
}

// SetOutputRatio 设置输出比例（用于测试场景切换）。
func (m *MockDexProtocol) SetOutputRatio(ratioPercent int64) {
	m.outputRatio = big.NewInt(ratioPercent)
}

// SetLatency 设置延迟（用于测试场景切换）。
func (m *MockDexProtocol) SetLatency(d time.Duration) {
	m.latency = d
}

// ============================================================
// MockSwapBuilder -- 模拟 SwapBuilder，实现 SwapBuilder 接口
// ============================================================

// MockSwapBuilder 模拟 Swap 交易构建器。
type MockSwapBuilder struct {
	dexID      dexwallet.DexID
	chainID    coinset.ChainID
	protocol   dexwallet.ProtocolType
	shouldFail bool
}

// NewMockSwapBuilder 创建 Mock SwapBuilder。
func NewMockSwapBuilder(dexID dexwallet.DexID, chainID coinset.ChainID, protocol dexwallet.ProtocolType, shouldFail bool) *MockSwapBuilder {
	return &MockSwapBuilder{
		dexID:      dexID,
		chainID:    chainID,
		protocol:   protocol,
		shouldFail: shouldFail,
	}
}

// Build 构建 Swap 交易。
func (b *MockSwapBuilder) Build(ctx context.Context, req dexwallet.SwapRequest) (*dexwallet.SwapResult, error) {
	if b.shouldFail {
		return nil, fmt.Errorf("mock build failed for %s", b.dexID)
	}

	if req.Amount == nil || req.Amount.Sign() <= 0 {
		return nil, fmt.Errorf("invalid amount for %s", b.dexID)
	}

	// 模拟输出金额（95% 的输入金额）
	outputAmount := new(big.Int).Mul(req.Amount, big.NewInt(95))
	outputAmount.Div(outputAmount, big.NewInt(100))

	// 计算最小输出
	minOutput := new(big.Int).Mul(outputAmount, big.NewInt(int64(10000-req.SlippageBps)))
	minOutput.Div(minOutput, big.NewInt(10000))

	return &dexwallet.SwapResult{
		DexID:        b.dexID,
		ChainID:      b.chainID,
		Direction:    req.Direction,
		InputAmount:  new(big.Int).Set(req.Amount),
		OutputAmount: outputAmount,
		MinOutput:    minOutput,
		SlippageBps:  req.SlippageBps,
		GasCost:      big.NewInt(50000),
		TxData:       []byte(fmt.Sprintf("mock_tx_%s_%s", b.dexID, req.Amount.String())),
	}, nil
}

// DexID 返回支持的 DEX 标识。
func (b *MockSwapBuilder) DexID() dexwallet.DexID {
	return b.dexID
}

// ChainID 返回支持的链标识。
func (b *MockSwapBuilder) ChainID() coinset.ChainID {
	return b.chainID
}

// ProtocolType 返回协议类型。
func (b *MockSwapBuilder) ProtocolType() dexwallet.ProtocolType {
	return b.protocol
}

// SetShouldFail 设置是否模拟失败。
func (b *MockSwapBuilder) SetShouldFail(fail bool) {
	b.shouldFail = fail
}
