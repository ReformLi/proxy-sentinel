package proxy

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strings"
	"testing"
)

// gzip 响应应解压为可读文本
func TestDecodeResponseBodyGzip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	io.WriteString(zw, `{"message":"hello 中文"}`)
	zw.Close()

	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Content-Encoding", "gzip")

	got := decodeResponseBody(buf.String(), h)
	if !strings.Contains(got, `"message"`) || !strings.Contains(got, "中文") {
		t.Fatalf("gzip 应解压为原文, got: %q", got)
	}
}

// deflate 响应应解压
func TestDecodeResponseBodyDeflate(t *testing.T) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	io.WriteString(zw, `{"ok":true}`)
	zw.Close()

	h := http.Header{}
	h.Set("Content-Encoding", "deflate")

	got := decodeResponseBody(buf.String(), h)
	if !strings.Contains(got, `"ok":true`) {
		t.Fatalf("deflate 应解压为原文, got: %q", got)
	}
}

// 截断的 gzip 流：解出前半段也保留（部分可用优于全丢）
func TestDecodeResponseBodyGzipTruncated(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	io.WriteString(zw, strings.Repeat("abcdefgh", 64)) // 512 字节，压缩后截一半
	zw.Close()
	truncated := buf.String()[:buf.Len()/2]

	h := http.Header{}
	h.Set("Content-Encoding", "gzip")
	got := decodeResponseBody(truncated, h)
	if got == "" || strings.HasPrefix(got, "[gzip") {
		t.Fatalf("截断流应解出部分内容, got: %q", got)
	}
	if !strings.HasPrefix(got, "abcde") {
		t.Fatalf("部分内容应为原始文本前缀, got: %q", got)
	}
}

// 二进制 Content-Type 存占位符
func TestDecodeResponseBodyBinary(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "image/png")
	got := decodeResponseBody("\x89PNG\r\n\x1a\n", h)
	if !strings.Contains(got, "[binary image/png") {
		t.Fatalf("二进制应存占位符, got: %q", got)
	}
}

// br 压缩存占位符；非 UTF-8 文本兜底占位
func TestDecodeResponseBodyBrAndNonUTF8(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Encoding", "br")
	if got := decodeResponseBody("whatever", h); !strings.Contains(got, "[br-compressed") {
		t.Fatalf("br 应存占位符, got: %q", got)
	}

	h2 := http.Header{}
	h2.Set("Content-Type", "text/html; charset=gbk")
	if got := decodeResponseBody("\xc4\xe3\xba\xc3", h2); !strings.Contains(got, "[non-utf8 body") {
		t.Fatalf("非UTF-8应存占位符, got: %q", got)
	}
}

// 纯文本与空体原样返回
func TestDecodeResponseBodyPassthrough(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	if got := decodeResponseBody(`{"a":1}`, h); got != `{"a":1}` {
		t.Fatalf("纯文本应原样返回, got: %q", got)
	}
	if got := decodeResponseBody("", h); got != "" {
		t.Fatalf("空体应原样返回, got: %q", got)
	}
}
