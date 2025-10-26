#!/usr/bin/env bash
# Start the external payment server.
# - agent mode: verifies RFC9421 + Content-Digest with DID middleware
# - echo  mode: no verification, just returns a static JSON
#
# Flags passed through to the Go binary:
#   -port <n>
#   -require[=true|false]   # require RFC9421 signature on requests

set -Eeuo pipefail

# ---------- Resolve repo root ----------
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

# ---------- Config (env overridable) ----------
HOST="${HOST:-localhost}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"
EXTERNAL_IMPL="${EXTERNAL_IMPL:-agent}"      # "agent" or "echo"
EXTERNAL_REQUIRE_SIG="${EXTERNAL_REQUIRE_SIG:-1}"  # 1=require, 0=optional

mkdir -p logs pids

# ---------- Helpers ----------
kill_port() {
  local port="$1"
  local pids
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -z "$pids" ]] && return
  echo "[kill] external-payment on :$port -> $pids"
  kill $pids 2>/dev/null || true
  sleep 0.25
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

bool_word() {
  # convert 1/0,true/false,yes/no,on/off → true|false (defaults to  false)
  local v="${1:-}"
  v="$(echo "$v" | tr '[:upper:]' '[:lower:]')"
  case "$v" in
    1|true|on|yes)  echo "true" ;;
    0|false|off|no) echo "false" ;;
    *)              echo "false" ;;
  esac
}

# ---------- Show effective config ----------
echo "[cfg] EXTERNAL_IMPL=${EXTERNAL_IMPL}"
echo "[cfg] EXTERNAL_REQUIRE_SIG=${EXTERNAL_REQUIRE_SIG}"
echo "[cfg] EXT_PAYMENT_PORT=${EXT_PAYMENT_PORT}"

# ---------- Ensure previous process is stopped ----------
kill_port "$EXT_PAYMENT_PORT"

# Build -require arg
REQ_ARGS=()
if [[ "$(bool_word "$EXTERNAL_REQUIRE_SIG")" == "true" ]]; then
  # Passing -require (no value) sets it to true in Go's flag parser
  REQ_ARGS+=( "-require" )
else
  REQ_ARGS+=( "-require=false" )
fi

# ---------- Launch ----------
case "$EXTERNAL_IMPL" in
  agent)
    if [[ -x bin/external-payment ]]; then
      echo "[start] External Payment (AGENT, requireSig=$(bool_word "$EXTERNAL_REQUIRE_SIG")) :${EXT_PAYMENT_PORT} [bin]"
      nohup bin/external-payment \
        -port "$EXT_PAYMENT_PORT" \
        "${REQ_ARGS[@]}" \
        > logs/external-payment.log 2>&1 & echo $! > pids/external-payment.pid

    elif [[ -f cmd/external-payment/main.go ]]; then
      echo "[start] External Payment (AGENT, requireSig=$(bool_word "$EXTERNAL_REQUIRE_SIG")) :${EXT_PAYMENT_PORT} [go run]"
      nohup go run ./cmd/external-payment/main.go \
        -port "$EXT_PAYMENT_PORT" \
        "${REQ_ARGS[@]}" \
        > logs/external-payment.log 2>&1 & echo $! > pids/external-payment.pid

    else
      echo "[ERR] EXTERNAL_IMPL=agent but cmd/external-payment/main.go not found."
      echo "      Provide the agent code or run with EXTERNAL_IMPL=echo."
      exit 1
    fi
    ;;

  echo)
    # Simple echo server (no signature verification)
    if [[ ! -f cmd/ext-payment-echo/main.go ]]; then
      echo "[GEN] Creating minimal external echo at cmd/ext-payment-echo/main.go"
      mkdir -p cmd/ext-payment-echo
      cat > cmd/ext-payment-echo/main.go <<'EOF'
// Minimal external echo (NO signature verify)
package main

import (
  "encoding/json"
  "flag"
  "fmt"
  "log"
  "net/http"
)

func main() {
  port := flag.Int("port", 19083, "port")
  flag.Parse()

  mux := http.NewServeMux()
  mux.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type","application/json")
    w.Write([]byte(`{"id":"ext","from":"external","to":"payment","content":"OK (external echo)","type":"response"}`))
  })
  mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type","application/json")
    json.NewEncoder(w).Encode(map[string]any{
      "name":"external-echo",
      "type":"payment",
      "sage_enabled": false,
    })
  })
  addr := fmt.Sprintf(":%d", *port)
  log.Printf("external echo on %s\n", addr)
  log.Fatal(http.ListenAndServe(addr, mux))
}
EOF
    fi

    echo "[start] External Payment (ECHO) :${EXT_PAYMENT_PORT}"
    nohup go run ./cmd/ext-payment-echo/main.go \
      -port "$EXT_PAYMENT_PORT" \
      > logs/external-payment.log 2>&1 & echo $! > pids/external-payment.pid
    ;;

  *)
    echo "[ERR] Unknown EXTERNAL_IMPL=$EXTERNAL_IMPL (use 'agent' or 'echo')"
    exit 1
    ;;
esac

# ---------- Health check ----------
if ! wait_http "http://${HOST}:${EXT_PAYMENT_PORT}/status" 40 0.25; then
  echo "[FAIL] external-payment failed to respond on /status"
  tail -n 120 logs/external-payment.log || true
  exit 1
fi

echo "[ok] logs: logs/external-payment.log  pid: $(cat pids/external-payment.pid 2>/dev/null || echo '?')"
