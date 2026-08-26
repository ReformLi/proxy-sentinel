package ratelimit

import (
	"testing"
	"time"
)

// 令牌桶应允许整窗额度的一次性突发，随后拒绝，等待补充后恢复放行
func TestTokenBucketBurstAndRefill(t *testing.T) {
	const window = 200 * time.Millisecond
	const limit = 3
	l := NewLimiter(window)

	for i := 0; i < limit; i++ {
		if ok, _ := l.Allow(1, limit); !ok {
			t.Fatalf("第 %d 次请求应放行（容量内突发）", i+1)
		}
	}
	ok, retry := l.Allow(1, limit)
	if ok {
		t.Fatal("超过容量应拒绝")
	}
	if retry <= 0 || retry > window {
		t.Fatalf("建议等待时长不合理: %v", retry)
	}
	time.Sleep(retry + 30*time.Millisecond)
	if ok, _ := l.Allow(1, limit); !ok {
		t.Fatal("等待令牌补充后应恢复放行")
	}
}

// 不同 key 之间互不影响；limit<=0 表示不限流
func TestTokenBucketKeyIndependenceAndUnlimited(t *testing.T) {
	l := NewLimiter(time.Minute)
	const limit = 2
	for i := 0; i < limit; i++ {
		if ok, _ := l.Allow(100, limit); !ok {
			t.Fatal("key=100 应放行")
		}
	}
	if ok, _ := l.Allow(100, limit); ok {
		t.Fatal("key=100 超限应拒绝")
	}
	// 另一个 key 桶独立
	if ok, _ := l.Allow(200, limit); !ok {
		t.Fatal("key=200 应不受 key=100 影响")
	}
	// limit<=0 直接放行
	if ok, _ := l.Allow(100, 0); !ok {
		t.Fatal("limit=0 应不限流")
	}
}

// Remove 与 Sweep 应清除桶，清除后按新桶重新计满额度
func TestTokenBucketRemoveAndSweep(t *testing.T) {
	l := NewLimiter(time.Minute)
	const limit = 1
	if ok, _ := l.Allow(1, limit); !ok {
		t.Fatal("首次应放行")
	}
	if ok, _ := l.Allow(1, limit); ok {
		t.Fatal("额度耗尽应拒绝")
	}
	l.Remove(1)
	if ok, _ := l.Allow(1, limit); !ok {
		t.Fatal("Remove 后应重新放行")
	}

	// Sweep：超过 2 倍窗口未活跃的桶应被清除
	l2 := NewLimiter(10 * time.Millisecond)
	if ok, _ := l2.Allow(9, 1); !ok {
		t.Fatal("应放行")
	}
	l2.buckets[9].last = time.Now().Add(-l2.window * 3) // 模拟长时间闲置
	l2.Sweep()
	l2.mu.Lock()
	_, exists := l2.buckets[9]
	l2.mu.Unlock()
	if exists {
		t.Fatal("闲置桶应被 Sweep 清除")
	}
}
