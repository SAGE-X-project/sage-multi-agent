#!/bin/bash
# SAGE V4 Agent Registration (self-signed)
# - No registrar --key anymore.
# - Each agent signs/tx with its own EOA (from generated_agent_keys.json).
# - Optionally pre-fund agent EOAs via --funding-key.

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

# Defaults (adjust if your tree differs)
CONTRACT_ADDRESS="0x5FbDB2315678afecb367f032d93F642f64180aa3"
RPC_URL="http://localhost:8545"
DEMO_FILE="$PROJECT_ROOT/../sage-fe/demo-agents-metadata.json"
ABI_FILE="$PROJECT_ROOT/../sage/contracts/ethereum/artifacts/contracts/SageRegistryV4.sol/SageRegistryV4.json"
KEYS_FILE="$PROJECT_ROOT/generated_agent_keys.json"

# Optional per-agent funding
FUNDING_KEY=""
FUNDING_AMOUNT_WEI="10000000000000000"  # 0.01 ETH

usage() {
  cat <<EOF
Usage: $0 [options]

Options:
  --contract ADDRESS             SageRegistryV4 (proxy) address (default: $CONTRACT_ADDRESS)
  --rpc URL                      RPC endpoint (default: $RPC_URL)
  --demo FILE                    Demo metadata file (default: $DEMO_FILE)
  --abi FILE                     ABI artifact file (default: $ABI_FILE)
  --keys FILE                    Agent keys JSON (default: $KEYS_FILE)
  --funding-key HEX              (optional) funder private key WITHOUT 0x
  --funding-amount-wei AMT       (optional) amount per agent in wei (default: $FUNDING_AMOUNT_WEI)
  --help                         Show this help
EOF
}

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --contract)            CONTRACT_ADDRESS="$2"; shift 2 ;;
    --rpc)                 RPC_URL="$2"; shift 2 ;;
    --demo)                DEMO_FILE="$2"; shift 2 ;;
    --abi)                 ABI_FILE="$2"; shift 2 ;;
    --keys)                KEYS_FILE="$2"; shift 2 ;;
    --funding-key)         FUNDING_KEY="$2"; shift 2 ;;
    --funding-amount-wei)  FUNDING_AMOUNT_WEI="$2"; shift 2 ;;
    --help)                usage; exit 0 ;;
    *) echo -e "${RED}Unknown option: $1${NC}"; usage; exit 1 ;;
  esac
done

echo " Checking blockchain connection..."
if ! curl -s -X POST --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' "$RPC_URL" >/dev/null; then
  echo -e "${RED} Error: Cannot connect to blockchain at $RPC_URL${NC}"
  echo "   Start Hardhat node first: cd sage/contracts/ethereum && npx hardhat node"
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

[[ -f "$DEMO_FILE" ]] || { echo -e "${RED} Demo file not found: $DEMO_FILE${NC}"; exit 1; }
[[ -f "$ABI_FILE"  ]] || { echo -e "${RED} ABI file not found: $ABI_FILE${NC}"; exit 1; }
[[ -f "$KEYS_FILE" ]] || { echo -e "${RED} Keys file not found: $KEYS_FILE${NC}"; exit 1; }

echo ""
echo "======================================"
echo " Registration Configuration"
echo "======================================"
echo "Contract: $CONTRACT_ADDRESS"
echo "RPC URL : $RPC_URL"
echo "Demo    : $DEMO_FILE"
echo "ABI     : $ABI_FILE"
echo "Keys    : $KEYS_FILE"
if [[ -n "$FUNDING_KEY" ]]; then
  echo "Funding : enabled | per-agent: $FUNDING_AMOUNT_WEI wei"
else
  echo "Funding : disabled"
fi
echo "======================================"
echo ""

cd "$PROJECT_ROOT"

CMD=( go run tools/registration/register_agents.go
  -contract="$CONTRACT_ADDRESS"
  -rpc="$RPC_URL"
  -demo="$DEMO_FILE"
  -abi="$ABI_FILE"
  -keys="$KEYS_FILE"
)

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
