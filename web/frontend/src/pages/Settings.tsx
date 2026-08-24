import { useCallback, useEffect, useState } from 'react'
import { BellRing, Plus, RefreshCw, Save, Send, Trash2 } from 'lucide-react'
import { api, type AlertConfigInfo, type AlertRules, type SettingsInfo } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input, Select } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

/** 配置管理：后端节点增删改（运行时生效 + 数据库持久化）、策略切换、只读运行参数 */
export default function Settings() {
  const [info, setInfo] = useState<SettingsInfo | null>(null)
  const [urls, setUrls] = useState<string[]>([])
  const [strategy, setStrategy] = useState('round_robin')
  const [newUrl, setNewUrl] = useState('')
  const [editing, setEditing] = useState<Record<number, string>>({})
  const [msg, setMsg] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    api
      .get<SettingsInfo>('/api/settings')
      .then((d) => {
        setInfo(d)
        setUrls((d.backends ?? []).map((b) => b.url))
        setStrategy(d.strategy)
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    load()
    // 健康状态 30 秒刷新
    const t = setInterval(load, 30000)
    return () => clearInterval(t)
  }, [load])

  const healthOf = (url: string) => (info?.backends ?? []).find((b) => b.url === url)?.healthy

  const addUrl = () => {
    const u = newUrl.trim()
    if (!u) return
    if (!/^https?:\/\/.+/.test(u)) {
      setMsg('地址需以 http:// 或 https:// 开头')
      return
    }
    if (urls.includes(u)) {
      setMsg('该后端已存在')
      return
    }
    setUrls([...urls, u])
    setNewUrl('')
    setMsg('')
  }

  const save = async () => {
    setSaving(true)
    setMsg('')
    try {
      await api.put('/api/settings/backends', { backends: urls, strategy })
      load()
      setMsg('已保存：立即生效，重启后保留')
    } catch (e) {
      setMsg(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="max-w-4xl space-y-4">
      {/* 后端节点管理 */}
      <Card>
        <CardHeader>
          <CardTitle>后端节点管理</CardTitle>
          <CardDescription>
            增删改后立即生效并持久化到数据库（重启后优先于 config.yaml）
            {info?.managed && <Badge variant="secondary" className="ml-2">数据库管理中</Badge>}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* 节点列表 */}
          <div className="space-y-2">
            {urls.map((url, i) => {
              const editing_ = editing[i] ?? url
              const healthy = healthOf(url)
              return (
                <div key={i} className="flex items-center gap-2">
                  <span
                    className={`h-2.5 w-2.5 shrink-0 rounded-full ${
                      healthy === undefined ? 'bg-muted-foreground/40' : healthy ? 'bg-emerald-500' : 'bg-red-500'
                    }`}
                    title={healthy === undefined ? '健康状态未知' : healthy ? '健康' : '不可用'}
                  />
                  <Input
                    value={editing_}
                    onChange={(e) => setEditing((s) => ({ ...s, [i]: e.target.value }))}
                    className="flex-1 font-mono text-xs"
                  />
                  <Button
                    size="icon"
                    variant="ghost"
                    title="恢复原值"
                    onClick={() =>
                      setEditing((s) => {
                        const n = { ...s }
                        delete n[i]
                        return n
                      })
                    }
                  >
                    <RefreshCw className="h-4 w-4" />
                  </Button>
                  <Button size="icon" variant="ghost" title="删除" onClick={() => setUrls(urls.filter((_, j) => j !== i))}>
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </div>
              )
            })}
            {urls.length === 0 && <p className="text-sm text-muted-foreground">暂无后端（保存前至少保留一个）</p>}
          </div>

          {/* 新增 */}
          <div className="flex gap-2">
            <Input
              placeholder="https://backend.example.com"
              value={newUrl}
              onChange={(e) => setNewUrl(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && addUrl()}
              className="font-mono text-xs"
            />
            <Button variant="outline" onClick={addUrl}>
              <Plus className="h-4 w-4" /> 添加
            </Button>
          </div>

          {/* 策略 + 保存 */}
          <div className="flex items-center gap-3 border-t pt-4">
            <span className="text-sm text-muted-foreground">负载均衡策略</span>
            <Select value={strategy} onChange={(e) => setStrategy(e.target.value)} className="w-44">
              <option value="round_robin">round_robin（轮询）</option>
              <option value="random">random（随机）</option>
            </Select>
            <div className="flex-1" />
            <Button onClick={save} disabled={saving || urls.length === 0}>
              <Save className="h-4 w-4" /> {saving ? '保存中…' : '保存'}
            </Button>
          </div>
          {msg && <p className="text-sm text-muted-foreground">{msg}</p>}
        </CardContent>
      </Card>

      {/* 只读运行参数 */}
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>日志配置（只读）</CardTitle>
            <CardDescription>在 config.yaml 或环境变量中修改后重启生效</CardDescription>
          </CardHeader>
          <CardContent>
            <dl className="space-y-2 text-sm">
              <Row k="日志级别" v={info?.log.level ?? '—'} />
              <Row k="采样率" v={String(info?.log.sample_rate ?? '—')} />
              <Row k="保留天数" v={`${info?.log.retention_days ?? '—'} 天`} />
              <Row k="敏感字段脱敏" v={info?.log.mask_sensitive ? '开启' : '关闭'} />
              <Row k="日志体截断上限" v={`${fmtBytes(info?.log.body_max_bytes)}`} />
            </dl>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>代理配置（只读）</CardTitle>
            <CardDescription>在 config.yaml 或环境变量中修改后重启生效</CardDescription>
          </CardHeader>
          <CardContent>
            <dl className="space-y-2 text-sm">
              <Row k="超时时间" v={`${info?.proxy.timeout_seconds ?? '—'} 秒`} />
              <Row k="最大请求体" v={fmtBytes(info?.proxy.max_body_bytes)} />
              <Row k="信任转发头 (XFF)" v={info?.proxy.trust_forwarded_headers ? '是' : '否'} />
              <Row
                k="默认限流（每 Token）"
                v={info?.rate_limit.default_rpm ? `${info.rate_limit.default_rpm} 次/分钟` : '未启用'}
              />
            </dl>
          </CardContent>
        </Card>
      </div>

      {/* 告警通知 */}
      <AlertCard />
    </div>
  )
}

/** 空输入按 0 处理 */
const toNum = (v: string) => (v === '' ? 0 : Number(v))

/** 告警通知（钉钉）：规则阈值存数据库改完即生效；webhook 凭据在 config.yaml 配置 */
function AlertCard() {
  const [cfg, setCfg] = useState<AlertConfigInfo | null>(null)
  const [rules, setRules] = useState<AlertRules>({
    enabled: false,
    error_rate_pct: 10,
    window_minutes: 5,
    min_sample: 20,
    backend_down: true,
    silence_minutes: 10,
  })
  const [msg, setMsg] = useState('')
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)

  useEffect(() => {
    api
      .get<AlertConfigInfo>('/api/alert/config')
      .then((d) => {
        setCfg(d)
        setRules(d.rules)
      })
      .catch(() => {})
  }, [])

  const save = async () => {
    setSaving(true)
    setMsg('')
    try {
      await api.put('/api/alert/config', rules)
      setMsg('已保存：立即生效，重启后保留')
    } catch (e) {
      setMsg(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const test = async () => {
    setTesting(true)
    setMsg('')
    try {
      const r = await api.post<{ message: string }>('/api/alert/test')
      setMsg(r.message)
    } catch (e) {
      setMsg(e instanceof Error ? e.message : '发送失败')
    } finally {
      setTesting(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <BellRing className="h-4 w-4" /> 告警通知（钉钉）
        </CardTitle>
        <CardDescription>
          规则保存后立即生效（每 {cfg?.check_interval_seconds ?? 30} 秒评估一次）；webhook 地址在 config.yaml → alert.dingtalk 配置，修改后重启生效
          {cfg?.dingtalk_configured ? (
            <Badge variant="default" className="ml-2 bg-emerald-600">webhook 已配置</Badge>
          ) : (
            <Badge variant="destructive" className="ml-2">webhook 未配置</Badge>
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {!cfg?.dingtalk_configured && (
          <p className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-600 dark:text-amber-400">
            尚未配置钉钉机器人：钉钉群 → 群设置 → 智能群助手 → 添加机器人（自定义），把 webhook 地址填入 config.yaml 的
            alert.dingtalk.webhook_url（安全设置选"加签"时把 SEC 密钥填入 secret），重启后即可发送通知。
          </p>
        )}

        <CheckRow
          label="启用告警"
          hint="关闭后引擎停止评估（规则保留）"
          checked={rules.enabled}
          onChange={(v) => setRules({ ...rules, enabled: v })}
        />
        <CheckRow
          label="后端宕机 / 恢复通知"
          hint="节点被健康检查剔除或恢复时推送"
          checked={rules.backend_down}
          onChange={(v) => setRules({ ...rules, backend_down: v })}
        />

        <div className="grid gap-4 sm:grid-cols-4">
          <NumField
            label="错误率阈值 (%)"
            hint="窗口内 5xx 占比超过该值时告警，0 = 关闭"
            value={rules.error_rate_pct}
            onChange={(v) => setRules({ ...rules, error_rate_pct: v })}
          />
          <NumField
            label="统计窗口（分钟）"
            hint="错误率统计的时间窗口"
            value={rules.window_minutes}
            onChange={(v) => setRules({ ...rules, window_minutes: v })}
          />
          <NumField
            label="最小样本量"
            hint="窗口内请求数不足时不判定，防误报"
            value={rules.min_sample}
            onChange={(v) => setRules({ ...rules, min_sample: v })}
          />
          <NumField
            label="静默期（分钟）"
            hint="同一告警在该时间内不重复发送"
            value={rules.silence_minutes}
            onChange={(v) => setRules({ ...rules, silence_minutes: v })}
          />
        </div>

        <div className="flex items-center gap-3 border-t pt-4">
          <Button variant="outline" onClick={test} disabled={testing || !cfg?.dingtalk_configured}>
            <Send className="h-4 w-4" /> {testing ? '发送中…' : '发送测试消息'}
          </Button>
          <div className="flex-1" />
          <Button onClick={save} disabled={saving}>
            <Save className="h-4 w-4" /> {saving ? '保存中…' : '保存'}
          </Button>
        </div>
        {msg && <p className="text-sm text-muted-foreground">{msg}</p>}
      </CardContent>
    </Card>
  )
}

/** 开关行（原生 checkbox + 样式包装） */
function CheckRow({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string
  hint?: string
  checked: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <label className="flex cursor-pointer items-center gap-3">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="h-4 w-4 shrink-0 cursor-pointer accent-primary"
      />
      <span className="text-sm font-medium">{label}</span>
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  )
}

/** 数字输入字段（带标签与提示） */
function NumField({
  label,
  hint,
  value,
  onChange,
}: {
  label: string
  hint?: string
  value: number
  onChange: (v: number) => void
}) {
  const [text, setText] = useState(String(value))
  useEffect(() => setText(String(value)), [value])
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium">{label}</label>
      <Input
        type="number"
        min={0}
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          onChange(toNum(e.target.value))
        }}
      />
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-center justify-between border-b pb-2 last:border-0 last:pb-0">
      <dt className="text-muted-foreground">{k}</dt>
      <dd className="font-medium">{v}</dd>
    </div>
  )
}

function fmtBytes(n: number | undefined): string {
  if (n === undefined) return '—'
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(0)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${n} B`
}
