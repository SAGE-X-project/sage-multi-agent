# API Documentation

## Agent Framework API

### Overview

The `internal/agent` framework provides high-level abstractions for building SAGE protocol agents, eliminating the need for direct sage imports and reducing boilerplate code.

## Core Types

### Agent

The main framework type that encapsulates all agent functionality.

```go
type Agent struct {
    // Configuration
    name string
    did  sagedid.AgentDID
    
    // Core components (private)
    keys           *keys.KeySet
    resolver       *agentdid.Resolver
    sessionManager *session.Manager
    middleware     *middleware.DIDAuth
    
    // HPKE components (optional)
    hpkeServer *hpke.Server
    hpkeClient *hpke.Client
    
    // HTTP server
    httpServer *sagehttp.HTTPServer
}
```

### Config

Configuration for creating an agent.

```go
type Config struct {
    Name             string // Agent identifier (e.g., "payment")
    DID              string // Agent's DID
    SigningKeyFile   string // Path to signing key JWK
    KEMKeyFile       string // Path to KEM key JWK (HPKE only)
    RPCEndpoint      string // Ethereum RPC endpoint
    ContractAddress  string // SAGE registry contract
    PrivateKey       string // Operator key for registry
    HPKEEnabled      bool   // Enable HPKE encryption
    RequireSignature bool   // Require HTTP signature verification
}
```

## Constructor Functions

### NewAgent

Creates a new agent with explicit configuration.

**Signature:**
```go
func NewAgent(config Config) (*Agent, error)
```

**Parameters:**
- `config`: Agent configuration struct

**Returns:**
- `*Agent`: Initialized agent
- `error`: Error if initialization fails

**Example:**
```go
agent, err := agent.NewAgent(agent.Config{
    Name:             "payment",
    DID:              "did:sage:payment",
    SigningKeyFile:   "/keys/payment_sign.jwk",
    KEMKeyFile:       "/keys/payment_kem.jwk",
    RPCEndpoint:      "http://127.0.0.1:8545",
    ContractAddress:  "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512",
    PrivateKey:       "0x47e179...",
    HPKEEnabled:      true,
    RequireSignature: true,
})
if err != nil {
    return fmt.Errorf("create agent: %w", err)
}
```

### NewAgentFromEnv

Creates an agent from environment variables (recommended for production).

**Signature:**
```go
func NewAgentFromEnv(name, prefix string, hpkeEnabled, requireSignature bool) (*Agent, error)
```

**Parameters:**
- `name`: Agent name (e.g., "payment")
- `prefix`: Environment variable prefix (e.g., "PAYMENT")
- `hpkeEnabled`: Whether to enable HPKE
- `requireSignature`: Whether to require signature verification

**Environment Variables:**
- `{PREFIX}_DID`: Agent DID
- `{PREFIX}_JWK_FILE`: Signing key path
- `{PREFIX}_KEM_JWK_FILE`: KEM key path (if HPKE enabled)
- `ETH_RPC_URL`: Ethereum RPC endpoint (default: http://127.0.0.1:8545)
- `SAGE_REGISTRY_ADDRESS`: Registry contract (default: 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512)
- `SAGE_EXTERNAL_KEY`: Operator private key

**Returns:**
- `*Agent`: Initialized agent
- `error`: Error if initialization fails

**Example:**
```go
// Environment:
// PAYMENT_DID=did:sage:payment
// PAYMENT_JWK_FILE=/keys/payment_sign.jwk
// PAYMENT_KEM_JWK_FILE=/keys/payment_kem.jwk

agent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, true)
if err != nil {
    log.Fatalf("Failed to create agent: %v", err)
}
```

## Agent Methods

### GetName

Returns the agent's name.

**Signature:**
```go
func (a *Agent) GetName() string
```

### GetDID

Returns the agent's DID.

**Signature:**
```go
func (a *Agent) GetDID() sagedid.AgentDID
```

### GetHTTPServer

Returns the HPKE HTTP server for handling handshakes.

**Signature:**
```go
func (a *Agent) GetHTTPServer() *sagehttp.HTTPServer
```

**Returns:**
- `*sagehttp.HTTPServer`: HTTP server instance, or `nil` if HPKE not enabled

**Usage:**
```go
if agent.GetHTTPServer() != nil {
    // HPKE is available
    agent.GetHTTPServer().MessagesHandler().ServeHTTP(w, r)
}
```

### GetHPKEServer

Returns the underlying HPKE server.

**Signature:**
```go
func (a *Agent) GetHPKEServer() *hpke.Server
```

### GetResolver

Returns the DID resolver.

**Signature:**
```go
func (a *Agent) GetResolver() *agentdid.Resolver
```

### GetSessionManager

Returns the session manager.

**Signature:**
```go
func (a *Agent) GetSessionManager() *session.Manager
```

**Usage:**
```go
// Access underlying sage session manager
sess, ok := agent.GetSessionManager().GetUnderlying().GetByKeyID(kid)
if !ok {
    return fmt.Errorf("session not found: %s", kid)
}
```

### GetKeys

Returns the agent's key set.

**Signature:**
```go
func (a *Agent) GetKeys() *keys.KeySet
```

### CreateHPKEClient

Creates an HPKE client for encrypted outbound communication.

**Signature:**
```go
func (a *Agent) CreateHPKEClient(tr transport.MessageTransport) (*hpke.Client, error)
```

**Parameters:**
- `tr`: Transport for sending messages

**Returns:**
- `*hpke.Client`: HPKE client instance
- `error`: Error if client creation fails

**Example:**
```go
transport := prototx.NewA2ATransport(...)
client, err := agent.CreateHPKEClient(transport)
if err != nil {
    return fmt.Errorf("create HPKE client: %w", err)
}

kid, err := client.Initialize(ctx, contextID, clientDID, serverDID)
```

## Key Management API

### LoadFromJWKFile

Loads a key pair from a JWK file.

**Signature:**
```go
func LoadFromJWKFile(path string) (KeyPair, error)
```

**Parameters:**
- `path`: Path to JWK file

**Returns:**
- `KeyPair`: Loaded key pair
- `error`: Error if loading fails

**Example:**
```go
kp, err := keys.LoadFromJWKFile("/keys/payment_sign.jwk")
if err != nil {
    return fmt.Errorf("load key: %w", err)
}
```

### LoadKeySet

Loads a complete key set (signing + KEM).

**Signature:**
```go
func LoadKeySet(config KeyConfig) (*KeySet, error)
```

**Parameters:**
```go
type KeyConfig struct {
    SigningKeyFile string // Required
    KEMKeyFile     string // Optional
}
```

**Returns:**
- `*KeySet`: Key set with signing and KEM keys
- `error`: Error if loading fails

## DID Resolution API

### NewResolver

Creates a new DID resolver.

**Signature:**
```go
func NewResolver(config Config) (*Resolver, error)
```

**Parameters:**
```go
type Config struct {
    RPCEndpoint     string
    ContractAddress string
    PrivateKey      string
}
```

**Returns:**
- `*Resolver`: DID resolver instance
- `error`: Error if creation fails

**Example:**
```go
resolver, err := did.NewResolver(did.Config{
    RPCEndpoint:     "http://127.0.0.1:8545",
    ContractAddress: "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512",
    PrivateKey:      "0x47e179...",
})
```

### NewResolverFromEnv

Creates a resolver from environment variables.

**Signature:**
```go
func NewResolverFromEnv() (*Resolver, error)
```

**Environment Variables:**
- `ETH_RPC_URL`: Ethereum RPC endpoint (default: http://127.0.0.1:8545)
- `SAGE_REGISTRY_ADDRESS`: Registry contract (default: 0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512)
- `SAGE_EXTERNAL_KEY`: Operator private key

## HPKE Server API

### NewServer

Creates a new HPKE server.

**Signature:**
```go
func NewServer(config ServerConfig) (*Server, error)
```

**Parameters:**
```go
type ServerConfig struct {
    SigningKey     keys.KeyPair
    KEMKey         keys.KeyPair
    DID            sagedid.AgentDID
    Resolver       *agentdid.Resolver
    SessionManager *session.Manager
}
```

**Returns:**
- `*Server`: HPKE server instance
- `error`: Error if creation fails

**Example:**
```go
hpkeServer, err := hpke.NewServer(hpke.ServerConfig{
    SigningKey:     keySet.SigningKey,
    KEMKey:         keySet.KEMKey,
    DID:            agentDID,
    Resolver:       resolver,
    SessionManager: sessionMgr,
})
```

## Session Management API

### NewManager

Creates a new session manager.

**Signature:**
```go
func NewManager() *Manager
```

**Returns:**
- `*Manager`: Session manager instance

### GetUnderlying

Returns the underlying sage session manager.

**Signature:**
```go
func (m *Manager) GetUnderlying() *sagesession.Manager
```

**Usage:**
```go
mgr := session.NewManager()
sess, ok := mgr.GetUnderlying().GetByKeyID(kid)
```

## Middleware API

### NewDIDAuth

Creates DID authentication middleware.

**Signature:**
```go
func NewDIDAuth(config Config) (*DIDAuth, error)
```

**Parameters:**
```go
type Config struct {
    Resolver *agentdid.Resolver
    Optional bool  // Allow requests without signatures
}
```

**Returns:**
- `*DIDAuth`: DID auth middleware
- `error`: Error if creation fails

**Example:**
```go
auth, err := middleware.NewDIDAuth(middleware.Config{
    Resolver: resolver,
    Optional: false,  // Require signatures
})
if err != nil {
    return fmt.Errorf("create middleware: %w", err)
}

// Use with HTTP handler
handler := auth.GetUnderlying().Wrap(protectedHandler)
```

## Usage Patterns

### Eager HPKE Pattern (Payment/Medical)

```go
func NewPaymentAgent(requireSignature bool) (*PaymentAgent, error) {
    // Create framework agent with HPKE enabled
    fwAgent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, requireSignature)
    if err != nil {
        return nil, fmt.Errorf("create agent: %w", err)
    }
    
    pa := &PaymentAgent{
        agent: fwAgent,
        // ... other fields
    }
    
    // HPKE is already initialized, no lazy init needed
    return pa, nil
}

func (pa *PaymentAgent) handleHPKE(w http.ResponseWriter, r *http.Request) {
    if pa.agent == nil || pa.agent.GetHTTPServer() == nil {
        http.Error(w, "HPKE not available", http.StatusBadRequest)
        return
    }
    
    // Use the framework's HTTP server
    pa.agent.GetHTTPServer().MessagesHandler().ServeHTTP(w, r)
}
```

### Framework Keys Pattern (Planning)

```go
func (pa *PlanningAgent) initSigning() error {
    jwk := os.Getenv("PLANNING_JWK_FILE")
    
    // Use framework key loading
    kp, err := keys.LoadFromJWKFile(jwk)
    if err != nil {
        return fmt.Errorf("load key: %w", err)
    }
    
    pa.myKey = kp
    pa.a2a = a2aclient.NewA2AClient(pa.myDID, pa.myKey, http.DefaultClient)
    return nil
}
```

### Framework Helpers Pattern (Root)

```go
func (r *RootAgent) initSigning() error {
    // Use framework key loading
    kp, err := keys.LoadFromJWKFile(os.Getenv("ROOT_JWK_FILE"))
    if err != nil {
        return err
    }
    
    r.myKey = kp
    r.myDID = deriveDID(kp)
    r.a2a = a2aclient.NewA2AClient(r.myDID, r.myKey, r.httpClient)
    return nil
}

func (r *RootAgent) ensureResolver() error {
    if r.resolver != nil {
        return nil
    }
    
    // Use framework resolver creation
    resolver, err := did.NewResolver(did.Config{
        RPCEndpoint:     os.Getenv("ETH_RPC_URL"),
        ContractAddress: os.Getenv("SAGE_REGISTRY_ADDRESS"),
        PrivateKey:      os.Getenv("SAGE_EXTERNAL_KEY"),
    })
    if err != nil {
        return err
    }
    
    r.resolver = resolver
    return nil
}
```

## Error Handling

All framework functions return descriptive errors with context:

```go
agent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, true)
if err != nil {
    // Error includes context about which step failed
    // e.g., "load keys: read PAYMENT_JWK_FILE: no such file"
    log.Fatalf("Failed to create agent: %v", err)
}
```

## Migration from Direct sage Imports

### Before (Direct sage)

```go
import (
    sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
    "github.com/sage-x-project/sage/pkg/agent/crypto/formats"
    sagedid "github.com/sage-x-project/sage/pkg/agent/did"
    dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

// Manual key loading
raw, _ := os.ReadFile(jwkPath)
imp := formats.NewJWKImporter()
kp, _ := imp.Import(raw, sagecrypto.KeyFormatJWK)

// Manual resolver creation
cfg := &sagedid.RegistryConfig{...}
resolver, _ := dideth.NewEthereumClient(cfg)
```

### After (Framework)

```go
import (
    "github.com/sage-x-project/sage-multi-agent/internal/agent/keys"
    "github.com/sage-x-project/sage-multi-agent/internal/agent/did"
)

// Framework key loading
kp, err := keys.LoadFromJWKFile(jwkPath)

// Framework resolver creation
resolver, err := did.NewResolver(did.Config{...})
```

**Benefits:**
- ✅ 70% less code
- ✅ Better error messages
- ✅ Consistent API across all agents
- ✅ Easier to test and mock
