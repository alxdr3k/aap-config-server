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
| `RATE_LIMIT_ADMIN_RPS` / `RATE_LIMIT_ADMIN_BURST` | no | `0` / `0` | Admin endpoint token-bucket settings. Both must be positive to enable. |
| `RATE_LIMIT_SECRET_RESOLVE_RPS` / `RATE_LIMIT_SECRET_RESOLVE_BURST` | no | `0` / `0` | `resolve_secrets=true` token-bucket settings. Both must be positive to enable. |
| `RATE_LIMIT_WATCH_RPS` / `RATE_LIMIT_WATCH_BURST` | no | `0` / `0` | Config/env watch token-bucket settings. Both must be positive to enable. |
| `RATE_LIMIT_BATCH_RPS` / `RATE_LIMIT_BATCH_BURST` | no | `0` / `0` | Batch read token-bucket settings. Both must be positive to enable. |
| `RATE_LIMIT_READ_RPS` / `RATE_LIMIT_READ_BURST` | no | `0` / `0` | History endpoint token-bucket settings. **Recommended to enable** — history scans the full git log per request and has no authentication gate; an unconfigured server is susceptible to CPU-pinning DOS via concurrent history requests. |

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
- Git webhook refresh calls use the same admin API key boundary as other admin
  endpoints. `POST /api/v1/admin/git/webhook` discards the provider payload and
  calls `RefreshFromRepo` for the configured repo/branch, making duplicate
  webhook deliveries safe (`updated:false`).
- Optional token-bucket rate limits can protect admin, secret resolve, watch,
  and batch endpoint groups. Limited requests return `429 rate_limited` with
  a `Retry-After` header computed from the configured RPS; admin tokens are
  consumed only after successful API-key authentication.
  **Note:** Each limiter is a single global token bucket shared across all
  clients — it is not per-IP. A burst of reconnects (e.g. rolling pod restart)
  counts against the same bucket as normal traffic. Size `BURST` to absorb
  expected reconnect storms, or implement per-IP limiting in an upstream proxy
  when client-fairness is required.
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
- `POST /api/v1/admin/git/webhook` also uses `RefreshFromRepo` for immediate
  post-push refresh when a Git provider webhook is configured.
- `POST /api/v1/admin/reload` force-reloads even when HEAD did not move.

## Deployment

- Container builds are defined by `Dockerfile`.
- This repo owns the Config Server and Config Agent binaries, Docker image
  build targets, runtime configuration docs, and runbook guidance.
- Helm charts and Kubernetes manifests remain outside this repo unless a future
  decision explicitly moves deployment ownership here.
- Runtime network access should restrict unauthenticated config/env reads to trusted clients.

### Namespace topology

| Service | Namespace | Notes |
|---------|-----------|-------|
| Config Server, AAP Console | `aap-system` | Infra management services |
| Config Agent, workload pods (langfuse, litellm, keycloak) | `codei` | Workload namespace; may change |
| SealedSecret controller | `kube-system` | Default; unchanged |

**Config Agent placement rule:** Agent must run in the workload namespace (`codei`), not `aap-system`.
The Agent's ConfigMap/Secret/Lease targets all live in the workload namespace — placing the Agent
in the wrong namespace breaks leader election.

Agent `--config-server` flag must use the full FQDN:
```
--config-server=http://aap-config-server.aap-system.svc.cluster.local:8080
```

### Cross-namespace checklist (update when workload namespace changes)

When the workload namespace changes from `codei` to something else:

- [ ] **RoleBinding** — add a new `aap-config-server-sealedsecrets` RoleBinding in the new namespace
  (see _Config Server RBAC_ below); the old namespace binding can be removed once no workloads remain there
- [ ] **NetworkPolicy ingress** — update the `namespaceSelector` in the Config Server NetworkPolicy
  to include the new namespace label
- [ ] **Config Agent RBAC** — recreate ServiceAccount / Role / RoleBinding in the new namespace
- [ ] **`--config-server` FQDN** — unchanged; Config Server stays in `aap-system`

### Config Server RBAC — cross-namespace SealedSecret apply

Config Server applies SealedSecrets into the workload namespace, so its ServiceAccount (`aap-system`)
needs write access there. Use per-namespace RoleBindings rather than a ClusterRoleBinding.

```yaml
# ClusterRole — define once
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: aap-config-server-sealedsecrets
rules:
  - apiGroups: ["bitnami.com"]
    resources: ["sealedsecrets"]
    verbs: ["get", "list", "create", "update", "patch"]
---
# RoleBinding — one per workload namespace
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: aap-config-server-sealedsecrets
  namespace: codei          # repeat for each workload namespace
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: aap-config-server-sealedsecrets
subjects:
  - kind: ServiceAccount
    name: aap-config-server
    namespace: aap-system
```

A separate RoleBinding is also needed in `kube-system` for the SealedSecret controller
public-key proxy (`services/proxy` get).

### Image build

Build the Config Server image (default Dockerfile target):

```bash
make docker-build
# equivalent:
docker build --target config-server -t aap/config-server:latest .
```

Build the Config Agent image with the dedicated target:

```bash
make docker-build-agent
# equivalent:
docker build --target config-agent -t aap/config-agent:latest .
```

Tag and push to your registry before deploying:

```bash
docker tag aap/config-server:latest <registry>/aap/config-server:<version>
docker push <registry>/aap/config-server:<version>
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
  namespace: codei
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: litellm-config-agent
  namespace: codei
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
  namespace: codei
subjects:
  - kind: ServiceAccount
    name: litellm-config-agent
    namespace: codei
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
  namespace: codei
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
            - --config-server=http://aap-config-server.aap-system.svc.cluster.local:8080
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

### Config Server NetworkPolicy handoff example

The following snippet is a non-authoritative reference for the external
deployment system that owns NetworkPolicy manifests under `DEC-003`.
Adjust namespaces, pod selectors, and port ranges to match your cluster.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: aap-config-server
  namespace: aap-system
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: aap-config-server
  policyTypes:
    - Ingress
    - Egress
  ingress:
    # Allow Config Agent and admin clients on the HTTP API port.
    # List every workload namespace that hosts a Config Agent.
    # Without a 'from' clause, ingress is accepted cluster-wide — always add
    # explicit selectors in production.
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: codei  # workload namespace (update if namespace changes)
      ports:
        - port: 8080
          protocol: TCP
  egress:
    # Git remote over SSH (adjust port if using HTTPS/443)
    - ports:
        - port: 22
          protocol: TCP
    # TCP/443 egress covers: AAP Console API (CONSOLE_API_URL) and
    # Kubernetes API (SealedSecret lookup/apply). Scope to specific CIDRs
    # for the Console endpoint and cluster API server in production;
    # the ipBlock below is intentionally broad as a handoff baseline.
    - ports:
        - port: 443
          protocol: TCP
      to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              - 169.254.0.0/16
```

Network access requirements at runtime:

| Destination | Port | Required when |
|---|---|---|
| Git remote (SSH) | 22 | `GIT_SSH_KEY` set |
| Git remote (HTTPS) | 443 | `GIT_USERNAME`/`GIT_PASSWORD` set |
| AAP Console API | 443 | `CONSOLE_API_URL` set |
| Kubernetes API | 443 | secret writes with SealedSecret integration |

### External manifest ownership

Per `DEC-003`, Helm charts and Kubernetes manifests (Deployment, Service,
ServiceAccount, RBAC, NetworkPolicy, PodDisruptionBudget) remain in the
external deployment repo. This repo provides:

- Binary and Docker image build targets (`Dockerfile`, `Makefile`).
- Runtime env var reference (`docs/current/OPERATIONS.md` env vars table).
- Non-authoritative RBAC and NetworkPolicy handoff examples (above and
  `### Config Agent RBAC and deployment handoff example`).
- Runbook guidance (`docs/05_RUNBOOK.md`).

The external deployment owner is responsible for:

- Choosing image tags and registry paths.
- Applying RBAC, NetworkPolicy, and resource quota manifests.
- Managing `API_KEY`, Git auth secrets, and `CONFIG_AGENT_API_KEY`.
- Configuring rate limits (`RATE_LIMIT_*_RPS` / `RATE_LIMIT_*_BURST`) appropriate for their cluster.

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
2. Fix malformed YAML, schema errors, or dirty `configs/` checkout drift. Schema
   errors include unknown fields in known YAML envelopes, duplicate validated
   keys, invalid node shapes, and non-shell-compatible env var names.
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
