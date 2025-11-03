# Railway 배포 가이드

## 개요

SAGE Multi-Agent 시스템을 Railway 클라우드 플랫폼에 배포하는 가이드입니다.

## 배포 구조

3개의 독립적인 Railway 서비스로 배포:

1. **Root Agent** - DID 관리 및 중앙 조정
2. **Payment Agent** - 결제 처리
3. **Medical Agent** - 의료 데이터 처리

## 사전 준비

### 1. Railway 계정 생성

- https://railway.app 에서 계정 생성
- GitHub 계정으로 로그인 권장

### 2. Railway CLI 설치 (선택사항)

```bash
npm install -g @railway/cli
railway login
```

### 3. Agent 키 생성

각 Agent용 secp256k1 및 X25519 키를 생성합니다:

```bash
cd sage-multi-agent

# Root Agent 키 생성
mkdir -p keys/kem
./build/bin/root --generate-key --key-file=keys/root.jwk
./build/bin/root --generate-kem-key --kem-key-file=keys/kem/root.x25519.jwk

# Payment Agent 키 생성
./build/bin/payment --generate-key --key-file=keys/payment.jwk
./build/bin/payment --generate-kem-key --kem-key-file=keys/kem/payment.x25519.jwk

# Medical Agent 키 생성
./build/bin/medical --generate-key --key-file=keys/medical.jwk
./build/bin/medical --generate-kem-key --kem-key-file=keys/kem/medical.x25519.jwk
```

### 4. 키 파일을 환경 변수용 JSON으로 변환

```bash
# Root JWK
export ROOT_JWK=$(cat keys/root.jwk | jq -c .)
echo $ROOT_JWK

# Root KEM
export ROOT_KEM_JWK=$(cat keys/kem/root.x25519.jwk | jq -c .)
echo $ROOT_KEM_JWK

# Payment JWK
export PAYMENT_JWK=$(cat keys/payment.jwk | jq -c .)
export PAYMENT_KEM_JWK=$(cat keys/kem/payment.x25519.jwk | jq -c .)

# Medical JWK
export MEDICAL_JWK=$(cat keys/medical.jwk | jq -c .)
export MEDICAL_KEM_JWK=$(cat keys/kem/medical.x25519.jwk | jq -c .)
```

## Railway 배포 단계

### 1. Root Agent 배포

#### 1.1 새 프로젝트 생성

1. Railway 대시보드에서 "New Project" 클릭
2. "Deploy from GitHub repo" 선택
3. `sage-multi-agent` 저장소 선택

#### 1.2 Root Agent 서비스 생성

1. "New Service" 클릭
2. "Deploy from GitHub repo" 선택
3. Settings → General:
   - **Service Name**: `sage-root-agent`
   - **Root Directory**: `/` (루트)
   - **Dockerfile Path**: `Dockerfile.root`

#### 1.3 환경 변수 설정

Settings → Variables에서 다음 환경 변수 추가:

```bash
# 블록체인 설정
ETH_RPC_URL=https://eth-sepolia.g.alchemy.com/v2/v4TawV7y1l8GhqM_4_KZu5x7H9R6poNW
SAGE_REGISTRY_ADDRESS=0xC7eCF7Ad6ee71CB0d94f0eb00F46f1DDf432a808
CHAIN_ID=11155111

# Root Agent 설정
ROOT_AGENT_PORT=18080
ROOT_SAGE_ENABLED=true
ROOT_HPKE=true

# 키 설정 (JSON 문자열)
ROOT_JWK={"kty":"EC",...}  # 위에서 복사한 값
ROOT_KEM_JWK={"kty":"OKP",...}  # 위에서 복사한 값

# LLM 설정
LLM_ENABLED=true
LLM_PROVIDER=gemini-native
GEMINI_API_KEY=AIzaSyD0U6R1QKZqo0AWSWuLmRE1VFXeH_v59qk
GEMINI_MODEL=gemini-2.0-flash-exp
LLM_TIMEOUT=15s

# 로그 설정
LOG_LEVEL=info
```

#### 1.4 포트 설정

Settings → Networking:
- **Public Port**: `18080`
- **Private Port**: `18080`
- Public Domain 활성화

#### 1.5 배포

"Deploy" 버튼 클릭 → 빌드 및 배포 완료 대기

배포 완료 후 Public URL 확인 (예: `https://sage-root-agent.up.railway.app`)

### 2. Payment Agent 배포

동일한 프로젝트에서 "New Service" 클릭:

#### 2.1 서비스 설정

- **Service Name**: `sage-payment-agent`
- **Dockerfile Path**: `Dockerfile.payment`

#### 2.2 환경 변수

```bash
# 블록체인 설정
ETH_RPC_URL=https://eth-sepolia.g.alchemy.com/v2/v4TawV7y1l8GhqM_4_KZu5x7H9R6poNW
SAGE_REGISTRY_ADDRESS=0xC7eCF7Ad6ee71CB0d94f0eb00F46f1DDf432a808
CHAIN_ID=11155111

# Payment Agent 설정
PAYMENT_AGENT_PORT=19083
PAYMENT_SAGE_ENABLED=true
PAYMENT_REQUIRE_SIGNATURE=true

# 키 설정
PAYMENT_JWK={"kty":"EC",...}
PAYMENT_KEM_JWK={"kty":"OKP",...}

# LLM 설정
LLM_ENABLED=true
LLM_PROVIDER=gemini-native
GEMINI_API_KEY=AIzaSyD0U6R1QKZqo0AWSWuLmRE1VFXeH_v59qk
GEMINI_MODEL=gemini-2.0-flash-exp

# Root Agent URL (위에서 얻은 Public URL)
ROOT_AGENT_URL=https://sage-root-agent.up.railway.app

LOG_LEVEL=info
```

#### 2.3 포트 설정

- **Public Port**: `19083`
- **Private Port**: `19083`

### 3. Medical Agent 배포

동일한 프로젝트에서 "New Service" 클릭:

#### 3.1 서비스 설정

- **Service Name**: `sage-medical-agent`
- **Dockerfile Path**: `Dockerfile.medical`

#### 3.2 환경 변수

```bash
# 블록체인 설정
ETH_RPC_URL=https://eth-sepolia.g.alchemy.com/v2/v4TawV7y1l8GhqM_4_KZu5x7H9R6poNW
SAGE_REGISTRY_ADDRESS=0xC7eCF7Ad6ee71CB0d94f0eb00F46f1DDf432a808
CHAIN_ID=11155111

# Medical Agent 설정
MEDICAL_AGENT_PORT=19082
MEDICAL_SAGE_ENABLED=true
MEDICAL_REQUIRE_SIGNATURE=true

# 키 설정
MEDICAL_JWK={"kty":"EC",...}
MEDICAL_KEM_JWK={"kty":"OKP",...}

# LLM 설정
LLM_ENABLED=true
LLM_PROVIDER=gemini-native
GEMINI_API_KEY=AIzaSyD0U6R1QKZqo0AWSWuLmRE1VFXeH_v59qk
GEMINI_MODEL=gemini-2.0-flash-exp

# Root Agent URL
ROOT_AGENT_URL=https://sage-root-agent.up.railway.app

LOG_LEVEL=info
```

#### 3.3 포트 설정

- **Public Port**: `19082`
- **Private Port**: `19082`

## Sepolia 테스트넷에 Agent 등록

배포된 Agent들의 Public URL을 얻은 후, Sepolia에 등록합니다.

### 1. DID 확인

각 Agent의 JWK에서 Ethereum 주소를 도출:

```bash
# Payment Agent DID 확인
curl https://sage-payment-agent.up.railway.app/did

# Medical Agent DID 확인
curl https://sage-medical-agent.up.railway.app/did
```

### 2. Sepolia에 Agent 등록

```bash
# sage 프로젝트 디렉토리로 이동
cd /path/to/sage

# Payment Agent 등록
./build/bin/sage-did register \
  --chain ethereum \
  --network sepolia \
  --key /path/to/payment.jwk \
  --name "Payment Agent" \
  --endpoint "https://sage-payment-agent.up.railway.app" \
  --capabilities "payment,transaction"

# Medical Agent 등록
./build/bin/sage-did register \
  --chain ethereum \
  --network sepolia \
  --key /path/to/medical.jwk \
  --name "Medical Agent" \
  --endpoint "https://sage-medical-agent.up.railway.app" \
  --capabilities "medical,health-data"
```

### 3. DID 검증

Sepolia Etherscan에서 등록 확인:

```bash
# DID 조회
./build/bin/sage-did resolve did:sage:ethereum:0x[payment-address]
./build/bin/sage-did resolve did:sage:ethereum:0x[medical-address]
```

## 배포 확인

### Health Check

```bash
# Root Agent
curl https://sage-root-agent.up.railway.app/health

# Payment Agent
curl https://sage-payment-agent.up.railway.app/health

# Medical Agent
curl https://sage-medical-agent.up.railway.app/health
```

### 테스트 요청

```bash
# Payment Agent 테스트
curl -X POST https://sage-payment-agent.up.railway.app/api/payment \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -d '{"prompt": "Pay 100 USD to merchant"}'

# Medical Agent 테스트
curl -X POST https://sage-medical-agent.up.railway.app/api/medical \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -d '{"prompt": "Request medical record for patient ID 12345"}'
```

## 비용 예상

Railway Free Tier:
- $5 credit/month
- ~500 hours/month 실행 시간

예상 비용 (3개 서비스):
- Hobby Plan: $5/month per service = $15/month
- Pro Plan: $20/month unlimited usage

## 트러블슈팅

### 1. 빌드 실패

```bash
# 로컬에서 빌드 테스트
docker build -f Dockerfile.root -t sage-root .
docker build -f Dockerfile.payment -t sage-payment .
docker build -f Dockerfile.medical -t sage-medical .
```

### 2. 환경 변수 문제

- JWK JSON이 올바른 형식인지 확인
- 따옴표가 제대로 이스케이프되었는지 확인
- `jq -c .` 명령으로 압축된 JSON 사용

### 3. 네트워크 연결 문제

- Railway 서비스 간 통신은 Private Network 사용 권장
- Public URL은 외부 접근용으로만 사용

### 4. 로그 확인

Railway 대시보드 → Service → Deployments → View Logs

## 다음 단계

1. Frontend를 Vercel/Railway에 배포
2. Frontend에서 Railway Agent URL 사용
3. Sepolia 테스트넷으로 E2E 테스트
4. 프로덕션 배포 (Mainnet)

## 참고 자료

- [Railway 문서](https://docs.railway.app/)
- [SAGE 문서](https://github.com/sage-x-project/sage)
- [Sepolia Testnet](https://sepolia.etherscan.io/)
