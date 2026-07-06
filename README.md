# DeepSeek Web → API 代理服务

将 DeepSeek 网页版对话包装成 OpenAI 兼容 API 的服务。详细设计见同目录 `DeepSeek-Web-API-设计文档.md`。

## 技术栈

- 后端：Go 1.22 + Gin + playwright-go + GORM
- 数据库：MySQL 8.0
- 缓存：Redis 7
- 前端：Vue 3 + Vite + Element Plus
- 部署：Docker / Nginx / systemd

## 目录结构

```
deepseek-web-api/
├── cmd/server/            程序入口
├── internal/
│   ├── api/v1/            OpenAI 兼容 API
│   ├── api/admin/         管理后台 API
│   ├── core/              浏览器池/驱动/SSE/编排/限流
│   ├── model/             GORM 模型
│   ├── repository/        数据访问层
│   ├── middleware/        鉴权/日志/恢复
│   ├── auth/              JWT
│   └── config/            配置
├── web/                   Vue3 管理后台
├── scripts/               login_capture / init_db
├── deploy/                Dockerfile / compose / nginx / systemd / init.sql
└── data/storage_states/   登录态文件
```

## 快速开始（开发）

### 1. 准备环境

```bash
# Go 1.22+、Node 20+、MySQL 8、Redis 7
sudo apt install -y mysql-server redis-server
```

### 2. 配置

```bash
cp .env.example .env
# 编辑 .env，填入 MYSQL_DSN、ADMIN_JWT_SECRET、ADMIN_DEFAULT_PASSWORD 等
```

### 3. 安装依赖与浏览器

```bash
go mod tidy
go run ./cmd/server -install-browsers   # 安装 Playwright Chromium
```

### 4. 初始化数据库

```bash
# 方式一：直接用 SQL 脚本
mysql -u root -p < deploy/init.sql
# 方式二：用脚本（AutoMigrate + 种子管理员）
go run ./scripts/init_db
```

### 5. 获取 DeepSeek 登录态

```bash
go run ./scripts/login_capture -out data/storage_states/account_1.json
# 弹出的浏览器中手动登录 DeepSeek，回车后导出 storage_state
```

### 6. 启动后端

```bash
go run ./cmd/server
# 服务监听 :8000
```

> 首次启动需在管理后台或 DB 中创建账号并指向 storage_state 文件路径，然后重启服务使其加入浏览器池。

### 7. 启动前端

```bash
cd web && npm install && npm run dev
# 前端开发服务器 :5173，已配置代理转发到 :8000
```

打开 http://localhost:5173 ，用 `.env` 中的管理员账号登录。

## 使用 API

在管理后台「API Key」页面生成一个 Key，然后像调用 OpenAI 一样调用：

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Authorization: Bearer sk-dsk-xxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role":"user","content":"你好"}],
    "stream": false
  }'
```

OpenAI SDK 直接替换 `base_url` 即可。

## 部署

### Docker Compose

```bash
cd deploy
docker compose up -d --build
```

### systemd（非 Docker）

```bash
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o deepseek-api ./cmd/server
sudo cp deepseek-api /opt/deepseek-api/
sudo cp deploy/deepseek-api.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now deepseek-api
```

## 关键说明

- **登录态**：每个 DeepSeek 账号对应一个 `storage_state.json`，过期需在管理后台重新上传。服务启动时加载所有 `active` 账号到浏览器池。
- **并发**：浏览器池大小 = 最大并发；同一账号同时只处理一个对话。多账号轮询提高吞吐。
- **流式**：通过 DOM 轮询提取增量文本，转写为 OpenAI SSE 格式。DeepSeek 前端改版时需在 `internal/core/selectors.go` 校正选择器。
- **合规**：仅供个人学习使用，遵守 DeepSeek 服务条款，勿用于商业转售。
