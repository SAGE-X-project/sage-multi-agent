#!/usr/bin/env bash
# One-click launcher: 02_start_agents.sh (medical+payment) -> Gateway -> Root -> Client
# Modes: SAGE on|off, Gateway tamper|pass
# LLM: OpenAI (default). Set LLM_PROVIDER=openai|gemini

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

mkdir -p logs pids

# ---------- Helpers ----------
kill_port() {
  local port="$1"; local pids
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -z "$pids" ]] && return
  echo "[kill] :$port -> $pids"
  kill $pids 2>/dev/null || true
  sleep 0.2
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -n "$pids" ]] && kill -9 $pids >/dev/null 2>&1 || true
}
wait_http() {
  local url="$1" tries="${2:-120}" delay="${3:-0.25}"   # 30s
  for ((i=1;i<=tries;i++)); do
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then return 0; fi
    sleep "$delay"
  done; return 1
}
wait_tcp() {
  local host="$1" port="$2" tries="${3:-120}" delay="${4:-0.25}"
  for ((i=1;i<=tries;i++)); do
    if { exec 3<>/dev/tcp/"$host"/"$port"; } >/dev/null 2>&1; then exec 3>&- 3<&-; return 0; fi
    sleep "$delay"
  done; return 1
}
to_bool() { local v="${1:-}"; v="$(echo "$v" | tr '[:upper:]' '[:lower:]')"; case "$v" in 1|true|on|yes) echo "true";; 0|false|off|no) echo "false";; *) echo "$2";; esac; }
normalize_base() { echo "${1%/}"; }
require() { command -v "$1" >/dev/null 2>&1 || { echo "[ERR] '$1' not found"; exit 1; }; }
require go; require curl; require lsof

# ---------- Ports ----------
HOST="${HOST:-localhost}"
EXT_MEDICAL_PORT="${EXT_MEDICAL_PORT:-${MEDICAL_AGENT_PORT:-19082}}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-${PAYMENT_AGENT_PORT:-19083}}"
GATEWAY_PORT="${GATEWAY_PORT:-5500}"
ROOT_PORT="${ROOT_PORT:-${ROOT_AGENT_PORT:-18080}}"
CLIENT_PORT="${CLIENT_PORT:-8086}"

# ---------- Modes ----------
SAGE_MODE="${SAGE_MODE:-off}"      # on|off
GATEWAY_MODE="tamper"              # tamper|pass
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-$'\n[GW-ATTACK] injected by gateway'}}"

# ---------- LLM (OpenAI default; provider switchable) ----------
LLM_ENABLED="${LLM_ENABLED:-true}"
LLM_PROVIDER="$(echo "${LLM_PROVIDER:-openai}" | tr '[:upper:]' '[:lower:]')"

# OpenAI defaults
OPENAI_BASE_URL="$(normalize_base "${OPENAI_BASE_URL:-https://api.openai.com/v1}")"
OPENAI_API_KEY="${OPENAI_API_KEY:-${LLM_API_KEY:-}}"
OPENAI_MODEL="${OPENAI_MODEL:-${LLM_MODEL:-gpt-4o-mini}}"

# Gemini (optional) — only used if LLM_PROVIDER=gemini
GEMINI_API_URL="$(normalize_base "${GEMINI_API_URL:-https://generativelanguage.googleapis.com/v1beta/openai}")"
GEMINI_API_KEY="${GEMINI_API_KEY:-${GOOGLE_API_KEY:-}}"
GEMINI_MODEL="${GEMINI_MODEL:-gemini-2.5-flash}"

# Timeout for LLM client (llm.NewFromEnv reads LLM_TIMEOUT or LLM_TIMEOUT_MS)
LLM_TIMEOUT="${LLM_TIMEOUT:-12s}"

# ---------- CLI args ----------
DEBUG_FLAG=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --tamper)                 GATEWAY_MODE="tamper"; shift ;;
    --pass|--passthrough)     GATEWAY_MODE="pass"; shift ;;
    --attack-msg)             ATTACK_MESSAGE="${2:-}"; shift 2 ;;
    --sage=*)                 SAGE_MODE="$(printf '%s' "${1#*=}" | tr '[:upper:]' '[:lower:]')"; shift ;;
    --sage)                   SAGE_MODE="$(printf '%s' "${2:-on}" | tr '[:upper:]' '[:lower:]')"; shift 2 ;;
    --medical-port)           EXT_MEDICAL_PORT="${2:-$EXT_MEDICAL_PORT}"; shift 2 ;;
    --payment-port)           EXT_PAYMENT_PORT="${2:-$EXT_PAYMENT_PORT}"; shift 2 ;;
    --llm)                    LLM_ENABLED="$(to_bool "${2:-$LLM_ENABLED}" "$LLM_ENABLED")"; shift 2 ;;
    --llm-on)                 LLM_ENABLED="true"; shift ;;
    --llm-off)                LLM_ENABLED="false"; shift ;;
    --llm-provider)           LLM_PROVIDER="$(echo "${2:-$LLM_PROVIDER}" | tr '[:upper:]' '[:lower:]')"; shift 2 ;;
    --llm-base)               # set base for current provider
                              if [[ "$LLM_PROVIDER" == "gemini" ]]; then GEMINI_API_URL="$(normalize_base "${2:-$GEMINI_API_URL}")"
                              else OPENAI_BASE_URL="$(normalize_base "${2:-$OPENAI_BASE_URL}")"; fi; shift 2 ;;
    --llm-key)                if [[ "$LLM_PROVIDER" == "gemini" ]]; then GEMINI_API_KEY="${2:-}"
                              else OPENAI_API_KEY="${2:-}"; fi; shift 2 ;;
    --llm-model)              if [[ "$LLM_PROVIDER" == "gemini" ]]; then GEMINI_MODEL="${2:-$GEMINI_MODEL}"
                              else OPENAI_MODEL="${2:-$OPENAI_MODEL}"; fi; shift 2 ;;
    --llm-timeout)            LLM_TIMEOUT="${2:-$LLM_TIMEOUT}"; shift 2 ;;
    --debug)                  DEBUG_FLAG=1; shift ;;
    -h|--help)
      cat <<USAGE
Usage: $0 [--tamper|--pass] [--attack-msg <txt>] [--sage on|off]
          [--medical-port N] [--payment-port N]
          [--llm on|off] [--llm-provider openai|gemini]
          [--llm-base URL] [--llm-key KEY] [--llm-model MODEL] [--llm-timeout 12s] [--debug]
USAGE
      exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done
if [[ "$DEBUG_FLAG" == "1" ]]; then set -x; export PS4='[06:${LINENO}] '; fi

# Require key unless localhost or explicitly allowed
ALLOW_NO_KEY="${LLM_ALLOW_NO_KEY:-false}"
if [[ "$(to_bool "$LLM_ENABLED" true)" == "true" ]]; then
  case "$LLM_PROVIDER" in
    openai)
      if [[ -z "${OPENAI_API_KEY:-}" ]]; then
        if [[ "$ALLOW_NO_KEY" != "true" && "$OPENAI_BASE_URL" != *"localhost"* && "$OPENAI_BASE_URL" != *"127.0.0.1"* ]]; then
          echo "[ERR] Set OPENAI_API_KEY (or LLM_API_KEY)."; exit 1
        fi
      fi
      ;;
    gemini)
      if [[ -z "${GEMINI_API_KEY:-}" ]]; then
        if [[ "$ALLOW_NO_KEY" != "true" && "$GEMINI_API_URL" != *"localhost"* && "$GEMINI_API_URL" != *"127.0.0.1"* ]]; then
          echo "[ERR] Set GEMINI_API_KEY (or GOOGLE_API_KEY)."; exit 1
        fi
      fi
      ;;
  esac
fi

echo "[CFG] Ports: medical=${EXT_MEDICAL_PORT} payment=${EXT_PAYMENT_PORT} gateway=${GATEWAY_PORT} root=${ROOT_PORT} client=${CLIENT_PORT}"
echo "[CFG] SAGE_MODE=${SAGE_MODE} GATEWAY_MODE=${GATEWAY_MODE}"
if [[ "$LLM_PROVIDER" == "gemini" ]]; then
  echo "[LLM] provider=gemini enabled=${LLM_ENABLED} base=${GEMINI_API_URL} model=${GEMINI_MODEL} timeout=${LLM_TIMEOUT}"
else
  echo "[LLM] provider=openai enabled=${LLM_ENABLED} base=${OPENAI_BASE_URL} model=${OPENAI_MODEL} timeout=${LLM_TIMEOUT}"
fi

# Bridge LLM env for all processes
export LLM_PROVIDER LLM_TIMEOUT
if [[ "$LLM_PROVIDER" == "gemini" ]]; then
  export GEMINI_API_URL GEMINI_MODEL
  if [[ "$(to_bool "$LLM_ENABLED" true)" == "true" ]]; then export GEMINI_API_KEY; else unset GEMINI_API_KEY; fi
  # Clear OpenAI to avoid accidental precedence
  unset OPENAI_BASE_URL OPENAI_API_KEY OPENAI_MODEL
else
  export OPENAI_BASE_URL OPENAI_MODEL
  if [[ "$(to_bool "$LLM_ENABLED" true)" == "true" ]]; then export OPENAI_API_KEY; else unset OPENAI_API_KEY; fi
  # Clear Gemini to avoid accidental precedence
  unset GEMINI_API_URL GEMINI_API_KEY GEMINI_MODEL GEMINI_TIMEOUT
fi

# ---------- Kill any previous ----------
kill_port "${EXT_MEDICAL_PORT}"
kill_port "${EXT_PAYMENT_PORT}"
kill_port "${GATEWAY_PORT}"
kill_port "${ROOT_PORT}"
kill_port "${CLIENT_PORT}"

# ---------- (A) Start Agents via 02_start_agents.sh (ALWAYS with bash) ----------
# Pass generic flags; 02_start_agents.sh should just forward env or use these values.
if [[ "$LLM_PROVIDER" == "gemini" ]]; then
  AGENT_BASE="$GEMINI_API_URL"; AGENT_MODEL="$GEMINI_MODEL"; AGENT_KEY="${GEMINI_API_KEY:-}"
else
  AGENT_BASE="$OPENAI_BASE_URL"; AGENT_MODEL="$OPENAI_MODEL"; AGENT_KEY="${OPENAI_API_KEY:-}"
fi

AGENT_ARGS=(
  --medical-port  "${EXT_MEDICAL_PORT}"
  --payment-port  "${EXT_PAYMENT_PORT}"
  --llm "$(to_bool "$LLM_ENABLED" true)"
  --llm-base "${AGENT_BASE}"
  --llm-model "${AGENT_MODEL}"
)
[[ -n "${AGENT_KEY:-}" && "$(to_bool "$LLM_ENABLED" true)" == "true" ]] && AGENT_ARGS+=( --llm-key "${AGENT_KEY}" )
[[ "$DEBUG_FLAG" == "1" ]] && AGENT_ARGS+=( --debug )

echo "[CALL] scripts/02_start_agents.sh ${AGENT_ARGS[*]}"
bash scripts/02_start_agents.sh "${AGENT_ARGS[@]}"

# Quick health re-check
for url in \
  "http://${HOST}:${EXT_MEDICAL_PORT}/status" \
  "http://${HOST}:${EXT_PAYMENT_PORT}/status"
do
  wait_http "$url" 20 0.2 || echo "[WARN] not ready: $url"
done

# ---------- (B) Gateway ----------
if [[ "$GATEWAY_MODE" == "pass" ]]; then
  echo "[mode] Gateway PASS-THROUGH"
  nohup env -u ATTACK_MESSAGE go run ./cmd/gateway/main.go \
    -listen ":${GATEWAY_PORT}" \
    -pay-upstream "http://${HOST}:${EXT_PAYMENT_PORT}" \
    -med-upstream "http://${HOST}:${EXT_MEDICAL_PORT}" \
    -attack-msg="" \
    >"logs/gateway.log" 2>&1 & echo $! > "pids/gateway.pid"
else
  echo "[mode] Gateway TAMPER"
  nohup env ATTACK_MESSAGE="${ATTACK_MESSAGE}" go run ./cmd/gateway/main.go \
    -listen ":${GATEWAY_PORT}" \
    -pay-upstream "http://${HOST}:${EXT_PAYMENT_PORT}" \
    -med-upstream "http://${HOST}:${EXT_MEDICAL_PORT}" \
    -attack-msg "${ATTACK_MESSAGE}" \
    >"logs/gateway.log" 2>&1 & echo $! > "pids/gateway.pid"
fi
wait_tcp "$HOST" "$GATEWAY_PORT" 120 0.25 || { echo "[FAIL] gateway"; tail -n 200 logs/gateway.log || true; exit 1; }

# ---------- (C) Root ----------
export PAYMENT_URL="http://localhost:${GATEWAY_PORT}/payment"
export MEDICAL_URL="http://localhost:${GATEWAY_PORT}/medical"
export ROOT_JWK_FILE="${ROOT_JWK_FILE:-${PAYMENT_JWK_FILE:-./keys/payment.jwk}}"
export HPKE_KEYS_FILE="${HPKE_KEYS_FILE:-merged_agent_keys.json}"

ROOT_SAGE="$(to_bool "${SAGE_MODE:-off}" false)"

echo "[START] root     :${ROOT_PORT} (sage=${ROOT_SAGE})"
# --- Pin Root's LLM to OpenAI ---
OPENAI_API_BASE="${OPENAI_API_BASE:-https://api.openai.com/v1}"
OPENAI_MODEL="${OPENAI_MODEL:-gpt-4o-mini}"
# You can export OPENAI_API_KEY before running or put it in .env.

nohup env \
  PAYMENT_URL="${PAYMENT_URL}" \
  MEDICAL_URL="${MEDICAL_URL}" \
  ROOT_SAGE_ENABLED="${ROOT_SAGE}" \
  ROOT_HPKE="auto" \
  ROOT_JWK_FILE="${ROOT_JWK_FILE}" \
  HPKE_KEYS_FILE="${HPKE_KEYS_FILE}" \
  OPENAI_API_BASE="${OPENAI_API_BASE}" \
  OPENAI_API_KEY="${OPENAI_API_KEY:-}" \
  OPENAI_MODEL="${OPENAI_MODEL}" \
  go run ./cmd/root/main.go -port "${ROOT_PORT}" -sage "${ROOT_SAGE}" \
  >"logs/root.log" 2>&1 & echo $! > "pids/root.pid"


wait_http "http://${HOST}:${ROOT_PORT}/status" 120 0.25 || { echo "[FAIL] root /status"; tail -n 200 logs/root.log || true; exit 1; }

# ---------- (D) Client API ----------
echo "[START] client   :${CLIENT_PORT}"
nohup go run ./cmd/client/main.go -port "${CLIENT_PORT}" -root "http://${HOST}:${ROOT_PORT}" \
  >"logs/client.log" 2>&1 & echo $! > "pids/client.pid"
wait_http "http://${HOST}:${CLIENT_PORT}/api/sage/config" 120 0.25 || echo "[WARN] client not ready"

echo "--------------------------------------------------"
echo "[OK] medical  : http://${HOST}:${EXT_MEDICAL_PORT}/status"
echo "[OK] payment  : http://${HOST}:${EXT_PAYMENT_PORT}/status  (gateway :${GATEWAY_PORT})"
echo "[OK] root     : http://${HOST}:${ROOT_PORT}/status"
echo "[OK] client   : http://${HOST}:${CLIENT_PORT}/api/sage/config"
for n in medical payment gateway root client; do
  [[ -f "pids/${n}.pid" ]] && printf "  %-8s %s\n" "$n" "$(cat "pids/${n}.pid")"
done
echo "Logs: ./logs/*.log"
