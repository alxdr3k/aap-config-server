#!/usr/bin/env bash
set -euo pipefail

ROOT="${ROOT:-$HOME/ws}"
MY_SKILL="${MY_SKILL:-$ROOT/my-skill}"
BOILERPLATE="${BOILERPLATE:-$ROOT/boilerplate}"
SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEFAULT_TARGETS="$SKILL_DIR/targets.tsv"
BRANCH="chore/boilerplate-doc-sync"
COMMIT_MSG="${COMMIT_MSG:-docs: sync boilerplate policy files}"

# Coverage — what this script automatically syncs (overwrite, no surgical patching):
#
#   Tier 1 (universal — any repo with CLAUDE.md or AGENTS.md):
#     AGENTS.policy.md          boilerplate-owned cross-cutting agent behaviour rules
#     CLAUDE.md                 ensure @AGENTS.md (when present) + @AGENTS.policy.md imports
#
#   Tier 2 (boilerplate-structure — repos with numbered docs):
#     docs/04_IMPLEMENTATION_PLAN.policy.md
#     docs/DOCUMENTATION.policy.md
#
# Out of scope (intentionally never mutated by sync):
#   AGENTS.md     — project-owned; add AGENTS.policy.md ref via /boilerplate-migrate
#   TESTING.md, CI/CD docs, ADRs, source code

usage() {
  cat <<'USAGE'
Usage:
  boilerplate-sync-docs.sh discover
  boilerplate-sync-docs.sh plan   [targets.tsv | -]
  boilerplate-sync-docs.sh apply  [targets.tsv | -]
  boilerplate-sync-docs.sh sync   [targets.tsv]
  boilerplate-sync-docs.sh refs            [repo-path]          # add AGENTS.policy.md ref to AGENTS.md
  boilerplate-sync-docs.sh targets-update <repo-path> <profile> # add/update repo in targets.tsv
  boilerplate-sync-docs.sh coverage [--update] [--report]       # invariant tracking adoption status
  boilerplate-sync-docs.sh discover | boilerplate-sync-docs.sh plan -

targets.tsv columns:
  name<TAB>path<TAB>base<TAB>profile<TAB>invariant_tracking

profile:
  auto | universal | boilerplate | custom

invariant_tracking:
  none | policy_only | partial | full | auto

  - none         (default)  invariant tracking 미적용
  - policy_only             AGENTS.policy.md 의 관련 섹션만 적용
  - partial                 일부 자산만 (예: templates만)
  - full                    모든 자산 (templates + validator + workflow)
  - auto                    file presence 기반 자동 detect (`coverage` 서브커맨드 참고)
USAGE
}

die() { echo "error: $*" >&2; exit 1; }

default_branch() {
  local repo="$1" branch
  branch="$(git -C "$repo" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null | sed 's#^origin/##' || true)"
  echo "${branch:-main}"
}

is_direct_repo() {
  local name="$1"
  [[ -f "$MY_SKILL/direct-push-repos.txt" ]] && grep -qxF "$name" "$MY_SKILL/direct-push-repos.txt"
}

base_branch() {
  local repo="$1" name="$2"
  if is_direct_repo "$name"; then
    echo "main"
  elif git -C "$repo" show-ref --verify --quiet refs/remotes/origin/dev ||
       git -C "$repo" show-ref --verify --quiet refs/heads/dev; then
    echo "dev"
  else
    default_branch "$repo"
  fi
}

profile_for() {
  local repo="$1"
  if [[ -f "$repo/docs/04_IMPLEMENTATION_PLAN.md" && -f "$repo/docs/DOCUMENTATION.md" ]]; then
    echo "boilerplate"
  elif [[ -f "$repo/CLAUDE.md" || -f "$repo/AGENTS.md" ]]; then
    echo "universal"
  else
    echo "auto"
  fi
}

project_candidates() {
  find "$ROOT" -maxdepth 3 -name ".claude" -type d -exec dirname {} \; 2>/dev/null || true

  [[ -f "$MY_SKILL/deploy-projects.txt" ]] || return 0
  while IFS= read -r line; do
    line="${line%%#*}"
    line="$(printf '%s' "$line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    [[ -n "$line" ]] || continue
    if [[ "$line" == /* ]]; then echo "$line"
    else echo "$ROOT/$line"
    fi
  done < "$MY_SKILL/deploy-projects.txt"
}

discover() {
  project_candidates | sort -u | while IFS= read -r repo; do
    [[ -d "$repo/.git" || -f "$repo/.git" ]] || continue
    [[ -f "$repo/CLAUDE.md" || -f "$repo/AGENTS.md" ]] || continue
    local name base profile
    name="$(basename "$repo")"
    base="$(base_branch "$repo" "$name")"
    profile="$(profile_for "$repo")"
    printf '%s\t%s\t%s\t%s\n' "$name" "$repo" "$base" "$profile"
  done
}

# ── file list per profile ────────────────────────────────────────────────────

policy_files_for() {
  local profile="$1"
  echo "AGENTS.policy.md"
  if [[ "$profile" == "boilerplate" ]]; then
    echo "docs/04_IMPLEMENTATION_PLAN.policy.md"
    echo "docs/DOCUMENTATION.policy.md"
  fi
}

# ── plan ─────────────────────────────────────────────────────────────────────

file_up_to_date() {
  local src="$BOILERPLATE/$1" dst="$2/$1"
  [[ -f "$src" ]] || return 0       # boilerplate doesn't have it yet → skip
  [[ -f "$dst" ]] || return 1       # missing in target
  diff -q "$src" "$dst" >/dev/null 2>&1
}

claude_ok() {
  local repo="$1"
  [[ -f "$repo/CLAUDE.md" ]] || return 0   # no CLAUDE.md → nothing to check
  # Exact-line match: @-import lines must be at start of line with no trailing content
  grep -qx '@AGENTS.policy.md' "$repo/CLAUDE.md" || return 1
  # Only require @AGENTS.md import when AGENTS.md actually exists
  if [[ -f "$repo/AGENTS.md" ]]; then
    grep -qx '@AGENTS.md' "$repo/CLAUDE.md" || return 1
  fi
}

plan_one() {
  local name="$1" repo="$2" base="$3" profile="$4"
  if [[ ! -d "$repo/.git" && ! -f "$repo/.git" ]]; then
    printf '%s\tmissing-repo\t%s\t%s\n' "$name" "$base" "$repo"; return
  fi
  if [[ "$profile" == "custom" ]]; then
    printf '%s\tskip-custom\t%s\t%s\n' "$name" "$base" "$repo"; return
  fi

  local needs=()
  while IFS= read -r f; do
    file_up_to_date "$f" "$repo" || needs+=("$f")
  done < <(policy_files_for "$profile")
  claude_ok "$repo" || needs+=("CLAUDE.md:imports")
  # AGENTS.md:ref: non-mutating drift detection only.
  # Sync never edits AGENTS.md; run /boilerplate-migrate to resolve.
  if [[ -f "$repo/AGENTS.md" ]] && ! grep -q "AGENTS\.policy\.md" "$repo/AGENTS.md"; then
    needs+=("AGENTS.md:ref[migrate]")
  fi

  if [[ ${#needs[@]} -eq 0 ]]; then
    printf '%s\tup-to-date\t%s\t%s\n' "$name" "$base" "$repo"
  else
    printf '%s\tneeds:%s\t%s\t%s\n' "$name" "$(IFS=,; echo "${needs[*]}")" "$base" "$repo"
  fi
}

# ── apply ────────────────────────────────────────────────────────────────────

ensure_claude_imports() {
  local claude="$1"
  local repo_root
  repo_root="$(dirname "$claude")"
  # Use exact-line checks (grep -qx) to match claude_ok predicate exactly
  # Only add @AGENTS.md when AGENTS.md exists in the same repo
  if [[ -f "$repo_root/AGENTS.md" ]] && ! grep -qx '@AGENTS.md' "$claude"; then
    printf '@AGENTS.md\n' | cat - "$claude" > "$claude.tmp" && mv "$claude.tmp" "$claude"
  fi
  # Ensure exact @AGENTS.policy.md line; insert after @AGENTS.md or prepend
  if ! grep -qx '@AGENTS.policy.md' "$claude"; then
    if grep -qx '@AGENTS.md' "$claude"; then
      awk '/^@AGENTS\.md$/{print; print "@AGENTS.policy.md"; next}1' "$claude" > "$claude.tmp" && mv "$claude.tmp" "$claude"
    else
      printf '@AGENTS.policy.md\n' | cat - "$claude" > "$claude.tmp" && mv "$claude.tmp" "$claude"
    fi
  fi
}

ensure_agents_ref() {
  local agents="$1"
  grep -q "AGENTS.policy.md" "$agents" && return 0
  # Write ref to tmpfile — avoids shell backtick/quoting pitfalls entirely
  local tmpref
  tmpref="$(mktemp)"
  printf '%s\n' 'See also: `AGENTS.policy.md` — boilerplate-owned cross-cutting agent behaviour rules.' > "$tmpref"
  if grep -q "^## " "$agents"; then
    # awk reads ref from file; inserts before first ## heading only
    awk -v rfile="$tmpref" \
      'BEGIN{while((getline line < rfile)>0) ref=ref line "\n"}
       !done && /^## /{printf "%s", ref; done=1} {print}' \
      "$agents" > "$agents.tmp" && mv "$agents.tmp" "$agents"
  else
    cat "$tmpref" "$agents" > "$agents.tmp" && mv "$agents.tmp" "$agents"
  fi
  rm -f "$tmpref"
  grep -q "AGENTS.policy.md" "$agents" || { echo "ensure_agents_ref: insertion failed in $agents" >&2; return 1; }
}

apply_one() {
  local name="$1" repo="$2" base="$3" profile="$4"
  [[ -d "$repo/.git" || -f "$repo/.git" ]] || { echo "$name missing-repo"; return 0; }
  [[ "$profile" == "custom" ]] && { echo "$name skip-custom"; return 0; }

  local wt
  wt="$(mktemp -d "/tmp/boilerplate-sync-${name}.XXXXXX")"
  rmdir "$wt"

  git -C "$repo" fetch origin "$base" -q
  git -C "$repo" worktree add --detach "$wt" "origin/$base" -q
  git -C "$wt" checkout -b "$BRANCH" -q

  # Copy policy files
  while IFS= read -r f; do
    local src="$BOILERPLATE/$f"
    [[ -f "$src" ]] || continue
    mkdir -p "$wt/$(dirname "$f")"
    cp "$src" "$wt/$f"
  done < <(policy_files_for "$profile")

  # Ensure CLAUDE.md has @AGENTS.md (when AGENTS.md exists) + @AGENTS.policy.md
  if [[ -f "$wt/CLAUDE.md" ]]; then
    ensure_claude_imports "$wt/CLAUDE.md"
  fi
  # AGENTS.md is project-owned — mutations are handled by /boilerplate-migrate, not sync

  git -C "$wt" diff --check
  git -C "$wt" add -A
  if git -C "$wt" diff --cached --quiet; then
    echo "$name unchanged"
  else
    git -C "$wt" commit -m "$COMMIT_MSG" -q
    git -C "$wt" push origin HEAD:"$base" -q
    echo "$name pushed $(git -C "$wt" rev-parse --short HEAD) -> $base"
  fi

  git -C "$repo" worktree remove "$wt" --force >/dev/null
  git -C "$repo" branch -D "$BRANCH" >/dev/null 2>&1 || true
}

# ── sync ─────────────────────────────────────────────────────────────────────

with_targets() {
  local targets="$1" fn="$2"
  local input="$targets"
  [[ "$targets" == "-" ]] && input="/dev/stdin"
  [[ "$targets" != "-" && ! -f "$targets" ]] && die "target file not found: $targets"
  while IFS=$'\t' read -r name repo base profile rest; do
    [[ -n "${name:-}" && "$name" != \#* ]] || continue
    [[ -n "${repo:-}" && -n "${base:-}" ]] || die "bad target row for $name"
    profile="${profile:-auto}"
    "$fn" "$name" "$repo" "$base" "$profile"
  done < "$input"
}

sync_targets() {
  local targets="${1:-$DEFAULT_TARGETS}"
  [[ -f "$targets" ]] || die "target file not found: $targets"

  # Print plan (informational) then always run apply.
  # plan_one uses the local checkout; apply_one fetches origin/$base and is the
  # ground-truth check. Suppressing apply based on local plan state would hide
  # remote drift when the local checkout is stale. apply_one reports "unchanged"
  # when nothing needs pushing, so the extra fetch+diff cost is accepted for
  # correctness.
  with_targets "$targets" plan_one
  with_targets "$targets" apply_one
}

# ── dispatch ─────────────────────────────────────────────────────────────────

cmd="${1:-}"
case "$cmd" in
  discover) discover ;;
  plan)
    shift
    input="${1:--}"
    if [[ "$input" == "-" ]]; then
      with_targets "-" plan_one
    else
      with_targets "$input" plan_one
    fi
    ;;
  apply)
    shift
    input="${1:-$DEFAULT_TARGETS}"
    with_targets "$input" apply_one
    ;;
  sync)
    shift
    sync_targets "${1:-$DEFAULT_TARGETS}"
    ;;
  refs)
    # Add AGENTS.policy.md reference to AGENTS.md in the given repo (or CWD).
    # Used by /boilerplate when onboarding a specific repo. Does not commit.
    shift
    target="${1:-.}"
    agents="$target/AGENTS.md"
    [[ -f "$agents" ]] || die "AGENTS.md not found in $target"
    ensure_agents_ref "$agents" && echo "AGENTS.md updated: $agents" || echo "AGENTS.md already has reference: $agents"
    ;;
  targets-update)
    # Add or update a repo entry in targets.tsv.
    # Usage: targets-update <repo-path> <profile>
    shift
    [[ $# -eq 2 ]] || die "usage: targets-update <repo-path> <profile>"
    t_repo="$(cd "$1" && pwd)"
    t_profile="$2"
    [[ "$t_profile" =~ ^(universal|boilerplate|custom)$ ]] || die "profile must be universal|boilerplate|custom"
    t_name="$(basename "$t_repo")"
    t_base="$(git -C "$t_repo" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null | sed 's#^origin/##' || git -C "$t_repo" symbolic-ref --short HEAD 2>/dev/null || echo main)"
    t_line="${t_name}	${t_repo}	${t_base}	${t_profile}	none"
    if grep -q "^${t_name}	" "$DEFAULT_TARGETS" 2>/dev/null; then
      # Preserve existing invariant_tracking value if present
      existing="$(grep "^${t_name}	" "$DEFAULT_TARGETS" | cut -f5)"
      [[ -n "$existing" ]] && t_line="${t_name}	${t_repo}	${t_base}	${t_profile}	${existing}"
      sed -i '' "s|^${t_name}	.*|${t_line}|" "$DEFAULT_TARGETS"
      echo "updated: $t_line"
    else
      printf '%s\n' "$t_line" >> "$DEFAULT_TARGETS"
      echo "added: $t_line"
    fi
    ;;
  coverage)
    # Detect invariant tracking adoption per repo.
    #   --update : write detected status back into targets.tsv (only updates rows whose value is `auto`)
    #   --report : emit markdown report (suitable for boilerplate's docs/_generated/adoption_report.md)
    # --update + --report combined is rejected — the report would render the
    # pre-update declared values while targets.tsv has already been mutated,
    # producing a self-inconsistent artifact. Run them in two separate
    # invocations: first --update, then --report.
    shift
    do_update=0
    do_report=0
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --update) do_update=1 ;;
        --report) do_report=1 ;;
        *) die "unknown coverage flag: $1" ;;
      esac
      shift
    done
    if [[ $do_update -eq 1 && $do_report -eq 1 ]]; then
      die "coverage --update and --report are mutually exclusive (run separately to avoid stale-declared report)"
    fi

    detect_status() {
      local repo="$1"
      [[ -d "$repo" ]] || { echo "missing"; return; }
      local has_relations=0 has_term_schema=0 has_validator=0 has_workflow=0 has_glossary_dir=0 has_policy_section=0
      [[ -f "$repo/docs/templates/relation_enum.yaml" ]] && has_relations=1
      [[ -f "$repo/docs/templates/glossary_term_schema.yaml" ]] && has_term_schema=1
      [[ -f "$repo/scripts/validate_invariants.ts" ]] && has_validator=1
      [[ -f "$repo/.github/workflows/invariant-check.yml" ]] && has_workflow=1
      # docs/glossary must contain at least one tracked file to count as
      # adopted — git does not version empty directories, so a fresh clone
      # would lack the directory entirely and detection would flip.
      if [[ -d "$repo/.git" || -f "$repo/.git" ]]; then
        if git -C "$repo" ls-files --error-unmatch "docs/glossary/*" >/dev/null 2>&1; then
          has_glossary_dir=1
        fi
      else
        # Non-git or unusual layout — fall back to filesystem presence.
        [[ -d "$repo/docs/glossary" ]] && has_glossary_dir=1
      fi
      # policy_only signal: AGENTS.policy.md must contain the
      # "Cross-document invariant tracking" section heading. A repo without
      # this heading has not received the policy even if the file exists.
      if [[ -f "$repo/AGENTS.policy.md" ]] && \
         grep -q "Cross-document invariant tracking" "$repo/AGENTS.policy.md" 2>/dev/null; then
        has_policy_section=1
      fi

      local has_any_asset=$(( has_relations + has_term_schema + has_validator + has_workflow + has_glossary_dir ))
      local has_full_assets=0
      if [[ $has_relations -eq 1 && $has_term_schema -eq 1 && $has_validator -eq 1 && $has_workflow -eq 1 && $has_glossary_dir -eq 1 ]]; then
        has_full_assets=1
      fi

      if [[ $has_full_assets -eq 1 && $has_policy_section -eq 1 ]]; then
        echo "full"
      elif [[ $has_full_assets -eq 1 && $has_policy_section -eq 0 ]]; then
        # Tools landed but policy section did not — not yet complete.
        echo "partial"
      elif [[ $has_any_asset -ge 1 ]]; then
        echo "partial"
      elif [[ $has_policy_section -eq 1 ]]; then
        echo "policy_only"
      else
        echo "none"
      fi
    }

    # Per-target stale check. Returns one of:
    #   ok       checkout exists, on configured base, clean
    #   missing  no checkout
    #   branch   checkout exists but not on configured base
    #   dirty    checkout exists, on base, but uncommitted changes
    #   detached HEAD detached (no symbolic ref)
    # The classification is stable across report and update: report displays
    # it as a marker; update refuses to persist anything not marked `ok`.
    checkout_status() {
      local repo="$1" base="$2"
      [[ -d "$repo" ]] || { echo "missing"; return; }
      if [[ ! -d "$repo/.git" && ! -f "$repo/.git" ]]; then
        echo "missing"; return
      fi
      local cur
      cur="$(git -C "$repo" symbolic-ref --short HEAD 2>/dev/null || echo DETACHED)"
      if [[ "$cur" == "DETACHED" ]]; then echo "detached"; return; fi
      if [[ "$cur" != "$base" ]]; then echo "branch:$cur"; return; fi
      if [[ -n "$(git -C "$repo" status --short 2>/dev/null | head -1)" ]]; then
        echo "dirty"; return
      fi
      echo "ok"
    }

    # Stream rows + detected status into parallel arrays. Avoids here-strings
    # so the read-only coverage path works in restricted sandboxes.
    cov_names=()
    cov_repos=()
    cov_bases=()
    cov_declared=()
    cov_detected=()
    cov_effective=()
    cov_checkout=()    # ok | missing | branch:X | dirty | detached
    while IFS=$'\t' read -r name repo base profile tracking rest; do
      [[ -n "${name:-}" && "$name" != \#* ]] || continue
      tracking="${tracking:-none}"
      detected="$(detect_status "$repo")"
      effective="$tracking"
      [[ "$tracking" == "auto" ]] && effective="$detected"
      cov_names+=("$name")
      cov_repos+=("$repo")
      cov_bases+=("$base")
      cov_declared+=("$tracking")
      cov_detected+=("$detected")
      cov_effective+=("$effective")
      cov_checkout+=("$(checkout_status "$repo" "$base")")
    done < "$DEFAULT_TARGETS"

    # Console summary. In --report mode it is emitted on stderr to keep
    # stdout clean for redirect into the generated report file. The
    # checkout column shows whether `detected` reflects the configured
    # base — anything other than `ok` means detection ran against
    # arbitrary local state.
    summary_stream=1
    [[ $do_report -eq 1 ]] && summary_stream=2
    {
      printf '%-32s  %-10s  %-10s  %-10s  %s\n' "name" "declared" "detected" "effective" "checkout"
      printf '%-32s  %-10s  %-10s  %-10s  %s\n' "----" "--------" "--------" "---------" "--------"
      for i in "${!cov_names[@]}"; do
        printf '%-32s  %-10s  %-10s  %-10s  %s\n' \
          "${cov_names[$i]}" "${cov_declared[$i]}" "${cov_detected[$i]}" \
          "${cov_effective[$i]}" "${cov_checkout[$i]}"
      done
    } >&"$summary_stream"

    if [[ $do_update -eq 1 ]]; then
      # Only rows declared `auto` get rewritten with their detected value.
      # Persist only when checkout_status == ok (already on configured base,
      # clean) AND local base ref is not behind origin/$base. The fetch
      # check rejects stale local clones — a behind-base local would
      # otherwise rewrite an `auto` row to whatever the older snapshot
      # shows, and once rewritten the row is no longer `auto` and won't
      # be recomputed.
      skipped_missing=()
      skipped_dirty=()
      skipped_stale=()
      updated=0
      for i in "${!cov_names[@]}"; do
        [[ "${cov_declared[$i]}" == "auto" ]] || continue
        n="${cov_names[$i]}"
        repo="${cov_repos[$i]}"
        base="${cov_bases[$i]}"
        det="${cov_detected[$i]}"
        co="${cov_checkout[$i]}"
        if [[ "$det" == "missing" || "$co" == "missing" ]]; then
          skipped_missing+=("$n")
          continue
        fi
        if [[ "$co" != "ok" ]]; then
          skipped_dirty+=("$n($co)")
          continue
        fi
        # Verify local base is in sync with origin/$base. Fetch is mandatory
        # — silently using the cached ref could persist stale state when the
        # network is down. If fetch fails, refuse update for this row and
        # leave it as `auto` so the next run re-evaluates.
        if ! git -C "$repo" fetch origin "$base" -q 2>/dev/null; then
          skipped_stale+=("$n(fetch_failed)")
          continue
        fi
        local_sha="$(git -C "$repo" rev-parse HEAD 2>/dev/null || echo none)"
        remote_sha="$(git -C "$repo" rev-parse "origin/$base" 2>/dev/null || echo none)"
        if [[ "$local_sha" != "$remote_sha" || "$local_sha" == "none" ]]; then
          skipped_stale+=("$n(local=${local_sha:0:7},remote=${remote_sha:0:7})")
          continue
        fi
        # Field-aware rewrite via awk (handles regex metacharacters like
        # `trading.wave`). Atomic via tempfile + mv.
        tmp="$(mktemp "${DEFAULT_TARGETS}.XXXXXX")"
        awk -F'\t' -v OFS='\t' \
            -v target="$n" -v new_val="$det" \
            '$1 == target && $5 == "auto" { $5 = new_val } { print }' \
            "$DEFAULT_TARGETS" > "$tmp"
        mv "$tmp" "$DEFAULT_TARGETS"
        updated=$((updated+1))
      done
      echo "${updated} auto row(s) updated in $DEFAULT_TARGETS" >&2
      if [[ ${#skipped_missing[@]} -gt 0 ]]; then
        printf 'warning: %d row(s) skipped (target path missing): %s\n' \
          "${#skipped_missing[@]}" "${skipped_missing[*]}" >&2
      fi
      if [[ ${#skipped_dirty[@]} -gt 0 ]]; then
        printf 'warning: %d row(s) skipped (checkout not on base or dirty): %s\n' \
          "${#skipped_dirty[@]}" "${skipped_dirty[*]}" >&2
      fi
      if [[ ${#skipped_stale[@]} -gt 0 ]]; then
        printf 'warning: %d row(s) skipped (local HEAD != origin/base): %s\n' \
          "${#skipped_stale[@]}" "${skipped_stale[*]}" >&2
      fi
    fi

    if [[ $do_report -eq 1 ]]; then
      # Emit clean markdown report on stdout. No console summary, no
      # BEGIN/END markers. Caller redirects with `> adoption_report.md`.
      printf '# Adoption report — invariant tracking\n\n'
      printf '_Generated: %s_\n\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      printf 'Source: `targets.tsv` (my-skill/codex/skills/boilerplate/).\n\n'
      # checkout column makes detection's data source visible in the
      # generated report. Anything other than `ok` means detected was
      # computed against arbitrary local state, NOT the configured base.
      printf '| repo | declared | detected | effective | checkout |\n'
      printf '|---|---|---|---|---|\n'
      for i in "${!cov_names[@]}"; do
        marker="${cov_checkout[$i]}"
        # Wrap non-ok markers in code so they're visually distinct.
        [[ "$marker" != "ok" ]] && marker="\`$marker\`"
        printf '| %s | %s | %s | %s | %s |\n' \
          "${cov_names[$i]}" "${cov_declared[$i]}" "${cov_detected[$i]}" \
          "${cov_effective[$i]}" "$marker"
      done
      printf '\n## Status definitions\n\n'
      # printf %s avoids the trap where the first arg is interpreted as a flag
      # (a literal `-` makes printf fail under set -e and truncates the report).
      printf '%s\n' '- `none` — invariant tracking 미적용 (default)'
      printf '%s\n' '- `policy_only` — AGENTS.policy.md 의 invariant tracking 섹션만 적용 (도구 자산 없음)'
      printf '%s\n' '- `partial` — 일부 자산만 (예: templates만, 또는 자산은 다 있는데 policy 섹션 미반영)'
      printf '%s\n' '- `full` — 모든 자산 (templates + validator + workflow + glossary 디렉토리) + AGENTS.policy.md invariant tracking 섹션'
      printf '%s\n' '- `auto` — file presence 기반 자동 detect (declared=auto일 때만 effective가 detected와 일치)'
      printf '\n## Checkout markers\n\n'
      printf '%s\n' '- `ok` — checkout exists, on configured base, clean. Detection is authoritative.'
      printf '%s\n' '- `missing` — repo path absent. Detection is `missing`; rows are not persisted.'
      printf '%s\n' '- `branch:X` — checkout on branch X (not the configured base). Detection is local-only.'
      printf '%s\n' '- `dirty` — uncommitted changes present. Detection is local-only.'
      printf '%s\n' '- `detached` — HEAD detached. Detection is local-only.'
      printf '\nOnly `ok` rows can be persisted via `coverage --update`.\n'
    fi
    ;;
  -h|--help|help|"") usage ;;
  *) usage; exit 2 ;;
esac
