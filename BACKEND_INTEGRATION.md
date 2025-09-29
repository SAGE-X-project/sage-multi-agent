# SAGE Multi-Agent 백엔드 연동 가이드

##  개요

이 문서는 `sage-fe` 프론트엔드와 `sage-multi-agent` 백엔드 간의 통신 및 SAGE 프로토콜 연동에 대한 상세 가이드입니다.

##  아키텍처

### 통신 구조
```
Frontend (Next.js) 
    ↓ HTTP POST
API Route (/api/send-prompt)
    ↓ HTTP POST + Headers
Backend Client (port 8086)
    ↓ A2A Protocol
Root Agent (port 8080)
    ↓ WebSocket
WebSocket Server (port 8085)
    ↓ Real-time logs
Frontend (WebSocket Client)
```

##  프론트엔드 변경사항

### 1. 환경변수 설정 (`.env.local`)
```env
# Backend API Configuration
NEXT_PUBLIC_API_URL=http://localhost:8086
NEXT_PUBLIC_API_ENDPOINT=/send/prompt

# WebSocket Configuration  
NEXT_PUBLIC_WS_URL=ws://localhost:8085
NEXT_PUBLIC_WS_ENDPOINT=/ws

# WebSocket Settings
NEXT_PUBLIC_WS_RECONNECT_INTERVAL=1000
NEXT_PUBLIC_WS_MAX_RECONNECT_ATTEMPTS=5
NEXT_PUBLIC_WS_HEARTBEAT_INTERVAL=30000

# Feature Flags
NEXT_PUBLIC_ENABLE_SAGE_PROTOCOL=true
NEXT_PUBLIC_ENABLE_REALTIME_LOGS=true
```

### 2. WebSocket 연결 관리 시스템

#### WebSocketManager 클래스
- **위치**: `/src/lib/websocket/WebSocketManager.ts`
- **기능**:
  - 자동 재연결 (exponential backoff)
  - 하트비트 메커니즘
  - 메시지 큐잉
  - 에러 핸들링 및 복구
  - 상태 관리 (연결/끊김/재연결/에러)

#### useWebSocket 훅
- **위치**: `/src/hooks/useWebSocket.ts`
- **기능**:
  - React 컴포넌트에서 WebSocket 사용
  - 자동 연결/해제
  - 이벤트 핸들러 관리

### 3. SAGE 프로토콜 통합

#### API 요청 구조
```typescript
interface PromptRequest {
  prompt: string;
  sageEnabled?: boolean;  // SAGE 프로토콜 활성화 여부
  scenario?: "accommodation" | "delivery" | "payment";
  metadata?: {
    userId?: string;
    sessionId?: string;
    timestamp?: string;
  };
}
```

#### HTTP 헤더 추가
```typescript
headers: {
  "Content-Type": "application/json",
  "X-SAGE-Enabled": "true",  // SAGE 활성화 시
  "X-Scenario": "accommodation"  // 시나리오 정보
}
```

### 4. 에러 처리 강화

- **연결 실패 감지**: 백엔드 서버 미실행 시 명확한 에러 메시지
- **자동 재연결**: WebSocket 연결 끊김 시 자동 재시도
- **사용자 알림**: 연결 상태 UI 표시
- **에러 로깅**: 상세한 에러 정보 콘솔 출력

## 🔌 백엔드 연동 요구사항

### 1. WebSocket 서버 (포트 8085)

백엔드에서 제공해야 할 WebSocket 메시지 형식:

```go
type WebSocketMessage struct {
    Type      string      `json:"type"`      // "log", "error", "status", "heartbeat"
    Payload   interface{} `json:"payload"`   
    Timestamp string      `json:"timestamp"`
}

type AgentLog struct {
    Type           string `json:"type"`      // "routing", "planning", "ordering", "gateway", "sage", "error"
    From           string `json:"from"`      
    To             string `json:"to"`        
    Content        string `json:"content"`   
    Timestamp      string `json:"timestamp"`
    MessageId      string `json:"messageId,omitempty"`
    OriginalPrompt string `json:"originalPrompt,omitempty"`
    TamperedPrompt string `json:"tamperedPrompt,omitempty"`
}
```

### 2. HTTP API 엔드포인트 (포트 8086)

#### 요청 처리
```go
// client/main.go 수정 필요
func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
    // 1. SAGE 활성화 여부 확인
    sageEnabled := r.Header.Get("X-SAGE-Enabled") == "true"
    scenario := r.Header.Get("X-Scenario")
    
    // 2. 요청 본문 파싱
    var req PromptRequest
    json.NewDecoder(r.Body).Decode(&req)
    
    // 3. SAGE 모드에 따른 처리 분기
    if sageEnabled {
        // SAGE 프로토콜을 사용한 에이전트 통신
        // RFC-9421 서명 검증 활성화
    } else {
        // 일반 모드 (서명 검증 없음)
    }
    
    // 4. 응답에 로그 및 검증 결과 포함
    response := PromptResponse{
        Response: agentResponse,
        Logs: collectedLogs,
        SageVerification: verificationResult,
    }
}
```

### 3. SAGE 프로토콜 구현

#### RFC-9421 HTTP Message Signatures 사용
```go
// sage/request_handler.go 활용
type SageHttpRequestHandler struct {
    verifier   *rfc9421.HTTPVerifier
    agentDID   string
    privateKey ed25519.PrivateKey
}

// 서명 생성
func (h *SageHttpRequestHandler) SignRequest(req *http.Request) error {
    params := &rfc9421.SignatureInputParams{
        CoveredComponents: []string{
            `"@method"`,
            `"@path"`,
            `"content-type"`,
            `"date"`,
            `"x-agent-did"`,
        },
        KeyID:     h.agentDID,
        Algorithm: "ed25519",
        Created:   time.Now().Unix(),
    }
    return h.verifier.SignRequest(req, "sig1", params, h.privateKey)
}

// 서명 검증
func (h *SageHttpRequestHandler) VerifyRequest(req *http.Request) error {
    publicKey := h.getPublicKeyForAgent(req.Header.Get("X-Agent-DID"))
    return h.verifier.VerifyRequest(req, "sig1", publicKey)
}
```

### 4. Gateway 모드 처리

SAGE OFF 시 악의적 게이트웨이 시뮬레이션:

```go
// gateway/malicious_gateway.go 활용
if !sageEnabled && scenario != "" {
    // 데모용 메시지 변조 시뮬레이션
    switch scenario {
    case "accommodation":
        // 숙소 추천 변조
    case "delivery":
        // 배송지 변조
    case "payment":
        // 결제 정보 변조
    }
}
```

##  데이터 흐름

### 1. 사용자 요청 (SAGE ON)
```
User Input → Frontend → API Route → Backend Client
    ↓
Root Agent (SAGE 서명 생성)
    ↓
Planning/Ordering Agent (서명 검증)
    ↓
Response (검증 성공) → Frontend
```

### 2. 사용자 요청 (SAGE OFF)
```
User Input → Frontend → API Route → Backend Client
    ↓
Root Agent → Gateway (메시지 변조)
    ↓
Planning/Ordering Agent (변조된 메시지 처리)
    ↓
Response (위험 경고 없음) → Frontend
```

##  실행 방법

### 1. 백엔드 서버 시작
```bash
# Root Agent (포트 8080)
cd sage-multi-agent
go run cli/root/main.go --ws-port 8085

# Client Server (포트 8086)
go run client/main.go --port 8086 --root-url http://localhost:8080

# Sub-agents
go run cli/ordering/main.go --port 8083
go run cli/planning/main.go --port 8084
```

### 2. 프론트엔드 시작
```bash
cd sage-fe
npm install
npm run dev
```

##  테스트 체크리스트

- [ ] WebSocket 연결 확인 (포트 8085)
- [ ] 실시간 로그 수신 확인
- [ ] SAGE ON 모드 동작 확인
- [ ] SAGE OFF 모드 동작 확인
- [ ] 시나리오별 데모 동작 확인
- [ ] 에러 처리 및 재연결 확인
- [ ] 백엔드 미실행 시 에러 메시지 확인

##  추가 개발 필요사항

### 백엔드 (sage-multi-agent)
1. WebSocket 메시지 포맷 통일
2. Agent 간 통신 로그 수집 및 전송
3. SAGE 서명 검증 결과 응답에 포함
4. 시나리오별 데모 로직 구현

### 프론트엔드 (sage-fe)
1. ~~WebSocket 연결 관리~~ 
2. ~~SAGE 상태 전달~~ 
3. ~~에러 처리 강화~~ 
4. ~~환경변수 설정~~ 

##  보안 고려사항

1. **DID 관리**: 각 에이전트의 DID를 안전하게 관리
2. **키 관리**: Ed25519 개인키를 안전한 저장소에 보관
3. **서명 검증**: 모든 에이전트 간 통신에서 서명 검증 수행
4. **타임스탬프 검증**: 재생 공격 방지를 위한 타임스탬프 확인

## 📚 참고 자료

- [RFC 9421 - HTTP Message Signatures](https://datatracker.ietf.org/doc/html/rfc9421)
- [SAGE Protocol Documentation](../sage/docs/)
- [A2A Protocol Documentation](https://github.com/trpc-group/trpc-a2a-go)