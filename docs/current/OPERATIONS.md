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
  carry RFC3339 `updated_at`; stale retries are ignored, including older
  upserts that arrive after a newer delete.
- `/api/v1/status` reports App Registry cache/load state under
  `app_registry`; registry-only degradation appears in `degraded_components`
  but does not make `/readyz` fail.
- `/metrics` exposes Prometheus text metrics for HTTP request counts/latency,
  reload attempts/durations, Git operations, watch waits, and degraded state.
  Labels use route templates, operation/resource names, outcomes, and status
  codes; they do not include service identities or secret data.
- Operational state is exposed through `/readyz` and `/api/v1/status`.

## Background jobs

- A background git poll loop calls `RefreshFromRepo` every `GIT_POLL_INTERVAL`.
- The poll path only reloads when HEAD moved.
- `POST /api/v1/admin/reload` force-reloads even when HEAD did not move.

## Deployment

- Container builds are defined by `Dockerfile`.
- This repo owns the Config Server and Config Agent binaries, Docker image
  build targets, runtime configuration docs, and runbook guidance.
- Helm charts and Kubernetes manifests remain outside this repo unless a future
  decision explicitly moves deployment ownership here.
- Runtime network access should restrict unauthenticated config/env reads to trusted clients.

### Config Agent image build

The default Docker target remains `config-server`:

```bash
make docker-build
docker build --target config-server -t aap/config-server:latest .
```

Build the Config Agent image with the dedicated target:

```bash
make docker-build-agent
docker build --target config-agent -t aap/config-agent:latest .
```

### Config Agent RBAC and deployment handoff example

The following snippets are non-authoritative handoff examples for the external
deployment repo/system that owns manifests under `DEC-003`. Do not copy them
into this repo as a manifest tree unless deployment ownership changes.

If the target ConfigMap, Secret, and Lease are pre-created, mutating permissions
can stay resource-name scoped:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: litellm-config-agent
  namespace: ai-platform
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: litellm-config-agent
  namespace: ai-platform
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    resourceNames: ["litellm-config"]
    verbs: ["get", "patch", "update"]
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["litellm-env"]
    verbs: ["get", "patch", "update"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    resourceNames: ["litellm"]
    verbs: ["get", "patch"]
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    resourceNames: ["litellm-config-agent"]
    verbs: ["get", "patch", "update"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: litellm-config-agent
  namespace: ai-platform
subjects:
  - kind: ServiceAccount
    name: litellm-config-agent
    namespace: ai-platform
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: litellm-config-agent
```

If the Agent must create missing target resources, Kubernetes RBAC cannot
resource-name restrict `create`; the deployment owner must explicitly decide
whether to add namespace-scoped `create` on `configmaps`, `secrets`, and
`leases`.

Current `cmd/config-agent` still requires `--dry-run`. The live deployment shape
below records the target runtime contract for the external deployment owner once
the non-dry-run entrypoint is enabled:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: litellm-config-agent
  namespace: ai-platform
spec:
  replicas: 2
  selector:
    matchLabels:
      app.kubernetes.io/name: litellm-config-agent
  template:
    metadata:
      labels:
        app.kubernetes.io/name: litellm-config-agent
    spec:
      serviceAccountName: litellm-config-agent
      containers:
        - name: config-agent
          image: aap/config-agent:latest
          args:
            - --config-server=http://aap-config-server.default.svc:8080
            - --org=myorg
            - --project=ai
            - --service=litellm
            - --resolve-secrets
            - --target-namespace=ai-platform
            - --target-configmap=litellm-config
            - --target-secret=litellm-env
            - --target-deployment=litellm
            - --poll-interval=30s
            - --debounce-cooldown=10s
            - --debounce-quiet-period=10s
            - --debounce-max-wait=2m
          env:
            - name: CONFIG_AGENT_API_KEY
              valueFrom:
                secretKeyRef:
                  name: config-agent-api
                  key: api-key
```

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

### App Registry degradation

Symptom:

- `/readyz` returns 200.
- `/api/v1/status` returns `is_degraded: true`,
  `degraded_components: ["app_registry"]`, and
  `app_registry.last_load_error`.

Action:

1. Confirm `CONSOLE_API_URL` and network access to AAP Console.
2. Check Config Server logs for `app registry bootstrap failed`.
3. Let Console webhook retries repopulate changed app records, or restart
   Config Server after Console API is reachable to reload the full registry.

### Auth failure

Symptom:

- Admin or secret metadata endpoint returns `401 unauthorized`.

Action:

1. Send `Authorization: Bearer <API_KEY>` or `X-API-Key`.
2. Confirm the server and client are using the same key.
3. Do not disable auth outside local dev/test.
