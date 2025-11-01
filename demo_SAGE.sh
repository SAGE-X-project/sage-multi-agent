#!/usr/bin/env bash
# Demo: Client API -> Root (routes) -> Gateway (tamper/pass) -> External Payment

# --- force bash even if invoked via `sh` ---
if [ -z "${BASH_VERSION:-}" ]; then exec /usr/bin/env bash "$0" "$@"; fi

set -Eeuo pipefail

# ---------- repo ROOT detection (robust) ----------
SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)"
if command -v git >/dev/null 2>&1 && git -C "$SCRIPT_DIR" rev-parse --show-toplevel >/dev/null 2>&1; then
  ROOT_DIR="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
else
  if [[ -d "$SCRIPT_DIR/keys" || -f "$SCRIPT_DIR/go.mod" ]]; then
    ROOT_DIR="$SCRIPT_DIR"
  else
    ROOT_DIR="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd -P)"
  fi
fi
cd "$ROOT_DIR"
mkdir -p logs run tmp

# ---------- helpers ----------
abspath() {
  # usage: abspath <path or empty>
  local p="${1:-}"
  [[ -z "$p" ]] && { printf '\n'; return 0; }
  if [[ "$p" = /* ]]; then
    printf '%s\n' "$p"
  else
    printf '%s\n' "$ROOT_DIR/$p"
  fi
}
need() { command -v "$1" >/dev/null 2>&1 || { echo "[ERR] '$1' not found"; exit 1; }; }
wait_http() {
  local url="$1" tries="${2:-60}" delay="${3:-0.2}"
  for ((i=1;i<=tries;i++)); do
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then return 0; fi
    sleep "$delay"
  done; return 1
}

# ---------- load .env (export) ----------
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ROOT_DIR/.env"
  set +a
fi

# ---------- defaults ----------
: "${HOST:=localhost}"
: "${ROOT_AGENT_PORT:=18080}"
: "${ROOT_PORT:=${ROOT_AGENT_PORT}}"
: "${CLIENT_PORT:=8086}"
: "${GATEWAY_PORT:=5500}"
: "${EXT_PAYMENT_PORT:=19083}"
: "${PAYMENT_AGENT_PORT:=18083}"

# Default paths (will be normalized later)
: "${PAYMENT_JWK_FILE:=keys/external.jwk}"
: "${PAYMENT_KEM_JWK_FILE:=keys/kem/external.x25519.jwk}"
: "${MERGED_KEYS_FILE:=merged_agent_keys.json}"
: "${EXTERNAL_AGENT_NAME:=external}"
: "${PAYMENT_JWK_FILE:=keys/payment.jwk}"
: "${PAYMENT_DID:=}"

SAGE_MODE="on"          # on|off
GW_MODE="tamper"        # tamper|pass
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-$'\n[GW-ATTACK] injected by gateway'}}"
PROMPT="send 10 USDC to merchant"
FORCE_KILL=0

# HPKE flags
HPKE_MODE="off"         # on|off
HPKE_KEYS="generated_agent_keys.json"

usage() {
  cat <<EOF
Usage: $0 [--sage on|off] [--tamper|--pass] [--attack-msg "<txt>"] [--prompt "<text>"] [--hpke on|off] [--hpke-keys <path>] [--force-kill]
EOF
}

# ---------- CLI parsing ----------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --sage=*)
      SAGE_MODE="$(tr '[:upper:]' '[:lower:]'<<<"${1#*=}")"; shift ;;
    --sage)
      SAGE_MODE="$(tr '[:upper:]' '[:lower:]'<<<"${2:-on}")"; shift 2 ;;

    --hpke=*)
      HPKE_MODE="$(tr '[:upper:]' '[:lower:]'<<<"${1#*=}")"; shift ;;
    --hpke)
      HPKE_MODE="$(tr '[:upper:]' '[:lower:]'<<<"${2:-off}")"; shift 2 ;;

    --hpke-keys)
      HPKE_KEYS="${2:-generated_agent_keys.json}"; shift 2 ;;

    --tamper)  GW_MODE="tamper"; shift ;;
    --pass|--passthrough) GW_MODE="pass"; shift ;;
    --attack-msg) ATTACK_MESSAGE="${2:-}"; shift 2 ;;
    --prompt)  PROMPT="${2:-}"; shift 2 ;;
    --force-kill) FORCE_KILL=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

need go
need curl
if ! command -v jq >/dev/null 2>&1; then
  echo "[WARN] 'jq' not found; will fallback to safe shell escaping for JSON."
fi

# ---------- normalize to absolute paths (safe under set -u) ----------
PAYMENT_JWK_FILE="$(abspath "${PAYMENT_JWK_FILE:-}")"
PAYMENT_KEM_JWK_FILE="$(abspath "${PAYMENT_KEM_JWK_FILE:-}")"
MERGED_KEYS_FILE="$(abspath "${MERGED_KEYS_FILE:-}")"
PAYMENT_JWK_FILE="$(abspath "${PAYMENT_JWK_FILE:-}")"
HPKE_KEYS="$(abspath "${HPKE_KEYS:-generated_agent_keys.json}")"

# If HPKE is off, only blank out HPKE-related files (keep signing JWK for SAGE)
if [[ "$HPKE_MODE" != "on" ]]; then
  PAYMENT_KEM_JWK_FILE=""
  MERGED_KEYS_FILE=""
fi

# ---------- preflight ----------
dbg_path() {
  local label="$1" p="$2"
  echo "[DBG] $label=$p"
  [[ -n "$p" ]] && ls -l "$p" 2>/dev/null || true
}

# Root signing key is only required when SAGE is on
if [[ "$SAGE_MODE" == "on" ]] && [[ ! -f "$PAYMENT_JWK_FILE" ]]; then
  echo "[ERR] PAYMENT_JWK_FILE not found (SAGE on): $PAYMENT_JWK_FILE"
  dbg_path PAYMENT_JWK_FILE "$PAYMENT_JWK_FILE"
  exit 1
fi

# HPKE key files are only required when HPKE is on
if [[ "$HPKE_MODE" == "on" ]]; then
  if [[ -z "$PAYMENT_JWK_FILE" || ! -f "$PAYMENT_JWK_FILE" ]]; then
    echo "[ERR] PAYMENT_JWK_FILE not found: ${PAYMENT_JWK_FILE:-<empty>}"
    dbg_path PAYMENT_JWK_FILE "$PAYMENT_JWK_FILE"
    exit 1
  fi
  if [[ -z "$PAYMENT_KEM_JWK_FILE" || ! -f "$PAYMENT_KEM_JWK_FILE" ]]; then
    echo "[ERR] PAYMENT_KEM_JWK_FILE not found: ${PAYMENT_KEM_JWK_FILE:-<empty>}"
    dbg_path PAYMENT_KEM_JWK_FILE "$PAYMENT_KEM_JWK_FILE"
    exit 1
  fi
  if [[ -z "$MERGED_KEYS_FILE" || ! -f "$MERGED_KEYS_FILE" ]]; then
    echo "[ERR] MERGED_KEYS_FILE not found: ${MERGED_KEYS_FILE:-<empty>}"
    dbg_path MERGED_KEYS_FILE "$MERGED_KEYS_FILE"
    exit 1
  fi
fi

# Export only what is needed
export EXTERNAL_AGENT_NAME
if [[ "$HPKE_MODE" == "on" ]]; then
  export PAYMENT_JWK_FILE PAYMENT_KEM_JWK_FILE MERGED_KEYS_FILE
fi

echo "[CFG] SAGE_MODE=${SAGE_MODE}  GW_MODE=${GW_MODE}  HPKE_MODE=${HPKE_MODE}"
echo "[CFG] HPKE_KEYS=${HPKE_KEYS}"
echo "[CFG] PAYMENT_JWK_FILE=${PAYMENT_JWK_FILE}"
echo "[CFG] PAYMENT_JWK_FILE=${PAYMENT_JWK_FILE:-<empty>}"
echo "[CFG] PAYMENT_KEM_JWK_FILE=${PAYMENT_KEM_JWK_FILE:-<empty>}"
echo "[CFG] MERGED_KEYS_FILE=${MERGED_KEYS_FILE:-<empty>}"

# ---------- 1) bring up stack ----------
UP_ARGS=()
if [[ "$GW_MODE" == "pass" ]]; then UP_ARGS+=(--pass); else UP_ARGS+=(--tamper --attack-msg "$ATTACK_MESSAGE"); fi
if [[ $FORCE_KILL -eq 1 ]]; then UP_ARGS+=(--force-kill); fi
UP_ARGS+=( --hpke "$HPKE_MODE" --hpke-keys "$HPKE_KEYS" )

echo "[UP] starting stack via scripts/06_start_all.sh --sage ${SAGE_MODE} ${UP_ARGS[*]}"
SAGE_MODE="$SAGE_MODE" PAYMENT_JWK_FILE="$PAYMENT_JWK_FILE" PAYMENT_DID="$PAYMENT_DID" \
  bash "$ROOT_DIR/scripts/06_start_all.sh" --sage "$SAGE_MODE" "${UP_ARGS[@]}"

# ---------- 2) wait for endpoints ----------
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

# ---------- 3) fire a request (per-request toggles via headers only) ----------
REQ_PAYLOAD="$(mktemp -t req.XXXX.json)"
RESP_PAYLOAD="$(mktemp -t resp.XXXX.json)"

if command -v jq >/dev/null 2>&1; then
  jq -n --arg p "$PROMPT" '{prompt:$p}' > "$REQ_PAYLOAD"
else
  # naive escape
  esc=${PROMPT//\\/\\\\}; esc=${esc//\"/\\\"}
  printf '{"prompt":"%s"}\n' "$esc" > "$REQ_PAYLOAD"
fi

HTTP_CODE=$(curl -sS -o "$RESP_PAYLOAD" -w "%{http_code}" \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: $( [[ "$SAGE_MODE" == "on" ]] && echo true || echo false )" \
  -H "X-HPKE-Enabled: $( [[ "$HPKE_MODE" == "on" ]] && echo true || echo false )" \
  -X POST "http://${HOST}:${CLIENT_PORT}/api/request" \
  --data-binary @"$REQ_PAYLOAD" || true)
  
echo "[HTTP] client-api status: $HTTP_CODE"
if command -v jq >/dev/null 2>&1; then
  jq '{response:.response, verification:(.SAGEVerification // .sageVerification // null), metadata:.metadata, logs:(.logs // null)}' "$RESP_PAYLOAD" 2>/dev/null || cat "$RESP_PAYLOAD"
else
  cat "$RESP_PAYLOAD"
fi

echo
echo "----- payment.log (tail) -----"
tail -n 60 "$ROOT_DIR/logs/payment.log" 2>/dev/null || echo "(no log yet)"
echo "----- gateway.log (tail) -----"
tail -n 60 "$ROOT_DIR/logs/gateway.log" 2>/dev/null || echo "(no log yet)"
echo "----- root.log (tail) -----"
tail -n 60 "$ROOT_DIR/logs/root.log" 2>/dev/null || echo "(no log yet)"
echo "----- client.log (tail) -----"
tail -n 60 "$ROOT_DIR/logs/client.log" 2>/dev/null || echo "(no log yet)"

echo
echo "[CLEANUP] ensuring clean state via scripts/01_kill_ports.sh"
if [[ -f "$ROOT_DIR/scripts/01_kill_ports.sh" ]]; then
  ( bash "$ROOT_DIR/scripts/01_kill_ports.sh" --force || bash "$ROOT_DIR/scripts/01_kill_ports.sh" || true )
else
  echo "[WARN] cleanup script not found: $ROOT_DIR/scripts/01_kill_ports.sh"
fi
