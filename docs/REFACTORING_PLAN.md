# SAGE Multi-Agent Refactoring Plan

**Version:** 1.0
**Date:** 2024-11-02
**Status:** Planning Phase
**Target Completion:** Q1 2025

## Executive Summary

This document outlines the refactoring strategy for the `sage-multi-agent` project to transform it from a monolithic demo into a clean, pluggable, and extensible multi-agent framework. The refactoring will support the development of **sage-adk** (Agent Development Kit) by establishing clear architectural patterns and separating domain logic from infrastructure concerns.

### Primary Goals

1. **Eliminate `sage` direct dependencies** - Move all SAGE protocol functionality to `sage-a2a-go`
2. **Establish pluggable architecture** - Enable new agents to be added via plugins
3. **Apply SOLID & Clean Architecture principles** - Improve maintainability and testability
4. **Create clear DDD boundaries** - Separate domain logic from infrastructure
5. **Enable LLM provider flexibility** - Support multiple LLM providers via plugin system

---

## Current State Analysis

### Dependency Analysis Summary

**Total Go files analyzed:** 40
**Files with `sage` imports:** 10
**Files with `sage-a2a-go` imports:** 8

#### Package Usage Distribution

| Package | Files | Primary Usage |
|---------|-------|---------------|
| `sage/pkg/agent/crypto` | 8 | Key management (JWK import/export) |
| `sage/pkg/agent/did` | 9 | DID types, Ethereum resolver |
| `sage/pkg/agent/hpke` | 3 | HPKE client/server |
| `sage/pkg/agent/session` | 3 | HPKE session management |
| `sage/pkg/agent/transport` | 4 | SecureMessage protocol |
| `sage-a2a-go/pkg/client` | 3 | RFC 9421 signing (outbound) |
| `sage-a2a-go/pkg/server` | 3 | RFC 9421 verification (inbound) |

### Critical Issues Identified

1. **Code Duplication**: Payment and Medical agents share 90% identical infrastructure code (~250 lines duplicated)
2. **God Object**: Root agent is 1,756 lines mixing routing, LLM, HPKE, A2A, and business logic
3. **Environment Coupling**: 50+ direct `os.Getenv` calls scattered across codebase
4. **No Dependency Injection**: All dependencies created in constructors (hard to test)
5. **Infrastructure Duplication**: DID resolver logic duplicated 3+ times across agents

---

## Target Architecture

### Layered Architecture

```
┌───────────────────────────────────────────────────────┐
│             Application Layer                         │
│  • Agent Factories                                    │
│  • Plugin Registry                                    │
│  • Domain Processors (Payment, Medical, Planning)    │
└─────────────────┬─────────────────────────────────────┘
                  │
┌─────────────────▼─────────────────────────────────────┐
│             Domain Layer (Interfaces)                 │
│  • Agent                                              │
│  • DomainProcessor                                    │
│  • IntentRouter                                       │
│  • ConversationManager                                │
└─────────────────┬─────────────────────────────────────┘
                  │
┌─────────────────▼─────────────────────────────────────┐
│         Infrastructure Layer                          │
│  • BaseAgent                                          │
│  • A2AClient / HPKEManager / DIDResolver             │
│  • LLMService                                         │
│  • ConfigProvider                                     │
│  • Middleware (Auth, Logging, Recovery)              │
└─────────────────┬─────────────────────────────────────┘
                  │
┌─────────────────▼─────────────────────────────────────┐
│         External Dependencies                         │
│  • sage-a2a-go (A2A protocol)                        │
│  • Ethereum (DID registry)                            │
│  • LLM Providers (OpenAI, Gemini, etc.)              │
└───────────────────────────────────────────────────────┘
```

### Core Interfaces

```go
// Domain Layer

type Agent interface {
    Start(ctx context.Context) error
    Shutdown(ctx context.Context) error
    Handler() http.Handler
    Health() HealthStatus
}

type DomainProcessor interface {
    Process(ctx context.Context, msg *AgentMessage) (*AgentMessage, error)
    SupportedIntents() []string
}

type AgentPlugin interface {
    Name() string
    CreateProcessor(deps PluginDependencies) (DomainProcessor, error)
    RequiredConfig() []string
}

// Infrastructure Layer

type A2AClient interface {
    Do(ctx context.Context, req *http.Request) (*http.Response, error)
    DID() string
}

type HPKEManager interface {
    Encrypt(sessionID string, plaintext []byte) (ciphertext []byte, kid string, err error)
    Decrypt(kid string, ciphertext []byte) (plaintext []byte, err error)
    InitSession(ctx context.Context, clientDID, serverDID string) (kid string, err error)
}

type LLMService interface {
    Execute(ctx context.Context, templateName string, data any) (string, error)
    RegisterTemplate(template PromptTemplate)
    ExtractJSON(ctx context.Context, templateName string, data any, result any) error
}

type ConfigProvider interface {
    GetString(key string) string
    GetBool(key string) bool
    MustGet(key string) string
    Validate() error
}
```

---

## Migration Strategy (6-Phase Plan)

### Phase 1: Infrastructure Extraction (Weeks 1-2)

**Goal:** Extract duplicated infrastructure code into reusable packages

#### Tasks

1. Create `pkg/infrastructure/a2a/client.go`
   - Extract A2A client initialization from root/planning/client
   - Consolidate JWK loading logic
   - Add interface abstraction

2. Create `pkg/infrastructure/hpke/manager.go`
   - Extract HPKE setup from payment/medical agents
   - Unify session management
   - Add interface abstraction

3. Create `pkg/infrastructure/did/resolver.go`
   - Extract DID resolver logic (currently duplicated 3x)
   - Consolidate Ethereum client configuration
   - Add interface abstraction

4. Create `pkg/infrastructure/config/provider.go`
   - Centralize all `os.Getenv` calls
   - Add default values and validation
   - Support environment + file-based config

#### Success Criteria

- ✅ Remove 500+ lines of duplicated code
- ✅ All agents use shared infrastructure packages
- ✅ Zero direct `os.Getenv` calls in agent files
- ✅ Backward compatibility maintained (no behavior change)

#### Deliverables

- `pkg/infrastructure/a2a/client.go`
- `pkg/infrastructure/hpke/manager.go`
- `pkg/infrastructure/did/resolver.go`
- `pkg/infrastructure/config/env_provider.go`
- Migration guide document

---

### Phase 2: Domain Interfaces (Week 3)

**Goal:** Define clean domain interfaces and extract business logic

#### Tasks

1. Create `pkg/domain/agent.go`
   - Define `Agent` interface
   - Define `HealthStatus` type

2. Create `pkg/domain/processor.go`
   - Define `DomainProcessor` interface
   - Define `AgentMessage` canonical type

3. Extract Payment business logic
   - Create `pkg/application/processors/payment/processor.go`
   - Implement `DomainProcessor` interface
   - Move receipt generation logic

4. Extract Medical business logic
   - Create `pkg/application/processors/medical/processor.go`
   - Implement `DomainProcessor` interface
   - Move medical context handling logic

5. Extract Planning business logic
   - Create `pkg/application/processors/planning/processor.go`
   - Implement `DomainProcessor` interface
   - Move hotel search logic

#### Success Criteria

- ✅ Domain logic separated from infrastructure in all agents
- ✅ Each processor file < 200 lines
- ✅ All processors implement `DomainProcessor` interface
- ✅ 100% test coverage for processors (easy to test with mocks)

#### Deliverables

- `pkg/domain/` package with all interfaces
- `pkg/application/processors/payment/` package
- `pkg/application/processors/medical/` package
- `pkg/application/processors/planning/` package
- Unit tests for all processors

---

### Phase 3: BaseAgent Implementation (Week 4)

**Goal:** Create reusable base agent implementation

#### Tasks

1. Create `pkg/infrastructure/agent/base_agent.go`
   - Implement `Agent` interface
   - Handle HTTP server lifecycle
   - Provide middleware chain support
   - Implement graceful shutdown

2. Create middleware packages
   - `pkg/infrastructure/middleware/logging.go`
   - `pkg/infrastructure/middleware/recovery.go`
   - `pkg/infrastructure/middleware/metrics.go`

3. Migrate Payment agent to use `BaseAgent`
   - Refactor `agents/payment/agent.go`
   - Inject dependencies via constructor
   - Use `BaseAgent` for lifecycle management

4. Migrate Medical agent to use `BaseAgent`
   - Refactor `agents/medical/agent.go`
   - Inject dependencies via constructor
   - Use `BaseAgent` for lifecycle management

#### Success Criteria

- ✅ Payment/Medical agents share 80%+ infrastructure code
- ✅ Each agent file < 300 lines
- ✅ All dependencies injected via constructor
- ✅ Graceful shutdown implemented for all agents

#### Deliverables

- `pkg/infrastructure/agent/base_agent.go`
- `pkg/infrastructure/middleware/` package
- Refactored payment/medical agents
- Integration tests for BaseAgent

---

### Phase 4: LLM Service Layer (Week 5)

**Goal:** Centralize LLM integration and enable provider flexibility

#### Tasks

1. Create `pkg/infrastructure/llm/service.go`
   - Define `LLMService` interface
   - Implement template-based execution
   - Add response caching (LRU cache)
   - Add JSON extraction helper

2. Create `pkg/infrastructure/llm/templates.go`
   - Extract all inline prompts to templates
   - Define default templates (intent_route, payment_receipt, medical_info, etc.)
   - Support template registration

3. Create provider abstractions
   - `pkg/infrastructure/llm/providers/openai.go`
   - `pkg/infrastructure/llm/providers/gemini.go`
   - `pkg/infrastructure/llm/providers/anthropic.go`
   - `pkg/infrastructure/llm/providers/local.go` (Ollama support)

4. Update all processors to use `LLMService`
   - Replace inline `llm.Client.Chat()` calls
   - Use template execution
   - Add fallback logic for LLM failures

#### Success Criteria

- ✅ All LLM calls go through `LLMService`
- ✅ Zero inline prompt strings in processor code
- ✅ Support for 4+ LLM providers (OpenAI, Gemini, Anthropic, Local)
- ✅ 50%+ cache hit rate in production (measured via metrics)

#### Deliverables

- `pkg/infrastructure/llm/` package
- `pkg/infrastructure/llm/providers/` package
- Updated processors using `LLMService`
- LLM provider configuration documentation

---

### Phase 5: Plugin System (Weeks 6-7)

**Goal:** Enable pluggable agent architecture

#### Tasks

1. Create `pkg/application/registry.go`
   - Define `AgentPlugin` interface
   - Implement `PluginRegistry` for agent registration
   - Support dynamic agent loading

2. Create `pkg/application/factory.go`
   - Implement `AgentFactory` with dependency injection
   - Support builder pattern for agent creation
   - Validate plugin configuration

3. Implement plugins for existing agents
   - `pkg/application/processors/payment/plugin.go`
   - `pkg/application/processors/medical/plugin.go`
   - `pkg/application/processors/planning/plugin.go`

4. Create example new agent plugin
   - `pkg/application/processors/shopping/` (example)
   - Demonstrate pluggability
   - Document plugin development guide

5. Update `cmd/` entry points to use plugin system
   - Refactor `cmd/payment/main.go`
   - Refactor `cmd/medical/main.go`
   - Refactor `cmd/planning/main.go`

#### Success Criteria

- ✅ New agent can be added without modifying core packages
- ✅ Agent creation time reduced from 2-3 days to 2-3 hours
- ✅ Shopping agent plugin working as proof-of-concept
- ✅ Plugin development guide published

#### Deliverables

- `pkg/application/registry.go`
- `pkg/application/factory.go`
- Plugin implementations for all agents
- Example shopping agent plugin
- Plugin development guide (`docs/PLUGIN_DEVELOPMENT.md`)

---

### Phase 6: Root Agent Refactor (Weeks 8-9)

**Goal:** Break down god object, simplify orchestration

#### Tasks

1. Extract intent routing
   - Create `pkg/application/router/intent_router.go`
   - Move rule-based routing logic
   - Move LLM-based routing logic
   - Define `IntentRouter` interface

2. Extract slot extraction
   - Move slot extraction logic to respective domain processors
   - Payment slots → `PaymentProcessor`
   - Medical slots → `MedicalProcessor`
   - Planning slots → `PlanningProcessor`

3. Extract conversation management
   - Create `pkg/application/conversation/manager.go`
   - Implement `ConversationManager` interface
   - Move in-memory conversation state

4. Simplify Root agent
   - Reduce to orchestrator only (~500 lines)
   - Use `IntentRouter` for routing
   - Use `ConversationManager` for state
   - Use `BaseAgent` for lifecycle

5. Add comprehensive tests
   - Unit tests for `IntentRouter`
   - Unit tests for `ConversationManager`
   - Integration tests for root orchestration

#### Success Criteria

- ✅ Root agent file < 500 lines (down from 1,756)
- ✅ Clear separation: routing / state / orchestration
- ✅ 80%+ test coverage for root components
- ✅ No business logic in root agent (pure orchestration)

#### Deliverables

- `pkg/application/router/` package
- `pkg/application/conversation/` package
- Refactored root agent
- Comprehensive test suite

---

## sage-a2a-go Integration Plan

### Functionality to Move from `sage` to `sage-a2a-go`

Based on dependency analysis, these functions should be abstracted into `sage-a2a-go`:

#### High Priority

1. **HPKE Server Builder** (Currently duplicated in payment/medical)
   ```go
   // Proposed: github.com/sage-x-project/sage-a2a-go/pkg/server
   type HPKEServerBuilder struct {
       SigningKeyPath string
       KEMKeyPath     string
       ServerDID      string
       ResolverConfig *did.RegistryConfig
   }
   func (b *HPKEServerBuilder) Build() (*hpke.Server, *session.Manager, error)
   ```

2. **DID Middleware Builder** (Currently in internal/a2autil)
   ```go
   // Move to: github.com/sage-x-project/sage-a2a-go/pkg/server
   func NewDIDAuthMiddleware(config *RegistryConfig) (*DIDAuthMiddleware, error)
   ```

3. **Unified Key Loader** (Duplicated across 8 files)
   ```go
   // Proposed: github.com/sage-x-project/sage-a2a-go/pkg/crypto
   type KeyLoader struct {
       JWKPath string
   }
   func (l *KeyLoader) LoadSigningKey() (KeyPair, DID, error)
   func (l *KeyLoader) LoadKEMKey() (KeyPair, error)
   ```

4. **A2A Client Factory** (Pattern in root/planning/client)
   ```go
   // Proposed: github.com/sage-x-project/sage-a2a-go/pkg/client
   type A2AClientBuilder struct {
       JWKPath string
       DID     string
   }
   func (b *A2AClientBuilder) Build() (*A2AClient, error)
   ```

#### Medium Priority

5. **Agent Registration Helper** (Pattern in tools/registration)
   ```go
   // Proposed: github.com/sage-x-project/sage-a2a-go/pkg/registry
   type AgentRegistrar struct {
       Config *RegistryConfig
   }
   func (r *AgentRegistrar) RegisterAgent(params *RegistrationParams) error
   ```

6. **Transport Factory with HPKE** (Pattern in protocol/a2a_transport.go)
   ```go
   // Proposed: github.com/sage-x-project/sage-a2a-go/pkg/transport
   type TransportBuilder struct {
       Signer      A2AClient
       BaseURL     string
       HPKEEnabled bool
   }
   func (b *TransportBuilder) Build() (*Transport, error)
   ```

### Coordination with sage-a2a-go Team

This refactoring is contingent on `sage-a2a-go` providing these abstractions. We will:

1. **Document requirements** - Provide detailed specs to sage-a2a-go team
2. **Wait for sage-a2a-go release** - Coordinate version tagging
3. **Update dependencies** - Upgrade to new sage-a2a-go version
4. **Remove direct sage imports** - Replace with sage-a2a-go equivalents

---

## New Directory Structure

```
sage-multi-agent/
├── cmd/
│   ├── root/main.go              # Root orchestrator server
│   ├── payment/main.go           # Payment agent server
│   ├── medical/main.go           # Medical agent server
│   ├── planning/main.go          # Planning agent server
│   └── cli/main.go               # Admin CLI
│
├── pkg/
│   ├── domain/                   # Domain layer (interfaces only)
│   │   ├── agent.go
│   │   ├── processor.go
│   │   ├── router.go
│   │   ├── conversation.go
│   │   └── types.go
│   │
│   ├── application/              # Application layer (use cases)
│   │   ├── factory.go
│   │   ├── registry.go
│   │   ├── router/
│   │   │   ├── intent_router.go
│   │   │   └── rule_based.go
│   │   ├── conversation/
│   │   │   └── manager.go
│   │   └── processors/
│   │       ├── payment/
│   │       │   ├── processor.go
│   │       │   ├── plugin.go
│   │       │   └── processor_test.go
│   │       ├── medical/
│   │       │   ├── processor.go
│   │       │   ├── plugin.go
│   │       │   └── processor_test.go
│   │       ├── planning/
│   │       │   ├── processor.go
│   │       │   ├── plugin.go
│   │       │   └── processor_test.go
│   │       └── shopping/         # Example new agent
│   │           ├── processor.go
│   │           └── plugin.go
│   │
│   ├── infrastructure/           # Infrastructure layer
│   │   ├── agent/
│   │   │   ├── base_agent.go
│   │   │   └── base_agent_test.go
│   │   ├── a2a/
│   │   │   ├── client.go
│   │   │   └── middleware.go
│   │   ├── hpke/
│   │   │   └── manager.go
│   │   ├── did/
│   │   │   └── resolver.go
│   │   ├── llm/
│   │   │   ├── service.go
│   │   │   ├── templates.go
│   │   │   └── providers/
│   │   │       ├── openai.go
│   │   │       ├── gemini.go
│   │   │       ├── anthropic.go
│   │   │       └── local.go
│   │   ├── config/
│   │   │   ├── provider.go
│   │   │   ├── env.go
│   │   │   └── file.go
│   │   └── middleware/
│   │       ├── logging.go
│   │       ├── recovery.go
│   │       └── metrics.go
│   │
│   └── types/                    # Shared types
│       ├── message.go
│       ├── errors.go
│       └── health.go
│
├── internal/                     # Private helpers
│   ├── testutil/
│   │   ├── mocks.go
│   │   └── fixtures.go
│   └── util/
│       └── helpers.go
│
├── docs/
│   ├── ARCHITECTURE.md           # Architecture overview
│   ├── PLUGIN_DEVELOPMENT.md     # Plugin development guide
│   ├── REFACTORING_PLAN.md       # This document
│   └── API.md                    # API documentation
│
├── scripts/                      # Utility scripts (unchanged)
├── tools/                        # Tools (keygen, registration)
├── go.mod
└── README.md
```

---

## Testing Strategy

### Unit Testing

- **Target Coverage:** 80% minimum
- **Mock All External Dependencies:**
  - Mock `LLMService` for processor tests
  - Mock `ConfigProvider` for all component tests
  - Mock `A2AClient`, `HPKEManager`, `DIDResolver` for agent tests

### Integration Testing

- **Agent Lifecycle Tests:**
  - Start/shutdown behavior
  - Health check endpoints
  - Graceful shutdown under load

- **End-to-End Tests:**
  - Full message flow (client → root → payment/medical → response)
  - HPKE encryption/decryption
  - DID authentication

### Performance Testing

- **Benchmarks:**
  - Agent message throughput (target: 1000 msg/sec)
  - LLM cache hit rate (target: 50%)
  - Memory usage under load (target: <500MB per agent)

---

## Risk Mitigation

### Risk 1: Breaking Changes During Refactoring

**Mitigation:**
- Maintain backward compatibility in each phase
- Feature flags for new implementations
- Comprehensive regression test suite
- Incremental rollout per agent

### Risk 2: sage-a2a-go Dependency Delays

**Mitigation:**
- Begin with internal abstractions (Phase 1-4)
- Mock sage-a2a-go interfaces
- Parallel track: provide requirements to sage-a2a-go team early
- Have fallback plan to keep temporary adapters if needed

### Risk 3: Performance Regression

**Mitigation:**
- Benchmark before/after each phase
- Load testing in staging environment
- Profiling for memory/CPU hotspots
- Rollback plan if performance degrades >10%

### Risk 4: Team Onboarding

**Mitigation:**
- Comprehensive documentation for each phase
- Code review sessions for new patterns
- Pair programming for complex migrations
- Training sessions on SOLID/DDD principles

---

## Success Metrics

### Code Quality Metrics

| Metric | Current | Target |
|--------|---------|--------|
| **Lines of Code per File** | 1,756 max | <500 max |
| **Code Duplication** | 30% | <5% |
| **Test Coverage** | 20% | >80% |
| **Cyclomatic Complexity** | 45 max | <15 max |
| **Direct `os.Getenv` Calls** | 50+ | 0 |

### Developer Experience Metrics

| Metric | Current | Target |
|--------|---------|--------|
| **New Agent Development Time** | 2-3 days | 2-3 hours |
| **Build Time** | 12s | <10s |
| **Test Execution Time** | 8s | <5s |
| **Onboarding Time (New Dev)** | 1 week | 2 days |

### Architecture Metrics

| Metric | Current | Target |
|--------|---------|--------|
| **Direct `sage` Dependencies** | 10 files | 0 files |
| **Shared Infrastructure Code** | 30% | 90% |
| **LLM Providers Supported** | 2 | 4+ |
| **Pluggable Agents** | 0 | All agents |

---

## Timeline & Milestones

```
Week 1-2:  Phase 1 - Infrastructure Extraction
Week 3:    Phase 2 - Domain Interfaces
Week 4:    Phase 3 - BaseAgent Implementation
Week 5:    Phase 4 - LLM Service Layer
Week 6-7:  Phase 5 - Plugin System
Week 8-9:  Phase 6 - Root Agent Refactor
Week 10:   Final testing, documentation, release
```

**Target Release:** v0.2.0 (Post-refactoring)

---

## Post-Refactoring: Path to sage-adk

Once refactoring is complete, this project serves as the foundation for **sage-adk** (Agent Development Kit):

### sage-adk Components (Extracted from this Project)

1. **Core Package** (`github.com/sage-x-project/sage-adk`)
   - `pkg/agent` - BaseAgent implementation
   - `pkg/processor` - DomainProcessor interface
   - `pkg/router` - Intent routing
   - `pkg/conversation` - Conversation management

2. **Middleware Package** (`github.com/sage-x-project/sage-adk/middleware`)
   - Logging, recovery, metrics middleware
   - SAGE DID authentication middleware

3. **LLM Package** (`github.com/sage-x-project/sage-adk/llm`)
   - LLMService with template support
   - Providers for OpenAI, Gemini, Anthropic, Local

4. **Config Package** (`github.com/sage-x-project/sage-adk/config`)
   - ConfigProvider abstraction
   - Environment & file-based implementations

### Developer Experience with sage-adk

**Before (Current):**
```go
// 300+ lines of boilerplate per agent
// Manual HPKE setup, DID middleware, A2A client, etc.
```

**After (With sage-adk):**
```go
package main

import (
    "github.com/sage-x-project/sage-adk/agent"
    "github.com/sage-x-project/sage-adk/processor"
)

type MyProcessor struct{}

func (p *MyProcessor) Process(ctx, msg) (*AgentMessage, error) {
    // Domain logic only (10-50 lines)
    return &AgentMessage{Content: "processed"}, nil
}

func main() {
    agent.Run("my-agent", &MyProcessor{})  // One-liner!
}
```

---

## Appendix: Reference Documents

- [SAGE Protocol Specification](https://github.com/SAGE-X-project/sage)
- [sage-a2a-go Documentation](https://github.com/SAGE-X-project/sage-a2a-go)
- [Clean Architecture (Uncle Bob)](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html)
- [SOLID Principles](https://en.wikipedia.org/wiki/SOLID)
- [Domain-Driven Design (DDD)](https://martinfowler.com/bliki/DomainDrivenDesign.html)

---

**Document Owner:** SAGE-X Project Team
**Last Updated:** 2024-11-02
**Status:** Approved for Implementation
