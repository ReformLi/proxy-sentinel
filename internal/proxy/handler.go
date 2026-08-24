package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
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
	maxBodyBytes int64     // 请求体大小上限（拒绝超限请求）
	logBodyMax   int64     // 日志记录的请求/响应体截断上限（与 maxBodyBytes 独立，防止日志缓冲撑爆内存）
	trustXFF     bool      // 是否信任入站 X-Forwarded-For/X-Real-IP（多级代理时开启）
	transport    http.RoundTripper

	rules atomic.Pointer[RuleMatcher] // 定向分流规则（灰度发布），原子替换热生效
}

// NewHandler 创建反向代理处理器
func NewHandler(b Balancer, lw *logger.Writer, timeoutSec int, maxBodyBytes, logBodyMax int64, trustXFF bool) *Handler {
	h := &Handler{
		balancer:     b,
		logger:       lw,
		timeout:      time.Duration(timeoutSec) * time.Second,
		maxBodyBytes: maxBodyBytes,
		logBodyMax:   logBodyMax,
		trustXFF:     trustXFF,
		transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: time.Duration(timeoutSec) * time.Second,
		},
	}
	h.SetRules(nil) // 初始化空规则，避免每次请求 nil 判断
	return h
}

// SetRules 热更新定向分流规则（空切片 = 清空规则；nil = 不变）
func (h *Handler) SetRules(rules []storage.RouteRule) {
	if rules == nil {
		rules = []storage.RouteRule{}
	}
	h.rules.Store(NewRuleMatcher(rules))
}

// LoadRules 返回当前生效的定向规则（供 API 回显）
func (h *Handler) LoadRules() []storage.RouteRule {
	if m := h.rules.Load(); m != nil {
		return m.rules
	}
	return []storage.RouteRule{}
}

// ServeHTTP 处理 /proxy/* 请求
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 请求链路标记：客户端带了合法 X-Request-ID 则复用（跨系统排障串联），否则生成
	requestID := resolveRequestID(r.Header.Get("X-Request-ID"))
	w.Header().Set("X-Request-ID", requestID) // 回写响应头，客户端报障时可直接提供 ID

	// 后端选择：定向规则优先（灰度发布：测试账号固定进指定版本，即使该版本不健康也转发——
	// 定向语义下 fallback 到其他版本会破坏测试预期），未命中走负载均衡策略
	strippedPath := strings.TrimPrefix(r.URL.Path, pathPrefix)
	var backend string
	if b, hit := h.rules.Load().Match(r.Header, r.Cookies(), strippedPath); hit {
		backend = b
	} else {
		backend = h.balancer.Next()
	}
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
	// 日志缓冲使用独立的截断上限，避免大响应把内存撑爆
	rec := newResponseRecorder(w, h.logBodyMax)

	// 计算转发路径：剥离 /proxy 前缀，并保留 backend URL 自带的路径前缀
	forwardPath := joinURLPath(target.Path, strippedPath)

	// 在代理前确定客户端 IP（用于日志与 XFF），避免被伪造头污染
	client := h.clientIP(r)

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = forwardPath
			req.Host = target.Host
			// X-Forwarded 头：追加而非覆盖，保留可信的多级代理链
			appendXFF(req, client, h.trustXFF)
			req.Header.Set("X-Forwarded-Proto", scheme(r))
			req.Header.Set("X-Forwarded-Host", r.Host)
			req.Header.Set("X-Request-ID", requestID) // 透传给后端，便于全链路对齐
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

	// 记录日志（请求体按日志上限截断存储；logger 缺失时跳过记录不影响转发）
	if h.logger != nil {
		h.writeLog(r, reqBody, rec, start, client, backend, requestID)
	}
}

func (h *Handler) writeLog(r *http.Request, reqBody []byte, rec *responseRecorder, start time.Time, client, backend, requestID string) {
	h.logger.Write(&storage.LogRecord{
		Method:          r.Method,
		Path:            r.URL.Path,
		Query:           r.URL.RawQuery,
		RequestHeaders:  headersToJSON(r.Header),
		RequestBody:     truncateString(string(reqBody), h.logBodyMax),
		Status:          rec.status,
		ResponseHeaders: headersToJSON(rec.headers),
		ResponseBody:    rec.body.String(),
		Duration:        time.Since(start).Milliseconds(),
		ClientIP:        client,
		UserAgent:       r.UserAgent(),
		Referer:         r.Referer(),
		BackendURL:      backend,
		RequestID:       requestID,
		CreatedAt:       start,
	})
}

// resolveRequestID 复用入站 X-Request-ID 或生成新 ID（"req-" + 16 位十六进制）
// 入站值仅接受 8~128 位可打印 ASCII（防头注入与脏数据），否则忽略并重新生成
func resolveRequestID(inbound string) string {
	if inbound != "" && len(inbound) >= 8 && len(inbound) <= 128 {
		ok := true
		for i := 0; i < len(inbound); i++ {
			if inbound[i] < 0x21 || inbound[i] > 0x7E { // 非可打印 ASCII（含空格）即拒绝
				ok = false
				break
			}
		}
		if ok {
			return inbound
		}
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败极罕见，退化为时间戳保证功能可用
		return "req-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "req-" + hex.EncodeToString(b[:])
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

func (r *responseRecorder) setStatus(s int)         { r.status = s }
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

// Flush 透传 Flush，保证 SSE / 大文件流式下载不被日志记录器破坏
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 透传连接劫持，支持 WebSocket 升级等场景
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// clientIP 解析客户端 IP；仅当 trustXFF 开启时才信任入站转发头，防止伪造
func (h *Handler) clientIP(r *http.Request) string {
	if h.trustXFF {
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			// 取第一个（最原始来源）
			if i := strings.IndexByte(v, ','); i >= 0 {
				return strings.TrimSpace(v[:i])
			}
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Real-IP"); v != "" {
			return v
		}
	}
	return remoteIP(r)
}

// remoteIP 仅从 TCP 连接信息提取 IP（不信任任何请求头）；用 SplitHostPort 正确处理 IPv6
func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// appendXFF 设置发往后端的 X-Forwarded-For：
// 信任模式下追加（保留上游链路），否则直接覆盖为本连接 IP（清除伪造值）
func appendXFF(req *http.Request, client string, trust bool) {
	if trust {
		if prior := req.Header.Get("X-Forwarded-For"); prior != "" {
			req.Header.Set("X-Forwarded-For", prior+", "+client)
			return
		}
	}
	req.Header.Set("X-Forwarded-For", client)
}

// joinURLPath 拼接 backend 自带路径与转发路径，正确处理斜杠
func joinURLPath(base, fwd string) string {
	if fwd == "" {
		fwd = "/"
	}
	if base == "" || base == "/" {
		return fwd
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(fwd, "/")
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

func truncateString(s string, max int64) string {
	if int64(len(s)) <= max {
		return s
	}
	return s[:max]
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// writeJSONError 用 json.Marshal 序列化，避免消息含引号/换行时响应体断裂
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	body, _ := json.Marshal(map[string]string{"error": msg})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(code)
	w.Write(body)
}
