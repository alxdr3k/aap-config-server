# 06 Acceptance Tests

요구사항이 만족되었는지 검증하는 기준.

Implementation status는 `04_IMPLEMENTATION_PLAN.md`가 관리한다. 이 문서는
gate / acceptance 상태만 관리한다.

## AC Format

각 AC는 다음 형태를 권장:

```text
Given <초기 상태>
When  <행동>
Then  <기대 결과>
```

## Criteria

| ID | Requirement | Scenario | Verification | Status |
|---|---|---|---|---|
| `AC-001` | `FR-1`, `FR-16` | Given server config is missing required fields, When validation runs, Then startup fails closed. | `TEST-001` | `passing` |
| `AC-002` | `FR-1`, `FR-3`, `FR-12` | Given valid/invalid YAML files, When parsers run, Then valid metadata loads and invalid YAML/required-field gaps fail. | `TEST-002` | `passing` |
| `AC-003` | `FR-1` | Given a config repo, When the store loads or refreshes, Then it serves a complete atomic snapshot and never swaps on parse failure. | `TEST-003` | `passing` |
| `AC-004` | `FR-2`, `FR-3`, `FR-11`, `FR-12`, `FR-15` | Given a loaded snapshot, When read/discovery/status endpoints are called, Then responses match the current in-memory view. | `TEST-004` | `passing` |
| `AC-005` | `FR-4`, `FR-5` | Given an authenticated admin write/delete, When the request is valid, Then Git files are committed/pushed and reload outcome is explicit. | `TEST-005` | `passing` |
| `AC-006` | `FR-16`, `FR-17` | Given protected endpoints, When credentials are missing or invalid, Then the server returns 401; valid Bearer or `X-API-Key` succeeds. | `TEST-006` | `passing` |
| `AC-007` | `FR-1`, `FR-15` | Given a failed reload after a good snapshot, When readiness/status are queried, Then the server reports degraded while serving last-known-good data. | `TEST-007` | `passing` |
| `AC-008` | `FR-1` | Given the local `configs/` worktree is dirty outside server writes, When snapshot reload runs, Then reload fails closed. | `TEST-008` | `passing` |
| `AC-009` | `FR-4` | Given a Phase-1 admin write body includes `secrets`, When decoded, Then the request fails with 400 instead of silently dropping data. | `TEST-009` | `passing` |
| `AC-014` | Documentation migration | Given a new session, When it follows `AGENTS.md`, Then current status, code map, testing, runtime, and roadmap are discoverable from canonical docs. | manual link check + PR #10 CI | `passing` |
| `AC-015` | Documentation workflow | Given a PR changes Go source/runtime paths, When doc freshness runs, Then it comments with matching doc update candidates without blocking merge. | workflow YAML parse + pattern review | `passing` |
| `AC-020` | `FR-7`, `FR-17` | Secret write/resolve handles SealedSecret generation, K8s apply, no-store response, and audit logging. | future tests | `defined` |
| `AC-021` | `FR-8` | App Registry bootstrap and webhook cache keep service registration state available to readiness/status and recover from missed webhooks. | future tests | `defined` |
| `AC-030` | `FR-9` | Config Agent detects config changes, updates K8s resources, and triggers controlled rollout. | future tests | `defined` |
| `AC-040` | `FR-6`, `FR-10`, `FR-13`, `FR-14` | Watch/history/revert/inheritance features satisfy target PRD contracts. | future tests | `defined` |
| `AC-041` | operational extensions | ETag, gzip, batch reads, Prometheus metrics, and Git webhook refresh satisfy target PRD contracts without exposing secret plaintext. | future tests | `defined` |
| `AC-042` | production hardening | Schema validation, rate limiting, integration/load test harnesses, and deployment handoff docs are complete for the target architecture. | future tests | `defined` |

## Status vocabulary

| Status | Meaning |
|---|---|
| `defined` | 기준은 정의됐지만 아직 실행하지 않음 |
| `not_run` | 실행 대상이지만 아직 실행하지 않음 |
| `passing` | 통과 |
| `failing` | 실패 |
| `waived` | 명시적 사유로 면제 |

`pending`처럼 모호한 상태는 쓰지 않는다. 기능이 구현되지 않은 상태인지,
staging / manual acceptance가 아직 실행되지 않은 상태인지 분리한다.

## Tests

| ID | Name | Location | Covers |
|---|---|---|---|
| `TEST-001` | Config validation tests | `internal/config/config_test.go` | `AC-001` |
| `TEST-002` | YAML parser tests | `internal/parser/*_test.go` | `AC-002` |
| `TEST-003` | Store snapshot/reload tests | `internal/store/store_test.go` | `AC-003`, `AC-007` |
| `TEST-004` | Handler read/discovery/status tests | `internal/handler/handler_test.go` | `AC-004`, `AC-007` |
| `TEST-005` | Admin write/delete tests | `internal/store/store_test.go`, `internal/handler/handler_test.go`, `internal/gitops/repo_test.go` | `AC-005` |
| `TEST-006` | API key auth tests | `internal/handler/handler_test.go`, `internal/config/config_test.go` | `AC-006` |
| `TEST-007` | Degraded/reload tests | `internal/store/store_test.go`, `internal/handler/handler_test.go` | `AC-007` |
| `TEST-008` | Dirty checkout snapshot tests | `internal/gitops/repo_test.go` | `AC-008` |
| `TEST-009` | Secret field rejection tests | `internal/handler/handler_test.go` | `AC-009` |
| `TEST-020` | Secret write/resolve future tests | future secret/store/handler/k8s tests | `AC-020` |
| `TEST-021` | App Registry future tests | future registry/handler/status tests | `AC-021` |
| `TEST-030` | Config Agent future tests | future agent/k8s/e2e tests | `AC-030` |
| `TEST-040` | Console extension future tests | future watch/history/revert/inheritance tests | `AC-040` |
| `TEST-041` | Operational extension future tests | future HTTP/metrics/webhook tests | `AC-041` |
| `TEST-042` | Production hardening future tests | future schema/rate/integration/load tests | `AC-042` |

## Manual / Static Checks

| Check | Command | Covers | Last result |
|---|---|---|---|
| `STATIC-001` | `git diff --check` | whitespace / patch hygiene | passing locally |
| `STATIC-002` | `ruby -e 'require "yaml"; ARGV.each { \|f\| YAML.load_file(f) }' .github/workflows/ci.yml .github/workflows/doc-freshness.yml` | workflow YAML parse | passing locally |
| `STATIC-003` | `rg` placeholder/stale-link checks | documentation migration sanity | passing locally |
| `STATIC-004` | local Ruby markdown-link existence check | relative documentation links | passing locally |
| `LOCAL-001` | `. scripts/dev-env.sh && make test` | unit tests with repo-local Go | passing locally |
| `LOCAL-002` | `. scripts/dev-env.sh && go vet ./...` | Go static analysis | passing locally |
| `LOCAL-003` | `. scripts/dev-env.sh && make test-race` | race-enabled tests | passing locally |
| `LOCAL-004` | `. scripts/dev-env.sh && make build` | binary build | passing locally |

## Definition of Done

Project-level DoD:

- All `must` requirements have acceptance criteria.
- Required gates are `passing` or explicitly `waived`.
- Runtime behavior changes are reflected in `docs/current/`.
- Major decisions are recorded in ADR or `08_DECISION_REGISTER.md`.
- Traceability matrix links requirements, gates, slices, and evidence.

## Notes

- Current PRD uses `FR-*`; `DEC-002` keeps those IDs canonical.
- Planned target features remain `defined`, not `passing`, until code and tests land.
