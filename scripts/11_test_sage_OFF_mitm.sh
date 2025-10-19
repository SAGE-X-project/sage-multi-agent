#!/usr/bin/env bash
# SAGE OFF + tampering gateway
# - Toggles OFF (includes root) but skips if already OFF
# - Saves code & JSON; prints headers and JSON

set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

CLIENT_PORT="${CLIENT_PORT:-8086}"
BASE="http://localhost:${CLIENT_PORT}"
OUT_DIR="out"; mkdir -p "$OUT_DIR"

# Ports for quick status check (prefer *_AGENT_PORT if provided)
ROOT_PORT="${ROOT_PORT:-${ROOT_AGENT_PORT:-18080}}"
PAYMENT_PORT="${PAYMENT_PORT:-${PAYMENT_AGENT_PORT:-18083}}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"

extract_ct() {
  # Robustly extract Content-Type value, trim spaces, lowercase
  awk 'BEGIN{IGNORECASE=1}
       /^Content-Type:/ {
         sub(/^[^:]*:[[:space:]]*/, "", $0);  # drop header name and following spaces
         gsub(/[[:space:]]+$/, "", $0);       # trim trailing spaces
         print tolower($0);
         exit
       }' "$1" \
  | tr -d '\r'
}

# Save headers/body/code into a combined JSON (same style as 10_test_sage_ON_mitm.sh)
save_combined_json() {
  # save_combined_json <hdr_file> <body_file> <http_code> <outfile>
  local hdr="$1" body="$2" code="$3" out="$4"
  local ct; ct="$(extract_ct "$hdr")"

  if command -v jq >/dev/null 2>&1; then
    jq -n \
      --arg code  "$(printf %s "$code" | tr -d '\r')" \
      --arg ctype "${ct:-unknown}" \
      --rawfile headers "$hdr" \
      --rawfile body    "$body" '
        ($ctype | tostring | startswith("application/json")) as $isjson
        |
        {
          http_code: (($code | tonumber?) // 0),
          content_type: $ctype,
          headers_raw: $headers
        }
        +
        ( if $isjson
          then (try { body_json: ( $body | fromjson ) } catch { body_text: $body })
          else { body_text: $body }
          end )
      ' > "$out"
  else
    local b64; b64="$(base64 < "$body")"
    {
      echo "{"
      echo "  \"http_code\": ${code:-0},"
      echo "  \"content_type\": \"${ct:-unknown}\","
      echo "  \"headers_raw\": $(python - <<'PY' "$hdr"
import json,sys; print(json.dumps(open(sys.argv[1]).read()))
PY
),"
      echo "  \"body_b64\": \"${b64}\""
      echo "}"
    } > "$out"
  fi
}

echo
# Early skip: if already OFF on both root and payment, do not run the test
get_enabled() {
  local port="$1"
  local url="http://localhost:${port}/status"
  local hdr body code ct raw

  hdr="$(mktemp)"; body="$(mktemp)"
  code=$(curl -sS -m 3 -D "$hdr" -o "$body" -w "%{http_code}" "$url" || true)
  ct=$(awk 'BEGIN{IGNORECASE=1}/^Content-Type:/{sub(/^[^:]*:[[:space:]]*/,"");print;exit}' "$hdr" | tr -d '\r' | tr '[:upper:]' '[:lower:]')
  raw="$(cat "$body")"

  # 디버그: 왜 unknown 인지 이유까지 보여줌
  if [[ "$code" != "200" ]]; then
    echo "unknown"  # 반환값
    echo "[warn] $url -> HTTP $code (ct=${ct:-n/a})" >&2
    printf "[warn] body: %s\n" "$(echo "$raw" | head -c 200)" >&2
    return
  fi

  # 200인데 JSON이 아니면 unknown
  if ! command -v jq >/dev/null 2>&1 || [[ "${ct}" != application/json* ]]; then
    if echo "$raw" | grep -qi '"sage_enabled"[[:space:]]*:'; then
      # jq가 없어도 true/false 뽑기 (best-effort)
      if echo "$raw" | grep -qi '"sage_enabled"[[:space:]]*:[[:space:]]*true'; then echo "true"; else echo "false"; fi
    else
      echo "unknown"
      printf "[warn] %s returned 200 but not JSON with sage_enabled. ct=%s\n" "$url" "${ct:-n/a}" >&2
    fi
    return
  fi

  # 정상 JSON 파싱
  local v
  v=$(printf '%s' "$raw" | jq -r '.sage_enabled // "unknown"' 2>/dev/null || echo unknown)
  echo "$v"
}

cur_root=$(get_enabled "$ROOT_PORT") || cur_root=unknown
cur_pay=$(get_enabled "$PAYMENT_PORT") || cur_pay=unknown
cur_ext=$(get_enabled "$EXT_PAYMENT_PORT") || cur_ext=unknown
if [[ "$cur_root" == false && "$cur_pay" == false && "$cur_ext" == false ]]; then
  echo "[skip] SAGE already OFF on root(:$ROOT_PORT), payment(:$PAYMENT_PORT), and external(:$EXT_PAYMENT_PORT). Not running test."
  exit 0
fi

echo "[prep] ensure SAGE OFF"
# Toggle root/payment only if any of them is ON
if [[ "$cur_root" == true || "$cur_pay" == true ]]; then
  echo "  - toggling root/payment OFF via scripts/toggle_sage.sh"
  bash scripts/toggle_sage.sh off --include-root || {
    echo "[ERR] toggle_sage failed"; exit 1;
  }
  sleep 0.3
else
  echo "  - skip root/payment toggle (already OFF)"
fi

# Quick status after toggle (best-effort)
print_enabled() {
  local name="$1" port="$2"
  local v; v="$(get_enabled "$port")"
  echo "[status] ${name} :${v}"
}


# Toggle external payment OFF only if it is ON
if [[ "$cur_ext" == true ]]; then
  echo
  echo "[prep] toggle SAGE OFF on external payment..."
  curl -sS -m 3 -H "Content-Type: application/json" \
    -d '{"enabled":false}' \
    "http://localhost:${EXT_PAYMENT_PORT}/toggle-sage" >/dev/null || true
  sleep 0.2
else
  echo "[prep] external payment already OFF — skipping toggle"
fi

REQ='{"prompt":"send 100 USDC to merchant"}'
HDR="$(mktemp)"
BODY_TMP="$(mktemp)"
JSON_OUT="${OUT_DIR}/resp_sage_off.json"
CODE_OUT="${OUT_DIR}/resp_sage_off.code"

echo
echo "[test] POST /api/payment (SAGE OFF, tamper)"
HTTP_CODE=$(curl -sS \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: false" \
  -H "X-Scenario: mitm" \
  --data "${REQ}" \
  -D "$HDR" \
  -o "$BODY_TMP" \
  -w "%{http_code}" \
  "${BASE}/api/payment" || true)
echo -n "$HTTP_CODE" > "$CODE_OUT"

save_combined_json "$HDR" "$BODY_TMP" "$HTTP_CODE" "$JSON_OUT"

echo "[test] HTTP $HTTP_CODE"
echo "===== HEADERS ($HDR) ====="; cat "$HDR"; echo
echo "===== JSON ($JSON_OUT) ====="
if command -v jq >/dev/null 2>&1; then jq -C . "$JSON_OUT" || cat "$JSON_OUT"; else cat "$JSON_OUT"; fi
echo
echo "[test] done. Files saved:"
echo "  - $JSON_OUT ; $CODE_OUT"

# Save recent logs to logs/ (snapshots)
save_log() {
  local src="$1" outname="$2"; local dest="logs/${outname}"
  if [[ -f "$src" ]]; then
    tail -n 400 "$src" > "$dest" || cp "$src" "$dest" || true
    echo "  - $dest"
  else
    echo "  - $dest (source missing)"; : > "$dest"
  fi
}
echo "[logs] snapshots saved to logs/:"
save_log "logs/payment.log"          "off_payment.log"
save_log "logs/gateway.log"          "off_gateway.log"
save_log "logs/external-payment.log" "off_external-payment.log"
