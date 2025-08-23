#!/bin/bash

# Test script for sage-multi-agent system

# Change to project directory
cd /Users/0xtopaz/work/github/sage-x-project/sage-multi-agent

echo "=== Testing SAGE Multi-Agent System with --skip-verification ==="
echo

# Start root agent
echo "Starting root agent..."
./bin/root --skip-verification --port 8080 &
ROOT_PID=$!
sleep 2

# Start ordering agent
echo "Starting ordering agent..."
./bin/ordering --skip-verification --port 8081 &
ORDERING_PID=$!
sleep 2

# Start planning agent  
echo "Starting planning agent..."
./bin/planning --skip-verification --port 8084 &
PLANNING_PID=$!
sleep 2

echo
echo "All agents started with --skip-verification flag"
echo "Root agent PID: $ROOT_PID"
echo "Ordering agent PID: $ORDERING_PID"
echo "Planning agent PID: $PLANNING_PID"
echo
echo "Press Ctrl+C to stop all agents"

# Wait for interrupt
trap "echo 'Stopping agents...'; kill $ROOT_PID $ORDERING_PID $PLANNING_PID 2>/dev/null; exit" INT
wait