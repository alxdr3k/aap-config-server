# shellcheck shell=bash

line_count() {
  local file="$1"
  if [[ -s "$file" ]]; then
    wc -l < "$file" | tr -d ' '
  else
    echo 0
  fi
}

git_ref_exists() {
  git rev-parse --verify --quiet "$1" >/dev/null
}

review_range_ref() {
  local type="$1" base="$2"

  if [[ "$type" == "direct-push" ]] && git_ref_exists "refs/remotes/origin/main"; then
    echo "origin/main"
    return
  fi

  if git_ref_exists "$base"; then
    echo "$base"
  elif git_ref_exists "refs/remotes/origin/$base"; then
    echo "origin/$base"
  else
    echo ""
  fi
}

is_docs_path() {
  local path="$1"
  case "$path" in
    *.md|*.mdx|*.markdown|*.rst|*.adoc|*.txt) return 0 ;;
    docs/*|doc/*) return 0 ;;
    AGENTS.md|CLAUDE.md|README|README.*|CHANGELOG|CHANGELOG.*) return 0 ;;
    commands/*.md|codex/skills/*/SKILL.md|codex/rules/*.rules) return 0 ;;
    .claude/commands/*.md|.codex/skills/*/SKILL.md|.codex/skill-overrides/*.md) return 0 ;;
    *) return 1 ;;
  esac
}

is_contract_docs_path() {
  local path="$1"
  case "$path" in
    AGENTS.md|CLAUDE.md) return 0 ;;
    commands/*.md|codex/skills/*/SKILL.md|codex/rules/*.rules) return 0 ;;
    .claude/commands/*.md|.codex/skills/*/SKILL.md|.codex/skill-overrides/*.md) return 0 ;;
    docs/specs/*|docs/*SPEC*|docs/*spec*|docs/*SCHEMA*|docs/*schema*) return 0 ;;
    docs/*STATUS*|docs/*status*|docs/*ROADMAP*|docs/*roadmap*) return 0 ;;
    docs/*IMPLEMENTATION_PLAN*|docs/*DECISION*|docs/*QUESTIONS*) return 0 ;;
    *) return 1 ;;
  esac
}

classify_change_scope() {
  local files_file="$1" count all_docs contract_surface path
  count="$(line_count "$files_file")"
  if (( count == 0 )); then
    echo "none none false false"
    return
  fi

  all_docs=true
  contract_surface=false
  while IFS= read -r path; do
    [[ -n "$path" ]] || continue
    if ! is_docs_path "$path"; then
      all_docs=false
    fi
    if is_contract_docs_path "$path"; then
      contract_surface=true
    fi
  done < "$files_file"

  if [[ "$all_docs" == "true" && "$contract_surface" == "true" ]]; then
    echo "docs_only_contract docs_contract true false"
  elif [[ "$all_docs" == "true" ]]; then
    echo "docs_only_low_risk docs_only false false"
  else
    echo "code_or_runtime full true true"
  fi
}

change_scope() {
  local type base range_ref tmp_dir committed staged unstaged untracked changed
  local committed_count staged_count unstaged_count untracked_count changed_count
  local scope_kind profile contract_surface full_ci_required
  local range_command range_form
  require_jq || return 1

  type="$(repo_type)"
  base="$(review_base)"
  range_ref="$(review_range_ref "$type" "$base")"
  range_command=""
  range_form=""
  tmp_dir="$(mktemp -d)" || return 1
  committed="$tmp_dir/committed"
  staged="$tmp_dir/staged"
  unstaged="$tmp_dir/unstaged"
  untracked="$tmp_dir/untracked"
  changed="$tmp_dir/changed"

  : > "$committed"
  if [[ -n "$range_ref" ]]; then
    if git diff --name-only "$range_ref...HEAD" > "$committed" 2>/dev/null; then
      range_command="git diff $range_ref...HEAD"
      range_form="triple_dot"
    elif git diff --name-only "$range_ref" HEAD > "$committed" 2>/dev/null; then
      range_command="git diff $range_ref HEAD"
      range_form="two_dot"
      echo "WARN: triple-dot diff failed; falling back to two-dot form (semantics differ: includes unrelated base changes)" >&2
    else
      : > "$committed"
      range_form=""
    fi
  fi
  git diff --cached --name-only > "$staged"
  git diff --name-only > "$unstaged"
  git ls-files --others --exclude-standard > "$untracked"
  {
    cat "$committed"
    cat "$staged"
    cat "$unstaged"
    cat "$untracked"
  } | sed '/^$/d' | sort -u > "$changed"

  committed_count="$(line_count "$committed")"
  staged_count="$(line_count "$staged")"
  unstaged_count="$(line_count "$unstaged")"
  untracked_count="$(line_count "$untracked")"
  changed_count="$(line_count "$changed")"

  read -r scope_kind profile contract_surface full_ci_required < <(classify_change_scope "$changed")

  if jq -n \
    --arg repo "$(repo_name)" \
    --arg repo_type "$type" \
    --arg review_base "$base" \
    --arg range_ref "$range_ref" \
    --arg range_command "$range_command" \
    --arg range_form "$range_form" \
    --rawfile committed "$committed" \
    --rawfile staged "$staged" \
    --rawfile unstaged "$unstaged" \
    --rawfile untracked "$untracked" \
    --rawfile changed "$changed" \
    --argjson committed_count "$committed_count" \
    --argjson staged_count "$staged_count" \
    --argjson unstaged_count "$unstaged_count" \
    --argjson untracked_count "$untracked_count" \
    --argjson changed_count "$changed_count" \
    --arg scope_kind "$scope_kind" \
    --arg profile "$profile" \
    --argjson contract_surface "$contract_surface" \
    --argjson full_ci_required "$full_ci_required" '
    def lines($s): $s | split("\n") | map(select(length > 0));
    def review_input($kind; $summary; $command; $files):
      {kind:$kind, summary:$summary, command:$command, files:$files};
    (lines($committed)) as $committed_files |
    (lines($staged)) as $staged_files |
    (lines($unstaged)) as $unstaged_files |
    (lines($untracked)) as $untracked_files |
    (lines($changed)) as $changed_files |
    {
      schema_version:1,
      kind:"dev_cycle_change_scope",
      repo:{name:$repo, type:$repo_type},
      review_base:$review_base,
      range_form:(if $range_form == "" then null else $range_form end),
      change_scope:{
        kind:$scope_kind,
        changed_files_count:$changed_count,
        contract_surface:$contract_surface,
        review_required:($changed_count > 0),
        changed_files:$changed_files
      },
      verification_profile:{
        profile:$profile,
        full_ci_required:$full_ci_required,
        checks:(
          if $profile == "none" then ["git status --short"]
          elif $profile == "docs_only" then ["git diff --check", "relevant markdown/document validation only"]
          elif $profile == "docs_contract" then ["git diff --check", "render/generated skill consistency when command or skill docs changed", "schema/example validation when contract docs changed"]
          else ["repo /verify", "repo full/pre-PR checks"] end
        ),
        skipped:(
          if $full_ci_required then []
          else ["unit/app CI unless repo guidance requires it for the touched docs"] end
        )
      },
      review_inputs:(
        []
        + (if $committed_count > 0 then [review_input("base_range"; "committed changes since review base"; $range_command; $committed_files)] else [] end)
        + (if $staged_count > 0 then [review_input("staged_diff"; "staged changes"; "git diff --cached"; $staged_files)] else [] end)
        + (if $unstaged_count > 0 then [review_input("unstaged_diff"; "unstaged changes"; "git diff"; $unstaged_files)] else [] end)
        + (if $untracked_count > 0 then [review_input("untracked_files"; "untracked files"; "git ls-files --others --exclude-standard"; $untracked_files)] else [] end)
      )
    }'; then
    rm -rf "$tmp_dir"
    return 0
  else
    local status=$?
    rm -rf "$tmp_dir"
    return "$status"
  fi
}

numstat_totals() {
  awk -F '\t' '
    BEGIN { insertions = 0; deletions = 0; files = 0 }
    NF >= 3 {
      files++
      if ($1 ~ /^[0-9]+$/) insertions += $1
      if ($2 ~ /^[0-9]+$/) deletions += $2
    }
    END {
      printf "%s %s %s\n", insertions, deletions, files
    }
  '
}

untracked_text_line_total() {
  local file total lines
  total=0
  while IFS= read -r file; do
    [[ -n "$file" && -f "$file" ]] || continue
    if lines="$(awk 'END { print NR }' "$file" 2>/dev/null | tr -d ' ')"; then
      case "$lines" in
        ''|*[!0-9]*) ;;
        *) total=$((total + lines)) ;;
      esac
    fi
  done
  echo "$total"
}

review_dossier() {
  local type base range_ref tmp_dir scope_json numstat_file untracked_file changed_file
  local committed_numstat_ok numstat_range_form insertions deletions diff_files untracked_lines changed_lines
  require_jq || return 1

  type="$(repo_type)"
  base="$(review_base)"
  range_ref="$(review_range_ref "$type" "$base")"
  tmp_dir="$(mktemp -d)" || return 1
  numstat_file="$tmp_dir/numstat"
  untracked_file="$tmp_dir/untracked"
  changed_file="$tmp_dir/changed"
  : > "$numstat_file"

  committed_numstat_ok=false
  numstat_range_form=""
  if [[ -n "$range_ref" ]]; then
    if git diff --numstat "$range_ref...HEAD" >> "$numstat_file" 2>/dev/null; then
      committed_numstat_ok=true
      numstat_range_form="triple_dot"
    elif git diff --numstat "$range_ref" HEAD >> "$numstat_file" 2>/dev/null; then
      committed_numstat_ok=true
      numstat_range_form="two_dot"
      echo "WARN: triple-dot numstat failed; two-dot form used (changed_lines may include unrelated base changes)" >&2
    else
      echo "WARN: committed numstat failed; changed_lines may be underestimated (routing could be too conservative)" >&2
    fi
  fi
  git diff --cached --numstat >> "$numstat_file"
  git diff --numstat >> "$numstat_file"
  git ls-files --others --exclude-standard > "$untracked_file"

  scope_json="$(change_scope)" || {
    local status=$?
    rm -rf "$tmp_dir"
    return "$status"
  }

  printf '%s\n' "$scope_json" | jq -r '.change_scope.changed_files[]?' > "$changed_file"
  read -r insertions deletions diff_files < <(numstat_totals < "$numstat_file")
  untracked_lines="$(untracked_text_line_total < "$untracked_file")"
  changed_lines=$((insertions + deletions + untracked_lines))

  if jq -n \
    --argjson scope "$scope_json" \
    --rawfile changed "$changed_file" \
    --argjson insertions "$insertions" \
    --argjson deletions "$deletions" \
    --argjson diff_files "$diff_files" \
    --argjson untracked_text_lines "$untracked_lines" \
    --argjson changed_lines "$changed_lines" \
    --argjson committed_numstat_ok "$committed_numstat_ok" \
    --arg numstat_range_form "$numstat_range_form" '
    def lines($s): $s | split("\n") | map(select(length > 0));
    def trigger($id; $severity; $summary_ko; $evidence):
      {id:$id, severity:$severity, summary_ko:$summary_ko, evidence:$evidence};
    (lines($changed)) as $files |
    ([
      $files[]
      | select(
          test("(^|/)(auth|security|crypto|permission|policy|rbac|acl)(/|\\.|-|_|$)"; "i")
          or test("(^|/)(migration|migrations|schema|database|db|persistence)(/|\\.|-|_|$)"; "i")
          or test("(^|/)(deploy|build|ci|workflow|\\.github|docker|Dockerfile|Makefile)(/|\\.|-|_|$)"; "i")
          or test("(^|/)(config|env|secret|credential)(/|\\.|-|_|$)"; "i")
          or test("(^|/)(cli|command|commands|scripts)(/|\\.|-|_|$)"; "i")
        )
    ] | unique) as $critical_paths |
    (
      []
      + (if $changed_lines > 400 then
          [trigger("large_patch_over_400_lines"; "high"; "400라인을 초과한 큰 변경입니다."; {changed_lines:$changed_lines})]
        elif $changed_lines > 200 then
          [trigger("review_size_over_200_lines"; "medium"; "200라인을 초과해 리뷰 집중도가 떨어질 수 있습니다."; {changed_lines:$changed_lines})]
        else [] end)
      + (if ($scope.change_scope.changed_files_count // 0) > 5 then
          [trigger("many_files_over_5"; "high"; "변경 파일이 5개를 초과해 영향 범위 추적이 필요합니다."; {changed_files_count:$scope.change_scope.changed_files_count})]
        else [] end)
      + (if ($scope.change_scope.contract_surface // false) then
          [trigger("contract_surface"; "high"; "command/skill/schema/status 같은 계약 표면 변경입니다."; {kind:$scope.change_scope.kind})]
        else [] end)
      + (if ($scope.verification_profile.full_ci_required // false) then
          [trigger("runtime_or_code_change"; "medium"; "코드 또는 런타임 변경이라 전체/targeted 검증이 필요합니다."; {profile:$scope.verification_profile.profile})]
        else [] end)
      + (if ($critical_paths | length) > 0 then
          [trigger("critical_paths"; "high"; "보안/영속성/설정/배포/공개 CLI 경로가 변경됐습니다."; {paths:$critical_paths})]
        else [] end)
    ) as $risk_triggers |
    ($risk_triggers | map(select(.severity == "high")) | length) as $high_count |
    ($risk_triggers | map(select(.severity == "medium")) | length) as $medium_count |
    $scope + {
      kind:"dev_cycle_review_dossier",
      review_dossier:{
        summary:{
          insertions:$insertions,
          deletions:$deletions,
          untracked_text_lines:$untracked_text_lines,
          changed_lines:$changed_lines,
          diff_files_count:$diff_files,
          changed_files_count:$scope.change_scope.changed_files_count,
          committed_numstat_ok:$committed_numstat_ok,
          numstat_range_form:(if $numstat_range_form == "" then null else $numstat_range_form end)
        },
        risk_triggers:$risk_triggers,
        reviewer_route:{
          recommended:(
            if $high_count > 0 then "opus_or_high_effort"
            elif $medium_count > 0 then "standard_with_dossier"
            else "standard" end
          ),
          reason_ko:(
            if $high_count > 0 then "고위험 trigger가 있어 더 강한 리뷰 모델/추론을 권장합니다."
            elif $medium_count > 0 then "중간 위험 trigger가 있어 dossier 기반 집중 리뷰를 권장합니다."
            else "기계적 위험 trigger가 없어 표준 리뷰로 충분해 보입니다." end
          )
        },
        notes_ko:[
          "이 dossier는 diff 크기, 파일 확산, 계약/중요 경로 같은 기계적 신호만 계산합니다.",
          "200/400라인 기준과 파일 수 기준은 보편 법칙이 아니라 리뷰 집중도와 변경 확산을 보수적으로 다루기 위한 휴리스틱입니다.",
          "의미적 위험, 요구사항 적합성, 제품 판단은 reviewer가 별도로 확인해야 합니다."
        ]
      }
    }'; then
    rm -rf "$tmp_dir"
    return 0
  else
    local status=$?
    rm -rf "$tmp_dir"
    return "$status"
  fi
}
