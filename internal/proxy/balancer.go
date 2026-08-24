package proxy

import (
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"proxy-sentinel/internal/storage"
)

// Balancer 负载均衡器接口
type Balancer interface {
	// Next 返回一个健康的后端地址；无可用后端时返回空字符串
	Next() string
	// MarkUp 标记某后端恢复可用
	MarkUp(backend string)
	// MarkDown 标记某后端不可用
	MarkDown(backend string)
	// Backends 返回所有已配置后端（含健康状态）
	Backends() []BackendStat
}

// DynamicManager 可运行时调整的负载均衡器能力（/settings 页面使用）
type DynamicManager interface {
	Balancer
	// SetBackends 替换后端列表（带权重；新节点默认健康）
	SetBackends(backends []storage.WeightedBackend)
	// SetStrategy 切换负载均衡策略
	SetStrategy(strategy string) error
	// Strategy 当前策略
	Strategy() string
}

// BackendStat 后端状态信息
type BackendStat struct {
	URL        string `json:"url"`
	Healthy    bool   `json:"healthy"`
	Weight     int    `json:"weight"`
	HealthPath string `json:"health_path"` // 健康检查探测路径（空 = /）
}

// dynamicBalancer 支持 round_robin / random / weighted 策略与运行时热更新
type dynamicBalancer struct {
	mu       sync.RWMutex
	strategy string
	backends []storage.WeightedBackend
	healthy  map[string]bool
	counter  uint64 // 轮询计数（atomic）
}

// NewBalancer 创建负载均衡器（返回支持热更新的实例）
func NewBalancer(strategy string, backends []storage.WeightedBackend) DynamicManager {
	healthy := make(map[string]bool, len(backends))
	for _, b := range backends {
		healthy[b.URL] = true
	}
	return &dynamicBalancer{
		strategy: normalizeStrategy(strategy),
		backends: backends,
		healthy:  healthy,
	}
}

func normalizeStrategy(s string) string {
	if s == "random" || s == "weighted" {
		return s
	}
	return "round_robin"
}

func (b *dynamicBalancer) Next() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := len(b.backends)
	if n == 0 {
		return ""
	}
	switch b.strategy {
	case "random":
		healthy := make([]string, 0, n)
		for _, be := range b.backends {
			if b.healthy[be.URL] {
				healthy = append(healthy, be.URL)
			}
		}
		if len(healthy) == 0 {
			return b.backends[rand.Intn(n)].URL // 全不健康时兜底，让上层返回错误
		}
		return healthy[rand.Intn(len(healthy))]
	case "weighted":
		// 加权随机（灰度发布）：权重 0 的后端不接流量；
		// 健康检查剔除后剩余后端按权重归一化分配
		total := 0
		for _, be := range b.backends {
			if b.healthy[be.URL] && be.Weight > 0 {
				total += be.Weight
			}
		}
		if total == 0 {
			return "" // 无可接流量的后端
		}
		p := rand.Intn(total)
		for _, be := range b.backends {
			if !b.healthy[be.URL] || be.Weight <= 0 {
				continue
			}
			p -= be.Weight
			if p < 0 {
				return be.URL
			}
		}
		return "" // 不可达（total>0 时必有命中）
	default: // round_robin
		idx := int(atomic.AddUint64(&b.counter, 1)-1) % n
		for i := 0; i < n; i++ {
			candidate := b.backends[(idx+i)%n]
			if b.healthy[candidate.URL] {
				return candidate.URL
			}
		}
		return b.backends[0].URL // 全不健康时兜底
	}
}

func (b *dynamicBalancer) MarkDown(backend string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.healthy[backend]; ok {
		b.healthy[backend] = false
	}
}

func (b *dynamicBalancer) MarkUp(backend string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.healthy[backend]; ok {
		b.healthy[backend] = true
	}
}

func (b *dynamicBalancer) Backends() []BackendStat {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]BackendStat, 0, len(b.backends))
	for _, be := range b.backends {
		out = append(out, BackendStat{URL: be.URL, Healthy: b.healthy[be.URL], Weight: be.Weight, HealthPath: be.HealthPath})
	}
	return out
}

// SetBackends 替换后端列表：旧节点保留健康状态，新节点默认健康
func (b *dynamicBalancer) SetBackends(backends []storage.WeightedBackend) {
	b.mu.Lock()
	defer b.mu.Unlock()
	newHealthy := make(map[string]bool, len(backends))
	for _, wb := range backends {
		newHealthy[wb.URL] = true // 新节点默认健康；同名旧节点也会恢复初始态
	}
	b.backends = backends
	b.healthy = newHealthy
}

func (b *dynamicBalancer) SetStrategy(strategy string) error {
	if strategy != "round_robin" && strategy != "random" && strategy != "weighted" {
		return fmt.Errorf("非法的负载均衡策略: %s（仅支持 round_robin/random/weighted）", strategy)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.strategy = strategy
	return nil
}

func (b *dynamicBalancer) Strategy() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.strategy
}

// ProbeResult 单次健康探测结果（供落库与告警联动使用）
type ProbeResult struct {
	URL        string `json:"url"`
	Healthy    bool   `json:"healthy"`
	LatencyMs  int64  `json:"latency_ms"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error,omitempty"`
}

// ProbeRecorder 探测结果记录器（健康检查器每轮探测后回调；可为 nil）
type ProbeRecorder func(res ProbeResult)

// HealthChecker 定期探测后端健康状态（探测成功自动恢复 MarkUp）
type HealthChecker struct {
	balancer Balancer
	client   *http.Client
	interval time.Duration
	recorder ProbeRecorder
	done     chan struct{}
}

// NewHealthChecker 创建健康检查器；recorder 可为 nil（不记录探测明细）
func NewHealthChecker(b Balancer, interval time.Duration, recorder ProbeRecorder) *HealthChecker {
	return &HealthChecker{
		balancer: b,
		client:   &http.Client{
			// 探测超时须短于探测间隔，避免堆积
			Timeout: func() time.Duration {
				if interval > 5*time.Second {
					return 3 * time.Second
				}
				return interval / 2
			}(),
		},
		interval: interval,
		recorder: recorder,
		done:     make(chan struct{}),
	}
}

// Start 启动后台健康检查
func (h *HealthChecker) Start() {
	go func() {
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				h.check()
			case <-h.done:
				return
			}
		}
	}()
}

// Stop 停止健康检查
func (h *HealthChecker) Stop() { close(h.done) }

func (h *HealthChecker) check() {
	for _, be := range h.balancer.Backends() {
		go func(url, path string) {
			if path == "" {
				path = "/"
			}
			start := time.Now()
			res := ProbeResult{URL: url}
			resp, err := h.client.Get(url + path)
			res.LatencyMs = time.Since(start).Milliseconds()
			if err != nil {
				res.Error = err.Error()
				h.finish(url, res, false)
				return
			}
			resp.Body.Close()
			res.StatusCode = resp.StatusCode
			// 5xx 视为不健康，其余（含 4xx）视为存活
			h.finish(url, res, resp.StatusCode < 500)
		}(be.URL, be.HealthPath)
	}
}

// finish 应用探测结果：更新均衡器健康状态并回调记录器
func (h *HealthChecker) finish(url string, res ProbeResult, healthy bool) {
	res.Healthy = healthy
	if healthy {
		h.balancer.MarkUp(url)
	} else {
		h.balancer.MarkDown(url)
	}
	if h.recorder != nil {
		h.recorder(res)
	}
}
