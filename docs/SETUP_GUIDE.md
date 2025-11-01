# SAGE Multi-Agent Setup Guide

## Overview

This guide explains how to set up and configure the SAGE multi-agent system with proper contract integration, agent registration, and key management.

## Prerequisites

1. Go 1.23 or later
2. Access to an Ethereum or Kaia RPC endpoint
3. A funded wallet for contract deployment and agent registration
4. SAGE contracts deployed on your chosen network

## Configuration Files

### 1. Environment Variables (.env)

Create a `.env` file based on `.env.example`:

```bash
cp .env.example .env
```

Configure the following:

- **Contract Addresses**: Set the deployed contract addresses for your network
- **RPC Endpoints**: Configure Ethereum/Kaia RPC endpoints
- **API Keys**: Add your Google API key for LLM functionality
- **Private Key**: Set `REGISTRATION_PRIVATE_KEY` for agent registration

### 2. Agent Configuration (configs/agent_config.yaml)

This file defines all agents in the system:

- **Agent DIDs**: Unique identifiers for each agent
- **Endpoints**: Where each agent is accessible
- **Capabilities**: JSON-structured capabilities for each agent
- **Key Files**: Where agent keys are stored

### 3. Registration Configuration (configs/agent_registration.yaml)

Controls how agents are registered on the blockchain:

- **Network**: Which blockchain to use (ethereum/kaia)
- **Contract Path**: Path to the contract ABI
- **Gas Settings**: Gas limit and price configuration

## Setup Steps

### Step 1: Install Dependencies

```bash
go mod download
```

### Step 2: Generate Agent Keys

Keys are automatically generated when agents start for the first time. They are stored in the `keys/` directory.

### Step 3: Register Agents on Blockchain

Before starting the multi-agent system, all agents must be registered on the blockchain:

```bash
# Build the register CLI tool
go build -o bin/register cli/register/main.go

# Check registration status of all agents
./bin/register --check

# Check registration status of a specific agent
./bin/register --check --agent root

# Register all agents
./bin/register --all

# Or register individually
./bin/register --agent root
./bin/register --agent medical
./bin/register --agent planning
```

**Important Environment Setup:**

- Make sure your `.env` file has the correct network configuration
- For local development with Hardhat:
  ```bash
  SAGE_NETWORK=local
  LOCAL_CONTRACT_ADDRESS=0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512
  LOCAL_RPC_ENDPOINT=http://127.0.0.1:8545
  REGISTRATION_PRIVATE_KEY=ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
  ```
- Ensure `configs/agent_config.yaml` has matching network configuration:
  ```yaml
  network:
    chain: "local" # Must match SAGE_NETWORK in .env
  ```

### Step 4: Start the Agents

Start each agent in a separate terminal:

```bash
# Terminal 1: Root Agent
go run cli/root/main.go

# Terminal 2: MEDICAL Agent
go run cli/medical/main.go

# Terminal 3: Planning Agent
go run cli/planning/main.go

# Terminal 4: Client Interface
go run client/main.go
```

## Key Management

### Key Generation

Keys are automatically generated using SAGE's crypto package:

- Ed25519 keys for message signing
- Keys are stored securely in the `keys/` directory
- Each agent has its own unique key pair

### Key Storage Structure

```
keys/
├── root_agent.key          # Root agent's private key
├── root_agent.key.info     # Key metadata
├── MEDICAL_agent.key      # MEDICAL agent's private key
├── MEDICAL_agent.key.info # Key metadata
├── planning_agent.key      # Planning agent's private key
└── planning_agent.key.info # Key metadata
```

## Agent Registration Verification

The system automatically verifies that agents are registered before starting:

1. **Automatic Verification**: Agents check their registration status on startup
2. **Manual Override**: Use `--skip-verification` flag to skip (for testing only)
3. **Error Messages**: Clear instructions if registration is missing

Example output when agent is not registered:

```
========================================
AGENT REGISTRATION REQUIRED
========================================
The agent 'Root Orchestrator Agent' with DID 'did:sage:ethereum:root_agent_001' is not registered on the blockchain.

To register this agent, run:
  go run cli/register/main.go --agent root

Or to register all agents:
  go run cli/register/main.go --all
========================================
```

## Capabilities Schema

Agent capabilities follow a defined JSON schema (see `docs/capabilities-schema.json`):

```json
{
  "type": "root",
  "version": "1.0.0",
  "skills": ["task_routing", "agent_coordination", "request_analysis"],
  "subagents": ["medical", "planning"]
}
```

### Validation

Capabilities are automatically validated:

1. **Schema Validation**: Ensures correct structure
2. **Required Skills**: Verifies minimum required skills per agent type
3. **Version Format**: Validates semantic versioning

## Troubleshooting

### Common Issues

1. **"Agent not registered" error**

   - Run the registration CLI command
   - Ensure you have the correct private key set
   - Check contract address configuration

2. **"Failed to resolve DID" error**

   - Verify contract is deployed correctly
   - Check RPC endpoint is accessible
   - Ensure agent is registered on-chain

3. **Key generation issues**

   - Check write permissions for `keys/` directory
   - Ensure SAGE package is properly installed

4. **Contract interaction failures**
   - Verify contract ABI path is correct
   - Check account has sufficient gas
   - Ensure network configuration matches deployment

### Debug Mode

Enable detailed logging:

```bash
export LOG_LEVEL=debug
go run cli/root/main.go
```

## Security Considerations

1. **Private Keys**: Never commit private keys to version control
2. **Environment Variables**: Use `.env` files and keep them in `.gitignore`
3. **Key Storage**: Ensure `keys/` directory has restricted permissions (700)
4. **Contract Access**: Use separate accounts for development and production

## Network Configuration

### Ethereum Mainnet

```env
ETH_CONTRACT_ADDRESS=0x...
ETH_RPC_ENDPOINT=https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY
ETH_CHAIN_ID=1
```

### Kaia Testnet (Kairos)

```env
KAIA_CONTRACT_ADDRESS=0x...
KAIA_RPC_ENDPOINT=https://public-en.kairos.node.kaia.io
KAIA_CHAIN_ID=1001
```

## Monitoring

### Agent Status

Check agent registration and status:

```bash
# Check all agents
go run cli/register/main.go --check

# Verify specific agent
go run sage/verifier/main.go --agent root
```

### WebSocket Logs

Connect to WebSocket server for real-time logs:

```
ws://localhost:8085/ws
```

## Next Steps

1. Deploy smart contracts if not already deployed
2. Configure agent capabilities for your use case
3. Implement custom agent logic
4. Set up monitoring and alerting
5. Prepare for production deployment
