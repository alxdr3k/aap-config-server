# Current State

Status: active.

This file is the first read for new AI/human sessions. It is a compressed
current operating view, not full history.

## Product / Project

AAP Config Server is a Go HTTP service that serves per-service configuration
from a Git repository. Git is the source of truth; runtime reads are served
from an atomically swapped in-memory snapshot.

## Current roadmap position

- current milestone: `P1-M2` Config Agent rollout path next
- active tracks: `AGENT`
- active phase: `AGENT-1A`
- active slice: `AGENT-1A.3`
- last accepted gate: `AC-021`
- next gate: `P1-M2` / `AC-030`
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
- Auth-gated secret metadata read, admin secret writes, and
  `resolve_secrets=true` env var reads.
- Degraded state through `/readyz` and `/api/v1/status`.
- Secret runtime boundary settings and adapter interfaces for mounted volume
  reads, SealedSecret sealing, K8s apply, and audit logging.
- Mounted secret file reader with fsnotify-backed refresh events under
  `internal/secret`; env var secret resolve is wired to HTTP with no-store
  responses.
- Deterministic SealedSecret YAML generator with Bitnami public-key encryption
  adapter and controller certificate lookup; admin writes now use this path
  when Kubernetes adapters are configured.
- K8s dynamic-client SealedSecret apply adapter under `internal/secret`;
  admin write/runtime wiring now uses configured Kubernetes clients when
  in-cluster config is available.
- Non-sensitive secret audit logging for admin secret writes and resolved env
  var secret reads, plus best-effort plaintext cleanup in secret handling
  boundaries.
- AAP Console App Registry startup bootstrap client/cache under
  `internal/registry`, wired through `CONSOLE_API_URL` with bounded
  exponential backoff and graceful empty-cache startup on final failure.
- Auth-gated App Registry webhook endpoint for Console-driven cache upsert and
  delete updates.
- App Registry cache/load state in `/api/v1/status`, including degraded
  component reporting for registry-only Console load failures without failing
  `/readyz`.
- Config Agent bootstrap binary under `cmd/config-agent`, with runtime
  config loading, bounded Config Server API client, and local dry-run summary
  mode under `internal/agent`.
- Config Agent K8s Lease leader election module under `internal/agent`, using
  client-go LeaseLock with standby takeover coverage against the fake K8s
  client.

## Planned

- Config Agent polling loop, ConfigMap/Secret apply, Deployment rollout
  patching, debounce, image/RBAC examples, and e2e smoke coverage.
- Watch/history/revert endpoints, config inheritance, response optimizations,
  metrics, schema validation, rate limiting, and integration/load validation.

## Explicit non-goals

- Do not store secret plaintext in Git.
- Do not treat target-design PRD/HLD sections as implemented behavior.
- Do not hand-edit generated docs under `docs/generated/`.

## Current priorities

1. Start `AGENT-1A.3`: config/env fetch loop, version tracking, and retry/backoff behavior using read API polling.
2. Keep P1 work aligned with the leaf slices in `docs/04_IMPLEMENTATION_PLAN.md`.
3. Revisit roadmap sequencing only when a new decision changes dependencies.

## Current risks / unknowns

- No open migration decision questions in `docs/07_QUESTIONS_REGISTER.md`;
  roadmap leaf slices are defined through `P1-M3`.

## Current validation

- Commands are listed in `docs/current/TESTING.md`.
- Acceptance gates are listed in `docs/06_ACCEPTANCE_TESTS.md`.
- `AC-020` is passing for the secret write/resolve path, `AC-021` is passing
  for App Registry bootstrap/webhook/status integration, and `AGENT-1A.1` /
  `AGENT-1A.2` have local coverage for Config Agent bootstrap and leader
  election behavior. Subsequent dev-cycle PRs use the repo `check`, `lint`,
  `scan`, and `test` checks before merge.
- Repo-local Go 1.26.2 is available through `scripts/dev-env.sh`.
- Local `. scripts/dev-env.sh && make test`, `go vet ./...`,
  `make test-race`, `make lint`, and `make build` pass in this workspace.

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
