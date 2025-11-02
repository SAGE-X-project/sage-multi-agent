# Post-Migration Guide: Switching to sage-a2a-go v1.7.0

## 개요

이 가이드는 sage-a2a-go v1.7.0이 릴리스된 후, sage-multi-agent에서 `internal/agent`를 제거하고 `sage-a2a-go/pkg/agent`로 전환하는 방법을 설명합니다.

**전제 조건**: sage-a2a-go v1.7.0이 릴리스되어 있어야 합니다.

## 🔍 현재 상태 확인

### 1. sage-a2a-go v1.7.0 확인

```bash
# sage-a2a-go 저장소에서 태그 확인
cd /path/to/sage-a2a-go
git tag | grep v1.7.0

# 또는 GitHub releases 확인
curl -s https://api.github.com/repos/sage-x-project/sage-a2a-go/releases/latest | grep tag_name
```

**Expected**: `v1.7.0` 태그가 존재해야 함

### 2. 현재 sage-multi-agent 상태 확인

```bash
cd sage-multi-agent

# internal/agent 존재 확인
ls -la internal/agent/

# 현재 사용 중인 파일 확인
grep -r "internal/agent" agents/ | wc -l
```

**Expected**: `internal/agent` 디렉토리가 존재하고, agents에서 사용 중

## 📋 마이그레이션 단계

### Step 1: 백업 생성

```bash
# 현재 브랜치 백업
git checkout -b backup/pre-v1.7.0-migration

# internal/agent 백업
cp -r internal/agent internal/agent.backup-$(date +%Y%m%d)

# 현재 상태 커밋
git add internal/agent.backup-*
git commit -m "backup: Save internal/agent before migration"

# 작업 브랜치로 복귀 또는 생성
git checkout main  # 또는 작업 중인 브랜치
git checkout -b refactor/migrate-to-sage-a2a-go-v1.7.0
```

### Step 2: go.mod 업데이트

```bash
# sage-a2a-go v1.7.0으로 업데이트
go get github.com/sage-x-project/sage-a2a-go@v1.7.0

# 의존성 정리
go mod tidy

# 변경사항 확인
git diff go.mod go.sum
```

**Expected output (go.mod)**:
```diff
- github.com/sage-x-project/sage-a2a-go v1.6.0
+ github.com/sage-x-project/sage-a2a-go v1.7.0
```

### Step 3: Import 경로 자동 변경

```bash
# 모든 agent 파일에서 import 경로 변경
find agents -name "*.go" -type f -exec \
  sed -i '' 's|github\.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' {} \;

# 변경사항 확인
git diff agents/
```

**Expected changes**:
```diff
- import "github.com/sage-x-project/sage-multi-agent/internal/agent"
+ import "github.com/sage-x-project/sage-a2a-go/pkg/agent"
```

### Step 4: 수동 검토

일부 파일은 수동 확인이 필요할 수 있습니다:

```bash
# internal/agent 참조가 남아있는지 확인
grep -r "internal/agent" agents/ cmd/

# 만약 출력이 있다면 수동으로 수정
```

**주의**: 주석이나 문자열에도 참조가 있을 수 있으므로 확인 필요

### Step 5: 빌드 검증

```bash
# 전체 프로젝트 빌드
go build ./...

# 각 agent 바이너리 빌드
go build -o bin/root ./cmd/root
go build -o bin/payment ./cmd/payment
go build -o bin/medical ./cmd/medical
go build -o bin/gateway ./cmd/gateway
go build -o bin/client ./cmd/client

# 빌드 성공 확인
ls -lh bin/
```

**Expected**: 모든 빌드가 오류 없이 성공

**만약 빌드 실패 시**:

```bash
# 에러 메시지 확인
go build ./... 2>&1 | tee build-errors.log

# 일반적인 문제:
# 1. Import 경로가 완전히 변경되지 않음 → Step 3 재실행
# 2. API 변경 → 코드 수정 필요 (드물어야 함)
# 3. 의존성 문제 → go mod tidy 재실행
```

### Step 6: internal/agent 제거

```bash
# internal/agent 디렉토리 제거
git rm -r internal/agent

# 삭제 확인
ls internal/  # agent 디렉토리가 없어야 함

# 빌드 재확인 (internal/agent 없이)
go build ./...
```

**Expected**: 빌드가 여전히 성공해야 함

### Step 7: 테스트 실행

#### 단위 테스트 (있다면)

```bash
go test ./...
```

#### 통합 테스트

```bash
# 서비스 시작 (배포 환경 필요)
./scripts/06_start_all.sh --sage on --pass

# 간단한 요청 테스트
./scripts/07_send_prompt.sh --sage on --prompt "test"

# 또는 통합 테스트 실행
./scripts/integration_test.sh
```

**Expected**: 모든 테스트 통과

### Step 8: 문서 업데이트

#### README.md

```bash
# internal/agent 언급 제거 또는 업데이트
# Before:
# - Uses internal/agent framework

# After:
# - Uses sage-a2a-go/pkg/agent framework (v1.7.0)
```

**변경 예시**:

```diff
 ## Architecture

 sage-multi-agent uses the SAGE Agent Framework for simplified agent development:

-- **Framework**: `internal/agent` provides high-level abstractions
+- **Framework**: `sage-a2a-go/pkg/agent` (v1.7.0) provides high-level abstractions
 - **Zero Sage Imports**: Agent code doesn't directly import sage packages
 - **83% Code Reduction**: 165 lines → 10 lines for initialization

 ```go
-import "github.com/sage-x-project/sage-multi-agent/internal/agent"
+import "github.com/sage-x-project/sage-a2a-go/pkg/agent"

 agent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, true)
 ```
```

#### docs/API.md

```bash
# Import 경로 업데이트
sed -i '' 's|sage-multi-agent/internal/agent|sage-a2a-go/pkg/agent|g' docs/API.md
```

#### docs/DEPLOYMENT.md

```bash
# Import 경로 업데이트
sed -i '' 's|sage-multi-agent/internal/agent|sage-a2a-go/pkg/agent|g' docs/DEPLOYMENT.md
```

#### docs/PHASE2_FINAL_SUMMARY.md

마이그레이션 완료 섹션 추가:

```markdown
## 🚀 Phase 3: sage-a2a-go v1.7.0 Migration (완료)

**날짜**: 2025-XX-XX
**커밋**: [commit-hash]

### 작업 내용

- ✅ sage-a2a-go v1.7.0 릴리스
- ✅ sage-multi-agent에서 import 경로 변경
- ✅ internal/agent 디렉토리 제거
- ✅ 모든 agent 빌드 검증
- ✅ 통합 테스트 통과
- ✅ 문서 업데이트

### 결과

- internal/agent (1,785 줄) → sage-a2a-go/pkg/agent (upstream)
- Agent code는 변경 없음 (import 경로만)
- 모든 기능 정상 동작
```

### Step 9: 커밋

```bash
# 변경사항 확인
git status

# Expected changes:
# - modified: go.mod
# - modified: go.sum
# - modified: agents/root/agent.go
# - modified: agents/payment/agent.go
# - modified: agents/medical/agent.go
# - modified: agents/planning/agent.go
# - deleted: internal/agent/

# 스테이징
git add .

# 커밋 메시지 작성
git commit -m "$(cat <<'EOF'
refactor: Migrate to sage-a2a-go v1.7.0 agent framework

Replace internal/agent with official sage-a2a-go/pkg/agent.

Changes:
- Update go.mod to sage-a2a-go v1.7.0
- Replace all internal/agent imports with sage-a2a-go/pkg/agent
- Remove internal/agent directory (1,785 lines)
- Update documentation (README, API, DEPLOYMENT)

Agent framework has been successfully migrated upstream to sage-a2a-go.
All agents now use the official framework with no code changes.

Verification:
✅ All agent binaries build successfully
✅ No breaking changes to agent APIs
✅ Integration tests pass (manual)
✅ Documentation updated

Previous: internal/agent (local implementation)
Current: sage-a2a-go/pkg/agent v1.7.0 (upstream)

Related: Phase 2 refactoring, agent framework design
EOF
)"
```

### Step 10: 최종 검증

```bash
# 깨끗한 상태에서 빌드
go clean -cache
go build ./...

# 바이너리 다시 빌드
rm -rf bin/
mkdir bin
go build -o bin/root ./cmd/root
go build -o bin/payment ./cmd/payment
go build -o bin/medical ./cmd/medical
go build -o bin/gateway ./cmd/gateway
go build -o bin/client ./cmd/client

# 바이너리 크기 확인 (이전과 유사해야 함)
ls -lh bin/

# internal/agent 참조가 완전히 제거되었는지 확인
grep -r "internal/agent" . --exclude-dir=.git --exclude-dir=internal --exclude="*.backup*" || echo "✓ No references found"
```

**Expected**: 모든 검증 통과

## 🔧 문제 해결

### 문제 1: 빌드 실패 - "package internal/agent not found"

**원인**: Import 경로 변경이 불완전함

**해결**:
```bash
# 남아있는 참조 확인
grep -r "internal/agent" agents/ cmd/

# 수동으로 수정하거나 Step 3 재실행
```

### 문제 2: 빌드 실패 - "undefined: agent.SomeMethod"

**원인**: sage-a2a-go v1.7.0 API 차이

**해결**:
```bash
# sage-a2a-go의 API 문서 확인
# internal/agent과 다른 메서드명이나 시그니처 확인

# 예: GetUnderlying() 메서드가 제거되었을 수 있음
# 코드 수정 필요
```

### 문제 3: 런타임 오류 - "HPKE not available"

**원인**: 환경 변수 또는 초기화 문제 (코드 변경과 무관)

**해결**:
```bash
# 환경 변수 확인
echo $PAYMENT_KEM_JWK_FILE
ls -l $PAYMENT_KEM_JWK_FILE

# HPKE 초기화 로그 확인
grep -i "hpke" logs/payment.log
```

### 문제 4: go.mod 충돌

**원인**: 캐시된 모듈 버전

**해결**:
```bash
# 모듈 캐시 정리
go clean -modcache

# go.mod 재정리
go mod tidy

# v1.7.0 명시적으로 재설치
go get github.com/sage-x-project/sage-a2a-go@v1.7.0
```

## 📊 마이그레이션 체크리스트

### 사전 준비
- [ ] sage-a2a-go v1.7.0이 릴리스되었는지 확인
- [ ] 현재 sage-multi-agent가 정상 작동하는지 확인
- [ ] 백업 브랜치 생성

### 마이그레이션
- [ ] go.mod 업데이트 (v1.7.0)
- [ ] Import 경로 자동 변경
- [ ] 수동 검토 및 수정
- [ ] 빌드 검증
- [ ] internal/agent 제거
- [ ] 통합 테스트 실행

### 문서화
- [ ] README.md 업데이트
- [ ] docs/API.md 업데이트
- [ ] docs/DEPLOYMENT.md 업데이트
- [ ] docs/PHASE2_FINAL_SUMMARY.md 업데이트

### 최종 검증
- [ ] 전체 프로젝트 빌드
- [ ] 모든 바이너리 생성
- [ ] internal/agent 참조 완전 제거 확인
- [ ] 통합 테스트 통과
- [ ] 커밋 및 푸시

## 🚀 배포

마이그레이션 후 배포:

```bash
# 1. PR 생성 (GitHub)
git push origin refactor/migrate-to-sage-a2a-go-v1.7.0

# 2. PR 리뷰 및 승인

# 3. main 브랜치로 머지

# 4. 배포 (기존 프로세스 동일)
./scripts/06_start_all.sh --sage on --pass
```

## 📝 예상 변경 사항 요약

| 파일 | 변경 유형 | 예상 줄 수 |
|------|----------|------------|
| `go.mod` | 버전 업데이트 | 1-2 줄 |
| `go.sum` | 체크섬 업데이트 | 10-20 줄 |
| `agents/root/agent.go` | Import 경로 | 1-2 줄 |
| `agents/planning/agent.go` | Import 경로 | 1 줄 |
| `agents/payment/agent.go` | Import 경로 | 1 줄 |
| `agents/medical/agent.go` | Import 경로 | 1 줄 |
| `internal/agent/**` | 삭제 | -1,785 줄 |
| `README.md` | 경로 업데이트 | 5-10 줄 |
| `docs/API.md` | 경로 업데이트 | 20-30 줄 |
| `docs/DEPLOYMENT.md` | 경로 업데이트 | 10-20 줄 |

**총 예상 변경**: 약 -1,700 줄 (대부분 삭제)

## ⏱️ 예상 소요 시간

- **사전 준비**: 15분
- **마이그레이션**: 30분
- **테스트**: 30분
- **문서화**: 30분
- **검증 및 배포**: 30분

**총**: 약 2-2.5시간

## ✅ 성공 기준

마이그레이션이 성공적으로 완료되었다고 판단하는 기준:

1. ✅ `go build ./...` 오류 없이 성공
2. ✅ 모든 agent 바이너리 생성됨 (bin/)
3. ✅ `internal/agent` 디렉토리 완전 제거
4. ✅ `grep -r "internal/agent"` 출력 없음 (백업 제외)
5. ✅ 통합 테스트 통과
6. ✅ 모든 문서 업데이트 완료
7. ✅ git history 깔끔 (의미 있는 커밋 메시지)

## 📞 지원

문제가 발생하면:

1. 이 가이드의 "문제 해결" 섹션 확인
2. `docs/AGENT_FRAMEWORK_MIGRATION_GUIDE.md` 참조
3. sage-a2a-go v1.7.0 릴리스 노트 확인
4. GitHub Issues에 문의

## 🎉 완료!

마이그레이션이 완료되면:
- sage-multi-agent는 더 이상 자체 프레임워크를 유지하지 않음
- 모든 agent가 공식 sage-a2a-go 프레임워크 사용
- 향후 프레임워크 업데이트는 sage-a2a-go를 통해 진행
- 유지보수 부담 감소
