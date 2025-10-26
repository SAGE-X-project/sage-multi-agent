#!/usr/bin/env bash
# Run Payment Agent (in-proc) with optional A2A signing and HPKE payload encryption.
# - Only two HPKE options are exposed to the app: -hpke (bool) and -hpke-keys (path).
# - All other chain/key settings are taken from the payment app itself (initSigning/env).

set -Eeuo pipefail

# ---------- Paths ----------
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

mkdir -p logs pids

# ---------- Helpers ----------
kill_port() {
  local port="$1"; local pids
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -z "$pids" ]] && return
  echo "[kill] payment-agent on :$port -> $pids"
  kill $pids 2>/dev/null || true
  sleep 0.2
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -n "$pids" ]] && kill -9 $pids 2>/dev/null || true
}

wait_http() {
  local url="$1" tries="${2:-40}" delay="${3:-0.25}"
  for ((i=1;i<=tries;i++)); do
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done
  return 1
}

to_bool() {
  # normalize various truthy/falsey values to "true"/"false"
  local v="${1:-}"
  v="$(echo "${v}" | tr '[:upper:]' '[:lower:]')"
  case "$v" in
    1|true|on|yes)  echo "true" ;;
    0|false|off|no) echo "false" ;;
    *)              echo "$2" ;; # default if unknown
  esac
}

# ---------- Config (env defaults) ----------
HOST="${HOST:-localhost}"

# Payment agent port
PAYMENT_AGENT_PORT="${PAYMENT_AGENT_PORT:-18083}"

# Where the agent will send requests (gateway/external payment base)
PAYMENT_EXTERNAL_URL="${PAYMENT_EXTERNAL_URL:-http://localhost:5500}"

# A2A signing key (required when SAGE=true)
PAYMENT_JWK_FILE="${PAYMENT_JWK_FILE:-}"
PAYMENT_DID="${PAYMENT_DID:-}"

# Enable outbound signing
PAYMENT_SAGE_ENABLED="$(to_bool "${PAYMENT_SAGE_ENABLED:-true}" true)"

# HPKE switches (only two!)
HPKE_ENABLE="$(to_bool "${HPKE_ENABLE:-false}" false)"
HPKE_KEYS_PATH="${HPKE_KEYS_PATH:-generated_agent_keys.json}"

# ---------- Show effective config ----------
echo "[cfg] PAYMENT_AGENT_PORT=${PAYMENT_AGENT_PORT}"
echo "[cfg] PAYMENT_EXTERNAL_URL=${PAYMENT_EXTERNAL_URL}"
echo "[cfg] PAYMENT_SAGE_ENABLED=${PAYMENT_SAGE_ENABLED}"
echo "[cfg] PAYMENT_JWK_FILE=${PAYMENT_JWK_FILE:-<empty>}"
echo "[cfg] PAYMENT_DID=${PAYMENT_DID:-<empty>}"
echo "[cfg] HPKE_ENABLE=${HPKE_ENABLE}"
echo "[cfg] HPKE_KEYS_PATH=${HPKE_KEYS_PATH}"

# ---------- Kill previous ----------
kill_port "${PAYMENT_AGENT_PORT}"

# ---------- Build command ----------
ARGS=(
  -port "${PAYMENT_AGENT_PORT}"
  -external "${PAYMENT_EXTERNAL_URL}"
  -sage "${PAYMENT_SAGE_ENABLED}"
  -hpke "${HPKE_ENABLE}"
  -hpke-keys "${HPKE_KEYS_PATH}"
)

# JWK/DID are optional flags; pass only when set
if [[ -n "${PAYMENT_JWK_FILE}" ]]; then
  ARGS+=( -jwk "${PAYMENT_JWK_FILE}" )
fi
if [[ -n "${PAYMENT_DID}" ]]; then
  ARGS+=( -did "${PAYMENT_DID}" )
fi

# Warn if SAGE is enabled but JWK is missing
if [[ "${PAYMENT_SAGE_ENABLED}" == "true" && -z "${PAYMENT_JWK_FILE}" ]]; then
  echo "[warn] SAGE is enabled but PAYMENT_JWK_FILE is empty. The agent will fail to sign outbound requests."
fi

# ---------- Start ----------
if [[ -x bin/payment-agent ]]; then
  echo "[start] PaymentAgent :${PAYMENT_AGENT_PORT} [bin]"
  nohup bin/payment-agent "${ARGS[@]}" \
    > logs/payment-agent.log 2>&1 & echo $! > pids/payment-agent.pid

elif [[ -f cmd/payment/main.go ]]; then
  echo "[start] PaymentAgent :${PAYMENT_AGENT_PORT} [go run]"
  nohup go run ./cmd/payment/main.go "${ARGS[@]}" \
    > logs/payment-agent.log 2>&1 & echo $! > pids/payment-agent.pid

else
  echo "[ERR] cmd/payment/main.go not found and bin/payment-agent not present."
  exit 1
fi

# ---------- Health check ----------
if ! wait_http "http://${HOST}:${PAYMENT_AGENT_PORT}/status" 40 0.25; then
  echo "[FAIL] payment-agent failed to respond on /status"
  tail -n 120 logs/payment-agent.log || true
  exit 1
fi

echo "[ok] logs: logs/payment-agent.log  pid: $(cat pids/payment-agent.pid 2>/dev/null || echo '?')"
