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
| `TRACE-006` | `Q-002` | `ADR-003` | `FR-4` | `AC-005` / `TEST-005` | `P0-M2` | `CORE` | `CORE-1A` | `CORE-1A.5` | ADR says service mutex; implementation uses global git mutex. |
| `TRACE-007` |  | `ADR-004` | `FR-7`, `FR-17` | `AC-020` | `P1-M1` | `SECRET` | `SECRET-1A` | `SECRET-1A.1` | Planned SealedSecret/resolve path. |
| `TRACE-008` |  | `ADR-001`, `ADR-002` | `FR-9` | `AC-030` | `P1-M2` | `AGENT` | `AGENT-1A` | `AGENT-1A.1` | Planned Config Agent. |
| `TRACE-009` | `Q-001` | `DEC-001` | Documentation migration | `AC-014` | `P0-M3` | `DOC` | `DOC-1A` | `DOC-1A.1` | Boilerplate docs adopted; `FR-*` preserved for now. |
| `TRACE-010` |  | `DEC-001` | Documentation workflow | `AC-015` | `P0-M3` | `DOC` | `DOC-1A` | `DOC-1A.2` | PR template and doc freshness soft-check. |

## Invariants

- Every `must` requirement needs at least one AC.
- Every accepted DEC/ADR must identify impacted requirement/design/runbook areas.
- Every completed slice should link to at least one trace row.
- Every `accepted` milestone needs gate/test evidence.

## Gaps

- PRD `FR-*` to boilerplate `REQ-*` mapping is unresolved (`Q-001`).
- Some target PRD/HLD features have gates but no code/tests yet.
- ADR-003 concurrency design and current global mutex implementation need a follow-up decision or ADR update path (`Q-002`).
