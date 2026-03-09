# AAP Config Server — Product Requirements Document

> **Version**: 1.2
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
- 시크릿의 암호화 전송

### 1.3 핵심 원칙

| 원칙 | 설명 |
|------|------|
| **No Database** | 외부 DB 의존성 없이 동작 |
| **Git as Source of Truth** | 모든 설정의 원본은 Git 저장소 |
| **Memory-first Serving** | 요청은 메모리에서 즉시 응답 |
| **Secret Separation** | 시크릿 값은 절대 Git에 저장하지 않음 |
| **Secret Encryption** | 시크릿은 절대 평문으로 전송하지 않음 |

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
│                  ┌──────▼───────┐    ┌───────────────────┐   │
│                  │   REST API   │───▶│ Secret Encryption │   │
│                  │   Handler    │    │ Engine            │   │
│                  └──────┬───────┘    └───────────────────┘   │
│                         │                                    │
└─────────────────────────┼────────────────────────────────────┘
                          │
              ┌───────────▼───────────┐
              │   Config Agent        │  init container + sidecar
              │   (fetch, decrypt,    │  per client service Pod
              │    write files)       │
              ├───────────────────────┤
              │   litellm / 기타      │  reads local config.yaml
              │   클라이언트 서비스     │  + env.sh from shared vol
              └───────────────────────┘
```

Kubernetes API Server가 etcd를 source of truth로 쓰되 메모리 캐시로 응답하는 것과 동일한 패턴이다.

### 2.2 동작 흐름

1. **시작 시**: Git 저장소를 clone/pull → 모든 설정 파일을 파싱 → 메모리에 적재
2. **실행 중**: REST API 요청 → 메모리에서 즉시 응답 (I/O 없음)
3. **설정 변경 시**: Git webhook 또는 주기적 poll → 변경 감지 → 메모리 갱신 (hot reload)
4. **시크릿 요청 시**: 메타데이터(Git) + 시크릿 값(K8s Volume Mount) 조합 → 클라이언트 공개키로 암호화 → 응답

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
  │                         ├─ App ID + App Secret 발급 │
  │                         │                          │
  ├─ ECDH P-256 키 쌍 생성   │                          │
  ├─ 공개키를 App에 등록 ───▶│                          │
  │  (Console UI/API)       │                          │
  ├─ 비밀키 → K8s Secret ──▶ 클라이언트 Pod에 마운트     │
  │                         │                          │
  │                         │    (시작 시 전체 로드 +    │
  │                         ├─── 주기적 poll) ─────────▶│
  │                         │                          ├─ App Registry 캐시
  │                         │                          │
  │         litellm (app_id: "app-abc123")              │
  │              │                                      │
  │              ├─ GET /api/v1/.../config ─────────────▶│
  │              │  Header: X-App-ID                     │
  │              │  Header: X-App-Secret                 │
  │              │                                      ├─ App Registry 캐시에서 검증
  │              │                                      ├─ scope 확인 (org/project/service)
  │              │                                      ├─ 공개키 조회 → 시크릿 암호화
  │              │◀──────────────────────────────────────┤  응답
```

- **Admin**이 키 쌍을 생성하고, 공개키를 Console에 등록, 비밀키를 K8s Secret으로 배포
- **Config Server**는 Console의 App Registry를 읽어서 공개키를 캐시할 뿐, 키를 생성하거나 등록하지 않음

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
  "encryption": {
    "public_keys": [
      {
        "key_id": "key-2026-03",
        "status": "active",
        "public_key": "BASE64_ENCODED_ECDH_P256_PUBLIC_KEY"
      }
    ]
  },
  "created_at": "2026-03-01T00:00:00Z"
}
```

#### 왜 Config Server가 직접 관리하지 않는가

- org/project/service 계층은 AAP Console이 이미 관리하고 있으므로 이중 관리가 됨
- 앱 등록/폐기, scope 변경 같은 lifecycle은 Console의 책임
- Config Server는 **설정 저장 + 서빙**에만 집중, 권한은 Console에 위임

#### AAP Console 연동 방식: 시작 시 전체 로드 + 주기적 Poll

```
Config Server 시작
    │
    ├─ 1. Console API 호출: 전체 App Registry 로드
    │     GET /api/v1/apps?all=true
    │     → 메모리에 캐시
    │
    ├─ 2. Readiness: App Registry 로드 완료 시 readyz=true
    │
    └─ 3. 주기적 Poll (예: 30초 간격)
          GET /api/v1/apps?updated_since={last_sync_time}
          → 변경된 앱만 캐시 갱신
          → Console 장애 시 기존 캐시로 계속 서빙 (graceful degradation)
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
│   ├── master-key           ← "sk-actual-master-key" (평문)
│   ├── database-url         ← "postgresql://..." (평문)
│   └── ui-password          ← "actual-password" (평문)
├── llm-provider-keys/
│   ├── azure-gpt4           ← "sk-azure-..." (평문)
│   └── anthropic            ← "sk-ant-..." (평문)
└── litellm-infra/
    ├── redis-host           ← "redis.svc..." (평문)
    └── redis-password       ← "redis-pass" (평문)
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
  --from-literal=master-key=sk-xxx \
  --from-literal=database-url=postgresql://... \
  --from-literal=ui-password=xxx

kubectl create secret generic llm-provider-keys \
  --namespace=ai-platform \
  --from-literal=azure-gpt4=sk-xxx \
  --from-literal=anthropic=sk-xxx

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

## 4. 클라이언트 통합: Config Agent Sidecar

### 4.1 문제

litellm 등 대부분의 서비스는 임의 HTTP URL에서 설정을 로드하는 기능이 없다. litellm은 `--config /path/to/config.yaml` 또는 `CONFIG_FILE_PATH` 환경변수로 **로컬 파일만** 읽는다.
환경변수 역시 프로세스 시작 시점에 이미 설정되어 있어야 한다.

따라서 Config Server의 REST API와 클라이언트 서비스 사이를 연결하는 **Config Agent**가 필요하다.

### 4.2 Config Agent 역할

Config Agent는 클라이언트 서비스 Pod에 init container + sidecar로 배포되어 다음을 수행한다:

1. **Init 단계**: Config Server에서 설정 + 환경변수 fetch → 시크릿 복호화 → 로컬 파일로 기록
2. **Sidecar 단계**: Config Server를 long polling으로 watch → 변경 감지 시 파일 갱신 + 서비스에 reload 신호

### 4.3 아키텍처

```
┌─ litellm Pod ──────────────────────────────────────────────────┐
│                                                                │
│  ┌─ Init Container: config-agent init ──────────────────────┐  │
│  │                                                          │  │
│  │  1. Config Server API 호출 (resolve_secrets=true)        │  │
│  │  2. 응답의 $encrypted 필드를 클라이언트 비밀키로 복호화     │  │
│  │  3. /shared/config.yaml 기록 (litellm proxy_config)      │  │
│  │  4. /shared/env.sh 기록 (export KEY=VALUE 형식)           │  │
│  │  5. 완료 → init container 종료                            │  │
│  │                                                          │  │
│  └──────────────────────────────────────────────────────────┘  │
│       │ shared volume (/shared)                                │
│       ▼                                                        │
│  ┌─ Main Container: litellm ─────────────────────────────────┐ │
│  │                                                           │ │
│  │  entrypoint: source /shared/env.sh &&                     │ │
│  │              litellm --config /shared/config.yaml         │ │
│  │                                                           │ │
│  └───────────────────────────────────────────────────────────┘ │
│       ▲ file watch                                             │
│  ┌─ Sidecar Container: config-agent watch ───────────────────┐ │
│  │                                                           │ │
│  │  1. Config Server long polling (/config/watch)            │ │
│  │  2. 변경 감지 → 새 설정 fetch + 복호화                     │ │
│  │  3. /shared/config.yaml 갱신 (atomic write)               │ │
│  │  4. 환경변수 변경 시 → /shared/env_updated 플래그 기록     │ │
│  │                                                           │ │
│  └───────────────────────────────────────────────────────────┘ │
│                                                                │
│  Volumes:                                                      │
│  - /shared (emptyDir) — config.yaml, env.sh                   │
│  - /keys (K8s Secret mount) — 클라이언트 ECDH 비밀키           │
│                                                                │
└────────────────────────────────────────────────────────────────┘
```

### 4.4 설정 파일 생성

Config Agent는 Config Server 응답을 클라이언트 서비스의 네이티브 설정 형식으로 변환한다.

#### litellm용 config.yaml 생성

Config Server의 응답에서 `config` 블록을 추출하고, 시크릿을 복호화하여 litellm이 읽을 수 있는 `proxy_config` 형식으로 기록:

```yaml
# /shared/config.yaml (Config Agent가 생성)
model_list:
  - model_name: "azure-gpt4"
    litellm_params:
      model: "azure/gpt-4"
      api_base: "https://my-azure.openai.azure.com"
      api_key: "sk-actual-decrypted-azure-key"       # 복호화된 시크릿
      api_version: "2024-06-01"
    model_info:
      id: "azure-gpt4-eastus"
      description: "Azure GPT-4 East US"
  - model_name: "claude-sonnet"
    litellm_params:
      model: "anthropic/claude-sonnet-4-20250514"
      api_key: "sk-actual-decrypted-anthropic-key"   # 복호화된 시크릿

general_settings:
  master_key: "sk-actual-decrypted-master-key"       # 복호화된 시크릿
  database_url: "postgresql://..."                    # 복호화된 시크릿
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
      api_key: "actual-guardrail-key"                # 복호화된 시크릿

application:
  - application_name: "chatbot-prod"
    application_id: "app-001"
    allowed_models:
      - "azure-gpt4"
      - "claude-sonnet"
```

### 4.5 환경변수 주입

Config Agent는 `env_vars.yaml`의 내용을 fetch하여 환경변수 파일을 생성한다.

#### env.sh 생성

```bash
# /shared/env.sh (Config Agent가 생성, 시크릿 복호화 완료)
export LITELLM_LOG_LEVEL="INFO"
export LITELLM_NUM_WORKERS="4"
export LITELLM_PORT="4000"
export UI_USERNAME="admin"
export PROXY_BASE_URL="https://litellm.internal.example.com"
export STORE_MODEL_IN_DB="false"
export LITELLM_TELEMETRY="false"
export DATABASE_URL="postgresql://litellm:pass@db:5432/litellm"      # 복호화됨
export LITELLM_MASTER_KEY="sk-actual-master-key"                     # 복호화됨
export UI_PASSWORD="actual-ui-password"                              # 복호화됨
export REDIS_HOST="redis.ai-platform.svc.cluster.local"              # 복호화됨
export REDIS_PASSWORD="actual-redis-password"                        # 복호화됨
```

#### 파일 보안

- `/shared/env.sh`는 `0400` 권한으로 생성 (소유자 읽기 전용)
- shared volume은 `emptyDir`로 tmpfs(메모리) 사용 권장 → 디스크에 시크릿 미기록

```yaml
# K8s Pod spec 발췌
volumes:
  - name: shared-config
    emptyDir:
      medium: Memory    # tmpfs — 시크릿이 디스크에 절대 기록되지 않음
      sizeLimit: 10Mi
```

### 4.6 설정 Hot Reload

| 설정 유형 | Hot Reload 가능 여부 | 동작 |
|-----------|---------------------|------|
| config.yaml (proxy_config) | **가능** | Config Agent가 파일 갱신 → litellm이 파일 변경 감지하여 reload |
| env_vars (plain) | **불가** | 프로세스 환경변수는 시작 후 변경 불가 |
| env_vars (secret) | **불가** | 동일 |

환경변수가 변경된 경우, Config Agent sidecar가 `/shared/env_updated` 플래그 파일을 생성한다. 운영자는 이를 감지하여 Pod 재시작을 트리거할 수 있다 (K8s `livenessProbe`로 이 파일을 체크하는 방식 등).

```yaml
# 환경변수 변경 시 자동 재시작을 위한 liveness probe 예시
livenessProbe:
  exec:
    command:
      - /bin/sh
      - -c
      - "[ ! -f /shared/env_updated ]"    # env_updated 파일이 생기면 liveness 실패 → 재시작
  periodSeconds: 10
```

### 4.7 Config Agent API

Config Agent는 Config Server에 두 가지 API를 호출한다:

```
# 1. 설정 + 환경변수 조회 (init 시)
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config?resolve_secrets=true
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars?resolve_secrets=true

# 2. 변경 감지 (sidecar에서 long polling)
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config/watch?version={ver}
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars/watch?version={ver}
```

---

## 5. 시크릿 암호화

시크릿은 어떤 경우에도 평문으로 네트워크를 통해 전송하지 않는다. mTLS로 전송 채널을 보호하더라도, 응답 본문의 평문 시크릿은 로깅, 캐싱, 프록시 등에서 유출될 수 있다. 따라서 **응답 페이로드 레벨의 암호화**를 적용한다.

### 5.1 암호화 방식: Hybrid Encryption (ECDH + AES-256-GCM)

비대칭 키로 세션 키를 교환하고, 대칭 키로 실제 데이터를 암호화하는 하이브리드 방식을 사용한다.

```
┌─ Config Server ──────────────────────────────────────────────────┐
│                                                                  │
│  1. 임시 ECDH 키 쌍 생성 (ephemeral)                              │
│  2. 클라이언트 공개키 + 서버 임시 비밀키 → ECDH 공유 비밀 도출       │
│  3. 공유 비밀에서 AES-256-GCM 키 유도 (HKDF-SHA256)               │
│  4. 시크릿 값들을 AES-256-GCM으로 개별 암호화                       │
│  5. 서버 임시 공개키 + 암호화된 시크릿 → 응답                       │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─ Client ─────────────────────────────────────────────────────────┐
│                                                                  │
│  1. 서버 임시 공개키 + 클라이언트 비밀키 → 동일한 공유 비밀 도출     │
│  2. 동일한 HKDF로 AES-256-GCM 키 유도                             │
│  3. 각 시크릿 필드를 복호화                                        │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

**왜 ECDH + AES-256-GCM인가**:

| 속성 | 설명 |
|------|------|
| **Forward Secrecy** | 매 응답마다 임시 키 사용 → 서버 키 유출되어도 과거 응답 복호화 불가 |
| **필드 단위 암호화** | 개별 시크릿마다 별도 nonce → 시크릿 간 독립성 보장 |
| **성능** | ECDH P-256은 RSA 대비 ~10배 빠름, AES-GCM은 하드웨어 가속(AES-NI) 지원 |
| **무결성 보장** | GCM의 AEAD 특성으로 암호화 + 인증을 동시에 제공 |

### 5.2 필드 단위 암호화 (Field-level Encryption)

응답 전체를 암호화하지 않고, **시크릿 필드만 개별 암호화**한다.

이유:
- 클라이언트가 비시크릿 설정은 즉시 사용 가능 (복호화 불필요)
- 로그/캐시에 응답이 남아도 시크릿만 보호됨
- 부분적 복호화 실패가 전체 설정 사용을 차단하지 않음

### 5.3 응답 형식

`resolve_secrets=true`로 요청 시 시크릿이 포함된 응답:

```json
{
  "metadata": {
    "org": "myorg",
    "project": "ai-platform",
    "service": "litellm",
    "version": "a3f2b1c",
    "updated_at": "2026-03-09T10:00:00Z"
  },
  "encryption": {
    "scheme": "ECDH-ES+AES256GCM",
    "ephemeral_public_key": "BASE64_ENCODED_SERVER_EPHEMERAL_PUBLIC_KEY",
    "key_derivation": "HKDF-SHA256"
  },
  "config": {
    "general_settings": {
      "master_key": {
        "$encrypted": true,
        "ciphertext": "BASE64_ENCODED_CIPHERTEXT",
        "nonce": "BASE64_ENCODED_NONCE",
        "tag": "BASE64_ENCODED_AUTH_TAG"
      }
    },
    "model_list": [
      {
        "model_name": "gpt-4",
        "litellm_params": {
          "model": "azure/gpt-4",
          "api_base": "https://my-azure.openai.azure.com",
          "api_key": {
            "$encrypted": true,
            "ciphertext": "BASE64_ENCODED_CIPHERTEXT",
            "nonce": "BASE64_ENCODED_NONCE",
            "tag": "BASE64_ENCODED_AUTH_TAG"
          }
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

- 일반 설정(`model`, `api_base`, `router_settings`)은 평문
- 시크릿(`master_key`, `api_key`)만 `$encrypted` 객체로 암호화
- 각 시크릿은 고유한 `nonce`를 가짐 → 동일한 시크릿 값이라도 다른 ciphertext 생성

### 5.4 암호화 키 관리

암호화 공개키의 등록, 순환, 폐기는 모두 **AAP Console**에서 관리한다. 상세 흐름은 3.3절 참조.

### 5.5 시크릿 값 Resolve 전체 흐름

```
1. 클라이언트가 설정 요청
   GET /api/v1/.../config?resolve_secrets=true
   Header: X-App-ID: app-abc123
   Header: X-App-Secret: <app-secret>
   Header: X-Key-ID: key-2026-03

2. Config Server: 인증/인가 검증
   - App ID + App Secret 유효성 확인 (AAP Console App Registry 캐시 참조)
   - App의 scope이 요청한 org/project/service와 일치하는지 확인
   - resolve_secrets 권한 확인

3. Config Server: 설정 조립
   - config.yaml에서 *_secret_ref 필드 탐지
   - secrets.yaml에서 해당 ID의 K8s Secret 경로 확인
   - Volume Mount 경로에서 실제 시크릿 값 읽기 (/secrets/{namespace}/{name}/{key})

4. Config Server: 시크릿 암호화
   - 임시 ECDH 키 쌍 생성
   - App의 공개키(key_id로 App Registry에서 조회) + 임시 비밀키 → ECDH 공유 비밀
   - HKDF-SHA256으로 AES-256-GCM 키 유도
   - 각 시크릿 필드를 개별 nonce로 AES-256-GCM 암호화

5. Config Server: 응답 전송
   - 평문 설정 + 암호화된 시크릿 필드 + 임시 공개키
   - Cache-Control: no-store

6. 클라이언트: 복호화
   - 서버 임시 공개키 + 자신의 비밀키 → 동일 공유 비밀 도출
   - 각 $encrypted 필드를 복호화하여 평문 시크릿 획득
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
| `X-App-Secret` | Y | AAP Console에서 발급받은 App Secret |
| `X-Key-ID` | 시크릿 요청 시 | 사용할 암호화 공개키 식별자 |

**Query Parameters**:

| 파라미터 | 타입 | 설명 |
|----------|------|------|
| `resolve_secrets` | bool | `true`면 시크릿 값을 암호화하여 포함 (default: `false`) |
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

**Response** (`resolve_secrets=true`): 5.3절 참조

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

**Response** (`resolve_secrets=true`): `secret_refs`의 각 값이 `$encrypted` 객체로 치환되어 응답

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
GET /healthz                      # Liveness
GET /readyz                       # Readiness (git sync 완료 여부)
GET /api/v1/status                # 서버 상태 (마지막 sync 시각, 로드된 설정 수 등)
POST /api/v1/admin/reload         # 수동 설정 리로드 트리거
```

---

## 7. 고성능 설계

### 7.1 성능 목표

| 지표 | 목표 |
|------|------|
| Throughput | 100,000+ req/s (단일 인스턴스, 시크릿 미포함) |
| Throughput | 50,000+ req/s (단일 인스턴스, 시크릿 포함 — 암호화 오버헤드) |
| Latency (p99) | < 5ms (시크릿 미포함), < 15ms (시크릿 포함) |
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

#### (4) 암호화 성능 최적화

- **AES-NI 하드웨어 가속**: Go의 `crypto/aes`는 AES-NI 명령어 자동 활용
- **ECDH P-256 선택**: RSA-2048 대비 키 교환 ~10배 빠름
- **임시 키 배치 생성**: idle 시간에 임시 ECDH 키 쌍을 미리 생성하여 풀에 보관
- **시크릿 수 제한**: 단일 응답 내 시크릿 필드 수에 따라 선형 증가하므로, 서비스당 시크릿 수를 합리적으로 유지

---

## 8. 설정 변경 워크플로우

### 8.1 일반 설정 변경

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

### 8.2 시크릿 변경

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

## 9. 보안

### 9.1 인증/인가

| 계층 | 방식 |
|------|------|
| 서비스 간 통신 | mTLS (전송 채널 암호화) |
| API 인증 | App ID + App Secret (AAP Console에서 발급) |
| 접근 제어 | AAP Console App Registry의 scope 및 permissions로 검증 |
| 시크릿 접근 제어 | App의 `resolve_secrets` 권한 확인 |
| 시크릿 페이로드 | ECDH + AES-256-GCM 필드 단위 암호화 (5절 참조) |

### 9.2 시크릿 보호 원칙

| 원칙 | 구현 |
|------|------|
| **저장 시 분리** | Git에는 메타데이터만, 실제 값은 K8s Secret에만 존재 |
| **전송 시 암호화** | mTLS(채널) + ECDH+AES-256-GCM(페이로드) 이중 보호 |
| **로그 금지** | 시크릿 값은 절대 로그에 출력하지 않음, 로그에는 secret_ref ID만 기록 |
| **캐시 금지** | 시크릿 포함 응답에 `Cache-Control: no-store` 헤더, ETag 미적용 |
| **Forward Secrecy** | 매 응답마다 임시 키 사용 → 키 유출 시에도 과거 응답 복호화 불가 |
| **감사 로깅** | 시크릿 접근 시 App ID, 시간, 요청 scope을 감사 로그에 기록 |
| **메모리 내 시크릿** | 사용 후 메모리에서 즉시 제로화 (Go `crypto/subtle.ConstantTimeCompare` 패턴) |

### 9.3 위협 시나리오별 방어

| 위협 | 방어 |
|------|------|
| 네트워크 스니핑 | mTLS + 페이로드 암호화 → 이중 암호화 상태 |
| 프록시/LB 로깅 | 페이로드 레벨 암호화 → 중간 장비가 시크릿 평문 접근 불가 |
| 서버 메모리 덤프 | 시크릿은 요청 처리 중에만 메모리에 존재, 처리 후 제로화 |
| 클라이언트 키 유출 | Forward Secrecy로 과거 응답 보호, 키 순환으로 미래 노출 차단 |
| Git 저장소 유출 | 시크릿 값이 Git에 없으므로 영향 없음 (메타데이터만 노출) |
| Config Server 키 유출 | 서버는 장기 키를 보유하지 않음 (클라이언트 공개키만 보유) |

---

## 10. 기술 스택

| 구성 요소 | 기술 | 선택 이유 |
|-----------|------|-----------|
| 언어 | **Go** | 고성능 HTTP 서버, 단일 바이너리, 낮은 메모리, K8s 생태계 친화 |
| HTTP 서버 | `net/http` (stdlib) | 외부 의존성 최소화, HTTP/2 기본 지원 |
| Router | `go-chi/chi` 또는 stdlib `mux` (Go 1.22+) | 경량, 미들웨어 체이닝 |
| 암호화 | `crypto/ecdh`, `crypto/aes`, `crypto/cipher` (stdlib) | 표준 라이브러리, AES-NI 자동 활용 |
| 키 유도 | `golang.org/x/crypto/hkdf` | HKDF-SHA256 구현 |
| Git 연동 | `go-git` 또는 shell exec `git` | in-process git 조작 |
| YAML 파싱 | `gopkg.in/yaml.v3` | 표준 YAML 라이브러리 |
| 파일 감시 | `fsnotify` | Volume Mount 변경 감지 |
| 로깅 | `slog` (stdlib) | 구조화 로깅, 외부 의존성 없음 |
| 메트릭 | `prometheus/client_golang` | K8s 모니터링 표준 |
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

### Phase 2: Secrets & Encryption

- [ ] Volume Mount 기반 시크릿 로딩
- [ ] `secrets.yaml` 파싱 및 시크릿 참조 resolve (config + env_vars 모두)
- [ ] AAP Console App Registry 연동 (poll/webhook 기반 캐시)
- [ ] ECDH + AES-256-GCM 필드 단위 암호화 구현
- [ ] `resolve_secrets` 쿼리 파라미터 구현
- [ ] 키 순환 (deprecated key 유예 기간) 지원
- [ ] 시크릿 접근 감사 로깅

### Phase 3: Config Agent

- [ ] Config Agent CLI 구현 (init mode + watch mode)
- [ ] Config Server API fetch + 시크릿 복호화 로직
- [ ] 서비스별 네이티브 설정 파일 생성 (litellm proxy_config 형식 등)
- [ ] 환경변수 파일 생성 (`env.sh`, 0400 권한)
- [ ] Long polling 기반 설정 변경 감지 + atomic file write
- [ ] 환경변수 변경 시 `env_updated` 플래그 기록
- [ ] Config Agent Docker 이미지 빌드

### Phase 4: Auth & Security

- [ ] App ID + App Secret 인증 미들웨어
- [ ] App Registry 기반 scope 인가 검증
- [ ] 시크릿 메모리 제로화
- [ ] 보안 헤더 설정 (`Cache-Control: no-store` 등)
- [ ] shared volume tmpfs (Memory medium) 적용

### Phase 5: Performance & Operations

- [ ] ETag / If-None-Match 지원
- [ ] gzip 응답 압축
- [ ] 임시 ECDH 키 풀 (pre-generation)
- [ ] Prometheus 메트릭 export
- [ ] Git webhook 수신 엔드포인트
- [ ] Config watch (long polling) API
- [ ] Batch 조회 API
- [ ] 설정 상속 (계층적 _defaults merge)

### Phase 6: Hardening

- [ ] Graceful shutdown
- [ ] 설정 파일 스키마 검증
- [ ] Rate limiting
- [ ] 통합 테스트 / 부하 테스트 / 암호화 벤치마크
- [ ] Helm chart (Config Server + Config Agent sidecar injection)
