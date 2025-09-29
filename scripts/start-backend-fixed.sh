#!/bin/bash

# Start backend services with fixed SAGE integration

set -e

echo "======================================"
echo " Starting Fixed Backend Services"
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

# Load local environment
if [ -f ".env.local" ]; then
    export $(grep -v '^#' .env.local | xargs)
    echo -e "${GREEN} Loaded .env.local${NC}"
else
    echo -e "${YELLOW}  No .env.local found, using defaults${NC}"
fi

# Export configuration paths
export PROJECT_ROOT="$PROJECT_ROOT"
export AGENT_CONFIG_PATH="configs/agent_config_local.yaml"
export REGISTRATION_CONFIG_PATH="configs/agent_registration_local.yaml"

# Function to start a service
start_service() {
    local name=$1
    local cmd=$2
    echo -e "${GREEN}Starting $name...${NC}"
    $cmd &
    local pid=$!
    echo "$name PID: $pid"
    echo $pid >> .pids
    sleep 2
    
    # Check if process is still running
    if ! ps -p $pid > /dev/null 2>&1; then
        echo -e "${RED} $name failed to start${NC}"
        return 1
    fi
    
    return 0
}

# Clean up old PIDs file
rm -f .pids

# Start services with fixed code
echo ""
echo "Starting services with fixed SAGE integration..."
echo ""

# Start Client Server
if start_service "Client Server" \
    "go run client/main.go --port 8086 --root-url http://localhost:8080"; then
    echo -e "${GREEN} Client Server started${NC}"
else
    echo -e "${RED} Client Server failed${NC}"
fi

# Start Root Agent with fixed code and skip verification
if start_service "Root Agent (Fixed)" \
    "go run cli/root/main_fixed.go --port 8080 --ordering-url http://localhost:8083 --planning-url http://localhost:8084 --skip-verification"; then
    echo -e "${GREEN} Root Agent started${NC}"
else
    echo -e "${RED} Root Agent failed${NC}"
fi

# Start Ordering Agent
if start_service "Ordering Agent" \
    "go run cli/ordering/main.go --port 8083"; then
    echo -e "${GREEN} Ordering Agent started${NC}"
else
    echo -e "${RED} Ordering Agent failed${NC}"
fi

# Start Planning Agent
if start_service "Planning Agent" \
    "go run cli/planning/main.go --port 8084"; then
    echo -e "${GREEN} Planning Agent started${NC}"
else
    echo -e "${RED} Planning Agent failed${NC}"
fi

echo ""
echo "======================================"
echo -e "${GREEN} Backend services starting!${NC}"
echo "======================================"
echo ""
echo "Services:"
echo "  • Client Server: http://localhost:8086"
echo "  • Root Agent: http://localhost:8080"
echo "  • Ordering Agent: http://localhost:8083"
echo "  • Planning Agent: http://localhost:8084"
echo ""
echo "Test endpoints:"
echo "  curl http://localhost:8086/health"
echo "  curl http://localhost:8080/health"
echo ""
echo "Send a test prompt:"
echo '  curl -X POST http://localhost:8086/send/prompt \'
echo '    -H "Content-Type: application/json" \'
echo '    -d '"'"'{"prompt": "Hello, test message"}'"'"
echo ""
echo "To stop all services: ./scripts/stop-backend.sh"
echo ""
echo "Press Ctrl+C to stop all services"

# Function to cleanup on exit
cleanup() {
    echo ""
    echo "Stopping all services..."
    if [ -f .pids ]; then
        while read pid; do
            if ps -p $pid > /dev/null 2>&1; then
                kill $pid 2>/dev/null || true
            fi
        done < .pids
        rm -f .pids
    fi
    echo "All services stopped."
    exit 0
}

# Set up trap for cleanup
trap cleanup INT TERM

# Wait for all background processes
wait