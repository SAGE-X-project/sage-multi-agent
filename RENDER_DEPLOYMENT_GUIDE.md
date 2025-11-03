# Render.com 배포 가이드

## 개요

SAGE Multi-Agent 시스템을 Render.com에 자동 배포하는 가이드입니다.

## 장점

- ✅ `render.yaml` 파일로 완전 자동화
- ✅ GitHub 저장소 연동만으로 배포 가능
- ✅ 무료 티어 제공 (Starter Plan)
- ✅ 자동 SSL 인증서
- ✅ 헬스체크 내장
- ✅ 로그 및 모니터링

## 배포 단계

### 1. 사전 준비

#### 1.1 GitHub 저장소에 코드 푸시

```bash
cd /Users/kevin/work/github/sage-x-project/final/sage-multi-agent

# Git 초기화 (아직 안했다면)
git init
git add .
git commit -m "feat: Add SAGE Multi-Agent with Render deployment"

# GitHub 저장소 생성 후 푸시
git remote add origin https://github.com/YOUR_USERNAME/sage-multi-agent.git
git branch -M main
git push -u origin main
```

#### 1.2 Agent 키 생성

```bash
# 키 디렉토리 생성
mkdir -p keys/kem

# Root Agent 키
./build/bin/root --generate-key --key-file=keys/root.jwk
./build/bin/root --generate-kem-key --kem-key-file=keys/kem/root.x25519.jwk

# Payment Agent 키
./build/bin/payment --generate-key --key-file=keys/payment.jwk
./build/bin/payment --generate-kem-key --kem-key-file=keys/kem/payment.x25519.jwk

# Medical Agent 키
./build/bin/medical --generate-key --key-file=keys/medical.jwk
./build/bin/medical --generate-kem-key --kem-key-file=keys/kem/medical.x25519.jwk
```

#### 1.3 키를 JSON 문자열로 변환

```bash
# Root
export ROOT_JWK=$(cat keys/root.jwk | jq -c . | sed 's/"/\\"/g')
export ROOT_KEM_JWK=$(cat keys/kem/root.x25519.jwk | jq -c . | sed 's/"/\\"/g')

# Payment
export PAYMENT_JWK=$(cat keys/payment.jwk | jq -c . | sed 's/"/\\"/g')
export PAYMENT_KEM_JWK=$(cat keys/kem/payment.x25519.jwk | jq -c . | sed 's/"/\\"/g')

# Medical
export MEDICAL_JWK=$(cat keys/medical.jwk | jq -c . | sed 's/"/\\"/g')
export MEDICAL_KEM_JWK=$(cat keys/kem/medical.x25519.jwk | jq -c . | sed 's/"/\\"/g')

# 나중에 사용하기 위해 저장
echo "ROOT_JWK=$ROOT_JWK" > .env.render
echo "ROOT_KEM_JWK=$ROOT_KEM_JWK" >> .env.render
echo "PAYMENT_JWK=$PAYMENT_JWK" >> .env.render
echo "PAYMENT_KEM_JWK=$PAYMENT_KEM_JWK" >> .env.render
echo "MEDICAL_JWK=$MEDICAL_JWK" >> .env.render
echo "MEDICAL_KEM_JWK=$MEDICAL_KEM_JWK" >> .env.render
```

### 2. Render.com 배포

#### 2.1 Render 계정 생성

1. https://render.com 접속
2. "Get Started" 클릭
3. GitHub 계정으로 로그인

#### 2.2 Blueprint로 배포

1. Render 대시보드에서 "New" → "Blueprint" 클릭
2. GitHub 저장소 선택: `sage-multi-agent`
3. `render.yaml` 파일 자동 감지
4. "Apply" 클릭

#### 2.3 환경 변수 설정

각 서비스별로 환경 변수 추가 (Render 대시보드에서):

**Root Agent (sage-root-agent)**:
- `GEMINI_API_KEY`: `AIzaSyD0U6R1QKZqo0AWSWuLmRE1VFXeH_v59qk`
- `ROOT_JWK`: (위에서 생성한 값)
- `ROOT_KEM_JWK`: (위에서 생성한 값)

**Payment Agent (sage-payment-agent)**:
- `GEMINI_API_KEY`: `AIzaSyD0U6R1QKZqo0AWSWuLmRE1VFXeH_v59qk`
- `PAYMENT_JWK`: (위에서 생성한 값)
- `PAYMENT_KEM_JWK`: (위에서 생성한 값)

**Medical Agent (sage-medical-agent)**:
- `GEMINI_API_KEY`: `AIzaSyD0U6R1QKZqo0AWSWuLmRE1VFXeH_v59qk`
- `MEDICAL_JWK`: (위에서 생성한 값)
- `MEDICAL_KEM_JWK`: (위에서 생성한 값)

#### 2.4 배포 완료 대기

- 각 서비스가 빌드되고 배포됨 (약 5-10분 소요)
- 배포 로그 실시간 확인 가능

### 3. 배포 확인

#### 3.1 Public URL 확인

Render 대시보드에서 각 서비스의 URL 확인:
- Root Agent: `https://sage-root-agent.onrender.com`
- Payment Agent: `https://sage-payment-agent.onrender.com`
- Medical Agent: `https://sage-medical-agent.onrender.com`

#### 3.2 Health Check

```bash
curl https://sage-root-agent.onrender.com/health
curl https://sage-payment-agent.onrender.com/health
curl https://sage-medical-agent.onrender.com/health
```

### 4. Sepolia에 Agent 등록

#### 4.1 DID 확인

```bash
# Payment Agent DID
curl https://sage-payment-agent.onrender.com/did

# Medical Agent DID
curl https://sage-medical-agent.onrender.com/did
```

#### 4.2 Sepolia 등록

```bash
cd /Users/kevin/work/github/sage-x-project/final/sage

# Payment Agent 등록
./build/bin/sage-did register \
  --chain ethereum \
  --network sepolia \
  --key ../sage-multi-agent/keys/payment.jwk \
  --name "Payment Agent" \
  --endpoint "https://sage-payment-agent.onrender.com" \
  --capabilities "payment,transaction"

# Medical Agent 등록
./build/bin/sage-did register \
  --chain ethereum \
  --network sepolia \
  --key ../sage-multi-agent/keys/medical.jwk \
  --name "Medical Agent" \
  --endpoint "https://sage-medical-agent.onrender.com" \
  --capabilities "medical,health-data"
```

### 5. E2E 테스트

```bash
# Payment 테스트
curl -X POST https://sage-payment-agent.onrender.com/api/payment \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -d '{"prompt": "Pay 100 USD to merchant"}'

# Medical 테스트
curl -X POST https://sage-medical-agent.onrender.com/api/medical \
  -H "Content-Type: application/json" \
  -H "X-SAGE-Enabled: true" \
  -d '{"prompt": "Get patient health data"}'
```

## 비용

**Free Tier**:
- 750시간/월 무료
- 3개 서비스 = 각각 250시간 (~10일)
- 이후 자동 중지, 요청 시 재시작 (30초 소요)

**Starter Plan** ($7/month per service):
- 무제한 실행 시간
- 자동 재시작 없음
- 3개 서비스 = $21/month

## 업데이트 배포

코드 변경 후:

```bash
git add .
git commit -m "Update: ..."
git push origin main
```

→ Render가 자동으로 감지하여 재배포

## 로그 확인

Render 대시보드 → 서비스 선택 → "Logs" 탭

## 환경 변수 변경

Render 대시보드 → 서비스 선택 → "Environment" 탭 → 값 변경 → 자동 재배포

## 트러블슈팅

### 1. 빌드 실패

로컬에서 Docker 빌드 테스트:
```bash
docker build -f Dockerfile.root -t test-root .
```

### 2. Health Check 실패

- `/health` 엔드포인트가 200 응답하는지 확인
- 포트 설정이 올바른지 확인 (ROOT: 18080, PAYMENT: 19083, MEDICAL: 19082)

### 3. 서비스 간 통신 실패

- `ROOT_AGENT_URL` 환경 변수가 올바른지 확인
- Render의 Internal Network를 사용하려면 `serviceName.onrender.com` 사용

## 다음 단계

1. Frontend Vercel 배포
2. Frontend에서 Render Agent URL 사용
3. 프로덕션 배포 (Mainnet)

## 참고

- [Render 문서](https://render.com/docs)
- [Blueprint YAML 스펙](https://render.com/docs/blueprint-spec)
- [Docker 배포](https://render.com/docs/deploy-an-image)
