#!/usr/bin/env bash
# Wait for codex review on the current PR.
#
# Polls three feedback sources (issue comments, review submissions including
# state-only APPROVED/CHANGES_REQUESTED, inline review comments) and a pass
# reaction on the PR. Exits as soon as the pass reaction (newer than baseline)
# is observed, when any new feedback is present, when timeout is reached twice,
# or when a review request comment is not acknowledged with eyes within three
# subsequent polling iterations.
#
# Baseline = best-effort push timestamp:
#   1. GitHub Events API PushEvent.created_at (most accurate; works for own
#      and accessible fork repos)
#   2. PR timeline (committed/force_pushed events)
#   3. HEAD commit committer.date
# Refreshed every cycle so a fresh push during polling advances baseline.
# Future-skewed timestamps are clamped to "now" once and held stable so the
# baseline does not drift forward each cycle.
#
# Exit codes:
#   0 → configured actor added the pass reaction (newer than baseline)
#   1 → at least one new comment/review since baseline (printed to stdout)
#   2 → timeout reached twice, or review request was not acknowledged
#   3 → PR could not be detected
#   4 → permanent API failure (auth/permissions/missing PR)
#
# Usage:
#   wait-codex-review.sh [PR_NUMBER | PR_URL]
#
# Env:
#   CODEX_POLL_INTERVAL  seconds between polls (default 20)
#   CODEX_POLL_TIMEOUT   total wait limit in seconds (default 600)
#   CODEX_INITIAL_EMPTY_DELAY
#                         one-time sleep when the first successful activity
#                         poll has no comments/reviews/reactions (default 300)
#   CODEX_BASELINE       ISO timestamp; activity at/before this is ignored.
#   CODEX_REPO           owner/repo override (helpful in fork workflows).
#   CODEX_PASS_ACTOR     exact GitHub login that signals pass via reaction
#                        (default: chatgpt-codex-connector[bot])
#   CODEX_PASS_REACTION  GitHub reaction content (default: +1, i.e. 👍)
#   CODEX_REVIEW_REQUEST_BODY
#                        issue comment body posted when no eyes signal exists
#                        (default: @codex review)

set -euo pipefail

# --- Resolve PR number and repo (URL form supported) ---
arg="${1:-}"
if [[ "$arg" =~ ^https?://[^/]+/([^/]+/[^/]+)/pull/([0-9]+)/?$ ]]; then
  repo="${BASH_REMATCH[1]}"
  pr="${BASH_REMATCH[2]}"
else
  pr="${arg:-$(gh pr view --json number -q .number 2>/dev/null || true)}"
  repo="${CODEX_REPO:-}"
fi

if [ -z "$pr" ]; then
  echo "ERROR: pass PR number or URL, or run inside a branch with an open PR" >&2
  exit 3
fi

if [ -z "$repo" ]; then
  repo=$(gh pr view "$pr" --json baseRepository \
    -q '.baseRepository | "\(.owner.login)/\(.name)"' 2>/dev/null || true)
fi
if [ -z "$repo" ]; then
  remote=$(git remote get-url origin 2>/dev/null || true)
  if [[ "$remote" =~ github\.com[:/]([^/]+/[^/]+)$ ]]; then
    repo="${BASH_REMATCH[1]%.git}"
  fi
fi
if [ -z "$repo" ]; then
  echo "ERROR: could not determine repo for PR #$pr (set CODEX_REPO=owner/repo or pass full PR URL)" >&2
  exit 3
fi

interval="${CODEX_POLL_INTERVAL:-20}"
timeout="${CODEX_POLL_TIMEOUT:-600}"
initial_empty_delay="${CODEX_INITIAL_EMPTY_DELAY:-300}"
pass_actor="${CODEX_PASS_ACTOR:-chatgpt-codex-connector[bot]}"
pass_reaction="${CODEX_PASS_REACTION:-+1}"
review_request_body="${CODEX_REVIEW_REQUEST_BODY:-@codex review}"
request_author="$(gh api user -q .login 2>/dev/null || true)"
fetch_failures="$(mktemp)"
trap 'rm -f "$fetch_failures"' EXIT

# --- API helpers ---
# fetch_list_or_empty <label> <gh-api-args...>: return JSON array on stdout,
# even on transient failures (so partial success can still drive the loop).
# Permanent failures (HTTP 401/403/404) exit 4.
classify_api_error() {
  # Echo "permanent" or "transient" for the given gh-api error output.
  local out="$1"
  if printf '%s' "$out" | grep -qiE 'rate limit|secondary rate|abuse detection'; then
    echo transient; return
  fi
  if printf '%s' "$out" | grep -qE 'HTTP 40[134]|status code: 40[134]|Bad credentials|Not Found'; then
    echo permanent; return
  fi
  echo transient
}

fetch_list_or_empty() {
  local label="$1"; shift
  local out
  if ! out=$(gh api --paginate "$@" 2>&1); then
    if [ "$(classify_api_error "$out")" = "permanent" ]; then
      echo "ERROR: $label permanent API failure: $out" >&2
      exit 4
    fi
    printf '%s\n' "$label" >> "$fetch_failures"
    echo "WARN: $label fetch failed (using empty set): $out" >&2
    echo "[]"
    return 0
  fi
  printf '%s' "$out" | jq -s 'add // []'
}

FETCH_BASELINE_PERMANENT_RC=99
fetch_baseline() {
  local raw push_ts head_sha head_repo branch pr_info pr_err
  # PR lookup: distinguish permanent from transient failures.
  # NOTE: cannot use `exit 4` here — caller invokes us via command substitution
  # (which runs in a subshell), so exit only terminates the subshell. Return a
  # sentinel rc instead and let the caller propagate it.
  if ! pr_info=$(gh api "repos/$repo/pulls/$pr" 2>&1); then
    if [ "$(classify_api_error "$pr_info")" = "permanent" ]; then
      echo "ERROR: PR lookup permanent failure for $repo#$pr: $pr_info" >&2
      return $FETCH_BASELINE_PERMANENT_RC
    fi
    return 1
  fi
  branch=$(printf '%s' "$pr_info" | jq -r .head.ref 2>/dev/null || true)
  head_repo=$(printf '%s' "$pr_info" | jq -r '.head.repo.full_name // empty' 2>/dev/null || true)
  head_sha=$(printf '%s' "$pr_info" | jq -r '.head.sha // empty' 2>/dev/null || true)

  # 1) Events API PushEvent
  if [ -n "$branch" ] && [ -n "$head_repo" ]; then
    if raw=$(gh api --paginate "repos/$head_repo/events" 2>/dev/null); then
      push_ts=$(printf '%s' "$raw" | jq -r -s --arg ref "refs/heads/$branch" '
        add // []
        | [.[] | select(.type == "PushEvent") | select(.payload.ref == $ref) | .created_at]
        | max // empty')
      if [ -n "$push_ts" ]; then echo "$push_ts"; return 0; fi
    fi
  fi
  # 2) Timeline events
  if raw=$(gh api --paginate "repos/$repo/issues/$pr/timeline" 2>/dev/null); then
    push_ts=$(printf '%s' "$raw" | jq -r -s '
      add // []
      | [.[] |
          if .event == "committed" then (.committer.date // .author.date // empty)
          elif .event == "head_ref_force_pushed" then .created_at
          else empty end ]
      | max // empty')
    if [ -n "$push_ts" ]; then echo "$push_ts"; return 0; fi
  fi
  # 3) HEAD commit committer.date — query head repo when known (fork PRs),
  #    falling back to base only when head repo is unavailable.
  [ -z "$head_sha" ] && return 1
  local commit_repo="${head_repo:-$repo}"
  gh api "repos/$commit_repo/commits/$head_sha" -q .commit.committer.date 2>/dev/null
}

clamp_to_now() {
  local ts="$1"
  local now_iso
  now_iso=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  if [[ "$ts" > "$now_iso" ]]; then echo "$now_iso"; else echo "$ts"; fi
}

echo "→ polling PR $repo#$pr (interval=${interval}s, timeout=${timeout}s, pass_actor=$pass_actor)" >&2

# Baseline state — once we clamp a future-skewed value, hold it stable across
# polls until a fresh push changes the underlying fetched timestamp.
last_fetched_baseline=""
last_clamped_baseline=""
first_activity_probe=1
timeout_count=0
review_request_posted=0
review_request_acknowledged=0
review_request_polls=0

started=$(date +%s)
while :; do
  : > "$fetch_failures"

  now=$(date +%s)
  if [ $((now - started)) -ge "$timeout" ]; then
    timeout_count=$((timeout_count + 1))
    if [ "$timeout_count" -ge 2 ]; then
      echo "TIMEOUT after ${timeout_count} polling windows" >&2
      exit 2
    fi
    echo "TIMEOUT; continuing until second timeout" >&2
    first_activity_probe=1
    started=$(date +%s)
    continue
  fi

  if [ -n "${CODEX_BASELINE:-}" ]; then
    baseline="$CODEX_BASELINE"
  else
    set +e
    fetched=$(fetch_baseline)
    rc=$?
    set -e
    if [ "$rc" = "$FETCH_BASELINE_PERMANENT_RC" ]; then
      exit 4
    fi
    if [ "$rc" != "0" ] || [ -z "$fetched" ]; then
      echo "WARN: could not fetch baseline; retrying next poll" >&2
      sleep "$interval"; continue
    fi
    if [ "$fetched" != "$last_fetched_baseline" ]; then
      last_fetched_baseline="$fetched"
      new_clamped=$(clamp_to_now "$fetched")
      # Monotonic: never let baseline regress (e.g., if baseline source
      # changes from Events API to commit timestamp, keep the older later).
      if [ -n "$last_clamped_baseline" ] && [[ "$new_clamped" < "$last_clamped_baseline" ]]; then
        new_clamped="$last_clamped_baseline"
      fi
      last_clamped_baseline="$new_clamped"
    fi
    baseline="$last_clamped_baseline"
  fi

  # 1) Pass reaction wins (filtered by baseline so old 👍 doesn't falsely pass)
  reactions=$(fetch_list_or_empty "reactions" "repos/$repo/issues/$pr/reactions")
  pass=$(printf '%s' "$reactions" | jq --arg actor "$pass_actor" --arg react "$pass_reaction" --arg base "$baseline" '
    [.[] | select(.user.login == $actor) | select(.content == $react) | select(.created_at > $base)] | length')
  if [ "$pass" != "0" ]; then
    echo "PASSED (reaction $pass_reaction from $pass_actor; baseline=$baseline)" >&2
    exit 0
  fi

  # 2) Gather feedback. Each source is independent — partial successes still
  # surface their fresh items so a single endpoint failure doesn't stall.
  ic=$(fetch_list_or_empty "issue_comments"  "repos/$repo/issues/$pr/comments")
  rv=$(fetch_list_or_empty "reviews"         "repos/$repo/pulls/$pr/reviews")
  rc=$(fetch_list_or_empty "review_comments" "repos/$repo/pulls/$pr/comments")

  new_items=$(jq -n --arg base "$baseline" --arg request_body "$review_request_body" \
    --argjson ic "$ic" --argjson rv "$rv" --argjson rc "$rc" '
    ($ic | [.[] | select(.created_at > $base) | select(.body != $request_body) | {kind:"issue_comment", at:.created_at, login:.user.login, body:.body, state:""}])
    + ($rv | [.[] | select((.submitted_at // "") > $base) | {kind:"review", at:.submitted_at, login:.user.login, body:(.body // ""), state:(.state // "")}])
    + ($rc | [.[] | select(.created_at > $base) | {kind:"review_comment", at:.created_at, login:.user.login, path:.path, line:(.line // .original_line), body:.body, state:""}])
    | sort_by(.at)')

  count=$(printf '%s' "$new_items" | jq 'length')
  if [ "$count" != "0" ]; then
    echo "(baseline=$baseline)" >&2
    printf '%s' "$new_items" | jq -r '.[] |
      if .kind == "review_comment" then
        "=== [\(.kind)] \(.login) @ \(.at) — \(.path):\(.line) ===\n\(.body)\n"
      elif .kind == "review" then
        (if (.body | length) == 0 then
          "=== [review:\(.state)] \(.login) @ \(.at) ===\n(no body)\n"
        else
          "=== [review:\(.state)] \(.login) @ \(.at) ===\n\(.body)\n"
        end)
      else
        "=== [\(.kind)] \(.login) @ \(.at) ===\n\(.body)\n"
      end'
    exit 1
  fi

  if ! grep -qxE 'reactions|issue_comments' "$fetch_failures"; then
    eyes_signal=$(jq -n \
      --arg author "$request_author" \
      --arg request_body "$review_request_body" \
      --argjson reactions "$reactions" \
      --argjson ic "$ic" '
      (
        ($reactions | [.[] | select(.content == "eyes")] | length) > 0
      ) or (
        ($ic | [.[] |
          select(
            (($author | length) > 0 and ((.user.login // "") == $author))
            or (($author | length) == 0 and ((.body // "") == $request_body))
          )
          | select((.reactions.eyes // 0) > 0)
        ] | length) > 0
      )
      | if . then 1 else 0 end')

    if [ "$eyes_signal" = "1" ]; then
      review_request_acknowledged=1
      review_request_polls=0
    elif [ "$review_request_posted" = "0" ]; then
      echo "→ no eyes reaction on PR body or my comments; posting review request" >&2
      if ! gh api "repos/$repo/issues/$pr/comments" \
        -f body="$review_request_body" >/dev/null; then
        echo "ERROR: failed to post review request comment" >&2
        exit 4
      fi
      review_request_posted=1
      review_request_polls=0
      first_activity_probe=0
    elif [ "$review_request_acknowledged" = "0" ]; then
      review_request_polls=$((review_request_polls + 1))
      if [ "$review_request_polls" -ge 3 ]; then
        echo "TIMEOUT: review request was not acknowledged with eyes within ${review_request_polls} polling iterations" >&2
        exit 2
      fi
    fi
  fi

  if [ "$first_activity_probe" = "1" ] && [ ! -s "$fetch_failures" ]; then
    first_activity_probe=0
    total_activity=$(jq -n \
      --argjson reactions "$reactions" \
      --argjson ic "$ic" --argjson rv "$rv" --argjson rc "$rc" \
      '($reactions | length) + ($ic | length) + ($rv | length) + ($rc | length)')
    if [ "$total_activity" = "0" ]; then
      now=$(date +%s)
      remaining=$((timeout - (now - started)))
      if [ "$remaining" -gt 0 ]; then
        delay="$initial_empty_delay"
        if [ "$delay" -gt "$remaining" ]; then delay="$remaining"; fi
        echo "→ first PR activity poll was empty; sleeping ${delay}s before normal polling" >&2
        sleep "$delay"
        continue
      fi
    fi
  fi

  sleep "$interval"
done
