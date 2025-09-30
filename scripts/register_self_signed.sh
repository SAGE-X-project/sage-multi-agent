#!/bin/bash

# Self-signed Agent Registration Script
# Each agent registers itself using its own secp256k1 key

set -e

echo "======================================"
echo " Self-Signed Agent Registration"
echo "======================================"
echo ""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Configuration
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# Default values
CONTRACT_ADDRESS="0x5FbDB2315678afecb367f032d93F642f64180aa3"
RPC_URL="http://localhost:8545"
FUNDING_KEY="ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
DEMO_FILE="$PROJECT_ROOT/../sage-fe/demo-agents-metadata.json"
ABI_FILE="$PROJECT_ROOT/../sage/contracts/ethereum/artifacts/contracts/SageRegistryV2.sol/SageRegistryV2.json"
KEYS_DIR="$PROJECT_ROOT/keys"

# Change to project root to ensure go.mod is found
cd "$PROJECT_ROOT"

# Check if keys exist, if not generate them
if [ ! -f "$KEYS_DIR/all_keys.json" ]; then
    echo " Generating secp256k1 keys..."
    go run tools/keygen/generate_secp256k1_keys.go \
        -output="$KEYS_DIR" \
        -demo="$DEMO_FILE"
    
    if [ ! -f "$KEYS_DIR/all_keys.json" ]; then
        echo -e "${RED} Error: Key generation failed${NC}"
        exit 1
    fi
    echo -e "${GREEN} Keys generated${NC}"
    echo ""
fi

# Run self-signed registration
echo " Starting self-signed registration..."
echo ""

go run tools/registration/register_self_signed.go \
    -contract="$CONTRACT_ADDRESS" \
    -rpc="$RPC_URL" \
    -funding-key="$FUNDING_KEY" \
    -demo="$DEMO_FILE" \
    -abi="$ABI_FILE" \
    -keys="$KEYS_DIR"

echo ""
echo "======================================"
echo -e "${GREEN} Registration complete!${NC}"
echo "======================================"