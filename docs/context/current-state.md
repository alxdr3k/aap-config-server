# Current State

Status: active.

This file is the first read for new AI/human sessions. It is a compressed
current operating view, not full history.

## Product / Project

AAP Config Server is a Go HTTP service that serves per-service configuration
from a Git repository. Git is the source of truth; runtime reads are served
from an atomically swapped in-memory snapshot.

## Current roadmap position

- current milestone: `P1-M3` extension APIs next
- active tracks: `EXT`
- active phase: `EXT-1B`
- active slice: `EXT-1B.2`
- last accepted gate: `AC-030`
- next gate: `P1-M3` / `AC-040`
- canonical ledger: `docs/04_IMPLEMENTATION_PLAN.md`

## Implemented

- Go module `github.com/aap/config-server` with `cmd/config-server`.
- Runtime config loading from env/flags with fail-closed `API_KEY` behavior.
- Git clone/open/pull/commit/push using `go-git`.
- Phase-1 admin writes, deletes, refreshes, and Git operations are serialized
  globally by `ADR-005`; service-level mutexes remain target design only.
- In-memory store with atomic snapshot swap and last-known-good behavior.
- Store version-change notification and `WaitForVersionChange` primitive for
  long-poll watch endpoints.
- Parser support for `config.yaml`, `env_vars.yaml`, and `secrets.yaml` metadata.
- Read APIs for config, env vars, service discovery, status, health/readiness.
- Config/env vars watch APIs with resource-scoped `version` mismatch behavior
  and max 30s long-poll timeout returning `304 Not Modified` when unchanged.
  Env vars watch returns unresolved `plain` plus `secret_refs` payloads when
  changed.
- Git history iterator and service-scoped file-change classifier under
  `internal/gitops`, ready for the history API endpoint.
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
- Config Agent read polling loop under `internal/agent`, with config/env
  content-hash change detection, same-revision guard, handler-success-based
  state advancement, and retry backoff for fetch/handler failures.
- Config Agent native config/env.sh renderer under `internal/agent`, with
  deterministic YAML output, ConfigMap secret-reference preservation, and
  resolved-env validation before Secret payload generation.
- Config Agent ConfigMap/Secret apply adapter under `internal/agent`, using
  configured namespace/resource names only and preserving unrelated data keys
  when patching existing resources.
- Config Agent Deployment rollout patcher under `internal/agent`, updating
  pod-template annotations with a payload hash and restart timestamp to trigger
  a Kubernetes rolling restart.
- Config Agent leading-edge debounce state machine under `internal/agent`, with
  cooldown, quiet-period, and max-wait behavior covered by deterministic tests.
- Config Agent image build target, RBAC/deployment handoff examples, and
  fake-client e2e smoke coverage for fetch/render/apply/rollout flow.

## Planned

- History/revert endpoints, config inheritance, response optimizations,
  metrics, schema validation, rate limiting, and integration/load validation.

## Explicit non-goals

- Do not store secret plaintext in Git.
- Do not treat target-design PRD/HLD sections as implemented behavior.
- Do not hand-edit generated docs under `docs/generated/`.

## Current priorities

1. Start `EXT-1B.2`: implement the history API with `file`, `limit`, and
   `before` filtering.
2. Keep P1 work aligned with the leaf slices in `docs/04_IMPLEMENTATION_PLAN.md`.
3. Revisit roadmap sequencing only when a new decision changes dependencies.

## Current risks / unknowns

- No open migration decision questions in `docs/07_QUESTIONS_REGISTER.md`;
  roadmap leaf slices are defined through `P1-M3`.

## Current validation

- Commands are listed in `docs/current/TESTING.md`.
- Acceptance gates are listed in `docs/06_ACCEPTANCE_TESTS.md`.
- `AC-020` is passing for the secret write/resolve path, `AC-021` is passing
  for App Registry bootstrap/webhook/status integration, and `AGENT-1A.1`~
  `AGENT-1A.8` have local coverage for Config Agent bootstrap, leader
  election, read polling, rendering, ConfigMap/Secret apply, rollout patch, and
  debounce behavior, plus fake-client e2e smoke coverage for the Agent
  fetch/render/apply/rollout flow. Subsequent dev-cycle PRs use the repo
  `check`, `lint`, `scan`, and `test` checks before merge.
- `EXT-1A.1` has local store coverage for immediate stale-version return,
  successful refresh notification, failed-refresh non-notification, and context
  cancellation.
- `EXT-1A.2` has local handler coverage for version mismatch, missing
  version, invalid timeout, and `304 Not Modified` timeout behavior.
- `EXT-1A.3` has local store/handler coverage for env vars version mismatch,
  resource-scoped config-only non-wakeup behavior, unresolved secret refs,
  missing version, and `304 Not Modified` timeout behavior.
- `EXT-1B.1` has local gitops coverage for service-scoped file classification
  and newest-first commit history iteration.
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
