# shellcheck shell=bash

init_brief() {
  local run_id started_at start_epoch state_dir log jsonl run_json run_id_file start_epoch_file
  require_jq || return 1
  run_id="$(date -u +%Y%m%dT%H%M%SZ)"
  started_at="$(iso_now)"
  start_epoch="$(date -u +%s)"
  state_dir="$(ensure_state_dir)"
  log="$state_dir/dev-cycle-briefs.md"
  jsonl="$(brief_jsonl_file "$state_dir")"
  run_json="$(brief_run_json_file "$state_dir")"
  run_id_file="$(brief_run_id_file "$state_dir")"
  start_epoch_file="$(brief_start_epoch_file "$state_dir")"
  printf "# Dev Cycle Briefs %s\n\n" "$run_id" > "$log" || return 1
  : > "$jsonl" || return 1
  jq -n \
    --arg run_id "$run_id" \
    --arg started_at "$started_at" \
    --arg repo "$(repo_name)" \
    --arg repo_type "$(repo_type)" \
    --arg root "$(repo_root)" \
    '{schema_version:1, run_id:$run_id, started_at:$started_at, repo:{name:$repo, type:$repo_type, root:$root}}' \
    > "$run_json" || return 1
  printf '%s\n' "$run_id" > "$run_id_file" || return 1
  printf '%s\n' "$start_epoch" > "$start_epoch_file" || return 1
  shell_export DEV_CYCLE_RUN_ID "$run_id"
  shell_export DEV_CYCLE_BRIEF_LOG "$log"
  shell_export DEV_CYCLE_BRIEF_JSONL "$jsonl"
  shell_export DEV_CYCLE_RUN_JSON "$run_json"
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
  run_id=""
  log="$state_dir/dev-cycle-briefs.md"

  if [[ -f "$run_id_file" ]]; then
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

  if grep -Eq "^(## Cycle $cycle|사이클 $cycle 브리핑)$" "$log"; then
    echo "Cycle $cycle is already recorded in $log" >&2
    return 1
  fi

  if [[ "$cycle" =~ ^[0-9]+$ ]]; then
    if (( cycle == 1 )); then
      if grep -Eq '^(## Cycle |사이클 [0-9]+ 브리핑$)' "$log"; then
        echo "Brief log already contains cycles; run init-brief to start a new dev-cycle run." >&2
        return 1
      fi
    elif (( cycle > 1 )); then
      previous=$((cycle - 1))
      if ! grep -Eq "^(## Cycle $previous|사이클 $previous 브리핑)$" "$log"; then
        echo "Brief log is missing Cycle $previous before Cycle $cycle" >&2
        return 1
      fi
    fi
  fi
}

validate_jsonl_state() {
  local jsonl="$1"
  [[ -s "$jsonl" ]] || return 0

  if ! jq -e -s '
    all(.[]; ((.cycle | type) == "number") and (.cycle >= 1) and (.cycle == (.cycle | floor)))
  ' "$jsonl" >/dev/null; then
    echo "Brief JSONL is invalid or contains records without numeric cycle: $jsonl" >&2
    return 1
  fi

  if ! jq -e -s '
    ([.[].cycle] | sort) as $cycles |
    $cycles == [range(1; ($cycles | length) + 1)]
  ' "$jsonl" >/dev/null; then
    echo "Brief JSONL has non-contiguous cycle records: $jsonl" >&2
    return 1
  fi
}

validate_jsonl_append() {
  local cycle="$1" jsonl="$2" previous

  if [[ ! "$cycle" =~ ^[0-9]+$ ]]; then
    echo "Cycle must be numeric for JSONL append validation" >&2
    return 1
  fi

  validate_jsonl_state "$jsonl" || return 1

  if jq -e --argjson cycle "$cycle" 'select(.cycle == $cycle)' "$jsonl" >/dev/null 2>&1; then
    echo "Cycle $cycle is already recorded in $jsonl" >&2
    return 1
  fi

  if (( cycle == 1 )); then
    if [[ -s "$jsonl" ]]; then
      echo "Brief JSONL already contains cycles; run init-brief to start a new dev-cycle run." >&2
      return 1
    fi
  elif (( cycle > 1 )); then
    previous=$((cycle - 1))
    if ! jq -e --argjson previous "$previous" 'select(.cycle == $previous)' "$jsonl" >/dev/null 2>&1; then
      echo "Brief JSONL is missing Cycle $previous before Cycle $cycle. Run init-brief for a new run or repair the JSONL from the existing brief state." >&2
      return 1
    fi
  fi
}

backfill_jsonl_from_markdown_if_needed() {
  local log="$1" jsonl="$2" run_id="$3" repo repo_type branch head_sha tmp
  [[ ! -s "$jsonl" ]] || return 0
  grep -Eq '^(## Cycle [0-9]+|사이클 [0-9]+ 브리핑)$' "$log" || return 0

  repo="$(repo_name)"
  repo_type="$(repo_type)"
  branch="$(git branch --show-current 2>/dev/null || true)"
  head_sha="$(git rev-parse --short HEAD 2>/dev/null || true)"
  tmp="$(mktemp)" || return 1

  if ! awk '
    BEGIN { sep = sprintf("%c", 28) }
    function flush() {
      if (cycle != "") {
        print cycle sep result sep work sep conclusion sep verification sep review sep risk
      }
    }
    function reset() {
      result = ""; work = ""; conclusion = ""; verification = ""; review = ""; risk = ""
    }
    /^## Cycle [0-9]+$/ {
      flush(); reset(); cycle = $3; next
    }
    /^사이클 [0-9]+ 브리핑$/ {
      flush(); reset(); cycle = $2; next
    }
    cycle != "" && /^- Result: / { sub(/^- Result: /, ""); result = $0; next }
    cycle != "" && /^- Work: / { sub(/^- Work: /, ""); work = $0; next }
    cycle != "" && /^- Verification: / { sub(/^- Verification: /, ""); verification = $0; next }
    cycle != "" && /^- Review\/Ship: / { sub(/^- Review\/Ship: /, ""); review = $0; next }
    cycle != "" && /^- Risk: / { sub(/^- Risk: /, ""); risk = $0; next }
    cycle != "" && /^- 결과: / { sub(/^- 결과: /, ""); result = $0; next }
    cycle != "" && /^- 이번에 한 일: / { sub(/^- 이번에 한 일: /, ""); work = $0; next }
    cycle != "" && /^- 결론: / { sub(/^- 결론: /, ""); conclusion = $0; next }
    cycle != "" && /^- 검증: / { sub(/^- 검증: /, ""); verification = $0; next }
    cycle != "" && /^- 리뷰\/배포: / { sub(/^- 리뷰\/배포: /, ""); review = $0; next }
    cycle != "" && /^- 리스크: / { sub(/^- 리스크: /, ""); risk = $0; next }
    END { flush() }
  ' "$log" | while IFS="$(printf '\034')" read -r cycle result work conclusion verification review risk; do
    [[ "$cycle" =~ ^[0-9]+$ ]] || continue
    result="${result:-legacy}"
    work="${work:-${conclusion:-기존 Markdown brief에서 복원한 cycle입니다.}}"
    conclusion="${conclusion:-$work}"
    verification="${verification:-기존 Markdown brief에서 복원했습니다.}"
    review="${review:-기존 Markdown brief에서 복원했습니다.}"
    if is_empty_risk "$risk"; then
      if ! jq -nc \
        --argjson cycle "$cycle" \
        --arg result "$result" \
        --arg work "$work" \
        --arg conclusion "$conclusion" \
        --arg verification "$verification" \
        --arg review "$review" \
        --arg run_id "$run_id" \
        --arg repo "$repo" \
        --arg repo_type "$repo_type" \
        --arg branch "$branch" \
        --arg head_sha "$head_sha" \
        '{
          schema_version:1,
          cycle:$cycle,
          result:$result,
          actions:[{kind:"legacy_markdown", summary_ko:$work}],
          conclusion:{summary_ko:$conclusion},
          changes:[],
          verification:[{kind:"legacy_markdown", status:"recorded", summary_ko:$verification}],
          review_ship:{status:"recorded", summary_ko:$review},
          next_candidates:[],
          risks:[],
          run_id:$run_id,
          recorded_at:"legacy_markdown_backfill",
          repo:{name:$repo, type:$repo_type, branch:$branch, head:$head_sha}
        }'; then
        exit 1
      fi
    else
      if ! jq -nc \
        --argjson cycle "$cycle" \
        --arg result "$result" \
        --arg work "$work" \
        --arg conclusion "$conclusion" \
        --arg verification "$verification" \
        --arg review "$review" \
        --arg risk "$risk" \
        --arg run_id "$run_id" \
        --arg repo "$repo" \
        --arg repo_type "$repo_type" \
        --arg branch "$branch" \
        --arg head_sha "$head_sha" \
        '{
          schema_version:1,
          cycle:$cycle,
          result:$result,
          actions:[{kind:"legacy_markdown", summary_ko:$work}],
          conclusion:{summary_ko:$conclusion},
          changes:[],
          verification:[{kind:"legacy_markdown", status:"recorded", summary_ko:$verification}],
          review_ship:{status:"recorded", summary_ko:$review},
          next_candidates:[],
          risks:[{summary_ko:$risk}],
          run_id:$run_id,
          recorded_at:"legacy_markdown_backfill",
          repo:{name:$repo, type:$repo_type, branch:$branch, head:$head_sha}
        }'; then
        exit 1
      fi
    fi
  done > "$tmp"; then
    rm -f "$tmp"
    return 1
  fi

  if [[ -s "$tmp" ]]; then
    mv "$tmp" "$jsonl" || {
      rm -f "$tmp"
      return 1
    }
  else
    rm -f "$tmp"
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
