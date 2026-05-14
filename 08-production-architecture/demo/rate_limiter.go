// 08-production-architecture 限流与背压组件。
// 实现令牌桶限流器、Worker 池和带限流的 Swap 请求队列。
//
// [dexwallet 通用层] -- 限流逻辑对所有链一致，只有参数不同。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// TokenBucketLimiter 令牌桶限流器
// ---------------------------------------------------------------------------

// TokenBucketLimiter 令牌桶限流器。
// [dexwallet 通用层] -- 限流逻辑对所有链一致。
type TokenBucketLimiter struct {
	mu         sync.Mutex
	tokens     float64
	capacity   float64   // 桶容量
	refillRate float64   // 每秒填充速率
	lastRefill time.Time
}

// NewTokenBucketLimiter 创建令牌桶限流器。
// capacity 是桶的最大容量（决定允许的突发量），refillRate 是每秒填充速率（决定长期平均速率）。
func NewTokenBucketLimiter(capacity float64, refillRate float64) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		tokens:     capacity, // 初始状态桶是满的
		capacity:   capacity,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

// refill 惰性填充令牌。
// 不使用定时器，而是在每次调用时根据经过的时间计算应补充的令牌数。
// 调用方必须持有 mu 锁。
func (l *TokenBucketLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	tokensToAdd := elapsed * l.refillRate
	l.tokens += tokensToAdd
	if l.tokens > l.capacity {
		l.tokens = l.capacity
	}
	l.lastRefill = now
}

// Allow 尝试消费一个令牌，非阻塞。
// 如果有可用令牌，消费并返回 true；否则返回 false。
// 适用于快速失败场景（如 API 网关直接拒绝超限请求）。
func (l *TokenBucketLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()

	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}

// Wait 阻塞等待直到有令牌可用或 context 被取消。
// 适用于队列消费者场景（愿意等待但不想超时太久）。
func (l *TokenBucketLimiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		l.refill()

		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}

		// 计算等待时间：需要 1 个令牌，按 refillRate 计算
		waitDuration := time.Duration(float64(time.Second) / l.refillRate)
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return fmt.Errorf("rate limiter wait cancelled: %w", ctx.Err())
		case <-time.After(waitDuration):
			// 等待后重试
		}
	}
}

// Stats 返回当前令牌数和桶容量。
func (l *TokenBucketLimiter) Stats() (available float64, capacity float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refill()
	return l.tokens, l.capacity
}

// ---------------------------------------------------------------------------
// WorkerPool 工作池
// ---------------------------------------------------------------------------

// WorkerPool 工作池（限制并发 worker 数）。
// 使用缓冲 channel 作为信号量，控制同时执行的 goroutine 数量。
type WorkerPool struct {
	sem     chan struct{}
	maxSize int
}

// NewWorkerPool 创建工作池。
// maxSize 是最大并发 worker 数。
func NewWorkerPool(maxSize int) *WorkerPool {
	return &WorkerPool{
		sem:     make(chan struct{}, maxSize),
		maxSize: maxSize,
	}
}

// Submit 提交任务到工作池。
// 如果 Worker 池已满，会阻塞等待直到有空闲 Worker 或 context 被取消。
func (p *WorkerPool) Submit(ctx context.Context, task func()) error {
	select {
	case p.sem <- struct{}{}:
		// 获取到 Worker 许可
		go func() {
			defer func() { <-p.sem }() // 任务完成后释放许可
			task()
		}()
		return nil
	case <-ctx.Done():
		return fmt.Errorf("worker pool submit cancelled: %w", ctx.Err())
	}
}

// TrySubmit 非阻塞提交任务。
// 如果 Worker 池已满，立即返回 false。
func (p *WorkerPool) TrySubmit(task func()) bool {
	select {
	case p.sem <- struct{}{}:
		go func() {
			defer func() { <-p.sem }()
			task()
		}()
		return true
	default:
		return false
	}
}

// ActiveWorkers 返回当前正在执行的 Worker 数。
func (p *WorkerPool) ActiveWorkers() int {
	return len(p.sem)
}

// MaxSize 返回 Worker 池最大容量。
func (p *WorkerPool) MaxSize() int {
	return p.maxSize
}

// ---------------------------------------------------------------------------
// SwapTask 和 SwapQueue
// ---------------------------------------------------------------------------

// SwapTask 表示一个 Swap 请求任务。
type SwapTask struct {
	ID      string
	ChainID string
	DexID   string
	Handler func() error // 实际执行 Swap 的函数
	Result  chan error    // 返回执行结果
}

// SwapQueue 带限流的 Swap 请求队列。
// 三层防护：限流器（控制速率） -> 队列（缓冲突发） -> Worker 池（控制并发）。
type SwapQueue struct {
	limiter  *TokenBucketLimiter
	workers  *WorkerPool
	queue    chan SwapTask
	maxQueue int

	// 统计
	mu             sync.Mutex
	totalSubmitted int
	totalAccepted  int
	totalRejected  int
	totalCompleted int
	totalFailed    int
}

// NewSwapQueue 创建 Swap 请求队列。
// maxTPS 是最大请求速率，maxWorkers 是最大并发数，maxQueue 是队列容量。
func NewSwapQueue(maxTPS float64, burstCapacity float64, maxWorkers int, maxQueue int) *SwapQueue {
	return &SwapQueue{
		limiter:  NewTokenBucketLimiter(burstCapacity, maxTPS),
		workers:  NewWorkerPool(maxWorkers),
		queue:    make(chan SwapTask, maxQueue),
		maxQueue: maxQueue,
	}
}

// Submit 提交 Swap 任务。
// 先经过限流检查，再入队列。如果限流或队列已满，返回错误。
func (q *SwapQueue) Submit(task SwapTask) error {
	q.mu.Lock()
	q.totalSubmitted++
	q.mu.Unlock()

	// 第一层：限流检查
	if !q.limiter.Allow() {
		q.mu.Lock()
		q.totalRejected++
		q.mu.Unlock()
		slog.Info("swap 请求被限流拒绝", "task_id", task.ID, "chain", task.ChainID)
		return fmt.Errorf("rate limited: request %s rejected", task.ID)
	}

	// 第二层：入队列
	select {
	case q.queue <- task:
		q.mu.Lock()
		q.totalAccepted++
		q.mu.Unlock()
		return nil
	default:
		q.mu.Lock()
		q.totalRejected++
		q.mu.Unlock()
		slog.Warn("swap 队列已满，请求被拒绝", "task_id", task.ID, "chain", task.ChainID, "max_queue", q.maxQueue)
		return fmt.Errorf("queue full: request %s rejected (max=%d)", task.ID, q.maxQueue)
	}
}

// Start 启动队列消费者。
// 从队列中取出任务，提交到 Worker 池执行。
func (q *SwapQueue) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				slog.Info("swap 队列消费者停止")
				return
			case task := <-q.queue:
				// 第三层：提交到 Worker 池
				err := q.workers.Submit(ctx, func() {
					taskErr := task.Handler()
					q.mu.Lock()
					if taskErr != nil {
						q.totalFailed++
					} else {
						q.totalCompleted++
					}
					q.mu.Unlock()
					if task.Result != nil {
						task.Result <- taskErr
					}
				})
				if err != nil {
					slog.Warn("worker 池提交失败", "task_id", task.ID, "error", err)
					if task.Result != nil {
						task.Result <- err
					}
				}
			}
		}
	}()
}

// Stats 返回队列统计信息。
func (q *SwapQueue) Stats() (submitted, accepted, rejected, completed, failed int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.totalSubmitted, q.totalAccepted, q.totalRejected, q.totalCompleted, q.totalFailed
}

// QueueSize 返回当前队列中等待的任务数。
func (q *SwapQueue) QueueSize() int {
	return len(q.queue)
}

// ---------------------------------------------------------------------------
// CircuitBreaker 熔断器
// ---------------------------------------------------------------------------

// 熔断器状态常量。
const (
	circuitClosed   = "closed"
	circuitOpen     = "open"
	circuitHalfOpen = "half_open"
)

// CircuitBreakerConfig 熔断器配置。
type CircuitBreakerConfig struct {
	FailureThreshold    int           // 连续失败多少次后触发熔断（默认 5）
	RecoveryTimeout     time.Duration // Open 状态持续多久后进入 HalfOpen（默认 30s）
	HalfOpenMaxRequests int           // HalfOpen 状态允许多少请求通过（默认 1）
}

// CircuitBreaker 熔断器。
// 三种状态：Closed（正常）→ Open（熔断）→ HalfOpen（探测）→ Closed。
// 使用 sync.Mutex 保证并发安全。
type CircuitBreaker struct {
	mu sync.Mutex

	cfg   CircuitBreakerConfig
	state string // "closed" / "open" / "half_open"

	consecutiveFailures int       // Closed 状态下的连续失败计数
	openSince           time.Time // 进入 Open 状态的时间
	halfOpenAllowed     int       // HalfOpen 状态下已放行的请求数
}

// NewCircuitBreaker 创建熔断器。
// 使用提供的配置，如果某些字段为零值则使用默认值。
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMaxRequests <= 0 {
		cfg.HalfOpenMaxRequests = 1
	}
	return &CircuitBreaker{
		cfg:   cfg,
		state: circuitClosed,
	}
}

// Allow 检查是否允许请求通过。
// Closed：始终允许。
// Open：如果已超过 RecoveryTimeout 则转为 HalfOpen 再判断；否则拒绝。
// HalfOpen：如果已放行请求数 < HalfOpenMaxRequests 则允许；否则拒绝。
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case circuitClosed:
		return true

	case circuitOpen:
		// 检查是否已过恢复超时
		if time.Since(cb.openSince) >= cb.cfg.RecoveryTimeout {
			cb.state = circuitHalfOpen
			cb.halfOpenAllowed = 0
			// 进入 HalfOpen，继续判断是否放行
			if cb.halfOpenAllowed < cb.cfg.HalfOpenMaxRequests {
				cb.halfOpenAllowed++
				return true
			}
			return false
		}
		return false

	case circuitHalfOpen:
		if cb.halfOpenAllowed < cb.cfg.HalfOpenMaxRequests {
			cb.halfOpenAllowed++
			return true
		}
		return false

	default:
		return false
	}
}

// RecordSuccess 记录请求成功。
// 在 HalfOpen 状态下，成功会将熔断器重置为 Closed。
// 在 Closed 状态下，成功会重置连续失败计数。
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case circuitHalfOpen:
		// 探测成功，恢复到 Closed
		cb.state = circuitClosed
		cb.consecutiveFailures = 0
	case circuitClosed:
		cb.consecutiveFailures = 0
	}
}

// RecordFailure 记录请求失败。
// 在 Closed 状态下，连续失败达到阈值后切换到 Open。
// 在 HalfOpen 状态下，失败立即回到 Open。
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case circuitClosed:
		cb.consecutiveFailures++
		if cb.consecutiveFailures >= cb.cfg.FailureThreshold {
			cb.state = circuitOpen
			cb.openSince = time.Now()
		}
	case circuitHalfOpen:
		// 探测失败，回到 Open
		cb.state = circuitOpen
		cb.openSince = time.Now()
	}
}

// State 返回当前状态字符串："closed"、"open" 或 "half_open"。
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Reset 手动重置熔断器为 Closed 状态。
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = circuitClosed
	cb.consecutiveFailures = 0
	cb.halfOpenAllowed = 0
}
