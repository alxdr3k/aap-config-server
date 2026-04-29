# Operations

Status: active.

## Local run

Required local inputs:

- A Git repository containing `configs/orgs/{org}/projects/{project}/services/{service}/`.
- Git auth through SSH key or HTTPS BasicAuth.
- An `API_KEY`, unless explicitly opting into unauthenticated local dev.

Example:

```bash
export GIT_URL=git@github.com:myorg/aap-helm-charts.git
export GIT_SSH_KEY=$HOME/.ssh/id_ed25519
export API_KEY=$(openssl rand -hex 32)
export GIT_POLL_INTERVAL=30s

make build
./bin/config-server -addr :8080
```

Dev-only unauthenticated startup:

```bash
export ALLOW_UNAUTHENTICATED_DEV=true
```

Do not use that flag in production.

## Environment variables

| Name | Required | Default | Notes |
|---|---|---|---|
| `GIT_URL` | yes |  | Remote config repo URL. |
| `GIT_BRANCH` | no | `main` | Branch to clone/pull/push. |
| `GIT_LOCAL_PATH` | no | `/tmp/aap-helm-charts` | Local clone path. |
| `GIT_POLL_INTERVAL` | no | `30s` | Must be greater than zero. |
| `GIT_SSH_KEY` | no |  | SSH private key path. Mutually exclusive with BasicAuth. |
| `GIT_USERNAME` | no |  | HTTPS BasicAuth username. Must pair with `GIT_PASSWORD`. |
| `GIT_PASSWORD` | no |  | HTTPS BasicAuth password/token. Env-only. |
| `API_KEY` | prod yes |  | Required unless dev opt-in is set. |
| `ALLOW_UNAUTHENTICATED_DEV` | no | `false` | Local/test escape hatch only. |
| `ADDR` | no | `:8080` | HTTP listen address. |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error`. |
| `SECRET_MOUNT_PATH` | no | `/secrets` | Absolute root for mounted K8s Secret volume reads. |
| `SEALED_SECRET_CONTROLLER_NAMESPACE` | no | `kube-system` | Namespace for SealedSecret controller public-key lookup and admin write integration. |
| `SEALED_SECRET_CONTROLLER_NAME` | no | `sealed-secrets-controller` | Controller service name for SealedSecret public-key lookup and admin write integration. |
| `SEALED_SECRET_SCOPE` | no | `strict` | SealedSecret scope used by internal sealing adapters: `strict`, `namespace-wide`, or `cluster-wide`. |
| `K8S_APPLY_TIMEOUT` | no | `10s` | Timeout for SealedSecret apply adapter calls. |
| `SECRET_AUDIT_LOG_ENABLED` | no | `true` | Enables non-sensitive secret audit logging. |
| `CONSOLE_API_URL` | no |  | AAP Console base URL for startup App Registry load. |
| `CONSOLE_API_TIMEOUT` | no | `5s` | Timeout for AAP Console API calls. |
| `CONSOLE_REGISTRY_BOOTSTRAP_ATTEMPTS` | no | `5` | Maximum startup App Registry load attempts. |
| `CONSOLE_REGISTRY_BOOTSTRAP_INITIAL_BACKOFF` | no | `1s` | Initial startup App Registry retry backoff. |
| `CONSOLE_REGISTRY_BOOTSTRAP_MAX_BACKOFF` | no | `30s` | Maximum startup App Registry retry backoff. |

## Database

No database is used. Git is the source of truth; the server keeps an in-memory
snapshot for serving reads.

## Logs / observability

- Logs use structured JSON through `log/slog`.
- Secret audit logs include action, result, service identity, and secret IDs
  for admin secret writes and resolved env var secret reads; plaintext values
  are not logged.
- App Registry startup logs whether bootstrap was skipped, loaded, or failed
  after the configured attempts.
- App Registry webhook calls use the same admin API key boundary as other
  admin endpoints and update only the in-memory registry cache. Events must
  carry RFC3339 `updated_at`; stale retries are ignored.
- No Prometheus metrics endpoint is currently implemented.
- Operational state is exposed through `/readyz` and `/api/v1/status`.

## Background jobs

- A background git poll loop calls `RefreshFromRepo` every `GIT_POLL_INTERVAL`.
- The poll path only reloads when HEAD moved.
- `POST /api/v1/admin/reload` force-reloads even when HEAD did not move.

## Deployment

- Container build is defined by `Dockerfile`.
- This repo owns the Config Server binary, Docker image build, runtime
  configuration docs, and runbook guidance.
- Helm charts and Kubernetes manifests remain outside this repo unless a future
  decision explicitly moves deployment ownership here.
- Runtime network access should restrict unauthenticated config/env reads to trusted clients.

### CI/CD ownership

- Active CI workflow: `.github/workflows/ci.yml` on pull requests and
  direct pushes to `main` or `dev`.
- CD workflow: none active in this repo.
- Release source: `dev` is the integration branch; promote `dev` to `main`
  only through PR.
- Deployment owner: external deployment repo/system per DEC-003 unless a future
  decision moves manifests into this repo.
- CD guidance: `docs/11_CI_CD.md`.

## Troubleshooting

### Degraded readiness

Symptom:

- `/readyz` returns 503 `degraded`.
- `/api/v1/status` returns `is_degraded: true`.

Action:

1. Read `last_reload_error` from `/api/v1/status`.
2. Fix malformed YAML, schema errors, or dirty `configs/` checkout drift.
3. Call `POST /api/v1/admin/reload` with API key.
4. Confirm `/readyz` returns 200.

### Post-commit reload failure

Symptom:

- `POST /api/v1/admin/changes` returns `503 committed_but_reload_failed`.
- For secret writes, `POST /api/v1/admin/changes` can also return
  `503 committed_but_apply_failed` when the encrypted Git commit succeeded but
  Kubernetes apply failed.

Action:

1. Treat the Git commit as already written.
2. Inspect `reload_error` or `apply_error`.
3. For reload failures, fix the offending config repo state and force reload.
4. For apply failures, fix K8s access/controller issues, then re-apply the
   committed SealedSecret manifest or retry the admin write.

### Auth failure

Symptom:

- Admin or secret metadata endpoint returns `401 unauthorized`.

Action:

1. Send `Authorization: Bearer <API_KEY>` or `X-API-Key`.
2. Confirm the server and client are using the same key.
3. Do not disable auth outside local dev/test.
