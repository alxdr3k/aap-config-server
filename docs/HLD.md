# AAP Config Server — High-Level Design

> **Version**: 1.0
> **Date**: 2026-03-10
> **Status**: Draft

---

## 1. 시스템 개요

AAP Config Server는 서비스 설정을 중앙에서 관리하고, 시크릿을 안전하게 저장/배포하며, 변경사항을 자동으로 클러스터에 반영하는 시스템이다.

### 1.1 컴포넌트 구성

```
┌─────────────┐
│ AAP Console │
└──────┬──────┘
       │ Admin API
       │ (설정 + 시크릿)
       ▼
┌──────────────────────────────┐
│     Config Server (Go)       │
│                              │
│  ┌──────────────┐            │
│  │ SealedSecret │            │
│  │ Manager      │            │
│  │ (kubeseal)   │            │
│  └──┬───────┬───┘            │
│     │       │                │
│  ┌──┴───────────────┐       │
│  │ In-Memory        │       │
│  │ Config Store     │       │
│  └──────┬───────────┘       │
│  ┌──────▼───────────┐       │
│  │ REST API Handler │       │
│  └──────┬───────────┘       │
└─────┼───┼───────────┼───────┘
      │   │    ▲  ▲   │
apply │   │git │  │   │ polling
      │   │push│  │   │ (30초)
      │   │    │  │   │
      │┌──▼────┴──┐   │  volume
      ││ Git Repo │   │  mount
      ││ (config +│   │   │
      ││ sealed-  │   │   │
      ││ secrets/)│   │   │
      │└──────────┘   │   │
      │               │   │
      │  ┌────────────┴─┐ │
      │  │  K8s Secret  │ │
      │  │  (etcd)      │ │
      │  └──────────────┘ │
      │        ▲          │
      │        │ 복호화    │
      ▼        │          │
┌─────────────┐│          │
│SealedSecret ││          │
│Controller   │┘          │
└─────────────┘           │
                          │
       ┌──────────────────▼──────────┐
       │   Config Agent (replica=2)  │
       │   polling → ConfigMap/Secret│
       │   업데이트 → Rolling restart │
       │   (maxUnavail/Surge: 25%)   │
       └──────────────┬──────────────┘
                      │
       ┌──────────────▼──────────────┐
       │   litellm Deployment        │
       │   (32 replicas)             │
       └─────────────────────────────┘
```

### 1.2 각 컴포넌트 역할

| 컴포넌트 | 역할 | 하지 않는 것 |
|----------|------|-------------|
| **AAP Console** | 시크릿 생성, 설정 변경 요청, App 등록/관리 | 시크릿 저장/관리 |
| **Config Server** | 시크릿 암호화(kubeseal), Git 저장, SealedSecret apply, 설정 서빙(REST API), resolve_secrets | litellm Pod 직접 제어 |
| **SealedSecret Controller** | SealedSecret → K8s Secret 복호화 (K8s CRD) | 설정 관리 |
| **Config Agent** | Config Server 폴링, 변경 감지, ConfigMap/Secret 업데이트, Rolling restart 트리거 | 시크릿 관리/암호화/복호화 |
| **kubelet** | Secret/ConfigMap Volume Mount 자동 sync | - |
| **litellm Pod** | 마운트된 config/secret 파일 읽기 | - |

---

## 2. 핵심 흐름

### Config Server API 목록

> 아래 API는 모두 **Config Server가 제공하는 엔드포인트**이다. 호출 주체별로 분류한다.

**Console → Config Server Admin API (쓰기):**

| API | 용도 |
|-----|------|
| `POST /api/v1/admin/changes` | 설정/시크릿 일괄 생성·변경 (단일 atomic Git commit) |
| `DELETE /api/v1/admin/changes` | 설정/시크릿 일괄 삭제 (단일 atomic Git commit) |
| `POST /api/v1/admin/changes/validate` | 설정 검증 (dry-run, Git commit 없음) |
| `POST /api/v1/admin/changes/revert` | 특정 버전으로 설정/시크릿 롤백 (새 Git commit 생성) |
| `POST /api/v1/admin/app-registry/webhook` | App 등록/수정/삭제 (인메모리 인증 캐시 갱신) |

**Config Agent → Config Server 읽기 API:**

| API | 용도 |
|-----|------|
| `GET /api/v1/.../config` | 설정 조회 (시크릿 미치환, `os.environ/KEY` 문자열 그대로 반환) |
| `GET /api/v1/.../env_vars` | 환경변수 조회 (resolve_secrets=true 시 시크릿 평문 포함) |
| `GET /api/v1/.../config/watch` | 설정 변경 감지 (long polling) |
| `GET /api/v1/.../env_vars/watch` | 환경변수 변경 감지 (long polling) |

**Console → Config Server 읽기 API (조회/탐색):**

| API | 용도 |
|-----|------|
| `GET /api/v1/.../config` | 설정 조회 |
| `GET /api/v1/.../secrets` | 시크릿 메타데이터 조회 (평문 없음) |
| `GET /api/v1/orgs` / `projects` / `services` | 서비스 탐색 |
| `GET /api/v1/.../history` | 변경 이력 조회 (Git 기반) |

### 2.1 설정 쓰기 흐름 (Console → Config Server → Git)

Console이 설정/시크릿을 변경하면, Config Server의 통합 Admin API (`POST /admin/changes`)를 호출한다. Console은 Git/kubeseal/kubectl을 직접 조작하지 않는다 (**Console Creates, Server Manages** 원칙).

> **`aap-console` PRD 참조**: Console → Config Server 인터페이스는 Console PRD Section 3 "시스템 아키텍처 개요"에 정의된 Admin API 계약을 따른다.

```
Console                Config Server                Git Repo            K8s Cluster
  │                         │                          │                      │
  │  POST /admin/changes    │                          │                      │
  │  (config + env_vars     │                          │                      │
  │   + secrets)            │                          │                      │
  ├────────────────────────▶│                          │                      │
  │                         │                          │                      │
  │                         │  1. 스키마 검증            │                      │
  │                         │  2. secret_ref 유효성 확인 │                      │
  │                         │  3. secrets 있으면         │                      │
  │                         │     kubeseal 암호화       │                      │
  │                         │                          │                      │
  │                         │  4. 단일 Git commit & push│                      │
  │                         ├─────────────────────────▶│                      │
  │                         │   config.yaml            │                      │
  │                         │   env_vars.yaml          │                      │
  │                         │   sealed-secrets/*.yaml  │                      │
  │                         │                          │                      │
  │                         │  5. SealedSecret 변경 시   │                      │
  │                         │     K8s API apply        │                      │
  │                         ├─────────────────────────────────────────────────▶│
  │                         │                          │                      │
  │                         │  6. In-memory 갱신        │                      │
  │                         │                          │                      │
  │  응답: {version: "..."}  │                          │                      │
  │◀────────────────────────┤                          │                      │
  │                         │                          │                      │
  │  Console DB에 version 저장│                          │                      │
```

### 2.2 시크릿 처리 흐름 (changes API 내부)

`POST /admin/changes`에 `secrets` 필드가 포함되면, Config Server는 설정 변경과 함께 시크릿 암호화 + SealedSecret apply를 **단일 atomic commit**으로 처리한다.

```
POST /admin/changes 수신 (secrets 필드 포함)
      │
      ├─ 1. kubeseal 암호화 (공개키로 SealedSecret YAML 생성)
      │
      ├─ 2. config.yaml + env_vars.yaml + SealedSecret YAML
      │     → 단일 Git commit & push
      │
      ├─ 3. K8s API apply SealedSecret
      │     → SealedSecret Controller가 복호화 → K8s Secret 생성
      │     → kubelet: Config Server Pod Volume Mount 자동 sync (~60초)
      │
      └─ 4. In-memory 갱신 → 응답: {version: "..."}
```

**SealedSecret이란**: Bitnami SealedSecrets는 K8s Secret을 공개키로 암호화하여 Git에 안전하게 저장할 수 있게 하는 CRD이다. 클러스터 내 SealedSecret Controller만이 비밀키로 복호화할 수 있다.

### 2.3 App Registry (Project/App CRUD) 흐름

Console이 org/project/service/app을 생성·수정·삭제하면, webhook으로 Config Server에 통지한다. Config Server는 인메모리 App Registry 캐시를 갱신하여 이후 API 인증/인가에 활용한다.

#### App 등록

```
Admin (Browser)        Console                    Config Server
  │                       │                             │
  ├─ App 등록 요청 ────────▶│                             │
  │  (org/project/        │                             │
  │   service 지정)        │                             │
  │                       ├─ 1. App ID 발급 + DB 저장    │
  │                       │                             │
  │◀── UI: Project 정보   ─┤                             │
  │    (App ID 포함) 표시   │                             │
  │                       │                             │
  │                       ├─ 2. POST /admin/            │
  │                       │  app-registry/webhook       │
  │                       │  (fire-and-forget,          │
  │                       │   실패 시 async retry)       │
  │                       │────────────────────────────▶│
  │                       │                             ├─ App Registry 캐시에 추가
  │                       │◀────────────────────────────┤  {status: "ok"}
```

> webhook 전송은 DB 저장 이후 **비동기**로 수행된다. Config Server가 응답하지 않아도 Console의 App CRUD는 완료된다. 실패한 webhook은 async retry queue에서 재시도하며, 최종적으로는 Config Server의 주기적 전체 동기화가 정합성을 보장한다.

#### App 수정 / 삭제

```
Console              Config Server
  │                       │
  ├─ POST /admin/         │
  │  app-registry/webhook │
  │  {action: "update"    │   권한 변경, scope 수정 등
  │   또는 "delete"}      │
  │──────────────────────▶│
  │                       ├─ 캐시 갱신 (update) 또는 캐시 제거 (delete)
  │◀──────────────────────┤  {status: "ok"}
```

#### Config Server 시작 시 전체 로드

```
Config Server                Console API
      │                          │
      ├─ GET /api/v1/apps?all ──▶│
      │                          │
      │  ┌─ 성공 ─────────────────┤
      │  │                       ├─ 전체 App Registry 반환
      │◀─┤ [{app_id, org, ...}]  │
      │  │ → 메모리에 캐시 로드   │
      │  │                       │
      │  └─ 실패 ────────────────┤
      │    → exponential backoff │
      │      retry (최대 5회)     │
      │    → 최종 실패 시 빈 캐시  │
      │      로 기동 (설정 서빙은  │
      │      정상, App 인증 불가)  │
      │                          │
      ├─ readyz=true             │
```

> Config Server는 App Registry를 **직접 관리하지 않는다**. Console이 Single Source of Truth이며, Config Server는 캐시만 유지한다. Console 장애 시 기존 캐시로 서빙을 계속한다 (graceful degradation).

**시작 순서 독립성 (데드락 방지):**

Console과 Config Server는 **부팅 시 상호 의존하지 않도록** 설계한다:
- **Console**: 독립적으로 부팅 가능. App CRUD 시 Config Server webhook은 **fire-and-forget + async retry** 방식으로 처리하여, Config Server가 다운이어도 Console DB에는 정상 저장된다. 단, Config Server가 복구될 때까지 아래 기능은 제한된다:
  - App Registry webhook 전달 실패 → Config Server 캐시 불일치 (해당 App의 API 인증 불가)
  - 설정 조회/히스토리 등 Config Server 읽기 API 의존 화면 → 에러 상태
- **Config Server**: Console API에서 App Registry를 로드한다. Console이 아직 준비되지 않은 경우 **exponential backoff retry** (최대 5회)로 재시도하고, 실패 시에도 빈 캐시 상태로 기동하여 설정 서빙은 정상 수행한다 (App 인증만 불가). Console이 복구되면 webhook 수신 또는 주기적 전체 동기화로 캐시를 채운다.

**캐시 정합성 보정**: webhook 유실은 Console의 async retry queue가 재시도한다. Config Server 재시작 시에는 시작 시 전체 로드로 캐시가 복구된다. 별도 주기적 동기화는 불필요하다.

따라서 **어떤 순서로 기동해도 부팅 데드락이 발생하지 않는다**. 양쪽 모두 상대방 없이 readyz=true까지 도달할 수 있으며, runtime 시 상대방이 복구되면 정상 기능이 점진적으로 회복된다.

### 2.4 설정 변경 감지 + 적용 흐름

Config Agent는 Config Server를 주기적으로 폴링하여 변경을 감지하고, K8s 리소스를 업데이트한다.

```
Config Agent               Config Server                K8s Cluster
  │                              │                            │
  │  GET /poll (30초 간격)        │                            │
  ├─────────────────────────────▶│                            │
  │                              │                            │
  │  응답: {changed: true}       │                            │
  │◀─────────────────────────────┤                            │
  │                              │                            │
  │  GET /config                 │  (시크릿을 치환하지 않고     │
  │                              │   os.environ/KEY 문자열     │
  │                              │   그대로 응답)              │
  ├─────────────────────────────▶│                            │
  │  응답: 설정 (시크릿 미포함)   │                            │
  │◀─────────────────────────────┤                            │
  │                              │                            │
  │  GET /env_vars               │                            │
  │  ?resolve_secrets=true       │                            │
  ├─────────────────────────────▶│  Volume Mount에서           │
  │                              │  시크릿 값 읽기              │
  │  응답: 환경변수 + 시크릿 평문  │                            │
  │◀─────────────────────────────┤                            │
  │                              │                            │
  │  ConfigMap 업데이트 (config)   │                            │
  │  Secret 업데이트 (env.sh)     │                            │
  │  Rolling restart 트리거       │                            │
  ├──────────────────────────────────────────────────────────▶│
  │                              │                 litellm Pods
  │                              │                 재시작 + 새 설정
```

### 2.5 설정 롤백 흐름

Console이 특정 버전(Git commit hash)으로 롤백을 요청하면, Config Server가 해당 시점의 모든 파일(config, env_vars, SealedSecret)을 복원한다. 롤백도 새 Git commit으로 생성되어 이력이 forward-only로 유지된다.

```
Console              Config Server              Git Repo           K8s Cluster
  │                       │                        │                    │
  │  POST /admin/         │                        │                    │
  │  changes/revert     │                        │                    │
  │  {target_version:     │                        │                    │
  │   "a3f2b1c"}          │                        │                    │
  ├──────────────────────▶│                        │                    │
  │                       │                        │                    │
  │                       │  1. git show a3f2b1c:  │                    │
  │                       │     config.yaml        │                    │
  │                       │     env_vars.yaml      │                    │
  │                       │     sealed-secrets/    │                    │
  │                       ├───────────────────────▶│                    │
  │                       │◀───────────────────────┤                    │
  │                       │                        │                    │
  │                       │  2. 파일 내용 복원       │                    │
  │                       │     + 단일 Git commit   │                    │
  │                       │     & push             │                    │
  │                       ├───────────────────────▶│                    │
  │                       │                        │                    │
  │                       │  3. SealedSecret       │                    │
  │                       │     K8s API apply      │                    │
  │                       ├────────────────────────────────────────────▶│
  │                       │                        │                    │
  │                       │  4. In-memory 갱신      │                    │
  │                       │                        │                    │
  │  {version: "d7e8f9a"} │  ← 새 commit hash      │                    │
  │◀──────────────────────┤                        │                    │
  │                       │                        │                    │
  │  Console DB에          │                        │                    │
  │  새 version 저장       │                        │                    │
```

> **Atomic Commit 원칙**: 모든 변경(설정 생성/수정/삭제/롤백)은 단일 Git commit으로 처리한다. Console은 응답의 `version` (commit hash)을 DB에 저장하여 이력 추적과 롤백에 사용한다.

### 2.6 litellm Pod 내부 구조

litellm Pod는 ConfigMap(설정)과 Secret(환경변수)을 분리하여 마운트한다. config.yaml의 시크릿은 `os.environ/` 참조를 통해 환경변수에서 주입된다.

```
litellm Pod
┌──────────────────────────────────────────────────────────────┐
│                                                              │
│  ConfigMap: litellm-config (Volume Mount /config/)             │
│  └── config.yaml  ← Config Agent가 업데이트                    │
│      (litellm proxy_config 형식)                               │
│      - model_list, router_settings, ...                       │
│      - api_key: os.environ/AZURE_API_KEY  ← 환경변수 참조     │
│      - 시크릿 평문 없음                                        │
│                                                               │
│  Secret: litellm-env-secret (Volume Mount /env/)              │
│  └── env.sh       ← Config Agent가 업데이트                    │
│      - 평문 환경변수 (LITELLM_LOG_LEVEL, ...)                   │
│      - 시크릿 환경변수 (AZURE_API_KEY, DATABASE_URL, ...)       │
│      - Config Server가 resolve한 시크릿 평문 포함               │
│                                                               │
│  Entrypoint:                                                  │
│  source /env/env.sh && litellm --config /config/config.yaml   │
│  → litellm이 os.environ/KEY 읽으면 env.sh의 값이 사용됨        │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

**Volume Mount 스펙:**

```yaml
# litellm Deployment Pod spec 발췌
volumes:
  - name: config
    configMap:
      name: litellm-config              # config.yaml (시크릿 평문 없음)
      defaultMode: 0444
  - name: env
    secret:
      secretName: litellm-env-secret    # env.sh (시크릿 포함 → Secret 리소스)
      defaultMode: 0400
containers:
  - name: litellm
    volumeMounts:
      - name: config
        mountPath: /config              # 디렉토리 마운트 → kubelet 자동 갱신 가능
        readOnly: true
      - name: env
        mountPath: /env
        readOnly: true
    command: ["/bin/sh", "-c"]
    args: ["source /env/env.sh && litellm --config /config/config.yaml"]
```

**시크릿이 litellm에 도달하는 경로:**

```
SealedSecret Controller → K8s Secret 복호화
        │
        ▼
Config Server Pod (Volume Mount로 시크릿 파일 읽기)
        │
        ▼ resolve_secrets
Config Agent (polling) → K8s Secret (env.sh) 업데이트 → Rolling restart
        │
        ▼
litellm Pod (source /env/env.sh → os.environ/ 참조 resolve)
```

- Config Server가 Volume Mount로 K8s Secret 파일을 읽어 resolve한다
- Config Agent가 resolve된 값을 K8s Secret (env.sh)에 반영한다
- litellm Pod는 재시작 시 env.sh를 source하여 환경변수로 주입받는다

---

## 3. 시크릿 관리 아키텍처

### 3.1 시크릿 라이프사이클

```
┌─────────────────────────────────────────────────────────────────┐
│  시크릿 라이프사이클                                              │
│                                                                 │
│  생성        Console에서 시크릿 값 입력                            │
│    │         POST /admin/changes (secrets 필드) → Config Server  │
│    ▼                                                            │
│  암호화      Config Server가 kubeseal로 SealedSecret 생성        │
│    │         (SealedSecret Controller의 공개키 사용)              │
│    ▼                                                            │
│  저장        Git에 SealedSecret YAML commit & push              │
│    │         (암호화된 상태 → Git 유출 시에도 안전)                 │
│    ▼                                                            │
│  적용        Config Server가 K8s API로 SealedSecret apply         │
│    │                                                            │
│    ▼                                                            │
│  복호화      SealedSecret Controller가 → K8s Secret 생성         │
│    │         (클러스터 내 비밀키로만 복호화 가능)                   │
│    ▼                                                            │
│  배포        kubelet Volume Mount sync → Config Server Pod에 반영 │
│              Config Agent polling → ConfigMap/Secret 업데이트     │
│              → Rolling restart → litellm Pod에 반영              │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 Git 저장소 구조

```
config-repo/
├── _defaults/
│   └── common.yaml
│
├── orgs/
│   └── {org-name}/
│       ├── _defaults/
│       │   └── common.yaml
│       │
│       └── projects/
│           └── {project-name}/
│               ├── _defaults/
│               │   └── common.yaml
│               │
│               └── services/
│                   └── {service-name}/
│                       ├── config.yaml              # 일반 설정 (평문)
│                       ├── env_vars.yaml            # 환경변수 (평문 + secret refs)
│                       ├── secrets.yaml             # 시크릿 메타데이터 (값 없음)
│                       └── sealed-secrets/
│                           ├── litellm-secrets.yaml       # SealedSecret (암호화됨)
│                           ├── llm-provider-keys.yaml     # SealedSecret (암호화됨)
│                           ├── litellm-infra.yaml         # SealedSecret (암호화됨)
│                           └── guardrail-keys.yaml        # SealedSecret (암호화됨)
```

### 3.3 SealedSecret YAML 예시

```yaml
# sealed-secrets/litellm-secrets.yaml
# Config Server가 kubeseal로 생성, Git에 안전하게 저장
apiVersion: bitnami.com/v1alpha1
kind: SealedSecret
metadata:
  name: litellm-secrets
  namespace: ai-platform
spec:
  encryptedData:
    master-key: AgBy3i4OJSWK+PiTySYZZA9rO...    # 암호화된 값
    database-url: AgCtr8HNDO3YPBVQ+k2t0NP...     # 암호화된 값
    ui-password: AgBMnK90dDf8akJCLe9VzR...       # 암호화된 값
  template:
    metadata:
      name: litellm-secrets
      namespace: ai-platform
```

### 3.4 Config Server RBAC (시크릿 관리용)

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: config-server-secret-manager
  namespace: ai-platform
rules:
  # SealedSecret 생성/업데이트
  - apiGroups: ["bitnami.com"]
    resources: ["sealedsecrets"]
    verbs: ["get", "list", "create", "update", "patch"]
  # Secret은 Volume Mount로 읽기 (resolve_secrets용)
  # Secret RBAC는 Volume Mount 구성을 위한 최소 권한
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
  # kubeseal을 위한 SealedSecret Controller 공개키 조회
  - apiGroups: [""]
    resources: ["services/proxy"]
    resourceNames: ["sealed-secrets-controller"]
    verbs: ["get"]
```

---

## 4. 설정 변경 시나리오별 상세 흐름

### 4.1 일반 설정 변경 (config.yaml)

#### Console을 통한 설정 변경 (주 경로)

Console은 `POST /api/v1/admin/changes`를 호출한다. Config Server가 Git commit & push를 수행한다.

```
Console            Config Server          Git Repo         Config Agent        litellm Pods
  │                      │                   │                   │                   │
  ├─ POST /admin/changes▶│                   │                   │                   │
  │                      ├─ 스키마 검증       │                   │                   │
  │                      ├─ Git commit&push ▶│                   │                   │
  │                      ├─ 메모리 갱신       │                   │                   │
  │◀── {version: "..."}──┤                   │                   │                   │
  │                      │                   │                   │                   │
  │                      │◀──── poll (30초) ─────────────────────┤                   │
  │                      ├──── {changed: true} ────────────────▶│                   │
  │                      │                   │                   │                   │
  │                      │◀──── GET /config ────────────────────┤                   │
  │                      ├──── 설정 응답 ───────────────────────▶│                   │
  │                      │                   │                   ├─ ConfigMap 업데이트 │
  │                      │                   │                   ├─ annotation 패치   │
  │                      │                   │                   │   Rolling restart  │
  │                      │                   │                   │   maxUnavail: 25%  │
  │                      │                   │                   │         └─────────▶│
  │                      │                   │                   │   새 Pod → 새 설정  │
```

#### Git 직접 변경을 통한 설정 변경 (보조 경로)

Developer가 Git에 직접 PR merge하면 webhook으로 Config Server에 통지된다.

```
Developer          Git Repo         Config Server       Config Agent        litellm Pods
   │                  │                   │                   │                   │
   ├─ PR merge ──────▶│                   │                   │                   │
   │                  ├─ webhook ────────▶│                   │                   │
   │                  │                   ├─ git pull         │                   │
   │                  │                   ├─ 메모리 갱신       │                   │
   │                  │                   │◀── poll (30초) ───┤                   │
   │                  │                   ├── {changed: true}▶│                   │
   │                  │                   │◀── GET /config ───┤                   │
   │                  │                   ├── 설정 응답 ──────▶│                   │
   │                  │                   │                   ├─ ConfigMap 업데이트 │
   │                  │                   │                   ├─ Rolling restart ─▶│
   │                  │                   │                   │   새 Pod → 새 설정  │
```

**반영 방식**: ConfigMap 업데이트 → Deployment annotation 패치 → rolling restart (maxUnavailable/maxSurge: 25%)

### 4.2 환경변수 변경 (env_vars.yaml)

Console이 `POST /api/v1/admin/changes`에 `env_vars` 필드를 포함하여 호출한다. (config, env_vars, secrets를 동시에 변경할 수 있다.)

```
Console            Config Server          Git Repo         Config Agent        litellm Pods
  │                      │                   │                   │                   │
  ├─ POST /admin/changes▶│                   │                   │                   │
  │  (env_vars 포함)      │                   │                   │                   │
  │                      ├─ Git commit&push ▶│                   │                   │
  │                      ├─ 메모리 갱신       │                   │                   │
  │◀── {version: "..."}──┤                   │                   │                   │
  │                      │                   │                   │                   │
  │                      │◀──── poll (30초) ─────────────────────┤                   │
  │                      ├──── {changed: true} ────────────────▶│                   │
  │                      │                   │                   │                   │
  │                      │◀──── GET /env_vars (resolve_secrets) ┤                   │
  │                      ├──── 환경변수 응답 ───────────────────▶│                   │
  │                      │                   │                   ├─ Secret 업데이트   │
  │                      │                   │                   ├─ annotation 패치   │
  │                      │                   │                   │   Rolling restart  │
  │                      │                   │                   │   maxUnavail: 25%  │
  │                      │                   │                   │   maxSurge: 25%    │
  │                      │                   │                   │         └─────────▶│
  │                      │                   │                   │     재시작          │
```

**반영 방식**: Secret 업데이트 + Deployment annotation 패치 → K8s rolling update (maxUnavailable/maxSurge: 25%)

### 4.3 시크릿 변경 (Console에서 시작)

```
Console              Config Server           Git Repo        K8s Cluster         Config Agent
  │                       │                     │                 │                   │
  │  POST /admin/changes  │                     │                 │                   │
  │  (secrets 필드 포함)    │                     │                 │                   │
  ├──────────────────────▶│                     │                 │                   │
  │                       │                     │                 │                   │
  │                       ├─ kubeseal 암호화     │                 │                   │
  │                       │                     │                 │                   │
  │                       ├─ Git commit & push ▶│                 │                   │
  │                       │                     │                 │                   │
  │                       ├─ K8s API apply ─────────────────────▶│                   │
  │                       │  SealedSecret       │                 │                   │
  │                       │                     │                 │                   │
  │  {version: "..."}     │                     │  SealedSecret   │                   │
  │◀──────────────────────┤                     │  Controller     │                   │
  │                       │                     │  복호화 →       │                   │
  │                       │                     │  K8s Secret     │                   │
  │                       │                     │  생성/업데이트   │                   │
  │                       │                     │                 │                   │
  │                       │                     │  Config Server  │                   │
  │                       │                     │  Pod Volume     │                   │
  │                       │                     │  Mount 자동     │                   │
  │                       │                     │  sync (~60초)   │                   │
  │                       │                     │                 │                   │
  │                       │◀────────────────────────── poll (30초) ──────────────────┤
  │                       ├── {changed: true} ──────────────────────────────────────▶│
  │                       │                     │                 │                   │
  │                       │◀──── GET /config + GET /env_vars ───────────────────────┤
  │                       ├──── 응답 ──────────────────────────────────────────────▶│
  │                       │                     │                 │                   │
  │                       │                     │                 │  ConfigMap/Secret  │
  │                       │                     │                 │  업데이트 +        │
  │                       │                     │                 │  Rolling restart   │
```

**시크릿 변경 시 litellm 반영 경로:**

SealedSecret Controller → K8s Secret 복호화 → Config Server Pod Volume Mount 자동 갱신 (~60초) → Config Agent polling → resolve_secrets → K8s Secret (env.sh) 업데이트 → Rolling restart → litellm Pod 재시작 → env.sh source → os.environ/ resolve

---

## 5. 데이터 흐름 요약

### 5.1 데이터 저장 위치

| 데이터 | 저장 위치 | 형태 | 접근 주체 |
|--------|----------|------|----------|
| 일반 설정 (config.yaml) | Git | 평문 YAML | Config Server (git sync) |
| 환경변수 (env_vars.yaml) | Git | 평문 YAML + secret refs | Config Server (git sync) |
| 시크릿 메타데이터 (secrets.yaml) | Git | 평문 YAML (값 없음) | Config Server (git sync) |
| 시크릿 암호화본 (SealedSecret) | Git + K8s | 암호화 YAML | Config Server (생성), SealedSecret Controller (복호화) |
| 시크릿 평문 (K8s Secret) | K8s etcd | base64 (etcd encryption at rest) | Config Server (Volume Mount 읽기), kubelet (Volume Mount) |
| 서비스 설정 (ConfigMap) | K8s | litellm native 형식 | Config Agent (생성/업데이트), litellm Pod (읽기) |
| 환경변수 Secret (env.sh) | K8s Secret | export KEY=VALUE (시크릿 평문 포함) | Config Agent (생성/업데이트), litellm Pod (source) |

### 5.2 네트워크 통신

```
┌─────────────────────────────────────────────────────────────────┐
│  K8s Cluster 내부 통신 (Network Policy로 접근 제어)               │
│                                                                 │
│  Console ──── Admin API ────────▶ Config Server                 │
│  (클러스터 외부 → Ingress)          │          ▲                  │
│                                    │          │ polling          │
│                                    │     Config Agent            │
│                                    │                             │
│                                    ├── K8s API ──▶ SealedSecret  │
│                                    ├── Volume Mount ▶ Secret 읽기 │
│                                    └── Git ──────▶ Config Repo    │
│                                                                 │
│  SealedSecret Controller ──▶ K8s Secret ──▶ kubelet ──▶ Pods    │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## 6. 설정 상속 (Config Inheritance) 구현

### 6.1 개요

PRD 3.5에 정의된 설정 상속을 Config Server 내부에서 구현한다. `_defaults/common.yaml` 파일들을 계층적으로 merge하여 서비스별 최종 설정을 생성한다.

### 6.2 Merge 파이프라인

```
Git 저장소 로드 시 (시작 + 갱신):

  _defaults/common.yaml              ─┐
  orgs/{org}/_defaults/common.yaml    ─┼─ 계층별 파싱 후 메모리에 적재
  orgs/.../projects/{p}/_defaults/... ─┤
  orgs/.../services/{s}/config.yaml   ─┘

API 요청 시 (inherit=true):

  전역 defaults
       │ deep merge
       ▼
  조직 defaults
       │ deep merge
       ▼
  프로젝트 defaults
       │ deep merge
       ▼
  서비스 config.yaml
       │
       ▼
  최종 merged 설정 → API 응답
```

### 6.3 메모리 구조

```go
type ConfigStore struct {
    mu       sync.RWMutex

    // 서비스별 원본 설정 (상속 미적용)
    configs  map[string]*ServiceConfig   // key: "org/project/service"

    // 계층별 defaults
    globalDefaults   *DefaultsConfig              // _defaults/common.yaml
    orgDefaults      map[string]*DefaultsConfig   // key: "org"
    projectDefaults  map[string]*DefaultsConfig   // key: "org/project"

    // 서비스별 merged 설정 캐시 (상속 적용 완료)
    mergedCache map[string]*ResolvedConfig  // key: "org/project/service"

    version  string
}
```

### 6.4 Merge 전략

#### Pre-compute vs On-demand

| 방식 | 장점 | 단점 | 결정 |
|------|------|------|------|
| **Pre-compute** (Git 갱신 시 전체 merge) | API 응답 시 계산 없음. 읽기 성능 최대 | defaults 변경 시 전체 재계산 필요 | **채택** |
| On-demand (요청 시 매번 merge) | 메모리 절약 | 요청마다 merge 비용 발생 (p99 지연 증가) | 미채택 |

defaults 변경 빈도가 낮고 (드물게 발생), 읽기 요청이 압도적으로 많으므로 pre-compute가 적합하다.

#### Merge 구현

```go
// DeepMerge: base 위에 override를 deep merge한다.
// - 스칼라: override가 우선
// - map: 재귀 merge
// - 배열: override가 전체 교체
// - null: base에서 해당 키 삭제
func DeepMerge(base, override map[string]interface{}) map[string]interface{} {
    result := copyMap(base)
    for key, overrideVal := range override {
        if overrideVal == nil {
            delete(result, key)   // null → 키 삭제
            continue
        }
        baseVal, exists := result[key]
        if !exists {
            result[key] = deepCopy(overrideVal)
            continue
        }
        baseMap, baseIsMap := baseVal.(map[string]interface{})
        overMap, overIsMap := overrideVal.(map[string]interface{})
        if baseIsMap && overIsMap {
            result[key] = DeepMerge(baseMap, overMap)  // 재귀 merge
        } else {
            result[key] = deepCopy(overrideVal)  // 스칼라/배열: 전체 교체
        }
    }
    return result
}
```

### 6.5 갱신 흐름

Git 변경 감지 시 다음 순서로 재계산한다:

```
1. 변경된 파일 식별 (git diff)
2. 변경된 파일이 _defaults인지 서비스 config인지 판별
   ├─ _defaults 변경: 해당 계층 이하 모든 서비스의 mergedCache 무효화 + 재계산
   └─ 서비스 config 변경: 해당 서비스의 mergedCache만 재계산
3. atomic pointer swap으로 mergedCache 교체 (COW 패턴)
```

**defaults 변경의 영향 범위**:
- 전역 `_defaults` 변경 → 모든 서비스 재계산
- 조직 `_defaults` 변경 → 해당 조직 내 모든 서비스 재계산
- 프로젝트 `_defaults` 변경 → 해당 프로젝트 내 모든 서비스 재계산

### 6.6 env_vars 상속

`env_vars.yaml`도 동일한 계층 구조로 상속된다:

```yaml
# _defaults/common.yaml의 env_vars 블록
env_vars:
  plain:
    LITELLM_TELEMETRY: "false"    # 전역: 텔레메트리 비활성화
    LITELLM_LOG_LEVEL: "INFO"     # 전역: 기본 로그 레벨
```

서비스 `env_vars.yaml`이 동일 키를 정의하면 상위 값을 덮어쓴다. `secret_refs`도 동일하게 merge된다.

---

## 7. 주요 설계 결정 (Design Decisions)

### 7.1 왜 SealedSecret인가

| 대안 | 장점 | 단점 | 결정 |
|------|------|------|------|
| **SealedSecret** | Git에 안전하게 저장, 감사 추적, GitOps 친화적 | 클러스터에 Controller 필요 | **채택** |
| External Secrets Operator | 외부 Vault 연동 가능 | 외부 의존성(Vault) 추가, 복잡도 증가 | 미채택 |
| kubectl create secret | 단순함 | Git 추적 불가, 수동 관리, 감사 어려움 | 미채택 |
| SOPS | Git 암호화 가능 | KMS 의존성, K8s 네이티브가 아님 | 미채택 |

### 7.2 왜 Config Agent는 폴링인가

| 방식 | 장점 | 단점 | 결정 |
|------|------|------|------|
| **Long Polling** | 구현 간단, 방화벽 친화적, 디버깅 용이 | 변경 반영 지연 (최대 30초) | **채택** |
| Webhook (push) | 즉시 반영 | Config Server → Agent 방향 연결 필요, Agent 장애 시 유실 | 미채택 |
| gRPC streaming | 실시간 | 연결 유지 비용, 복잡도 | 미채택 |

### 7.3 왜 중앙 집중형 Agent인가

litellm처럼 동일 Deployment의 replica가 동일 config을 공유하는 경우:

| 방식 | Polling 요청 수 | 재시작 제어 | 결정 |
|------|----------------|------------|------|
| **중앙 Agent (replica=2)** | 2 | Rolling update 보장, HA | **채택** |
| 사이드카 (per Pod) | N (replica 수) | Thundering herd 위험 | 미채택 |

---

## 8. 의존성

### 8.1 클러스터 사전 요구사항

| 컴포넌트 | 용도 | 필수 여부 |
|----------|------|----------|
| **Bitnami SealedSecrets Controller** | SealedSecret → K8s Secret 복호화 | 필수 |
| **K8s etcd encryption at rest** | Secret 저장 시 암호화 | 권장 |
| **K8s Network Policy 지원** (Calico/Cilium) | Pod 간 네트워크 접근 제어 | 권장 |

### 8.2 Config Server 의존성

| 의존성 | 용도 |
|--------|------|
| Git Repository | 설정 파일 원본 저장소 (Source of Truth) |
| AAP Console API | App Registry 로드 (인증/인가) |
| K8s API (client-go) | SealedSecret apply |
| Volume Mount | Secret 값 읽기 (resolve_secrets) |
| SealedSecret Controller 공개키 | kubeseal 암호화 |

### 8.3 Config Agent 의존성

| 의존성 | 용도 |
|--------|------|
| Config Server API | 설정/환경변수 조회, 변경 감지 |
| K8s API | ConfigMap/Secret 업데이트, Deployment annotation 패치 |

---

## 9. 보안 경계

```
┌─ 보안 영역 ─────────────────────────────────────────────────────┐
│                                                                 │
│  시크릿 평문이 존재하는 곳 (최소화):                               │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  1. Console → Config Server Admin API 전송 중 (HTTPS)    │    │
│  │  2. Config Server 메모리 (kubeseal 처리 중, 즉시 폐기)   │    │
│  │  3. K8s etcd (encryption at rest)                       │    │
│  │  4. Config Server Pod Volume Mount (시크릿 파일)         │    │
│  │  5. Config Server 메모리 (resolve_secrets 응답 중)       │    │
│  │  6. Config Agent 메모리 (ConfigMap/Secret 업데이트 중)    │    │
│  │  7. litellm Pod 내부 (env.sh/환경변수)                   │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                 │
│  시크릿 평문이 존재하지 않는 곳:                                   │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  - Git 저장소 (SealedSecret = 암호화된 형태만)            │    │
│  │  - Config Server 로그 (secret_ref ID만 기록)             │    │
│  │  - HTTP 캐시 (Cache-Control: no-store)                  │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```
