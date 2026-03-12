# AAP Config Server — High-Level Design (HLD)

> **버전**: 1.2
> **작성일**: 2026-03-11
> **최종 수정일**: 2026-03-12
> **상태**: Draft
> **참조**: [PRD v2.0](./PRD.md)

---

## 0. PRD 요구사항 추적 (Requirement Traceability)

본 HLD는 [PRD v2.0](./PRD.md)에 정의된 기능 요구사항(FR-1 ~ FR-17)을 설계 수준에서 구현한다. 각 HLD 섹션에 `[FR-X]` 태그를 표기하여 PRD 요구사항과의 추적성을 보장한다.

### 0.1 FR → HLD 매핑

| FR ID | 기능 요구사항 | PRD 섹션 | HLD 섹션 | Phase |
|-------|-------------|----------|---------|-------|
| **FR-1** | Git-backed In-memory Config Store | 4.1 | 1.1, 3.2, 5, 6.3, 6.4, 6.5 | 1 |
| **FR-2** | 설정 조회 API | 4.2 | 2.1 | 1 |
| **FR-3** | 환경변수 조회 API | 4.3 | 2.1 | 1 |
| **FR-4** | 설정/시크릿 일괄 변경 Admin API | 4.4 | 2.2, 4, 4.1, 4.2, 4.3 | 1 |
| **FR-5** | 설정/시크릿 일괄 삭제 Admin API | 4.5 | 2.2, 4.4 | 1 |
| **FR-6** | 설정/환경변수 변경 감지 (Long Polling) | 4.6 | 2.1, 2.5 | 5 |
| **FR-7** | 시크릿 관리 (SealedSecret + Volume Mount) | 4.7 | 2.3, 3, 3.1, 3.3, 3.4, 4.3, 5 | 2 |
| **FR-8** | App Registry 연동 | 4.8 | 2.4 | 2 |
| **FR-9** | Config Agent (중앙 집중형) | 4.9 | 2.5, 2.7, 4.1, 4.2, 4.3, 5 | 3 |
| **FR-10** | 설정 상속 (Config Inheritance) | 4.10 | 6 | 5 |
| **FR-11** | 서비스 탐색 API | 4.11 | 2.1 | 5 |
| **FR-12** | 시크릿 메타데이터 API | 4.12 | 2.1 | 5 |
| **FR-13** | 변경 이력 API | 4.13 | 2.1 | 5 |
| **FR-14** | 설정 롤백 API | 4.14 | 2.6 | 5 |
| **FR-15** | 헬스체크 / 운영 API | 4.15 | 2.1 | 1 |
| **FR-16** | 인증/인가 (API Key Bearer 인증) | 4.16 | 2.4, 9 | 4 |
| **FR-17** | 시크릿 보안 | 4.17 | 3.4, 9 | 4 |

### 0.2 Console PRD 연관

Console PRD의 다음 FR이 Config Server API를 호출한다:

| Console FR | Console 기능 | Config Server FR | Config Server API |
|-----------|-------------|-----------------|-------------------|
| Console FR-3 | Project 삭제 (롤백) | FR-5, FR-8 | `DELETE /admin/changes`, `POST /admin/app-registry/webhook` |
| Console FR-5 | Langfuse SK/PK 전달 | FR-4, FR-7 | `POST /admin/changes` (secrets 필드) |
| Console FR-6 | LiteLLM Config 반영 | FR-4 | `POST /admin/changes` (config 필드) |
| Console FR-7 | 프로비저닝 파이프라인 | FR-4, FR-8 | `POST /admin/changes`, `POST /admin/app-registry/webhook` |
| Console FR-8 | 설정 버전 롤백 | FR-14 | `POST /admin/changes/revert` |

---

## 1. 시스템 개요 `[FR-1]` `[FR-7]` `[FR-9]`

AAP Config Server는 서비스 설정을 중앙에서 관리하고, 시크릿을 안전하게 저장/배포하며, 변경사항을 자동으로 클러스터에 반영하는 시스템이다.

### 1.1 컴포넌트 구성 `[FR-1]` `[FR-7]` `[FR-9]`

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
      ││aap-helm- │   │  mount
      ││charts    │   │   │
      ││(configs/ │   │   │
      ││+ sealed- │   │   │
      ││secrets/) │   │   │
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

## 2. 핵심 흐름 `[FR-2]` `[FR-3]` `[FR-4]` `[FR-5]` `[FR-11]` `[FR-12]` `[FR-13]` `[FR-15]`

### 2.1 Config Server API 목록

> 아래 API는 모두 **Config Server가 제공하는 엔드포인트**이다. 호출 주체별로 분류한다.

**Console → Config Server Admin API (쓰기):**

| API | 용도 |
|-----|------|
| `POST /api/v1/admin/changes` | 설정/시크릿 일괄 생성·변경 (단일 atomic Git commit) |
| `DELETE /api/v1/admin/changes` | 설정/시크릿 일괄 삭제 (단일 atomic Git commit) |
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

### 2.2 설정 쓰기 흐름 (Console → Config Server → Git) `[FR-4]` `[FR-5]`

Console이 설정/시크릿을 변경하면, Config Server의 통합 Admin API (`POST /admin/changes`)를 호출한다. Console은 Git/kubeseal/kubectl을 직접 조작하지 않는다 (**Console Creates, Server Manages** 원칙).

> **`aap-console` PRD 참조**: Console → Config Server 인터페이스는 Console PRD Section 3 "시스템 아키텍처 개요"에 정의된 Admin API 계약을 따른다.

```
Console                Config Server           aap-helm-charts       K8s Cluster
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

### 2.3 시크릿 처리 흐름 (changes API 내부) `[FR-7]`

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

### 2.4 App Registry 연동 및 API Key 인증 흐름 `[FR-8]` `[FR-16]`

Console이 org/project/service를 생성·수정·삭제하면, webhook으로 Config Server에 통지한다. Config Server는 인메모리 App Registry 캐시를 갱신한다.

Console → Config Server 통신은 **환경변수 기반 API Key Bearer 인증**으로 보호된다. 운영자가 양쪽에 동일한 API Key를 환경변수로 설정한다.

#### API Key 인증 흐름

```
Console                       Config Server
  │                                │
  │  POST /api/v1/admin/changes   │
  │  Authorization: Bearer <key>  │
  ├───────────────────────────────▶│
  │                                ├─ 1. Bearer 토큰 추출
  │                                ├─ 2. 환경변수 API_KEY와 비교
  │                                ├─ 3. 불일치 → 401
  │                                ├─ 4. 일치 → 요청 처리
  │  응답: {version: "..."}        │
  │◀───────────────────────────────┤
```

- Config Server: 환경변수 `API_KEY` (K8s Secret으로 주입 권장)
- Console: 환경변수 `CONFIG_SERVER_API_KEY`
- 키 교체: 양쪽 환경변수 변경 → Pod 재시작

#### App Registry webhook

Console이 App CRUD 시 Config Server로 webhook push(**fire-and-forget + async retry**)하여 인메모리 캐시를 갱신한다.

```
Console              Config Server
  │                       │
  ├─ POST /admin/         │
  │  app-registry/webhook │
  │  Authorization:       │
  │  Bearer <key>         │
  │──────────────────────▶│
  │                       ├─ 캐시 갱신
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
      │◀─┤ [{org, project, ...}] │
      │  │ → 메모리에 캐시 로드   │
      │  │                       │
      │  └─ 실패 ────────────────┤
      │    → exponential backoff │
      │      retry (최대 5회)     │
      │    → 최종 실패 시 빈 캐시  │
      │      로 기동 (설정 서빙은  │
      │      정상, API Key 인증은 │
      │      환경변수 기반으로     │
      │      독립 동작)           │
      │                          │
      ├─ readyz=true             │
```

> Config Server는 App Registry를 **직접 관리하지 않는다**. Console이 Single Source of Truth이며, Config Server는 캐시만 유지한다. Console 장애 시 기존 캐시로 서빙을 계속한다 (graceful degradation).

**시작 순서 독립성 (데드락 방지):**

Console과 Config Server는 **부팅 시 상호 의존하지 않도록** 설계한다:
- **Console**: 독립적으로 부팅 가능. App CRUD 시 Config Server webhook은 **fire-and-forget + async retry** 방식으로 처리하여, Config Server가 다운이어도 Console DB에는 정상 저장된다. 단, Config Server가 복구될 때까지 아래 기능은 제한된다:
  - App Registry webhook 전달 실패 → Config Server 캐시 불일치
  - 설정 조회/히스토리 등 Config Server 읽기 API 의존 화면 → 에러 상태
- **Config Server**: API Key 인증은 환경변수 기반이므로 Console 없이도 동작한다. App Registry 캐시는 Console API에서 로드하며, Console이 아직 준비되지 않은 경우 **exponential backoff retry** (최대 5회)로 재시도하고, 실패 시에도 빈 캐시 상태로 기동하여 설정 서빙은 정상 수행한다.

**캐시 정합성 보정**: webhook 유실은 Console의 async retry queue가 재시도한다. Config Server 재시작 시에는 시작 시 전체 로드로 캐시가 복구된다. 별도 주기적 동기화는 불필요하다.

따라서 **어떤 순서로 기동해도 부팅 데드락이 발생하지 않는다**. 양쪽 모두 상대방 없이 readyz=true까지 도달할 수 있으며, runtime 시 상대방이 복구되면 정상 기능이 점진적으로 회복된다.

### 2.5 설정 변경 감지 + 적용 흐름 `[FR-6]` `[FR-9]`

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

### 2.6 설정 롤백 흐름 `[FR-14]`

Console이 특정 버전(Git commit hash)으로 롤백을 요청하면, Config Server가 해당 시점의 모든 파일(config, env_vars, SealedSecret)을 복원한다. 롤백도 새 Git commit으로 생성되어 이력이 forward-only로 유지된다.

```
Console              Config Server         aap-helm-charts      K8s Cluster
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

### 2.7 litellm Pod 내부 구조 `[FR-9]`

litellm Pod는 ConfigMap과 Secret을 분리하여 마운트한다:

| Volume | K8s 리소스 | 마운트 경로 | 내용 |
|--------|-----------|-----------|------|
| config | ConfigMap (`litellm-config`) | `/config/` | config.yaml (시크릿은 `os.environ/` 참조, 평문 없음) |
| env | Secret (`litellm-env-secret`) | `/env/` | env.sh (평문 + 시크릿 환경변수 포함) |

Entrypoint: `source /env/env.sh && litellm --config /config/config.yaml`

시크릿 경로: SealedSecret Controller → K8s Secret 복호화 → Config Server Pod Volume Mount → Config Agent polling (resolve_secrets) → K8s Secret (env.sh) 업데이트 → Rolling restart → litellm Pod (env.sh source → os.environ/ resolve)

---

## 3. 시크릿 관리 아키텍처 `[FR-7]` `[FR-17]`

### 3.1 시크릿 라이프사이클 `[FR-7]`

생성(Console POST) → 암호화(Config Server kubeseal) → 저장(Git SealedSecret YAML) → 적용(K8s API apply) → 복호화(SealedSecret Controller → K8s Secret) → 배포(kubelet Volume Mount sync → Config Agent polling → Rolling restart → litellm Pod)

> 상세 흐름은 섹션 2.3 참조

### 3.2 Git 저장소 구조 (`aap-helm-charts`) `[FR-1]`

Config Server는 `aap-helm-charts` 레포의 `configs/` 하위만 읽고 쓴다. `charts/` 디렉토리는 Helm Chart 배포용이며 Config Server의 관심 밖이다.

```
aap-helm-charts/
├── charts/                              # Helm Charts (Config Server 관심 밖)
│   ├── litellm/
│   └── ...
│
├── configs/                             # Config Server 설정 데이터 루트
│   ├── _defaults/
│   │   └── common.yaml
│   │
│   └── orgs/
│       └── {org-name}/
│           ├── _defaults/
│           │   └── common.yaml
│           │
│           └── projects/
│               └── {project-name}/
│                   ├── _defaults/
│                   │   └── common.yaml
│                   │
│                   └── services/
│                       └── {service-name}/
│                           ├── config.yaml              # 일반 설정 (평문)
│                           ├── env_vars.yaml            # 환경변수 (평문 + secret refs)
│                           ├── secrets.yaml             # 시크릿 메타데이터 (값 없음)
│                           └── sealed-secrets/
│                               ├── litellm-secrets.yaml       # SealedSecret (암호화됨)
│                               ├── llm-provider-keys.yaml     # SealedSecret (암호화됨)
│                               ├── litellm-infra.yaml         # SealedSecret (암호화됨)
│                               └── guardrail-keys.yaml        # SealedSecret (암호화됨)
```

### 3.3 SealedSecret YAML 예시 `[FR-7]`

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

### 3.4 Config Server RBAC (시크릿 관리용) `[FR-7]` `[FR-17]`

Config Server에 필요한 최소 권한:
- `sealedsecrets` (bitnami.com): get, list, create, update, patch
- `secrets`: get (Volume Mount 구성용)
- `services/proxy` (sealed-secrets-controller): get (kubeseal 공개키 조회)

---

## 4. 설정 변경 시나리오별 상세 흐름 `[FR-4]`

### 4.1 일반 설정 변경 (config.yaml) `[FR-4]` `[FR-9]`

#### Console을 통한 설정 변경 (주 경로)

Console은 `POST /api/v1/admin/changes`를 호출한다. Config Server가 Git commit & push를 수행한다.

```
Console            Config Server     aap-helm-charts    Config Agent        litellm Pods
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
Developer     aap-helm-charts    Config Server       Config Agent        litellm Pods
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

### 4.2 환경변수 변경 (env_vars.yaml) `[FR-4]` `[FR-9]`

4.1과 동일한 흐름이되, Config Agent가 `GET /env_vars?resolve_secrets=true`로 조회하고 **Secret** (ConfigMap 대신)을 업데이트한다는 점만 다르다. config, env_vars, secrets를 동시에 변경할 수도 있다.

### 4.3 시크릿 변경 (Console에서 시작) `[FR-4]` `[FR-7]` `[FR-9]`

```
Console              Config Server      aap-helm-charts     K8s Cluster         Config Agent
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

### 4.4 설정/시크릿 일괄 삭제 (Project 삭제) `[FR-5]`

Console이 Project를 삭제할 때, 해당 서비스의 설정 + 시크릿 전체를 제거한다 (Console FR-3 롤백 대상).

```
Console              Config Server      aap-helm-charts     K8s Cluster         Config Agent
  │                       │                     │                 │                   │
  │  DELETE /admin/changes│                     │                 │                   │
  │  {org, project,       │                     │                 │                   │
  │   service}            │                     │                 │                   │
  ├──────────────────────▶│                     │                 │                   │
  │                       │                     │                 │                   │
  │                       ├─ 서비스 디렉토리     │                 │                   │
  │                       │  전체 삭제:          │                 │                   │
  │                       │  config.yaml        │                 │                   │
  │                       │  env_vars.yaml      │                 │                   │
  │                       │  secrets.yaml       │                 │                   │
  │                       │  sealed-secrets/    │                 │                   │
  │                       │                     │                 │                   │
  │                       ├─ Git commit&push ──▶│                 │                   │
  │                       ├─ 메모리에서 제거     │                 │                   │
  │◀── {version: "..."}───┤                     │                 │                   │
  │                       │                     │                 │                   │
  │  POST /admin/         │                     │                 │                   │
  │  app-registry/webhook │                     │                 │                   │
  │  {action: "delete"}   │                     │                 │                   │
  ├──────────────────────▶│                     │                 │                   │
  │                       ├─ App Registry       │                 │                   │
  │                       │  캐시에서 제거       │                 │                   │
  │◀── {status: "ok"} ───┤                     │                 │                   │
  │                       │                     │                 │                   │
  │                       │◀────────────────────────── poll (30초) ──────────────────┤
  │                       ├── {changed: true} ──────────────────────────────────────▶│
  │                       │                     │                 │  ConfigMap/Secret  │
  │                       │                     │                 │  정리 + Rolling    │
  │                       │                     │                 │  restart           │
```

> Console은 `DELETE /admin/changes`로 설정 삭제 후, `POST /admin/app-registry/webhook` (action: delete)로 App 등록 해제를 별도 호출한다. 두 호출은 Console의 프로비저닝 파이프라인(Console FR-7)에서 순차 실행된다.

---

## 5. 데이터 흐름 요약 `[FR-1]` `[FR-7]` `[FR-9]`

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
│                                    └── Git ──────▶ aap-helm-charts│
│                                                                 │
│  SealedSecret Controller ──▶ K8s Secret ──▶ kubelet ──▶ Pods    │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## 6. 설정 상속 (Config Inheritance) 구현 `[FR-10]`

### 6.1 개요

PRD 4.10에 정의된 설정 상속을 Config Server 내부에서 구현한다. `_defaults/common.yaml` 파일들을 계층적으로 merge하여 서비스별 최종 설정을 생성한다.

### 6.2 Merge 파이프라인

```
aap-helm-charts 로드 시 (시작 + 갱신):

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

### 6.3 메모리 구조 `[FR-1]`

```go
type Store struct {
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
    repo     GitRepo                      // interface 주입 (DI)
}
```

### 6.4 Merge 전략 `[FR-1]`

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
func DeepMerge(base, override map[string]any) map[string]any {
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
        baseMap, baseIsMap := baseVal.(map[string]any)
        overMap, overIsMap := overrideVal.(map[string]any)
        if baseIsMap && overIsMap {
            result[key] = DeepMerge(baseMap, overMap)  // 재귀 merge
        } else {
            result[key] = deepCopy(overrideVal)  // 스칼라/배열: 전체 교체
        }
    }
    return result
}
```

### 6.5 갱신 흐름 `[FR-1]`

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

상세 분석과 선택지 비교는 ADR 문서를 참조한다:

| 결정 | 채택 | ADR |
|------|------|-----|
| Config Agent debounce 전략 | Leading-edge Debounce | [ADR-001](./adr/001-config-agent-leading-edge-debounce.md) |
| Config Agent 배포 방식 | 중앙 집중형 (사이드카 대신) | [ADR-002](./adr/002-central-config-agent-vs-sidecar.md) |
| 동시 변경 처리 | 서비스별 Mutex + Pull-rebase | [ADR-003](./adr/003-concurrent-change-per-service-mutex.md) |
| 시크릿 저장 방식 | SealedSecret + Volume Mount | [ADR-004](./adr/004-secret-storage-sealedsecret-volume-mount.md) |

---

## 8. 의존성

> PRD 섹션 6 "비기능 요구사항 — 의존성" 참조. 주요 의존성:
> - **클러스터**: Bitnami SealedSecrets Controller (필수), K8s etcd encryption at rest (권장), Network Policy (권장)
> - **Config Server**: `aap-helm-charts` Git repo, AAP Console API, K8s API (client-go), Volume Mount, kubeseal
> - **Config Agent**: Config Server API, K8s API (ConfigMap/Secret/Deployment)

---

## 9. 보안 경계 `[FR-16]` `[FR-17]`

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

---

## 10. FR 추적 매트릭스

각 FR이 구현 시 관련되는 Go 패키지, API Handler, 외부 연동, Config Agent 컴포넌트를 매핑한다.

| FR | 설명 | Go 패키지 | API Handler | 외부 연동 | Config Agent |
|----|------|-----------|-------------|----------|--------------|
| **FR-1** | In-memory Config Store | `store` | — | Git (`go-git`) | — |
| **FR-2** | 설정 조회 API | `store`, `handler` | `GET /config` | — | — |
| **FR-3** | 환경변수 조회 API | `store`, `handler` | `GET /env_vars` | — | — |
| **FR-4** | 설정/시크릿 일괄 변경 | `store`, `gitops`, `seal` | `POST /admin/changes` | Git, kubeseal, K8s API | — |
| **FR-5** | 설정/시크릿 일괄 삭제 | `store`, `gitops` | `DELETE /admin/changes` | Git | — |
| **FR-6** | 변경 감지 API (watch) | `store`, `handler` | `GET /config/watch`, `GET /env_vars/watch` | — | watch client |
| **FR-7** | 시크릿 관리 | `seal`, `secret` | — | kubeseal, K8s API, Volume Mount | — |
| **FR-8** | App Registry 연동 | `registry` | `POST /admin/app-registry/webhook` | AAP Console API | — |
| **FR-9** | Config Agent | — | — | Config Server API, K8s API | `agent` 전체 |
| **FR-10** | 설정 상속 | `store`, `merge` | — | — | — |
| **FR-11** | 서비스 탐색 API | `store`, `handler` | `GET /orgs`, `/projects`, `/services` | — | — |
| **FR-12** | 시크릿 메타데이터 API | `store`, `handler` | `GET /secrets` | — | — |
| **FR-13** | 변경 이력 API | `gitops` | `GET /history` | Git | — |
| **FR-14** | 설정 롤백 API | `store`, `gitops`, `seal` | `POST /admin/changes/revert` | Git, kubeseal, K8s API | — |
| **FR-15** | 헬스체크 / 운영 API | `server` | `/healthz`, `/readyz`, `/status` | — | — |
| **FR-16** | 인증/인가 | `auth` | 미들웨어 (`Authorization: Bearer`) | 환경변수 `API_KEY` | — |
| **FR-17** | 시크릿 보안 | `secret`, `auth` | 미들웨어 | K8s Network Policy | — |

---

## 11. Go 설계 원칙

### 11.1 프로젝트 구조

Go 표준 프로젝트 레이아웃을 따른다. Module 이름: `github.com/aap/config-server`

```
aap-config-server/
├── cmd/
│   ├── config-server/        # Config Server 엔트리포인트
│   │   └── main.go
│   └── config-agent/         # Config Agent 엔트리포인트
│       └── main.go
│
├── internal/                 # 외부 import 불가 (캡슐화)
│   ├── server/               # HTTP 서버 설정, 라우터, graceful shutdown
│   ├── handler/              # HTTP 핸들러 (요청 파싱 → 서비스 호출 → 응답)
│   ├── store/                # In-memory Config Store (COW, RWMutex)
│   ├── apperror/             # 커스텀 에러 타입 (Code, Error, HTTP 매핑)
│   ├── parser/               # YAML 파서 (config.yaml, env_vars.yaml, secrets.yaml)
│   ├── gitops/               # Git clone/pull/commit/push (go-git 래핑)
│   ├── seal/                 # kubeseal 암호화 (interface 추상화)
│   ├── secret/               # Volume Mount 시크릿 로딩, resolve 로직
│   ├── merge/                # Deep merge (설정 상속)
│   ├── auth/                 # API Key Bearer 인증 미들웨어
│   ├── registry/             # App Registry 인메모리 캐시
│   ├── config/               # 서버 자체 설정 로딩 (환경변수/플래그)
│   └── agent/                # Config Agent 핵심 로직
│       ├── poller/           # Config Server polling
│       ├── applier/          # K8s ConfigMap/Secret/Deployment 업데이트
│       └── debounce/         # Leading-edge debounce
│
├── docs/                     # PRD, HLD, ADR 문서
├── go.mod
├── go.sum
├── Dockerfile
└── Makefile
```

**패키지 설계 원칙**:
- `internal/` 하위로 모든 비즈니스 로직을 배치하여 외부 패키지가 import할 수 없도록 캡슐화한다
- 각 패키지는 **하나의 명확한 책임**만 가진다
- 패키지 간 순환 의존성(circular dependency)을 허용하지 않는다
- `handler` → `store` → `parser` 방향의 단방향 의존 구조를 유지한다

### 11.2 의존성 주입 (Dependency Injection)

Go에서는 **constructor에 interface를 주입**하는 패턴을 사용한다. 프레임워크 없이 수동 DI로 구현한다.

```go
// internal/store/store.go — interface 정의는 사용하는 쪽에서
type GitRepo interface {
    Pull(ctx context.Context) (string, error)
    CommitAndPush(ctx context.Context, msg string, files []string) (string, error)
}

type Store struct {
    mu      sync.RWMutex
    configs map[string]*ResolvedConfig
    version string
    repo    GitRepo   // interface로 주입
}

func NewStore(repo GitRepo) *Store {
    return &Store{
        configs: make(map[string]*ResolvedConfig),
        repo:    repo,
    }
}
```

```go
// cmd/config-server/main.go — 조립 (Composition Root)
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    cfg := config.Load()

    repo := gitops.New(cfg.GitURL, cfg.GitBranch)
    st := store.NewStore(repo)
    h := handler.New(st)
    srv := server.New(cfg.Addr, h.Routes())

    if err := srv.Run(ctx); err != nil {
        slog.Error("server exited", "error", err)
        os.Exit(1)
    }
}
```

**원칙**:
- **Accept interfaces, return structs** — interface는 사용하는 패키지에서 정의한다
- `cmd/` 의 `main.go`가 유일한 Composition Root이다. 여기서만 구체 타입을 생성하고 주입한다
- 테스트에서는 interface를 mock/fake로 교체한다

### 11.3 context.Context 전파

모든 I/O 작업(HTTP 요청 처리, Git 작업, K8s API 호출, 파일 읽기)에 `context.Context`를 전파한다.

```go
// 모든 public 메서드의 첫 번째 파라미터는 context.Context
func (s *Store) GetConfig(ctx context.Context, org, project, service string) (*ResolvedConfig, error)
func (s *Store) ApplyChanges(ctx context.Context, req *ChangeRequest) (*ChangeResult, error)
func (r *Repo) CommitAndPush(ctx context.Context, msg string, files []string) (string, error)
```

**적용 범위**:
- HTTP handler: `r.Context()`에서 context를 추출하여 서비스 레이어에 전달
- Git 작업: `context.WithTimeout`으로 Git push 타임아웃 설정 (30초)
- K8s API 호출: `client-go`의 모든 메서드에 context 전달
- Graceful shutdown: `signal.NotifyContext`로 SIGINT/SIGTERM 수신 → 모든 진행 중 작업 취소

### 11.4 에러 처리

#### 커스텀 에러 타입

```go
// internal/apperror/error.go
package apperror

type Code string

const (
    CodeNotFound      Code = "NOT_FOUND"
    CodeConflict      Code = "CONFLICT"
    CodeValidation    Code = "VALIDATION_ERROR"
    CodeGitPush       Code = "GIT_PUSH_FAILED"
    CodeUnauthorized  Code = "UNAUTHORIZED"
    CodeInternal      Code = "INTERNAL"
)

type Error struct {
    Code    Code
    Message string
    Err     error   // 원본 에러 (래핑)
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }
```

#### 에러 → HTTP 응답 매핑

```go
// handler에서 errors.As로 타입 분기
var appErr *apperror.Error
if errors.As(err, &appErr) {
    switch appErr.Code {
    case apperror.CodeNotFound:
        http.Error(w, appErr.Message, http.StatusNotFound)
    case apperror.CodeValidation:
        http.Error(w, appErr.Message, http.StatusBadRequest)
    case apperror.CodeConflict:
        http.Error(w, appErr.Message, http.StatusConflict)
    case apperror.CodeUnauthorized:
        http.Error(w, appErr.Message, http.StatusUnauthorized)
    default:
        http.Error(w, "internal server error", http.StatusInternalServerError)
    }
    return
}
```

**원칙**:
- `fmt.Errorf("... : %w", err)`로 에러를 래핑하여 컨텍스트를 추가한다
- `errors.Is` / `errors.As`로 에러 타입을 검사한다
- 시크릿 값은 에러 메시지에 절대 포함하지 않는다 (secret_ref ID만 기록)
- 비즈니스 로직은 `apperror.Error`를 반환하고, handler가 HTTP 상태코드로 변환한다

### 11.5 서버 자체 설정 로딩

Config Server 자체의 설정(포트, Git URL 등)은 **환경변수 + 플래그**로 로드한다. Helm values → 환경변수 → Go 서버가 읽는 흐름이다.

```go
// internal/config/config.go
type ServerConfig struct {
    Addr          string        // HTTP 바인드 주소 (default: ":8080")
    GitURL        string        // aap-helm-charts 저장소 URL (필수)
    GitBranch     string        // Git 브랜치 (default: "main")
    GitPollInterval time.Duration // Git poll 주기 (default: 30s)
    APIKey        string        // Admin API 인증 키 (환경변수 API_KEY)
    SecretMountPath string      // Volume Mount 경로 (default: "/secrets")
    ConsoleAPIURL string        // AAP Console API URL (App Registry 로드)
    LogLevel      string        // slog 로그 레벨 (default: "info")
}

func Load() *ServerConfig {
    cfg := &ServerConfig{}
    flag.StringVar(&cfg.Addr, "addr", env("ADDR", ":8080"), "HTTP listen address")
    flag.StringVar(&cfg.GitURL, "git-url", env("GIT_URL", ""), "Git repository URL")
    flag.StringVar(&cfg.GitBranch, "git-branch", env("GIT_BRANCH", "main"), "Git branch")
    // ... 나머지 필드
    flag.Parse()
    return cfg
}

func env(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}
```

**원칙**:
- 환경변수가 우선, 플래그가 fallback (K8s 환경에서는 환경변수가 주 설정 수단)
- 필수 설정이 누락되면 시작 시 `log.Fatal`로 빠르게 실패한다
- 시크릿 설정(API_KEY)은 환경변수로만 받는다 (플래그는 `ps` 명령으로 노출 위험)

### 11.6 Graceful Shutdown

서버 시작부터 Phase 1에 포함한다. Go에서 graceful shutdown은 기본 요구사항이다.

```go
func (s *Server) Run(ctx context.Context) error {
    httpSrv := &http.Server{Addr: s.addr, Handler: s.handler}

    errCh := make(chan error, 1)
    go func() { errCh <- httpSrv.ListenAndServe() }()

    select {
    case err := <-errCh:
        return fmt.Errorf("server failed: %w", err)
    case <-ctx.Done():
        slog.Info("shutting down server")
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        return httpSrv.Shutdown(shutdownCtx)
    }
}
```

- `signal.NotifyContext`로 SIGINT/SIGTERM 감지
- `http.Server.Shutdown`으로 진행 중인 요청 완료 대기 (최대 10초)
- Git 동기화, K8s watcher 등 백그라운드 goroutine도 context 취소로 정리
