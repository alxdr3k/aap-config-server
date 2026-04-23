# AAP Config Server

HTTP service that serves per-service configuration backed by a Git repository as
the single source of truth. The server clones the config repo at startup, loads
every `config.yaml` / `env_vars.yaml` / `secrets.yaml` into an in-memory
snapshot, and swaps the snapshot atomically when the repo changes.

> **Status:** Phase-1 MVP. The [PRD](docs/PRD.md) and [HLD](docs/HLD.md) describe a
> larger target architecture (Config Agent, SealedSecret controller, registry
> webhook, history/revert, watch). Those are **not** implemented yet — see the
> feature matrix below.

## Feature matrix

| Area                                               | Status      |
| -------------------------------------------------- | ----------- |
| Git-backed config read (`GET /config`, `env_vars`) | Implemented |
| Service discovery (`orgs` → `projects` → `services`) | Implemented |
| Admin write (`POST /admin/changes` config + env_vars) | Implemented |
| Admin delete (`DELETE /admin/changes`)             | Implemented |
| Admin reload (`POST /admin/reload`)                | Implemented |
| API key auth (Authorization: Bearer, X-API-Key)    | Implemented |
| Last-known-good snapshot on parse error            | Implemented |
| `committed_but_reload_failed` post-commit signal   | Implemented |
| `deleted_but_reload_failed` post-delete signal     | Implemented |
| Degraded state exposed via `/readyz` and `/status` | Implemented |
| Secret metadata read (`GET .../secrets`)           | Implemented (auth-gated) |
| Secret **write** via `secrets` field on POST       | **Not implemented** — rejected with 400 |
| SealedSecret generation / kubeseal integration     | Not implemented |
| K8s apply of SealedSecret objects                  | Not implemented |
| Watch / stream endpoint                            | Not implemented |
| History / revert endpoints                         | Not implemented |
| Config Agent, registry webhook                     | Not implemented |

If a feature is listed as "Not implemented", treat descriptions in the PRD/HLD
as planned design — the server will refuse requests that depend on them.

## Quickstart

Prerequisites: Go 1.24+, a Git repo (SSH or HTTPS) that holds the config tree
under `configs/orgs/<org>/projects/<proj>/services/<svc>/`.

```bash
git clone <this repo> && cd aap-config-server

export GIT_URL=git@github.com:myorg/aap-helm-charts.git
export GIT_SSH_KEY=$HOME/.ssh/id_ed25519     # or set GIT_URL=https://... and use BasicAuth
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
| `GIT_SSH_KEY`                | no                       | —                     | Path to SSH private key when using an `ssh://` remote. |
| `API_KEY`                    | yes (prod) / no (dev)    | —                     | See below.                                             |
| `ALLOW_UNAUTHENTICATED_DEV`  | no                       | `false`               | Set to `true` to boot without an API key — dev/test only. |
| `ADDR`                       | no                       | `:8080`               | HTTP listen address.                                   |
| `LOG_LEVEL`                  | no                       | `info`                | `debug`, `info`, `warn`, `error`.                     |
| `SECRET_MOUNT_PATH`          | no                       | `/secrets`            | Reserved for future secret-mount logic.                |
| `CONSOLE_API_URL`            | no                       | —                     | Reserved.                                              |

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
    "message": "bump retries"
  }'
```

Successful response:

```json
{
  "status":     "committed",
  "version":    "<commit hash>",
  "updated_at": "2026-04-23T10:00:00Z",
  "files":      ["config.yaml", "env_vars.yaml"]
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

#### Rejected fields

The server refuses unknown JSON fields on admin endpoints (`DisallowUnknownFields`).
In particular:

```json
{ "secrets": [ ... ] }        // 400
```

is rejected with a message explaining that secret writes are not part of
Phase-1. This prevents silent data loss for clients that follow the PRD v2.1
schema.

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
  "deleted_files": ["config.yaml", "env_vars.yaml", "secrets.yaml"]
}
```

If the Git delete succeeded but the in-memory snapshot could not be reloaded
from the new HEAD, the response is `503` with:

```json
{
  "status":        "deleted_but_reload_failed",
  "version":       "<commit hash>",
  "deleted_files": ["config.yaml", "env_vars.yaml", "secrets.yaml"],
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
- **Post-commit reload.** `ApplyChanges` commits and pushes before reloading.
  A successful commit with a failed reload produces the `committed_but_reload_failed`
  response documented above rather than a bare `200`. Similarly,
  `DeleteChanges` produces `deleted_but_reload_failed` if the post-delete
  reload fails.
- **Post-reload admin endpoint.** `POST /api/v1/admin/reload` returns
  `503 {"status":"reload_failed","reload_error":"..."}` when the reload fails,
  so operators can distinguish parse errors from generic 500s.
- **Poll interval must be positive.** `GIT_POLL_INTERVAL=0s` (or negative) is
  rejected at startup rather than panicking inside `time.NewTicker`.

## Development

```bash
make build          # compile the binary
make test           # go test ./...
make test-race      # go test -race ./...
make lint           # golangci-lint (if installed)
make docker-build   # build the container image
```

The CI pipeline (`.github/workflows/ci.yml`) runs `go vet`, race tests, and
`govulncheck` on every push to or pull request targeting `main`.

## Repository layout

```
cmd/config-server/     # main()
internal/config/       # env/flag parsing + Validate()
internal/gitops/       # go-git wrapper (clone, pull, commit, push, snapshot)
internal/store/        # atomic snapshot, parse + aggregate
internal/parser/       # YAML types + parsers
internal/handler/      # HTTP handlers, auth middleware, JSON error envelope
internal/server/       # http.Server lifecycle + readiness probe
internal/apperror/     # typed domain errors → HTTP status mapping
docs/                  # PRD, HLD, ADRs, development process
```

See [docs/PRD.md](docs/PRD.md) and [docs/HLD.md](docs/HLD.md) for the target
architecture (not all of which is implemented yet — see the feature matrix).
