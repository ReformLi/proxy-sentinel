import { useCallback, useEffect, useRef, useState } from 'react'
import { BellRing, Database, GitBranch, Plus, RefreshCw, Replace, Save, Send, ShieldAlert, Trash2, Undo2 } from 'lucide-react'
import { api, type AlertConfigInfo, type AlertRules, type IPACLConfig, type IPACLDefault, type IPACLEntry, type IPACLMode, type RewriteRule, type RouteRule, type RouteRuleType, type SettingsInfo } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input, Select } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

/** 配置管理：后端节点/权重/策略/定向规则（运行时生效 + 数据库持久化）、只读运行参数 */
export default function Settings() {
  const [info, setInfo] = useState<SettingsInfo | null>(null)
  const [rows, setRows] = useState<{ url: string; weight: number; health_path: string }[]>([])
  const [strategy, setStrategy] = useState('round_robin')
  const [rules, setRules] = useState<RouteRule[]>([])
  const [rewrites, setRewrites] = useState<RewriteRule[]>([])
  const [newUrl, setNewUrl] = useState('')
  const [editing, setEditing] = useState<Record<number, string>>({})
  const [msg, setMsg] = useState('')
  const [msgType, setMsgType] = useState<'error' | 'success'>('error')
  const [saving, setSaving] = useState(false)
  // 已保存基线（结构化，支持"回退"整体恢复）
  const [baseline, setBaseline] = useState<{
    rows: { url: string; weight: number; health_path: string }[]
    strategy: string
    rules: RouteRule[]
    rewrites: RewriteRule[]
  } | null>(null)
  const baselineRef = useRef(baseline)
  baselineRef.current = baseline
  const dirtyRef = useRef(false)

  const snapOf = (
    rs: { url: string; weight: number; health_path: string }[],
    st: string,
    ru: RouteRule[],
    rw: RewriteRule[],
  ) => JSON.stringify({ rows: rs, strategy: st, rules: ru, rewrites: rw })

  // 将服务器配置应用到本地（仅在无未保存改动时调用，避免轮询覆盖正在编辑的内容）
  const applyServer = (d: SettingsInfo) => {
    const rs = (d.backends ?? []).map((b) => ({ url: b.url, weight: b.weight ?? 1, health_path: b.health_path ?? '' }))
    setRows(rs)
    setStrategy(d.strategy)
    setRules(d.rules ?? [])
    setRewrites(d.rewrites ?? [])
    setEditing({})
    setBaseline({ rows: rs, strategy: d.strategy, rules: d.rules ?? [], rewrites: d.rewrites ?? [] })
  }

  // 回退到最近一次已保存的状态
  const revert = () => {
    const b = baselineRef.current
    if (!b) return
    setRows(b.rows)
    setStrategy(b.strategy)
    setRules(b.rules)
    setRewrites(b.rewrites)
    setEditing({})
    setMsg('')
    setMsgType('error')
  }

  const load = useCallback((apply = false) => {
    api
      .get<SettingsInfo>('/api/settings')
      .then((d) => {
        setInfo(d)
        if (apply || !dirtyRef.current) applyServer(d)
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    load(true)
    // 健康状态 30 秒刷新；有未保存改动时只刷新健康徽标，不覆盖编辑内容
    const t = setInterval(() => load(false), 30000)
    return () => clearInterval(t)
  }, [load])

  // URL 编辑框的值并入行数据（保存时生效；顺带修复此前“编辑 URL 不落库”的缺陷）
  const committedRows = rows.map((r, i) => {
    const e = editing[i]
    return e !== undefined && e.trim() !== '' ? { ...r, url: e.trim() } : r
  })
  const dirty = baseline !== null && snapOf(committedRows, strategy, rules, rewrites) !== snapOf(baseline.rows, baseline.strategy, baseline.rules, baseline.rewrites)
  useEffect(() => {
    dirtyRef.current = dirty
  })

  const healthOf = (url: string) => (info?.backends ?? []).find((b) => b.url === url)?.healthy

  const addUrl = () => {
    const u = newUrl.trim()
    if (!u) return
    if (!/^https?:\/\/.+/.test(u)) {
      setMsgType('error')
      setMsg('地址需以 http:// 或 https:// 开头')
      return
    }
    if (rows.some((r) => r.url === u)) {
      setMsgType('error')
      setMsg('该后端已存在')
      return
    }
    setRows([...rows, { url: u, weight: 1, health_path: '' }])
    setNewUrl('')
    setMsg('')
  }

  const totalWeight = rows.reduce((s, r) => s + (r.weight || 0), 0)

  const save = async () => {
    setSaving(true)
    setMsg('')
    try {
      await api.put('/api/settings/backends', { backends: committedRows, strategy, rules, rewrites })
      setRows(committedRows)
      setEditing({})
      setBaseline({ rows: committedRows, strategy, rules, rewrites })
      setMsgType('success')
      setMsg('已保存：立即生效，重启后保留')
      load(true)
    } catch (e) {
      setMsgType('error')
      setMsg(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-4">
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
                      healthy === undefined ? 'bg-amber-500' : healthy ? 'bg-emerald-500' : 'bg-red-500'
                    }`}
                    title={healthy === undefined ? '待保存：尚未注册到网关，保存后开始健康检查' : healthy ? '健康' : '不可用'}
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
                  <div className="flex shrink-0 items-center gap-1" title="健康检查探测路径（空 = /），需以 / 开头">
                    <span className="text-xs text-muted-foreground">探测</span>
                    <Input
                      value={row.health_path}
                      placeholder="/"
                      onChange={(e) =>
                        setRows((s) => s.map((x, j) => (j === i ? { ...x, health_path: e.target.value } : x)))
                      }
                      className="w-24 font-mono text-xs"
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
              onChange={(e) => {
                setNewUrl(e.target.value)
                if (msg) setMsg('')
              }}
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
            {dirty && (
              <Button variant="outline" onClick={revert} disabled={saving}>
                <Undo2 className="h-4 w-4" /> 回退
              </Button>
            )}
            <Button onClick={save} disabled={saving || rows.length === 0 || !dirty}>
              <Save className="h-4 w-4" /> {saving ? '保存中…' : '保存'}
            </Button>
          </div>
          {msg && (
            <p className={`text-sm ${msgType === 'error' ? 'text-destructive' : 'text-emerald-600 dark:text-emerald-400'}`}>
              {msg}
            </p>
          )}
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
              <Row k="代理日志保留" v={fmtDays(info?.log.retention_days)} />
              <Row k="健康日志保留" v={fmtDays(info?.log.health_retention_days)} />
              <Row k="审计日志保留" v={fmtDays(info?.log.audit_retention_days)} />
              <Row k="敏感字段脱敏" v={info?.log.mask_sensitive ? '开启' : '关闭'} />
              <Row k="日志体截断上限" v={`${fmtBytes(info?.log.body_max_bytes)}`} />
              <Row k="异步队列上限" v={fmtNum(info?.log.queue_capacity)} />
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
              <Row k="最大请求体（小请求读内存）" v={fmtBytes(info?.proxy.max_body_bytes)} />
              <Row k="流式上传上限（大请求透传）" v={fmtBytes(info?.proxy.max_upload_bytes)} />
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

      {/* 数据维护 */}
      <MaintenanceCard />
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
  const [msgType, setMsgType] = useState<'error' | 'success'>('error')
  const [saving, setSaving] = useState(false)
  // 已保存基线（结构化，支持"回退"整体恢复）
  const [snapshot, setSnapshot] = useState<{ mode: IPACLMode; def: IPACLDefault; bl: IPACLEntry[]; wl: IPACLEntry[] } | null>(null)

  useEffect(() => {
    api
      .get<IPACLConfig>('/api/ip-acl')
      .then((d) => {
        setMode(d.mode)
        setDef(d.default ?? 'allow')
        setBlacklist((d.blacklist ?? []).map((e) => ({ value: e.value, note: e.note })))
        setWhitelist((d.whitelist ?? []).map((e) => ({ value: e.value, note: e.note })))
        setSnapshot({
          mode: d.mode,
          def: d.default ?? 'allow',
          bl: (d.blacklist ?? []).map((e) => ({ value: e.value, note: e.note })),
          wl: (d.whitelist ?? []).map((e) => ({ value: e.value, note: e.note })),
        })
      })
      .catch(() => {})
  }, [])

  const aclSnap = (m: IPACLMode, d: IPACLDefault, bl: IPACLEntry[], wl: IPACLEntry[]) =>
    JSON.stringify({ m, d, bl: bl.map((e) => ({ value: e.value, note: e.note })), wl: wl.map((e) => ({ value: e.value, note: e.note })) })

  const dirty = snapshot !== null && aclSnap(mode, def, blacklist, whitelist) !== aclSnap(snapshot.mode, snapshot.def, snapshot.bl, snapshot.wl)

  const revert = () => {
    if (!snapshot) return
    setMode(snapshot.mode)
    setDef(snapshot.def)
    setBlacklist(snapshot.bl)
    setWhitelist(snapshot.wl)
    setMsg('')
    setMsgType('error')
  }

  const save = async () => {
    setSaving(true)
    setMsg('')
    try {
      await api.put('/api/ip-acl', { mode, default: def, blacklist, whitelist })
      setSnapshot({ mode, def, bl: blacklist, wl: whitelist })
      setMsgType('success')
      setMsg('已保存：立即生效，重启后保留')
    } catch (e) {
      setMsgType('error')
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
          {dirty && (
            <Button variant="outline" onClick={revert} disabled={saving}>
              <Undo2 className="h-4 w-4" /> 回退
            </Button>
          )}
          <Button onClick={save} disabled={saving || denyAllRisk || !dirty}>
            <Save className="h-4 w-4" /> {saving ? '保存中…' : '保存'}
          </Button>
        </div>
        {denyAllRisk && (
          <p className="text-sm text-amber-600 dark:text-amber-400">
            默认动作为「拒绝」且白名单为空 = 拒绝所有请求，请先添加白名单条目
          </p>
        )}
        {msg && (
          <p className={`text-sm ${msgType === 'error' ? 'text-destructive' : 'text-emerald-600 dark:text-emerald-400'}`}>
            {msg}
          </p>
        )}
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
          onChange={(e) => {
            setNewValue(e.target.value)
            onError('')
          }}
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
    latency_ms: 0,
    silence_minutes: 10,
  })
  const [msg, setMsg] = useState('')
  const [msgType, setMsgType] = useState<'error' | 'success'>('error')
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  // 已保存基线（结构化，支持"回退"整体恢复）
  const [snapshot, setSnapshot] = useState<AlertRules | null>(null)

  const rulesSnap = (r: AlertRules) =>
    JSON.stringify({
      enabled: r.enabled,
      error_rate_pct: r.error_rate_pct,
      window_minutes: r.window_minutes,
      min_sample: r.min_sample,
      backend_down: r.backend_down,
      latency_ms: r.latency_ms,
      silence_minutes: r.silence_minutes,
    })

  useEffect(() => {
    api
      .get<AlertConfigInfo>('/api/alert/config')
      .then((d) => {
        setCfg(d)
        setRules(d.rules)
        setSnapshot(d.rules)
      })
      .catch(() => {})
  }, [])

  const dirty = snapshot !== null && rulesSnap(rules) !== rulesSnap(snapshot)

  const revert = () => {
    if (!snapshot) return
    setRules(snapshot)
    setMsg('')
    setMsgType('error')
  }

  const save = async () => {
    setSaving(true)
    setMsg('')
    try {
      await api.put('/api/alert/config', rules)
      setSnapshot(rules)
      setMsgType('success')
      setMsg('已保存：立即生效，重启后保留')
    } catch (e) {
      setMsgType('error')
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
      setMsgType('success')
      setMsg(r.message)
    } catch (e) {
      setMsgType('error')
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

        <div className="grid gap-4 sm:grid-cols-5">
          <NumField
            label="错误率阈值 (%)"
            hint="窗口内 5xx 占比超过该值时告警，0 = 关闭"
            value={rules.error_rate_pct}
            onChange={(v) => setRules({ ...rules, error_rate_pct: v })}
          />
          <NumField
            label="统计窗口（分钟）"
            hint="错误率/延迟统计的时间窗口"
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
            label="延迟阈值 (ms)"
            hint="后端探测平均延迟超过该值时告警，0 = 关闭"
            value={rules.latency_ms}
            onChange={(v) => setRules({ ...rules, latency_ms: v })}
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
          {dirty && (
            <Button variant="outline" onClick={revert} disabled={saving}>
              <Undo2 className="h-4 w-4" /> 回退
            </Button>
          )}
          <Button onClick={save} disabled={saving || !dirty}>
            <Save className="h-4 w-4" /> {saving ? '保存中…' : '保存'}
          </Button>
        </div>
        {msg && (
          <p className={`text-sm ${msgType === 'error' ? 'text-destructive' : 'text-emerald-600 dark:text-emerald-400'}`}>
            {msg}
          </p>
        )}
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
  if (n >= 1024 * 1024 * 1024) return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(2)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${n} B`
}

function fmtDays(n: number | undefined): string {
  if (n === undefined) return '—'
  if (n === 0) return '关闭（不自动清理）'
  return `${n} 天`
}

function fmtNum(n: number | undefined): string {
  if (n === undefined) return '—'
  if (n === 0) return '不限制'
  return n.toLocaleString()
}

/** 数据维护：表状态 + 手动清理 */
function MaintenanceCard() {
  const [stats, setStats] = useState<import('@/lib/api').MaintenanceStats | null>(null)
  const [loading, setLoading] = useState(false)
  const [purging, setPurging] = useState(false)
  const [purgeConfirm, setPurgeConfirm] = useState(false)
  const [sel, setSel] = useState<Record<string, boolean>>({ log: true, health: false, audit: false })
  const [keepDays, setKeepDays] = useState<number>(7)
  const [confirm, setConfirm] = useState(false)
  const [msg, setMsg] = useState('')
  const [msgType, setMsgType] = useState<'error' | 'success'>('error')

  const load = useCallback(() => {
    setLoading(true)
    api
      .get<import('@/lib/api').MaintenanceStats>('/api/maintenance/stats')
      .then((d) => {
        setStats(d)
        setMsg('')
      })
      .catch((e) => {
        setMsgType('error')
        setMsg(e instanceof Error ? e.message : '加载失败')
      })
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const purged = (d: Record<string, number>): string => {
    const names: Record<string, string> = { log: '代理日志', health: '健康检查', audit: '审计日志' }
    return Object.entries(d)
      .map(([k, v]) => `${names[k] ?? k} ${v.toLocaleString()} 条`)
      .join('、')
  }

  const purge = async () => {
    const tables = (Object.keys(sel) as Array<keyof typeof sel>).filter((k) => sel[k])
    if (tables.length === 0) {
      setMsgType('error')
      setMsg('至少选择一个要清理的表')
      return
    }
    if (keepDays <= 0 || !Number.isFinite(keepDays)) {
      setMsgType('error')
      setMsg('保留天数必须大于 0')
      return
    }
    if (!confirm) {
      setMsgType('error')
      setMsg('请先勾选确认删除（数据不可恢复）')
      return
    }
    // 触发自定义确认弹框
    setPurgeConfirm(true)
  }

  const purgeExecute = async () => {
    setPurgeConfirm(false)
    const tables = (Object.keys(sel) as Array<keyof typeof sel>).filter((k) => sel[k])
    setPurging(true)
    setMsg('')
    try {
      const r = await api.post<import('@/lib/api').PurgeResult>('/api/maintenance/purge', {
        tables,
        keep_days: keepDays,
        confirm: true,
      })
      setMsgType('success')
      const total = Object.values(r.deleted).reduce((a, b) => a + b, 0)
      setMsg(total > 0 ? `清理完成：共删除 ${purged(r.deleted)}` : '没有符合条件的记录可清理')
      setConfirm(false)
      load()
    } catch (e) {
      setMsgType('error')
      setMsg(e instanceof Error ? e.message : '清理失败')
    } finally {
      setPurging(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Database className="h-4 w-4" /> 数据维护
        </CardTitle>
        <CardDescription>
          自动保留期在 config.yaml 修改后重启生效；此处可手动按天数清理数据
          {stats && (
            <span className="ml-2 text-muted-foreground">
              （数据库文件合计 {fmtBytes(stats.db_size_bytes)}）
            </span>
          )}
          {loading && <span className="ml-2 text-muted-foreground">加载中…</span>}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* 表状态表格 */}
        <div className="overflow-hidden rounded-md border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="w-10 px-3 py-2 text-left">
                  <span className="sr-only">选择</span>
                </th>
                <th className="px-3 py-2 text-left font-medium">数据类型</th>
                <th className="px-3 py-2 text-right font-medium">当前条数</th>
                <th className="px-3 py-2 text-right font-medium">估算大小</th>
                <th className="px-3 py-2 text-right font-medium">自动保留期</th>
              </tr>
            </thead>
            <tbody>
              {(stats?.tables ?? []).map((t) => (
                <tr key={t.table} className="border-t last:border-0">
                  <td className="px-3 py-2">
                    <input
                      type="checkbox"
                      checked={sel[t.table] ?? false}
                      onChange={(e) => setSel((s) => ({ ...s, [t.table]: e.target.checked }))}
                      className="h-4 w-4 cursor-pointer accent-primary"
                    />
                  </td>
                  <td className="px-3 py-2 font-medium">{t.label}</td>
                  <td className="px-3 py-2 text-right tabular-nums">{t.count.toLocaleString()}</td>
                  <td className="px-3 py-2 text-right tabular-nums">{fmtBytes(t.size_bytes)}</td>
                  <td className="px-3 py-2 text-right tabular-nums">
                    {t.retention_days === 0 ? (
                      <Badge variant="outline">关闭</Badge>
                    ) : (
                      <span>{t.retention_days} 天</span>
                    )}
                  </td>
                </tr>
              ))}
              {!stats?.tables.length && !loading && (
                <tr>
                  <td colSpan={5} className="px-3 py-6 text-center text-muted-foreground">
                    暂无数据
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {/* 手动清理操作 */}
        <div className="flex flex-wrap items-end gap-4 rounded-md border p-4">
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-muted-foreground">保留最近天数</label>
            <Select
              value={String(keepDays)}
              onChange={(e) => setKeepDays(Number(e.target.value))}
              className="w-40"
            >
              {[1, 3, 7, 14, 30, 90, 180].map((d) => (
                <option key={d} value={d}>
                  {d} 天
                </option>
              ))}
            </Select>
          </div>
          <div className="flex items-end">
            <label className="flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2.5">
              <input
                type="checkbox"
                checked={confirm}
                onChange={(e) => setConfirm(e.target.checked)}
                className="h-4 w-4 cursor-pointer accent-destructive"
              />
              <span className="text-sm text-destructive">
                确认删除（保留 {keepDays} 天内的数据，更早的不可恢复）
              </span>
            </label>
          </div>
          <div className="flex items-end gap-2">
            <Button variant="outline" onClick={load} disabled={loading}>
              <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
              刷新
            </Button>
            <Button
              variant="destructive"
              onClick={purge}
              disabled={purging || loading}
              className="shadow-sm"
            >
              <Trash2 className="h-4 w-4" />
              {purging ? '清理中…' : '执行清理'}
            </Button>
          </div>
        </div>

        {msg && (
          <p className={`text-sm ${msgType === 'error' ? 'text-destructive' : 'text-emerald-600 dark:text-emerald-400'}`}>
            {msg}
          </p>
        )}
      </CardContent>

      <ConfirmDialog
        open={purgeConfirm}
        title="确认清理数据"
        message={`保留最近 ${keepDays} 天，删除：${(Object.keys(sel) as Array<keyof typeof sel>).filter((k) => sel[k]).map((k) => ({ log: '代理日志', health: '健康检查', audit: '审计日志' }[k] ?? k)).join('、')}。数据不可恢复！`}
        confirmText="执行清理"
        onConfirm={purgeExecute}
        onClose={() => setPurgeConfirm(false)}
      />
    </Card>
  )
}
