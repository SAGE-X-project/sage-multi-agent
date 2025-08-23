#!/bin/bash

# Agent Funding Script
# This script funds agent addresses with ETH for gas fees

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
AMOUNT="0.1"
RPC_URL="http://localhost:8545"
KEYS_DIR="keys"
DRY_RUN=""

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --amount)
            AMOUNT="$2"
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
        --keys-dir)
            KEYS_DIR="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN="--dry-run"
            shift
            ;;
        --help)
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --amount ETH     Amount of ETH to send to each agent (default: 0.1)"
            echo "  --rpc URL        RPC endpoint (default: http://localhost:8545)"
            echo "  --key KEY        Private key for funding (default: from .env or Hardhat #0)"
            echo "  --keys-dir DIR   Directory containing agent keys (default: keys)"
            echo "  --dry-run        Simulate without sending transactions"
            echo "  --help           Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                           # Fund all agents with 0.1 ETH each"
            echo "  $0 --amount 0.5              # Fund with 0.5 ETH each"
            echo "  $0 --dry-run                 # Check what would be funded"
            echo "  $0 --key ac097...            # Use specific private key"
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

echo -e "${BLUE}======================================"
echo "💰 Funding Agent Addresses"
echo -e "======================================${NC}"
echo ""

# Check if keys directory exists
if [ ! -d "$KEYS_DIR" ]; then
    echo -e "${RED}❌ Keys directory not found: $KEYS_DIR${NC}"
    echo "Please generate agent keys first by running the agents"
    exit 1
fi

# Build the funding tool if needed
echo -e "${YELLOW}🔨 Building funding tool...${NC}"
go build -o scripts/fund_agents scripts/fund_agents.go

# Run the funding tool
if [ -n "$PRIVATE_KEY" ]; then
    ./scripts/fund_agents \
        --amount "$AMOUNT" \
        --rpc "$RPC_URL" \
        --key "$PRIVATE_KEY" \
        --keys "$KEYS_DIR" \
        $DRY_RUN
else
    ./scripts/fund_agents \
        --amount "$AMOUNT" \
        --rpc "$RPC_URL" \
        --keys "$KEYS_DIR" \
        $DRY_RUN
fi

# Clean up
rm -f scripts/fund_agents

echo ""
echo -e "${GREEN}✅ Done!${NC}"