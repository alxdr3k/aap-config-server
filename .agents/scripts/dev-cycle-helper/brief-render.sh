# shellcheck shell=bash

render_cycle_markdown() {
  local payload_file="$1"
  jq -r '
    def result_label:
      (.result // "" | tostring) as $r |
      ($r | ascii_downcase | gsub("[ _-]"; "")) as $n |
      if $n == "allclear" then "ALL CLEAR"
      elif $n == "shipped" then "배포 완료 (shipped)"
      elif $n == "blocked" then "차단됨 (blocked)"
      elif $n == "docfixneeded" then "문서 수정 필요 (doc_fix_needed)"
      else $r end;
    def summaries($items):
      [($items // [])[]? | (.summary_ko // .summary // .command // empty) | tostring | select(. != "")];
    def joined($items):
      (summaries($items)) as $xs |
      if ($xs | length) == 0 then "기록 없음"
      elif ($xs | length) == 1 then $xs[0]
      else "\n" + ($xs | map("  - " + .) | join("\n")) end;
    def field($label; $value):
      if ($value | startswith("\n")) then "- \($label):\($value)"
      else "- \($label): \($value)" end;
    def candidate_line:
      "- " + ((.id // "후보") | tostring) +
      (if (.summary_ko // "") != "" then ": " + (.summary_ko | tostring) else "" end) +
      (if (.status // "") != "" then " (" + (.status | tostring) + ")" else "" end) +
      (if (.unblock_ko // "") != "" then " 시작 조건: " + (.unblock_ko | tostring) else "" end);
    def promotion_candidate_line:
      "- " + ((.id // "후보") | tostring) +
      (if (.summary_ko // .summary // "") != "" then ": " + ((.summary_ko // .summary) | tostring) else "" end) +
      (if (.status // "") != "" then " (" + (.status | tostring) + ")" else "" end) +
      (if has("eligible") then (if .eligible then " - 자동 승격 가능" else " - 자동 승격 제외" end) else "" end) +
      (if (.reason_ko // .unblock_ko // "") != "" then " 이유: " + ((.reason_ko // .unblock_ko) | tostring) else "" end);
    def promotion_line:
      "- " + ((.id // "후보") | tostring) +
      (if (.summary_ko // .summary // "") != "" then ": " + ((.summary_ko // .summary) | tostring) else "" end) +
      (if ((.status_before // "") != "" or (.status_after // "") != "") then " (" + ((.status_before // "?") | tostring) + " -> " + ((.status_after // "ready") | tostring) + ")" else "" end) +
      (if (.path // "") != "" then " 파일: " + (.path | tostring) else "" end) +
      (if (.reason_ko // "") != "" then " 이유: " + (.reason_ko | tostring) else "" end);
    def risk_line:
      ((.summary_ko // .summary // "기록 없음") | tostring) +
      (if (.issue_url // "") != "" then " (이슈: " + (.issue_url | tostring) + ")" else "" end) +
      (if (.issue_error // "") != "" then " (이슈 생성 실패: " + (.issue_error | tostring) + ")" else "" end) +
      (if (.next_action_ko // "") != "" then " 다음 조치: " + (.next_action_ko | tostring) else "" end);
    [
      "사이클 \(.cycle) 브리핑",
      "",
      "- 결과: \(result_label)",
      field("이번에 한 일"; joined(.actions)),
      "- 결론: \(.conclusion.summary_ko // "기록 없음")" + (if (.conclusion.reason_ko // "") != "" then " " + (.conclusion.reason_ko | tostring) else "" end),
      (if ((.next_candidates // []) | length) > 0 then "- 다음 검토 후보:\n" + ((.next_candidates // []) | map("  " + candidate_line) | join("\n")) else empty end),
      (if has("auto_promotion_candidates") then (if ((.auto_promotion_candidates // []) | length) > 0 then "- 자동 승격 검토:\n" + ((.auto_promotion_candidates // []) | map("  " + promotion_candidate_line) | join("\n")) else "- 자동 승격 검토: 후보 없음" end) else empty end),
      (if has("auto_promotions") then (if ((.auto_promotions // []) | length) > 0 then "- 자동 승격:\n" + ((.auto_promotions // []) | map("  " + promotion_line) | join("\n")) else "- 자동 승격: 없음" end) else empty end),
      field("검증"; joined(.verification)),
      "- 리뷰/배포: \(.review_ship.summary_ko // .review_ship.status // "기록 없음")",
      (if ((.risks // []) | length) > 0 then "- 리스크:\n" + ((.risks // []) | map("  - " + risk_line) | join("\n")) else "- 리스크: 없음" end)
    ] | join("\n")
  ' "$payload_file"
}

finish_cycle_json_file() {
  local input_file="$1" output_mode="${2:-json}"
  local context run_id log state_dir jsonl cycle now branch head_sha repo repo_type record_file rendered issue_url issue_err issue_msg risk_count title summary
  require_jq || return 1
  context="$(brief_context)" || return 1
  run_id="$(printf '%s\n' "$context" | sed -n '1p')"
  log="$(printf '%s\n' "$context" | sed -n '2p')"
  state_dir="$(ensure_state_dir)"
  jsonl="$(brief_jsonl_file "$state_dir")"
  touch "$jsonl" || return 1
  backfill_jsonl_from_markdown_if_needed "$log" "$jsonl" "$run_id" || return 1

  if ! jq -e '
    def nonempty_string: type == "string" and test("\\S");
    def item_summary: ((.summary_ko // .summary // .command // "") | nonempty_string);
    def optional_item_summary: ((.summary_ko // .summary // .id // "") | nonempty_string);
    def candidate_item: optional_item_summary and ((has("eligible") | not) or (.eligible | type == "boolean"));
    type == "object" and
    .schema_version == 1 and
    ((.cycle | type) == "number") and (.cycle >= 1) and (.cycle == (.cycle | floor)) and
    (.result | nonempty_string) and
    (.actions | type == "array" and length > 0) and
    all(.actions[]; item_summary) and
    (.conclusion | type == "object") and
    (.conclusion.summary_ko | nonempty_string) and
    (.verification | type == "array" and length > 0) and
    all(.verification[]; item_summary) and
    (.review_ship | type == "object") and
    ((.review_ship.summary_ko // .review_ship.status // "") | nonempty_string) and
    (.risks | type == "array") and
    all(.risks[]; ((.summary_ko // .summary // "") | nonempty_string)) and
    (.next_candidates // [] | type == "array") and
    (.auto_promotion_candidates // [] | type == "array") and
    all((.auto_promotion_candidates // [])[]; candidate_item) and
    (.auto_promotions // [] | type == "array") and
    all((.auto_promotions // [])[]; optional_item_summary)
  ' "$input_file" >/dev/null; then
    echo "Invalid dev-cycle brief JSON. Required: schema_version=1, integer cycle, result, non-empty actions[].summary_ko, conclusion.summary_ko, non-empty verification[].summary_ko, review_ship summary/status, risks[].summary_ko when risks are present. Optional candidate/promotion arrays must contain items with summary_ko, summary, or id." >&2
    return 1
  fi

  cycle="$(jq -r '.cycle' "$input_file")" || return 1
  validate_cycle_append "$cycle" "$log" || return 1
  validate_jsonl_append "$cycle" "$jsonl" || return 1

  now="$(iso_now)"
  branch="$(git branch --show-current 2>/dev/null || true)"
  head_sha="$(git rev-parse --short HEAD 2>/dev/null || true)"
  repo="$(repo_name)"
  repo_type="$(repo_type)"
  record_file="$(mktemp)" || return 1
  jq \
    --arg run_id "$run_id" \
    --arg recorded_at "$now" \
    --arg repo "$repo" \
    --arg repo_type "$repo_type" \
    --arg branch "$branch" \
    --arg head_sha "$head_sha" \
    '. + {
      run_id:$run_id,
      recorded_at:$recorded_at,
      repo:{name:$repo, type:$repo_type, branch:$branch, head:$head_sha}
    } |
    .changes = (.changes // []) |
    .next_candidates = (.next_candidates // []) |
    .risks = ([.risks[]? | select(((.summary_ko // .summary // "") | tostring | length) > 0)])' \
    "$input_file" > "$record_file" || {
      rm -f "$record_file"
      return 1
    }

  risk_count="$(jq '[.risks[]? | select((.summary_ko // .summary // "") != "")] | length' "$record_file")" || {
    rm -f "$record_file"
    return 1
  }
  if (( risk_count > 0 )); then
    rendered="$(render_cycle_markdown "$record_file")" || {
      rm -f "$record_file"
      return 1
    }
    summary="$(jq -r '
      (.risks[0].summary_ko // .risks[0].summary // "dev-cycle risk")
      | tostring
      | split("\n")[0]
      | if length > 90 then .[0:90] else . end
    ' "$record_file")" || {
      rm -f "$record_file"
      return 1
    }
    title="[dev-cycle risk] $summary"
    issue_err="$(mktemp)" || {
      rm -f "$record_file"
      return 1
    }
    if issue_url="$(gh issue create --title "$title" --body "$rendered" 2>"$issue_err")"; then
      rm -f "$issue_err"
      jq --arg issue_url "$issue_url" '
        .risks = (.risks | map(. + {issue_url:$issue_url})) |
        .review_ship.summary_ko = ((.review_ship.summary_ko // .review_ship.status // "기록 없음") + "; 리스크 이슈 생성: " + $issue_url)
      ' "$record_file" > "$record_file.tmp" && mv "$record_file.tmp" "$record_file" || {
        rm -f "$record_file" "$record_file.tmp"
        return 1
      }
    else
      issue_msg="$(sed -n '1p' "$issue_err" 2>/dev/null || true)"
      rm -f "$issue_err"
      jq --arg issue_error "$issue_msg" '
        .risks = (.risks | map(. + {issue_error:$issue_error})) |
        .review_ship.summary_ko = ((.review_ship.summary_ko // .review_ship.status // "기록 없음") + "; 리스크 이슈 생성 실패")
      ' "$record_file" > "$record_file.tmp" && mv "$record_file.tmp" "$record_file" || {
        rm -f "$record_file" "$record_file.tmp"
        return 1
      }
      echo "리스크 이슈 생성 실패; 이슈 링크 없이 브리핑을 기록했습니다." >&2
    fi
  fi

  rendered="$(render_cycle_markdown "$record_file")" || {
    rm -f "$record_file"
    return 1
  }
  jq -c . "$record_file" >> "$jsonl" || {
    rm -f "$record_file"
    return 1
  }
  printf '%s\n\n' "$rendered" >> "$log" || {
    rm -f "$record_file"
    return 1
  }

  if [[ "$output_mode" == "markdown" ]]; then
    printf '%s\n\n' "$rendered" || {
      rm -f "$record_file"
      return 1
    }
  else
    jq -n \
      --slurpfile record "$record_file" \
      --arg rendered_markdown "$rendered" \
      '{
        ok:true,
        cycle:$record[0].cycle,
        result:$record[0].result,
        auto_promotions_count:(($record[0].auto_promotions // []) | length),
        rendered_markdown:$rendered_markdown
      }' || {
        rm -f "$record_file"
        return 1
      }
  fi
  rm -f "$record_file"
}

finish_cycle_json() {
  local input_file status
  input_file="$(mktemp)" || return 1
  cat > "$input_file" || {
    rm -f "$input_file"
    return 1
  }
  if finish_cycle_json_file "$input_file" json; then
    status=0
  else
    status=$?
  fi
  rm -f "$input_file"
  return "$status"
}

finish_cycle() {
  local cycle result work verification review_ship risk next_action payload_file status
  cycle="${DEV_CYCLE_CYCLE:?set DEV_CYCLE_CYCLE}"
  result="${DEV_CYCLE_RESULT:?set DEV_CYCLE_RESULT}"
  work="${DEV_CYCLE_WORK:?set DEV_CYCLE_WORK}"
  verification="${DEV_CYCLE_VERIFICATION:?set DEV_CYCLE_VERIFICATION}"
  review_ship="${DEV_CYCLE_REVIEW_SHIP:?set DEV_CYCLE_REVIEW_SHIP}"
  risk="${DEV_CYCLE_RISK:-없음}"
  next_action="${DEV_CYCLE_NEXT_ACTION:-기록된 리스크를 다음 cycle에서 triage합니다.}"

  if [[ ! "$cycle" =~ ^[0-9]+$ ]]; then
    echo "DEV_CYCLE_CYCLE must be numeric for JSON brief handling" >&2
    return 1
  fi

  payload_file="$(mktemp)" || return 1
  if is_empty_risk "$risk"; then
    jq -n \
      --argjson cycle "$cycle" \
      --arg result "$result" \
      --arg work "$work" \
      --arg verification "$verification" \
      --arg review_ship "$review_ship" \
      '{
        schema_version:1,
        cycle:$cycle,
        result:$result,
        actions:[{kind:"legacy", summary_ko:$work}],
        conclusion:{summary_ko:$work},
        changes:[],
        verification:[{kind:"legacy", status:"recorded", summary_ko:$verification}],
        review_ship:{status:"recorded", summary_ko:$review_ship},
        next_candidates:[],
        risks:[]
      }' > "$payload_file" || {
        rm -f "$payload_file"
        return 1
      }
  else
    jq -n \
      --argjson cycle "$cycle" \
      --arg result "$result" \
      --arg work "$work" \
      --arg verification "$verification" \
      --arg review_ship "$review_ship" \
      --arg risk "$risk" \
      --arg next_action "$next_action" \
      '{
        schema_version:1,
        cycle:$cycle,
        result:$result,
        actions:[{kind:"legacy", summary_ko:$work}],
        conclusion:{summary_ko:$work},
        changes:[],
        verification:[{kind:"legacy", status:"recorded", summary_ko:$verification}],
        review_ship:{status:"recorded", summary_ko:$review_ship},
        next_candidates:[],
        risks:[{summary_ko:$risk, next_action_ko:$next_action}]
      }' > "$payload_file" || {
        rm -f "$payload_file"
        return 1
      }
  fi

  if finish_cycle_json_file "$payload_file" markdown; then
    status=0
  else
    status=$?
  fi
  rm -f "$payload_file"
  return "$status"
}

summary_json() {
  local context run_id log state_dir jsonl start_epoch_file start_epoch now elapsed elapsed_text repo
  require_jq || return 1
  context="$(brief_context)" || return 1
  run_id="$(printf '%s\n' "$context" | sed -n '1p')"
  log="$(printf '%s\n' "$context" | sed -n '2p')"
  state_dir="$(ensure_state_dir)"
  jsonl="$(brief_jsonl_file "$state_dir")"
  if [[ ! -f "$jsonl" ]]; then
    : > "$jsonl" || return 1
  fi
  backfill_jsonl_from_markdown_if_needed "$log" "$jsonl" "$run_id" || return 1
  validate_jsonl_state "$jsonl" || return 1
  start_epoch_file="$(brief_start_epoch_file "$state_dir")"
  elapsed=0
  if [[ -f "$start_epoch_file" ]]; then
    start_epoch="$(sed -n '1p' "$start_epoch_file")"
    if [[ "$start_epoch" =~ ^[0-9]+$ ]]; then
      now="$(date -u +%s)"
      elapsed=$((now - start_epoch))
    fi
  fi
  elapsed_text="$(format_duration "$elapsed")"
  repo="$(repo_name)"
  jq -n \
    --slurpfile cycles "$jsonl" \
    --arg run_id "$run_id" \
    --arg repo "$repo" \
    --arg log "$log" \
    --argjson elapsed_seconds "$elapsed" \
    --arg elapsed_text "$elapsed_text" '
    def result_label($r):
      ($r // "" | tostring) as $v |
      ($v | ascii_downcase | gsub("[ _-]"; "")) as $n |
      if $n == "allclear" then "ALL CLEAR"
      elif $n == "shipped" then "배포 완료 (shipped)"
      elif $n == "blocked" then "차단됨 (blocked)"
      elif $n == "docfixneeded" then "문서 수정 필요 (doc_fix_needed)"
      else $v end;
    def headline($c):
      $c.conclusion.summary_ko // ([$c.actions[]?.summary_ko][0]) // "기록 없음";
    def item_text:
      (.summary_ko // .summary // .command // empty) | tostring | select(. != "");
    def risk_text:
      ((.summary_ko // .summary // "기록 없음") | tostring) +
      (if (.issue_url // "") != "" then " (이슈: " + (.issue_url | tostring) + ")" else "" end) +
      (if (.issue_error // "") != "" then " (이슈 생성 실패: " + (.issue_error | tostring) + ")" else "" end) +
      (if (.next_action_ko // "") != "" then " 다음 조치: " + (.next_action_ko | tostring) else "" end);
    def candidate_text:
      ((.id // "후보") | tostring) + ": " +
      ((.summary_ko // "설명 없음") | tostring) +
      (if (.status // "") != "" then " (" + (.status | tostring) + ")" else "" end) +
      (if (.unblock_ko // "") != "" then " 시작 조건: " + (.unblock_ko | tostring) else "" end);
    def promotion_candidate_text:
      ((.id // "후보") | tostring) + ": " +
      ((.summary_ko // .summary // "설명 없음") | tostring) +
      (if (.status // "") != "" then " (" + (.status | tostring) + ")" else "" end) +
      (if has("eligible") then (if .eligible then " - 자동 승격 가능" else " - 자동 승격 제외" end) else "" end) +
      (if (.reason_ko // .unblock_ko // "") != "" then " 이유: " + ((.reason_ko // .unblock_ko) | tostring) else "" end);
    def promotion_text:
      ((.id // "후보") | tostring) + ": " +
      ((.summary_ko // .summary // "설명 없음") | tostring) +
      (if ((.status_before // "") != "" or (.status_after // "") != "") then " (" + ((.status_before // "?") | tostring) + " -> " + ((.status_after // "ready") | tostring) + ")" else "" end) +
      (if (.path // "") != "" then " 파일: " + (.path | tostring) else "" end) +
      (if (.reason_ko // "") != "" then " 이유: " + (.reason_ko | tostring) else "" end);
    def block($label; $xs):
      if ($xs | length) == 0 then "- \($label): 없음"
      elif ($xs | length) == 1 then "- \($label): \($xs[0])"
      else "- \($label):\n" + ($xs | map("  - " + .) | join("\n")) end;
    ($cycles | length) as $count |
    ($cycles[-1] // {}) as $last |
    ($last.next_candidates // []) as $candidates |
    ([$cycles[]? as $c | ($c.auto_promotion_candidates // [])[]? | "사이클 \($c.cycle): \(promotion_candidate_text)"]) as $promotion_candidates |
    ([$cycles[]? as $c | ($c.auto_promotions // [])[]? | "사이클 \($c.cycle): \(promotion_text)"]) as $promotions |
    ([
      "최종 브리핑",
      "",
      "- 결과: 총 \($count)개 사이클, 마지막 결과 \(result_label($last.result // "none"))",
      block("작업"; if $count == 0 then [] else [$cycles[] | "사이클 \(.cycle): \(headline(.))"] end),
      block("검증"; [$cycles[] as $c | ($c.verification // [])[]? | item_text as $v | "사이클 \($c.cycle): \($v)"]),
      block("리뷰/배포"; if $count == 0 then [] else [$cycles[] | "사이클 \(.cycle): \(.review_ship.summary_ko // .review_ship.status // "기록 없음")"] end),
      (if ($promotion_candidates | length) > 0 then block("자동 승격 검토"; $promotion_candidates) else empty end),
      (if ($promotions | length) > 0 then block("자동 승격"; $promotions) else empty end),
      (if ($candidates | length) > 0 then block("다음 검토 후보"; [$candidates[] | candidate_text]) else empty end),
      block("리스크"; [$cycles[] as $c | ($c.risks // [])[]? | risk_text as $r | "사이클 \($c.cycle): \($r)"]),
      "- 걸린 시간: \($elapsed_text)"
    ] | join("\n")) as $rendered |
    {
      schema_version:1,
      run_id:$run_id,
      repo:$repo,
      log:$log,
      elapsed:{seconds:$elapsed_seconds, text:$elapsed_text},
      cycles:($cycles | map({cycle, result, headline_ko:headline(.)})),
      open_risks:[$cycles[]?.risks[]?],
      next_candidates:$candidates,
      auto_promotion_candidates:[$cycles[]?.auto_promotion_candidates[]?],
      auto_promotions:[$cycles[]?.auto_promotions[]?],
      rendered_markdown:$rendered
    }'
}

summary() {
  local context log state_dir start_epoch_file start_epoch now elapsed
  context="$(brief_context)"
  log="$(printf '%s\n' "$context" | sed -n '2p')"
  sed -n '1,120p' "$log"
  state_dir="$(ensure_state_dir)"
  start_epoch_file="$(brief_start_epoch_file "$state_dir")"
  if [[ -f "$start_epoch_file" ]]; then
    start_epoch="$(sed -n '1p' "$start_epoch_file")"
    if [[ "$start_epoch" =~ ^[0-9]+$ ]]; then
      now="$(date -u +%s)"
      elapsed=$((now - start_epoch))
      printf -- '- Elapsed: %s\n' "$(format_duration "$elapsed")"
    fi
  fi
}
