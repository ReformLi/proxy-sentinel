package proxy

import (
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
	// MarkDown 标记某后端不可用
	MarkDown(backend string)
	// Backends 返回所有已配置后端（含健康状态）
	Backends() []BackendStat
}

// BackendStat 后端状态信息
type BackendStat struct {
	URL     string `json:"url"`
	Healthy bool   `json:"healthy"`
}

// roundRobinBalancer 轮询负载均衡
type roundRobinBalancer struct {
	backends []string
	healthy  map[string]bool
	mu       sync.RWMutex
	counter  uint64
}

// NewBalancer 根据策略创建负载均衡器
func NewBalancer(strategy string, backends []string) Balancer {
	healthy := make(map[string]bool, len(backends))
	for _, b := range backends {
		healthy[b] = true
	}
	rb := &roundRobinBalancer{
		backends: backends,
		healthy:  healthy,
	}
	switch strategy {
	case "random":
		return &randomBalancer{backends: backends, healthy: healthy}
	default:
		return rb
	}
}

func (b *roundRobinBalancer) Next() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := len(b.backends)
	if n == 0 {
		return ""
	}
	// 尝试 n 次，跳过不健康后端
	idx := int(atomic.AddUint64(&b.counter, 1)-1) % n
	for i := 0; i < n; i++ {
		candidate := b.backends[(idx+i)%n]
		if b.healthy[candidate] {
			return candidate
		}
	}
	// 全部不健康时返回第一个（兜底，让上层报错）
	return b.backends[0]
}

func (b *roundRobinBalancer) MarkDown(backend string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.healthy[backend] = false
}

func (b *roundRobinBalancer) Backends() []BackendStat {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]BackendStat, 0, len(b.backends))
	for _, be := range b.backends {
		out = append(out, BackendStat{URL: be, Healthy: b.healthy[be]})
	}
	return out
}

// randomBalancer 随机负载均衡
type randomBalancer struct {
	backends []string
	healthy  map[string]bool
	mu       sync.RWMutex
	rnd      *rand.Rand
}

func (b *randomBalancer) Next() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.rnd == nil {
		b.rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	n := len(b.backends)
	if n == 0 {
		return ""
	}
	// 收集健康后端
	healthy := make([]string, 0, n)
	for _, be := range b.backends {
		if b.healthy[be] {
			healthy = append(healthy, be)
		}
	}
	if len(healthy) == 0 {
		return b.backends[b.rnd.Intn(n)]
	}
	return healthy[b.rnd.Intn(len(healthy))]
}

func (b *randomBalancer) MarkDown(backend string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.healthy[backend] = false
}

func (b *randomBalancer) Backends() []BackendStat {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]BackendStat, 0, len(b.backends))
	for _, be := range b.backends {
		out = append(out, BackendStat{URL: be, Healthy: b.healthy[be]})
	}
	return out
}

// HealthChecker 定期探测后端健康状态
type HealthChecker struct {
	balancer   Balancer
	client     *http.Client
	interval   time.Duration
	done       chan struct{}
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
			// 任意响应即视为可达；5xx 视为不健康
			if resp.StatusCode >= 500 {
				h.balancer.MarkDown(url)
			}
		}(be.URL)
	}
}
