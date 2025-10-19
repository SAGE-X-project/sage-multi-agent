#!/bin/bash
set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${YELLOW}Ensuring backend is running...${NC}"
./scripts/start-backend.sh &
sleep 4

echo -e "${YELLOW}Configure SAGE OFF on root and payment...${NC}"
# Toggle root off
go run ./cli --root http://localhost:8080 toggle-sage off >/dev/null 2>&1 || true

# Restart payment on 18083 with SAGE off
pkill -f "/cmd/payment" >/dev/null 2>&1 || true
sleep 1
go run ./cmd/payment --port 18083 --sage false & echo $! >> .pids
sleep 1

# Start tamper gateway at 8083 → 18083 (if not already)
if ! lsof -i :8083 >/dev/null 2>&1; then
  go run ./cmd/gateway --listen :8083 --dest http://localhost:18083 --attack " [GATEWAY TAMPER]" & echo $! >> .pids
  sleep 1
fi

echo -e "${YELLOW}Sending payment request via client...${NC}"
REQUEST='{"prompt":"please pay 1 ETH to 0x123"}'
echo "----- HTTP Request (client -> backend)"; echo "$REQUEST"; echo
RESP=$(curl -s -X POST http://localhost:8086/send/prompt \
  -H 'Content-Type: application/json' \
  -d "$REQUEST")

echo "Response: $RESP"
if echo "$RESP" | rg -qi 'error|failed|tamper'; then
  echo -e "${RED}[SAGE OFF] Unexpected detection/error; should pass without warning.${NC}"
  exit 1
else
  echo -e "${GREEN}[SAGE OFF] Tampered message processed without warning (as expected).${NC}"
  exit 0
fi
