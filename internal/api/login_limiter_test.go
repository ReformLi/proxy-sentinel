package api

import (
	"testing"
	"time"
)

// 连续失败达到阈值后应锁定；锁定期间即使密码正确（allowed）也应拒绝
func TestLoginLimiterLockOnThreshold(t *testing.T) {
	l := newLoginLimiter(3, 60)
	ip := "10.0.0.1"

	for i := 0; i < 3; i++ {
		if !l.allowed(ip) {
			t.Fatalf("第 %d 次失败前不应被锁定", i+1)
		}
		l.fail(ip)
	}
	if l.allowed(ip) {
		t.Fatal("达到失败阈值后应被锁定")
	}
}

// 失败计数应跨请求累积（allowed 不得清零未锁定条目的计数）
func TestLoginLimiterFailuresAccumulate(t *testing.T) {
	l := newLoginLimiter(3, 60)
	ip := "10.0.0.2"

	for i := 0; i < 2; i++ {
		if !l.allowed(ip) {
			t.Fatalf("第 %d 次失败前不应被锁定", i+1)
		}
		l.fail(ip)
	}
	// 中间穿插一次 allowed 判断（正常登录流程每次都会先调 allowed）
	if !l.allowed(ip) {
		t.Fatal("未达阈值不应被锁定")
	}
	l.fail(ip)
	if l.allowed(ip) {
		t.Fatal("穿插 allowed 后计数被意外清零，未触发锁定")
	}
}

// 登录成功应清除记录；锁定过期后计数重新累计
func TestLoginLimiterSuccessAndExpiry(t *testing.T) {
	l := newLoginLimiter(2, 60)
	ip := "10.0.0.3"

	l.fail(ip)
	l.success(ip)
	if !l.allowed(ip) {
		t.Fatal("成功登录后应放行")
	}
	// 成功已重置计数：1 次失败不应锁定，2 次才锁定
	l.fail(ip)
	if !l.allowed(ip) {
		t.Fatal("计数重置后仅 1 次失败不应锁定")
	}
	l.fail(ip)
	if l.allowed(ip) {
		t.Fatal("重新累计 2 次失败后应锁定")
	}

	// 模拟锁过期
	l.mu.Lock()
	l.entries["10.0.0.4"] = &loginEntry{
		failures:    2,
		lastFail:    time.Now().Add(-time.Hour),
		lockedUntil: time.Now().Add(-time.Hour),
	}
	l.mu.Unlock()
	if !l.allowed("10.0.0.4") {
		t.Fatal("锁过期后应放行")
	}
}
