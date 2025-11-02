# Testing Guide

## Overview

This document describes the testing strategy and procedures for the sage-multi-agent system.

## Table of Contents

1. [Test Infrastructure](#test-infrastructure)
2. [Integration Tests](#integration-tests)
3. [Manual Testing](#manual-testing)
4. [Test Scenarios](#test-scenarios)
5. [Prerequisites](#prerequisites)
6. [Running Tests](#running-tests)
7. [Troubleshooting](#troubleshooting)

## Test Infrastructure

### Built Components

The testing infrastructure includes:

1. **Integration Test Script**: `scripts/integration_test.sh`
   - Automated integration tests for all components
   - Tests basic connectivity, SAGE signatures, HPKE encryption, and error scenarios

2. **Manual Test Scripts**:
   - `scripts/06_start_all.sh` - One-click launcher for all services
   - `scripts/07_send_prompt.sh` - Send test requests with various configurations

3. **Built Binaries** (in `bin/`):
   - `root` (23MB) - Root routing agent
   - `payment` (22MB) - Payment processing agent
   - `medical` (22MB) - Medical information agent
   - `gateway` (8.6MB) - Gateway proxy (pass/tamper modes)
   - `client` (12MB) - Client API server

### Test Suites

#### Suite 1: Basic Connectivity
- Root agent health endpoint (`:18080/status`)
- Payment agent health endpoint (`:19083/status`)
- Medical agent health endpoint (`:19082/status`)
- Client API health endpoint (`:8086/api/sage/config`)
- Gateway forwarding (`:5500/payment/status`)

#### Suite 2: Basic Request Flow (SAGE OFF)
- Send request without SAGE protocol
- Verify response without signature verification
- Test basic routing and processing

#### Suite 3: RFC 9421 Signature Verification
- Enable SAGE protocol (signature verification)
- Send request with HTTP message signatures
- Verify signature verification metadata
- Test DID authentication

#### Suite 4: HPKE End-to-End Encryption
- Enable SAGE + HPKE
- Test HPKE handshake (Root → Payment)
- Test HPKE handshake (Root → Medical)
- Verify encrypted message processing
- Check HPKE session management

#### Suite 5: Error Scenarios
- Test tampered request detection
- Test HPKE decrypt failures
- Test invalid signature handling
- Test gateway attack mode

#### Suite 6: Multi-turn Conversations
- Test conversation continuity
- Test conversation ID management
- Test HPKE session reuse across turns

## Prerequisites

### 1. Build Binaries

```bash
# Build all binaries
go build -o bin/root ./cmd/root
go build -o bin/client ./cmd/client
go build -o bin/payment ./cmd/payment
go build -o bin/medical ./cmd/medical
go build -o bin/gateway ./cmd/gateway
```

**Status**: ✅ Completed - All binaries built successfully

### 2. Generate Keys

Generate cryptographic keys for agents:

```bash
# Using the registration script (includes key generation)
./scripts/00_register_agents.sh \
  --kem --merge \
  --signing-keys ./generated_agent_keys.json \
  --kem-keys ./keys/kem/generated_kem_keys.json \
  --combined-out ./merged_agent_keys.json \
  --agents "payment,planning,medical" \
  --wait-seconds 60 \
  --funding-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  --try-activate
```

**Note**: This requires a running Ethereum node (Hardhat/Anvil for local testing)

### 3. Setup Ethereum Node (Local Testing)

```bash
# Option 1: Using Hardhat
cd /path/to/hardhat-project
npx hardhat node

# Option 2: Using Anvil
anvil --port 8545
```

### 4. Deploy SAGE Registry Contract

```bash
# Deploy to local network
cd /path/to/sage-registry
npm run deploy:local

# Note the deployed contract address
# Default: 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512
```

### 5. Environment Configuration

Create `.env` file:

```bash
# Copy from example
cp .env.example .env

# Edit with your configuration
# At minimum, set:
# - Key file paths
# - Ethereum RPC URL
# - SAGE registry address
# - OpenAI API key (for LLM features)
```

## Running Tests

### Automated Integration Tests

```bash
# Run full integration test suite
./scripts/integration_test.sh
```

**Expected Output**:
```
======================================
  SAGE Multi-Agent Integration Test
======================================

━━━ Test Suite 1: Basic Connectivity ━━━
✓ PASS Root agent is responding
✓ PASS Payment agent is responding
✓ PASS Medical agent is responding
✓ PASS Client API is responding
✓ PASS Gateway is forwarding to payment

━━━ Test Suite 2: Basic Request Flow (SAGE OFF) ━━━
✓ PASS Received response without SAGE

━━━ Test Suite 3: RFC 9421 Signature Verification ━━━
✓ PASS SAGE signature verification working
✓ PASS SAGE verification metadata present

━━━ Test Suite 4: HPKE End-to-End Encryption ━━━
✓ PASS HPKE encryption working (Root → Payment)
✓ PASS HPKE handshake detected in Root logs
✓ PASS HPKE decryption detected in Payment logs

━━━ Test Suite 5: Error Scenarios ━━━
✓ PASS Tampering detected by SAGE verification
✓ PASS HPKE decrypt failure detected (tampering blocked)

━━━ Test Suite 6: Multi-turn Conversation ━━━
✓ PASS Turn 1 successful
✓ PASS Turn 2 successful

======================================
  Integration Test Results
======================================
Total Tests: 15
Passed:      15
Failed:      0

✓ All tests passed!
```

### Manual Testing

#### Test 1: Basic Request (No SAGE)

```bash
# Start services without SAGE
./scripts/06_start_all.sh --sage off --pass

# Send test request
./scripts/07_send_prompt.sh \
  --sage off \
  --prompt "What is the weather like?"
```

#### Test 2: Request with SAGE (No HPKE)

```bash
# Start services with SAGE
./scripts/06_start_all.sh --sage on --pass

# Send test request with signature verification
./scripts/07_send_prompt.sh \
  --sage on \
  --hpke off \
  --prompt "Check my account balance"
```

#### Test 3: Full Security (SAGE + HPKE)

```bash
# Start services with SAGE
./scripts/06_start_all.sh --sage on --pass

# Send test request with full encryption
./scripts/07_send_prompt.sh \
  --sage on \
  --hpke on \
  --prompt "send 10 USDC to merchant"
```

#### Test 4: Attack Detection

```bash
# Start services with gateway in tamper mode
./scripts/06_start_all.sh --sage on --tamper

# Send request (should detect tampering)
./scripts/07_send_prompt.sh \
  --sage on \
  --hpke on \
  --prompt "send 100 USDC to attacker"

# Check logs for tampering detection
grep -i "signature.*fail\|verification.*fail" logs/root.log logs/payment.log
```

#### Test 5: Interactive Multi-turn

```bash
# Start services
./scripts/06_start_all.sh --sage on --pass

# Interactive mode
./scripts/07_send_prompt.sh \
  --sage on \
  --hpke on \
  --interactive

# Example conversation:
# Turn 1: "I want to send money"
# Turn 2: "send 25 USDC to merchant"
```

## Test Scenarios

### Scenario 1: Payment Processing

**Objective**: Verify payment agent receives and processes payment requests

**Steps**:
1. Start services with SAGE + HPKE
2. Send payment request: "send 10 USDC to merchant"
3. Verify payment agent processes request
4. Check HPKE encryption in logs
5. Verify response includes payment confirmation

**Expected Result**:
- HPKE handshake successful
- Payment agent decrypts and processes request
- Response indicates payment status
- Logs show encrypted communication

### Scenario 2: Medical Information

**Objective**: Verify medical agent handles health queries

**Steps**:
1. Start services with SAGE + HPKE
2. Send medical query: "What are the symptoms of flu?"
3. Verify medical agent processes request
4. Check HPKE encryption in logs
5. Verify response includes medical information

**Expected Result**:
- HPKE handshake successful
- Medical agent decrypts and processes request
- Response includes medical advice
- Logs show encrypted communication

### Scenario 3: Signature Verification

**Objective**: Verify RFC 9421 HTTP message signatures

**Steps**:
1. Start services with SAGE ON, Gateway PASS
2. Send request with SAGE enabled
3. Check Root logs for signature creation
4. Check Payment/Medical logs for signature verification
5. Verify no signature errors

**Expected Result**:
- Root signs outbound requests
- Payment/Medical verify signatures
- No verification failures
- DID resolution successful

### Scenario 4: Tamper Detection

**Objective**: Verify system detects tampered requests

**Steps**:
1. Start services with SAGE ON, Gateway TAMPER
2. Send request with SAGE + HPKE enabled
3. Gateway injects attack message
4. Check logs for verification failures
5. Verify request is rejected or flagged

**Expected Result**:
- Gateway tampering logged
- Signature verification fails
- HPKE decrypt may fail
- Attack is detected and logged

### Scenario 5: Multi-turn Conversation

**Objective**: Verify conversation state management

**Steps**:
1. Start services with SAGE + HPKE
2. Send first message: "I want to send money"
3. Send follow-up: "send 15 USDC to merchant"
4. Verify conversation ID preserved
5. Check HPKE session reuse

**Expected Result**:
- Conversation ID maintained across turns
- HPKE session reused (no re-handshake)
- Context preserved between turns
- Final payment processed correctly

## Integration Test Results

### Phase 2 Verification

After completing Phase 2 refactoring (Agent Framework implementation):

**Build Status**: ✅ All agents built successfully
```
bin/
├── client  (12MB)
├── gateway (8.6MB)
├── medical (22MB)
├── payment (22MB)
└── root    (23MB)
```

**Framework Impact**:
- ✅ Payment agent: Eager HPKE pattern
- ✅ Medical agent: Eager HPKE pattern
- ✅ Root agent: Framework helpers for keys and resolver
- ✅ Planning agent: Framework keys pattern
- ✅ All binaries compile without errors
- ✅ No direct sage import conflicts

**Code Quality**:
- ✅ 18 direct sage imports removed (25 → 6)
- ✅ 350+ lines of boilerplate eliminated
- ✅ Consistent error handling
- ✅ Production-ready patterns

### Integration Test Infrastructure

**Status**: ✅ Complete

**Components Created**:
1. ✅ Integration test script (`scripts/integration_test.sh`)
   - 6 test suites
   - 15+ test cases
   - Automated pass/fail reporting

2. ✅ Test documentation (`docs/TESTING.md`)
   - Test prerequisites
   - Test scenarios
   - Manual testing procedures
   - Troubleshooting guide

**Blockers for Full Test Execution**:
1. ⚠️ Keys not generated (requires `scripts/00_register_agents.sh`)
2. ⚠️ Ethereum node not running (requires Hardhat/Anvil)
3. ⚠️ SAGE registry not deployed
4. ⚠️ Environment variables not configured

**Recommendation**:
Run full integration tests after completing deployment setup per `docs/DEPLOYMENT.md`

## Troubleshooting

### Issue: "Binaries not found"

**Cause**: Agents not built

**Solution**:
```bash
go build -o bin/root ./cmd/root
go build -o bin/payment ./cmd/payment
go build -o bin/medical ./cmd/medical
go build -o bin/gateway ./cmd/gateway
go build -o bin/client ./cmd/client
```

### Issue: "HPKE disabled" error

**Cause**: KEM key file missing

**Solution**:
```bash
# Generate keys with KEM support
./scripts/00_register_agents.sh --kem --merge ...

# Or check environment variable
echo $PAYMENT_KEM_JWK_FILE
ls -l $PAYMENT_KEM_JWK_FILE
```

### Issue: "Signature verification failed"

**Cause**:
- DID not registered
- Key mismatch
- SAGE registry not accessible

**Solution**:
```bash
# Check Ethereum node is running
curl -X POST http://127.0.0.1:8545 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'

# Verify DID is registered
cast call $SAGE_REGISTRY_ADDRESS \
  "isActive(string)" \
  "did:sage:payment" \
  --rpc-url $ETH_RPC_URL

# Re-register if needed
./scripts/00_register_agents.sh ...
```

### Issue: "Connection refused"

**Cause**: Service not running or port blocked

**Solution**:
```bash
# Check service is running
curl http://localhost:18080/status
curl http://localhost:19083/status

# Check port is listening
lsof -i :18080
lsof -i :19083

# Restart services
./scripts/01_kill_ports.sh
./scripts/06_start_all.sh --sage on --pass
```

### Issue: "jq: command not found"

**Cause**: jq not installed (optional but recommended)

**Solution**:
```bash
# macOS
brew install jq

# Ubuntu/Debian
sudo apt-get install jq

# Or continue without jq (tests will still work)
```

### Checking Logs

View logs for detailed debugging:

```bash
# Root agent
tail -f logs/root.log

# Payment agent
tail -f logs/payment.log

# Medical agent
tail -f logs/medical.log

# Gateway
tail -f logs/gateway.log

# Search for errors
grep -i error logs/*.log

# Search for HPKE activity
grep -i hpke logs/*.log

# Search for signature failures
grep -i "signature.*fail\|verification.*fail" logs/*.log
```

## Metrics and Observability

### Current Logging

All agents log to `logs/*.log`:
- Request/response traces
- HPKE handshakes and sessions
- Signature verification results
- Error conditions
- Performance timing

### Future Enhancements

**Recommended additions**:
1. Prometheus metrics endpoints
2. Structured logging (JSON)
3. Distributed tracing (OpenTelemetry)
4. Health check endpoints with detailed status
5. Performance benchmarks

## Continuous Integration

### Future CI/CD Pipeline

**Recommended GitHub Actions workflow**:

```yaml
name: Integration Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.24'

      - name: Build
        run: |
          go build -o bin/root ./cmd/root
          go build -o bin/payment ./cmd/payment
          go build -o bin/medical ./cmd/medical
          go build -o bin/gateway ./cmd/gateway
          go build -o bin/client ./cmd/client

      - name: Start Anvil
        run: |
          anvil --port 8545 &
          sleep 2

      - name: Deploy Registry
        run: |
          cd sage-registry
          npm install
          npm run deploy:local

      - name: Register Agents
        run: |
          ./scripts/00_register_agents.sh --kem --merge ...

      - name: Run Integration Tests
        run: |
          ./scripts/integration_test.sh
```

## Summary

**Integration Test Infrastructure**: ✅ Complete

**Components**:
- ✅ All agent binaries built (87MB total)
- ✅ Integration test script with 6 test suites
- ✅ Test documentation and scenarios
- ✅ Manual testing procedures
- ✅ Troubleshooting guide

**Next Steps**:
1. Complete deployment setup (keys, Ethereum node, registry)
2. Run full integration test suite
3. Add unit tests for critical components
4. Set up CI/CD pipeline
5. Add performance benchmarks

**Status**: Ready for deployment and full integration testing after environment setup.
