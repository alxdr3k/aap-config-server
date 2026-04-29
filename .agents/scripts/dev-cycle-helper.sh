#!/usr/bin/env bash
# Shared helpers for dev-cycle commands/skills.

set -euo pipefail

script_dir() {
  local source dir
  source="${BASH_SOURCE[0]}"
  while [[ -L "$source" ]]; do
    dir="$(cd -P "$(dirname "$source")" && pwd)"
    source="$(readlink "$source")"
    [[ "$source" != /* ]] && source="$dir/$source"
  done
  cd -P "$(dirname "$source")" && pwd
}

direct_push_list_file() {
  local dir candidate
  dir="$(script_dir)"
  for candidate in "$dir/direct-push-repos.txt" "$dir/../direct-push-repos.txt"; do
    if [[ -f "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return
    fi
  done
  echo "Missing direct-push-repos.txt next to dev-cycle-helper.sh" >&2
  return 1
}

direct_push_repos() {
  sed 's/^[[:space:]]*//; s/[[:space:]]*$//; /^[[:space:]]*$/d; /^#/d' "$(direct_push_list_file)"
}

repo_root() {
  git rev-parse --show-toplevel
}

repo_name() {
  local remote name
  remote="$(git remote get-url origin 2>/dev/null || true)"
  if [[ -n "$remote" ]]; then
    remote="${remote%.git}"
    name="${remote##*/}"
    [[ "$remote" == *:* && "$remote" != http* ]] && name="${remote##*:}"
    name="${name##*/}"
    if [[ -n "$name" ]]; then
      printf '%s\n' "$name"
      return
    fi
  fi
  basename "$(repo_root)"
}

repo_type() {
  local name
  name="$(repo_name)"
  if direct_push_repos | grep -qxF "$name"; then
    echo "direct-push"
  else
    echo "standard"
  fi
}

default_branch() {
  local branch
  branch="$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null | sed 's#^origin/##' || true)"
  if [[ -z "$branch" ]]; then
    branch="$(git remote show origin 2>/dev/null | sed -n '/HEAD branch/s/.*: //p' || true)"
  fi
  echo "${branch:-main}"
}

review_base() {
  if [[ "$(repo_type)" == "direct-push" ]]; then
    echo "main"
    return
  fi

  local base
  base="$(gh pr view --json baseRefName -q .baseRefName 2>/dev/null || true)"
  if [[ -n "$base" ]]; then
    echo "$base"
  elif git show-ref --verify --quiet refs/remotes/origin/dev; then
    echo "dev"
  else
    default_branch
  fi
}

sync_repo() {
  local current type default
  current="$(git branch --show-current)"
  type="$(repo_type)"
  default="$(default_branch)"

  git fetch origin

  if [[ -z "$current" ]]; then
    echo "Detached HEAD: cannot run dev-cycle sync safely" >&2
    return 1
  fi

  if [[ "$type" == "direct-push" ]]; then
    if [[ "$current" != "main" && -n "$(git status --porcelain)" ]]; then
      echo "Dirty worktree on $current: cannot switch direct-push repo to main safely" >&2
      return 1
    fi
    if [[ "$current" == "main" && -n "$(git status --porcelain)" ]]; then
      echo "Dirty worktree on main: commit, stash, or clean before direct-push sync" >&2
      return 1
    fi
    git switch main
    git pull --ff-only origin main
    return
  fi

  if git show-ref --verify --quiet refs/remotes/origin/dev; then
    if [[ "$current" == "dev" ]]; then
      git pull --ff-only origin dev
    elif [[ -z "$(git status --porcelain)" ]]; then
      git switch dev
      git pull --ff-only origin dev
      git switch "$current"
    else
      echo "Dirty worktree: fetched origin/dev, skipped local dev checkout" >&2
    fi
  elif [[ "$current" == "$default" ]]; then
    git pull --ff-only origin "$default"
  fi
}

ensure_state_dir() {
  local root git_dir state_dir exclude_file
  root="$(repo_root)"
  git_dir="$(git rev-parse --git-dir)"
  state_dir="$root/.dev-cycle"
  exclude_file="$git_dir/info/exclude"

  mkdir -p "$state_dir"
  if [[ -f "$exclude_file" ]]; then
    grep -qxF ".dev-cycle/" "$exclude_file" 2>/dev/null || echo ".dev-cycle/" >> "$exclude_file"
  fi
  echo "$state_dir"
}

shell_export() {
  local key="$1" value="$2"
  printf 'export %s=%q\n' "$key" "$value"
}

brief_run_id_file() {
  local state_dir="$1"
  printf '%s\n' "$state_dir/dev-cycle-run-id"
}

init_brief() {
  local run_id state_dir log run_id_file
  run_id="$(date -u +%Y%m%dT%H%M%SZ)"
  state_dir="$(ensure_state_dir)"
  log="$state_dir/dev-cycle-briefs.md"
  run_id_file="$(brief_run_id_file "$state_dir")"
  printf "# Dev Cycle Briefs %s\n\n" "$run_id" > "$log"
  printf '%s\n' "$run_id" > "$run_id_file"
  shell_export DEV_CYCLE_RUN_ID "$run_id"
  shell_export DEV_CYCLE_BRIEF_LOG "$log"
}

validate_brief() {
  local run_id="${1:-}" log="${2:-}" first
  [[ -n "$run_id" && -n "$log" && -f "$log" ]] || return 1
  first="$(head -n 1 "$log")"
  [[ "$first" == "# Dev Cycle Briefs $run_id" ]]
}

brief_context() {
  local state_dir run_id_file run_id log
  state_dir="$(ensure_state_dir)"
  run_id_file="$(brief_run_id_file "$state_dir")"
  run_id="${DEV_CYCLE_RUN_ID:-}"
  log="${DEV_CYCLE_BRIEF_LOG:-$state_dir/dev-cycle-briefs.md}"

  if [[ -z "$run_id" && -f "$run_id_file" ]]; then
    run_id="$(sed -n '1p' "$run_id_file")"
  fi

  if ! validate_brief "$run_id" "$log"; then
    echo "No valid dev-cycle brief state. Run init-brief at the start of this dev-cycle run." >&2
    return 1
  fi

  printf '%s\n%s\n' "$run_id" "$log"
}

validate_cycle_append() {
  local cycle="$1" log="$2" previous

  if grep -qxF "## Cycle $cycle" "$log"; then
    echo "Cycle $cycle is already recorded in $log" >&2
    return 1
  fi

  if [[ "$cycle" =~ ^[0-9]+$ ]]; then
    if (( cycle == 1 )); then
      if grep -q '^## Cycle ' "$log"; then
        echo "Brief log already contains cycles; run init-brief to start a new dev-cycle run." >&2
        return 1
      fi
    elif (( cycle > 1 )); then
      previous=$((cycle - 1))
      if ! grep -qxF "## Cycle $previous" "$log"; then
        echo "Brief log is missing Cycle $previous before Cycle $cycle" >&2
        return 1
      fi
    fi
  fi
}

is_empty_risk() {
  local risk normalized
  risk="${1:-}"
  normalized="$(printf '%s' "$risk" | tr '[:upper:]' '[:lower:]' | sed 's/^[[:space:]]*//; s/[[:space:].。]*$//')"
  case "$normalized" in
    ""|"없음"|"none"|"no risk"|"n/a"|"na") return 0 ;;
    *) return 1 ;;
  esac
}

finish_cycle() {
  local cycle result work verification review_ship risk next_action log brief issue_url title summary issue_err issue_msg context
  cycle="${DEV_CYCLE_CYCLE:?set DEV_CYCLE_CYCLE}"
  result="${DEV_CYCLE_RESULT:?set DEV_CYCLE_RESULT}"
  work="${DEV_CYCLE_WORK:?set DEV_CYCLE_WORK}"
  verification="${DEV_CYCLE_VERIFICATION:?set DEV_CYCLE_VERIFICATION}"
  review_ship="${DEV_CYCLE_REVIEW_SHIP:?set DEV_CYCLE_REVIEW_SHIP}"
  risk="${DEV_CYCLE_RISK:-없음}"
  next_action="${DEV_CYCLE_NEXT_ACTION:-Triage the recorded risk.}"
  context="$(brief_context)"
  log="$(printf '%s\n' "$context" | sed -n '2p')"

  validate_cycle_append "$cycle" "$log"

  brief="$(cat <<EOF
## Cycle $cycle
- Result: $result
- Work: $work
- Verification: $verification
- Review/Ship: $review_ship
- Risk: $risk
EOF
)"

  if ! is_empty_risk "$risk"; then
    summary="$(printf '%s' "$risk" | head -n 1 | cut -c 1-90)"
    title="[dev-cycle risk] $summary"
    issue_err="$(mktemp)"
    if issue_url="$(gh issue create --title "$title" --body "$brief

Next action: $next_action" 2>"$issue_err")"; then
      rm -f "$issue_err"
      risk="$risk (tracked: $issue_url)"
    else
      issue_msg="$(sed -n '1p' "$issue_err" 2>/dev/null || true)"
      rm -f "$issue_err"
      review_ship="$review_ship; risk issue creation failed"
      risk="$risk (issue creation failed${issue_msg:+: $issue_msg}; next action: $next_action)"
      echo "Risk issue creation failed; brief recorded without issue link" >&2
    fi
    brief="$(cat <<EOF
## Cycle $cycle
- Result: $result
- Work: $work
- Verification: $verification
- Review/Ship: $review_ship
- Risk: $risk
EOF
)"
  fi

  printf '%s\n\n' "$brief" | tee -a "$log"
}

summary() {
  local context log
  context="$(brief_context)"
  log="$(printf '%s\n' "$context" | sed -n '2p')"
  sed -n '1,120p' "$log"
}

usage() {
  cat <<'EOF'
usage: dev-cycle-helper.sh <command>

commands:
  direct-push-list
  repo-name
  repo-type
  default-branch
  review-base
  sync
  init-brief
  validate-brief <run-id> <brief-log>
  finish-cycle
  summary
EOF
}

cmd="${1:-}"
case "$cmd" in
  direct-push-list) direct_push_repos ;;
  repo-name) repo_name ;;
  repo-type) repo_type ;;
  default-branch) default_branch ;;
  review-base) review_base ;;
  sync) sync_repo ;;
  init-brief) init_brief ;;
  validate-brief) shift; validate_brief "$@" ;;
  finish-cycle) finish_cycle ;;
  summary) summary ;;
  help|-h|--help|"") usage ;;
  *) echo "unknown command: $cmd" >&2; usage >&2; exit 2 ;;
esac
