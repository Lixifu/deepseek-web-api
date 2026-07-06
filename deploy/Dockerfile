# ===== Build stage: 后端 =====
FROM golang:1.22-bookworm AS builder

WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o deepseek-api ./cmd/server

# ===== Build stage: 前端 =====
FROM node:20-bookworm AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm install
COPY web/ .
RUN npm run build

# ===== Runtime stage =====
FROM debian:bookworm-slim

# Playwright Chromium 运行依赖
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata \
    libnss3 libnspr4 libdbus-1-3 libatk1.0-0 libatk-bridge2.0-0 libcups2 \
    libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 \
    libgbm1 libpango-1.0-0 libcairo2 libasound2 \
    fonts-noto-c fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /build/deepseek-api .
COPY --from=frontend /web/dist ./web/dist

# 安装 Playwright Chromium 二进制
RUN ./deepseek-api -install-browsers

ENV TZ=Asia/Shanghai
ENV APP_HOST=0.0.0.0
ENV APP_PORT=8000
EXPOSE 8000
CMD ["./deepseek-api"]
