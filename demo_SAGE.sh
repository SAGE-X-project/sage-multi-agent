#!/usr/bin/env bash
# Demo: Client API -> Root (routes to in-proc Payment) -> signed HTTP -> Gateway (tamper/pass)
#       -> External Agent (verify) -> back

set -Eeuo pipefail

# ---------- repo ROOT detection (robust) ----------
SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)"
if command -v git >/dev/null 2>&1 && git -C "$SCRIPT_DIR" rev-parse --show-toplevel >/dev/null 2>&1; then
  ROOT_DIR="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
else
  # If this script sits at repo root, keys/ must exist alongside it
  if [[ -d "$SCRIPT_DIR/keys" || -f "$SCRIPT_DIR/go.mod" ]]; then
    ROOT_DIR="$SCRIPT_DIR"
  else
    # fallback: parent
    ROOT_DIR="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd -P)"
  fi
fi
cd "$ROOT_DIR"
mkdir -p logs run tmp

# ---------- helpers ----------
abspath() {
  # usage: abspath <path>
  local p="$1"
  [[ -z "$p" ]] && return 0
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
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then
      return 0
    fi
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
: "${CHECK_PAYMENT_STATUS:=0}"

# Defaults → absolute paths (can be overridden by env/CLI later; we normalize again below)
: "${EXTERNAL_JWK_FILE:=$(abspath keys/external.jwk)}"
: "${EXTERNAL_KEM_JWK_FILE:=$(abspath keys/kem/external.x25519.jwk)}"
: "${MERGED_KEYS_FILE:=$(abspath merged_agent_keys.json)}"
: "${EXTERNAL_AGENT_NAME:=external}"

: "${PAYMENT_JWK_FILE:=$(abspath keys/payment.jwk)}"
: "${PAYMENT_DID:=}"

SAGE_MODE="on"
GW_MODE="tamper"
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-$'\n[GW-ATTACK] injected by gateway'}}"
PROMPT="send 10 USDC to merchant"
FORCE_KILL=0

# HPKE flags (default OFF)
HPKE_MODE="off"   # on|off
HPKE_KEYS="$(abspath generated_agent_keys.json)"

usage() {
  cat <<EOF
Usage: $0 [--sage on|off] [--tamper|--pass] [--attack-msg "<txt>"] [--prompt "<text>"] [--hpke on|off] [--hpke-keys <path>] [--force-kill]
EOF
}

# ---------- CLI parsing ----------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --sage)        SAGE_MODE="${2:-on}"; shift 2 ;;
    --tamper)      GW_MODE="tamper"; shift ;;
    --pass|--passthrough) GW_MODE="pass"; shift ;;
    --attack-msg)  ATTACK_MESSAGE="${2:-}"; shift 2 ;;
    --prompt)      PROMPT="${2:-}"; shift 2 ;;
    --hpke)
      val="${2:-off}"
      case "$(printf '%s' "$val" | tr '[:upper:]' '[:lower:]')" in
        on|true|1|yes) HPKE_MODE="on" ;;
        off|false|0|no|"") HPKE_MODE="off" ;;
        *) echo "[ERR] --hpke expects on|off (got: $val)"; exit 1 ;;
      esac
      shift 2
      ;;
    --hpke-keys)   HPKE_KEYS="$(abspath "${2:-generated_agent_keys.json}")"; shift 2 ;;
    --force-kill)  FORCE_KILL=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "[WARN] unknown arg: $1"; shift ;;
  esac
done

# normalize env overrides to absolute if they exist
EXTERNAL_JWK_FILE="$(abspath "$EXTERNAL_JWK_FILE")"
EXTERNAL_KEM_JWK_FILE="$(abspath "$EXTERNAL_KEM_JWK_FILE")"
MERGED_KEYS_FILE="$(abspath "$MERGED_KEYS_FILE")"
PAYMENT_JWK_FILE="$(abspath "$PAYMENT_JWK_FILE")"

# ---------- preflight ----------
need go
need curl
if ! command -v jq >/dev/null 2>&1; then
  echo "[WARN] 'jq' not found; will fallback to safe shell escaping for JSON."
fi

dbg_path() {
  local label="$1" p="$2"
  echo "[DBG] CWD=$(pwd)"
  echo "[DBG] $label=$p"
  ls -l "$p" 2>/dev/null || true
}

# Payment agent (in-proc) key file must exist for root/payment chain
if [[ ! -f "$PAYMENT_JWK_FILE" ]]; then
  echo "[ERR] PAYMENT_JWK_FILE not found: $PAYMENT_JWK_FILE"
  dbg_path PAYMENT_JWK_FILE "$PAYMENT_JWK_FILE"
  exit 1
fi

# External-payment service requires these (export for children too)
if [[ ! -f "$EXTERNAL_JWK_FILE" ]]; then
  echo "[ERR] EXTERNAL_JWK_FILE not found: $EXTERNAL_JWK_FILE"
  dbg_path EXTERNAL_JWK_FILE "$EXTERNAL_JWK_FILE"
  exit 1
fi
if [[ "$HPKE_MODE" == "on" ]] && [[ ! -f "$EXTERNAL_KEM_JWK_FILE" ]]; then
  echo "[ERR] EXTERNAL_KEM_JWK_FILE not found (HPKE on): $EXTERNAL_KEM_JWK_FILE"
  dbg_path EXTERNAL_KEM_JWK_FILE "$EXTERNAL_KEM_JWK_FILE"
  exit 1
fi
if [[ ! -f "$MERGED_KEYS_FILE" ]]; then
  echo "[ERR] MERGED_KEYS_FILE not found: $MERGED_KEYS_FILE"
  dbg_path MERGED_KEYS_FILE "$MERGED_KEYS_FILE"
  exit 1
fi

export EXTERNAL_JWK_FILE EXTERNAL_KEM_JWK_FILE MERGED_KEYS_FILE EXTERNAL_AGENT_NAME

echo "[CFG] SAGE_MODE                = $SAGE_MODE"
echo "[CFG] GATEWAY_MODE             = $GW_MODE"
echo "[CFG] ATTACK_MESSAGE           = ${#ATTACK_MESSAGE} bytes"
echo "[CFG] PROMPT                   = $PROMPT"
echo "[CFG] PAYMENT_JWK_FILE         = $PAYMENT_JWK_FILE"
echo "[CFG] HPKE_ENABLE              = $( [[ "$HPKE_MODE" == "on" ]] && echo 1 || echo 0 )"
echo "[CFG] HPKE_KEYS                = $HPKE_KEYS"
echo "[CFG] EXTERNAL_JWK_FILE        = $EXTERNAL_JWK_FILE"
echo "[CFG] EXTERNAL_KEM_JWK_FILE    = $EXTERNAL_KEM_JWK_FILE"
echo "[CFG] MERGED_KEYS_FILE         = $MERGED_KEYS_FILE"
echo "[CFG] EXTERNAL_AGENT_NAME      = $EXTERNAL_AGENT_NAME"

# ---------- 1) bring up stack ----------
UP_ARGS=()
if [[ "$GW_MODE" == "pass" ]]; then UP_ARGS+=(--pass); else UP_ARGS+=(--tamper --attack-msg "$ATTACK_MESSAGE"); fi
if [[ $FORCE_KILL -eq 1 ]]; then UP_ARGS+=(--force-kill); fi
UP_ARGS+=( --hpke "$HPKE_MODE" --hpke-keys "$HPKE_KEYS" )

echo "[UP] starting stack via scripts/06_start_all.sh ${UP_ARGS[*]}"

SAGE_MODE="$SAGE_MODE" PAYMENT_JWK_FILE="$PAYMENT_JWK_FILE" PAYMENT_DID="$PAYMENT_DID" \
  bash "$ROOT_DIR/scripts/06_start_all.sh" "${UP_ARGS[@]}"

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

# ---------- 3) fire a request ----------
REQ_PAYLOAD="$(mktemp -t req.XXXX.json)"
RESP_PAYLOAD="$(mktemp -t resp.XXXX.json)"

if command -v jq >/dev/null 2>&1; then
  printf '{"prompt": %s}\n' "$(jq -Rsa . <<<"$PROMPT")" > "$REQ_PAYLOAD"
else
  esc=${PROMPT//\\/\\\\}; esc=${esc//\"/\\\"}
  printf '{"prompt":"%s"}\n' "$esc" > "$REQ_PAYLOAD"
fi

SAGE_MODE_LOWER="$(printf '%s' "$SAGE_MODE" | tr '[:upper:]' '[:lower:]')"
SAGE_HDR="false"
[[ "$SAGE_MODE_LOWER" == "on" ]] && SAGE_HDR="true"

echo
echo "[REQ] POST /api/request X-SAGE-Enabled: $SAGE_HDR  (scenario=demo)"
HTTP_CODE=$(curl -sS -o "$RESP_PAYLOAD" -w "%{http_code}" \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: $SAGE_HDR" \
  -H "X-Scenario: demo" \
  -X POST "http://${HOST}:${CLIENT_PORT}/api/request" \
  --data-binary @"$REQ_PAYLOAD" || true)

echo "[HTTP] client-api status: $HTTP_CODE"

echo
echo "========== Client API Response (summary) =========="
if command -v jq >/dev/null 2>&1; then
  jq '{response:.response, verification:(.SAGEVerification // .sageVerification // null), metadata:.metadata, logs:(.logs // null)}' "$RESP_PAYLOAD" 2>/dev/null || cat "$RESP_PAYLOAD"
else
  cat "$RESP_PAYLOAD"
fi
echo "==================================================="

DETECTED="no"
if grep -q '"external error: 401' "$RESP_PAYLOAD" || grep -q 'content-digest mismatch' "$RESP_PAYLOAD"; then
  DETECTED="yes"
fi

SAGE_MODE_UP="$(printf '%s' "$SAGE_MODE" | tr '[:lower:]' '[:upper:]')"
GW_MODE_UP="$(printf '%s' "$GW_MODE" | tr '[:lower:]' '[:upper:]')"

echo
echo "[RESULT] SAGE=${SAGE_MODE_UP}, GATEWAY=${GW_MODE_UP} -> MITM detected by external? => $DETECTED"

echo
echo "----- external-payment.log (tail) -----"
tail -n 60 "$ROOT_DIR/logs/external-payment.log" 2>/dev/null || echo "(no log yet)"
echo
echo "----- gateway.log (tail) -----"
tail -n 60 "$ROOT_DIR/logs/gateway.log" 2>/dev/null || echo "(no log yet)"
echo
echo "----- root.log (tail) -----"
tail -n 60 "$ROOT_DIR/logs/root.log" 2>/dev/null || echo "(no log yet)"
echo
echo "----- client.log (tail) -----"
tail -n 60 "$ROOT_DIR/logs/client.log" 2>/dev/null || echo "(no log yet)"

echo
echo "[HINT] Compare scenarios:"
echo "  1) SAGE ON + pass      -> success (no MITM)"
echo "  2) SAGE ON + tamper    -> 401 or digest mismatch (detected)"
echo "  3) SAGE OFF + tamper   -> likely passes (no detection)"

echo
echo "[UP] ensuring clean state via scripts/01_kill_ports.sh"
bash "$ROOT_DIR/scripts/01_kill_ports.sh" || true
