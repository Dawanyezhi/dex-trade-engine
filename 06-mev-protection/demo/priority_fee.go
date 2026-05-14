package main

import (
	"fmt"
	"sort"
	"sync"
)

// PriorityFeeRecommender 基于历史数据的优先费推荐器。
// 收集最近 N 个区块的优先费样本，按百分位数推荐合适的优先费。
type PriorityFeeRecommender struct {
	mu         sync.RWMutex
	samples    []uint64 // 最近 N 个区块的优先费样本
	maxSamples int      // 最大样本数
}

// NewPriorityFeeRecommender 创建优先费推荐器。
// maxSamples 控制样本窗口大小，建议 20-150。
func NewPriorityFeeRecommender(maxSamples int) *PriorityFeeRecommender {
	if maxSamples <= 0 {
		maxSamples = 50
	}
	return &PriorityFeeRecommender{
		samples:    make([]uint64, 0, maxSamples),
		maxSamples: maxSamples,
	}
}

// AddSample 添加一个优先费样本（来自最新区块）。
// 当样本数超过 maxSamples 时，丢弃最旧的样本。
func (r *PriorityFeeRecommender) AddSample(fee uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.samples = append(r.samples, fee)
	if len(r.samples) > r.maxSamples {
		// 丢弃最旧的样本
		r.samples = r.samples[len(r.samples)-r.maxSamples:]
	}
}

// AddSamples 批量添加优先费样本。
func (r *PriorityFeeRecommender) AddSamples(fees []uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.samples = append(r.samples, fees...)
	if len(r.samples) > r.maxSamples {
		r.samples = r.samples[len(r.samples)-r.maxSamples:]
	}
}

// Recommend 返回推荐的优先费。
// level 取值：
//   - "low"    -- 25th percentile（省钱，可能排队较久）
//   - "medium" -- 50th percentile（平衡选择）
//   - "high"   -- 75th percentile（优先处理）
//
// 如果样本为空，返回 0。
func (r *PriorityFeeRecommender) Recommend(level string) uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.samples) == 0 {
		return 0
	}

	// 复制并排序样本
	sorted := make([]uint64, len(r.samples))
	copy(sorted, r.samples)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	var percentile int
	switch level {
	case "low":
		percentile = 25
	case "medium":
		percentile = 50
	case "high":
		percentile = 75
	default:
		percentile = 50
	}

	return r.getPercentile(sorted, percentile)
}

// getPercentile 从已排序的切片中获取指定百分位的值。
func (r *PriorityFeeRecommender) getPercentile(sorted []uint64, percentile int) uint64 {
	if len(sorted) == 0 {
		return 0
	}

	// 计算索引：index = len * percentile / 100
	idx := len(sorted) * percentile / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}

	return sorted[idx]
}

// SampleCount 返回当前样本数量。
func (r *PriorityFeeRecommender) SampleCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.samples)
}

// String 格式化推荐结果为可读字符串。
func (r *PriorityFeeRecommender) String() string {
	low := r.Recommend("low")
	medium := r.Recommend("medium")
	high := r.Recommend("high")
	return fmt.Sprintf("优先费推荐 [样本数: %d] -- 低: %d, 中: %d, 高: %d",
		r.SampleCount(), low, medium, high)
}
