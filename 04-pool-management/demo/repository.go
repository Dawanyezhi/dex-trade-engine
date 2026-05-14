// Package main 实现流动性池管理的演示。
// [demo 层] -- MemoryPoolRepository 是 dexwallet.Repository 接口的内存实现。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"sync"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// MemoryPoolRepository 基于内存的池子和交易记录存储。
// 实现 dexwallet.Repository 接口的所有方法。
// 使用 sync.RWMutex 保证并发安全。
//
// 生产系统中 Repository 通常基于 MySQL/PostgreSQL + Redis。
// 这里用内存 map 模拟，接口一致但数据不持久化。
type MemoryPoolRepository struct {
	mu    sync.RWMutex
	pools map[string]*dexwallet.Pool     // address -> Pool
	txs   map[string]*dexwallet.TxRecord // txHash -> TxRecord
}

// NewMemoryPoolRepository 创建内存存储实例。
func NewMemoryPoolRepository() *MemoryPoolRepository {
	return &MemoryPoolRepository{
		pools: make(map[string]*dexwallet.Pool),
		txs:   make(map[string]*dexwallet.TxRecord),
	}
}

// SavePool 保存池子数据到内存。
// 如果池子已存在，覆盖更新。
func (r *MemoryPoolRepository) SavePool(_ context.Context, pool *dexwallet.Pool) error {
	if pool == nil {
		return fmt.Errorf("pool is nil")
	}
	if pool.Address == "" {
		return fmt.Errorf("pool address is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 存副本，避免外部修改影响存储数据
	copied := copyPool(pool)
	r.pools[pool.Address] = copied

	slog.Debug("pool saved to repository",
		"address", pool.Address,
		"dex_id", pool.DexID,
		"state", pool.State,
	)
	return nil
}

// GetPool 从内存中获取池子数据。
func (r *MemoryPoolRepository) GetPool(_ context.Context, address string) (*dexwallet.Pool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pool, ok := r.pools[address]
	if !ok {
		return nil, fmt.Errorf("pool not found: %s", address)
	}

	// 返回副本，避免外部修改影响存储数据
	return copyPool(pool), nil
}

// SaveTxRecord 保存交易记录到内存。
func (r *MemoryPoolRepository) SaveTxRecord(_ context.Context, record *dexwallet.TxRecord) error {
	if record == nil {
		return fmt.Errorf("tx record is nil")
	}
	if record.TxHash == "" {
		return fmt.Errorf("tx hash is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.txs[record.TxHash] = record

	slog.Debug("tx record saved to repository",
		"tx_hash", record.TxHash,
		"status", record.Status,
	)
	return nil
}

// GetTxRecord 从内存中获取交易记录。
func (r *MemoryPoolRepository) GetTxRecord(_ context.Context, txHash string) (*dexwallet.TxRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	record, ok := r.txs[txHash]
	if !ok {
		return nil, fmt.Errorf("tx record not found: %s", txHash)
	}
	return record, nil
}

// UpdateTxStatus 更新交易状态。
func (r *MemoryPoolRepository) UpdateTxStatus(_ context.Context, txHash string, status dexwallet.TxStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.txs[txHash]
	if !ok {
		return fmt.Errorf("tx record not found: %s", txHash)
	}
	record.Status = status

	slog.Debug("tx status updated",
		"tx_hash", txHash,
		"new_status", status,
	)
	return nil
}

// ListPools 列出所有存储的池子（调试用）。
func (r *MemoryPoolRepository) ListPools() []*dexwallet.Pool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pools := make([]*dexwallet.Pool, 0, len(r.pools))
	for _, p := range r.pools {
		pools = append(pools, copyPool(p))
	}
	return pools
}

// PoolCount 返回存储的池子数量。
func (r *MemoryPoolRepository) PoolCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.pools)
}

// copyPool 创建 Pool 的浅拷贝。
// Liquidity 使用 big.Int，需要独立拷贝以避免共享引用。
func copyPool(p *dexwallet.Pool) *dexwallet.Pool {
	if p == nil {
		return nil
	}
	copied := *p
	if p.Liquidity != nil {
		copied.Liquidity = new(big.Int).Set(p.Liquidity)
	}
	if p.Extra != nil {
		copied.Extra = make(map[string]interface{}, len(p.Extra))
		for k, v := range p.Extra {
			copied.Extra[k] = v
		}
	}
	return &copied
}
