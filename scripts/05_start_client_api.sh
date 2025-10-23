#!/usr/bin/env bash
# Start Client HTTP server (frontend entry)

set -euo pipefail
source .env
mkdir -p logs pids

ROOT_URL="http://localhost:${ROOT_AGENT_PORT:-18080}"

nohup go run cmd/client/main.go \
  -port ${CLIENT_PORT:-8086} \
  -root "${ROOT_URL}" \
  > logs/client.log 2>&1 & echo $! > pids/client.pid

echo "[start] Client API started on :${CLIENT_PORT:-8086}"
