#!/usr/bin/env bash
# Start Client HTTP server (frontend entry)

set -euo pipefail
source .env
mkdir -p logs pids

nohup go run cmd/client-api/main.go \
  -port ${CLIENT_PORT} \
  -root http://localhost:${ROOT_AGENT_PORT} \
  -payment http://localhost:${PAYMENT_AGENT_PORT} \
  > logs/client.log 2>&1 & echo $! > pids/client.pid

echo "[start] Client API started on :${CLIENT_PORT}"
