# Deployment Guide

## Overview

This guide covers deploying the sage-multi-agent system in different environments.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Local Development](#local-development)
3. [Docker Deployment](#docker-deployment)
4. [Production Deployment](#production-deployment)
5. [Configuration](#configuration)
6. [Security Checklist](#security-checklist)
7. [Monitoring](#monitoring)
8. [Troubleshooting](#troubleshooting)

## Prerequisites

### System Requirements

- **Go 1.24+** for building from source
- **Docker & Docker Compose** for containerized deployment
- **Ethereum RPC node** (Hardhat/Anvil for development, Geth/Infura for production)
- **2GB RAM minimum** per agent service
- **Network ports**: 18080, 8086, 5500, 19082, 19083

### Dependencies

```bash
# Install Go 1.24+
wget https://go.dev/dl/go1.24.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.24.linux-amd64.tar.gz

# Install Docker
curl -fsSL https://get.docker.com | sh

# Install Docker Compose
sudo curl -L "https://github.com/docker/compose/releases/latest/download/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
sudo chmod +x /usr/local/bin/docker-compose
```

## Local Development

### 1. Clone and Build

```bash
git clone https://github.com/sage-x-project/sage-multi-agent
cd sage-multi-agent

# Build all binaries
go build -o bin/root ./cmd/root
go build -o bin/client ./cmd/client
go build -o bin/payment ./cmd/payment
go build -o bin/medical ./cmd/medical
go build -o bin/gateway ./cmd/gateway
```

### 2. Start Ethereum Dev Node

```bash
# Using Hardhat
cd /path/to/hardhat-project
npx hardhat node

# Or using Anvil
anvil --port 8545
```

### 3. Deploy SAGE Registry

```bash
# Deploy registry contract
cd /path/to/sage-registry
npm run deploy:local

# Note the deployed contract address (default: 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512)
```

### 4. Register Agents

```bash
./scripts/00_register_agents.sh \
  --kem --merge \
  --signing-keys ./generated_agent_keys.json \
  --kem-keys ./keys/kem/generated_kem_keys.json \
  --combined-out ./merged_agent_keys.json \
  --agents "payment,planning,medical" \
  --wait-seconds 60 \
  --funding-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  --try-activate
```

### 5. Configure Environment

Create `.env` file:

```bash
# Ethereum
ETH_RPC_URL=http://127.0.0.1:8545
SAGE_REGISTRY_ADDRESS=0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512
SAGE_EXTERNAL_KEY=0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80

# Root Agent
ROOT_JWK_FILE=./keys/root_key.jwk
ROOT_DID=did:sage:ethereum:0x...
ROOT_SAGE_ENABLED=true

# Payment Agent
PAYMENT_JWK_FILE=./keys/payment_key.jwk
PAYMENT_KEM_JWK_FILE=./keys/kem/payment_kem.jwk
PAYMENT_DID=did:sage:ethereum:0x...
PAYMENT_SAGE_ENABLED=true

# Medical Agent
MEDICAL_JWK_FILE=./keys/medical_key.jwk
MEDICAL_KEM_JWK_FILE=./keys/kem/medical_kem.jwk
MEDICAL_DID=did:sage:ethereum:0x...
MEDICAL_SAGE_ENABLED=true

# Planning Agent
PLANNING_JWK_FILE=./keys/planning_key.jwk
PLANNING_DID=did:sage:ethereum:0x...
PLANNING_SAGE_ENABLED=true

# LLM (Optional)
OPENAI_API_KEY=sk-...
OPENAI_BASE_URL=https://api.openai.com/v1
LLM_MODEL=gpt-4o-mini
```

### 6. Start Services

```bash
# Start all services
./scripts/06_start_all.sh --pass

# Or start individually
./bin/root &
./bin/client &
./bin/gateway --mode pass &
./bin/payment &
./bin/medical &
```

### 7. Test

```bash
# Test basic connectivity
curl http://localhost:18080/health

# Send a test request
./scripts/07_send_prompt.sh --sage on --prompt "Test message"
```

## Docker Deployment

### Build Images

```bash
# Build all Docker images
docker-compose build

# Or build individually
docker build -t sage-multi-agent/root:latest -f docker/Dockerfile.root .
docker build -t sage-multi-agent/payment:latest -f docker/Dockerfile.payment .
docker build -t sage-multi-agent/medical:latest -f docker/Dockerfile.medical .
docker build -t sage-multi-agent/client:latest -f docker/Dockerfile.client .
docker build -t sage-multi-agent/gateway:latest -f docker/Dockerfile.gateway .
```

### Docker Compose

Create `docker-compose.yml`:

```yaml
version: '3.8'

services:
  root:
    image: sage-multi-agent/root:latest
    ports:
      - "18080:18080"
    environment:
      - ETH_RPC_URL=${ETH_RPC_URL}
      - SAGE_REGISTRY_ADDRESS=${SAGE_REGISTRY_ADDRESS}
      - ROOT_JWK_FILE=/keys/root_key.jwk
      - ROOT_SAGE_ENABLED=true
    volumes:
      - ./keys:/keys:ro
      - ./logs:/logs
    networks:
      - sage-network
    restart: unless-stopped

  client:
    image: sage-multi-agent/client:latest
    ports:
      - "8086:8086"
    environment:
      - ROOT_URL=http://root:18080
    networks:
      - sage-network
    depends_on:
      - root
    restart: unless-stopped

  payment:
    image: sage-multi-agent/payment:latest
    ports:
      - "19083:19083"
    environment:
      - ETH_RPC_URL=${ETH_RPC_URL}
      - SAGE_REGISTRY_ADDRESS=${SAGE_REGISTRY_ADDRESS}
      - PAYMENT_JWK_FILE=/keys/payment_key.jwk
      - PAYMENT_KEM_JWK_FILE=/keys/kem/payment_kem.jwk
      - PAYMENT_SAGE_ENABLED=true
      - OPENAI_API_KEY=${OPENAI_API_KEY}
    volumes:
      - ./keys:/keys:ro
      - ./logs:/logs
    networks:
      - sage-network
    restart: unless-stopped

  medical:
    image: sage-multi-agent/medical:latest
    ports:
      - "19082:19082"
    environment:
      - ETH_RPC_URL=${ETH_RPC_URL}
      - SAGE_REGISTRY_ADDRESS=${SAGE_REGISTRY_ADDRESS}
      - MEDICAL_JWK_FILE=/keys/medical_key.jwk
      - MEDICAL_KEM_JWK_FILE=/keys/kem/medical_kem.jwk
      - MEDICAL_SAGE_ENABLED=true
      - OPENAI_API_KEY=${OPENAI_API_KEY}
    volumes:
      - ./keys:/keys:ro
      - ./logs:/logs
    networks:
      - sage-network
    restart: unless-stopped

  gateway:
    image: sage-multi-agent/gateway:latest
    ports:
      - "5500:5500"
    environment:
      - MODE=pass  # or 'tamper' for demo
      - PAYMENT_URL=http://payment:19083
      - MEDICAL_URL=http://medical:19082
    networks:
      - sage-network
    depends_on:
      - payment
      - medical
    restart: unless-stopped

networks:
  sage-network:
    driver: bridge
```

### Start with Docker Compose

```bash
# Start all services
docker-compose up -d

# View logs
docker-compose logs -f

# Stop all services
docker-compose down
```

## Production Deployment

### 1. Generate Production Keys

```bash
# Generate new keys (DO NOT use demo keys in production!)
./scripts/generate_production_keys.sh

# Store keys securely (e.g., AWS Secrets Manager, HashiCorp Vault)
```

### 2. Use Production Ethereum Node

```bash
# Update .env with production RPC
ETH_RPC_URL=https://mainnet.infura.io/v3/YOUR_PROJECT_ID
# or
ETH_RPC_URL=https://eth-mainnet.g.alchemy.com/v2/YOUR_API_KEY
```

### 3. Deploy Registry Contract

```bash
# Deploy to mainnet or testnet
cd /path/to/sage-registry
npm run deploy:mainnet

# Update SAGE_REGISTRY_ADDRESS with deployed address
```

### 4. Configure Reverse Proxy (Nginx)

```nginx
# /etc/nginx/sites-available/sage-multi-agent

upstream root_backend {
    server 127.0.0.1:18080;
}

upstream client_backend {
    server 127.0.0.1:8086;
}

server {
    listen 80;
    server_name your-domain.com;

    # Redirect to HTTPS
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name your-domain.com;

    ssl_certificate /etc/letsencrypt/live/your-domain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/your-domain.com/privkey.pem;

    # Client API
    location /api/ {
        proxy_pass http://client_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # Root Agent (internal only, not exposed)
    # location /root/ {
    #     proxy_pass http://root_backend;
    # }
}
```

### 5. Systemd Service Files

Create `/etc/systemd/system/sage-root.service`:

```ini
[Unit]
Description=SAGE Multi-Agent Root Service
After=network.target

[Service]
Type=simple
User=sage
Group=sage
WorkingDirectory=/opt/sage-multi-agent
EnvironmentFile=/opt/sage-multi-agent/.env
ExecStart=/opt/sage-multi-agent/bin/root
Restart=on-failure
RestartSec=10
StandardOutput=append:/var/log/sage/root.log
StandardError=append:/var/log/sage/root-error.log

[Install]
WantedBy=multi-user.target
```

Create similar files for other services:
- `sage-client.service`
- `sage-payment.service`
- `sage-medical.service`

### 6. Enable and Start Services

```bash
sudo systemctl daemon-reload
sudo systemctl enable sage-root sage-client sage-payment sage-medical
sudo systemctl start sage-root sage-client sage-payment sage-medical

# Check status
sudo systemctl status sage-root
```

## Configuration

### Environment Variables by Service

#### Root Agent
- `ROOT_JWK_FILE` (required)
- `ROOT_DID` (auto-derived if not set)
- `ROOT_SAGE_ENABLED` (default: true)
- `ETH_RPC_URL` (required)
- `SAGE_REGISTRY_ADDRESS` (required)

#### Payment/Medical Agents
- `{AGENT}_JWK_FILE` (required)
- `{AGENT}_KEM_JWK_FILE` (required for HPKE)
- `{AGENT}_DID` (auto-derived if not set)
- `{AGENT}_SAGE_ENABLED` (default: true)
- `{AGENT}_LLM_ENDPOINT` (optional)
- `{AGENT}_LLM_API_KEY` (optional)
- `{AGENT}_LLM_MODEL` (default: gpt-4o-mini)

#### Planning Agent
- `PLANNING_JWK_FILE` (required)
- `PLANNING_DID` (auto-derived if not set)
- `PLANNING_EXTERNAL_URL` (optional, falls back to local)

### Key File Locations

**Development:**
```
./keys/
├── root_key.jwk
├── payment_key.jwk
├── medical_key.jwk
├── planning_key.jwk
└── kem/
    ├── payment_kem.jwk
    └── medical_kem.jwk
```

**Production:**
```
/etc/sage/keys/
├── root_key.jwk (mode: 400, owner: sage)
├── payment_key.jwk
├── medical_key.jwk
└── kem/
    ├── payment_kem.jwk
    └── medical_kem.jwk
```

## Security Checklist

### ✅ Pre-Deployment

- [ ] Generate new production keys (do NOT use demo keys)
- [ ] Store private keys in secure vault (AWS Secrets Manager, Vault, etc.)
- [ ] Use HTTPS/TLS for all public endpoints
- [ ] Configure firewall rules (limit exposed ports)
- [ ] Enable DID signature verification (`SAGE_ENABLED=true`)
- [ ] Review and limit CORS settings
- [ ] Set up log rotation and monitoring
- [ ] Configure rate limiting on Client API
- [ ] Use strong Ethereum RPC credentials
- [ ] Enable audit logging

### ✅ Runtime

- [ ] Monitor for failed signature verifications
- [ ] Track HPKE session failures
- [ ] Set up alerting for service failures
- [ ] Regular key rotation schedule
- [ ] Monitor resource usage (CPU, memory, network)
- [ ] Regular security updates (Go, dependencies)

### ✅ Key Management

```bash
# Key file permissions (production)
chmod 400 /etc/sage/keys/*.jwk
chown sage:sage /etc/sage/keys/*.jwk

# Rotate keys every 90 days
# 1. Generate new keys
# 2. Register new DIDs
# 3. Update environment variables
# 4. Restart services with zero downtime
# 5. Deactivate old DIDs after grace period
```

## Monitoring

### Health Checks

```bash
# Root Agent
curl http://localhost:18080/health

# Payment Agent
curl http://localhost:19083/status

# Medical Agent
curl http://localhost:19082/status

# Client API
curl http://localhost:8086/health
```

### Metrics (Prometheus)

Add Prometheus endpoint to each service:

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

http.Handle("/metrics", promhttp.Handler())
```

**Metrics to track:**
- Request count per agent
- Request duration (p50, p95, p99)
- HPKE session count
- Signature verification failures
- Error rate by type

### Logs

```bash
# View real-time logs
tail -f /var/log/sage/root.log

# Search for errors
grep ERROR /var/log/sage/*.log

# Check HPKE failures
grep "HPKE" /var/log/sage/payment.log
```

## Troubleshooting

### Common Issues

#### 1. "HPKE disabled" error

**Cause:** KEM key file missing or HPKE not properly initialized

**Solution:**
```bash
# Check KEM key file exists
ls -l $PAYMENT_KEM_JWK_FILE

# Verify HPKE is enabled in agent initialization
# Payment/Medical agents use Eager pattern - HPKE initializes at startup
# Check startup logs for initialization errors
```

#### 2. Signature verification failed

**Cause:** DID not registered or key mismatch

**Solution:**
```bash
# Verify DID is registered
cast call $SAGE_REGISTRY_ADDRESS "isActive(string)" "did:sage:payment" --rpc-url $ETH_RPC_URL

# Check key matches registered public key
# Compare key fingerprint with on-chain value
```

#### 3. Connection refused to external agents

**Cause:** Service not running or firewall blocking

**Solution:**
```bash
# Check service status
systemctl status sage-payment

# Check port is listening
netstat -tlnp | grep 19083

# Check firewall rules
sudo ufw status
```

#### 4. Out of memory

**Cause:** HPKE session buildup or memory leak

**Solution:**
```bash
# Monitor memory usage
watch -n 1 'ps aux | grep sage'

# Restart service to clear sessions
systemctl restart sage-payment

# Consider implementing session cleanup/expiry
```

### Debug Mode

Enable verbose logging:

```bash
# Set log level
export LOG_LEVEL=debug

# Or use built-in flags
./bin/root --log-level=debug
```

### Emergency Procedures

#### Service Unresponsive

```bash
# 1. Check health
curl http://localhost:18080/health

# 2. Check logs
tail -100 /var/log/sage/root.log

# 3. Graceful restart
systemctl restart sage-root

# 4. Force restart if needed
systemctl kill -s SIGKILL sage-root
systemctl start sage-root
```

#### Security Incident

```bash
# 1. Immediately disable affected service
systemctl stop sage-payment

# 2. Capture logs
cp /var/log/sage/payment.log /var/log/sage/payment-incident-$(date +%s).log

# 3. Rotate compromised keys
./scripts/emergency_key_rotation.sh

# 4. Review audit logs
# 5. Restart with new keys
# 6. Monitor for anomalies
```

## Scaling

### Horizontal Scaling

```yaml
# docker-compose.yml (scaled)
services:
  payment:
    deploy:
      replicas: 3  # Multiple instances
    # Add load balancer (nginx, HAProxy)
```

### Load Balancing

```nginx
upstream payment_backend {
    server payment-1:19083;
    server payment-2:19083;
    server payment-3:19083;
    
    # Sticky sessions for HPKE (session affinity)
    ip_hash;
}
```

**Note:** HPKE sessions are in-memory per instance. Use session affinity or implement distributed session storage.

## Support

- **Documentation**: `/docs/`
- **Issues**: https://github.com/sage-x-project/sage-multi-agent/issues
- **SAGE Protocol**: https://github.com/sage-x-project/sage
