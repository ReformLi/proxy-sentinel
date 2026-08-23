package logger

import (
	"strings"

	"proxy-sentinel/internal/storage"
)

// 敏感字段名（小写匹配）
var sensitiveFields = map[string]bool{
	"authorization": true,
	"cookie":         true,
	"set-cookie":      true,
	"password":        true,
	"passwd":          true,
	"secret":          true,
	"token":           true,
	"api-key":         true,
	"apikey":          true,
}

// maskValue 把敏感值脱敏，保留前 2 字符
func maskValue(v string) string {
	if len(v) <= 2 {
		return strings.Repeat("*", len(v))
	}
	return v[:2] + strings.Repeat("*", len(v)-2)
}

// MaskRecord 返回脱敏后的日志记录副本
// Headers 与 Body 均以 JSON 字符串形式存储，统一按 JSON 敏感字段脱敏。
func MaskRecord(r *storage.LogRecord) *storage.LogRecord {
	cp := *r
	cp.RequestHeaders = maskJSON(cp.RequestHeaders)
	cp.ResponseHeaders = maskJSON(cp.ResponseHeaders)
	cp.RequestBody = maskJSON(cp.RequestBody)
	cp.ResponseBody = maskJSON(cp.ResponseBody)
	return &cp
}

// maskJSON 对 JSON 文本做敏感字段脱敏
// 命中 "key":"value" 形式时，将 value 替换为脱敏后的值（大小写不敏感）。
func maskJSON(s string) string {
	if s == "" {
		return s
	}
	for key := range sensitiveFields {
		s = maskJSONStringField(s, key)
	}
	return s
}

// maskJSONStringField 将 JSON 中 "key":"value" 的 value 脱敏（不处理转义边界情况）
func maskJSONStringField(body, key string) string {
	needle := "\"" + key + "\""
	lower := strings.ToLower(body)
	idx := 0
	for {
		pos := strings.Index(lower[idx:], needle)
		if pos < 0 {
			return body
		}
		pos += idx
		keyEnd := pos + len(needle)
		// 查找冒号
		colonRel := strings.IndexByte(body[keyEnd:], ':')
		if colonRel < 0 {
			return body
		}
		valStart := keyEnd + colonRel + 1
		// 跳过空白
		i := valStart
		for i < len(body) && (body[i] == ' ' || body[i] == '\t') {
			i++
		}
		if i >= len(body) || body[i] != '"' {
			idx = keyEnd
			continue
		}
		// 查找闭合引号
		closeQuote := strings.IndexByte(body[i+1:], '"')
		if closeQuote < 0 {
			return body
		}
		valContent := body[i+1 : i+1+closeQuote]
		masked := maskValue(valContent)
		body = body[:i+1] + masked + body[i+1+closeQuote:]
		lower = strings.ToLower(body)
		idx = i + 1 + len(masked) + 1
	}
}
