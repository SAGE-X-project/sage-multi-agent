#!/usr/bin/env bash
# SAGE ON + tampering gateway
# - Saves ONE combined JSON + code
# - Prints raw headers + pretty JSON to terminal
# - Leaves only: out/resp_sage_on.json, out/resp_sage_on.code

set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

CLIENT_PORT="${CLIENT_PORT:-8086}"
BASE="http://localhost:${CLIENT_PORT}"
OUT_DIR="out"
mkdir -p "$OUT_DIR"

# -------- helpers --------
extract_ct() {
  # Extract Content-Type value (trim spaces, lowercase)
  awk 'BEGIN{IGNORECASE=1}
       /^Content-Type:/ {
         sub(/^[^:]*:[[:space:]]*/, "", $0);
         gsub(/[[:space:]]+$/, "", $0);
         print tolower($0);
         exit
       }' "$1" \
  | tr -d '\r'
}

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

print_combined_json() {
  local file="$1"
  if command -v jq >/dev/null 2>&1; then
    jq -C . "$file" || cat "$file"
  else
    cat "$file"
  fi
}

# -------- request payload (temp) --------
# Use the API's expected shape: {"prompt": "..."}
PAYLOAD="$(mktemp)"
cat > "$PAYLOAD" <<'JSON'
{
  "prompt": "please pay 10 USDT to 0xbeef...",
  "sageEnabled": true,
  "scenario": "mitm"
}
JSON

HDR="$(mktemp)"
BODY="$(mktemp)"
JSON_OUT="${OUT_DIR}/resp_sage_on.json"
CODE_OUT="${OUT_DIR}/resp_sage_on.code"

echo "[test] POST /api/payment (SAGE ON, tamper)"
HTTP_CODE=$(curl -sS -X POST \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -H "X-Scenario: mitm" \
  --data @"$PAYLOAD" \
  -D "$HDR" \
  -o "$BODY" \
  -w "%{http_code}" \
  "${BASE}/api/payment" || true)
echo -n "$HTTP_CODE" > "$CODE_OUT"

save_combined_json "$HDR" "$BODY" "$HTTP_CODE" "$JSON_OUT"

echo "[test] HTTP $HTTP_CODE"
echo "===== HEADERS ($HDR) ====="
cat "$HDR"; echo
echo "===== JSON ($JSON_OUT) ====="
print_combined_json "$JSON_OUT"; echo

# cleanup temps
rm -f "$HDR" "$BODY" "$PAYLOAD"

echo "[test] done. Files saved:"
echo "  - $JSON_OUT ; $CODE_OUT"
echo "See also logs/payment.log, logs/gateway.log, logs/external-payment.log"
