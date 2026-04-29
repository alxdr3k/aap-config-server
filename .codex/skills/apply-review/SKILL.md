---
name: apply-review
description: Apply all review feedback; split into timeout-safe chunks; do not stop until every item is done
argument-hint: [review text, PR/comment URL, or path to review notes]
---
<!-- my-skill:generated
skill: apply-review
base-sha256: ba3667e54d35f30af571758527531a5df003f09afddc81f0dfa29da875ff037d
overlay-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
output-sha256: ba3667e54d35f30af571758527531a5df003f09afddc81f0dfa29da875ff037d
do-not-edit: edit .codex/skill-overrides/apply-review.md instead
-->

# Apply review

Apply **every** item of the review below. Work the whole list to completion in this session - do not hand control back until the list is empty or something is genuinely blocked.

## Review content

$ARGUMENTS

---

## How to execute

### 1. Parse and plan (do this first, always)

- If `$ARGUMENTS` is a URL or a reference (PR number, comment ID, file path), fetch the actual content first.
- Extract every distinct actionable item. Keep independent items separate; merge only trivially-related fixes.
- Write the plan with Codex's `update_plan` tool - one item per discrete change. Include file/area hints in the item text so you can resume after interruption.
- If the review is ambiguous, list your interpretation as part of the plan rather than asking up front. Only stop for a question when an item is genuinely undecidable.

### 2. Split for timeout safety

Each plan item must be small enough that its execution won't approach response or context limits.

- Aim for plan items that complete in **<= 10 tool calls**.
- If one review item spans many files, split by file or by concern (e.g. "refactor module X: step 1/3 - extract service", "step 2/3 - update callers", "step 3/3 - adjust tests").
- Use `rg` / `rg --files` first for broad searches, and parallelize independent read-only shell commands when available.
- If sub-agent delegation is explicitly requested by the user and available in the current Codex environment, delegate only concrete, bounded, non-overlapping side work.
- Prefer `apply_patch` for manual edits. Keep diffs minimal.

### 3. Execute sequentially

- Mark the current plan item `in_progress` **before** starting it. Never have more than one `in_progress` at a time.
- Make the change, then run the *minimum* verification that belongs to it (targeted test, lint on the touched file, type check).
- Mark the plan item `completed` as soon as it's actually done. Do not batch completions at the end.
- If a plan item reveals a follow-up, add a new item rather than growing the current one.
- If a verification fails, fix it within the same item before moving on.

### 4. Keep going until done

- Do **not** stop after one or two items.
- Do **not** ask "should I continue?" between items. The command name *is* the authorization.
- Only stop when:
  1. Every plan item is `completed`, **or**
  2. A plan item is blocked on information only the user can provide, **or**
  3. An error surfaces that would be unsafe to silently work around (failing migration, destructive git state, external service returning unexpected shape).
- On stop-for-block: state exactly which plan item is blocked, what you tried, and what you need - then stop.

### 5. Final pass

- Run the project's full verification (tests + lint/type-check as defined by project conventions).
- Summarize: what changed (one line per plan item), what (if anything) remains, and what the user should review.

---

## Constraints

- **Scope discipline**: address review items only. No drive-by refactors, no speculative cleanup.
- **User-facing language**: summarize review feedback, progress, questions, and final reports in Korean. Keep code, commands, filenames, and quoted source text in their original language.
- **Project conventions**: respect `AGENTS.md` and existing repo guidance/code style when present. If a review item conflicts with a project rule, flag it and follow the rule.
- **Commits**: do not commit unless the review or project workflow explicitly requests it. If commits are expected, pair test + implementation per the project's TDD rules.
- **Secrets / destructive ops**: never fix a review item by weakening a security check, skipping hooks, or force-pushing.
