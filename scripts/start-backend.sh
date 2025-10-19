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
# Use existing config files in configs/
export AGENT_CONFIG_PATH="configs/agent_config.yaml"
export REGISTRATION_CONFIG_PATH="configs/agent_registration.yaml"

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

# Start Client Server (HTTP gateway to gRPC A2A)
start_service "Client Server" \
    "go run client/main.go --port 8086 --grpc localhost:8084"

# Start Root Agent
start_service "Root Agent" \
    "go run ./cmd/root --port 8080 --ordering-url http://localhost:8082 --planning-url http://localhost:8081 --payment-url http://localhost:8083 --sage true"

# Start Ordering Agent
start_service "Ordering Agent" \
    "go run ./cmd/ordering --port 8082 --sage true"

# Start Planning Agent
start_service "Planning Agent" \
    "go run ./cmd/planning --port 8081 --sage true"

# Start Payment Agent
start_service "Payment Agent" \
    "go run ./cmd/payment --port 8083 --sage true"

echo ""
echo "======================================"
echo -e "${GREEN} All backend services started!${NC}"
echo "======================================"
echo ""
echo "Services running:"
echo "  • Client Server:  http://localhost:8086"
echo "  • Root Agent:     http://localhost:8080 (A2A gRPC :8084)"
echo "  • Planning Agent: http://localhost:8081"
echo "  • Ordering Agent: http://localhost:8082"
echo "  • Payment Agent:  http://localhost:8083"
echo ""
echo "Send a test prompt:"
echo "  curl -X POST http://localhost:8086/send/prompt -H 'Content-Type: application/json' -d '{"prompt":"hello"}'"
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
