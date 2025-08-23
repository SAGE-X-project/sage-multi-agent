# SAGE Multi-Agent System

A secure multi-agent communication system implementing the SAGE (Secure Agent Gateway Exchange) protocol with blockchain-based identity verification. This system demonstrates cryptographically secure agent-to-agent messaging using RFC-9421 compliant signatures and Ethereum blockchain for public key management.

## Key Features

### SAGE Protocol Implementation
- **RFC-9421 Compliant**: Full implementation of HTTP message signatures standard
- **Blockchain Integration**: Agent identities and public keys stored on Ethereum smart contracts
- **Cryptographic Security**: ECDSA-secp256k1 signatures for message authentication
- **Strict Verification**: Reject-on-failure mode prevents processing of unverified messages
- **Error Response System**: Automated error responses for failed verifications

### Multi-Agent Architecture
- **Distributed Agents**: Independent agents communicating through secure channels
- **Message Routing**: Intelligent routing based on agent capabilities
- **Request/Response Correlation**: Track conversations with unique IDs and nonces
- **Conversation Management**: Complete request-response chain tracking

## System Architecture

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│   Client    │────▶│  Root Agent  │────▶│ Sub-Agents   │
│  (Frontend) │     │  (Router)    │     │ (Specialized)│
└─────────────┘     └──────────────┘     └──────────────┘
       │                    │                     │
       └────────────────────┼─────────────────────┘
                           │
                    ┌──────▼──────┐
                    │  Blockchain  │
                    │   (Ethereum) │
                    │  Registry    │
                    └──────────────┘
```

### Core Components

#### Agents
- **Root Agent** (Port 8080): Central router and orchestrator
- **Ordering Agent** (Port 8083): Order processing and management
- **Planning Agent** (Port 8084): Planning and scheduling tasks

#### SAGE Components
- **Message Signer**: Creates RFC-9421 compliant signatures
- **Message Verifier**: Validates signatures using blockchain public keys
- **SAGE Manager**: Orchestrates signing/verification operations
- **Ethereum Adapter**: Interfaces with blockchain for DID resolution

#### Client Interfaces
- **API Server** (Port 8086): REST API for frontend integration
- **CLI Client**: Command-line interface for testing
- **WebSocket Server**: Real-time bidirectional communication

## Getting Started

### Prerequisites

1. **Go 1.21+**: Required for building the system
2. **Ethereum Node**: Local or remote (Hardhat, Ganache, or mainnet/testnet)
3. **Environment Variables**:
   ```bash
   # Blockchain Configuration
   export LOCAL_RPC_ENDPOINT=http://127.0.0.1:8545
   export LOCAL_CONTRACT_ADDRESS=0x5FbDB2315678afecb367f032d93F642f64180aa3
   export LOCAL_CHAIN_ID=31337
   
   # Optional: API Keys
   export GOOGLE_API_KEY=your_gemini_api_key  # For AI features
   ```

### Quick Start

#### 1. Clone and Build
```bash
git clone https://github.com/sage-x-project/sage-multi-agent
cd sage-multi-agent

# Build all components
make build

# Or build individually
make build-agents
make build-tools
make build-tests
```

#### 2. Deploy Smart Contracts (if using local blockchain)
```bash
# Start local blockchain (in separate terminal)
npx hardhat node

# Deploy SAGE Registry contract
cd ../sage/contracts
npm run deploy:local

# Note the contract address and update LOCAL_CONTRACT_ADDRESS
```

#### 3. Register Agents on Blockchain
```bash
# Generate keys for agents
./scripts/register_agents.sh

# Fund agents with ETH (for gas fees)
./scripts/fund_agents.sh

# Register agents on blockchain
go run tools/registration/register_local_agents.go
```

#### 4. Start Agents
```bash
# Start all agents (in separate terminals or use screen/tmux)
./scripts/start-backend.sh

# Or start individually
./cli/ordering/ordering
./cli/planning/planning  
./cli/root/root
```

#### 5. Test the System
```bash
# Run comprehensive tests
make test

# Test SAGE verification
go run test/test_blockchain_verification.go

# Test agent messaging
go run test/test_agent_messaging.go
```

## API Usage

### REST API

#### Send Message to Agent
```bash
curl -X POST http://localhost:8086/send/prompt \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Plan a 3-day trip to Tokyo"}'
```

#### Toggle SAGE Protocol
```bash
# Enable SAGE
curl -X POST http://localhost:8086/api/sage/enable

# Disable SAGE  
curl -X POST http://localhost:8086/api/sage/disable

# Check status
curl http://localhost:8086/api/sage/status
```

### WebSocket API
```javascript
const ws = new WebSocket('ws://localhost:8087/ws');

ws.onopen = () => {
  ws.send(JSON.stringify({
    type: 'message',
    content: 'Process order for product XYZ'
  }));
};

ws.onmessage = (event) => {
  const response = JSON.parse(event.data);
  console.log('Response:', response);
};
```

## SAGE Protocol Details

### Message Structure
Each message includes:
- **Agent DID**: Unique identifier registered on blockchain
- **Message ID**: Unique message identifier
- **Timestamp**: Message creation time
- **Nonce**: Replay attack prevention
- **Algorithm**: Signature algorithm (ECDSA-secp256k1)
- **Signature**: Cryptographic signature of message
- **Metadata**: Additional context and routing information

### Verification Process
1. Extract agent DID from message
2. Resolve public key from blockchain
3. Verify signature using public key
4. Check timestamp freshness
5. Validate nonce uniqueness
6. Return verification result or error

### Request/Response Correlation
```json
{
  "request": {
    "message_id": "root-1234567890-abc123",
    "from_agent_did": "did:sage:ethereum:root_agent_001",
    "to_agent_did": "did:sage:ethereum:ordering_agent_001",
    "request_context": {
      "request_id": "req-123",
      "nonce": "unique-nonce-456"
    }
  },
  "response": {
    "response_id": "ordering-resp-1234567891-def456",
    "in_response_to": {
      "original_request_id": "req-123",
      "original_sender_did": "did:sage:ethereum:root_agent_001",
      "original_nonce": "unique-nonce-456",
      "original_message_digest": "sha256:..."
    }
  }
}
```

## Testing

### Unit Tests
```bash
# Run all tests
make test

# Run with coverage
make test-coverage

# Run specific tests
go test ./adapters/...
go test ./types/...
```

### Integration Tests
```bash
# Test blockchain verification
go run test/test_blockchain_verification.go

# Test agent messaging
go run test/test_agent_messaging.go

# Test SAGE API
go run test/test_sage_api.go
```

### Manual Testing
```bash
# Use CLI client
./cli/cli -url http://localhost:8080

# Check SAGE status
curl http://localhost:8086/api/sage/status

# Send test message
curl -X POST http://localhost:8086/api/agent/message \
  -H "Content-Type: application/json" \
  -H "X-Agent-DID: did:sage:ethereum:root_agent_001" \
  -d '{"message": "Test message"}'
```

## Project Structure

```
sage-multi-agent/
├── adapters/           # SAGE protocol adapters
│   ├── message_signer.go
│   ├── message_verifier.go
│   ├── sage_manager.go
│   └── ethereum_resolver_adapter.go
├── api/                # API handlers
├── cli/                # CLI tools
├── config/             # Configuration management
├── configs/            # Configuration files
├── scripts/            # Deployment and setup scripts
├── test/               # Test files
├── tools/              # Utility tools
├── types/              # Type definitions
└── websocket/          # WebSocket server
```

## Configuration

### Agent Configuration (`configs/agent_config.yaml`)
```yaml
agents:
  root:
    did: "did:sage:ethereum:root_agent_001"
    name: "Root Orchestrator Agent"
    endpoint: "http://localhost:8080"
    key_file: "keys/root-key.json"
    capabilities:
      type: "root"
      
  ordering:
    did: "did:sage:ethereum:ordering_agent_001"
    name: "Ordering Agent"
    endpoint: "http://localhost:8083"
    key_file: "keys/ordering-key.json"
    capabilities:
      type: "ordering"
```

### Network Configuration
See [Network Configuration Guide](docs/NETWORK_CONFIGURATION.md) for detailed setup instructions.

## Development

### Adding New Agents
1. Define agent in `configs/agent_config.yaml`
2. Generate keys: `go run tools/keygen/generate_secp256k1_keys.go -agent new_agent`
3. Register on blockchain: Update registration script
4. Implement agent logic
5. Add to startup scripts

### Extending SAGE Protocol
1. Implement new verification methods in `adapters/`
2. Add new message types in `types/`
3. Update handlers in `api/`
4. Add tests in `test/`

## Documentation

- [Setup Guide](docs/SETUP_GUIDE.md)
- [Network Configuration](docs/NETWORK_CONFIGURATION.md)
- [Backend Integration](BACKEND_INTEGRATION.md)
- [Production Communication](docs/PRODUCTION_COMMUNICATION.md)

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Acknowledgments

- SAGE Protocol specification
- RFC-9421 HTTP Message Signatures
- Ethereum blockchain for decentralized identity
- Go-Ethereum for blockchain interaction