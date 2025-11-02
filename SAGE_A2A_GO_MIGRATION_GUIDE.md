# sage-a2a-go v1.7.0 Agent Framework 적용 가이드

## 📋 목차

1. [개요](#개요)
2. [현재 상태 분석](#현재-상태-분석)
3. [마이그레이션 전략](#마이그레이션-전략)
4. [단계별 적용 방법](#단계별-적용-방법)
5. [코드 비교 예제](#코드-비교-예제)
6. [테스트 및 검증](#테스트-및-검증)
7. [트러블슈팅](#트러블슈팅)

---

## 개요

### 목적

sage-multi-agent 프로젝트에서 현재 사용 중인 `internal/agent` 프레임워크를 **sage-a2a-go v1.7.0**의 공식 Agent Framework로 마이그레이션합니다.

### 주요 변경 사항

| 구분 | Before (internal/agent) | After (sage-a2a-go) |
|------|------------------------|---------------------|
| **Import 경로** | `github.com/sage-x-project/sage-multi-agent/internal/agent` | `github.com/sage-x-project/sage-a2a-go/pkg/agent/framework` |
| **패키지 위치** | Private (internal) | Public (pkg) |
| **유지보수** | sage-multi-agent 로컬 | sage-a2a-go 공식 라이브러리 |
| **버전 관리** | 없음 | v1.7.0+ 시맨틱 버저닝 |
| **테스트 커버리지** | 제한적 | 57.1% (52개 테스트) |

### 기대 효과

✅ **코드 중복 제거**: 동일한 코드를 두 곳에서 유지보수할 필요 없음
✅ **공식 지원**: sage-a2a-go 팀의 직접 관리 및 업데이트
✅ **테스트 보장**: 52개의 종합 테스트로 품질 보증
✅ **일관성**: 모든 SAGE 프로젝트가 동일한 프레임워크 사용

---

## 현재 상태 분석

### sage-multi-agent의 internal/agent 구조

```
internal/agent/
├── agent.go           # 메인 Agent 타입 및 초기화
├── keys/
│   └── keys.go        # 키 로딩 및 관리
├── session/
│   └── session.go     # HPKE 세션 관리
├── did/
│   ├── did.go         # DID resolver
│   └── env.go         # 환경 변수 설정
├── middleware/
│   └── middleware.go  # HTTP DID 인증
└── hpke/
    ├── hpke.go        # HPKE 클라이언트/서버
    └── transport.go   # Transport 타입
```

### 현재 사용 중인 에이전트

| 에이전트 | 파일 | Framework 사용 |
|---------|------|---------------|
| **Payment** | `agents/payment/agent.go` | ✅ 사용 중 |
| **Medical** | `agents/medical/agent.go` | ✅ 사용 중 |
| **Root** | `agents/root/agent.go` | ⚠️ 부분 사용 (HPKE 클라이언트만) |
| **Planning** | `agents/planning/agent.go` | ❌ 미사용 |

---

## 마이그레이션 전략

### Phase 1: 준비 (1-2일)

1. ✅ **sage-a2a-go v1.7.0 테스트 완료** (완료)
2. ✅ **Ethereum 환경 확인** (완료)
3. ✅ **컨트랙트 배포 검증** (완료)
4. 📝 **마이그레이션 가이드 작성** (진행 중)

### Phase 2: 의존성 전환 (1일)

1. `go.mod`에 sage-a2a-go v1.7.0 추가
2. Import 경로 변경
3. 컴파일 오류 수정

### Phase 3: 에이전트 리팩토링 (2-3일)

1. Payment Agent 리팩토링
2. Medical Agent 리팩토링
3. Root Agent 리팩토링 (HPKE 클라이언트)

### Phase 4: 테스트 및 검증 (1-2일)

1. 단위 테스트 실행
2. 통합 테스트 실행
3. E2E 시나리오 테스트

### Phase 5: 정리 (1일)

1. `internal/agent` 디렉토리 제거 (또는 deprecated 표시)
2. 문서 업데이트
3. CHANGELOG 작성

**총 예상 기간**: 5-9일

---

## 단계별 적용 방법

### Step 1: go.mod 업데이트

```bash
cd /Users/kevin/work/github/sage-x-project/demo/sage-multi-agent

# sage-a2a-go 버전 확인
cd ../sage-a2a-go
git tag | grep v1.7.0

# sage-multi-agent로 돌아와서 의존성 추가
cd ../sage-multi-agent
go get github.com/sage-x-project/sage-a2a-go@v1.7.0
go mod tidy
```

### Step 2: Import 경로 일괄 변경

```bash
# 모든 Go 파일에서 import 경로 변경
find . -name "*.go" -type f -exec sed -i '' \
  's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent/framework|g' {} +

# 변경 확인
git diff
```

### Step 3: 컴파일 검증

```bash
# 컴파일 시도
go build ./...

# 에러가 있다면 하나씩 수정
# 주요 체크 포인트:
# 1. 타입 호환성
# 2. 메서드 시그니처
# 3. 패키지 구조
```

---

## 코드 비교 예제

### Example 1: Payment Agent 초기화

#### Before (internal/agent)

```go
package payment

import (
    "github.com/sage-x-project/sage-multi-agent/internal/agent"
    "github.com/sage-x-project/sage-a2a-go/pkg/server"
)

type PaymentAgent struct {
    RequireSignature bool
    logger           *log.Logger
    agent            *agent.Agent  // internal framework
    mw               *server.DIDAuthMiddleware
    // ... other fields
}

func NewPaymentAgent(requireSignature bool) (*PaymentAgent, error) {
    logger := log.New(os.Stdout, "[payment] ", log.LstdFlags)

    // Create framework agent (Eager pattern)
    fwAgent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, requireSignature)
    if err != nil {
        logger.Printf("[payment] Framework agent init failed: %v", err)
        fwAgent = nil // graceful degradation
    }

    pa := &PaymentAgent{
        RequireSignature: requireSignature,
        logger:           logger,
        agent:            fwAgent,
    }

    // ... DID middleware setup
    return pa, nil
}
```

#### After (sage-a2a-go)

```go
package payment

import (
    "github.com/sage-x-project/sage-a2a-go/pkg/agent/framework"
    "github.com/sage-x-project/sage-a2a-go/pkg/server"
)

type PaymentAgent struct {
    RequireSignature bool
    logger           *log.Logger
    agent            *framework.Agent  // sage-a2a-go framework
    mw               *server.DIDAuthMiddleware
    // ... other fields
}

func NewPaymentAgent(requireSignature bool) (*PaymentAgent, error) {
    logger := log.New(os.Stdout, "[payment] ", log.LstdFlags)

    // Create framework agent - API 동일
    fwAgent, err := framework.NewAgentFromEnv("payment", "PAYMENT", true, requireSignature)
    if err != nil {
        logger.Printf("[payment] Framework agent init failed: %v", err)
        fwAgent = nil // graceful degradation
    }

    pa := &PaymentAgent{
        RequireSignature: requireSignature,
        logger:           logger,
        agent:            fwAgent,
    }

    // ... DID middleware setup (변경 없음)
    return pa, nil
}
```

**변경 사항**: Import 경로만 변경, 나머지 코드는 동일!

---

### Example 2: HPKE 사용

#### Before (internal/agent)

```go
import "github.com/sage-x-project/sage-multi-agent/internal/agent"

func (pa *PaymentAgent) handleHPKEMessage(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
    // HPKE 서버 접근
    hpkeServer := pa.agent.GetHPKEServer()
    if hpkeServer == nil {
        return nil, fmt.Errorf("HPKE not initialized")
    }

    // 메시지 처리
    return hpkeServer.GetUnderlying().HandleMessage(ctx, msg)
}
```

#### After (sage-a2a-go)

```go
import "github.com/sage-x-project/sage-a2a-go/pkg/agent/framework"

func (pa *PaymentAgent) handleHPKEMessage(ctx context.Context, msg *transport.SecureMessage) (*transport.Response, error) {
    // HPKE 서버 접근 - 동일한 API
    hpkeServer := pa.agent.GetHPKEServer()
    if hpkeServer == nil {
        return nil, fmt.Errorf("HPKE not initialized")
    }

    // 메시지 처리 - 동일한 API
    return hpkeServer.GetUnderlying().HandleMessage(ctx, msg)
}
```

**변경 사항**: Import 경로만 변경!

---

### Example 3: Root Agent HPKE 클라이언트

#### Before (internal/agent)

```go
import (
    "github.com/sage-x-project/sage-multi-agent/internal/agent"
    "github.com/sage-x-project/sage-multi-agent/internal/agent/hpke"
)

type RootAgent struct {
    // ... fields
    hpkeClient *sagehpke.Client  // Directly using sage
}

func (r *RootAgent) initHPKE() error {
    // Manual initialization - complex code
    transport := prototx.NewA2ATransport(...)

    // Load keys
    sigPath := os.Getenv("ROOT_JWK_FILE")
    raw, _ := os.ReadFile(sigPath)
    signKP, _ := formats.NewJWKImporter().Import(raw, crypto.KeyFormatJWK)

    // Create session manager
    sessionMgr := session.NewManager()

    // Create resolver
    resolver, _ := dideth.NewEthereumClient(...)

    // Finally create client
    r.hpkeClient = sagehpke.NewClient(
        transport,
        resolver,
        signKP,
        string(r.myDID),
        sagehpke.DefaultInfoBuilder{},
        sessionMgr,
    )

    return nil
}
```

#### After (sage-a2a-go)

```go
import "github.com/sage-x-project/sage-a2a-go/pkg/agent/framework"

type RootAgent struct {
    // ... fields
    agent *framework.Agent  // Use framework
}

func (r *RootAgent) initHPKE() error {
    // One-liner initialization!
    var err error
    r.agent, err = framework.NewAgentFromEnv("root", "ROOT", true, true)
    if err != nil {
        return fmt.Errorf("init agent: %w", err)
    }

    // HPKE client ready to use
    // Access via r.agent.CreateHPKEClient() when needed
    return nil
}

func (r *RootAgent) sendEncrypted(ctx context.Context, targetDID string, payload []byte) error {
    // Create HPKE client with transport
    transport := prototx.NewA2ATransport(...)
    hpkeClient, err := r.agent.CreateHPKEClient(transport)
    if err != nil {
        return err
    }

    // Use client
    return hpkeClient.GetUnderlying().SendHandshake(ctx, targetDID, payload)
}
```

**코드 감소**: ~165 lines → ~10 lines (94% 감소)

---

## 테스트 및 검증

### 1. 컴파일 테스트

```bash
# 전체 빌드
go build ./...

# 각 에이전트 개별 빌드
go build ./cmd/payment
go build ./cmd/medical
go build ./cmd/root
go build ./cmd/planning
go build ./cmd/client
```

### 2. 단위 테스트

```bash
# 전체 테스트
go test ./...

# 에이전트별 테스트
go test ./agents/payment/...
go test ./agents/medical/...
go test ./agents/root/...

# 커버리지 확인
go test -cover ./agents/...
```

### 3. 통합 테스트

```bash
# Ethereum 노드 실행 확인
lsof -i :8545

# 컨트랙트 배포 확인
curl -X POST http://127.0.0.1:8545 \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_getCode","params":["0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512","latest"],"id":1}'

# 에이전트 실행 테스트
./demo_SAGE.sh --tamper --hpke on
```

### 4. E2E 시나리오 테스트

```bash
# Payment 시나리오
curl -X POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H 'X-SAGE-Enabled: true' \
  -H 'X-Scenario: payment' \
  -d '{"prompt":"send 5 usdc to bob"}'

# Medical 시나리오
curl -X POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H 'X-SAGE-Enabled: true' \
  -H 'X-Scenario: medical' \
  -d '{"prompt":"what are my medications?"}'
```

---

## 트러블슈팅

### Issue 1: Import 오류

**증상**:
```
package github.com/sage-x-project/sage-multi-agent/internal/agent:
cannot find package
```

**해결**:
```bash
# Import 경로 변경 확인
grep -r "internal/agent" . --include="*.go"

# 놓친 파일 수동 수정
# internal/agent → sage-a2a-go/pkg/agent/framework
```

---

### Issue 2: 타입 불일치

**증상**:
```
cannot use agent (type *framework.Agent) as type *agent.Agent
```

**해결**:
```go
// Before
import "github.com/sage-x-project/sage-multi-agent/internal/agent"
type MyAgent struct {
    agent *agent.Agent
}

// After
import "github.com/sage-x-project/sage-a2a-go/pkg/agent/framework"
type MyAgent struct {
    agent *framework.Agent  // 타입 변경
}
```

---

### Issue 3: 환경 변수 누락

**증상**:
```
Framework agent init failed: environment variable PAYMENT_JWK_FILE is not set
```

**해결**:
```bash
# 환경 변수 설정 확인
cat .env

# 필요한 변수 추가
export PAYMENT_JWK_FILE="keys/external.secp256k1.jwk"
export PAYMENT_KEM_JWK_FILE="keys/kem/external.x25519.jwk"
export PAYMENT_DID="did:sage:local:external"
```

---

### Issue 4: Ethereum 노드 연결 실패

**증상**:
```
create registry client: failed to create AgentCard client:
failed to get network ID: Internal error
```

**해결**:
```bash
# Hardhat 노드 재시작
pkill -f "hardhat node"
cd /path/to/sage/contracts/ethereum
npx hardhat node --port 8545 --chain-id 31337 &

# 컨트랙트 재배포
npx hardhat run scripts/deploy-agentcard.js --network localhost

# 배포 확인
curl -X POST http://127.0.0.1:8545 \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_getCode","params":["0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512","latest"],"id":1}'
```

---

### Issue 5: HPKE 초기화 실패

**증상**:
```
Framework agent init failed: load KEM key: file not found
```

**해결**:
```bash
# KEM 키 존재 확인
ls -la keys/kem/*.jwk

# 키가 없으면 생성
go run tools/keygen/gen_agents_key.go --name external --output keys/

# 키 경로 확인
export PAYMENT_KEM_JWK_FILE="keys/kem/external.x25519.jwk"
```

---

## 체크리스트

### 마이그레이션 전

- [ ] sage-a2a-go v1.7.0 테스트 완료 (52/52 PASS)
- [ ] Hardhat 노드 실행 중
- [ ] AgentCardRegistry 배포 완료 (0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512)
- [ ] 환경 변수 설정 확인
- [ ] 키 파일 존재 확인

### 마이그레이션 중

- [ ] go.mod에 sage-a2a-go v1.7.0 추가
- [ ] Import 경로 일괄 변경
- [ ] Payment Agent 리팩토링
- [ ] Medical Agent 리팩토링
- [ ] Root Agent 리팩토링
- [ ] Planning Agent 확인
- [ ] 컴파일 성공

### 마이그레이션 후

- [ ] 단위 테스트 전체 PASS
- [ ] 통합 테스트 PASS
- [ ] E2E 시나리오 테스트 PASS
- [ ] `internal/agent` 디렉토리 정리
- [ ] 문서 업데이트
- [ ] CHANGELOG 작성
- [ ] Git 커밋 및 태그

---

## 참고 자료

### sage-a2a-go v1.7.0 문서

- [Agent Framework README](https://github.com/sage-x-project/sage-a2a-go/blob/main/pkg/agent/framework/README.md)
- [Example: Payment Agent](https://github.com/sage-x-project/sage-a2a-go/blob/main/examples/framework/payment_agent.go)
- [CHANGELOG v1.7.0](https://github.com/sage-x-project/sage-a2a-go/blob/main/CHANGELOG.md#170---2025-11-02)
- [Migration Guide](https://github.com/sage-x-project/sage-a2a-go/blob/main/AGENT_FRAMEWORK_MIGRATION_GUIDE.md)

### sage-multi-agent 문서

- [BACKEND_INTEGRATION.md](./BACKEND_INTEGRATION.md)
- [SAGE_A2A_USAGE_REPORT.md](./SAGE_A2A_USAGE_REPORT.md)
- [README.md](./README.md)

---

## 지원

질문이나 문제가 있으면:

1. GitHub Issues 생성: [sage-a2a-go](https://github.com/sage-x-project/sage-a2a-go/issues)
2. GitHub Discussions 참여
3. 프로젝트 문서 참조

---

**작성일**: 2025-11-03
**버전**: sage-a2a-go v1.7.0
**대상**: sage-multi-agent 마이그레이션
