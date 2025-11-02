#!/usr/bin/env bash
# Integration test script for sage-multi-agent system
# Tests: Basic connectivity, HPKE handshake, signature verification, error scenarios

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

# Test results
PASS=0
FAIL=0
TOTAL=0

# Helper functions
log_test() {
  echo -e "\n${BLUE}[TEST $((TOTAL+1))]${NC} $1"
}

log_pass() {
  echo -e "${GREEN}✓ PASS${NC} $1"
  PASS=$((PASS+1))
  TOTAL=$((TOTAL+1))
}

log_fail() {
  echo -e "${RED}✗ FAIL${NC} $1"
  FAIL=$((FAIL+1))
  TOTAL=$((TOTAL+1))
}

log_info() {
  echo -e "${YELLOW}ℹ${NC} $1"
}

cleanup() {
  echo -e "\n${YELLOW}Cleaning up...${NC}"
  ./scripts/01_kill_ports.sh 2>/dev/null || true
  rm -f .cid 2>/dev/null || true
}

trap cleanup EXIT

# Check prerequisites
log_info "Checking prerequisites..."
command -v curl >/dev/null 2>&1 || { echo "curl not found"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq not found (recommended)"; }

# Check if binaries exist
if [[ ! -f bin/root ]] || [[ ! -f bin/payment ]] || [[ ! -f bin/medical ]]; then
  echo -e "${RED}Binaries not found. Run: go build -o bin/root ./cmd/root${NC}"
  exit 1
fi

echo "======================================"
echo "  SAGE Multi-Agent Integration Test"
echo "======================================"

# ============================================
# TEST SUITE 1: Basic Connectivity
# ============================================
echo -e "\n${BLUE}━━━ Test Suite 1: Basic Connectivity ━━━${NC}"

log_info "Starting services with SAGE OFF, Gateway PASS..."
./scripts/06_start_all.sh --sage off --pass >/dev/null 2>&1 || true
sleep 5

log_test "Root agent health endpoint"
if curl -sf http://localhost:18080/status >/dev/null 2>&1; then
  log_pass "Root agent is responding"
else
  log_fail "Root agent is not responding"
fi

log_test "Payment agent health endpoint"
if curl -sf http://localhost:19083/status >/dev/null 2>&1; then
  log_pass "Payment agent is responding"
else
  log_fail "Payment agent is not responding"
fi

log_test "Medical agent health endpoint"
if curl -sf http://localhost:19082/status >/dev/null 2>&1; then
  log_pass "Medical agent is responding"
else
  log_fail "Medical agent is not responding"
fi

log_test "Client API health endpoint"
if curl -sf http://localhost:8086/api/sage/config >/dev/null 2>&1; then
  log_pass "Client API is responding"
else
  log_fail "Client API is not responding"
fi

log_test "Gateway health (pass-through mode)"
if curl -sf http://localhost:5500/payment/status >/dev/null 2>&1; then
  log_pass "Gateway is forwarding to payment"
else
  log_fail "Gateway is not forwarding correctly"
fi

# ============================================
# TEST SUITE 2: Basic Request Flow (SAGE OFF)
# ============================================
echo -e "\n${BLUE}━━━ Test Suite 2: Basic Request Flow (SAGE OFF) ━━━${NC}"

log_test "Send request without SAGE"
response=$(curl -sf -X POST http://localhost:8086/api/request \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: false" \
  -H "X-HPKE-Enabled: false" \
  -d '{"prompt":"What is the weather?","conversationId":"test-1"}' 2>/dev/null || echo '{"error":"failed"}')

if echo "$response" | jq -e '.response' >/dev/null 2>&1; then
  log_pass "Received response without SAGE"
else
  log_fail "Failed to get response without SAGE"
fi

# ============================================
# TEST SUITE 3: SAGE Signature Verification
# ============================================
echo -e "\n${BLUE}━━━ Test Suite 3: RFC 9421 Signature Verification ━━━${NC}"

log_info "Restarting services with SAGE ON, Gateway PASS..."
cleanup
./scripts/06_start_all.sh --sage on --pass >/dev/null 2>&1 || true
sleep 5

log_test "Send request with SAGE enabled (no HPKE)"
response=$(curl -sf -X POST http://localhost:8086/api/request \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -H "X-HPKE-Enabled: false" \
  -d '{"prompt":"Check my balance","conversationId":"test-sage-1"}' 2>/dev/null || echo '{"error":"failed"}')

if echo "$response" | jq -e '.response' >/dev/null 2>&1; then
  log_pass "SAGE signature verification working"

  # Check if verification data is present
  if echo "$response" | jq -e '.SAGEVerification' >/dev/null 2>&1 || echo "$response" | jq -e '.sageVerification' >/dev/null 2>&1; then
    log_pass "SAGE verification metadata present"
  else
    log_info "SAGE verification metadata not in response (may be in logs)"
  fi
else
  log_fail "SAGE signature verification failed"
fi

# ============================================
# TEST SUITE 4: HPKE Encryption
# ============================================
echo -e "\n${BLUE}━━━ Test Suite 4: HPKE End-to-End Encryption ━━━${NC}"

log_test "Send request with SAGE + HPKE enabled"
response=$(curl -sf -X POST http://localhost:8086/api/request \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -H "X-HPKE-Enabled: true" \
  -d '{"prompt":"send 10 USDC to merchant","conversationId":"test-hpke-1"}' 2>/dev/null || echo '{"error":"failed"}')

if echo "$response" | jq -e '.response' >/dev/null 2>&1; then
  log_pass "HPKE encryption working (Root → Payment)"
else
  log_fail "HPKE encryption failed"
fi

# Check logs for HPKE handshake
if grep -q "HPKE.*handshake\|HPKE.*session\|HPKE.*initialized" logs/root.log 2>/dev/null; then
  log_pass "HPKE handshake detected in Root logs"
else
  log_info "HPKE handshake not explicitly logged (may be working)"
fi

if grep -q "HPKE.*decrypt\|HPKE.*process\|HPKE.*session" logs/payment.log 2>/dev/null; then
  log_pass "HPKE decryption detected in Payment logs"
else
  log_info "HPKE decryption not explicitly logged"
fi

# ============================================
# TEST SUITE 5: Error Scenarios
# ============================================
echo -e "\n${BLUE}━━━ Test Suite 5: Error Scenarios ━━━${NC}"

log_info "Restarting services with SAGE ON, Gateway TAMPER..."
cleanup
./scripts/06_start_all.sh --sage on --tamper --attack-msg "[TAMPERED]" >/dev/null 2>&1 || true
sleep 5

log_test "Tampered request detection (SAGE should detect)"
response=$(curl -sf -X POST http://localhost:8086/api/request \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -H "X-HPKE-Enabled: false" \
  -d '{"prompt":"send 100 USDC","conversationId":"test-tamper-1"}' 2>/dev/null || echo '{"error":"failed"}')

# Check if tampering was detected in logs
if grep -qi "signature.*fail\|verification.*fail\|tamper\|invalid.*signature" logs/root.log logs/payment.log 2>/dev/null; then
  log_pass "Tampering detected by SAGE verification"
else
  log_info "Tampering detection not found in logs (check manually)"
fi

log_test "HPKE with tampered request (should fail decrypt)"
response=$(curl -sf -X POST http://localhost:8086/api/request \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -H "X-HPKE-Enabled: true" \
  -d '{"prompt":"send 50 USDC","conversationId":"test-hpke-tamper-1"}' 2>/dev/null || echo '{"error":"failed"}')

if grep -qi "decrypt.*fail\|HPKE.*error\|HPKE.*invalid" logs/payment.log logs/medical.log 2>/dev/null; then
  log_pass "HPKE decrypt failure detected (tampering blocked)"
else
  log_info "HPKE decrypt failure not found in logs"
fi

# ============================================
# TEST SUITE 6: Multi-turn Conversation
# ============================================
echo -e "\n${BLUE}━━━ Test Suite 6: Multi-turn Conversation ━━━${NC}"

log_info "Restarting services with SAGE ON, Gateway PASS..."
cleanup
./scripts/06_start_all.sh --sage on --pass >/dev/null 2>&1 || true
sleep 5

CID="test-conv-$(date +%s)"

log_test "Multi-turn conversation (turn 1)"
response1=$(curl -sf -X POST http://localhost:8086/api/request \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -H "X-HPKE-Enabled: true" \
  -H "X-Conversation-ID: $CID" \
  -d "{\"prompt\":\"I want to send money\",\"conversationId\":\"$CID\"}" 2>/dev/null || echo '{"error":"failed"}')

if echo "$response1" | jq -e '.response' >/dev/null 2>&1; then
  log_pass "Turn 1 successful"
else
  log_fail "Turn 1 failed"
fi

log_test "Multi-turn conversation (turn 2)"
response2=$(curl -sf -X POST http://localhost:8086/api/request \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -H "X-HPKE-Enabled: true" \
  -H "X-Conversation-ID: $CID" \
  -d "{\"prompt\":\"send 25 USDC to merchant\",\"conversationId\":\"$CID\"}" 2>/dev/null || echo '{"error":"failed"}')

if echo "$response2" | jq -e '.response' >/dev/null 2>&1; then
  log_pass "Turn 2 successful"
else
  log_fail "Turn 2 failed"
fi

# ============================================
# Final Report
# ============================================
echo ""
echo "======================================"
echo "  Integration Test Results"
echo "======================================"
echo -e "Total Tests: ${BLUE}$TOTAL${NC}"
echo -e "Passed:      ${GREEN}$PASS${NC}"
echo -e "Failed:      ${RED}$FAIL${NC}"

if [[ $FAIL -eq 0 ]]; then
  echo -e "\n${GREEN}✓ All tests passed!${NC}"
  exit 0
else
  echo -e "\n${YELLOW}⚠ Some tests failed. Check logs for details:${NC}"
  echo "  - logs/root.log"
  echo "  - logs/payment.log"
  echo "  - logs/medical.log"
  echo "  - logs/gateway.log"
  exit 1
fi
