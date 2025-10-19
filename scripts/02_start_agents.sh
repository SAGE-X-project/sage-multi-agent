#!/usr/bin/env bash
# Start Planning / Ordering / Payment agents (SAGE inbound verify = ON)

set -euo pipefail
source .env
mkdir -p logs pids

nohup go run cmd/agent-planning/main.go -port ${PLANNING_AGENT_PORT} -sage=true > logs/planning.log 2>&1 & echo $! > pids/planning.pid
nohup go run cmd/agent-ordering/main.go -port ${ORDERING_AGENT_PORT} -sage=true > logs/ordering.log 2>&1 & echo $! > pids/ordering.pid
nohup go run cmd/agent-payment/main.go  -port ${PAYMENT_AGENT_PORT}  -sage=true > logs/payment.log  2>&1 & echo $! > pids/payment.pid

echo "[start] Planning/Ordering/Payment started."
