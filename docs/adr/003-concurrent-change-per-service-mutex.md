# ADR-003: 동시 변경 처리 — 서비스별 Mutex

- **Status**: Accepted
- **Date**: 2026-03-12
- **Related**: PRD FR-4 (설정/시크릿 일괄 변경 Admin API)

## Context

Console에서 여러 사용자가 거의 동시에 설정을 변경하면, Config Server가 `POST /admin/changes`를 병렬로 처리하면서 Git push 충돌이 발생할 수 있다.

```
User A: POST /admin/changes (litellm config 변경)
User B: POST /admin/changes (litellm env_vars 변경)
         ↓
Config Server: 두 요청 동시 처리
  A: 파일 수정 → git commit → git push ✓
  B: 파일 수정 → git commit → git push ✗ (rejected!)
```

이 충돌을 어떻게 처리할 것인가?

## Decision Drivers

- Git push 충돌으로 사용자 요청이 실패하면 안 됨
- 같은 서비스의 같은 설정을 동시에 수정하면 데이터 유실 위험
- 다른 서비스 간 변경은 병렬 처리하여 성능 유지
- 해결 방식이 단순하고 예측 가능해야 함

## Considered Options

### Option 1: Optimistic Concurrency (version 기반)

요청에 `expected_version` 필드를 포함시켜, 서버의 현재 version과 다르면 `409 Conflict` 반환. Console이 최신 설정을 다시 fetch하고 재시도.

- **장점**: lock-free, 높은 동시성
- **단점**:
  - Console에 재시도 로직 필요 (복잡도 증가)
  - 다른 서비스의 변경으로도 version이 바뀌므로, 실제 충돌이 없는데도 409 발생 가능 (false positive)
  - 사용자가 "저장" 후 "충돌, 다시 시도하세요" 메시지를 보는 UX 문제

### Option 2: 글로벌 Mutex

모든 `POST /admin/changes` 요청을 단일 mutex로 직렬화.

- **장점**: 구현이 가장 단순, Git 충돌 원천 차단
- **단점**: 다른 서비스 간 변경도 직렬화되어 불필요한 대기 발생. 서비스 수가 많아지면 병목

### Option 3: 서비스별 Mutex + Pull-rebase (선택)

같은 서비스 경로(`org/project/service`)에 대한 요청은 mutex로 직렬화. 다른 서비스 간 요청은 병렬 처리하되, `git push` rejected 시 pull-rebase로 자동 해결.

- **장점**: 같은 서비스 내 충돌 원천 차단. 다른 서비스 간 병렬 처리 유지
- **단점**: pull-rebase 로직 구현 필요

## Decision

**Option 3: 서비스별 Mutex + Pull-rebase**를 채택한다.

### 핵심 근거: Git 충돌은 구조적으로 불가능하다

처음에는 Option 1의 `409 Conflict` 응답까지 설계했으나, 분석 결과 **충돌이 발생할 수 있는 경로가 존재하지 않음**을 발견했다:

| 상황 | Mutex | 수정 파일 | 충돌 가능성 |
|------|-------|----------|------------|
| 같은 서비스 | 같은 mutex → 직렬화 | 같은 파일 | **불가** (순차 처리) |
| 다른 서비스 | 다른 mutex → 병렬 | 다른 파일 | **불가** (파일이 다름) |

서비스별 mutex가 같은 서비스를 직렬화하고, 다른 서비스는 `configs/orgs/{org}/projects/{proj}/services/{svc}/` 하위의 서로 다른 파일을 수정하므로 Git 충돌이 발생할 수 없다. Pull-rebase는 다른 서비스 간 `git push` rejected를 해결하기 위한 것이며, 파일이 다르므로 **항상 자동 성공**한다.

### Mutex 범위

Mutex는 **Git 작업만** 보호한다:

```
mutex.Lock()
  ├─ 파일 수정 (~ms)
  ├─ git commit (~ms)
  ├─ git push (~1-2초)
  └─ 메모리 갱신 (~ms)
mutex.Unlock()
  ↓
API 즉시 응답 (200 OK)    ← 여기서 Console에 응답
  ↓ (비동기, mutex 밖)
Config Agent polling → rolling restart (~2-3분)
```

API 응답 시간은 mutex 보유 시간(~1-2초)에만 영향받으며, rolling restart는 완전 비동기이다.

## Consequences

### 긍정적

- Git 충돌이 구조적으로 불가능 → 409 Conflict 응답 불필요 → Console 재시도 로직 불필요
- 같은 서비스: 완전 직렬화로 데이터 일관성 보장
- 다른 서비스: 병렬 처리 유지, pull-rebase로 항상 자동 해결
- Mutex 보유 시간이 짧아(~1-2초) API 응답 지연이 최소

### 부정적

- 같은 서비스에 대한 동시 요청은 순차 처리되므로, 극단적으로 많은 동시 요청 시 대기 시간 증가
  - 완화: 설정 변경은 빈도가 낮은 작업(분~시간 단위)이므로 실제로 문제되지 않음

### 설계 과정에서 제거한 것

- `409 Conflict` 응답 및 관련 에러 형식 — 발생할 수 없는 시나리오에 대한 처리이므로 제거
- Optimistic concurrency의 `expected_version` 파라미터 — 불필요
