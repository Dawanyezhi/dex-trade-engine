package main

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"
)

// RBFAccelerator Replace-By-Fee 交易替换加速器。
// 用于 EVM 链上交易卡住时，通过提高 Gas 来替换原交易。
//
// RBF 的核心条件：
//  1. 新交易的 nonce 必须与原交易相同
//  2. 新交易的 gasPrice（或 maxFeePerGas）必须高于原交易
//  3. 不同客户端对最低涨幅有不同要求（通常 >= 10%）
type RBFAccelerator struct {
	mu           sync.Mutex
	maxMultiplier int64 // 最大 Gas 乘数（基点，13000 = 1.3x，20000 = 2.0x）
	minMultiplier int64 // 最小 Gas 乘数（基点，11000 = 1.1x）
}

// RBFResult RBF 替换结果。
type RBFResult struct {
	OriginalGas uint64 // 原始 Gas Price
	NewGas      uint64 // 新 Gas Price
	Multiplier  int64  // 实际使用的乘数（基点）
}

// NewRBFAccelerator 创建 RBF 加速器。
// maxMultiplier 以基点表示，20000 = 2.0x。
func NewRBFAccelerator(maxMultiplier int64) *RBFAccelerator {
	if maxMultiplier < 11000 {
		maxMultiplier = 20000 // 默认最大 2.0x
	}
	return &RBFAccelerator{
		maxMultiplier: maxMultiplier,
		minMultiplier: 11000, // 最低 1.1x（满足 Geth 的 10% 最低涨幅要求）
	}
}

// Accelerate 用更高 Gas 重新构建交易。
//
// originalTx: 原始交易数据（简化表示：前 8 字节为 Gas Price，其余为交易负载）
// gasMultiplier: Gas 乘数（基点），例如 13000 = 1.3x，15000 = 1.5x
//
// 返回新的交易数据和可能的错误。
func (a *RBFAccelerator) Accelerate(originalTx []byte, gasMultiplier int64) ([]byte, error) {
	a.mu.Lock()
	maxMul := a.maxMultiplier
	minMul := a.minMultiplier
	a.mu.Unlock()

	if len(originalTx) < 8 {
		return nil, fmt.Errorf("rbf: 交易数据太短，最少需要 8 字节")
	}

	// 验证乘数范围
	if gasMultiplier < minMul {
		return nil, fmt.Errorf("rbf: Gas 乘数 %d 低于最小值 %d（%.1fx）",
			gasMultiplier, minMul, float64(minMul)/10000.0)
	}
	if gasMultiplier > maxMul {
		return nil, fmt.Errorf("rbf: Gas 乘数 %d 超过最大值 %d（%.1fx）",
			gasMultiplier, maxMul, float64(maxMul)/10000.0)
	}

	// 从原始交易中提取 Gas Price（简化：前 8 字节）
	originalGas := binary.BigEndian.Uint64(originalTx[:8])
	if originalGas == 0 {
		return nil, fmt.Errorf("rbf: 原始 Gas Price 为 0")
	}

	// 计算新 Gas Price
	newGas := originalGas * uint64(gasMultiplier) / 10000

	// 确保新 Gas 确实更高
	if newGas <= originalGas {
		return nil, fmt.Errorf("rbf: 新 Gas %d 不高于原始 Gas %d", newGas, originalGas)
	}

	// 构建新交易（替换 Gas Price 部分）
	newTx := make([]byte, len(originalTx))
	copy(newTx, originalTx)
	binary.BigEndian.PutUint64(newTx[:8], newGas)

	slog.Info("RBF 加速交易",
		"original_gas", originalGas,
		"new_gas", newGas,
		"multiplier", fmt.Sprintf("%.2fx", float64(gasMultiplier)/10000.0),
	)

	return newTx, nil
}

// AccelerateResult 与 Accelerate 相同，但额外返回结果详情。
func (a *RBFAccelerator) AccelerateResult(originalTx []byte, gasMultiplier int64) ([]byte, *RBFResult, error) {
	if len(originalTx) < 8 {
		return nil, nil, fmt.Errorf("rbf: 交易数据太短")
	}

	originalGas := binary.BigEndian.Uint64(originalTx[:8])

	newTx, err := a.Accelerate(originalTx, gasMultiplier)
	if err != nil {
		return nil, nil, err
	}

	newGas := binary.BigEndian.Uint64(newTx[:8])

	return newTx, &RBFResult{
		OriginalGas: originalGas,
		NewGas:      newGas,
		Multiplier:  gasMultiplier,
	}, nil
}

// BuildMockTx 构建一个模拟交易数据，用于演示。
// gasPrice: Gas Price（单位 Gwei）
// payload: 交易负载（交易数据的剩余部分）
func BuildMockTx(gasPrice uint64, payload []byte) []byte {
	tx := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint64(tx[:8], gasPrice)
	copy(tx[8:], payload)
	return tx
}

// ExtractGasPrice 从模拟交易中提取 Gas Price。
func ExtractGasPrice(tx []byte) uint64 {
	if len(tx) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(tx[:8])
}
