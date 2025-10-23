#!/usr/bin/env bash
# Start Root; payment runs as a separate process; outbound Payment -> Gateway -> External

set -euo pipefail
source .env
mkdir -p logs pids

ROOT_PORT="${ROOT_AGENT_PORT:-18080}"
PLANNING_URL="http://localhost:${PLANNING_AGENT_PORT:-18081}"
ORDERING_URL="http://localhost:${ORDERING_AGENT_PORT:-18082}"
PAYMENT_URL="http://localhost:${PAYMENT_AGENT_PORT:-18083}"

nohup go run cmd/root/main.go \
  -port "${ROOT_PORT}" \
  -planning "${PLANNING_URL}" \
  -ordering "${ORDERING_URL}" \
  -payment  "${PAYMENT_URL}" \
  > logs/root.log 2>&1 & echo $! > pids/root.pid

echo "[start] Root started on :${ROOT_PORT} (routes → planning/ordering/payment)"
