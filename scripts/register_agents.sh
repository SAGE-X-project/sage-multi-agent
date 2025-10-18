#!/bin/bash

# Agent Registration Script for Local Blockchain
# This script registers all demo agents on the local Hardhat blockchain

set -e  # Exit on error

echo "======================================"
echo " SAGE Agent Registration Tool"
echo "======================================"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# Default values
CONTRACT_ADDRESS="0x5FbDB2315678afecb367f032d93F642f64180aa3"
RPC_URL="http://localhost:8545"
PRIVATE_KEY="ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
DEMO_FILE="$PROJECT_ROOT/../sage-fe/demo-agents-metadata.json"
ABI_FILE="$PROJECT_ROOT/../sage/contracts/ethereum/artifacts/contracts/SageRegistryV2.sol/SageRegistryV2.json"

# Added: default flags mapped to register_agents.go (safe for SageRegistryV2)
MODE="local"                         # 'local' (default) or 'self-signed'
SIGMODE="bytes32"                    # 'bytes', 'bytes32', or 'raw'
SENDER_IN_DIGEST="auto"              # 'auto', 'registrar', 'agent'
DID_IN_DIGEST="none"                 # 'none', 'string', 'hash'
KEYHASH_MODE="raw"                   # 'raw', 'xy', 'compressed'

# Added: make cooldown disabled by default for speed (0 wait, 0 retries)
COOLDOWN_WAIT_SEC="0"               # was 65 → 0 to disable waiting by default
COOLDOWN_RETRIES="0"                # was 5  → 0 to disable retries by default

DISABLE_HOOKS="false"                # best-effort owner-only

# Added: funding passthrough to Go tool (optional)
FUNDING_KEY=""                       # Added: if set, pre-fund agents (private key without 0x)
FUNDING_AMOUNT_WEI="10000000000000000"  # Added: default 0.01 ETH in wei

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --contract)
            CONTRACT_ADDRESS="$2"
            shift 2
            ;;
        --rpc)
            RPC_URL="$2"
            shift 2
            ;;
        --key)
            PRIVATE_KEY="$2"
            shift 2
            ;;
        --demo)
            DEMO_FILE="$2"
            shift 2
            ;;
        # Added: new options passthrough for the unified tool
        --mode)
            MODE="$2"
            shift 2
            ;;
        --sigmode)
            SIGMODE="$2"
            shift 2
            ;;
        --sender-in-digest)
            SENDER_IN_DIGEST="$2"
            shift 2
            ;;
        --did-in-digest)
            DID_IN_DIGEST="$2"
            shift 2
            ;;
        --keyhash-mode)
            KEYHASH_MODE="$2"
            shift 2
            ;;
        --cooldown-wait-sec)
            COOLDOWN_WAIT_SEC="$2"
            shift 2
            ;;
        --cooldown-retries)
            COOLDOWN_RETRIES="$2"
            shift 2
            ;;
        --disable-hooks)
            # Added: boolean flag with no value (set to true when present)
            DISABLE_HOOKS="true"
            shift 1
            ;;
        # Added: funding flags passthrough (NEW)
        --funding-key)
            # Added: private key (without 0x) that will fund agent accounts for gas
            FUNDING_KEY="$2"
            shift 2
            ;;
        --funding-amount-wei)
            # Added: amount in wei to fund per agent (default 0.01 ETH)
            FUNDING_AMOUNT_WEI="$2"
            shift 2
            ;;
        --help)
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --contract ADDRESS          Contract address (default: $CONTRACT_ADDRESS)"
            echo "  --rpc URL                   RPC endpoint (default: $RPC_URL)"
            echo "  --key KEY                   Private key without 0x (default: Hardhat account #0)"
            echo "  --demo FILE                 Demo metadata file (default: ../sage-fe/demo-agents-metadata.json)"
            echo ""
            echo "  --mode MODE                 'local' (default) or 'self-signed'"
            echo "  --sigmode MODE              'bytes', 'bytes32'(default), or 'raw'"
            echo "  --sender-in-digest VAL      'auto'(default), 'registrar', or 'agent'"
            echo "  --did-in-digest VAL         'none'(default), 'string', or 'hash'"
            echo "  --keyhash-mode MODE         'raw'(default), 'xy', or 'compressed'"
            echo "  --cooldown-wait-sec N       Wait seconds when cooldown (default: $COOLDOWN_WAIT_SEC)"
            echo "  --cooldown-retries N        Retries for cooldown (default: $COOLDOWN_RETRIES)"
            echo "  --disable-hooks             Try disabling before/after hooks (owner only)"
            echo ""
            # Added: help lines for funding
            echo "  --funding-key KEY           (Optional) Funder private key without 0x to pre-fund agents"
            echo "  --funding-amount-wei AMT    (Optional) Amount in wei per agent (default: $FUNDING_AMOUNT_WEI)"
            echo ""
            echo "Note: In 'self-signed' mode, fund agents with your separate funding script beforehand."
            echo "      Or pass --funding-key here to pre-fund automatically."
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# Check if local blockchain is running
echo " Checking blockchain connection..."
if ! curl -s -X POST --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' $RPC_URL > /dev/null; then
    echo -e "${RED} Error: Cannot connect to blockchain at $RPC_URL${NC}"
    echo ""
    echo "Please make sure the local blockchain is running:"
    echo "  cd sage/contracts/ethereum"
    echo "  npx hardhat node"
    exit 1
fi
echo -e "${GREEN} Blockchain connected${NC}"

# Check if contract is deployed
echo " Checking contract deployment..."
CODE=$(curl -s -X POST --data "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getCode\",\"params\":[\"$CONTRACT_ADDRESS\", \"latest\"],\"id\":1}" $RPC_URL | grep -o '"result":"[^"]*"' | cut -d'"' -f4)
if [ "$CODE" == "0x" ] || [ -z "$CODE" ]; then
    echo -e "${RED} Error: No contract found at $CONTRACT_ADDRESS${NC}"
    echo ""
    echo "Please deploy the SageRegistryV2 contract first:"
    echo "  cd sage/contracts/ethereum"
    echo "  npx hardhat run scripts/deploy-v2.js --network localhost"
    exit 1
fi
echo -e "${GREEN} Contract found at $CONTRACT_ADDRESS${NC}"

# Check if demo file exists
if [ ! -f "$DEMO_FILE" ]; then
    echo -e "${RED} Error: Demo file not found at $DEMO_FILE${NC}"
    exit 1
fi
echo -e "${GREEN} Demo file found${NC}"

# Check if ABI file exists
if [ ! -f "$ABI_FILE" ]; then
    echo -e "${RED} Error: ABI file not found at $ABI_FILE${NC}"
    echo ""
    echo "Please compile the contracts first:"
    echo "  cd sage/contracts/ethereum"
    echo "  npx hardhat compile"
    exit 1
fi
echo -e "${GREEN} ABI file found${NC}"

echo ""
echo "======================================"
echo " Registration Configuration"
echo "======================================"
echo "Contract: $CONTRACT_ADDRESS"
echo "RPC URL: $RPC_URL"
echo "Demo File: $DEMO_FILE"
# Added: show effective execution mode/options
echo "Mode: $MODE"
echo "sigmode: $SIGMODE | sender-in-digest: $SENDER_IN_DIGEST | did-in-digest: $DID_IN_DIGEST | keyhash-mode: $KEYHASH_MODE"
echo "cooldown: ${COOLDOWN_WAIT_SEC}s x ${COOLDOWN_RETRIES} retries | disable-hooks: $DISABLE_HOOKS"
# Added: show funding summary when provided
if [[ -n "$FUNDING_KEY" ]]; then
  echo "funding: enabled | amount-per-agent-wei: $FUNDING_AMOUNT_WEI"
else
  echo "funding: disabled"
fi
echo "======================================"
echo ""

# Added: remind funding if self-signed mode and not handled here
if [[ "$MODE" == "self-signed" && -z "$FUNDING_KEY" ]]; then
    echo -e "${YELLOW}Note:${NC} self-signed mode requires each agent account to have gas."
    echo -e "${YELLOW}      Run your separate funding script BEFORE this if needed, or pass --funding-key here.${NC}"
    echo ""
fi

# Change to project root
cd "$PROJECT_ROOT"

# Run the Go registration script
echo " Starting agent registration..."
echo ""

# Added: build command with optional flags
CMD=( go run tools/registration/register_agents.go
    -contract="$CONTRACT_ADDRESS"
    -rpc="$RPC_URL"
    -key="$PRIVATE_KEY"
    -demo="$DEMO_FILE"
    -abi="$ABI_FILE"
    -mode="$MODE"
    -sigmode="$SIGMODE"
    -sender-in-digest="$SENDER_IN_DIGEST"
    -did-in-digest="$DID_IN_DIGEST"
    -keyhash-mode="$KEYHASH_MODE"
    -cooldown-wait-sec="$COOLDOWN_WAIT_SEC"
    -cooldown-retries="$COOLDOWN_RETRIES"
)

# Added: append boolean only when true to avoid surprising behavior
if [[ "$DISABLE_HOOKS" == "true" ]]; then
    CMD+=( -disable-hooks=true )
fi

# Added: pass funding flags only when provided
if [[ -n "$FUNDING_KEY" ]]; then
    CMD+=( -funding-key="$FUNDING_KEY" -funding-amount-wei="$FUNDING_AMOUNT_WEI" )
fi

# Execute
"${CMD[@]}"

echo ""
echo "======================================"
echo -e "${GREEN} Registration process complete!${NC}"
echo "======================================"
echo ""
echo "Next steps:"
echo "1. Start the backend services:"
echo "   ./scripts/start-backend.sh"
echo ""
echo "2. Start the frontend:"
echo "   cd ../sage-fe"
echo "   npm run dev"
echo ""
echo "3. Access the demo at http://localhost:3000"
