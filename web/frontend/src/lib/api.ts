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
  created_at: string
}

export interface PagedLogs {
  total: number
  page: number
  page_size: number
  data: LogRecord[] | null
}

export interface BackendStat {
  url: string
  healthy: boolean
}

export interface SettingsInfo {
  backends: BackendStat[] | null
  strategy: string
  log: {
    level: string
    sample_rate: number
    retention_days: number
    mask_sensitive: boolean
    body_max_bytes: number
  }
  proxy: {
    timeout_seconds: number
    max_body_bytes: number
    trust_forwarded_headers: boolean
  }
  rate_limit: {
    default_rpm: number
  }
  managed: boolean
}

export interface TokenInfo {
  id: number
  name: string
  rate_limit_rpm: number
  created_at: string
  last_used_at: string | null
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
}
