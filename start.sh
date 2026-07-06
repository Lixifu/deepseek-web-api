#!/usr/bin/env bash
# DeepSeek Web API 一键启动脚本（服务器重启后使用）
# 用法: bash start.sh        前台启动并打印状态
#       bash start.sh stop   停止所有服务
#       bash start.sh status 查看运行状态
#       bash start.sh restart 重启所有服务
set -u

# ============== 路径与配置 ==============
PROJECT_DIR="/home/admin/myworkplace/deepseekapi/deepseek-web-api"
WEB_DIR="$PROJECT_DIR/web"
BACKEND_BIN="$PROJECT_DIR/deepseek-api"
LOG_DIR="$PROJECT_DIR/logs"
BACKEND_LOG="$LOG_DIR/backend.log"
FRONTEND_LOG="$LOG_DIR/frontend.log"
BACKEND_PID="$LOG_DIR/backend.pid"
FRONTEND_PID="$LOG_DIR/frontend.pid"

BACKEND_PORT=8000
FRONTEND_PORT=5173

mkdir -p "$LOG_DIR"

# ============== 颜色输出 ==============
GREEN='\033[0;32m'; YELLOW='\033[0;33m'; RED='\033[0;31m'; NC='\033[0m'
log()  { echo -e "${GREEN}[✓]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[✗]${NC} $*"; }
step() { echo -e "\n${YELLOW}==> $*${NC}"; }

# ============== 通用函数 ==============
wait_port() {
  local port=$1 timeout=${2:-30} i=0
  while ! ss -tlnp 2>/dev/null | grep -q ":$port "; do
    sleep 1; i=$((i+1))
    [ $i -ge $timeout ] && return 1
  done
  return 0
}

is_running_pidfile() {
  local pf=$1
  [ -f "$pf" ] || return 1
  local pid; pid=$(cat "$pf" 2>/dev/null)
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

# ============== 启用 Swap ==============
enable_swap() {
  step "启用 Swap"
  for sf in /swapfile /swapfile2; do
    if [ -f "$sf" ] && ! swapon --show | grep -q "$sf"; then
      swapon "$sf" 2>/dev/null && log "已启用 $sf" || warn "$sf 启用失败（可能已启用）"
    elif [ -f "$sf" ]; then
      log "$sf 已启用"
    fi
  done
  free -h | head -3
}

# ============== 启动数据库与缓存 ==============
start_deps() {
  step "启动 MariaDB / Redis"
  systemctl start mariadb 2>/dev/null || systemctl start mysqld 2>/dev/null || warn "MySQL/MariaDB 启动失败"
  systemctl start redis   2>/dev/null || systemctl start redis-server 2>/dev/null || warn "Redis 启动失败"

  log "等待 MariaDB 就绪..."
  if wait_port 3306 30; then log "MariaDB 已就绪 (3306)"; else err "MariaDB 启动超时"; fi
  log "等待 Redis 就绪..."
  if wait_port 6379 10; then log "Redis 已就绪 (6379)"; else err "Redis 启动超时"; fi
}

# ============== 启动后端 ==============
start_backend() {
  step "启动后端 (Go :$BACKEND_PORT)"
  if is_running_pidfile "$BACKEND_PID"; then
    warn "后端已在运行 (pid=$(cat $BACKEND_PID))，跳过"
    return 0
  fi
  if ss -tlnp 2>/dev/null | grep -q ":$BACKEND_PORT "; then
    warn "$BACKEND_PORT 端口已被占用，请先停止旧进程"
    return 1
  fi
  [ -x "$BACKEND_BIN" ] || { err "后端二进制不存在: $BACKEND_BIN"; return 1; }

  cd "$PROJECT_DIR"
  nohup "$BACKEND_BIN" >>"$BACKEND_LOG" 2>&1 &
  echo $! >"$BACKEND_PID"
  disown

  log "等待后端监听 $BACKEND_PORT ..."
  if wait_port "$BACKEND_PORT" 30; then
    log "后端已启动 (pid=$(cat $BACKEND_PID))"
  else
    err "后端启动超时，查看日志: $BACKEND_LOG"
    tail -20 "$BACKEND_LOG"
    return 1
  fi
}

# ============== 启动前端 ==============
start_frontend() {
  step "启动前端 (Vite :$FRONTEND_PORT)"
  if is_running_pidfile "$FRONTEND_PID"; then
    warn "前端已在运行 (pid=$(cat $FRONTEND_PID))，跳过"
    return 0
  fi
  if ss -tlnp 2>/dev/null | grep -q ":$FRONTEND_PORT "; then
    warn "$FRONTEND_PORT 端口已被占用，请先停止旧进程"
    return 1
  fi
  [ -d "$WEB_DIR/node_modules" ] || { err "前端依赖未安装，请在 $WEB_DIR 执行 npm install"; return 1; }

  cd "$WEB_DIR"
  nohup npm run dev >>"$FRONTEND_LOG" 2>&1 &
  echo $! >"$FRONTEND_PID"
  disown

  log "等待前端监听 $FRONTEND_PORT ..."
  if wait_port "$FRONTEND_PORT" 30; then
    log "前端已启动 (pid=$(cat $FRONTEND_PID))"
  else
    err "前端启动超时，查看日志: $FRONTEND_LOG"
    tail -20 "$FRONTEND_LOG"
    return 1
  fi
}

# ============== 停止 ==============
stop_all() {
  step "停止服务"
  for name in frontend backend; do
    pf="$LOG_DIR/$name.pid"
    if [ -f "$pf" ]; then
      pid=$(cat "$pf")
      if kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null
        for i in $(seq 1 10); do
          kill -0 "$pid" 2>/dev/null || break
          sleep 1
        done
        kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null
        log "$name 已停止 (pid=$pid)"
      else
        warn "$name 进程不存在"
      fi
      rm -f "$pf"
    else
      warn "$name 没有 pid 文件"
    fi
  done
  # 兜底：用 pkill 清理残留
  pkill -f "deepseek-api$" 2>/dev/null && log "已清理残留 deepseek-api 进程"
  pkill -f "vite.*deepseek-web-api-admin" 2>/dev/null && log "已清理残留 vite 进程"
  # 清理 Chromium/Playwright 残留（避免新启动时冲突）
  pkill -9 -f "chrome-headless-shell" 2>/dev/null && log "已清理残留 Chromium 进程"
  pkill -9 -f "ms-playwright-go" 2>/dev/null && log "已清理残留 Playwright driver"
  sleep 2
}

# ============== 状态 ==============
show_status() {
  step "服务状态"
  echo "------------------------------------------------"
  printf "%-12s %-10s %-10s %s\n" "服务" "PID" "端口" "状态"
  echo "------------------------------------------------"
  for name in backend frontend; do
    pf="$LOG_DIR/$name.pid"
    port=$([ "$name" = "backend" ] && echo $BACKEND_PORT || echo $FRONTEND_PORT)
    pid="-"; st="${RED}未运行${NC}"
    if [ -f "$pf" ]; then
      pid=$(cat "$pf" 2>/dev/null)
      if kill -0 "$pid" 2>/dev/null; then st="${GREEN}运行中${NC}"; else pid="-"; fi
    fi
    printf "%-12s %-10s %-10s %b\n" "$name" "$pid" "$port" "$st"
  done
  echo "------------------------------------------------"
  echo "后端日志: $BACKEND_LOG"
  echo "前端日志: $FRONTEND_LOG"
  echo ""
  echo "访问地址:"
  echo "  前端控制台: http://<服务器IP>:$FRONTEND_PORT   (登录: admin / admin123)"
  echo "  后端 API  : http://<服务器IP>:$BACKEND_PORT/v1/models"
  echo "  健康检查  : http://<服务器IP>:$BACKEND_PORT/healthz"
}

# ============== 主入口 ==============
case "${1:-start}" in
  start)
    enable_swap
    start_deps
    start_backend
    start_frontend
    show_status
    ;;
  stop)
    stop_all
    ;;
  restart)
    stop_all
    sleep 2
    enable_swap
    start_deps
    start_backend
    start_frontend
    show_status
    ;;
  status)
    show_status
    ;;
  *)
    echo "用法: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac
