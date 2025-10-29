#!/usr/bin/env bash
# Send a prompt to the running Client API and (optionally) interact until Root collects info
# and routes to Payment. Shows new Gateway/Root/Payment logs after each turn.
#
# Usage:
#   scripts/07_send_prompt.sh [--sage on|off] [--hpke on|off] [--prompt "<text>"]
#   scripts/07_send_prompt.sh --prompt-file prompt.txt
#   scripts/07_send_prompt.sh --interactive
#   echo "send 10 USDC" | scripts/07_send_prompt.sh -i
#
# Notes:
# - SAGE/HPKE headers are per-request and preserved across turns.
# - If jq is available, we auto-detect "need more info" and "routed to payment".

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
SCENARIO="user"
INTERACTIVE=0   # NEW

usage() {
  cat <<EOF
Usage: $0 [--sage on|off] [--hpke on|off] [--prompt "<text>"] [--prompt-file <path>] [--scenario <name>] [--payment] [--interactive|-i]
If no --prompt/--prompt-file given, reads stdin.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --sage=*)      SAGE="${1#*=}"; shift ;;
    --sage)        SAGE="${2:-off}"; shift 2 ;;
    --hpke=*)      HPKE="${1#*=}"; shift ;;
    --hpke)        HPKE="${2:-off}"; shift 2 ;;
    --prompt)      PROMPT="${2:-}"; shift 2 ;;
    --prompt-file) PROMPT_FILE="${2:-}"; shift 2 ;;
    --scenario=*)  SCENARIO="${1#*=}"; shift ;;
    --scenario)    SCENARIO="${2:-user}"; shift 2 ;;
    --payment)     PROMPT="send 10 USDC to merchant"; shift ;;
    --interactive|-i) INTERACTIVE=1; shift ;;
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

# Default prompt targets payment so HPKE/SAGE path is visible
if [[ -z "$PROMPT" ]]; then
  PROMPT="send 10 USDC to merchant"
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

REQ_PAYLOAD="$(mktemp -t prompt.req.XXXX.json)"
RESP_PAYLOAD="$(mktemp -t prompt.resp.XXXX.json)"

GW_LOG="logs/gateway.log"
ROOT_LOG="logs/root.log"
PAY_LOG="logs/payment.log"

# Track log offsets across turns
get_lines() { [[ -f "$1" ]] && wc -l < "$1" || echo 0; }
PRE_GW=$(get_lines "$GW_LOG")
PRE_ROOT=$(get_lines "$ROOT_LOG")
PRE_PAY=$(get_lines "$PAY_LOG")

send_once() {
  local text="$1"
  # write request JSON (use jq if available)
  if command -v jq >/dev/null 2>&1; then
    printf '{"prompt": %s}\n' "$(jq -Rsa . <<<"$text")" > "$REQ_PAYLOAD"
  else
    local esc=${text//\\/\\\\}; esc=${esc//\"/\\\"}
    printf '{"prompt":"%s"}\n' "$esc" > "$REQ_PAYLOAD"
  fi

  echo "[REQ] POST /api/request  X-SAGE-Enabled: $SAGE_HDR  X-HPKE-Enabled: $HPKE_HDR  X-Scenario: $SCENARIO"
  local code
  code=$(curl -sS -o "$RESP_PAYLOAD" -w "%{http_code}" \
    -H "Content-Type: application/json" \
    -H "X-SAGE-Enabled: $SAGE_HDR" \
    -H "X-HPKE-Enabled: $HPKE_HDR" \
    -H "X-Scenario: $SCENARIO" \
    -X POST "http://${HOST}:${CLIENT_PORT}/api/request" \
    --data-binary @"$REQ_PAYLOAD" || true)
  echo "[HTTP] client-api status: $code"

  echo
  echo "========== Client API Response (summary) =========="
  if command -v jq >/dev/null 2>&1; then
    jq '{response:.response, verification:(.SAGEVerification // .sageVerification // null), metadata:.metadata, logs:(.logs // null)}' "$RESP_PAYLOAD" 2>/dev/null || cat "$RESP_PAYLOAD"
  else
    cat "$RESP_PAYLOAD"
  fi
  echo "==================================================="

  # Show new logs since previous turn
  if [[ -f "$GW_LOG" ]]; then
    echo
    echo "----- Gateway new logs (since request) -----"
    local start=$((PRE_GW+1))
    tail -n +"$start" "$GW_LOG" || true
    PRE_GW=$(get_lines "$GW_LOG")
  else
    echo "(gateway log not found: $GW_LOG)"
  fi

  if [[ -f "$ROOT_LOG" ]]; then
    echo
    echo "----- Root new logs (since request) -----"
    local start=$((PRE_ROOT+1))
    tail -n +"$start" "$ROOT_LOG" || true
    PRE_ROOT=$(get_lines "$ROOT_LOG")
  else
    echo "(root log not found: $ROOT_LOG)"
  fi

  if [[ -f "$PAY_LOG" ]]; then
    echo
    echo "----- Payment service new logs (since request) -----"
    local start=$((PRE_PAY+1))
    tail -n +"$start" "$PAY_LOG" || true
    PRE_PAY=$(get_lines "$PAY_LOG")
  else
    echo "(payment log not found: $PAY_LOG)"
  fi
}

should_continue() {
  # returns 0 to continue (need more info), 1 to stop
  if ! command -v jq >/dev/null 2>&1; then
    # Without jq, ask user manually
    read -r -p "[NEXT] Provide more info (empty = stop): " NEXT
    if [[ -z "$NEXT" ]]; then
      return 1
    else
      PROMPT="$NEXT"
      return 0
    fi
  fi

  # With jq: detect "need more info" and "payment routed"
  local need_more routed final stage done_flag
  need_more=$(jq -r '(.metadata.requiresMoreInfo // .metadata.needMore // .metadata.need_input // false)' "$RESP_PAYLOAD" 2>/dev/null || echo "false")
  routed=$(jq -r '(.metadata.route // .metadata.nextAgent // .metadata.routed_to // .metadata.target // "")' "$RESP_PAYLOAD" 2>/dev/null || echo "")
  final=$(jq -r '(.metadata.final // .metadata.final_agent // "")' "$RESP_PAYLOAD" 2>/dev/null || echo "")
  stage=$(jq -r '(.metadata.stage // "")' "$RESP_PAYLOAD" 2>/dev/null || echo "")
  done_flag=$(jq -r '(.metadata.done // false)' "$RESP_PAYLOAD" 2>/dev/null || echo "false")

  # If clearly routed to payment or finished, stop
  case ",${routed},${final},${stage}," in
    *",payment,"*|*",done,"*|*",final,"*|*",completed,"*)
      echo "[ROUTED] metadata indicates Payment/done. Ending interaction."
      return 1
      ;;
  esac

  if [[ "$done_flag" == "true" ]]; then
    echo "[DONE] metadata.done=true. Ending interaction."
    return 1
  fi

  # If need_more is true, ask user for more info
  case "$(echo "$need_more" | tr '[:upper:]' '[:lower:]')" in
    true|1|yes|on)
      # If server provided explicit questions, show them
      local qcount
      qcount=$(jq -r '(.metadata.questions // .metadata.missing // []) | length' "$RESP_PAYLOAD" 2>/dev/null || echo 0)
      if [[ "$qcount" -gt 0 ]]; then
        echo
        echo ">> Missing info from Root:"
        jq -r '(.metadata.questions // .metadata.missing // []) | to_entries[] | "\(.key+1). \(.value)"' "$RESP_PAYLOAD" 2>/dev/null || true
      fi
      read -r -p "[NEXT] Provide info: " NEXT
      PROMPT="$NEXT"
      [[ -z "$PROMPT" ]] && return 1 || return 0
      ;;
    *) # otherwise stop unless user wants to continue
      read -r -p "[NEXT] Continue with another message? (leave empty to stop): " NEXT
      if [[ -z "$NEXT" ]]; then
        return 1
      else
        PROMPT="$NEXT"
        return 0
      fi
      ;;
  esac
}

# --------- Run (single or interactive) ---------
if [[ "$INTERACTIVE" -eq 1 ]]; then
  turn=1
  while : ; do
    echo
    echo "===== TURN $turn ====="
    send_once "$PROMPT"
    if ! should_continue; then
      break
    fi
    turn=$((turn+1))
  done
else
  send_once "$PROMPT"
fi

echo
echo "[HINT] Check logs/root.log and logs/payment.log for full trace."
