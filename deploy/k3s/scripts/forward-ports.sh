#!/bin/bash
# Auto port-forward gateway and ws-gateway to localhost
# Usage: ./deploy/k3s/scripts/forward-ports.sh [start|stop|status]
# Default Ingress (for non-localhost access): http://172.27.95.205

K3S_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PID_DIR="$K3S_DIR/.pids"
mkdir -p "$PID_DIR"

GW_PID_FILE="$PID_DIR/gw-forward.pid"
WS_PID_FILE="$PID_DIR/ws-forward.pid"

start_forward() {
  local name="$1"
  local svc="$2"
  local local_port="$3"
  local target_port="$4"
  local pid_file="$5"

  if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
    echo "[$name] already running (port $local_port)"
    return
  fi

  kubectl port-forward "svc/$svc" "$local_port:$target_port" &>/dev/null &
  echo $! > "$pid_file"
  echo "[$name] $svc → localhost:$local_port"
}

stop_forward() {
  local name="$1"
  local pid_file="$2"

  if [ -f "$pid_file" ]; then
    local pid=$(cat "$pid_file")
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null
      echo "[$name] stopped (pid=$pid)"
    fi
    rm -f "$pid_file"
  fi
}

case "${1:-start}" in
  start)
    start_forward "gateway"    "aim-gateway"    8080 8080 "$GW_PID_FILE"
    start_forward "ws-gateway" "aim-ws-gateway"  8081 8081 "$WS_PID_FILE"
    echo ""
    echo "=== Port Forwards ==="
    echo "  REST API:  http://localhost:8080"
    echo "  WebSocket: ws://localhost:8081"
    echo "  Ingress:   http://172.27.95.205  (Traefik, no port-forward needed)"
    ;;
  stop)
    stop_forward "gateway"    "$GW_PID_FILE"
    stop_forward "ws-gateway" "$WS_PID_FILE"
    ;;
  status)
    for f in gateway:8080:"$GW_PID_FILE" ws-gateway:8081:"$WS_PID_FILE"; do
      name="${f%%:*}"
      port="${f#*:}"; port="${port%:*}"
      pid_file="${f##*:}"
      if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        echo "[$name] localhost:$port ✓"
      else
        echo "[$name] not running ✗"
      fi
    done
    ;;
  *)
    echo "Usage: $0 {start|stop|status}"
    exit 1
    ;;
esac
