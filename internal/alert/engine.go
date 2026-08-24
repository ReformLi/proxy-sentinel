package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"proxy-sentinel/internal/proxy"
	"proxy-sentinel/internal/storage"
)

// Rules 告警规则（存数据库 settings 表，页面修改后立即生效，无需重启）
type Rules struct {
	Enabled        bool    `json:"enabled"`          // 总开关
	ErrorRatePct   float64 `json:"error_rate_pct"`   // 统计窗口内 5xx 占比阈值（%），0 = 关闭该规则
	WindowMinutes  int     `json:"window_minutes"`   // 错误率统计窗口（分钟）
	MinSample      int     `json:"min_sample"`       // 触发错误率告警的最小请求数（避免小样本误报）
	BackendDown    bool    `json:"backend_down"`     // 后端宕机/恢复通知
	LatencyMs      int     `json:"latency_ms"`       // 探测平均延迟阈值（ms），0 = 关闭该规则
	SilenceMinutes int     `json:"silence_minutes"`  // 静默期：同一告警 N 分钟内不重复发送
}

// DefaultRules 默认规则（保守：默认关闭，错误率 10% 且样本 ≥20 才告警）
var DefaultRules = Rules{
	Enabled:        false,
	ErrorRatePct:   10,
	WindowMinutes:  5,
	MinSample:      20,
	BackendDown:    true,
	LatencyMs:      0,
	SilenceMinutes: 10,
}

// Validate 校验规则取值
func (r Rules) Validate() error {
	if r.ErrorRatePct < 0 || r.ErrorRatePct > 100 {
		return fmt.Errorf("错误率阈值必须在 0~100 之间")
	}
	if r.WindowMinutes < 1 || r.WindowMinutes > 60 {
		return fmt.Errorf("统计窗口必须在 1~60 分钟之间")
	}
	if r.MinSample < 1 {
		return fmt.Errorf("最小样本量必须 ≥1")
	}
	if r.SilenceMinutes < 1 || r.SilenceMinutes > 1440 {
		return fmt.Errorf("静默期必须在 1~1440 分钟之间")
	}
	if r.LatencyMs < 0 || r.LatencyMs > 60000 {
		return fmt.Errorf("延迟阈值必须在 0~60000ms 之间（0 = 关闭）")
	}
	return nil
}

// settingKeyAlertRules settings 表中告警规则的键名
const settingKeyAlertRules = "alert_rules"

// BackendLister 提供后端健康状态快照（由负载均衡器实现）
type BackendLister interface {
	Backends() []proxy.BackendStat
}

// Engine 告警引擎：周期评估规则（后端宕机/恢复 + 错误率），命中且不在静默期时发送钉钉通知
type Engine struct {
	db       *storage.DB
	balancer BackendLister
	ding     *DingTalk
	interval time.Duration

	mu       sync.Mutex
	rules    Rules
	lastSent map[string]time.Time  // 告警键 → 上次发送时间（静默期判断）
	healthy  map[string]bool       // 上一轮后端健康快照（状态变化检测）
	done     chan struct{}
}

// NewEngine 创建告警引擎；ding 可为 nil（未配置 webhook，引擎空转不发通知）
func NewEngine(db *storage.DB, balancer BackendLister, ding *DingTalk, checkIntervalSeconds int) *Engine {
	if checkIntervalSeconds < 5 {
		checkIntervalSeconds = 30
	}
	e := &Engine{
		db:       db,
		balancer: balancer,
		ding:     ding,
		interval: time.Duration(checkIntervalSeconds) * time.Second,
		lastSent: make(map[string]time.Time),
		healthy:  make(map[string]bool),
		done:     make(chan struct{}),
	}
	e.rules = e.loadRules(context.Background())
	return e
}

// Start 启动后台评估循环
func (e *Engine) Start() {
	// 先记录一次基线快照，避免把"启动前就已宕机"的后端误报为新增故障
	e.recordBaseline()
	go func() {
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.evaluate()
			case <-e.done:
				return
			}
		}
	}()
}

// Stop 停止评估循环
func (e *Engine) Stop() { close(e.done) }

// DingConfigured 钉钉 webhook 是否已配置
func (e *Engine) DingConfigured() bool { return e.ding.Configured() }

// CheckIntervalSeconds 检查周期（秒）
func (e *Engine) CheckIntervalSeconds() int { return int(e.interval.Seconds()) }

// GetRules 返回当前规则（优先内存缓存，与数据库保持同步）
func (e *Engine) GetRules() Rules {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rules
}

// SetRules 校验、持久化并热更新规则（PUT /api/alert/config 调用，无需重启）
func (e *Engine) SetRules(ctx context.Context, r Rules) error {
	if err := r.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := e.db.SetSetting(ctx, settingKeyAlertRules, string(b)); err != nil {
		return fmt.Errorf("持久化告警规则失败: %w", err)
	}
	e.mu.Lock()
	e.rules = r
	e.mu.Unlock()
	return nil
}

// loadRules 从数据库读取规则；不存在或损坏时回退默认值
func (e *Engine) loadRules(ctx context.Context) Rules {
	rules := DefaultRules
	v, ok, err := e.db.GetSetting(ctx, settingKeyAlertRules)
	if err != nil || !ok {
		return rules
	}
	if err := json.Unmarshal([]byte(v), &rules); err != nil {
		return DefaultRules // 存量数据损坏时回退默认，不阻断启动
	}
	return rules
}

// recordBaseline 记录当前后端健康快照作为基线
func (e *Engine) recordBaseline() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.healthy = make(map[string]bool)
	for _, be := range e.balancer.Backends() {
		e.healthy[be.URL] = be.Healthy
	}
}

// evaluate 单轮评估：每轮从数据库重载规则（页面保存后最多一个周期内生效）
func (e *Engine) evaluate() {
	rules := e.loadRules(context.Background())
	e.mu.Lock()
	e.rules = rules
	e.mu.Unlock()

	// 未启用或未配置通知渠道：只维护基线快照，不做评估
	if !rules.Enabled || !e.ding.Configured() {
		e.recordBaseline()
		return
	}
	e.checkBackends(rules)
	e.checkErrorRate(rules)
	e.checkLatency(rules)
}

// checkBackends 检测后端健康状态变化（宕机/恢复）
func (e *Engine) checkBackends(rules Rules) {
	current := make(map[string]bool)
	for _, be := range e.balancer.Backends() {
		current[be.URL] = be.Healthy
	}

	e.mu.Lock()
	prev := e.healthy
	e.healthy = current
	e.mu.Unlock()

	for url, now := range current {
		before, known := prev[url]
		if !known {
			continue // 本轮新增的后端，跳过（避免改配置时误报）
		}
		switch {
		case before && !now:
			e.fire(rules, "backend-down:"+url,
				"🔴 后端宕机",
				fmt.Sprintf("**后端节点不可用**\n\n- 地址：%s\n- 时间：%s\n\n该节点已被负载均衡剔除，恢复后将自动通知。", url, time.Now().Format("2006-01-02 15:04:05")))
		case !before && now:
			e.mu.Lock()
			delete(e.lastSent, "backend-down:"+url) // 恢复后清除宕机静默记录，下次故障立即告警
			e.mu.Unlock()
			e.fireSilenceBypass("backend-up:"+url,
				"🟢 后端恢复",
				fmt.Sprintf("**后端节点已恢复**\n\n- 地址：%s\n- 时间：%s\n\n节点已重新参与负载均衡。", url, time.Now().Format("2006-01-02 15:04:05")))
		}
	}
}

// checkErrorRate 检测统计窗口内 5xx 错误率
func (e *Engine) checkErrorRate(rules Rules) {
	if rules.ErrorRatePct <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	from := time.Now().Add(-time.Duration(rules.WindowMinutes) * time.Minute)
	total, errCount, err := e.db.GetWindowCounts(ctx, from)
	if err != nil {
		log.Printf("告警引擎：查询错误率统计失败: %v", err)
		return
	}
	if total < int64(rules.MinSample) {
		return // 样本不足，不判定
	}
	rate := float64(errCount) / float64(total) * 100
	if rate < rules.ErrorRatePct {
		return
	}
	e.fire(rules, "error-rate",
		"⚠️ 错误率超阈值",
		fmt.Sprintf("**代理错误率超过阈值**\n\n- 当前错误率：**%.1f%%**（阈值 %.1f%%）\n- 窗口：%d 分钟内 %d 次请求，%d 次 5xx\n- 时间：%s",
			rate, rules.ErrorRatePct, rules.WindowMinutes, total, errCount, time.Now().Format("2006-01-02 15:04:05")))
}

// checkLatency 检测窗口内各后端健康探测平均延迟（来自 backend_health_logs，仅统计健康探测）
func (e *Engine) checkLatency(rules Rules) {
	if rules.LatencyMs <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	from := time.Now().Add(-time.Duration(rules.WindowMinutes) * time.Minute)
	statsList, err := e.db.GetProbeLatencyStats(ctx, from)
	if err != nil {
		log.Printf("告警引擎：查询探测延迟统计失败: %v", err)
		return
	}
	for _, st := range statsList {
		if st.AvgMs < float64(rules.LatencyMs) {
			continue
		}
		e.fire(rules, "backend-latency:"+st.Backend,
			"🐌 后端延迟过高",
			fmt.Sprintf("**后端探测延迟超过阈值**\n\n- 地址：%s\n- 窗口平均延迟：**%.0fms**（阈值 %dms，%d 次探测）\n- 时间：%s\n\n节点存活但响应缓慢，请关注。",
				st.Backend, st.AvgMs, rules.LatencyMs, st.Probes, time.Now().Format("2006-01-02 15:04:05")))
	}
}

// fire 发送告警（受静默期约束）
func (e *Engine) fire(rules Rules, key, title, text string) {
	e.mu.Lock()
	if t, ok := e.lastSent[key]; ok && time.Since(t) < time.Duration(rules.SilenceMinutes)*time.Minute {
		e.mu.Unlock()
		return // 静默期内，跳过
	}
	e.mu.Unlock()
	e.deliver(key, title, text)
}

// fireSilenceBypass 发送恢复类通知（不受静默期约束：恢复本身就是"解除"信号）
func (e *Engine) fireSilenceBypass(key, title, text string) {
	e.deliver(key, title, text)
}

// deliver 实际发送 + 审计记录（发送失败只打运行日志，不写审计，避免故障期间刷库）
func (e *Engine) deliver(key, title, text string) {
	if err := e.ding.Send(title, text); err != nil {
		log.Printf("告警引擎：发送钉钉通知失败 [%s]: %v", key, err)
		return
	}
	log.Printf("告警引擎：已发送通知 [%s] %s", key, title)
	e.mu.Lock()
	e.lastSent[key] = time.Now()
	e.mu.Unlock()
	_ = e.db.InsertAudit(context.Background(), "system", "alert_sent: "+title, "")
}

// SendTest 发送测试消息（POST /api/alert/test，供页面验证 webhook 连通性）
func (e *Engine) SendTest(username string) error {
	text := fmt.Sprintf("**这是一条测试消息**\n\n- Proxy Sentinel 告警通道验证成功\n- 操作人：%s\n- 时间：%s\n\n收到此消息说明钉钉机器人配置正确。",
		username, time.Now().Format("2006-01-02 15:04:05"))
	if err := e.ding.Send("✅ 告警测试", text); err != nil {
		return err
	}
	return e.db.InsertAudit(context.Background(), username, "alert_test_sent", "")
}
