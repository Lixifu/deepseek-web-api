# DeepSeek Web API

将 DeepSeek 网页版对话封装为 OpenAI 兼容 API，并提供浏览器池、共享等待队列、管理后台、运行监控和审计能力。

> 本项目通过浏览器自动化访问第三方网页，仅适合学习与自托管研究。使用前请确认符合 DeepSeek 服务条款及所在地区的法律法规。

## 主要功能

- OpenAI 兼容接口：`/v1/chat/completions`、`/v1/models`，支持流式 SSE。
- 浏览器池等待队列：池繁忙时按 FIFO 排队，可配置容量与超时。
- Redis 共享队列：多实例共享顺序、容量和租约；队列不保存提示词、Cookie 或 API Key。
- 账号热加载：新增账号或更新 `storage_state.json` 后立即生效，无需重启后端。
- Prometheus 与管理台监控：调用量、耗时、成功率、账号健康度、队列长度和 Chromium 内存。
- 多管理员与 RBAC：`superadmin`、`admin`、`viewer` 三种角色，支持改密、重置密码、禁用账号和令牌失效。
- 管理操作审计：记录管理端写操作，支持定时归档、在线/归档查询及 CSV、JSON 导出。
- API Key、调用限流、用量统计和对话记录管理。

## 技术栈

- 后端：Go 1.22、Gin、GORM、playwright-go
- 数据：MySQL 8 / MariaDB、Redis 7
- 前端：Vue 3、TypeScript、Vite、Element Plus、ECharts
- 部署：systemd、Nginx、Docker Compose

## 项目结构

```text
.
├── cmd/server/              后端入口
├── data/storage_states/     DeepSeek 登录态（内容不会提交）
├── deploy/                  systemd、Nginx、数据库和容器配置
├── internal/
│   ├── api/                 OpenAI 兼容 API 与管理 API
│   ├── auth/                JWT
│   ├── config/              环境配置
│   ├── core/                浏览器池、队列、驱动、SSE 与限流
│   ├── maintenance/         审计日志归档
│   ├── middleware/          鉴权、审计、日志与恢复
│   ├── model/               数据模型
│   ├── observability/       Prometheus 指标
│   └── repository/          数据访问
├── scripts/                 登录态采集、数据库初始化和诊断工具
└── web/                     Vue 管理后台
```

## 本地开发

### 1. 准备环境

- Go 1.22+
- Node.js 20+
- MySQL 8 或兼容的 MariaDB
- Redis 7

### 2. 创建配置

```bash
cp .env.example .env
```

至少修改以下配置：

```dotenv
MYSQL_DSN=deepseek:your-password@tcp(localhost:3306)/deepseek_api?charset=utf8mb4&parseTime=true&loc=Local
ADMIN_JWT_SECRET=replace-with-at-least-32-random-characters
ADMIN_DEFAULT_USERNAME=admin
ADMIN_DEFAULT_PASSWORD=replace-with-a-strong-password
```

生产环境的 `ADMIN_JWT_SECRET` 必须至少 32 个字符。`.env`、登录态、证书、日志、备份和本地构建产物均已被 Git 与 Docker 构建上下文排除。

### 3. 初始化数据库

二选一：

```bash
mysql -u root -p < deploy/init.sql
```

```bash
go run ./scripts/init_db
```

服务启动时也会执行 GORM AutoMigrate，但生产升级前仍应先备份数据库。

### 4. 安装 Chromium 并采集登录态

```bash
go run ./cmd/server -install-browsers
go run ./scripts/login_capture -out data/storage_states/account_1.json
```

在弹出的浏览器中完成 DeepSeek 登录，再按终端提示保存登录态。也可以启动服务后在管理后台新增账号并上传登录态。

### 5. 启动后端与前端

```bash
go run ./cmd/server
```

```bash
cd web
npm ci
npm run dev
```

- 管理后台：<http://localhost:5173>
- API：<http://localhost:8000/v1>
- 健康检查：<http://localhost:8000/healthz>
- Prometheus：<http://localhost:8000/metrics>

## 调用 API

先在管理后台创建 API Key：

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Authorization: Bearer sk-dsk-xxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": false
  }'
```

OpenAI SDK 用户只需将 `base_url` 改为 `http://localhost:8000/v1`，并使用管理后台生成的 API Key。

## 关键配置

| 配置 | 默认值 | 说明 |
| --- | ---: | --- |
| `BROWSER_POOL_SIZE` | `4` | 当前实例的浏览器池容量 |
| `BROWSER_QUEUE_MAX_SIZE` | `100` | 最大等待请求数，`0` 表示不允许排队 |
| `BROWSER_QUEUE_TIMEOUT_SECONDS` | `120` | 单个请求最长排队时间 |
| `REDIS_SHARED_QUEUE_ENABLED` | `true`（示例配置） | 启用 Redis 多实例共享队列 |
| `BROWSER_CLUSTER_CONCURRENCY` | `0` | 集群总容量；`0` 时使用当前实例池容量 |
| `BROWSER_QUEUE_LEASE_TTL_SECONDS` | `60` | 异常实例租约回收时间 |
| `AUDIT_ARCHIVE_AFTER_DAYS` | `90` | 在线审计日志转归档的天数 |
| `AUDIT_ARCHIVE_RETENTION_DAYS` | `0` | 归档保留天数；`0` 表示永久保留 |
| `AUDIT_EXPORT_MAX_ROWS` | `10000` | 单次审计导出的最大行数 |
| `RATE_LIMIT_PER_MINUTE` | `60` | 单个 API Key 每分钟限额 |

完整配置及说明见 [.env.example](.env.example)。

### 多实例队列

所有实例必须：

1. 连接同一 Redis；
2. 使用相同的 `REDIS_QUEUE_KEY_PREFIX`；
3. 将 `BROWSER_CLUSTER_CONCURRENCY` 配置为所有实例浏览器容量之和；
4. 为 Redis 启用 AOF，推荐 `appendfsync everysec`。

可参考 [deploy/redis-persistence.conf](deploy/redis-persistence.conf)。实例异常退出后，未释放租约会在 TTL 到期后自动回收；HTTP 连接本身不会跨进程恢复，客户端仍需采用正常的超时重试策略。

## 管理权限与审计

| 角色 | 权限 |
| --- | --- |
| `superadmin` | 全部权限，包括管理员、角色与审计日志管理 |
| `admin` | 账号、登录态和 API Key 管理 |
| `viewer` | 只读查看仪表盘、指标、账号、API Key 和对话记录 |

- 修改或重置密码、修改角色、禁用管理员会提升令牌版本，使旧 JWT 立即失效。
- 所有管理端写请求和审计导出都会记录审计日志，但不会记录密码或请求体。
- 审计任务按配置定时归档；管理后台支持手动归档与 CSV、JSON 导出。

## Prometheus

```yaml
scrape_configs:
  - job_name: deepseek-web-api
    static_configs:
      - targets: ["127.0.0.1:8000"]
```

`/metrics` 包含服务运行状态，生产环境应仅允许 Prometheus 所在内网或本机访问。

## 部署

### systemd + Nginx

```bash
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o deepseek-api ./cmd/server
cd web && npm ci && npm run build && cd ..

sudo cp deploy/deepseek-api.service /etc/systemd/system/
sudo cp deploy/nginx-server.conf /etc/nginx/conf.d/deepseek-web-api.conf
sudo cp deploy/mariadb-local.cnf /etc/my.cnf.d/deepseek-local.cnf
sudo mkdir -p /var/www/deepseek-web-api
sudo cp -a web/dist/. /var/www/deepseek-web-api/
sudo chmod 600 .env data/storage_states/*.json

sudo nginx -t
sudo systemctl daemon-reload
sudo systemctl enable --now mariadb redis nginx deepseek-api
```

`deploy/deepseek-api.service` 中的用户与绝对路径需要和实际服务器一致。Redis 持久化配置应合并到系统 Redis 配置，再重启 Redis。

```bash
./start.sh status
./start.sh logs
```

### Docker Compose

`deploy/docker-compose.yml` 是部署模板。使用前请设置数据库密码、域名和 `deploy/certs/` 下的 TLS 证书，并确认前端静态资源的挂载方式，再执行：

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

## 验证

```bash
go test ./...

cd web
npm ci
npm run build
```

## 安全建议

- 不要提交 `.env`、`storage_state.json`、TLS 私钥、数据库备份或构建二进制。
- 生产环境使用随机强密码和至少 32 字符的 JWT Secret，并限制 `.env` 与登录态权限为 `0600`。
- 仅通过 HTTPS 暴露管理后台与 API；数据库、Redis 和 `/metrics` 不应直接暴露到公网。
- 定期备份数据库和 Redis AOF，并演练恢复流程。
