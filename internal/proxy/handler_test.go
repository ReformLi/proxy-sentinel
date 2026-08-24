package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"proxy-sentinel/internal/storage"
)

// TestRequestIDHeader 验证：每个代理请求都会生成/复用 X-Request-ID，
// 并回写到响应头、注入转发头
func TestRequestIDHeader(t *testing.T) {
	var mu sync.Mutex
	var gotForwardedID string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotForwardedID = r.Header.Get("X-Request-ID")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h := &Handler{
		balancer:   NewBalancer("round_robin", []storage.WeightedBackend{{URL: backend.URL, Weight: 1}}),
		timeout:    5 * time.Second,
		maxBodyBytes: 1024,
		logBodyMax: 1024,
		transport:  http.DefaultTransport,
	}
	h.SetRules(nil)

	// 1. 自动生成
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/proxy/get", nil)
	h.ServeHTTP(w, req)
	respID := w.Header().Get("X-Request-ID")
	if respID == "" {
		t.Fatalf("响应头缺少 X-Request-ID（headers=%v）", w.Header())
	}
	if respID != gotForwardedID {
		t.Fatalf("转发给后端的 ID(%q) 与响应头 ID(%q) 不一致", gotForwardedID, respID)
	}

	// 2. 复用入站 ID
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/proxy/get", nil)
	req2.Header.Set("X-Request-ID", "my-custom-trace-id-001")
	h.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Request-ID") != "my-custom-trace-id-001" {
		t.Fatalf("未复用入站 ID，got=%q", w2.Header().Get("X-Request-ID"))
	}
	if gotForwardedID != "my-custom-trace-id-001" {
		t.Fatalf("转发未复用入站 ID，got=%q", gotForwardedID)
	}

	// 3. 非法入站 ID（含空格/控制字符）忽略并重新生成
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/proxy/get", nil)
	req3.Header.Set("X-Request-ID", "bad id with spaces\r\n")
	h.ServeHTTP(w3, req3)
	id3 := w3.Header().Get("X-Request-ID")
	if id3 == "bad id with spaces\r\n" || len(id3) < 8 {
		t.Fatalf("非法入站 ID 未被忽略，got=%q", id3)
	}
}
