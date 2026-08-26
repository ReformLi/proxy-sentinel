package ratelimit

import (
	"math"
	"sync"
	"time"
)

// bucket 每个 key 的令牌桶状态：tokens 为当前剩余令牌，last 为上次补充时间
type bucket struct {
	tokens float64
	last   time.Time
}

// maxKeys 触发惰性清理的桶数量上限（防 key 极多时 map 无限增长）
const maxKeys = 1 << 16

// Limiter 基于 O(1) 令牌桶的内存限流器（按 key 独立计数，如按 Token ID）。
// 桶容量 = limit（允许整窗额度一次性突发），按 limit/window 每秒匀速补充；
// 长期平均速率与滑动窗口语义一致，但单请求开销从 O(n) 降为 O(1)，且每 key 仅占常数内存。
// limit <= 0 表示不限流；超限返回 (false, 建议等待时长)
type Limiter struct {
	mu      sync.Mutex
	window  time.Duration
	buckets map[int64]*bucket
}

// NewLimiter 创建限流器，window 为统计窗口（如 1 分钟）
func NewLimiter(window time.Duration) *Limiter {
	return &Limiter{
		window:  window,
		buckets: make(map[int64]*bucket),
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

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxKeys {
			l.sweepLocked(now)
		}
		b = &bucket{}
		l.buckets[key] = b
	}
	rate := float64(limit) / l.window.Seconds() // 每秒补充速率
	// 按流逝时间补充令牌；上限 = 桶容量 = limit（新桶 last 为零值，首请求即补满）
	b.tokens = math.Min(float64(limit), b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// 超限：等待补满 1 个令牌
	retry := time.Duration((1 - b.tokens) / rate * float64(time.Second))
	if retry < 0 {
		retry = 0
	}
	return false, retry
}

// Remove 移除 key 的计数记录（Token 被吊销时调用，避免内存滞留）
func (l *Limiter) Remove(key int64) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// Sweep 清理闲置桶（Token 删除但未触发 Remove 时的兜底）。
// 闲置超过 2 倍窗口才清除：此时桶早已补充满，清除后再访问语义不变
func (l *Limiter) Sweep() {
	now := time.Now()
	l.mu.Lock()
	l.sweepLocked(now)
	l.mu.Unlock()
}

// sweepLocked 清理闲置桶；调用方必须已持有 l.mu（Allow 持锁路径与 Sweep 共用）
func (l *Limiter) sweepLocked(now time.Time) {
	idle := l.window * 2
	for k, b := range l.buckets {
		if now.Sub(b.last) > idle {
			delete(l.buckets, k)
		}
	}
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
