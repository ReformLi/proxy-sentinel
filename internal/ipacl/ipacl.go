// Package ipacl 提供代理入口的 IP 黑白名单匹配：
// 支持精确 IP（1.2.3.4 / ::1）与 CIDR 网段（10.0.0.0/8 / fe80::/10）。
// 双名单语义（黑名单优先）：
//  1. 命中黑名单 → 拒绝（白名单救不回来）
//  2. 命中白名单 → 放行
//  3. 都未命中   → 默认动作（allow / deny）
// 名单在管理页保存后编译为只读结构，经原子替换热生效（请求路径无锁）。
package ipacl

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// Mode 名单模式
type Mode string

const (
	ModeOff Mode = "off" // 关闭（不拦截，名单保留）
	ModeOn  Mode = "on"  // 双名单启用
)

// DefaultAction 两份名单都未命中时的默认动作
type DefaultAction string

const (
	DefaultAllow DefaultAction = "allow" // 默认放行（黑名单为主，白名单做例外开窗）
	DefaultDeny  DefaultAction = "deny"  // 默认拒绝（白名单为准入门槛，黑名单挖洞）
)

// Entry 一条名单条目（value = 精确 IP 或 CIDR）
type Entry struct {
	Value string `json:"value"`
	Note  string `json:"note"`
}

// Config 名单配置（settings 表持久化的 JSON 结构）
type Config struct {
	Mode      Mode          `json:"mode"`
	Default   DefaultAction `json:"default"`
	Blacklist []Entry       `json:"blacklist"`
	Whitelist []Entry       `json:"whitelist"`
}

// legacyConfig 旧单名单格式（v1：mode = off/blacklist/whitelist + 单份 entries）
type legacyConfig struct {
	Mode    Mode    `json:"mode"`
	Entries []Entry `json:"entries"`
}

// ParseConfig 解析 JSON 配置；自动识别旧格式并转换为双名单语义：
// 旧 blacklist → blacklist=entries + default=allow；旧 whitelist → whitelist=entries + default=deny
func ParseConfig(raw []byte) (Config, error) {
	// 先按新格式尝试；通过"是否有 entries 字段"区分新旧（新格式无 entries）
	var probe struct {
		Entries *[]Entry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Config{}, fmt.Errorf("配置不是合法 JSON: %w", err)
	}
	if probe.Entries != nil {
		// 旧格式：转换
		var lc legacyConfig
		if err := json.Unmarshal(raw, &lc); err != nil {
			return Config{}, fmt.Errorf("解析旧格式配置失败: %w", err)
		}
		cfg := Config{Blacklist: []Entry{}, Whitelist: []Entry{}}
		switch lc.Mode {
		case ModeOff:
			cfg.Mode = ModeOff
			cfg.Default = DefaultAllow
		case "blacklist":
			cfg.Mode = ModeOn
			cfg.Default = DefaultAllow
			cfg.Blacklist = lc.Entries
		case "whitelist":
			cfg.Mode = ModeOn
			cfg.Default = DefaultDeny
			cfg.Whitelist = lc.Entries
		default:
			// 未知模式按关闭处理，保留条目在黑名单里避免丢数据
			cfg.Mode = ModeOff
			cfg.Default = DefaultAllow
			cfg.Blacklist = lc.Entries
		}
		return cfg, nil
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置失败: %w", err)
	}
	if cfg.Blacklist == nil {
		cfg.Blacklist = []Entry{}
	}
	if cfg.Whitelist == nil {
		cfg.Whitelist = []Entry{}
	}
	return cfg, nil
}

// List 编译后的只读名单；一次构建，之后仅读，可安全并发访问
type List struct {
	mode  Mode
	def   DefaultAction
	black *rules // 黑名单（命中即拒绝）
	white *rules // 白名单（命中放行）
}

// rules 一份编译后的名单（精确 IP + CIDR 前缀）
type rules struct {
	exact   map[netip.Addr]struct{}
	prefix4 []netip.Prefix
	prefix6 []netip.Prefix
}

// Compile 校验并编译配置；任何非法条目都会使整份配置被拒绝（防半生效）
func Compile(cfg Config) (*List, error) {
	switch cfg.Mode {
	case ModeOff, ModeOn:
	default:
		return nil, fmt.Errorf("无效模式 %q（可选 off / on）", cfg.Mode)
	}
	switch cfg.Default {
	case "", DefaultAllow, DefaultDeny:
		if cfg.Default == "" {
			cfg.Default = DefaultAllow
		}
	default:
		return nil, fmt.Errorf("无效默认动作 %q（可选 allow / deny）", cfg.Default)
	}

	l := &List{mode: cfg.Mode, def: cfg.Default}
	black, err := compileRules(cfg.Blacklist, "黑名单")
	if err != nil {
		return nil, err
	}
	white, err := compileRules(cfg.Whitelist, "白名单")
	if err != nil {
		return nil, err
	}
	l.black, l.white = black, white

	// 防呆：默认拒绝 + 白名单为空 = 拒绝所有请求（含管理页自身走 /proxy 的场景）
	if cfg.Mode == ModeOn && cfg.Default == DefaultDeny && len(cfg.Whitelist) == 0 {
		return nil, fmt.Errorf("默认动作为「拒绝」时白名单不能为空（否则将拒绝所有请求）")
	}
	return l, nil
}

// compileRules 编译一份名单；label 用于错误信息
func compileRules(entries []Entry, label string) (*rules, error) {
	r := &rules{exact: make(map[netip.Addr]struct{})}
	seen := make(map[string]struct{}, len(entries))
	for i, e := range entries {
		v := strings.TrimSpace(e.Value)
		if v == "" {
			return nil, fmt.Errorf("%s第 %d 条：IP 不能为空", label, i+1)
		}
		if _, dup := seen[v]; dup {
			return nil, fmt.Errorf("%s第 %d 条：%s 重复", label, i+1, v)
		}
		seen[v] = struct{}{}

		if strings.Contains(v, "/") {
			p, err := netip.ParsePrefix(v)
			if err != nil {
				return nil, fmt.Errorf("%s第 %d 条 %q 不是合法的 CIDR 网段", label, i+1, v)
			}
			p = p.Masked()
			if p.Addr().Is4() {
				r.prefix4 = append(r.prefix4, p)
			} else {
				r.prefix6 = append(r.prefix6, p)
			}
			continue
		}
		a, err := netip.ParseAddr(v)
		if err != nil {
			return nil, fmt.Errorf("%s第 %d 条 %q 不是合法的 IP 地址", label, i+1, v)
		}
		// v4-mapped（::ffff:1.2.3.4）归一化为 IPv4，保证不同写法命中同一条规则
		r.exact[a.Unmap()] = struct{}{}
	}
	return r, nil
}

// Mode 返回名单模式
func (l *List) Mode() Mode { return l.mode }

// Default 返回默认动作
func (l *List) Default() DefaultAction { return l.def }

// Allowed 按黑优先语义判定是否放行
func (l *List) Allowed(ip string) bool {
	if l.mode != ModeOn {
		return true // 关闭模式全放行
	}
	if l.black.contains(ip) {
		return false // 黑名单绝对优先
	}
	if l.white.contains(ip) {
		return true // 白名单放行
	}
	return l.def == DefaultAllow // 都未命中走默认动作
}

// contains 判断 IP 是否命中名单；解析失败按未命中处理
func (r *rules) contains(ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	a = a.Unmap() // v4-mapped → IPv4
	if _, ok := r.exact[a]; ok {
		return true
	}
	if a.Is4() {
		for _, p := range r.prefix4 {
			if p.Contains(a) {
				return true
			}
		}
		// IPv4 请求也可能命中 IPv6 网段规则（覆盖 v4-mapped 空间的写法，如 ::ffff:0:0/96）
		mapped := netip.AddrFrom16(a.As16())
		for _, p := range r.prefix6 {
			if p.Contains(mapped) {
				return true
			}
		}
		return false
	}
	for _, p := range r.prefix6 {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
