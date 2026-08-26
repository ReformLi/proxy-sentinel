// 统一 API 客户端：401 自动跳转登录页
export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
    ...init,
  })
  if (resp.status === 401 && !path.startsWith('/api/auth/login')) {
    // 会话过期：跳转登录页（保留当前路径便于登录后回跳）
    // 已在 /login 页时不再跳转，否则会把 /login 自身反复拼进 redirect 造成无限重定向闪屏
    if (window.location.pathname !== '/login') {
      const cur = encodeURIComponent(window.location.pathname + window.location.search)
      window.location.href = `/login?redirect=${cur}`
    }
    throw new ApiError(401, '未登录')
  }
  const text = await resp.text()
  let data: unknown = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    /* 非 JSON 响应 */
  }
  if (!resp.ok) {
    const msg = (data as { error?: string })?.error ?? `请求失败 (${resp.status})`
    throw new ApiError(resp.status, msg)
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'POST', body: body !== undefined ? JSON.stringify(body) : undefined }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PUT', body: body !== undefined ? JSON.stringify(body) : undefined }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}

// ---- 类型定义（与后端 JSON 字段对齐）----

export interface RealtimeStats {
  today_total: number
  error_count: number
  error_rate: number
  avg_duration: number
  last_minute_qps: number
}

export interface TrendPoint {
  ts: string
  count: number
  error_count: number
  avg_duration: number
}

export interface Percentiles {
  p50: number
  p90: number
  p99: number
}

export interface StatusBucket {
  class: string
  count: number
}

export interface TopPath {
  path: string
  count: number
}

export interface TrendData {
  points: TrendPoint[] | null
  percentiles: Percentiles
  status_distribution: StatusBucket[] | null
  top_paths: TopPath[] | null
}

export interface FlowNode {
  backend_url: string
  count: number
  avg_duration: number
  error_count: number
}

export interface ClientBucket {
  key: string
  count: number
}

export interface LogRecord {
  id: number
  method: string
  path: string
  query: string
  request_headers: string
  request_body: string
  status: number
  response_headers: string
  response_body: string
  duration: number
  client_ip: string
  user_agent: string
  referer: string
  backend_url: string
  request_id: string
  created_at: string
}

export interface PagedLogs {
  total: number
  page: number
  page_size: number
  data: LogRecord[] | null
}

export interface AuditRecord {
  id: number
  username: string
  action: string
  ip: string
  created_at: string
}

export interface PagedAudits {
  total: number
  page: number
  page_size: number
  data: AuditRecord[] | null
}

export interface BackendStat {
  url: string
  healthy: boolean
  weight: number
  health_path?: string
}

export type RouteRuleType = 'header' | 'cookie' | 'path'

export interface RouteRule {
  type: RouteRuleType
  key: string
  value: string
  backend: string
}

export interface RewriteRule {
  prefix: string
  replacement: string
  backend: string
}

export interface SettingsInfo {
  backends: BackendStat[] | null
  strategy: string
  rules: RouteRule[] | null
  rewrites: RewriteRule[] | null
  log: {
    level: string
    sample_rate: number
    retention_days: number
    health_retention_days: number
    audit_retention_days: number
    mask_sensitive: boolean
    body_max_bytes: number
    queue_capacity: number
  }
  proxy: {
    timeout_seconds: number
    max_body_bytes: number
    max_upload_bytes: number
    trust_forwarded_headers: boolean
  }
  rate_limit: {
    default_rpm: number
  }
  managed: boolean
}

export interface MaintenanceTableStat {
  table: 'log' | 'health' | 'audit'
  label: string
  count: number
  size_bytes: number
  retention_days: number
  time_column: string
}

export interface MaintenanceStats {
  db_size_bytes: number
  tables: MaintenanceTableStat[]
}

export interface PurgeRequest {
  tables: Array<'log' | 'health' | 'audit'>
  keep_days: number
  confirm: true
}

export interface PurgeResult {
  deleted: Record<string, number>
}

export interface TokenInfo {
  id: number
  name: string
  rate_limit_rpm: number
  created_at: string
  last_used_at: string | null
  expires_at: string | null
}

export interface TokenListData {
  tokens: TokenInfo[] | null
  default_rpm: number
}

export interface CreatedToken {
  message: string
  token: string
  name: string
  rate_limit_rpm: number
  expires_at?: string
}

export interface AlertRules {
  enabled: boolean
  error_rate_pct: number
  window_minutes: number
  min_sample: number
  backend_down: boolean
  latency_ms: number
  silence_minutes: number
}

export interface AlertConfigInfo {
  rules: AlertRules
  dingtalk_configured: boolean
  check_interval_seconds: number
}

export type IPACLMode = 'off' | 'on'

export type IPACLDefault = 'allow' | 'deny'

export interface IPACLEntry {
  value: string
  note: string
}

export interface IPACLConfig {
  mode: IPACLMode
  default: IPACLDefault
  blacklist: IPACLEntry[] | null
  whitelist: IPACLEntry[] | null
}

export interface HealthPoint {
  ts: string
  healthy: boolean
  latency_avg: number
  probes: number
}

export interface TrafficPoint {
  ts: string
  count: number
  error_count: number
  avg_duration: number
}

export interface BackendMonitorItem {
  backend: string
  healthy: boolean
  health_path: string
  uptime_pct: number
  probes: HealthPoint[] | null
  traffic: TrafficPoint[] | null
}

export interface BackendMonitorResp {
  window: string
  items: BackendMonitorItem[] | null
}
