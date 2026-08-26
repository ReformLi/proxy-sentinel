// backend.go: 高并发 mock 后端（替代 flask dev server 做压测上游）
// 用法：go run scripts/backend.go --port 18080   # 另起一个终端 --port 18081
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	var port int
	var errorRate, delayRate float64
	var maxDelay float64
	flag.IntVar(&port, "port", 18080, "listening port")
	flag.Float64Var(&errorRate, "error-rate", 0, "random error rate percent (0-100)")
	flag.Float64Var(&delayRate, "delay-rate", 0, "random delay rate percent (0-100)")
	flag.Float64Var(&maxDelay, "max-delay", 1.0, "max random delay seconds")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// /status/<code> 和 /delay/<sec> 的语法保持和 proxy_backend.py 兼容
		path := strings.TrimPrefix(r.URL.Path, "/")
		if parts := strings.SplitN(path, "/", 2); len(parts) == 2 {
			switch parts[0] {
			case "status":
				if code, err := strconv.Atoi(parts[1]); err == nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(code)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"message":     fmt.Sprintf("Manual status %d", code),
						"server_port": port,
					})
					return
				}
			case "delay":
				if sec, err := strconv.ParseFloat(parts[1], 64); err == nil {
					time.Sleep(time.Duration(sec * float64(time.Second)))
				}
			}
		}
		if path == "health" || r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "port": port})
			return
		}

		// 随机错误率
		if errorRate > 0 && rand01()*100 < errorRate {
			code := []int{400, 404, 429, 500, 502, 503, 504}[int(rand01()*7)]
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "injected", "server_port": port, "code": code})
			return
		}
		// 随机延迟
		if delayRate > 0 && rand01()*100 < delayRate {
			time.Sleep(time.Duration(maxDelay * rand01() * float64(time.Second)))
		}

		// 回显：和 flask 版兼容的 server_port 字段（benchmark 用不到，但负载均衡验证用）
		resp := map[string]any{
			"method":      r.Method,
			"path":        r.URL.Path,
			"query":       r.URL.RawQuery,
			"headers":     r.Header,
			"remote_addr": r.RemoteAddr,
			"server_port": port,
			"ts":          time.Now().UnixNano(),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(resp)
	})

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	fmt.Fprintf(os.Stderr, "✅ go backend listening on %s (error-rate=%.1f%%, delay-rate=%.1f%%, max-delay=%.2fs)\n",
		addr, errorRate, delayRate, maxDelay)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// xorshift64 轻量伪随机（避免 rand 全局锁）
var state = uint64(time.Now().UnixNano() | 1)

func rand01() float64 {
	x := state
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	state = x
	return float64(x>>11) / float64(uint64(1)<<53)
}
