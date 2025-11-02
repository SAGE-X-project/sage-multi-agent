# SAGE Multi‑Agent (Demo)

Secure multi‑agent demo showcasing:

- **RFC 9421 HTTP Message Signatures** on outbound requests (A2A signer)
- **DID resolution** against an Ethereum Registry (on‑chain public keys)
- **HPKE session bootstrap** + payload encryption between agents
- **High-level Agent Framework** for simplified crypto/DID/HPKE management
- A tampering Gateway that demonstrates signature failure and HPKE integrity

This repo demonstrates a modern multi-agent architecture with:
- ✅ **Eager HPKE initialization** for Payment/Medical agents (production-ready)
- ✅ **Framework-based key management** eliminating 350+ lines of boilerplate
- ✅ **Clean separation** between business logic and protocol complexity

## Architecture

### Overview

```
┌─────────────┐
│   Client    │ (HTTP API :8086)
└──────┬──────┘
       │
       v
┌─────────────────────────────────────────────────┐
│              Root Agent (:18080)                │
│  ┌──────────────────────────────────────────┐  │
│  │  In-proc Agents (business logic)         │  │
│  │  ├─ Planning  (hotels, travel)           │  │
│  │  ├─ Medical   (health advice)            │  │
│  │  └─ Payment   (transactions)             │  │
│  └──────────────────────────────────────────┘  │
└────────┬────────────────────────────────────────┘
         │ (Outbound: RFC 9421 + HPKE)
         v
┌─────────────┐      ┌──────────────────────────┐
│   Gateway   │◄────►│  External Agents         │
│  (:5500)    │      │  ├─ Payment (:19083)     │
│  [Tamper]   │      │  └─ Medical (:19082)     │
└─────────────┘      └──────────────────────────┘
```

### Agent Framework (NEW!)

All agents now use the **internal/agent framework** for simplified management:

```
internal/agent/
├── keys/        # Key loading & management (JWK, PEM)
├── did/         # DID resolver abstraction
├── hpke/        # HPKE server/client wrappers
├── session/     # Session management
└── middleware/  # DID authentication middleware
```

**Benefits:**
- ✅ 18 direct sage imports removed (25 → 6)
- ✅ 350+ lines of boilerplate code eliminated
- ✅ Consistent error handling across agents
- ✅ Easier testing and maintenance

### HPKE Patterns

**Eager Initialization** (Payment/Medical):
- HPKE initialized at startup
- Fail-fast on configuration errors
- Production-ready pattern

**Lazy Initialization** (Root):
- HPKE sessions created per-target on demand
- Supports multiple external targets
- Client-side pattern

## Components

- **Root Agent** `cmd/root/main.go` (HTTP, default `:18080`)
  - Routes requests to in-proc or external agents
  - HPKE client for encrypted outbound communication
  - RFC 9421 signing for all external requests

- **In‑proc Agents** (`agents/*`)
  - **Planning**: Hotel/travel recommendations (framework keys)
  - **Medical**: Health advice with LLM (framework agent, Eager HPKE)
  - **Payment**: Transaction processing with LLM (framework agent, Eager HPKE)

- **External Agents**
  - Payment server `cmd/payment/main.go` (HTTP, `:19083`)
  - Medical server `cmd/medical/main.go` (HTTP, `:19082`)
  - Both use DID authentication + HPKE encryption

- **Gateway** `cmd/gateway/main.go` (HTTP, `:5500`)
  - Reverse proxy with optional tampering
  - Demonstrates signature/HPKE failure modes

- **Client API** `cmd/client/main.go` (HTTP, `:8086`)
  - Frontend-facing REST API
  - Conversation state management

### Libraries

- **A2A signer/verifier**: `github.com/sage-x-project/sage-a2a-go`
- **Core crypto/DID/HPKE**: `github.com/sage-x-project/sage`
- **Agent Framework**: `internal/agent` (will migrate to sage-a2a-go v1.7.0)

**Note**: This repo uses local `replace` directives in go.mod for development. Remove these for production builds.

## Default Ports

- Root: `18080`
- Client API: `8086`
- Gateway: `5500`
- External Payment: `19083`
- External Medical: `19082`

All of these can be overridden via `.env` or flags (see scripts below).

## Prerequisites

- **Go 1.24+** (toolchain declared in go.mod)
- **Ethereum dev node** (Hardhat/Anvil) or any RPC with SAGE Registry V4 deployed
- **Basic tooling**: `curl`, `jq` (optional), `cast` (optional) for funding

## Environment Variables

### Core Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `ETH_RPC_URL` | `http://127.0.0.1:8545` | Ethereum RPC endpoint for DID resolution |
| `SAGE_REGISTRY_ADDRESS` | `0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512` | SAGE Registry V4 contract address |
| `SAGE_EXTERNAL_KEY` | - | Operator private key (hex, no 0x) for registry transactions |

### Root Agent

| Variable | Required | Description |
|----------|----------|-------------|
| `ROOT_JWK_FILE` | ✅ Yes | Path to Root agent signing key (secp256k1 JWK) |
| `ROOT_DID` | No | Root agent DID (auto-derived from key if not set) |
| `ROOT_SAGE_ENABLED` | No | Enable RFC 9421 signing (default: `true`) |

### Planning Agent

| Variable | Required | Description |
|----------|----------|-------------|
| `PLANNING_JWK_FILE` | ✅ Yes | Path to Planning agent signing key (JWK) |
| `PLANNING_DID` | No | Planning agent DID (auto-derived if not set) |
| `PLANNING_EXTERNAL_URL` | No | External planning service URL (falls back to local) |
| `PLANNING_SAGE_ENABLED` | No | Enable signing (default: `true`) |

### Payment Agent (External Server)

| Variable | Required | Description |
|----------|----------|-------------|
| `PAYMENT_JWK_FILE` | ✅ Yes | Path to signing key (secp256k1 JWK) |
| `PAYMENT_KEM_JWK_FILE` | ✅ Yes* | Path to KEM key (X25519 JWK, required for HPKE) |
| `PAYMENT_DID` | No | Payment agent DID |
| `PAYMENT_SAGE_ENABLED` | No | Enable DID auth (default: `true`) |
| `PAYMENT_LLM_ENDPOINT` | No | LLM API endpoint (falls back to `OPENAI_BASE_URL`) |
| `PAYMENT_LLM_API_KEY` | No | LLM API key (falls back to `OPENAI_API_KEY`) |
| `PAYMENT_LLM_MODEL` | No | LLM model name (default: `gpt-4o-mini`) |

**\*Required when HPKE is enabled**

### Medical Agent (External Server)

| Variable | Required | Description |
|----------|----------|-------------|
| `MEDICAL_JWK_FILE` | ✅ Yes | Path to signing key (secp256k1 JWK) |
| `MEDICAL_KEM_JWK_FILE` | ✅ Yes* | Path to KEM key (X25519 JWK, required for HPKE) |
| `MEDICAL_DID` | No | Medical agent DID |
| `MEDICAL_SAGE_ENABLED` | No | Enable DID auth (default: `true`) |
| `MEDICAL_LLM_ENDPOINT` | No | LLM API endpoint |
| `MEDICAL_LLM_API_KEY` | No | LLM API key |
| `MEDICAL_LLM_MODEL` | No | LLM model name |

**\*Required when HPKE is enabled**

### LLM Configuration (Global Fallbacks)

| Variable | Default | Description |
|----------|---------|-------------|
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible API endpoint |
| `OPENAI_API_KEY` | - | API key for LLM requests |
| `LLM_MODEL` | `gpt-4o-mini` | Default model for all agents |

### Demo Keys

Demo keys are provided for local development:
- `keys/*.jwk` - Signing keys (secp256k1)
- `keys/kem/*.jwk` - KEM keys (X25519)
- `generated_agent_keys.json` - Merged key registry
- `merged_agent_keys.json` - Combined signing + KEM keys

**⚠️ Security**: These keys are for development only. Never use in production.

## Quick Start: Register, Launch, Send

1. Register agents (only once)

```bash
sh ./scripts/00_register_agents.sh \
  --kem --merge \
  --signing-keys ./generated_agent_keys.json \
  --kem-keys ./keys/kem/generated_kem_keys.json \
  --combined-out ./merged_agent_keys.json \
  --agents "payment,planing,medical" \
  --wait-seconds 60 \
  --funding-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  --try-activate
```

- Merges signing+KEM keys, commits→registers, and tries activation after the delay.
- The `--funding-key` shown is the default Hardhat/Anvil dev key; replace if needed.

2. Launch services (Gateway tamper by default)

```bash
./scripts/06_start_all.sh --tamper  # tamper mode (default)
./scripts/06_start_all.sh --pass   # pass-through (no tampering)
```

- `--pass`: Gateway forwards requests unchanged.
- No flag: Gateway injects a small tamper string into JSON (or flips ciphertext when HPKE is on).

3. Send a message

Using helper script (recommended):

```bash
# Single turn (signed)
./scripts/07_send_prompt.sh --sage on --prompt "iPhone 15 프로를 쿠팡에서 구매해서 서울 특별시 00길로 배송해줘"

# With HPKE (requires SAGE on)
./scripts/07_send_prompt.sh --sage on --hpke on --prompt "당뇨병 진단을 받았어. 혈당은 180이야. 식단  관리 방법 알려줘"

# Interactive multi-turn
./scripts/07_send_prompt.sh --sage on -i
```

Or via curl to the Client API:

```bash
curl -sS -X POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H 'X-SAGE-Enabled: true' \
  --data-binary '{"prompt":"send 5 usdc to bob"}' | jq
```

## Frontend Request Guide

Endpoint

- `POST http://localhost:8086/api/request`

Headers

- `Content-Type: application/json` (required)
- `X-SAGE-Enabled: true|false` — enable/disable A2A signing (required for HPKE)
- `X-HPKE-Enabled: true|false` — request HPKE (requires SAGE=true)
- `X-Conversation-ID` or `X-SAGE-Context-ID` — optional; keeps conversation state across turns
- `X-Scenario: <name>` — optional, for logging and demos

Body

- `{ "prompt": "..." }` (optionally include `conversationId` too)

Rules

- If `X-HPKE-Enabled: true` while `X-SAGE-Enabled: false`, the API returns `400 Bad Request`.
- When HPKE is ON, the first request may perform a session handshake; subsequent requests carry ciphertext.

Examples

```bash
# SAGE ON (signed), HPKE OFF
curl -sS -X POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H 'X-SAGE-Enabled: true' \
  --data-binary '{"prompt":"buy an iPhone 15"}' | jq

# SAGE ON + HPKE ON
curl -sS -X POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H 'X-SAGE-Enabled: true' \
  -H 'X-HPKE-Enabled: true' \
  --data-binary '{"prompt":"send 10 USDC to merchant"}' | jq

# Multi-turn with a fixed conversation id
CID="demo-$(date +%s)"
curl -sS -X POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H "X-SAGE-Enabled: true" \
  -H "X-Conversation-ID: $CID" -H "X-SAGE-Context-ID: $CID" \
  --data-binary '{"prompt":"buy an iPhone 15"}' | jq
curl -sS -X POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H "X-SAGE-Enabled: true" \
  -H "X-Conversation-ID: $CID" -H "X-SAGE-Context-ID: $CID" \
  --data-binary '{"prompt":"ship to Seoul, card, budget 1,500,000 KRW"}' | jq
```

## Frontend request examples (fetch)

```ts
await fetch("http://localhost:8086/api/request", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-SAGE-Enabled": "true",
    // "X-HPKE-Enabled": "true", // optional; requires SAGE=true
    // "X-Conversation-ID": "demo-123", // optional conversation id
  },
  body: JSON.stringify({ prompt: "send 10 USDC" }),
});
```

## What to Expect (Demo)

- SAGE ON + Gateway Tamper: External Payment rejects mutated bodies (4xx) because DID middleware verifies RFC 9421 over the exact bytes. You should see an error bubble back to Root/Client.
- SAGE OFF + Gateway Tamper: Mutations pass through; you will see modified content reach External.
- HPKE ON: Payment encrypts payloads to External. The Gateway’s ciphertext bit‑flip breaks decryption; External returns an HPKE decrypt error. Plain responses are re‑encrypted back to the client.

## Internals (Project Structure)

### Agent Framework (`internal/agent/`)

High-level abstractions eliminating direct sage dependencies:

```
internal/agent/
├── agent.go          # Main Agent struct with NewAgentFromEnv()
├── keys/
│   ├── keys.go       # Key loading (JWK, PEM)
│   └── formats/      # Format importers
├── did/
│   └── resolver.go   # DID resolver abstraction
├── hpke/
│   ├── hpke.go       # HPKE server wrapper
│   └── transport.go  # Transport interface
├── session/
│   └── session.go    # Session management
└── middleware/
    └── middleware.go # DID auth middleware
```

**Key files:**
- `agent.go:70-230` - `NewAgent()` and `NewAgentFromEnv()` constructors
- `keys/keys.go:45-80` - `LoadFromJWKFile()` helper
- `did/resolver.go:30-65` - `NewResolver()` with config
- `hpke/hpke.go:85-200` - `NewServer()` with Eager initialization

### Agents (`agents/`)

Business logic agents using the framework:

```
agents/
├── root/
│   └── agent.go      # Routing, HPKE client, RFC 9421 signing
├── planning/
│   └── agent.go      # Hotel/travel logic (framework keys)
├── payment/
│   └── agent.go      # Payment logic (framework agent, Eager HPKE)
└── medical/
    └── agent.go      # Medical logic (framework agent, Eager HPKE)
```

**Key patterns:**
- `payment/agent.go:59-90` - Eager HPKE initialization
- `medical/agent.go:59-90` - Eager HPKE initialization
- `planning/agent.go:158-184` - Framework key loading
- `root/agent.go:241-287` - Lazy HPKE per-target sessions

### Other Components

- **Client API**: `api/api.go`, `cmd/client/main.go`
- **A2A transport**: `protocol/a2a_transport.go` (RFC 9421 wrapper)
- **DID middleware**: `internal/a2autil/middleware.go` (framework wrapper)
- **Gateway**: `gateway/gateway.go`, `cmd/gateway/main.go` (tamper proxy)
- **External servers**: `cmd/payment/main.go`, `cmd/medical/main.go`

## Troubleshooting

- Check logs under `logs/*.log` (launcher scripts write there)
- Verify middleware env: `ETH_RPC_URL`, `SAGE_REGISTRY_ADDRESS`
- Kill stuck ports: `scripts/01_kill_ports.sh --force`
- Ensure keys exist: `keys/*.jwk`, `keys/kem/*.jwk`, `generated_agent_keys.json`
- If developing without local `sage` repos, remove/adjust `replace` lines in `go.mod` and run `go mod tidy`

## Migration to sage-a2a-go v1.7.0

> ⚠️ **Status**: This project uses `internal/agent` framework (Phase 2 complete). Migration to **sage-a2a-go v1.7.0** is planned when the official release is available.

### Current State (Phase 2 Complete)

✅ **All agents refactored** to use the framework:
- **18 sage imports removed** (25 → 6)
- **350+ lines of boilerplate eliminated**
- **14 helper functions deleted**
- **Eager HPKE pattern** for Payment/Medical agents

### Migration Plan (When v1.7.0 Released)

**Step 1**: Verify sage-a2a-go v1.7.0 contains `pkg/agent/` framework
```bash
go get github.com/sage-x-project/sage-a2a-go@v1.7.0
```

**Step 2**: Update import paths
```bash
find agents internal/a2autil -type f -name "*.go" -exec sed -i '' \
  's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' {} +
```

**Step 3**: Remove internal framework
```bash
rm -rf internal/agent
go mod tidy
```

**Step 4**: Test and verify
```bash
go build ./...
go test ./...
./scripts/06_start_all.sh --pass
./scripts/07_send_prompt.sh --sage on --hpke on --prompt "Test"
```

### Benefits of Migration

- ✅ **Official Support**: Maintained by sage-a2a-go team
- ✅ **External Dependency**: No internal framework maintenance
- ✅ **Version Management**: Semantic versioning (v1.7.0+)
- ✅ **Community Contributions**: Shared improvements across projects

### Documentation

See detailed migration documentation in:
- 📖 [Phase 2 Completion Summary](./docs/PHASE2_FINAL_SUMMARY.md)
- 📊 [Phase 2 Progress](./docs/PHASE2_COMPLETE.md)
- 📝 [Next Steps Guide](./docs/NEXT_STEPS.md)

**Estimated Migration Time**: 2-3 hours (when v1.7.0 is available)

---

## Security Notes

- Demo keys are for local development only. Do not reuse in production.
- The Gateway demonstrates attacks; never deploy it in front of real systems.
- HPKE session/nonce/replay protection is handled by `sage` session manager. Keep processes single‑instance for predictable demos.
