# sage-a2a-go v1.7.0 마이그레이션 빠른 시작

> 📖 상세 가이드: [SAGE_A2A_GO_MIGRATION_GUIDE.md](./SAGE_A2A_GO_MIGRATION_GUIDE.md)

## 🎯 한눈에 보기

```
internal/agent → sage-a2a-go/pkg/agent/framework
```

**예상 시간**: 5-9일
**코드 감소**: 평균 78% (165 lines → 10 lines)
**테스트 보장**: 52개 테스트, 57.1% 커버리지

---

## ⚡ 빠른 시작 (3단계)

### 1️⃣ 의존성 추가

```bash
go get github.com/sage-x-project/sage-a2a-go@v1.7.0
go mod tidy
```

### 2️⃣ Import 일괄 변경

```bash
find . -name "*.go" -type f -exec sed -i '' \
  's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent/framework|g' {} +
```

### 3️⃣ 타입 업데이트

```go
// Before
import "github.com/sage-x-project/sage-multi-agent/internal/agent"
agent *agent.Agent

// After
import "github.com/sage-x-project/sage-a2a-go/pkg/agent/framework"
agent *framework.Agent
```

---

## 📝 변경 사항 체크리스트

### Payment Agent (`agents/payment/agent.go`)

- [ ] Import 경로 변경
- [ ] `agent.Agent` → `framework.Agent` 타입 변경
- [ ] 컴파일 확인
- [ ] 테스트 실행

### Medical Agent (`agents/medical/agent.go`)

- [ ] Import 경로 변경
- [ ] `agent.Agent` → `framework.Agent` 타입 변경
- [ ] 컴파일 확인
- [ ] 테스트 실행

### Root Agent (`agents/root/agent.go`)

- [ ] Import 경로 변경
- [ ] HPKE 클라이언트 코드 간소화
- [ ] 컴파일 확인
- [ ] 테스트 실행

---

## 🧪 빠른 검증

```bash
# 컴파일
go build ./...

# 테스트
go test ./agents/...

# 실행
./demo_SAGE.sh --tamper --hpke on
```

---

## 🆘 자주 발생하는 문제

### Q1: "cannot find package" 오류

```bash
# 의존성 재설치
go clean -modcache
go mod download
go mod tidy
```

### Q2: Ethereum 연결 오류

```bash
# Hardhat 재시작
pkill -f "hardhat node"
cd ../sage/contracts/ethereum
npx hardhat node --port 8545 --chain-id 31337 &
npx hardhat run scripts/deploy-agentcard.js --network localhost
```

### Q3: 환경 변수 누락

```bash
# 필수 환경 변수
export PAYMENT_JWK_FILE="keys/external.secp256k1.jwk"
export PAYMENT_KEM_JWK_FILE="keys/kem/external.x25519.jwk"
export PAYMENT_DID="did:sage:local:external"
```

---

## 📚 더 알아보기

- [전체 마이그레이션 가이드](./SAGE_A2A_GO_MIGRATION_GUIDE.md)
- [sage-a2a-go Framework 문서](https://github.com/sage-x-project/sage-a2a-go/blob/main/pkg/agent/framework/README.md)
- [코드 예제](https://github.com/sage-x-project/sage-a2a-go/blob/main/examples/framework/)

---

**작성일**: 2025-11-03
**버전**: sage-a2a-go v1.7.0
