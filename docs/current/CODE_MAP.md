# Code Map

Status: active.

## Entry points

| Path | Purpose |
|---|---|
| `cmd/config-server/main.go` | Composition root: load config, initialize git repo/store/handler/server, start poll loop. |
| `cmd/config-agent/main.go` | Config Agent bootstrap entrypoint: load agent config, create Config Server client, run local dry-run reads. |
| `Dockerfile` | Container image build targets for the config-server and config-agent binaries. |
| `Makefile` | Project build/test/lint/docker command surface. |

## Runtime / App

| Path | Purpose |
|---|---|
| `internal/config/` | Environment/flag parsing and validation for runtime configuration. |
| `internal/server/` | `http.Server` lifecycle, graceful shutdown, readiness probe. |
| `internal/handler/` | HTTP routing, request decoding, API key auth, JSON response/error envelope, config/env cache validators and gzip compression, config/env watch long polling, versioned reads, history API responses, and public revert endpoint responses. |
| `internal/apperror/` | Typed domain errors and error-code mapping used by handlers and store. |
| `internal/agent/` | Config Agent bootstrap runtime config, bounded Config Server API client, dry-run summary runner, K8s Lease leader election wrapper, read polling/version tracking loop, native config/env.sh payload renderer, ConfigMap/Secret apply adapter, Deployment rollout patcher, leading-edge debounce state machine, and e2e smoke coverage for the composed Agent flow. |

## Domain / Services

| Path | Purpose |
|---|---|
| `internal/store/` | In-memory service snapshot, Git-backed reload, defaults source parsing/metadata, internal inherited config/env merge precomputation, inherited historical config/env reads, resource-scoped version tokens, historical config/env file reads, revert restore planning/application, config/env/secret apply operations, delete operations, history filtering, degraded status. |
| `internal/parser/` | YAML structs/parsers/validation for config, env vars, secrets, and defaults. |
| `internal/secret/` | Secret runtime boundary types, mounted K8s Secret file reader/watch support, deterministic SealedSecret YAML generation, controller public-key encryption, K8s SealedSecret apply adapter, and slog-backed non-sensitive audit logging. |
| `internal/registry/` | AAP Console App Registry HTTP client, in-memory cache, startup bootstrap retry/backoff logic, cache update semantics, and status state. |

## Data / Persistence

| Path | Purpose |
|---|---|
| `internal/gitops/` | `go-git` wrapper for clone/open, pull, commit/push, delete/push, service restore/push, snapshot walking, service-scoped historical file reads, history iteration, and file-change classification. |
| Config repo `configs/orgs/{org}/projects/{project}/services/{service}/` | External Git-backed data tree read and written by the server. |

## Tests

| Path | Purpose |
|---|---|
| `internal/config/*_test.go` | Runtime config validation. |
| `internal/apperror/*_test.go` | Error wrapping and `errors.As` behavior. |
| `internal/parser/*_test.go` | YAML parser happy-path and validation failures. |
| `internal/secret/*_test.go` | Secret boundary value/default behavior, mounted secret reader/watch behavior, deterministic SealedSecret YAML generation, public-key encryption wiring, and K8s apply adapter behavior. |
| `internal/registry/*_test.go` | Console App Registry client decoding, cache replacement/update semantics, and startup bootstrap retry behavior. |
| `internal/store/*_test.go` | Snapshot reload, defaults source parsing, internal inheritance merge semantics, inheritance/admin-write preservation, resource-scoped versions, version-change waiting, historical config/env reads, revert restore planning/application, history filtering, config/env/secret apply, secret audit logging, delete, degraded behavior, concurrency. |
| `internal/gitops/*_test.go` | Local Git clone/pull/commit/delete/restore/snapshot behavior plus service history/file-change primitives. |
| `internal/handler/*_test.go` | HTTP routes, config/env ETag, `If-None-Match`, and gzip behavior, config/env watch behavior, versioned and inherited read behavior, history API behavior, revert endpoint behavior, auth, admin write response shape and service-level payload preservation, App Registry webhook auth/cache updates, App Registry status reporting, secret write input cleanup, resolved env var secret reads, secret audit logging, reload/degraded status. |
| `internal/agent/*_test.go` | Config Agent config loading/validation, Config Server API client behavior, bounded responses, dry-run counts, K8s Lease leader election takeover behavior, fetch loop retry/version tracking, renderer validation, ConfigMap/Secret apply behavior, rollout patch behavior, debounce timing behavior, and e2e smoke coverage under the `e2e` build tag. |

## Needs audit

| Path | Reason |
|---|---|
| `docs/02_HLD.md` | Includes target flows beyond the current Agent bootstrap slice; current implementation boundaries are summarized in `README.md` and `docs/current/*`. |
| `docs/01_PRD.md` | Phase checklist predates current implementation status; use `docs/04_IMPLEMENTATION_PLAN.md` as status ledger. |
