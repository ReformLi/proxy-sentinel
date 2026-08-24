package proxy

import (
	"fmt"
	"net/http"
	"strings"

	"proxy-sentinel/internal/storage"
)

// 规则类型
const (
	RuleTypeHeader = "header"
	RuleTypeCookie = "cookie"
	RuleTypePath   = "path"
)

// ValidateRules 校验规则列表（保存前调用）：
// 类型合法、必填字段齐全、目标后端必须存在于后端列表。
func ValidateRules(rules []storage.RouteRule, backends []storage.WeightedBackend) error {
	known := make(map[string]bool, len(backends))
	for _, b := range backends {
		known[b.URL] = true
	}
	for i, r := range rules {
		switch r.Type {
		case RuleTypeHeader, RuleTypeCookie:
			if strings.TrimSpace(r.Key) == "" {
				return fmt.Errorf("规则第 %d 条：%s 类型需要指定名称", i+1, r.Type)
			}
			if r.Value == "" {
				return fmt.Errorf("规则第 %d 条：匹配值不能为空", i+1)
			}
		case RuleTypePath:
			if !strings.HasPrefix(r.Value, "/") {
				return fmt.Errorf("规则第 %d 条：路径前缀需以 / 开头", i+1)
			}
		default:
			return fmt.Errorf("规则第 %d 条：非法类型 %q（可选 header/cookie/path）", i+1, r.Type)
		}
		if !known[r.Backend] {
			return fmt.Errorf("规则第 %d 条：目标后端 %q 不在后端列表中", i+1, r.Backend)
		}
	}
	return nil
}

// RuleMatcher 定向分流规则匹配器：编译后只读，可安全并发访问
type RuleMatcher struct {
	rules []storage.RouteRule
}

// NewRuleMatcher 创建匹配器（规则顺序即优先级，第一条命中生效）
func NewRuleMatcher(rules []storage.RouteRule) *RuleMatcher {
	return &RuleMatcher{rules: rules}
}

// Match 按顺序匹配规则；strippedPath 为剥离 /proxy 前缀后的转发路径（即后端看到的路径）
func (m *RuleMatcher) Match(header http.Header, cookies []*http.Cookie, strippedPath string) (backend string, hit bool) {
	if m == nil {
		return "", false
	}
	for _, r := range m.rules {
		switch r.Type {
		case RuleTypeHeader:
			if header.Get(r.Key) == r.Value {
				return r.Backend, true
			}
		case RuleTypeCookie:
			for _, c := range cookies {
				if c.Name == r.Key && c.Value == r.Value {
					return r.Backend, true
				}
			}
		case RuleTypePath:
			if strings.HasPrefix(strippedPath, r.Value) {
				return r.Backend, true
			}
		}
	}
	return "", false
}
