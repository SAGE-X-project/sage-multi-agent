#!/bin/bash

# Agent Registration Script using secp256k1 keys for Ethereum compatibility
# This script generates secp256k1 keys and registers agents on the local blockchain

set -e  # Exit on error

echo "======================================"
echo " SAGE Agent Registration (secp256k1)"
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
KEYS_DIR="$PROJECT_ROOT/keys"

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
        --help)
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --contract ADDRESS  Contract address (default: $CONTRACT_ADDRESS)"
            echo "  --rpc URL          RPC endpoint (default: $RPC_URL)"
            echo "  --key KEY          Private key without 0x (default: Hardhat account #0)"
            echo "  --demo FILE        Demo metadata file (default: ../sage-fe/demo-agents-metadata.json)"
            echo "  --help             Show this help message"
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
echo "Keys Directory: $KEYS_DIR"
echo "======================================"
echo ""

# Change to project root
cd "$PROJECT_ROOT"

# Step 1: Generate secp256k1 keys
echo " Step 1: Generating secp256k1 keys for agents..."
echo ""

go run tools/keygen/generate_secp256k1_keys.go \
    -output="$KEYS_DIR" \
    -demo="$DEMO_FILE"

if [ ! -f "$KEYS_DIR/all_keys.json" ]; then
    echo -e "${RED} Error: Key generation failed${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN} Keys generated successfully${NC}"
echo ""

# Step 2: Register agents with secp256k1 keys
echo " Step 2: Registering agents on blockchain..."
echo ""

go run tools/registration/register_with_secp256k1.go \
    -contract="$CONTRACT_ADDRESS" \
    -rpc="$RPC_URL" \
    -key="$PRIVATE_KEY" \
    -demo="$DEMO_FILE" \
    -abi="$ABI_FILE" \
    -keys="$KEYS_DIR"

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