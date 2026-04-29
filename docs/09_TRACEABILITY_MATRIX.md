# 09 Traceability Matrix

Question ↔ Decision ↔ Requirement ↔ Gate/Test ↔ Milestone/Track/Phase/Slice 연결.

## How to use

- 한 줄 = 하나의 trace path.
- 새 결정이 무엇에 영향을 주는지 명확히 남기는 용도.
- 완료 slice와 gate evidence를 연결해 "landed"와 "accepted"의 근거를 남기는 용도.
- Weekly로 누락 없이 채워졌는지 점검.

## Matrix

| TRACE-ID | Question | Decision / ADR | Requirement | Gate / Test | Milestone | Track | Phase | Slice | Notes |
|---|---|---|---|---|---|---|---|---|---|
| `TRACE-001` |  |  | `FR-1` | `AC-003` / `TEST-003` | `P0-M1` | `CORE` | `CORE-1A` | `CORE-1A.3` | Git-backed in-memory store. |
| `TRACE-002` |  |  | `FR-2`, `FR-3`, `FR-11`, `FR-12` | `AC-004` / `TEST-004` | `P0-M1` | `CORE` | `CORE-1A` | `CORE-1A.4` | Read, discovery, secret metadata APIs. |
| `TRACE-003` |  | `ADR-003` | `FR-4`, `FR-5` | `AC-005` / `TEST-005` | `P0-M1` | `CORE` | `CORE-1A` | `CORE-1A.5` | Admin write/delete through Git. |
| `TRACE-004` |  |  | `FR-15` | `AC-007` / `TEST-007` | `P0-M2` | `OPS` | `OPS-1A` | `OPS-1A.2` | Degraded readiness/status and force reload. |
| `TRACE-005` |  |  | `FR-16`, `FR-17` | `AC-006` / `TEST-006` | `P0-M2` | `OPS` | `OPS-1A` | `OPS-1A.1` | API key auth boundary. |
| `TRACE-006` | `Q-002` | `ADR-005` | `FR-4` | `AC-005` / `TEST-005` | `P0-M1` | `CORE` | `CORE-1A` | `CORE-1A.5` | Phase-1 accepts global Git/store serialization; ADR-003 remains target design. |
| `TRACE-007` |  | `ADR-004` | `FR-7`, `FR-17` | `AC-020` / `TEST-020` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.1`~`SECRET-1A.8` | Implemented SealedSecret/write/resolve/security path. |
| `TRACE-008` |  | `ADR-001`, `ADR-002` | `FR-9` | `AC-030` / `TEST-030` | `P1-M2` | `AGENT` | `AGENT-1A` | `AGENT-1A.1`~`AGENT-1A.8` | Leaf-planned Config Agent rollout path. |
| `TRACE-009` | `Q-001` | `DEC-001`, `DEC-002` | Documentation migration | `AC-014` | `P0-M3` | `DOC` | `DOC-1A` | `DOC-1A.1` | Boilerplate docs adopted; `FR-*` remains canonical. |
| `TRACE-010` |  | `DEC-001` | Documentation workflow | `AC-015` | `P0-M3` | `DOC` | `DOC-1A` | `DOC-1A.2` | PR template and doc freshness soft-check. |
| `TRACE-011` | `Q-003` | `DEC-003` | Deployment ownership | operations/runbook docs | `P0-M3` | `DOC` | `DOC-1A` | `DOC-1A.1` | Repo owns binary/image/runtime docs; Helm/K8s manifests remain external. |
| `TRACE-012` |  |  | `FR-8` | `AC-021` / `TEST-021` | `P1-M1` | `REGISTRY` | `REGISTRY-1A` | `REGISTRY-1A.1`~`REGISTRY-1A.3` | Console App Registry bootstrap, webhook cache, and readiness/status track. |
| `TRACE-013` |  |  | `FR-6` | `AC-040` / `TEST-040` | `P1-M3` | `EXT` | `EXT-1A` | `EXT-1A.1`~`EXT-1A.3` | Leaf-planned config/env watch APIs. |
| `TRACE-014` |  |  | `FR-13`, `FR-14` | `AC-040` / `TEST-040` | `P1-M3` | `EXT` | `EXT-1B` | `EXT-1B.1`~`EXT-1B.5` | Leaf-planned history, versioned reads, and revert path. |
| `TRACE-015` |  |  | `FR-10` | `AC-040` / `TEST-040` | `P1-M3` | `EXT` | `EXT-1C` | `EXT-1C.1`~`EXT-1C.4` | Leaf-planned inheritance and merge semantics. |
| `TRACE-016` |  |  | Operational extensions | `AC-041` / `TEST-041` | `P1-M3` | `EXT` | `EXT-1D` | `EXT-1D.1`~`EXT-1D.5` | Leaf-planned ETag, gzip, batch, metrics, and Git webhook work. |
| `TRACE-017` | `Q-003` | `DEC-003` | Production hardening | `AC-042` / `TEST-042` | `P1-M3` | `HARDEN` | `HARDEN-1A` | `HARDEN-1A.1`~`HARDEN-1A.5` | Leaf-planned schema, rate, integration/load, and deployment handoff work. |
| `TRACE-018` |  | `ADR-004` | `FR-7`, `FR-17` | `AC-020` / `internal/config/config_test.go`, `internal/secret/types_test.go` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.1` | Secret runtime config and adapter boundaries landed; audit implementation is tracked by `TRACE-025`. |
| `TRACE-019` |  | `ADR-004` | `FR-7`, `FR-17` | `AC-020` / `internal/secret/volume_test.go` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.2` | Mounted K8s Secret file reader and fsnotify refresh events landed before HTTP secret resolve wiring. |
| `TRACE-020` |  | `ADR-004` | `FR-7`, `FR-17` | `AC-020` / `internal/secret/sealed_test.go` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.3` | Deterministic SealedSecret YAML generator landed as the encryption-boundary slice before public-key lookup and admin wiring. |
| `TRACE-021` |  | `ADR-004` | `FR-7`, `FR-17` | `AC-020` / `internal/secret/apply_test.go` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.4` | K8s dynamic-client SealedSecret create/update adapter landed before admin secret write integration. |
| `TRACE-022` |  | `ADR-004` | `FR-7`, `FR-17` | `AC-020` / `internal/secret/encrypt_test.go` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.5` | SealedSecret controller public-key lookup and Bitnami hybrid encryptor wiring landed before admin secret write integration. |
| `TRACE-023` |  | `ADR-004` | `FR-4`, `FR-7`, `FR-17` | `AC-020` / `internal/store/store_test.go`, `internal/handler/handler_test.go` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.6` | Admin secret writes now commit secrets metadata plus SealedSecret manifests, apply them to Kubernetes, and report apply/reload outcomes explicitly. |
| `TRACE-024` |  | `ADR-004` | `FR-3`, `FR-7`, `FR-17` | `AC-020` / `internal/handler/handler_test.go` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.7` | `resolve_secrets=true` env var reads now require auth, read mounted K8s Secret values, and return no-store responses without ETag. |
| `TRACE-025` |  | `ADR-004` | `FR-7`, `FR-17` | `AC-020` / `internal/store/store_test.go`, `internal/handler/handler_test.go` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.8` | Non-sensitive secret audit logging now covers admin secret writes and resolved env var reads; tests assert audit logs do not include plaintext and secret values are best-effort cleaned up. |
| `TRACE-026` |  |  | `FR-8` | `AC-021` / `internal/registry/*_test.go`, `internal/config/config_test.go` | `P1-M1` | `REGISTRY` | `REGISTRY-1A` | `REGISTRY-1A.1` | AAP Console App Registry startup bootstrap now has runtime config, HTTP client decoding, in-memory cache replacement, and bounded exponential backoff with graceful final failure. |
| `TRACE-027` |  |  | `FR-8` | `AC-021` / `internal/handler/handler_test.go`, `internal/registry/*_test.go` | `P1-M1` | `REGISTRY` | `REGISTRY-1A` | `REGISTRY-1A.2` | Authenticated App Registry webhook now supports Console-driven cache create/update/upsert and idempotent delete semantics. |

## Invariants

- Every `must` requirement needs at least one AC.
- Every accepted DEC/ADR must identify impacted requirement/design/runbook areas.
- Every completed slice should link to at least one trace row.
- Every `accepted` milestone needs gate/test evidence.

## Gaps

- Some target PRD/HLD features have leaf roadmap slices and future gates but no code/tests yet.
