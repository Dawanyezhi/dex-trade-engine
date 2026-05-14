// Package alarm 提供可插拔的告警接口。
// [dexwallet 通用层] — 告警接口对所有链一致，只有消息内容和阈值不同。
package alarm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Level 告警级别。
type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

// Alert 告警消息。
type Alert struct {
	Level   Level  `json:"level"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Chain   string `json:"chain"`
	Module  string `json:"module"`
}

// Sender 告警发送接口（可插拔：钉钉/Lark/PagerDuty）。
type Sender interface {
	Send(ctx context.Context, alert Alert) error
	Name() string
}

// Manager 告警管理器，支持多通道发送。
type Manager struct {
	mu      sync.RWMutex
	senders []Sender

	dedupWindow time.Duration          // <= 0 表示不去重
	dedupMu     sync.Mutex             // 保护 dedupMap
	dedupMap    map[string]time.Time   // key -> 上次发送时间
}

// NewManager 创建告警管理器（不开启去重）。
func NewManager(senders ...Sender) *Manager {
	return &Manager{senders: senders}
}

// NewManagerWithDedup 创建带去重能力的告警管理器。
// dedupWindow <= 0 时等价于不去重。
func NewManagerWithDedup(dedupWindow time.Duration, senders ...Sender) *Manager {
	m := &Manager{
		senders:     senders,
		dedupWindow: dedupWindow,
	}
	if dedupWindow > 0 {
		m.dedupMap = make(map[string]time.Time)
	}
	return m
}

// dedupKey 生成告警去重键。
func dedupKey(alert Alert) string {
	return alert.Chain + ":" + alert.Module + ":" + alert.Title
}

// AddSender 添加告警通道。
func (m *Manager) AddSender(s Sender) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.senders = append(m.senders, s)
}

// Send 向所有通道发送告警。如果开启了去重且同一 key 在 dedupWindow 内已发送过，则跳过。
func (m *Manager) Send(ctx context.Context, alert Alert) {
	// 去重检查
	if m.dedupWindow > 0 && m.dedupMap != nil {
		key := dedupKey(alert)
		now := time.Now()

		m.dedupMu.Lock()
		if lastSent, ok := m.dedupMap[key]; ok && now.Sub(lastSent) < m.dedupWindow {
			m.dedupMu.Unlock()
			slog.Debug("alarm deduplicated, skipping",
				"key", key,
				"last_sent", lastSent,
				"dedup_window", m.dedupWindow,
			)
			return
		}
		m.dedupMap[key] = now
		m.dedupMu.Unlock()
	}

	m.mu.RLock()
	senders := make([]Sender, len(m.senders))
	copy(senders, m.senders)
	m.mu.RUnlock()

	for _, s := range senders {
		if err := s.Send(ctx, alert); err != nil {
			slog.Error("alarm send failed",
				"sender", s.Name(),
				"title", alert.Title,
				"error", err,
			)
		}
	}
}

// ConsoleSender 控制台告警（demo 用，生产用钉钉/Lark）。
type ConsoleSender struct{}

func NewConsoleSender() *ConsoleSender {
	return &ConsoleSender{}
}

func (c *ConsoleSender) Name() string {
	return "console"
}

func (c *ConsoleSender) Send(_ context.Context, alert Alert) error {
	slog.Warn(fmt.Sprintf("[ALARM][%s] %s: %s (chain=%s, module=%s)",
		alert.Level, alert.Title, alert.Message, alert.Chain, alert.Module))
	return nil
}
