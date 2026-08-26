package logger

import "testing"

func TestMaskJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "普通字符串值",
			in:   `{"password":"secret123"}`,
			want: `{"password":"se*******"}`,
		},
		{
			name: "值内含转义引号（旧实现会脱敏错位）",
			in:   `{"password":"a\"b"}`,
			want: `{"password":"a\**"}`,
		},
		{
			name: "值内含转义反斜杠",
			in:   `{"token":"ab\\cd"}`,
			want: `{"token":"ab****"}`,
		},
		{
			name: "数组值整体脱敏（旧实现完全不脱敏）",
			in:   `{"token":["alice-key","bob-key"]}`,
			want: `{"token":"***"}`,
		},
		{
			name: "大小写不敏感",
			in:   `{"Password":"AbCdEf"}`,
			want: `{"Password":"Ab****"}`,
		},
		{
			name: "非敏感字段不动",
			in:   `{"username":"alice","password":"secret"}`,
			want: `{"username":"alice","password":"se****"}`,
		},
		{
			name: "空值与短值",
			in:   `{"token":"ab"}`,
			want: `{"token":"**"}`,
		},
		{
			name: "嵌套结构",
			in:   `{"data":{"api-key":"sk-1234567890"},"ok":true}`,
			want: `{"data":{"api-key":"sk***********"},"ok":true}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := maskJSON(c.in); got != c.want {
				t.Errorf("maskJSON(%s)\n got  = %s\n want = %s", c.in, got, c.want)
			}
		})
	}
}
