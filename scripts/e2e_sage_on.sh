#!/bin/bash
set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${YELLOW}Starting backend...${NC}"
./scripts/start-backend.sh &
sleep 4

echo -e "${YELLOW}Rebinding Payment behind tamper gateway...${NC}"
# Stop existing payment (if running)
pkill -f "/cmd/payment" >/dev/null 2>&1 || true
sleep 1

# Start payment on 18083
go run ./cmd/payment --port 18083 --sage true & echo $! >> .pids
sleep 1

# Start tamper gateway at 8083 → 18083
go run ./cmd/gateway --listen :8083 --dest http://localhost:18083 --attack " [GATEWAY TAMPER]" & echo $! >> .pids
sleep 1

REQUEST='{"prompt":"please pay 1 ETH to 0x123"}'
echo -e "${YELLOW}Sending payment request via client...${NC}"
echo "----- HTTP Request (client -> backend)"; echo "$REQUEST"; echo
RESP=$(curl -s -X POST http://localhost:8086/send/prompt \
  -H 'Content-Type: application/json' \
  -d "$REQUEST")

echo "Response: $RESP"
if echo "$RESP" | rg -qi 'tampering|error|failed|verification'; then
  echo -e "${GREEN}[SAGE ON] Tampering detected as expected.${NC}"
  exit 0
else
  echo -e "${RED}[SAGE ON] Expected detection, but none found.${NC}"
  exit 1
fi
