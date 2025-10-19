#!/usr/bin/env bash
# One-click launcher for: external payment, gateway, planning, ordering, internal payment, root, client API
# - Auto-generates cmd/{planning,ordering}/main.go if missing (using your module path)
# - Creates logs/
# - Starts everything and probes health

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
mkdir -p logs

[[ -f .env ]] && source .env
# Ensure debug flags from .env are exported to child processes
[[ -n "${PAYMENT_DEBUG_SIG:-}" ]] && export PAYMENT_DEBUG_SIG

# Resolve module path (fallback to repo default)
MOD_PATH="$(go list -m -f '{{.Path}}' 2>/dev/null || echo 'github.com/sage-x-project/sage-multi-agent')"

# ---------- Ports (support both legacy and *_AGENT_PORT) ----------
HOST="${HOST:-localhost}"

ROOT_PORT="${ROOT_PORT:-${ROOT_AGENT_PORT:-18080}}"
PLANNING_PORT="${PLANNING_PORT:-${PLANNING_AGENT_PORT:-18081}}"
ORDERING_PORT="${ORDERING_PORT:-${ORDERING_AGENT_PORT:-18082}}"
PAYMENT_PORT="${PAYMENT_PORT:-${PAYMENT_AGENT_PORT:-18083}}"
CLIENT_PORT="${CLIENT_PORT:-8086}"

GATEWAY_PORT="${GATEWAY_PORT:-5500}"
EXT_PAYMENT_PORT="${EXT_PAYMENT_PORT:-19083}"

EXTERNAL_IMPL="${EXTERNAL_IMPL:-agent}"
ATTACK_MESSAGE="${ATTACK_MESSAGE:-${ATTACK_MSG:-'\n[GW-ATTACK] injected by gateway'}}"

require() { command -v "$1" >/dev/null 2>&1 || { echo "[ERR] '$1' not found"; exit 1; }; }
require go
require curl

wait_tcp() {
  local host="$1" port="$2" tries="${3:-60}" delay="${4:-0.2}"
  for ((i=1;i<=tries;i++)); do
    if { exec 3<>/dev/tcp/"$host"/"$port"; } >/dev/null 2>&1; then
      exec 3>&- 3<&-
      return 0
    fi
    sleep "$delay"
  done
  return 1
}

wait_http() {
  local url="$1" tries="${2:-30}" delay="${3:-0.3}"
  for ((i=1;i<=tries;i++)); do
    if curl -sSf -m 1 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done
  return 1
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
  echo "[KILL] Port :$port occupied → killing ($pids)"
  kill $pids 2>/dev/null || true
  sleep 0.2
  pids="$(lsof -ti tcp:"$port" -sTCP:LISTEN || true)"
  [[ -n "$pids" ]] && kill -9 $pids 2>/dev/null || true
}

start_bg() {
  local name="$1" port="$2"; shift 2
  local cmd=( "$@" )
  kill_port "$port"
  echo "[START] $name on :$port"
  printf "[CMD] %s\n" "${cmd[@]}"
  ( "${cmd[@]}" ) >"logs/${name}.log" 2>&1 &
  if ! wait_tcp "$HOST" "$port" 60 0.2; then
    tail_fail "$name"
    exit 1
  fi
}

# ---------- Auto-generate planning/ordering mains if missing ----------
gen_main_if_missing() {
  local path="$1" pkg="$2" defPort="$3" env1="$4" env2="$5"
  local dir="cmd/${pkg}"
  local file="${dir}/main.go"
  [[ -f "$file" ]] && return
  echo "[GEN] creating ${file}"
  mkdir -p "$dir"
  cat > "$file" <<EOF
package main

import (
  "flag"
  "log"
  "os"
  "strconv"

  "${MOD_PATH}/${pkg}"
)

func envPort(names []string, def int) int {
  for _, n := range names {
    if v := os.Getenv(n); v != "" {
      if p, err := strconv.Atoi(v); err == nil && p > 0 {
        return p
      }
    }
  }
  return def
}

func main() {
  defPort := envPort([]string{"${env1}", "${env2}"}, ${defPort})
  port := flag.Int("port", defPort, "HTTP port for ${pkg} agent")
  sage := flag.Bool("sage", true, "enable SAGE verification (inbound)")
  flag.Parse()

  ag := ${pkg}.New${titlecase(pkg)}Agent("${pkg}", *port)
  ag.SAGEEnabled = *sage

  log.Printf("[${pkg}] starting on :%d (SAGE=%v)", *port, *sage)
  if err := ag.Start(); err != nil {
    log.Fatal(err)
  }
}
EOF
}

# little helper used in template
titlecase() { echo "$1" | awk '{print toupper(substr($0,1,1)) substr($0,2)}'; }

gen_main_if_missing "cmd/planning/main.go" "planning" 18081 "PLANNING_PORT" "PLANNING_AGENT_PORT"
gen_main_if_missing "cmd/ordering/main.go" "ordering" 18082 "ORDERING_PORT" "ORDERING_AGENT_PORT"

# ---------- 1) External payment ----------
if [[ "$EXTERNAL_IMPL" == "agent" ]]; then
  if [[ -f cmd/payment/main.go ]]; then
    start_bg "external-payment" "$EXT_PAYMENT_PORT" \
      go run ./cmd/payment/main.go \
        -port "$EXT_PAYMENT_PORT"
    if ! wait_http "http://${HOST}:${EXT_PAYMENT_PORT}/status" 30 0.3; then
      tail_fail "external-payment"
      echo "[WARN] Falling back to echo external..."
      EXTERNAL_IMPL="echo"
    fi
  else
    echo "[WARN] cmd/payment/main.go not found → using echo external"
    EXTERNAL_IMPL="echo"
  fi
fi

if [[ "$EXTERNAL_IMPL" == "echo" ]]; then
  if [[ ! -f cmd/ext-payment-echo/main.go ]]; then
    echo "[GEN] creating cmd/ext-payment-echo/main.go"
    mkdir -p cmd/ext-payment-echo
    cat > cmd/ext-payment-echo/main.go <<'EOF'
// Minimal external echo without deps
package main

import (
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
    w.Write([]byte(`{"name":"external-echo","type":"payment","sage_enabled":false}`))
  })
  addr := fmt.Sprintf(":%d", *port)
  log.Printf("external echo on %s\n", addr)
  log.Fatal(http.ListenAndServe(addr, mux))
}
EOF
  fi

  start_bg "external-payment" "$EXT_PAYMENT_PORT" \
    go run ./cmd/ext-payment-echo/main.go \
      -port "$EXT_PAYMENT_PORT"

  if ! wait_http "http://${HOST}:${EXT_PAYMENT_PORT}/status" 30 0.3; then
    tail_fail "external-payment"
    exit 1
  fi
fi

# ---------- 2) Gateway ----------
if [[ -f cmd/gateway/main.go ]]; then
  start_bg "gateway" "$GATEWAY_PORT" \
    go run ./cmd/gateway/main.go \
      -listen ":${GATEWAY_PORT}" \
      -upstream "http://${HOST}:${EXT_PAYMENT_PORT}" \
      -attack-msg "${ATTACK_MESSAGE}"
else
  echo "[SKIP] gateway main not found"
fi

# ---------- 3) Planning ----------
start_bg "planning" "$PLANNING_PORT" \
  go run ./cmd/planning/main.go -port "$PLANNING_PORT"

# ---------- 4) Ordering ----------
start_bg "ordering" "$ORDERING_PORT" \
  go run ./cmd/ordering/main.go -port "$ORDERING_PORT"

# ---------- 5) Internal Payment (routes to gateway as external) ----------
if [[ -f cmd/payment/main.go ]]; then
  start_bg "payment" "$PAYMENT_PORT" \
    go run ./cmd/payment/main.go \
      -port "$PAYMENT_PORT" \
      -external "http://${HOST}:${GATEWAY_PORT}"
else
  echo "[SKIP] payment agent main not found"
fi

# ---------- 6) Root ----------
if [[ -f cmd/root/main.go ]]; then
  start_bg "root" "$ROOT_PORT" \
    go run ./cmd/root/main.go \
      -port "$ROOT_PORT" \
      -payment "http://${HOST}:${PAYMENT_PORT}" \
      -planning "http://${HOST}:${PLANNING_PORT}" \
      -ordering "http://${HOST}:${ORDERING_PORT}"
else
  echo "[SKIP] root agent main not found"
fi

# ---------- 7) Client API ----------
if [[ -f cmd/client/main.go ]]; then
  start_bg "client" "$CLIENT_PORT" \
    go run ./cmd/client/main.go \
      -port "$CLIENT_PORT" \
      -root "http://${HOST}:${ROOT_PORT}" \
      -payment "http://${HOST}:${PAYMENT_PORT}"
else
  echo "[SKIP] client api main not found"
fi

# ---------- Summary ----------
echo "--------------------------------------------------"
printf "[CHK] %-22s %s\n" "External Payment" "http://${HOST}:${EXT_PAYMENT_PORT}/status"
printf "[CHK] %-22s %s\n" "Gateway (TCP)"     "tcp://${HOST}:${GATEWAY_PORT}"
printf "[CHK] %-22s %s\n" "Planning"          "http://${HOST}:${PLANNING_PORT}/status"
printf "[CHK] %-22s %s\n" "Ordering"          "http://${HOST}:${ORDERING_PORT}/status"
printf "[CHK] %-22s %s\n" "Payment"           "http://${HOST}:${PAYMENT_PORT}/status"
printf "[CHK] %-22s %s\n" "Root"              "http://${HOST}:${ROOT_PORT}/status"
printf "[CHK] %-22s %s\n" "Client API"        "http://${HOST}:${CLIENT_PORT}/api/sage/config"
echo "--------------------------------------------------"

for url in \
  "http://${HOST}:${EXT_PAYMENT_PORT}/status" \
  "http://${HOST}:${PLANNING_PORT}/status" \
  "http://${HOST}:${ORDERING_PORT}/status" \
  "http://${HOST}:${PAYMENT_PORT}/status" \
  "http://${HOST}:${ROOT_PORT}/status" \
  "http://${HOST}:${CLIENT_PORT}/api/sage/config"; do
  if curl -sSf -m 1 "$url" >/dev/null 2>&1; then
    echo "[OK] $url"
  else
    echo "[WARN] not ready: $url"
  fi
done

echo "[DONE] Startup sequence initiated. Use 'tail -f logs/*.log' to watch."
