# Proxy Sentinel · 代理网关与日志分析平台 需求规格说明书（PRD）

**项目名称**：`proxy-sentinel`  
**版本**：V1.0  
**状态**：待开发  
**目标平台**：独立部署（Linux VPS / Docker / Vercel）

---

## 1. 项目背景与目标

### 1.1 痛点分析

- **Vercel 日志限制**：Hobby 计划日志保存时间短、查看窗口小、无结构化查询能力。
- **日志抓取滞后**：现有 LogVault 方案通过定时拉取 API 获取日志，存在延迟且无法实时拦截请求。
- **数据不可视化**：缺少流量趋势、错误率、热点路径等直观数据看板。
- **无法扩展**：无法支持负载均衡、灰度发布、请求/响应体分析等高级功能。
- **认证隔离需求**：代理接口（服务间调用）与可视化页面（人工访问）需要互相独立的认证体系。

### 1.2 项目目标

构建一个**独立部署的反向代理网关**，具备以下核心能力：

1. **实时请求拦截**：所有请求先经过代理，再转发至目标后端。
2. **完整日志记录**：记录请求方法、路径、Headers、请求体、响应体、状态码、耗时、客户端 IP。
3. **负载均衡**：支持轮询/随机分发，可配置多个后端目标。
4. **可视化看板**：实时 QPS、错误率、趋势图、状态码分布、热点路径。
5. **数据流向拓扑**：直观展示请求从客户端 → 代理 → 后端的完整链路。
6. **双认证体系**：
   - **代理接口**：Bearer Token 认证（机器间调用）
   - **可视化页面**：JWT + HttpOnly Cookie 认证（人工管理员访问）
7. **数据导出**：支持导出为 CSV/JSON/PDF 报告。

---

## 2. 功能模块详情

### 模块一：反向代理引擎（核心）

| 功能                | 描述                           | 优先级 |
|:----------------- |:---------------------------- |:--- |
| **HTTP/HTTPS 转发** | 支持全路径转发，保留原始请求方法和 Headers    | P0  |
| **负载均衡策略**        | 轮询（Round Robin）和随机（Random）   | P0  |
| **健康检查**          | 自动剔除不可用的后端节点                 | P1  |
| **路径重写**          | 支持 `/api/v1/*` → `/v2/*` 等规则 | P2  |
| **超时控制**          | 可配置连接/读取/写入超时时间（默认 30s）      | P1  |
| **请求体大小限制**       | 可配置最大请求体（默认 10MB）            | P1  |
| **WebSocket 支持**  | 透传 WebSocket 连接              | P2  |
| **缓冲/流式响应**       | 支持大文件流式传输，不缓存全部内容            | P1  |

### 模块二：日志记录系统

| 功能         | 描述                                           | 优先级 |
|:---------- |:-------------------------------------------- |:--- |
| **完整请求记录** | 方法、路径、Headers、请求体（截断/完整可配置）                  | P0  |
| **完整响应记录** | 状态码、Headers、响应体（截断/完整可配置）                    | P0  |
| **性能指标**   | 请求耗时（毫秒）、处理开始/结束时间                           | P0  |
| **客户端信息**  | IP 地址、User-Agent、Referer                     | P0  |
| **异步写入**   | 日志写入不阻塞请求响应                                  | P0  |
| **批量落盘**   | 积累 100 条或每 5 秒批量写入一次                         | P1  |
| **敏感信息脱敏** | 自动屏蔽 `Authorization`、`Cookie`、`Password` 等字段 | P1  |
| **采样记录**   | 高并发时可配置采样率（如 10%）                            | P2  |

### 模块三：数据存储层

| 组件          | 方案                       | 说明                        | 优先级 |
|:----------- |:------------------------ |:------------------------- |:--- |
| **主数据库**    | SQLite（生产可切换 PostgreSQL） | 轻量、零配置、单文件                | P0  |
| **日志表**     | `proxy_logs`             | 存储所有请求日志                  | P0  |
| **用户表**     | `users`                  | 存储可视化页面管理员账号（bcrypt 加密）   | P0  |
| **Token 表** | `proxy_tokens`           | 存储代理接口调用 Token（支持多 Token） | P0  |
| **归档机制**    | 按天分表或自动删除过期数据（默认保留 30 天） | P1                        |     |
| **数据备份**    | 支持导出为 JSON/CSV           | P2                        |     |

### 模块四：认证与安全（双认证体系）

#### 4.1 代理接口认证（服务间调用）

| 功能           | 描述                                          | 优先级 |
|:------------ |:------------------------------------------- |:--- |
| **认证方式**     | Bearer Token（静态 API Key）                    | P0  |
| **Token 存储** | 数据库 `proxy_tokens` 表，支持多 Token 并存           | P0  |
| **Token 管理** | 支持通过配置文件或环境变量初始化，暂不支持动态增删（V1）               | P1  |
| **认证位置**     | HTTP Header `Authorization: Bearer <token>` | P0  |
| **认证范围**     | 仅 `/proxy/*` 路由需要验证                         | P0  |

#### 4.2 可视化页面认证（人工访问）

| 功能        | 描述                               | 优先级 |
|:--------- |:-------------------------------- |:--- |
| **认证方式**  | 用户名 + 密码 → JWT → HttpOnly Cookie | P0  |
| **密码存储**  | bcrypt 加密，成本因子 10                | P0  |
| **会话管理**  | JWT 存储于 HttpOnly Cookie，24 小时过期  | P0  |
| **登录页面**  | 独立登录页面 `/login`                  | P0  |
| **登出功能**  | 清除 Cookie                        | P0  |
| **认证范围**  | 所有 `/dashboard/*` 和 `/api/*` 路由  | P0  |
| **防暴力破解** | 5 次失败后锁 IP 15 分钟（可配置）            | P1  |
| **操作审计**  | 记录登录成功/失败事件（存 `audit_logs` 表）    | P2  |

#### 4.3 认证隔离对比

| 维度        | 代理接口 (`/proxy/*`)        | 可视化页面 (`/dashboard/*` + `/api/*`) |
|:--------- |:------------------------ |:--------------------------------- |
| **认证方式**  | Bearer Token（静态 API Key） | JWT（存储在 HttpOnly Cookie）          |
| **凭证位置**  | HTTP Headers             | Cookie                            |
| **适用对象**  | 后端服务（机器）                 | 人工管理员（浏览器）                        |
| **过期时间**  | 永久（除非手动吊销）               | 24 小时自动过期                         |
| **用户名密码** | 不需要                      | 需要（登录时输入）                         |
| **存储位置**  | `proxy_tokens` 表         | `users` 表（bcrypt）                 |
| **中间件**   | `ProxyAuthMiddleware`    | `WebAuthMiddleware`               |

### 模块五：可视化看板（Dashboard）

#### 5.1 实时总览

| 卡片指标       | 说明              |
|:---------- |:--------------- |
| **当前 QPS** | 最近 1 分钟的每秒请求数   |
| **错误率**    | 5xx 状态码占比       |
| **平均耗时**   | 所有请求的平均响应时间（毫秒） |
| **总请求数**   | 今日累计请求数         |

#### 5.2 趋势图（ECharts）

- **请求量趋势**：最近 1 小时/24 小时/7 天的折线图。
- **错误趋势**：叠加在请求量之上的错误数折线。
- **耗时趋势**：P50、P90、P99 分位数曲线。

#### 5.3 分布图

- **状态码分布**：饼图展示 2xx / 4xx / 5xx 占比。
- **热点路径**：Top 10 访问最多的路径（条形图）。
- **客户端分布**：按 IP 或 User-Agent 聚合（地图/饼图）。

#### 5.4 数据流向拓扑（Flow Map）

- 节点：客户端 → 代理网关 → 后端服务（多个）。
- 边：连线宽度表示请求量，颜色表示平均耗时。
- 交互：悬停显示具体数值，点击跳转日志详情。

### 模块六：日志查询与检索

| 功能        | 描述                           | 优先级 |
|:--------- |:---------------------------- |:--- |
| **多条件筛选** | 按时间范围、状态码、路径、方法、耗时范围筛选       | P0  |
| **全文搜索**  | 在请求体/响应体中搜索关键词               | P1  |
| **分页展示**  | 每页 50 条，支持翻页                 | P0  |
| **详情展开**  | 点击某条日志，展开查看完整 Headers 和 Body | P0  |
| **导出功能**  | 将筛选结果导出为 CSV/JSON            | P1  |
| **实时流日志** | 类似 `tail -f`，实时显示新日志（SSE 推送） | P1  |

### 模块七：配置管理

| 功能              | 描述                                           | 优先级 |
|:--------------- |:-------------------------------------------- |:--- |
| **配置文件**        | `config.yaml` 或环境变量                          | P0  |
| **后端目标列表**      | `BACKEND_URLS`：支持多个后端地址                      | P0  |
| **负载均衡策略**      | `BALANCER_STRATEGY`：`round_robin` / `random` | P0  |
| **日志级别**        | `LOG_LEVEL`：debug / info / warn / error      | P1  |
| **采样率**         | `LOG_SAMPLE_RATE`：0.0 ~ 1.0                  | P2  |
| **保留天数**        | `LOG_RETENTION_DAYS`：默认 30 天                 | P1  |
| **JWT Secret**  | 可视化页面 JWT 签名密钥（环境变量）                         | P0  |
| **管理员账号**       | 首次启动自动创建（用户名从配置读取，密码从环境变量读取）                 | P0  |
| **代理 Token 列表** | 初始化时从配置文件或环境变量加载                             | P0  |

---

## 3. 数据库设计（SQLite 版本）

### 表 1：`proxy_logs`（日志记录表）

```sql
CREATE TABLE proxy_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  method TEXT NOT NULL,
  path TEXT NOT NULL,
  query TEXT,
  request_headers TEXT,
  request_body TEXT,
  status INTEGER NOT NULL,
  response_headers TEXT,
  response_body TEXT,
  duration INTEGER NOT NULL,
  client_ip TEXT,
  user_agent TEXT,
  referer TEXT,
  backend_url TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_path ON proxy_logs(path);
CREATE INDEX idx_status ON proxy_logs(status);
CREATE INDEX idx_created_at ON proxy_logs(created_at DESC);
```

### 表 2：`users`（可视化页面管理员表）

```sql
CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### 表 3：`proxy_tokens`（代理接口 Token 表）

```sql
CREATE TABLE proxy_tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  token TEXT UNIQUE NOT NULL,
  name TEXT,                    -- Token 备注名称，如 "logvault-prod"
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  last_used_at DATETIME
);

CREATE INDEX idx_token ON proxy_tokens(token);
```

### 表 4：`audit_logs`（操作审计表，可选）

```sql
CREATE TABLE audit_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT,
  action TEXT,
  ip TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

---

## 4. API 接口设计

| 方法     | 路径                    | 功能             | 认证方式                      |
|:------ |:--------------------- |:-------------- |:------------------------- |
| `POST` | `/api/auth/login`     | 可视化页面登录        | ❌                         |
| `POST` | `/api/auth/logout`    | 可视化页面登出        | ✅ WebAuth                 |
| `GET`  | `/api/stats/realtime` | 实时统计（QPS、错误率等） | ✅ WebAuth                 |
| `GET`  | `/api/stats/trend`    | 趋势数据           | ✅ WebAuth                 |
| `GET`  | `/api/stats/flow`     | 数据流向拓扑数据       | ✅ WebAuth                 |
| `GET`  | `/api/logs`           | 分页查询日志列表       | ✅ WebAuth                 |
| `GET`  | `/api/logs/:id`       | 获取单条日志详情       | ✅ WebAuth                 |
| `GET`  | `/api/logs/stream`    | SSE 实时日志流      | ✅ WebAuth                 |
| `GET`  | `/api/export/csv`     | 导出筛选结果为 CSV    | ✅ WebAuth                 |
| `ANY`  | `/proxy/*`            | 反向代理转发         | ✅ ProxyAuth（Bearer Token） |
| `GET`  | `/health`             | 健康检查           | ❌                         |

---

## 5. 前端页面规划

| 页面       | 路由           | 功能                   | 认证        |
|:-------- |:------------ |:-------------------- |:--------- |
| **登录页**  | `/login`     | 用户名 + 密码登录           | ❌         |
| **仪表盘**  | `/dashboard` | 实时看板（卡片 + 趋势图 + 分布图） | ✅ WebAuth |
| **日志查询** | `/logs`      | 筛选 + 表格展示 + 详情弹窗     | ✅ WebAuth |
| **数据流向** | `/flow`      | 拓扑关系图                | ✅ WebAuth |
| **配置管理** | `/settings`  | 后端节点管理（增删改）          | ✅ WebAuth |

---

## 6. 技术架构

| 层级         | 技术选型                                       | 理由                    |
|:---------- |:------------------------------------------ |:--------------------- |
| **编程语言**   | Go 1.21+                                   | 高性能、并发强、部署简单（单二进制）    |
| **Web 框架** | 标准库 `net/http` + 自定义路由                     | 足够轻量，无额外依赖            |
| **反向代理**   | `net/http/httputil.ReverseProxy`           | Go 标准库，稳定可靠           |
| **数据库**    | SQLite（`modernc.org/sqlite`，纯 Go 实现，无 CGO） | 零配置、单文件、写入性能足够        |
| **JWT 库**  | `github.com/golang-jwt/jwt/v5`             | 成熟稳定                  |
| **密码加密**   | `golang.org/x/crypto/bcrypt`               | 行业标准                  |
| **前端框架**   | React 18 + Vite                            | 复用现有技术栈，开发效率高         |
| **UI 组件**  | shadcn/ui + Tailwind CSS                   | 轻量、美观、适合后台管理          |
| **图表库**    | Apache ECharts 5                           | 功能强大、中文文档完善           |
| **实时推送**   | SSE（Server-Sent Events）                    | 比 WebSocket 简单，足够看板场景 |
| **部署方式**   | Docker / Systemd / 直接运行二进制                 | 灵活、跨平台                |

---

## 7. 目录结构

```
proxy-sentinel/
├── cmd/
│   └── sentinel/
│       └── main.go                # 主入口
├── internal/
│   ├── proxy/
│   │   ├── handler.go             # 反向代理核心
│   │   └── balancer.go            # 负载均衡
│   ├── auth/
│   │   ├── proxy_auth.go          # Bearer Token 认证中间件
│   │   ├── web_auth.go            # JWT Cookie 认证中间件
│   │   ├── jwt.go                 # JWT 签发/校验
│   │   └── password.go            # bcrypt 哈希工具
│   ├── logger/
│   │   ├── db.go                  # 日志异步写入数据库
│   │   └── models.go              # 数据模型
│   ├── stats/
│   │   ├── realtime.go            # 实时统计
│   │   └── trend.go               # 趋势数据
│   └── api/
│       ├── auth.go                # 登录/登出接口
│       ├── stats.go               # 统计接口
│       └── logs.go                # 日志查询接口
├── web/                           # 前端 React 项目
│   ├── src/
│   ├── package.json
│   └── vite.config.ts
├── migrations/
│   └── 001_initial_schema.sql     # 建表脚本
├── scripts/
│   └── init_admin.go              # 初始化管理员（可选）
├── config.yaml                    # 配置文件模板
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── .cursorrules                   # Cursor AI 规则
└── README.md
```

---

## 8. 部署方案

### 方式一：Docker（推荐）

```dockerfile
FROM golang:1.21 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o sentinel ./cmd/sentinel

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/sentinel .
COPY --from=builder /app/web/dist ./web/dist
COPY config.yaml .
EXPOSE 8080
CMD ["./sentinel"]
```

### 方式二：直接部署（VPS）

```bash
# 交叉编译
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o sentinel ./cmd/sentinel
# 上传并运行
scp sentinel root@your-server:/usr/local/bin/
# 创建 Systemd 服务
```

---

## 9. 开发路线图

| 阶段          | 时间    | 里程碑                               |
|:----------- |:----- |:--------------------------------- |
| **Phase 1** | 第 1 周 | 基础代理转发 + 日志记录（SQLite）             |
| **Phase 2** | 第 2 周 | 双认证系统（WebAuth + ProxyAuth） + 登录页面 |
| **Phase 3** | 第 3 周 | 日志查询 API + 前端布局                   |
| **Phase 4** | 第 4 周 | 可视化看板（ECharts 集成）+ 实时刷新           |
| **Phase 5** | 第 5 周 | 负载均衡 + 数据流向拓扑 + 导出功能              |
| **Phase 6** | 第 6 周 | 性能优化 + 健康检查 + 部署文档                |

---

## 10. 安全加固点

| 安全措施            | 实现方式                                                                    |
|:--------------- |:----------------------------------------------------------------------- |
| **代理 Token 安全** | 存储在数据库，支持多 Token，可单独吊销                                                  |
| **Web 认证安全**    | bcrypt 加密密码，JWT 存储于 HttpOnly Cookie，24 小时过期 |
| **HTTPS 强制**    | 生产环境启用 `Secure: true` Cookie                                            |
| **防暴力破解**       | 登录接口增加 IP 限流（5 次失败锁 15 分钟）                                              |
| **CSRF 防护**     | HttpOnly Cookie + `SameSite=Lax`                                        |
| **敏感信息脱敏**      | 日志记录时自动屏蔽 `Authorization`、`Cookie`、`Password`                           |
| **环境变量隔离**      | JWT Secret、管理员密码等敏感配置通过环境变量注入                                           |

---

## 11. 验收标准

- [ ] 代理能正确转发请求到后端，并返回正确响应。
- [ ] 所有请求/响应完整记录到数据库（包含 Headers 和 Body）。
- [ ] `/proxy/*` 路由必须携带有效 Bearer Token，否则返回 401。
- [ ] `/dashboard/*` 和 `/api/*` 路由必须登录，否则跳转到 `/login`。
- [ ] 登录页面正常工作，JWT Cookie 设置正确。
- [ ] Dashboard 卡片数据实时更新（≤ 5 秒延迟）。
- [ ] 趋势图和分布图展示正确数据。
- [ ] 数据流向拓扑显示节点和边的连接关系。
- [ ] 日志查询支持按条件筛选、分页、详情展开。
- [ ] 负载均衡可配置多个后端，轮询生效。
- [ ] 单机压测 QPS ≥ 2000，内存 < 300MB。

---
