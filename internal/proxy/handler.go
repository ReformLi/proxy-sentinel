package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"proxy-sentinel/internal/logger"
	"proxy-sentinel/internal/storage"
)

// pathPrefix 是代理路由的固定前缀，转发时会被剥离
const pathPrefix = "/proxy"

// Handler 反向代理核心处理器
type Handler struct {
	balancer     Balancer
	logger       *logger.Writer
	timeout      time.Duration
	maxBodyBytes int64
	transport    http.RoundTripper
}

// NewHandler 创建反向代理处理器
func NewHandler(b Balancer, lw *logger.Writer, timeoutSec int, maxBodyBytes int64) *Handler {
	return &Handler{
		balancer:     b,
		logger:       lw,
		timeout:      time.Duration(timeoutSec) * time.Second,
		maxBodyBytes: maxBodyBytes,
		transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: time.Duration(timeoutSec) * time.Second,
		},
	}
}

// ServeHTTP 处理 /proxy/* 请求
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	backend := h.balancer.Next()
	if backend == "" {
		writeJSONError(w, http.StatusBadGateway, "no available backend")
		return
	}

	// 读取并限制请求体
	var reqBody []byte
	if r.Body != nil && r.ContentLength != 0 {
		limited := io.LimitReader(r.Body, h.maxBodyBytes+1)
		b, err := io.ReadAll(limited)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "read request body failed")
			return
		}
		if int64(len(b)) > h.maxBodyBytes {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		reqBody = b
		r.Body = io.NopCloser(bytes.NewReader(reqBody))
		r.ContentLength = int64(len(reqBody))
	}

	target, err := url.Parse(backend)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "invalid backend url")
		return
	}

	start := time.Now()
	rec := newResponseRecorder(w, h.maxBodyBytes)

	// 计算转发路径：剥离 /proxy 前缀
	forwardPath := strings.TrimPrefix(r.URL.Path, pathPrefix)
	if forwardPath == "" {
		forwardPath = "/"
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = forwardPath
			req.Host = target.Host
			// 保留原始查询参数
			// X-Forwarded 头
			req.Header.Set("X-Forwarded-For", clientIP(r))
			req.Header.Set("X-Forwarded-Proto", scheme(r))
			req.Header.Set("X-Forwarded-Host", r.Host)
		},
		ModifyResponse: func(resp *http.Response) error {
			rec.setStatus(resp.StatusCode)
			rec.setHeaders(resp.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			h.balancer.MarkDown(backend)
			rec.setStatus(http.StatusBadGateway)
			writeJSONError(w, http.StatusBadGateway, "backend error: "+err.Error())
		},
		Transport: h.transport,
	}

	// 使用带超时的上下文
	r = r.WithContext(ctx)
	proxy.ServeHTTP(rec, r)

	// 记录日志
	h.logger.Write(&storage.LogRecord{
		Method:          r.Method,
		Path:            r.URL.Path,
		Query:           r.URL.RawQuery,
		RequestHeaders:  headersToJSON(r.Header),
		RequestBody:     string(reqBody),
		Status:          rec.status,
		ResponseHeaders: headersToJSON(rec.headers),
		ResponseBody:    rec.body.String(),
		Duration:        time.Since(start).Milliseconds(),
		ClientIP:        clientIP(r),
		UserAgent:       r.UserAgent(),
		Referer:         r.Referer(),
		BackendURL:      backend,
		CreatedAt:       start,
	})
}

// responseRecorder 包装 ResponseWriter 以捕获状态码、响应头和响应体（截断）
type responseRecorder struct {
	http.ResponseWriter
	status      int
	headers     http.Header
	body        *bytes.Buffer
	maxBody     int64
	wroteHeader bool
}

func newResponseRecorder(w http.ResponseWriter, maxBody int64) *responseRecorder {
	return &responseRecorder{
		ResponseWriter: w,
		status:         http.StatusOK,
		headers:        http.Header{},
		body:           &bytes.Buffer{},
		maxBody:        maxBody,
	}
}

func (r *responseRecorder) setStatus(s int)        { r.status = s }
func (r *responseRecorder) setHeaders(h http.Header) { r.headers = h.Clone() }

func (r *responseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(r.status)
	}
	// 截断保存用于日志
	if int64(r.body.Len()) < r.maxBody {
		rest := r.maxBody - int64(r.body.Len())
		r.body.Write(b[:min64(int64(len(b)), rest)])
	}
	return r.ResponseWriter.Write(b)
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// 取第一个
		if i := strings.IndexByte(v, ','); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	// 去除端口
	idx := strings.LastIndexByte(r.RemoteAddr, ':')
	if idx > 0 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
		return v
	}
	return "http"
}

func headersToJSON(h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	b, err := json.Marshal(headerMap(h))
	if err != nil {
		return ""
	}
	return string(b)
}

func headerMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		m[k] = strings.Join(v, ", ")
	}
	return m
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(msg)+12))
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":"%s"}`, msg)
}
