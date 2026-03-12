# AAP Config Server — TDD 개발 프로세스 가이드

> 이 문서는 AAP Config Server 개발 시 TDD(Test-Driven Development) 기반으로 작업하는 구체적인 프로세스를 정의한다.
> 프로젝트 구조, 패키지 설계, 의존성 주입, 에러 처리 등 **Go 설계 원칙**은 [HLD 섹션 11](./HLD.md#11-go-설계-원칙)을 참조한다.

---

## 1. 개발 사이클 (Red → Green → Refactor)

모든 기능 구현은 아래 사이클을 따른다:

```
1. RED    — 실패하는 테스트를 먼저 작성한다
2. GREEN  — 테스트를 통과하는 최소한의 코드를 작성한다
3. REFACTOR — 테스트가 통과하는 상태를 유지하면서 코드를 정리한다
```

### 1.1 단계별 상세

**RED (테스트 작성)**:
- 구현하려는 동작을 테스트로 먼저 표현한다
- 테스트를 실행하여 **실패하는 것을 확인**한다 (이 단계를 건너뛰지 않는다)
- 실패 메시지가 의도한 대로인지 확인한다 (예: "undefined function" vs "expected X got Y")

**GREEN (최소 구현)**:
- 테스트를 통과시키는 **가장 단순한 코드**를 작성한다
- 미래 요구사항을 예측하여 추가 구현하지 않는다
- 하드코딩이라도 괜찮다 — 다음 테스트가 일반화를 강제한다

**REFACTOR (정리)**:
- 중복 제거, 네이밍 개선, 메서드 추출 등
- **테스트를 계속 실행하면서** 리팩토링한다
- 새로운 기능을 추가하지 않는다

---

## 2. 테스트 계층 및 작성 전략

### 2.1 테스트 유형

| 유형 | 도구 | 대상 | 비율 목표 |
|------|------|------|-----------|
| **Unit Test** | `go test` | 개별 함수/메서드 (파서, store, handler) | 70% |
| **Integration Test** | `go test -tags=integration` | 컴포넌트 간 상호작용 (Git sync + Config Store + API) | 20% |
| **E2E Test** | `kind` + `go test -tags=e2e` | 전체 시스템 (Config Server + Agent + K8s) | 10% |

### 2.2 테스트 작성 순서 (Outside-In)

기능 개발 시 **바깥에서 안쪽으로** 테스트를 작성한다:

```
1. API Handler Spec    — "이 API를 호출하면 어떤 응답이 와야 하는가"
2. Service Logic Spec  — "이 서비스가 어떤 동작을 해야 하는가"
3. Store/Parser Spec   — "이 파서/저장소가 어떤 데이터를 다뤄야 하는가"
4. K8s Client Spec     — "이 K8s 작업이 올바르게 실행되어야 하는가"
```

### 2.3 외부 의존성 격리 전략

외부 의존성(Git, K8s API, kubeseal)은 **항상 Mock/Fake**한다:

| 의존성 | Mock 방식 |
|--------|----------|
| **K8s API** | `client-go/kubernetes/fake` 사용, 실제 클러스터 불필요 |
| **Git** | `t.TempDir()`에 test fixture repo 생성 |
| **HTTP** | `httptest.NewServer`로 실제 HTTP 통신 테스트 |
| **kubeseal** | interface로 추상화, 테스트에서 mock 주입 |

- 실제 외부 서비스에 의존하는 테스트는 작성하지 않는다 (CI 환경에서 실행 불가)
- 시크릿 테스트 데이터에 실제 시크릿 절대 포함 금지

---

## 3. Phase별 TDD 전략

### 3.1 Phase 1: Core (MVP)

```
테스트 먼저                          구현
━━━━━━━━━━━━━━━━━━━━                ━━━━━━━━━━━━━━━━━━
0. 프로젝트 구조 세팅                → go mod init, cmd/, internal/ 디렉토리
   서버 설정 로딩 테스트             → 환경변수 + 플래그 로딩
   커스텀 에러 타입 테스트           → 에러 코드, Unwrap, errors.As 동작 확인
1. config.yaml 파싱 테스트           → YAML 파서 구현
2. env_vars.yaml 파싱 테스트         → 환경변수 파서 구현
3. secrets.yaml 파싱 테스트          → 시크릿 메타데이터 파서 구현
4. ConfigStore CRUD 테스트           → In-memory store 구현 (COW, DI)
5. Git clone/pull 테스트             → Git sync 로직 구현
6. REST API 핸들러 테스트            → HTTP 핸들러 구현
   - GET /config 응답 형식
   - 쿼리 파라미터 처리
   - 에러 응답 (에러 코드 → HTTP 상태코드 변환)
   - context.Context 전파 확인
7. Health check 테스트               → /healthz, /readyz 구현
8. Graceful shutdown 테스트          → signal.NotifyContext + http.Server.Shutdown
```

**테스트 예시 (Phase 1)**:
```go
// YAML 파싱 테스트 — Red: 이 테스트를 먼저 작성
func TestParseConfigYAML(t *testing.T) {
    input := `
version: "1"
metadata:
  service: litellm
  org: myorg
  project: ai-platform
config:
  model_list:
    - model_name: "azure-gpt4"
      litellm_params:
        model: "azure/gpt-4"
        api_key_secret_ref: "azure-gpt4-api-key"
`
    cfg, err := ParseConfig([]byte(input))
    require.NoError(t, err)
    assert.Equal(t, "litellm", cfg.Metadata.Service)
    assert.Len(t, cfg.Config.ModelList, 1)
    assert.Equal(t, "azure-gpt4-api-key",
        cfg.Config.ModelList[0].LitellmParams.APIKeySecretRef)
}

// Store 테스트 — context.Context + 에러 타입 검증 예시
func TestGetConfig_NotFound(t *testing.T) {
    ctx := context.Background()
    repo := &fakeGitRepo{}  // interface mock
    s := store.NewStore(repo)

    _, err := s.GetConfig(ctx, "no-org", "no-proj", "no-svc")
    require.Error(t, err)

    var appErr *apperror.Error
    require.ErrorAs(t, err, &appErr)
    assert.Equal(t, apperror.CodeNotFound, appErr.Code)
}
```

### 3.2 Phase 2: Secrets

```
테스트 먼저                          구현
━━━━━━━━━━━━━━━━━━━━                ━━━━━━━━━━━━━━━━━━
1. Volume Mount 파일 읽기 테스트     → Secret loader 구현
2. secret_ref resolve 테스트         → resolve 로직 구현
   - config 내 *_secret_ref 치환
   - env_vars 내 secret_refs 치환
3. SealedSecret 생성 테스트          → kubeseal 연동 구현 (interface)
4. SealedSecret Git push 테스트      → Git commit/push 구현
5. resolve_secrets=true 응답 테스트  → API 파라미터 처리
6. Cache-Control 헤더 테스트         → 보안 헤더 미들웨어
7. 감사 로깅 테스트                  → audit logger 구현
```

### 3.3 Phase 3: Config Agent

```
테스트 먼저                          구현
━━━━━━━━━━━━━━━━━━━━                ━━━━━━━━━━━━━━━━━━
1. Config Server API 호출 테스트     → HTTP client 구현
   (httptest.Server 사용)
2. 변경 감지 로직 테스트             → version 비교 로직
3. litellm config.yaml 생성 테스트   → 설정 변환기 구현
4. env.sh 생성 테스트                → 환경변수 파일 생성기
5. ConfigMap CRUD 테스트             → K8s client-go (fake client)
6. Secret CRUD 테스트                → K8s client-go (fake client)
7. Rolling restart 트리거 테스트     → Deployment patch 로직
8. 변경 유형 판별 테스트             → config vs env_vars 구분
9. Debounce 로직 테스트              → leading-edge debounce 구현
```

---

## 4. 기능별 개발 워크플로우

### 4.1 새로운 기능 구현 시

```
1. PRD에서 해당 FR(기능 요구사항) 확인
2. 기능을 작은 단위로 분해 (각 단위가 하나의 TDD 사이클)
3. 각 단위에 대해:
   a. 실패하는 테스트 작성
   b. 테스트 통과하는 최소 코드 작성
   c. 리팩토링
   d. 커밋 (테스트 + 구현 함께)
4. 전체 테스트 스위트 실행하여 regression 없는지 확인
5. 기능 완료 커밋
```

### 4.2 버그 수정 시

```
1. 버그를 재현하는 테스트를 먼저 작성한다 (RED)
2. 테스트가 실패하는 것을 확인한다
3. 버그를 수정한다 (GREEN)
4. 리팩토링 (필요 시)
5. 커밋: "fix: 버그 설명" + 재현 테스트 포함
```

---

## 5. 테스트 작성 규칙

| 규칙 | 설명 |
|------|------|
| **테스트 파일 위치** | 구현 파일과 동일 패키지 (`_test.go` 접미사) |
| **테이블 드리븐 테스트** | 복수 케이스는 `[]struct{ name, input, expected }` 패턴 사용 |
| **외부 의존성 격리** | interface로 추상화, 테스트에서 mock/fake 주입 |
| **context.Context** | 테스트에서도 `context.Background()` 또는 `context.WithTimeout`을 전달 |
| **에러 검증** | `errors.Is` / `errors.As`로 에러 타입 확인, 문자열 비교 금지 |
| **Race 검출** | CI에서 `-race` 플래그 필수. 동시성 관련 코드는 로컬에서도 `-race`로 실행 |
| **시크릿 테스트** | 테스트 데이터에 실제 시크릿 절대 포함 금지 |

> 의존성 주입, 에러 타입, 프로젝트 구조 등 구체적인 Go 설계 패턴은 [HLD 섹션 11](./HLD.md#11-go-설계-원칙)을 참조한다.

---

## 6. 개발 워크플로우

```
기능 하나당 반복:

1. 요구사항에서 테스트 케이스 도출
2. 실패하는 테스트 작성 (Red)
3. go test → 실패 확인
4. 테스트 통과하는 최소 코드 작성 (Green)
5. go test → 통과 확인
6. 리팩토링 (Refactor)
7. go test → 여전히 통과 확인
8. 커밋
9. 다음 기능으로
```

---

## 7. 커밋 전략

### 7.1 커밋 단위

- **하나의 TDD 사이클 = 하나의 커밋** (테스트 + 구현 코드 함께)
- 테스트만 있는 커밋, 구현만 있는 커밋을 분리하지 않는다
- 리팩토링은 별도 커밋으로 분리할 수 있다

### 7.2 커밋 메시지 형식

```
<type>(<scope>): <description>

# type: feat, fix, refactor, test, docs, chore
# scope: 영향 범위 (config-parser, api, store, agent, secret 등)

# 예시:
test: config-parser - add YAML parsing test for model_list
feat: config-parser - implement ParseConfig for model_list
refactor: config-parser - extract common YAML parsing logic
fix: agent - rolling restart annotation 누락 수정
```

---

## 8. 실행 명령어

```bash
# 전체 테스트 실행
go test ./...

# 특정 테스트 함수 실행
go test ./... -run TestParseConfigYAML

# 짧은 테스트만 실행 (CI 빠른 피드백)
go test -short ./...

# Race condition 검출
go test -race ./...

# Integration 테스트 실행
go test -tags=integration ./...

# E2E 테스트 실행 (kind 클러스터 필요)
go test -tags=e2e ./...

# 커버리지 리포트 생성
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

---

## 9. CI에서의 테스트

- 모든 PR은 전체 테스트 스위트가 통과해야 머지 가능
- 커버리지 80% 이상 유지
- 새로운 코드에 대한 테스트가 없으면 리뷰에서 반려
