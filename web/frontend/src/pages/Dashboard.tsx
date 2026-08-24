import { useCallback, useEffect, useMemo, useState } from 'react'
import type * as echarts from 'echarts'
import { Activity, AlertTriangle, Clock, Globe } from 'lucide-react'
import { api, type RealtimeStats, type TrendData, type ClientBucket } from '@/lib/api'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { EChart } from '@/components/EChart'
import { fmtNum } from '@/lib/utils'

const windows = [
  { value: '1h', label: '近 1 小时' },
  { value: '24h', label: '近 24 小时' },
  { value: '7d', label: '近 7 天' },
]

/** 仪表盘：实时指标卡片 + 趋势/耗时折线 + 状态码/热点路径/客户端分布图 */
export default function Dashboard() {
  const [realtime, setRealtime] = useState<RealtimeStats | null>(null)
  const [trend, setTrend] = useState<TrendData | null>(null)
  const [win, setWin] = useState('24h')
  const [clientsBy, setClientsBy] = useState<'ip' | 'ua'>('ip')
  const [clients, setClients] = useState<ClientBucket[]>([])

  // 实时指标：5 秒轮询（PRD 验收：卡片数据延迟 ≤ 5s）
  useEffect(() => {
    const load = () => api.get<RealtimeStats>('/api/stats/realtime').then(setRealtime).catch(() => {})
    load()
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  }, [])

  // 趋势与分布：跟随窗口切换，30 秒刷新
  const loadTrend = useCallback(() => {
    api.get<TrendData>(`/api/stats/trend?window=${win}`).then(setTrend).catch(() => {})
  }, [win])
  useEffect(() => {
    loadTrend()
    const t = setInterval(loadTrend, 30000)
    return () => clearInterval(t)
  }, [loadTrend])

  // 客户端分布
  useEffect(() => {
    api
      .get<{ items: ClientBucket[] }>(`/api/stats/clients?window=${win}&by=${clientsBy}`)
      .then((d) => setClients(d.items ?? []))
      .catch(() => {})
  }, [win, clientsBy])

  const cards = [
    {
      label: '当前 QPS',
      value: (realtime?.last_minute_qps ?? 0).toFixed(2),
      icon: Activity,
      desc: '最近 1 分钟每秒请求数',
    },
    {
      label: '错误率',
      value: (realtime?.error_rate ?? 0).toFixed(2) + '%',
      icon: AlertTriangle,
      desc: `今日 5xx：${fmtNum(realtime?.error_count)} 次`,
    },
    {
      label: '平均耗时',
      value: Math.round(realtime?.avg_duration ?? 0) + ' ms',
      icon: Clock,
      desc: '今日全部请求平均响应',
    },
    {
      label: '今日请求',
      value: fmtNum(realtime?.today_total),
      icon: Globe,
      desc: '今日累计请求数',
    },
  ]

  // 图表 option（hook 统一在组件顶部调用）
  const trendOption = useTrendOption(trend)
  const durationOption = useDurationOption(trend)
  const statusOption = useStatusOption(trend)
  const topPathsOption = useTopPathsOption(trend)
  const clientsOption = useClientsOption(clients)

  return (
    <div className="space-y-6">
      {/* 实时指标卡片 */}
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {cards.map(({ label, value, icon: Icon, desc }) => (
          <Card key={label}>
            <CardContent className="flex items-center justify-between p-5">
              <div>
                <div className="text-sm text-muted-foreground">{label}</div>
                <div className="mt-1 text-2xl font-bold tabular-nums">{value}</div>
                <div className="mt-1 text-xs text-muted-foreground">{desc}</div>
              </div>
              <Icon className="h-8 w-8 text-muted-foreground/50" />
            </CardContent>
          </Card>
        ))}
      </div>

      {/* 窗口切换 */}
      <div className="flex items-center gap-2">
        {windows.map((w) => (
          <button
            key={w.value}
            onClick={() => setWin(w.value)}
            className={`rounded-md border px-3 py-1.5 text-sm transition-colors ${
              win === w.value ? 'bg-primary text-primary-foreground' : 'bg-card hover:bg-accent'
            }`}
          >
            {w.label}
          </button>
        ))}
      </div>

      {/* 趋势图：请求量 + 错误 */}
      <Card>
        <CardHeader>
          <CardTitle>请求量与错误趋势</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="h-72">
            <EChart option={trendOption} />
          </div>
        </CardContent>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        {/* 耗时趋势 + 分位数 */}
        <Card>
          <CardHeader>
            <CardTitle>耗时趋势（P50 / P90 / P99）</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="mb-3 flex gap-4 text-sm">
              {(['p50', 'p90', 'p99'] as const).map((k, i) => (
                <span key={k} className="text-muted-foreground">
                  <span className="mr-1.5 inline-block h-2 w-2 rounded-full" style={{ background: ['#3b82f6', '#f59e0b', '#ef4444'][i] }} />
                  {k.toUpperCase()}：
                  <span className="font-semibold text-foreground">{Math.round(trend?.percentiles?.[k] ?? 0)} ms</span>
                </span>
              ))}
            </div>
            <div className="h-56">
              <EChart option={durationOption} />
            </div>
          </CardContent>
        </Card>

        {/* 状态码分布饼图 */}
        <Card>
          <CardHeader>
            <CardTitle>状态码分布</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="h-56">
              <EChart option={statusOption} />
            </div>
          </CardContent>
        </Card>

        {/* 热点路径条形图 */}
        <Card>
          <CardHeader>
            <CardTitle>热点路径 Top 10</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="h-64">
              <EChart option={topPathsOption} />
            </div>
          </CardContent>
        </Card>

        {/* 客户端分布 */}
        <Card>
          <CardHeader className="flex-row items-center justify-between space-y-0">
            <CardTitle>客户端分布</CardTitle>
            <div className="flex gap-1">
              {(['ip', 'ua'] as const).map((k) => (
                <button
                  key={k}
                  onClick={() => setClientsBy(k)}
                  className={`rounded border px-2 py-0.5 text-xs ${
                    clientsBy === k ? 'bg-primary text-primary-foreground' : 'bg-card hover:bg-accent'
                  }`}
                >
                  {k === 'ip' ? '按 IP' : '按 UA'}
                </button>
              ))}
            </div>
          </CardHeader>
          <CardContent>
            <div className="h-64">
              <EChart option={clientsOption} />
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

// ---- 图表 option 构造（useMemo 缓存避免不必要的重渲染）----

function useTrendOption(trend: TrendData | null) {
  const points = trend?.points ?? []
  return useMemo<echarts.EChartsOption>(
    () => ({
      tooltip: { trigger: 'axis' },
      legend: { data: ['请求数', '错误数'] },
      grid: { left: 8, right: 16, top: 36, bottom: 28, containLabel: true },
      xAxis: { type: 'category', data: points.map((p) => new Date(p.ts).toLocaleTimeString('zh-CN', { hour12: false })) },
      yAxis: { type: 'value', minInterval: 1 },
      series: [
        { name: '请求数', type: 'line', smooth: true, showSymbol: false, data: points.map((p) => p.count), areaStyle: { opacity: 0.08 } },
        { name: '错误数', type: 'line', smooth: true, showSymbol: false, itemStyle: { color: '#ef4444' }, data: points.map((p) => p.error_count) },
      ],
    }),
    [points],
  )
}

function useDurationOption(trend: TrendData | null) {
  const points = trend?.points ?? []
  return useMemo<echarts.EChartsOption>(
    () => ({
      tooltip: { trigger: 'axis', valueFormatter: (v) => `${Math.round(Number(v ?? 0))} ms` },
      grid: { left: 8, right: 16, top: 16, bottom: 28, containLabel: true },
      xAxis: { type: 'category', data: points.map((p) => new Date(p.ts).toLocaleTimeString('zh-CN', { hour12: false })) },
      // 轴标签人性化：大数值换算成秒，避免标签过宽挤占绘图区（containLabel 保证不被裁剪）
      yAxis: {
        type: 'value',
        axisLabel: {
          formatter: (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${Math.round(v)}ms`),
        },
      },
      series: [
        {
          name: '平均耗时',
          type: 'line',
          smooth: true,
          showSymbol: false,
          itemStyle: { color: '#3b82f6' },
          areaStyle: { opacity: 0.08 },
          data: points.map((p) => Math.round(p.avg_duration)),
        },
      ],
    }),
    [points],
  )
}

const statusColors: Record<string, string> = { '2xx': '#22c55e', '3xx': '#3b82f6', '4xx': '#f59e0b', '5xx': '#ef4444' }

function useStatusOption(trend: TrendData | null) {
  const dist = trend?.status_distribution ?? []
  return useMemo<echarts.EChartsOption>(
    () => ({
      tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
      legend: { bottom: 0 },
      series: [
        {
          type: 'pie',
          radius: ['42%', '70%'],
          center: ['50%', '44%'],
          label: { formatter: '{b} {d}%' },
          data: dist.map((d) => ({ name: d.class, value: d.count, itemStyle: { color: statusColors[d.class] } })),
        },
      ],
    }),
    [dist],
  )
}

function useTopPathsOption(trend: TrendData | null) {
  const paths = [...(trend?.top_paths ?? [])].reverse()
  return useMemo<echarts.EChartsOption>(
    () => ({
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
      grid: { left: 8, right: 40, top: 8, bottom: 24, containLabel: true },
      xAxis: { type: 'value', minInterval: 1 },
      yAxis: {
        type: 'category',
        data: paths.map((p) => (p.path.length > 32 ? p.path.slice(0, 30) + '…' : p.path)),
        axisLabel: { width: 220, overflow: 'truncate' },
      },
      series: [{ type: 'bar', itemStyle: { color: '#6366f1' }, barMaxWidth: 18, data: paths.map((p) => p.count) }],
    }),
    [paths],
  )
}

function useClientsOption(clients: ClientBucket[]) {
  return useMemo<echarts.EChartsOption>(
    () => ({
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
      grid: { left: 8, right: 40, top: 8, bottom: 24, containLabel: true },
      xAxis: { type: 'value', minInterval: 1 },
      yAxis: {
        type: 'category',
        data: clients.map((c) => (c.key.length > 36 ? c.key.slice(0, 34) + '…' : c.key)).reverse(),
        axisLabel: { width: 260, overflow: 'truncate' },
      },
      series: [{ type: 'bar', itemStyle: { color: '#14b8a6' }, barMaxWidth: 18, data: clients.map((c) => c.count).reverse() }],
    }),
    [clients],
  )
}
