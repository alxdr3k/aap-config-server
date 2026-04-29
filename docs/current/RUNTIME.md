# Runtime Flow

Status: active.

## Current implemented flow

1. `cmd/config-server/main.go` loads runtime config from env/flags.
2. Startup validates required config, including `GIT_URL`, secret runtime
   boundary settings, and `API_KEY` unless `ALLOW_UNAUTHENTICATED_DEV=true`.
3. `gitops.Repo` opens or clones the configured Git repo/branch.
4. `store.LoadFromRepo` performs one startup pull, then snapshots `configs/`.
5. The store parses `config.yaml`, `env_vars.yaml`, and `secrets.yaml` files under `configs/orgs/{org}/projects/{project}/services/{service}/`.
6. If all parsed files are valid, the store atomically swaps the serving snapshot.
7. HTTP handlers serve reads from memory and admin writes through the store.
8. A background poll loop calls `RefreshFromRepo` at `GIT_POLL_INTERVAL`.

Admin writes, deletes, background refreshes, and Git worktree mutations are
serialized globally in Phase-1. `ADR-005` records this as the accepted current
implementation; `ADR-003` remains the future service-level mutex target design.

## Implemented API surface

- `GET /healthz`
- `GET /readyz`
- `GET /api/v1/status`
- `GET /api/v1/orgs`
- `GET /api/v1/orgs/{org}/projects`
- `GET /api/v1/orgs/{org}/projects/{project}/services`
- `GET /api/v1/orgs/{org}/projects/{project}/services/{service}/config`
- `GET /api/v1/orgs/{org}/projects/{project}/services/{service}/env_vars`
- `GET /api/v1/orgs/{org}/projects/{project}/services/{service}/secrets`
- `POST /api/v1/admin/changes`
- `DELETE /api/v1/admin/changes`
- `POST /api/v1/admin/reload`

## Auth boundary

- Admin endpoints require `Authorization: Bearer <API_KEY>` or `X-API-Key`.
- Secret metadata read also requires auth.
- Config/env var reads are currently unauthenticated; deployment must restrict network access.
- Empty `API_KEY` is only allowed with explicit `ALLOW_UNAUTHENTICATED_DEV=true`.

## Planned flow

- `internal/secret` now defines adapter-neutral boundaries for mounted volume
  reads, SealedSecret sealing, K8s apply, and non-sensitive audit logging.
- `internal/secret.FileVolumeReader` can read mounted K8s Secret files and
  refresh cached values from fsnotify events; HTTP `resolve_secrets=true` is
  still planned.
- `internal/secret.DeterministicSealer` can generate stable Bitnami
  SealedSecret YAML from an injected encryptor.
- `internal/secret.ControllerPublicKeyProvider` and
  `internal/secret.PublicKeyEncryptor` can fetch the SealedSecret controller
  certificate through the Kubernetes service proxy and encrypt values using
  Bitnami's hybrid encryption format.
- `internal/secret.DynamicApplier` can create/update Bitnami SealedSecret
  objects through a Kubernetes dynamic client; admin write wiring is still
  planned.
- `POST /api/v1/admin/changes` will eventually accept secret values, generate SealedSecrets, commit encrypted manifests, apply them to Kubernetes, and support secret resolution.
- Config Agent, registry webhook, watch/history/revert, and inheritance are target design only.

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
| Admin delete succeeds but reload fails | Response is `503 deleted_but_reload_failed`; Git delete remains. |
| Dirty `configs/` worktree during snapshot | Reload fails closed to avoid serving data not represented by HEAD. |
| Unknown admin JSON field | Request fails with `400 invalid_body`. |
| `secrets` field on admin write | Explicitly rejected in Phase-1 with 400. |
| Unsafe mounted secret reference | Volume reader rejects it before filesystem access. |
| Invalid SealedSecret generation input | Sealer rejects missing path identity, namespace/name/data, or path-unsafe keys before emitting YAML. |
| SealedSecret public-key lookup/encryption failure | Encryptor returns context-rich controller lookup, certificate parse, or encryption errors without logging plaintext values. |
| Invalid SealedSecret apply manifest | Applier rejects missing YAML, wrong kind, or name/namespace mismatches before K8s API calls. |
| K8s SealedSecret apply failure | Applier returns context-rich get/create/update errors and respects apply timeout/cancellation. |

## Debug path

1. Check `/healthz` for process liveness.
2. Check `/readyz` for readiness/degraded state.
3. Check `/api/v1/status` for `version`, `services_loaded`, `last_reload_at`, and `last_reload_error`.
4. Inspect logs for git pull/push/reload errors.
5. Validate the config repo `configs/` tree against parser expectations.
6. Use `POST /api/v1/admin/reload` after fixing malformed YAML or dirty checkout drift.
