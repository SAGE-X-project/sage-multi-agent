#!/usr/bin/env bash
# One-click launcher (minimal, IN-PROC planning/ordering):
# payment (verifier) -> gateway (tamper|pass) -> root (routes out) -> client
#
# Options:
#   --tamper                  start gateway with tamper ON  (default)
#   --pass | --passthrough    start gateway pass-through
#   --attack-msg "<text>"     override tamper message
#   --sage on|off             set SAGE mode for Root/Payment (default: env SAGE_MODE or off)
#   --hpke on|off             enable HPKE at Root for outbound (default: off)
#   --hpke-keys <path>        DID mapping for Root HPKE (default: generated_agent_keys.json)
#   --force-kill              kill occupied ports automatically
#
# Notes:
# - PaymentAgent runs IN-PROC inside the root process. Its logs go to logs/root.log.

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
mkdir -p logs

# load .env if present
_PASSED_SAGE_MODE="${SAGE_MODE-}"
[[ -f .env ]] && source .env
[[ -n "${_PASSED_SAGE_MODE}" ]] && SAGE_MODE="${_PASSED_SAGE_MODE}"

# ---------- Defaults ----------
HOST="${HOST:-localhost}"
ROOT_PORT="${ROOT_PORT:-${ROOT_AGENT_PORT:-8080}}"
CLIENT_PORT="${CLIENT_PORT:-8086}"
GATEWAY_PORT="${GATEWAY_PORT:-5500}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"

# Payment sub-agent (IN-PROC) needs these to call outward
PAYMENT_EXTERNAL_URL_DEFAULT="http://${HOST}:${GATEWAY_PORT}"
PAYMENT_EXTERNAL_URL="${PAYMENT_EXTERNAL_URL:-$PAYMENT_EXTERNAL_URL_DEFAULT}"
PAYMENT_JWK_FILE="${PAYMENT_JWK_FILE:-keys/payment.jwk}"   # MUST exist for outbound signing
[[ -n "${PAYMENT_DID:-}" ]] || true

# HPKE control (CLI flags; default OFF)
PAYMENT_HPKE_ENABLE="0"                                    # 0=off, 1=on
PAYMENT_HPKE_KEYS="${PAYMENT_HPKE_KEYS:-generated_agent_keys.json}"

GATEWAY_MODE="tamper"                         # tamper|pass
SAGE_MODE_CLI=""                              # on|off (optional CLI override)
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-$'\n[GW-ATTACK] injected by gateway'}}"
FORCE_KILL=0

usage() {
  cat <<EOF
Usage: $0 [--tamper|--pass] [--attack-msg "<text>"] [--sage on|off] [--hpke on|off] [--hpke-keys <path>] [--force-kill]
EOF
}

# ---------- Args ----------
PAYMENT_HPKE_ENABLE="0"
PAYMENT_HPKE_KEYS="${PAYMENT_HPKE_KEYS:-generated_agent_keys.json}"

SAGE_MODE="${SAGE_MODE:-off}"      # env 기본값
HPKE_CLI="off"                     # CLI로 받은 hpke 값(기본 off)

# ---------- Args ----------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --tamper)                 GATEWAY_MODE="tamper"; shift ;;
    --pass|--passthrough)     GATEWAY_MODE="pass"; shift ;;
    --attack-msg)             ATTACK_MESSAGE="${2:-}"; shift 2 ;;

    # === NEW: --sage 지원 (공백/=/대소문자 모두) ===
    --sage=*)
      SAGE_MODE="$(printf '%s' "${1#*=}" | tr '[:upper:]' '[:lower:]')"; shift ;;
    --sage)
      SAGE_MODE="$(printf '%s' "${2:-on}" | tr '[:upper:]' '[:lower:]')"; shift 2 ;;

    # hpke도 공백/=/대소문자 모두 지원
    --hpke=*)
      HPKE_CLI="$(printf '%s' "${1#*=}" | tr '[:upper:]' '[:lower:]')"; shift ;;
    --hpke)
      HPKE_CLI="$(printf '%s' "${2:-off}" | tr '[:upper:]' '[:lower:]')"; shift 2 ;;

    --hpke-keys)
      PAYMENT_HPKE_KEYS="${2:-generated_agent_keys.json}"; shift 2 ;;

    --force-kill)             FORCE_KILL=1; shift ;;
    -h|--help)                usage; exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

# SAGE 모드 해석
case "$SAGE_MODE" in
  on|true|1|yes)  EXT_VERIFY="on";  ROOT_SAGE="true"  ;;
  *)              EXT_VERIFY="off"; ROOT_SAGE="false" ;;
esac

# HPKE 모드 해석
case "$HPKE_CLI" in
  on|true|1|yes)  PAYMENT_HPKE_ENABLE="1" ;;
  *)              PAYMENT_HPKE_ENABLE="0" ;;
esac

require() { command -v "$1" >/dev/null 2>&1 || { echo "[ERR] '$1' not found"; exit 1; }; }
require go
require curl

wait_tcp() {
  local host="$1" port="$2" tries="${3:-60}" delay="${4:-0.2}"
  for ((i=1;i<=tries;i++)); do
    if { exec 3<>/dev/tcp/"$host"/"$port"; } >/dev/null 2>&1; then exec 3>&- 3<&-; return 0; fi
    sleep "$delay"
  done; return 1
}
tail_fail() {
  local name="$1"
  echo "[FAIL] $name failed to start. Showing last 120 log lines:"
  echo "--------------------------------------------------------"
  tail -n 120 "logs/${name}.log" || true
  echo "--------------------------------------------------------"
}
kill_port() {
  local port="$1"
  local pids
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -z "$pids" ]] && return
  echo "[KILL] :$port occupied → $pids"
  kill $pids 2>/dev/null || true
  sleep 0.2
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -n "$pids" ]] && kill -9 $pids 2>/dev/null || true
}
start_bg() {
  local name="$1" port="$2"; shift 2
  local cmd=( "$@" )
  if [[ $FORCE_KILL -eq 1 ]]; then kill_port "$port"; fi
  echo "[START] $name on :$port"
  printf "[CMD] %s\n" "${cmd[*]}"
  ( "${cmd[@]}" ) >"logs/${name}.log" 2>&1 &
  if ! wait_tcp "$HOST" "$port" 60 0.2; then tail_fail "$name"; exit 1; fi
}

# ---------- 0) optional kill ----------
if [[ $FORCE_KILL -eq 1 ]]; then
  "$ROOT_DIR/scripts/01_kill_ports.sh" --force || true
fi

# ---------- preflight: payment JWK ----------
if [[ ! -f "$PAYMENT_JWK_FILE" ]]; then
  echo "[ERR] PAYMENT_JWK_FILE not found: $PAYMENT_JWK_FILE"
  echo "      Put a JWK at that path or export PAYMENT_JWK_FILE=<path>"
  exit 1
fi

# ---------- 1) Payment service (verifier) ----------
# Resolve final SAGE mode: CLI > env > default(off)
SMODE="$(printf '%s' "${SAGE_MODE:-off}" | tr '[:upper:]' '[:lower:]')"
case "$SMODE" in
  off|false|0|no) EXT_VERIFY="off"; ROOT_SAGE="false" ;;
  *)              EXT_VERIFY="on";  ROOT_SAGE="true"  ;;
esac


# Optional HPKE server keys for payment (auto-enable if both exist)
SIGN_JWK=""; KEM_JWK=""; KEYS_FILE="$PAYMENT_HPKE_KEYS"
if [[ "$PAYMENT_HPKE_ENABLE" = "1" ]]; then
  SIGN_JWK="${EXTERNAL_JWK_FILE:-}"
  KEM_JWK="${EXTERNAL_KEM_JWK_FILE:-}"
fi

PAYMENT_ARGS=(
  go run ./cmd/payment/main.go
    -port "$EXT_PAYMENT_PORT"
    -require $([ "$EXT_VERIFY" = "on" ] && echo true || echo false)
    -keys "$KEYS_FILE"
)
[[ -n "$SIGN_JWK" ]] && PAYMENT_ARGS+=( -sign-jwk "$SIGN_JWK" )
[[ -n "$KEM_JWK" ]] && PAYMENT_ARGS+=( -kem-jwk "$KEM_JWK" )

start_bg "payment" "$EXT_PAYMENT_PORT" "${PAYMENT_ARGS[@]}"

# ---------- 2) Gateway (relay to payment) ----------
if [[ -f cmd/gateway/main.go ]]; then
  GW_MAIN="cmd/gateway/main.go"
  if [[ $FORCE_KILL -eq 1 ]]; then kill_port "$GATEWAY_PORT"; fi
  if [[ "$GATEWAY_MODE" == "pass" ]]; then
    echo "[mode] Gateway PASS-THROUGH"
    start_bg "gateway" "$GATEWAY_PORT" \
      env -u ATTACK_MESSAGE \
      go run "./${GW_MAIN}" \
        -listen ":${GATEWAY_PORT}" \
        -upstream "http://${HOST}:${EXT_PAYMENT_PORT}" \
        -attack-msg=""
  else
    echo "[mode] Gateway TAMPER (attack-msg length: ${#ATTACK_MESSAGE})"
    start_bg "gateway" "$GATEWAY_PORT" \
      go run "./${GW_MAIN}" \
        -listen ":${GATEWAY_PORT}" \
        -upstream "http://${HOST}:${EXT_PAYMENT_PORT}" \
        -attack-msg "${ATTACK_MESSAGE}"
  fi
else
  echo "[SKIP] gateway main not found"
fi

# ---------- 3) Root ----------
HPKE_FLAG=$([ "${PAYMENT_HPKE_ENABLE}" = "1" ] && echo true || echo false)
ROOT_ARGS=( -port "$ROOT_PORT" "-sage=${ROOT_SAGE}" "-hpke=${HPKE_FLAG}" -hpke-keys "${PAYMENT_HPKE_KEYS}" )

ROOT_ENV=( "PAYMENT_EXTERNAL_URL=${PAYMENT_EXTERNAL_URL}" "ROOT_SAGE_ENABLED=${ROOT_SAGE}" "ROOT_HPKE=${HPKE_FLAG}" )
[[ -n "${PAYMENT_JWK_FILE:-}" ]] && ROOT_ENV+=( "ROOT_JWK_FILE=${PAYMENT_JWK_FILE}" )
[[ -n "${PAYMENT_DID:-}"      ]] && ROOT_ENV+=( "ROOT_DID=${PAYMENT_DID}" )

start_bg "root" "$ROOT_PORT" env "${ROOT_ENV[@]}" go run ./cmd/root/main.go "${ROOT_ARGS[@]}"

# Build root CLI flags (pass -hpke/-hpke-keys here)
ROOT_ARGS=( -port "$ROOT_PORT" -sage "$ROOT_SAGE" )
if [[ "${PAYMENT_HPKE_ENABLE}" = "1" ]]; then
  ROOT_ARGS+=( -hpke -hpke-keys "${PAYMENT_HPKE_KEYS}" )
fi

echo "[ENV]  PAYMENT_EXTERNAL_URL=${PAYMENT_EXTERNAL_URL}"
[[ -n "${PAYMENT_JWK_FILE:-}" ]] && echo "[ENV]  ROOT_JWK_FILE=${PAYMENT_JWK_FILE}"
[[ -n "${PAYMENT_DID:-}" ]] && echo "[ENV]  ROOT_DID=${PAYMENT_DID}"
echo "[HPKE] enable=${PAYMENT_HPKE_ENABLE} keys=${PAYMENT_HPKE_KEYS}"
echo "[ROOT_ARGS] ${ROOT_ARGS[*]}"

start_bg "root" "$ROOT_PORT" \
  env "${ROOT_ENV[@]}" \
  go run ./cmd/root/main.go "${ROOT_ARGS[@]}"

# ---------- 4) Client API ----------
start_bg "client" "$CLIENT_PORT" \
  go run ./cmd/client/main.go \
    -port "$CLIENT_PORT" \
    -root "http://${HOST}:${ROOT_PORT}"

# ---------- Summary ----------
echo "--------------------------------------------------"
printf "[CHK] %-22s %s\n" "Payment Service"  "http://${HOST}:${EXT_PAYMENT_PORT}/status"
printf "[CHK] %-22s %s\n" "Gateway (TCP)"     "tcp://${HOST}:${GATEWAY_PORT}"
printf "[CHK] %-22s %s\n" "Root"              "http://${HOST}:${ROOT_PORT}/status"
printf "[CHK] %-22s %s\n" "Client API"        "http://${HOST}:${CLIENT_PORT}/api/sage/config"
echo "--------------------------------------------------"

for url in \
  "http://${HOST}:${EXT_PAYMENT_PORT}/status" \
  "http://${HOST}:${ROOT_PORT}/status" \
  "http://${HOST}:${CLIENT_PORT}/api/sage/config"; do
  if curl -sSf -m 1 "$url" >/dev/null 2>&1; then
    echo "[OK] $url"
  else
    echo "[WARN] not ready: $url"
  fi
done

echo "[DONE] Startup initiated. Use: tail -f logs/*.log"
