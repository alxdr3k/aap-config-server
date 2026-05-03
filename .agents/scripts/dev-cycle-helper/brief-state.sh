# shellcheck shell=bash

init_brief() {
  local run_id started_at start_epoch state_dir log jsonl audit_jsonl run_json run_id_file start_epoch_file base_sha
  require_jq || return 1
  run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
  started_at="$(iso_now)"
  start_epoch="$(date -u +%s)"
  state_dir="$(ensure_state_dir)"
  log="$state_dir/dev-cycle-briefs.md"
  jsonl="$(brief_jsonl_file "$state_dir")"
  audit_jsonl="$(brief_audit_jsonl_file "$state_dir")"
  run_json="$(brief_run_json_file "$state_dir")"
  run_id_file="$(brief_run_id_file "$state_dir")"
  start_epoch_file="$(brief_start_epoch_file "$state_dir")"
  base_sha="$(git rev-parse --short HEAD 2>/dev/null || true)"
  printf "# Dev Cycle Briefs %s\n\n" "$run_id" > "$log" || return 1
  : > "$jsonl" || return 1
  : > "$audit_jsonl" || return 1
  jq -n \
    --arg run_id "$run_id" \
    --arg started_at "$started_at" \
    --arg repo "$(repo_name)" \
    --arg repo_type "$(repo_type)" \
    --arg root "$(repo_root)" \
    --arg base_sha "$base_sha" \
    '{schema_version:1, run_id:$run_id, started_at:$started_at, repo:{name:$repo, type:$repo_type, root:$root}, base_sha:(if $base_sha == "" then null else $base_sha end)}' \
    > "$run_json" || return 1
  printf '%s\n' "$run_id" > "$run_id_file" || return 1
  printf '%s\n' "$start_epoch" > "$start_epoch_file" || return 1
  shell_export DEV_CYCLE_RUN_ID "$run_id"
  shell_export DEV_CYCLE_BRIEF_LOG "$log"
  shell_export DEV_CYCLE_BRIEF_JSONL "$jsonl"
  shell_export DEV_CYCLE_AUDIT_JSONL "$audit_jsonl"
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
    cycle != "" && /^- Review\/Land: / { sub(/^- Review\/Land: /, ""); review = $0; next }
    cycle != "" && /^- Risk: / { sub(/^- Risk: /, ""); risk = $0; next }
    cycle != "" && /^- 결과: / { sub(/^- 결과: /, ""); result = $0; next }
    cycle != "" && /^- 이번에 한 일: / { sub(/^- 이번에 한 일: /, ""); work = $0; next }
    cycle != "" && /^- 결론: / { sub(/^- 결론: /, ""); conclusion = $0; next }
    cycle != "" && /^- 검증: / { sub(/^- 검증: /, ""); verification = $0; next }
    cycle != "" && /^- 리뷰\/배포: / { sub(/^- 리뷰\/배포: /, ""); review = $0; next }
    cycle != "" && /^- 리뷰\/반영: / { sub(/^- 리뷰\/반영: /, ""); review = $0; next }
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
          review_land:{status:"recorded", summary_ko:$review},
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
          review_land:{status:"recorded", summary_ko:$review},
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

record_audit_baseline() {
  local context state_dir run_json cycles_jsonl post_sync_sha tmp base
  require_jq || return 1
  context="$(brief_context)" || return 1
  state_dir="$(ensure_state_dir)"
  run_json="$(brief_run_json_file "$state_dir")"
  cycles_jsonl="$(brief_jsonl_file "$state_dir")"

  # idempotent: only update before any cycle is finished
  if [[ -s "$cycles_jsonl" ]]; then
    return 0
  fi

  # baseline must track the resolved review base, not the current work branch.
  # In standard repos sync_repo can leave HEAD on a stale non-base branch, which
  # would otherwise pull unrelated upstream commits into the first audit window.
  base="$(review_base 2>/dev/null || true)"
  post_sync_sha=""
  if [[ -n "$base" ]]; then
    post_sync_sha="$(git rev-parse --short "refs/remotes/origin/$base" 2>/dev/null || true)"
    [[ -z "$post_sync_sha" ]] && post_sync_sha="$(git rev-parse --short "refs/heads/$base" 2>/dev/null || true)"
  fi
  [[ -z "$post_sync_sha" ]] && post_sync_sha="$(git rev-parse --short HEAD 2>/dev/null || true)"
  [[ -n "$post_sync_sha" ]] || return 0
  [[ -f "$run_json" ]] || return 0

  tmp="$(mktemp)" || return 1
  if ! jq --arg s "$post_sync_sha" '.base_sha = $s' "$run_json" > "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  mv "$tmp" "$run_json" || {
    rm -f "$tmp"
    return 1
  }
}

audit_pass_json() {
  local input_file context run_id log state_dir cycles_jsonl audit_jsonl after_cycle audit_turn now branch head_sha repo repo_type record_file rendered status audit_every
  require_jq || return 1

  audit_every="${1:-0}"
  if ! [[ "$audit_every" =~ ^[0-9]+$ ]]; then
    echo "audit-pass-json: optional first argument must be a non-negative integer audit_every (got: $audit_every). Pass the value of --opus-audit-every to enforce exact window coverage; pass 0 or omit to skip length enforcement." >&2
    return 1
  fi

  input_file="$(mktemp)" || return 1
  cat > "$input_file" || {
    rm -f "$input_file"
    return 1
  }

  if ! jq -e '
    def nonempty_string: type == "string" and test("\\S");
    type == "object" and
    .schema_version == 1 and
    ((.after_cycle | type) == "number") and (.after_cycle >= 1) and (.after_cycle == (.after_cycle | floor)) and
    (.summary_ko | nonempty_string) and
    ((.findings // []) | type == "array") and
    all((.findings // [])[]; ((.summary_ko // .summary // "") | nonempty_string)) and
    ((.recommended_next // []) | type == "array") and
    all((.recommended_next // [])[]; ((.summary_ko // .summary // .id // "") | nonempty_string)) and
    ((.over_cycles // []) | type == "array") and
    all((.over_cycles // [])[]; (type == "number") and . >= 1 and . == (. | floor))
  ' "$input_file" >/dev/null; then
    echo "Invalid opus audit pass JSON. Required: schema_version=1, integer after_cycle>=1, summary_ko, optional findings[].summary_ko, optional recommended_next[].summary_ko/id, optional over_cycles[] integer." >&2
    rm -f "$input_file"
    return 1
  fi

  context="$(brief_context)" || {
    rm -f "$input_file"
    return 1
  }
  run_id="$(printf '%s\n' "$context" | sed -n '1p')"
  log="$(printf '%s\n' "$context" | sed -n '2p')"
  state_dir="$(ensure_state_dir)"
  cycles_jsonl="$(brief_jsonl_file "$state_dir")"
  audit_jsonl="$(brief_audit_jsonl_file "$state_dir")"
  touch "$cycles_jsonl" || {
    rm -f "$input_file"
    return 1
  }
  touch "$audit_jsonl" || {
    rm -f "$input_file"
    return 1
  }
  backfill_jsonl_from_markdown_if_needed "$log" "$cycles_jsonl" "$run_id" || {
    rm -f "$input_file"
    return 1
  }

  after_cycle="$(jq -r '.after_cycle' "$input_file")" || {
    rm -f "$input_file"
    return 1
  }

  if [[ -s "$cycles_jsonl" ]]; then
    if ! jq -e --argjson c "$after_cycle" 'select(.cycle == $c)' "$cycles_jsonl" >/dev/null 2>&1; then
      echo "after_cycle $after_cycle is not yet recorded in $cycles_jsonl. Run finish-cycle-json for that cycle before recording an audit pass." >&2
      rm -f "$input_file"
      return 1
    fi
    local latest_cycle
    latest_cycle="$(jq -s 'map(.cycle) | max // 0' "$cycles_jsonl")" || {
      rm -f "$input_file"
      return 1
    }
    if [[ "$after_cycle" != "$latest_cycle" ]]; then
      echo "after_cycle ($after_cycle) must equal the most recent recorded cycle ($latest_cycle). Audit gate cannot record a stale window after newer cycles have completed." >&2
      rm -f "$input_file"
      return 1
    fi
  else
    echo "No cycles recorded yet; audit pass requires at least one finished cycle." >&2
    rm -f "$input_file"
    return 1
  fi

  if ! jq -e --argjson after "$after_cycle" --argjson audit_every "$audit_every" --slurpfile cs "$cycles_jsonl" '
    ((.over_cycles // []) | length) as $n |
    ($n >= 1) and
    (if $audit_every > 0 then $n == $audit_every else true end) and
    (
      (.over_cycles // []) as $oc |
      ($cs | map(.cycle)) as $available |
      ($oc | map(select(($available | index(.)) == null)) | length == 0) and
      ($oc[-1] == $after) and
      (($oc[0]) as $start | ([range($start; $start + $n)]) == $oc)
    )
  ' "$input_file" >/dev/null; then
    echo "over_cycles must be a non-empty contiguous ascending list of cycle numbers ending at after_cycle ($after_cycle), every cycle must already be recorded in $cycles_jsonl, and (if audit_every > 0) length must equal audit_every ($audit_every)." >&2
    rm -f "$input_file"
    return 1
  fi

  record_file="$(mktemp)" || {
    rm -f "$input_file"
    return 1
  }

  local existing_record marker idempotent norm_input norm_existing
  existing_record=""
  idempotent=false
  if [[ -s "$audit_jsonl" ]]; then
    existing_record="$(jq -c --argjson c "$after_cycle" 'select(.after_cycle == $c)' "$audit_jsonl" 2>/dev/null | head -1)"
  fi

  if [[ -n "$existing_record" ]]; then
    norm_input="$(jq -c '{schema_version, after_cycle, summary_ko, findings:(.findings // []), recommended_next:(.recommended_next // []), over_cycles:(.over_cycles // []), no_action_reason_ko:(.no_action_reason_ko // null)}' "$input_file")" || {
      rm -f "$input_file" "$record_file"
      return 1
    }
    norm_existing="$(printf '%s\n' "$existing_record" | jq -c '{schema_version, after_cycle, summary_ko, findings:(.findings // []), recommended_next:(.recommended_next // []), over_cycles:(.over_cycles // []), no_action_reason_ko:(.no_action_reason_ko // null)}')" || {
      rm -f "$input_file" "$record_file"
      return 1
    }
    if [[ "$norm_input" != "$norm_existing" ]]; then
      echo "Audit pass for after_cycle $after_cycle is already recorded with a different payload. Idempotent retry must replay the same input. To replace audit results, edit .dev-cycle/dev-cycle-audit-passes.jsonl directly or start a new run with init-brief." >&2
      rm -f "$input_file" "$record_file"
      return 1
    fi
    printf '%s\n' "$existing_record" > "$record_file" || {
      rm -f "$input_file" "$record_file"
      return 1
    }
    idempotent=true
  else
    audit_turn=$(($(wc -l < "$audit_jsonl" | tr -d '[:space:]') + 1))
    now="$(iso_now)"
    branch="$(git branch --show-current 2>/dev/null || true)"
    head_sha="$(git rev-parse --short HEAD 2>/dev/null || true)"
    repo="$(repo_name)"
    repo_type="$(repo_type)"

    if ! jq \
      --arg run_id "$run_id" \
      --arg recorded_at "$now" \
      --arg repo "$repo" \
      --arg repo_type "$repo_type" \
      --arg branch "$branch" \
      --arg head_sha "$head_sha" \
      --argjson audit_turn "$audit_turn" \
      '. + {
        kind:"opus_audit_pass",
        audit_turn:$audit_turn,
        run_id:$run_id,
        recorded_at:$recorded_at,
        repo:{name:$repo, type:$repo_type, branch:$branch, head:$head_sha},
        findings:(.findings // []),
        recommended_next:(.recommended_next // []),
        over_cycles:(.over_cycles // [])
      }' "$input_file" > "$record_file"; then
      rm -f "$input_file" "$record_file"
      return 1
    fi
  fi

  if ! rendered="$(render_audit_markdown "$record_file")"; then
    rm -f "$input_file" "$record_file"
    return 1
  fi

  marker="$(printf 'Opus 환기 audit (turn %s, after cycle %s)' "$(jq -r '.audit_turn' "$record_file")" "$after_cycle")"
  if ! grep -qxF "$marker" "$log"; then
    if ! printf '%s\n\n' "$rendered" >> "$log"; then
      rm -f "$input_file" "$record_file"
      return 1
    fi
  fi

  if [[ -z "$existing_record" ]]; then
    if ! jq -c . "$record_file" >> "$audit_jsonl"; then
      rm -f "$input_file" "$record_file"
      return 1
    fi
  fi

  if ! jq -n \
    --slurpfile record "$record_file" \
    --arg rendered_markdown "$rendered" \
    --argjson idempotent "$idempotent" \
    '{
      ok:true,
      kind:"opus_audit_pass",
      audit_turn:$record[0].audit_turn,
      after_cycle:$record[0].after_cycle,
      rendered_markdown:$rendered_markdown,
      idempotent:$idempotent
    }'; then
    status=$?
    rm -f "$input_file" "$record_file"
    return "$status"
  fi
  rm -f "$input_file" "$record_file"
}
