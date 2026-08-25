# Proxy Sentinel · 代理网关与日志分析平台

**版本**：V1.0
**状态**：已完成（核心功能全部实现并冒烟测试通过）
**目标平台**：独立部署（Linux VPS / Docker / Windows）

独立部署的反向代理网关：拦截全部请求并转发至后端集群，完整记录请求/响应日志，提供可视化看板、日志检索、数据流向拓扑与在线配置管理。前端构建产物嵌入单二进制，零依赖部署。

---

## 1. 功能特性

### 反向代理引擎
- HTTP/HTTPS 全路径转发，保留原始请求方法与 Headers
- 负载均衡：轮询（round_robin）/ 随机（random）/ 加权随机（weighted，按权重比例分流），支持运行时切换并持久化
- 健康检查：30s 周期探测（每后端可配独立探测路径），自动剔除故障节点、恢复后自动上线
- 超时控制（默认 30s）、请求体大小双层限制（`max_body_bytes` 10MB + `max_upload_bytes` 1GB）、流式响应（大文件不缓存）
- **大请求体流式透传**：请求体 ≤ 10MB 读进内存（可记日志 body）；> 10MB 走流式透传（不占内存，日志只记元信息与大小标记），超 `max_upload_bytes` 返回 413；chunked（未知大小）用 `http.MaxBytesReader` 边读边限
- 可选信任 `X-Forwarded-For`（仅前置可信反代时开启）

### 请求链路追踪（X-Request-ID）
- 每个请求生成唯一链路 ID（`req-` + 16 位十六进制），回写响应头 `X-Request-ID`
- 客户端可自带合法 ID（8~128 位可打印 ASCII）实现跨系统串联，非法值自动重新生成
- ID 透传后端请求头，便于全链路对齐；落库并建索引
- 日志页支持按链路 ID 筛选，详情弹窗一键复制 / 查同请求

### 流量治理（灰度发布 + 路径重写）
- **定向分流规则**：按 Header / Cookie / 路径前缀将请求固定路由到指定后端（优先于负载均衡），自上而下第一条命中生效
- **加权灰度**：weighted 策略下按后端权重比例放量（如 95:5），权重 0 = 灰度回退开关（不接流量、节点保留）
- **路径重写**：前缀替换语义（Nginx `proxy_pass` 风格），如 `/api/v1 → /v2`；支持剥离前缀与限定后端生效；段边界匹配（`/v1` 不误伤 `/v10`）
- 典型灰度流程：新节点权重 0 上线 → 定向规则供测试账号验证 → 逐步放量 → 异常时权重调 0 秒级回退
- 所有规则保存即热生效（原子替换，请求路径无锁）、重启保留、变更记审计

### IP 黑白名单
- 双名单模型：黑名单绝对优先（命中即拒，白名单救不回），白名单放行，都未命中走默认动作（放行/拒绝可选）
- 支持精确 IP 与 CIDR 网段（IPv4/IPv6），IPv4-mapped IPv6 自动归一化
- 判定使用 TCP 直连 IP，不读 `X-Forwarded-For`（客户端无法伪造绕过）
- 挂在认证之前，被拒请求不消耗下游资源；防呆：默认拒绝 + 空白名单拒绝保存

### 后端健康监控
- 健康检查探测结果落库（`backend_health_logs`）：健康状态、探测 RTT、状态码、失败原因，随日志保留期自动清理
- 每后端可配独立探测路径（默认 `/`），探活与业务路径解耦
- 独立监控页：探测 RTT 趋势折线 + 不健康时段红色色带、窗口可用率（uptime%）、真实流量/5xx 错误率双图
- 1h / 24h / 7d 窗口（桶宽自适应 1min/10min/30min），30s 自动刷新
- 告警联动：窗口内平均探测延迟超阈值推送"后端延迟过高"（节点存活但缓慢的提前预警）

### 日志系统
- 完整记录：方法、路径、Headers、请求/响应体、状态码、耗时、客户端 IP/UA/Referer、命中的后端、链路 ID
- 异步批量落盘（满 100 条或每 5s 单事务提交），不阻塞请求响应
- **队列容量上限**（`queue_capacity` 默认 10000，0=不限制）：DB 写入慢导致队列堆积时丢弃最旧日志并输出降级告警，防止 OOM
- 敏感字段自动脱敏（`Authorization`、`Cookie`、`Password` 等）
- 采样率可配置（高并发下降采样）、保留天数自动清理（默认 30 天）

### 双认证体系
| 维度 | 代理接口 `/proxy/*` | 可视化页面 `/api/*` |
|:--|:--|:--|
| 认证方式 | Bearer Token（哈希存储） | JWT（HttpOnly Cookie，24h 过期） |
| 适用对象 | 后端服务（机器） | 管理员（浏览器） |
| 存储 | `proxy_tokens` 表，支持多 Token | `users` 表（bcrypt） |

### 告警通知（钉钉机器人）
- 内置告警引擎：错误率（5xx 占比 + 最小样本量防误报）/ 后端宕机与恢复 / 后端探测延迟三类规则
- 静默期控制（同一告警 N 分钟内不重复），恢复通知不受静默期约束
- 未配置 Webhook 时引擎空转（仅告警日志，不影响代理），配置后即恢复推送
- 规则与 Webhook 热更新，支持一键测试推送

### Token 管理
- 代理 Token 可视化管理：新增（自动生成 `sk-` + 128bit 熵或自定义）、重命名、独立限流值（RPM，0=跟随全局）
- **可选过期时间**（GitHub 风格）：永不过期 / 7 / 30 / 90 天 / 自定义天数；过期 Token 标记作废不删除（保留审计痕迹），不续期（强制轮换更安全）
- 列表项状态点（与后端健康点同款）：绿色=正常可用、黄色=即将过期（≤7 天）、红色=已作废
- 明文 Token 仅创建时返回一次；支持吊销，变更记审计

### 可视化看板（React 18 + ECharts）
- **仪表盘**：QPS / 错误率 / 平均耗时 / 今日请求 4 卡片（5s 轮询），请求量与错误趋势、P50/P90/P99 耗时曲线、状态码饼图、热点路径 Top10、客户端分布（按 IP/UA）
- **日志查询**：多条件筛选（时间/状态码/方法/路径/耗时/关键词/后端/链路 ID）、分页、详情弹窗（链路 ID 复制/查同请求）、SSE 实时流（类 tail -f）、CSV 导出（防公式注入）
- **数据流向拓扑**：客户端 → 网关 → 后端集群；边宽=请求量、颜色=耗时（绿快红慢），点击后端节点下钻日志
- **后端监控**：每后端卡片（健康徽标/可用率/最近延迟/流量摘要）+ 探测 RTT 折线（不健康时段红色色带）+ 真实流量与 5xx 双图
- **Token 管理**：代理 Token 增删改、独立限流配置、可选过期时间与状态徽标（已作废/即将过期/正常）
- **配置管理**：后端节点增删改（权重/探测路径）、健康状态实时展示、负载策略切换、定向分流与路径重写规则编辑、IP 黑白名单、告警配置——保存立即生效且重启保留

---

## 2. 快速开始

### 环境要求
- Go 1.27+
- Node.js 18+（构建前端需要；`web/dist` 不入库，clone 后必须先构建前端才能编译后端）

### 构建与运行

```bash
# 1. 构建前端（web/dist 不入库，clone 后必须执行一次；之后改了前端代码再执行）
cd web/frontend
npm install
npm run build          # 产物输出到 web/dist（被 .gitignore 忽略，不入库）

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

backends:                      # 后端目标列表（可多个，负载均衡；初始值，UI 保存后由数据库接管）
  - url: https://httpbin.org
    weight: 1                  # 仅 weighted 策略生效（0~100）
    # health_path: /healthz    # 健康检查探测路径（可选，默认 /）

balancer:
  strategy: round_robin        # round_robin | random | weighted

database:
  path: "./data/sentinel.db"   # SQLite 数据库文件路径

proxy:
  timeout_seconds: 30          # 连接/读取超时
  max_body_bytes: 10485760     # ≤10MB 读内存记日志；>10MB 流式透传（不读进内存）
  max_upload_bytes: 1073741824 # 流式透传上限 1GB（文件上传）；超限 413；0=不限制
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
  queue_capacity: 10000       # 异步队列上限（满时丢弃最旧+告警）；0=不限制

# SECURE_COOKIE=true           # 生产 HTTPS 环境设置（环境变量）
```

> 说明：通过 `/settings` 页面修改的后端列表、策略、定向分流/路径重写规则会持久化到数据库，重启后**优先于** config.yaml 生效；定向与重写规则仅存数据库（纯运行时管理，保存即热生效）。

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
| `GET` | `/api/stats/backends` | 后端健康监控（探测序列/可用率/流量，`window=1h/24h/7d`） | ✅ |
| `GET` | `/api/logs` | 分页查询日志（多条件筛选） | ✅ |
| `GET` | `/api/logs/:id` | 单条日志详情 | ✅ |
| `GET` | `/api/logs/stream` | SSE 实时日志流 | ✅ |
| `GET` | `/api/logs/export` | 导出筛选结果 CSV | ✅ |
| `GET` | `/api/settings` | 读取运行时配置（含定向规则/路径重写） | ✅ |
| `PUT` | `/api/settings/backends` | 更新后端列表/策略/分流规则/重写规则（立即生效+持久化） | ✅ |
| `GET` | `/api/tokens` | 代理 Token 列表（元数据） | ✅ |
| `POST` | `/api/tokens` | 新增 Token（明文仅返回一次） | ✅ |
| `PUT` | `/api/tokens/:id` | 重命名 / 独立限流值 | ✅ |
| `DELETE` | `/api/tokens/:id` | 吊销 Token | ✅ |
| `GET` | `/api/alert/config` | 读取告警配置 | ✅ |
| `PUT` | `/api/alert/config` | 更新告警规则 / Webhook | ✅ |
| `POST` | `/api/alert/test` | 发送测试告警 | ✅ |
| `GET` | `/api/ip-acl` | 读取 IP 黑白名单 | ✅ |
| `PUT` | `/api/ip-acl` | 更新 IP 黑白名单 | ✅ |
| `ANY` | `/proxy/*` | 反向代理转发（IP 名单 → Bearer 认证 → 限流） | ✅ Bearer Token |
| `GET` | `/health` | 健康检查 | ❌ |

---

## 5. 目录结构

```
proxy-sentinel/
├── cmd/sentinel/main.go        # 主入口：装配、启动、优雅退出、保留期清理
├── internal/
│   ├── proxy/
│   │   ├── handler.go          # 反向代理核心（流式转发、X-Request-ID、日志捕获）
│   │   ├── balancer.go         # 动态负载均衡（轮询/随机/加权）+ 健康检查
│   │   ├── rules.go            # 定向分流规则（灰度发布）
│   │   └── rewrite.go          # 路径重写规则（前缀替换）
│   ├── ipacl/                  # IP 黑白名单（双名单、CIDR、原子热更新）
│   ├── alert/                  # 告警引擎（规则评估 + 钉钉推送）
│   ├── auth/
│   │   ├── proxy_auth.go       # Bearer Token 中间件（哈希校验）
│   │   ├── web_auth.go         # JWT Cookie 中间件
│   │   ├── jwt.go              # JWT 签发/校验
│   │   └── password.go         # bcrypt
│   ├── logger/
│   │   ├── db.go               # 异步批量写入 + SSE 广播
│   │   └── models.go           # 数据模型 + 脱敏
│   ├── stats/                  # 实时/趋势/流向统计服务
│   ├── storage/                # SQLite（建表、日志、认证、设置、统计、健康探测记录查询）
│   ├── api/                    # HTTP 路由与 handler
│   └── config/                 # 配置加载（yaml + dotenv + 环境变量）
├── web/
│   ├── embed.go                # go:embed 嵌入前端产物（单二进制交付）
│   ├── dist/                   # 前端构建产物（不入库，npm run build 生成；仅 .gitkeep 占位）
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
- Token 过期作废机制：到期标记不删除（保留审计痕迹）、不续期（强制轮换）
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
- [x] 多后端负载均衡（轮询/随机/加权可切换）
- [x] 请求链路标记（X-Request-ID 生成/复用/透传/日志筛选）
- [x] IP 黑白名单（双名单、CIDR、认证前拦截）
- [x] 灰度发布（定向分流 + 加权放量 + 权重 0 回退）
- [x] 路径重写规则（前缀替换、剥离前缀、限定后端）
- [x] 后端健康监控（探测落库、RTT 趋势、可用率、延迟告警联动）
- [x] Token 过期时间与状态徽标（可选过期、作废标记、强制轮换）
- [ ] 单机压测 QPS ≥ 2000、内存 < 300MB（待实测）

## 9. 未实现项（V1.1 计划，均为原 P2）

- WebSocket 透传
- JSON/PDF 格式导出（当前仅 CSV）
- 性能压测报告
