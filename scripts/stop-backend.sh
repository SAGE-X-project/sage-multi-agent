#!/bin/bash

# Stop all backend services

echo "Stopping SAGE backend services..."

# Kill processes from PID file
if [ -f .pids ]; then
    while read pid; do
        if ps -p $pid > /dev/null 2>&1; then
            echo "Stopping process $pid"
            kill $pid 2>/dev/null || true
        fi
    done < .pids
    rm -f .pids
    echo "All services stopped."
else
    echo "No PID file found. Services may not be running."
    
    # Try to find and kill processes by port
    echo "Checking for services on known ports..."
    
    # Kill processes on specific ports
    for port in 8080 8083 8084 8085 8086; do
        pid=$(lsof -ti:$port)
        if [ ! -z "$pid" ]; then
            echo "Stopping service on port $port (PID: $pid)"
            kill $pid 2>/dev/null || true
        fi
    done
fi

echo "Backend services stopped."