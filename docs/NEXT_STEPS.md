# 다음 단계 작업 가이드

## 현재 상황 요약

### ✅ 완료된 작업

1. **프레임워크 프로토타입 구현** (커밋: `ece406a`)
   - `internal/agent/` 패키지 전체 구현
   - 문서 작성 (DESIGN.md, MIGRATION_GUIDE.md)
   - 컴파일 테스트 통과

2. **Phase 2 시작** (커밋: `da58fcb`)
   - `internal/a2autil/middleware.go` 리팩토링
   - 직접 sage import 2개 제거
   - 3개 에이전트가 간접적으로 프레임워크 사용 시작

### 📊 현재 sage import 현황

```
프로젝트 전체:
├─ internal/agent/*          → sage 사용 (프레임워크 내부에서만)
├─ internal/a2autil/*        → sage import 0개 ✅
├─ agents/payment/agent.go   → sage import 7개 ⚠️
├─ agents/medical/agent.go   → sage import 7개 ⚠️
├─ agents/planning/agent.go  → sage import ? ⚠️
├─ agents/root/agent.go      → sage import ? ⚠️
├─ protocol/a2a_transport.go → sage import ? ⚠️
└─ cmd/client/main.go        → sage import ? ⚠️
```

## 다음 단계별 작업 계획

### 📌 옵션 1: 병렬 작업 (권장)

당신이 다른 세션에서 sage-a2a-go 작업을 했다고 했으므로, 두 가지를 병렬로 진행:

#### A. sage-a2a-go 작업 (다른 세션에서 이미 진행?)
- `internal/agent/` 코드를 sage-a2a-go의 `pkg/agent/`로 이식
- MIGRATION_GUIDE.md의 단계별 가이드 참고
- 테스트 작성
- sage-a2a-go v1.7.0 릴리스

#### B. sage-multi-agent 작업 (이 프로젝트)
- sage-a2a-go v1.7.0이 나올 때까지 기다림
- 나온 후: `internal/agent` → `github.com/sage-x-project/sage-a2a-go/pkg/agent`로 import 변경
- 나머지 에이전트들 리팩토링

### 📌 옵션 2: 순차 작업 (단순)

sage-a2a-go 작업 없이 이 프로젝트에서만 계속 진행:

#### 단계 1: 간단한 것부터 - Root Agent HPKE 클라이언트
**난이도**: ⭐ (쉬움)
**예상 시간**: 30분
**영향**: Root agent의 HPKE 클라이언트 생성 부분만 교체

**작업 내용**:
```go
// agents/root/agent.go에서

// Before (현재):
import "github.com/sage-x-project/sage/pkg/agent/hpke"
cli := hpke.NewClient(t, r.resolver, r.myKey, clientDID, hpke.DefaultInfoBuilder{}, sMgr)

// After (프레임워크 사용):
import "github.com/sage-x-project/sage-multi-agent/internal/agent"
// Root agent에 agent 필드 추가
hpkeClient, err := r.agent.CreateHPKEClient(transport)
```

**결과**: Root agent에서 sage/pkg/agent/hpke import 제거

---

#### 단계 2: 중간 난이도 - Planning Agent 검토
**난이도**: ⭐⭐ (중간)
**예상 시간**: 1시간
**영향**: Planning agent 구조 파악 및 리팩토링 여부 결정

**작업 내용**:
1. `agents/planning/agent.go`에서 sage import 확인
2. Payment/Medical과 같은 구조인지 확인
3. 간단히 교체 가능한 부분이 있는지 파악

---

#### 단계 3: 복잡함 - Payment/Medical Agent 설계 결정 필요
**난이도**: ⭐⭐⭐ (어려움)
**예상 시간**: 2-4시간
**영향**: 가장 큰 sage import 제거 효과

**문제점**: Payment와 Medical agent는 **lazy HPKE initialization** 패턴 사용
```go
// 현재 구조:
type PaymentAgent struct {
    hpkeMgr *session.Manager  // nil로 시작
    hpkeSrv *hpke.Server      // nil로 시작
    hpkeMu  sync.Mutex        // lazy init lock
}

// 첫 HPKE 요청 시 ensureHPKE() 호출
func (e *PaymentAgent) ensureHPKE() error {
    e.hpkeMu.Lock()
    defer e.hpkeMu.Unlock()

    if e.hpkeSrv != nil {
        return nil  // 이미 초기화됨
    }

    // 165 lines of initialization...
}
```

**선택지**:

##### 선택 A: Lazy 패턴 유지 (더 복잡)
프레임워크에 lazy initialization 기능 추가:
```go
// internal/agent/agent.go에 추가
func (a *Agent) EnableHPKELazily() error {
    // HPKE 서버 lazy 초기화 로직
}
```

**장점**:
- 메모리 효율적 (HPKE 사용 안 하면 초기화 안 함)
- 기존 동작 방식 유지

**단점**:
- 프레임워크 복잡도 증가
- 추가 구현 필요

##### 선택 B: Eager 패턴으로 전환 (더 간단)
에이전트 생성 시 HPKE 무조건 초기화:
```go
func NewPaymentAgent(requireSignature bool) (*PaymentAgent, error) {
    agent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, requireSignature)
    // HPKE가 무조건 초기화됨

    return &PaymentAgent{
        agent: agent,
        // ... 나머지 필드
    }, nil
}
```

**장점**:
- 현재 프레임워크 그대로 사용 가능
- 코드 단순화

**단점**:
- 항상 HPKE 초기화 (메모리 사용 증가)
- Lazy loading의 이점 상실

**권장**: **선택 B (Eager 패턴)**
- 실제 production에서 HPKE는 거의 항상 사용됨
- 메모리 차이 미미 (키 2개 로딩 정도)
- 코드 단순성이 더 중요

---

#### 단계 4: protocol/a2a_transport.go 검토
**난이도**: ⭐⭐ (중간)
**예상 시간**: 1시간
**영향**: Transport wrapper 개선

**작업 내용**:
1. 현재 sage import 확인
2. 프레임워크로 교체 가능한지 검토
3. 필요시 프레임워크에 추가 기능 구현

## 🎯 추천 작업 순서

### 가장 빠른 성과를 원한다면:

```
1단계: Root Agent HPKE 클라이언트 (30분) ✅ 즉시 가능
   ↓
2단계: Planning Agent 검토 (1시간) ✅ 비교적 쉬움
   ↓
3단계: Payment/Medical 설계 결정 → Eager 패턴 선택 (2시간)
   ↓
4단계: protocol/a2a_transport 검토 (1시간)
```

### 가장 안전한 방법을 원한다면:

```
1단계: sage-a2a-go 이식 완료까지 대기
   ↓
2단계: sage-a2a-go v1.7.0 사용으로 전환
   ↓
3단계: 모든 에이전트를 새 프레임워크로 전환
```

## 📝 각 단계별 상세 가이드

### 🔷 단계 1: Root Agent HPKE 클라이언트 교체

#### 1.1 현재 상태 확인
```bash
grep -n "hpke.NewClient" agents/root/agent.go
```

#### 1.2 Root Agent에 framework 통합
```go
// agents/root/agent.go

// 구조체에 필드 추가
type RootAgent struct {
    name string
    port int
    // ... 기존 필드들

    // 추가: 프레임워크 agent
    frameworkAgent *agent.Agent  // 새로 추가
}

// NewRootAgent 수정
func NewRootAgent(name string, port int) *RootAgent {
    // 프레임워크 agent 생성 (HPKE 클라이언트용)
    fwAgent, err := agent.NewAgentFromEnv(
        "root",
        "ROOT",
        false,  // HPKE 서버는 필요 없음 (클라이언트만)
        false,  // 서명도 필요 없음
    )
    if err != nil {
        log.Printf("[root] Failed to create framework agent: %v", err)
    }

    return &RootAgent{
        name: name,
        port: port,
        frameworkAgent: fwAgent,
        // ... 기존 필드들
    }
}
```

#### 1.3 HPKE 클라이언트 생성 교체
```go
// ensureHPKEForTarget 함수에서

// Before:
cli := hpke.NewClient(t, r.resolver, r.myKey, clientDID, hpke.DefaultInfoBuilder{}, sMgr)

// After:
cli, err := r.frameworkAgent.CreateHPKEClient(t)
if err != nil {
    return fmt.Errorf("create HPKE client: %w", err)
}
```

#### 1.4 테스트 및 커밋
```bash
go build -o /dev/null ./agents/root/...
git add agents/root/agent.go
git commit -m "refactor: Use framework for Root agent HPKE client"
```

---

### 🔷 단계 3: Payment Agent Eager 패턴 전환 (예시)

#### 3.1 구조체 단순화
```go
// agents/payment/agent.go

type PaymentAgent struct {
    RequireSignature bool

    // 프레임워크 사용
    agent *agent.Agent  // 새로 추가

    logger *log.Logger

    // HPKE 관련 필드 제거 (agent에 포함됨)
    // hpkeMgr *session.Manager  ← 삭제
    // hpkeSrv *hpke.Server      ← 삭제
    // hsrv    *sagehttp.HTTPServer ← 삭제
    // hpkeMu  sync.Mutex        ← 삭제

    // HTTP 관련은 유지
    openMux *http.ServeMux
    protMux *http.ServeMux
    handler http.Handler
    httpSrv *http.Server

    llmClient llm.Client
}
```

#### 3.2 초기화 간소화
```go
func NewPaymentAgent(requireSignature bool) (*PaymentAgent, error) {
    // 프레임워크 agent 생성 (HPKE 자동 초기화)
    fwAgent, err := agent.NewAgentFromEnv(
        "payment",
        "PAYMENT",
        true,  // HPKE 활성화
        requireSignature,
    )
    if err != nil {
        return nil, fmt.Errorf("create framework agent: %w", err)
    }

    pa := &PaymentAgent{
        RequireSignature: requireSignature,
        agent:           fwAgent,
        logger:          log.New(os.Stdout, "[payment] ", log.LstdFlags),
    }

    // Open mux 설정
    pa.openMux = http.NewServeMux()
    pa.openMux.HandleFunc("/status", pa.statusHandler)

    // Protected mux 설정
    pa.protMux = http.NewServeMux()
    pa.protMux.HandleFunc("/payment/process", pa.processHandler)

    // ... 나머지 설정

    return pa, nil
}
```

#### 3.3 ensureHPKE() 제거
```go
// ensureHPKE() 함수 전체 삭제 (165 lines)
// HPKE는 이미 agent 생성 시 초기화됨
```

#### 3.4 HPKE 사용 부분 수정
```go
func (e *PaymentAgent) processHandler(w http.ResponseWriter, r *http.Request) {
    // Before:
    // if err := e.ensureHPKE(); err != nil { ... }

    // After:
    // HPKE는 이미 초기화됨, 바로 사용
    if isHPKE(r) {
        // e.hsrv 대신 e.agent.GetHTTPServer() 사용
        e.agent.GetHTTPServer().MessagesHandler().ServeHTTP(w, r)
        return
    }

    // ... 나머지 로직
}
```

## 💡 결정해야 할 사항

다음 중 하나를 선택해주세요:

### A. 빠른 진행 (권장)
→ **단계 1 (Root Agent)부터 시작**
- 바로 실행 가능
- 30분 내 완료
- 즉시 성과 확인

### B. 큰 그림 우선
→ **Payment/Medical 설계 결정 먼저**
- Lazy vs Eager 패턴 결정
- 전체 구조 확정 후 작업

### C. 외부 작업 대기
→ **sage-a2a-go 이식 완료까지 대기**
- 안전하지만 시간 소요
- 이식 완료 후 한 번에 전환

## ❓ 질문

다음 단계로 넘어가기 전에 답변해주세요:

1. **sage-a2a-go 작업 상태**는?
   - [ ] 완료됨 → `internal/agent` 삭제하고 sage-a2a-go import로 전환
   - [ ] 진행 중 → 완료까지 대기
   - [ ] 안 했음 → 이 프로젝트에서만 계속 진행

2. **Payment/Medical의 Lazy HPKE**를 어떻게 처리?
   - [ ] Eager로 전환 (간단, 권장)
   - [ ] Lazy 유지 (복잡, 프레임워크 수정 필요)
   - [ ] 나중에 결정

3. **다음 작업 우선순위**는?
   - [ ] Root Agent부터 (30분, 쉬움)
   - [ ] Planning Agent 검토 (1시간, 중간)
   - [ ] 전체 설계 결정 먼저

답변 주시면 그에 맞는 구체적인 작업을 진행하겠습니다!
