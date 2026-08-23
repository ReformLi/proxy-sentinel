import { useEffect, useRef } from 'react'
import * as echarts from 'echarts'
import { cn } from '@/lib/utils'

interface EChartProps {
  option: echarts.EChartsOption
  className?: string
  style?: React.CSSProperties
  /** 节点点击事件（params 结构见 ECharts 文档） */
  onElementClick?: (params: echarts.ECElementEvent) => void
}

/** ECharts React 封装：自动初始化/销毁/自适应尺寸/点击事件 */
export function EChart({ option, className, style, onElementClick }: EChartProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<echarts.ECharts | null>(null)
  const clickRef = useRef(onElementClick)
  clickRef.current = onElementClick

  useEffect(() => {
    if (!containerRef.current) return
    const chart = echarts.init(containerRef.current)
    chartRef.current = chart
    const onClick = (params: echarts.ECElementEvent) => clickRef.current?.(params)
    chart.on('click', onClick)

    const observer = new ResizeObserver(() => chart.resize())
    observer.observe(containerRef.current)
    return () => {
      observer.disconnect()
      chart.dispose()
      chartRef.current = null
    }
  }, [])

  useEffect(() => {
    chartRef.current?.setOption(option, { notMerge: true })
  }, [option])

  return <div ref={containerRef} className={cn('h-full w-full', className)} style={style} />
}
