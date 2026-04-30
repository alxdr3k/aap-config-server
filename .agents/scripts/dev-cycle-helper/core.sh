# shellcheck shell=bash

script_dir() {
  local source dir
  if [[ -n "${DEV_CYCLE_HELPER_SCRIPT_DIR:-}" ]]; then
    printf '%s\n' "$DEV_CYCLE_HELPER_SCRIPT_DIR"
    return
  fi

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
  for candidate in "$dir/direct-push-repos.txt" "$dir/../direct-push-repos.txt" "$dir/../../direct-push-repos.txt"; do
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

brief_start_epoch_file() {
  local state_dir="$1"
  printf '%s\n' "$state_dir/dev-cycle-start-epoch"
}

format_duration() {
  local total="$1" days hours minutes seconds parts
  (( total < 0 )) && total=0
  days=$((total / 86400))
  hours=$(((total % 86400) / 3600))
  minutes=$(((total % 3600) / 60))
  seconds=$((total % 60))

  parts=()
  if (( days > 0 )); then parts+=("${days}d"); fi
  if (( hours > 0 )); then parts+=("${hours}h"); fi
  if (( minutes > 0 )); then parts+=("${minutes}m"); fi
  if (( seconds > 0 || ${#parts[@]} == 0 )); then parts+=("${seconds}s"); fi
  printf '%s\n' "${parts[*]}"
}

iso_now() {
  date -u +%Y-%m-%dT%H:%M:%SZ
}

require_jq() {
  if ! command -v jq >/dev/null 2>&1; then
    echo "jq is required for dev-cycle JSON brief handling" >&2
    return 1
  fi
}

brief_jsonl_file() {
  local state_dir="$1"
  printf '%s\n' "$state_dir/dev-cycle-briefs.jsonl"
}

brief_run_json_file() {
  local state_dir="$1"
  printf '%s\n' "$state_dir/dev-cycle-run.json"
}
