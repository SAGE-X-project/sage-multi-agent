#!/usr/bin/env bash
# Quick health check for SAGE demo components (HTTP + TCP)
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"
[[ -f "$ENV_FILE" ]] && source "$ENV_FILE"

HOST="${HOST:-localhost}"
CLIENT_PORT="${CLIENT_PORT:-8086}"
ROOT_PORT="${ROOT_PORT:-${ROOT_AGENT_PORT:-8080}}"
PLANNING_PORT="${PLANNING_PORT:-${PLANNING_AGENT_PORT:-8084}}"
ORDERING_PORT="${ORDERING_PORT:-${ORDERING_AGENT_PORT:-8083}}"
GATEWAY_PORT="${GATEWAY_PORT:-5500}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"

:

http_ok() { curl -sSf -m 1 "$1" >/dev/null 2>&1; }
port_open() {
  local host="$1" port="$2"
  { exec 3<>/dev/tcp/"$host"/"$port"; } >/dev/null 2>&1 && { exec 3>&- 3<&-; return 0; }
  return 1
}

echo "=================================================="
echo " SAGE Demo: Port & Endpoint Quick Check"
echo " Host: $HOST"
echo " .env: $ENV_FILE"
:
echo "=================================================="

check_http() {
  local title="$1" url="$2"
  if http_ok "$url"; then
    echo "[OK]   $(printf '%-18s' "$title") HTTP $url (code=200)"
  else
    echo "[FAIL] $(printf '%-18s' "$title") HTTP $url"
  fi
}

check_tcp() {
  local title="$1" host="$2" port="$3"
  if port_open "$host" "$port"; then
    echo "[OK]   $(printf '%-18s' "$title") TCP $host:$port is open"
  else
    echo "[FAIL] $(printf '%-18s' "$title") TCP $host:$port closed"
  fi
}

check_http "Client API"        "http://${HOST}:${CLIENT_PORT}/api/sage/config"
check_http "Root Agent"        "http://${HOST}:${ROOT_PORT}/status"
check_http "Planning Agent"    "http://${HOST}:${PLANNING_PORT}/status"
check_http "Ordering Agent"    "http://${HOST}:${ORDERING_PORT}/status"
check_http "Payment Service"   "http://${HOST}:${EXT_PAYMENT_PORT}/status"
echo "--------------------------------------------------"
check_tcp  "Gateway (TCP)"     "$HOST" "$GATEWAY_PORT"
check_tcp  "Payment Service"   "$HOST" "$EXT_PAYMENT_PORT"
echo "--------------------------------------------------"
echo "[DONE] Health check finished."
