package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
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
	balancer       Balancer
	logger         *logger.Writer
	timeout        time.Duration
	maxBodyBytes   int64 // 请求体大小上限：超过此值走流式透传（不读进内存）
	maxUploadBytes int64 // 流式透传上限（文件上传/大请求体，0=不限制）；超出返回 413
	logBodyMax     int64 // 日志记录的请求/响应体截断上限（与 maxBodyBytes 独立，防止日志缓冲撑爆内存）
	trustXFF       bool  // 是否信任入站 X-Forwarded-For/X-Real-IP（多级代理时开启）
	transport      http.RoundTripper

	rules atomic.Pointer[RuleMatcher] // 定向分流规则（灰度发布），原子替换热生效
	rewrites atomic.Pointer[Rewriter] // 路径重写规则，原子替换热生效
}

// NewHandler 创建反向代理处理器
func NewHandler(b Balancer, lw *logger.Writer, timeoutSec int, maxBodyBytes, maxUploadBytes, logBodyMax int64, trustXFF bool) *Handler {
	h := &Handler{
		balancer:       b,
		logger:         lw,
		timeout:        time.Duration(timeoutSec) * time.Second,
		maxBodyBytes:   maxBodyBytes,
		maxUploadBytes: maxUploadBytes,
		logBodyMax:     logBodyMax,
		trustXFF:       trustXFF,
		transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: time.Duration(timeoutSec) * time.Second,
		},
	}
	h.SetRules(nil)     // 初始化空规则，避免每次请求 nil 判断
	h.SetRewrites(nil)
	return h
}

// SetRewrites 热更新路径重写规则（空切片 = 清空；nil = 清空）
func (h *Handler) SetRewrites(rules []storage.RewriteRule) {
	if rules == nil {
		rules = []storage.RewriteRule{}
	}
	h.rewrites.Store(NewRewriter(rules))
}

// LoadRewrites 返回当前生效的重写规则（供 API 回显）
func (h *Handler) LoadRewrites() []storage.RewriteRule {
	if rw := h.rewrites.Load(); rw != nil {
		return rw.rules
	}
	return []storage.RewriteRule{}
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

	// 请求体处理：小请求读进内存（可记日志 body），大请求流式透传（不占内存）
	// - ContentLength <= maxBodyBytes：读进内存，日志可记 body
	// - ContentLength > maxBodyBytes 或 chunked（未知大小）：流式透传，不读进内存
	//   - 已知大小超 maxUploadBytes：直接 413（body 一个字节都没读）
	//   - 未知大小（chunked）：用 MaxBytesReader 边读边限，超限中断
	var reqBody []byte
	if r.Body != nil && r.ContentLength != 0 {
		cl := r.ContentLength
		if cl > h.maxBodyBytes || cl < 0 {
			// 大请求体或 chunked：流式透传，不读进内存
			if cl > 0 && h.maxUploadBytes > 0 && cl > h.maxUploadBytes {
				// 已知大小超上传限制：直接拒绝（body 一个字节都没读）
				writeJSONError(w, http.StatusRequestEntityTooLarge, "upload body too large")
				return
			}
			// chunked 或大请求：用 MaxBytesReader 包装防超限（maxUploadBytes=0 表示不限制）
			if h.maxUploadBytes > 0 {
				r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
			}
			// 日志记元信息标记（body 未记录，排障时可见大小）
			if cl > 0 {
				reqBody = []byte("[streamed " + strconv.FormatInt(cl, 10) + " bytes]")
			} else {
				reqBody = []byte("[streamed chunked]")
			}
		} else {
			// 小请求体：读进内存用于日志记录
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
	}

	target, err := url.Parse(backend)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "invalid backend url")
		return
	}

	start := time.Now()
	// 日志缓冲使用独立的截断上限，避免大响应把内存撑爆
	rec := newResponseRecorder(w, h.logBodyMax)

	// 计算转发路径：先按重写规则替换前缀（仅对已选定后端生效的规则），
	// 再拼接 backend URL 自带的路径前缀。日志记录的仍是客户端原始路径。
	forwardPath := joinURLPath(target.Path, h.rewrites.Load().Apply(strippedPath, backend))

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
			req.Header.Set("X-Forwarded-Proto", scheme(r, h.trustXFF))
			req.Header.Set("X-Forwarded-Host", r.Host)
			req.Header.Set("X-Request-ID", requestID) // 透传给后端，便于全链路对齐
		},
		ModifyResponse: func(resp *http.Response) error {
			rec.setStatus(resp.StatusCode)
			rec.setHeaders(resp.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			// 客户端主动断开（context canceled）：后端无责，不标记下线——
			// 否则高并发下大量客户端取消（弱网/超时放弃）会把健康后端连续"误伤"下线，
			// 流量集中到剩余后端甚至全量返回 no available backend
			if errors.Is(err, context.Canceled) {
				rec.setStatus(499) // nginx 惯例码：client closed request（此时写入已无接收方，仅用于日志统计）
				return
			}
			h.balancer.MarkDown(backend)
			rec.setStatus(http.StatusBadGateway)
			// 对外统一文案：err 可能含后端内网地址/端口/超时配置，直接回显会泄漏内部拓扑
			log.Printf("[proxy] backend error: backend=%s path=%s err=%v", backend, req.URL.Path, err)
			writeJSONError(w, http.StatusBadGateway, "backend unavailable")
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

// scheme 判断发往后端的协议：
// - 直连 TLS → https
// - trustXFF 开启（多级反代场景）才信任 X-Forwarded-Proto，否则该头可被客户端伪造
// - 默认 http
func scheme(r *http.Request, trustXFF bool) string {
	if r.TLS != nil {
		return "https"
	}
	if trustXFF {
		if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
			return v
		}
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
