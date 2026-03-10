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
│ (시크릿 생성) │
└──────┬──────┘
       │ webhook
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
| **Config Agent** | Config Server 폴링, 변경 감지, ConfigMap 업데이트, Rolling restart 트리거 | 시크릿 관리/암호화/복호화 |
| **kubelet** | Secret/ConfigMap Volume Mount 자동 sync | - |
| **litellm Pod** | 마운트된 config/secret 파일 읽기 | - |

---

## 2. 핵심 흐름

### 2.1 시크릿 생성/변경 흐름

Console이 시크릿을 생성하면, Config Server가 관리 주체로서 암호화, Git 저장, 클러스터 apply를 모두 수행한다.

```
Console                Config Server                Git Repo            K8s Cluster
  │                         │                          │                      │
  │  POST /webhook          │                          │                      │
  │  (시크릿 평문 포함)       │                          │                      │
  ├────────────────────────▶│                          │                      │
  │                         │                          │                      │
  │                         │  1. kubeseal 암호화       │                      │
  │                         │     (공개키로 SealedSecret│                      │
  │                         │      YAML 생성)          │                      │
  │                         │                          │                      │
  │                         │  2. Git commit & push    │                      │
  │                         ├─────────────────────────▶│                      │
  │                         │   sealed-secrets/        │                      │
  │                         │     litellm-secrets.yaml │                      │
  │                         │                          │                      │
  │                         │  3. kubectl apply        │                      │
  │                         │     SealedSecret         │                      │
  │                         ├─────────────────────────────────────────────────▶│
  │                         │                          │                      │
  │                         │                          │  SealedSecret Controller
  │                         │                          │  복호화 → K8s Secret 생성
  │                         │                          │                      │
  │                         │                          │  kubelet: Volume Mount
  │                         │                          │  자동 sync (~60초)
  │                         │                          │                      │
```

**SealedSecret이란**: Bitnami SealedSecrets는 K8s Secret을 공개키로 암호화하여 Git에 안전하게 저장할 수 있게 하는 CRD이다. 클러스터 내 SealedSecret Controller만이 비밀키로 복호화할 수 있다.

### 2.2 설정 변경 감지 + 적용 흐름

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
  │  GET /config                 │                            │
  │  ?resolve_secrets=true       │                            │
  ├─────────────────────────────▶│                            │
  │                              │  Volume Mount 경로에서       │
  │                              │  시크릿 값 읽기              │
  │                              │  (/secrets/{ns}/{name}/{key})│
  │                              │                            │
  │  응답: 전체 설정 + 시크릿     │                            │
  │◀─────────────────────────────┤                            │
  │                              │                            │
  │  ConfigMap 업데이트            │                            │
  ├──────────────────────────────────────────────────────────▶│
  │                              │                            │
  │  (환경변수 변경 시)            │                            │
  │  Rolling restart 트리거       │                            │
  ├──────────────────────────────────────────────────────────▶│
  │                              │                            │
  │                              │                 litellm Pods
  │                              │                 재시작 + 새 설정
```

### 2.3 litellm Pod 내부 구조

litellm Pod는 두 가지 경로로 설정과 시크릿을 수신한다.

```
litellm Pod
┌──────────────────────────────────────────────────────────────┐
│                                                              │
│  ConfigMap (Volume Mount)                                    │
│  └── config.yaml  ← Config Agent가 업데이트                   │
│      (litellm proxy_config 형식)                              │
│      - model_list, router_settings, ...                      │
│      - 시크릿 값 포함 (Config Server에서 resolve됨)             │
│                                                              │
│  Secret (Volume Mount)                                       │
│  └── api-keys     ← SealedSecret Controller가 업데이트        │
│                      kubelet이 자동 sync (~60초)              │
│                                                              │
│  환경변수                                                     │
│  └── source /config/env.sh                                   │
│      - 평문 환경변수 (LITELLM_LOG_LEVEL, ...)                  │
│      - 시크릿 환경변수 (DATABASE_URL, MASTER_KEY, ...)          │
│      ← Config Agent가 Secret 리소스로 관리                    │
│                                                              │
│  Entrypoint:                                                 │
│  source /config/env.sh && litellm --config /config/config.yaml│
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

**시크릿이 litellm에 도달하는 두 경로:**

| 경로 | 흐름 | 용도 |
|------|------|------|
| **Volume Mount** | SealedSecret Controller → K8s Secret → kubelet sync → 파일 | litellm이 시크릿 파일을 직접 읽는 경우 |
| **Config Agent** | Config Server (resolve) → Config Agent → ConfigMap/Secret → Pod 재시작 | config.yaml 내 시크릿, 환경변수 시크릿 |

---

## 3. 시크릿 관리 아키텍처

### 3.1 시크릿 라이프사이클

```
┌─────────────────────────────────────────────────────────────────┐
│  시크릿 라이프사이클                                              │
│                                                                 │
│  생성        Console에서 시크릿 값 입력                            │
│    │         POST /webhook → Config Server                      │
│    ▼                                                            │
│  암호화      Config Server가 kubeseal로 SealedSecret 생성        │
│    │         (SealedSecret Controller의 공개키 사용)              │
│    ▼                                                            │
│  저장        Git에 SealedSecret YAML commit & push              │
│    │         (암호화된 상태 → Git 유출 시에도 안전)                 │
│    ▼                                                            │
│  적용        Config Server가 kubectl apply SealedSecret          │
│    │                                                            │
│    ▼                                                            │
│  복호화      SealedSecret Controller가 → K8s Secret 생성         │
│    │         (클러스터 내 비밀키로만 복호화 가능)                   │
│    ▼                                                            │
│  배포        kubelet Volume Mount sync → Pod에 반영              │
│              Config Agent polling → ConfigMap 업데이트 → 반영     │
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

```
Developer/Console       Git Repo         Config Server       Config Agent        litellm Pods
   │                      │                   │                   │                   │
   ├─ PR merge / ────────▶│                   │                   │                   │
   │  webhook             ├─ webhook ────────▶│                   │                   │
   │                      │                   ├─ git pull         │                   │
   │                      │                   ├─ 메모리 갱신       │                   │
   │                      │                   │                   │                   │
   │                      │                   │◀── poll (30초) ───┤                   │
   │                      │                   ├── {changed: true}▶│                   │
   │                      │                   │                   │                   │
   │                      │                   │◀── GET /config ───┤                   │
   │                      │                   ├── 설정 응답 ──────▶│                   │
   │                      │                   │                   ├─ ConfigMap 업데이트 │
   │                      │                   │                   ├─ Deployment        │
   │                      │                   │                   │  annotation 패치   │
   │                      │                   │                   │         │          │
   │                      │                   │                   │   Rolling restart  │
   │                      │                   │                   │   maxUnavail: 25%  │
   │                      │                   │                   │         └─────────▶│
   │                      │                   │                   │   새 Pod → 새 설정  │
```

**반영 방식**: ConfigMap 업데이트 → Deployment annotation 패치 → rolling restart (maxUnavailable/maxSurge: 25%)

### 4.2 환경변수 변경 (env_vars.yaml)

```
Developer/Console       Git Repo         Config Server       Config Agent        litellm Pods
   │                      │                   │                   │                   │
   ├─ PR merge / ────────▶│                   │                   │                   │
   │  webhook             ├─ webhook ────────▶│                   │                   │
   │                      │                   ├─ 메모리 갱신       │                   │
   │                      │                   │                   │                   │
   │                      │                   │◀── poll (30초) ───┤                   │
   │                      │                   ├── {changed: true}▶│                   │
   │                      │                   │                   │                   │
   │                      │                   │◀── GET /env_vars ─┤                   │
   │                      │                   ├── 환경변수 응답 ──▶│                   │
   │                      │                   │                   ├─ Secret 업데이트   │
   │                      │                   │                   ├─ Deployment        │
   │                      │                   │                   │  annotation 패치   │
   │                      │                   │                   │         │          │
   │                      │                   │                   │   K8s rolling      │
   │                      │                   │                   │   update 시작      │
   │                      │                   │                   │   maxUnavail: 25%  │
   │                      │                   │                   │   maxSurge: 25%    │
   │                      │                   │                   │     재시작          │
```

**반영 방식**: Secret 업데이트 + Deployment annotation 패치 → K8s rolling update (maxUnavailable/maxSurge: 25%)

### 4.3 시크릿 변경 (Console에서 시작)

```
Console              Config Server           Git Repo        K8s Cluster         Config Agent
  │                       │                     │                 │                   │
  │  POST /webhook        │                     │                 │                   │
  │  (새 시크릿 값)        │                     │                 │                   │
  ├──────────────────────▶│                     │                 │                   │
  │                       │                     │                 │                   │
  │                       ├─ kubeseal 암호화     │                 │                   │
  │                       │                     │                 │                   │
  │                       ├─ Git commit & push ▶│                 │                   │
  │                       │                     │                 │                   │
  │                       ├─ kubectl apply ─────────────────────▶│                   │
  │                       │  SealedSecret       │                 │                   │
  │                       │                     │                 │                   │
  │                       │                     │  SealedSecret   │                   │
  │                       │                     │  Controller     │                   │
  │                       │                     │  복호화 →       │                   │
  │                       │                     │  K8s Secret     │                   │
  │                       │                     │  생성/업데이트   │                   │
  │                       │                     │                 │                   │
  │                       │                     │  Volume Mount   │                   │
  │                       │                     │  자동 sync      │                   │
  │                       │                     │  (~60초)        │                   │
  │                       │                     │                 │                   │
  │                       │                     │                 │◀── poll (30초) ───┤
  │                       │                     │                 ├── {changed} ─────▶│
  │                       │                     │                 │                   │
  │                       │                     │                 │  ConfigMap/Secret  │
  │                       │                     │                 │  업데이트 +        │
  │                       │                     │                 │  Rolling restart   │
```

**시크릿 변경 시 두 경로로 litellm에 반영:**
1. **Volume Mount 경로**: SealedSecret Controller → K8s Secret → kubelet sync → litellm 파일 자동 갱신
2. **Config Agent 경로**: Config Server 폴링 → resolve_secrets → ConfigMap/Secret 업데이트 → Rolling restart

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

### 5.2 네트워크 통신

```
┌─────────────────────────────────────────────────────────────────┐
│  K8s Cluster 내부 통신 (Network Policy로 접근 제어)               │
│                                                                 │
│  Console ──── webhook ──────────▶ Config Server                 │
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

## 6. 주요 설계 결정

### 6.1 왜 SealedSecret인가

| 대안 | 장점 | 단점 | 결정 |
|------|------|------|------|
| **SealedSecret** | Git에 안전하게 저장, 감사 추적, GitOps 친화적 | 클러스터에 Controller 필요 | **채택** |
| External Secrets Operator | 외부 Vault 연동 가능 | 외부 의존성(Vault) 추가, 복잡도 증가 | 미채택 |
| kubectl create secret | 단순함 | Git 추적 불가, 수동 관리, 감사 어려움 | 미채택 |
| SOPS | Git 암호화 가능 | KMS 의존성, K8s 네이티브가 아님 | 미채택 |

### 6.2 왜 Config Agent는 폴링인가

| 방식 | 장점 | 단점 | 결정 |
|------|------|------|------|
| **Long Polling** | 구현 간단, 방화벽 친화적, 디버깅 용이 | 변경 반영 지연 (최대 30초) | **채택** |
| Webhook (push) | 즉시 반영 | Config Server → Agent 방향 연결 필요, Agent 장애 시 유실 | 미채택 |
| gRPC streaming | 실시간 | 연결 유지 비용, 복잡도 | 미채택 |

### 6.3 왜 중앙 집중형 Agent인가

litellm처럼 동일 Deployment의 replica가 동일 config을 공유하는 경우:

| 방식 | Polling 요청 수 | 재시작 제어 | 결정 |
|------|----------------|------------|------|
| **중앙 Agent (replica=2)** | 2 | Rolling update 보장, HA | **채택** |
| 사이드카 (per Pod) | N (replica 수) | Thundering herd 위험 | 미채택 |

---

## 7. 의존성

### 7.1 클러스터 사전 요구사항

| 컴포넌트 | 용도 | 필수 여부 |
|----------|------|----------|
| **Bitnami SealedSecrets Controller** | SealedSecret → K8s Secret 복호화 | 필수 |
| **K8s etcd encryption at rest** | Secret 저장 시 암호화 | 권장 |
| **K8s Network Policy 지원** (Calico/Cilium) | Pod 간 네트워크 접근 제어 | 권장 |

### 7.2 Config Server 의존성

| 의존성 | 용도 |
|--------|------|
| Git Repository | 설정 파일 원본 저장소 (Source of Truth) |
| AAP Console API | App Registry 로드 (인증/인가) |
| K8s API | SealedSecret apply |
| Volume Mount | Secret 값 읽기 (resolve_secrets) |
| SealedSecret Controller 공개키 | kubeseal 암호화 |

### 7.3 Config Agent 의존성

| 의존성 | 용도 |
|--------|------|
| Config Server API | 설정/환경변수 조회, 변경 감지 |
| K8s API | ConfigMap/Secret 업데이트, Deployment annotation 패치 |

---

## 8. 보안 경계

```
┌─ 보안 영역 ─────────────────────────────────────────────────────┐
│                                                                 │
│  시크릿 평문이 존재하는 곳 (최소화):                               │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  1. Console → Config Server webhook 전송 중 (HTTPS)     │    │
│  │  2. Config Server 메모리 (kubeseal 처리 중, 즉시 폐기)   │    │
│  │  3. K8s etcd (encryption at rest)                       │    │
│  │  4. Config Server 메모리 (resolve_secrets 응답 중)       │    │
│  │  5. Config Agent 메모리 (ConfigMap/Secret 업데이트 중)    │    │
│  │  6. litellm Pod 내부 (파일/환경변수)                     │    │
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
