# AGENTS.md

## Read order

For normal implementation tasks in this repo, read:

1. `docs/context/current-state.md`
2. `docs/04_IMPLEMENTATION_PLAN.md` active milestone / track / phase / slice
3. `docs/current/CODE_MAP.md`
4. `docs/current/TESTING.md`
5. task-relevant source files
6. relevant ADR only if changing architecture or product scope

Do not read archived design docs by default.

## Source of truth

- Code and tests are authoritative for implemented behavior once implementation exists.
- Generated docs are derived references; fix the generator or source rather than hand-editing generated output.
- `docs/context/current-state.md` is the compressed current state.
- `docs/04_IMPLEMENTATION_PLAN.md` owns roadmap / status ledger: milestone, track, phase, slice, gate, evidence, and next work.
- `docs/01_PRD.md` owns product scope.
- `docs/02_HLD.md` owns intended high-level design.
- `docs/current/` owns thin implementation-state navigation docs.
- `docs/07_QUESTIONS_REGISTER.md` owns open questions.
- `docs/08_DECISION_REGISTER.md` owns lightweight decisions.
- `docs/adr/` owns major architecture decisions.
- `docs/discovery/` and `docs/design/archive/` are history, not authority.

## When changing code

- If runtime behavior changes, update `docs/current/RUNTIME.md`.
- If roadmap position, slice status, gate status, evidence, or next work changes,
  update `docs/04_IMPLEMENTATION_PLAN.md`.
- If the active milestone / track / phase / slice changes, update
  `docs/context/current-state.md`.
- If acceptance gate definitions or results change, update
  `docs/06_ACCEPTANCE_TESTS.md`.
- If module/file layout changes, update `docs/current/CODE_MAP.md`.
- If parser/store/data model changes, update `docs/current/DATA_MODEL.md` and
  re-run the generated-doc command if one exists, then commit the regenerated
  file under `docs/generated/`.
- If test/lint/typecheck/eval commands change, update `docs/current/TESTING.md`.
- If operational/env/deployment behavior changes, update `docs/current/OPERATIONS.md` or `docs/05_RUNBOOK.md`.
- If product scope changes, update `docs/01_PRD.md`.
- If architecture direction changes, create or supersede an ADR.
- Do not rewrite archived design notes for implementation changes.
- If the thin doc you are editing carries a `Last verified against code:
  <SHA> (<date>)` header, update the SHA and date to the current commit
  before pushing.

## Validation

Use commands from `docs/current/TESTING.md`.

Do not invent commands.

If validation cannot be run, report why.

Current local caveat: some workspaces may not have Go installed. In that case,
run the static documentation checks you can, leave gate status as `not_run`,
and report the missing `go` binary explicitly.

## Branching

- Primary integration branch: `dev`.
- Feature work flows `feat/*` -> `dev` -> `main`.
- Create feature branches from `dev`, and open PRs back to `dev`.
- Promote `dev` to `main` only through a PR.
- Never push to `main` directly.
- Never force-push.

## Extraction tasks

When asked to prepare external knowledge-base extraction (e.g. for a personal
`second-brain`-style vault, team wiki, or other curated knowledge system):

1. Read [`docs/templates/EXTRACTION_TEMPLATE.md`](docs/templates/EXTRACTION_TEMPLATE.md) — it is canonical.
2. Read the relevant retrospective / discovery / Q / DEC / ADR source.
3. Prepare an extraction candidate table with `Kind` and `Action` from the template's allowed values.
4. Distinguish candidate vs promoted — every row is a candidate. Do not claim a candidate has been promoted unless the target knowledge base has accepted it.
5. Do not promote raw transcript, stale drafts, rejected recommendations, or project-only implementation details. List them under `Do not promote` with rationale.
6. `Do not promote` must not be left blank. Use `None — reviewed` only after explicit review.
7. Preserve source anchors (repo / path / commit / PR / ADR / DEC / Q). If unknown, write `anchor missing`. Do not fabricate.
8. Report results as Created / Modified / Promoted / Dropped (omit empty groups).
9. Do not modify the external knowledge base unless explicitly asked. Boilerplate prepares the packet; the target knowledge base owns final placement, schema, and validation.
