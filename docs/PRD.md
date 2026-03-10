# AAP Config Server — Product Requirements Document

> **Version**: 1.4
> **Date**: 2026-03-10
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
| **Secret Separation** | 시크릿 평문은 절대 Git에 저장하지 않음. SealedSecret(암호화된 형태)만 Git에 저장 |
| **Secret at Rest** | 시크릿 저장은 K8s Secret(etcd encryption at rest)에 위임. Git에는 SealedSecret으로 암호화 저장 |
| **Console Creates, Server Manages** | Console은 시크릿 생성만, Config Server가 암호화/저장/적용의 관리 주체 |

---

## 2. 아키텍처

### 2.1 Git-backed In-memory Config Server

```
                    ┌─────────────┐
                    │ AAP Console │
                    │ (시크릿 생성) │
                    └──────┬──────┘
                           │ webhook (시크릿 포함)
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
      │  │  Git Repo    │   │  mount
      │  │  (config +   │   │   │
      │  │  sealed-     │   │   │
      │  │  secrets/)   │   │   │
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

1. **시작 시**: Git 저장소를 clone/pull → 모든 설정 파일을 파싱 → 메모리에 적재
2. **실행 중**: REST API 요청 → 메모리에서 즉시 응답 (I/O 없음)
3. **설정 변경 시**: Git webhook 또는 주기적 poll → 변경 감지 → 메모리 갱신
4. **시크릿 요청 시**: 메타데이터(Git) + 시크릿 값(K8s Volume Mount) 조합 → 클러스터 내부 네트워크로 응답
5. **시크릿 생성/변경 시**: Console webhook 수신 → kubeseal 암호화 → SealedSecret YAML Git push + K8s apply → SealedSecret Controller가 K8s Secret 생성
6. **클라이언트 설정 전파**: Config Agent(중앙 Deployment)가 Config Server를 polling → 변경 감지 시 K8s ConfigMap 업데이트 → 각 Pod에 volume mount로 전파 → rolling restart 트리거 (maxUnavailable/maxSurge: 25%)

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

### 3.1 설정 저장소 구조 (Git Repository)

Git 저장소 자체가 설정 데이터베이스 역할을 한다. 별도 DB 없이 파일 시스템 기반 계층 구조로 설정을 관리한다.

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
│                       ├── config.yaml              # 서비스 설정 (proxy_config 등)
│                       ├── env_vars.yaml            # 환경변수 (plain + secret refs)
│                       ├── secrets.yaml             # 시크릿 메타데이터 (값 없음)
│                       └── sealed-secrets/          # SealedSecret YAML (암호화됨)
│                           ├── litellm-secrets.yaml
│                           ├── llm-provider-keys.yaml
│                           └── litellm-infra.yaml
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

  - id: "anthropic-api-key"
    description: "Anthropic API Key"
    k8s_secret:
      name: "llm-provider-keys"
      namespace: "ai-platform"
      key: "anthropic"

  - id: "litellm-db-url"
    description: "LiteLLM PostgreSQL connection string"
    k8s_secret:
      name: "litellm-secrets"
      namespace: "ai-platform"
      key: "database-url"

  - id: "litellm-ui-password"
    description: "LiteLLM UI admin password"
    k8s_secret:
      name: "litellm-secrets"
      namespace: "ai-platform"
      key: "ui-password"

  - id: "litellm-redis-host"
    description: "Redis host for LiteLLM caching"
    k8s_secret:
      name: "litellm-infra"
      namespace: "ai-platform"
      key: "redis-host"

  - id: "litellm-redis-password"
    description: "Redis password"
    k8s_secret:
      name: "litellm-infra"
      namespace: "ai-platform"
      key: "redis-password"

  - id: "litellm-smtp-password"
    description: "SMTP password for email notifications"
    k8s_secret:
      name: "litellm-infra"
      namespace: "ai-platform"
      key: "smtp-password"

  - id: "guardrail-api-key"
    description: "Guardrail service API key"
    k8s_secret:
      name: "guardrail-keys"
      namespace: "ai-platform"
      key: "api-key"
```

### 3.3 앱 식별 및 권한: AAP Console 연동

Config Server는 자체적으로 클라이언트 등록이나 scope 관리를 하지 않는다. **AAP Console**이 org/project/service/app의 계층 구조와 권한의 단일 소스(Single Source of Truth)이다.

#### App ID 기반 식별

각 클라이언트 서비스(litellm 등)는 AAP Console에서 발급받은 **App ID**로 자신을 식별한다.

```
Admin                    AAP Console              Config Server
  │                         │                          │
  ├─ App 등록 요청 ─────────▶│                          │
  │  (org, project, service) │                          │
  │                         ├─ App ID 발급              │
  │                         │                          │
  │                         ├─── webhook push ────────▶│
  │                         │   (App 등록/수정/삭제 시)  ├─ App Registry 캐시 갱신
  │                         │                          │
  │         litellm (app_id: "app-abc123")              │
  │              │                                      │
  │              ├─ GET /api/v1/.../config ─────────────▶│
  │              │  Header: X-App-ID                     │
  │              │                                      ├─ App Registry 캐시에서 검증
  │              │                                      ├─ scope 확인 (org/project/service)
  │              │◀──────────────────────────────────────┤  응답
```

- **Admin**이 AAP Console에서 App을 등록하고 App ID를 발급
- **AAP Console**이 App Registry 변경 시 Config Server로 webhook push
- **Config Server**는 push받은 정보로 인증/인가 캐시를 갱신할 뿐, 직접 관리하지 않음

#### AAP Console App Registry 데이터 모델

Config Server는 AAP Console로부터 다음 정보를 수신/캐시한다:

```json
{
  "app_id": "app-abc123",
  "app_name": "litellm-prod",
  "org": "myorg",
  "project": "ai-platform",
  "service": "litellm",
  "permissions": {
    "config_read": true,
    "env_vars_read": true,
    "resolve_secrets": true
  },
  "created_at": "2026-03-01T00:00:00Z"
}
```

#### 왜 Config Server가 직접 관리하지 않는가

- org/project/service 계층은 AAP Console이 이미 관리하고 있으므로 이중 관리가 됨
- 앱 등록/폐기, scope 변경 같은 lifecycle은 Console의 책임
- Config Server는 **설정 저장 + 서빙**에만 집중, 권한은 Console에 위임

#### AAP Console 연동 방식: 시작 시 전체 로드 + Webhook Push

```
Config Server 시작
    │
    ├─ 1. Console API 호출: 전체 App Registry 로드
    │     GET /api/v1/apps?all=true
    │     → 메모리에 캐시
    │
    ├─ 2. Readiness: Git sync + App Registry 로드 모두 완료 시 readyz=true
    │
    └─ 3. Console → Config Server webhook 수신 (실시간)
          POST /api/v1/admin/app-registry/webhook
          → Console이 App 등록/수정/삭제 시 push
          → Config Server가 캐시 갱신
          → Console 장애 시 기존 캐시로 계속 서빙 (graceful degradation)
          → Config Server 재시작 시 1번으로 전체 재로드
```

### 3.4 시크릿 관리: Volume Mount + Reference 패턴

#### 전체 구조

```
┌─ Git Repo ─────────────────────┐
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

K8s Secret을 Pod에 volume으로 마운트하면, Secret의 각 `data` key가 **개별 파일**로 마운트된다. Config Server Pod의 Deployment manifest에서 이를 설정한다:

```yaml
# Config Server Deployment (발췌)
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: config-server
          volumeMounts:
            # 시크릿별로 마운트 경로 지정
            - name: litellm-secrets
              mountPath: /secrets/ai-platform/litellm-secrets
              readOnly: true
            - name: llm-provider-keys
              mountPath: /secrets/ai-platform/llm-provider-keys
              readOnly: true
            - name: litellm-infra
              mountPath: /secrets/ai-platform/litellm-infra
              readOnly: true
            - name: guardrail-keys
              mountPath: /secrets/ai-platform/guardrail-keys
              readOnly: true

      volumes:
        - name: litellm-secrets
          secret:
            secretName: litellm-secrets       # K8s Secret 객체 이름
        - name: llm-provider-keys
          secret:
            secretName: llm-provider-keys
        - name: litellm-infra
          secret:
            secretName: litellm-infra
        - name: guardrail-keys
          secret:
            secretName: guardrail-keys
```

마운트 결과 Config Server Pod 내부의 파일 시스템:

```
/secrets/ai-platform/
├── litellm-secrets/
│   ├── master-key           ← "EXAMPLE-master-key-replace-me" (평문)
│   ├── database-url         ← "postgresql://user:EXAMPLE@host:5432/db" (평문)
│   └── ui-password          ← "EXAMPLE-password-replace-me" (평문)
├── llm-provider-keys/
│   ├── azure-gpt4           ← "EXAMPLE-azure-key-replace-me" (평문)
│   └── anthropic            ← "EXAMPLE-anthropic-key-replace-me" (평문)
├── litellm-infra/
│   ├── redis-host           ← "redis.svc..." (평문)
│   ├── redis-password       ← "EXAMPLE-redis-pass-replace-me" (평문)
│   └── smtp-password        ← "EXAMPLE-smtp-pass-replace-me" (평문)
└── guardrail-keys/
    └── api-key              ← "EXAMPLE-guardrail-key-replace-me" (평문)
```

> **참고**: K8s Secret의 `data`는 base64로 인코딩되어 저장되지만, volume mount 시 kubelet이 자동으로 base64 **디코딩**하여 파일에 기록한다. Config Server는 파일을 읽으면 바로 평문 시크릿 값을 얻는다.

#### 시크릿 자동 갱신

시크릿의 소유권과 역할을 명확히 구분한다:

| 주체 | 역할 |
|------|------|
| **K8s Secret (클러스터)** | 시크릿 값의 유일한 원본 (Single Source of Truth) |
| **마운트된 파일 (Pod 내부)** | kubelet이 관리하는 **읽기 전용 복사본** — Pod/Config Server가 수정하지 않음 |
| **Config Server (메모리)** | 마운트된 파일을 **읽기만** 함 — 변경 감지 시 메모리 캐시를 갱신할 뿐 |

K8s Secret 값이 변경되면 (Console → Config Server → SealedSecret → SealedSecret Controller가 K8s Secret 업데이트), **kubelet이 자동으로 마운트된 파일을 갱신**한다:

1. kubelet은 주기적으로 마운트된 Secret의 변경을 확인 (기본 `--sync-frequency=60s`)
2. 변경이 감지되면 마운트된 파일을 새 값으로 교체 (symlink swap 방식)
3. **Pod 재시작 없이** 파일 내용이 바뀜
4. Config Server는 `fsnotify`로 파일 변경을 감지하여 메모리 캐시를 갱신

```
K8s Secret (원본)       마운트된 파일 (읽기 전용)    Config Server (메모리)
━━━━━━━━━━━━━━━━        ━━━━━━━━━━━━━━━━━━━━      ━━━━━━━━━━━━━━━━━━━━

SealedSecret Controller
→ K8s Secret 업데이트
       │
       ▼
kubelet: 변경 감지 (~60초 이내)
       │
       ▼
마운트된 파일 갱신 (symlink swap, atomic)
                                               fsnotify 이벤트 수신
                                                     │
                                                     ▼
                                               메모리 캐시 갱신
                                               → 다음 API 요청부터 새 시크릿 반영
```

클러스터의 Secret과 마운트된 파일 사이의 불일치는 kubelet sync 주기(~60초) 동안만 발생하며, kubelet이 자동으로 동기화한다.

#### 시크릿 생성/변경: Console → Config Server → SealedSecret

시크릿의 생성과 변경은 Console에서 시작하여 Config Server가 처리한다:

1. **Console**이 시크릿 값을 포함한 webhook을 Config Server에 전송
2. **Config Server**가 kubeseal로 SealedSecret YAML 생성 (공개키 암호화)
3. **Config Server**가 SealedSecret YAML을 Git에 commit & push
4. **Config Server**가 SealedSecret을 K8s 클러스터에 apply
5. **SealedSecret Controller**가 복호화하여 K8s Secret 생성/업데이트
6. **kubelet**이 Volume Mount된 파일을 자동 갱신 (~60초 이내)

```
Console → Config Server → kubeseal 암호화 → Git commit → kubectl apply
                                                              │
                                              SealedSecret Controller
                                              복호화 → K8s Secret
                                                              │
                                                     kubelet Volume Mount sync
                                                              │
                                                     Config Server Pod 파일 갱신
```

> Console은 시크릿 생성/변경 요청만 하고, 이후 관리는 Config Server가 담당한다.
> 수동 `kubectl create secret`은 사용하지 않는다.

**주의사항**:
- `subPath`로 마운트한 Secret은 **자동 갱신이 되지 않는다** — 반드시 디렉토리 단위로 마운트해야 함
- 갱신 지연은 kubelet sync period에 의존 (기본 ~60초, 최대 `sync-frequency + watch cache propagation delay`)
- 설정 서버 특성상 시크릿의 1분 이내 갱신 지연은 허용 가능하다

### 3.5 설정 상속 (Config Inheritance)

설정 중복을 줄이기 위한 계층적 상속 구조:

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

## 4. 클라이언트 통합: Config Agent (중앙 집중형)

### 4.1 문제

litellm 등 대부분의 서비스는 임의 HTTP URL에서 설정을 로드하는 기능이 없다. litellm은 `--config /path/to/config.yaml` 또는 `CONFIG_FILE_PATH` 환경변수로 **로컬 파일만** 읽는다.
환경변수 역시 프로세스 시작 시점에 이미 설정되어 있어야 한다.

따라서 Config Server의 REST API와 클라이언트 서비스 사이를 연결하는 **Config Agent**가 필요하다.

#### 왜 사이드카가 아닌 중앙 집중형인가

litellm처럼 동일 Deployment의 replica가 동일 config을 공유하는 경우, 사이드카 패턴에는 두 가지 구조적 문제가 있다:

| 문제 | 사이드카 (기존) | 중앙 집중형 (현재) |
|------|---------------|------------------|
| **Thundering Herd** | Config 변경 시 N개 pod 동시 재시작 → 순간 서비스 불가 | ConfigMap 업데이트 → K8s rolling update로 점진적 반영 |
| **Redundant Polling** | N개 sidecar가 각각 polling → 요청 N배 증폭 | Agent 1개만 polling → 요청 1/N |

예: litellm이 클러스터당 32 replica인 경우, 사이드카 방식은 32배의 불필요한 polling과 동시 재시작 위험이 있다.

### 4.2 Config Agent 역할

Config Agent는 대상 Deployment별로 **독립된 Deployment(replica=2)**로 배포되어 다음을 수행한다:

1. **Config Server polling**: 설정 변경을 감지 (long polling / watch)
2. **ConfigMap 업데이트**: 변경된 설정을 K8s ConfigMap에 반영 (K8s API 호출)
3. **Rolling restart 트리거**: 변경 감지 시 항상 Deployment annotation 패치로 rolling restart 수행 (maxUnavailable: 25%, maxSurge: 25%)

### 4.3 아키텍처

```
┌─ Config Agent Deployment (replica=2) ────────────────────────┐
│                                                               │
│  1. Config Server API 호출 (resolve_secrets=true)              │
│  2. 응답에서 시크릿이 resolve된 설정 수신                       │
│  3. K8s ConfigMap 업데이트 (설정 파일 내용)                     │
│  4. K8s Secret 업데이트 (환경변수 중 시크릿 값)                 │
│  5. Deployment annotation 패치 → rolling restart (항상 수행)    │
│                                                               │
│  필요 권한 (RBAC):                                             │
│  - configmaps: get, create, update, patch                     │
│  - secrets: get, create, update, patch                        │
│  - deployments: get, patch (annotation 패치용)                 │
│                                                               │
└───────────────────┬───────────────────────────────────────────┘
                    │ K8s API
                    ▼
┌─ K8s ConfigMap: litellm-config ──────────────────────────────┐
│  data:                                                        │
│    config.yaml: |        ← litellm proxy_config 형식          │
│      model_list:                                              │
│        - model_name: "azure-gpt4"                             │
│          ...                                                  │
│    env.sh: |             ← export KEY=VALUE 형식              │
│      export LITELLM_LOG_LEVEL="INFO"                          │
│      ...                                                      │
└───────────────────┬───────────────────────────────────────────┘
                    │ volume mount (kubelet 자동 전파)
                    ▼
┌─ litellm Deployment (replicas: 32) ──────────────────────────┐
│                                                               │
│  Pod 1..32 (모두 동일 구조):                                   │
│                                                               │
│  ┌─ Init Container: config-init ───────────────────────────┐  │
│  │  /bin/sh -c "source /config/env.sh && exec env > ..."   │  │
│  │  → ConfigMap의 env.sh를 프로세스 환경변수로 주입           │  │
│  └─────────────────────────────────────────────────────────┘  │
│       │                                                       │
│       ▼                                                       │
│  ┌─ Main Container: litellm ───────────────────────────────┐  │
│  │  entrypoint: source /config/env.sh &&                   │  │
│  │              litellm --config /config/config.yaml       │  │
│  │                                                         │  │
│  │  /config/config.yaml ← ConfigMap volume mount           │  │
│  │  (kubelet이 ~60초 이내 자동 갱신)                         │  │
│  └─────────────────────────────────────────────────────────┘  │
│                                                               │
│  Volumes:                                                     │
│  - /config (ConfigMap: litellm-config)                        │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

### 4.4 Config Agent RBAC

Config Agent가 K8s API를 호출하려면 전용 ServiceAccount와 최소 권한 RBAC이 필요하다:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: config-agent
  namespace: ai-platform
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: config-agent-role
  namespace: ai-platform
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get", "create", "update", "patch"]
    resourceNames: ["litellm-config"]          # 대상 ConfigMap만 접근 허용
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["create"]                          # 최초 생성 시 resourceNames 제약 불가
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "create", "update", "patch"]
    resourceNames: ["litellm-env-secret"]      # 시크릿 포함 환경변수 Secret
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["create"]                          # 최초 생성 시 resourceNames 제약 불가
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "patch"]
    resourceNames: ["litellm"]                 # 대상 Deployment만 패치 허용
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: config-agent-binding
  namespace: ai-platform
subjects:
  - kind: ServiceAccount
    name: config-agent
roleRef:
  kind: Role
  name: config-agent-role
  apiGroup: rbac.authorization.k8s.io
```

### 4.5 설정 파일 생성

Config Agent는 Config Server 응답을 클라이언트 서비스의 네이티브 설정 형식으로 변환하여 K8s ConfigMap에 기록한다.

#### litellm용 config.yaml 생성

Config Server의 응답에서 `config` 블록을 추출하고, litellm이 읽을 수 있는 `proxy_config` 형식으로 ConfigMap의 `data.config.yaml`에 기록:

```yaml
# ConfigMap litellm-config의 data.config.yaml (Config Agent가 생성)
model_list:
  - model_name: "azure-gpt4"
    litellm_params:
      model: "azure/gpt-4"
      api_base: "https://my-azure.openai.azure.com"
      api_key: "EXAMPLE-azure-key-replace-me"          # 시크릿 (Config Server에서 resolve됨)
      api_version: "2024-06-01"
    model_info:
      id: "azure-gpt4-eastus"
      description: "Azure GPT-4 East US"
  - model_name: "claude-sonnet"
    litellm_params:
      model: "anthropic/claude-sonnet-4-20250514"
      api_key: "EXAMPLE-anthropic-key-replace-me"     # 시크릿 (Config Server에서 resolve됨)

general_settings:
  master_key: "EXAMPLE-master-key-replace-me"         # 시크릿 (Config Server에서 resolve됨)
  database_url: "postgresql://user:EXAMPLE@host:5432/db"  # 시크릿 (Config Server에서 resolve됨)
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
      api_key: "EXAMPLE-guardrail-key-replace-me"     # 시크릿 (Config Server에서 resolve됨)

application:
  - application_name: "chatbot-prod"
    application_id: "app-001"
    allowed_models:
      - "azure-gpt4"
      - "claude-sonnet"
```

### 4.6 환경변수 주입

Config Agent는 `env_vars.yaml`의 내용을 fetch하여 환경변수 파일을 ConfigMap에 기록한다.

#### env.sh 생성

```bash
# ConfigMap litellm-config의 data.env.sh (Config Agent가 생성)
export LITELLM_LOG_LEVEL="INFO"
export LITELLM_NUM_WORKERS="4"
export LITELLM_PORT="4000"
export UI_USERNAME="admin"
export PROXY_BASE_URL="https://litellm.internal.example.com"
export STORE_MODEL_IN_DB="false"
export LITELLM_TELEMETRY="false"
export DATABASE_URL="postgresql://user:EXAMPLE@host:5432/db"         # 시크릿 (Config Server에서 resolve됨)
export LITELLM_MASTER_KEY="EXAMPLE-master-key-replace-me"            # 시크릿 (Config Server에서 resolve됨)
export UI_PASSWORD="EXAMPLE-password-replace-me"                     # 시크릿 (Config Server에서 resolve됨)
export REDIS_HOST="redis.ai-platform.svc.cluster.local"              # 시크릿 (Config Server에서 resolve됨)
export REDIS_PASSWORD="EXAMPLE-redis-pass-replace-me"                # 시크릿 (Config Server에서 resolve됨)
```

#### 시크릿이 포함된 설정의 보안

ConfigMap에 시크릿 값이 resolve된 설정이 저장되므로, 접근 제어가 중요하다:

- Config Agent의 RBAC으로 해당 ConfigMap에 대한 접근을 최소 주체로 제한
- 시크릿이 포함된 설정은 ConfigMap 대신 **K8s Secret**으로 저장하는 것을 권장 (etcd encryption at rest 적용)
- `defaultMode: 0400`으로 마운트하여 읽기 전용으로 제한

```yaml
# litellm Deployment Pod spec 발췌
volumes:
  - name: config
    projected:
      sources:
        - configMap:
            name: litellm-config           # 일반 설정 (config.yaml)
        - secret:
            name: litellm-env-secret       # 시크릿 포함 환경변수 (env.sh)
      defaultMode: 0400
```

### 4.7 설정 변경 유형별 반영 전략

변경 감지 시 Config Agent는 항상 ConfigMap/Secret 업데이트 후 rolling restart를 수행한다:

| 설정 유형 | 업데이트 대상 | 동작 |
|-----------|-------------|------|
| config.yaml (proxy_config) | **ConfigMap** | ConfigMap 업데이트 → rolling restart |
| env_vars (plain/secret) | **Secret** | Secret 업데이트 → rolling restart |

#### Rolling Restart 메커니즘

Config Agent가 ConfigMap/Secret 업데이트 후 대상 Deployment의 annotation을 패치한다:

```go
// Config Agent가 K8s API로 Deployment annotation 패치
patch := fmt.Sprintf(`{
  "spec": {
    "template": {
      "metadata": {
        "annotations": {
          "config-agent/config-hash": "%s",
          "config-agent/restart-at": "%s"
        }
      }
    }
  }
}`, newConfigHash, time.Now().Format(time.RFC3339))

clientset.AppsV1().Deployments(namespace).Patch(
    ctx, deploymentName, types.StrategicMergePatchType, []byte(patch), ...)
```

K8s는 Pod template annotation이 변경되면 Deployment의 `strategy`에 따라 rolling update를 수행한다:

```yaml
# litellm Deployment의 rolling update 전략
spec:
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 25%   # K8s 기본값
      maxSurge: 25%          # K8s 기본값
  template:
    metadata:
      annotations:
        config-agent/config-hash: ""     # Config Agent가 업데이트
        config-agent/restart-at: ""      # Config Agent가 업데이트
```

이 방식으로 32개 replica 중 **최대 25%(8개)만 동시에 재시작**되므로, 나머지는 항상 서비스 가능 상태를 유지한다.

### 4.8 Config Agent API

Config Agent는 Config Server에 두 가지 API를 호출한다:

```
# 1. 설정 + 환경변수 조회 (시작 시 + 변경 감지 후)
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config?resolve_secrets=true
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars?resolve_secrets=true

# 2. 변경 감지 (long polling loop)
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config/watch?version={ver}
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars/watch?version={ver}
```

Config Agent는 **Deployment당 1개**이므로, 동일 서비스의 replica 수와 무관하게 Config Server에 대한 polling 요청은 항상 1개뿐이다.

### 4.9 Config Agent 배포 구성

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: config-agent-litellm
  namespace: ai-platform
spec:
  replicas: 1
  selector:
    matchLabels:
      app: config-agent
      target: litellm
  template:
    metadata:
      labels:
        app: config-agent
        target: litellm
    spec:
      serviceAccountName: config-agent
      containers:
        - name: config-agent
          image: aap/config-agent:latest
          args:
            - --config-server=https://config-server.config-system.svc:8443
            - --org=myorg
            - --project=ai-platform
            - --service=litellm
            - --target-configmap=litellm-config
            - --target-secret=litellm-env-secret
            - --target-deployment=litellm
            - --poll-interval=30s
          env:
            - name: APP_ID
              valueFrom:
                configMapKeyRef:
                  name: config-agent-settings
                  key: app-id
```

---

## 5. 시크릿 보안

시크릿 값의 보호는 K8s 네이티브 보안 메커니즘에 위임한다.

### 5.1 보안 전략: 클러스터 내부 통신 + K8s Secret

Config Server와 Config Agent는 동일 K8s 클러스터 내에서 동작하므로, 종단 간 암호화(mTLS 등)는 불필요하다. 클러스터 내부 네트워크(Pod-to-Pod)의 보안은 K8s Network Policy로 제어한다.

```
┌─ 저장 (at rest) ─────────────────────────────────────────────────┐
│                                                                  │
│  Git: SealedSecret YAML (암호화본) — 클러스터 비밀키 없이 복호화 불가│
│  K8s etcd: Secret (encryption at rest 활성화)                     │
│  Config Server Pod: Volume Mount (kubelet이 자동 sync)            │
│  Config Server 메모리: 평문 (요청 시만 존재, 처리 후 제로화)         │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘

┌─ 전송 (in transit) ──────────────────────────────────────────────┐
│                                                                  │
│  Config Server ←──(클러스터 내부)──→ Config Agent                  │
│  → 동일 클러스터 내 Pod-to-Pod 통신                                │
│  → K8s Network Policy로 접근 제어                                 │
│  → 시크릿 포함 응답에 Cache-Control: no-store 헤더                 │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### 5.2 응답 형식

`resolve_secrets=true`로 요청 시 시크릿이 **평문으로 resolve되어** 응답에 포함된다:

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
      "master_key": "EXAMPLE-master-key-replace-me"
    },
    "model_list": [
      {
        "model_name": "gpt-4",
        "litellm_params": {
          "model": "azure/gpt-4",
          "api_base": "https://my-azure.openai.azure.com",
          "api_key": "EXAMPLE-azure-key-replace-me"
        }
      }
    ],
    "router_settings": {
      "routing_strategy": "least-busy",
      "num_retries": 3
    }
  }
}
```

- 일반 설정(`model`, `api_base`, `router_settings`)과 시크릿(`master_key`, `api_key`) 모두 평문
- `Cache-Control: no-store` 헤더로 캐싱 방지

### 5.3 시크릿 값 Resolve 전체 흐름

```
1. 클라이언트가 설정 요청
   GET /api/v1/.../config?resolve_secrets=true
   Header: X-App-ID: app-abc123

2. Config Server: 인증/인가 검증
   - App ID 유효성 확인 (AAP Console App Registry 캐시 참조)
   - App의 scope이 요청한 org/project/service와 일치하는지 확인
   - resolve_secrets 권한 확인

3. Config Server: 설정 조립
   - config.yaml에서 *_secret_ref 필드 탐지
   - secrets.yaml에서 해당 ID의 K8s Secret 경로 확인
   - Volume Mount 경로에서 실제 시크릿 값 읽기 (/secrets/{namespace}/{name}/{key})

4. Config Server: 응답 전송
   - 시크릿이 resolve된 설정 응답
   - Cache-Control: no-store

5. 클라이언트: 수신
   - 별도 복호화 없이 즉시 사용
```

---

## 6. API 설계

### 6.1 설정 조회 API

#### 단일 서비스 설정 조회

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config
```

**Request Headers**:

| 헤더 | 필수 | 설명 |
|------|------|------|
| `X-App-ID` | Y | AAP Console에서 발급받은 App ID |

**Query Parameters**:

| 파라미터 | 타입 | 설명 |
|----------|------|------|
| `resolve_secrets` | bool | `true`면 시크릿 값을 resolve하여 평문으로 포함 (default: `false`) |
| `keys` | string | 쉼표 구분, 특정 키만 반환 (예: `model_list,router_settings`) |
| `format` | string | `yaml` 또는 `json` (default: `json`) |
| `inherit` | bool | `true`면 상위 레벨 기본값 merge (default: `true`) |

**Response** (`resolve_secrets=false`):

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

**Response** (`resolve_secrets=true`): 5.2절 참조

#### 환경변수 조회

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars
```

**Query Parameters**: 설정 조회 API와 동일 (`resolve_secrets`, `format`, `inherit`)

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

### 6.2 설정 변경 감지 API (Long Polling)

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config/watch?version={current_version}
```

- 현재 클라이언트가 가진 `version`(git commit hash)과 서버의 최신 버전이 다르면 즉시 응답
- 같으면 변경이 생길 때까지 hold (최대 30초 후 `304 Not Modified`)
- 클라이언트는 응답 받은 후 다시 요청 (long polling loop)

### 6.3 헬스체크 / 운영 API

```
GET  /healthz                                    # Liveness
GET  /readyz                                     # Readiness (git sync + App Registry 로드 완료 여부)
GET  /api/v1/status                              # 서버 상태 (마지막 sync 시각, 로드된 설정 수 등)
POST /api/v1/admin/reload                        # 수동 설정 리로드 트리거
POST /api/v1/admin/app-registry/webhook          # AAP Console App Registry 변경 수신
POST /api/v1/admin/secrets/webhook               # AAP Console 시크릿 생성/변경 수신
```

#### 시크릿 Webhook

```
POST /api/v1/admin/secrets/webhook
```

Console이 시크릿을 생성/변경할 때 호출한다. Config Server는 수신 후 kubeseal 암호화 → Git push → SealedSecret apply를 수행한다.

**Request Body**:

```json
{
  "action": "create",
  "org": "myorg",
  "project": "ai-platform",
  "service": "litellm",
  "secret": {
    "k8s_secret_name": "litellm-secrets",
    "namespace": "ai-platform",
    "data": {
      "master-key": "actual-secret-value"
    }
  }
}
```

**지원하는 action**: `create` (생성), `update` (변경), `delete` (삭제)

**Response** (`200 OK`):

```json
{
  "status": "applied",
  "sealed_secret": "litellm-secrets",
  "git_commit": "a1b2c3d"
}
```

---

## 7. 고성능 설계

### 7.1 성능 목표

| 지표 | 목표 |
|------|------|
| Throughput | 100,000+ req/s (단일 인스턴스) |
| Latency (p99) | < 5ms |
| Memory | 설정 1,000개 기준 < 100MB |
| 시작 시간 | < 5초 (cold start) |

### 7.2 성능 전략

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

갱신 중에도 읽기 요청은 거의 차단되지 않는다.

#### (3) HTTP 최적화

- **HTTP/2 지원**: 다중 요청 멀티플렉싱
- **ETag / If-None-Match**: 변경 없으면 `304` 응답 (body 전송 생략, 시크릿 미포함 응답에만 적용)
- **gzip 응답 압축**: 대용량 설정 응답 시 대역폭 절약
- **Connection pooling**: Keep-Alive로 연결 재사용

---

## 8. 설정 변경 워크플로우

### 8.1 일반 설정 변경

```
Developer        Git Repo         Config Server       Config Agent        litellm Pods
   │                │                   │                   │                   │
   ├─ PR merge ────▶│                   │                   │                   │
   │                ├─ webhook ────────▶│                   │                   │
   │                │                   ├─ git pull         │                   │
   │                │                   ├─ 메모리 갱신       │                   │
   │                │                   │                   │                   │
   │                │                   │◀── long poll ─────┤                   │
   │                │                   ├── 변경 응답 ──────▶│                   │
   │                │                   │                   ├─ ConfigMap 업데이트 │
   │                │                   │                   ├─ Deployment        │
   │                │                   │                   │  annotation 패치   │
   │                │                   │                   │         │          │
   │                │                   │                   │   Rolling restart  │
   │                │                   │                   │   maxUnavail: 25%  │
   │                │                   │                   │   maxSurge: 25%    │
   │                │                   │                   │         └─────────▶│
   │                │                   │                   │      (새 Pod 시작 →│
   │                │                   │                   │       새 설정 로드) │
```

### 8.2 환경변수 변경 (Rolling Restart)

```
Developer        Git Repo         Config Server       Config Agent        litellm Pods
   │                │                   │                   │                   │
   ├─ PR merge ────▶│                   │                   │                   │
   │                ├─ webhook ────────▶│                   │                   │
   │                │                   ├─ 메모리 갱신       │                   │
   │                │                   │◀── long poll ─────┤                   │
   │                │                   ├── 변경 응답 ──────▶│                   │
   │                │                   │                   ├─ Secret 업데이트   │
   │                │                   │                   ├─ Deployment        │
   │                │                   │                   │  annotation 패치   │
   │                │                   │                   │         │          │
   │                │                   │                   │         ▼          │
   │                │                   │                   │   K8s rolling      │
   │                │                   │                   │   update 시작      │
   │                │                   │                   │         │          │
   │                │                   │                   │   maxUnavailable:1 │
   │                │                   │                   │   → Pod 1개씩      │
   │                │                   │                   │     순차 재시작     │
```

### 8.3 시크릿 변경 (Console → Config Server → SealedSecret)

```
Console              Config Server           Git Repo        K8s Cluster         Config Agent
  │                       │                     │                 │                   │
  │  POST /webhook        │                     │                 │                   │
  │  (새 시크릿 값)        │                     │                 │                   │
  ├──────────────────────▶│                     │                 │                   │
  │                       │                     │                 │                   │
  │                       ├─ kubeseal 암호화     │                 │                   │
  │                       ├─ Git commit & push ▶│                 │                   │
  │                       ├─ kubectl apply ─────────────────────▶│                   │
  │                       │  SealedSecret       │                 │                   │
  │                       │                     │  SealedSecret   │                   │
  │                       │                     │  Controller     │                   │
  │                       │                     │  복호화 →       │                   │
  │                       │                     │  K8s Secret     │                   │
  │                       │                     │                 │                   │
  │                       │                     │  Volume Mount   │                   │
  │                       │                     │  자동 sync      │                   │
  │                       │                     │                 │                   │
  │                       │                     │                 │◀── poll (30초) ───┤
  │                       │                     │                 ├── {changed} ─────▶│
  │                       │                     │                 │                   │
  │                       │                     │                 │  ConfigMap/Secret  │
  │                       │                     │                 │  업데이트 +        │
  │                       │                     │                 │  Rolling restart   │
```

---

## 9. 보안

### 9.1 인증/인가

| 계층 | 방식 |
|------|------|
| 서비스 간 통신 | 클러스터 내부 통신 (K8s Network Policy로 접근 제어) |
| API 인증 | App ID (AAP Console에서 발급) |
| 접근 제어 | AAP Console App Registry의 scope 및 permissions로 검증 |
| 시크릿 접근 제어 | App의 `resolve_secrets` 권한 확인 |

### 9.2 시크릿 보호 원칙

| 원칙 | 구현 |
|------|------|
| **저장 시 분리** | Git에는 메타데이터 + SealedSecret(암호화본)만 저장. 시크릿 평문은 K8s Secret(etcd encryption at rest)에만 존재 |
| **전송 시 보호** | 클러스터 내부 통신, K8s Network Policy로 접근 제어 |
| **로그 금지** | 시크릿 값은 절대 로그에 출력하지 않음, 로그에는 secret_ref ID만 기록 |
| **캐시 금지** | 시크릿 포함 응답에 `Cache-Control: no-store` 헤더, ETag 미적용 |
| **감사 로깅** | 시크릿 접근 시 App ID, 시간, 요청 scope을 감사 로그에 기록 |
| **메모리 내 시크릿** | 사용 후 메모리에서 즉시 제로화 |

### 9.3 위협 시나리오별 방어

| 위협 | 방어 |
|------|------|
| 네트워크 스니핑 | 클러스터 내부 통신 + K8s Network Policy로 접근 제어 |
| 서버 메모리 덤프 | 시크릿은 요청 처리 중에만 메모리에 존재, 처리 후 제로화 |
| Git 저장소 유출 | Git에는 SealedSecret(암호화본)만 존재. 클러스터의 SealedSecret Controller 비밀키 없이 복호화 불가능하므로 영향 최소 |

---

## 10. 기술 스택

| 구성 요소 | 기술 | 선택 이유 |
|-----------|------|-----------|
| 언어 | **Go** | 고성능 HTTP 서버, 단일 바이너리, 낮은 메모리, K8s 생태계 친화 |
| HTTP 서버 | `net/http` (stdlib) | 외부 의존성 최소화, HTTP/2 기본 지원 |
| Router | `go-chi/chi` 또는 stdlib `mux` (Go 1.22+) | 경량, 미들웨어 체이닝 |
| Git 연동 | `go-git` 또는 shell exec `git` | in-process git 조작 |
| YAML 파싱 | `gopkg.in/yaml.v3` | 표준 YAML 라이브러리 |
| 파일 감시 | `fsnotify` | Volume Mount 변경 감지 |
| 로깅 | `slog` (stdlib) | 구조화 로깅, 외부 의존성 없음 |
| 메트릭 | `prometheus/client_golang` | K8s 모니터링 표준 |
| K8s 클라이언트 | `client-go` | Config Agent: ConfigMap/Secret/Deployment 조작, Config Server: SealedSecret apply + Secret 조회 |
| SealedSecret | `kubeseal` (CLI 또는 Go 라이브러리) | 시크릿 암호화 → SealedSecret YAML 생성 |
| 컨테이너 | Distroless 기반 | 최소 이미지, 불필요한 바이너리 없음 |

---

## 11. 개발 프로세스: TDD

### 11.1 TDD 원칙

모든 기능 구현은 **Red → Green → Refactor** 사이클을 따른다:

1. **Red**: 실패하는 테스트를 먼저 작성한다
2. **Green**: 테스트를 통과하는 최소한의 코드를 작성한다
3. **Refactor**: 동작을 유지하면서 코드를 개선한다

### 11.2 테스트 계층

| 계층 | 범위 | 도구 | 실행 빈도 |
|------|------|------|----------|
| **Unit Test** | 개별 함수/메서드 | `go test` | 코드 변경 시마다 |
| **Integration Test** | 컴포넌트 간 상호작용 (Git sync + Config Store + API) | `go test -tags=integration` | PR마다 |
| **E2E Test** | 전체 시스템 (Config Server + Agent + K8s) | `kind` + `go test -tags=e2e` | 릴리스 전 |

### 11.3 Phase별 TDD 전략

#### Phase 1: Core (MVP)

```
테스트 먼저                          구현
━━━━━━━━━━━━━━━━━━━━                ━━━━━━━━━━━━━━━━━━
1. config.yaml 파싱 테스트           → YAML 파서 구현
2. env_vars.yaml 파싱 테스트         → 환경변수 파서 구현
3. secrets.yaml 파싱 테스트          → 시크릿 메타데이터 파서 구현
4. ConfigStore CRUD 테스트           → In-memory store 구현 (COW)
5. Git clone/pull 테스트             → Git sync 로직 구현
6. REST API 핸들러 테스트            → HTTP 핸들러 구현
   - GET /config 응답 형식
   - 쿼리 파라미터 처리
   - 에러 응답
7. Health check 테스트               → /healthz, /readyz 구현
```

**테스트 예시 (Phase 1)**:
```go
// config_parser_test.go — Red: 이 테스트를 먼저 작성
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
```

#### Phase 2: Secrets

```
테스트 먼저                          구현
━━━━━━━━━━━━━━━━━━━━                ━━━━━━━━━━━━━━━━━━
1. Volume Mount 파일 읽기 테스트     → Secret loader 구현
2. secret_ref resolve 테스트         → resolve 로직 구현
   - config 내 *_secret_ref 치환
   - env_vars 내 secret_refs 치환
3. SealedSecret 생성 테스트          → kubeseal 연동 구현
4. SealedSecret Git push 테스트      → Git commit/push 구현
5. resolve_secrets=true 응답 테스트  → API 파라미터 처리
6. Cache-Control 헤더 테스트         → 보안 헤더 미들웨어
7. 감사 로깅 테스트                  → audit logger 구현
```

#### Phase 3: Config Agent

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
```

### 11.4 테스트 작성 규칙

| 규칙 | 설명 |
|------|------|
| **테스트 파일 위치** | 구현 파일과 동일 패키지 (`_test.go` 접미사) |
| **테이블 드리븐 테스트** | 복수 케이스는 `[]struct{ name, input, expected }` 패턴 사용 |
| **외부 의존성 격리** | interface로 추상화, 테스트에서 mock/fake 주입 |
| **K8s 테스트** | `client-go/kubernetes/fake` 사용, 실제 클러스터 불필요 |
| **Git 테스트** | 임시 디렉토리에 test fixture repo 생성 |
| **HTTP 테스트** | `httptest.NewServer`로 실제 HTTP 통신 테스트 |
| **시크릿 테스트** | 테스트 데이터에 실제 시크릿 절대 포함 금지 |

### 11.5 개발 워크플로우

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

### 11.6 커밋 컨벤션

```
test: <scope> - 실패하는 테스트 추가
feat: <scope> - 테스트 통과하는 구현
refactor: <scope> - 코드 개선 (동작 변경 없음)
fix: <scope> - 버그 수정
docs: <scope> - 문서 변경
```

예시:
```
test: config-parser - add YAML parsing test for model_list
feat: config-parser - implement ParseConfig for model_list
refactor: config-parser - extract common YAML parsing logic
```

---

## 12. 마일스톤

### Phase 1: Core (MVP)

- [ ] Go 프로젝트 구조 세팅
- [ ] Git 저장소 clone/pull 및 설정 파일 파싱 (`config.yaml`, `env_vars.yaml`, `secrets.yaml`)
- [ ] In-memory config store 구현 (COW 패턴)
- [ ] REST API: 설정 조회 (`GET /api/v1/.../config`)
- [ ] REST API: 환경변수 조회 (`GET /api/v1/.../env_vars`)
- [ ] 주기적 Git poll 기반 설정 갱신
- [ ] Health check 엔드포인트 (`/healthz`, `/readyz`)
- [ ] Dockerfile 및 기본 K8s manifests

### Phase 2: Secrets

- [ ] Volume Mount 기반 시크릿 로딩
- [ ] `secrets.yaml` 파싱 및 시크릿 참조 resolve (config + env_vars 모두)
- [ ] SealedSecret 생성 (kubeseal 암호화)
- [ ] SealedSecret YAML Git commit & push
- [ ] SealedSecret kubectl apply (K8s API)
- [ ] Console webhook 수신 → 시크릿 생성/변경 처리
- [ ] AAP Console App Registry 연동 (시작 시 전체 로드 + webhook push 수신)
- [ ] `resolve_secrets` 쿼리 파라미터 구현
- [ ] 시크릿 포함 응답에 `Cache-Control: no-store` 헤더 적용
- [ ] 시크릿 접근 감사 로깅

### Phase 3: Config Agent (중앙 집중형)

- [ ] Config Agent Deployment 구현 (long polling loop)
- [ ] Config Server API fetch 로직 (resolve_secrets=true)
- [ ] 서비스별 네이티브 설정 파일 생성 (litellm proxy_config 형식 등)
- [ ] K8s client-go 연동: ConfigMap 생성/업데이트
- [ ] K8s client-go 연동: Secret 생성/업데이트 (시크릿 포함 환경변수)
- [ ] 변경 감지 후 ConfigMap/Secret 업데이트 → Deployment annotation 패치 → rolling restart
- [ ] RBAC 설정 (ServiceAccount, Role, RoleBinding)
- [ ] Config Agent Docker 이미지 빌드

### Phase 4: Auth & Security

- [ ] App ID 인증 미들웨어
- [ ] App Registry 기반 scope 인가 검증
- [ ] K8s Network Policy 설정 (Config Server 접근 제어)
- [ ] 시크릿 메모리 제로화
- [ ] 보안 헤더 설정 (`Cache-Control: no-store` 등)

### Phase 5: Performance & Operations

- [ ] ETag / If-None-Match 지원
- [ ] gzip 응답 압축
- [ ] Prometheus 메트릭 export
- [ ] Git webhook 수신 엔드포인트
- [ ] Config watch (long polling) API
- [ ] Batch 조회 API
- [ ] 설정 상속 (계층적 _defaults merge)

### Phase 6: Hardening

- [ ] Graceful shutdown
- [ ] 설정 파일 스키마 검증
- [ ] Rate limiting
- [ ] 통합 테스트 / 부하 테스트
- [ ] Helm chart (Config Server + Config Agent Deployment)
