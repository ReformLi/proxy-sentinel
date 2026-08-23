# Proxy Sentinel · 代理网关与日志分析平台

**版本**：V1.0
**状态**：已完成（核心功能全部实现并冒烟测试通过）
**目标平台**：独立部署（Linux VPS / Docker / Windows）

独立部署的反向代理网关：拦截全部请求并转发至后端集群，完整记录请求/响应日志，提供可视化看板、日志检索、数据流向拓扑与在线配置管理。前端构建产物嵌入单二进制，零依赖部署。

---

## 1. 功能特性

### 反向代理引擎
- HTTP/HTTPS 全路径转发，保留原始请求方法与 Headers
- 负载均衡：轮询（round_robin）/ 随机（random），支持运行时切换并持久化
- 健康检查：30s 周期探测，自动剔除故障节点、恢复后自动上线
- 超时控制（默认 30s）、请求体大小限制（默认 10MB）、流式响应（大文件不缓存）
- 可选信任 `X-Forwarded-For`（仅前置可信反代时开启）

### 日志系统
- 完整记录：方法、路径、Headers、请求/响应体、状态码、耗时、客户端 IP/UA/Referer、命中的后端
- 异步批量落盘（满 100 条或每 5s 单事务提交），不阻塞请求响应
- 敏感字段自动脱敏（`Authorization`、`Cookie`、`Password` 等）
- 采样率可配置（高并发下降采样）、保留天数自动清理（默认 30 天）

### 双认证体系
| 维度 | 代理接口 `/proxy/*` | 可视化页面 `/api/*` |
|:--|:--|:--|
| 认证方式 | Bearer Token（哈希存储） | JWT（HttpOnly Cookie，24h 过期） |
| 适用对象 | 后端服务（机器） | 管理员（浏览器） |
| 存储 | `proxy_tokens` 表，支持多 Token | `users` 表（bcrypt） |

### 可视化看板（React 18 + ECharts）
- **仪表盘**：QPS / 错误率 / 平均耗时 / 今日请求 4 卡片（5s 轮询），请求量与错误趋势、P50/P90/P99 耗时曲线、状态码饼图、热点路径 Top10、客户端分布（按 IP/UA）
- **日志查询**：多条件筛选（时间/状态码/方法/路径/耗时/关键词/后端）、分页、详情弹窗、SSE 实时流（类 tail -f）、CSV 导出（防公式注入）
- **数据流向拓扑**：客户端 → 网关 → 后端集群；边宽=请求量、颜色=耗时（绿快红慢），点击后端节点下钻日志
- **配置管理**：后端节点增删改、健康状态实时展示、负载均衡策略切换，保存立即生效且重启保留

---

## 2. 快速开始

### 环境要求
- Go 1.27+
- Node.js 18+（仅构建前端需要，产物已提交至 `web/dist` 时可跳过）

### 构建与运行

```bash
# 1. （可选）构建前端——web/dist 已存在且未改动前端时可跳过
cd web/frontend
npm install
npm run build          # 产物输出到 web/dist

# 2. 编译后端（前端通过 go:embed 嵌入，单二进制交付）
cd ../..
go build -o sentinel ./cmd/sentinel

# 3. 准备配置
cp config.yaml config.local.yaml   # 或直接使用 config.yaml

# 4. 启动
./sentinel
```

启动日志示例：

```
2026/08/23 20:24:49 配置文件路径: .../config.yaml
2026/08/23 20:24:49 配置加载完成：监听 :8080，后端数=1，策略=round_robin，代理Token数=2
2026/08/23 20:24:49 数据库文件路径: .../data/sentinel.db
2026/08/23 20:24:49 Proxy Sentinel 已启动，监听 :8080
```

浏览器访问 `http://localhost:8080`，使用 `config.yaml` 中的管理员账号登录（首次启动自动创建）。

### 调用代理接口

```bash
curl -H "Authorization: Bearer dev-token-123" "http://localhost:8080/proxy/get?foo=bar"
```

无 Token 或 Token 无效返回 401。

---

## 3. 配置说明

配置优先级：**环境变量 > .env.local / .env > config.yaml**。环境变量映射规则：大写 + 下划线，如 `server.port` → `SERVER_PORT`。

```yaml
server:
  port: "8080"                 # 监听端口

backends:                      # 后端目标列表（可多个，负载均衡）
  - https://httpbin.org

balancer:
  strategy: round_robin        # round_robin | random

database:
  path: "./data/sentinel.db"   # SQLite 数据库文件路径

proxy:
  timeout_seconds: 30          # 连接/读取超时
  max_body_bytes: 10485760     # 最大请求体 10MB
  trust_forwarded_headers: false # 仅前置可信反代时开启

auth:
  admin_username: admin        # 管理员（首次启动自动创建）
  admin_password: "admin123"   # ⚠ 生产环境务必更换
  jwt_secret: "..."            # ⚠ 生产环境务必更换
  proxy_tokens:                # 代理接口 Bearer Token（首次启动写入数据库，哈希存储）
    - "dev-token-123"

log:
  level: debug                 # debug | info | warn | error
  sample_rate: 1.0             # 1.0=全量记录，0.1=采样 10%
  retention_days: 30           # 日志保留天数（自动清理）
  mask_sensitive: true         # 敏感字段脱敏
  body_max_bytes: 65536        # 日志记录的请求/响应体截断上限

# SECURE_COOKIE=true           # 生产 HTTPS 环境设置（环境变量）
```

> 说明：通过 `/settings` 页面修改的后端列表与策略会持久化到数据库，重启后**优先于** config.yaml 生效。

---

## 4. API 概览

| 方法 | 路径 | 功能 | 认证 |
|:--|:--|:--|:--|
| `POST` | `/api/auth/login` | 登录（防暴力破解：5 次失败锁 IP 15 分钟） | ❌ |
| `POST` | `/api/auth/logout` | 登出 | ✅ |
| `GET` | `/api/auth/me` | 当前会话 | ✅ |
| `GET` | `/api/stats/realtime` | 实时指标（QPS、错误率等） | ✅ |
| `GET` | `/api/stats/trend` | 趋势数据（`window=1h/24h/7d`） | ✅ |
| `GET` | `/api/stats/flow` | 数据流向拓扑数据 | ✅ |
| `GET` | `/api/stats/clients` | 客户端分布（`by=ip/ua`） | ✅ |
| `GET` | `/api/logs` | 分页查询日志（多条件筛选） | ✅ |
| `GET` | `/api/logs/:id` | 单条日志详情 | ✅ |
| `GET` | `/api/logs/stream` | SSE 实时日志流 | ✅ |
| `GET` | `/api/logs/export` | 导出筛选结果 CSV | ✅ |
| `GET` | `/api/settings` | 读取运行时配置 | ✅ |
| `PUT` | `/api/settings/backends` | 更新后端列表/策略（立即生效+持久化） | ✅ |
| `ANY` | `/proxy/*` | 反向代理转发 | ✅ Bearer Token |
| `GET` | `/health` | 健康检查 | ❌ |

---

## 5. 目录结构

```
proxy-sentinel/
├── cmd/sentinel/main.go        # 主入口：装配、启动、优雅退出、保留期清理
├── internal/
│   ├── proxy/
│   │   ├── handler.go          # 反向代理核心（流式转发、日志捕获）
│   │   └── balancer.go         # 动态负载均衡 + 健康检查
│   ├── auth/
│   │   ├── proxy_auth.go       # Bearer Token 中间件（哈希校验）
│   │   ├── web_auth.go         # JWT Cookie 中间件
│   │   ├── jwt.go              # JWT 签发/校验
│   │   └── password.go         # bcrypt
│   ├── logger/
│   │   ├── db.go               # 异步批量写入 + SSE 广播
│   │   └── models.go           # 数据模型 + 脱敏
│   ├── stats/                  # 实时/趋势/流向统计服务
│   ├── storage/                # SQLite（建表、日志、认证、设置、统计查询）
│   ├── api/                    # HTTP 路由与 handler
│   └── config/                 # 配置加载（yaml + dotenv + 环境变量）
├── web/
│   ├── embed.go                # go:embed 嵌入前端产物（单二进制交付）
│   ├── dist/                   # 前端构建产物（已提交）
│   └── frontend/               # React 18 + Vite + TS + Tailwind + ECharts 源码
├── scripts/init_admin.go       # 手动重置管理员密码（可选）
├── Dockerfile
├── config.yaml
└── go.mod
```

---

## 6. 部署

### Docker

```bash
docker build -t proxy-sentinel .
docker run -d --name sentinel \
  -p 8080:8080 \
  -v $(pwd)/data:/app/data \
  -e ADMIN_PASSWORD=your-strong-password \
  -e JWT_SECRET=your-long-random-secret \
  proxy-sentinel
```

### VPS 直接部署

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o sentinel ./cmd/sentinel
scp sentinel root@your-server:/usr/local/bin/
# 配置 Systemd 服务后运行
```

> 前端产物通过 `go:embed` 嵌入二进制，服务器上无需 Node.js 与前端文件。

---

## 7. 安全特性

- Token 哈希存储（SHA-256），支持多 Token 与历史明文自动迁移
- bcrypt（成本因子 10）密码哈希；登录失败比对耗时恒定，消除用户名枚举时序差
- JWT 存于 HttpOnly + SameSite=Lax Cookie；HTTPS 环境设 `SECURE_COOKIE=true`
- 登录防暴力破解：IP 级限流锁定
- 日志敏感字段脱敏、CSV 公式注入防护、请求体大小双层限制（代理层 + 日志层）
- 全参数化 SQL（无注入）、代理头伪造防护（`trust_forwarded_headers` 默认关闭）
- 操作审计：登录成功/失败、登出、配置变更落库 `audit_logs`

---

## 8. 验收状态（PRD §11）

- [x] 代理正确转发请求并返回响应
- [x] 请求/响应完整记录（含 Headers 和 Body）
- [x] `/proxy/*` 无有效 Token 返回 401
- [x] 未登录访问受保护路由跳转 `/login`
- [x] 登录/登出/JWT Cookie 正常
- [x] Dashboard 卡片 ≤ 5s 延迟刷新
- [x] 趋势图与分布图数据正确
- [x] 数据流向拓扑节点/边正常
- [x] 日志筛选、分页、详情展开
- [x] 多后端负载均衡（轮询/随机可切换）
- [ ] 单机压测 QPS ≥ 2000、内存 < 300MB（待实测）

## 9. 未实现项（V1.1 计划，均为原 P2）

- 路径重写规则（`/api/v1/*` → `/v2/*`）
- WebSocket 透传
- JSON/PDF 格式导出（当前仅 CSV）
- 性能压测报告
