# 04 Implementation Plan

제품 gate, 기술 흐름, 구현 slice 상태를 한 곳에서 시퀀싱한다.

세부 tracking은 issue tracker가 맡고, 이 문서는 roadmap / status ledger의
canonical view만 유지한다. 구현 단계의 얇은 문서 레이어
(`docs/context/current-state.md`, `docs/current/`)에는 전체 roadmap inventory를
복제하지 않는다.

## Taxonomy

| Term | Meaning | Example ID | Notes |
|---|---|---|---|
| Milestone | 제품 / 사용자 관점의 delivery gate | `P0-M1` | "사용자가 어떤 상태를 얻는가"를 기준으로 정의 |
| Track | 기술 영역 또는 큰 구현 흐름 | `CORE` | api, data, runtime, ops 같은 영역 |
| Phase | track 안의 구현 단계 | `CORE-1A` | 같은 track 안에서 순서가 있는 단계 |
| Slice / Task | 커밋 가능한 구현 단위 | `CORE-1A.1` | PR / commit / issue와 연결 가능한 크기 |
| Gate | 검증 / acceptance 기준 | `AC-001` / `TEST-001` | `06_ACCEPTANCE_TESTS.md` 또는 테스트 위치로 연결 |
| Evidence | 완료를 뒷받침하는 근거 | PR, code, tests, current docs | 본문 복제 대신 링크 / ID로 남김 |

## Status vocabulary

Implementation status:

| Status | Meaning |
|---|---|
| `planned` | 계획됨. 아직 시작 조건이 충족되지 않음 |
| `ready` | 시작 가능. dependency와 scope가 충분히 정리됨 |
| `in_progress` | 구현 또는 문서 작업 진행 중 |
| `landed` | 코드 / 문서 변경이 반영됨 |
| `accepted` | gate를 통과했고 milestone 기준으로 수용됨 |
| `blocked` | blocker 때문에 진행 불가 |
| `deferred` | 의도적으로 뒤로 미룸 |
| `dropped` | 하지 않기로 함 |

Gate status:

| Status | Meaning |
|---|---|
| `defined` | 기준은 정의됐지만 아직 실행하지 않음 |
| `not_run` | 실행 대상이지만 아직 실행하지 않음 |
| `passing` | 통과 |
| `failing` | 실패 |
| `waived` | 명시적 사유로 면제 |

## Milestones

| Milestone | Product / user gate | Target date | Status | Gate | Evidence | Notes |
|---|---|---|---|---|---|---|
| `P0-M1` | Phase-1 Config Server MVP serves Git-backed config/env data and supports admin config/env writes. |  | `landed` | `AC-001`~`AC-008` | `cmd/config-server`, `internal/*`, `README.md` | Existing implementation predates this ledger. |
| `P0-M2` | Operational hardening for degraded state, auth, reload, and dirty checkout safety. |  | `landed` | `AC-009`~`AC-013` | `internal/store`, `internal/handler`, `internal/gitops` | Some PRD phase labels differ from actual landing order. |
| `P0-M3` | Documentation system migrated to boilerplate structure. |  | `in_progress` | `AC-014`, `AC-015` | `docs/00_*`, `docs/current/*`, `AGENTS.md`, `.github/` | Active migration slice. |
| `P1-M1` | Secret write/resolve with SealedSecret and K8s apply. |  | `planned` | `AC-020` | ADR-004 | Target design only. |
| `P1-M2` | Config Agent rollout path. |  | `planned` | `AC-030` | ADR-001, ADR-002 | Target design only. |
| `P1-M3` | Watch/history/revert/inheritance/metrics operational features. |  | `planned` | `AC-040` | `docs/01_PRD.md` | Target design only. |

## Tracks

| Track | Purpose | Active phase | Status | Notes |
|---|---|---|---|---|
| `CORE` | Core Config Server runtime, parser, store, Git sync, read/write APIs. | `CORE-1A` | `landed` | Code exists in `cmd/` and `internal/`. |
| `OPS` | Auth, readiness, degraded state, reload, CI/runtime operations. | `OPS-1A` | `landed` | Runtime docs now live under `docs/current/`. |
| `DOC` | Boilerplate documentation migration and status ledger. | `DOC-1A` | `in_progress` | Current active work. |
| `SECRET` | Secret write/resolve and SealedSecret integration. | `SECRET-1A` | `planned` | Planned. |
| `AGENT` | Config Agent and rollout orchestration. | `AGENT-1A` | `planned` | Planned. |
| `EXT` | Watch/history/revert/inheritance/metrics extensions. | `EXT-1A` | `planned` | Planned. |

## Phases / Slices

| Slice | Milestone | Track | Phase | Goal | Depends | Gate | Gate status | Status | Evidence | Next |
|---|---|---|---|---|---|---|---|---|---|---|
| `CORE-1A.1` | `P0-M1` | `CORE` | `CORE-1A` | Runtime config load and validation. |  | `AC-001` / `TEST-001` | `not_run` | `landed` | `internal/config` | Run gate in an environment with Go installed. |
| `CORE-1A.2` | `P0-M1` | `CORE` | `CORE-1A` | YAML parsing for config/env/secrets metadata. | `CORE-1A.1` | `AC-002` / `TEST-002` | `not_run` | `landed` | `internal/parser` | Run gate in an environment with Go installed. |
| `CORE-1A.3` | `P0-M1` | `CORE` | `CORE-1A` | Git-backed store with atomic snapshot reload. | `CORE-1A.2` | `AC-003` / `TEST-003` | `not_run` | `landed` | `internal/store`, `internal/gitops` | Run gate in an environment with Go installed. |
| `CORE-1A.4` | `P0-M1` | `CORE` | `CORE-1A` | Read/discovery APIs from memory. | `CORE-1A.3` | `AC-004` / `TEST-004` | `not_run` | `landed` | `internal/handler` | Run gate in an environment with Go installed. |
| `CORE-1A.5` | `P0-M1` | `CORE` | `CORE-1A` | Admin write/delete for config/env files. | `CORE-1A.3` | `AC-005` / `TEST-005` | `not_run` | `landed` | `internal/store`, `internal/handler` | Run gate in an environment with Go installed. |
| `OPS-1A.1` | `P0-M2` | `OPS` | `OPS-1A` | API key auth for admin and secret metadata endpoints. | `CORE-1A.4` | `AC-006` / `TEST-006` | `not_run` | `landed` | `internal/handler`, `internal/config` | Run gate in an environment with Go installed. |
| `OPS-1A.2` | `P0-M2` | `OPS` | `OPS-1A` | Degraded state, last-known-good snapshot, force reload. | `CORE-1A.3` | `AC-007` / `TEST-007` | `not_run` | `landed` | `internal/store`, `internal/handler` | Run gate in an environment with Go installed. |
| `OPS-1A.3` | `P0-M2` | `OPS` | `OPS-1A` | Dirty `configs/` checkout reload protection. | `CORE-1A.3` | `AC-008` / `TEST-008` | `not_run` | `landed` | `internal/gitops` | Run gate in an environment with Go installed. |
| `DOC-1A.1` | `P0-M3` | `DOC` | `DOC-1A` | Add boilerplate docs and move PRD/HLD to numbered canonical files. |  | `AC-014` | `not_run` | `landed` | `docs/`, `AGENTS.md` | Go test blocked locally because `go` is not installed. |
| `DOC-1A.2` | `P0-M3` | `DOC` | `DOC-1A` | Add PR template and doc freshness soft-check for Go source paths. | `DOC-1A.1` | `AC-015` | `passing` | `landed` | `.github/pull_request_template.md`, `.github/workflows/doc-freshness.yml` | YAML parsed locally with Ruby. |
| `SECRET-1A.1` | `P1-M1` | `SECRET` | `SECRET-1A` | Implement secret write acceptance and explicit value handling. | `OPS-1A.1` | `AC-020` | `defined` | `planned` | ADR-004 | Define implementation slices. |
| `AGENT-1A.1` | `P1-M2` | `AGENT` | `AGENT-1A` | Implement Config Agent polling/apply/restart path. | `SECRET-1A.1` | `AC-030` | `defined` | `planned` | ADR-001, ADR-002 | Define implementation slices. |
| `EXT-1A.1` | `P1-M3` | `EXT` | `EXT-1A` | Implement watch/history/revert/inheritance/metrics backlog. | `CORE-1A` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` | Prioritize backlog. |

## Gates / Acceptance

- Gate definitions live in `06_ACCEPTANCE_TESTS.md`.
- Automated checks are listed in `docs/current/TESTING.md`.
- A slice can be `landed` before its gate is `passing`.
- A milestone is `accepted` only when its required gates are passing or explicitly waived.

## Traceability

- Completed slices should have a row in `09_TRACEABILITY_MATRIX.md`.
- Link slices to the relevant Q / DEC / ADR, FR / NFR, AC / TEST, and milestone.
- Do not use trace rows as a backlog. They are connection records for important paths.

## Dependencies

- External systems: Git repository referenced by `GIT_URL`.
- Libraries / vendors: `go-git`, `yaml.v3`, Go standard library HTTP stack.
- Planned: Kubernetes API, Bitnami SealedSecrets, Config Agent runtime.

## Risks (open)

- `Q-001`: Requirement ID migration strategy (`FR-*` vs `REQ-*`).
- `Q-002`: ADR-003 service mutex vs current global git mutex.
- `Q-003`: Deployment ownership for Helm/K8s manifests.

## Capacity / Timeline

- Owner: project maintainers.
- Target dates: not currently assigned.
- Update cadence: adjust this ledger whenever roadmap, slice, gate, or evidence changes.
