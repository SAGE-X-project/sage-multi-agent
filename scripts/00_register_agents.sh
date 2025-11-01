#!/usr/bin/env bash
# AgentCardRegistry Registration Orchestrator (aligned with AgentCardClient)
# - Signing only   : register_agents.go       (-tags reg_agent) → ECDSA key, commit→register→(activate)
# - KEM register   : register_kem_agents.go   (-tags reg_kem)   → ECDSA + X25519 in one shot
#
# Notes:
#   • ECDSA publicKey: 0x04 + X + Y (65B uncompressed) recommended. (Compressed allowed; restored in Go.)
#   • X25519 publicKey: exactly 32 bytes (64 hex chars; 0x prefix allowed).
#   • Sign in Go over chainId + registry(address(this)) + owner(Address)
#     with the EIP-191 (eth_sign) prefix, same as the contract.

set -euo pipefail

# ---------- Colors ----------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

echo "======================================"
echo " AgentCard Registration Tool"
echo "======================================"
echo ""

# ---------- Paths / Defaults ----------
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# ENV precedence: SAGE_REGISTRY_V4_ADDRESS → (fallback) SAGE_REGISTRY_ADDRESS
CONTRACT_ADDRESS="${SAGE_REGISTRY_V4_ADDRESS:-${SAGE_REGISTRY_ADDRESS:-0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512}}"
RPC_URL="${ETH_RPC_URL:-http://127.0.0.1:8545}"

# Signing keys JSON default
if [[ -f "$PROJECT_ROOT/generated_agent_keys.json" ]]; then
  SIGNING_KEYS_DEFAULT="$PROJECT_ROOT/generated_agent_keys.json"
elif [[ -f "$PROJECT_ROOT/keys/generated_agent_keys.json" ]]; then
  SIGNING_KEYS_DEFAULT="$PROJECT_ROOT/keys/generated_agent_keys.json"
else
  SIGNING_KEYS_DEFAULT="$PROJECT_ROOT/generated_agent_keys.json"
fi

# KEM keys JSON default
if [[ -f "$PROJECT_ROOT/keys/kem/generated_kem_keys.json" ]]; then
  KEM_KEYS_DEFAULT="$PROJECT_ROOT/keys/kem/generated_kem_keys.json"
else
  KEM_KEYS_DEFAULT="$PROJECT_ROOT/keys/kem/generated_kem_keys.json"
fi

SIGNING_KEYS="$SIGNING_KEYS_DEFAULT"
KEM_KEYS="$KEM_KEYS_DEFAULT"

# Merge output (top-level array; includes x25519Public/x25519Private always)
COMBINED_OUT="$PROJECT_ROOT/merged_agent_keys.json"
DO_MERGE=0                    # --merge 시 1
ADDR_SOURCE="$SIGNING_KEYS"   # funding 시 주소 추출에 사용할 JSON

# Funding
FUNDING_KEY=""
FUNDING_AMOUNT_WEI="100000000000000000"   # 0.01 ETH

# Agent filter (comma-separated)
AGENTS="${SAGE_AGENTS:-}"

# Execution toggles
DO_SIGNING=1      # 기본: ECDSA 등록
DO_KEM=0          # --kem 또는 --both 지정 시 활성화
TRY_ACTIVATE=0
WAIT_SECONDS=65   # commit→register 최소 60초 이상

# Optional: PEM→JSON builder path for KEM (legacy)
PEM_DIR=""
REQUIRE_PRIV=0

usage() {
  cat <<EOF
Usage: $0 [options]

Options:
  --contract ADDRESS         AgentCardRegistry (proxy) address (default/env)
  --rpc URL                  RPC endpoint (default/env)

  --signing-keys FILE        Signing keys JSON (secp256k1)
                             Default: $SIGNING_KEYS_DEFAULT
  --kem-keys FILE            KEM (X25519) keys JSON; supports top-level array or {"agents":[]}
                             Default: $KEM_KEYS_DEFAULT
  --merge                    Build merged JSON (SIGNING+KEM) → includes x25519 fields ("" if absent)
  --combined-out FILE        Output path for merged JSON (default: $COMBINED_OUT)

  --pem-dir DIR              (optional) Build KEM JSON from PEMs in DIR before registering
  --require-priv             (optional) Builder fails if *_x25519_priv.pem missing

  --agents "A,B,C"           (optional) limit to these agent names

  --funding-key HEX          (optional) funder private key (hex, 0x-prefixed ok)
  --funding-amount-wei AMT   (optional) wei per address (default: $FUNDING_AMOUNT_WEI)

  --kem                      Do KEM registration (ECDSA + X25519)
  --both                     Run signing first, then KEM

  --wait-seconds N           Seconds to wait between commit and register (default: $WAIT_SECONDS)
  --try-activate             Try to activate immediately if activation time has passed

  --help                     Show help
EOF
}

# ---------- Parse args ----------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --contract)            CONTRACT_ADDRESS="$2"; shift 2 ;;
    --rpc)                 RPC_URL="$2"; shift 2 ;;
    --signing-keys)        SIGNING_KEYS="$2"; shift 2 ;;
    --kem-keys)            KEM_KEYS="$2"; shift 2 ;;
    --merge)               DO_MERGE=1; shift ;;
    --combined-out)        COMBINED_OUT="$2"; shift 2 ;;
    --pem-dir)             PEM_DIR="$2"; shift 2 ;;
    --require-priv)        REQUIRE_PRIV=1; shift ;;
    --agents)              AGENTS="$2"; shift 2 ;;
    --funding-key)         FUNDING_KEY="$2"; shift 2 ;;
    --funding-amount-wei)  FUNDING_AMOUNT_WEI="$2"; shift 2 ;;
    --kem)                 DO_KEM=1; DO_SIGNING=0; shift ;;
    --both)                DO_KEM=1; DO_SIGNING=1; shift ;;
    --wait-seconds)        WAIT_SECONDS="$2"; shift 2 ;;
    --try-activate)        TRY_ACTIVATE=1; shift ;;
    --help|-h)             usage; exit 0 ;;
    *) echo -e "${RED}Unknown option: $1${NC}"; usage; exit 1 ;;
  esac
done

# ---------- Binaries ----------
need() { command -v "$1" >/dev/null 2>&1 || { echo -e "${RED}Error: '$1' not found${NC}"; exit 1; }; }
need go
need curl
has_jq=0; command -v jq >/dev/null 2>&1 && has_jq=1
has_cast=0; command -v cast >/dev/null 2>&1 && has_cast=1

# Export AGENTS for jq filters
[[ -n "${AGENTS:-}" ]] && export AGENTS

# ---------- JSON-RPC helpers ----------
wei_to_hex() { local dec="$1"; printf "0x%x" "${dec}"; }
jsonrpc() {
  local method="$1"; local params="$2"
  curl -s -X POST "$RPC_URL" -H 'Content-Type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}"
}

fund_one_addr_devnode() {
  local addr="$1"
  local hex_amt; hex_amt="$(wei_to_hex "$FUNDING_AMOUNT_WEI")"
  local res
  res="$(jsonrpc "hardhat_setBalance" "[\"$addr\",\"$hex_amt\"]")" || true
  if echo "$res" | grep -q "\"error\""; then
    res="$(jsonrpc "anvil_setBalance" "[\"$addr\",\"$hex_amt\"]")" || true
    if echo "$res" | grep -q "\"error\""; then
      return 1
    else
      echo "  - anvil_setBalance OK (addr=$addr, amount=$hex_amt)"; return 0
    fi
  else
    echo "  - hardhat_setBalance OK (addr=$addr, amount=$hex_amt)"; return 0
  fi
}

jq_addresses() {
  local file="$1"
  if [[ $has_jq -ne 1 ]]; then echo ""; return 1; fi
  jq -r '
    def roots: if type=="array" then . else .agents end;
    roots as $r
    | (env.AGENTS // "") as $flt
    | ($flt|tostring|split(",")|map(gsub("^\\s+|\\s+$";""))) as $names
    | if $flt=="" or ($names|length==0) then
        $r[]
      else
        $r[] | select( (.name|tostring) as $n | $names | index($n) )
      end
    | .address
  ' "$file"
}

fund_addresses_devnode() {
  [[ $has_jq -ne 1 ]] && { echo -e "${YELLOW}Warning:${NC} skipping auto-funding because jq is not installed"; return 1; }
  local addrs; addrs=$(jq_addresses "$ADDR_SOURCE") || return 1
  [[ -z "$addrs" ]] && { echo -e "${YELLOW}Warning:${NC} no addresses to fund ($ADDR_SOURCE)"; return 1; }
  local ok_any=0
  while IFS= read -r addr; do
    [[ -z "$addr" ]] && continue
    if fund_one_addr_devnode "$addr"; then ok_any=1; else echo "  - setBalance failed (addr=$addr)"; fi
  done <<< "$addrs"
  [[ $ok_any -eq 1 ]] && return 0 || return 1
}

fund_addresses_cast() {
  [[ $has_cast -ne 1 ]] && return 1
  [[ -n "$FUNDING_KEY" ]] || return 1
  [[ $has_jq -ne 1 ]] && { echo -e "${YELLOW}Warning:${NC} skipping cast funding because jq is not installed"; return 1; }
  local addrs; addrs=$(jq_addresses "$ADDR_SOURCE") || return 1
  [[ -z "$addrs" ]] && { echo -e "${YELLOW}Warning:${NC} no addresses to fund ($ADDR_SOURCE)"; return 1; }
  local ok_any=0
  while IFS= read -r addr; do
    [[ -z "$addr" ]] && continue
    local bal_hex; bal_hex=$(jsonrpc "eth_getBalance" "[\"$addr\",\"latest\"]" | grep -o '"result":"[^"]*"' | cut -d'"' -f4 || echo "0x0")
    if [[ "$bal_hex" == "0x0" || "$bal_hex" == "0x" ]]; then
      echo "  - cast send: $addr <= ${FUNDING_AMOUNT_WEI} wei"
      if cast send --rpc-url "$RPC_URL" --private-key "$FUNDING_KEY" "$addr" --value "$FUNDING_AMOUNT_WEI" >/dev/null 2>&1; then ok_any=1; else echo "    * cast transfer failed (addr=$addr)"; fi
    else
      echo "  - balance exists: $addr ($bal_hex) → skip transfer"; ok_any=1
    fi
  done <<< "$addrs"
  [[ $ok_any -eq 1 ]] && return 0 || return 1
}

# ---------- Validators ----------
validate_signing_json() {
  local f="$1"; [[ -f "$f" ]] || { echo -e "${RED}Signing keys not found: $f${NC}"; exit 1; }
  if [[ $has_jq -eq 1 ]]; then
    jq -e 'type=="array" and length>=1 and (.[0]|has("name") and has("did") and has("publicKey") and has("privateKey") and has("address"))' "$f" >/dev/null 2>&1 \
      && echo -e "${GREEN} Signing keys JSON OK${NC} (records: $(jq 'length' "$f"))" \
      || { echo -e "${RED}Error:${NC} '$f' is not a valid signing-key summary array."; exit 1; }
    # ⛔️ Removed problematic startswith check (jq crashed due to some row type variations/missing fields)
  else
    grep -q '"publicKey"' "$f" || { echo -e "${RED}Error:${NC} missing 'publicKey' in '$f'"; exit 1; }
    grep -q '"privateKey"' "$f" || { echo -e "${RED}Error:${NC} missing 'privateKey' in '$f'"; exit 1; }
    echo -e "${YELLOW}Warning:${NC} jq not installed; minimal validation for signing keys."
  fi
}

validate_kem_json() {
  local f="$1"; [[ -f "$f" ]] || { echo -e "${RED}KEM keys file not found: $f${NC}"; exit 1; }
  if [[ $has_jq -eq 1 ]]; then
    jq -e '
      (
        (type=="object") and (.agents|type=="array")
      ) or (
        (type=="array")
      )
    ' "$f" >/dev/null 2>&1 \
      || { echo -e "${RED}Error:${NC} '$f' must be either {\"agents\":[...]} or a top-level array."; exit 1; }
    echo -e "${GREEN} KEM keys JSON OK${NC}"
  else
    grep -Eq '"agents"|^\s*\[' "$f" || { echo -e "${RED}Error:${NC} not a recognized KEM JSON shape."; exit 1; }
    grep -q 'x25519Public' "$f" || { echo -e "${RED}Error:${NC} missing x25519Public."; exit 1; }
    echo -e "${YELLOW}Warning:${NC} jq not installed; minimal validation for KEM keys."
  fi
}

# ---------- Connectivity ----------
echo " Checking blockchain connection..."
if ! curl -s -X POST --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' "$RPC_URL" >/dev/null; then
  echo -e "${RED} Error: Cannot connect to blockchain at $RPC_URL${NC}"; exit 1
fi
echo -e "${GREEN} Blockchain connected${NC}"

echo " Checking registry contract..."
CODE=$(curl -s -X POST --data "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getCode\",\"params\":[\"$CONTRACT_ADDRESS\", \"latest\"],\"id\":1}" "$RPC_URL" | grep -o '"result":"[^"]*"' | cut -d'"' -f4)
if [[ "$CODE" == "0x" || -z "$CODE" ]]; then
  echo -e "${RED} Error: No contract at $CONTRACT_ADDRESS${NC}"; exit 1
fi
echo -e "${GREEN} Contract found${NC}"

# ---------- Validate JSONs ----------
if [[ $DO_SIGNING -eq 1 ]]; then
  echo " Validating signing keys..."; validate_signing_json "$SIGNING_KEYS"
fi

if [[ $DO_KEM -eq 1 ]]; then
  echo " Validating KEM inputs..."
  if [[ $DO_MERGE -eq 1 ]]; then
    # Always validate the signing file
    validate_signing_json "$SIGNING_KEYS"
    # Validate KEM file if present; if missing, merged JSON will create empty fields
    if [[ -f "$KEM_KEYS" ]]; then
      validate_kem_json "$KEM_KEYS"
    else
      echo -e "${YELLOW}Note:${NC} KEM keys file missing; merged JSON은 x25519 필드를 빈 문자열로 생성."
    fi
  else
    if [[ -n "$PEM_DIR" ]]; then
      echo " Will build KEM JSON from PEM before registering."
    else
      validate_kem_json "$KEM_KEYS"
    fi
    validate_signing_json "$SIGNING_KEYS"
  fi
fi

# ---------- Summary ----------
echo ""
echo "======================================"
echo " Registration Configuration"
echo "======================================"
echo "Contract : $CONTRACT_ADDRESS"
echo "RPC URL  : $RPC_URL"
if [[ $DO_SIGNING -eq 1 ]]; then
  echo "Signing  : $SIGNING_KEYS (ECDSA only)"
else
  echo "Signing  : (disabled)"
fi
if [[ $DO_KEM -eq 1 ]]; then
  if [[ $DO_MERGE -eq 1 ]]; then
    echo "KEM Reg  : will MERGE signing+kem → $COMBINED_OUT (top-level array)"
  elif [[ -n "$PEM_DIR" ]]; then
    echo "KEM Reg  : will build JSON from PEM ($PEM_DIR) → $KEM_KEYS"
  else
    echo "KEM Reg  : $KEM_KEYS (ECDSA+X25519)"
  fi
else
  echo "KEM Reg  : (disabled)"
fi
[[ -n "${AGENTS:-}" ]] && echo "Agents   : $AGENTS" || echo "Agents   : ALL (no filter)"
if [[ -n "$FUNDING_KEY" ]]; then
  echo "Funding  : enabled | per-address: $FUNDING_AMOUNT_WEI wei"
else
  echo "Funding  : (try dev RPC setBalance; cast fallback if --funding-key given)"
fi
if [[ $DO_SIGNING -eq 1 && $DO_KEM -eq 1 ]]; then
  echo "Mode     : both (signing first, then KEM)"
elif [[ $DO_KEM -eq 1 ]]; then
  echo "Mode     : KEM only"
else
  echo "Mode     : signing only"
fi
echo "Wait     : ${WAIT_SECONDS}s between commit→register | TryActivate=${TRY_ACTIVATE}"
echo "======================================"
echo ""

cd "$PROJECT_ROOT"

# ---------- Signing-only registration (ECDSA) ----------
if [[ $DO_SIGNING -eq 1 ]]; then
  echo -e "${YELLOW}>>> Registering SIGNING keys (ECDSA only)...${NC}"
  CMD=( go run -tags=reg_agent tools/registration/register_agents.go
    -contract="$CONTRACT_ADDRESS"
    -rpc="$RPC_URL"
    -keys="$SIGNING_KEYS"
    -wait-seconds="$WAIT_SECONDS"
  )
  [[ -n "${AGENTS:-}" ]] && CMD+=( -agents="$AGENTS" )
  [[ -n "$FUNDING_KEY" ]] && CMD+=( -funding-key="$FUNDING_KEY" -funding-amount-wei="$FUNDING_AMOUNT_WEI" )
  [[ $TRY_ACTIVATE -eq 1 ]] && CMD+=( -try-activate )
  "${CMD[@]}"
  echo -e "${GREEN}Signing-only registration done.${NC}\n"
fi

# ---------- KEM Register flow (ECDSA+X25519) ----------
if [[ $DO_KEM -eq 1 ]]; then
  echo -e "${YELLOW}>>> Registering agents with KEM (ECDSA+X25519)...${NC}"

  # (0) Build MERGED JSON (use Go builder; always include x25519 fields)
  if [[ $DO_MERGE -eq 1 ]]; then
    echo -e "${YELLOW}>>> Building MERGED JSON (ECDSA+X25519, did=address if missing) -> ${COMBINED_OUT}${NC}"
    # Pass -kem flag if KEM file exists; otherwise create empty string fields
    go run tools/registration/build_combined_from_signing.go \
      -signing "$SIGNING_KEYS" \
      ${KEM_KEYS:+-kem "$KEM_KEYS"} \
      -out "$COMBINED_OUT" \
      ${AGENTS:+-agents "$AGENTS"}
    echo -e "${GREEN}Merged keys at ${COMBINED_OUT}${NC}"
    KEM_KEYS="$COMBINED_OUT"
    ADDR_SOURCE="$COMBINED_OUT"
  else
    ADDR_SOURCE="$SIGNING_KEYS"
  fi

  # (1) Prefund per-agent senders
  echo -e "${YELLOW}>>> Prefunding agent senders (if possible)...${NC}"
  if fund_addresses_devnode; then
    echo -e "${GREEN}Dev-node setBalance funding for agents done.${NC}"
  else
    if [[ -n "$FUNDING_KEY" ]] && fund_addresses_cast; then
      echo -e "${GREEN}cast funding for agents done.${NC}"
    else
      echo -e "${YELLOW}Warning:${NC} automatic funding failed → manually check sender (agent) balances"
    fi
  fi
  echo ""

  # (2) (optional) PEM → JSON build path (when not merging)
  if [[ -n "$PEM_DIR" && $DO_MERGE -eq 0 ]]; then
    if [[ -f tools/registration/build_kem_json_from_pem.go ]]; then
      echo -e "${YELLOW}>>> Building KEM JSON from PEM (${PEM_DIR}) -> ${KEM_KEYS}${NC}"
      CMD_BUILD=( go run tools/registration/build_kem_json_from_pem.go
        -pem-dir "$PEM_DIR"
        -out "$KEM_KEYS"
        -signing-keys "$SIGNING_KEYS"
      )
      [[ -n "${AGENTS:-}" ]] && CMD_BUILD+=( -agents="$AGENTS" )
      [[ $REQUIRE_PRIV -eq 1 ]] && CMD_BUILD+=( -require-priv )
      "${CMD_BUILD[@]}"
      echo -e "${GREEN}KEM JSON built at ${KEM_KEYS}.${NC}\n"
    else
      echo -e "${RED}Error:${NC} tools/registration/build_kem_json_from_pem.go not found. Provide --kem-keys or add the builder."
      exit 1
    fi
  fi

  # (3) Run reg_kem
  if [[ ! -f tools/registration/register_kem_agents.go ]]; then
    echo -e "${RED}Error:${NC} tools/registration/register_kem_agents.go not found."; exit 1
  fi

  CMD2=( go run -tags=reg_kem tools/registration/register_kem_agents.go
    -contract="$CONTRACT_ADDRESS"
    -rpc="$RPC_URL"
    -kem-keys="$KEM_KEYS"
    -signing-keys="$SIGNING_KEYS"
    -wait-seconds="$WAIT_SECONDS"
  )
  [[ -n "${AGENTS:-}" ]] && CMD2+=( -agents="$AGENTS" )
  [[ $TRY_ACTIVATE -eq 1 ]] && CMD2+=( -try-activate )
  "${CMD2[@]}"

  echo -e "${GREEN}KEM register process done.${NC}\n"
fi

echo "======================================"
echo -e "${GREEN} Registration process complete!${NC}"
echo "======================================"
