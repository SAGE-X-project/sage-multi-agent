# SAGE Multi‑Agent (Demo)

Secure multi‑agent demo showcasing:

- RFC 9421 HTTP Message Signatures on outbound requests (A2A signer)
- DID resolution against an Ethereum Registry (on‑chain public keys)
- HPKE session bootstrap + payload encryption between Payment → External
- A tampering Gateway that demonstrates signature failure and HPKE integrity

This repo wires together in‑proc agents behind a Root, an External Payment service (with DID auth), a Gateway proxy (optional tamper), and a simple Client API.

## Components

- Root Agent `cmd/root/main.go` (HTTP, default `:18080`)
- In‑proc sub‑agents: Planning, Ordering, Payment (`agents/*`)
- External Payment server `cmd/external-payment/main.go` (HTTP, default `:19083`)
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

All of these can be overridden via `.env` or flags (see scripts below).

## Prerequisites

- Go 1.24+ (toolchain declared in go.mod)
- An Ethereum dev node (Hardhat/Anvil) or any RPC with the SAGE Registry V4 deployed
- Basic tooling: `curl`, `jq` (optional), `cast` (optional) for funding

Environment used by servers and middleware (with working defaults):

- `ETH_RPC_URL` (default `http://127.0.0.1:8545`)
- `SAGE_REGISTRY_V4_ADDRESS` (default `0x5FbDB2315678afecb367f032d93F642f64180aa3`)
- `SAGE_EXTERNAL_KEY` (optional; hex without 0x; used for tx signing if needed)
- `PAYMENT_JWK_FILE` (path to secp256k1 JWK for Payment outbound signing)
- `EXTERNAL_JWK_FILE` and `EXTERNAL_KEM_JWK_FILE` for the External Payment server

Demo keys are provided under `keys/` and `generated_agent_keys.json` for convenience.

## demo_SAGE

1. Prerequisite — register agents on‑chain first

```bash
# Writes/uses generated_agent_keys.json and registers to SageRegistryV4
./scripts/00_register_agents.sh --both   # or --kem / --kem-only / signing only
```

If you don't have keys yet:

- Generate signing (ECDSA secp256k1) keys and summary first (required before registration):
  - `go run tools/keygen/gen_agents_key.go --agents "payment,ordering,planing,external"`
- To use HPKE, generate KEM (X25519) keys (External server requires a KEM private JWK):
  - `go run tools/keygen/gen_kem_keys.go --agents "payment,external"`
  - Ensure `EXTERNAL_KEM_JWK_FILE` points to the external agent's KEM JWK (default: `keys/kem/external.x25519.jwk`).

2. Run the demo with three toggles

- SAGE on/off (request‑time):

  - SAGE ON: send requests with `-H 'X-SAGE-Enabled: true'` to sign agent→external (default)
  - SAGE OFF: use `-H 'X-SAGE-Enabled: false'` (no signing)
  - Optional global switch: `scripts/toggle_sage.sh on|off`

- Gateway tamper/pass (process‑start):

  - `./demo_SAGE.sh --tamper` (mutate bodies; demo attack) — default
  - `./demo_SAGE.sh --pass` (pass‑through)

- HPKE on/off (process‑start):

  - `./demo_SAGE.sh --hpke on --hpke-keys generated_agent_keys.json`
  - `./demo_SAGE.sh --hpke off` (default)
  - Requires KEM keys (see above). HPKE is only available when SAGE mode is ON (`X-SAGE-Enabled: true`).

- Prompt (request content):
  - `./demo_SAGE.sh --prompt "<text>"` sets the prompt the Client API sends (default: `send 10 USDC to merchant`).

Defaults when flags are omitted

- `--sage on`
- `--tamper` (gateway mutates bodies)
- `--hpke off`
- `--hpke-keys generated_agent_keys.json`
- `--prompt "send 10 USDC to merchant"`

Examples

```bash
# Tamper + HPKE OFF (observe RFC9421 signature failure when SAGE=ON)
sh ./demo_SAGE.sh --tamper --hpke off

# Tamper + HPKE ON (observe HPKE decrypt error on manipulated ciphertext)
sh ./demo_SAGE.sh --tamper --hpke on --hpke-keys generated_agent_keys.json

# Pass‑through + HPKE ON (no manipulation; encrypted hop to External)
sh ./demo_SAGE.sh --pass --hpke on
```

Send a request (SAGE ON/OFF is per request):

```bash
curl -sS POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H 'X-SAGE-Enabled: true' \
  -d '{"prompt":"send 5 usdc to bob"}' | jq
```

3. What happens with HPKE/tamper/SAGE

- HPKE ON → Gateway sees only ciphertext. If tamper=ON, it flips a byte; External rejects with HPKE decrypt error.
- HPKE OFF + SAGE ON → Gateway changes plaintext; External’s DID middleware detects RFC 9421 signature mismatch (4xx).
- HPKE OFF + SAGE OFF → Tampered payload passes through; demo shows why signing matters.

## Manual Start (granular)

1. External Payment (verifies RFC 9421 + DID)

```bash
# require signatures (default)
EXTERNAL_REQUIRE_SIG=1 ./scripts/02_start_external_payment_agent.sh

# or allow unsigned (demo)
EXTERNAL_REQUIRE_SIG=0 ./scripts/02_start_external_payment_agent.sh
```

2. Gateway (tamper or pass‑through)

```bash
./scripts/03_start_gateway_tamper.sh
# or
./scripts/03_start_gateway_pass.sh
```

3. Root + HPKE (optional)

```bash
go run ./cmd/root/main.go \
  -port 18080 \
  -hpke \
  -hpke-keys generated_agent_keys.json
```

4. Client API (signs client→root if provided a JWK)

```bash
go run ./cmd/client/main.go -port 8086 -root http://localhost:18080

# Optional client signing
go run ./cmd/client/main.go \
  -client-jwk keys/payment.jwk \
  -client-did did:sage:generated:client-1
```

## Registering Agents (on‑chain)

The DID middleware resolves keys from the SAGE Registry V4. For a clean setup:

- Generate ECDSA keys and summary (demo already includes `generated_agent_keys.json`):

  - `go run -tags=reg_agents_key tools/keygen/gen_agents_key.go --agents "payment,ordering,planing,external"`

- Generate X25519 KEM keys (optional, demo includes `keys/kem/generated_kem_keys.json`):

  - `go run -tags=reg_kem_key tools/keygen/gen_kem_keys.go --agents "payment,external"`

- Register ECDSA and add KEM in one flow:

```bash
# Requires ETH_RPC_URL and SAGE_REGISTRY_V4_ADDRESS (defaults are local dev)
./scripts/00_register_agents.sh --both
```

Funding helpers are built‑in (Hardhat/Anvil setBalance; optional `--funding-key` + `cast`).

## Making Requests

- The Client API exposes a single endpoint. Root routes by content (planning/ordering/payment).

```bash
curl -sS POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H 'X-SAGE-Enabled: true' \
  -d '{"prompt":"send 5 usdc to bob"}' | jq
```

Header semantics:

- `X-SAGE-Enabled: true|false` toggles A2A signing at sub‑agents (Payment→External)
- `X-Scenario` is forwarded to agents as metadata (optional)

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
- External Payment (handshake + data mode): `cmd/external-payment/main.go`
- Payment HPKE client wiring: `agents/payment/hpke_wrap.go`

## Troubleshooting

- Check logs under `logs/*.log` (launcher scripts write there)
- Verify middleware env: `ETH_RPC_URL`, `SAGE_REGISTRY_V4_ADDRESS`
- Kill stuck ports: `scripts/01_kill_ports.sh --force`
- Ensure keys exist: `keys/*.jwk`, `keys/kem/*.jwk`, `generated_agent_keys.json`
- If developing without local `sage` repos, remove/adjust `replace` lines in `go.mod` and run `go mod tidy`

## Security Notes

- Demo keys are for local development only. Do not reuse in production.
- The Gateway demonstrates attacks; never deploy it in front of real systems.
- HPKE session/nonce/replay protection is handled by `sage` session manager. Keep processes single‑instance for predictable demos.
