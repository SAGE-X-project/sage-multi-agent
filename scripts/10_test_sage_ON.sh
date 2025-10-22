#!/usr/bin/env bash
# SAGE ON test (client -> root -> payment(:18083) -> gateway -> external)
# - tamper | pass
# - POST /api/payment
# - Save headers/body; simple verdict

set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

GATEWAY_MODE="${GATEWAY_MODE:-tamper}"   # tamper | pass
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-$'\n[GW-ATTACK] injected by gateway'}}"
NO_RESTART_GATEWAY=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tamper)        GATEWAY_MODE="tamper"; shift ;;
    --pass|--passthrough) GATEWAY_MODE="pass"; shift ;;
    --attack-msg)    ATTACK_MESSAGE="$2"; shift 2 ;;
    --no-restart-gateway) NO_RESTART_GATEWAY=1; shift ;;
    -h|--help) cat <<EOF
Usage: $0 [--tamper | --pass] [--attack-msg TEXT] [--no-restart-gateway]
EOF
      exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

HOST="${HOST:-localhost}"
CLIENT_PORT="${CLIENT_PORT:-8086}"
ROOT_PORT="${ROOT_PORT:-${ROOT_AGENT_PORT:-8080}}"
GATEWAY_PORT="${GATEWAY_PORT:-5500}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"
PAYMENT_PORT="${PAYMENT_PORT:-${PAYMENT_AGENT_PORT:-18083}}"

BASE="http://${HOST}:${CLIENT_PORT}"
OUT_DIR="out"; mkdir -p "$OUT_DIR"

echo "[info] BASE=${BASE}"
echo "[info] Ports: root=${ROOT_PORT} gateway=${GATEWAY_PORT} external=${EXT_PAYMENT_PORT} payment=${PAYMENT_PORT}"
echo "[info] Gateway mode: ${GATEWAY_MODE} (no-restart-gw=${NO_RESTART_GATEWAY})"
echo "[info] EXTERNAL_IMPL=${EXTERNAL_IMPL:-agent} (see scripts/start_external_payment.sh)"

wait_tcp() {
  local host="$1" port="$2" tries="${3:-60}" delay="${4:-0.2}"
  for ((i=1;i<=tries;i++)); do
    if { exec 3<>/dev/tcp/"$host"/"$port"; } >/dev/null 2>&1; then exec 3>&- 3<&-; return 0; fi
    sleep "$delay"
  done; return 1
}
wait_http() {
  local url="$1" tries="${2:-40}" delay="${3:-0.25}"
  for ((i=1;i<=tries;i++)); do
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then return 0; fi
    sleep "$delay"
  done; return 1
}

if [[ $NO_RESTART_GATEWAY -eq 0 ]]; then
  if ! curl -sSf -m 1 "http://${HOST}:${EXT_PAYMENT_PORT}/status" >/dev/null 2>&1; then
    scripts/start_external_payment.sh
  fi
  wait_http "http://${HOST}:${EXT_PAYMENT_PORT}/status" 40 0.25

  if [[ "$GATEWAY_MODE" == "pass" ]]; then
    scripts/03_start_gateway_pass.sh
  else
    ATTACK_MESSAGE="$ATTACK_MESSAGE" scripts/03_start_gateway_tamper.sh
  fi
fi

echo "[prep] waiting for gateway tcp :${GATEWAY_PORT} ..."
wait_tcp "$HOST" "$GATEWAY_PORT" 60 0.2 && echo "[prep] gateway tcp is open"

echo "[preflight] probing endpoints"
probe_json() {
  local name="$1" url="$2"
  local code; code=$(curl -sS -m 2 -w "%{http_code}" -o /dev/null "$url" || true)
  echo "[probe] ${name} -> HTTP ${code}"
}
probe_tcp() {
  local name="$1" port="$2"
  if wait_tcp "$HOST" "$port" 1 0.1; then echo "[probe] ${name}@:${port} -> TCP OPEN"; else echo "[probe] ${name}@:${port} -> TCP CLOSED"; fi
}
probe_json root     "http://${HOST}:${ROOT_PORT}/status"
probe_tcp  gateway  "$GATEWAY_PORT"
probe_json external "http://${HOST}:${EXT_PAYMENT_PORT}/status"
probe_json client   "http://${HOST}:${CLIENT_PORT}/api/sage/config"
probe_tcp  payment  "$PAYMENT_PORT"

PAYLOAD="$(mktemp)"
cat > "$PAYLOAD" <<'JSON'
{
  "prompt": "please pay 10 USDT to 0xbeef...",
  "sageEnabled": true,
  "scenario": "mitm"
}
JSON

HDR="$(mktemp)"; BODY="$(mktemp)"
JSON_OUT="${OUT_DIR}/resp_sage_on.json"; CODE_OUT="${OUT_DIR}/resp_sage_on.code"

echo "[test] POST /api/payment (SAGE ON)"
HTTP_CODE=$(curl -sS -X POST \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -H "X-Scenario: mitm" \
  --data @"$PAYLOAD" \
  -D "$HDR" -o "$BODY" -w "%{http_code}" \
  "${BASE}/api/payment" || true)
echo -n "$HTTP_CODE" > "$CODE_OUT"

extract_ct(){ awk 'BEGIN{IGNORECASE=1}/^Content-Type:/{sub(/^[^:]*:[[:space:]]*/,"");gsub(/[[:space:]]+$/,"");print tolower($0);exit}' "$1" | tr -d '\r'; }
ct="$(extract_ct "$HDR")"
if command -v jq >/dev/null 2>&1; then
  jq -n --arg code "$HTTP_CODE" --arg ctype "${ct:-unknown}" --rawfile headers "$HDR" --rawfile body "$BODY" '
    ($ctype | tostring | startswith("application/json")) as $isjson
    |
    { http_code: (($code|tonumber?)//0), content_type: $ctype, headers_raw: $headers }
    + ( if $isjson then (try { body_json: ($body|fromjson) } catch { body_text: $body }) else { body_text: $body } end )
  ' > "$JSON_OUT"
else
  {
    echo "{"
    echo "  \"http_code\": ${HTTP_CODE:-0},"
    echo "  \"content_type\": \"${ct:-unknown}\""
    echo "}"
  } > "$JSON_OUT"
fi

echo "[test] HTTP $HTTP_CODE"
echo "===== HEADERS ($HDR) ====="; cat "$HDR"; echo
echo "===== JSON ($JSON_OUT) ====="; (command -v jq >/dev/null 2>&1 && jq -C . "$JSON_OUT") || cat "$JSON_OUT"; echo

echo
echo "========== Result Summary =========="
echo "Gateway mode       : ${GATEWAY_MODE}"
echo "HTTP status        : ${HTTP_CODE}"
ok_text=$(grep -i '"payment processed' "$BODY" >/dev/null 2>&1 && echo 1 || echo 0)
fail_text=$(grep -i 'unauthorized\|verification failed\|content-digest mismatch' "$BODY" >/dev/null 2>&1 && echo 1 || echo 0)
echo "Body says OK?      : ${ok_text}"
echo "Body says FAIL?    : ${fail_text}"
if [[ "$GATEWAY_MODE" == "pass" ]]; then
  if [[ "$HTTP_CODE" == "200" && "$ok_text" == "1" ]]; then
    echo "[VERDICT] ✅ pass-through OK"
  else
    echo "[VERDICT] ⚠️ Unexpected for pass-through. Check logs."
  fi
else
  if [[ "$HTTP_CODE" =~ ^4 && "$fail_text" == "1" ]]; then
    echo "[VERDICT] ✅ tamper detected"
  else
    echo "[VERDICT] ⚠️ Tamper NOT detected. Check logs."
  fi
fi
echo "===================================="
echo

echo "[diagnose] scanning logs (tail -n 200)"
for f in logs/root.log logs/gateway.log logs/external-payment.log logs/payment.log; do
  echo "----- $f -----"
  [[ -f "$f" ]] && tail -n 200 "$f" | sed -e 's/^/[tail] /' || echo "(missing)"
done

rm -f "$HDR" "$BODY" "$PAYLOAD"
echo "[done] Files saved: $JSON_OUT ; $CODE_OUT"
