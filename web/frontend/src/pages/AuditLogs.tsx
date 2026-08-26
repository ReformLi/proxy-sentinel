import { useCallback, useEffect, useState } from 'react'
import { Download, Search, User, Globe } from 'lucide-react'
import { api, type AuditRecord, type PagedAudits } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog } from '@/components/ui/dialog'
import { fmtTime } from '@/lib/utils'

const PAGE_SIZE = 50

export default function AuditLogs() {
  const [username, setUsername] = useState('')
  const [keyword, setKeyword] = useState('')
  const [ip, setIP] = useState('')
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const [page, setPage] = useState(1)
  const [data, setData] = useState<PagedAudits | null>(null)
  const [detail, setDetail] = useState<AuditRecord | null>(null)

  const queryParams = useCallback(
    (pageNo = page) => {
      const q = new URLSearchParams()
      q.set('page', String(pageNo))
      q.set('page_size', String(PAGE_SIZE))
      if (username) q.set('username', username)
      if (keyword) q.set('keyword', keyword)
      if (ip) q.set('ip', ip)
      if (start) q.set('start', new Date(start).toISOString())
      if (end) q.set('end', new Date(end).toISOString())
      return q
    },
    [page, username, keyword, ip, start, end],
  )

  const load = useCallback(() => {
    api.get<PagedAudits>(`/api/audit-logs?${queryParams()}`).then(setData).catch(() => {})
  }, [queryParams])

  useEffect(load, [load])

  const exportCSV = () => {
    window.location.href = `/api/audit-logs/export?${queryParams(1)}`
  }

  const totalPages = data ? Math.max(1, Math.ceil(data.total / data.page_size)) : 1
  const rows = data?.data ?? []

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="flex flex-wrap items-center gap-2 p-4">
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <User className="h-3.5 w-3.5" /> 用户
          </span>
          <Input placeholder="用户名（精确）" value={username} onChange={(e) => setUsername(e.target.value)} className="w-40" />
          <Input placeholder="操作关键词" value={keyword} onChange={(e) => setKeyword(e.target.value)} className="w-56" />
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <Globe className="h-3.5 w-3.5" /> IP
          </span>
          <Input placeholder="IP（精确或前缀）" value={ip} onChange={(e) => setIP(e.target.value)} className="w-40" />
          <Input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} className="w-52" />
          <span className="text-muted-foreground">至</span>
          <Input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} className="w-52" />
          <Button size="sm" onClick={() => { setPage(1); load() }}>
            <Search className="h-3.5 w-3.5" /> 查询
          </Button>
          <Button size="sm" variant="outline" onClick={exportCSV}>
            <Download className="h-3.5 w-3.5" /> 导出 CSV
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b bg-muted/50 text-left text-xs text-muted-foreground">
                  <th className="px-3 py-2.5">ID</th>
                  <th className="px-3 py-2.5">用户</th>
                  <th className="px-3 py-2.5">操作</th>
                  <th className="px-3 py-2.5">IP</th>
                  <th className="px-3 py-2.5">时间</th>
                </tr>
              </thead>
              <tbody>
                {rows.length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-3 py-10 text-center text-muted-foreground">
                      暂无审计日志
                    </td>
                  </tr>
                )}
                {rows.map((a) => (
                  <tr
                    key={a.id}
                    className="cursor-pointer border-b transition-colors last:border-0 hover:bg-muted/50"
                    onClick={() => setDetail(a)}
                  >
                    <td className="px-3 py-2 tabular-nums text-muted-foreground">{a.id}</td>
                    <td className="px-3 py-2">
                      <span className="inline-flex items-center gap-1 font-medium">
                        <User className="h-3.5 w-3.5 text-muted-foreground" />
                        {a.username || <span className="text-muted-foreground">—</span>}
                      </span>
                    </td>
                    <td className="max-w-[50%] px-3 py-2 break-words">{a.action}</td>
                    <td className="px-3 py-2 font-mono text-xs">{a.ip || '—'}</td>
                    <td className="whitespace-nowrap px-3 py-2 text-xs text-muted-foreground">{fmtTime(a.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {data && (
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

      <AuditDetailDialog audit={detail} onClose={() => setDetail(null)} />
    </div>
  )
}

function AuditDetailDialog({ audit, onClose }: { audit: AuditRecord | null; onClose: () => void }) {
  if (!audit) return null
  const meta: [string, string][] = [
    ['ID', String(audit.id)],
    ['用户', audit.username || '—'],
    ['IP', audit.ip || '—'],
    ['时间', fmtTime(audit.created_at)],
  ]
  return (
    <Dialog open onClose={onClose} title={`审计日志 #${audit.id}`}>
      <div className="space-y-4 text-sm">
        <div className="grid grid-cols-2 gap-x-6 gap-y-1.5 rounded-md border bg-muted/30 p-4">
          {meta.map(([k, v]) => (
            <div key={k}>
              <span className="text-xs text-muted-foreground">{k}</span>
              <div className="break-all font-mono text-xs">{v}</div>
            </div>
          ))}
        </div>
        <div>
          <div className="mb-1.5 text-xs font-semibold text-muted-foreground">操作详情</div>
          <pre className="whitespace-pre-wrap break-words rounded-md border bg-muted/20 p-3 font-mono text-xs leading-relaxed">
{audit.action || '（空）'}
          </pre>
        </div>
      </div>
    </Dialog>
  )
}
