package proxy

import (
	"net/http"
	"testing"

	"proxy-sentinel/internal/storage"
)

func TestWeightedStrategyDistribution(t *testing.T) {
	// 权重 9:1，采样 10000 次验证分布（统计容差 ±5%）
	b := NewBalancer("weighted", []storage.WeightedBackend{
		{URL: "http://a", Weight: 9},
		{URL: "http://b", Weight: 1},
	})
	counts := map[string]int{}
	const n = 10000
	for i := 0; i < n; i++ {
		be := b.Next()
		if be == "" {
			t.Fatal("应有后端返回")
		}
		counts[be]++
	}
	if counts["http://a"] < int(n*0.85) || counts["http://a"] > int(n*0.95) {
		t.Errorf("权重 9 的后端分布异常: %d/%d", counts["http://a"], n)
	}
}

func TestWeightedZeroWeightExcluded(t *testing.T) {
	// 权重 0 的后端不接流量（灰度回退语义：新版本权重调 0 = 全量回旧版本）
	b := NewBalancer("weighted", []storage.WeightedBackend{
		{URL: "http://a", Weight: 100},
		{URL: "http://b", Weight: 0},
	})
	for i := 0; i < 100; i++ {
		if be := b.Next(); be != "http://a" {
			t.Fatalf("权重 0 后端不应接流量，got %s", be)
		}
	}
}

func TestWeightedAllZero(t *testing.T) {
	b := NewBalancer("weighted", []storage.WeightedBackend{
		{URL: "http://a", Weight: 0},
	})
	if be := b.Next(); be != "" {
		t.Errorf("全部权重 0 应返回空，got %s", be)
	}
}

func TestWeightedHealthyFallback(t *testing.T) {
	// a 挂掉后流量全部落到 b（权重归一化到剩余健康节点）
	b := NewBalancer("weighted", []storage.WeightedBackend{
		{URL: "http://a", Weight: 90},
		{URL: "http://b", Weight: 10},
	})
	b.MarkDown("http://a")
	for i := 0; i < 50; i++ {
		if be := b.Next(); be != "http://b" {
			t.Fatalf("a 不健康时应路由到 b，got %s", be)
		}
	}
}

func TestValidateRules(t *testing.T) {
	backends := []storage.WeightedBackend{{URL: "http://a", Weight: 1}, {URL: "http://b", Weight: 1}}
	ok := []storage.RouteRule{
		{Type: RuleTypeHeader, Key: "X-Gray", Value: "1", Backend: "http://b"},
		{Type: RuleTypeCookie, Key: "beta", Value: "on", Backend: "http://b"},
		{Type: RuleTypePath, Value: "/api/v2/", Backend: "http://b"},
	}
	if err := ValidateRules(ok, backends); err != nil {
		t.Errorf("合法规则不应报错: %v", err)
	}
	bad := []struct {
		name  string
		rules []storage.RouteRule
	}{
		{"非法类型", []storage.RouteRule{{Type: "ip", Value: "x", Backend: "http://a"}}},
		{"header 缺名", []storage.RouteRule{{Type: RuleTypeHeader, Value: "1", Backend: "http://a"}}},
		{"cookie 缺值", []storage.RouteRule{{Type: RuleTypeCookie, Key: "beta", Backend: "http://a"}}},
		{"path 不以 / 开头", []storage.RouteRule{{Type: RuleTypePath, Value: "api", Backend: "http://a"}}},
		{"目标后端不在列表", []storage.RouteRule{{Type: RuleTypeHeader, Key: "X", Value: "1", Backend: "http://ghost"}}},
	}
	for _, c := range bad {
		if err := ValidateRules(c.rules, backends); err == nil {
			t.Errorf("%s: 期望报错，实际通过", c.name)
		}
	}
}

func TestRuleMatcherMatch(t *testing.T) {
	m := NewRuleMatcher([]storage.RouteRule{
		{Type: RuleTypeHeader, Key: "X-Gray", Value: "1", Backend: "http://b"},
		{Type: RuleTypeCookie, Key: "beta", Value: "on", Backend: "http://b"},
		{Type: RuleTypePath, Value: "/api/v2/", Backend: "http://b"},
	})

	// header 命中
	h := http.Header{}
	h.Set("X-Gray", "1")
	if be, hit := m.Match(h, nil, "/x"); !hit || be != "http://b" {
		t.Errorf("header 规则应命中: hit=%v be=%s", hit, be)
	}
	// header 值不匹配
	h2 := http.Header{}
	h2.Set("X-Gray", "2")
	if _, hit := m.Match(h2, nil, "/x"); hit {
		t.Error("header 值不匹配不应命中")
	}
	// cookie 命中
	cookies := []*http.Cookie{{Name: "beta", Value: "on"}}
	if be, hit := m.Match(http.Header{}, cookies, "/x"); !hit || be != "http://b" {
		t.Errorf("cookie 规则应命中: hit=%v be=%s", hit, be)
	}
	// path 前缀命中
	if be, hit := m.Match(http.Header{}, nil, "/api/v2/users"); !hit || be != "http://b" {
		t.Errorf("path 规则应命中: hit=%v be=%s", hit, be)
	}
	// path 前缀不命中（/api/v20 应命中 /api/v2 吗？前缀匹配按字符串，会命中——测试前缀语义需注意）
	if _, hit := m.Match(http.Header{}, nil, "/api/v1/users"); hit {
		t.Error("path 前缀不匹配不应命中")
	}
	// 全不命中
	if _, hit := m.Match(http.Header{}, nil, "/other"); hit {
		t.Error("无匹配规则不应命中")
	}
	// 规则顺序优先：第一条命中即返回
	m2 := NewRuleMatcher([]storage.RouteRule{
		{Type: RuleTypePath, Value: "/", Backend: "http://a"},
		{Type: RuleTypeHeader, Key: "X-Gray", Value: "1", Backend: "http://b"},
	})
	h3 := http.Header{}
	h3.Set("X-Gray", "1")
	if be, _ := m2.Match(h3, nil, "/any"); be != "http://a" {
		t.Errorf("第一条命中即生效，期望 http://a，got %s", be)
	}
}
