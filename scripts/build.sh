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
build_component "Root Agent" "./cli/root" "bin/root"

# Build Ordering Agent
build_component "Ordering Agent" "./cli/ordering" "bin/ordering"

# Build Planning Agent
build_component "Planning Agent" "./cli/planning" "bin/planning"

# Build CLI Client
build_component "CLI Client" "./cli" "bin/cli"

# Build Client Server (basic version)
build_component "Client Server" "./client/main.go" "bin/client"

# Build Enhanced Client Server
build_component "Enhanced Client Server" "./client/enhanced_main.go" "bin/enhanced_client"

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