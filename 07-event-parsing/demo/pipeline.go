// Pipeline Stage 解析架构
// 灵感来源：evmwallet 的 10+ 阶段设计（ParseStage → ValidateStage → ClassifyStage → ...）
//
// 核心思想：将传统的"扁平解析"替换为链式处理管道。
// 每个阶段（Stage）独立实现一个关注点，通过组合实现灵活的解析流水线。
//
// 与 evmwallet 生产代码的对应关系：
//   - FailedTxFilterStage     → evmwallet ParseStage 中的 status 检查
//   - BalanceValidationStage  → evmwallet ValidateBalanceStage（对比 pre/post 余额）
//   - ClassificationStage     → irwallet ClassifyStage（按 sender/receiver 分类交易）
//   - AddressFilterStage      → evmwallet FilterStage（过滤非监控地址的事件）
//   - DeduplicationStage      → evmwallet DeduplicateStage（同一 tx 的多条日志可能重复）
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// PipelineStage 管道阶段接口。
type PipelineStage interface {
	Name() string
	Process(ctx context.Context, events []dexwallet.ChainEvent) ([]dexwallet.ChainEvent, error)
	Required() bool
}

// PipelineStats 管道执行统计信息。
type PipelineStats struct {
	StagesRun      int
	StagesFailed   int
	EventsIn       int
	EventsOut      int
	StageDurations map[string]time.Duration
}

// Pipeline 事件处理管道。
type Pipeline struct {
	stages []PipelineStage
}

func NewPipeline() *Pipeline                          { return &Pipeline{stages: make([]PipelineStage, 0)} }
func (p *Pipeline) AddStage(s PipelineStage) *Pipeline { p.stages = append(p.stages, s); return p }

func (p *Pipeline) Run(ctx context.Context, events []dexwallet.ChainEvent) ([]dexwallet.ChainEvent, PipelineStats, error) {
	stats := PipelineStats{EventsIn: len(events), StageDurations: make(map[string]time.Duration)}
	current := events
	for _, stage := range p.stages {
		select {
		case <-ctx.Done():
			stats.EventsOut = len(current)
			return current, stats, ctx.Err()
		default:
		}
		start := time.Now()
		result, err := stage.Process(ctx, current)
		stats.StagesRun++
		stats.StageDurations[stage.Name()] = time.Since(start)
		if err != nil {
			stats.StagesFailed++
			if stage.Required() {
				stats.EventsOut = len(current)
				return nil, stats, fmt.Errorf("pipeline stage %q failed: %w", stage.Name(), err)
			}
			slog.Warn("pipeline stage failed (non-required)", "stage", stage.Name(), "error", err)
			continue
		}
		current = result
	}
	stats.EventsOut = len(current)
	return current, stats, nil
}

// --- Stage 1: FailedTxFilterStage ---

type FailedTxFilterStage struct{}

func NewFailedTxFilterStage() *FailedTxFilterStage { return &FailedTxFilterStage{} }
func (s *FailedTxFilterStage) Name() string        { return "FailedTxFilter" }
func (s *FailedTxFilterStage) Required() bool      { return false }

func (s *FailedTxFilterStage) Process(_ context.Context, events []dexwallet.ChainEvent) ([]dexwallet.ChainEvent, error) {
	result := make([]dexwallet.ChainEvent, 0, len(events))
	for i := range events {
		if events[i].Success {
			result = append(result, events[i])
		}
	}
	return result, nil
}

// --- Stage 2: BalanceValidationStage ---

type BalanceValidationStage struct {
	pre  []MockTokenBalance
	post []MockTokenBalance
}

func NewBalanceValidationStage(pre, post []MockTokenBalance) *BalanceValidationStage {
	return &BalanceValidationStage{pre: pre, post: post}
}

func (s *BalanceValidationStage) Name() string   { return "BalanceValidation" }
func (s *BalanceValidationStage) Required() bool { return false }

func (s *BalanceValidationStage) Process(_ context.Context, events []dexwallet.ChainEvent) ([]dexwallet.ChainEvent, error) {
	if len(s.pre) == 0 && len(s.post) == 0 {
		return events, nil
	}
	calc := NewBalanceDiffCalculator()
	diffs := calc.Calculate(s.pre, s.post)
	for i := range events {
		v := calc.Validate(diffs, &events[i])
		if !v.IsValid {
			slog.Warn("balance validation mismatch", "tx", events[i].TxHash, "discrepancy", v.Discrepancy)
		}
	}
	return events, nil
}

// --- Stage 3: ClassificationStage ---

type ClassificationStage struct {
	classifier *TxClassifier
}

func NewClassificationStage(lookup func(string) AddressType) *ClassificationStage {
	return &ClassificationStage{classifier: NewTxClassifier(lookup)}
}

func (s *ClassificationStage) Name() string   { return "Classification" }
func (s *ClassificationStage) Required() bool { return true }

func (s *ClassificationStage) Process(_ context.Context, events []dexwallet.ChainEvent) ([]dexwallet.ChainEvent, error) {
	return s.classifier.Classify(events), nil
}

// --- Stage 4: AddressFilterStage ---

type AddressFilterStage struct {
	filter *AddressFilter
}

func NewAddressFilterStage(filter *AddressFilter) *AddressFilterStage {
	return &AddressFilterStage{filter: filter}
}

func (s *AddressFilterStage) Name() string   { return "AddressFilter" }
func (s *AddressFilterStage) Required() bool { return true }

func (s *AddressFilterStage) Process(_ context.Context, events []dexwallet.ChainEvent) ([]dexwallet.ChainEvent, error) {
	result := make([]dexwallet.ChainEvent, 0, len(events))
	for i := range events {
		e := &events[i]
		if s.filter.Contains(e.Sender) || s.filter.Contains(e.Receiver) ||
			s.filter.Contains(e.TokenIn) || s.filter.Contains(e.TokenOut) {
			result = append(result, events[i])
		}
	}
	return result, nil
}

// --- Stage 5: DeduplicationStage ---

type DeduplicationStage struct{}

func NewDeduplicationStage() *DeduplicationStage { return &DeduplicationStage{} }
func (s *DeduplicationStage) Name() string       { return "Deduplication" }
func (s *DeduplicationStage) Required() bool     { return false }

func (s *DeduplicationStage) Process(_ context.Context, events []dexwallet.ChainEvent) ([]dexwallet.ChainEvent, error) {
	seen := make(map[string]struct{}, len(events))
	result := make([]dexwallet.ChainEvent, 0, len(events))
	for i := range events {
		key := fmt.Sprintf("%s:%s:%s", events[i].TxHash, events[i].Type, events[i].Pool)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, events[i])
	}
	return result, nil
}

var _ PipelineStage = (*FailedTxFilterStage)(nil)
var _ PipelineStage = (*BalanceValidationStage)(nil)
var _ PipelineStage = (*ClassificationStage)(nil)
var _ PipelineStage = (*AddressFilterStage)(nil)
var _ PipelineStage = (*DeduplicationStage)(nil)
