#!/usr/bin/env bash
set -euo pipefail

services=(mariadb redis deepseek-api nginx)

show_status() {
  systemctl --no-pager --full status "${services[@]}" || true
  echo
  echo "管理后台: http://<服务器IP>:5173"
  echo "API:      http://<服务器IP>:8000/v1"
  echo "健康检查: http://127.0.0.1:8000/healthz"
}

case "${1:-start}" in
  start)
    systemctl start "${services[@]}"
    show_status
    ;;
  stop)
    systemctl stop deepseek-api nginx
    show_status
    ;;
  restart)
    systemctl restart mariadb redis deepseek-api nginx
    show_status
    ;;
  status)
    show_status
    ;;
  logs)
    journalctl -u deepseek-api -f
    ;;
  *)
    echo "用法: $0 {start|stop|restart|status|logs}" >&2
    exit 2
    ;;
esac
