#!/bin/bash

# Start all backend services for local blockchain demo

set -e

echo "======================================"
echo " Starting SAGE Backend Services"
echo "======================================"
echo ""

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
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

# Export configuration paths for local blockchain
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
}

# Clean up old PIDs file
rm -f .pids

# Start Enhanced Client Server with WebSocket
start_service "Enhanced Client Server" \
    "go run client/enhanced_main.go --port 8086 --root-url http://localhost:8080 --ws-port 8085"

# Start Root Agent (skip verification for local testing)
start_service "Root Agent" \
    "go run cli/root/main.go --port 8080 --ordering-url http://localhost:8083 --planning-url http://localhost:8084 --skip-verification"

# Start Ordering Agent
start_service "Ordering Agent" \
    "go run cli/ordering/main.go --port 8083"

# Start Planning Agent
start_service "Planning Agent" \
    "go run cli/planning/main.go --port 8084"

echo ""
echo "======================================"
echo -e "${GREEN} All backend services started!${NC}"
echo "======================================"
echo ""
echo "Services running:"
echo "  • Enhanced Client Server: http://localhost:8086"
echo "  • WebSocket Server: ws://localhost:8085"
echo "  • Root Agent: http://localhost:8080"
echo "  • Ordering Agent: http://localhost:8083"
echo "  • Planning Agent: http://localhost:8084"
echo ""
echo "Check health: curl http://localhost:8086/health"
echo "Check stats: curl http://localhost:8086/metrics"
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