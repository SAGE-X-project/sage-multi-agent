# SAGE & A2A-GO Package Usage Report
**Project:** sage-multi-agent
**Date:** November 2, 2025
**Purpose:** Complete survey of all sage and a2a-go package dependencies for refactoring to sage-a2a-go

---

## Executive Summary

The sage-multi-agent project has **23 Go files** importing packages from either `github.com/sage-x-project/sage` or `github.com/sage-x-project/sage-a2a-go`. The usage falls into these major categories:

1. **Crypto/Key Management** (7 files) - Key generation, import/export, signing
2. **DID Resolution** (7 files) - DID registry interaction, agent card management
3. **HPKE Encryption** (3 files) - End-to-end encryption with handshake
4. **A2A Signing/Verification** (6 files) - RFC 9421 HTTP message signatures
5. **Transport/Protocol** (4 files) - SecureMessage handling, HTTP transport
6. **Registry/On-chain** (4 files) - Ethereum registry operations

Most functionality can be consolidated into sage-a2a-go with proper interface design. Critical gaps exist in HPKE server implementation and session management.

---

## File-by-File Breakdown

### 1. `/agents/root/agent.go` (Lines: 1756)
**Primary Agent:** Root routing agent

#### Imports:
```go
Line 28: a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
Line 30: "github.com/sage-x-project/sage/pkg/agent/transport"
Line 34: sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
Line 35: "github.com/sage-x-project/sage/pkg/agent/crypto/formats"
Line 36: sagedid "github.com/sage-x-project/sage/pkg/agent/did"
Line 37: dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
Line 38: "github.com/sage-x-project/sage/pkg/agent/hpke"
Line 39: "github.com/sage-x-project/sage/pkg/agent/session"
```

#### Usage Analysis:

**A2A Client (sage-a2a-go):**
- Line 69: `a2a *a2aclient.A2AClient` - HTTP signing client
- Line 154: `r.a2a.Do(ctx, req)` - Signed HTTP request execution
- Line 188: `a2aclient.NewA2AClient(r.myDID, r.myKey, r.httpClient)` - Client initialization

**Key Management (sage):**
- Line 68: `myKey sagecrypto.KeyPair` - Agent's signing key
- Line 169: `formats.NewJWKImporter().Import()` - JWK key import (Lines 168-172)
- Line 186: Key pair storage and management

**DID Operations (sage):**
- Line 67: `myDID sagedid.AgentDID` - Agent DID
- Line 76: `resolver sagedid.Resolver` - DID resolver interface
- Lines 196-214: Ethereum registry initialization for DID resolution
- Line 187: DID string type for agent identity

**HPKE (sage):**
- Lines 82-87: `hpkeState` struct with client, session manager, KID
- Line 282: `hpke.NewClient()` - HPKE client initialization
- Lines 299-336: HPKE encryption/decryption operations
- Line 85: `session.Manager` for HPKE session management

**Transport (sage):**
- Line 467-477: `transport.SecureMessage` construction
- Line 30: Transport package for protocol messaging

**Business Logic:** Infrastructure - routing, security, protocol handling

---

### 2. `/agents/payment/agent.go` (Lines: 682)
**Primary Agent:** Payment processing with HPKE support

#### Imports:
```go
Line 31: sagedid "github.com/sage-x-project/sage/pkg/agent/did"
Line 32: dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
Line 33: sagehttp "github.com/sage-x-project/sage/pkg/agent/transport/http"
Line 36: "github.com/sage-x-project/sage/pkg/agent/hpke"
Line 37: "github.com/sage-x-project/sage/pkg/agent/session"
Line 38: "github.com/sage-x-project/sage/pkg/agent/transport"
Line 41: "github.com/sage-x-project/sage-a2a-go/pkg/server"
Line 42: sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
Line 43: "github.com/sage-x-project/sage/pkg/agent/crypto/formats"
```

#### Usage Analysis:

**DID Middleware (sage-a2a-go):**
- Line 60: `mw *server.DIDAuthMiddleware` - RFC 9421 verification
- Line 79: Middleware initialization via a2autil
- Line 84: Error handler setup

**HPKE Server (sage):**
- Lines 54-57: HPKE server components (manager, server, HTTP adapter)
- Line 300-306: `hpke.NewServer()` with KEM key pair
- Lines 139-152: Session-based decryption (data mode)
- Lines 169-179: Response encryption with KID
- Line 307: HTTP server for HPKE handshake

**Key Management (sage):**
- Lines 522-536: Server signing key loading from JWK
- Lines 538-552: KEM key loading for HPKE
- Line 519: `formats.NewJWKImporter()` for key import

**DID Resolution (sage):**
- Lines 554-572: Ethereum resolver initialization
- Line 295: DID lookup from keys file
- Line 567: `dideth.NewEthereumClient()` for registry

**Transport (sage):**
- Lines 154-164: SecureMessage construction for HPKE
- Line 308-309: HPKE message handler

**Business Logic:** Domain - payment processing with LLM-generated receipts

---

### 3. `/agents/medical/agent.go` (Lines: 690)
**Primary Agent:** Medical information with HPKE (identical security to payment)

#### Imports:
```go
Line 27: sagedid "github.com/sage-x-project/sage/pkg/agent/did"
Line 28: dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
Line 29: sagehttp "github.com/sage-x-project/sage/pkg/agent/transport/http"
Line 32: "github.com/sage-x-project/sage/pkg/agent/hpke"
Line 33: "github.com/sage-x-project/sage/pkg/agent/session"
Line 34: "github.com/sage-x-project/sage/pkg/agent/transport"
Line 37: "github.com/sage-x-project/sage-a2a-go/pkg/server"
Line 38: sagecrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
Line 39: "github.com/sage-x-project/sage/pkg/agent/crypto/formats"
```

#### Usage Analysis:
**Identical to payment agent** - same security model, middleware, HPKE implementation
- Lines 77-89: DID middleware setup
- Lines 260-313: HPKE lazy initialization
- Lines 139-180: HPKE data mode encryption/decryption
- Lines 510-540: Key loading functions
- Lines 542-560: Resolver initialization

**Business Logic:** Domain - medical information responses with LLM

---

### 4. `/tools/keygen/gen_agents_key.go` (Lines: 249)
**Purpose:** Generate secp256k1 keys for agents

#### Imports:
```go
Line 33: agentcrypto "github.com/sage-x-project/sage/pkg/agent/crypto"
Line 34: "github.com/sage-x-project/sage/pkg/agent/crypto/formats"
Line 35: "github.com/sage-x-project/sage/pkg/agent/crypto/keys"
```

#### Usage Analysis:

**Key Generation (sage):**
- Line 108: `keys.GenerateSecp256k1KeyPair()` - ECDSA key generation
- Line 114: `jwkExp.Export(kp, agentcrypto.KeyFormatJWK)` - Export to JWK format
- Line 89: JWK exporter initialization

**Output:** Generates JWK files, legacy JSON summary, and all_keys.json for verifiers

**Business Logic:** Infrastructure - key management tooling

---

### 5. `/tools/registration/register_agents.go` (Lines: 462)
**Purpose:** Register agents to on-chain registry

#### Imports:
```go
Line 29: "github.com/sage-x-project/sage/pkg/agent/did"
Line 30: agentcard "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
```

#### Usage Analysis:

**Registry Operations (sage):**
- Line 119: `agentcard.NewAgentCardClient()` - Registry client
- Lines 366-375: `did.RegistrationParams` construction
- Line 188: `client.CommitRegistration()` - Commit phase
- Line 197: `client.RegisterAgent()` - Reveal phase
- Line 207: `client.ActivateAgent()` - Activation

**Signature Generation:**
- Lines 439-456: ECDSA ownership signature for registry

**Business Logic:** Infrastructure - on-chain agent registration

---

### 6. `/tools/registration/register_kem_agents.go` (Lines: 518)
**Purpose:** Register agents with both ECDSA and X25519 keys

#### Imports:
```go
Line 27: "github.com/sage-x-project/sage/pkg/agent/did"
Line 28: agentcard "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
```

#### Usage Analysis:

**Registry Operations (sage):**
- Lines 108-111: AgentCard client initialization
- Lines 422-437: Registration params with dual keys
- Lines 207-230: Commit-reveal-activate flow

**Signature Generation:**
- Lines 445-463: ECDSA ownership signature
- Lines 465-486: X25519 ownership signature

**Business Logic:** Infrastructure - enhanced registration with KEM keys

---

### 7. `/protocol/a2a_transport.go` (Lines: 162)
**Purpose:** A2A transport abstraction for HPKE and signing

#### Imports:
```go
Line 12: "github.com/sage-x-project/sage/pkg/agent/transport"
```

#### Usage Analysis:

**Transport Protocol (sage):**
- Line 38: `transport.SecureMessage` parameter
- Lines 108-151: `transport.Response` construction
- Line 56: Handshake mode with full SecureMessage JSON
- Lines 66-73: HPKE metadata handling (KID)

**Business Logic:** Infrastructure - protocol transport layer

---

### 8. `/internal/agentmux/handler.go` (Lines: 34)
**Purpose:** HTTP handler multiplexing with DID middleware

#### Imports:
```go
Line 7: "github.com/sage-x-project/sage-a2a-go/pkg/server"
```

#### Usage Analysis:

**DID Middleware (sage-a2a-go):**
- Line 13: `mw *server.DIDAuthMiddleware` parameter
- Line 19: `mw.Wrap(protectedMux)` - Apply middleware
- Lines 29-30: Protected endpoint mapping

**Business Logic:** Infrastructure - HTTP routing

---

### 9. `/internal/a2autil/middleware.go` (Lines: 73)
**Purpose:** DID middleware builder utility

#### Imports:
```go
Line 11: "github.com/sage-x-project/sage-a2a-go/pkg/server"
Line 12: "github.com/sage-x-project/sage/pkg/agent/did"
Line 13: dideth "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
```

#### Usage Analysis:

**DID Resolution (sage):**
- Line 51: `dideth.NewAgentCardClient()` - V4 resolver
- Line 57: `dideth.NewEthereumClient()` - Public key client
- Lines 41-48: Registry configuration

**Middleware Creation (sage-a2a-go):**
- Line 62: `server.NewDIDAuthMiddleware()` - RFC 9421 verifier
- Line 63: Optional verification mode

**Business Logic:** Infrastructure - security middleware setup

---

### 10. `/api/api.go` (Lines: 219)
**Purpose:** Client API gateway to Root

#### Imports:
```go
Line 12: a2aclient "github.com/sage-x-project/sage-a2a-go/pkg/client"
```

#### Usage Analysis:

**A2A Client (sage-a2a-go):**
- Line 33: `a2aClient *a2aclient.A2AClient` field
- Line 147: `g.a2aClient.Do(r.Context(), reqOut)` - Signed request
- Line 203: Toggle SAGE with signed request

**Business Logic:** Infrastructure - API gateway

---

### 11. `/cmd/root/main.go` (Lines: 144)
**Purpose:** Root agent process entry point

No direct sage/a2a imports - uses agent package

---

### 12. `/cmd/payment/main.go` (Lines: 141)
**Purpose:** Payment agent process entry point

No direct sage/a2a imports - uses agent package

---

### 13. `/cmd/medical/main.go` (Lines: 141)
**Purpose:** Medical agent process entry point

No direct sage/a2a imports - uses agent package

---

### 14. `/cmd/planning/main.go` (Lines: 81)
**Purpose:** Planning agent process entry point

No direct sage/a2a imports - uses agent package

---

### 15. `/cmd/client/main.go` (Lines: 183)
**Purpose:** Client CLI tool

No direct sage/a2a imports - uses API package

---

### 16-23. `/agents/root/` helper files
Additional files with sage imports for LLM helpers, slot management:
- `llm_helpers.go`
- `llm_questions.go`
- `llm_router.go`
- `medical_slots.go`
- `payment_slots.go`

No direct sage imports - domain logic only

---

## Functionality Categorization

### 1. Crypto/Key Management
**Current Usage:**
- Key generation (secp256k1, X25519)
- JWK import/export
- Key pair management
- ECDSA signing operations

**Files:**
- `tools/keygen/gen_agents_key.go`
- `agents/root/agent.go`
- `agents/payment/agent.go`
- `agents/medical/agent.go`

**Required in sage-a2a-go:**
```go
type KeyManager interface {
    GenerateKeyPair(keyType string) (KeyPair, error)
    ImportJWK(data []byte) (KeyPair, error)
    ExportJWK(kp KeyPair) ([]byte, error)
    Sign(kp KeyPair, message []byte) ([]byte, error)
}
```

**Priority:** CRITICAL - Core functionality

---

### 2. DID Resolution
**Current Usage:**
- Ethereum registry client
- Agent card operations
- DID to public key resolution
- KEM key resolution

**Files:**
- `internal/a2autil/middleware.go`
- `agents/root/agent.go`
- `agents/payment/agent.go`
- `agents/medical/agent.go`

**Required in sage-a2a-go:**
```go
type DIDResolver interface {
    GetAgentByDID(ctx context.Context, did string) (*Agent, error)
    ResolvePublicKey(ctx context.Context, did string) ([]byte, error)
    ResolveKEMKey(ctx context.Context, did string) ([]byte, error)
}
```

**Priority:** CRITICAL - Authentication backbone

---

### 3. HPKE Encryption
**Current Usage:**
- Client initialization with KEM
- Server with session management
- Handshake protocol
- Encrypt/decrypt with KID

**Files:**
- `agents/root/agent.go`
- `agents/payment/agent.go`
- `agents/medical/agent.go`

**Required in sage-a2a-go:**
```go
type HPKEClient interface {
    Initialize(ctx context.Context, contextID, clientDID, serverDID string) (kid string, error)
    Encrypt(plaintext []byte) ([]byte, error)
    Decrypt(ciphertext []byte) ([]byte, error)
}

type HPKEServer interface {
    HandleHandshake(ctx context.Context, msg *SecureMessage) (*Response, error)
    GetSession(kid string) (Session, bool)
}

type Session interface {
    Encrypt(plaintext []byte) ([]byte, error)
    Decrypt(ciphertext []byte) ([]byte, error)
}
```

**Priority:** HIGH - E2E encryption

**Gap Analysis:** sage-a2a-go currently lacks server-side HPKE implementation

---

### 4. A2A Signing/Verification
**Current Usage:**
- RFC 9421 HTTP message signatures
- DID-based authentication
- Request/response signing
- Middleware for verification

**Files:**
- `internal/agentmux/handler.go`
- `internal/a2autil/middleware.go`
- `api/api.go`
- `agents/root/agent.go`

**Required in sage-a2a-go:**
Already implemented in sage-a2a-go/pkg/client and /pkg/server

**Priority:** CRITICAL - Already available

---

### 5. Transport/Protocol
**Current Usage:**
- SecureMessage structure
- Response structure
- HTTP transport adapters

**Files:**
- `protocol/a2a_transport.go`
- `agents/root/agent.go`
- `agents/payment/agent.go`
- `agents/medical/agent.go`

**Required in sage-a2a-go:**
```go
type Transport interface {
    Send(ctx context.Context, msg *SecureMessage) (*Response, error)
}

type SecureMessage struct {
    ID        string
    ContextID string
    TaskID    string
    Payload   []byte
    DID       string
    Metadata  map[string]string
    Role      string
}
```

**Priority:** HIGH - Core messaging

---

### 6. Registry/On-chain Operations
**Current Usage:**
- Agent registration (commit-reveal)
- Activation
- Registry configuration

**Files:**
- `tools/registration/register_agents.go`
- `tools/registration/register_kem_agents.go`

**Required in sage-a2a-go:**
```go
type RegistryClient interface {
    CommitRegistration(ctx context.Context, params *RegistrationParams) (*Status, error)
    RegisterAgent(ctx context.Context, status *Status) (*Status, error)
    ActivateAgent(ctx context.Context, status *Status) error
    GetAgentByDID(ctx context.Context, did string) (*Agent, error)
}
```

**Priority:** MEDIUM - Setup/deployment only

---

## Gap Analysis

### Critical Gaps in sage-a2a-go

1. **HPKE Server Implementation**
   - Missing: `hpke.Server`, session management
   - Required by: payment, medical agents
   - Impact: Cannot run secure external agents

2. **Session Manager**
   - Missing: `session.Manager` for HPKE
   - Required by: All HPKE-enabled agents
   - Impact: No stateful encryption sessions

3. **HTTP Transport Adapter**
   - Missing: `transport/http.HTTPServer`
   - Required by: HPKE handshake flow
   - Impact: Manual handshake implementation needed

### Available in sage-a2a-go

1. **A2A Client/Server** ✓
   - RFC 9421 signing
   - DID verification middleware

2. **DID Resolution** (Partial)
   - Basic resolution available
   - Need AgentCard v4 support

---

## Requirements for sage-a2a-go Team

### Priority 1: CRITICAL (Block migration)

**HPKE Server Package:**
```go
package hpke

type Server struct {
    signingKey KeyPair
    kemKey     KeyPair
    sessions   *SessionManager
    resolver   DIDResolver
}

func NewServer(sign, kem KeyPair, sessions *SessionManager, resolver DIDResolver) *Server
func (s *Server) HandleHandshake(ctx context.Context, msg *SecureMessage) (*Response, error)
```

**Session Management:**
```go
package session

type Manager struct {
    sessions sync.Map
}

func NewManager() *Manager
func (m *Manager) CreateSession(kid string, keys []byte) (*Session, error)
func (m *Manager) GetByKeyID(kid string) (*Session, bool)
```

### Priority 2: HIGH

**Key Management Unification:**
```go
package crypto

type KeyPair interface {
    PublicKey() crypto.PublicKey
    PrivateKey() crypto.PrivateKey
    ID() string
    Sign(message []byte) ([]byte, error)
}

type Importer interface {
    Import(data []byte, format KeyFormat) (KeyPair, error)
}

type Exporter interface {
    Export(kp KeyPair, format KeyFormat) ([]byte, error)
}
```

### Priority 3: MEDIUM

**Registry Client Enhancement:**
- Support for AgentCard v4
- Dual key registration (ECDSA + X25519)
- Commit-reveal-activate flow

---

## Migration Checklist

### Phase 1: Foundation (Week 1)
- [ ] Implement HPKE server in sage-a2a-go
- [ ] Add session management
- [ ] Create HTTP transport adapter
- [ ] Test with payment agent

### Phase 2: Key Management (Week 2)
- [ ] Unify key interfaces
- [ ] Implement JWK import/export
- [ ] Add key generation functions
- [ ] Migrate keygen tool

### Phase 3: DID & Registry (Week 3)
- [ ] Enhance DID resolver
- [ ] Add AgentCard v4 support
- [ ] Implement registry client
- [ ] Migrate registration tools

### Phase 4: Agent Migration (Week 4)
- [ ] Update payment agent imports
- [ ] Update medical agent imports
- [ ] Update root agent imports
- [ ] Test E2E flows

### Phase 5: Cleanup (Week 5)
- [ ] Remove sage dependencies
- [ ] Update documentation
- [ ] Performance testing
- [ ] Security audit

---

## Usage Patterns & Examples

### Pattern 1: HPKE-Enabled Agent
```go
// Current (sage)
import (
    "github.com/sage-x-project/sage/pkg/agent/hpke"
    "github.com/sage-x-project/sage/pkg/agent/session"
)

mgr := session.NewManager()
srv := hpke.NewServer(signKP, mgr, serverDID, resolver, opts)

// Target (sage-a2a-go)
import "github.com/sage-x-project/sage-a2a-go/pkg/hpke"

mgr := hpke.NewSessionManager()
srv := hpke.NewServer(signKP, kemKP, mgr, resolver)
```

### Pattern 2: DID Middleware
```go
// Current (mixed)
import (
    "github.com/sage-x-project/sage-a2a-go/pkg/server"
    "github.com/sage-x-project/sage/pkg/agent/did/ethereum"
)

resolver := ethereum.NewEthereumClient(cfg)
mw := server.NewDIDAuthMiddleware(resolver, client)

// Target (sage-a2a-go)
import "github.com/sage-x-project/sage-a2a-go/pkg/did"

resolver := did.NewEthereumResolver(cfg)
mw := did.NewAuthMiddleware(resolver)
```

### Pattern 3: A2A Client
```go
// Current (sage-a2a-go)
import "github.com/sage-x-project/sage-a2a-go/pkg/client"

a2a := client.NewA2AClient(myDID, myKey, httpClient)
resp, err := a2a.Do(ctx, req)

// Target (same - already in sage-a2a-go)
```

---

## Recommendations

### Immediate Actions
1. **Prioritize HPKE server** implementation in sage-a2a-go
2. **Create compatibility layer** for smooth migration
3. **Document all interfaces** before implementation

### Architecture Decisions
1. **Separate concerns:** Keep crypto, DID, and transport as distinct packages
2. **Interface-first:** Define all interfaces before implementation
3. **Backward compatibility:** Support existing key formats and protocols

### Testing Strategy
1. **Unit tests** for each component
2. **Integration tests** for E2E flows
3. **Migration tests** comparing old vs new behavior
4. **Performance benchmarks** for crypto operations

---

## Appendix: Import Statistics

| Package | Files | Lines | Critical |
|---------|-------|-------|----------|
| sage-a2a-go/pkg/client | 3 | 15 | Yes |
| sage-a2a-go/pkg/server | 4 | 20 | Yes |
| sage/pkg/agent/crypto | 7 | 42 | Yes |
| sage/pkg/agent/did | 7 | 35 | Yes |
| sage/pkg/agent/hpke | 3 | 21 | Yes |
| sage/pkg/agent/session | 3 | 9 | Yes |
| sage/pkg/agent/transport | 4 | 16 | Yes |

**Total unique imports:** 31
**Total import statements:** 158
**Files affected:** 23/50+ (46%)

---

## Contact & Next Steps

**For sage-a2a-go team:**
- Review critical gaps (HPKE server, session management)
- Prioritize API design for Week 1 deliverables
- Schedule migration planning session

**For sage-multi-agent team:**
- Prepare compatibility tests
- Document current behavior
- Identify migration risks

**Target completion:** 5 weeks from approval

---

*End of Report*