# AAP Config Server — Product Requirements Document

> **버전**: 2.1
> **작성일**: 2026-03-12
> **최종 수정일**: 2026-04-29
> **상태**: **Approved design — Phase-1 implementation in progress.** 문서가 설명하는 전체 v2.1 계약 중 Config Agent, App Registry webhook, watch/history/revert, inheritance 등은 아직 구현되지 않았다. SealedSecret write/resolve path는 구현되어 있으며, 실제로 현재 제공되는 범위는 [README.md](../README.md#feature-matrix) 의 Feature Matrix를 기준으로 한다. 본 문서의 API/저장소/아키텍처 기술은 **목표 계약**이며, 구현되지 않은 엔드포인트·필드는 Phase-1에서 명시적으로 거부된다.
> **참조**: [HLD](./02_HLD.md) · [development-process.md](./development-process.md) · [README.md Feature Matrix](../README.md#feature-matrix)

---

## 목차

1. 개요
2. 아키텍처 개요
3. 저장소 설계
4. 핵심 기능 요구사항
5. API 요약
6. 비기능 요구사항
7. 기술 스택
8. 개발 프로세스: TDD
9. 마일스톤

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
| **Git as Source of Truth** | 모든 설정의 원본은 `aap-helm-charts` Git 저장소의 `configs/` 디렉토리 |
| **Memory-first Serving** | 요청은 메모리에서 즉시 응답 |
| **Secret Separation** | 시크릿 평문은 절대 Git에 저장하지 않음. SealedSecret(암호화된 형태)만 Git에 저장 |
| **Secret at Rest** | 시크릿 저장은 K8s Secret(etcd encryption at rest)에 위임. Git에는 SealedSecret으로 암호화 저장 |
| **Console Creates, Server Manages** | Console은 설정/시크릿 생성·변경을 요청만 하고, Config Server가 검증/Git 저장/적용의 관리 주체 |

### 1.4 기능 요구사항 요약 (FR Index)

본 문서 전체에 걸쳐 정의된 기능 요구사항(FR)의 인덱스이다. 각 FR은 섹션 4에서 `[FR-X]` 태그로 정의된다.

| FR ID | 기능 요구사항 | 정의 섹션 | Phase |
|-------|-------------|----------|-------|
| **FR-1** | Git-backed In-memory Config Store | 4.1 | 1 |
| **FR-2** | 설정 조회 API (`GET .../config`) | 4.2 | 1 |
| **FR-3** | 환경변수 조회 API (`GET .../env_vars`) | 4.3 | 1 |
| **FR-4** | 설정/시크릿 일괄 변경 Admin API (`POST /api/v1/admin/changes`) | 4.4 | 1 |
| **FR-5** | 설정/시크릿 일괄 삭제 Admin API (`DELETE /api/v1/admin/changes`) | 4.5 | 1 |
| **FR-6** | 설정/환경변수 변경 감지 API (Long Polling) | 4.6 | 5 |
| **FR-7** | 시크릿 관리 (SealedSecret + Volume Mount) | 4.7 | 2 |
| **FR-8** | App Registry 연동 (Console webhook) | 4.8 | 2 |
| **FR-9** | Config Agent (중앙 집중형) | 4.9 | 3 |
| **FR-10** | 설정 상속 (Config Inheritance) | 4.10 | 5 |
| **FR-11** | 서비스 탐색 API (`GET /api/v1/orgs`, `.../projects`, `.../services`) | 4.11 | 5 |
| **FR-12** | 시크릿 메타데이터 API (`GET .../secrets`) | 4.12 | 5 |
| **FR-13** | 변경 이력 API (`GET .../history`) | 4.13 | 5 |
| **FR-14** | 설정 롤백 API (`POST /api/v1/admin/changes/revert`) | 4.14 | 5 |
| **FR-15** | 헬스체크 / 운영 API (`/healthz`, `/readyz`, `/api/v1/status`) | 4.15 | 1 |
| **FR-16** | 인증/인가 (API Key Bearer 인증) | 4.16 | 4 |
| **FR-17** | 시크릿 보안 (전송/저장/로그 보호) | 4.17 | 4 |

> **Console PRD 연관**: Console PRD의 FR-3(Project 삭제), FR-5(Langfuse SK/PK), FR-6(LiteLLM Config), FR-7(프로비저닝 파이프라인), FR-8(버전 롤백)이 Config Server의 FR-4, FR-5, FR-7, FR-8, FR-14를 호출한다.

---

## 2. 아키텍처 개요

### 2.1 시스템 구조

```
                    ┌─────────────┐
                    │ AAP Console │
                    └──────┬──────┘
                           │ Admin API
                           │ (설정 + 시크릿)
                           ▼
┌──────────────────────────────────────────────────┐
│              Config Server (Go)                   │
│                                                   │
│  ┌──────────────┐  ┌──────────────┐              │
│  │ SealedSecret │  │ In-Memory    │              │
│  │ Manager      │  │ Config Store │              │
│  │ (kubeseal)   │  │ (config +    │              │
│  └──┬───────┬───┘  │  env_vars +  │              │
│     │       │      │  secrets)    │              │
│     │       │      └──────┬──────┘              │
│     │       │      ┌──────▼──────┐              │
│     │       │      │  REST API   │              │
│     │       │      │  Handler    │              │
│     │       │      └──────┬──────┘              │
└─────┼───────┼─────────────┼──────────────────────┘
      │       │      ▲  ▲   │
      │ git   │  git │  │   │ polling
      │ push  │ sync │  │   │ (30초)
      │       │      │  │   │
      │  ┌────▼──────┴──┐   │  volume
      │  │aap-helm-    │   │  mount
      │  │charts       │   │   │
      │  │(configs/ +  │   │   │
      │  │sealed-      │   │   │
      │  │secrets/)    │   │   │
      │  └──────────────┘   │   │
      │                     │   │
      │ apply  ┌────────────┴─┐ │
      │        │  K8s Secret  │ │
      │        │  (etcd)      │ │
      │        └──────────────┘ │
      │              ▲          │
      │              │ 복호화    │
      ▼              │          │
┌─────────────┐      │          │
│SealedSecret │──────┘          │
│Controller   │                 │
│(SealedSecret                  │
│ → K8s Secret)                 │
└─────────────┘                 │
                                │
              ┌─────────────────▼─────────┐
              │   Config Agent            │  Deployment (replica=2)
              │   (중앙 집중형)             │  per target Deployment
              │                           │
              │  1. Config Server         │
              │     polling (30초)         │
              │  2. K8s ConfigMap/Secret  │
              │     업데이트               │
              │  3. Rolling restart       │
              │     (항상 수행)             │
              │     maxUnavailable: 25%   │
              │     maxSurge: 25%         │
              └───────────┬───────────────┘
                          │ ConfigMap/Secret update
                          │ + Deployment annotation 패치
              ┌───────────▼───────────┐
              │   litellm Deployment  │  replicas: 32
              │                       │
              │  Pod 1..32 각각       │  ConfigMap volume mount로
              │  동일 config 공유      │  설정 파일 수신
              └───────────────────────┘
```

Kubernetes API Server가 etcd를 source of truth로 쓰되 메모리 캐시로 응답하는 것과 동일한 패턴이다.

### 2.2 동작 흐름

1. **시작 시**: `aap-helm-charts` Git 저장소를 clone/pull → `configs/` 하위의 모든 설정 파일을 파싱 → 메모리에 적재
2. **실행 중**: REST API 요청 → 메모리에서 즉시 응답 (I/O 없음)
3. **설정 쓰기 시**: Console Admin API (`POST /api/v1/admin/changes`) 수신 → 스키마 검증 → 설정/시크릿 일괄 처리 → 단일 Git commit & push → 메모리 갱신
4. **설정 변경 감지 시**: Git webhook 또는 주기적 poll → 변경 감지 → 메모리 갱신
5. **시크릿 요청 시**: 메타데이터(Git) + 시크릿 값(K8s Volume Mount) 조합 → 클러스터 내부 네트워크로 응답
6. **시크릿 생성/변경 시**: `POST /api/v1/admin/changes`의 `secrets` 필드 수신 → kubeseal 암호화 → SealedSecret YAML Git push + K8s apply → SealedSecret Controller가 K8s Secret 생성
7. **클라이언트 설정 전파**: Config Agent(중앙 Deployment)가 Config Server를 polling → 변경 감지 시 K8s ConfigMap 업데이트 → 각 Pod에 volume mount로 전파 → rolling restart 트리거 (maxUnavailable/maxSurge: 25%)

### 2.3 수평 확장

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

## 3. 저장소 설계

### 3.1 설정 저장소 구조 (`aap-helm-charts` Git Repository)

`aap-helm-charts` 레포가 AAP 서비스의 Helm Chart와 Config Server의 설정 데이터를 모두 포함한다. Config Server는 이 레포의 `configs/` 디렉토리를 설정 데이터베이스로 사용한다. SealedSecret YAML도 같은 레포에 저장하여 **설정과 시크릿의 단일 atomic commit**을 보장한다.

```
aap-helm-charts/
├── charts/                          # Helm Charts (Config Server 관심 밖)
│   ├── litellm/
│   ├── langfuse/
│   └── ...
│
├── configs/                         # Config Server 설정 데이터 루트
│   ├── _defaults/                   # 전역 기본 설정
│   │   └── common.yaml
│   │
│   └── orgs/
│       └── {org-name}/
│           ├── _defaults/           # 조직 레벨 기본 설정
│           │   └── common.yaml
│           │
│           └── projects/
│               └── {project-name}/
│                   ├── _defaults/   # 프로젝트 레벨 기본 설정
│                   │   └── common.yaml
│                   │
│                   └── services/
│                       └── {service-name}/
│                           ├── config.yaml              # 서비스 설정 (proxy_config 등)
│                           ├── env_vars.yaml            # 환경변수 (plain + secret refs)
│                           ├── secrets.yaml             # 시크릿 메타데이터 (값 없음)
│                           └── sealed-secrets/          # SealedSecret YAML (암호화됨)
│                               ├── litellm-secrets.yaml
│                               ├── llm-provider-keys.yaml
│                               ├── litellm-infra.yaml
│                               └── guardrail-keys.yaml
```

> **레포 통합 이유**: `POST /api/v1/admin/changes`에서 config + secrets를 단일 atomic Git commit으로 처리하려면 모든 파일이 같은 레포에 있어야 한다. `aap-helm-charts`는 이미 AAP 서비스 배포의 중심 레포이며, SealedSecret YAML은 본질적으로 K8s 배포 매니페스트이므로 여기에 저장하는 것이 자연스럽다.
>
> **Config Server 접근 범위**: Config Server는 `configs/` 하위만 읽고 쓴다. `charts/` 디렉토리는 Config Server의 관심 밖이다.

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
  model_list:
    - model_name: "azure-gpt4"
      litellm_params:
        model: "azure/gpt-4"
        api_base: "https://my-azure.openai.azure.com"
        api_key_secret_ref: "azure-gpt4-api-key"      # → secrets.yaml 참조
        api_version: "2024-06-01"
      model_info:
        id: "azure-gpt4-eastus"
        description: "Azure GPT-4 East US"

    - model_name: "claude-sonnet"
      litellm_params:
        model: "anthropic/claude-sonnet-4-20250514"
        api_key_secret_ref: "anthropic-api-key"        # → secrets.yaml 참조
      model_info:
        id: "anthropic-sonnet"

  general_settings:
    master_key_secret_ref: "litellm-master-key"        # → secrets.yaml 참조
    database_url_secret_ref: "litellm-db-url"          # → secrets.yaml 참조
    alert_types:
      - "llm_exceptions"
      - "llm_requests_hanging"

  router_settings:
    routing_strategy: "least-busy"
    num_retries: 3
    timeout: 60

  litellm_settings:
    drop_params: true
    set_verbose: false
    max_budget: 1000.0
    budget_duration: "30d"

  guardrails:
    - guardrail_name: "content-filter"
      litellm_params:
        guardrail: "aporia"
        mode: "pre_call"
        api_base: "https://guardrail.internal"
        api_key_secret_ref: "guardrail-api-key"        # → secrets.yaml 참조

  application:
    - application_name: "chatbot-prod"
      application_id: "app-001"
      allowed_models:
        - "azure-gpt4"
        - "claude-sonnet"
```

#### 환경변수 설정 (`env_vars.yaml`)

litellm 같은 서비스는 설정 파일 외에도 환경변수로 동작을 제어한다. Helm chart의 `envVars`, `extraEnvVars`에 해당하는 값들을 config server에서 중앙 관리한다.

```yaml
# orgs/myorg/projects/ai-platform/services/litellm/env_vars.yaml
version: "1"
metadata:
  service: litellm
  org: myorg
  project: ai-platform

env_vars:
  # 평문 환경변수 (non-secret)
  plain:
    LITELLM_LOG_LEVEL: "INFO"
    LITELLM_NUM_WORKERS: "4"
    LITELLM_PORT: "4000"
    UI_USERNAME: "admin"
    PROXY_BASE_URL: "https://litellm.internal.example.com"
    STORE_MODEL_IN_DB: "false"
    LITELLM_TELEMETRY: "false"

  # 시크릿 환경변수 (secret_ref → secrets.yaml 참조)
  secret_refs:
    AZURE_API_KEY: "azure-gpt4-api-key"          # → secrets.yaml의 id
    ANTHROPIC_API_KEY: "anthropic-api-key"
    GUARDRAIL_API_KEY: "guardrail-api-key"
    DATABASE_URL: "litellm-db-url"             # → secrets.yaml의 id
    LITELLM_MASTER_KEY: "litellm-master-key"
    UI_PASSWORD: "litellm-ui-password"
    REDIS_HOST: "litellm-redis-host"
    REDIS_PASSWORD: "litellm-redis-password"
    SMTP_PASSWORD: "litellm-smtp-password"
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
  # ... (서비스별 시크릿 항목 추가)
```

---

## 4. 핵심 기능 요구사항

> 이하 모든 FR은 이 섹션에서 정의된다.

### 4.1 FR-1: Git-backed In-memory Config Store `[FR-1]`

`aap-helm-charts` Git 저장소의 `configs/` 하위 설정 파일을 메모리에 적재하고, REST API 요청 시 메모리에서 즉시 응답하는 핵심 저장소 엔진.

#### 메모리 구조

```go
// 핵심 자료구조 (상세: HLD 섹션 6.3, 11.2 참조)
type Store struct {
    mu       sync.RWMutex
    configs  map[string]*ResolvedConfig  // key: "org/project/service"
    version  string                       // current git commit hash
    repo     GitRepo                      // interface 주입 (DI)
}

// 모든 public 메서드는 context.Context를 첫 번째 파라미터로 받는다
func (s *Store) GetConfig(ctx context.Context, org, project, service string) (*ResolvedConfig, error)
func (s *Store) ApplyChanges(ctx context.Context, req *ChangeRequest) (*ChangeResult, error)
```

- 모든 읽기는 `RLock` → 동시 읽기 무제한
- 쓰기(설정 갱신)는 `Lock` → COW(Copy-on-Write) 패턴으로 읽기 차단 최소화
- 외부 의존성(Git 등)은 interface로 추상화하여 테스트에서 mock 교체 가능

#### Copy-on-Write 갱신

```
1. 새 Git 변경 감지
2. 변경된 설정만 파싱 (diff 기반)
3. 새 map 생성 (기존 map 복사 + 변경분 적용)
4. atomic pointer swap (sync.RWMutex Lock 최소 구간)
5. 기존 map은 진행 중인 요청 완료 후 GC
```

갱신 중에도 읽기 요청은 거의 차단되지 않는다.

#### HTTP 최적화

- **HTTP/2 지원**: 다중 요청 멀티플렉싱
- **ETag / If-None-Match**: 변경 없으면 `304` 응답 (body 전송 생략, 시크릿 미포함 응답에만 적용)
- **gzip 응답 압축**: 대용량 설정 응답 시 대역폭 절약
- **Connection pooling**: Keep-Alive로 연결 재사용

#### 성능 목표

| 지표 | 목표 |
|------|------|
| Throughput | 100,000+ req/s (단일 인스턴스) |
| Latency (p99) | < 5ms |
| Memory | 설정 1,000개 기준 < 100MB |
| 시작 시간 | < 5초 (cold start) |

### 4.2 FR-2: 설정 조회 API `[FR-2]`

#### 단일 서비스 설정 조회

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config
```

**Request Headers**:

| 헤더 | 필수 | 설명 |
|------|------|------|
| `Authorization` | Y | `Bearer <API_KEY>` — 환경변수로 설정된 API Key |

**Query Parameters**:

| 파라미터 | 타입 | 설명 |
|----------|------|------|
| `keys` | string | 쉼표 구분, 특정 키만 반환 (예: `model_list,router_settings`) |
| `format` | string | `yaml` 또는 `json` (default: `json`) |
| `inherit` | bool | `true`면 상위 레벨 기본값 merge (default: `true`) |
| `version` | string | 특정 Git commit hash의 설정을 반환 (default: 최신) |

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
    "general_settings": {
      "master_key_secret_ref": "litellm-master-key"
    },
    "model_list": [ ... ],
    "router_settings": { ... }
  }
}
```

- Config API는 시크릿을 resolve하지 않는다. `*_secret_ref` 참조를 그대로 반환한다.
- 시크릿 resolve는 환경변수 API(`GET .../env_vars?resolve_secrets=true`)에서만 지원한다.

#### 다중 서비스 설정 일괄 조회

```
POST /api/v1/configs/batch
```

```json
{
  "queries": [
    { "org": "myorg", "project": "ai-platform", "service": "litellm" },
    { "org": "myorg", "project": "ai-platform", "service": "gateway" }
  ]
}
```

### 4.3 FR-3: 환경변수 조회 API `[FR-3]`

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars
```

**Query Parameters**:

| 파라미터 | 타입 | 설명 |
|----------|------|------|
| `resolve_secrets` | bool | `true`면 `secret_refs`의 시크릿 값을 resolve하여 평문으로 포함 (default: `false`) |
| `format` | string | `yaml` 또는 `json` (default: `json`) |
| `inherit` | bool | `true`면 상위 레벨 기본값 merge (default: `true`) |
| `version` | string | 특정 Git commit hash의 설정을 반환 (default: 최신) |

**Response** (`resolve_secrets=false`):

```json
{
  "metadata": {
    "org": "myorg",
    "project": "ai-platform",
    "service": "litellm",
    "version": "a3f2b1c"
  },
  "env_vars": {
    "plain": {
      "LITELLM_LOG_LEVEL": "INFO",
      "LITELLM_NUM_WORKERS": "4",
      "LITELLM_PORT": "4000"
    },
    "secret_refs": {
      "DATABASE_URL": "litellm-db-url",
      "LITELLM_MASTER_KEY": "litellm-master-key"
    }
  }
}
```

**Response** (`resolve_secrets=true`): `secret_refs`의 각 값이 실제 시크릿 평문으로 치환되어 응답

```json
{
  "metadata": {
    "org": "myorg",
    "project": "ai-platform",
    "service": "litellm",
    "version": "a3f2b1c"
  },
  "env_vars": {
    "plain": {
      "LITELLM_LOG_LEVEL": "INFO",
      "LITELLM_NUM_WORKERS": "4",
      "LITELLM_PORT": "4000"
    },
    "secrets": {
      "DATABASE_URL": "postgresql://user:EXAMPLE@host:5432/db",
      "LITELLM_MASTER_KEY": "EXAMPLE-master-key-replace-me"
    }
  }
}
```

> `resolve_secrets=false`에서는 `secret_refs` (시크릿 ID 참조), `resolve_secrets=true`에서는 `secrets` (실제 평문 값)로 키 이름이 달라진다.

### 4.4 FR-4: 설정/시크릿 일괄 변경 Admin API `[FR-4]`

> **`aap-console` PRD 참조**: Console → Config Server 인터페이스는 Console PRD Section 3 "시스템 아키텍처 개요"에 정의된 Admin API 계약을 따른다.

Console이 설정과 시크릿을 **한 번에** 전달하면, Config Server가 설정 파일 갱신 + 시크릿 암호화(kubeseal) + SealedSecret apply를 **단일 atomic Git 커밋**으로 처리한다. Console은 Git/kubeseal/kubectl 의존성 없이 이 API만 호출한다.

```
POST /api/v1/admin/changes
```

**Request Headers**:

| 헤더 | 필수 | 설명 |
|------|------|------|
| `Authorization` | Y | `Bearer <API_KEY>` — 환경변수로 설정된 API Key |

**Request Body**:

```json
{
  "org": "myorg",
  "project": "ai-platform",
  "service": "litellm",
  "config": {
    "model_list": [
      {
        "model_name": "azure-gpt4",
        "litellm_params": {
          "model": "azure/gpt-4",
          "api_base": "https://my-azure.openai.azure.com",
          "api_key_secret_ref": "azure-gpt4-api-key",
          "api_version": "2024-06-01"
        }
      }
    ],
    "general_settings": {
      "master_key_secret_ref": "litellm-master-key",
      "database_url_secret_ref": "litellm-db-url"
    },
    "router_settings": {
      "routing_strategy": "least-busy",
      "num_retries": 3,
      "timeout": 60
    }
  },
  "env_vars": {
    "plain": {
      "LITELLM_LOG_LEVEL": "INFO",
      "LITELLM_NUM_WORKERS": "4",
      "LITELLM_PORT": "4000"
    },
    "secret_refs": {
      "DATABASE_URL": "litellm-db-url",
      "LITELLM_MASTER_KEY": "litellm-master-key"
    }
  },
  "secrets": {
    "litellm-secrets": {
      "namespace": "ai-platform",
      "data": {
        "master-key": "actual-secret-value",
        "database-url": "postgresql://..."
      }
    }
  },
  "message": "Add azure-gpt4 model with API keys"
}
```

- `org`, `project`, `service`: 대상 서비스 경로
- `config`: config.yaml 블록 (optional — 생략 시 config.yaml 미변경)
- `env_vars`: env_vars.yaml 블록 (optional — 생략 시 env_vars.yaml 미변경)
- `secrets`: 시크릿 평문 (optional — 생략 시 시크릿 미변경). key는 K8s Secret 이름, data는 key-value 쌍
- `message`: Git commit 메시지 (optional, 없으면 자동 생성)

> 각 필드는 모두 optional이므로, 설정만 변경·시크릿만 변경·둘 다 변경 모두 이 단일 API로 처리한다.

**Response** (`200 OK`):

```json
{
  "status": "committed",
  "version": "b4c5d6e",
  "updated_at": "2026-03-10T10:00:00Z"
}
```

- `version`: Git commit hash — Console이 이 값을 DB에 저장하여 이력 추적 및 롤백에 사용

**처리 흐름**:
1. Config Server가 요청 body를 스키마 검증
2. `secret_ref` 값이 secrets 필드 또는 기존 secrets.yaml에 존재하는지 확인
3. `secrets` 필드가 있으면: kubeseal 암호화 → SealedSecret YAML 생성
4. config.yaml / env_vars.yaml / SealedSecret YAML 파일 생성/업데이트
5. 모든 변경을 **단일 Git commit** & push
6. SealedSecret이 변경되었으면 K8s API apply
7. In-memory config store 갱신
8. 다음 Config Agent polling 시 변경 감지 → ConfigMap/Secret 업데이트 → rolling restart

> **Atomic Commit 원칙**: 하나의 API 호출로 발생하는 모든 파일 변경(config.yaml, env_vars.yaml, SealedSecret YAML)은 **반드시 단일 Git commit**으로 처리한다. 이 commit hash가 해당 변경의 유일한 버전 식별자가 되며, Console은 이 값을 DB에 저장하여 이력 추적 및 롤백에 사용한다.

#### 동시 변경 처리

동일 서비스에 대한 요청은 **서비스 경로별 mutex**(`sync.Map`에 서비스 경로(`"org/project/service"`)를 key로 하여 mutex를 저장)로 직렬화한다. 다른 서비스 간 요청은 서로 다른 파일을 수정하므로 **Git 충돌이 구조적으로 불가능**하다. Mutex는 Git 작업(파일 수정 → commit → push, ~1-2초)만 보호하며, 이후의 Config Agent polling → rolling restart는 완전 비동기이다.

다른 서비스 간 병렬 처리 시 `git push` rejected가 발생할 수 있으나, 파일이 다르므로 **pull-rebase로 항상 자동 해결**된다 (최대 3회 재시도 후 `503`).

> 상세 설계: [ADR-003: 동시 변경 처리 — 서비스별 Mutex](./adr/003-concurrent-change-per-service-mutex.md)
> Phase-1 current implementation serializes Git/store writes globally; see
> [ADR-005: Phase-1 global Git serialization](./adr/005-phase-1-global-git-serialization.md).

### 4.5 FR-5: 설정/시크릿 일괄 삭제 Admin API `[FR-5]`

Project 삭제 시 해당 서비스의 설정 + 시크릿 전체를 제거한다. Console이 Project 삭제 워크플로우에서 호출한다.

```
DELETE /api/v1/admin/changes
```

**Request Body**:

```json
{
  "org": "myorg",
  "project": "ai-platform",
  "service": "litellm"
}
```

**Response** (`200 OK`):

```json
{
  "status": "deleted",
  "version": "c5d6e7f",
  "deleted_files": ["config.yaml", "env_vars.yaml", "secrets.yaml", "sealed-secrets/litellm-secrets.yaml"]
}
```

**처리 흐름**:
1. 해당 서비스 디렉토리의 config.yaml, env_vars.yaml, secrets.yaml, sealed-secrets/ 삭제
2. 단일 Git commit & push
3. In-memory store에서 해당 서비스 설정 제거
4. Config Agent가 다음 polling 시 변경 감지 → ConfigMap/Secret 정리

### 4.6 FR-6: 설정/환경변수 변경 감지 API `[FR-6]`

#### 설정 변경 감지

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config/watch?version={current_version}
```

- 현재 클라이언트가 가진 `version`(git commit hash)과 서버의 최신 버전이 다르면 즉시 응답
- 같으면 변경이 생길 때까지 hold (최대 30초 후 `304 Not Modified`)
- 클라이언트는 응답 받은 후 다시 요청 (long polling loop)

#### 환경변수 변경 감지

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars/watch?version={current_version}
```

설정 변경 감지 API와 동일한 동작. Config Agent는 config/watch와 env_vars/watch를 **병렬로** 호출하여 각각의 변경을 독립적으로 감지한다.

- `version`이 서버 최신과 다르면 즉시 응답
- 같으면 변경 시까지 hold (최대 30초 후 `304 Not Modified`)

### 4.7 FR-7: 시크릿 관리 `[FR-7]`

SealedSecret + Volume Mount 기반의 시크릿 관리. 시크릿 평문은 Git에 저장하지 않으며, K8s Secret(etcd)에만 존재한다.

#### 전체 구조

```
┌─ aap-helm-charts (configs/) ───┐
│  secrets.yaml (메타데이터만)     │
│  - id: "api-key-abc"           │
│  - k8s_secret: name, key       │
│  - description                 │
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
│      azure-gpt4    ← 파일      │
│      anthropic     ← 파일      │
└────────────────────────────────┘
```

#### Volume Mount 동작 원리

K8s Secret을 Pod에 volume으로 마운트하면, Secret의 각 `data` key가 `/secrets/{namespace}/{name}/{key}` 경로에 **개별 파일**로 마운트된다. kubelet이 base64를 자동 디코딩하므로, Config Server는 파일을 읽으면 바로 평문 시크릿 값을 얻는다.

#### 시크릿 자동 갱신

K8s Secret 변경 시 kubelet이 마운트된 파일을 자동 갱신한다 (기본 `--sync-frequency=60s`, symlink swap 방식). Config Server는 `fsnotify`로 파일 변경을 감지하여 메모리 캐시를 갱신한다. Pod 재시작 없이 동작하며, 갱신 지연은 최대 ~60초이다.

#### 시크릿 생성/변경

Console이 `POST /api/v1/admin/changes`의 `secrets` 필드로 시크릿 평문을 전송하면, Config Server가 kubeseal 암호화 → SealedSecret YAML Git commit & push → K8s API apply를 처리한다. 이후 SealedSecret Controller 복호화 → K8s Secret 생성 → kubelet Volume Mount sync (~60초)로 Config Server Pod에 반영된다.

> `subPath` 마운트 시 자동 갱신이 되지 않으므로, 반드시 디렉토리 단위로 마운트해야 한다.

> 상세 설계: [ADR-004: 시크릿 저장 — SealedSecret + Volume Mount](./adr/004-secret-storage-sealedsecret-volume-mount.md)

#### 시크릿 값 Resolve 흐름

`resolve_secrets`는 **환경변수 API에서만 사용**한다. Config API는 `os.environ/` 참조를 유지하여 반환한다.

`resolve_secrets=true` 요청 시 Config Server는: API Key 인증/인가 검증 → `env_vars.yaml`의 `secret_refs` 탐지 → `secrets.yaml`에서 K8s Secret 경로 확인 → Volume Mount 파일에서 시크릿 값 읽기(`/secrets/{namespace}/{name}/{key}`) → 평문 환경변수 + 시크릿 환경변수를 합산하여 응답 (`Cache-Control: no-store`).

#### Config Server RBAC (시크릿 관리용)

Config Server에 필요한 최소 권한:
- `sealedsecrets` (bitnami.com): get, list, create, update, patch
- `secrets`: get (Volume Mount 구성용)
- `services/proxy` (sealed-secrets-controller): get (kubeseal 공개키 조회)

### 4.8 FR-8: App Registry 연동 `[FR-8]`

Config Server는 자체적으로 클라이언트 등록을 하지 않는다. **AAP Console**이 org/project/service의 계층 구조의 단일 소스(Single Source of Truth)이다.

#### API Key 기반 인증

Console → Config Server 통신은 **환경변수 기반 API Key**로 인증한다. 운영자가 양쪽에 동일한 API Key를 환경변수로 설정하고, Console은 `Authorization: Bearer <API_KEY>` 헤더로 전송한다. 상세 내용은 **FR-16** 참조.

#### AAP Console 연동 방식

- **시작 시**: Console API(`GET /api/v1/apps?all=true`)로 전체 App Registry 로드 → 메모리 캐시 (실패 시 exponential backoff retry 최대 5회, 최종 실패 시 빈 캐시로 기동)
- **런타임**: Console이 `POST /api/v1/admin/app-registry/webhook`으로 변경 통지 → 캐시 갱신
- **복구**: webhook 유실 시 Console async retry 재시도, Config Server 재시작 시 전체 로드로 복구

### 4.9 FR-9: Config Agent `[FR-9]`

litellm 등 대부분의 서비스는 로컬 파일(`--config /path/to/config.yaml`)로만 설정을 읽으므로, Config Server REST API와 클라이언트 서비스 사이를 연결하는 **Config Agent**가 필요하다. 사이드카 대신 **중앙 집중형**(Deployment당 1개)으로 배포하여 thundering herd와 N배 polling 증폭을 방지한다.

> 상세 설계: [ADR-002: 중앙 집중형 Config Agent vs Sidecar](./adr/002-central-config-agent-vs-sidecar.md)

#### Config Agent 역할

Config Agent는 대상 Deployment별로 **독립된 Deployment(replica=2)**로 배포되어 다음을 수행한다:

1. **Leader Election**: replica=2 중 하나만 active로 동작한다. K8s Lease 기반 leader election으로 이중 적용을 방지하며, leader 장애 시 나머지 replica가 자동으로 인계한다.
2. **Config Server polling**: 설정 변경을 감지 (long polling / watch)
3. **ConfigMap 업데이트**: 변경된 설정을 K8s ConfigMap에 반영 (K8s API 호출)
4. **Rolling restart 트리거**: 변경 감지 시 항상 Deployment annotation 패치로 rolling restart 수행 (maxUnavailable: 25%, maxSurge: 25%)

#### 데이터 흐름

```
Config Agent → Config Server API fetch
  → K8s ConfigMap (config.yaml, os.environ/ 참조 유지)
  → K8s Secret (env.sh, export KEY=VALUE 형식)
  → Deployment annotation 패치 → rolling restart
    → litellm Pod: source /env/env.sh && litellm --config /config/config.yaml
```

#### Config Agent RBAC

Config Agent에 필요한 최소 권한 (대상 리소스에만 `resourceNames`로 제한):
- `leases` (coordination.k8s.io): get, create, update (leader election용)
- `configmaps`: get, create, update, patch (대상 ConfigMap만)
- `secrets`: get, create, update, patch (환경변수 Secret만)
- `deployments`: get, patch (대상 Deployment만, annotation 패치용)

#### 설정 파일 생성

Config Agent는 Config Server 응답을 클라이언트 서비스의 네이티브 설정 형식으로 변환하여 K8s ConfigMap에 기록한다.

**litellm용 config.yaml 생성:**

Config Server 응답의 `config` 블록을 litellm `proxy_config` 형식으로 ConfigMap에 기록한다. **시크릿 값은 `os.environ/` 참조로 유지**하여 ConfigMap에 평문이 들어가지 않는다:

```yaml
# ConfigMap litellm-config의 data.config.yaml (발췌)
model_list:
  - model_name: "azure-gpt4"
    litellm_params:
      model: "azure/gpt-4"
      api_key: os.environ/AZURE_API_KEY     # 환경변수 참조 (평문 아님)
general_settings:
  master_key: os.environ/LITELLM_MASTER_KEY  # 환경변수 참조 (평문 아님)
```

#### 환경변수 주입

Config Agent는 `env_vars.yaml`의 내용을 `resolve_secrets=true`로 fetch하여 **K8s Secret**에 환경변수를 기록한다. 시크릿이 포함되므로 ConfigMap이 아닌 Secret 리소스를 사용한다.

**env.sh 생성:**

Config Agent가 `resolve_secrets=true`로 fetch한 환경변수를 K8s **Secret**(`litellm-env-secret`)에 `export KEY=VALUE` 형식으로 기록한다. 시크릿 포함이므로 ConfigMap이 아닌 Secret을 사용한다.

litellm Pod의 entrypoint: `source /env/env.sh && litellm --config /config/config.yaml`

#### 설정 변경 유형별 반영 전략

변경 감지 시 Config Agent는 항상 ConfigMap/Secret 업데이트 후 rolling restart를 수행한다:

| 설정 유형 | 업데이트 대상 | 동작 |
|-----------|-------------|------|
| config.yaml (proxy_config) | **ConfigMap** | ConfigMap 업데이트 → rolling restart |
| env_vars (plain/secret) | **Secret** | Secret 업데이트 → rolling restart |

**Rolling Restart 메커니즘:**

Config Agent가 ConfigMap/Secret 업데이트 후 Deployment의 Pod template annotation(`config-agent/config-hash`, `config-agent/restart-at`)을 패치하여 rolling restart를 트리거한다. K8s rolling update 전략(maxUnavailable/maxSurge: 25%)에 따라 점진적으로 반영된다.

#### 연속 변경과 재시작 폭풍 방지 (Debounce)

**Leading-edge Debounce**: 첫 변경은 즉시 적용, 이후 연속 변경만 배칭한다. 파라미터: `--debounce-cooldown=10s`, `--debounce-quiet-period=10s`, `--debounce-max-wait=2m`.

| 시나리오 | 동작 | litellm 반영 지연 |
|----------|------|-------------------|
| **단일 변경** | 즉시 적용 (leading edge) | polling 주기(~30초)만큼만 |
| **10초 이내 연속 변경** | 첫 변경 즉시, 나머지 배칭 후 통합 적용 | 첫 변경: ~30초, 이후: ~40초 |
| **2분 이상 연속 변경** | 첫 변경 즉시, maxWait(2분)마다 중간 적용 | 최악: 2분 |

> Console에서 "저장" 시 API는 ~1-2초 안에 응답한다(Git commit 완료). litellm Pod 반영은 비동기이다.

> 상세 설계: [ADR-001: Config Agent Leading-edge Debounce](./adr/001-config-agent-leading-edge-debounce.md)

#### Config Agent API

Config Agent는 Config Server에 다음 API를 호출한다:

```
# 1. 설정 조회 (시작 시 + 변경 감지 후)
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config
# → config.yaml용 (os.environ/ 참조 유지, 시크릿 미포함)

# 2. 환경변수 조회 (시크릿 resolve 포함)
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars?resolve_secrets=true
# → env.sh용 (시크릿 평문 포함 → K8s Secret에 저장)

# 3. 변경 감지 (long polling loop)
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config/watch?version={ver}
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars/watch?version={ver}
```

Config Agent는 **Deployment당 1개**이므로, 동일 서비스의 replica 수와 무관하게 Config Server에 대한 polling 요청은 항상 1개뿐이다.

#### Config Agent 배포 구성

대상 Deployment별로 Config Agent Deployment(replica=2)를 배포한다. 주요 설정 args: `--config-server`, `--org`, `--project`, `--service`, `--target-configmap`, `--target-secret`, `--target-deployment`, `--poll-interval=30s`, `--debounce-*`

### 4.10 FR-10: 설정 상속 `[FR-10]`

설정 중복을 줄이기 위한 계층적 상속 구조. 같은 조직/프로젝트 내 여러 서비스가 공통 설정(라우터 전략, 타임아웃, guardrail 등)을 공유할 때 각 서비스마다 반복 정의하지 않도록 한다.

#### 상속 계층

```
_defaults/common.yaml          (전역 기본값)
  ↓ override
orgs/{org}/_defaults/common.yaml   (조직 기본값)
  ↓ override
orgs/{org}/projects/{proj}/_defaults/common.yaml  (프로젝트 기본값)
  ↓ override
orgs/{org}/projects/{proj}/services/{svc}/config.yaml  (서비스 설정)
```

#### 상속 대상

| 파일 | 상속 여부 | 설명 |
|------|----------|------|
| `config.yaml` | **O** | 공통 설정 (router_settings, litellm_settings, guardrails 등) |
| `env_vars.yaml` | **O** | 공통 환경변수 (LITELLM_LOG_LEVEL, LITELLM_TELEMETRY 등) |
| `secrets.yaml` | **X** | 시크릿은 서비스별 고유. 상위 레벨에서 상속하지 않음 |

#### Merge 전략: Deep Merge with Override

하위 레벨이 상위 레벨의 동일 키를 덮어쓴다.

| 타입 | Merge 동작 | 예시 |
|------|-----------|------|
| **스칼라 값** | 하위가 상위를 덮어씀 | 전역 `timeout: 60` → 서비스 `timeout: 120` → 결과: `120` |
| **객체 (map)** | 재귀적 deep merge | 전역 `router_settings: {num_retries: 3}` + 서비스 `router_settings: {timeout: 120}` → 결과: `{num_retries: 3, timeout: 120}` |
| **배열** | **전체 교체** (merge하지 않음) | 전역 `model_list: [A, B]` → 서비스 `model_list: [C]` → 결과: `[C]` |
| **null 값** | 상위 키 삭제 | 전역 `set_verbose: true` → 서비스 `set_verbose: null` → 결과: 키 자체 제거 |

> **배열을 전체 교체하는 이유**: `model_list` 같은 배열은 요소 간 identity(어떤 모델이 "같은 모델"인지) 판단이 모호하여 element-wise merge가 예측 불가능하다. 전체 교체가 가장 명확하다.

#### `_defaults/common.yaml` 형식

`config.yaml`과 동일한 구조이되, `metadata` 블록 없이 `config`/`env_vars` 블록만 포함한다:

```yaml
# _defaults/common.yaml (전역 기본값 예시)
config:
  router_settings:
    routing_strategy: "least-busy"
    num_retries: 3
    timeout: 60
  litellm_settings:
    drop_params: true
    set_verbose: false
    max_budget: 1000.0
    budget_duration: "30d"

env_vars:
  plain:
    LITELLM_TELEMETRY: "false"
    LITELLM_LOG_LEVEL: "INFO"
```

#### API 동작

| 파라미터 | 동작 |
|----------|------|
| `inherit=true` (기본값) | 전역 → 조직 → 프로젝트 → 서비스 순으로 merge한 최종 설정 반환 |
| `inherit=false` | 해당 레벨의 설정만 반환 (상속 없음). Console이 서비스 고유 설정만 편집할 때 사용 |

#### Admin API와의 관계

- `POST /api/v1/admin/changes`: `service` 레벨 설정만 쓴다. `_defaults` 디렉토리는 직접 수정하지 않는다.
- `_defaults` 변경은 Git 직접 수정 (PR merge) 또는 별도 Admin API(Phase 5+)로 처리한다.
- Console은 항상 `inherit=true` 응답을 사용자에게 보여주되, 저장 시에는 서비스 레벨 설정만 전송한다.

### 4.11 FR-11: 서비스 탐색 API `[FR-11]`

Console이 설정 가능한 서비스 목록을 탐색하기 위한 API. `aap-helm-charts` 저장소의 `configs/` 디렉토리 구조를 기반으로 응답한다.

#### 조직 목록

```
GET /api/v1/orgs
```

**Response**:

```json
{
  "orgs": ["myorg", "partner-org"]
}
```

#### 프로젝트 목록

```
GET /api/v1/orgs/{org}/projects
```

**Response**:

```json
{
  "org": "myorg",
  "projects": ["ai-platform", "data-pipeline"]
}
```

#### 서비스 목록

```
GET /api/v1/orgs/{org}/projects/{project}/services
```

**Response**:

```json
{
  "org": "myorg",
  "project": "ai-platform",
  "services": [
    {
      "name": "litellm",
      "has_config": true,
      "has_env_vars": true,
      "has_secrets": true,
      "updated_at": "2026-03-09T10:00:00Z"
    }
  ]
}
```

### 4.12 FR-12: 시크릿 메타데이터 API `[FR-12]`

secrets.yaml의 메타데이터를 조회하는 API. 시크릿 **평문 값은 포함되지 않으며**, K8s Secret 위치 정보(name, namespace, key)만 반환한다.

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/secrets
```

**Response** (`200 OK`):

```json
{
  "metadata": {
    "org": "myorg",
    "project": "ai-platform",
    "service": "litellm",
    "version": "a3f2b1c"
  },
  "secrets": [
    {
      "id": "litellm-master-key",
      "description": "LiteLLM master API key",
      "k8s_secret": {
        "name": "litellm-secrets",
        "namespace": "ai-platform",
        "key": "master-key"
      }
    },
    {
      "id": "azure-gpt4-api-key",
      "description": "Azure OpenAI GPT-4 API Key",
      "k8s_secret": {
        "name": "llm-provider-keys",
        "namespace": "ai-platform",
        "key": "azure-gpt4"
      }
    }
  ]
}
```

> 평문 값은 절대 포함하지 않는다. 시크릿 값 자체는 `POST /api/v1/admin/changes`의 `secrets` 필드로만 관리한다.

### 4.13 FR-13: 변경 이력 API `[FR-13]`

Git 커밋 이력을 기반으로 설정 변경 이력을 조회한다. PRD 1.2의 "설정 변경 이력의 자동 추적 (Git 기반)" 요건을 충족한다.

#### 변경 이력 조회

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/history
```

**Query Parameters**:

| 파라미터 | 타입 | 설명 |
|----------|------|------|
| `file` | string | 특정 파일만 필터 (예: `config`, `env_vars`, `secrets`) |
| `limit` | int | 반환할 커밋 수 (default: `20`, max: `100`) |
| `before` | string | 이 커밋 이전의 이력만 반환 (pagination) |

**Response**:

```json
{
  "metadata": {
    "org": "myorg",
    "project": "ai-platform",
    "service": "litellm"
  },
  "history": [
    {
      "version": "b4c5d6e",
      "message": "Add azure-gpt4 model",
      "author": "console/admin@example.com",
      "timestamp": "2026-03-10T10:00:00Z",
      "files_changed": ["config.yaml"]
    },
    {
      "version": "a3f2b1c",
      "message": "Update worker count to 4",
      "author": "console/admin@example.com",
      "timestamp": "2026-03-09T09:30:00Z",
      "files_changed": ["env_vars.yaml"]
    }
  ]
}
```

#### 특정 버전의 설정 조회

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config?version={commit_hash}
```

기존 설정 조회 API(FR-2)에 `version` 파라미터를 추가하여, 특정 시점의 설정을 조회할 수 있다. 변경 이력과 함께 사용하여 diff/rollback에 활용한다.

### 4.14 FR-14: 설정 롤백 API `[FR-14]`

Console이 특정 버전으로 서비스 설정을 롤백할 때 사용한다. Console은 이전에 저장해둔 version(Git commit hash)을 지정하여 해당 시점의 설정/환경변수/시크릿을 모두 복원한다.

```
POST /api/v1/admin/changes/revert
```

**Request Body**:

```json
{
  "org": "myorg",
  "project": "ai-platform",
  "service": "litellm",
  "target_version": "a3f2b1c",
  "message": "Rollback to stable version (before gpt-4 model addition)"
}
```

- `target_version`: 롤백 대상 Git commit hash. Console이 이력에서 선택한 버전.
- `message`: Git commit 메시지 (optional, 없으면 `"Rollback to {target_version}"` 자동 생성)

**Response** (`200 OK`):

```json
{
  "status": "rolled_back",
  "version": "d7e8f9a",
  "target_version": "a3f2b1c",
  "restored_files": ["config.yaml", "env_vars.yaml", "sealed-secrets/litellm-secrets.yaml"],
  "updated_at": "2026-03-10T12:00:00Z"
}
```

- `version`: 롤백으로 생성된 **새로운** Git commit hash (롤백도 새 커밋이다)
- `restored_files`: 복원된 파일 목록

**처리 흐름**:
1. `target_version` commit이 해당 서비스 경로의 변경을 포함하는지 검증
2. `git show {target_version}:orgs/.../config.yaml` 등으로 해당 시점의 파일 내용 추출
3. config.yaml, env_vars.yaml 파일을 해당 시점 내용으로 덮어쓰기
4. SealedSecret 파일도 해당 시점 내용으로 복원 → K8s API apply
5. 모든 변경을 **단일 Git commit** & push
6. In-memory config store 갱신
7. 다음 Config Agent polling 시 변경 감지 → ConfigMap/Secret 업데이트 → rolling restart

> **롤백은 Git revert가 아니다**: `target_version`의 파일 내용을 현재 HEAD에 새 commit으로 적용한다. Git 이력은 forward-only로 유지되어 감사 추적이 보장된다.

> **시크릿 롤백**: SealedSecret 파일도 `target_version` 시점의 내용으로 복원된다. SealedSecret Controller가 복호화하여 K8s Secret을 갱신하고, 이후 Volume Mount sync → Config Agent polling → litellm 재시작 경로를 따른다.

**에러 케이스**:

| 상황 | 응답 | 설명 |
|------|------|------|
| `target_version`이 존재하지 않음 | `404` | 유효하지 않은 commit hash |
| `target_version`에 해당 서비스 파일 없음 | `404` | 해당 시점에 서비스가 존재하지 않았음 |
| 현재 설정과 동일 | `200` (no-op) | 변경 없음, 새 commit 생성하지 않음 |

### 4.15 FR-15: 헬스체크 / 운영 API `[FR-15]`

```
GET  /healthz                                    # Liveness
GET  /readyz                                     # Readiness (git sync + App Registry 로드 완료 여부)
GET  /api/v1/status                              # 서버 상태 (마지막 sync 시각, 로드된 설정 수 등)
POST /api/v1/admin/reload                        # 수동 설정 리로드 트리거
```

### 4.16 FR-16: 인증/인가 `[FR-16]`

| 계층 | 방식 |
|------|------|
| 네트워크 수준 | 클러스터 내부 통신 (K8s Network Policy로 접근 제어) |
| API 인증 | **환경변수 기반 API Key** — `Authorization: Bearer <api-key>` 헤더로 전송 |
| 시크릿 접근 제어 | 유효한 API Key를 가진 클라이언트만 `resolve_secrets=true` 사용 가능 |

#### API Key 설정

운영자가 Config Server와 Console 양쪽에 **동일한 API Key를 환경변수로 설정**한다. 동적 발급/폐기 API 없이 환경변수만으로 관리한다.

| 항목 | 상세 |
|------|------|
| **Config Server** | 환경변수 `API_KEY`에 키 값 설정. K8s Secret으로 주입 권장 |
| **Console** | 환경변수 `CONFIG_SERVER_API_KEY`에 동일한 키 값 설정 |
| **검증** | 요청의 `Authorization: Bearer <key>`에서 키를 추출 → 환경변수 값과 constant-time 비교 |
| **키 교체** | 양쪽 환경변수 변경 → Pod 재시작 (Helm values 또는 K8s Secret 업데이트) |

#### 인증 흐름

```
Console                       Config Server
  │                                │
  │  POST /api/v1/admin/changes   │
  │  Authorization: Bearer <key>  │
  ├───────────────────────────────▶│
  │                                │
  │                                ├─ 1. Bearer 토큰 추출
  │                                ├─ 2. 환경변수 API_KEY와 비교
  │                                ├─ 3. 불일치 → 401 Unauthorized
  │                                ├─ 4. 일치 → 요청 처리
  │                                │
  │  응답: {version: "..."}        │
  │◀───────────────────────────────┤
```

> **K8s Secret 활용**: API Key는 Helm values의 secret 참조 또는 K8s Secret으로 관리하여, Git에 평문이 노출되지 않도록 한다.

### 4.17 FR-17: 시크릿 보안 `[FR-17]`

시크릿 값의 보호는 K8s 네이티브 보안 메커니즘에 위임한다.

#### 시크릿 보호 원칙

Config Server와 Config Agent는 동일 K8s 클러스터 내에서 동작하므로, 종단 간 암호화(mTLS 등)는 불필요하다.

| 원칙 | 구현 |
|------|------|
| **저장 시 분리** | Git에는 메타데이터 + SealedSecret(암호화본)만 저장. 시크릿 평문은 K8s Secret(etcd encryption at rest)에만 존재 |
| **전송 시 보호** | 클러스터 내부 통신, K8s Network Policy로 접근 제어 |
| **로그 금지** | 시크릿 값은 절대 로그에 출력하지 않음, 로그에는 secret_ref ID만 기록 |
| **캐시 금지** | 시크릿 포함 응답에 `Cache-Control: no-store` 헤더, ETag 미적용 |
| **감사 로깅** | 시크릿 접근 시 요청 시간, 요청 scope을 감사 로그에 기록 |
| **메모리 내 시크릿** | 사용 후 메모리에서 즉시 제로화 |

---

## 5. API 요약

#### Console → Config Server (Admin API)

Console은 Admin API만 호출한다. Git/kubeseal/kubectl 등 인프라 작업은 Config Server 내부 관심사이다.

| Method | Endpoint | 용도 |
|--------|----------|------|
| POST | `/api/v1/admin/changes` | 설정/시크릿 일괄 생성·변경 (단일 atomic Git commit) |
| DELETE | `/api/v1/admin/changes` | 설정/시크릿 일괄 삭제 (단일 atomic Git commit) |
| POST | `/api/v1/admin/changes/revert` | 특정 버전으로 설정/시크릿 롤백 (새 Git commit 생성) |
| POST | `/api/v1/admin/app-registry/webhook` | App 등록/수정/삭제 (인메모리 캐시 갱신) |

#### Config Agent → Config Server (읽기 API)

Config Agent는 설정 조회 + 변경 감지 API를 호출한다.

| Method | Endpoint | 용도 |
|--------|----------|------|
| GET | `/api/v1/orgs/{org}/projects/{project}/services/{service}/config` | 설정 조회 (os.environ/ 참조 유지) |
| GET | `/api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars` | 환경변수 조회 (resolve_secrets=true 시 시크릿 평문 포함) |
| GET | `/api/v1/.../config/watch?version={ver}` | 설정 변경 감지 (long polling) |
| GET | `/api/v1/.../env_vars/watch?version={ver}` | 환경변수 변경 감지 (long polling) |

#### Console → Config Server (읽기 API)

Console이 설정 조회/탐색/이력 확인을 위해 사용하는 API.

| Method | Endpoint | 용도 |
|--------|----------|------|
| GET | `/api/v1/orgs/{org}/projects/{project}/services/{service}/config` | 설정 조회 |
| GET | `/api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars` | 환경변수 조회 |
| GET | `/api/v1/orgs/{org}/projects/{project}/services/{service}/secrets` | 시크릿 메타데이터 조회 (평문 없음) |
| GET | `/api/v1/orgs` / `orgs/{org}/projects` / `.../services` | 서비스 탐색 |
| GET | `/api/v1/orgs/{org}/projects/{project}/services/{service}/history` | 변경 이력 조회 (Git 기반) |
| POST | `/api/v1/configs/batch` | 다중 서비스 설정 일괄 조회 |

#### 운영 API

| Method | Endpoint | 용도 |
|--------|----------|------|
| GET | `/healthz` | Liveness |
| GET | `/readyz` | Readiness (git sync + App Registry 로드 완료) |
| GET | `/api/v1/status` | 서버 상태 |
| POST | `/api/v1/admin/reload` | 수동 설정 리로드 |

---

## 6. 비기능 요구사항

### 6.1 의존성

#### 클러스터 사전 요구사항

| 컴포넌트 | 용도 | 필수 여부 |
|----------|------|----------|
| **Bitnami SealedSecrets Controller** | SealedSecret → K8s Secret 복호화 | 필수 |
| **K8s etcd encryption at rest** | Secret 저장 시 암호화 | 권장 |
| **K8s Network Policy 지원** (Calico/Cilium) | Pod 간 네트워크 접근 제어 | 권장 |

#### Config Server 의존성

| 의존성 | 용도 |
|--------|------|
| `aap-helm-charts` Git Repository | 설정 파일 원본 저장소 (Source of Truth). `configs/` 하위만 접근 |
| AAP Console API | App Registry 로드 (인증/인가) |
| K8s API (client-go) | SealedSecret apply |
| Volume Mount | Secret 값 읽기 (resolve_secrets) |
| SealedSecret Controller 공개키 | kubeseal 암호화 |

#### Config Agent 의존성

| 의존성 | 용도 |
|--------|------|
| Config Server API | 설정/환경변수 조회, 변경 감지 |
| K8s API | ConfigMap/Secret 업데이트, Deployment annotation 패치 |

---

## 7. 기술 스택

**Go Module**: `github.com/aap/config-server` (Go 1.26+)

| 구성 요소 | 기술 | 선택 이유 |
|-----------|------|-----------|
| 언어 | **Go 1.26+** | 고성능 HTTP 서버, 단일 바이너리, 낮은 메모리, K8s 생태계 친화 |
| HTTP 서버 | `net/http` (stdlib) | 외부 의존성 최소화, HTTP/2 기본 지원 |
| Router | stdlib `mux` (Go 1.26+ `http.NewServeMux` enhanced routing) | 외부 의존성 zero, 메서드+패턴 라우팅 지원 |
| Git 연동 | `go-git` 또는 shell exec `git` | in-process git 조작 |
| YAML 파싱 | `gopkg.in/yaml.v3` | 표준 YAML 라이브러리 |
| 파일 감시 | `fsnotify` | Volume Mount 변경 감지 |
| 로깅 | `slog` (stdlib) | 구조화 로깅, 외부 의존성 없음 |
| 메트릭 | `prometheus/client_golang` | K8s 모니터링 표준 |
| K8s 클라이언트 | `client-go` | Config Agent: ConfigMap/Secret/Deployment 조작, Config Server: SealedSecret apply + Secret 조회 |
| SealedSecret | `kubeseal` (CLI 또는 Go 라이브러리) | 시크릿 암호화 → SealedSecret YAML 생성 |
| 컨테이너 | Distroless 기반 | 최소 이미지, 불필요한 바이너리 없음 |

> **Go 설계 원칙**: 프로젝트 구조, 의존성 주입, context.Context 전파, 에러 처리 전략, graceful shutdown, 서버 자체 설정 로딩 등 Go 관용적 설계 원칙은 [HLD 섹션 11](./02_HLD.md#11-go-설계-원칙)에서 상세히 정의한다.

---

## 8. 개발 프로세스: TDD

> 상세 내용은 [development-process.md](./development-process.md) 참조

---

## 9. Phase Scope

이 섹션은 제품/기술 scope를 phase 단위로 묶어 보여준다. 현재 구현 상태,
gate 상태, evidence, next work의 canonical ledger는
[`04_IMPLEMENTATION_PLAN.md`](./04_IMPLEMENTATION_PLAN.md)이다.

### Phase 1: Core (MVP) — `[FR-1]` `[FR-2]` `[FR-3]` `[FR-4]` `[FR-5]` `[FR-15]`

- Go 프로젝트 구조, 서버 설정 로딩, 커스텀 에러 타입
- `aap-helm-charts` 저장소 clone/pull 및 `configs/` 하위 설정 파일 파싱
- In-memory config store
- 설정/환경변수 조회 API
- Admin write/delete API for config/env vars
- 주기적 Git poll 기반 설정 갱신
- Health, readiness, and status APIs (`/healthz`, `/readyz`, `/api/v1/status`)
- Graceful shutdown
- Docker image build

### Phase 2: Secrets — `[FR-7]` `[FR-8]`

- Volume Mount 기반 시크릿 로딩
- `secrets.yaml` 파싱 및 시크릿 참조 resolve
- SealedSecret 생성 및 Git commit/push
- SealedSecret K8s API apply
- `POST /api/v1/admin/changes`의 `secrets` 필드 처리
- AAP Console App Registry 연동
- `resolve_secrets` 쿼리 파라미터
- 시크릿 응답 보안 헤더 및 감사 로깅

### Phase 3: Config Agent (중앙 집중형) — `[FR-9]`

- Config Agent Deployment
- K8s Lease 기반 leader election
- Config Server API fetch 로직
- 서비스별 네이티브 설정 파일 생성
- K8s ConfigMap/Secret 업데이트
- Deployment annotation 패치 및 rolling restart
- RBAC 및 Config Agent image build

### Phase 4: Auth & Security — `[FR-16]` `[FR-17]`

- API Key Bearer 인증
- API Key 환경변수/Helm values 연동
- K8s Network Policy
- 시크릿 메모리 제로화
- 보안 헤더

### Phase 5: Console Integration & Operations — `[FR-6]` `[FR-10]` `[FR-11]` `[FR-12]` `[FR-13]` `[FR-14]`

- 서비스 탐색 API
- 시크릿 메타데이터 조회 API
- 변경 이력 및 특정 버전 조회
- 설정 롤백 API
- ETag / If-None-Match
- gzip 응답 압축
- Prometheus metrics
- Git webhook
- Config watch API
- Batch 조회 API
- 설정 상속

### Phase 6: Hardening

- 설정 파일 스키마 검증
- Rate limiting
- 통합 테스트 / 부하 테스트
- Helm chart / K8s deployment manifests (outside this repo unless `DEC-003` changes)
