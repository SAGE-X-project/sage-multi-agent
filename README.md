# SAGE Multi‑Agent (Demo)

Secure multi‑agent demo showcasing:

- RFC 9421 HTTP Message Signatures on outbound requests (A2A signer)
- DID resolution against an Ethereum Registry (on‑chain public keys)
- HPKE session bootstrap + payload encryption between Payment → External
- A tampering Gateway that demonstrates signature failure and HPKE integrity

This repo wires together in‑proc agents behind a Root, an External Payment service (with DID auth), a Gateway proxy (optional tamper), and a simple Client API.

## Components

- Root Agent `cmd/root/main.go` (HTTP, default `:18080`)
- In‑proc sub‑agents: Planning, MEDICAL, Payment (`agents/*`)
- External Payment server `cmd/payment/main.go` (HTTP, default `:19083`)
- Gateway reverse proxy `cmd/gateway/main.go` (HTTP, default `:5500`)
- Client API `cmd/client/main.go` (HTTP, default `:8086`)

Libraries used:

- A2A signer/verifier: `github.com/sage-x-project/sage-a2a-go`
- Core crypto/DID/HPKE/session: `github.com/sage-x-project/sage`

Notes on go.mod: this repo uses local `replace` directives to sibling checkouts of `sage` and `sage-a2a-go`. Adjust or remove these lines if you are not developing with local copies.

## Default Ports

- Root: `18080`
- Client API: `8086`
- Gateway: `5500`
- External Payment: `19083`
- External Medical: `19082`

All of these can be overridden via `.env` or flags (see scripts below).

## Prerequisites

- Go 1.24+ (toolchain declared in go.mod)
- An Ethereum dev node (Hardhat/Anvil) or any RPC with the SAGE Registry V4 deployed
- Basic tooling: `curl`, `jq` (optional), `cast` (optional) for funding

Environment used by servers and middleware (with working defaults):

- `ETH_RPC_URL` (default `http://127.0.0.1:8545`)
- `SAGE_REGISTRY_ADDRESS` (default `0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512`)
- `SAGE_EXTERNAL_KEY` (optional; hex without 0x; used for tx signing if needed)
- `PAYMENT_JWK_FILE` (path to secp256k1 JWK for Payment outbound signing)
- `PAYMENT_JWK_FILE` and `PAYMENT_KEM_JWK_FILE` for the External Payment server

Demo keys are provided under `keys/` and `generated_agent_keys.json` for convenience.

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
./scripts/07_send_prompt.sh --sage on --prompt "iPhone 15 프로 구매해줘"

# With HPKE (requires SAGE on)
./scripts/07_send_prompt.sh --sage on --hpke on --prompt "고지혈증 진단을받았어. 어떻게 관리해야할까"

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

## Internals (where things live)

- Root routing and health: `agents/root/agent.go`
- Client API facade: `api/api.go`, `cmd/client/main.go`
- A2A transport used by Payment: `protocol/a2a_transport.go`
- DID middleware wrapper: `internal/a2autil/middleware.go`
- Gateway reverse proxy (tamper): `gateway/gateway.go`, `cmd/gateway/main.go`
- External Payment (handshake + data mode): `cmd/payment/main.go`
- Payment HPKE client wiring: `agents/payment/hpke_wrap.go`

## Troubleshooting

- Check logs under `logs/*.log` (launcher scripts write there)
- Verify middleware env: `ETH_RPC_URL`, `SAGE_REGISTRY_ADDRESS`
- Kill stuck ports: `scripts/01_kill_ports.sh --force`
- Ensure keys exist: `keys/*.jwk`, `keys/kem/*.jwk`, `generated_agent_keys.json`
- If developing without local `sage` repos, remove/adjust `replace` lines in `go.mod` and run `go mod tidy`

## Security Notes

- Demo keys are for local development only. Do not reuse in production.
- The Gateway demonstrates attacks; never deploy it in front of real systems.
- HPKE session/nonce/replay protection is handled by `sage` session manager. Keep processes single‑instance for predictable demos.
