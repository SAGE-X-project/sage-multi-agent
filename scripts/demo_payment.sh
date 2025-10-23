#!/usr/bin/env bash
# Demo: Client API -> Root (routes to in-proc Payment) -> signed HTTP -> Gateway (tamper/pass) -> External Agent (verify) -> back
#
# Options:
#   --sage on|off              # SAGE 서명 사용 여부 (기본: on; in-proc Payment -> External 서명)
#   --tamper | --pass          # Gateway 조작 여부 (기본: --tamper)
#   --attack-msg "<txt>"       # tamper일 때 바디에 덧붙일 텍스트
#   --prompt "<text>"          # 결제 시나리오 프롬프트 (기본: "send 10 USDC to merchant")
#   --force-kill               # 시작 전에 포트 점유 프로세스 강제 종료
#
# 동작:
# 1) minimal bring-up (external payment + gateway + root + client) 실행
# 2) 클라이언트 API에 결제 요청 전송 (X-SAGE-Enabled 헤더로 on/off)
# 3) 응답/검증 결과 요약 및 로그 tail 출력 (탐지 시 "external error: 401 ..." 확인)

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
mkdir -p logs run tmp

# ---- defaults (env override) ----
[[ -f .env ]] && source .env

# 안전한 기본값 강제 지정 (set -u 대비)
: "${HOST:=localhost}"
: "${ROOT_AGENT_PORT:=18080}"
: "${ROOT_PORT:=${ROOT_AGENT_PORT}}"
: "${CLIENT_PORT:=8086}"
: "${GATEWAY_PORT:=5500}"
: "${EXT_PAYMENT_PORT:=19083}"
: "${PAYMENT_AGENT_PORT:=18083}"            # 디버그용 Payment HTTP를 별도로 켤 때만 사용
: "${CHECK_PAYMENT_STATUS:=0}"               # 1이면 :PAYMENT_AGENT_PORT /status도 체크

# 결제 서브에이전트가 외부로 나갈 때 사용하는 JWK (서명용)
: "${PAYMENT_JWK_FILE:=keys/payment.jwk}"
: "${PAYMENT_DID:=}"   # 비워두면 키에서 유도

SAGE_MODE="on"                   # on|off (클라이언트->루트 헤더 & 루트의 /toggle-sage 동기화)
GW_MODE="tamper"                 # tamper|pass
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-$'\n[GW-ATTACK] injected by gateway'}}"
PROMPT="send 10 USDC to merchant"
FORCE_KILL=0

usage() {
  cat <<EOF
Usage: $0 [--sage on|off] [--tamper|--pass] [--attack-msg "<txt>"] [--prompt "<text>"] [--force-kill]

Examples:
  $0 --sage on --tamper
  $0 --sage off --pass
  $0 --sage on --tamper --attack-msg "[MITM TEST]" --prompt "transfer 50 USDC"
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --sage)        SAGE_MODE="${2:-on}"; shift 2 ;;
    --tamper)      GW_MODE="tamper"; shift ;;
    --pass|--passthrough) GW_MODE="pass"; shift ;;
    --attack-msg)  ATTACK_MESSAGE="${2:-}"; shift 2 ;;
    --prompt)      PROMPT="${2:-}"; shift 2 ;;
    --force-kill)  FORCE_KILL=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

need() { command -v "$1" >/dev/null 2>&1 || { echo "[ERR] '$1' not found"; exit 1; }; }
need go
need curl
if ! command -v jq >/dev/null 2>&1; then
  echo "[WARN] 'jq' not found; JSON 결과 요약이 제한됩니다. (brew/apt로 jq 설치 권장)"
fi

# ---- helper ----
wait_http() {
  local url="$1" tries="${2:-60}" delay="${3:-0.2}"
  for ((i=1;i<=tries;i++)); do
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done; return 1
}

# ---- 0) preflight ----
if [[ ! -f "$PAYMENT_JWK_FILE" ]]; then
  echo "[ERR] PAYMENT_JWK_FILE not found: $PAYMENT_JWK_FILE"
  exit 1
fi

echo "[CFG] SAGE_MODE         = $SAGE_MODE"
echo "[CFG] GATEWAY_MODE      = $GW_MODE"
echo "[CFG] ATTACK_MESSAGE    = ${#ATTACK_MESSAGE} bytes"
echo "[CFG] PROMPT            = $PROMPT"
echo "[CFG] PAYMENT_JWK_FILE  = $PAYMENT_JWK_FILE"
[[ -n "$PAYMENT_DID" ]] && echo "[CFG] PAYMENT_DID        = $PAYMENT_DID"

# ---- 1) bring up stack (minimal) ----
UP_ARGS=()
if [[ "$GW_MODE" == "pass" ]]; then UP_ARGS+=(--pass); else UP_ARGS+=(--tamper --attack-msg "$ATTACK_MESSAGE"); fi
if [[ $FORCE_KILL -eq 1 ]]; then UP_ARGS+=(--force-kill); fi

echo "[UP] starting stack via scripts/06_start_all.sh ${UP_ARGS[*]}"
PAYMENT_JWK_FILE="$PAYMENT_JWK_FILE" PAYMENT_DID="$PAYMENT_DID" \
  bash ./scripts/06_start_all.sh "${UP_ARGS[@]}"

# ---- 2) wait for endpoints ----
echo "[WAIT] endpoints"
for url in \
  "http://${HOST}:${EXT_PAYMENT_PORT}/status" \
  "http://${HOST}:${GATEWAY_PORT}/status" \
  "http://${HOST}:${ROOT_PORT}/status" \
  "http://${HOST}:${CLIENT_PORT}/api/sage/config"
do
  if wait_http "$url" 50 0.2; then
    echo "[OK] $url"
  else
    echo "[WARN] not ready: $url"
  fi
done

# 선택적으로 Payment 디버그 HTTP도 확인하고 싶다면
if [[ "${CHECK_PAYMENT_STATUS}" == "1" ]]; then
  url="http://${HOST}:${PAYMENT_AGENT_PORT}/status"
  if wait_http "$url" 20 0.2; then
    echo "[OK] $url"
  else
    echo "[WARN] not ready: $url"
  fi
fi

# ---- 3) fire a payment request through Client API ----
SAGE_HDR="false"
# (macOS bash 3 호환) 소문자 비교를 tr로 수행
SAGE_MODE_LOWER="$(printf '%s' "$SAGE_MODE" | tr '[:upper:]' '[:lower:]')"
if [[ "$SAGE_MODE_LOWER" == "on" ]]; then
  SAGE_HDR="true"
fi

REQ_PAYLOAD="$(mktemp -t req.XXXX.json)"
RESP_PAYLOAD="$(mktemp -t resp.XXXX.json)"

printf '{"prompt": %q}' "$PROMPT" > "$REQ_PAYLOAD"

echo
echo "[REQ] POST /api/payment  X-SAGE-Enabled: $SAGE_HDR  (scenario=demo)"
HTTP_CODE=$(curl -sS -o "$RESP_PAYLOAD" -w "%{http_code}" \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: $SAGE_HDR" \
  -H "X-Scenario: demo" \
  -X POST "http://${HOST}:${CLIENT_PORT}/api/payment" \
  --data-binary @"$REQ_PAYLOAD" || true)

echo "[HTTP] client-api status: $HTTP_CODE"

# ---- 4) summarize result ----
echo
echo "========== Client API Response (summary) =========="
if command -v jq >/dev/null 2>&1; then
  jq '{response:.response, verification:(.SAGEVerification // .sageVerification // null), metadata:.metadata, logs:(.logs // null)}' "$RESP_PAYLOAD" 2>/dev/null || cat "$RESP_PAYLOAD"
else
  cat "$RESP_PAYLOAD"
fi
echo "==================================================="

# 탐지 여부 추정: response에 "external error: 401" 또는 digest mismatch 포함 시, 외부 검증 실패로 MITM 감지
DETECTED="no"
if grep -q '"external error: 401' "$RESP_PAYLOAD" || grep -q 'content-digest mismatch' "$RESP_PAYLOAD"; then
  DETECTED="yes"
fi

# 대문자 출력도 tr로 (macOS bash 3 호환)
SAGE_MODE_UP="$(printf '%s' "$SAGE_MODE" | tr '[:lower:]' '[:upper:]')"
GW_MODE_UP="$(printf '%s' "$GW_MODE" | tr '[:lower:]' '[:upper:]')"

echo
echo "[RESULT] SAGE=${SAGE_MODE_UP}, GATEWAY=${GW_MODE_UP} -> MITM detected by external? => $DETECTED"

# ---- 5) helpful logs ----
echo
echo "----- external-payment.log (tail) -----"
tail -n 60 logs/external-payment.log 2>/dev/null || echo "(no log yet)"
echo
echo "----- gateway.log (tail) -----"
tail -n 60 logs/gateway.log 2>/dev/null || echo "(no log yet)"
echo
echo "----- root.log (tail) -----"
tail -n 60 logs/root.log 2>/dev/null || echo "(no log yet)"
echo
echo "----- client.log (tail) -----"
tail -n 60 logs/client.log 2>/dev/null || echo "(no log yet)"

echo
echo "[HINT] 정상 시나리오 비교:"
echo "  1) 서명 ON + 패스스루  -> 성공 (MITM 없음)"
echo "       ./scripts/demo_payment.sh --sage on  --pass"
echo "  2) 서명 ON + 조작      -> 실패 (외부에서 401, 또는 content-digest mismatch)"
echo "       ./scripts/demo_payment.sh --sage on  --tamper"
echo "  3) 서명 OFF + 조작     -> 대개 통과 (검증 없으니 감지 불가)"
echo "       ./scripts/demo_payment.sh --sage off --tamper"
