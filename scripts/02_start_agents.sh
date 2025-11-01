#!/usr/bin/env bash
# Start external agents: Medical, Payment (verifier with optional HPKE & LLM).
# Exposes /status and /process on each service.
# Ports (defaults): medical=19082, payment=19083
# LLM: JAMINAI_* ONLY

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# Load .env for child processes
set -a
[[ -f .env ]] && source .env
set +a

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
  [[ -n "$pids" ]] && kill -9 $pids 2>/dev/null || true
}
wait_http() {
  local url="$1" tries="${2:-40}" delay="${3:-0.25}"
  for ((i=1;i<=tries;i++)); do
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then return 0; fi
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

# ---------- Config (env defaults) ----------
HOST="${HOST:-localhost}"
MEDICAL_PORT="${EXT_MEDICAL_PORT:-${MEDICAL_AGENT_PORT:-19082}}"
PAYMENT_PORT="${EXT_PAYMENT_PORT:-${PAYMENT_AGENT_PORT:-19083}}"

# Signature verification default for Payment follows SAGE_MODE (off => require=false)
SMODE="$(printf '%s' "${SAGE_MODE:-}" | tr '[:upper:]' '[:lower:]')"
DEFAULT_REQUIRE="true"; case "$SMODE" in off|false|0|no) DEFAULT_REQUIRE="false";; esac
PAYMENT_REQUIRE_SIGNATURE="$(to_bool "${PAYMENT_REQUIRE_SIGNATURE:-$DEFAULT_REQUIRE}" "$DEFAULT_REQUIRE")"

# Payment HPKE keys (optional)
SIGN_JWK="${PAYMENT_JWK_FILE:-}"
KEM_JWK="${PAYMENT_KEM_JWK_FILE:-}"
HPKE_KEYS_PATH="${HPKE_KEYS_FILE:-merged_agent_keys.json}"

# ---------- LLM shared (bridge to llm.NewFromEnv) ----------
LLM_ENABLED="$(to_bool "${LLM_ENABLED:-true}" true)"
# 기본값은 정답 경로
LLM_BASE_URL="$(normalize_base "${LLM_BASE_URL:-${JAMINAI_API_URL:-https://generativelanguage.googleapis.com/v1beta/openai}}")"
LLM_API_KEY="${LLM_API_KEY:-${JAMINAI_API_KEY:-${GEMINI_API_KEY:-${GOOGLE_API_KEY:-}}}}"
LLM_MODEL="${LLM_MODEL:-${JAMINAI_MODEL:-gemini-2.5-flash}}"
LLM_TIMEOUT_MS="${LLM_TIMEOUT_MS:-80000}"
LLM_LANG_DEFAULT="${LLM_LANG_DEFAULT:-auto}"

# CLI overrides
while [[ $# -gt 0 ]]; do
  case "$1" in
    --medical-port)  MEDICAL_PORT="${2:-$MEDICAL_PORT}"; shift 2 ;;
    --payment-port)  PAYMENT_PORT="${2:-$PAYMENT_PORT}"; shift 2 ;;
    --llm)           LLM_ENABLED="$(to_bool "${2:-$LLM_ENABLED}" "$LLM_ENABLED")"; shift 2 ;;
    --llm-on)        LLM_ENABLED="true"; shift ;;
    --llm-off)       LLM_ENABLED="false"; shift ;;
    --llm-base)      LLM_BASE_URL="$(normalize_base "${2:-$LLM_BASE_URL}")"; shift 2 ;;
    --llm-key)       LLM_API_KEY="${2:-}"; shift 2 ;;
    --llm-model)     LLM_MODEL="${2:-$LLM_MODEL}"; shift 2 ;;
    --llm-timeout-ms)LLM_TIMEOUT_MS="${2:-$LLM_TIMEOUT_MS}"; shift 2 ;;
    --llm-lang)      LLM_LANG_DEFAULT="${2:-$LLM_LANG_DEFAULT}"; shift 2 ;;
    -h|--help)
      cat <<USAGE
Usage: $0 [--medical-port N] [--payment-port N]
          [--llm on|off] [--llm-base URL] [--llm-key KEY] [--llm-model MODEL] [--llm-timeout-ms MS] [--llm-lang auto|ko|en]
USAGE
      exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

# Bridge -> JAMINAI_* env (used by llm.NewFromEnv in agents)
export JAMINAI_API_URL="$LLM_BASE_URL"
export JAMINAI_MODEL="$LLM_MODEL"
if [[ "$LLM_ENABLED" == "true" && -n "$LLM_API_KEY" ]]; then
  export JAMINAI_API_KEY="$LLM_API_KEY"
else
  unset JAMINAI_API_KEY
fi
if [[ "$LLM_TIMEOUT_MS" =~ ^[0-9]+$ ]]; then
  export JAMINAI_TIMEOUT="$((LLM_TIMEOUT_MS/1000))s"
fi

echo "[cfg] LLM: enabled=${LLM_ENABLED} base=${LLM_BASE_URL} model=${LLM_MODEL} timeout_ms=${LLM_TIMEOUT_MS}"
echo "[cfg] Ports: medical=${MEDICAL_PORT} payment=${PAYMENT_PORT}"
echo "[cfg] Payment require signature=${PAYMENT_REQUIRE_SIGNATURE}"
echo "[cfg] Keys: HPKE=${HPKE_KEYS_PATH} SIGN=${SIGN_JWK:-<empty>} KEM=${KEM_JWK:-<empty>}"

# ---------- Kill previous ----------
kill_port "${MEDICAL_PORT}"
kill_port "${PAYMENT_PORT}"

# ---------- 1) Medical ----------
if [[ -f cmd/medical/main.go ]]; then
  echo "[start] medical :${MEDICAL_PORT}"
  nohup go run ./cmd/medical/main.go \
    -port "${MEDICAL_PORT}" \
    -llm "${LLM_ENABLED}" \
    -llm-url "${LLM_BASE_URL}" \
    -llm-model "${LLM_MODEL}" \
    -llm-lang "${LLM_LANG_DEFAULT}" \
    -llm-timeout "${LLM_TIMEOUT_MS}" \
    > logs/medical.log 2>&1 & echo $! > pids/medical.pid
  wait_http "http://${HOST}:${MEDICAL_PORT}/status" 40 0.25 || { echo "[FAIL] medical /status"; tail -n 120 logs/medical.log || true; exit 1; }
else
  echo "[WARN] cmd/medical/main.go not found. Skipping medical."
fi

# ---------- 2) Payment ----------
if [[ -f cmd/payment/main.go ]]; then
  echo "[start] payment :${PAYMENT_PORT}"
  ARGS=(
    -port "${PAYMENT_PORT}"
    -require "${PAYMENT_REQUIRE_SIGNATURE}"
    -keys "${HPKE_KEYS_PATH}"
    -llm "${LLM_ENABLED}"
    -llm-url "${LLM_BASE_URL}"
    -llm-model "${LLM_MODEL}"
    -llm-lang "${LLM_LANG_DEFAULT}"
    -llm-timeout "${LLM_TIMEOUT_MS}"
  )
  [[ -n "${LLM_API_KEY}" && "$LLM_ENABLED" == "true" ]] && ARGS+=( -llm-key "${LLM_API_KEY}" )
  [[ -n "${SIGN_JWK}" ]] && ARGS+=( -sign-jwk "${SIGN_JWK}" )
  [[ -n "${KEM_JWK}"  ]] && ARGS+=( -kem-jwk "${KEM_JWK}" )

  nohup go run ./cmd/payment/main.go "${ARGS[@]}" \
    > logs/payment.log 2>&1 & echo $! > pids/payment.pid

  if ! wait_http "http://${HOST}:${PAYMENT_PORT}/status" 40 0.25; then
    echo "[FAIL] payment /status"
    tail -n 120 logs/payment.log || true
    exit 1
  fi
else
  echo "[ERR] cmd/payment/main.go not found."
  exit 1
fi

# ---------- Show endpoints for Root ----------
echo "--------------------------------------------------"
echo "[OK] medical  : http://${HOST}:${MEDICAL_PORT}/status"
echo "[OK] payment  : http://${HOST}:${PAYMENT_PORT}/status"
echo "Export these for Root:"
echo "  export MEDICAL_EXTERNAL_URL=\"http://${HOST}:${MEDICAL_PORT}\""
echo "  export PAYMENT_URL=\"http://${HOST}:${PAYMENT_PORT}\""
echo "--------------------------------------------------"
