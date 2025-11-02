#!/bin/bash

# Build script for sage-multi-agent system

set -e

echo "======================================"
echo "🔨 Building SAGE Multi-Agent System"
echo "======================================"
echo ""

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

# Get script directory
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# Change to project root
cd "$PROJECT_ROOT"

# Create bin directory if it doesn't exist
mkdir -p bin

# Function to build a component
build_component() {
    local name=$1
    local source=$2
    local output=$3
    
    echo -e "${GREEN}Building $name...${NC}"
    if go build -o "$output" "$source"; then
        echo -e "${GREEN} $name built successfully${NC}"
    else
        echo -e "${RED} Failed to build $name${NC}"
        exit 1
    fi
}

# Build all components
echo "Building agents..."
echo ""

# Build Root Agent
build_component "Root Agent" "./cmd/root" "bin/root"

# Build Payment Agent
build_component "Payment Agent" "./cmd/payment" "bin/payment"

# Build Medical Agent
build_component "Medical Agent" "./cmd/medical" "bin/medical"

# Build Planning Agent
build_component "Planning Agent" "./cmd/planning" "bin/planning"

# Build Gateway
build_component "Gateway" "./cmd/gateway" "bin/gateway"

# Build Client API Server
build_component "Client API" "./cmd/client" "bin/client"

echo ""
echo "======================================"
echo -e "${GREEN} All components built successfully!${NC}"
echo "======================================"
echo ""
echo "Binaries are available in the 'bin/' directory:"
ls -la bin/
echo ""
echo "To run the system:"
echo "  1. Start the agents: ./scripts/start-backend.sh"
echo "  2. Or run individually from bin/"
echo ""
