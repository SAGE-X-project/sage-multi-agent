#!/usr/bin/env bash
# Start Root; routes outward via Gateway to Payment (verifier)

set -euo pipefail
[[ -f .env ]] && source .env
mkdir -p logs pids

ROOT_PORT="${ROOT_AGENT_PORT:-18080}"
GATEWAY_PORT="${GATEWAY_PORT:-5500}"
PAYMENT_EXTERNAL_URL="http://localhost:${GATEWAY_PORT}"

# Respect SAGE_MODE env if provided (off => -sage false)
SMODE="$(printf '%s' "${SAGE_MODE:-off}" | tr '[:upper:]' '[:lower:]')"
ROOT_SAGE="true"
case "$SMODE" in
  off|false|0|no) ROOT_SAGE="false" ;;
esac

nohup go run cmd/root/main.go \
  -port "${ROOT_PORT}" \
  -sage "${ROOT_SAGE}" \
  -payment-external  "${PAYMENT_EXTERNAL_URL}" \
  > logs/root.log 2>&1 & echo $! > pids/root.pid

echo "[start] Root started on :${ROOT_PORT} (outbound → gateway ${PAYMENT_EXTERNAL_URL}) SAGE=${ROOT_SAGE}"
