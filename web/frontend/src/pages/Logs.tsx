import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Copy, Download, Fingerprint, Radio, Search } from 'lucide-react'
import { api, type LogRecord, type PagedLogs } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input, Select } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog } from '@/components/ui/dialog'
import { fmtTime } from '@/lib/utils'

const PAGE_SIZE = 50

function statusVariant(status: number) {
  if (status >= 500) return 'destructive' as const
  if (status >= 400) return 'warning' as const
  return 'success' as const
}

export default function Logs() {
  const [params] = useSearchParams()
  // 筛选条件（backend_url 从流向图跳转带入，request_id 从详情弹窗"查同请求"带入）
  const [method, setMethod] = useState('')
  const [path, setPath] = useState('')
  const [keyword, setKeyword] = useState('')
  const [statusMin, setStatusMin] = useState('')
  const [minDuration, setMinDuration] = useState('')
  const [backend, setBackend] = useState(params.get('backend_url') ?? '')
  const [requestId, setRequestId] = useState(params.get('request_id') ?? '')

  const [page, setPage] = useState(1)
  const [data, setData] = useState<PagedLogs | null>(null)
  const [detail, setDetail] = useState<LogRecord | null>(null)
  const [live, setLive] = useState(false)
  const [liveLogs, setLiveLogs] = useState<LogRecord[]>([])
  const esRef = useRef<EventSource | null>(null)

  const queryParams = useCallback(
    (pageNo = page) => {
      const q = new URLSearchParams()
      q.set('page', String(pageNo))
      q.set('page_size', String(PAGE_SIZE))
      if (method) q.set('method', method)
      if (path) q.set('path', path)
      if (keyword) q.set('keyword', keyword)
      if (statusMin) q.set('status_min', statusMin)
      if (minDuration) q.set('min_duration', minDuration)
      if (backend) q.set('backend_url', backend)
      if (requestId) q.set('request_id', requestId)
      return q
    },
    [page, method, path, keyword, statusMin, minDuration, backend, requestId],
  )

  const load = useCallback(() => {
    api.get<PagedLogs>(`/api/logs?${queryParams()}`).then(setData).catch(() => {})
  }, [queryParams])

  useEffect(load, [load])

  // SSE 实时日志流（tail -f）
  const toggleLive = () => {
    if (live) {
      esRef.current?.close()
      esRef.current = null
      setLive(false)
      return
    }
    const es = new EventSource('/api/logs/stream')
    esRef.current = es
    es.onmessage = (ev) => {
      try {
        const rec = JSON.parse(ev.data) as LogRecord
        setLiveLogs((prev) => [rec, ...prev].slice(0, PAGE_SIZE))
      } catch {
        /* 忽略心跳等非 JSON 事件 */
      }
    }
    es.onerror = () => {
      // 连接断开（登录过期 / 服务重启）：自动停止
      es.close()
      esRef.current = null
      setLive(false)
    }
    setLive(true)
  }
  useEffect(() => () => esRef.current?.close(), [])

  const exportCSV = () => {
    window.location.href = `/api/logs/export?${queryParams(1)}`
  }

  const rows = live ? liveLogs : (data?.data ?? [])
  const totalPages = data ? Math.max(1, Math.ceil(data.total / data.page_size)) : 1

  return (
    <div className="space-y-4">
      {/* 筛选栏 */}
      <Card>
        <CardContent className="flex flex-wrap items-center gap-2 p-4">
          <Select value={method} onChange={(e) => setMethod(e.target.value)} className="w-28">
            <option value="">全部方法</option>
            {['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS'].map((m) => (
              <option key={m}>{m}</option>
            ))}
          </Select>
          <Input placeholder="路径包含" value={path} onChange={(e) => setPath(e.target.value)} className="w-44" />
          <Input placeholder="关键词（请求/响应体）" value={keyword} onChange={(e) => setKeyword(e.target.value)} className="w-52" />
          <Select value={statusMin} onChange={(e) => setStatusMin(e.target.value)} className="w-32">
            <option value="">全部状态</option>
            <option value="200">≥ 2xx</option>
            <option value="400">≥ 4xx</option>
            <option value="500">仅 5xx</option>
          </Select>
          <Input placeholder="最小耗时 ms" value={minDuration} onChange={(e) => setMinDuration(e.target.value)} className="w-32" type="number" />
          {backend && (
            <Badge variant="secondary" className="h-9 max-w-72 truncate">
              后端: {backend}
              <button className="ml-1 text-muted-foreground hover:text-foreground" onClick={() => setBackend('')} aria-label="清除后端筛选">
                ×
              </button>
            </Badge>
          )}
          {requestId && (
            <Badge variant="secondary" className="h-9 max-w-72 truncate">
              链路: {requestId}
              <button className="ml-1 text-muted-foreground hover:text-foreground" onClick={() => setRequestId('')} aria-label="清除链路筛选">
                ×
              </button>
            </Badge>
          )}
          <Button size="sm" onClick={() => { setPage(1); load() }}>
            <Search className="h-3.5 w-3.5" /> 查询
          </Button>
          <Button size="sm" variant={live ? 'destructive' : 'outline'} onClick={toggleLive}>
            <Radio className="h-3.5 w-3.5" /> {live ? '停止实时' : '实时日志'}
          </Button>
          <Button size="sm" variant="outline" onClick={exportCSV} disabled={live}>
            <Download className="h-3.5 w-3.5" /> 导出 CSV
          </Button>
        </CardContent>
      </Card>

      {/* 日志表格 */}
      <Card>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b bg-muted/50 text-left text-xs text-muted-foreground">
                  <th className="px-3 py-2.5">ID</th>
                  <th className="px-3 py-2.5">方法</th>
                  <th className="px-3 py-2.5">路径</th>
                  <th className="px-3 py-2.5">状态</th>
                  <th className="px-3 py-2.5">耗时</th>
                  <th className="px-3 py-2.5">链路 ID</th>
                  <th className="px-3 py-2.5">客户端 IP</th>
                  <th className="px-3 py-2.5">后端</th>
                  <th className="px-3 py-2.5">时间</th>
                </tr>
              </thead>
              <tbody>
                {rows.length === 0 && (
                  <tr>
                    <td colSpan={9} className="px-3 py-10 text-center text-muted-foreground">
                      {live ? '等待新日志流入…' : '暂无日志'}
                    </td>
                  </tr>
                )}
                {rows.map((l) => (
                  <tr
                    key={`${l.id}-${l.created_at}`}
                    className="cursor-pointer border-b transition-colors last:border-0 hover:bg-muted/50"
                    onClick={() => setDetail(l)}
                  >
                    <td className="px-3 py-2 tabular-nums text-muted-foreground">{live ? '·' : l.id}</td>
                    <td className="px-3 py-2 font-mono text-xs font-semibold">{l.method}</td>
                    <td className="max-w-72 truncate px-3 py-2 font-mono text-xs">{l.path}</td>
                    <td className="px-3 py-2">
                      <Badge variant={statusVariant(l.status)}>{l.status}</Badge>
                    </td>
                    <td className="px-3 py-2 tabular-nums">{l.duration} ms</td>
                    <td
                      className="max-w-44 truncate px-3 py-2 font-mono text-xs text-indigo-500 dark:text-indigo-400"
                      title={l.request_id || undefined}
                      onClick={(e) => {
                        // 点击链路 ID：以该 ID 筛选列表（同一次请求的所有记录）
                        if (l.request_id) {
                          e.stopPropagation()
                          setRequestId(l.request_id)
                          setPage(1)
                        }
                      }}
                    >
                      {l.request_id ? (
                        <span className="cursor-pointer hover:underline">{l.request_id}</span>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">{l.client_ip}</td>
                    <td className="max-w-56 truncate px-3 py-2 font-mono text-xs text-muted-foreground">{l.backend_url}</td>
                    <td className="whitespace-nowrap px-3 py-2 text-xs text-muted-foreground">{fmtTime(l.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {/* 分页（实时模式隐藏） */}
      {!live && data && (
        <div className="flex items-center justify-between text-sm text-muted-foreground">
          <span>
            共 {data.total.toLocaleString()} 条 · 第 {data.page} / {totalPages} 页
          </span>
          <div className="flex gap-2">
            <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
              上一页
            </Button>
            <Button size="sm" variant="outline" disabled={page >= totalPages} onClick={() => setPage((p) => p + 1)}>
              下一页
            </Button>
          </div>
        </div>
      )}

      {/* 详情弹窗 */}
      <LogDetailDialog log={detail} onClose={() => setDetail(null)} />
    </div>
  )
}

/** 日志详情：基础信息 + 请求/响应 Headers 与 Body（点击行时再拉取完整数据） */
function LogDetailDialog({ log, onClose }: { log: LogRecord | null; onClose: () => void }) {
  const [full, setFull] = useState<LogRecord | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    setFull(null)
    setCopied(false)
    if (log) {
      api.get<LogRecord>(`/api/logs/${log.id}`).then(setFull).catch(() => setFull(log))
    }
  }, [log])

  if (!log) return null
  const rec = full ?? log

  const copyRequestId = async () => {
    if (!rec.request_id) return
    try {
      await navigator.clipboard.writeText(rec.request_id)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      /* 剪贴板不可用时静默 */
    }
  }

  const prettyHeaders = (raw: string) => {
    try {
      return JSON.stringify(JSON.parse(raw), null, 2)
    } catch {
      return raw || '（空）'
    }
  }

  const meta: [string, string][] = [
    ['ID', String(rec.id)],
    ['方法', rec.method],
    ['状态', String(rec.status)],
    ['耗时', `${rec.duration} ms`],
    ['客户端 IP', rec.client_ip],
    ['User-Agent', rec.user_agent],
    ['Referer', rec.referer],
    ['后端', rec.backend_url],
    ['时间', fmtTime(rec.created_at)],
  ]

  return (
    <Dialog open onClose={onClose} title={`日志详情 #${log.id}`}>
      <div className="space-y-4 text-sm">
        <div className="grid grid-cols-2 gap-x-6 gap-y-1.5 rounded-md border bg-muted/30 p-4 md:grid-cols-3">
          {meta.map(([k, v]) => (
            <div key={k} className="min-w-0">
              <span className="text-xs text-muted-foreground">{k}</span>
              <div className="break-all font-mono text-xs">{v || '—'}</div>
            </div>
          ))}
          <div className="min-w-0">
            <span className="text-xs text-muted-foreground">路径</span>
            <div className="break-all font-mono text-xs">
              {rec.path}
              {rec.query ? `?${rec.query}` : ''}
            </div>
          </div>
        </div>

        {/* 链路标记：复制 + 查同请求所有记录 */}
        {rec.request_id && (
          <div className="flex items-center gap-2 rounded-md border border-indigo-500/30 bg-indigo-500/5 px-3 py-2">
            <Fingerprint className="h-4 w-4 shrink-0 text-indigo-500 dark:text-indigo-400" />
            <span className="text-xs text-muted-foreground">链路标记</span>
            <span className="min-w-0 flex-1 truncate font-mono text-xs text-indigo-600 dark:text-indigo-400">{rec.request_id}</span>
            <Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={copyRequestId}>
              <Copy className="h-3 w-3" /> {copied ? '已复制' : '复制'}
            </Button>
            <Button size="sm" variant="outline" className="h-7 px-2 text-xs" onClick={() => window.open(`/logs?request_id=${encodeURIComponent(rec.request_id)}`, '_self')}>
              查同请求
            </Button>
          </div>
        )}

        <Section title="请求 Headers">
          <pre className="max-h-48 overflow-auto">{prettyHeaders(rec.request_headers)}</pre>
        </Section>
        <Section title="请求 Body">
          <pre className="max-h-48 overflow-auto">{rec.request_body || '（空）'}</pre>
        </Section>
        <Section title="响应 Headers">
          <pre className="max-h-48 overflow-auto">{prettyHeaders(rec.response_headers)}</pre>
        </Section>
        <Section title="响应 Body">
          <pre className="max-h-48 overflow-auto">{rec.response_body || '（空）'}</pre>
        </Section>
      </div>
    </Dialog>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1.5 text-xs font-semibold text-muted-foreground">{title}</div>
      <div className="rounded-md border bg-muted/20 p-3 font-mono text-xs leading-relaxed">{children}</div>
    </div>
  )
}
