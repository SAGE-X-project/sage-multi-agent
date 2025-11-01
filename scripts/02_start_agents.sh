#!/usr/bin/env bash
# Start external agents: Medical, Payment (verifier with optional HPKE & LLM).
# Exposes /status and /process on each service.
# Ports (defaults): medical=19082, payment=19083
# LLM: GEMINI_* ONLY

# ── Force bash (arrays) ────────────────────────────────────────────────────────
if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# --- Load .env and auto-export so os.Getenv(...) can see
set -a
[[ -f .env ]] && source .env
set +a

mkdir -p logs pids

# ---------- Helpers ----------
require() { command -v "$1" >/dev/null 2>&1 || { echo "[ERR] '$1' not found"; exit 1; }; }
require go; require curl; require lsof

kill_port() {
  local port="$1"; local pids
  pids="$(lsof -n -P -tiTCP:"$port" -sTCP:LISTEN || true)"
  [[ -z "$pids" ]] && return
  echo "[kill] :$port -> $pids"
  kill $pids 2>/dev/null || true
  sleep 0.2
  pids="$(lsof -n -P -tiTCP:"$port" -sTCP:LISTEN || true)"
  [[ -n "$pids" ]] && kill -9 $pids 2>/dev/null || true
}

wait_http() {
  local url="$1" tries="${2:-120}" delay="${3:-0.25}"
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

# Write as an if-block to work safely with set -e (avoid exit on condition failure)
warn_if_missing() {
  local p="$1" name="$2"
  if [[ -n "$p" && ! -f "$p" ]]; then
    echo "[WARN] $name file not found: $p"
  fi
}

# ---------- Config (env defaults) ----------
HOST="${HOST:-localhost}"
MEDICAL_PORT="${EXT_MEDICAL_PORT:-${MEDICAL_AGENT_PORT:-19082}}"
PAYMENT_PORT="${EXT_PAYMENT_PORT:-${PAYMENT_AGENT_PORT:-19083}}"

# Signature verification defaults follow SAGE_MODE (off => require=false)
SMODE="$(printf '%s' "${SAGE_MODE:-}" | tr '[:upper:]' '[:lower:]')"
DEFAULT_REQUIRE="true"; case "$SMODE" in off|false|0|no) DEFAULT_REQUIRE="false";; esac
PAYMENT_REQUIRE_SIGNATURE="$(to_bool "${PAYMENT_REQUIRE_SIGNATURE:-$DEFAULT_REQUIRE}" "$DEFAULT_REQUIRE")"
MEDICAL_REQUIRE_SIGNATURE="$(to_bool "${MEDICAL_REQUIRE_SIGNATURE:-$DEFAULT_REQUIRE}" "$DEFAULT_REQUIRE")"

# HPKE keys (shared DID map)
HPKE_KEYS_FILE="${HPKE_KEYS_FILE:-merged_agent_keys.json}"
MEDICAL_JWK_FILE="${MEDICAL_JWK_FILE:-}"
MEDICAL_KEM_JWK_FILE="${MEDICAL_KEM_JWK_FILE:-}"
PAYMENT_JWK_FILE="${PAYMENT_JWK_FILE:-}"
PAYMENT_KEM_JWK_FILE="${PAYMENT_KEM_JWK_FILE:-}"

# ---------- LLM shared (bridge to llm.NewFromEnv) ----------
LLM_ENABLED="$(to_bool "${LLM_ENABLED:-true}" true)"
GEMINI_API_URL="$(normalize_base "${LLM_BASE_URL:-${GEMINI_API_URL:-https://generativelanguage.googleapis.com/v1beta/openai}}")"
GEMINI_API_KEY="${LLM_API_KEY:-${GEMINI_API_KEY:-${GEMINI_API_KEY:-${GOOGLE_API_KEY:-${OPENAI_API_KEY:-}}}}}"
GEMINI_MODEL="${LLM_MODEL:-${GEMINI_MODEL:-gemini-2.5-flash}}"
GEMINI_TIMEOUT_MS="${LLM_TIMEOUT_MS:-80000}"
LLM_LANG_DEFAULT="${LLM_LANG_DEFAULT:-auto}"

DEBUG_FLAG=

# ---------- CLI overrides ----------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --medical-port)   MEDICAL_PORT="${2:-$MEDICAL_PORT}"; shift 2 ;;
    --payment-port)   PAYMENT_PORT="${2:-$PAYMENT_PORT}"; shift 2 ;;
    --llm)            LLM_ENABLED="$(to_bool "${2:-$LLM_ENABLED}" "$LLM_ENABLED")"; shift 2 ;;
    --llm-on)         LLM_ENABLED="true"; shift ;;
    --llm-off)        LLM_ENABLED="false"; shift ;;
    --llm-base)       GEMINI_API_URL="$(normalize_base "${2:-$GEMINI_API_URL}")"; shift 2 ;;
    --llm-key)        GEMINI_API_KEY="${2:-}"; shift 2 ;;
    --llm-model)      GEMINI_MODEL="${2:-$GEMINI_MODEL}"; shift 2 ;;
    --llm-timeout-ms) GEMINI_TIMEOUT_MS="${2:-$GEMINI_TIMEOUT_MS}"; shift 2 ;;
    --llm-lang)       LLM_LANG_DEFAULT="${2:-$LLM_LANG_DEFAULT}"; shift 2 ;;
    --debug)          DEBUG_FLAG=1; shift ;;
    -h|--help)
      cat <<USAGE
Usage: $0 [--medical-port N] [--payment-port N]
          [--llm on|off] [--llm-base URL] [--llm-key KEY] [--llm-model MODEL] [--llm-timeout-ms MS] [--llm-lang auto|ko|en]
          [--debug]
USAGE
      exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

[[ -n "$DEBUG_FLAG" ]] && { export PS4='[02:${LINENO}] '; set -x; }

# ---------- Export env so Go os.Getenv(...) sees them ----------
export HPKE_KEYS_FILE MEDICAL_JWK_FILE MEDICAL_KEM_JWK_FILE PAYMENT_JWK_FILE PAYMENT_KEM_JWK_FILE
export GEMINI_API_URL GEMINI_MODEL
if [[ "$LLM_ENABLED" == "true" && -n "$GEMINI_API_KEY" ]]; then
  export GEMINI_API_KEY
else
  unset GEMINI_API_KEY
fi
if [[ "$GEMINI_TIMEOUT_MS" =~ ^[0-9]+$ ]]; then
  export GEMINI_TIMEOUT="$((GEMINI_TIMEOUT_MS/1000))s"
fi

echo "[cfg] LLM: enabled=${LLM_ENABLED} base=${GEMINI_API_URL} model=${GEMINI_MODEL} timeout_ms=${GEMINI_TIMEOUT_MS}"
echo "[cfg] Ports: medical=${MEDICAL_PORT} payment=${PAYMENT_PORT}"
echo "[cfg] Require signature: medical=${MEDICAL_REQUIRE_SIGNATURE} payment=${PAYMENT_REQUIRE_SIGNATURE}"
echo "[cfg] Keys:"
echo "  HPKE_KEYS_FILE=${HPKE_KEYS_FILE}"
echo "  MEDICAL_JWK_FILE=${MEDICAL_JWK_FILE}"
echo "  MEDICAL_KEM_JWK_FILE=${MEDICAL_KEM_JWK_FILE}"
echo "  PAYMENT_JWK_FILE=${PAYMENT_JWK_FILE}"
echo "  PAYMENT_KEM_JWK_FILE=${PAYMENT_KEM_JWK_FILE}"

# Print warnings only (do not exit)
warn_if_missing "$HPKE_KEYS_FILE" "HPKE_KEYS_FILE"
warn_if_missing "$MEDICAL_JWK_FILE" "MEDICAL_JWK_FILE"
warn_if_missing "$MEDICAL_KEM_JWK_FILE" "MEDICAL_KEM_JWK_FILE"
warn_if_missing "$PAYMENT_JWK_FILE" "PAYMENT_JWK_FILE"
warn_if_missing "$PAYMENT_KEM_JWK_FILE" "PAYMENT_KEM_JWK_FILE"

# ---------- Kill previous ----------
kill_port "${MEDICAL_PORT}"
kill_port "${PAYMENT_PORT}"

# ---------- 1) Medical ----------
if [[ -f cmd/medical/main.go ]]; then
  echo "[start] medical :${MEDICAL_PORT}"
  MED_ARGS=(
    -port "${MEDICAL_PORT}"
    -require "${MEDICAL_REQUIRE_SIGNATURE}"
    -llm "${LLM_ENABLED}"
    -llm-url "${GEMINI_API_URL}"
    -llm-model "${GEMINI_MODEL}"
    -llm-lang "${LLM_LANG_DEFAULT}"
    -llm-timeout "${GEMINI_TIMEOUT_MS}"
  )
  # Add flags only if keys exist (otherwise start even without HPKE)
  [[ -f "${HPKE_KEYS_FILE}" ]]         && MED_ARGS+=( -keys "${HPKE_KEYS_FILE}" )
  [[ -n "${GEMINI_API_KEY:-}" && "$LLM_ENABLED" == "true" ]] && MED_ARGS+=( -llm-key "${GEMINI_API_KEY}" )
  [[ -f "${MEDICAL_JWK_FILE}" ]]       && MED_ARGS+=( -sign-jwk "${MEDICAL_JWK_FILE}" )
  [[ -f "${MEDICAL_KEM_JWK_FILE}"  ]]  && MED_ARGS+=( -kem-jwk "${MEDICAL_KEM_JWK_FILE}" )

  echo "[cmd] go run ./cmd/medical/main.go ${MED_ARGS[*]}"
  nohup env \
    HPKE_KEYS_FILE="${HPKE_KEYS_FILE}" \
    MEDICAL_JWK_FILE="${MEDICAL_JWK_FILE}" \
    MEDICAL_KEM_JWK_FILE="${MEDICAL_KEM_JWK_FILE}" \
    GEMINI_API_URL="${GEMINI_API_URL}" GEMINI_MODEL="${GEMINI_MODEL}" GEMINI_API_KEY="${GEMINI_API_KEY:-}" GEMINI_TIMEOUT="${GEMINI_TIMEOUT:-}" \
    go run ./cmd/medical/main.go "${MED_ARGS[@]}" \
    > logs/medical.log 2>&1 & echo $! > pids/medical.pid

  if ! wait_http "http://${HOST}:${MEDICAL_PORT}/status" 40 0.25; then
    echo "[FAIL] medical /status"
    tail -n 200 logs/medical.log || true
    exit 1
  fi
else
  echo "[WARN] cmd/medical/main.go not found. Skipping medical."
fi

# ---------- 2) Payment ----------
if [[ -f cmd/payment/main.go ]]; then
  echo "[start] payment :${PAYMENT_PORT}"
  PAY_ARGS=(
    -port "${PAYMENT_PORT}"
    -require "${PAYMENT_REQUIRE_SIGNATURE}"
    -llm "${LLM_ENABLED}"
    -llm-url "${GEMINI_API_URL}"
    -llm-model "${GEMINI_MODEL}"
    -llm-lang "${LLM_LANG_DEFAULT}"
    -llm-timeout "${GEMINI_TIMEOUT_MS}"
  )
  # Add flags only if keys exist
  [[ -f "${HPKE_KEYS_FILE}" ]]           && PAY_ARGS+=( -keys "${HPKE_KEYS_FILE}" )
  [[ -n "${GEMINI_API_KEY:-}" && "$LLM_ENABLED" == "true" ]] && PAY_ARGS+=( -llm-key "${GEMINI_API_KEY}" )
  [[ -f "${PAYMENT_JWK_FILE}" ]]         && PAY_ARGS+=( -sign-jwk "${PAYMENT_JWK_FILE}" )
  [[ -f "${PAYMENT_KEM_JWK_FILE}"  ]]    && PAY_ARGS+=( -kem-jwk "${PAYMENT_KEM_JWK_FILE}" )

  echo "[cmd] go run ./cmd/payment/main.go ${PAY_ARGS[*]}"
  nohup env \
    HPKE_KEYS_FILE="${HPKE_KEYS_FILE}" \
    PAYMENT_JWK_FILE="${PAYMENT_JWK_FILE}" \
    PAYMENT_KEM_JWK_FILE="${PAYMENT_KEM_JWK_FILE}" \
    GEMINI_API_URL="${GEMINI_API_URL}" GEMINI_MODEL="${GEMINI_MODEL}" GEMINI_API_KEY="${GEMINI_API_KEY:-}" GEMINI_TIMEOUT="${GEMINI_TIMEOUT:-}" \
    go run ./cmd/payment/main.go "${PAY_ARGS[@]}" \
    > logs/payment.log 2>&1 & echo $! > pids/payment.pid

  if ! wait_http "http://${HOST}:${PAYMENT_PORT}/status" 40 0.25; then
    echo "[FAIL] payment /status"
    tail -n 200 logs/payment.log || true
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
echo "  export MEDICAL_URL=\"http://${HOST}:${MEDICAL_PORT}\""
echo "  export PAYMENT_URL=\"http://${HOST}:${PAYMENT_PORT}\""
echo "--------------------------------------------------"
