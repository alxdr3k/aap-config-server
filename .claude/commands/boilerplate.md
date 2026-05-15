Manage boilerplate policy sync across repositories.

If `--help` is passed or the argument is unrecognized, show this usage guide.

## Usage

```
/boilerplate                         # sync all repos in targets.tsv
/boilerplate --status                # show sync status for all repos in targets.tsv
/boilerplate <repo>                  # Tier 1 — policy import only (safe for any repo)
/boilerplate <repo> --docs           # Tier 2 — + doc partner files
/boilerplate <repo> --prune          # Tier 3 — + remove AGENTS.md boilerplate duplicates
/boilerplate <repo> --align          # Tier 4 — agent-driven structural gap analysis
/boilerplate --help                  # show this guide
```

Tier summary:

| Flag | What it does | Touches project docs? |
|------|-------------|----------------------|
| (none) | Copy `AGENTS.policy.md`, fix CLAUDE.md imports | No |
| `--docs` | + copy doc partner files | No |
| `--prune` | + remove AGENTS.md sections now in policy | Only removes duplicate boilerplate sections |
| `--align` | + audit structure gaps, guided fill | Yes — with user confirmation |

---

## No-arg: sync all targets

Run the sync script against the cached target list:

```bash
BOILERPLATE=~/ws/boilerplate \
  bash ~/.codex/skills/boilerplate/scripts/boilerplate-sync-docs.sh sync
```

Each repo is synced at its profile-determined tier:
- `boilerplate` profile → Tier 2 (policy files + doc partner files)
- `universal` profile → Tier 1 (policy file only)
- `custom` → skipped

---

## `--status`: sync 현황 표시

targets.tsv의 전체 repo에 대해 plan을 실행하고 결과를 표로 표시한다.

1. plan 실행:
   ```bash
   BOILERPLATE=~/ws/boilerplate \
     bash ~/.codex/skills/boilerplate/scripts/boilerplate-sync-docs.sh \
     plan ~/.codex/skills/boilerplate/targets.tsv
   ```

2. targets.tsv에서 profile 읽기:
   ```bash
   cat ~/.codex/skills/boilerplate/targets.tsv
   ```

3. 두 결과를 조인하여 다음 포맷으로 표시:

   ```
   Repo                   Tier  Base    Status
   ─────────────────────────────────────────────────────────
   actwyn                 T2    main    ✓ up-to-date
   concluv                T2    main    ✗ CLAUDE.md:imports, AGENTS.policy.md
   devdeck                T2    main    ✓ up-to-date
   second-brain           T1    main    ✓ up-to-date
   open-codesign          —     main    – skip (custom)
   trading.wave           T1    dev     ✗ AGENTS.md:ref[migrate]
   ```

   Tier 기준:
   - **T2** — `boilerplate` profile (policy + doc partner files synced)
   - **T1** — `universal` profile (policy file only)
   - **—** — `custom` (skipped, manual migration needed)

   Status 기준:
   - ✓ `up-to-date` — all managed files in sync
   - ✗ `needs:<files>` — one or more files out of sync; list what's missing
   - – `skip-custom` — custom profile, not managed by sync
   - ! `missing-repo` — repo path not found on disk

   After the table, print a summary line: `X up-to-date, Y need sync, Z skipped`.

---

## With `<repo>`: adopt or update a specific repo

### Tier 1 — default (safe for any repo)

1. Copy `AGENTS.policy.md` from boilerplate to `<repo>` root.
2. Ensure CLAUDE.md has `@AGENTS.md` (if AGENTS.md exists) and `@AGENTS.policy.md`:
   ```bash
   BOILERPLATE=~/ws/boilerplate \
     bash ~/.codex/skills/boilerplate/scripts/boilerplate-sync-docs.sh apply <(echo -e "$(basename <repo>)\t<repo>\t<base>\tuniversal")
   ```
3. Add reference to AGENTS.md (if it exists):
   ```bash
   BOILERPLATE=~/ws/boilerplate \
     bash ~/.codex/skills/boilerplate/scripts/boilerplate-sync-docs.sh refs <repo>
   ```
4. Update targets.tsv — set profile to `universal`:
   ```bash
   bash ~/.codex/skills/boilerplate/scripts/boilerplate-sync-docs.sh targets-update <repo> universal
   ```

### Tier 2 — `--docs`

All Tier 1 steps, then:

1. Verify target has boilerplate-structure docs (`docs/04_IMPLEMENTATION_PLAN.md`, `docs/DOCUMENTATION.md`).
   If missing: stop and report — run without `--docs` or restructure docs first.
2. Copy doc partner files:
   - `docs/04_IMPLEMENTATION_PLAN.policy.md`
   - `docs/DOCUMENTATION.policy.md`
3. Update targets.tsv — set profile to `boilerplate`.

### Tier 3 — `--prune`

All Tier 2 steps, then:

Removes AGENTS.md sections that are now covered by `AGENTS.policy.md`. Does not delete any files.

1. Audit `AGENTS.md` for duplicate boilerplate sections:
   - `## Working principles`
   - Terse output flags block in `## Validation`
   - `## Extraction tasks` methodology body (keep path reference line)
2. Show the user exactly what will be removed. Confirm before each deletion.
3. If `## Extraction tasks` existed, ensure this line survives:
   ```
   Extraction template: `docs/templates/EXTRACTION_TEMPLATE.md`
   ```
4. Update targets.tsv — set profile to `boilerplate`.

### Tier 4 — `--align` (agent-driven)

All Tier 3 steps, then run a full gap analysis **in the target repo** and close each gap.

**Default posture**: alignment is the default action. The goal is a repo that reads as if it started from boilerplate today. Deviations require explicit project-specific justification — "we had this before" is not justification.

**Stubs and compatibility files are gaps, not overrides.** A file that exists only to forward readers to `docs/current/` is a drift artifact. Recommend removal by default. A file that was intentionally kept must be backed by a DEC; if no DEC exists, treat it as a gap.

Gap audit checklist — run all items, then present findings and proposed actions together:

**A. Structure (file presence)**
- [ ] All numbered docs (00–11) present? Missing → create from boilerplate template
- [ ] `docs/context/current-state.md` exists?
- [ ] `docs/current/` has CODE_MAP, TESTING, RUNTIME, DATA_MODEL, OPERATIONS?
- [ ] No stub files forwarding to `docs/current/` without a DEC? → recommend removal

**B. Content drift (existing files)**
- [ ] `AGENTS.md` read order: references `docs/context/current-state.md` first, then `docs/04_IMPLEMENTATION_PLAN.md`, `docs/current/CODE_MAP.md`, `docs/current/TESTING.md`, `docs/11_CI_CD.md` (for CI work)?
- [ ] `AGENTS.md` has `## When changing code` section covering all boilerplate update triggers?
- [ ] `AGENTS.md` has `## Validation` section pointing to `docs/current/TESTING.md`?
- [ ] `AGENTS.md` has `## Extraction tasks` section (path reference line)?
- [ ] `.policy.md` files referenced in `AGENTS.md`?
- [ ] `docs/DOCUMENTATION.md` trigger table includes all policy rows (extraction packet, raw Q&A distillation, rejected recommendation, agent policy changes)?
- [ ] Any `AGENTS.md` conventions that contradict `AGENTS.policy.md`?

**C. Template cleanup**
- [ ] `docs/templates/` contains files removed from boilerplate (HLD, PRD, IMPLEMENTATION_PLAN, PROJECT_RETROSPECTIVE, RUNBOOK templates)? → recommend removal if no active reference

For each gap: state what's missing and what the boilerplate-aligned content should be. The recommended action is always alignment unless the project has an explicit override reason. Confirm with user once (show all gaps together) before applying — do not ask for each gap individually.

This tier requires running inside the **target repo** session, not the boilerplate repo.

---

## targets.tsv update

The `targets-update` subcommand adds or updates a repo entry:

```bash
BOILERPLATE=~/ws/boilerplate \
  bash ~/.codex/skills/boilerplate/scripts/boilerplate-sync-docs.sh \
  targets-update <repo-path> <profile>
```

Profiles: `universal` | `boilerplate` | `custom`

---

## Verification (all tiers)

```bash
grep -x '@AGENTS.policy.md' <repo>/CLAUDE.md    # exact import line
grep 'AGENTS.policy.md' <repo>/AGENTS.md         # reference present
git -C <repo> diff --stat HEAD~1                 # what changed
```
