#!/usr/bin/env bash
# Start Payment service (verifier) with optional HPKE.
# - Replaces the old payment process. Exposes /status and /process.

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

# Listen port (prefer EXT_PAYMENT_PORT; fall back to PAYMENT_AGENT_PORT)
PORT="${EXT_PAYMENT_PORT:-${PAYMENT_AGENT_PORT:-19083}}"

# Signature verification required
# Default follows SAGE_MODE when provided: SAGE off => require=false
SMODE="$(printf '%s' "${SAGE_MODE:-}" | tr '[:upper:]' '[:lower:]')"
DEFAULT_REQUIRE="true"
case "$SMODE" in
  off|false|0|no) DEFAULT_REQUIRE="false" ;;
esac
PAYMENT_REQUIRE_SIGNATURE="$(to_bool "${PAYMENT_REQUIRE_SIGNATURE:-$DEFAULT_REQUIRE}" "$DEFAULT_REQUIRE")"

# HPKE server keys (auto-enabled if both present)
SIGN_JWK="${EXTERNAL_JWK_FILE:-}"
KEM_JWK="${EXTERNAL_KEM_JWK_FILE:-}"
HPKE_KEYS_PATH="${HPKE_KEYS_FILE:-merged_agent_keys.json}"

# ---------- Show effective config ----------
echo "[cfg] PAYMENT_PORT=${PORT}"
echo "[cfg] PAYMENT_REQUIRE_SIGNATURE=${PAYMENT_REQUIRE_SIGNATURE}"
echo "[cfg] EXTERNAL_JWK_FILE=${SIGN_JWK:-<empty>}"
echo "[cfg] EXTERNAL_KEM_JWK_FILE=${KEM_JWK:-<empty>}"
echo "[cfg] HPKE_KEYS_PATH=${HPKE_KEYS_PATH}"

# ---------- Kill previous ----------
kill_port "${PORT}"

# ---------- Build command ----------
ARGS=(
  -port "${PORT}"
  -require "${PAYMENT_REQUIRE_SIGNATURE}"
  -keys "${HPKE_KEYS_PATH}"
)
[[ -n "${SIGN_JWK}" ]] && ARGS+=( -sign-jwk "${SIGN_JWK}" )
[[ -n "${KEM_JWK}" ]] && ARGS+=( -kem-jwk "${KEM_JWK}" )

# ---------- Start ----------
if [[ -f cmd/payment/main.go ]]; then
  echo "[start] Payment service :${PORT} [go run]"
  nohup go run ./cmd/payment/main.go "${ARGS[@]}" \
    > logs/payment.log 2>&1 & echo $! > pids/payment.pid
else
  echo "[ERR] cmd/payment/main.go not found."
  exit 1
fi

# ---------- Health check ----------
if ! wait_http "http://${HOST}:${PORT}/status" 40 0.25; then
  echo "[FAIL] payment service failed to respond on /status"
  tail -n 120 logs/payment.log || true
  exit 1
fi

echo "[ok] logs: logs/payment.log  pid: $(cat pids/payment.pid 2>/dev/null || echo '?')"
