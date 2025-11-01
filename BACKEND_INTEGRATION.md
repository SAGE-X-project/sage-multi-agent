# SAGE Multi‑Agent 백엔드 연동 가이드

본 문서는 `sage-fe` 프론트엔드와 본 레포(`sage-multi-agent`) 백엔드를 연동하는 방법과, SAGE 프로토콜(A2A 서명, DID 검증, HPKE)을 데모 옵션에 따라 체험하는 방법을 설명합니다.

## 아키텍처 개요

```
Frontend
  ↓ HTTP POST (/api/request)
Client API (:8086)
  ↓ HTTP POST (/process)
Root (:18080, in‑proc Planning/Medical/Payment)
  ↓ HTTP POST
Gateway (:5500, tamper/pass)
  ↓ HTTP POST
External Payment (:19083, DID 미들웨어로 서명 검증, HPKE 수신)
```

선택적으로 WebSocket 로그 서버를 붙일 수 있습니다(`websocket/enhanced_server.go`). 기본 실행 흐름에는 포함되어 있지 않습니다.

## 프론트엔드 변경사항

### 1) 환경 변수 예시 (`.env.local`)

```env
# Backend API endpoint
NEXT_PUBLIC_API_URL=http://localhost:8086
NEXT_PUBLIC_API_ENDPOINT=/api/request

# Optional WebSocket (if enabled)
NEXT_PUBLIC_WS_URL=ws://localhost:8085
NEXT_PUBLIC_WS_ENDPOINT=/ws
NEXT_PUBLIC_WS_RECONNECT_INTERVAL=1000
NEXT_PUBLIC_WS_MAX_RECONNECT_ATTEMPTS=5
NEXT_PUBLIC_WS_HEARTBEAT_INTERVAL=30000

# Feature flags
NEXT_PUBLIC_ENABLE_SAGE_PROTOCOL=true
NEXT_PUBLIC_ENABLE_REALTIME_LOGS=false
```

### 2) WebSocket(선택)

`websocket/enhanced_server.go`를 사용해 /ws, /health, /stats 엔드포인트를 제공할 수 있습니다. 기본 데모에는 필수는 아닙니다.

### 3) SAGE 프로토콜 통합

#### 요청 바디 + 헤더

```typescript
interface PromptRequest {
  prompt: string;
  sageEnabled?: boolean; // (선택) 클라에서 관리 시 사용. 권장: 헤더로 제어
  scenario?: "planning" | "medical" | "payment";
  metadata?: Record<string, string>;
}
```

권장 헤더 예시:

```typescript
{
  "Content-Type": "application/json",
  "X-SAGE-Enabled": "true",   // SAGE ON/OFF (per request)
  "X-Scenario": "payment"      // 선택: UI 시나리오 표시용
}
```

### 4) 에러 처리/로그

- 백엔드 미실행 시 사용자에게 명확한 오류 표시
- (선택) WebSocket 사용 시 자동 재연결/하트비트/상태 표시
- 서버 로그는 `logs/*.log` 확인(payment, gateway, root, client)

## 🔌 데모 토글 및 효과

실행 스크립트(`demo_SAGE.sh`, `scripts/06_start_all.sh`)로 다음을 제어할 수 있습니다.

- SAGE ON/OFF (요청 단위)

  - 헤더 `X-SAGE-Enabled: true|false` (기본: ON)

- Gateway tamper/pass (프로세스 시작 시)

  - `--tamper`(기본) 또는 `--pass`
  - tamper일 때 게이트웨이는 JSON 바디를 변조하거나 HPKE ciphertext의 1바이트를 flip합니다.

- HPKE ON/OFF (프로세스 시작 시)
  - `--hpke on|off` (기본 off)
  - KEM(X25519) 키 필요: `keys/kem/external.x25519.jwk`
  - 본 데모에서 HPKE는 SAGE가 ON일 때만 유효하게 사용됩니다.

효과 요약:

- HPKE ON + tamper → 게이트웨이가 ciphertext를 변조하면 External에서 복호화 오류(검출)
- HPKE OFF + SAGE ON + tamper → External DID 미들웨어가 RFC9421 서명 불일치로 거부(4xx)
- HPKE OFF + SAGE OFF + tamper → 변조가 통과(보안 위험 데모)

## HTTP API 엔드포인트 (Client API :8086)

- 엔드포인트: `POST /api/request`
- 헤더: `Content-Type: application/json`, `X-SAGE-Enabled: true|false`, `X-Scenario: <opt>`
- 바디: `{ "prompt": "..." }`

## SAGE/HPKE 구현(요약)

- A2A 서명: `github.com/sage-x-project/sage-a2a-go` 클라이언트를 사용해 RFC9421 서명을 생성/첨부
- DID 검증: External Payment에서 a2a-go 미들웨어가 검증
- HPKE: Payment→External 간 초기화/세션(`agents/payment/hpke_wrap.go`, `cmd/payment/main.go`)

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

## 데이터 흐름

### 1. 사용자 요청 (SAGE ON)

```
User Input → Frontend → Client API
    ↓
Root Agent
    ↓
Sub‑Agents (in‑proc)
    ↓
Response (검증 성공) → Frontend
```

### 2. 사용자 요청 (SAGE OFF)

```
User Input → Frontend → Client API
    ↓
Root Agent → Gateway (tamper)
    ↓
Sub‑Agents (변조된 메시지 처리)
    ↓
Response (위험 경고 없음) → Frontend
```

## 실행 방법

### 간단 실행(추천)

```bash
# 게이트웨이 변조 + HPKE off (기본)
./demo_SAGE.sh --tamper --hpke off

# 게이트웨이 변조 + HPKE on
./demo_SAGE.sh --tamper --hpke on --hpke-keys generated_agent_keys.json

# 패스스루 + HPKE on
./demo_SAGE.sh --pass --hpke on --hpke-keys generated_agent_keys.json
```

### 수동 실행(그대로)

1. External Payment: `scripts/02_start_external_payment_agent.sh`
2. Gateway: `scripts/03_start_gateway_tamper.sh` 또는 `scripts/03_start_gateway_pass.sh`
3. Root: `go run ./cmd/root/main.go -port 18080 [-hpke -hpke-keys ...]`
4. Client API: `go run ./cmd/client/main.go -port 8086 -root http://localhost:18080`

## 프론트엔드에서 호출 예시

```bash
curl -sS POST http://localhost:8086/api/request \
  -H 'Content-Type: application/json' \
  -H 'X-SAGE-Enabled: true' \
  -H 'X-Scenario: payment' \
  -d '{"prompt":"send 5 usdc to bob"}' | jq
```

## 참고/보안

- 에이전트 등록/키 준비: README의 “Registering Agents (on‑chain)” 절 참고
- 포트 정리: `scripts/01_kill_ports.sh --force`
- 데모 키는 로컬 개발용. 운영에 재사용 금지
- RFC 9421: https://datatracker.ietf.org/doc/html/rfc9421
