# Current State

Status: active.

This file is the first read for new AI/human sessions. It is a compressed
current operating view, not full history.

## Product / Project

AAP Config Server is a Go HTTP service that serves per-service configuration
from a Git repository. Git is the source of truth; runtime reads are served
from an atomically swapped in-memory snapshot.

## Current roadmap position

- current milestone: `P0-M1` Phase-1 Config Server MVP
- active tracks: `CORE`, `OPS`, `DOC`
- active phase: `DOC-1A` documentation boilerplate migration
- active slice: `DOC-1A.2`
- last accepted gate: none recorded in this migrated ledger
- next gate: `make test` in an environment with Go installed
- canonical ledger: `docs/04_IMPLEMENTATION_PLAN.md`

## Implemented

- Go module `github.com/aap/config-server` with `cmd/config-server`.
- Runtime config loading from env/flags with fail-closed `API_KEY` behavior.
- Git clone/open/pull/commit/push using `go-git`.
- In-memory store with atomic snapshot swap and last-known-good behavior.
- Parser support for `config.yaml`, `env_vars.yaml`, and `secrets.yaml` metadata.
- Read APIs for config, env vars, service discovery, status, health/readiness.
- Auth-gated admin write/delete/reload endpoints.
- Auth-gated secret metadata read; secret value write/resolve is not implemented.
- Degraded state through `/readyz` and `/api/v1/status`.

## Planned

- SealedSecret generation, K8s apply, and secret value resolve.
- Config Agent and registry webhook.
- Watch/history/revert endpoints.
- Config inheritance and additional operational hardening.

## Explicit non-goals

- Do not store secret plaintext in Git.
- Do not treat target-design PRD/HLD sections as implemented behavior.
- Do not hand-edit generated docs under `docs/generated/`.

## Current priorities

1. Finish documentation migration to the numbered boilerplate structure.
2. Keep implemented behavior discoverable under `docs/current/`.
3. Normalize Phase roadmap/status into `docs/04_IMPLEMENTATION_PLAN.md`.

## Current risks / unknowns

- `Q-001`: Should PRD `FR-*` remain the canonical requirement IDs, or should the project migrate to `REQ-*`?
- `Q-002`: ADR-003 describes service-level mutex; current implementation serializes git operations globally.
- `Q-003`: Exact deployment owner for Helm/K8s manifests is not represented in this repo.

## Current validation

- Commands are listed in `docs/current/TESTING.md`.
- Acceptance gates are listed in `docs/06_ACCEPTANCE_TESTS.md`.
- Local `make test` is currently blocked in this environment because `go` is not installed.

## Needs audit

- HLD package list includes planned packages not present in the current code.
- CI claims in README should be kept aligned with `.github/workflows/ci.yml`.
- Decide whether `FR-*` should remain canonical or gain `REQ-*` aliases.

## Links

- PRD: `docs/01_PRD.md`
- HLD: `docs/02_HLD.md`
- Roadmap / status ledger: `docs/04_IMPLEMENTATION_PLAN.md`
- Acceptance tests: `docs/06_ACCEPTANCE_TESTS.md`
- Questions: `docs/07_QUESTIONS_REGISTER.md`
- Decisions: `docs/08_DECISION_REGISTER.md`
- ADRs: `docs/adr/`
