# Extraction Template

Use this template when closing a milestone retrospective, final retrospective,
major discovery note, or register entry that produced reusable knowledge.

This template describes what may be promoted from the project repo into an
external curated knowledge base — for example a personal `second-brain`, a team
wiki, or another reusable knowledge system.

It does not perform the promotion by itself. The external knowledge base
applies its own ingestion, schema, sensitivity, and promotion rules.

---

## Scope

- Project:
- Milestone / document:
- Date:
- Source document: <path inside project repo>
- Related PR:
- Related ADR / DEC / Q:
- External knowledge base target: <repo or system name>
- Extraction owner:

---

## Extraction candidate table

| ID | Kind | Candidate | Evidence / source | Proposed target | Action | Confidence | Notes |
|---|---|---|---|---|---|---|---|
| EX-001 | project_hub_update |  |  |  | create / modify / promote / drop | low / medium / high |  |
| EX-002 | adr_candidate |  |  |  | create / modify / promote / drop | low / medium / high |  |
| EX-003 | lesson_candidate |  |  |  | create / modify / promote / drop | low / medium / high |  |
| EX-004 | resource_candidate |  |  |  | create / modify / promote / drop | low / medium / high |  |
| EX-005 | do_not_promote |  |  |  | drop | low / medium / high |  |

Allowed `Kind` values:

- `project_hub_update`
- `adr_candidate`
- `lesson_candidate`
- `resource_candidate`
- `do_not_promote`
- `open_question`
- `current_state_update`
- `negative_knowledge`
- `other`

Allowed `Action` values:

- `create` — create a new artifact in the external knowledge base or project repo.
- `modify` — update an existing artifact.
- `promote` — move a distilled candidate into the external curated knowledge base.
- `drop` — deliberately do not promote into the external curated knowledge base.

`drop` semantics: dropped means "not promoted into the external curated
knowledge layer." It does *not* mean deletion from the project repo, raw event
ledger, transcript, git history, artifact store, or original source.

`candidate` vs `promoted`: every row here is a candidate / proposal. Final
promotion happens only after the target knowledge base reviews and accepts it
through its own process.

---

## Project hub update

Use this section for a short update that may be reflected in a knowledge-base
project hub. Include only stable state changes, not raw journal entries.

- Current milestone:
- Active tracks:
- Active phase:
- Active slice:
- Last accepted gate:
- Next gate:
- Important links:
- One-line learning log candidate:

---

## ADR candidates

List architecture decisions that should become or update ADRs.

Do not duplicate decisions already captured in `docs/adr/` or
`docs/08_DECISION_REGISTER.md`. Reference existing entries and explain only the
delta.

| Candidate | Why ADR-level? | Evidence | Existing decision link | Action |
|---|---|---|---|---|
|  |  |  |  | create / modify / promote / drop |

---

## Lesson candidates

List reusable lessons that may apply beyond this project.

Each lesson candidate should include:

- short title,
- one-sentence lesson,
- why it matters,
- source anchor,
- scope,
- when not to apply.

| Title | Lesson | Why it matters | Source anchor | Scope | When not to apply |
|---|---|---|---|---|---|
|  |  |  |  |  |  |

---

## Resource candidates

List external references, concepts, tools, or reusable resource notes.

Do not promote random links without a reason.

| Candidate | Type | Why useful | Source anchor | Suggested topic |
|---|---|---|---|---|
|  | link / concept / tool / paper |  |  |  |

---

## Do not promote

List things that should deliberately not be promoted into the external curated
knowledge base.

Do not leave this section blank. Use `None — reviewed` only if reviewed and
truly none.

Typical do-not-promote examples:

- raw Q&A transcript,
- PRD wording draft,
- temporary comparison table,
- rejected recommendation,
- stale plan,
- duplicate of an existing lesson/resource,
- project-specific implementation detail without cross-project value,
- sensitive content not suitable for the target knowledge base.

| Item | Reason | Rule / rationale | Keep where? |
|---|---|---|---|
|  |  |  | project repo / artifact store / transcript / nowhere |

`drop` semantics restated: dropped means "not promoted into the external
curated knowledge base." It does not mean delete the source.

Raw Q&A handling: raw Q&A may be used as source material here. Distill it
before promoting. Do not promote raw transcript itself unless the target
knowledge base explicitly wants transcripts.

---

## Source anchors

Every promoted candidate should have a source anchor when possible. Anchors
let downstream readers walk back to the project repo path / commit / PR / ADR
that produced the lesson.

| Candidate ID | Repo | Path | Commit | PR | ADR / DEC / Q | Notes |
|---|---|---|---|---|---|---|
| EX-001 | `<owner/project>` | `docs/...md` | `<SHA>` | `<PR URL>` | `ADR-####` / `DEC-###` / `Q-###` |  |

If a source anchor is missing, write `anchor missing` and explain why. Do not
fabricate anchors.

---

## Reporting format

After preparing or applying an extraction packet, report the result in four
groups. Omit any empty group.

```text
Created:
- <path or artifact> — what was created and why.

Modified:
- <path or artifact> — what changed and why.

Promoted:
- <source> → <target> — why this was promoted.

Dropped:
- <item> — why this was not promoted.
```

Optional follow-up notes:

- Missing anchors:
- Follow-up decisions:
- Follow-up questions:

---

## Downstream policy

The target external knowledge base decides final placement, schema,
sensitivity, and validation rules. This boilerplate provides the project-side
extraction packet only.

Do not copy downstream policy text into this template.

Examples of downstream-only concerns (kept out of this template):

- knowledge-base folder layout,
- frontmatter / schema fields,
- ingestion rules,
- sensitivity classification,
- vault-specific naming conventions.

If you are extracting toward a specific knowledge base — e.g. an
`alxdr3k/second-brain`-style vault — consult that vault's own policy
documents (typically under `_System/AI/` and `_System/Templates/`) for final
placement rules.
