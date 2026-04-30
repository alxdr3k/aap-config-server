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
| `P0-M1` | Phase-1 Config Server MVP serves Git-backed config/env data and supports admin config/env writes. |  | `accepted` | `AC-001`~`AC-005` | `cmd/config-server`, `internal/*`, `README.md`, PR #10 CI | Existing implementation predates this ledger. |
| `P0-M2` | Operational hardening for auth, degraded state, reload, dirty checkout safety, and fail-closed admin write decoding. |  | `accepted` | `AC-006`~`AC-009` | `internal/store`, `internal/handler`, `internal/gitops`, PR #10 CI | Some PRD phase labels differ from actual landing order. Secret write support is tracked by `SECRET-1A.6`. |
| `P0-M3` | Documentation system migrated to boilerplate structure. |  | `accepted` | `AC-014`, `AC-015` | `docs/00_*`, `docs/current/*`, `AGENTS.md`, `.github/`, PR #10 | Migration landed on main. |
| `P1-M1` | Secret write/resolve with SealedSecret, K8s apply, and Console App Registry integration. |  | `accepted` | `AC-020`, `AC-021` | ADR-004, `internal/secret`, `internal/registry`, `internal/handler` | Secret path and App Registry integration landed. |
| `P1-M2` | Config Agent rollout path. |  | `in_progress` | `AC-030` | ADR-001, ADR-002 | `AGENT-1A.7` landed; `AGENT-1A.8` is ready next. |
| `P1-M3` | Console integration extensions and production hardening. |  | `planned` | `AC-040`~`AC-042` | `docs/01_PRD.md`, `DEC-003` | Leaf slices defined. |

## Tracks

| Track | Purpose | Active phase | Status | Notes |
|---|---|---|---|---|
| `CORE` | Core Config Server runtime, parser, store, Git sync, read/write APIs. | `CORE-1A` | `accepted` | Code exists in `cmd/` and `internal/`; CI passed on PR #10. |
| `OPS` | Auth, readiness, degraded state, reload, CI/runtime operations. | `OPS-1A` | `accepted` | Runtime docs now live under `docs/current/`; CI passed on PR #10. |
| `DOC` | Boilerplate documentation migration and status ledger. | `DOC-1A` | `accepted` | Landed through PR #10. |
| `SECRET` | Secret write/resolve and SealedSecret integration. | `SECRET-1A` | `accepted` | Runtime boundaries, volume reader, deterministic SealedSecret YAML generation, public-key encryption, admin secret writes, K8s apply, secret value resolve, and audit hardening landed. |
| `REGISTRY` | AAP Console App Registry bootstrap and webhook cache. | `REGISTRY-1A` | `accepted` | Startup bootstrap, webhook cache updates, and status observability landed. |
| `AGENT` | Config Agent and rollout orchestration. | `AGENT-1A` | `in_progress` | Agent bootstrap, leader election, read polling, rendering, ConfigMap/Secret apply, rollout patch, and debounce landed; later slices remain planned until their direct dependencies land. |
| `EXT` | Watch, history, revert, inheritance, batch, webhook, metrics, and HTTP response extensions. | `EXT-1A`~`EXT-1D` | `planned` | Planned. |
| `HARDEN` | Schema validation, rate limiting, integration/load tests, and deployment handoff docs. | `HARDEN-1A` | `planned` | Planned. |

## Phases / Slices

| Slice | Milestone | Track | Phase | Goal | Depends | Gate | Gate status | Status | Evidence | Next |
|---|---|---|---|---|---|---|---|---|---|---|
| `CORE-1A.1` | `P0-M1` | `CORE` | `CORE-1A` | Runtime config load and validation. |  | `AC-001` / `TEST-001` | `passing` | `accepted` | `internal/config`, PR #10 CI |  |
| `CORE-1A.2` | `P0-M1` | `CORE` | `CORE-1A` | YAML parsing for config/env/secrets metadata. | `CORE-1A.1` | `AC-002` / `TEST-002` | `passing` | `accepted` | `internal/parser`, PR #10 CI |  |
| `CORE-1A.3` | `P0-M1` | `CORE` | `CORE-1A` | Git-backed store with atomic snapshot reload. | `CORE-1A.2` | `AC-003` / `TEST-003` | `passing` | `accepted` | `internal/store`, `internal/gitops`, PR #10 CI |  |
| `CORE-1A.4` | `P0-M1` | `CORE` | `CORE-1A` | Read/discovery APIs from memory. | `CORE-1A.3` | `AC-004` / `TEST-004` | `passing` | `accepted` | `internal/handler`, PR #10 CI |  |
| `CORE-1A.5` | `P0-M1` | `CORE` | `CORE-1A` | Admin write/delete for config/env files. | `CORE-1A.3` | `AC-005` / `TEST-005` | `passing` | `accepted` | `internal/store`, `internal/handler`, PR #10 CI |  |
| `OPS-1A.1` | `P0-M2` | `OPS` | `OPS-1A` | API key auth for admin and secret metadata endpoints. | `CORE-1A.4` | `AC-006` / `TEST-006` | `passing` | `accepted` | `internal/handler`, `internal/config`, PR #10 CI |  |
| `OPS-1A.2` | `P0-M2` | `OPS` | `OPS-1A` | Degraded state, last-known-good snapshot, force reload. | `CORE-1A.3` | `AC-007` / `TEST-007` | `passing` | `accepted` | `internal/store`, `internal/handler`, PR #10 CI |  |
| `OPS-1A.3` | `P0-M2` | `OPS` | `OPS-1A` | Dirty `configs/` checkout reload protection. | `CORE-1A.3` | `AC-008` / `TEST-008` | `passing` | `accepted` | `internal/gitops`, PR #10 CI |  |
| `OPS-1A.4` | `P0-M2` | `OPS` | `OPS-1A` | Reject unknown admin write fields fail-closed instead of silently dropping data. | `CORE-1A.5` | `AC-009` / `TEST-009` | `passing` | `accepted` | `internal/handler`, PR #10 CI | Historical `secrets` rejection was superseded by `SECRET-1A.6`. |
| `DOC-1A.1` | `P0-M3` | `DOC` | `DOC-1A` | Add boilerplate docs and move PRD/HLD to numbered canonical files. |  | `AC-014` | `passing` | `accepted` | `docs/`, `AGENTS.md`, PR #10 |  |
| `DOC-1A.2` | `P0-M3` | `DOC` | `DOC-1A` | Add PR template and doc freshness soft-check for Go source paths. | `DOC-1A.1` | `AC-015` | `passing` | `accepted` | `.github/pull_request_template.md`, `.github/workflows/doc-freshness.yml`, PR #10 |  |
| `SECRET-1A.1` | `P1-M1` | `SECRET` | `SECRET-1A` | Add secret runtime config, interfaces, and dependency boundaries for volume reads, sealing, K8s apply, and audit logging. | `OPS-1A.1` | `AC-020` | `passing` | `landed` | `internal/config`, `internal/secret`, `docs/current/*` |  |
| `SECRET-1A.2` | `P1-M1` | `SECRET` | `SECRET-1A` | Implement Volume Mount secret reader and fsnotify-backed refresh for mounted secret files. | `SECRET-1A.1` | `AC-020` | `passing` | `landed` | `internal/secret/volume.go`, `internal/secret/volume_test.go` |  |
| `SECRET-1A.3` | `P1-M1` | `SECRET` | `SECRET-1A` | Implement SealedSecret generation adapter and deterministic YAML output for secret payloads. | `SECRET-1A.1` | `AC-020` | `passing` | `landed` | `internal/secret/sealed.go`, `internal/secret/sealed_test.go` |  |
| `SECRET-1A.4` | `P1-M1` | `SECRET` | `SECRET-1A` | Implement K8s apply adapter for SealedSecret objects with context-aware error handling. | `SECRET-1A.3` | `AC-020` | `passing` | `landed` | `internal/secret/apply.go`, `internal/secret/apply_test.go` |  |
| `SECRET-1A.5` | `P1-M1` | `SECRET` | `SECRET-1A` | Implement SealedSecret controller public-key lookup and encryptor wiring for deterministic SealedSecret generation. | `SECRET-1A.3` | `AC-020` | `passing` | `landed` | `internal/secret/encrypt.go`, `internal/secret/encrypt_test.go` |  |
| `SECRET-1A.6` | `P1-M1` | `SECRET` | `SECRET-1A` | Accept `secrets` in admin writes, write metadata plus SealedSecret files in one Git commit, apply to K8s, and reload outcome explicitly. | `SECRET-1A.2`, `SECRET-1A.4`, `SECRET-1A.5`, `CORE-1A.5` | `AC-020` | `passing` | `landed` | `internal/store`, `internal/handler`, `cmd/config-server` |  |
| `SECRET-1A.7` | `P1-M1` | `SECRET` | `SECRET-1A` | Implement `resolve_secrets=true` for env var reads with auth, Volume Mount lookup, `Cache-Control: no-store`, and no ETag. | `SECRET-1A.2`, `SECRET-1A.6` | `AC-020` | `passing` | `landed` | `internal/handler`, `cmd/config-server` |  |
| `SECRET-1A.8` | `P1-M1` | `SECRET` | `SECRET-1A` | Add secret audit logging, no-plaintext log assertions, and best-effort memory cleanup for secret handling paths. | `SECRET-1A.6`, `SECRET-1A.7` | `AC-020` | `passing` | `landed` | `internal/secret/audit.go`, `internal/store`, `internal/handler` |  |
| `REGISTRY-1A.1` | `P1-M1` | `REGISTRY` | `REGISTRY-1A` | Add AAP Console API client, runtime config, startup registry load, and bounded exponential backoff. | `OPS-1A.1` | `AC-021` | `passing` | `landed` | `internal/registry`, `internal/config`, `cmd/config-server` |  |
| `REGISTRY-1A.2` | `P1-M1` | `REGISTRY` | `REGISTRY-1A` | Add authenticated App Registry webhook endpoint and in-memory cache update semantics. | `REGISTRY-1A.1` | `AC-021` | `passing` | `landed` | `internal/handler`, `internal/registry` |  |
| `REGISTRY-1A.3` | `P1-M1` | `REGISTRY` | `REGISTRY-1A` | Integrate registry load/cache state into readiness, status, and operations docs. | `REGISTRY-1A.2`, `OPS-1A.2` | `AC-021` | `passing` | `landed` | `internal/handler`, `internal/registry`, `docs/current/*` |  |
| `AGENT-1A.1` | `P1-M2` | `AGENT` | `AGENT-1A` | Add Config Agent binary, runtime config, Config Server API client, and local dry-run mode. | `SECRET-1A.7` | `AC-030` | `passing` | `landed` | `cmd/config-agent`, `internal/agent`, `Makefile` |  |
| `AGENT-1A.2` | `P1-M2` | `AGENT` | `AGENT-1A` | Implement K8s Lease leader election with standby takeover behavior. | `AGENT-1A.1` | `AC-030` | `passing` | `landed` | `internal/agent` |  |
| `AGENT-1A.3` | `P1-M2` | `AGENT` | `AGENT-1A` | Implement config/env fetch loop, version tracking, and retry/backoff behavior using read API polling. | `AGENT-1A.1` | `AC-030` | `passing` | `landed` | `internal/agent` |  |
| `AGENT-1A.4` | `P1-M2` | `AGENT` | `AGENT-1A` | Render native service config and `env.sh` payloads while preserving secret references in ConfigMaps. | `AGENT-1A.3` | `AC-030` | `passing` | `landed` | `internal/agent` |  |
| `AGENT-1A.5` | `P1-M2` | `AGENT` | `AGENT-1A` | Apply target ConfigMap and Secret resources with create/update/patch behavior constrained to configured resource names. | `AGENT-1A.4` | `AC-030` | `passing` | `landed` | `internal/agent` |  |
| `AGENT-1A.6` | `P1-M2` | `AGENT` | `AGENT-1A` | Patch target Deployment annotations to trigger controlled rolling restarts. | `AGENT-1A.5` | `AC-030` | `passing` | `landed` | `internal/agent` |  |
| `AGENT-1A.7` | `P1-M2` | `AGENT` | `AGENT-1A` | Implement leading-edge debounce with cooldown, quiet period, and max-wait controls. | `AGENT-1A.6` | `AC-030` | `passing` | `landed` | `internal/agent` |  |
| `AGENT-1A.8` | `P1-M2` | `AGENT` | `AGENT-1A` | Add Config Agent image build, RBAC/deployment examples, and e2e smoke coverage with fake K8s/client dependencies. | `AGENT-1A.7` | `AC-030` | `defined` | `ready` | ADR-001, ADR-002, `DEC-003` | Start here next. |
| `EXT-1A.1` | `P1-M3` | `EXT` | `EXT-1A` | Add store notification and version-wait primitive for long-poll watch endpoints. | `CORE-1A.3` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1A.2` | `P1-M3` | `EXT` | `EXT-1A` | Implement `config/watch` long-poll endpoint with timeout and version mismatch behavior. | `EXT-1A.1`, `CORE-1A.4` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1A.3` | `P1-M3` | `EXT` | `EXT-1A` | Implement `env_vars/watch` long-poll endpoint with timeout and independent change detection. | `EXT-1A.1`, `CORE-1A.4` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1B.1` | `P1-M3` | `EXT` | `EXT-1B` | Add Git history iterator and file-change classifier for service-scoped history. | `CORE-1A.3` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1B.2` | `P1-M3` | `EXT` | `EXT-1B` | Implement history API with `file`, `limit`, and `before` filtering. | `EXT-1B.1`, `CORE-1A.4` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1B.3` | `P1-M3` | `EXT` | `EXT-1B` | Add versioned config/env reads from historical Git commits. | `EXT-1B.1`, `CORE-1A.4` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1B.4` | `P1-M3` | `EXT` | `EXT-1B` | Validate revert targets and restore service files from a selected commit without mutating history. | `EXT-1B.3`, `CORE-1A.5` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1B.5` | `P1-M3` | `EXT` | `EXT-1B` | Implement revert commit/push/reload flow, including SealedSecret rollback apply when secret files are restored. | `EXT-1B.4`, `SECRET-1A.4` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1C.1` | `P1-M3` | `EXT` | `EXT-1C` | Parse global/org/project `_defaults/common.yaml` files and expose inherited source metadata for tests. | `CORE-1A.2` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1C.2` | `P1-M3` | `EXT` | `EXT-1C` | Implement deep merge with scalar override, recursive map merge, array replacement, and null deletion. | `EXT-1C.1` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1C.3` | `P1-M3` | `EXT` | `EXT-1C` | Apply `inherit=true/false` query semantics to config and env var read paths. | `EXT-1C.2`, `CORE-1A.4` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1C.4` | `P1-M3` | `EXT` | `EXT-1C` | Preserve service-level admin write behavior while inherited reads are enabled, with docs and regression tests. | `EXT-1C.3`, `CORE-1A.5` | `AC-040` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1D.1` | `P1-M3` | `EXT` | `EXT-1D` | Add ETag and `If-None-Match` support for non-secret config/env responses. | `CORE-1A.4`, `SECRET-1A.7` | `AC-041` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1D.2` | `P1-M3` | `EXT` | `EXT-1D` | Add gzip response compression for eligible read APIs. | `EXT-1D.1` | `AC-041` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1D.3` | `P1-M3` | `EXT` | `EXT-1D` | Implement batch config/env read API for multiple services. | `EXT-1C.3`, `CORE-1A.4` | `AC-041` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1D.4` | `P1-M3` | `EXT` | `EXT-1D` | Add Prometheus metrics for reloads, Git operations, API latency, watch waits, and degraded state. | `OPS-1A.2`, `EXT-1A.3` | `AC-041` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `EXT-1D.5` | `P1-M3` | `EXT` | `EXT-1D` | Add authenticated Git webhook trigger for immediate refresh after config repo changes. | `OPS-1A.1`, `OPS-1A.2` | `AC-041` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `HARDEN-1A.1` | `P1-M3` | `HARDEN` | `HARDEN-1A` | Add explicit schema validation layer for config, env vars, defaults, and secret metadata files. | `EXT-1C.1`, `SECRET-1A.6` | `AC-042` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `HARDEN-1A.2` | `P1-M3` | `HARDEN` | `HARDEN-1A` | Add configurable rate limiting for admin, secret resolve, watch, and batch endpoints. | `OPS-1A.1`, `EXT-1D.3` | `AC-042` | `defined` | `planned` | `docs/01_PRD.md` |  |
| `HARDEN-1A.3` | `P1-M3` | `HARDEN` | `HARDEN-1A` | Build integration test harness with fake Git, fake K8s, and fake Console dependencies. | `SECRET-1A.8`, `REGISTRY-1A.3`, `AGENT-1A.8` | `AC-042` | `defined` | `planned` | `docs/development-process.md` |  |
| `HARDEN-1A.4` | `P1-M3` | `HARDEN` | `HARDEN-1A` | Add load/concurrency test profiles for admin writes, watch waits, and Config Agent polling. | `HARDEN-1A.3`, `EXT-1A.3` | `AC-042` | `defined` | `planned` | `docs/development-process.md` |  |
| `HARDEN-1A.5` | `P1-M3` | `HARDEN` | `HARDEN-1A` | Finalize deployment handoff docs for image, env vars, network policy expectations, and external manifest ownership. | `HARDEN-1A.4`, `DEC-003` | `AC-042` | `defined` | `planned` | `docs/05_RUNBOOK.md`, `docs/current/OPERATIONS.md` |  |

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
- Planned: AAP Console API, Kubernetes API, Bitnami SealedSecrets,
  Config Agent runtime, and Prometheus metrics.

## Risks (open)

- None currently.

## Capacity / Timeline

- Owner: project maintainers.
- Target dates: not currently assigned.
- Update cadence: adjust this ledger whenever roadmap, slice, gate, or evidence changes.
