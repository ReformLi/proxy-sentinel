# Proxy Sentinel · 部署手册

> 适用版本：V1（当前主分支）。
> 阅读顺序建议：**第 1 章必读**（单实例约束直接决定你的部署形态），其余按需查阅。
> 配置字段速查见第 5 章；故障排查见第 12 章。

---

## 目录

- [1. 部署模型：单实例架构（必读）](#1-部署模型单实例架构必读)
- [2. 环境要求](#2-环境要求)
- [3. 构建产物](#3-构建产物)
- [4. 部署方式](#4-部署方式)
- [5. 配置参考](#5-配置参考)
- [6. 数据库选型与运维](#6-数据库选型与运维)
- [7. 反向代理与 HTTPS（Nginx）](#7-反向代理与-httpsnginx)
- [8. 首次启动检查清单](#8-首次启动检查清单)
- [9. 升级与回滚](#9-升级与回滚)
- [10. 数据备份与恢复](#10-数据备份与恢复)
- [11. 监控与告警](#11-监控与告警)
- [12. 安全加固清单](#12-安全加固清单)
- [13. 故障排查](#13-故障排查)
- [14. 卸载](#14-卸载)

---

## 1. 部署模型：单实例架构（必读）

### 1.1 结论

**Proxy Sentinel V1 设计为单进程单实例部署。** 在同一份数据上运行多个副本（负载均衡双活、`docker compose --scale=2` 等）**不受支持**，会导致限流失效、防爆破被绕过、SQLite 锁死等具体问题（见 1.3）。

这是设计取舍而非缺陷：网关本身是轻量组件（实测 4443 QPS / 峰值内存 59MB，见 [BENCHMARK_REPORT.md](BENCHMARK_REPORT.md)），单实例 + 进程自动重启即可覆盖绝大多数可用性需求。

### 1.2 哪些状态在进程内存里

| 状态 | 位置 | 说明 |
|---|---|---|
| **限流令牌桶** | 进程内存 | 按 Token ID 分桶，`rate_limit.default_rpm` / Token 独立配额都记在这里 |
| **登录失败锁定** | 进程内存 | 每个源 IP 独立计数：连续失败 5 次锁定该 IP 15 分钟 |
| **用户存在性 / 令牌版本缓存** | 进程内存 | 30 秒 TTL；改密码、删用户时本实例主动清除 |
| **实时统计** | 进程内存 | 当前 QPS / 并发 / 延迟分位等，重启清零（历史趋势在数据库） |
| **后端健康标记** | 进程内存 | 健康检查由本进程执行，上下线状态不共享 |

数据库（SQLite/MySQL/PG）只持久化：日志、用户、Token、审计、后端配置、告警规则。**内存态不落库、不共享。**

### 1.3 多实例会发生什么

| 场景 | 后果 |
|---|---|
| 起了 2 个副本做双活 | 限流配额 ×2（`default_rpm=6000` 实际放行 12000/分钟） |
| 攻击者爆破登录 | 每实例独立计数 5 次，轮询请求可绕过锁定（实际需 5×N 次才全锁） |
| 改密码 / 删用户 | 另一实例的缓存最多滞后 30 秒才生效（最终一致，但不是立即） |
| **SQLite + 多进程写同一 db 文件** | **绝对禁止**：写入互相冲突，触发 `busy_timeout`（5 秒）排队，吞吐骤降甚至整库锁死 |
| 看板数据 | 实时统计取决于请求命中哪个实例，两个实例数字对不上 |

### 1.4 需要高可用怎么办

按可用性需求从低到高：

1. **单实例 + 自动重启（推荐，覆盖 99% 场景）**
   - systemd `Restart=always` 或 Docker `restart: unless-stopped`
   - 进程崩溃 → 3 秒内自动拉起，恢复窗口 = 重启耗时（秒级）
   - 前提：配置文件与数据库放在宿主机（容器挂卷），重启不丢状态之外的东西

2. **冷备**
   - 备机装好二进制与配置，平时不接流量
   - 故障时切换 DNS / 上游负载均衡指向备机
   - 注意 SQLite 场景备机没有实时数据，切过去是"空历史 + 当前配置"；要无缝请用 MySQL/PG（数据在共享库）

3. **真正的多活**
   - V1.1 计划：限流与登录锁定迁移到 Redis 共享存储（见 README「未实现项」）
   - 在此之前**不要**自行多实例化

---

## 2. 环境要求

| 项 | 要求 |
|---|---|
| 操作系统 | Linux（推荐）/ Windows / macOS；Docker 方式则宿主机任意 |
| CPU / 内存 | 1 核 / 128MB 起步即可运行；按压测基准，处理 4000+ QPS 峰值内存约 60MB |
| 磁盘 | 数据库 + 日志保留量；SQLite 单机典型 < 1GB（默认 30 天日志保留） |
| 运行时依赖 | **无**。前端通过 `go:embed` 打进二进制，服务器不需要 Node.js |
| 端口 | 默认监听 `:8080`（管理面板与代理网关共用一个端口） |

---

## 3. 构建产物

交付物是**单个静态二进制**（约 20MB，内嵌前端）：

```bash
# 1. 构建前端（clone 后必须执行一次；改动前端代码后重新执行）
cd web/frontend && npm install && npm run build && cd ../..

# 2. 编译后端（CGO_ENABLED=0 → 纯静态，任意 Linux 直接跑）
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o sentinel ./cmd/sentinel

# Windows 开发机
go build -o sentinel.exe ./cmd/sentinel
```

交叉编译目标架构按需替换 `GOARCH`（arm64 等）。产物 `sentinel` 拷到服务器即可，无其它文件依赖。

---

## 4. 部署方式

### 4.1 Linux + systemd（生产推荐）

```bash
# 1. 安放文件
sudo useradd -r -s /usr/sbin/nologin sentinel
sudo mkdir -p /etc/sentinel /var/lib/sentinel
sudo cp sentinel /usr/local/bin/
sudo cp config.example.yaml /etc/sentinel/config.yaml
sudo chown -R sentinel:sentinel /etc/sentinel /var/lib/sentinel

# 2. 编辑配置（至少改 admin_password / jwt_secret / proxy_tokens / backends 四项）
sudo vi /etc/sentinel/config.yaml
#   database.path 建议写绝对路径：/var/lib/sentinel/sentinel.db
```

创建 `/etc/systemd/system/sentinel.service`：

```ini
[Unit]
Description=Proxy Sentinel - reverse proxy & log analytics
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=sentinel
Group=sentinel
WorkingDirectory=/etc/sentinel
ExecStart=/usr/local/bin/sentinel -c /etc/sentinel/config.yaml
Restart=always
RestartSec=3
# 高并发场景适当调大文件描述符上限
LimitNOFILE=65535

# 基础加固
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/sentinel

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now sentinel
sudo systemctl status sentinel          # 应为 active (running)
curl http://127.0.0.1:8080/health       # {"status":"ok"}
journalctl -u sentinel -f               # 跟日志（stdout 进 journald）
```

日志即 journald，无需额外 logrotate；如需落盘用 `journalctl -u sentinel -o cat > /var/log/sentinel.log` 或配置 journald 持久化。

### 4.2 Docker Compose（推荐快速）

```bash
# 1. 准备配置（真实 config.yaml 不入库）
cp config.example.yaml config.yaml      # 修改四个 ⚠ 项

# 2. 启动（首次自动构建镜像；敏感项通过环境变量注入，覆盖 config.yaml 同名字段）
ADMIN_PASSWORD=your-strong-password \
JWT_SECRET=$(openssl rand -hex 32) \
BACKEND_URLS=http://10.0.0.5:8000,http://10.0.0.6:8000 \
docker compose up -d

docker compose logs -f sentinel
```

要点（详见 [docker-compose.yml](docker-compose.yml)）：
- `./data:/root/data`：SQLite 数据持久化（配置里 `database.path` 保持相对路径 `./data/sentinel.db` 即落在这里）
- `./config.yaml:/root/config.yaml:ro`：配置外挂只读，改完 `docker compose restart` 生效
- `CONFIG_PATH=/root/config.yaml`：告诉二进制配置位置（也可用 `-c` 覆盖 CMD）
- `restart: unless-stopped`：崩溃自动拉起
- 停止：`docker compose down`（数据保留在宿主机 `./data`）

### 4.3 Docker 手动

```bash
docker build -t proxy-sentinel:latest .

docker run -d --name sentinel \
  -p 8080:8080 \
  -v $(pwd)/data:/root/data \
  -v $(pwd)/config.yaml:/root/config.yaml:ro \
  -e CONFIG_PATH=/root/config.yaml \
  -e ADMIN_PASSWORD=your-strong-password \
  -e JWT_SECRET=$(openssl rand -hex 32) \
  -e SECURE_COOKIE=true \
  --restart unless-stopped \
  proxy-sentinel:latest
```

> 注意：Dockerfile 构建时会 `COPY config.yaml` 进镜像，但**运行时挂载的文件会覆盖镜像内同名文件**——生产请始终挂载外部 config.yaml，避免把真实配置打进镜像层。

### 4.4 Windows（开发 / 内网）

```powershell
go build -o sentinel.exe ./cmd/sentinel
.\sentinel.exe -c config.yaml
```

生产长期运行建议用 [NSSM](https://nssm.cc/) 注册为 Windows 服务，或直接上 Docker Desktop。其余章节（配置、备份、升级）同样适用。

---

## 5. 配置参考

### 5.1 配置加载优先级

```
命令行 -c/--config 指定路径  >  环境变量 CONFIG_PATH  >  ./config.yaml
```

- 相对路径找不到时，会从当前目录**逐级向上**查找同名文件（便于在子目录启动）
- `.env` / `.env.local` 自动加载（同样向上查找），最终优先级：
  **shell 已设环境变量 > .env.local > .env > 配置文件**
- 完整示例：[config.example.yaml](config.example.yaml)、[.env.example](.env.example)

### 5.2 配置文件字段

| 字段 | 默认值 | 说明 |
|---|---|---|
| `server.port` | `8080` | 监听端口（面板 + 代理共用） |
| `backends[]` | 必填 | 上游后端地址列表 |
| `balancer.strategy` | `round_robin` | `round_robin` / `random`（`weighted` 及按后端权重、定向分流、路径重写在「设置」页面配置，持久化到数据库并优先于本文件） |
| `database.driver` | `sqlite` | `sqlite` / `mysql` / `postgres` |
| `database.path` | `./data/sentinel.db` | SQLite 文件路径 |
| `database.dsn` | — | MySQL/PG 连接串（driver 非 sqlite 时生效） |
| `proxy.timeout_seconds` | `30` | 后端连接 + 响应头超时 |
| `proxy.max_body_bytes` | `10485760` | 10MB；小于该值才把 body 记入日志，超出走流式透传 |
| `proxy.max_upload_bytes` | `1073741824` | 1GB 流式上限，超出返回 413；0 = 不限 |
| `proxy.trust_forwarded_headers` | `false` | **仅当 Sentinel 前面有可信反向代理时设 true**，否则客户端可伪造来源 IP |
| `auth.admin_username` | `admin` | 首次启动创建的管理员 |
| `auth.admin_password` | 必填 | ≥8 字符；改后重启即同步生效（所有旧会话失效） |
| `auth.jwt_secret` | 必填 | ≥32 字符随机串；弱值（如 `change-me-please`）会被启动校验直接拒绝 |
| `auth.proxy_tokens[]` | 必填 | 初始 Bearer Token 列表，首次启动写入数据库；之后在「令牌管理」页面维护 |
| `rate_limit.default_rpm` | `0` | 0 = 不限流；单位 次/分钟/Token |
| `alert.check_interval_seconds` | `30` | 告警评估周期（≥5 秒） |
| `alert.dingtalk.webhook_url` | 空 | 钉钉机器人 webhook，空 = 不启用告警 |
| `alert.dingtalk.secret` | 空 | 机器人「加签」密钥 |
| `log.level` | `info` | `debug/info/warn/error` |
| `log.sample_rate` | `1.0` | 落库采样率，如 `0.01` = 1%（会等比缩小看板统计，生产推荐） |
| `log.retention_days` | `30` | proxy_logs 自动清理天数（0 = 不清理） |
| `log.health_retention_days` | `14` | backend_health_logs 保留天数 |
| `log.audit_retention_days` | `180` | audit_logs 保留天数 |
| `log.mask_sensitive` | `true` | 日志脱敏（Authorization/Cookie/密码等） |
| `log.body_max_bytes` | `65536` | 单条日志 body 截断上限 |
| `log.queue_capacity` | `10000` | 异步写库队列，满时丢最旧并告警；0 = 不限 |

### 5.3 环境变量对照

命名规则：配置路径大写 + 下划线。高频项：

| 环境变量 | 对应配置 |
|---|---|
| `SERVER_PORT` | `server.port` |
| `BACKEND_URLS` | `backends`（逗号分隔） |
| `BALANCER_STRATEGY` | `balancer.strategy` |
| `DATABASE_DRIVER` / `DATABASE_PATH` / `DATABASE_DSN` | 数据库三项 |
| `PROXY_TIMEOUT_SECONDS` / `PROXY_MAX_BODY_BYTES` / `PROXY_MAX_UPLOAD_BYTES` / `PROXY_TRUST_FORWARDED_HEADERS` | proxy 四项 |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` / `JWT_SECRET` / `PROXY_TOKENS` | auth 四项 |
| `RATE_LIMIT_DEFAULT_RPM` | `rate_limit.default_rpm` |
| `ALERT_CHECK_INTERVAL_SECONDS` / `ALERT_DINGTALK_WEBHOOK_URL` / `ALERT_DINGTALK_SECRET` | alert 三项 |
| `LOG_LEVEL` / `LOG_SAMPLE_RATE` / `LOG_RETENTION_DAYS` / `LOG_HEALTH_RETENTION_DAYS` / `LOG_AUDIT_RETENTION_DAYS` / `LOG_MASK_SENSITIVE` / `LOG_BODY_MAX_BYTES` / `LOG_QUEUE_CAPACITY` | log 各项 |
| `CONFIG_PATH` | 配置文件路径（非配置项） |
| `SECURE_COOKIE` | Cookie Secure 标记（无对应 yaml 键） |

---

## 6. 数据库选型与运维

| | SQLite（默认） | MySQL | PostgreSQL |
|---|---|---|---|
| 适用 | 单机、中小流量、零运维 | 团队已有 MySQL、需集中备份 | 同左 |
| 连接参数 | `busy_timeout=5000ms`，连接池 ≤10 | 标准 DSN | 标准 DSN |
| 空间回收 | 维护页「VACUUM」按钮 | 自动（InnoDB） | 自动（VACUUM） |
| 多实例前提 | ❌ 禁止 | 数据可共享但限流/锁定仍是进程内存态（见第 1 章） | 同左 |

切换方式：`database.driver` + `database.dsn`，DSN 模板：

```yaml
database:
  driver: "mysql"
  dsn: "user:password@tcp(127.0.0.1:3306)/sentinel?parseTime=true&charset=utf8mb4"
  # driver: "postgres"
  # dsn: "host=127.0.0.1 port=5432 user=sentinel password=xxx dbname=sentinel sslmode=disable"
```

首次启动自动建表 + 增量迁移，无需手动执行 SQL。三库行为一致（清理任务、分页上限、CSV 导出等）。

---

## 7. 反向代理与 HTTPS（Nginx）

Sentinel 自身不终止 TLS。生产标准姿势：Nginx 挂证书 → 转发 Sentinel：

```nginx
server {
    listen 443 ssl http2;
    server_name gateway.example.com;

    ssl_certificate     /etc/nginx/certs/fullchain.pem;
    ssl_certificate_key /etc/nginx/certs/privkey.pem;

    # WebSocket / SSE 升级支持
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;   # 与 proxy.timeout_seconds 匹配或略大
    }
}
server {
    listen 80;
    server_name gateway.example.com;
    return 301 https://$host$request_uri;
}
```

配套两处改动（**必须成对出现**）：

1. `proxy.trust_forwarded_headers: true` —— 让 Sentinel 从 `X-Forwarded-For` 取真实客户端 IP（用于限流统计、登录锁定、日志）。前置是可信 Nginx 时安全；直连暴露时保持 `false` 防伪造。
2. `SECURE_COOKIE=true` —— HTTPS 下启用 Secure Cookie，否则部分浏览器拒发管理面板会话。

---

## 8. 首次启动检查清单

```bash
# 1. 启动后观察日志（确认配置加载、无 ⚠ 告警）
journalctl -u sentinel -n 50        # 或 docker compose logs sentinel

# 2. 健康检查
curl http://127.0.0.1:8080/health   # {"status":"ok"}（无认证，可给 LB 探活用）

# 3. 浏览器打开面板
#    http://<host>:8080  → 302 跳 /dashboard → /login
#    用 auth.admin_username / admin_password 登录

# 4. 面板内操作（建议顺序）
#    a. 用户管理：把 admin 改强密码 / 创建日常 viewer 只读账号
#    b. 令牌管理：创建正式 Token（可设独立 RPM 与过期天数），吊销示例 Token
#    c. 后端与设置：确认后端列表、策略、健康检查路径
#    d. 告警通知：填钉钉 webhook → 点「测试」按钮验证连通
#    e. 数据维护：确认三张表保留天数符合合规要求

# 5. 代理链路验证（用刚创建的 Token）
curl -H "Authorization: Bearer <your-token>" \
     http://<host>:8080/proxy/api/anything
#    期望 200 且响应来自上游后端；/logs 页面应出现这条记录
```

---

## 9. 升级与回滚

### 9.1 升级（单实例，秒级停机）

```bash
# 1. 备份（见第 10 章）
# 2. 停止
sudo systemctl stop sentinel          # 或 docker compose down
# 3. 替换二进制
sudo cp sentinel-new /usr/local/bin/sentinel
# 4. 启动（数据库结构变化由程序自动增量迁移，无需手工 SQL）
sudo systemctl start sentinel
# 5. 验证 /health 与面板
```

升级注意：

- 收到 SIGTERM 后进程会**优雅停机**（处理完在途请求再退出），LB 场景先摘流量再停更稳
- **跨版本升级旧库时，全员会被登出一次需重新登录**——升级迁移会作废旧版签发的 JWT（安全设计，非故障）
- 停机窗口内代理流量中断；如不可接受，用 1.4 节冷备切换

### 9.2 回滚

二进制换回旧版本即可直接启动：数据库新增列均为增量、旧版代码查询不感知，向后兼容。若新版迁移已写入新数据（如新表），回滚后这些数据闲置无害。极少数情况（回滚后行为异常）用第 10 章的备份恢复。

---

## 10. 数据备份与恢复

### 备份

| 数据库 | 方式 |
|---|---|
| SQLite | ① 停机备份（最稳）：`systemctl stop sentinel` → `cp data/sentinel.db backup/sentinel-$(date +%F).db` → 启动。② 在线备份：`sqlite3 data/sentinel.db ".backup backup/sentinel-$(date +%F).db"` |
| MySQL | `mysqldump -u sentinel -p sentinel > sentinel-$(date +%F).sql`（可热备） |
| PostgreSQL | `pg_dump -U sentinel sentinel > sentinel-$(date +%F).sql`（可热备） |

配置与 Token 同步备份：`config.yaml` / `.env`（注意权限 600）。

建议 cron 每日一次，保留 7~30 份。示例：

```cron
0 3 * * * sqlite3 /var/lib/sentinel/sentinel.db ".backup /backup/sentinel-$(date +\%F).db" && find /backup -name 'sentinel-*.db' -mtime +14 -delete
```

### 恢复

```bash
sudo systemctl stop sentinel
cp backup/sentinel-2026-08-26.db /var/lib/sentinel/sentinel.db
sudo systemctl start sentinel      # 结构差异由自动迁移补齐
```

---

## 11. 监控与告警

- **存活探针**：`GET /health`（无认证），供 Nginx upstream check / K8s liveness / 云商健康检查
- **业务告警**：面板「告警通知」配置规则（错误率、P99 延迟、后端宕机等阈值），推送钉钉机器人；`POST /api/alert/test` 可发测试消息
- **容量基线**（单机，Windows 实测，详见 [BENCHMARK_REPORT.md](BENCHMARK_REPORT.md)）：
  - QPS ≈ 4400（并发 200，日志采样 1%）
  - P99 ≈ 56ms，进程内存峰值 ≈ 59MB
  - 内存采样脚本：`scripts/memwatch.ps1`（PowerShell，采样 RSS 与峰值）
- **日志**：stdout 即全部运行日志；SQLite 中的 proxy_logs 也可在面板检索 / 导出 CSV
- 持续超基线（CPU 打满 / 内存持续上涨）时优先检查：`log.sample_rate` 是否为 1.0、`log.queue_capacity` 是否为 0（不限流队列）、上游后端是否拖慢

---

## 12. 安全加固清单

上线前逐项核对：

- [ ] `jwt_secret` 为 ≥32 字符随机串（`openssl rand -hex 32`）；弱值会被启动校验拒绝
- [ ] `admin_password` ≥12 字符强密码；日常操作用 viewer 账号
- [ ] HTTPS 终止（第 7 章）+ `SECURE_COOKIE=true`
- [ ] `proxy.trust_forwarded_headers`：有可信前置代理才 true，直连暴露保持 false
- [ ] 8080 端口最小化暴露：仅 Nginx / 内网可达；`/proxy/*` 可用面板「IP 黑白名单」再收一层
- [ ] 初始示例 Token 全部吊销，改用独立 RPM + 过期时间的新 Token
- [ ] `log.mask_sensitive: true`（默认开）；CSV 导出注意接收方处理
- [ ] 数据库文件 / 备份文件权限 600，归属运行用户
- [ ] 定期备份已配置（第 10 章）
- [ ] 审计日志留存 180 天（默认）；面板「审计日志」仅 admin 可见，定期复查异常登录记录

---

## 13. 故障排查

| 现象 | 原因与处理 |
|---|---|
| 启动即退出，报「JWT Secret 使用了已知的弱默认值」 | 校验拦截弱密钥。换随机强密钥 |
| 启动即退出，报「必须配置至少一个后端 / Token」 | `backends` / `auth.proxy_tokens` 为空。补配置或 `BACKEND_URLS` / `PROXY_TOKENS` 环境变量 |
| 日志显示「未找到配置文件」 | 工作目录不对。systemd 用 `WorkingDirectory`；或显式 `-c /绝对路径/config.yaml` |
| 登录一直 429「IP 已被锁定 15 分钟」 | 触发防爆破锁定（连续 5 次失败/IP）。等 15 分钟或重启进程（内存态清空） |
| 代理返回 429 + `Retry-After` | 该 Token 令牌桶配额用尽，头里是精确恢复秒数；调大 Token 的 RPM 或 `default_rpm` |
| 代理返回 502 `backend unavailable` | 上游不可达/超时（内部细节在服务端日志）。查后端存活与 `timeout_seconds` |
| 代理返回 502 `no available backend` | 全部后端被健康检查下线。面板「后端监控」看健康状态，等探活恢复或修后端 |
| 代理 401 | Token 无效/已吊销/已过期；吊销即时生效（校验走数据库） |
| SQLite 报 `database is locked` | 有第二个进程在写同一个库文件（**违反单实例约束**），或外部工具占用。排查进程：`fuser sentinel.db` |
| 升级后所有人被登出 | 预期行为：迁移作废旧版 JWT，重新登录即可 |
| 面板统计数字比实际小 | `log.sample_rate < 1.0` 导致按比例采样；看板趋势同比例缩小 |
| 客户端断开导致请求中断 | 正常：客户端取消不记为后端故障，日志状态码 499 |
| 容器时区不对 | 镜像已设 `TZ=Asia/Shanghai`；自定义则 `-e TZ=<zone>` |
| 改了 config.yaml 不生效 | ① 后端列表等运行时设置以数据库为准（面板改过就优先）；② 密码类改动需重启；③ 确认没被环境变量覆盖 |

---

## 14. 卸载

```bash
# systemd
sudo systemctl disable --now sentinel
sudo rm /usr/local/bin/sentinel /etc/systemd/system/sentinel.service
sudo rm -rf /etc/sentinel /var/lib/sentinel     # 配置与数据库（含全部日志/审计，不可恢复）

# Docker
docker compose down
docker rmi proxy-sentinel:latest
rm -rf ./data ./config.yaml
```

---

*文档与代码同步维护：改动部署相关行为（配置项、环境变量、端口、迁移逻辑）时请同步更新本手册。*
