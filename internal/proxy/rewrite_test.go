package proxy

import (
	"testing"

	"proxy-sentinel/internal/storage"
)

func TestRewriterApply(t *testing.T) {
	rw := NewRewriter([]storage.RewriteRule{
		{Prefix: "/api/v1", Replacement: "/v1"},                      // 替换前缀
		{Prefix: "/legacy", Replacement: ""},                         // 剥离前缀
		{Prefix: "/svc", Replacement: "/internal/svc", Backend: "http://b2"}, // 限定后端
	})

	cases := []struct {
		path, backend, want string
	}{
		// 前缀替换（段边界匹配）
		{"/api/v1/users", "http://b1", "/v1/users"},
		{"/api/v1", "http://b1", "/v1"},
		// 段边界：/api/v10 不应被 /api/v1 规则命中
		{"/api/v10/users", "http://b1", "/api/v10/users"},
		// 前缀不匹配原样返回
		{"/other/path", "http://b1", "/other/path"},
		// 剥离前缀
		{"/legacy/api", "http://b1", "/api"},
		{"/legacy", "http://b1", ""},
		// 限定后端：后端匹配才重写
		{"/svc/x", "http://b2", "/internal/svc/x"},
		{"/svc/x", "http://b1", "/svc/x"},
		// 第一条命中即停止（/api/v1 已被规则 1 处理，不会进入规则 2）
		{"/api/v1/legacy", "http://b1", "/v1/legacy"},
	}
	for _, c := range cases {
		if got := rw.Apply(c.path, c.backend); got != c.want {
			t.Errorf("Apply(%q, %q) = %q, want %q", c.path, c.backend, got, c.want)
		}
	}
}

func TestRewriterEmpty(t *testing.T) {
	rw := NewRewriter(nil)
	if got := rw.Apply("/any", "http://b"); got != "/any" {
		t.Errorf("空规则应原样返回，got %q", got)
	}
}

func TestValidateRewrites(t *testing.T) {
	backends := []storage.WeightedBackend{{URL: "http://b1", Weight: 1}, {URL: "http://b2", Weight: 1}}
	ok := []storage.RewriteRule{
		{Prefix: "/api/v1", Replacement: "/v1"},
		{Prefix: "/legacy", Replacement: ""},
		{Prefix: "/svc", Replacement: "/internal/svc", Backend: "http://b2"},
	}
	if err := ValidateRewrites(ok, backends); err != nil {
		t.Errorf("合法规则不应报错: %v", err)
	}
	bad := []struct {
		name  string
		rules []storage.RewriteRule
	}{
		{"前缀不以 / 开头", []storage.RewriteRule{{Prefix: "api", Replacement: "/v1"}}},
		{"前缀是裸 /", []storage.RewriteRule{{Prefix: "/", Replacement: "/x"}}},
		{"替换值不以 / 开头", []storage.RewriteRule{{Prefix: "/a", Replacement: "v1"}}},
		{"限定后端不在列表", []storage.RewriteRule{{Prefix: "/a", Replacement: "/b", Backend: "http://ghost"}}},
	}
	for _, c := range bad {
		if err := ValidateRewrites(c.rules, backends); err == nil {
			t.Errorf("%s: 期望报错，实际通过", c.name)
		}
	}
}
