#!/usr/bin/env bash
# SAGE V4 Agent Registration (self-signed by agents)
# - Signing only: register_agents.go (//go:build reg_agent)            ← ECDSA만 등록
# - KEM Register: register_kem_agents.go (//go:build reg_kem)          ← ECDSA+X25519을 한 번에 Register 또는 addKey
# - 기본 동작: --kem/--kem-only 시 signing JSON 기반으로 merged JSON을 빌드(ecdsa+x25519, did=address) 후 등록
#
# 예:
#   # 1) 서명키만 등록
#   sh ./scripts/00_register_agents.sh \
#     --signing-keys ./generated_agent_keys.json \
#     --agents "ordering,planing,payment,external"
#
#   # 2) KEM만 등록(미등록이면 Register, 이미 있으면 addKey)
#   sh ./scripts/00_register_agents.sh \
#     --kem \
#     --signing-keys ./generated_agent_keys.json \
#     --agents "ordering,planing,payment,external"
#
#   # 3) 두 개 다(서명 → KEM)
#   sh ./scripts/00_register_agents.sh \
#     --both \
#     --signing-keys ./generated_agent_keys.json \
#     --agents "ordering,planing,payment,external"
#
#   # 참고: KEM JSON을 이미 따로 갖고 있고 그대로 쓰고 싶다면 --no-merge와 --kem-keys 지정
#   sh ./scripts/00_register_agents.sh \
#     --kem --no-merge \
#     --kem-keys ./keys/kem/generated_kem_keys.json \
#     --signing-keys ./generated_agent_keys.json \
#     --agents "ordering,planing,payment,external"

set -euo pipefail

# ---------- Colors ----------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

echo "======================================"
echo " SAGE V4 Agent Registration Tool"
echo "======================================"
echo ""

# ---------- Paths / Defaults ----------
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

CONTRACT_ADDRESS="${SAGE_REGISTRY_V4_ADDRESS:-0x5FbDB2315678afecb367f032d93F642f64180aa3}"
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

# merged(통합) 키 파일 경로 & 플래그
COMBINED_OUT="$PROJECT_ROOT/merged_agent_keys.json"
BUILD_MERGED=1             # 기본: KEM 실행 시 signing 기반으로 merged JSON 자동 생성
ADDR_SOURCE="$SIGNING_KEYS" # 펀딩/주소 추출 시 사용할 JSON (기본은 signing)

# Funding
FUNDING_KEY=""
FUNDING_AMOUNT_WEI="10000000000000000"   # 0.01 ETH

# Agent filter (comma-separated)
AGENTS="${SAGE_AGENTS:-}"

# Execution toggles
DO_SIGNING=1      # 기본: ECDSA만 등록
DO_KEM=0          # --kem / --kem-only / --both 로 켜기

# Optional: build KEM JSON from PEM before registering (레거시 경로)
PEM_DIR=""
REQUIRE_PRIV=0   # pass to builder if supported

usage() {
  cat <<EOF
Usage: $0 [options]

Options:
  --contract ADDRESS         SageRegistryV4 (proxy) address (default/env)
  --rpc URL                  RPC endpoint (default/env)

  --signing-keys FILE        Signing keys JSON (secp256k1)
                             Default: $SIGNING_KEYS_DEFAULT
  --kem-keys FILE            KEM (HPKE X25519) keys JSON (object.agents[] or top-level array)
                             Default: $KEM_KEYS_DEFAULT
  --pem-dir DIR              (optional) Build KEM JSON from PEMs in DIR before registering
  --require-priv             (optional) Builder fails if *_x25519_priv.pem is missing

  --combined-out FILE        (optional) merged JSON output path (default: $COMBINED_OUT)
  --no-merge                 (optional) don't build merged JSON; use --kem-keys as-is

  --agents "A,B,C"           (optional) limit to these agent names

  --funding-key HEX          (optional) funder private key (hex, 0x-prefixed ok)
  --funding-amount-wei AMT   (optional) wei per address (default: $FUNDING_AMOUNT_WEI)

  --kem                      Register ECDSA + X25519 (single Register/addKey)
  --kem-only                 Only run KEM Register (skip plain signing)
  --both                     Run signing-only first, then KEM

  --help                     Show help

Notes:
- KEM Register는 tx sender가 에이전트 EOA여야 하며, Register 메시지의 sender와 일치해야 합니다.
- 기본 동작은 signing-keys에 포함된 각 에이전트의 privateKey(EOA)로 트랜잭션을 보냅니다.
- 펀딩(로컬 dev 편의):
    1) dev node면 hardhat_setBalance / anvil_setBalance 로 바로 잔액 세팅
    2) (1) 실패 + --funding-key 제공 + cast 설치 시, funder가 실제 송금
EOF
}

# ---------- Parse args ----------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --contract)            CONTRACT_ADDRESS="$2"; shift 2 ;;
    --rpc)                 RPC_URL="$2"; shift 2 ;;
    --signing-keys)        SIGNING_KEYS="$2"; shift 2 ;;
    --kem-keys)            KEM_KEYS="$2"; shift 2 ;;
    --pem-dir)             PEM_DIR="$2"; shift 2 ;;
    --require-priv)        REQUIRE_PRIV=1; shift ;;
    --agents)              AGENTS="$2"; shift 2 ;;
    --funding-key)         FUNDING_KEY="$2"; shift 2 ;;
    --funding-amount-wei)  FUNDING_AMOUNT_WEI="$2"; shift 2 ;;
    --kem)                 DO_KEM=1; DO_SIGNING=0; shift ;;
    --kem-only)            DO_KEM=1; DO_SIGNING=0; shift ;;
    --both)                DO_KEM=1; DO_SIGNING=1; shift ;;
    --combined-out)        COMBINED_OUT="$2"; shift 2 ;;
    --no-merge)            BUILD_MERGED=0; shift ;;
    --help|-h)             usage; exit 0 ;;
    *) echo -e "${RED}Unknown option: $1${NC}"; usage; exit 1 ;;
  esac
done

# jq/curl/go/cast 체크
need() { command -v "$1" >/dev/null 2>&1 || { echo -e "${RED}Error: '$1' not found${NC}"; exit 1; }; }
need go
need curl
has_jq=0; command -v jq >/dev/null 2>&1 && has_jq=1
has_cast=0; command -v cast >/dev/null 2>&1 && has_cast=1

# AGENTS를 jq env에서 쓰므로 export
[[ -n "${AGENTS:-}" ]] && export AGENTS

# ---------- Helpers ----------
wei_to_hex() {
  local dec="$1"
  printf "0x%x" "${dec}"
}

jsonrpc() {
  local method="$1"
  local params="$2"
  curl -s -X POST "$RPC_URL" \
    -H 'Content-Type: application/json' \
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
      echo "  - anvil_setBalance OK (addr=$addr, amount=$hex_amt)"
      return 0
    fi
  else
    echo "  - hardhat_setBalance OK (addr=$addr, amount=$hex_amt)"
    return 0
  fi
}

# ADDR_SOURCE(JSON)가 top-level array 또는 {"agents":[...]} 모두 지원
jq_addresses() {
  local file="$1"
  if [[ $has_jq -ne 1 ]]; then
    echo ""
    return 1
  fi
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
  [[ $has_jq -ne 1 ]] && { echo -e "${YELLOW}Warning:${NC} jq 미설치로 자동 펀딩 스킵"; return 1; }
  local addrs
  addrs=$(jq_addresses "$ADDR_SOURCE") || return 1
  [[ -z "$addrs" ]] && { echo -e "${YELLOW}Warning:${NC} 펀딩할 address 없음 ($ADDR_SOURCE)"; return 1; }
  local ok_any=0
  while IFS= read -r addr; do
    [[ -z "$addr" ]] && continue
    if fund_one_addr_devnode "$addr"; then ok_any=1; else echo "  - setBalance 실패 (addr=$addr)"; fi
  done <<< "$addrs"
  [[ $ok_any -eq 1 ]] && return 0 || return 1
}

fund_addresses_cast() {
  [[ $has_cast -ne 1 ]] && return 1
  [[ -z "$FUNDING_KEY" ]] && return 1
  [[ $has_jq -ne 1 ]] && { echo -e "${YELLOW}Warning:${NC} jq 미설치로 cast 펀딩 스킵"; return 1; }
  local addrs
  addrs=$(jq_addresses "$ADDR_SOURCE") || return 1
  [[ -z "$addrs" ]] && { echo -e "${YELLOW}Warning:${NC} 펀딩할 address 없음 ($ADDR_SOURCE)"; return 1; }
  local ok_any=0
  while IFS= read -r addr; do
    [[ -z "$addr" ]] && continue
    local bal_hex
    bal_hex=$(jsonrpc "eth_getBalance" "[\"$addr\",\"latest\"]" | grep -o '"result":"[^"]*"' | cut -d'"' -f4 || echo "0x0")
    if [[ "$bal_hex" == "0x0" || "$bal_hex" == "0x" ]]; then
      echo "  - cast send: $addr <= ${FUNDING_AMOUNT_WEI} wei"
      if cast send --rpc-url "$RPC_URL" --private-key "$FUNDING_KEY" "$addr" --value "$FUNDING_AMOUNT_WEI" >/dev/null 2>&1; then
        ok_any=1
      else
        echo "    * cast 송금 실패 (addr=$addr)"
      fi
    else
      echo "  - 잔액 존재: $addr ($bal_hex) → 송금 스킵"
      ok_any=1
    fi
  done <<< "$addrs"
  [[ $ok_any -eq 1 ]] && return 0 || return 1
}

# ---------- Validators ----------
validate_signing_json() {
  local f="$1"
  [[ -f "$f" ]] || { echo -e "${RED}Signing keys file not found: $f${NC}"; exit 1; }
  if [[ $has_jq -eq 1 ]]; then
    if ! jq -e 'type=="array" and length>=1 and
                (.[0]|has("name") and has("did") and has("publicKey") and has("privateKey") and has("address"))' "$f" >/dev/null 2>&1; then
      echo -e "${RED}Error:${NC} '$f' is not a valid signing-key summary array."
      exit 1
    fi
    echo -e "${GREEN} Signing keys JSON OK${NC} (records: $(jq 'length' "$f"))"
  else
    grep -q '"publicKey"' "$f" || { echo -e "${RED}Error:${NC} missing 'publicKey' in '$f'"; exit 1; }
    grep -q '"privateKey"' "$f" || { echo -e "${RED}Error:${NC} missing 'privateKey' in '$f'"; exit 1; }
    echo -e "${YELLOW}Warning:${NC} jq not installed; minimal validation for signing keys."
  fi
}

validate_kem_json() {
  local f="$1"
  [[ -f "$f" ]] || { echo -e "${RED}KEM keys file not found: $f${NC}"; exit 1; }
  if [[ $has_jq -eq 1 ]]; then
    # object.agents[] OR top-level array — 둘 다 허용
    if ! jq -e '
      (
        (type=="object") and (.agents|type=="array") and
        ( (.agents|length)==0 or (.agents[0]|has("name") and has("x25519Public")) )
      )
      or
      (
        (type=="array") and
        ( (length)==0 or (.[0]|has("name") and has("x25519Public")) )
      )
    ' "$f" >/dev/null 2>&1; then
      echo -e "${RED}Error:${NC} '$f' must be either {\"agents\":[...]} or a top-level array of rows with name/x25519Public."
      exit 1
    fi
    local count
    count=$(jq -r 'if type=="object" then (.agents|length) else (length) end' "$f")
    echo -e "${GREEN} KEM keys JSON OK${NC} (agents: ${count})"
  else
    grep -Eq '"agents"|^\s*\[' "$f" || { echo -e "${RED}Error:${NC} not a recognized KEM JSON shape."; exit 1; }
    grep -q 'x25519Public' "$f" || { echo -e "${RED}Error:${NC} missing x25519Public."; exit 1; }
    echo -e "${YELLOW}Warning:${NC} jq not installed; minimal validation for KEM keys."
  fi
}

# ---------- Connectivity ----------
echo " Checking blockchain connection..."
if ! curl -s -X POST --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' "$RPC_URL" >/dev/null; then
  echo -e "${RED} Error: Cannot connect to blockchain at $RPC_URL${NC}"
  exit 1
fi
echo -e "${GREEN} Blockchain connected${NC}"

echo " Checking registry contract..."
CODE=$(curl -s -X POST --data "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getCode\",\"params\":[\"$CONTRACT_ADDRESS\", \"latest\"],\"id\":1}" "$RPC_URL" | grep -o '"result":"[^"]*"' | cut -d'"' -f4)
if [[ "$CODE" == "0x" || -z "$CODE" ]]; then
  echo -e "${RED} Error: No contract at $CONTRACT_ADDRESS${NC}"
  exit 1
fi
echo -e "${GREEN} Contract found${NC}"

# ---------- Validate JSONs ----------
if [[ $DO_SIGNING -eq 1 ]]; then
  echo " Validating signing keys..."
  validate_signing_json "$SIGNING_KEYS"
fi

if [[ $DO_KEM -eq 1 ]]; then
  if [[ $BUILD_MERGED -eq 1 ]]; then
    echo " Will build MERGED JSON from signing (ECDSA+X25519, did=address): $COMBINED_OUT"
    echo " Validating signing keys for senders..."
    validate_signing_json "$SIGNING_KEYS"
  else
    if [[ -n "$PEM_DIR" ]]; then
      echo " Will build KEM JSON from PEM before registering."
    else
      echo " Validating KEM keys..."
      validate_kem_json "$KEM_KEYS"
    fi
    echo " Validating signing keys for senders..."
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
  if [[ $BUILD_MERGED -eq 1 ]]; then
    echo "KEM Reg  : merged from signing -> $COMBINED_OUT (Register/AddKey: ECDSA+X25519, did=address)"
  elif [[ -n "$PEM_DIR" ]]; then
    echo "KEM Reg  : will build JSON from PEM ($PEM_DIR) -> $KEM_KEYS"
  else
    echo "KEM Reg  : $KEM_KEYS (Register/AddKey: ECDSA+X25519)"
  fi
else
  echo "KEM Reg  : (disabled)"
fi
if [[ -n "${AGENTS:-}" ]]; then
  echo "Agents   : $AGENTS"
else
  echo "Agents   : ALL (no filter)"
fi
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
echo "======================================"
echo ""

cd "$PROJECT_ROOT"

# ---------- Run signing-only registration (ECDSA) ----------
if [[ $DO_SIGNING -eq 1 ]]; then
  echo -e "${YELLOW}>>> Registering SIGNING keys (ECDSA only)...${NC}"
  CMD=( go run -tags=reg_agent tools/registration/register_agents.go
    -contract="$CONTRACT_ADDRESS"
    -rpc="$RPC_URL"
    -keys="$SIGNING_KEYS"
  )
  [[ -n "${AGENTS:-}" ]] && CMD+=( -agents="$AGENTS" )
  if [[ -n "$FUNDING_KEY" ]]; then
    CMD+=( -funding-key="$FUNDING_KEY" -funding-amount-wei="$FUNDING_AMOUNT_WEI" )
  fi
  "${CMD[@]}"
  echo -e "${GREEN}Signing-only registration done.${NC}"
  echo ""
fi

# ---------- KEM Register flow (ECDSA+X25519 in one Register or addKey) ----------
if [[ $DO_KEM -eq 1 ]]; then
  echo -e "${YELLOW}>>> Registering agents with KEM (ECDSA+X25519)...${NC}"

  # (0) merged JSON 만들기(기본) 또는 기존 KEM JSON 사용
  if [[ $BUILD_MERGED -eq 1 ]]; then
    echo -e "${YELLOW}>>> Building MERGED JSON (ECDSA+X25519, did=address) -> ${COMBINED_OUT}${NC}"
    go run tools/registration/build_combined_from_signing.go \
      -signing "$SIGNING_KEYS" \
      -out "$COMBINED_OUT" \
      ${AGENTS:+-agents "$AGENTS"}
    echo -e "${GREEN}Merged keys at ${COMBINED_OUT}${NC}"
    # 이후 주소/펀딩/등록 모두 merged 파일 기준으로
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
      echo -e "${YELLOW}Warning:${NC} 자동 펀딩 실패 → sender(에이전트) 잔액 직접 확인 필요"
    fi
  fi
  echo ""

  # (2) (옵션) PEM → JSON 빌드 경로 (no-merge일 때만 의미)
  if [[ -n "$PEM_DIR" && $BUILD_MERGED -eq 0 ]]; then
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
      echo -e "${GREEN}KEM JSON built at ${KEM_KEYS}.${NC}"
      echo ""
    else
      echo -e "${RED}Error:${NC} tools/registration/build_kem_json_from_pem.go not found."
      echo "       Provide --kem-keys directly or add the builder."
      exit 1
    fi
  fi

  # (3) Register/AddKey with KEM (reg_kem)
  if [[ ! -f tools/registration/register_kem_agents.go ]]; then
    echo -e "${RED}Error:${NC} tools/registration/register_kem_agents.go not found."
    exit 1
  fi

  # merged 모드에서는 두 입력 모두 동일 파일을 넘겨 DID/주소/키 일치 보장
  if [[ $BUILD_MERGED -eq 1 ]]; then
    CMD2=( go run -tags=reg_kem tools/registration/register_kem_agents.go
      -contract="$CONTRACT_ADDRESS"
      -rpc="$RPC_URL"
      -kem-keys="$KEM_KEYS"
      -signing-keys="$KEM_KEYS"
    )
  else
    CMD2=( go run -tags=reg_kem tools/registration/register_kem_agents.go
      -contract="$CONTRACT_ADDRESS"
      -rpc="$RPC_URL"
      -kem-keys="$KEM_KEYS"
      -signing-keys="$SIGNING_KEYS"
    )
  fi
  [[ -n "${AGENTS:-}" ]] && CMD2+=( -agents="$AGENTS" )
  "${CMD2[@]}"

  echo -e "${GREEN}KEM register process done.${NC}"
  echo ""
fi

echo "======================================"
echo -e "${GREEN} Registration process complete!${NC}"
echo "======================================"
