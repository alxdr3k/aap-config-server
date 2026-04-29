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

`sealed-secrets/` and `_defaults` are not loaded into the current serving
snapshot.

## Current entities

| Entity | Purpose | Source |
|---|---|---|
| `ServiceKey` | Unique `org/project/service` identifier. | `internal/store/types.go` |
| `ServiceData` | In-memory aggregate for one service. | `internal/store/types.go` |
| `ServiceConfig` | Parsed `config.yaml` with metadata and arbitrary config map. | `internal/parser/types.go` |
| `EnvVarsConfig` | Parsed `env_vars.yaml` with plain env vars and secret refs. | `internal/parser/types.go` |
| `SecretsConfig` | Parsed `secrets.yaml` metadata; no secret plaintext. | `internal/parser/types.go` |
| `secret.RuntimeConfig` | Runtime knobs for future secret mount, SealedSecret, K8s apply, and audit adapters. | `internal/secret/types.go` |
| `secret.Reference` / `secret.Value` | Boundary types for future plaintext secret reads; values are copied and can be best-effort zeroed. | `internal/secret/types.go` |
| `ChangeRequest` | Internal representation of admin write input. | `internal/store/types.go` |
| `DeleteRequest` | Internal representation of admin delete input. | `internal/store/types.go` |
| `StoreStatus` | Runtime status exposed through `/api/v1/status`. | `internal/store/types.go` |

## Storage

| Store | Purpose | Source |
|---|---|---|
| External Git repo | Canonical persisted config data. | `GIT_URL`, `internal/gitops/` |
| Local Git clone | Working tree used for pull/commit/push/snapshot. | `GIT_LOCAL_PATH` |
| Atomic in-memory snapshot | Lock-free serving view for reads. | `internal/store/store.go` |

## YAML validation

- `config.yaml` and `env_vars.yaml` require `metadata.service`, `metadata.org`, and `metadata.project`.
- `secrets.yaml` entries require `id` and a complete `k8s_secret` pointer: `name`, `namespace`, and `key`.
- Invalid YAML or missing required fields fail reload closed.

## Lifecycle states

| Entity | States | Notes |
|---|---|---|
| Store snapshot | loaded, stale-last-known-good | Reload only swaps on full parse success. |
| Store status | ready, degraded | Degraded means the latest reload failed but the previous snapshot remains available. |
| Admin write | committed, committed_but_reload_failed | The second state means Git push succeeded but memory reload failed. |
| Admin delete | deleted, deleted_but_reload_failed | The second state means Git delete succeeded but memory reload failed. |

## Needs audit

- No generated reference docs currently exist under `docs/generated/`.
- Planned SealedSecret manifests, K8s apply payloads, and Config Agent data
  models are target design only.
