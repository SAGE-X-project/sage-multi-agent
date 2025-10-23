#!/usr/bin/env bash
# One-click launcher (minimal, IN-PROC sub-agents):
# external payment (agent|echo) -> gateway (tamper|pass) -> root (in-proc planning/ordering/payment) -> client
#
# Options:
#   --tamper                  start gateway with tamper ON  (default)
#   --pass | --passthrough    start gateway pass-through
#   --attack-msg "<text>"     override tamper message
#   --external agent|echo     choose external payment impl (default: agent)
#   --force-kill              kill occupied ports automatically

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
mkdir -p logs

# load .env if present
[[ -f .env ]] && source .env

# ---------- Defaults ----------
HOST="${HOST:-localhost}"
ROOT_PORT="${ROOT_PORT:-${ROOT_AGENT_PORT:-8080}}"
CLIENT_PORT="${CLIENT_PORT:-8086}"
GATEWAY_PORT="${GATEWAY_PORT:-5500}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"

# Payment sub-agent (IN-PROC) needs these to call outward
PAYMENT_EXTERNAL_URL_DEFAULT="http://${HOST}:${GATEWAY_PORT}"
PAYMENT_EXTERNAL_URL="${PAYMENT_EXTERNAL_URL:-$PAYMENT_EXTERNAL_URL_DEFAULT}"
PAYMENT_JWK_FILE="${PAYMENT_JWK_FILE:-keys/payment.jwk}"   # MUST exist for signing
# optional: PAYMENT_DID (derive from key if empty)

GATEWAY_MODE="tamper"                         # tamper|pass
EXTERNAL_IMPL="${EXTERNAL_IMPL:-agent}"       # agent|echo
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-$'\n[GW-ATTACK] injected by gateway'}}"
FORCE_KILL=0

usage() {
  cat <<EOF
Usage: $0 [--tamper|--pass] [--attack-msg "<text>"] [--external agent|echo] [--force-kill]

Examples:
  $0 --tamper                           # gateway가 바디 조작 (기본)
  $0 --pass                             # 게이트웨이 패스스루
  $0 --tamper --attack-msg "[MITM]"     # 조작 문구 지정
  $0 --external echo                    # 외부 결제 echo 모드
EOF
}

# ---------- Args ----------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --tamper)        GATEWAY_MODE="tamper"; shift ;;
    --pass|--passthrough) GATEWAY_MODE="pass"; shift ;;
    --attack-msg)    ATTACK_MESSAGE="${2:-}"; shift 2 ;;
    --external)      EXTERNAL_IMPL="${2:-agent}"; shift 2 ;;
    --force-kill)    FORCE_KILL=1; shift ;;
    -h|--help)       usage; exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

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
  "$ROOT_DIR/scripts/kill_ports.sh" --force || true
fi

# ---------- preflight: payment JWK ----------
if [[ ! -f "$PAYMENT_JWK_FILE" ]]; then
  echo "[ERR] PAYMENT_JWK_FILE not found: $PAYMENT_JWK_FILE"
  echo "      Put a JWK at that path or export PAYMENT_JWK_FILE=<path>"
  exit 1
fi

# ---------- 1) External payment (agent|echo) ----------
SMODE="$(printf '%s' "${SAGE_MODE:-on}" | tr '[:upper:]' '[:lower:]')"
case "$SMODE" in
  off|false|0|no) EXT_VERIFY="off" ;;
  *)              EXT_VERIFY="on"  ;;
esac

# on → 1, off → 0
EXTERNAL_REQUIRE_SIG=0
[ "$EXT_VERIFY" = "on" ] && EXTERNAL_REQUIRE_SIG=1

echo "[cfg] EXTERNAL_IMPL=${EXTERNAL_IMPL}"
echo "[cfg] EXTERNAL_REQUIRE_SIG=${EXTERNAL_REQUIRE_SIG} -> -require $([ "$EXT_VERIFY" = "on" ] && echo true || echo false)"

EXTERNAL_IMPL="$EXTERNAL_IMPL" EXT_PAYMENT_PORT="$EXT_PAYMENT_PORT" EXTERNAL_REQUIRE_SIG="$EXTERNAL_REQUIRE_SIG"\
  "$ROOT_DIR/scripts/02_start_external_payment_agent.sh"

# ---------- 2) Gateway (relay to external payment) ----------
if [[ -f cmd/gateway/main.go ]]; then
  # 경로 호환: cmd/ggateway 또는 cmd/gateway
  GW_MAIN="cmd/gateway/main.go"

  if [[ $FORCE_KILL -eq 1 ]]; then kill_port "$GATEWAY_PORT"; fi
  if [[ "$GATEWAY_MODE" == "pass" ]]; then
    echo "[mode] Gateway PASS-THROUGH"
    start_bg "gateway" "$GATEWAY_PORT" \
      go run "./${GW_MAIN}" \
        -listen ":${GATEWAY_PORT}" \
        -upstream "http://${HOST}:${EXT_PAYMENT_PORT}" \
        -attack-msg ""
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

# ---------- 3) Root (IN-PROC sub agents). 서브 결제가 외부로 나갈 때 쓸 ENV 주입 ----------
ROOT_ENV=( "PAYMENT_EXTERNAL_URL=${PAYMENT_EXTERNAL_URL}" "PAYMENT_JWK_FILE=${PAYMENT_JWK_FILE}" )
if [[ -n "${PAYMENT_DID:-}" ]]; then ROOT_ENV+=( "PAYMENT_DID=${PAYMENT_DID}" ); fi

echo "[ENV] PAYMENT_EXTERNAL_URL=${PAYMENT_EXTERNAL_URL}"
echo "[ENV] PAYMENT_JWK_FILE=${PAYMENT_JWK_FILE}"
[[ -n "${PAYMENT_DID:-}" ]] && echo "[ENV] PAYMENT_DID=${PAYMENT_DID}"

start_bg "root" "$ROOT_PORT" \
  env "${ROOT_ENV[@]}" \
  go run ./cmd/root/main.go \
    -port "$ROOT_PORT"

# ---------- 4) Client API ----------
start_bg "client" "$CLIENT_PORT" \
  go run ./cmd/client/main.go \
    -port "$CLIENT_PORT" \
    -root "http://${HOST}:${ROOT_PORT}"

# ---------- Summary ----------
echo "--------------------------------------------------"
printf "[CHK] %-22s %s\n" "External Payment" "http://${HOST}:${EXT_PAYMENT_PORT}/status"
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

echo "[DONE] Startup (minimal, in-proc sub-agents) initiated. Use: tail -f logs/*.log"
