# AAP Config Server

HTTP service that serves per-service configuration backed by a Git repository as
the single source of truth. The server clones the config repo at startup, loads
every `config.yaml` / `env_vars.yaml` / `secrets.yaml` into an in-memory
snapshot, and swaps the snapshot atomically when the repo changes.

> **Status:** Phase-1 MVP. The [PRD](docs/01_PRD.md) and [HLD](docs/02_HLD.md) describe a
> larger target architecture (Config Agent rollout, history/revert, watch,
> inheritance, and related production extensions). Some of that target is still
> planned — see the feature matrix below.

## Feature matrix

| Area                                               | Status      |
| -------------------------------------------------- | ----------- |
| Git-backed config/env read (`GET .../config`, `GET .../env_vars`) | Implemented |
| Service discovery (`GET /api/v1/orgs` → `projects` → `services`) | Implemented |
| Admin write (`POST /api/v1/admin/changes` config + env_vars) | Implemented |
| Admin delete (`DELETE /api/v1/admin/changes`)             | Implemented |
| Admin reload (`POST /api/v1/admin/reload`)                | Implemented |
| API key auth (Authorization: Bearer, X-API-Key)    | Implemented |
| Last-known-good snapshot on parse error            | Implemented |
| `committed_but_reload_failed` post-commit signal   | Implemented |
| `committed_but_apply_failed` secret apply signal   | Implemented |
| `deleted_but_reload_failed` post-delete signal     | Implemented |
| Degraded state exposed via `/readyz` and `/api/v1/status` | Implemented |
| Secret metadata read (`GET .../secrets`)           | Implemented (auth-gated) |
| Secret **write** via `secrets` field on POST       | Implemented when Kubernetes SealedSecret adapters are configured |
| SealedSecret generation / kubeseal integration     | Implemented with deterministic YAML generation and Bitnami public-key encryption |
| K8s apply of SealedSecret objects                  | Implemented for admin secret writes |
| Secret value resolve (`env_vars?resolve_secrets=true`) | Implemented with API key auth, mounted secret refresh, and `Cache-Control: no-store` |
| Secret audit logging                               | Implemented for admin secret writes and resolved env var secret reads |
| App Registry startup bootstrap                     | Implemented when `CONSOLE_API_URL` is set |
| App Registry webhook                               | Implemented (auth-gated cache upsert/delete) |
| App Registry state in `/api/v1/status`             | Implemented |
| Config Agent binary/API client/local dry-run       | Implemented |
| Config Agent K8s Lease leader election             | Implemented as internal module |
| Config Agent read polling/version tracking         | Implemented as internal module |
| Config Agent native config/env.sh rendering        | Implemented as internal module |
| Config Agent ConfigMap/Secret apply                | Implemented as internal module |
| Config Agent Deployment rollout patch              | Implemented as internal module |
| Config Agent debounce / non-dry-run orchestration  | Not implemented |
| Watch / stream endpoint                            | Not implemented |
| History / revert endpoints                         | Not implemented |

If a feature is listed as "Not implemented", treat descriptions in the PRD/HLD
as planned design — the server will refuse requests that depend on them.

## Quickstart

Prerequisites: Go 1.26+, a Git repo (SSH or HTTPS) that holds the config tree
under `configs/orgs/<org>/projects/<proj>/services/<svc>/`.

```bash
git clone <this repo> && cd aap-config-server

export GIT_URL=git@github.com:myorg/aap-helm-charts.git
export GIT_SSH_KEY=$HOME/.ssh/id_ed25519     # SSH auth (for git@ / ssh:// remotes)
# …or, for an https:// remote, use BasicAuth:
#   export GIT_URL=https://github.com/myorg/aap-helm-charts.git
#   export GIT_USERNAME=myuser
#   export GIT_PASSWORD=ghp_xxx               # env-only; never passed via flag
export API_KEY=$(openssl rand -hex 32)        # required in non-dev
export GIT_POLL_INTERVAL=30s

make build
./bin/config-server -addr :8080
```

Smoke test:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/api/v1/status
curl http://localhost:8080/api/v1/orgs
```

## Environment variables

| Name                         | Required                 | Default               | Notes                                                  |
| ---------------------------- | ------------------------ | --------------------- | ------------------------------------------------------ |
| `GIT_URL`                    | yes                      | —                     | Remote URL of the config repo.                        |
| `GIT_BRANCH`                 | no                       | `main`                |                                                        |
| `GIT_LOCAL_PATH`             | no                       | `/tmp/aap-helm-charts` | Local clone location.                                  |
| `GIT_POLL_INTERVAL`          | no                       | `30s`                 | Must be > 0; `0` or negative is rejected at startup.   |
| `GIT_SSH_KEY`                | no                       | —                     | Path to SSH private key when using an `ssh://` remote. Mutually exclusive with `GIT_USERNAME`/`GIT_PASSWORD`. |
| `GIT_USERNAME`               | no                       | —                     | HTTPS BasicAuth username (pair with `GIT_PASSWORD`).   |
| `GIT_PASSWORD`               | no                       | —                     | HTTPS BasicAuth password/token. Env-only; not accepted as a flag. |
| `API_KEY`                    | yes (prod) / no (dev)    | —                     | See below.                                             |
| `ALLOW_UNAUTHENTICATED_DEV`  | no                       | `false`               | Set to `true` to boot without an API key — dev/test only. |
| `ADDR`                       | no                       | `:8080`               | HTTP listen address.                                   |
| `LOG_LEVEL`                  | no                       | `info`                | `debug`, `info`, `warn`, `error`.                     |
| `SECRET_MOUNT_PATH`          | no                       | `/secrets`            | Absolute root for mounted K8s Secret volume reads.      |
| `SEALED_SECRET_CONTROLLER_NAMESPACE` | no                | `kube-system`         | Namespace for SealedSecret controller public-key lookup and admin write integration. |
| `SEALED_SECRET_CONTROLLER_NAME` | no                    | `sealed-secrets-controller` | Controller service name for SealedSecret public-key lookup and admin write integration. |
| `SEALED_SECRET_SCOPE`        | no                       | `strict`              | SealedSecret scope used by internal sealing adapters: `strict`, `namespace-wide`, or `cluster-wide`. |
| `K8S_APPLY_TIMEOUT`          | no                       | `10s`                 | Timeout for SealedSecret apply adapter calls.          |
| `SECRET_AUDIT_LOG_ENABLED`   | no                       | `true`                | Enables non-sensitive secret audit logging.            |
| `CONSOLE_API_URL`            | no                       | —                     | AAP Console base URL for startup App Registry load.    |
| `CONSOLE_API_TIMEOUT`        | no                       | `5s`                  | Timeout for AAP Console API calls.                     |
| `CONSOLE_REGISTRY_BOOTSTRAP_ATTEMPTS` | no            | `5`                   | Maximum startup App Registry load attempts.            |
| `CONSOLE_REGISTRY_BOOTSTRAP_INITIAL_BACKOFF` | no     | `1s`                  | Initial startup App Registry retry backoff.            |
| `CONSOLE_REGISTRY_BOOTSTRAP_MAX_BACKOFF` | no         | `30s`                 | Maximum startup App Registry retry backoff.            |

## Config Agent dry-run

`config-agent` currently exposes local dry-run summary output. Runtime config,
Config Server reads, leader election, fetch/render, and ConfigMap/Secret apply
and rollout patching exist as internal modules; debounce and non-dry-run
orchestration remain planned follow-up work.

```bash
make build-agent

CONFIG_SERVER_URL=http://localhost:8080 \
CONFIG_AGENT_ORG=myorg \
CONFIG_AGENT_PROJECT=ai \
CONFIG_AGENT_SERVICE=litellm \
./bin/config-agent --dry-run
```

Use `--resolve-secrets` only when the agent has `CONFIG_AGENT_API_KEY` or
`API_KEY`; the dry-run output reports counts and does not print secret values.

### Auth: fail-closed by default

Without `API_KEY`, startup fails with

```
config: API_KEY is required. Set ALLOW_UNAUTHENTICATED_DEV=true only for local dev/test
```

Set `ALLOW_UNAUTHENTICATED_DEV=true` only for local smoke tests; the server
will log a loud warning so it's obvious in logs that auth is disabled.

## API

Most responses are JSON. The exceptions are `/healthz` and `/readyz`, which return plain-text bodies (`ok`, `not ready`, or `degraded`) with no `Content-Type: application/json` header — they are intended for health-check tooling, not API consumers. Errors use a stable JSON envelope:

```json
{ "error": { "code": "validation", "message": "org, project and service are required" } }
```

Current JSON error codes are `not_found`, `validation`, `conflict`,
`unauthorized`, `git_push_failed`, `internal`, and `invalid_body`.

### Authenticated endpoints

Send the API key via either header (Bearer is canonical; `X-API-Key` is a
backwards-compatible alias):

```
Authorization: Bearer <API_KEY>
X-API-Key: <API_KEY>
```

Admin endpoints (`POST /api/v1/admin/changes`, `DELETE /api/v1/admin/changes`,
`POST /api/v1/admin/reload`) and the secret-metadata read
(`GET /api/v1/orgs/.../secrets`) require auth. Config and env_vars reads are
currently unauthenticated; deploy behind a NetworkPolicy that only admits the
expected clients.

### Read

```bash
# Health / readiness
GET /healthz
GET /readyz
GET /api/v1/status

# Discovery
GET /api/v1/orgs
GET /api/v1/orgs/{org}/projects
GET /api/v1/orgs/{org}/projects/{project}/services

# Per-service reads
GET /api/v1/orgs/{org}/projects/{project}/services/{svc}/config
GET /api/v1/orgs/{org}/projects/{project}/services/{svc}/env_vars
GET /api/v1/orgs/{org}/projects/{project}/services/{svc}/env_vars?resolve_secrets=true   # auth required, no-store
GET /api/v1/orgs/{org}/projects/{project}/services/{svc}/secrets   # auth required
```

### Write

```bash
curl -X POST http://localhost:8080/api/v1/admin/changes \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "org":     "myorg",
    "project": "ai",
    "service": "litellm",
    "config":  { "router_settings": { "num_retries": 3 } },
    "env_vars": {
      "plain":       { "LOG_LEVEL": "INFO" },
      "secret_refs": { "API_KEY": "litellm-api-key" }
    },
    "secrets": {
      "litellm-secrets": {
        "namespace": "ai-platform",
        "data": { "api-key": "actual-secret-value" }
      }
    },
    "message": "bump retries"
  }'
```

App Registry webhook:

```bash
POST /api/v1/admin/app-registry/webhook   # auth required
```

Webhook events must include `updated_at` (RFC3339) so delayed async retries
cannot overwrite newer cache state or resurrect entries after a newer delete.

Successful response:

```json
{
  "status":     "committed",
  "version":    "<commit hash>",
  "updated_at": "2026-04-23T10:00:00Z",
  "files":      ["config.yaml", "env_vars.yaml", "secrets.yaml", "sealed-secrets/ai-platform/litellm-secrets.yaml"]
}
```

If the Git push succeeded but the in-memory snapshot could not be refreshed
(e.g. a malformed YAML already on `main`), the response is `503` with:

```json
{
  "status":       "committed_but_reload_failed",
  "version":      "<commit hash>",
  "reload_error": "refusing to swap snapshot: 1 file(s) failed to parse: ..."
}
```

Operators should investigate before trusting subsequent reads — the serving
snapshot is the last-known-good view, which may be stale.

If the Git commit succeeded but applying a generated SealedSecret failed, the
response is `503 committed_but_apply_failed` with `apply_error`. The encrypted
manifest remains committed and should be reconciled or re-applied by an
operator.

Secret writes require the server to run with Kubernetes SealedSecret adapters
configured. If they are unavailable, the request fails validation before any
Git commit. Secret namespaces and K8s Secret object names are also validated
against Kubernetes DNS naming rules before any Git write.

#### Rejected fields

The server refuses unknown JSON fields on admin endpoints (`DisallowUnknownFields`).

### Delete

```bash
curl -X DELETE http://localhost:8080/api/v1/admin/changes \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"org":"myorg","project":"ai","service":"litellm"}'
```

Successful response:

```json
{
  "status":        "deleted",
  "version":       "<commit hash>",
  "deleted_files": ["config.yaml", "env_vars.yaml", "secrets.yaml", "sealed-secrets/"]
}
```

If the Git delete succeeded but the in-memory snapshot could not be reloaded
from the new HEAD, the response is `503` with:

```json
{
  "status":        "deleted_but_reload_failed",
  "version":       "<commit hash>",
  "deleted_files": ["config.yaml", "env_vars.yaml", "secrets.yaml", "sealed-secrets/"],
  "reload_error":  "refusing to swap snapshot: 1 file(s) failed to parse: ..."
}
```

### Reload

```bash
curl -X POST http://localhost:8080/api/v1/admin/reload \
  -H "Authorization: Bearer $API_KEY"
```

## Operational notes

- **Last-known-good snapshot.** The store only swaps the serving snapshot when
  every file parses. A malformed `config.yaml` in the repo will fail the
  reload; reads keep returning the previous snapshot. Check logs for
  `"refusing to swap snapshot"` errors and fix the offending file.
- **Degraded state.** When the last reload failed (background poll or admin
  reload), the server is in a degraded state: it serves the last-known-good
  snapshot but the snapshot may be stale. In this state:
  - `/readyz` returns `503 degraded` so Kubernetes removes the pod from load
    balancer rotation until the underlying YAML is fixed.
  - `/api/v1/status` returns `"status": "degraded"` with `is_degraded: true`
    and `last_reload_error` describing the parse failure.
- **App Registry status.** `/api/v1/status` includes `app_registry.status`,
  `apps_loaded`, `last_loaded_at`, `last_updated_at`, and `last_load_error`
  when present. A registry-only load failure is reported in
  `degraded_components` but does not make `/readyz` fail; Config Server keeps
  serving Git-backed config so Console and Config Server do not deadlock on
  startup order. If `CONSOLE_API_URL` is unset, webhook updates can change
  `apps_loaded` and `last_updated_at`, but `app_registry.status` stays
  `not_configured` because no full Console snapshot was loaded.
- **Post-commit reload.** `ApplyChanges` commits and pushes before reloading.
  A successful commit with a failed reload produces the `committed_but_reload_failed`
  response documented above rather than a bare `200`. Similarly,
  `DeleteChanges` produces `deleted_but_reload_failed` if the post-delete
  reload fails.
- **Secret audit logs.** When `SECRET_AUDIT_LOG_ENABLED=true`, admin secret
  writes and `resolve_secrets=true` env var reads emit non-sensitive audit
  events with action, result, service identity, and secret IDs. Plaintext
  values are not logged.
- **App Registry bootstrap.** If `CONSOLE_API_URL` is set, startup loads
  `GET /api/v1/apps?all=true` into an in-memory cache with bounded
  exponential backoff. Final failure is logged and startup continues with the
  existing cache, which is empty on a fresh process.
- **Post-reload admin endpoint.** `POST /api/v1/admin/reload` is a **force
  reload**: it pulls, then re-parses the current checkout unconditionally.
  Unlike the background poll (which skips the reload when HEAD hasn't moved),
  the operator endpoint always re-parses, so a degraded store recovers on
  the next call once the offending YAML is fixed. Response shapes:
  - `200 {"status":"ok","updated":<bool>,"version":"..."}` — `updated` is
    `true` when the serving snapshot actually changed (HEAD moved, or we
    recovered from a degraded state).
  - `503 {"status":"reload_failed","reload_error":"..."}` — reload (pull or
    parse) failed; the previous snapshot keeps serving.
- **Startup pull.** On startup the server clones the repo if the local path is
  empty, otherwise it opens the existing clone and runs one pull before the
  first snapshot. That keeps a persistent-volume / dev clone from serving
  stale content until the first background poll tick. A pull failure at
  startup is logged and the on-disk checkout is used; later background polls
  catch up when the remote becomes reachable.
- **Dirty worktree detection.** The snapshot walk refuses to build a view over
  a `configs/` subtree that has been mutated outside the server's own locked
  write path (modified, staged, or untracked files). Such drift would make
  `/api/v1/status.version` lie about what's being served, so the reload fails
  closed and the server enters the degraded state documented above.
- **Schema validation.** `config.yaml` and `env_vars.yaml` require
  `metadata.service` / `metadata.org` / `metadata.project`; every
  `secrets.yaml` entry requires an `id` and a complete `k8s_secret` pointer
  (`name`, `namespace`, `key`). Files that parse as valid YAML but miss
  these fields fail the reload instead of loading as an unreachable or
  half-referenced entry.
- **Poll interval must be positive.** `GIT_POLL_INTERVAL=0s` (or negative) is
  rejected at startup rather than panicking inside `time.NewTicker`.

## Development

For the repo-local Go toolchain and caches:

```bash
. scripts/dev-env.sh
```

```bash
make build          # compile config-server and config-agent
make build-server   # compile only config-server
make build-agent    # compile only config-agent
make test           # go test ./...
make test-race      # go test -race ./...
make lint           # golangci-lint (if installed)
make docker-build   # build the container image
```

Container image build support does not include Helm/Kubernetes manifest
ownership; see `docs/current/OPERATIONS.md`.

The CI pipeline (`.github/workflows/ci.yml`) runs `go vet`, race tests, and
`govulncheck` on every push to or pull request targeting `main`.

## Repository layout

```
cmd/config-server/     # main()
cmd/config-agent/      # Config Agent dry-run entrypoint
internal/agent/        # Config Agent runtime config + Config Server client
internal/config/       # env/flag parsing + Validate()
internal/gitops/       # go-git wrapper (clone, pull, commit, push, snapshot)
internal/store/        # atomic snapshot, parse + aggregate
internal/parser/       # YAML types + parsers
internal/handler/      # HTTP handlers, auth middleware, JSON error envelope
internal/server/       # http.Server lifecycle + readiness probe
internal/apperror/     # typed domain errors → HTTP status mapping
docs/                  # numbered project docs, current implementation docs, ADRs
```

See [docs/01_PRD.md](docs/01_PRD.md) and [docs/02_HLD.md](docs/02_HLD.md) for the target
architecture (not all of which is implemented yet — see the feature matrix).
