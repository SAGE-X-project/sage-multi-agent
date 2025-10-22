#!/usr/bin/env bash
# Start Root; Payment runs IN-PROCESS; outbound Payment -> Gateway -> External

set -euo pipefail
source .env
mkdir -p logs pids


nohup go run cmd/root/main.go \
  -port ${ROOT_AGENT_PORT:-18080} \
  -planning http://localhost:${PLANNING_AGENT_PORT:-18081} \
  -ordering http://localhost:${ORDERING_AGENT_PORT:-18082} \
  > logs/root.log 2>&1 & echo $! > pids/root.pid

echo "[start] Root started on :${ROOT_AGENT_PORT:-18080} (Embedded Payment → Gateway :${GATEWAY_PORT:-5500})"
