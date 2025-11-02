# Agent Framework Migration Guide

## 목적

본 가이드는 sage-multi-agent의 `internal/agent` 프레임워크를 sage-a2a-go로 이식하는 방법을 설명합니다.

## 이식 전 체크리스트

- [ ] AGENT_FRAMEWORK_DESIGN.md 읽기
- [ ] internal/agent/* 코드 검토
- [ ] internal/agent/example_payment.go 사용 예시 확인
- [ ] 컴파일 테스트 확인: `go build -o /dev/null ./internal/agent/...`

## 이식 단계

### 1단계: sage-a2a-go 저장소 준비

```bash
cd /path/to/sage-a2a-go
mkdir -p pkg/agent/{keys,session,did,middleware,hpke}
```

### 2단계: 기본 패키지 복사

#### 2.1 Keys 패키지

```bash
# 파일 복사
cp sage-multi-agent/internal/agent/keys/keys.go \
   sage-a2a-go/pkg/agent/keys/keys.go

# import 경로 수정
sed -i '' 's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' \
   sage-a2a-go/pkg/agent/keys/keys.go
```

#### 2.2 Session 패키지

```bash
# 파일 복사
cp sage-multi-agent/internal/agent/session/session.go \
   sage-a2a-go/pkg/agent/session/session.go

# import 경로 수정
sed -i '' 's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' \
   sage-a2a-go/pkg/agent/session/session.go
```

**TODO**: `GetUnderlying()` 메서드 제거 및 세션 관리 메서드 구현

```go
// 추가 구현 필요
func (m *Manager) GetSession(kid string) (*Session, error) { ... }
func (m *Manager) StoreSession(kid string, session *Session) error { ... }
func (m *Manager) DeleteSession(kid string) error { ... }
func (m *Manager) ListSessions() []string { ... }
func (m *Manager) Clear() { ... }
```

#### 2.3 DID 패키지

```bash
# 파일 복사
cp sage-multi-agent/internal/agent/did/*.go \
   sage-a2a-go/pkg/agent/did/

# import 경로 수정
sed -i '' 's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' \
   sage-a2a-go/pkg/agent/did/*.go
```

**TODO**: `Get*()` 메서드 제거 및 DID 해결 메서드 구현

```go
// 추가 구현 필요
func (r *Resolver) Resolve(did string) (*DIDDocument, error) { ... }
func (r *Resolver) GetPublicKey(did string, keyID string) (PublicKey, error) { ... }
func (r *Resolver) VerifySignature(did string, message []byte, signature []byte) error { ... }
func (r *Resolver) Register(did string, document *DIDDocument) error { ... }
func (r *Resolver) Update(did string, document *DIDDocument) error { ... }
```

#### 2.4 Middleware 패키지

```bash
# 파일 복사
cp sage-multi-agent/internal/agent/middleware/middleware.go \
   sage-a2a-go/pkg/agent/middleware/middleware.go

# import 경로 수정
sed -i '' 's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' \
   sage-a2a-go/pkg/agent/middleware/middleware.go
```

**TODO**: `GetUnderlying()` 메서드 제거 및 미들웨어 메서드 구현

```go
// 추가 구현 필요
func (d *DIDAuth) Verify(r *http.Request) (*VerificationResult, error) { ... }
func (d *DIDAuth) Sign(r *http.Request, privateKey KeyPair, keyID string) error { ... }
func (d *DIDAuth) Middleware() func(http.Handler) http.Handler { ... }
```

#### 2.5 HPKE 패키지

```bash
# 파일 복사
cp sage-multi-agent/internal/agent/hpke/*.go \
   sage-a2a-go/pkg/agent/hpke/

# import 경로 수정
sed -i '' 's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' \
   sage-a2a-go/pkg/agent/hpke/*.go
```

**TODO**: SendHandshake/SendData 메서드 구현 및 `GetUnderlying()` 제거

```go
// 주석 해제 및 구현
func (c *Client) SendHandshake(ctx context.Context, targetDID sagedid.AgentDID, payload []byte) (*transport.Response, string, error) {
    // sage HPKE 클라이언트 API에 맞게 구현
    return c.underlying.SendHandshake(ctx, string(targetDID), payload)
}

func (c *Client) SendData(ctx context.Context, kid string, targetDID sagedid.AgentDID, payload []byte) (*transport.Response, error) {
    // sage HPKE 클라이언트 API에 맞게 구현
    return c.underlying.SendData(ctx, kid, string(targetDID), payload)
}
```

### 3단계: 메인 Agent 프레임워크 복사

```bash
# 파일 복사
cp sage-multi-agent/internal/agent/agent.go \
   sage-a2a-go/pkg/agent/agent.go

# import 경로 수정
sed -i '' 's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' \
   sage-a2a-go/pkg/agent/agent.go
```

### 4단계: 컴파일 테스트

```bash
cd sage-a2a-go
go build -o /dev/null ./pkg/agent/...
```

에러가 발생하면:
1. import 경로 확인
2. 타입 호환성 확인
3. 누락된 메서드 구현

### 5단계: 문서 작성

#### README.md

```bash
cat > sage-a2a-go/pkg/agent/README.md << 'EOF'
# SAGE Agent Framework

High-level framework for building SAGE protocol agents without directly importing sage.

## Features

- **Zero Sage Imports**: No direct sage package imports in agent code
- **83% Code Reduction**: 165 lines → 10 lines for initialization
- **Business Logic Focus**: Crypto/DID/HPKE handled by framework

## Quick Start

\```go
import "github.com/sage-x-project/sage-a2a-go/pkg/agent"

// Create agent from environment variables
agent, err := agent.NewAgentFromEnv(
    "payment",  // name
    "PAYMENT",  // env prefix
    true,       // HPKE enabled
    true,       // require signature
)
\```

See [DESIGN.md](../../docs/AGENT_FRAMEWORK_DESIGN.md) for detailed documentation.
EOF
```

#### examples/ 디렉토리

```bash
# 예시 코드 복사
cp sage-multi-agent/internal/agent/example_payment.go \
   sage-a2a-go/examples/payment_agent.go

# import 경로 수정
sed -i '' 's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' \
   sage-a2a-go/examples/payment_agent.go
```

### 6단계: 테스트 작성

```go
// sage-a2a-go/pkg/agent/keys/keys_test.go
package keys_test

import (
    "testing"
    "github.com/sage-x-project/sage-a2a-go/pkg/agent/keys"
)

func TestLoadFromJWKFile(t *testing.T) {
    // 테스트 구현
}
```

### 7단계: 버전 관리

```bash
cd sage-a2a-go
git add pkg/agent/
git commit -m "Add high-level agent framework

- Zero sage imports in agent code
- 83% code reduction (165 lines → 10 lines)
- Keys, session, DID, middleware, HPKE abstractions
- Environment-based configuration
- Comprehensive documentation

Migrated from sage-multi-agent prototype at
github.com/sage-x-project/sage-multi-agent/internal/agent
"
git tag v1.7.0
git push origin main --tags
```

## sage-multi-agent에서 Phase 2 진행

sage-a2a-go에서 이식 작업이 진행되는 동안, sage-multi-agent에서는 병렬로 Phase 2를 진행합니다.

### Phase 2: 기존 에이전트 리팩토링

#### Payment Agent 리팩토링

```go
// agents/payment/agent.go - Before
type PaymentAgent struct {
    RequireSignature bool
    logger           *log.Logger
    hpkeMgr          *session.Manager
    hpkeSrv          *hpke.Server
    hsrv             *sagehttp.HTTPServer
    mw               *server.DIDAuthMiddleware
    // ... 더 많은 필드
}

func (e *PaymentAgent) ensureHPKE() error {
    // 165 lines of initialization boilerplate
    sigPath := os.Getenv("PAYMENT_JWK_FILE")
    raw, _ := os.ReadFile(sigPath)
    signKP, _ := formats.NewJWKImporter().Import(raw, crypto.KeyFormatJWK)
    // ... 계속
}

// agents/payment/agent.go - After
import "github.com/sage-x-project/sage-multi-agent/internal/agent"

type PaymentAgent struct {
    agent  *agent.Agent
    logger *log.Logger
}

func NewPaymentAgent(requireSignature bool) (*PaymentAgent, error) {
    agent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, requireSignature)
    if err != nil {
        return nil, err
    }

    return &PaymentAgent{
        agent:  agent,
        logger: log.New(os.Stdout, "[payment] ", log.LstdFlags),
    }, nil
}
```

#### Medical Agent 리팩토링

동일한 패턴 적용:

```go
func NewMedicalAgent(requireSignature bool) (*MedicalAgent, error) {
    agent, err := agent.NewAgentFromEnv("medical", "MEDICAL", true, requireSignature)
    if err != nil {
        return nil, err
    }

    return &MedicalAgent{
        agent:  agent,
        logger: log.New(os.Stdout, "[medical] ", log.LstdFlags),
    }, nil
}
```

#### Root Agent HPKE 클라이언트 리팩토링

```go
// Root agent에서 HPKE 클라이언트 생성 시
transport := prototx.NewA2ATransport(...)
hpkeClient, err := r.agent.CreateHPKEClient(transport)
```

## 검증 체크리스트

### sage-a2a-go 이식 검증

- [ ] 모든 파일이 `pkg/agent/` 디렉토리에 복사됨
- [ ] import 경로가 sage-a2a-go로 변경됨
- [ ] `go build -o /dev/null ./pkg/agent/...` 성공
- [ ] 모든 `GetUnderlying()` 메서드 제거됨
- [ ] 주석 처리된 메서드 (SendHandshake, SendData 등) 구현됨
- [ ] README.md 작성됨
- [ ] 예시 코드 작성됨
- [ ] 테스트 코드 작성됨
- [ ] 문서 (DESIGN.md, MIGRATION.md) 복사됨
- [ ] 버전 태그 생성됨

### sage-multi-agent Phase 2 검증

- [ ] Payment agent 리팩토링 완료
- [ ] Medical agent 리팩토링 완료
- [ ] Root agent HPKE 클라이언트 리팩토링 완료
- [ ] 모든 에이전트 컴파일 성공
- [ ] 통합 테스트 통과
- [ ] 직접 sage import 0개로 감소 확인

## 예상 결과

### sage-a2a-go v1.7.0

```
pkg/agent/
├── README.md
├── agent.go
├── keys/
│   ├── keys.go
│   └── keys_test.go
├── session/
│   ├── session.go
│   └── session_test.go
├── did/
│   ├── did.go
│   ├── env.go
│   └── did_test.go
├── middleware/
│   ├── middleware.go
│   └── middleware_test.go
└── hpke/
    ├── hpke.go
    ├── transport.go
    └── hpke_test.go

examples/
└── payment_agent.go

docs/
├── AGENT_FRAMEWORK_DESIGN.md
└── AGENT_FRAMEWORK_MIGRATION_GUIDE.md
```

### sage-multi-agent (Phase 2 완료 후)

- Payment agent: 686 lines → ~120 lines (83% 감소)
- Medical agent: 690 lines → ~120 lines (83% 감소)
- Root agent: HPKE 클라이언트 생성 간소화
- **직접 sage import**: 6 files → 0 files (100% 제거)

## 문제 해결

### 컴파일 에러: import cycle

**증상**: `import cycle not allowed`

**해결**: 패키지 구조 확인, 순환 의존성 제거

### 타입 불일치

**증상**: `cannot use X as type Y`

**해결**: 타입 별칭 또는 인터페이스 확인, GetUnderlying() 사용 여부 점검

### 런타임 에러: nil pointer

**증상**: `panic: runtime error: invalid memory address`

**해결**: 초기화 순서 확인, nil 체크 추가

## 참고 자료

- [AGENT_FRAMEWORK_DESIGN.md](./AGENT_FRAMEWORK_DESIGN.md) - 설계 문서
- [internal/agent/example_payment.go](../internal/agent/example_payment.go) - 사용 예시
- [sage-a2a-go v1.6.0 Release Notes](https://github.com/sage-x-project/sage-a2a-go/releases/tag/v1.6.0)

## 연락처

이식 과정에서 문제가 발생하면 sage-multi-agent 저장소의 Issue를 생성하거나
프로토타입 코드를 참고하세요.
