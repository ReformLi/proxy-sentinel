package proxy

import (
	"fmt"
	"strings"

	"proxy-sentinel/internal/storage"
)

// ValidateRewrites 校验重写规则列表（保存前调用）：
// 前缀必须以 / 开头且不能是裸 "/"（"/" 匹配一切，等于全量重写，易误配）；
// 替换值为空（= 剥离前缀）或以 / 开头；限定后端必须存在于后端列表。
func ValidateRewrites(rules []storage.RewriteRule, backends []storage.WeightedBackend) error {
	known := make(map[string]bool, len(backends))
	for _, b := range backends {
		known[b.URL] = true
	}
	for i, r := range rules {
		if !strings.HasPrefix(r.Prefix, "/") || r.Prefix == "/" {
			return fmt.Errorf("重写规则第 %d 条：前缀需以 / 开头且不能是单独的 /（%q）", i+1, r.Prefix)
		}
		if r.Replacement != "" && !strings.HasPrefix(r.Replacement, "/") {
			return fmt.Errorf("重写规则第 %d 条：替换值需以 / 开头或留空（剥离前缀）", i+1)
		}
		if r.Backend != "" && !known[r.Backend] {
			return fmt.Errorf("重写规则第 %d 条：限定后端 %q 不在后端列表中", i+1, r.Backend)
		}
	}
	return nil
}

// Rewriter 路径重写引擎：编译后只读，可安全并发访问。
// 语义：按顺序找第一条"前缀匹配且后端匹配"的规则，做一次前缀替换后停止（不做链式重写）。
type Rewriter struct {
	rules []storage.RewriteRule
}

// NewRewriter 创建重写器
func NewRewriter(rules []storage.RewriteRule) *Rewriter {
	return &Rewriter{rules: rules}
}

// Apply 对转发路径应用重写；path 为剥离 /proxy 前缀后的路径，backend 为已选定的后端。
// 无命中规则时原样返回。
func (rw *Rewriter) Apply(path, backend string) string {
	if rw == nil {
		return path
	}
	for _, r := range rw.rules {
		if r.Backend != "" && r.Backend != backend {
			continue // 规则限定其他后端，跳过
		}
		if path == r.Prefix || strings.HasPrefix(path, r.Prefix+"/") {
			return strings.Replace(path, r.Prefix, r.Replacement, 1)
		}
	}
	return path
}
