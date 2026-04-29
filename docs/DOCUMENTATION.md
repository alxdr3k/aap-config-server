# Documentation Policy

## Purpose

This docs tree separates project-stage decisions and roadmap/status tracking
from implementation-stage current state.

## Source-of-truth hierarchy

1. Code and tests once implementation exists
2. Roadmap / status ledger in `docs/04_IMPLEMENTATION_PLAN.md`
3. Thin current-state docs under `docs/context/` and `docs/current/`
4. PRD/HLD/spec/runbook/acceptance/CI/CD docs
5. ADRs and Decision Register
6. Generated docs under `docs/generated/` as derived references
7. Discovery and archived design notes

## Rules

- Current docs are for fast orientation, not full history.
- `docs/04_IMPLEMENTATION_PLAN.md` owns the roadmap / status ledger:
  milestone, track, phase, slice, gate, status, evidence, and next work.
- `docs/context/current-state.md` only summarizes the active roadmap position.
- `docs/current/` describes implemented state navigation, not future roadmap inventory.
- `docs/11_CI_CD.md` describes stack-neutral CI/CD guidance. Actual commands
  live in `docs/current/TESTING.md`; deployment ownership and runbooks live in
  `docs/current/OPERATIONS.md` and `docs/05_RUNBOOK.md`.
- Accepted ADRs are not edited to reflect new behavior; create a new ADR that supersedes the old one.
- Discovery notes are not current implementation authority.
- Archived design notes are not current implementation authority.
- Do not update long historical notes on every implementation change.
- Prefer small targeted doc patches over broad rewrites.
- Prefer generated docs for schema/API/enums when generation exists.
- If code changes behavior/schema/runtime, update the relevant thin current doc in the same PR.

## What to update when

Use this table:

| Change type | Required doc action |
|---|---|
| Product scope changes | update `docs/01_PRD.md`; add DEC/ADR if needed |
| Architecture changes | update `docs/02_HLD.md`; add/supersede ADR |
| Roadmap taxonomy or slice status changes | update `docs/04_IMPLEMENTATION_PLAN.md` |
| Active milestone / track / phase / slice changes | update `docs/context/current-state.md` |
| Gate definition or acceptance status changes | update `docs/06_ACCEPTANCE_TESTS.md` |
| Runtime behavior changes | update `docs/current/RUNTIME.md` |
| Module/file layout changes | update `docs/current/CODE_MAP.md` |
| Parser/store/data model changes | update `docs/current/DATA_MODEL.md` |
| Test/lint/typecheck/eval command changes | update `docs/current/TESTING.md` |
| Operational/env/deployment changes | update `docs/current/OPERATIONS.md` or `docs/05_RUNBOOK.md` |
| CI/CD workflow, required check, release, or branch protection changes | update `docs/current/TESTING.md`, `docs/current/OPERATIONS.md`, `docs/05_RUNBOOK.md`, `docs/06_ACCEPTANCE_TESTS.md`, and `docs/11_CI_CD.md` as applicable |
| New open question | add Q row to `docs/07_QUESTIONS_REGISTER.md` |
| Lightweight accepted decision | add DEC row to `docs/08_DECISION_REGISTER.md` |
| Major accepted decision | add ADR under `docs/adr/` |
| Cross-document impact | update `docs/09_TRACEABILITY_MATRIX.md` |
| Historical exploration | put under `docs/discovery/` or `docs/design/archive/` |
| Reusable lesson discovered | add candidate to retrospective; promote via external knowledge-base process using `docs/templates/EXTRACTION_TEMPLATE.md` |
| Milestone completion | update `docs/04_IMPLEMENTATION_PLAN.md`, `docs/context/current-state.md`, `docs/09_TRACEABILITY_MATRIX.md`, and `docs/10_PROJECT_RETROSPECTIVE.md` |
| Major project completion | complete final retrospective and prepare extraction packet (`docs/templates/EXTRACTION_TEMPLATE.md`) |
| Raw Q&A / discovery produces reusable knowledge | distill into extraction candidates; do not promote raw transcript |
| Rejected / stale recommendation identified | add to `Do not promote` in extraction packet with rationale |

## Roadmap / status migration

When adopting this boilerplate in an existing project, normalize scattered
roadmap language into the taxonomy in `docs/04_IMPLEMENTATION_PLAN.md`:

1. Map product / user-facing gates to Milestones.
2. Map technical streams to Tracks.
3. Map ordered implementation stages inside a track to Phases.
4. Map commit-sized or PR-sized implementation units to Slices.
5. Map acceptance criteria, automated tests, staging checks, or manual
   verification to Gates.
6. Split ambiguous `done` / `pending` states into implementation status
   (`planned`, `landed`, `accepted`, etc.) and gate status (`defined`,
   `not_run`, `passing`, etc.).
7. Move the canonical inventory into `04_IMPLEMENTATION_PLAN.md`, then trim
   duplicate status inventories from `current-state`, runtime, architecture,
   and agent instructions.
8. Preserve source anchors when moving status: repo path, commit, PR, ADR,
   DEC, Q, AC, TEST, or issue ID. If unknown, write `anchor missing`.

## CI/CD migration

When adopting this boilerplate in an existing project, migrate CI/CD knowledge
without rewriting history or inventing a cleaner process than the one that
exists.

1. Inventory workflow files, external CI/CD systems, release scripts, deploy
   platforms, package registries, cron jobs, Makefiles, and manual steps.
2. Copy real validation commands into `docs/current/TESTING.md`. If a command
   is unknown, write `Needs audit` instead of guessing.
3. Record deployment ownership, environments, secrets ownership, release
   triggers, and rollback boundaries in `docs/current/OPERATIONS.md`.
4. Move step-by-step deploy, rollback, monitor, and incident procedures into
   `docs/05_RUNBOOK.md`.
5. Use `docs/11_CI_CD.md` for guidance and
   `docs/templates/CI_CD_TEMPLATE.md` as a worksheet when a migration packet or
   single planning view is useful.

## Enforcement mechanisms

The rules above are honor-system unless something forces compliance. Adopt
these patterns once your project has a code base; they are conventions, not
boilerplate code, so each project wires them up to its own stack.

### Doc Freshness CI

A GitHub Action that diffs the PR (or push) against the base, and warns when
code paths change without a matching roadmap/status, acceptance gate, thin
current-state doc, generated doc, or ADR update. The warning is a soft
comment, not a merge gate — the goal is to surface drift fast, not block ships.

This repo uses `.github/workflows/doc-freshness.yml` as a soft warning check.
It watches changes under `cmd/`, `internal/`, `Dockerfile`, `Makefile`,
`go.mod`, `go.sum`, and CI workflow files, then comments on PRs when matching
documentation may be stale.

Untrusted GitHub event input must only flow through `env:` blocks, never
inlined into shell `run:` commands. The workflow follows this convention.

The PR template at `.github/pull_request_template.md` carries the matching
documentation-impact checklist.

### SHA freshness headers

Thin current-state docs that describe rapidly-evolving logic (LLM call paths,
parsing pipelines, judgment / decision rules) can carry a header on lines 3-5:

```
> Last verified against code: <commit-SHA> (<YYYY-MM-DD>)
```

**Rule:** any commit that modifies code whose behaviour a SHA-headered doc
describes must also update that header. Stale headers are a doc gap, not a
cosmetic issue.

Add the header only to docs that genuinely track fast-moving logic — not to
every thin doc. Most current-state docs do not need it.

### Generated docs

`docs/generated/` holds outputs derived from code, parser types, config, or
specs. This project currently has no active generated-doc command. When one
is added, pair each generated file with a generator script committed in the
project.

Rules:
- Do not edit generated docs by hand.
- Run the generator and commit the output in the same PR as the source change.
- The PR template carries a checkbox for this.
- See `docs/generated/README.md` for the project's active generators.
