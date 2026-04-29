# Current State

Status: active.

This file is the first read for new AI/human sessions. It is a compressed
current operating view, not full history.

## Product / Project

AAP Config Server is a Go HTTP service that serves per-service configuration
from a Git repository. Git is the source of truth; runtime reads are served
from an atomically swapped in-memory snapshot.

## Current roadmap position

- current milestone: `P1-M1` secret write/resolve path started
- active tracks: `SECRET`
- active phase: `SECRET-1A`
- active slice: none
- last accepted gate: `AC-014` / `AC-015` via PR #10
- next gate: `P1-M1` / `AC-020`, `AC-021`
- canonical ledger: `docs/04_IMPLEMENTATION_PLAN.md`

## Implemented

- Go module `github.com/aap/config-server` with `cmd/config-server`.
- Runtime config loading from env/flags with fail-closed `API_KEY` behavior.
- Git clone/open/pull/commit/push using `go-git`.
- Phase-1 admin writes, deletes, refreshes, and Git operations are serialized
  globally by `ADR-005`; service-level mutexes remain target design only.
- In-memory store with atomic snapshot swap and last-known-good behavior.
- Parser support for `config.yaml`, `env_vars.yaml`, and `secrets.yaml` metadata.
- Read APIs for config, env vars, service discovery, status, health/readiness.
- Auth-gated admin write/delete/reload endpoints.
- Auth-gated secret metadata read; secret value write/resolve is not implemented.
- Degraded state through `/readyz` and `/api/v1/status`.
- Secret runtime boundary settings and adapter interfaces for future volume
  reads, SealedSecret sealing, K8s apply, and audit logging.

## Planned

- SealedSecret generation, K8s apply, and secret value resolve.
- App Registry bootstrap and webhook cache.
- Config Agent rollout path.
- Watch/history/revert endpoints, config inheritance, response optimizations,
  metrics, schema validation, rate limiting, and integration/load validation.

## Explicit non-goals

- Do not store secret plaintext in Git.
- Do not treat target-design PRD/HLD sections as implemented behavior.
- Do not hand-edit generated docs under `docs/generated/`.

## Current priorities

1. Continue `SECRET-1A` with `SECRET-1A.2`.
2. Keep P1 work aligned with the leaf slices in `docs/04_IMPLEMENTATION_PLAN.md`.
3. Revisit roadmap sequencing only when a new decision changes dependencies.

## Current risks / unknowns

- No open migration decision questions in `docs/07_QUESTIONS_REGISTER.md`;
  roadmap leaf slices are defined through `P1-M3`.

## Current validation

- Commands are listed in `docs/current/TESTING.md`.
- Acceptance gates are listed in `docs/06_ACCEPTANCE_TESTS.md`.
- PR #10 established `AC-014` / `AC-015`; subsequent dev-cycle PRs use the
  repo `check`, `lint`, `scan`, and `test` checks before merge.
- Repo-local Go 1.26.2 is available through `scripts/dev-env.sh`.
- Local `. scripts/dev-env.sh && make test`, `go vet ./...`, `make test-race`, and `make build` pass in this workspace.

## Needs audit

- No active migration-loop audit item remains.
- Re-audit HLD/current implementation boundaries when planned packages are
  implemented.
- Re-audit README CI claims when `.github/workflows/ci.yml` changes.

## Links

- PRD: `docs/01_PRD.md`
- HLD: `docs/02_HLD.md`
- Roadmap / status ledger: `docs/04_IMPLEMENTATION_PLAN.md`
- Acceptance tests: `docs/06_ACCEPTANCE_TESTS.md`
- Questions: `docs/07_QUESTIONS_REGISTER.md`
- Decisions: `docs/08_DECISION_REGISTER.md`
- ADRs: `docs/adr/`
