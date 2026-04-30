# Runtime Flow

Status: active.

## Current implemented flow

1. `cmd/config-server/main.go` loads runtime config from env/flags.
2. Startup validates required config, including `GIT_URL`, secret runtime
   boundary settings, and `API_KEY` unless `ALLOW_UNAUTHENTICATED_DEV=true`.
3. If `CONSOLE_API_URL` is configured, startup attempts the AAP Console App
   Registry bootstrap with bounded retry; failure is recorded but does not
   abort startup.
4. `gitops.Repo` opens or clones the configured Git repo/branch.
5. `store.LoadFromRepo` performs one startup pull, then snapshots `configs/`.
6. The store parses `config.yaml`, `env_vars.yaml`, and `secrets.yaml` files under `configs/orgs/{org}/projects/{project}/services/{service}/`.
7. If all parsed files are valid, the store atomically swaps the serving snapshot.
8. HTTP handlers serve reads from memory and admin writes through the store.
9. A background poll loop calls `RefreshFromRepo` at `GIT_POLL_INTERVAL`.

Admin writes, deletes, background refreshes, and Git worktree mutations are
serialized globally in Phase-1. `ADR-005` records this as the accepted current
implementation; `ADR-003` remains the future service-level mutex target design.

## Config Agent bootstrap flow

`cmd/config-agent` currently supports local dry-run mode only:

1. Load agent runtime config from env/flags, requiring `CONFIG_SERVER_URL`,
   service identity (`CONFIG_AGENT_ORG`, `CONFIG_AGENT_PROJECT`,
   `CONFIG_AGENT_SERVICE` or matching flags), and `--dry-run`.
2. Create a bounded Config Server API client with the configured HTTP timeout.
3. Fetch `GET .../config` and `GET .../env_vars`; `--resolve-secrets` adds
   `resolve_secrets=true` and requires `CONFIG_AGENT_API_KEY` or `API_KEY`.
4. Log a summary with counts for config keys, plain env vars, secret refs, and
   resolved secrets. Secret values are not printed.

The dry-run CLI does not start leader election.

## Config Agent leader election

`internal/agent` wraps client-go `leaderelection` with a Kubernetes `LeaseLock`
for one-active-Agent semantics. The wrapper validates lease namespace/name,
replica identity, and timing settings before creating the elector. It blocks
until context cancellation, invokes start/stop/new-leader callbacks, and uses
`ReleaseOnCancel=true` by default so a standby replica can take over promptly
after a clean shutdown.

## Config Agent fetch loop

`internal/agent` can poll Config Server read APIs without the future watch
endpoints. The loop fetches both `config` and `env_vars`, rejects a poll if the
two reads came from different repo revisions, tracks the last successfully
handled content hashes, treats the first snapshot as changed, and only advances
state after the caller's handler succeeds. Fetch or handler failures retry with
bounded exponential backoff; successful polls reset backoff and wait
`PollInterval`.

## Config Agent rendering

`internal/agent` can render fetched snapshots into the payloads that future
Kubernetes apply code will write:

- Config snapshots render the `config` object as deterministic native YAML for
  the target ConfigMap. Strings such as `os.environ/API_KEY` are preserved as
  literal config values and are not resolved or masked by the renderer.
- Env var snapshots render to `env.sh` as sorted `export KEY='VALUE'` lines for
  the target Secret. The renderer requires resolved snapshots, rejects
  remaining `secret_refs`, rejects duplicate plain/secret keys, and validates
  shell-compatible env var names.

ConfigMap/Secret apply is implemented separately below; Deployment patching and
debounce behavior remain planned Agent slices.

## Config Agent ConfigMap/Secret apply

`internal/agent` can apply rendered payloads to Kubernetes through a typed
client-go client:

- `config.yaml` is written to the configured ConfigMap data key.
- `env.sh` is written to the configured Secret data key as bytes.
- Existing resources are patched with merge patches so unrelated data keys are
  preserved; missing resources are created.
- The target namespace, ConfigMap name, and Secret name are required and
  validated before any Kubernetes API call.

Deployment patching and debounce behavior remain planned Agent slices.

## Implemented API surface

- `GET /healthz`
- `GET /readyz`
- `GET /api/v1/status`
- `GET /api/v1/orgs`
- `GET /api/v1/orgs/{org}/projects`
- `GET /api/v1/orgs/{org}/projects/{project}/services`
- `GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config`
- `GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars`
  (`resolve_secrets=true` requires auth and returns `Cache-Control: no-store`)
- `GET /api/v1/orgs/{org}/projects/{project}/services/{service}/secrets`
- `POST /api/v1/admin/changes`
- `DELETE /api/v1/admin/changes`
- `POST /api/v1/admin/reload`
- `POST /api/v1/admin/app-registry/webhook`

## Auth boundary

- Admin endpoints require `Authorization: Bearer <API_KEY>` or `X-API-Key`.
- Secret metadata read also requires auth.
- Env var reads with `resolve_secrets=true` require auth because they return
  plaintext secret values.
- Config reads and unresolved env var reads are currently unauthenticated;
  deployment must restrict network access.
- Empty `API_KEY` is only allowed with explicit `ALLOW_UNAUTHENTICATED_DEV=true`.

## Current flow

- `internal/secret` now defines adapter-neutral boundaries for mounted volume
  reads, SealedSecret sealing, K8s apply, and non-sensitive audit logging.
- `internal/secret.FileVolumeReader` can read mounted K8s Secret files and
  refresh cached values from fsnotify events; HTTP `resolve_secrets=true`
  uses it to resolve env var secret refs.
- `internal/secret.DeterministicSealer` can generate stable Bitnami
  SealedSecret YAML from an injected encryptor.
- `internal/secret.ControllerPublicKeyProvider` and
  `internal/secret.PublicKeyEncryptor` can fetch the SealedSecret controller
  certificate through the Kubernetes service proxy and encrypt values using
  Bitnami's hybrid encryption format.
- `internal/secret.DynamicApplier` can create/update Bitnami SealedSecret
  objects through a Kubernetes dynamic client.
- `POST /api/v1/admin/changes` accepts secret values when secret adapters are
  configured, generates SealedSecrets, commits encrypted manifests with
  metadata, applies them to Kubernetes, and reports apply/reload failures
  explicitly.
- Secret handling paths emit non-sensitive audit events when
  `SECRET_AUDIT_LOG_ENABLED=true`; emitted fields are action, result,
  service identity, and secret IDs, never plaintext values.
- If `CONSOLE_API_URL` is configured, startup fetches
  `GET /api/v1/apps?all=true` from AAP Console into an in-memory App Registry
  cache using bounded exponential backoff. Final failure is logged and the
  process continues with the existing cache.
- `POST /api/v1/admin/app-registry/webhook` lets AAP Console update the App
  Registry cache with authenticated create/update/upsert/delete notifications;
  each event must include RFC3339 `updated_at` so stale async retries are
  ignored, including older upserts that arrive after a newer delete.
- `/api/v1/status` exposes App Registry state under `app_registry` with
  `status`, `apps_loaded`, `last_loaded_at`, `last_updated_at`, and
  `last_load_error` when present. Registry-only load failure is reported as a
  degraded component, but `/readyz` remains tied to process/store readiness so
  Config Server can serve Git-backed config even when Console is temporarily
  unavailable. If startup bootstrap is not configured, webhook updates can
  change `apps_loaded` and `last_updated_at`, but the status remains
  `not_configured` to show that no full Console snapshot was loaded.
- Config Agent Kubernetes apply/rollout behavior, watch/history/revert, and
  inheritance are target design only.

## Failure modes

| Failure | Expected handling |
|---|---|
| Missing `GIT_URL` | Startup fails during config validation. |
| Missing `API_KEY` without dev opt-in | Startup fails closed. |
| Non-positive `GIT_POLL_INTERVAL` | Startup fails validation. |
| Invalid secret runtime setting | Startup fails during config validation. |
| Startup pull transient failure | Warning logged; on-disk checkout is parsed. |
| YAML parse/validation failure during reload | Snapshot is not swapped; last-known-good snapshot keeps serving. |
| Degraded store | `/readyz` returns 503 and `/api/v1/status` reports `is_degraded`. |
| Admin write succeeds but reload fails | Response is `503 committed_but_reload_failed`; Git commit remains. |
| Admin secret write succeeds but SealedSecret apply fails | Response is `503 committed_but_apply_failed`; encrypted Git commit remains and `apply_error` is returned. |
| App Registry startup load fails after configured attempts | Startup continues with the existing registry cache, `/api/v1/status` reports `app_registry.status=degraded`, and `/readyz` remains 200 when the store is otherwise ready. |
| App Registry webhook without valid API key | Request fails with `401 unauthorized`; cache is unchanged. |
| Admin delete succeeds but reload fails | Response is `503 deleted_but_reload_failed`; Git delete remains. |
| Dirty `configs/` worktree during snapshot | Reload fails closed to avoid serving data not represented by HEAD. |
| Unknown admin JSON field | Request fails with `400 invalid_body`. |
| `resolve_secrets=true` without valid API key | Request fails with `401 unauthorized`. |
| Resolved env var secret response | Mounted secret files are refreshed before response; response includes `Cache-Control: no-store` and omits `ETag`. |
| Duplicate `secrets.yaml` IDs during resolve | Request fails instead of choosing an ambiguous mounted secret value. |
| `secrets` field on admin write without configured adapters | Request fails validation before Git commit. |
| Unsafe mounted secret reference | Volume reader rejects it before filesystem access. |
| Invalid SealedSecret generation input | Store/sealer reject missing path identity, Kubernetes-incompatible namespace/name, missing data, or path-unsafe keys before emitting YAML or committing Git files. |
| SealedSecret public-key lookup/encryption failure | Encryptor returns context-rich controller lookup, certificate parse, or encryption errors without logging plaintext values. |
| Invalid SealedSecret apply manifest | Applier rejects missing YAML, wrong kind, or name/namespace mismatches before K8s API calls. |
| K8s SealedSecret apply failure | Applier returns context-rich get/create/update errors and respects apply timeout/cancellation. |

## Debug path

1. Check `/healthz` for process liveness.
2. Check `/readyz` for readiness/degraded state.
3. Check `/api/v1/status` for `version`, `services_loaded`, `last_reload_at`, `last_reload_error`, and `app_registry`.
4. Inspect logs for git pull/push/reload errors.
5. Validate the config repo `configs/` tree against parser expectations.
6. Use `POST /api/v1/admin/reload` after fixing malformed YAML or dirty checkout drift.
