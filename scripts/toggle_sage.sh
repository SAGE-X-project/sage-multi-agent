#!/usr/bin/env bash
# Toggle SAGE ON/OFF for agents.
# - Skips toggle if already in desired state
# - Can include root (pass --include-root)
# - Requires each agent to expose /status and /toggle-sage *without* DID auth

set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

usage() {
  echo "Usage: $0 on|off [--include-root]"
  exit 1
}

[[ $# -ge 1 ]] || usage
MODE="$1"; shift || true
INCLUDE_ROOT=false
if [[ "${1:-}" == "--include-root" ]]; then INCLUDE_ROOT=true; fi
[[ "$MODE" == "on" || "$MODE" == "off" ]] || usage

# Ports from .env (with sane defaults)
ROOT_PORT="${ROOT_AGENT_PORT:-18080}"
PLANNING_PORT="${PLANNING_AGENT_PORT:-18081}"
ORDERING_PORT="${ORDERING_AGENT_PORT:-18082}"
PAYMENT_PORT="${PAYMENT_AGENT_PORT:-18083}"

declare -a AGENTS
$INCLUDE_ROOT && AGENTS+=("root:$ROOT_PORT")
AGENTS+=("planning:$PLANNING_PORT" "ordering:$ORDERING_PORT" "payment:$PAYMENT_PORT")

want_enabled=true
[[ "$MODE" == "off" ]] && want_enabled=false

json_bool() { $want_enabled && echo true || echo false; }

get_enabled() {
  # get_enabled <port>  -> prints true/false/unknown (without jq fallback)
  local port="$1"
  local body hdr tmp
  hdr="$(mktemp)"; tmp="$(mktemp)"
  curl -sS -m 2 -D "$hdr" -o "$tmp" "http://localhost:${port}/status" >/dev/null 2>&1 || { echo "unknown"; return; }
  local ct
  ct="$(awk 'BEGIN{IGNORECASE=1}/^Content-Type:/{print tolower($2);exit}' "$hdr" | tr -d '\r')"
  if command -v jq >/dev/null 2>&1 && [[ "${ct:-}" == application/json* ]]; then
    jq -r 'try .sage_enabled catch "unknown"' "$tmp" 2>/dev/null || echo "unknown"
  else
    # Very simple grep-based fallback
    if grep -qi '"sage_enabled"[[:space:]]*:[[:space:]]*true' "$tmp"; then echo true
    elif grep -qi '"sage_enabled"[[:space:]]*:[[:space:]]*false' "$tmp"; then echo false
    else echo "unknown"; fi
  fi
}

toggle_one() {
  # toggle_one <name> <port>
  local name="$1" port="$2"
  local cur; cur="$(get_enabled "$port")"
  if [[ "$cur" != "unknown" ]]; then
    if [[ "$cur" == "$(json_bool)" ]]; then
      echo "[skip] $name already ${MODE} on :${port}"
      return 0
    fi
  fi
  echo "[toggle] $name (${MODE}) -> :${port}"
  curl -sS -m 3 -H "Content-Type: application/json" \
    -d "{\"enabled\":$(json_bool)}" \
    "http://localhost:${port}/toggle-sage" || echo " (no response)"
  echo
}

for a in "${AGENTS[@]}"; do
  IFS=':' read -r n p <<<"$a"
  toggle_one "$n" "$p"
done

echo "[done] SAGE ${MODE} applied (best-effort)."
