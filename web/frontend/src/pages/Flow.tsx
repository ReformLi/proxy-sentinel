import { useEffect, useMemo, useState } from 'react'
import type * as echarts from 'echarts'
import { useNavigate } from 'react-router-dom'
import { api, type FlowNode } from '@/lib/api'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { EChart } from '@/components/EChart'
import { fmtNum } from '@/lib/utils'

const windows = [
  { value: '1h', label: '近 1 小时' },
  { value: '24h', label: '近 24 小时' },
  { value: '7d', label: '近 7 天' },
]

/** 数据流向拓扑：客户端 → Sentinel 网关 → 后端集群
 *  边宽 = 请求量；边颜色 = 平均耗时（绿快红慢）；悬停显示数值；点击后端节点跳转日志详情 */
export default function Flow() {
  const navigate = useNavigate()
  const [win, setWin] = useState('24h')
  const [nodes, setNodes] = useState<FlowNode[]>([])

  useEffect(() => {
    api.get<FlowNode[]>(`/api/stats/flow?window=${win}`).then((d) => setNodes(d ?? [])).catch(() => setNodes([]))
  }, [win])

  const option = useMemo<echarts.EChartsOption>(() => {
    const total = nodes.reduce((s, n) => s + n.count, 0)
    const durations = nodes.map((n) => n.avg_duration)
    const minD = Math.min(0, ...durations)
    const maxD = Math.max(1, ...durations)

    // graph 节点：客户端 / 网关 / 各后端（后端按当前列表顺序编号：后端1、后端2…）
    const graphNodes = [
      { id: 'clients', name: '客户端', x: 60, y: 300, symbolSize: 64, itemStyle: { color: '#6366f1' } },
      { id: 'gateway', name: '代理网关\nProxy Sentinel', x: 340, y: 300, symbolSize: 84, itemStyle: { color: '#0ea5e9' } },
      ...nodes.map((n, i) => {
        const y = nodes.length === 1 ? 300 : 80 + (i * 440) / (nodes.length - 1)
        return {
          id: `be-${i}`,
          name: n.backend_url,
          x: 660,
          y,
          symbolSize: 48,
          itemStyle: { color: n.error_count > 0 ? '#ef4444' : '#22c55e' },
          // 方块内显示编号，悬停 tooltip 显示完整 URL
          label: { show: true, formatter: `后端${i + 1}`, position: 'inside' as const, color: '#fff' },
          tooltip: { formatter: `后端${i + 1}：${n.backend_url}` },
        }
      }),
    ]

    // graph 边：统一线宽 + 统一箭头；边 label 显示请求量与占比
    const edge = (
      source: string,
      target: string,
      count: number,
      duration: number,
      share?: number,
    ) => ({
      source,
      target,
      lineStyle: {
        width: 2.5,
        color: durationColor(duration, minD, maxD),
        curveness: 0,
      },
      value: count,
      label: {
        show: true,
        position: 'middle' as const,
        rotate: 0,
        fontSize: 11,
        formatter: share !== undefined ? `${fmtNum(count)} (${share.toFixed(1)}%)` : fmtNum(count),
        backgroundColor: 'rgba(255,255,255,0.9)',
        padding: [2, 5, 2, 5] as [number, number, number, number],
        borderRadius: 4,
      },
    })
    const gatewayAvg = nodes.length ? nodes.reduce((s, n) => s + n.avg_duration * n.count, 0) / Math.max(1, total) : 0
    const graphEdges = [
      // 客户端 → 网关：只显示总量，不计算占比（恒 100%）
      edge('clients', 'gateway', total, gatewayAvg),
      // 网关 → 各后端：显示请求量 + 占总量比例
      ...nodes.map((n, i) =>
        edge('gateway', `be-${i}`, n.count, n.avg_duration, total > 0 ? (n.count / total) * 100 : 0),
      ),
    ]

    return {
      tooltip: {
        formatter: (params) => {
          const p = Array.isArray(params) ? params[0] : params
          if (p?.dataType === 'edge') return `请求量：${fmtNum(Number(p.value ?? 0))}`
          // 节点：后端节点带专属 tooltip（编号+完整 URL），客户端/网关显示名称
          const data = p?.data as { tooltip?: { formatter?: string } } | undefined
          if (data?.tooltip?.formatter) return data.tooltip.formatter
          return p?.name ?? ''
        },
      },
      legend: {
        bottom: 10,
        data: ['快 (<P50)', '中', '慢 (>P90)'],
        formatter: () => '节点：绿=无错误 红=有5xx · 边颜色：绿=快 红=慢 · 边标签：请求量（占比）',
        selectedMode: false,
      },
      series: [
        {
          type: 'graph',
          layout: 'none',
          symbol: 'roundRect',
          symbolSize: 50,
          edgeSymbol: ['none', 'arrow'],
          edgeSymbolSize: 10,
          roam: true,
          label: { show: true, fontSize: 12, color: '#fff' },
          emphasis: { focus: 'adjacency', lineStyle: { width: 5 } },
          data: graphNodes,
          links: graphEdges,
          lineStyle: { opacity: 0.9 },
        },
      ],
    }
  }, [nodes])

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div>
            <CardTitle>数据流向拓扑</CardTitle>
            <CardDescription>边颜色表示平均耗时（绿快红慢），边标签显示请求量及占比；悬停查看数值，点击后端节点跳转对应日志</CardDescription>
          </div>
          <div className="flex gap-2">
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
        </CardHeader>
        <CardContent>
          <div className="h-[420px]">
            {nodes.length === 0 ? (
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">暂无流量数据</div>
            ) : (
              <EChart
                option={option}
                onElementClick={(params) => {
                  // 点击后端节点 → 携带 backend 筛选跳转日志页
                  if (params.dataType === 'node' && String(params.name).startsWith('http')) {
                    navigate(`/logs?backend_url=${encodeURIComponent(params.name)}`)
                  }
                }}
              />
            )}
          </div>
        </CardContent>
      </Card>

      {/* 后端流量明细表 */}
      <Card>
        <CardHeader>
          <CardTitle>后端流量明细</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50 text-left text-xs text-muted-foreground">
                <th className="px-4 py-2.5">后端地址</th>
                <th className="px-4 py-2.5">请求量</th>
                <th className="px-4 py-2.5">平均耗时</th>
                <th className="px-4 py-2.5">错误数</th>
                <th className="px-4 py-2.5">操作</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((n) => (
                <tr key={n.backend_url} className="border-b last:border-0 hover:bg-muted/50">
                  <td className="px-4 py-2.5 font-mono text-xs">{n.backend_url}</td>
                  <td className="px-4 py-2.5 tabular-nums">{fmtNum(n.count)}</td>
                  <td className="px-4 py-2.5 tabular-nums">{Math.round(n.avg_duration)} ms</td>
                  <td className="px-4 py-2.5 tabular-nums">
                    <span className={n.error_count > 0 ? 'text-destructive' : ''}>{fmtNum(n.error_count)}</span>
                  </td>
                  <td className="px-4 py-2.5">
                    <button
                      className="text-xs text-primary underline-offset-2 hover:underline"
                      onClick={() => navigate(`/logs?backend_url=${encodeURIComponent(n.backend_url)}`)}
                    >
                      查看日志
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>
    </div>
  )
}

function durationColor(d: number, minD: number, maxD: number): string {
  if (maxD <= minD) return '#22c55e'
  const t = (d - minD) / (maxD - minD) // 0=最快 1=最慢
  // 绿(34,197,94) → 黄(245,158,11) → 红(239,68,68)
  const stops: [number, number, number][] = [
    [34, 197, 94],
    [245, 158, 11],
    [239, 68, 68],
  ]
  const seg = t < 0.5 ? 0 : 1
  const local = t < 0.5 ? t * 2 : (t - 0.5) * 2
  const [r1, g1, b1] = stops[seg]
  const [r2, g2, b2] = stops[seg + 1]
  const mix = (a: number, b: number) => Math.round(a + (b - a) * local)
  return `rgb(${mix(r1, r2)},${mix(g1, g2)},${mix(b1, b2)})`
}
