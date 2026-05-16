# shellcheck shell=bash

# check_test_plan: reads PR body markdown from stdin and validates that it
# contains a non-empty "Test plan" section. Emits a JSON ack to stdout.
# Returns 0 when valid, non-zero (with ok:false ack) otherwise.
#
# Applies the CommonMark constraints that matter for a gate against
# accidental omission: ATX headings only (no setext), 0-3 leading spaces,
# 1-6 `#`s, required space (or end-of-line) after the hashes, optional
# closing `#` markers. Tracks fenced code blocks (``` / ~~~) and HTML
# comment blocks (`<!-- ... -->`) so that heading-looking lines inside
# them do not satisfy the gate or terminate the section. Setext
# (`Text\n---`) is intentionally not supported — a line-based check
# cannot reproduce its CommonMark semantics.
check_test_plan() {
  require_jq || return 1

  local body
  body="$(cat | tr -d '\r')"

  if [[ -z "${body//[[:space:]]/}" ]]; then
    jq -nc '{ok:false, kind:"test_plan_check", reason_ko:"PR body가 비어 있음"}'
    return 1
  fi

  local awk_lib='
function leading_spaces(line,    n) {
  n = 0
  while (n < length(line) && substr(line, n+1, 1) == " ") n++
  return n
}
function atx_hashes(line,    s, n, ch) {
  s = leading_spaces(line)
  if (s > 3) return 0
  n = 0
  while (s + n < length(line) && substr(line, s+n+1, 1) == "#") n++
  if (n < 1 || n > 6) return 0
  if (s + n == length(line)) return n
  ch = substr(line, s + n + 1, 1)
  if (ch == " " || ch == "\t") return n
  return 0
}
function is_test_plan_heading(line,    n, lower, s, rest) {
  n = atx_hashes(line)
  if (n != 2 && n != 3) return 0
  lower = tolower(line)
  s = leading_spaces(lower)
  rest = substr(lower, s + n + 1)
  if (rest ~ /^[ \t]+(test plan|테스트[ \t]+계획)([ \t]+#+)?[ \t]*$/) return n
  return 0
}
function fence_open_marker(line,    m) {
  if (match(line, /^[ ]?[ ]?[ ]?(```+|~~~+)/)) {
    m = substr(line, RSTART, RLENGTH)
    sub(/^[ ]+/, "", m)
    return m
  }
  return ""
}
function fence_close_match(line, open_marker,    closer, oc, cc) {
  if (match(line, /^[ ]?[ ]?[ ]?(```+|~~~+)[ \t]*$/)) {
    closer = substr(line, RSTART, RLENGTH)
    sub(/^[ ]+/, "", closer); sub(/[ \t]+$/, "", closer)
    oc = substr(open_marker, 1, 1)
    cc = substr(closer, 1, 1)
    if (oc == cc && length(closer) >= length(open_marker)) return 1
  }
  return 0
}
function html_comment_open(line) {
  return (line ~ /^[ ]?[ ]?[ ]?<!--/) && (line !~ /-->/)
}
function html_comment_singleline(line) {
  return line ~ /^[ ]?[ ]?[ ]?<!--.*-->[ \t]*$/
}
'

  local header_output
  header_output="$(printf '%s\n' "$body" | awk "$awk_lib"'
    BEGIN { in_fence = 0; fence_marker = ""; in_html = 0 }
    {
      line = $0
      if (in_fence) {
        if (fence_close_match(line, fence_marker)) { in_fence = 0; fence_marker = "" }
        next
      }
      if (in_html) {
        if (line ~ /-->/) in_html = 0
        next
      }
      m = fence_open_marker(line)
      if (m != "") { fence_marker = m; in_fence = 1; next }
      if (html_comment_singleline(line)) next
      if (html_comment_open(line)) { in_html = 1; next }
      n = is_test_plan_heading(line)
      if (n > 0) { print NR " " n; exit }
    }
  ')"

  if [[ -z "$header_output" ]]; then
    jq -nc '{ok:false, kind:"test_plan_check", reason_ko:"PR body에 `## Test plan` (또는 `## 테스트 계획`) 섹션 헤더가 없음"}'
    return 1
  fi

  local header_line tp_level
  read -r header_line tp_level <<< "$header_output"

  local section
  section="$(printf '%s\n' "$body" | awk -v start="$header_line" -v tp_level="$tp_level" "$awk_lib"'
    BEGIN { in_fence = 0; fence_marker = ""; in_html = 0 }
    NR <= start { next }
    {
      line = $0
      if (in_fence) {
        if (fence_close_match(line, fence_marker)) { in_fence = 0; fence_marker = "" }
        print line
        next
      }
      if (in_html) {
        if (line ~ /-->/) {
          in_html = 0
          pos = index(line, "-->")
          after = substr(line, pos + 3)
          sub(/^[ \t]+/, "", after)
          sub(/[ \t]+$/, "", after)
          if (length(after) > 0) print after
        }
        next
      }
      m = fence_open_marker(line)
      if (m != "") { fence_marker = m; in_fence = 1; print line; next }
      if (html_comment_singleline(line)) next
      if (html_comment_open(line)) { in_html = 1; next }
      n = atx_hashes(line)
      if (n > 0 && n <= tp_level) exit
      print line
    }
  ')"

  local content_lines
  content_lines="$(printf '%s\n' "$section" | grep -cv '^[[:space:]]*$' || true)"
  content_lines="${content_lines:-0}"

  if (( content_lines == 0 )); then
    jq -nc '{ok:false, kind:"test_plan_check", reason_ko:"`## Test plan` 섹션이 비어 있음"}'
    return 1
  fi

  jq -nc --argjson lines "$content_lines" \
    '{ok:true, kind:"test_plan_check", summary_ko:("Test plan 섹션 확인됨 (\($lines)줄)"), content_lines:$lines}'
}
