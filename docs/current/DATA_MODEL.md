# Data Model

Status: active.

## Source of truth

Code, tests, and the external config Git repository are authoritative.

This document is a human-readable map.

## Repository layout

Config files are discovered under:

```text
configs/orgs/{org}/projects/{project}/services/{service}/
```

Recognized files:

- `config.yaml`
- `env_vars.yaml`
- `secrets.yaml`
- `_defaults/common.yaml` at global, org, and project levels

`sealed-secrets/` is not loaded into the current serving snapshot. `_defaults`
files are parsed into defaults source metadata, but their values are not yet
merged into read responses.

## Current entities

| Entity | Purpose | Source |
|---|---|---|
| `ServiceKey` | Unique `org/project/service` identifier. | `internal/store/types.go` |
| `ServiceData` | In-memory aggregate for one service; also used as the typed result for historical config/env reads. | `internal/store/types.go` |
| `ServiceConfig` | Parsed `config.yaml` with metadata and arbitrary config map. | `internal/parser/types.go` |
| `EnvVarsConfig` | Parsed `env_vars.yaml` with plain env vars and secret refs. | `internal/parser/types.go` |
| `SecretsConfig` | Parsed `secrets.yaml` metadata; no secret plaintext. | `internal/parser/types.go` |
| `DefaultsConfig` | Parsed `_defaults/common.yaml` config/env defaults. | `internal/parser/types.go` |
| `DefaultsSource` | Store metadata describing inherited defaults sources available to a service. | `internal/store/types.go` |
| `secret.RuntimeConfig` | Runtime knobs for secret mount, SealedSecret, K8s apply, and audit adapters. | `internal/secret/types.go` |
| `secret.Reference` / `secret.Value` | Boundary types for plaintext secret reads/writes; values are copied and can be best-effort zeroed. | `internal/secret/types.go` |
| `secret.FileVolumeReader` | Mounted K8s Secret file reader/cache with fsnotify refresh events. | `internal/secret/volume.go` |
| `secret.DeterministicSealer` | Adapter that turns plaintext secret values into deterministic Bitnami SealedSecret YAML through an injected encryptor. | `internal/secret/sealed.go` |
| `secret.ControllerPublicKeyProvider` / `secret.PublicKeyEncryptor` | Controller certificate lookup and Bitnami hybrid encryption for SealedSecret data items. | `internal/secret/encrypt.go` |
| `secret.DynamicApplier` | Adapter that creates or updates Bitnami SealedSecret objects through a Kubernetes dynamic client. | `internal/secret/apply.go` |
| `secret.AuditEvent` / `secret.SlogAuditor` | Non-sensitive audit event boundary and slog-backed implementation for secret write/resolve activity. | `internal/secret/types.go`, `internal/secret/audit.go` |
| `registry.App` / `registry.Cache` | Console-owned app registration record and in-memory registry snapshot. | `internal/registry/types.go`, `internal/registry/cache.go` |
| `registry.ConsoleClient` | HTTP client that loads `GET /api/v1/apps?all=true` from AAP Console. | `internal/registry/client.go` |
| `store.SecretWrite` | Admin write boundary for plaintext secret values grouped by K8s Secret name before sealing. | `internal/store/types.go` |
| `ChangeRequest` | Internal representation of admin write input. | `internal/store/types.go` |
| `DeleteRequest` | Internal representation of admin delete input. | `internal/store/types.go` |
| `RevertRequest` / `RevertPlan` / `RevertResult` | Store-level revert target validation input, non-mutating service-file restore plan, and forward-only revert application result. | `internal/store/types.go` |
| `HistoryOptions` / `HistoryEntry` | Store-level service history query options and response records for the public history API. | `internal/store/types.go` |
| `StoreStatus` | Runtime status exposed through `/api/v1/status`. | `internal/store/types.go` |
| `gitops.ServiceHistoryEntry` / `gitops.ServiceFileChange` / `gitops.ServiceFileContent` | Git commit history records, service-scoped changed-file classifications, and historical file snapshots for history/revert features. | `internal/gitops/repo.go` |
| Store version wait primitive | In-memory notification channel used by config/env watch endpoints to wait until the loaded Git version changes. | `internal/store/store.go` |
| Resource-scoped version tokens | Per-service config/env version tokens retained across reloads until each resource payload changes. | `internal/store/types.go`, `internal/store/store.go` |

## Storage

| Store | Purpose | Source |
|---|---|---|
| External Git repo | Canonical persisted config data. | `GIT_URL`, `internal/gitops/` |
| Local Git clone | Working tree used for pull/commit/push/snapshot. | `GIT_LOCAL_PATH` |
| Atomic in-memory snapshot | Lock-free serving view for reads. | `internal/store/store.go` |

## YAML validation

- `config.yaml` and `env_vars.yaml` require `metadata.service`, `metadata.org`, and `metadata.project`.
- `secrets.yaml` entries require `id` and a complete `k8s_secret` pointer: `name`, `namespace`, and `key`.
- Mounted secret reads reject empty or path-unsafe reference segments before
  resolving `{SECRET_MOUNT_PATH}/{namespace}/{name}/{key}`.
- SealedSecret generation sorts data keys and emits stable YAML field order;
  path identity and K8s target segments are validated before plaintext values
  are passed to the injected encryptor.
- The sealing scope is passed into the encryptor request and also emitted in
  manifest annotations so ciphertext and metadata stay aligned.
- Public-key encryption fetches the controller service certificate from
  `/v1/cert.pem`, validates an RSA certificate, and uses Bitnami's
  hybrid-encryption format with scope-compatible labels.
- Generated SealedSecret manifest paths use
  `sealed-secrets/{namespace}/{name}.yaml` under the service directory to avoid
  cross-namespace filename collisions.
- SealedSecret apply validates manifest kind/name/namespace before create/update
  through the `bitnami.com/v1alpha1` `sealedsecrets` resource.
- Admin secret writes merge existing `secrets.yaml` metadata with new plaintext
  values, commit metadata plus SealedSecret manifests together, and never write
  plaintext values to Git.
- Resolved env var reads map `env_vars.secret_refs` IDs through `secrets.yaml`
  metadata and refresh mounted files through `secret.VolumeReader`; responses
  are no-store and omit ETag.
- Secret audit events carry action, result, service identity, and secret IDs
  only. Plaintext values remain confined to `secret.Value` boundaries and HTTP
  responses for authenticated `resolve_secrets=true` calls.
- App Registry startup load replaces the in-memory registry cache from Console
  API data. Final bootstrap failure records the error but preserves the
  existing cache and is visible through `/api/v1/status`.
- App Registry webhook updates perform per-app upsert or idempotent delete
  against the same in-memory cache; Console remains the source of truth.
  Webhook events require RFC3339 `updated_at`, and stale events older than the
  current cache entry or latest delete watermark are ignored.
- Invalid YAML or missing required fields fail reload closed.

## Lifecycle states

| Entity | States | Notes |
|---|---|---|
| Store snapshot | loaded, stale-last-known-good | Reload only swaps on full parse success. |
| Store version waiter | waiting, changed, canceled | Waiters return immediately for stale resource versions, wake after successful snapshot version changes, and do not wake on failed reloads that keep serving last-known-good data. Watch handlers re-check resource-scoped versions and map unchanged timeout to `304 Not Modified`. |
| Store status | ready, degraded | Degraded means the latest reload failed but the previous snapshot remains available. |
| App Registry cache | not_configured, ok, degraded | `ok` means a full Console snapshot loaded. `not_configured` is preserved when only webhook updates arrive without startup bootstrap. Degraded means the last Console full load failed; webhook updates still record `last_updated_at`, while the load failure remains visible until a later full load succeeds. `/readyz` is not failed for registry-only degradation. |
| Config Agent leader lease | active, standby | A Kubernetes `Lease` elects one active Agent replica. Standby replicas observe the same lease and take over after the holder releases or expires. |
| Config Agent fetch state | handled, changed | The fetch loop verifies config/env reads come from the same repo revision, tracks the last successfully handled content hashes, and treats the first snapshot or content hash changes as changed. Failed handlers do not advance the handled state. |
| Config Agent rendered payloads | config YAML, env.sh | The renderer converts a fetched config object into deterministic native YAML while preserving `os.environ/...` strings, and converts resolved env vars into sorted shell exports for the target Secret. |
| Config Agent apply target | namespace, ConfigMap, Secret | The apply adapter requires concrete resource names, writes `config.yaml` to the ConfigMap and `env.sh` to the Secret, and patches existing resources without replacing unrelated data keys. |
| Config Agent rollout patch | Deployment annotation update | The rollout patcher writes a payload hash and restart timestamp to the target Deployment pod template annotations, triggering Kubernetes rolling update behavior. |
| Config Agent debounce state | idle, cooldown, pending | The debounce state machine applies the first change immediately, batches cooldown-window changes until quiet-period or max-wait, and starts a new cooldown after each apply. |
| Admin write | committed, committed_but_apply_failed, committed_but_reload_failed, committed_but_apply_and_reload_failed | Non-committed validation/sealing failures happen before Git writes; post-commit apply/reload failures are explicit. |
| Admin delete | deleted, deleted_but_reload_failed | The second state means Git delete succeeded but memory reload failed. |

## Needs audit

- No generated reference docs currently exist under `docs/generated/`.
- Config Agent live non-dry-run entrypoint wiring is target design only; image
  build targets, RBAC/deployment handoff examples, bootstrap Config Server
  response DTOs, leader election config, fetch loop state, rendered payloads,
  ConfigMap/Secret apply targets, rollout patches, debounce state, and
  fake-client e2e smoke coverage live under the current repo docs/code.
