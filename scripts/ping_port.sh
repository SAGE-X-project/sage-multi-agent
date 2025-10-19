#!/usr/bin/env bash
# Quick port & endpoint check for SAGE demo
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"
[[ -f "$ENV_FILE" ]] && source "$ENV_FILE"

HOST="${HOST:-localhost}"

# Safe defaults with mapping to avoid "unbound variable"
ROOT_PORT="${ROOT_PORT:-${ROOT_AGENT_PORT:-18080}}"
PLANNING_PORT="${PLANNING_PORT:-${PLANNING_AGENT_PORT:-18081}}"
ORDERING_PORT="${ORDERING_PORT:-${ORDERING_AGENT_PORT:-18082}}"
PAYMENT_PORT="${PAYMENT_PORT:-${PAYMENT_AGENT_PORT:-18083}}"
CLIENT_PORT="${CLIENT_PORT:-8086}"

# Optional API server (legacy)
API_PORT="${API_PORT:-${API_HTTP_PORT:-8080}}"

GATEWAY_PORT="${GATEWAY_PORT:-5500}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"

echo "=================================================="
echo " SAGE Demo: Port & Endpoint Quick Check"
echo " Host: $HOST"
echo " .env: $ENV_FILE"
echo "=================================================="

check_http() {
  local name="$1" url="$2"
  local code
  code=$(curl -sS -m 1 -o /dev/null -w "%{http_code}" "$url" || echo 000)
  if [[ "$code" =~ ^2|3|4[0-9]{2}$ ]]; then
    printf "[OK]   %-20s HTTP %s (code=%s)\n" "$name" "$url" "$code"
  else
    printf "[FAIL] %-20s HTTP %s (code=%s)\n" "$name" "$url" "$code"
  fi
}

check_tcp() {
  local name="$1" host="$2" port="$3"
  if { exec 3<>/dev/tcp/"$host"/"$port"; } >/dev/null 2>&1; then
    exec 3>&- 3<&-
    printf "[OK]   %-20s TCP %s:%s is open\n" "$name" "$host" "$port"
  else
    printf "[FAIL] %-20s TCP %s:%s not reachable\n" "$name" "$host" "$port"
  fi
}

check_http "Client API"        "http://${HOST}:${CLIENT_PORT}/api/sage/config"
check_http "Root Agent"        "http://${HOST}:${ROOT_PORT}/status"
check_http "Planning Agent"    "http://${HOST}:${PLANNING_PORT}/status"
check_http "Ordering Agent"    "http://${HOST}:${ORDERING_PORT}/status"
check_http "Payment Agent"     "http://${HOST}:${PAYMENT_PORT}/status"
check_http "External Payment"  "http://${HOST}:${EXT_PAYMENT_PORT}/status"
check_http "API server (opt)"  "http://${HOST}:${API_PORT}/api/sage/config"

echo "--------------------------------------------------"
check_tcp "Gateway (TCP)"       "$HOST" "$GATEWAY_PORT"
check_tcp "External Payment"    "$HOST" "$EXT_PAYMENT_PORT"
echo "--------------------------------------------------"
echo "[DONE] Health check finished."
