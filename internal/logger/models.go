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

// maskJSONStringField 将 JSON 中 "key":"value" 或 "key":["v1","v2"] 的值脱敏。
// 字符串值扫描到闭合引号，正确跳过 \" 与 \\ 转义序列（否则值内含转义引号会脱敏错位）
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
		if i >= len(body) {
			return body
		}
		var masked string
		var next int // 值结束位置（闭合引号或数组右括号之后）
		switch body[i] {
		case '"':
			// 字符串值：逐字符扫描闭合引号，跳过转义序列
			j := i + 1
			for j < len(body) {
				if body[j] == '\\' {
					j += 2 // 跳过转义字符（\" \\ 等）
					continue
				}
				if body[j] == '"' {
					break
				}
				j++
			}
			if j >= len(body) {
				return body // 未闭合的 JSON，放弃脱敏
			}
			masked = maskValue(body[i+1 : j])
			next = j + 1
		case '[':
			// 数组值：整体替换为 "***"（如 "tokens":["a","b"]）
			end := strings.IndexByte(body[i:], ']')
			if end < 0 {
				return body // 未闭合的数组，放弃脱敏
			}
			masked = "***"
			next = i + end + 1
		default:
			// 数字/布尔等其他类型：跳过（敏感字段极少为非字符串）
			idx = keyEnd
			continue
		}
		body = body[:i] + "\"" + masked + "\"" + body[next:]
		lower = strings.ToLower(body)
		idx = i + len(masked) + 2
	}
}
