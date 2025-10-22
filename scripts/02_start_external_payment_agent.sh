#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
[[ -f .env ]] && source .env

HOST="${HOST:-localhost}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"
EXTERNAL_IMPL="${EXTERNAL_IMPL:-agent}"   # .env 우선, 없으면 agent

mkdir -p logs pids

kill_port() {
  local port="$1"; local pids
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -z "$pids" ]] && return
  echo "[kill] external-payment on :$port -> $pids"
  kill $pids 2>/dev/null || true
  sleep 0.2
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

echo "[cfg] EXTERNAL_IMPL=${EXTERNAL_IMPL}"
kill_port "$EXT_PAYMENT_PORT"

case "$EXTERNAL_IMPL" in
  agent)
    # 에이전트 엔트리 확인 (go run 또는 사전 빌드된 바이너리 둘 다 허용)
    if [[ -x bin/external-payment-agent ]]; then
      echo "[start] External Payment (AGENT, verify=ON) :${EXT_PAYMENT_PORT} [bin]"
      nohup bin/external-payment-agent -port "$EXT_PAYMENT_PORT" \
        > logs/external-payment.log 2>&1 & echo $! > pids/external-payment.pid

    elif [[ -f cmd/external-payment-agent/main.go ]]; then
      echo "[start] External Payment (AGENT, verify=ON) :${EXT_PAYMENT_PORT} [go run]"
      # 필요하면 여기에서 체인 ENV (RPC_URL, REGISTRY_ADDR 등) export
      nohup go run ./cmd/external-payment-agent/main.go \
        -port "$EXT_PAYMENT_PORT" \
        > logs/external-payment.log 2>&1 & echo $! > pids/external-payment.pid

    else
      echo "[ERR] EXTERNAL_IMPL=agent 이지만 cmd/external-payment-agent/main.go 가 없습니다."
      echo "      에이전트 코드를 추가하거나, 임시로 EXTERNAL_IMPL=echo 로 실행하세요."
      exit 1
    fi
    ;;

  echo)
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

    echo "[start] External Payment (ECHO, verify=OFF) :${EXT_PAYMENT_PORT}"
    nohup go run ./cmd/ext-payment-echo/main.go \
      -port "$EXT_PAYMENT_PORT" \
      > logs/external-payment.log 2>&1 & echo $! > pids/external-payment.pid
    ;;

  *)
    echo "[ERR] Unknown EXTERNAL_IMPL=$EXTERNAL_IMPL (use 'agent' or 'echo')"
    exit 1
    ;;
esac

if ! wait_http "http://${HOST}:${EXT_PAYMENT_PORT}/status" 40 0.25; then
  echo "[FAIL] external-payment failed to respond on /status"
  tail -n 120 logs/external-payment.log || true
  exit 1
fi

echo "[ok] logs: logs/external-payment.log  pid: $(cat pids/external-payment.pid 2>/dev/null || echo '?')"
