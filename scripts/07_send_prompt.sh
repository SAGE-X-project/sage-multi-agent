#!/usr/bin/env bash
# Send a prompt to the running Client API and (optionally) interact until Root collects info
# and routes to external agents. Shows new Gateway/Root/Medical/Payment logs after each turn.
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
# - Default: when an external forward is detected, .cid is deleted automatically.
#   Disable this behavior with --keep-cid (or --no-clear-cid-on-external).

[ -n "$BASH_VERSION" ] || exec bash "$0" "$@"
set -Eeuo pipefail

# ---------- helpers ----------
strip_cr() { printf '%s' "$1" | tr -d '\r'; }
trim_ws()  { awk '{$1=$1; print}' <<<"$1"; }

# ---------- env & defaults ----------
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

# Hard defaults for set -u safety
: "${PROMPT:=}"
: "${PROMPT_FILE:=}"
: "${INTERACTIVE:=0}"
: "${SAGE:=off}"
: "${HPKE:=off}"
: "${SCENARIO:=user}"
: "${HOST:=localhost}"
: "${CLIENT_PORT:=8086}"
: "${ROOT_AGENT_PORT:=18080}"
: "${GATEWAY_PORT:=5500}"
: "${CID_FILE:=.cid}"
: "${CLEAR_CID_ON_EXTERNAL:=1}"   # <— default ON

# Normalize env values (remove CR/LF, trim)
HOST="$(trim_ws "$(strip_cr "${HOST}")")"
CLIENT_PORT="$(trim_ws "$(strip_cr "${CLIENT_PORT}")")"
ROOT_PORT="$(trim_ws "$(strip_cr "${ROOT_AGENT_PORT}")")"
GATEWAY_PORT="$(trim_ws "$(strip_cr "${GATEWAY_PORT}")")"

SAGE="$(trim_ws "$(strip_cr "${SAGE}")")"
HPKE="$(trim_ws "$(strip_cr "${HPKE}")")"
SCENARIO="$(trim_ws "$(strip_cr "${SCENARIO}")")"
CID_FILE="$(trim_ws "$(strip_cr "${CID_FILE}")")"

usage() {
  cat <<EOF
Usage: $0 [--sage on|off] [--hpke on|off] [--prompt "<text>"] [--prompt-file <path>] [--scenario <name>] [--payment] [--interactive|-i] [--keep-cid]
EOF
}

# ---------- args ----------
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
    --new-cid)
      CID="cid-$(date +%s%N)"
      echo "$CID" > "$CID_FILE"
      shift ;;
    --keep-cid|--no-clear-cid-on-external)
      CLEAR_CID_ON_EXTERNAL=0; shift ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

# ---------- prompt ----------
if [[ -z "$PROMPT" ]]; then
  if [[ -n "${PROMPT_FILE}" ]]; then
    PROMPT="$(cat "$PROMPT_FILE")"
  elif ! tty -s; then
    PROMPT="$(cat -)"
  fi
fi
# default prompt to exercise payment path
if [[ -z "$PROMPT" ]]; then
  PROMPT="send 10 USDC to merchant"
fi

# ---------- headers ----------
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

# ---------- temp files ----------
REQ_PAYLOAD="$(mktemp -t prompt.req.XXXX.json)"
RESP_PAYLOAD="$(mktemp -t prompt.resp.XXXX.json)"

# ---------- logs ----------
GW_LOG="logs/gateway.log"
ROOT_LOG="logs/root.log"
MED_LOG="logs/medical.log"
PAY_LOG="logs/payment.log"
get_lines() { [[ -f "$1" ]] && wc -l < "$1" || echo 0; }

# ---------- conversation id ----------
ensure_cid() {
  if [[ -s "$CID_FILE" ]]; then
    CID="$(cat "$CID_FILE")"
  else
    CID="cid-$(date +%s%N)"
    echo "$CID" > "$CID_FILE"
  fi
}

# ---------- send once ----------
send_once() {
  local text="$1"

  # Snapshot log lines BEFORE request
  local PRE_GW PRE_ROOT PRE_MED PRE_PAY
  PRE_GW=$(get_lines "$GW_LOG")
  PRE_ROOT=$(get_lines "$ROOT_LOG")
  PRE_MED=$(get_lines "$MED_LOG")
  PRE_PAY=$(get_lines "$PAY_LOG")

  # CID for this turn
  ensure_cid

  # Write JSON body (with conversationId)
  if command -v jq >/dev/null 2>&1; then
    printf '{"prompt": %s, "conversationId": %s}\n' \
      "$(jq -Rsa . <<<"$text")" \
      "$(jq -R . <<<"$CID")" > "$REQ_PAYLOAD"
  else
    local esc=${text//\\/\\\\}; esc=${esc//\"/\\\"}
    printf '{"prompt":"%s","conversationId":"%s"}\n' "$esc" "$CID" > "$REQ_PAYLOAD"
  fi

  echo "[REQ] POST /api/request  X-SAGE-Enabled: $SAGE_HDR  X-HPKE-Enabled: $HPKE_HDR  X-Scenario: $SCENARIO"
  local code
  code=$(curl -sS -o "$RESP_PAYLOAD" -w "%{http_code}" \
    -H "Content-Type: application/json" \
    -H "X-SAGE-Enabled: $SAGE_HDR" \
    -H "X-HPKE-Enabled: $HPKE_HDR" \
    -H "X-Scenario: $SCENARIO" \
    -H "X-Conversation-ID: $CID" \
    -H "X-SAGE-Context-ID: $CID" \
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

  # Show new logs since previous turn — capture to variables for detection
  local GW_NEW="" ROOT_NEW="" MED_NEW="" PAY_NEW=""

  if [[ -f "$GW_LOG" ]]; then
    echo
    echo "----- Gateway new logs (since request) -----"
    local start=$((PRE_GW+1))
    GW_NEW="$(tail -n +"$start" "$GW_LOG" || true)"
    printf "%s\n" "$GW_NEW"
  else
    echo "(gateway log not found: $GW_LOG)"
  fi

  if [[ -f "$ROOT_LOG" ]]; then
    echo
    echo "----- Root new logs (since request) -----"
    local start=$((PRE_ROOT+1))
    ROOT_NEW="$(tail -n +"$start" "$ROOT_LOG" || true)"
    printf "%s\n" "$ROOT_NEW"
  else
    echo "(root log not found: $ROOT_LOG)"
  fi

  if [[ -f "$MED_LOG" ]]; then
    echo
    echo "----- Medical service new logs (since request) -----"
    local start=$((PRE_MED+1))
    MED_NEW="$(tail -n +"$start" "$MED_LOG" || true)"
    printf "%s\n" "$MED_NEW"
  else
    echo "(medical log not found: $MED_LOG)"
  fi

  if [[ -f "$PAY_LOG" ]]; then
    echo
    echo "----- Payment service new logs (since request) -----"
    local start=$((PRE_PAY+1))
    PAY_NEW="$(tail -n +"$start" "$PAY_LOG" || true)"
    printf "%s\n" "$PAY_NEW"
  else
    echo "(payment log not found: $PAY_LOG)"
  fi

  echo
  echo "[HINT] Check logs/root.log, logs/medical.log and logs/payment.log for full trace."

  # --- Detect external forwarding in the NEW logs we just printed ---
  if [[ "${CLEAR_CID_ON_EXTERNAL}" -eq 1 ]]; then
    external_sent=0
    # 1) Gateway OUTBOUND to external agents
    if grep -Eq 'GW OUTBOUND >>> POST .*\/(payment|medical|planning)\/process' <<<"$GW_NEW"; then
      external_sent=1
    fi
    # 2) Root-side explicit forward/send markers
    if [[ $external_sent -eq 0 ]]; then
      if grep -Ei '\[root\]\[(payment|medical|planning)\]\[(send|forward)\].*(sendExternal|-> external)' <<<"$ROOT_NEW"; then
        external_sent=1
      fi
    fi
    if [[ $external_sent -eq 1 ]]; then
      rm -f "$CID_FILE" 2>/dev/null || true
      echo "[CID] cleared (.cid) because this turn was forwarded to an external agent"
      CID=""
    fi
  fi
}

# ---------- interactive guard ----------
should_continue() {
  # returns 0 to continue (need more info), 1 to stop
  if ! command -v jq >/dev/null 2>&1; then
    read -r -p "[NEXT] Provide more info (empty = stop): " NEXT
    if [[ -z "${NEXT}" ]]; then
      return 1
    else
      PROMPT="$NEXT"
      return 0
    fi
  fi

  local need_more routed final stage done_flag
  need_more=$(jq -r '(.metadata.requiresMoreInfo // .metadata.needMore // .metadata.need_input // false)' "$RESP_PAYLOAD" 2>/dev/null || echo "false")
  routed=$(jq -r '(.metadata.route // .metadata.nextAgent // .metadata.routed_to // .metadata.target // "")' "$RESP_PAYLOAD" 2>/dev/null || echo "")
  final=$(jq -r '(.metadata.final // .metadata.final_agent // "")' "$RESP_PAYLOAD" 2>/dev/null || echo "")
  stage=$(jq -r '(.metadata.stage // "")' "$RESP_PAYLOAD" 2>/dev/null || echo "")
  done_flag=$(jq -r '(.metadata.done // false)' "$RESP_PAYLOAD" 2>/dev/null || echo "false")

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

  case "$(echo "$need_more" | tr '[:upper:]' '[:lower:]')" in
    true|1|yes|on)
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
    *)
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

# ---------- run ----------
if [[ "${INTERACTIVE:-0}" -eq 1 ]]; then
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
