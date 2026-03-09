# AAP Config Server — Product Requirements Document

> **Version**: 1.0
> **Date**: 2026-03-09
> **Status**: Draft

---

## 1. 개요

### 1.1 배경

운영 환경에 배포된 여러 서비스(litellm 등)는 각자 다양한 설정값을 필요로 한다. 현재 이러한 설정들은 각 서비스별로 분산 관리되고 있어, 설정 변경 시 일관성 유지와 추적이 어렵다.

### 1.2 목적

**AAP Config Server**는 다음을 제공하는 경량 설정 관리 서버이다:

- 서비스별 설정의 중앙 집중 관리
- REST API를 통한 설정 조회
- DB 없이 동작하는 고성능 설정 서빙
- 설정 변경 이력의 자동 추적 (Git 기반)
- 시크릿과 일반 설정의 분리 관리

### 1.3 핵심 원칙

| 원칙 | 설명 |
|------|------|
| **No Database** | 외부 DB 의존성 없이 동작 |
| **Git as Source of Truth** | 모든 설정의 원본은 Git 저장소 |
| **Memory-first Serving** | 요청은 메모리에서 즉시 응답 |
| **Secret Separation** | 시크릿 값은 절대 Git에 저장하지 않음 |

---

## 2. 아키텍처

### 2.1 왜 Git+Nginx가 아닌가

기존에 검토한 "Git에 설정 파일 저장 → Nginx로 정적 서빙" 방식의 한계:

| 한계 | 설명 |
|------|------|
| 동적 질의 불가 | 조건부 필터링, 검색, 다중 키 조회가 불가능 |
| 설정 검증 없음 | 잘못된 설정이 그대로 서빙됨 |
| 접근 제어 어려움 | 서비스별/환경별 접근 제어가 Nginx만으로는 복잡 |
| 시크릿 통합 불가 | K8s Secret 값을 Nginx가 직접 읽어 응답할 수 없음 |
| 설정 조합/상속 불가 | 공통 설정 + 서비스별 오버라이드 같은 패턴 불가 |

### 2.2 제안 아키텍처: Git-backed In-memory Config Server

```
┌─────────────────────────────────────────────────────────┐
│                    Config Server (Go)                   │
│                                                         │
│  ┌──────────┐    ┌──────────────┐    ┌───────────────┐  │
│  │ Git Sync │───▶│ In-Memory    │◀───│ K8s Secret    │  │
│  │ (poll /  │    │ Config Store │    │ Loader        │  │
│  │ webhook) │    │              │    │ (volume mount)│  │
│  └──────────┘    └──────┬───────┘    └───────────────┘  │
│                         │                               │
│                  ┌──────▼───────┐                        │
│                  │   REST API   │                        │
│                  │   Handler    │                        │
│                  └──────┬───────┘                        │
│                         │                               │
└─────────────────────────┼───────────────────────────────┘
                          │
              ┌───────────▼───────────┐
              │   litellm / 기타      │
              │   클라이언트 서비스     │
              └───────────────────────┘
```

**핵심 아이디어**: Kubernetes API Server가 etcd를 source of truth로 쓰되 메모리 캐시로 응답하는 것과 동일한 패턴.

#### 동작 흐름

1. **시작 시**: Git 저장소를 clone/pull → 모든 설정 파일을 파싱 → 메모리에 적재
2. **실행 중**: REST API 요청 → 메모리에서 즉시 응답 (I/O 없음)
3. **설정 변경 시**: Git webhook 또는 주기적 poll → 변경 감지 → 메모리 갱신 (hot reload)
4. **시크릿 요청 시**: 메타데이터(Git) + 시크릿 값(K8s Volume Mount) 조합하여 응답

### 2.3 왜 이 아키텍처가 더 나은가

| 기존 (Git+Nginx) | 제안 (Git-backed In-memory) |
|---|---|
| 파일 단위 정적 서빙 | 키 단위 동적 질의 가능 |
| 설정 변경 → git pull 필요 | Webhook/poll로 자동 갱신 |
| 시크릿 별도 관리 필요 | 통합 API로 시크릿 포함 응답 |
| 디스크 I/O | 메모리 응답 (수십만 req/s 가능) |
| Nginx 설정 관리 추가 부담 | 단일 바이너리, 자체 서빙 |

---

## 3. 저장소 설계

### 3.1 설정 저장소 구조 (Git Repository)

DB를 대체하는 핵심 설계. Git 저장소 자체가 설정 데이터베이스 역할을 한다.

```
config-repo/
├── _defaults/                    # 전역 기본 설정
│   └── common.yaml
│
├── orgs/
│   └── {org-name}/
│       ├── _defaults/            # 조직 레벨 기본 설정
│       │   └── common.yaml
│       │
│       └── projects/
│           └── {project-name}/
│               ├── _defaults/    # 프로젝트 레벨 기본 설정
│               │   └── common.yaml
│               │
│               └── services/
│                   └── {service-name}/
│                       ├── config.yaml      # 일반 설정
│                       └── secrets.yaml     # 시크릿 메타데이터 (값 없음)
```

### 3.2 설정 파일 형식

#### 일반 설정 (`config.yaml`)

```yaml
# orgs/myorg/projects/ai-platform/services/litellm/config.yaml
version: "1"
metadata:
  service: litellm
  org: myorg
  project: ai-platform
  updated_at: "2026-03-09T10:00:00Z"

config:
  general_settings:
    master_key_secret_ref: "litellm-master-key"    # → secrets.yaml 참조
    database_url_secret_ref: "litellm-db-url"

  model_list:
    - model_name: "gpt-4"
      litellm_params:
        model: "azure/gpt-4"
        api_base: "https://my-azure.openai.azure.com"
        api_key_secret_ref: "azure-gpt4-api-key"  # → secrets.yaml 참조

    - model_name: "claude-sonnet"
      litellm_params:
        model: "anthropic/claude-sonnet-4-20250514"
        api_key_secret_ref: "anthropic-api-key"

  router_settings:
    routing_strategy: "least-busy"
    num_retries: 3
```

#### 시크릿 메타데이터 (`secrets.yaml`)

```yaml
# orgs/myorg/projects/ai-platform/services/litellm/secrets.yaml
version: "1"
secrets:
  - id: "litellm-master-key"
    description: "LiteLLM master API key"
    k8s_secret:
      name: "litellm-secrets"          # K8s Secret 객체 이름
      namespace: "ai-platform"
      key: "master-key"                # Secret 내 data key

  - id: "azure-gpt4-api-key"
    description: "Azure OpenAI GPT-4 API Key"
    k8s_secret:
      name: "llm-provider-keys"
      namespace: "ai-platform"
      key: "azure-gpt4"

  - id: "anthropic-api-key"
    description: "Anthropic API Key"
    k8s_secret:
      name: "llm-provider-keys"
      namespace: "ai-platform"
      key: "anthropic"
```

### 3.3 시크릿 관리 전략

기존 검토안(K8s Secret 객체를 JSON/YAML로 Git에 저장하고 시크릿 값은 K8s Secret으로 관리)을 개선한다.

#### 제안: Volume Mount + Reference 패턴

```
┌─ Git Repo ─────────────────────┐
│  secrets.yaml (메타데이터만)     │
│  - id: "api-key-abc"           │
│  - k8s_secret: name, key       │
│  - description, rotation_policy│
└────────────┬───────────────────┘
             │ 참조
             ▼
┌─ K8s Secret ───────────────────┐
│  name: "llm-provider-keys"     │
│  data:                         │
│    azure-gpt4: <base64 값>     │
│    anthropic: <base64 값>      │
└────────────┬───────────────────┘
             │ volume mount
             ▼
┌─ Config Server Pod ────────────┐
│  /secrets/ai-platform/         │
│    llm-provider-keys/          │
│      azure-gpt4                │
│      anthropic                 │
└────────────────────────────────┘
```

**왜 Volume Mount인가 (vs K8s API 호출)**:

| 방식 | 장점 | 단점 |
|------|------|------|
| K8s API 호출 | 실시간 조회 | API 서버 부하, RBAC 복잡, 네트워크 의존 |
| **Volume Mount** | **파일 읽기로 초고속, K8s가 자동 갱신, 네트워크 불필요** | **갱신 지연 ~1분 (kubelet sync period)** |

설정 서버 특성상 시크릿의 1분 이내 갱신 지연은 허용 가능하므로, Volume Mount가 최적이다.

#### 시크릿 값 Resolve 흐름

1. 클라이언트가 설정 요청 (예: litellm config)
2. Config Server가 `config.yaml`에서 `*_secret_ref` 필드 탐지
3. `secrets.yaml`에서 해당 ID의 K8s Secret 경로 확인
4. Volume Mount 경로에서 실제 값 읽기 (`/secrets/{namespace}/{name}/{key}`)
5. 설정에 시크릿 값을 인라인하여 응답

```json
// 클라이언트가 받는 최종 응답 (시크릿 resolved)
{
  "general_settings": {
    "master_key": "sk-actual-master-key-value"
  },
  "model_list": [
    {
      "model_name": "gpt-4",
      "litellm_params": {
        "model": "azure/gpt-4",
        "api_key": "actual-azure-api-key"
      }
    }
  ]
}
```

### 3.4 설정 상속 (Config Inheritance)

DB 없이도 설정의 중복을 줄이기 위한 계층적 상속 구조:

```
_defaults/common.yaml          (전역 기본값)
  ↓ override
orgs/{org}/_defaults/common.yaml   (조직 기본값)
  ↓ override
orgs/{org}/projects/{proj}/_defaults/common.yaml  (프로젝트 기본값)
  ↓ override
orgs/{org}/projects/{proj}/services/{svc}/config.yaml  (서비스 설정)
```

Merge 전략: **deep merge with override** — 하위 레벨이 상위 레벨의 동일 키를 덮어쓴다.

---

## 4. API 설계

### 4.1 설정 조회 API

#### 단일 서비스 설정 조회

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config
```

**Query Parameters**:

| 파라미터 | 타입 | 설명 |
|----------|------|------|
| `resolve_secrets` | bool | `true`면 시크릿 값을 인라인 (default: `false`) |
| `keys` | string | 쉼표 구분, 특정 키만 반환 (예: `model_list,router_settings`) |
| `format` | string | `yaml` 또는 `json` (default: `json`) |
| `inherit` | bool | `true`면 상위 레벨 기본값 merge (default: `true`) |

**Response**:

```json
{
  "metadata": {
    "org": "myorg",
    "project": "ai-platform",
    "service": "litellm",
    "version": "a3f2b1c",
    "updated_at": "2026-03-09T10:00:00Z"
  },
  "config": {
    "general_settings": { ... },
    "model_list": [ ... ],
    "router_settings": { ... }
  }
}
```

#### 다중 서비스 설정 일괄 조회

```
POST /api/v1/configs/batch
```

```json
{
  "queries": [
    { "org": "myorg", "project": "ai-platform", "service": "litellm" },
    { "org": "myorg", "project": "ai-platform", "service": "gateway" }
  ],
  "resolve_secrets": false
}
```

### 4.2 설정 변경 감지 API

#### Long Polling

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config/watch?version={current_version}
```

- 현재 클라이언트가 가진 `version`(git commit hash)과 서버의 최신 버전이 다르면 즉시 응답
- 같으면 변경이 생길 때까지 hold (최대 30초 후 `304 Not Modified`)
- 클라이언트는 응답 받은 후 다시 요청 (long polling loop)

### 4.3 헬스체크 / 운영 API

```
GET /healthz                      # Liveness
GET /readyz                       # Readiness (git sync 완료 여부)
GET /api/v1/status                # 서버 상태 (마지막 sync 시각, 로드된 설정 수 등)
POST /api/v1/admin/reload         # 수동 설정 리로드 트리거
```

---

## 5. 고성능 설계

### 5.1 성능 목표

| 지표 | 목표 |
|------|------|
| Throughput | 100,000+ req/s (단일 인스턴스) |
| Latency (p99) | < 5ms (시크릿 미포함), < 10ms (시크릿 포함) |
| Memory | 설정 1,000개 기준 < 100MB |
| 시작 시간 | < 5초 (cold start) |

### 5.2 성능 전략

#### (1) In-Memory Store

```go
// 핵심 자료구조
type ConfigStore struct {
    mu       sync.RWMutex
    configs  map[string]*ResolvedConfig  // key: "org/project/service"
    version  string                       // current git commit hash
}
```

- 모든 읽기는 `RLock` → 동시 읽기 무제한
- 쓰기(설정 갱신)는 `Lock` → COW(Copy-on-Write) 패턴으로 읽기 차단 최소화

#### (2) Copy-on-Write 갱신

```
1. 새 Git 변경 감지
2. 변경된 설정만 파싱 (diff 기반)
3. 새 map 생성 (기존 map 복사 + 변경분 적용)
4. atomic pointer swap (sync.RWMutex Lock 최소 구간)
5. 기존 map은 진행 중인 요청 완료 후 GC
```

→ 갱신 중에도 읽기 요청은 거의 차단되지 않음

#### (3) HTTP 최적화

- **HTTP/2 지원**: 다중 요청 멀티플렉싱
- **ETag / If-None-Match**: 변경 없으면 `304` 응답 (body 전송 생략)
- **gzip 응답 압축**: 대용량 설정 응답 시 대역폭 절약
- **Connection pooling**: Keep-Alive로 연결 재사용

#### (4) 수평 확장

```
                    ┌─ Config Server Pod 1 ─┐
                    │  (in-memory cache)     │
 Client ──▶ LB ───▶├─ Config Server Pod 2 ─┤
                    │  (in-memory cache)     │
                    └─ Config Server Pod 3 ─┘

 각 Pod가 독립적으로 Git sync → 모든 Pod가 동일 데이터 보유
 → Stateless, 어떤 Pod로 요청해도 동일 결과
```

---

## 6. 설정 변경 워크플로우

### 6.1 일반 설정 변경

```
Developer                Git Repo              Config Server
   │                        │                       │
   ├─ config.yaml 수정 ────▶│                       │
   ├─ PR 생성 ─────────────▶│                       │
   │                        │                       │
   │   (리뷰 & 승인)         │                       │
   │                        │                       │
   ├─ merge ───────────────▶│                       │
   │                        ├─ webhook ────────────▶│
   │                        │                       ├─ git pull
   │                        │                       ├─ 설정 파싱
   │                        │                       ├─ 메모리 갱신
   │                        │                       │
   │   (다음 API 요청부터 새 설정 반영)               │
```

### 6.2 시크릿 변경

```
Admin                  K8s Cluster            Config Server
  │                        │                       │
  ├─ kubectl edit secret ─▶│                       │
  │   (또는 Sealed Secrets) │                       │
  │                        ├─ volume update ───────▶│
  │                        │   (kubelet sync)       ├─ fsnotify 감지
  │                        │                       ├─ 시크릿 값 갱신
  │                        │                       │
  │   (다음 API 요청부터 새 시크릿 반영)              │
```

---

## 7. 보안

### 7.1 인증/인가

| 계층 | 방식 |
|------|------|
| 서비스 간 인증 | mTLS 또는 Service Account Token |
| API 인증 | Bearer Token (static token 또는 K8s SA token) |
| 시크릿 접근 제어 | `resolve_secrets=true` 요청 시 별도 권한 검증 |

### 7.2 시크릿 보호

- 시크릿 값은 **절대 Git에 저장하지 않음**
- 시크릿 값은 **절대 로그에 출력하지 않음**
- `resolve_secrets=true` 응답은 **캐시하지 않음** (ETag 미적용)
- 시크릿이 포함된 응답에 `Cache-Control: no-store` 헤더 설정

---

## 8. 기술 스택

| 구성 요소 | 기술 | 선택 이유 |
|-----------|------|-----------|
| 언어 | **Go** | 고성능 HTTP 서버, 단일 바이너리, 낮은 메모리 사용, K8s 생태계 친화 |
| HTTP 서버 | `net/http` (stdlib) | 외부 의존성 최소화, HTTP/2 기본 지원 |
| Router | `go-chi/chi` 또는 stdlib `mux` (Go 1.22+) | 경량, 미들웨어 체이닝 |
| Git 연동 | `go-git` 또는 shell exec `git` | in-process git 조작 |
| YAML 파싱 | `gopkg.in/yaml.v3` | 표준 YAML 라이브러리 |
| 파일 감시 | `fsnotify` | Volume Mount 변경 감지 |
| 로깅 | `slog` (stdlib) | 구조화 로깅, 외부 의존성 없음 |
| 메트릭 | `prometheus/client_golang` | K8s 모니터링 표준 |
| 컨테이너 | Distroless 또는 Alpine 기반 | 최소 이미지 사이즈 |

---

## 9. 마일스톤

### Phase 1: Core (MVP)

- [ ] Go 프로젝트 구조 세팅
- [ ] Git 저장소 clone/pull 및 설정 파일 파싱
- [ ] In-memory config store 구현
- [ ] REST API: 단일 서비스 설정 조회 (`GET /api/v1/.../config`)
- [ ] 주기적 Git poll 기반 설정 갱신
- [ ] Health check 엔드포인트
- [ ] Dockerfile 및 기본 K8s manifests

### Phase 2: Secrets & Security

- [ ] Volume Mount 기반 시크릿 로딩
- [ ] `secrets.yaml` 파싱 및 시크릿 참조 resolve
- [ ] `resolve_secrets` 쿼리 파라미터 구현
- [ ] Bearer Token 인증 미들웨어
- [ ] 시크릿 접근 감사 로깅

### Phase 3: Performance & Operations

- [ ] ETag / If-None-Match 지원
- [ ] gzip 응답 압축
- [ ] Prometheus 메트릭 export
- [ ] Git webhook 수신 엔드포인트
- [ ] Config watch (long polling) API
- [ ] Batch 조회 API
- [ ] 설정 상속 (계층적 _defaults merge)

### Phase 4: Hardening

- [ ] Graceful shutdown
- [ ] 설정 파일 스키마 검증
- [ ] Rate limiting
- [ ] 통합 테스트 / 부하 테스트
- [ ] Helm chart

---

## 10. 대안 검토 기록

아래는 검토 후 채택하지 않은 대안들과 그 사유이다.

| 대안 | 검토 결과 | 미채택 사유 |
|------|-----------|------------|
| **Git + Nginx 정적 서빙** | 가장 단순 | 동적 질의, 시크릿 통합, 설정 검증 불가 |
| **Embedded KV (bbolt, badger)** | 고성능 | 사실상 DB이며, Git 버전 관리와 이중 관리 |
| **K8s ConfigMap으로 모든 설정 관리** | K8s 네이티브 | ConfigMap 1MB 제한, 변경 추적 어려움, PR 리뷰 불가 |
| **etcd 직접 사용** | 검증된 KV | 별도 클러스터 운영 부담, DB와 유사한 운영 복잡도 |
| **Spring Cloud Config Server** | 성숙한 솔루션 | JVM 기반으로 리소스 과다, K8s 시크릿 통합 부재 |
| **K8s API로 Secret 실시간 조회** | 최신 값 보장 | API Server 부하, 네트워크 의존, 고빈도 조회 시 throttling |

---

## 부록 A: Config Server vs 기존 방식 비교

```
[기존: 서비스별 분산 관리]

litellm Pod                    gateway Pod
├── ConfigMap (설정)            ├── ConfigMap (설정)
├── Secret (시크릿)             ├── Secret (시크릿)
└── 변경 시 재배포 필요          └── 변경 시 재배포 필요

→ 문제: 설정 분산, 이력 추적 불가, 일괄 변경 어려움


[제안: Config Server 중앙 관리]

Config Git Repo (source of truth)
    ↓ sync
Config Server (in-memory)
    ↓ REST API
├── litellm Pod (설정 조회)
├── gateway Pod (설정 조회)
└── 기타 서비스들...

→ 장점: 중앙 관리, Git 이력, PR 리뷰, 무중단 변경, 일괄 관리
```
