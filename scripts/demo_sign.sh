#!/usr/bin/env bash
# Demo: Client API -> Root (routes to in-proc Payment) -> signed HTTP -> Gateway (tamper/pass) -> External Agent (verify) -> back
#
# Options:
#   --sage on|off              # SAGE signing flag for sub-agents' outbound calls (default: on)
#   --tamper | --pass          # Gateway tamper mode (default: --tamper)
#   --attack-msg "<txt>"       # message to append in tamper mode
#   --prompt "<text>"          # demo prompt (e.g., "send 10 USDC to merchant")
#   --force-kill               # kill occupied ports before start
#
# Flow:
#   1) bring up external payment + gateway + root + client
#   2) POST to client /api/request with X-SAGE-Enabled header
#   3) show summarized result + tail logs

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
mkdir -p logs run tmp

# ---- defaults (env override) ----
[[ -f .env ]] && source .env

: "${HOST:=localhost}"
: "${ROOT_AGENT_PORT:=18080}"
: "${ROOT_PORT:=${ROOT_AGENT_PORT}}"
: "${CLIENT_PORT:=8086}"
: "${GATEWAY_PORT:=5500}"
: "${EXT_PAYMENT_PORT:=19083}"
: "${PAYMENT_AGENT_PORT:=18083}"
: "${CHECK_PAYMENT_STATUS:=0}"

: "${PAYMENT_JWK_FILE:=keys/payment.jwk}"
: "${PAYMENT_DID:=}"

SAGE_MODE="on"
GW_MODE="tamper"
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-$'\n[GW-ATTACK] injected by gateway'}}"
PROMPT="send 10 USDC to merchant"
FORCE_KILL=0

usage() {
  cat <<EOF
Usage: $0 [--sage on|off] [--tamper|--pass] [--attack-msg "<txt>"] [--prompt "<text>"] [--force-kill]
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --sage)        SAGE_MODE="${2:-on}"; shift 2 ;;
    --tamper)      GW_MODE="tamper"; shift ;;
    --pass|--passthrough) GW_MODE="pass"; shift ;;
    --attack-msg)  ATTACK_MESSAGE="${2:-}"; shift 2 ;;
    --prompt)      PROMPT="${2:-}"; shift 2 ;;
    --force-kill)  FORCE_KILL=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

need() { command -v "$1" >/dev/null 2>&1 || { echo "[ERR] '$1' not found"; exit 1; }; }
need go
need curl

if ! command -v jq >/dev/null 2>&1; then
  echo "[WARN] 'jq' not found; will fallback to safe shell escaping for JSON."
fi

wait_http() {
  local url="$1" tries="${2:-60}" delay="${3:-0.2}"
  for ((i=1;i<=tries;i++)); do
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done; return 1
}

# ---- 0) preflight ----
if [[ ! -f "$PAYMENT_JWK_FILE" ]]; then
  echo "[ERR] PAYMENT_JWK_FILE not found: $PAYMENT_JWK_FILE"
  exit 1
fi

echo "[CFG] SAGE_MODE         = $SAGE_MODE"
echo "[CFG] GATEWAY_MODE      = $GW_MODE"
echo "[CFG] ATTACK_MESSAGE    = ${#ATTACK_MESSAGE} bytes"
echo "[CFG] PROMPT            = $PROMPT"
echo "[CFG] PAYMENT_JWK_FILE  = $PAYMENT_JWK_FILE"
[[ -n "$PAYMENT_DID" ]] && echo "[CFG] PAYMENT_DID        = $PAYMENT_DID"

# ---- 1) bring up stack (minimal) ----
UP_ARGS=()
if [[ "$GW_MODE" == "pass" ]]; then UP_ARGS+=(--pass); else UP_ARGS+=(--tamper --attack-msg "$ATTACK_MESSAGE"); fi
if [[ $FORCE_KILL -eq 1 ]]; then UP_ARGS+=(--force-kill); fi

echo "[UP] starting stack via scripts/06_start_all.sh ${UP_ARGS[*]}"

SAGE_MODE="$SAGE_MODE" PAYMENT_JWK_FILE="$PAYMENT_JWK_FILE" PAYMENT_DID="$PAYMENT_DID" \
  bash ./scripts/06_start_all.sh "${UP_ARGS[@]}"

# ---- 2) wait for endpoints ----
echo "[WAIT] endpoints"
for url in \
  "http://${HOST}:${EXT_PAYMENT_PORT}/status" \
  "http://${HOST}:${GATEWAY_PORT}/status" \
  "http://${HOST}:${ROOT_PORT}/status" \
  "http://${HOST}:${CLIENT_PORT}/api/sage/config"
do
  if wait_http "$url" 50 0.2; then
    echo "[OK] $url"
  else
    echo "[WARN] not ready: $url"
  fi
done

# ---- 3) fire a request through Client API (/api/request) ----
# Build replay-safe JSON body. NEVER use printf %q for JSON.
REQ_PAYLOAD="$(mktemp -t req.XXXX.json)"
RESP_PAYLOAD="$(mktemp -t resp.XXXX.json)"

if command -v jq >/dev/null 2>&1; then
  # jq encodes any string safely as JSON
  printf '{"prompt": %s}\n' "$(jq -Rsa . <<<"$PROMPT")" > "$REQ_PAYLOAD"
else
  # portable escape: backslash and double quotes
  esc=${PROMPT//\\/\\\\}
  esc=${esc//\"/\\\"}
  printf '{"prompt":"%s"}\n' "$esc" > "$REQ_PAYLOAD"
fi

# Header: X-SAGE-Enabled (lower/upper safe on macOS bash)
SAGE_MODE_LOWER="$(printf '%s' "$SAGE_MODE" | tr '[:upper:]' '[:lower:]')"
SAGE_HDR="false"
if [[ "$SAGE_MODE_LOWER" == "on" ]]; then SAGE_HDR="true"; fi

echo
echo "[REQ] POST /api/request X-SAGE-Enabled: $SAGE_HDR  (scenario=demo)"
HTTP_CODE=$(curl -sS -o "$RESP_PAYLOAD" -w "%{http_code}" \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: $SAGE_HDR" \
  -H "X-Scenario: demo" \
  -X POST "http://${HOST}:${CLIENT_PORT}/api/request" \
  --data-binary @"$REQ_PAYLOAD" || true)

echo "[HTTP] client-api status: $HTTP_CODE"

# ---- 4) summarize result ----
echo
echo "========== Client API Response (summary) =========="
if command -v jq >/dev/null 2>&1; then
  jq '{response:.response, verification:(.SAGEVerification // .sageVerification // null), metadata:.metadata, logs:(.logs // null)}' "$RESP_PAYLOAD" 2>/dev/null || cat "$RESP_PAYLOAD"
else
  cat "$RESP_PAYLOAD"
fi
echo "==================================================="

DETECTED="no"
if grep -q '"external error: 401' "$RESP_PAYLOAD" || grep -q 'content-digest mismatch' "$RESP_PAYLOAD"; then
  DETECTED="yes"
fi

SAGE_MODE_UP="$(printf '%s' "$SAGE_MODE" | tr '[:lower:]' '[:upper:]')"
GW_MODE_UP="$(printf '%s' "$GW_MODE" | tr '[:lower:]' '[:upper:]')"

echo
echo "[RESULT] SAGE=${SAGE_MODE_UP}, GATEWAY=${GW_MODE_UP} -> MITM detected by external? => $DETECTED"

# ---- 5) helpful logs ----
echo
echo "----- external-payment.log (tail) -----"
tail -n 60 logs/external-payment.log 2>/dev/null || echo "(no log yet)"
echo
echo "----- gateway.log (tail) -----"
tail -n 60 logs/gateway.log 2>/dev/null || echo "(no log yet)"
echo
echo "----- root.log (tail) -----"
tail -n 60 logs/root.log 2>/dev/null || echo "(no log yet)"
echo
echo "----- client.log (tail) -----"
tail -n 60 logs/client.log 2>/dev/null || echo "(no log yet)"

echo
echo "[HINT] Compare scenarios:"
echo "  1) SAGE ON + pass      -> success (no MITM)"
echo "  2) SAGE ON + tamper    -> 401 or digest mismatch (detected)"
echo "  3) SAGE OFF + tamper   -> likely passes (no detection)"

echo
echo "[UP] ensuring clean state via scripts/01_kill_ports.sh"
bash ./scripts/01_kill_ports.sh || true
