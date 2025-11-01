#!/usr/bin/env bash
# One-click launcher: 02_start_agents.sh (medical+payment) -> Gateway -> Root -> Client
# Modes: SAGE on|off, Gateway tamper|pass
# LLM: JAMINAI_* ONLY

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
  [[ -n "$pids" ]] && kill -9 $pids 2>&1 >/dev/null || true
}
wait_http() {
  local url="$1" tries="${2:-40}" delay="${3:-0.25}"
  for ((i=1;i<=tries;i++)); do
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then return 0; fi
    sleep "$delay"
  done; return 1
}
wait_tcp() {
  local host="$1" port="$2" tries="${3:-60}" delay="${4:-0.2}"
  for ((i=1;i<=tries;i++)); do
    if { exec 3<>/dev/tcp/"$host"/"$port"; } >/dev/null 2>&1; then exec 3>&- 3<&-; return 0; fi
    sleep "$delay"
  done; return 1
}
to_bool() {
  local v="${1:-}"; v="$(echo "$v" | tr '[:upper:]' '[:lower:]')"
  case "$v" in 1|true|on|yes) echo "true";; 0|false|off|no) echo "false";; *) echo "$2";; esac
}
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

# ---------- LLM (JAMINAI_* ONLY, OpenAI-compatible path) ----------
LLM_ENABLED="${LLM_ENABLED:-true}"
JAMINAI_API_URL="$(normalize_base "${JAMINAI_API_URL:-https://generativelanguage.googleapis.com/v1beta/openai}")"
if [[ -z "${JAMINAI_API_KEY:-}" && -n "${GEMINI_API_KEY:-}" ]]; then
  JAMINAI_API_KEY="${GEMINI_API_KEY}"
fi
if [[ -z "${JAMINAI_API_KEY:-}" && -n "${GOOGLE_API_KEY:-}" ]]; then
  JAMINAI_API_KEY="${GOOGLE_API_KEY}"
fi
JAMINAI_MODEL="${JAMINAI_MODEL:-gemini-2.5-flash}"
JAMINAI_TIMEOUT="${JAMINAI_TIMEOUT:-12s}"

# ---------- CLI args ----------
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
    --llm-base)               JAMINAI_API_URL="$(normalize_base "${2:-$JAMINAI_API_URL}")"; shift 2 ;;
    --llm-key)                JAMINAI_API_KEY="${2:-}"; shift 2 ;;
    --llm-model)              JAMINAI_MODEL="${2:-$JAMINAI_MODEL}"; shift 2 ;;
    --llm-timeout)            JAMINAI_TIMEOUT="${2:-$JAMINAI_TIMEOUT}"; shift 2 ;;
    -h|--help)
      cat <<USAGE
Usage: $0 [--tamper|--pass] [--attack-msg <txt>] [--sage on|off]
          [--medical-port N] [--payment-port N]
          [--llm on|off] [--llm-base URL] [--llm-key KEY] [--llm-model MODEL] [--llm-timeout 12s]
USAGE
      exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

if [[ "$(to_bool "$LLM_ENABLED" true)" == "true" && -z "${JAMINAI_API_KEY:-}" ]]; then
  echo "[ERR] Set JAMINAI_API_KEY (or GEMINI_API_KEY/GOOGLE_API_KEY)."
  exit 1
fi

echo "[CFG] Ports: medical=${EXT_MEDICAL_PORT} payment=${EXT_PAYMENT_PORT} gateway=${GATEWAY_PORT} root=${ROOT_PORT} client=${CLIENT_PORT}"
echo "[CFG] SAGE_MODE=${SAGE_MODE} GATEWAY_MODE=${GATEWAY_MODE}"
echo "[LLM] enabled=${LLM_ENABLED} base=${JAMINAI_API_URL} model=${JAMINAI_MODEL} timeout=${JAMINAI_TIMEOUT}"

# Bridge to JAMINAI_* for all processes
export JAMINAI_API_URL JAMINAI_MODEL JAMINAI_TIMEOUT
if [[ "$(to_bool "$LLM_ENABLED" true)" == "true" ]]; then export JAMINAI_API_KEY; else unset JAMINAI_API_KEY; fi

# ---------- Kill any previous ----------
kill_port "${EXT_MEDICAL_PORT}"
kill_port "${EXT_PAYMENT_PORT}"
kill_port "${GATEWAY_PORT}"
kill_port "${ROOT_PORT}"
kill_port "${CLIENT_PORT}"

# ---------- (A) Start Agents via 02_start_agents.sh ----------
AGENT_ARGS=(
  --medical-port  "${EXT_MEDICAL_PORT}"
  --payment-port  "${EXT_PAYMENT_PORT}"
  --llm "$(to_bool "$LLM_ENABLED" true)"
  --llm-base "${JAMINAI_API_URL}"
  --llm-model "${JAMINAI_MODEL}"
)
[[ -n "${JAMINAI_API_KEY:-}" && "$(to_bool "$LLM_ENABLED" true)" == "true" ]] && AGENT_ARGS+=( --llm-key "${JAMINAI_API_KEY}" )

echo "[CALL] scripts/02_start_agents.sh ${AGENT_ARGS[*]}"
scripts/02_start_agents.sh "${AGENT_ARGS[@]}"

# Quick health re-check
for url in \
  "http://${HOST}:${EXT_MEDICAL_PORT}/status" \
  "http://${HOST}:${EXT_PAYMENT_PORT}/status"
do
  wait_http "$url" 20 0.2 || echo "[WARN] not ready: $url"
done

# ---------- (B) Gateway (fronting Payment) ----------
if [[ "$GATEWAY_MODE" == "pass" ]]; then
  echo "[mode] Gateway PASS-THROUGH"
  nohup env -u ATTACK_MESSAGE go run ./cmd/gateway/main.go \
    -listen ":${GATEWAY_PORT}" -upstream "http://${HOST}:${EXT_PAYMENT_PORT}" -attack-msg="" \
    >"logs/gateway.log" 2>&1 & echo $! > "pids/gateway.pid"
else
  echo "[mode] Gateway TAMPER"
  nohup env ATTACK_MESSAGE="${ATTACK_MESSAGE}" go run ./cmd/gateway/main.go \
    -listen ":${GATEWAY_PORT}" -upstream "http://${HOST}:${EXT_PAYMENT_PORT}" -attack-msg "${ATTACK_MESSAGE}" \
    >"logs/gateway.log" 2>&1 & echo $! > "pids/gateway.pid"
fi
wait_tcp "$HOST" "$GATEWAY_PORT" 60 0.2 || { echo "[FAIL] gateway"; tail -n 200 logs/gateway.log || true; exit 1; }

# ---------- (C) Root ----------
export PAYMENT_URL="http://localhost:${GATEWAY_PORT}"
export MEDICAL_EXTERNAL_URL="http://localhost:${EXT_MEDICAL_PORT}"
export ROOT_JWK_FILE="${ROOT_JWK_FILE:-${PAYMENT_JWK_FILE:-./keys/payment.jwk}}"

ROOT_SAGE="$(to_bool "$SAGE_MODE" false)"
echo "[START] root     :${ROOT_PORT} (sage=${ROOT_SAGE})"
nohup env \
  PAYMENT_URL="${PAYMENT_URL}" \
  MEDICAL_EXTERNAL_URL="${MEDICAL_EXTERNAL_URL}" \
  ROOT_SAGE_ENABLED="${ROOT_SAGE}" \
  ROOT_HPKE="false" \
  ROOT_JWK_FILE="${ROOT_JWK_FILE}" \
  JAMINAI_API_URL="${JAMINAI_API_URL}" \
  JAMINAI_API_KEY="${JAMINAI_API_KEY:-}" \
  JAMINAI_MODEL="${JAMINAI_MODEL}" \
  JAMINAI_TIMEOUT="${JAMINAI_TIMEOUT}" \
  go run ./cmd/root/main.go -port "${ROOT_PORT}" -sage "${ROOT_SAGE}" \
  >"logs/root.log" 2>&1 & echo $! > "pids/root.pid"

wait_http "http://${HOST}:${ROOT_PORT}/status" 40 0.25 || { echo "[FAIL] root /status"; tail -n 200 logs/root.log || true; exit 1; }

# ---------- (D) Client API ----------
echo "[START] client   :${CLIENT_PORT}"
nohup go run ./cmd/client/main.go -port "${CLIENT_PORT}" -root "http://${HOST}:${ROOT_PORT}" \
  >"logs/client.log" 2>&1 & echo $! > "pids/client.pid"
wait_http "http://${HOST}:${CLIENT_PORT}/api/sage/config" 40 0.25 || echo "[WARN] client not ready"

echo "--------------------------------------------------"
echo "[OK] medical  : http://${HOST}:${EXT_MEDICAL_PORT}/status"
echo "[OK] payment  : http://${HOST}:${EXT_PAYMENT_PORT}/status  (gateway :${GATEWAY_PORT})"
echo "[OK] root     : http://${HOST}:${ROOT_PORT}/status"
echo "[OK] client   : http://${HOST}:${CLIENT_PORT}/api/sage/config"
for n in medical payment gateway root client; do
  [[ -f "pids/${n}.pid" ]] && printf "  %-8s %s\n" "$n" "$(cat "pids/${n}.pid")"
done
echo "Logs: ./logs/*.log"
