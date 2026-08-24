import { useCallback, useEffect, useState } from 'react'
import { BellRing, GitBranch, Plus, RefreshCw, Replace, Save, Send, ShieldAlert, Trash2 } from 'lucide-react'
import { api, type AlertConfigInfo, type AlertRules, type IPACLConfig, type IPACLDefault, type IPACLEntry, type IPACLMode, type RewriteRule, type RouteRule, type RouteRuleType, type SettingsInfo } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input, Select } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

/** 配置管理：后端节点/权重/策略/定向规则（运行时生效 + 数据库持久化）、只读运行参数 */
export default function Settings() {
  const [info, setInfo] = useState<SettingsInfo | null>(null)
  const [rows, setRows] = useState<{ url: string; weight: number }[]>([])
  const [strategy, setStrategy] = useState('round_robin')
  const [rules, setRules] = useState<RouteRule[]>([])
  const [rewrites, setRewrites] = useState<RewriteRule[]>([])
  const [newUrl, setNewUrl] = useState('')
  const [editing, setEditing] = useState<Record<number, string>>({})
  const [msg, setMsg] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    api
      .get<SettingsInfo>('/api/settings')
      .then((d) => {
        setInfo(d)
        setRows((d.backends ?? []).map((b) => ({ url: b.url, weight: b.weight ?? 1 })))
        setStrategy(d.strategy)
        setRules(d.rules ?? [])
        setRewrites(d.rewrites ?? [])
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
    if (rows.some((r) => r.url === u)) {
      setMsg('该后端已存在')
      return
    }
    setRows([...rows, { url: u, weight: 1 }])
    setNewUrl('')
    setMsg('')
  }

  const totalWeight = rows.reduce((s, r) => s + (r.weight || 0), 0)

  const save = async () => {
    setSaving(true)
    setMsg('')
    try {
      await api.put('/api/settings/backends', { backends: rows, strategy, rules, rewrites })
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
          {/* 节点列表（含权重） */}
          <div className="space-y-2">
            {rows.map((row, i) => {
              const editing_ = editing[i] ?? row.url
              const healthy = healthOf(row.url)
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
                  <div className="flex shrink-0 items-center gap-1" title="权重 0~100，仅 weighted 策略生效；0 = 不接流量">
                    <span className="text-xs text-muted-foreground">权重</span>
                    <Input
                      type="number"
                      min={0}
                      max={100}
                      value={row.weight}
                      onChange={(e) =>
                        setRows((s) =>
                          s.map((x, j) => (j === i ? { ...x, weight: Math.min(100, Math.max(0, Number(e.target.value) || 0)) } : x)),
                        )
                      }
                      className="w-20 text-xs"
                    />
                  </div>
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
                  <Button size="icon" variant="ghost" title="删除" onClick={() => setRows(rows.filter((_, j) => j !== i))}>
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </div>
              )
            })}
            {rows.length === 0 && <p className="text-sm text-muted-foreground">暂无后端（保存前至少保留一个）</p>}
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

          {/* 定向分流规则 */}
          <div className="space-y-2 rounded-lg border p-3">
            <div className="flex items-center gap-2">
              <GitBranch className="h-4 w-4 text-indigo-500" />
              <span className="text-sm font-semibold">定向分流规则（灰度发布）</span>
              <span className="text-xs text-muted-foreground">
                命中规则的请求固定路由到指定后端（优先于负载均衡），自上而下第一条命中生效
              </span>
            </div>
            {rules.map((rule, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2">
                <Select
                  value={rule.type}
                  onChange={(e) =>
                    setRules((s) => s.map((x, j) => (j === i ? { ...x, type: e.target.value as RouteRuleType } : x)))
                  }
                  className="w-28 text-xs"
                >
                  <option value="header">Header</option>
                  <option value="cookie">Cookie</option>
                  <option value="path">路径前缀</option>
                </Select>
                {rule.type !== 'path' ? (
                  <Input
                    placeholder={rule.type === 'header' ? 'Header 名（如 X-Gray）' : 'Cookie 名（如 beta）'}
                    value={rule.key}
                    onChange={(e) => setRules((s) => s.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))}
                    className="w-44 font-mono text-xs"
                  />
                ) : null}
                <Input
                  placeholder={rule.type === 'path' ? '路径前缀（如 /api/v2/）' : '匹配值（精确匹配）'}
                  value={rule.value}
                  onChange={(e) => setRules((s) => s.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))}
                  className="w-52 font-mono text-xs"
                />
                <span className="text-xs text-muted-foreground">→</span>
                <Select
                  value={rule.backend}
                  onChange={(e) => setRules((s) => s.map((x, j) => (j === i ? { ...x, backend: e.target.value } : x)))}
                  className="min-w-40 flex-1 font-mono text-xs"
                >
                  <option value="">选择目标后端</option>
                  {rows.map((r) => (
                    <option key={r.url} value={r.url}>
                      {r.url}
                    </option>
                  ))}
                </Select>
                <Button size="icon" variant="ghost" title="删除" onClick={() => setRules(rules.filter((_, j) => j !== i))}>
                  <Trash2 className="h-4 w-4 text-destructive" />
                </Button>
              </div>
            ))}
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => setRules([...rules, { type: 'header', key: '', value: '', backend: '' }])}>
                <Plus className="h-3.5 w-3.5" /> 添加规则
              </Button>
              {rules.length === 0 && <span className="text-xs text-muted-foreground">无规则：所有流量走负载均衡策略</span>}
            </div>
          </div>

          {/* 路径重写规则 */}
          <div className="space-y-2 rounded-lg border p-3">
            <div className="flex items-center gap-2">
              <Replace className="h-4 w-4 text-amber-500" />
              <span className="text-sm font-semibold">路径重写规则</span>
              <span className="text-xs text-muted-foreground">
                转发前替换路径前缀（如 /api/v1 → /v1）；自上而下第一条命中生效，日志记录原始路径
              </span>
            </div>
            {rewrites.map((rw, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2">
                <Input
                  placeholder="匹配前缀（如 /api/v1）"
                  value={rw.prefix}
                  onChange={(e) => setRewrites((s) => s.map((x, j) => (j === i ? { ...x, prefix: e.target.value } : x)))}
                  className="w-44 font-mono text-xs"
                />
                <span className="text-xs text-muted-foreground">→</span>
                <Input
                  placeholder="替换为（留空 = 剥离前缀）"
                  value={rw.replacement}
                  onChange={(e) => setRewrites((s) => s.map((x, j) => (j === i ? { ...x, replacement: e.target.value } : x)))}
                  className="w-44 font-mono text-xs"
                />
                <Select
                  value={rw.backend}
                  onChange={(e) => setRewrites((s) => s.map((x, j) => (j === i ? { ...x, backend: e.target.value } : x)))}
                  className="min-w-40 flex-1 font-mono text-xs"
                  title="限定仅对该后端生效（可选）"
                >
                  <option value="">全部后端</option>
                  {rows.map((r) => (
                    <option key={r.url} value={r.url}>
                      仅 {r.url}
                    </option>
                  ))}
                </Select>
                <Button size="icon" variant="ghost" title="删除" onClick={() => setRewrites(rewrites.filter((_, j) => j !== i))}>
                  <Trash2 className="h-4 w-4 text-destructive" />
                </Button>
              </div>
            ))}
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => setRewrites([...rewrites, { prefix: '', replacement: '', backend: '' }])}>
                <Plus className="h-3.5 w-3.5" /> 添加重写
              </Button>
              {rewrites.length === 0 && <span className="text-xs text-muted-foreground">无规则：路径原样转发</span>}
            </div>
          </div>

          {/* 策略 + 保存 */}
          <div className="flex flex-wrap items-center gap-3 border-t pt-4">
            <span className="text-sm text-muted-foreground">负载均衡策略</span>
            <Select value={strategy} onChange={(e) => setStrategy(e.target.value)} className="w-52">
              <option value="round_robin">round_robin（轮询）</option>
              <option value="random">random（随机）</option>
              <option value="weighted">weighted（加权随机·灰度）</option>
            </Select>
            {strategy === 'weighted' && totalWeight > 0 && (
              <span className="text-xs text-muted-foreground">
                流量比例：{rows.filter((r) => r.weight > 0).map((r) => `${((r.weight / totalWeight) * 100).toFixed(1)}%`).join(' / ')}
              </span>
            )}
            {strategy === 'weighted' && totalWeight === 0 && (
              <span className="text-xs text-amber-600 dark:text-amber-400">weighted 策略要求至少一个后端权重 &gt; 0</span>
            )}
            <div className="flex-1" />
            <Button onClick={save} disabled={saving || rows.length === 0}>
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

      {/* IP 访问控制 */}
      <IPACLCard />
    </div>
  )
}

/** IP 黑白名单（双名单）：作用于 /proxy 入口（认证之前拦截），保存后立即生效。
 *  评估顺序：黑名单命中拒绝（绝对优先）→ 白名单命中放行 → 都未命中走默认动作 */
function IPACLCard() {
  const [mode, setMode] = useState<IPACLMode>('off')
  const [def, setDef] = useState<IPACLDefault>('allow')
  const [blacklist, setBlacklist] = useState<IPACLEntry[]>([])
  const [whitelist, setWhitelist] = useState<IPACLEntry[]>([])
  const [msg, setMsg] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api
      .get<IPACLConfig>('/api/ip-acl')
      .then((d) => {
        setMode(d.mode)
        setDef(d.default ?? 'allow')
        setBlacklist(d.blacklist ?? [])
        setWhitelist(d.whitelist ?? [])
      })
      .catch(() => {})
  }, [])

  const save = async () => {
    setSaving(true)
    setMsg('')
    try {
      await api.put('/api/ip-acl', { mode, default: def, blacklist, whitelist })
      setMsg('已保存：立即生效，重启后保留')
    } catch (e) {
      setMsg(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const denyAllRisk = mode === 'on' && def === 'deny' && whitelist.length === 0

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldAlert className="h-4 w-4" /> IP 访问控制（/proxy 入口）
        </CardTitle>
        <CardDescription>
          按客户端 TCP 直连 IP 拦截（不读 X-Forwarded-For，无法伪造绕过）；保存后立即生效，重启后保留
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* 开关 + 默认动作 */}
        <div className="flex flex-wrap items-center gap-3">
          <Select
            value={mode}
            onChange={(e) => {
              setMode(e.target.value as IPACLMode)
              setMsg('')
            }}
            className="w-40"
          >
            <option value="off">关闭（不拦截）</option>
            <option value="on">启用</option>
          </Select>
          <Select
            value={def}
            onChange={(e) => {
              setDef(e.target.value as IPACLDefault)
              setMsg('')
            }}
            className="w-64"
            disabled={mode === 'off'}
          >
            <option value="allow">默认放行（黑名单为主）</option>
            <option value="deny">默认拒绝（白名单为准入）</option>
          </Select>
          <span className="text-xs text-muted-foreground">
            判定顺序：黑名单命中 → 拒绝；白名单命中 → 放行；都未命中 → 默认动作
          </span>
        </div>

        {/* 双名单条目 */}
        <div className="grid gap-4 lg:grid-cols-2">
          <ACLGroup
            title="黑名单"
            hint="命中即拒绝（优先级最高，白名单无法覆盖）"
            entries={blacklist}
            disabled={mode === 'off'}
            onChange={setBlacklist}
            onError={setMsg}
          />
          <ACLGroup
            title="白名单"
            hint="命中放行；默认动作为「拒绝」时不可为空"
            entries={whitelist}
            disabled={mode === 'off'}
            onChange={setWhitelist}
            onError={setMsg}
          />
        </div>

        <div className="flex items-center gap-3 border-t pt-4">
          <p className="text-xs text-muted-foreground">保存时后端会校验全部条目，存在非法条目将整单拒绝</p>
          <div className="flex-1" />
          <Button onClick={save} disabled={saving || denyAllRisk}>
            <Save className="h-4 w-4" /> {saving ? '保存中…' : '保存'}
          </Button>
        </div>
        {denyAllRisk && (
          <p className="text-sm text-amber-600 dark:text-amber-400">
            默认动作为「拒绝」且白名单为空 = 拒绝所有请求，请先添加白名单条目
          </p>
        )}
        {msg && <p className="text-sm text-muted-foreground">{msg}</p>}
      </CardContent>
    </Card>
  )
}

/** 一份名单分组：条目列表 + 添加行 */
function ACLGroup({
  title,
  hint,
  entries,
  disabled,
  onChange,
  onError,
}: {
  title: string
  hint: string
  entries: IPACLEntry[]
  disabled: boolean
  onChange: (v: IPACLEntry[]) => void
  onError: (m: string) => void
}) {
  const [newValue, setNewValue] = useState('')
  const [newNote, setNewNote] = useState('')

  // 前端轻校验格式（最终以后端校验为准）
  const validEntry = (v: string) =>
    /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/.test(v) || /^([0-9a-fA-F:]+:+)+[0-9a-fA-F:]+(\/\d{1,3})?$/.test(v)

  const add = () => {
    const v = newValue.trim()
    if (!v) return
    if (!validEntry(v)) {
      onError('格式需为 IP（1.2.3.4 / ::1）或 CIDR 网段（10.0.0.0/8 / fe80::/10）')
      return
    }
    if (entries.some((e) => e.value === v)) {
      onError(`${title}中已存在 ${v}`)
      return
    }
    onChange([...entries, { value: v, note: newNote.trim() }])
    setNewValue('')
    setNewNote('')
    onError('')
  }

  return (
    <div className={`space-y-2 rounded-lg border p-3 ${disabled ? 'opacity-50' : ''}`}>
      <div>
        <span className="text-sm font-semibold">{title}</span>
        <span className="ml-2 text-xs text-muted-foreground">{hint}</span>
      </div>
      {entries.map((e, i) => (
        <div key={i} className="flex items-center gap-2">
          <span className="w-44 shrink-0 truncate rounded-md border bg-muted/30 px-2 py-1.5 font-mono text-xs" title={e.value}>
            {e.value}
          </span>
          <Input
            value={e.note}
            placeholder="备注（可选）"
            disabled={disabled}
            onChange={(ev) => onChange(entries.map((x, j) => (j === i ? { ...x, note: ev.target.value } : x)))}
            className="flex-1 text-xs"
          />
          <Button
            size="icon"
            variant="ghost"
            title="删除"
            disabled={disabled}
            onClick={() => onChange(entries.filter((_, j) => j !== i))}
          >
            <Trash2 className="h-4 w-4 text-destructive" />
          </Button>
        </div>
      ))}
      {entries.length === 0 && <p className="text-xs text-muted-foreground">暂无条目</p>}
      <div className="flex gap-2 pt-1">
        <Input
          placeholder="IP / CIDR"
          value={newValue}
          disabled={disabled}
          onChange={(e) => setNewValue(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && add()}
          className="w-44 font-mono text-xs"
        />
        <Input placeholder="备注（可选）" value={newNote} disabled={disabled} onChange={(e) => setNewNote(e.target.value)} className="flex-1 text-xs" />
        <Button variant="outline" size="sm" onClick={add} disabled={disabled}>
          <Plus className="h-3.5 w-3.5" /> 添加
        </Button>
      </div>
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
