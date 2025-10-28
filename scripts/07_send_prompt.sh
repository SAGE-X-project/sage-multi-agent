#!/usr/bin/env bash
# Send a prompt to the running Client API and show Gateway packet logs.
#
# Usage:
#   scripts/07_send_prompt.sh [--sage on|off] [--hpke on|off] [--prompt "<text>"]
#   scripts/07_send_prompt.sh --prompt-file prompt.txt
#   echo "send 10 USDC" | scripts/07_send_prompt.sh

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

HOST="${HOST:-localhost}"
CLIENT_PORT="${CLIENT_PORT:-8086}"
GATEWAY_PORT="${GATEWAY_PORT:-5500}"
ROOT_PORT="${ROOT_AGENT_PORT:-18080}"

SAGE="off"
HPKE="off"
PROMPT=""
PROMPT_FILE=""

usage() {
  cat <<EOF
Usage: $0 [--sage on|off] [--hpke on|off] [--prompt "<text>"] [--prompt-file <path>]
If no --prompt/--prompt-file given, reads stdin.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --sage)        SAGE="${2:-off}"; shift 2 ;;
    --hpke)        HPKE="${2:-off}"; shift 2 ;;
    --prompt)      PROMPT="${2:-}"; shift 2 ;;
    --prompt-file) PROMPT_FILE="${2:-}"; shift 2 ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

if [[ -z "$PROMPT" ]]; then
  if [[ -n "$PROMPT_FILE" ]]; then
    PROMPT="$(cat "$PROMPT_FILE")"
  elif ! tty -s; then
    PROMPT="$(cat -)"
  fi
fi

if [[ -z "$PROMPT" ]]; then
  read -r -p "Enter prompt: " PROMPT
fi

SAGE_HDR="false"
case "$(echo "$SAGE" | tr '[:upper:]' '[:lower:]')" in
  on|true|1|yes) SAGE_HDR="true" ;;
  *)             SAGE_HDR="false" ;;
esac

HPKE_HDR="false"
case "$(echo "$HPKE" | tr '[:upper:]' '[:lower:]')" in
  on|true|1|yes) HPKE_HDR="true" ;;
  *)             HPKE_HDR="false" ;;
esac

# Enforce: when SAGE is OFF, HPKE must be OFF
if [[ "$SAGE_HDR" == "false" && "$HPKE_HDR" == "true" ]]; then
  echo "[WARN] SAGE is OFF → forcing HPKE=OFF for this request" >&2
  HPKE_HDR="false"
fi

# Single-request mode: do not call any config endpoints. Headers below drive behavior per-request.

REQ_PAYLOAD="$(mktemp -t prompt.req.XXXX.json)"
RESP_PAYLOAD="$(mktemp -t prompt.resp.XXXX.json)"

if command -v jq >/dev/null 2>&1; then
  printf '{"prompt": %s}\n' "$(jq -Rsa . <<<"$PROMPT")" > "$REQ_PAYLOAD"
else
  esc=${PROMPT//\\/\\\\}; esc=${esc//\"/\\\"}
  printf '{"prompt":"%s"}\n' "$esc" > "$REQ_PAYLOAD"
fi

GW_LOG="logs/gateway.log"
ROOT_LOG="logs/root.log"
EXT_LOG="logs/external-payment.log"
PRE_GW=0; PRE_ROOT=0; PRE_EXT=0
[[ -f "$GW_LOG" ]] && PRE_GW=$(wc -l < "$GW_LOG" || echo 0)
[[ -f "$ROOT_LOG" ]] && PRE_ROOT=$(wc -l < "$ROOT_LOG" || echo 0)
[[ -f "$EXT_LOG" ]] && PRE_EXT=$(wc -l < "$EXT_LOG" || echo 0)

echo "[REQ] POST /api/request  X-SAGE-Enabled: $SAGE_HDR  X-HPKE-Enabled: $HPKE_HDR"
HTTP_CODE=$(curl -sS -o "$RESP_PAYLOAD" -w "%{http_code}" \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: $SAGE_HDR" \
  -H "X-HPKE-Enabled: $HPKE_HDR" \
  -H "X-Scenario: user" \
  -X POST "http://${HOST}:${CLIENT_PORT}/api/request" \
  --data-binary @"$REQ_PAYLOAD" || true)

echo "[HTTP] client-api status: $HTTP_CODE"

echo "\n========== Client API Response (summary) =========="
if command -v jq >/dev/null 2>&1; then
  jq '{response:.response, verification:(.SAGEVerification // .sageVerification // null), metadata:.metadata, logs:(.logs // null)}' "$RESP_PAYLOAD" 2>/dev/null || cat "$RESP_PAYLOAD"
else
  cat "$RESP_PAYLOAD"
fi
echo "==================================================="

if [[ -f "$GW_LOG" ]]; then
  echo "\n----- Gateway new logs (since request) -----"
  START=$((PRE_GW+1))
  tail -n +"$START" "$GW_LOG" || true
else
  echo "(gateway log not found: $GW_LOG)"
fi

if [[ -f "$ROOT_LOG" ]]; then
  echo "\n----- Root new logs (since request) -----"
  START=$((PRE_ROOT+1))
  tail -n +"$START" "$ROOT_LOG" || true
else
  echo "(root log not found: $ROOT_LOG)"
fi

if [[ -f "$EXT_LOG" ]]; then
  echo "\n----- External-payment new logs (since request) -----"
  START=$((PRE_EXT+1))
  tail -n +"$START" "$EXT_LOG" || true
else
  echo "(external log not found: $EXT_LOG)"
fi

echo "\n[HINT] Check logs/root.log and logs/external-payment.log for full trace."
