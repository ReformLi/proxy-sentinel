import { useCallback, useEffect, useMemo, useState } from 'react'
import type * as echarts from 'echarts'
import { Activity, HeartPulse, RefreshCw } from 'lucide-react'
import { api, type BackendMonitorItem } from '@/lib/api'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { EChart } from '@/components/EChart'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const windows = [
  { value: '1h', label: '近 1 小时' },
  { value: '24h', label: '近 24 小时' },
  { value: '7d', label: '近 7 天' },
]

/** 后端监控：健康探测 RTT 趋势 + 不健康时段色带 + 真实流量/错误率 */
export default function Backends() {
  const [win, setWin] = useState('24h')
  const [items, setItems] = useState<BackendMonitorItem[]>([])
  const [loading, setLoading] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    api
      .get<{ window: string; items: BackendMonitorItem[] | null }>(`/api/stats/backends?window=${win}`)
      .then((d) => setItems(d.items ?? []))
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [win])

  useEffect(() => {
    load()
    const t = setInterval(load, 30000) // 30s 刷新（探测周期 30s）
    return () => clearInterval(t)
  }, [load])

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-bold">后端监控</h1>
        <span className="text-xs text-muted-foreground">探测周期 30s · 每 30 秒自动刷新</span>
        <div className="flex-1" />
        <div className="flex gap-1 rounded-md border p-0.5">
          {windows.map((w) => (
            <button
              key={w.value}
              onClick={() => setWin(w.value)}
              className={cn(
                'rounded px-2.5 py-1 text-xs transition-colors',
                win === w.value ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-accent',
              )}
            >
              {w.label}
            </button>
          ))}
        </div>
        <Button variant="outline" size="icon" onClick={load} disabled={loading} title="刷新">
          <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />
        </Button>
      </div>

      {items.length === 0 && <p className="text-sm text-muted-foreground">暂无后端（先在配置管理中添加）</p>}

      <div className="space-y-4">
        {items.map((it) => (
          <BackendCard key={it.backend} item={it} win={win} />
        ))}
      </div>
    </div>
  )
}

function BackendCard({ item, win }: { item: BackendMonitorItem; win: string }) {
  const probeOption = useProbeOption(item, win)
  const trafficOption = useTrafficOption(item, win)
  const totalReq = (item.traffic ?? []).reduce((s, p) => s + p.count, 0)
  const totalErr = (item.traffic ?? []).reduce((s, p) => s + p.error_count, 0)
  const errRate = totalReq > 0 ? ((totalErr / totalReq) * 100).toFixed(2) : '0.00'
  const lastLatency = item.probes?.length ? Math.round(item.probes[item.probes.length - 1].latency_avg) : null

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-center gap-2">
          <span
            className={cn(
              'h-2.5 w-2.5 rounded-full',
              item.healthy ? 'bg-emerald-500' : 'bg-red-500 animate-pulse',
            )}
          />
          <CardTitle className="font-mono text-sm">{item.backend}</CardTitle>
          <span className={cn('rounded px-1.5 py-0.5 text-xs', item.healthy ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : 'bg-red-500/10 text-red-600 dark:text-red-400')}>
            {item.healthy ? '健康' : '不可用'}
          </span>
          <span className="text-xs text-muted-foreground">
            探测路径 <code className="font-mono">{item.health_path || '/'}</code>
          </span>
          <div className="flex-1" />
          <div className="flex items-center gap-4 text-xs text-muted-foreground">
            <span title="窗口内探测可用率">
              可用率 <span className={cn('font-semibold', item.uptime_pct >= 99 ? 'text-emerald-600 dark:text-emerald-400' : item.uptime_pct >= 90 ? 'text-amber-600 dark:text-amber-400' : 'text-red-600 dark:text-red-400')}>{item.uptime_pct.toFixed(1)}%</span>
            </span>
            <span title="最近一次探测延迟">
              <HeartPulse className="mr-1 inline h-3.5 w-3.5" />
              {lastLatency !== null ? `${lastLatency} ms` : '—'}
            </span>
            <span title="窗口内真实流量">
              <Activity className="mr-1 inline h-3.5 w-3.5" />
              {totalReq} 次 · 错误率 {errRate}%
            </span>
          </div>
        </div>
      </CardHeader>
      <CardContent className="grid gap-4 lg:grid-cols-2">
        <div>
          <p className="mb-1 text-xs font-medium text-muted-foreground">探测延迟（ms）· 红色色带 = 不健康时段</p>
          <EChart option={probeOption} style={{ height: 220 }} />
        </div>
        <div>
          <p className="mb-1 text-xs font-medium text-muted-foreground">真实流量（请求量 / 5xx 次数）</p>
          <EChart option={trafficOption} style={{ height: 220 }} />
        </div>
      </CardContent>
    </Card>
  )
}

function fmtTs(iso: string, win: string) {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  // 7d 窗口显示日期，其余显示时分
  const pad = (n: number) => String(n).padStart(2, '0')
  if (win === '7d') return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 探测 RTT 折线 + 不健康桶 markArea（连续不健康桶合并为一段红色背景） */
function useProbeOption(item: BackendMonitorItem, win: string): echarts.EChartsOption {
  return useMemo(() => {
    const probes = item.probes ?? []
    const xs = probes.map((p) => fmtTs(p.ts, win))
    const ys = probes.map((p) => Math.round(p.latency_avg))
    const areas: [number, number][] = []
    let start = -1
    probes.forEach((p, i) => {
      if (!p.healthy) {
        if (start < 0) start = i
      } else if (start >= 0) {
        areas.push([start, i - 1])
        start = -1
      }
    })
    if (start >= 0) areas.push([start, probes.length - 1])
    const markArea = areas.map(
      ([s, e]) =>
        [
          { xAxis: xs[s], itemStyle: { color: 'rgba(239,68,68,0.15)' } },
          { xAxis: xs[e] },
        ] as [{ xAxis: string; itemStyle: { color: string } }, { xAxis: string }],
    )
    return {
      tooltip: { trigger: 'axis' },
      grid: { left: 8, right: 12, top: 12, bottom: 24, containLabel: true },
      xAxis: { type: 'category', data: xs, axisLabel: { fontSize: 10 } },
      yAxis: { type: 'value', name: 'ms', axisLabel: { fontSize: 10 } },
      series: [
        {
          type: 'line',
          data: ys,
          showSymbol: false,
          smooth: true,
          lineStyle: { color: '#10b981', width: 1.5 },
          areaStyle: { color: 'rgba(16,185,129,0.08)' },
          markArea: markArea.length ? { silent: true, data: markArea } : undefined,
        },
      ],
    }
  }, [item, win])
}

/** 流量柱状 + 5xx 折线（双 Y 轴） */
function useTrafficOption(item: BackendMonitorItem, win: string): echarts.EChartsOption {
  return useMemo(() => {
    const traffic = item.traffic ?? []
    const xs = traffic.map((p) => fmtTs(p.ts, win))
    return {
      tooltip: { trigger: 'axis' },
      // 双轴名称与图例信息重复（请求量/5xx），小图里去掉轴名称避免与图例挤在一起
      legend: { top: 0, right: 0, itemWidth: 14, itemHeight: 8, textStyle: { fontSize: 10 } },
      grid: { left: 8, right: 8, top: 22, bottom: 24, containLabel: true },
      xAxis: { type: 'category', data: xs, axisLabel: { fontSize: 10 } },
      yAxis: [
        { type: 'value', axisLabel: { fontSize: 10 } },
        { type: 'value', axisLabel: { fontSize: 10 }, splitLine: { show: false } },
      ],
      series: [
        {
          name: '请求量',
          type: 'bar',
          data: traffic.map((p) => p.count),
          itemStyle: { color: '#6366f1' },
          barMaxWidth: 12,
        },
        {
          name: '5xx',
          type: 'line',
          yAxisIndex: 1,
          data: traffic.map((p) => p.error_count),
          showSymbol: false,
          lineStyle: { color: '#ef4444', width: 1.5 },
        },
      ],
    }
  }, [item, win])
}
