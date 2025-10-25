#!/bin/bash
# SAGE V4 Agent Registration (self-signed by agents)
# - Fund agent EOAs with --funding-key
# - Each agent signs and sends its own registration tx with its own private key
# - Agents are selected from generated_agent_keys.json (optionally filtered by --agents)

set -euo pipefail

echo "======================================"
echo " SAGE V4 Agent Registration Tool"
echo "======================================"
echo ""

# Colors
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

# Paths
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# Defaults (env > flag > default)
CONTRACT_ADDRESS="${SAGE_REGISTRY_V4_ADDRESS:-0x5FbDB2315678afecb367f032d93F642f64180aa3}"
RPC_URL="${ETH_RPC_URL:-http://127.0.0.1:8545}"
KEYS_FILE="$PROJECT_ROOT/generated_agent_keys.json"

# Funding options
FUNDING_KEY=""                                 # hex (with or without 0x)
FUNDING_AMOUNT_WEI="10000000000000000"         # 0.01 ETH

# Agent filter (comma-separated)
AGENTS="${SAGE_AGENTS:-}"

usage() {
  cat <<EOF
Usage: $0 [options]

Options:
  --contract ADDRESS         SageRegistryV4 (proxy) address (default/env: \$SAGE_REGISTRY_V4_ADDRESS or $CONTRACT_ADDRESS)
  --rpc URL                  RPC endpoint (default/env: \$ETH_RPC_URL or $RPC_URL)
  --keys FILE                Agent keys JSON (default: $KEYS_FILE)
  --agents "A,B,C"           (optional) comma-separated agent names to register (overrides SAGE_AGENTS)
  --funding-key HEX          (optional) funder private key (hex, no spaces). If omitted, no funding happens.
  --funding-amount-wei AMT   (optional) amount per agent in wei (default: $FUNDING_AMOUNT_WEI)
  --help                     Show this help
EOF
}

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --contract)            CONTRACT_ADDRESS="$2"; shift 2 ;;
    --rpc)                 RPC_URL="$2"; shift 2 ;;
    --keys)                KEYS_FILE="$2"; shift 2 ;;
    --agents)              AGENTS="$2"; shift 2 ;;
    --funding-key)         FUNDING_KEY="$2"; shift 2 ;;
    --funding-amount-wei)  FUNDING_AMOUNT_WEI="$2"; shift 2 ;;
    --help)                usage; exit 0 ;;
    *) echo -e "${RED}Unknown option: $1${NC}"; usage; exit 1 ;;
  esac
done

echo " Checking blockchain connection..."
if ! curl -s -X POST --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' "$RPC_URL" >/dev/null; then
  echo -e "${RED} Error: Cannot connect to blockchain at $RPC_URL${NC}"
  echo "   Start local node first (e.g. Hardhat or Anvil)"
  exit 1
fi
echo -e "${GREEN} Blockchain connected${NC}"

echo " Checking registry contract..."
CODE=$(curl -s -X POST --data "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getCode\",\"params\":[\"$CONTRACT_ADDRESS\", \"latest\"],\"id\":1}" "$RPC_URL" | grep -o '"result":"[^"]*"' | cut -d'"' -f4)
if [[ "$CODE" == "0x" || -z "$CODE" ]]; then
  echo -e "${RED} Error: No contract at $CONTRACT_ADDRESS${NC}"
  echo "   Deploy the registry first (SageRegistryV4 proxy)"
  exit 1
fi
echo -e "${GREEN} Contract found${NC}"

[[ -f "$KEYS_FILE" ]] || { echo -e "${RED} Keys file not found: $KEYS_FILE${NC}"; exit 1; }

echo ""
echo "======================================"
echo " Registration Configuration"
echo "======================================"
echo "Contract : $CONTRACT_ADDRESS"
echo "RPC URL  : $RPC_URL"
echo "Keys     : $KEYS_FILE"
if [[ -n "$AGENTS" ]]; then
  echo "Agents   : $AGENTS"
else
  if [[ -n "${SAGE_AGENTS:-}" ]]; then
    echo "Agents   : (from SAGE_AGENTS) $SAGE_AGENTS"
  else
    echo "Agents   : ALL (no filter)"
  fi
fi
if [[ -n "$FUNDING_KEY" ]]; then
  echo "Funding  : enabled | per-agent: $FUNDING_AMOUNT_WEI wei"
else
  echo "Funding  : disabled"
fi
echo "Mode     : self-signed by agents (agents NEED gas)"
echo "======================================"
echo ""

cd "$PROJECT_ROOT"

CMD=( go run tools/registration/register_agents.go
  -contract="$CONTRACT_ADDRESS"
  -rpc="$RPC_URL"
  -keys="$KEYS_FILE"
)

# Pass agents filter to Go if provided
if [[ -n "$AGENTS" ]]; then
  CMD+=( -agents="$AGENTS" )
fi

# Optional funding parameters
if [[ -n "$FUNDING_KEY" ]]; then
  CMD+=( -funding-key="$FUNDING_KEY" -funding-amount-wei="$FUNDING_AMOUNT_WEI" )
fi

echo " Starting self-signed registration..."
echo " (This will print each tx hash; agents must have gas or use --funding-key)"
echo ""

"${CMD[@]}"

echo ""
echo "======================================"
echo -e "${GREEN} Registration process complete!${NC}"
echo "======================================"
