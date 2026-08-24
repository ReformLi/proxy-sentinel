package ipacl

import "testing"

func mustCompile(t *testing.T, cfg Config) *List {
	t.Helper()
	l, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile 失败: %v", err)
	}
	return l
}

func TestCompileValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"非法模式", Config{Mode: "graylist"}},
		{"非法默认动作", Config{Mode: ModeOn, Default: "maybe"}},
		{"非法 IP", Config{Mode: ModeOn, Blacklist: []Entry{{Value: "1.2.3.999"}}}},
		{"非法 CIDR", Config{Mode: ModeOn, Blacklist: []Entry{{Value: "10.0.0.0/99"}}}},
		{"空白条目", Config{Mode: ModeOn, Whitelist: []Entry{{Value: "  "}}}},
		{"黑名单重复", Config{Mode: ModeOn, Blacklist: []Entry{{Value: "1.2.3.4"}, {Value: "1.2.3.4"}}}},
		{"默认拒绝但白名单空", Config{Mode: ModeOn, Default: DefaultDeny, Blacklist: []Entry{{Value: "1.2.3.4"}}}},
	}
	for _, c := range cases {
		if _, err := Compile(c.cfg); err == nil {
			t.Errorf("%s: 期望报错，实际通过", c.name)
		}
	}
	// off 模式允许空名单（含默认拒绝，因为关闭时不生效）
	if _, err := Compile(Config{Mode: ModeOff, Default: DefaultDeny}); err != nil {
		t.Errorf("off 模式空名单不应报错: %v", err)
	}
	// 默认动作缺省按 allow 处理
	l := mustCompile(t, Config{Mode: ModeOn, Blacklist: []Entry{{Value: "1.2.3.4"}}})
	if l.Default() != DefaultAllow {
		t.Errorf("缺省默认动作应为 allow，got %s", l.Default())
	}
}

func TestBlacklistOnly(t *testing.T) {
	// 等价于旧黑名单模式：黑名单 + 默认放行
	l := mustCompile(t, Config{
		Mode:    ModeOn,
		Default: DefaultAllow,
		Blacklist: []Entry{
			{Value: "203.0.113.7"},
			{Value: "10.0.0.0/8"},
			{Value: "fe80::/10"},
		},
	})
	deny := []string{"203.0.113.7", "10.1.2.3", "10.255.255.255", "fe80::1", "febf:ffff::abcd"}
	allow := []string{"192.168.1.1", "fec0::1", "8.8.8.8", "not-an-ip"}
	for _, ip := range deny {
		if l.Allowed(ip) {
			t.Errorf("黑名单 IP %s 应被拒绝", ip)
		}
	}
	for _, ip := range allow {
		if !l.Allowed(ip) {
			t.Errorf("非名单 IP %s 应被放行", ip)
		}
	}
}

func TestWhitelistOnly(t *testing.T) {
	// 等价于旧白名单模式：白名单 + 默认拒绝
	l := mustCompile(t, Config{
		Mode:    ModeOn,
		Default: DefaultDeny,
		Whitelist: []Entry{
			{Value: "192.168.1.0/24"},
			{Value: "::1"},
		},
	})
	allow := []string{"192.168.1.1", "192.168.1.254", "::1"}
	deny := []string{"192.168.2.1", "8.8.8.8", "fe80::1"}
	for _, ip := range allow {
		if !l.Allowed(ip) {
			t.Errorf("白名单 IP %s 应被放行", ip)
		}
	}
	for _, ip := range deny {
		if l.Allowed(ip) {
			t.Errorf("非白名单 IP %s 应被拒绝", ip)
		}
	}
}

func TestDualBlackWins(t *testing.T) {
	// 白名单网段挖洞：白 10.0.0.0/8，黑 10.0.0.5，默认拒绝
	l := mustCompile(t, Config{
		Mode:    ModeOn,
		Default: DefaultDeny,
		Blacklist: []Entry{
			{Value: "10.0.0.5", Note: "中毒机器"},
		},
		Whitelist: []Entry{
			{Value: "10.0.0.0/8", Note: "公司网段"},
		},
	})
	if !l.Allowed("10.0.0.1") {
		t.Error("白名单网段内非黑名单 IP 应放行")
	}
	if l.Allowed("10.0.0.5") {
		t.Error("同时命中黑白名单（黑名单在白网段内）应拒绝——黑名单绝对优先")
	}
	if l.Allowed("192.0.2.1") {
		t.Error("两份名单都未命中 + 默认拒绝 → 应拒绝")
	}
}

func TestDualWhitelistException(t *testing.T) {
	// 黑网段内放白名单例外：黑 203.0.113.0/24，白 203.0.113.99，默认放行。
	// 黑名单绝对优先语义下例外 IP 依然被拒（误拒比误放安全，"开窗"需把网段拆细实现）
	l := mustCompile(t, Config{
		Mode:    ModeOn,
		Default: DefaultAllow,
		Blacklist: []Entry{
			{Value: "203.0.113.0/24"},
		},
		Whitelist: []Entry{
			{Value: "203.0.113.99", Note: "合作伙伴专线"},
		},
	})
	if l.Allowed("203.0.113.1") {
		t.Error("黑名单网段内 IP 应拒绝")
	}
	if l.Allowed("203.0.113.99") {
		t.Error("黑名单网段内的白名单例外 IP 也应拒绝——黑名单绝对优先")
	}
	if !l.Allowed("8.8.8.8") {
		t.Error("两份名单都未命中 + 默认放行 → 应放行")
	}
}

func TestIPv4MappedNormalization(t *testing.T) {
	// 规则写 IPv4，请求地址是 v4-mapped IPv6（Go 服务在双栈监听时会出现）
	l := mustCompile(t, Config{Mode: ModeOn, Blacklist: []Entry{{Value: "203.0.113.7"}}})
	if l.Allowed("::ffff:203.0.113.7") {
		t.Error("v4-mapped 形式的黑名单 IP 应被拒绝")
	}
	// 规则写 v4-mapped，请求是普通 IPv4
	l2 := mustCompile(t, Config{Mode: ModeOn, Blacklist: []Entry{{Value: "::ffff:203.0.113.7"}}})
	if l2.Allowed("203.0.113.7") {
		t.Error("普通 IPv4 应命中 v4-mapped 规则")
	}
}

func TestOffModeAllowsAll(t *testing.T) {
	l := mustCompile(t, Config{
		Mode:      ModeOff,
		Blacklist: []Entry{{Value: "1.2.3.4"}},
	})
	if !l.Allowed("1.2.3.4") || !l.Allowed("5.6.7.8") {
		t.Error("off 模式应放行所有 IP")
	}
}

func TestParseConfigLegacyConversion(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want Config
	}{
		{
			name: "旧黑名单",
			raw:  `{"mode":"blacklist","entries":[{"value":"1.2.3.4"}]}`,
			want: Config{Mode: ModeOn, Default: DefaultAllow, Blacklist: []Entry{{Value: "1.2.3.4"}}, Whitelist: []Entry{}},
		},
		{
			name: "旧白名单",
			raw:  `{"mode":"whitelist","entries":[{"value":"::1"}]}`,
			want: Config{Mode: ModeOn, Default: DefaultDeny, Blacklist: []Entry{}, Whitelist: []Entry{{Value: "::1"}}},
		},
		{
			name: "旧关闭",
			raw:  `{"mode":"off","entries":[]}`,
			want: Config{Mode: ModeOff, Default: DefaultAllow, Blacklist: []Entry{}, Whitelist: []Entry{}},
		},
		{
			name: "新格式",
			raw:  `{"mode":"on","default":"deny","blacklist":[{"value":"1.2.3.4"}],"whitelist":[{"value":"10.0.0.0/8"}]}`,
			want: Config{Mode: ModeOn, Default: DefaultDeny, Blacklist: []Entry{{Value: "1.2.3.4"}}, Whitelist: []Entry{{Value: "10.0.0.0/8"}}},
		},
	}
	for _, c := range cases {
		got, err := ParseConfig([]byte(c.raw))
		if err != nil {
			t.Errorf("%s: ParseConfig 报错: %v", c.name, err)
			continue
		}
		if got.Mode != c.want.Mode || got.Default != c.want.Default {
			t.Errorf("%s: mode/default = %s/%s, want %s/%s", c.name, got.Mode, got.Default, c.want.Mode, c.want.Default)
		}
		if len(got.Blacklist) != len(c.want.Blacklist) || len(got.Whitelist) != len(c.want.Whitelist) {
			t.Errorf("%s: 名单长度不符 black=%d white=%d", c.name, len(got.Blacklist), len(got.Whitelist))
		}
	}
	if _, err := ParseConfig([]byte(`not json`)); err == nil {
		t.Error("非法 JSON 应报错")
	}
}
