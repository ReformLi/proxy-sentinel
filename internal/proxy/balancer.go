package proxy

import (
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
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
	// SetBackends 替换后端列表（新节点默认健康）
	SetBackends(urls []string)
	// SetStrategy 切换负载均衡策略
	SetStrategy(strategy string) error
	// Strategy 当前策略
	Strategy() string
}

// BackendStat 后端状态信息
type BackendStat struct {
	URL     string `json:"url"`
	Healthy bool   `json:"healthy"`
}

// dynamicBalancer 支持 round_robin / random 双策略与运行时热更新
type dynamicBalancer struct {
	mu       sync.RWMutex
	strategy string
	backends []string
	healthy  map[string]bool
	counter  uint64 // 轮询计数（atomic）
}

// NewBalancer 创建负载均衡器（返回支持热更新的实例）
func NewBalancer(strategy string, backends []string) DynamicManager {
	healthy := make(map[string]bool, len(backends))
	for _, b := range backends {
		healthy[b] = true
	}
	return &dynamicBalancer{
		strategy: normalizeStrategy(strategy),
		backends: backends,
		healthy:  healthy,
	}
}

func normalizeStrategy(s string) string {
	if s == "random" {
		return "random"
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
			if b.healthy[be] {
				healthy = append(healthy, be)
			}
		}
		if len(healthy) == 0 {
			return b.backends[rand.Intn(n)] // 全不健康时兜底，让上层返回错误
		}
		return healthy[rand.Intn(len(healthy))]
	default: // round_robin
		idx := int(atomic.AddUint64(&b.counter, 1)-1) % n
		for i := 0; i < n; i++ {
			candidate := b.backends[(idx+i)%n]
			if b.healthy[candidate] {
				return candidate
			}
		}
		return b.backends[0] // 全不健康时兜底
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
		out = append(out, BackendStat{URL: be, Healthy: b.healthy[be]})
	}
	return out
}

// SetBackends 替换后端列表：旧节点保留健康状态，新节点默认健康
func (b *dynamicBalancer) SetBackends(urls []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	newHealthy := make(map[string]bool, len(urls))
	for _, u := range urls {
		newHealthy[u] = true // 新节点默认健康；同名旧节点也会恢复初始态
	}
	b.backends = urls
	b.healthy = newHealthy
}

func (b *dynamicBalancer) SetStrategy(strategy string) error {
	if strategy != "round_robin" && strategy != "random" {
		return fmt.Errorf("非法的负载均衡策略: %s（仅支持 round_robin/random）", strategy)
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

// HealthChecker 定期探测后端健康状态（探测成功自动恢复 MarkUp）
type HealthChecker struct {
	balancer Balancer
	client   *http.Client
	interval time.Duration
	done     chan struct{}
}

// NewHealthChecker 创建健康检查器
func NewHealthChecker(b Balancer, interval time.Duration) *HealthChecker {
	return &HealthChecker{
		balancer: b,
		client:   &http.Client{Timeout: 3 * time.Second},
		interval: interval,
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
		go func(url string) {
			resp, err := h.client.Get(url)
			if err != nil {
				h.balancer.MarkDown(url)
				return
			}
			resp.Body.Close()
			// 5xx 视为不健康，其余（含 4xx）视为存活
			if resp.StatusCode >= 500 {
				h.balancer.MarkDown(url)
			} else {
				h.balancer.MarkUp(url)
			}
		}(be.URL)
	}
}
