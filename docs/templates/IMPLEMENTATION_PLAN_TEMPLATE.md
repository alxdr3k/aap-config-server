# Implementation Plan — <Project>

Product gates, technical tracks, phases, commit-sized slices, and acceptance
gates are tracked here.

Detailed issue tracking may live elsewhere. This file is the canonical
roadmap / status ledger; do not duplicate the full ledger into
`docs/context/current-state.md` or `docs/current/`.

## Taxonomy

| Term | Meaning | Example ID |
|---|---|---|
| Milestone | Product / user-facing delivery gate | `P0-M1` |
| Track | Technical area or major workstream | `<TRACK>` |
| Phase | Ordered implementation stage inside a track | `<TRACK-1A>` |
| Slice / Task | Commit-sized implementation unit | `<TRACK-1A.1>` |
| Gate | Acceptance or validation criterion | `AC-###` / `TEST-###` |
| Evidence | Links proving status | code, tests, PR, docs |

## Thin-doc boundary

- This file owns roadmap / status ledger details.
- `docs/context/current-state.md` summarizes only the active position.
- `docs/current/` describes implemented navigation, runtime, data, testing,
  and operations state. It does not own future roadmap inventory.
- Evidence should be links / IDs, not copied implementation detail.

## Status vocabulary

Implementation status:

| Status | Meaning |
|---|---|
| `planned` | Planned, not yet startable |
| `ready` | Startable |
| `in_progress` | Work in progress |
| `landed` | Code / docs have landed |
| `accepted` | Required gates passed or were explicitly waived |
| `blocked` | Blocked |
| `deferred` | Intentionally postponed |
| `dropped` | Intentionally not doing |

Gate status:

| Status | Meaning |
|---|---|
| `defined` | Defined but not executed |
| `not_run` | Expected to run, not yet run |
| `passing` | Passing |
| `failing` | Failing |
| `waived` | Explicitly waived |

## Milestones

| Milestone | Product / user gate | Target date | Status | Gate | Evidence | Notes |
|---|---|---|---|---|---|---|
| `P0-M1` |  |  | `planned` | `AC-###` |  |  |

## Tracks

| Track | Purpose | Active phase | Status | Notes |
|---|---|---|---|---|
| `<TRACK>` |  | `<TRACK-1A>` | `planned` |  |

## Phases / Slices

| Slice | Milestone | Track | Phase | Goal | Depends | Gate | Gate status | Status | Evidence | Next |
|---|---|---|---|---|---|---|---|---|---|---|
| `<TRACK-1A.1>` | `P0-M1` | `<TRACK>` | `<TRACK-1A>` |  |  | `AC-###` / `TEST-###` | `defined` | `planned` |  |  |

## Gates / Acceptance

- Gate definitions live in `06_ACCEPTANCE_TESTS.md`.
- Automated checks are listed in `docs/current/TESTING.md` once they exist.
- A slice can be `landed` before its gate is `passing`.
- A milestone is `accepted` only when its required gates are passing or explicitly waived.

## Traceability

- Completed slices should have a row in `09_TRACEABILITY_MATRIX.md`.
- Link slices to the relevant Q / DEC / ADR, REQ / NFR, AC / TEST, and milestone.
- Do not use trace rows as a backlog. They are connection records for important paths.

## Dependencies

## Risks (open)

→ `03_RISK_SPIKES.md`

## Capacity / Timeline

- Team:
- Hours/week:
- ETA:

## Migrating existing projects

1. Search existing docs for scattered roadmap/status language.
2. Map product/user-facing gates to Milestones.
3. Map technical streams to Tracks.
4. Map ordered implementation stages inside each track to Phases.
5. Map PR-sized or commit-sized units to Slices.
6. Map acceptance criteria, tests, staging checks, or manual verification to Gates.
7. Populate this ledger first, then remove duplicate roadmap/status inventories
   from current-state, current docs, runtime, architecture, and agent docs.
8. Split ambiguous `done` / `pending` states into implementation status and gate status.
9. Preserve source anchors when moving information. If unknown, write `anchor missing`.
