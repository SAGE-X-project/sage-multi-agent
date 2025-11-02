# sage-a2a-go Enhancement Requirements

**From:** sage-multi-agent team
**To:** sage-a2a-go maintainers
**Date:** 2024-11-02
**Priority:** Critical for v0.2.0 refactoring
**Timeline:** 5 weeks

---

## Executive Summary

The `sage-multi-agent` project currently depends on **both** `sage` and `sage-a2a-go` packages. To achieve proper architectural separation, we need to migrate all functionality to `sage-a2a-go`, eliminating direct `sage` dependencies.

**Current State:**
- 23 files import from `sage` packages
- 8 files import from `sage-a2a-go` packages
- Mixed usage creates maintenance burden and architectural confusion

**Goal State:**
- 0 files import from `sage` directly
- All agent functionality uses `sage-a2a-go` APIs
- Clear separation: sage-a2a-go (agent SDK) → sage (low-level protocol)

---

## Critical Gaps (MUST HAVE for Migration)

### 1. HPKE Server Implementation

**Status:** ❌ Missing in sage-a2a-go
**Blocks:** Payment agent, Medical agent migration
**Current workaround:** Direct `sage/pkg/agent/hpke` usage

**Required API:**

```go
// Package: github.com/sage-x-project/sage-a2a-go/pkg/hpke

type Server struct {
    signingKey KeyPair
    kemKey     KeyPair
    sessions   *SessionManager
    resolver   DIDResolver
    opts       ServerOptions
}

type ServerOptions struct {
    KEM               KeyPair     // Optional: separate KEM key
    AllowedDIDs       []string    // Optional: whitelist
    SessionTimeout    time.Duration
    MaxSessions       int
}

// NewServer creates an HPKE server instance
func NewServer(
    signingKey KeyPair,
    sessions *SessionManager,
    serverDID string,
    resolver DIDResolver,
    opts *ServerOptions,
) (*Server, error)

// HandleMessage processes HPKE handshake or data messages
// Returns: response, session created?, error
func (s *Server) HandleMessage(
    ctx context.Context,
    msg *SecureMessage,
) (*Response, bool, error)

// MessagesHandler returns an HTTP handler for HPKE endpoint
func (s *Server) MessagesHandler() http.Handler
```

**Usage Pattern (Current in sage-multi-agent):**

```go
// payment/agent.go:300-310
signKP := loadSigningKey()
kemKP := loadKEMKey()
resolver := buildResolver()
mgr := session.NewManager()

hsrv := hpke.NewServer(signKP, mgr, serverDID, resolver, &hpke.ServerOpts{
    KEM: kemKP,
})

// HTTP handler for handshake
handshakeHandler := hsrv.MessagesHandler()

// Data mode processing
sess, _ := mgr.GetByKeyID(kid)
plaintext, _ := sess.Decrypt(ciphertext)
```

**Why Needed:**
- Payment and Medical agents run as external services
- They receive HPKE-encrypted requests from Root agent
- Handshake establishes shared secret, data mode uses session encryption
- Without this, agents cannot support end-to-end encryption

---

### 2. Session Manager

**Status:** ❌ Missing in sage-a2a-go
**Blocks:** All HPKE functionality
**Current workaround:** Direct `sage/pkg/agent/session` usage

**Required API:**

```go
// Package: github.com/sage-x-project/sage-a2a-go/pkg/session

type Manager struct {
    sessions   sync.Map
    timeout    time.Duration
    maxSessions int
}

type Session struct {
    ID          string
    ClientDID   string
    ServerDID   string
    SharedSecret []byte
    CreatedAt   time.Time
    LastUsedAt  time.Time
}

func NewManager() *Manager

// CreateSession stores a new HPKE session
func (m *Manager) CreateSession(
    kid string,
    clientDID string,
    serverDID string,
    sharedSecret []byte,
) (*Session, error)

// GetByKeyID retrieves session by KID
func (m *Manager) GetByKeyID(kid string) (*Session, bool)

// Encrypt encrypts plaintext using session
func (s *Session) Encrypt(plaintext []byte) (ciphertext []byte, err error)

// Decrypt decrypts ciphertext using session
func (s *Session) Decrypt(ciphertext []byte) (plaintext []byte, err error)

// DeleteSession removes session
func (m *Manager) DeleteSession(kid string) error

// Cleanup removes expired sessions
func (m *Manager) Cleanup()
```

**Usage Pattern:**

```go
// Root agent (client side):
hpkeMgr := session.NewManager()
sess, _ := hpkeMgr.CreateSession(kid, clientDID, serverDID, secret)
ciphertext, _ := sess.Encrypt(plaintext)

// Payment agent (server side):
hpkeMgr := session.NewManager()
sess, found := hpkeMgr.GetByKeyID(kid)
plaintext, _ := sess.Decrypt(ciphertext)
```

**Why Needed:**
- HPKE data mode requires stateful session storage
- Sessions map KID (key identifier) to shared secrets
- Both client and server need session management
- Current implementation in `sage` not accessible via clean API

---

### 3. HTTP Transport Adapter

**Status:** ❌ Missing in sage-a2a-go
**Blocks:** HPKE handshake HTTP endpoint
**Current workaround:** Direct `sage/pkg/agent/transport/http` usage

**Required API:**

```go
// Package: github.com/sage-x-project/sage-a2a-go/pkg/transport

type HPKEHandler func(ctx context.Context, msg *SecureMessage) (*Response, error)

type HTTPServer struct {
    handler HPKEHandler
}

// NewHTTPServer wraps HPKE handler as HTTP endpoint
func NewHTTPServer(handler HPKEHandler) *HTTPServer

// MessagesHandler returns HTTP handler for /messages endpoint
func (s *HTTPServer) MessagesHandler() http.Handler
```

**Usage Pattern:**

```go
// payment/agent.go:308-310
hsrv := hpke.NewServer(...)
httpAdapter := sagehttp.NewHTTPServer(hsrv.HandleMessage)
handler := httpAdapter.MessagesHandler()

mux.Handle("/messages", handler)
```

**Why Needed:**
- HPKE handshake expects specific HTTP endpoint format
- Converts HTTP request → SecureMessage → HPKE processing → HTTP response
- Standardizes error handling and content-type negotiation

---

## High Priority Enhancements

### 4. Unified Key Management

**Status:** ⚠️ Partial in sage-a2a-go (signing only)
**Impact:** Code duplication across 7 files

**Required API:**

```go
// Package: github.com/sage-x-project/sage-a2a-go/pkg/crypto

// KeyPair unifies ECDSA and X25519 keys
type KeyPair interface {
    PublicKey() crypto.PublicKey
    PrivateKey() crypto.PrivateKey
    ID() string // DID or key ID
    Type() KeyType // ECDSA, X25519
    Sign(message []byte) ([]byte, error)
}

type KeyType string

const (
    KeyTypeECDSA   KeyType = "ecdsa"
    KeyTypeX25519  KeyType = "x25519"
)

type KeyFormat string

const (
    KeyFormatJWK KeyFormat = "jwk"
    KeyFormatPEM KeyFormat = "pem"
)

// Importer loads keys from various formats
type Importer interface {
    Import(data []byte, format KeyFormat) (KeyPair, error)
}

// Exporter serializes keys to formats
type Exporter interface {
    Export(kp KeyPair, format KeyFormat) ([]byte, error)
}

// Generator creates new key pairs
type Generator interface {
    GenerateSecp256k1() (KeyPair, error)
    GenerateX25519() (KeyPair, error)
}

func NewJWKImporter() Importer
func NewJWKExporter() Exporter
func NewKeyGenerator() Generator
```

**Current Duplication:**

```go
// Duplicated in 7 files:
importer := formats.NewJWKImporter()
kp, err := importer.Import(raw, sagecrypto.KeyFormatJWK)
```

**Target:**

```go
importer := crypto.NewJWKImporter()
kp, err := importer.Import(raw, crypto.KeyFormatJWK)
```

**Why Needed:**
- Every agent loads signing keys from JWK files
- Payment/Medical agents load dual keys (signing + KEM)
- Tools generate keys and export to JWK
- Current `sage` crypto is low-level, need agent-friendly wrapper

---

### 5. Enhanced DID Resolution

**Status:** ✅ Basic resolver exists, ⚠️ Missing AgentCard v4
**Impact:** Registration tools cannot migrate

**Required API:**

```go
// Package: github.com/sage-x-project/sage-a2a-go/pkg/did

// Extend existing DIDResolver
type Resolver interface {
    ResolvePublicKey(did string) (crypto.PublicKey, error)
    ResolveKEMKey(did string) (crypto.PublicKey, error)
    ResolveAgentCard(did string) (*AgentCard, error) // NEW
}

// AgentCard represents on-chain agent metadata
type AgentCard struct {
    DID          string
    Name         string
    Description  string
    Endpoint     string
    Capabilities []string
    Keys         []PublicKeyInfo
    Status       AgentStatus
    RegisteredAt time.Time
    ActivatedAt  time.Time
}

type PublicKeyInfo struct {
    ID       string
    Type     KeyType
    PublicKey []byte
}

type AgentStatus string

const (
    StatusCommitted AgentStatus = "committed"
    StatusRevealed  AgentStatus = "revealed"
    StatusActive    AgentStatus = "active"
)

// NewEthereumResolver creates resolver for Ethereum registry
func NewEthereumResolver(config *RegistryConfig) (Resolver, error)

type RegistryConfig struct {
    RPCEndpoint         string
    ContractAddress     string
    PrivateKey          string // For write operations
    GasPrice            *big.Int
    MaxRetries          int
    ConfirmationBlocks  uint64
}
```

**Why Needed:**
- DID resolution currently requires direct `sage/pkg/agent/did/ethereum` usage
- Need full AgentCard support (v4 with dual keys)
- Registration tools need write operations (commit, reveal, activate)

---

### 6. Registry Client (Agent Registration)

**Status:** ❌ Missing in sage-a2a-go
**Impact:** Cannot migrate registration tools

**Required API:**

```go
// Package: github.com/sage-x-project/sage-a2a-go/pkg/registry

type Client interface {
    // Commit phase: hash commitment on-chain
    CommitRegistration(ctx context.Context, params *RegistrationParams) (*Status, error)

    // Reveal phase: publish actual data
    RegisterAgent(ctx context.Context, status *Status) (*Status, error)

    // Activate phase: make agent discoverable
    ActivateAgent(ctx context.Context, status *Status) error

    // Query existing agent
    GetAgentByDID(ctx context.Context, did string) (*AgentCard, error)

    // Update activation delay
    SetActivationDelay(ctx context.Context, delay time.Duration) error
}

type RegistrationParams struct {
    DID          string
    Name         string
    Description  string
    Endpoint     string
    Capabilities []string
    Keys         []KeyInfo
    Signatures   [][]byte // Signatures for each key
}

type KeyInfo struct {
    Type      KeyType
    PublicKey []byte
}

type Status struct {
    DID           string
    TxHash        string
    BlockNumber   uint64
    Status        AgentStatus
    CommitHash    [32]byte
    ActivationTime time.Time
}

func NewRegistryClient(config *RegistryConfig) (Client, error)
```

**Usage Pattern:**

```go
// tools/registration/register_agents.go
client := registry.NewRegistryClient(cfg)

// Phase 1: Commit
status, _ := client.CommitRegistration(ctx, &RegistrationParams{
    DID:  "did:sage:ethereum:0x1234...",
    Name: "payment",
    Keys: []KeyInfo{{Type: KeyTypeECDSA, PublicKey: pubBytes}},
    Signatures: [][]byte{sig},
})

// Wait for confirmation...
time.Sleep(60 * time.Second)

// Phase 2: Reveal
status, _ = client.RegisterAgent(ctx, status)

// Phase 3: Activate
err = client.ActivateAgent(ctx, status)
```

**Why Needed:**
- Agent registration uses commit-reveal-activate pattern
- Prevents front-running attacks on DID registration
- Tools currently use `sage/pkg/agent/did/ethereum.NewAgentCardClient`
- Need clean, agent-focused API

---

## Medium Priority (Nice to Have)

### 7. SecureMessage & Transport

**Status:** ⚠️ Defined in sage, needs sage-a2a-go version
**Impact:** protocol/ package duplication

**Required API:**

```go
// Package: github.com/sage-x-project/sage-a2a-go/pkg/protocol

type SecureMessage struct {
    ID        string
    ContextID string
    TaskID    string
    Payload   []byte
    DID       string
    Metadata  map[string]string
    Role      string
}

type Response struct {
    ID      string
    Status  int
    Data    []byte
    Headers map[string]string
}

type Transport interface {
    Send(ctx context.Context, msg *SecureMessage) (*Response, error)
}

// A2ATransport wraps A2A client with SecureMessage protocol
type A2ATransport struct {
    client      A2AClient
    baseURL     string
    hpkeMode    bool
}

func NewA2ATransport(client A2AClient, baseURL string, hpkeMode bool) *A2ATransport
```

**Why Needed:**
- Standardize message format across agents
- Simplify HPKE vs non-HPKE switching
- Currently defined in `sage/pkg/agent/transport`

---

## Implementation Priority & Timeline

### Week 1: HPKE Foundation (CRITICAL)
- [ ] Implement `hpke.Server`
- [ ] Implement `session.Manager`
- [ ] Implement `transport.HTTPServer`
- [ ] Add comprehensive tests
- [ ] Document HPKE flow

**Deliverable:** Payment agent can run with sage-a2a-go only

### Week 2: Key Management (HIGH)
- [ ] Unify `crypto.KeyPair` interface
- [ ] Implement JWK importer/exporter
- [ ] Add key generator
- [ ] Support both ECDSA and X25519

**Deliverable:** All agents load keys via sage-a2a-go

### Week 3: DID & Registry (HIGH)
- [ ] Enhance DID resolver with AgentCard v4
- [ ] Implement registry client (commit-reveal-activate)
- [ ] Add Ethereum write operations
- [ ] Support dual-key registration

**Deliverable:** Registration tools migrate to sage-a2a-go

### Week 4: Testing & Migration (HIGH)
- [ ] Integration tests for all components
- [ ] Performance benchmarks
- [ ] Migration guide for users
- [ ] Update sage-multi-agent to use new APIs

**Deliverable:** sage-multi-agent v0.2.0 with zero sage imports

### Week 5: Polish & Release (MEDIUM)
- [ ] Documentation updates
- [ ] Example code
- [ ] Security audit
- [ ] sage-a2a-go release (version TBD)

**Deliverable:** Production-ready sage-a2a-go release

---

## Interface Design Principles

### 1. Agent-First API
- Hide low-level cryptographic details
- Provide sensible defaults
- Easy to use correctly, hard to use incorrectly

**Good:**
```go
server := hpke.NewServer(signingKey, kemKey, sessions, resolver)
```

**Bad:**
```go
server := hpke.NewServer(
    signingKey,
    &hpke.Opts{
        KEM: kemKey,
        KDF: hpke.HKDF_SHA256,
        AEAD: hpke.AES_128_GCM,
        Suite: 0x0010,
    },
)
```

### 2. Minimize Dependencies
- Don't force users to import multiple packages for one operation
- Provide all-in-one constructors

**Good:**
```go
import "github.com/sage-x-project/sage-a2a-go/pkg/agent"

agent := agent.NewSecureAgent(config)
```

**Bad:**
```go
import (
    "github.com/sage-x-project/sage-a2a-go/pkg/client"
    "github.com/sage-x-project/sage-a2a-go/pkg/server"
    "github.com/sage-x-project/sage-a2a-go/pkg/hpke"
    "github.com/sage-x-project/sage-a2a-go/pkg/did"
    "github.com/sage-x-project/sage-a2a-go/pkg/crypto"
)
```

### 3. Backward Compatibility
- Support existing JWK key formats
- Maintain RFC 9421 compliance
- Don't break existing agents

### 4. Clear Error Messages
- Include context (what operation, what went wrong, how to fix)
- Use typed errors for programmatic handling

```go
type HPKEError struct {
    Op   string // "handshake", "encrypt", "decrypt"
    DID  string
    Err  error
}
```

---

## Testing Requirements

### Unit Tests
- [ ] HPKE server handshake
- [ ] Session manager operations
- [ ] Key import/export
- [ ] DID resolution
- [ ] Registry operations

### Integration Tests
- [ ] Full HPKE flow (handshake + data mode)
- [ ] A2A signing + HPKE encryption
- [ ] Agent registration workflow
- [ ] Multi-agent communication

### Performance Benchmarks
- [ ] HPKE encrypt/decrypt throughput
- [ ] Session manager lookup latency
- [ ] DID resolution caching
- [ ] A2A signature overhead

### Migration Tests
- [ ] Compare old vs new behavior
- [ ] Ensure no breaking changes
- [ ] Validate key format compatibility

---

## Documentation Needs

### API Documentation
- [ ] Godoc for all public APIs
- [ ] Usage examples for each package
- [ ] Migration guide from sage

### Architecture Documentation
- [ ] HPKE flow diagram
- [ ] Session lifecycle
- [ ] Registration workflow
- [ ] Multi-agent communication patterns

### Developer Guides
- [ ] Quick start: Build an agent
- [ ] HPKE integration guide
- [ ] Testing best practices
- [ ] Troubleshooting common issues

---

## Success Criteria

**For sage-a2a-go:**
- ✅ All critical gaps implemented
- ✅ 80%+ test coverage
- ✅ Performance benchmarks pass
- ✅ Documentation complete
- ✅ Security review passed

**For sage-multi-agent:**
- ✅ Zero direct `sage` package imports
- ✅ All agents use sage-a2a-go
- ✅ All tests pass
- ✅ No performance regression
- ✅ Backward compatible (can still read old keys/configs)

**Timeline:**
- Week 5: sage-a2a-go release
- Week 6: sage-multi-agent v0.2.0 release

---

## Questions & Clarifications

### Q1: Should HPKE be in separate package or integrated with A2A?
**A:** Separate package (`pkg/hpke`) for modularity. Some agents may want A2A signing without HPKE.

### Q2: How to handle sage version upgrades?
**A:** sage-a2a-go wraps sage APIs. When sage upgrades, sage-a2a-go absorbs breaking changes, keeps stable API for users.

### Q3: Do we need both client and server for HPKE?
**A:** Yes. Root agent needs HPKE client (encrypt), Payment/Medical need HPKE server (decrypt).

### Q4: Key format compatibility?
**A:** Must support existing JWK files. Can add new formats (PEM, etc.) later.

### Q5: Session persistence?
**A:** In-memory for v1. Persistence (Redis, etc.) can be added later via interface.

---

## Contact Information

**sage-multi-agent team:**
- Repository: https://github.com/SAGE-X-project/sage-multi-agent
- Issues: Use GitHub issues with `[sage-a2a-go]` prefix

**sage-a2a-go team:**
- Repository: https://github.com/SAGE-X-project/sage-a2a-go
- Issues: Tag with `enhancement` label

**Next Steps:**
1. sage-a2a-go team reviews this document
2. Schedule kickoff meeting for API design
3. Create GitHub issues for each component
4. Begin Week 1 implementation

---

**Document Version:** 1.0
**Last Updated:** 2024-11-02
**Status:** Awaiting sage-a2a-go Team Review
