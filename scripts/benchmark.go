// benchmark.go: proxy-sentinel 单机压测工具
// 用法：go run scripts/benchmark.go --url=http://127.0.0.1:18000/proxy/status/200 \
//                                  --token=bench-token-0001 \
//                                  --duration=60s --concurrency=200
package main

import (
	"context"
	"crypto/tls"
	"encoding/csv"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	totalRequests atomic.Uint64
	totalErrors   atomic.Uint64
	ok2xx         atomic.Uint64
	err4xx        atomic.Uint64
	err5xx        atomic.Uint64
)

type options struct {
	url         string
	token       string
	duration    time.Duration
	concurrency int
	keepAlive   bool
	noKeepAlive bool
	method      string
	timeout     time.Duration
	outCSV      string
}

func newTransport(keepAlive bool) *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DisableCompression:    false,
		DisableKeepAlives:     !keepAlive,
		MaxIdleConns:          5000,
		MaxIdleConnsPerHost:   5000,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2: false,
	}
}

func worker(ctx context.Context, client *http.Client, opt *options, wg *sync.WaitGroup, latencies *[]float64, mu *sync.Mutex) {
	defer wg.Done()
	localLat := make([]float64, 0, 2048)
	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			*latencies = append(*latencies, localLat...)
			mu.Unlock()
			return
		default:
		}
		req, err := http.NewRequestWithContext(ctx, opt.method, opt.url, nil)
		if err != nil {
			totalErrors.Add(1)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+opt.token)
		req.Header.Set("User-Agent", "sentinel-bench/1.0")
		start := time.Now()
		resp, err := client.Do(req)
		d := time.Since(start).Seconds() * 1000.0
		localLat = append(localLat, d)
		totalRequests.Add(1)
		if err != nil {
			totalErrors.Add(1)
			continue
		}
		// 必须读完并关闭，否则连接不回收
		var discard [2048]byte
		for resp.Body != nil {
			_, err := resp.Body.Read(discard[:])
			if err != nil {
				break
			}
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			ok2xx.Add(1)
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			err4xx.Add(1)
			totalErrors.Add(1)
		case resp.StatusCode >= 500:
			err5xx.Add(1)
			totalErrors.Add(1)
		}
	}
}

func main() {
	var opt options
	flag.StringVar(&opt.url, "url", "http://127.0.0.1:18000/proxy/status/200", "target URL (gateway proxy endpoint)")
	flag.StringVar(&opt.token, "token", "bench-token-0001", "Bearer token")
	flag.DurationVar(&opt.duration, "duration", 30*time.Second, "test duration")
	flag.IntVar(&opt.concurrency, "concurrency", 200, "number of concurrent workers")
	flag.BoolVar(&opt.noKeepAlive, "no-keepalive", false, "disable HTTP keep-alive (default: enabled)")
	flag.StringVar(&opt.method, "method", "GET", "HTTP method")
	flag.DurationVar(&opt.timeout, "timeout", 15*time.Second, "per-request timeout")
	flag.StringVar(&opt.outCSV, "csv", "", "output raw latency CSV path (optional, heavy)")
	flag.Parse()

	opt.keepAlive = !opt.noKeepAlive

	// 预检
	fmt.Printf("🚀 Proxy-Sentinel Benchmark\n")
	fmt.Printf("   target      : %s\n", opt.url)
	fmt.Printf("   concurrency : %d\n", opt.concurrency)
	fmt.Printf("   duration    : %s\n", opt.duration)
	fmt.Printf("   keep-alive  : %v\n", opt.keepAlive)
	fmt.Printf("   method      : %s\n", opt.method)

	preclient := &http.Client{Timeout: 5 * time.Second, Transport: newTransport(true)}
	req, _ := http.NewRequest(opt.method, opt.url, nil)
	req.Header.Set("Authorization", "Bearer "+opt.token)
	pr, err := preclient.Do(req)
	if err != nil {
		fmt.Printf("\n❌ 预检失败：网关不可达：%v\n", err)
		os.Exit(1)
	}
	// drain body
	var db [512]byte
	for pr.Body != nil {
		_, err := pr.Body.Read(db[:])
		if err != nil {
			break
		}
	}
	if pr.Body != nil {
		pr.Body.Close()
	}
	if pr.StatusCode < 200 || pr.StatusCode >= 300 {
		fmt.Printf("\n❌ 预检失败：状态码 %d。请检查 sentinel 是否启动、token 是否正确、后端是否可用。\n", pr.StatusCode)
		os.Exit(1)
	}
	fmt.Printf("   preflight   : OK (HTTP %d)\n\n", pr.StatusCode)

	// 捕获 Ctrl+C，提前终止
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		fmt.Println("\n⌨️  收到中断，提前停止...")
		cancel()
	}()

	// 启动 worker
	var (
		wg        sync.WaitGroup
		latencies []float64
		mu        sync.Mutex
	)
	start := time.Now()
	tr := newTransport(opt.keepAlive)
	client := &http.Client{
		Timeout:   opt.timeout,
		Transport: tr,
	}
	for i := 0; i < opt.concurrency; i++ {
		wg.Add(1)
		go worker(ctx, client, &opt, &wg, &latencies, &mu)
	}

	// 定时报告
	stopTicker := make(chan struct{})
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		prev := uint64(0)
		prevT := start
		first := true
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopTicker:
				return
			case now := <-tick.C:
				cur := totalRequests.Load()
				elapsed5 := now.Sub(prevT).Seconds()
				qps5 := float64(cur-prev) / elapsed5
				if !first {
					fmt.Printf("   %6.1fs │ 累计 %8d req │ 最近5s QPS=%.0f\n", now.Sub(start).Seconds(), cur, qps5)
				}
				first = false
				prev = cur
				prevT = now
			}
		}
	}()

	// 到时间后取消
	select {
	case <-time.After(opt.duration):
		cancel()
	case <-ctx.Done():
	}
	wg.Wait()
	close(stopTicker)
	elapsed := time.Since(start).Seconds()

	// 统计
	total := totalRequests.Load()
	totalErr := totalErrors.Load()
	ok := ok2xx.Load()
	e4 := err4xx.Load()
	e5 := err5xx.Load()
	qps := float64(total) / elapsed

	sort.Float64s(latencies)
	n := len(latencies)
	p := func(pct float64) float64 {
		if n == 0 {
			return 0
		}
		idx := int(float64(n-1) * pct)
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		return latencies[idx]
	}
	avg := 0.0
	if n > 0 {
		s := 0.0
		for _, x := range latencies {
			s += x
		}
		avg = s / float64(n)
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Println("              压 测 结 果 汇 总")
	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Printf("  目标 URL         : %s\n", opt.url)
	fmt.Printf("  并发 worker      : %d\n", opt.concurrency)
	fmt.Printf("  HTTP Keep-Alive  : %v\n", opt.keepAlive)
	fmt.Printf("  实际运行时长     : %.2f s\n", elapsed)
	fmt.Printf("  总请求数         : %d\n", total)
	fmt.Printf("  └─ 成功 2xx      : %d (%.1f%%)\n", ok, 100.0*float64(ok)/float64(max1(total)))
	fmt.Printf("  └─ 错误 4xx      : %d (%.1f%%)\n", e4, 100.0*float64(e4)/float64(max1(total)))
	fmt.Printf("  └─ 错误 5xx      : %d (%.1f%%)\n", e5, 100.0*float64(e5)/float64(max1(total)))
	fmt.Printf("  └─ 网络/超时     : %d\n", totalErr-e4-e5)
	fmt.Printf("  整体 QPS         : %.0f req/s\n", qps)
	fmt.Printf("  延迟分布 (n=%d):\n", n)
	fmt.Printf("    P50 (median)   : %.2f ms\n", p(0.50))
	fmt.Printf("    P90            : %.2f ms\n", p(0.90))
	fmt.Printf("    P99            : %.2f ms\n", p(0.99))
	fmt.Printf("    P99.9          : %.2f ms\n", p(0.999))
	fmt.Printf("    AVG            : %.2f ms\n", avg)
	fmt.Printf("    MAX            : %.2f ms\n", p(1.0))
	fmt.Println("═══════════════════════════════════════════════════════")

	// 验收结论
	passQPS := qps >= 2000
	passLatP99 := p(0.99) < 100
	passErr := totalErr == 0
	fmt.Printf("\n🎯 验收指标:\n")
	fmt.Printf("   QPS ≥ 2000            : %v  (实际 %.0f)\n", passQPS, qps)
	fmt.Printf("   P99 延迟 < 100ms      : %v  (实际 %.2f ms)\n", passLatP99, p(0.99))
	fmt.Printf("   零错误（4xx/5xx/超时）: %v  (实际 %d)\n", passErr, totalErr)
	if passQPS && passLatP99 && passErr {
		fmt.Println("\n✅ 全部验收通过")
	} else {
		fmt.Println("\n⚠️  部分指标未达标（见上）")
	}

	// 写 CSV
	if opt.outCSV != "" && n > 0 {
		f, err := os.Create(opt.outCSV)
		if err == nil {
			w := csv.NewWriter(f)
			_ = w.Write([]string{"latency_ms"})
			buf := make([]string, 1)
			for _, v := range latencies {
				buf[0] = fmt.Sprintf("%.3f", v)
				_ = w.Write(buf)
			}
			w.Flush()
			f.Close()
			fmt.Printf("\n💾 原始延迟样本已写入: %s\n", opt.outCSV)
		} else {
			fmt.Printf("\n⚠️  写 CSV 失败: %v\n", err)
		}
	}
}

func max1(v uint64) uint64 {
	if v == 0 {
		return 1
	}
	return v
}
