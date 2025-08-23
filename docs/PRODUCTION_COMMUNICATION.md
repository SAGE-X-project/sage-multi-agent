# Production-Grade Communication System

## Overview

This document describes the enhanced production-grade communication system between the SAGE frontend and multi-agent backend, implementing all requirements from the BACKEND_INTEGRATION.md specification.

## Architecture Components

### 1. Enhanced Client Server (`client/enhanced_main.go`)

Production-grade HTTP server with:
- **SAGE Protocol Integration**: Full RFC-9421 message signature support
- **Request/Response Caching**: 5-minute TTL cache for non-SAGE requests
- **Comprehensive Metrics**: Request counts, response times, success rates
- **Health Monitoring**: Real-time health checks for all services
- **Error Recovery**: Graceful error handling with retry logic
- **CORS Support**: Full cross-origin resource sharing
- **Graceful Shutdown**: Proper cleanup on termination

### 2. Enhanced WebSocket Server (`websocket/enhanced_server.go`)

Real-time communication system with:
- **Message Types**: Structured messages (log, error, status, heartbeat, connection)
- **Log Buffering**: 100-message buffer for new clients
- **Client Management**: Track connected clients with metadata
- **Heartbeat System**: 30-second heartbeat for connection monitoring
- **Statistics API**: Real-time server statistics
- **CORS Support**: WebSocket CORS headers
- **Graceful Shutdown**: Clean disconnection handling

### 3. Message Types (`types/messages.go`)

Comprehensive type definitions:
- **PromptRequest/Response**: Structured request/response with metadata
- **AgentLog**: Detailed logging with levels and types
- **SAGEVerificationResult**: Cryptographic verification status
- **WebSocketMessage**: Typed WebSocket communication
- **HealthCheckResponse**: Service health monitoring
- **ErrorDetail**: Structured error information

## API Endpoints

### Client Server (Port 8086)

#### POST `/send/prompt`
Main endpoint for processing prompts.

**Request**:
```json
{
  "prompt": "Book a hotel in Tokyo",
  "sageEnabled": true,
  "scenario": "accommodation",
  "metadata": {
    "userId": "user123",
    "sessionId": "session456"
  }
}
```

**Headers**:
```
X-SAGE-Enabled: true
X-Scenario: accommodation
```

**Response**:
```json
{
  "response": "I found 3 hotels in Tokyo...",
  "sageVerification": {
    "verified": true,
    "agentDid": "did:sage:ethereum:root_agent_001",
    "signatureValid": true,
    "timestamp": 1234567890
  },
  "metadata": {
    "requestId": "uuid-here",
    "processingTime": 1234.56,
    "timestamp": "2024-01-15T10:30:00Z"
  },
  "logs": [...]
}
```

#### GET `/health`
Health check endpoint.

**Response**:
```json
{
  "status": "healthy",
  "timestamp": "2024-01-15T10:30:00Z",
  "version": "1.0.0",
  "services": {
    "root-agent": {
      "name": "Root Agent",
      "status": "up",
      "latency": 12.34
    },
    "websocket": {
      "name": "WebSocket Server",
      "status": "up"
    }
  }
}
```

#### GET `/metrics`
Metrics endpoint for monitoring.

**Response**:
```json
{
  "http_metrics": {
    "total_requests": 1000,
    "successful_requests": 950,
    "failed_requests": 50,
    "success_rate": 95.0,
    "avg_response_time": 234.56,
    "uptime": 3600
  },
  "websocket_stats": {
    "clients": 5,
    "buffer_size": 45,
    "uptime": 3600
  }
}
```

### WebSocket Server (Port 8085)

#### WS `/ws`
WebSocket endpoint for real-time logs.

**Message Format**:
```json
{
  "type": "log",
  "payload": {
    "type": "routing",
    "from": "root-agent",
    "to": "planning-agent",
    "content": "Routing request to planning agent",
    "timestamp": "2024-01-15T10:30:00Z",
    "messageId": "msg-123",
    "level": "info"
  },
  "timestamp": "2024-01-15T10:30:00Z",
  "messageId": "ws-msg-456"
}
```

#### GET `/health`
WebSocket server health check.

#### GET `/stats`
WebSocket server statistics.

## SAGE Protocol Implementation

### When SAGE is Enabled

1. **Request Signing**: All requests are signed using Ed25519 keys
2. **DID Resolution**: Agent DIDs are resolved from blockchain
3. **Signature Verification**: All inter-agent communications are verified
4. **Audit Trail**: Complete cryptographic audit trail maintained

### When SAGE is Disabled

1. **No Signing**: Messages sent without signatures
2. **Demo Tampering**: Optional message tampering for demonstration
3. **Warning Logs**: Clear warnings about security risks
4. **Caching Enabled**: Responses cached for performance

## Running the System

### 1. Start Backend Services

```bash
# Start WebSocket server and client server
go run client/enhanced_main.go \
  --port 8086 \
  --root-url http://localhost:8080 \
  --ws-port 8085

# Start root agent with enhanced WebSocket
go run root/main.go --ws-port 8085

# Start sub-agents
go run ordering/main.go --port 8083
go run planning/main.go --port 8084
```

### 2. Environment Variables

Create `.env` file:
```env
# API Configuration
ROOT_AGENT_URL=http://localhost:8080
CLIENT_PORT=8086
WS_PORT=8085

# SAGE Configuration
SAGE_ENABLED=true
ETH_CONTRACT_ADDRESS=0x...
ETH_RPC_ENDPOINT=https://...

# Agent Ports
ORDERING_AGENT_PORT=8083
PLANNING_AGENT_PORT=8084
```

### 3. Frontend Integration

The frontend should:
1. Connect to WebSocket at `ws://localhost:8085/ws`
2. Send POST requests to `http://localhost:8086/send/prompt`
3. Handle structured responses with verification data
4. Display real-time logs from WebSocket

## Production Features

### 1. Reliability
- **Retry Logic**: Automatic retry with exponential backoff
- **Circuit Breaker**: Prevent cascading failures
- **Health Monitoring**: Continuous service health checks
- **Graceful Degradation**: Fallback to cached responses

### 2. Performance
- **Response Caching**: 5-minute TTL for repeated requests
- **Connection Pooling**: Reuse HTTP connections
- **Parallel Processing**: Concurrent request handling
- **Resource Limits**: Configured timeouts and buffer sizes

### 3. Security
- **CORS Configuration**: Proper cross-origin headers
- **Request Validation**: Input sanitization and validation
- **Rate Limiting**: (Ready for implementation)
- **SAGE Protocol**: Cryptographic message verification

### 4. Observability
- **Structured Logging**: Consistent log format with levels
- **Metrics Collection**: Request metrics and performance data
- **Health Endpoints**: Service health monitoring
- **Real-time Logs**: WebSocket-based log streaming

### 5. Maintainability
- **Clean Architecture**: Separation of concerns
- **Configuration Management**: Environment-based config
- **Error Handling**: Comprehensive error types
- **Documentation**: Inline documentation and guides

## Testing the System

### 1. Test SAGE Enabled Mode
```bash
curl -X POST http://localhost:8086/send/prompt \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -d '{"prompt": "Book a hotel in Tokyo"}'
```

### 2. Test SAGE Disabled Mode
```bash
curl -X POST http://localhost:8086/send/prompt \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: false" \
  -H "X-Scenario: accommodation" \
  -d '{"prompt": "Book a hotel in Tokyo"}'
```

### 3. Test Health Check
```bash
curl http://localhost:8086/health
```

### 4. Test WebSocket Connection
```javascript
const ws = new WebSocket('ws://localhost:8085/ws');
ws.onmessage = (event) => {
  const message = JSON.parse(event.data);
  console.log('Received:', message);
};
```

## Monitoring and Alerts

### Key Metrics to Monitor
1. **Request Rate**: Requests per second
2. **Error Rate**: Failed requests percentage
3. **Response Time**: P50, P95, P99 latencies
4. **WebSocket Connections**: Active connection count
5. **Cache Hit Rate**: Percentage of cached responses

### Alert Thresholds
- Error rate > 5%
- Response time P95 > 1000ms
- WebSocket connections drop > 50%
- Service health check failures

## Future Enhancements

1. **Rate Limiting**: Implement per-client rate limits
2. **Authentication**: Add JWT-based authentication
3. **Message Queue**: Add message queue for reliability
4. **Distributed Tracing**: Implement OpenTelemetry
5. **Load Balancing**: Add multiple backend instances
6. **Database Integration**: Persist logs and metrics
7. **GraphQL API**: Alternative API interface
8. **gRPC Support**: Binary protocol option

## Troubleshooting

### Common Issues

1. **WebSocket Connection Failed**
   - Check if port 8085 is available
   - Verify CORS settings
   - Check firewall rules

2. **SAGE Verification Failed**
   - Verify agent registration on blockchain
   - Check key file permissions
   - Validate DID configuration

3. **High Response Times**
   - Check network latency
   - Monitor agent processing times
   - Review cache configuration

4. **Memory Usage**
   - Adjust buffer sizes
   - Implement log rotation
   - Monitor goroutine leaks

## Conclusion

This production-grade communication system provides a robust, secure, and scalable foundation for the SAGE multi-agent platform. It implements all requirements from the BACKEND_INTEGRATION.md specification with additional production features for reliability, performance, and maintainability.