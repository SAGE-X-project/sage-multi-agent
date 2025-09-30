#!/bin/bash

echo "======================================"
echo "Running SAGE Multi-Agent Test Suite"
echo "======================================"
echo ""

# Color codes
GREEN='\033[0;32m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Track results
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

run_test() {
    local test_file=$1
    local test_name=$2

    echo -e "${BLUE}Running: $test_name${NC}"
    if go test -v "$test_file" -timeout 30s > /tmp/test_output.log 2>&1; then
        echo -e "${GREEN}✓ PASSED${NC}"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        echo -e "${RED}✗ FAILED${NC}"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        cat /tmp/test_output.log | grep -E "(FAIL|Error|panic)" | head -10
    fi
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    echo ""
}

# Run individual test suites
run_test "./test/flexible_did_test.go" "Flexible DID Support Tests"
run_test "./test/message_router_test.go" "Message Router Tests"
run_test "./test/circuit_breaker_test.go" "Circuit Breaker Tests"
run_test "./test/websocket_reconnect_test.go" "WebSocket Reconnection Tests"
run_test "./test/e2e_scenarios_test.go" "End-to-End Scenario Tests"
run_test "./test/agent_registration_test.go" "Agent Registration & Blockchain Integration Tests"

# Print summary
echo "======================================"
echo "Test Summary"
echo "======================================"
echo -e "Total Tests:  $TOTAL_TESTS"
echo -e "${GREEN}Passed:       $PASSED_TESTS${NC}"
if [ $FAILED_TESTS -gt 0 ]; then
    echo -e "${RED}Failed:       $FAILED_TESTS${NC}"
else
    echo -e "Failed:       $FAILED_TESTS"
fi
echo "======================================"
echo ""

if [ $FAILED_TESTS -eq 0 ]; then
    echo -e "${GREEN}All tests passed! ✓${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed. Please review the output above.${NC}"
    exit 1
fi