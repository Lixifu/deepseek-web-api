# DeepSeek Web API

将 DeepSeek 网页版封装为 OpenAI 兼容 API，内置浏览器池、管理后台、监控和审计功能。

> 本项目仅适合学习与自托管研究。使用前请确认符合 DeepSeek 服务条款及所在地区的法律法规。

## 功能

- OpenAI 兼容接口：`/v1/chat/completions`、`/v1/models`，支持流式 SSE。
- 浏览器池繁忙时进入 FIFO 队列，可通过 Redis 支持多实例共享。
- 新增账号或更新登录态后热加载，无需重启服务。
- Prometheus 指标和管理台监控：调用耗时、成功率、账号健康度、队列长度、浏览器内存。
- `superadmin`、`admin`、`viewer` 多管理员权限体系。
- 管理操作审计、定时归档以及 CSV、JSON 导出。

## 环境要求

- Go 1.22+
- Node.js 20+
- MySQL 8 或 MariaDB
- Redis 7

## 快速开始

复制配置文件并修改数据库、JWT 密钥和管理员密码：

```bash
cp .env.example .env
```

初始化数据库并安装 Chromium：

```bash
mysql -u root -p < deploy/init.sql
go run ./cmd/server -install-browsers
```

采集一个 DeepSeek 登录态：

```bash
go run ./scripts/login_capture -out data/storage_states/account_1.json
```

启动后端：

```bash
go run ./cmd/server
```

启动管理后台：

```bash
cd web
npm ci
npm run dev
```

- 管理后台：<http://localhost:5173>
- API：<http://localhost:8000/v1>
- 健康检查：<http://localhost:8000/healthz>
- Prometheus：<http://localhost:8000/metrics>

## 调用示例

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

OpenAI SDK 用户只需把 `base_url` 改为 `http://localhost:8000/v1`。

## 常用配置

| 配置 | 说明 |
| --- | --- |
| `BROWSER_POOL_SIZE` | 当前实例的浏览器容量 |
| `BROWSER_QUEUE_MAX_SIZE` | 最大排队请求数 |
| `BROWSER_QUEUE_TIMEOUT_SECONDS` | 排队超时时间 |
| `REDIS_SHARED_QUEUE_ENABLED` | 是否启用多实例共享队列 |
| `BROWSER_CLUSTER_CONCURRENCY` | 集群总浏览器容量 |
| `AUDIT_ARCHIVE_AFTER_DAYS` | 审计日志归档时间 |
| `RATE_LIMIT_PER_MINUTE` | 单个 API Key 每分钟限额 |

完整配置见 [.env.example](.env.example)。多实例应连接同一 Redis、使用相同队列前缀，并启用 AOF 持久化。

## 权限

- `superadmin`：全部权限，包括管理员和审计管理。
- `admin`：账号、登录态和 API Key 管理。
- `viewer`：只读查看仪表盘、账号、Key 和对话记录。

## 部署

项目提供以下部署配置：

- `deploy/deepseek-api.service`：systemd 服务。
- `deploy/nginx-server.conf`：Nginx 反向代理和前端静态资源。
- `deploy/redis-persistence.conf`：Redis 持久化参考配置。
- `deploy/docker-compose.yml`：Docker Compose 模板。

非 Docker 环境可使用：

```bash
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o deepseek-api ./cmd/server
cd web && npm ci && npm run build && cd ..
./start.sh restart
./start.sh status
```

部署前请按实际服务器修改 systemd 文件中的用户和路径，并备份项目、数据库与 Redis。

## 验证

```bash
go test ./...
go vet ./...
cd web && npm ci && npm run build
```

## 安全提示

- 不要提交 `.env`、登录态、TLS 私钥、数据库备份或构建产物。
- 生产环境使用强密码和至少 32 字符的 `ADMIN_JWT_SECRET`。
- 数据库、Redis 和 `/metrics` 不应直接暴露到公网。
