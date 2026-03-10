# AAP Config Server — Product Requirements Document

> **Version**: 1.3
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
| **Secret at Rest** | 시크릿 저장은 K8s Secret(etcd encryption at rest)에 위임 |

---

## 2. 아키텍처

### 2.1 Git-backed In-memory Config Server

```
┌──────────────────────────────────────────────────────────────┐
│                     Config Server (Go)                       │
│                                                              │
│  ┌──────────┐    ┌──────────────┐    ┌───────────────┐       │
│  │ Git Sync │───▶│ In-Memory    │◀───│ K8s Secret    │       │
│  │ (poll /  │    │ Config Store │    │ Loader        │       │
│  │ webhook) │    │ (config +    │    │ (volume mount)│       │
│  └──────────┘    │  env_vars)   │    └───────────────┘       │
│                  └──────┬───────┘                            │
│                         │                                    │
│                  ┌──────▼───────┐                            │
│                  │   REST API   │                            │
│                  │   Handler    │                            │
│                  └──────┬───────┘                            │
│                         │                                    │
└─────────────────────────┼────────────────────────────────────┘
                          │
              ┌───────────▼───────────┐
              │   Config Agent        │  Deployment (replica=1)
              │   (중앙 집중형)         │  per target Deployment
              │                       │
              │  1. Config Server     │
              │     polling/watch     │
              │  2. K8s ConfigMap     │
              │     업데이트           │
              │  3. Rolling restart   │
              │     트리거 (필요 시)    │
              └───────────┬───────────┘
                          │ ConfigMap/Secret update
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
3. **설정 변경 시**: Git webhook 또는 주기적 poll → 변경 감지 → 메모리 갱신 (hot reload)
4. **시크릿 요청 시**: 메타데이터(Git) + 시크릿 값(K8s Volume Mount) 조합 → 클러스터 내부 네트워크로 응답
5. **클라이언트 설정 전파**: Config Agent(중앙 Deployment)가 Config Server를 polling → 변경 감지 시 K8s ConfigMap 업데이트 → 각 Pod에 volume mount로 전파 → 필요 시 rolling restart 트리거

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
│                       ├── config.yaml      # 서비스 설정 (proxy_config 등)
│                       ├── env_vars.yaml    # 환경변수 (plain + secret refs)
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

Config Server는 AAP Console로부터 다음 정보를 조회/캐시한다:

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
└── litellm-infra/
    ├── redis-host           ← "redis.svc..." (평문)
    └── redis-password       ← "EXAMPLE-redis-pass-replace-me" (평문)
```

> **참고**: K8s Secret의 `data`는 base64로 인코딩되어 저장되지만, volume mount 시 kubelet이 자동으로 base64 **디코딩**하여 파일에 기록한다. Config Server는 파일을 읽으면 바로 평문 시크릿 값을 얻는다.

#### 시크릿 자동 갱신

시크릿의 소유권과 역할을 명확히 구분한다:

| 주체 | 역할 |
|------|------|
| **K8s Secret (클러스터)** | 시크릿 값의 유일한 원본 (Single Source of Truth) |
| **마운트된 파일 (Pod 내부)** | kubelet이 관리하는 **읽기 전용 복사본** — Pod/Config Server가 수정하지 않음 |
| **Config Server (메모리)** | 마운트된 파일을 **읽기만** 함 — 변경 감지 시 메모리 캐시를 갱신할 뿐 |

K8s Secret 값이 변경되면 (`kubectl edit secret` 등), **kubelet이 자동으로 마운트된 파일을 갱신**한다:

1. kubelet은 주기적으로 마운트된 Secret의 변경을 확인 (기본 `--sync-frequency=60s`)
2. 변경이 감지되면 마운트된 파일을 새 값으로 교체 (symlink swap 방식)
3. **Pod 재시작 없이** 파일 내용이 바뀜
4. Config Server는 `fsnotify`로 파일 변경을 감지하여 메모리 캐시를 갱신

```
K8s Secret (원본)       마운트된 파일 (읽기 전용)    Config Server (메모리)
━━━━━━━━━━━━━━━━        ━━━━━━━━━━━━━━━━━━━━      ━━━━━━━━━━━━━━━━━━━━

kubectl edit secret
  (값 변경)
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

#### 시크릿 초기 생성

Volume Mount는 K8s Secret 객체가 클러스터에 이미 존재해야 동작한다. 따라서 Config Server 배포 전에 Secret 객체를 먼저 생성해야 한다:

```bash
# 1. Secret 객체 생성 (최초 1회)
kubectl create secret generic litellm-secrets \
  --namespace=ai-platform \
  --from-literal=master-key=EXAMPLE-master-key-replace-me \
  --from-literal=database-url='postgresql://user:EXAMPLE@host:5432/db' \
  --from-literal=ui-password=EXAMPLE-password-replace-me

kubectl create secret generic llm-provider-keys \
  --namespace=ai-platform \
  --from-literal=azure-gpt4=EXAMPLE-azure-key-replace-me \
  --from-literal=anthropic=EXAMPLE-anthropic-key-replace-me

# 2. Config Server 배포 (Secret을 volume mount)
kubectl apply -f config-server-deployment.yaml

# 3. 이후 시크릿 값 변경 시
kubectl edit secret llm-provider-keys -n ai-platform
# → kubelet이 자동으로 마운트된 파일 갱신 → Config Server가 감지하여 캐시 갱신
```

> 운영 환경에서는 `kubectl create secret` 대신 Sealed Secrets, External Secrets Operator, 또는 CI/CD 파이프라인으로 Secret 객체를 관리하는 것을 권장한다.

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

Config Agent는 대상 Deployment별로 **독립된 Deployment(replica=1)**로 배포되어 다음을 수행한다:

1. **Config Server polling**: 설정 변경을 감지 (long polling / watch)
2. **ConfigMap 업데이트**: 변경된 설정을 K8s ConfigMap에 반영 (K8s API 호출)
3. **Rolling restart 트리거**: 환경변수 등 hot reload 불가능한 변경 시, 대상 Deployment에 annotation 패치로 rolling restart 유도

### 4.3 아키텍처

```
┌─ Config Agent Deployment (replica=1) ────────────────────────┐
│                                                               │
│  1. Config Server API 호출 (resolve_secrets=true)              │
│  2. 응답에서 시크릿이 resolve된 설정 수신                       │
│  3. K8s ConfigMap 업데이트 (설정 파일 내용)                     │
│  4. K8s Secret 업데이트 (환경변수 중 시크릿 값)                 │
│  5. 변경 유형에 따라 rolling restart 트리거                     │
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

Config Agent는 변경 유형에 따라 다른 반영 전략을 사용한다:

| 설정 유형 | 반영 방식 | 동작 |
|-----------|----------|------|
| config.yaml (proxy_config) | **ConfigMap 업데이트** | kubelet이 ~60초 이내 파일 갱신 → litellm이 파일 변경 감지하여 hot reload |
| env_vars (plain/secret) | **Rolling restart** | 프로세스 환경변수는 시작 후 변경 불가 → Deployment annotation 패치로 rolling restart |

#### Rolling Restart 메커니즘

환경변수가 변경된 경우, Config Agent가 대상 Deployment의 annotation을 패치한다:

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
      maxUnavailable: 1    # 최대 1개 pod만 동시에 내림
      maxSurge: 1           # 최대 1개 pod을 추가로 생성
  template:
    metadata:
      annotations:
        config-agent/config-hash: ""     # Config Agent가 업데이트
        config-agent/restart-at: ""      # Config Agent가 업데이트
```

이 방식으로 32개 replica 중 **최대 1개만 동시에 재시작**되므로, 31/32는 항상 서비스 가능 상태를 유지한다.

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
│  K8s Secret → etcd (encryption at rest 활성화)                    │
│  → Config Server Pod에 Volume Mount (kubelet이 복호화)            │
│  → Config Server 메모리에서만 평문 존재                             │
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
GET /healthz                                    # Liveness
GET /readyz                                     # Readiness (git sync + App Registry 로드 완료 여부)
GET /api/v1/status                              # 서버 상태 (마지막 sync 시각, 로드된 설정 수 등)
POST /api/v1/admin/reload                       # 수동 설정 리로드 트리거
POST /api/v1/admin/app-registry/webhook         # AAP Console App Registry 변경 수신
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
   │                │                   │                   │         │          │
   │                │                   │                   │         ▼          │
   │                │                   │                   │   kubelet 전파     │
   │                │                   │                   │   (~60초 이내)     │
   │                │                   │                   │         │          │
   │                │                   │                   │         └─────────▶│
   │                │                   │                   │      (파일 갱신 →   │
   │                │                   │                   │       hot reload)  │
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

### 8.3 시크릿 변경

```
Admin                  K8s Cluster            Config Server       Config Agent
  │                        │                       │                   │
  ├─ kubectl edit secret ─▶│                       │                   │
  │   (또는 Sealed Secrets) │                       │                   │
  │                        ├─ volume update ───────▶│                   │
  │                        │   (kubelet sync)       ├─ fsnotify 감지   │
  │                        │                       ├─ 시크릿 값 갱신    │
  │                        │                       │◀── long poll ─────┤
  │                        │                       ├── 변경 응답 ──────▶│
  │                        │                       │                   ├─ ConfigMap/
  │                        │                       │                   │  Secret 업데이트
  │                        │                       │                   ├─ 필요 시
  │                        │                       │                   │  rolling restart
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
| **저장 시 분리** | Git에는 메타데이터만, 실제 값은 K8s Secret(etcd encryption at rest)에만 존재 |
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
| Git 저장소 유출 | 시크릿 값이 Git에 없으므로 영향 없음 (메타데이터만 노출) |

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
| K8s 클라이언트 | `client-go` | Config Agent: ConfigMap/Secret/Deployment 조작 |
| 컨테이너 | Distroless 기반 | 최소 이미지, 불필요한 바이너리 없음 |

---

## 11. 마일스톤

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
- [ ] 변경 유형 판별: config만 변경 vs 환경변수 변경
- [ ] config만 변경: ConfigMap 업데이트 → kubelet 자동 전파 (hot reload)
- [ ] 환경변수 변경: Deployment annotation 패치로 rolling restart 트리거
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
