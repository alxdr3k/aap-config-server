---
name: apply-review
description: Apply all review feedback; split into timeout-safe chunks; do not stop until every item is done
argument-hint: [review text, PR/comment URL, or path to review notes]
---

# Apply review

Apply **every** item of the review below. Work the whole list to completion in this session — do not hand control back until the list is empty or something is genuinely blocked.

## Review content

$ARGUMENTS

---

## Model Routing

Claude Code에서 model-routed sub-agent를 사용할 수 있으면 아래 원칙을 따른다. 사용할 수 없거나 handoff 비용이 더 크면 같은 세션에서 수행한다.

- 복잡하거나 논쟁적인 review item의 타당성 판단은 Opus read-only reviewer를 우선한다.
- 실제 코드 수정은 Sonnet/main execution을 기본으로 한다. 작은 todo마다 별도 worker를 만들지 않는다.
- URL/PR/comment fetch, 상태 확인, 단순 정리는 script/CLI를 먼저 사용한다. LLM 요약이 필요할 때만 Haiku 또는 Explore를 쓴다.
- `dev-cycle-helper.sh review-dossier`를 사용할 수 있으면 review item 적용 전후에 dossier를 만들어 diff size, 파일 확산, 계약/중요 경로를 reviewer 입력 정보로 활용한다. Opus reviewer 사용 여부는 review item 자체의 semantic risk를 보고 본인이 판단한다.
- Opus reviewer에게는 review 원문, 관련 diff, helper-generated review dossier 또는 수동 risk summary, 필요한 call site, 검증 출력만 전달한다. 전체 repo나 이전 transcript를 기본으로 넘기지 않는다.
- 같은 파일군에서 3회 이상 반박/재검토가 이어질 때만 reviewer resume을 고려한다. 기본은 이전 finding 요약 + incremental diff로 새 검토를 요청한다.

## How to execute

### 1. Parse and plan (do this first, always)

- If `$ARGUMENTS` is a URL or a reference (PR number, comment ID, file path), fetch the actual content first.
- Extract every distinct actionable item. Keep independent items separate; merge only trivially-related fixes.
- Write the plan to `TodoWrite` — one todo per discrete change. Include file/area hints in the todo text so you can resume after interruption.
- If the review is ambiguous, list your interpretation as part of the plan rather than asking up front. Only stop for a question when an item is genuinely undecidable.

### 2. Split for timeout safety

Each todo must be small enough that its execution won't approach response or context limits.

- Aim for todos that complete in **≤ 10 tool calls**.
- If one review item spans many files, split by file or by concern (e.g. "refactor module X: step 1/3 — extract service", "step 2/3 — update callers", "step 3/3 — adjust tests").
- Large search/exploration steps → delegate to a sub-agent (`Explore`, `general-purpose`) so the main loop stays lean.
- Prefer `Edit` over rewriting whole files; keep diffs minimal.

### 3. Execute sequentially

- Mark the current todo `in_progress` **before** starting it. Never have more than one `in_progress` at a time.
- Make the change, then run the *minimum* verification that belongs to it (targeted test, lint on the touched file, type check).
- Mark the todo `completed` as soon as it's actually done. Do not batch completions at the end.
- If a todo reveals a follow-up, add a new todo rather than growing the current one.
- If a verification fails, fix it within the same todo before moving on.

### 4. Keep going until done

- Do **not** stop after one or two items.
- Do **not** ask "should I continue?" between items. The command name *is* the authorization.
- Only stop when:
  1. Every todo is `completed`, **or**
  2. A todo is blocked on information only the user can provide, **or**
  3. An error surfaces that would be unsafe to silently work around (failing migration, destructive git state, external service returning unexpected shape).
- On stop-for-block: state exactly which todo is blocked, what you tried, and what you need — then stop.

### 5. Final pass

- Run the project's full verification (tests + lint/type-check as defined by project conventions).
- Summarize: what changed (one line per todo), what (if anything) remains, and what the user should review.

---

## Constraints

- **Scope discipline**: address review items only. No drive-by refactors, no speculative cleanup.
- **User-facing language**: summarize review feedback, progress, questions, and final reports in Korean. Keep code, commands, filenames, and quoted source text in their original language.
- **Project conventions**: respect `AGENTS.md`, `CLAUDE.md`, `.claude/rules/**`, and existing code style when present. If a review item conflicts with a project rule, flag it and follow the rule.
- **Commits**: do not commit unless the review or project workflow explicitly requests it. If commits are expected, pair test + implementation per the project's TDD rules.
- **Secrets / destructive ops**: never fix a review item by weakening a security check or skipping hooks.
