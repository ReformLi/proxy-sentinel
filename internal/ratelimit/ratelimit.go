package ratelimit

import (
	"sync"
	"time"
)

// Limiter 基于滑动窗口日志的内存限流器（按 key 独立计数，如按 Token ID）
// limit <= 0 表示不限流；超限返回 (false, 建议等待时长)
type Limiter struct {
	mu      sync.Mutex
	window  time.Duration
	history map[int64][]time.Time
}

// NewLimiter 创建限流器，window 为统计窗口（如 1 分钟）
func NewLimiter(window time.Duration) *Limiter {
	return &Limiter{
		window:  window,
		history: make(map[int64][]time.Time),
	}
}

// Allow 判断 key 本次请求是否放行
func (l *Limiter) Allow(key int64, limit int) (bool, time.Duration) {
	if limit <= 0 {
		return true, 0 // 不限流
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// 滑出窗口外的旧记录
	entries := l.history[key]
	keep := entries[:0]
	for _, ts := range entries {
		if now.Sub(ts) < l.window {
			keep = append(keep, ts)
		}
	}
	if len(keep) >= limit {
		// 超限：需等待最早一条记录滑出窗口
		l.history[key] = keep
		retry := l.window - now.Sub(keep[0])
		if retry < 0 {
			retry = 0
		}
		return false, retry
	}
	keep = append(keep, now)
	l.history[key] = keep
	return true, 0
}

// Remove 移除 key 的计数记录（Token 被吊销时调用，避免内存滞留）
func (l *Limiter) Remove(key int64) {
	l.mu.Lock()
	delete(l.history, key)
	l.mu.Unlock()
}

// Sweep 清理全部过期计数（Token 删除但未触发 Remove 时的兜底）
func (l *Limiter) Sweep() {
	now := time.Now()
	l.mu.Lock()
	for k, entries := range l.history {
		keep := entries[:0]
		for _, ts := range entries {
			if now.Sub(ts) < l.window {
				keep = append(keep, ts)
			}
		}
		if len(keep) == 0 {
			delete(l.history, k)
		} else {
			l.history[k] = keep
		}
	}
	l.mu.Unlock()
}

// StartSweeper 启动后台周期清理，返回停止函数
func (l *Limiter) StartSweeper(interval time.Duration) (stop func()) {
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				l.Sweep()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}
