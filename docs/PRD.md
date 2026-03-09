# AAP Config Server — Product Requirements Document

> **Version**: 1.1
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
│  │ webhook) │    │              │    │ (volume mount)│       │
│  └──────────┘    └──────┬───────┘    └───────────────┘       │
│                         │                                    │
│                  ┌──────▼───────┐    ┌───────────────────┐   │
│                  │   REST API   │───▶│ Secret Encryption │   │
│                  │   Handler    │    │ Engine            │   │
│                  └──────┬───────┘    └───────────────────┘   │
│                         │                                    │
└─────────────────────────┼────────────────────────────────────┘
                          │
              ┌───────────▼───────────┐
              │   litellm / 기타      │
              │   클라이언트 서비스     │
              │   (client private key)│
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
├── clients/                      # 클라이언트 등록 정보
│   └── {client-id}.yaml          # 클라이언트별 공개키, 권한 등
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

#### 클라이언트 등록 (`clients/{client-id}.yaml`)

```yaml
# clients/litellm-prod.yaml
client_id: "litellm-prod"
description: "Production LiteLLM instance"
public_key: |
  -----BEGIN PUBLIC KEY-----
  MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA...
  -----END PUBLIC KEY-----
allowed_scopes:
  - org: "myorg"
    project: "ai-platform"
    service: "litellm"
    resolve_secrets: true
created_at: "2026-03-01T00:00:00Z"
```

### 3.3 시크릿 관리: Volume Mount + Reference 패턴

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

K8s Secret을 Volume Mount로 마운트하여 파일 읽기로 접근한다. K8s API를 매 요청마다 호출하는 방식 대비:

- **네트워크 불필요**: 로컬 파일 읽기이므로 API Server 부하 없음
- **kubelet 자동 갱신**: Secret 변경 시 kubelet이 마운트된 파일을 자동 갱신 (~1분 이내)
- **고빈도 조회 안전**: K8s API throttling 걱정 없음

설정 서버 특성상 시크릿의 1분 이내 갱신 지연은 허용 가능하다.

### 3.4 설정 상속 (Config Inheritance)

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

## 4. 시크릿 암호화

시크릿은 어떤 경우에도 평문으로 네트워크를 통해 전송하지 않는다. mTLS로 전송 채널을 보호하더라도, 응답 본문의 평문 시크릿은 로깅, 캐싱, 프록시 등에서 유출될 수 있다. 따라서 **응답 페이로드 레벨의 암호화**를 적용한다.

### 4.1 암호화 방식: Hybrid Encryption (ECDH + AES-256-GCM)

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

### 4.2 필드 단위 암호화 (Field-level Encryption)

응답 전체를 암호화하지 않고, **시크릿 필드만 개별 암호화**한다.

이유:
- 클라이언트가 비시크릿 설정은 즉시 사용 가능 (복호화 불필요)
- 로그/캐시에 응답이 남아도 시크릿만 보호됨
- 부분적 복호화 실패가 전체 설정 사용을 차단하지 않음

### 4.3 응답 형식

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

### 4.4 클라이언트 등록 및 키 관리

#### 등록 흐름

```
Admin                    Git Repo                Config Server
  │                         │                         │
  ├─ 클라이언트 키 쌍 생성    │                         │
  │  (ECDH P-256)           │                         │
  │                         │                         │
  ├─ clients/litellm.yaml  │                         │
  │  (공개키 포함) PR ──────▶│                         │
  │                         │                         │
  │   (리뷰 & 승인 & merge)  │                         │
  │                         ├─ sync ─────────────────▶│
  │                         │                         ├─ 공개키 메모리 적재
  │                         │                         │
  ├─ 비밀키를 K8s Secret ──▶ 클라이언트 Pod에 마운트    │
  │  으로 배포               │                         │
```

#### 키 순환 (Key Rotation)

```yaml
# clients/litellm-prod.yaml — 키 순환 시
client_id: "litellm-prod"
public_keys:
  - key_id: "key-2026-03"
    status: "active"
    public_key: |
      -----BEGIN PUBLIC KEY-----
      (새 키)
      -----END PUBLIC KEY-----
  - key_id: "key-2026-01"
    status: "deprecated"      # 유예 기간 후 삭제
    expires_at: "2026-04-01T00:00:00Z"
    public_key: |
      -----BEGIN PUBLIC KEY-----
      (이전 키)
      -----END PUBLIC KEY-----
```

- 클라이언트는 요청 시 `X-Key-ID` 헤더로 사용할 키를 지정
- `deprecated` 키는 유예 기간 동안 허용, 이후 거부
- Config Server가 알 수 없는 `key_id`로 요청 시 `401` 응답

### 4.5 시크릿 값 Resolve 전체 흐름

```
1. 클라이언트가 설정 요청
   GET /api/v1/.../config?resolve_secrets=true
   Header: Authorization: Bearer <token>
   Header: X-Client-ID: litellm-prod
   Header: X-Key-ID: key-2026-03

2. Config Server: 인증/인가 검증
   - Bearer Token 유효성
   - 클라이언트 ID의 allowed_scopes에 해당 서비스 + resolve_secrets 권한 확인

3. Config Server: 설정 조립
   - config.yaml에서 *_secret_ref 필드 탐지
   - secrets.yaml에서 해당 ID의 K8s Secret 경로 확인
   - Volume Mount 경로에서 실제 시크릿 값 읽기 (/secrets/{namespace}/{name}/{key})

4. Config Server: 시크릿 암호화
   - 임시 ECDH 키 쌍 생성
   - 클라이언트 공개키(key_id로 조회) + 임시 비밀키 → ECDH 공유 비밀
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

## 5. API 설계

### 5.1 설정 조회 API

#### 단일 서비스 설정 조회

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config
```

**Request Headers**:

| 헤더 | 필수 | 설명 |
|------|------|------|
| `Authorization` | Y | `Bearer <token>` |
| `X-Client-ID` | 시크릿 요청 시 | 클라이언트 식별자 |
| `X-Key-ID` | 시크릿 요청 시 | 사용할 공개키 식별자 |

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

**Response** (`resolve_secrets=true`): 4.3절 응답 형식 참조

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

### 5.2 설정 변경 감지 API (Long Polling)

```
GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config/watch?version={current_version}
```

- 현재 클라이언트가 가진 `version`(git commit hash)과 서버의 최신 버전이 다르면 즉시 응답
- 같으면 변경이 생길 때까지 hold (최대 30초 후 `304 Not Modified`)
- 클라이언트는 응답 받은 후 다시 요청 (long polling loop)

### 5.3 헬스체크 / 운영 API

```
GET /healthz                      # Liveness
GET /readyz                       # Readiness (git sync 완료 여부)
GET /api/v1/status                # 서버 상태 (마지막 sync 시각, 로드된 설정 수 등)
POST /api/v1/admin/reload         # 수동 설정 리로드 트리거
```

---

## 6. 고성능 설계

### 6.1 성능 목표

| 지표 | 목표 |
|------|------|
| Throughput | 100,000+ req/s (단일 인스턴스, 시크릿 미포함) |
| Throughput | 50,000+ req/s (단일 인스턴스, 시크릿 포함 — 암호화 오버헤드) |
| Latency (p99) | < 5ms (시크릿 미포함), < 15ms (시크릿 포함) |
| Memory | 설정 1,000개 기준 < 100MB |
| 시작 시간 | < 5초 (cold start) |

### 6.2 성능 전략

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

## 7. 설정 변경 워크플로우

### 7.1 일반 설정 변경

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

### 7.2 시크릿 변경

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

## 8. 보안

### 8.1 인증/인가

| 계층 | 방식 |
|------|------|
| 서비스 간 통신 | mTLS (전송 채널 암호화) |
| API 인증 | Bearer Token (static token 또는 K8s SA token) |
| 시크릿 접근 제어 | 클라이언트 등록 정보의 `allowed_scopes`에서 `resolve_secrets` 권한 확인 |
| 시크릿 페이로드 | ECDH + AES-256-GCM 필드 단위 암호화 (4절 참조) |

### 8.2 시크릿 보호 원칙

| 원칙 | 구현 |
|------|------|
| **저장 시 분리** | Git에는 메타데이터만, 실제 값은 K8s Secret에만 존재 |
| **전송 시 암호화** | mTLS(채널) + ECDH+AES-256-GCM(페이로드) 이중 보호 |
| **로그 금지** | 시크릿 값은 절대 로그에 출력하지 않음, 로그에는 secret_ref ID만 기록 |
| **캐시 금지** | 시크릿 포함 응답에 `Cache-Control: no-store` 헤더, ETag 미적용 |
| **Forward Secrecy** | 매 응답마다 임시 키 사용 → 키 유출 시에도 과거 응답 복호화 불가 |
| **감사 로깅** | 시크릿 접근 시 클라이언트 ID, 시간, 요청 scope을 감사 로그에 기록 |
| **메모리 내 시크릿** | 사용 후 메모리에서 즉시 제로화 (Go `crypto/subtle.ConstantTimeCompare` 패턴) |

### 8.3 위협 시나리오별 방어

| 위협 | 방어 |
|------|------|
| 네트워크 스니핑 | mTLS + 페이로드 암호화 → 이중 암호화 상태 |
| 프록시/LB 로깅 | 페이로드 레벨 암호화 → 중간 장비가 시크릿 평문 접근 불가 |
| 서버 메모리 덤프 | 시크릿은 요청 처리 중에만 메모리에 존재, 처리 후 제로화 |
| 클라이언트 키 유출 | Forward Secrecy로 과거 응답 보호, 키 순환으로 미래 노출 차단 |
| Git 저장소 유출 | 시크릿 값이 Git에 없으므로 영향 없음 (메타데이터만 노출) |
| Config Server 키 유출 | 서버는 장기 키를 보유하지 않음 (클라이언트 공개키만 보유) |

---

## 9. 기술 스택

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

## 10. 마일스톤

### Phase 1: Core (MVP)

- [ ] Go 프로젝트 구조 세팅
- [ ] Git 저장소 clone/pull 및 설정 파일 파싱
- [ ] In-memory config store 구현 (COW 패턴)
- [ ] REST API: 단일 서비스 설정 조회 (`GET /api/v1/.../config`)
- [ ] 주기적 Git poll 기반 설정 갱신
- [ ] Health check 엔드포인트 (`/healthz`, `/readyz`)
- [ ] Dockerfile 및 기본 K8s manifests

### Phase 2: Secrets & Encryption

- [ ] Volume Mount 기반 시크릿 로딩
- [ ] `secrets.yaml` 파싱 및 시크릿 참조 resolve
- [ ] 클라이언트 등록 및 공개키 관리
- [ ] ECDH + AES-256-GCM 필드 단위 암호화 구현
- [ ] `resolve_secrets` 쿼리 파라미터 구현
- [ ] 키 순환 (deprecated key 유예 기간) 지원
- [ ] 시크릿 접근 감사 로깅

### Phase 3: Auth & Security

- [ ] Bearer Token 인증 미들웨어
- [ ] 클라이언트별 `allowed_scopes` 인가 검증
- [ ] 시크릿 메모리 제로화
- [ ] 보안 헤더 설정 (`Cache-Control: no-store` 등)

### Phase 4: Performance & Operations

- [ ] ETag / If-None-Match 지원
- [ ] gzip 응답 압축
- [ ] 임시 ECDH 키 풀 (pre-generation)
- [ ] Prometheus 메트릭 export
- [ ] Git webhook 수신 엔드포인트
- [ ] Config watch (long polling) API
- [ ] Batch 조회 API
- [ ] 설정 상속 (계층적 _defaults merge)

### Phase 5: Hardening

- [ ] Graceful shutdown
- [ ] 설정 파일 스키마 검증
- [ ] Rate limiting
- [ ] 통합 테스트 / 부하 테스트 / 암호화 벤치마크
- [ ] Helm chart
