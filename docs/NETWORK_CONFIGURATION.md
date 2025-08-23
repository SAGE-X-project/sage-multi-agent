# Network Configuration Guide

This guide explains how to configure SAGE Multi-Agent system to work with different blockchain networks.

## Supported Networks

The system supports the following networks:

| Network | Chain ID | Type | Description |
|---------|----------|------|-------------|
| `local` | 31337 | Development | Local Hardhat node |
| `ethereum` | 1 | Mainnet | Ethereum mainnet |
| `sepolia` | 11155111 | Testnet | Ethereum Sepolia testnet |
| `kaia` | 8217 | Mainnet | Kaia (formerly Klaytn) mainnet |
| `kairos` | 1001 | Testnet | Kaia testnet (Kairos) |

## Configuration Steps

### 1. Create Environment File

Copy the example environment file:

```bash
cp .env.example .env
```

### 2. Select Network

Set the `SAGE_NETWORK` variable to your desired network:

```env
# Select which network to use
SAGE_NETWORK=local  # Options: local, ethereum, sepolia, kaia, kairos
```

### 3. Configure Network-Specific Settings

Based on your selected network, configure the appropriate variables:

#### Local Network (Hardhat)

```env
SAGE_NETWORK=local
LOCAL_CONTRACT_ADDRESS=0x5FbDB2315678afecb367f032d93F642f64180aa3
LOCAL_RPC_ENDPOINT=http://localhost:8545
LOCAL_CHAIN_ID=31337
```

**Setup:**
1. Start local Hardhat node:
   ```bash
   cd ../sage/contracts/ethereum
   npx hardhat node
   ```
2. Deploy contracts:
   ```bash
   npx hardhat run scripts/deploy-v2.js --network localhost
   ```
3. Update `LOCAL_CONTRACT_ADDRESS` with deployed contract address

#### Ethereum Mainnet

```env
SAGE_NETWORK=ethereum
ETH_CONTRACT_ADDRESS=0xYourContractAddress
ETH_RPC_ENDPOINT=https://eth-mainnet.g.alchemy.com/v2/YOUR_API_KEY
ETH_CHAIN_ID=1
```

**Requirements:**
- Alchemy or Infura API key
- Deployed SAGE contract on Ethereum mainnet
- ETH for gas fees

#### Sepolia Testnet

```env
SAGE_NETWORK=sepolia
SEPOLIA_CONTRACT_ADDRESS=0xYourContractAddress
SEPOLIA_RPC_ENDPOINT=https://eth-sepolia.g.alchemy.com/v2/YOUR_API_KEY
SEPOLIA_CHAIN_ID=11155111
```

**Requirements:**
- Alchemy or Infura API key
- Deployed SAGE contract on Sepolia
- Sepolia ETH from faucet

#### Kaia Mainnet

```env
SAGE_NETWORK=kaia
KAIA_CONTRACT_ADDRESS=0xYourContractAddress
KAIA_RPC_ENDPOINT=https://public-en-cypress.klaytn.net
KAIA_CHAIN_ID=8217
```

**Requirements:**
- Deployed SAGE contract on Kaia mainnet
- KAIA tokens for gas fees

#### Kairos Testnet (Kaia Testnet)

```env
SAGE_NETWORK=kairos
KAIROS_CONTRACT_ADDRESS=0xYourContractAddress
KAIROS_RPC_ENDPOINT=https://public-en.kairos.node.kaia.io
KAIROS_CHAIN_ID=1001
```

**Requirements:**
- Deployed SAGE contract on Kairos
- Test KAIA from faucet: https://kairos.wallet.kaia.io/faucet

### 4. Configure Agent Registration Key

Set the private key for agent registration (without 0x prefix):

```env
REGISTRATION_PRIVATE_KEY=your_private_key_here
```

⚠️ **Security Warning**: Never commit private keys to version control!

### 5. Set API Keys

Configure required API keys:

```env
GOOGLE_API_KEY=your_google_api_key_here
```

## Network-Specific Configuration Files

You can also override network settings in agent configuration files:

### configs/agent_config.yaml

```yaml
network:
  chain: "kairos"  # Override SAGE_NETWORK from env
  confirmation_blocks: 3
  gas_limit: 3000000
```

### configs/agent_config_local.yaml

```yaml
network:
  chain: "local"
  confirmation_blocks: 1
  gas_limit: 5000000
```

## Running with Different Networks

### Using Environment Variable

```bash
# Default (uses SAGE_NETWORK from .env)
./bin/ordering -port 8083

# Override network for specific run
SAGE_NETWORK=sepolia ./bin/ordering -port 8083
```

### Using Configuration File

```bash
# Use local configuration
AGENT_CONFIG_PATH=configs/agent_config_local.yaml ./bin/ordering

# Use production configuration
AGENT_CONFIG_PATH=configs/agent_config.yaml ./bin/ordering
```

## Troubleshooting

### Error: "NETWORK_CONTRACT_ADDRESS not set"

This error occurs when the contract address for the selected network is not configured.

**Solution:**
1. Check your `SAGE_NETWORK` setting
2. Ensure the corresponding `*_CONTRACT_ADDRESS` is set
3. Example for Kairos:
   ```env
   SAGE_NETWORK=kairos
   KAIROS_CONTRACT_ADDRESS=0xYourContractAddress
   ```

### Error: "unsupported network"

This error occurs when an invalid network name is specified.

**Solution:**
Use one of the supported network names:
- `local` (or `localhost`, `hardhat`)
- `ethereum` (or `eth`, `mainnet`)
- `sepolia`
- `kaia` (or `klaytn`, `cypress`)
- `kairos` (or `kaia-testnet`)

### Connection Errors

If you cannot connect to the RPC endpoint:

1. **Check RPC URL**: Ensure the RPC endpoint is correct and accessible
2. **API Keys**: Verify API keys for Alchemy/Infura are valid
3. **Network Status**: Check if the network is operational
4. **Firewall**: Ensure your firewall allows outbound HTTPS connections

## Testing Network Configuration

Test your configuration:

```bash
# Build the agents
make build

# Test connection (will fail if network not configured properly)
./bin/ordering -port 8083 -skip-verification

# Check logs for network connection
tail -f logs/ordering.log
```

## Best Practices

1. **Development**: Use `local` network for development and testing
2. **Staging**: Use `sepolia` or `kairos` for staging/testing
3. **Production**: Use `ethereum` or `kaia` for production
4. **Security**: 
   - Never commit `.env` files with real private keys
   - Use different keys for different environments
   - Rotate keys regularly
5. **Monitoring**: Set up monitoring for RPC endpoint availability

## Advanced Configuration

### Custom RPC Endpoints

You can use custom RPC endpoints for any network:

```env
# Custom Ethereum RPC
ETH_RPC_ENDPOINT=https://your-custom-node.example.com

# Custom Kaia RPC
KAIA_RPC_ENDPOINT=https://your-kaia-node.example.com
```

### Multiple Environments

Create environment-specific files:

```bash
# Development
.env.development

# Staging
.env.staging

# Production
.env.production
```

Load specific environment:

```bash
# Load staging environment
cp .env.staging .env
./bin/ordering

# Or use dotenv directly
dotenv -e .env.staging ./bin/ordering
```

## Contract Deployment

For deploying SAGE contracts on different networks, refer to the SAGE documentation:

```bash
cd ../sage/contracts/ethereum

# Deploy to Sepolia
npx hardhat run scripts/deploy-v2.js --network sepolia

# Deploy to Kairos
npx hardhat run scripts/deploy-v2.js --network kairos
```

After deployment, update the corresponding `*_CONTRACT_ADDRESS` in your `.env` file.