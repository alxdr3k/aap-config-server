# AGENTS.policy.md

Boilerplate-owned agent policy. Synced to all repos — do not edit in project repos.
Project-specific guidance belongs in `AGENTS.md`.

## Working principles

- Think before editing: state assumptions and tradeoffs when ambiguity changes the solution. Ask before unsafe guesses.
- Keep it simple: add only what the request needs. No speculative features, abstractions, configuration, or docs.
- Make surgical changes: touch only relevant files, preserve local style, and clean up only debris introduced by your change.
- Separate planning from execution: record known scope with status, but execute only ready and authorized work.
- Verify goals: turn work into checkable outcomes, run documented checks, and report any validation you could not run.

## Validation

Prefer terse output flags to reduce context size:

Tests:
- pytest: `-q --tb=short`
- go test: omit `-v` (quiet by default)
- jest / vitest: `--reporter=dot` or `--silent`
- rspec: `-f p` (default)
- cargo test: `-- --quiet`

Lint / typecheck / build:
- eslint: `--format compact`
- rubocop: `--format simple`
- tsc: `--noEmit --pretty false`

Package installs:
- npm: `npm ci --silent`
- yarn: `yarn install --silent`
- bundle: `bundle install --quiet`
- pip: `pip install -q`
- cargo: `cargo fetch -q`

Do not read generated or lock files (`package-lock.json`, `Gemfile.lock`, `yarn.lock`, `*.generated.*`, `schema.rb`, etc.) — they are not source of truth and waste context.

Do not invent commands.

If validation cannot be run, report why.

## Extraction tasks

When asked to prepare external knowledge-base extraction (e.g. for a personal
`second-brain`-style vault, team wiki, or other curated knowledge system):

1. Read the project's extraction template (path defined in `AGENTS.md`) — it is canonical.
2. Read the relevant retrospective / discovery / Q / DEC / ADR source.
3. Prepare an extraction candidate table with `Kind` and `Action` from the template's allowed values.
4. Distinguish candidate vs promoted — every row is a candidate. Do not claim a candidate has been promoted unless the target knowledge base has accepted it.
5. Do not promote raw transcript, stale drafts, rejected recommendations, or project-only implementation details. List them under `Do not promote` with rationale.
6. `Do not promote` must not be left blank. Use `None — reviewed` only after explicit review.
7. Preserve source anchors (repo / path / commit / PR / ADR / DEC / Q). If unknown, write `anchor missing`. Do not fabricate.
8. Report results as Created / Modified / Promoted / Dropped (omit empty groups).
9. Do not modify the external knowledge base unless explicitly asked. Boilerplate prepares the packet; the target knowledge base owns final placement, schema, and validation.
