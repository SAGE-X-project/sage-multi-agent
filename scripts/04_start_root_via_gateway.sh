#!/usr/bin/env bash
# Start Root; route Payment through the Gateway (so MITM sits between)

set -euo pipefail
source .env
mkdir -p logs pids

nohup go run cmd/agent-root/main.go \
  -port ${ROOT_AGENT_PORT} \
  -planning-url http://localhost:${PLANNING_AGENT_PORT} \
  -ordering-url http://localhost:${ORDERING_AGENT_PORT} \
  -payment-url  http://localhost:${GATEWAY_PORT} \
  -sage=true \
  > logs/root.log 2>&1 & echo $! > pids/root.pid

echo "[start] Root started on :${ROOT_AGENT_PORT} (Payment via Gateway :${GATEWAY_PORT})"
