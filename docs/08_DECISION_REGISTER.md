# 08 Decision Register

작은 ~ 중간 크기의 결정을 가벼운 레코드로 남긴다. 큰 결정은 `adr/`.

## How to use

- 질문(Q-###)이 합의에 도달하면 여기로 승격.
- 한 항목은 짧게. 더 긴 설명이 필요하면 ADR로 승격.
- `supersedes`로 이전 결정과 연결.

## Decisions

### DEC-001: Adopt boilerplate numbered documentation as canonical project structure

- Date: 2026-04-29
- Status: accepted
- Deciders: maintainers
- Supersedes: —
- Superseded by: —
- Resolves: —
- Impacts: documentation structure, `AGENTS.md`, `docs/01_PRD.md`, `docs/02_HLD.md`, `docs/current/*`

**Context**

The repo already had PRD, HLD, ADRs, README, and development-process docs, but
roadmap/status, acceptance gates, current-state navigation, and traceability
were scattered or absent.

**Decision**

Adopt the boilerplate's numbered project-delivery docs and implementation-stage
current docs. Move existing PRD/HLD to `docs/01_PRD.md` and `docs/02_HLD.md`.
Keep compatibility stubs at `docs/PRD.md` and `docs/HLD.md`.

**Rationale**

This preserves existing links while giving new sessions a stable read order and
a single place for roadmap/status.

**Consequences**

- Positive: clearer source-of-truth hierarchy and faster onboarding.
- Negative: temporary duplicate PRD/HLD paths exist as compatibility stubs.
- Follow-ups: `DOC-1A.1`.

### DEC-002: Keep `FR-*` as canonical requirement IDs

- Date: 2026-04-29
- Status: accepted
- Deciders: maintainers
- Supersedes: —
- Superseded by: —
- Resolves: `Q-001`
- Impacts: `docs/01_PRD.md`, `docs/02_HLD.md`, `docs/06_ACCEPTANCE_TESTS.md`, traceability, migration docs

**Context**

The project already uses `FR-1` through `FR-17` across PRD, HLD, ADR,
acceptance, traceability, and current docs. Boilerplate examples use `REQ-*`,
but the template does not require immediate renumbering.

**Decision**

Keep `FR-*` as the canonical requirement ID format. Do not migrate existing
requirements to `REQ-*` during the boilerplate documentation migration. Add
`REQ-*` aliases only if a future external process or tool requires
boilerplate-native IDs.

**Rationale**

This avoids avoidable churn in established links and keeps current requirement,
acceptance, and ADR references stable while preserving a clear path for aliases
later.

**Consequences**

- Positive: stable requirement references across docs, ADRs, and tests.
- Negative: project IDs differ from boilerplate examples.
- Follow-ups: none required.
