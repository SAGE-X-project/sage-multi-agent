# Sage-Multi-Agent

This project demonstrates a multi-agent system built with the A2A (Agent-to-Agent) framework and integrated with the SAGE protocol. The system features a root agent that intelligently routes requests to specialized sub-agents based on the content analysis of user prompts.  
The root agent does not directly deliver messages to sub-agents, but sends them to each sub-agent's gateway, which is intentionally malicious and tampers with the user's initial prompt for demonstration purposes.  
With SAGE protocol enabled (default), the system detects and prevents malicious attacks from the gateway. When SAGE protocol is disabled (using `-activate-sage=false`), the agents process the tampered messages without detecting the manipulation, demonstrating the importance of message integrity verification.

## System Architecture

The system consists of multiple components working together:

### Core Agents

* **Root Agent** (Port: 8080)
  - Acts as the central router for the system
  - Analyzes incoming requests and directs them to appropriate specialized agents
  - Manages communication between sub-agents

* **Specialized Sub-Agents**
  - **Ordering Agent** (Port: 8082)
    * Handles product ordering requests
    * Provides simulated order processing (demo purposes only)
    * Generates detailed order confirmations
  
  - **Planning Agent** (Port: 8084)
    * Creates comprehensive trip plans
    * Suggests places to visit, accommodations, and dining options
    * Provides customized travel itineraries

### Client Applications

* **Client Server** (Port: 8086)
  - Frontend interface server
  - Provides REST API endpoint for user prompts
  - Endpoint: POST `/send/prompt`
  - Forwards requests to the root agent and returns responses

* **CLI Client**
  - Command-line interface for testing
  - Direct interaction with the agent system
  - Recommended for development and testing

## Getting Started

### Prerequisites

* Go (version 1.23 or later)
* Google API Key for Gemini model
  - Get it from [Google AI Studio](https://ai.google.dev/gemini-api/docs/api-key)
  - Set as environment variable: `export GOOGLE_API_KEY=your_api_key`
* Blockchain network configuration
  - See [Network Configuration Guide](docs/NETWORK_CONFIGURATION.md) for details

### Using Makefile (Recommended)

```bash
cd sage-multi-agent

# Show all available commands
make help

# Build all components
make build

# Build specific components
make build-agents    # Build all agents
make build-cli       # Build CLI client
make build-root      # Build root agent only
make build-ordering  # Build ordering agent only
make build-planning  # Build planning agent only

# Clean build artifacts
make clean

# Run tests
make test
make test-coverage   # With coverage report

# Run agents directly
make run-root
make run-ordering
make run-planning
```

### Manual Build

```bash
cd sage-multi-agent

# Build the root agent
go build -o cli/root/root ./cli/root

# Build the sub-agents
go build -o cli/ordering/ordering ./cli/ordering
go build -o cli/planning/planning ./cli/planning

# Build the CLI client
go build -o cli/cli ./cli

# Build the client server
go build -o client/client ./client
```

### Running the Agents

Run each agent in a separate terminal window:

#### Terminal 1: Ordering Agent
```bash
cd sage-multi-agent
./cli/ordering/ordering
# SAGE protocol is activated by default

# To deactivate SAGE protocol, use activate-sage=false flag
./cli/ordering/ordering -activate-sage=false
```

#### Terminal 2: Planning Agent
```bash
cd sage-multi-agent
./cli/planning/planning
# SAGE protocol is activated by default

# To deactivate SAGE protocol, use activate-sage=false flag
# You may also set custom port with -port flag
./cli/planning/planning -activate-sage=false -port 8085

```

#### Terminal 3: Root Agent
```bash
cd sage-multi-agent
./cli/root/root
```

Remember to set the `GOOGLE_API_KEY` environment variable to use Gemini model:

```bash
export GOOGLE_API_KEY=your_api_key
./cli/root/root
```

### Running in Background (Alternative)

Alternatively, you can run all agents in the background:

```bash
cd sage-multi-agent
./cli/ordering/ordering -port 8081 &
./cli/planning/planning -port 8084 &
./cli/root/root -port 8080 &
```

## Using the CLI to Interact with the System

Once all agents are running, use the CLI client to interact with the system:

```bash
cd sage-multi-agent
./cli/cli
```

This will connect to the root agent at http://localhost:8080 by default. You can specify a different URL with the `-url` flag:

```bash
./cli/cli -url http://localhost:8080
```

After starting the CLI, you'll see a prompt where you can type requests:

```
Connected to root agent at http://localhost:8080
Type your requests and press Enter. Type 'exit' to quit.
> 
```

## Using the Client server to Interact with the System(Recommended for Frontend Integration)

use client server if you want to deliver user input with api. 

run the client server
```bash
cd sage-multi-agent
./client/client
```

Send a POST request to test the API:
```bash
curl -X POST http://localhost:8086/send/prompt \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Give me 3 days plan to Tokyo next week"}'
```

## How It Works

1. The client(cli or client server) sends your request to the root agent.
2. The root agent analyzes the content of your request to determine which specialized agent should handle it.
3. The root agent forwards your request to the appropriate sub-agent's gateway
4. The sub-agent's gateway tampers the user's prompt and delivers it to the sub-agent
5. If SAGE protocol is enabled (default), it detects the tampering and returns an authorization error
6. If SAGE is disabled (-activate-sage=false), the sub-agent processes the tampered request without detection
7. The root agent returns this response to the client.
8. The client displays the response.

## Stopping the Agents

If you ran the agents in separate terminal windows, use Ctrl+C to stop each one.

If you ran them in the background, find and kill the processes:

```bash
# Find the PIDs
pgrep -f "root|ordering|planning"

# Kill the processes
kill $(pgrep -f "root|ordering|planning")
``` 
 