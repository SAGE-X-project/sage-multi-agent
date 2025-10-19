#!/usr/bin/env bash
# Start Gateway with tamper ON (break signatures)

set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

HOST="${HOST:-localhost}"
GATEWAY_PORT="${GATEWAY_PORT:-5500}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"
# Support both ATTACK_MESSAGE and legacy ATTACK_MSG
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-$'\n[GW-ATTACK] injected by gateway'}}"

mkdir -p logs pids

kill_port() {
  local port="$1"; local pids
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -z "$pids" ]] && return
  echo "[kill] gateway on :$port -> $pids"
  kill $pids 2>/dev/null || true
  sleep 0.2
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -n "$pids" ]] && kill -9 $pids 2>/dev/null || true
}

kill_port "$GATEWAY_PORT"

echo "[start] Gateway (TAMPER) :${GATEWAY_PORT} -> external :${EXT_PAYMENT_PORT}"
echo "        attack-msg length: ${#ATTACK_MESSAGE}"

nohup go run ./cmd/gateway/main.go \
  -listen ":${GATEWAY_PORT}" \
  -upstream "http://${HOST}:${EXT_PAYMENT_PORT}" \
  -attack-msg "${ATTACK_MESSAGE}" \
  > logs/gateway.log 2>&1 & echo $! > pids/gateway.pid

echo "[ok] logs: logs/gateway.log  pid: $(cat pids/gateway.pid 2>/dev/null || echo '?')"
