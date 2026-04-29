# 07 Questions Register

아직 답이 정해지지 않은 열린 질문.

## How to use

1. 질문이 생기면 여기에 `Q-###`로 기록.
2. 제안된 답(Proposed Answer)을 아래에 기록.
3. 승인되면 `08_DECISION_REGISTER.md`(작은 결정) 또는 `adr/`(큰 결정)으로 승격.
4. 질문 항목의 status를 `resolved`로 바꾸고 decision ID를 링크.

## Questions

### Q-001: Should existing `FR-*` IDs migrate to boilerplate `REQ-*` IDs?

- Opened: 2026-04-29
- Owner: maintainers
- Status: open
- Proposed Answer: Preserve `FR-*` during this migration and optionally add `REQ-*` aliases later if the project needs boilerplate-native requirement IDs.
- Blocks: full PRD normalization
- Resolution: pending

**Context**

The existing PRD/HLD and ADR links already use `FR-1` through `FR-17`.
Renumbering immediately would create avoidable link churn.

**Discussion**

- `FR-*` is stable and already meaningful.
- Boilerplate examples use `REQ-*`, but the templates do not require immediate renumbering.
- Traceability can map either vocabulary.

---

### Q-002: Should ADR-003 be superseded or should implementation move from global git mutex to service-level mutex?

- Opened: 2026-04-29
- Owner: maintainers
- Status: open
- Proposed Answer: Keep current global mutex documented as Phase-1 simplification, then decide whether service-level concurrency is worth implementing before high write volume.
- Blocks: `CORE` concurrency hardening
- Resolution: pending

**Context**

ADR-003 accepts service-level mutex + pull-rebase. Current `internal/gitops.Repo`
serializes Git operations globally, and `internal/store.Store` also serializes
write/refresh operations.

**Discussion**

- Current implementation is simpler and likely acceptable for low-frequency config changes.
- ADR-003's target design may still be correct for scale.
- A superseding ADR is cleaner than editing an accepted ADR in place.

---

### Q-003: Where should Helm/Kubernetes deployment manifests live?

- Opened: 2026-04-29
- Owner: maintainers
- Status: open
- Proposed Answer: Keep this repo focused on the binary until deployment ownership is explicit; document current absence in `docs/current/OPERATIONS.md`.
- Blocks: `OPS` deployment gate
- Resolution: pending

**Context**

The README references container build support, but this repo does not currently
own Helm charts or K8s manifests for deploying Config Server.
