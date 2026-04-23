# Project Setup

## Custom Commands

### /apply-review

Apply **every** item of the review feedback. Work the whole list to completion in this session — do not hand control back until the list is empty or something is genuinely blocked.

**Description:** Apply all review feedback; split into timeout-safe chunks; do not stop until every item is done

**Arguments:** `[review text, PR/comment URL, or path to review notes]`

#### How to execute

##### 1. Parse and plan (do this first, always)

- If the argument is a URL or a reference (PR number, comment ID, file path), fetch the actual content first.
- Extract every distinct actionable item. Keep independent items separate; merge only trivially-related fixes.
- Write the plan to `TodoWrite` — one todo per discrete change. Include file/area hints in the todo text so you can resume after interruption.
- If the review is ambiguous, list your interpretation as part of the plan rather than asking up front. Only stop for a question when an item is genuinely undecidable.

##### 2. Split for timeout safety

Each todo must be small enough that its execution won't approach response or context limits.

- Aim for todos that complete in **≤ 10 tool calls**.
- If one review item spans many files, split by file or by concern (e.g. "refactor module X: step 1/3 — extract service", "step 2/3 — update callers", "step 3/3 — adjust tests").
- Large search/exploration steps → delegate to a sub-agent (`Explore`, `general-purpose`) so the main loop stays lean.
- Prefer `Edit` over rewriting whole files; keep diffs minimal.

##### 3. Execute sequentially

- Mark the current todo `in_progress` **before** starting it. Never have more than one `in_progress` at a time.
- Make the change, then run the *minimum* verification that belongs to it (targeted test, lint on the touched file, type check).
- Mark the todo `completed` as soon as it's actually done. Do not batch completions at the end.
- If a todo reveals a follow-up, add a new todo rather than growing the current one.
- If a verification fails, fix it within the same todo before moving on.

##### 4. Keep going until done

- Do **not** stop after one or two items.
- Do **not** ask "should I continue?" between items. The command name *is* the authorization.
- Only stop when:
  1. Every todo is `completed`, **or**
  2. A todo is blocked on information only the user can provide, **or**
  3. An error surfaces that would be unsafe to silently work around (failing migration, destructive git state, external service returning unexpected shape).
- On stop-for-block: state exactly which todo is blocked, what you tried, and what you need — then stop.

##### 5. Final pass

- Run the project's full verification (tests + lint/type-check as defined by project conventions).
- Summarize: what changed (one line per todo), what (if anything) remains, and what the user should review.

#### Constraints

- **Scope discipline**: address review items only. No drive-by refactors, no speculative cleanup.
- **Project conventions**: respect `CLAUDE.md`, `.claude/rules/**`, and existing code style. If a review item conflicts with a project rule, flag it and follow the rule.
- **Commits**: do not commit unless the review or project workflow explicitly requests it. If commits are expected, pair test + implementation per the project's TDD rules.
- **Secrets / destructive ops**: never fix a review item by weakening a security check, skipping hooks, or force-pushing.
