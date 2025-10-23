#!/usr/bin/env bash
# Kill known SAGE demo ports (TERM then KILL).
# - Reads repo-root .env if present
# - Default ports are provided when .env is missing
# Usage:
#   scripts/kill_ports.sh            # interactive confirm
#   scripts/kill_ports.sh --force    # no confirm

set -Eeuo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"
ENV_FILE="${ENV_FILE:-$PROJECT_ROOT/.env}"

if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$ENV_FILE"
fi

# Default ports (override via .env)
CLIENT_PORT="${CLIENT_PORT:-8086}"
ROOT_PORT="${ROOT_AGENT_PORT:-18080}"
PLANNING_PORT="${PLANNING_AGENT_PORT:-18081}"
ORDERING_PORT="${ORDERING_AGENT_PORT:-18082}"
PAYMENT_AGENT_PORT="${PAYMENT_AGENT_PORT:-18083}"   # payment agent runs as a separate process
GATEWAY_PORT="${GATEWAY_PORT:-5500}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"

# All listeners we want to clear
PORTS=(
  "$CLIENT_PORT" "$ROOT_PORT"
  "$PLANNING_PORT" "$ORDERING_PORT" "$PAYMENT_AGENT_PORT"
  "$GATEWAY_PORT" "$EXT_PAYMENT_PORT"
)

FORCE=0
if [[ "${1:-}" == "--force" ]]; then FORCE=1; fi

echo "SAGE kill-ports: ${PORTS[*]}"
if [[ $FORCE -eq 0 ]]; then
  read -r -p "Proceed to kill processes on these ports? [y/N] " ans
  [[ "$ans" == "y" || "$ans" == "Y" ]] || { echo "Aborted."; exit 0; }
fi

kill_port() {
  local port="$1"
  local pids
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  if [[ -z "$pids" ]]; then
    echo "[INFO] No listener on :$port"
    return
  fi
  echo "[INFO] Killing port :$port -> PIDs: $pids"
  kill $pids 2>/dev/null || true
  sleep 0.3
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  if [[ -n "$pids" ]]; then
    echo "[WARN] Forcing kill on :$port -> $pids"
    kill -9 $pids 2>/dev/null || true
  fi
}

for p in "${PORTS[@]}"; do
  kill_port "$p"
done

echo "[DONE] Ports cleared."
