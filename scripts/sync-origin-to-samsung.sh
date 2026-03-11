#!/bin/bash
# Sync origin branch to samsung main: rewrite author/committer, remove URLs.
# Uses incremental mode when possible (only new commits) for speed.
#
# Usage: ./scripts/sync-origin-to-samsung.sh [origin_branch] [--full]
#   origin_branch: branch on origin (default: claude/config-server-setup-4kjXZ)
#   --full: force full rewrite (skips incremental, use when starting fresh)
#
# Env (optional): AUTHOR_NAME, AUTHOR_EMAIL (default: ygdg.kim, ygdg.kim@samsung.com)
#
# Incremental: stores last synced origin commit in refs/sync-state/, only rewrites new commits.
# Full: reset + filter-branch on entire history (slow for many commits).
#
# On cherry-pick conflict: always prefer origin (--theirs); samsung/main changes are discarded.
# Empty cherry-picks (no diff after resolution) are skipped; script runs to completion non-interactively.

set -e

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

ORIGIN_BRANCH="${1:-claude/config-server-setup-4kjXZ}"
[[ "$ORIGIN_BRANCH" == "--full" ]] && { FORCE_FULL=1; ORIGIN_BRANCH="claude/config-server-setup-4kjXZ"; }
[[ "$2" == "--full" ]] && FORCE_FULL=1

AUTHOR_NAME="${AUTHOR_NAME:-ygdg.kim}"
AUTHOR_EMAIL="${AUTHOR_EMAIL:-ygdg.kim@samsung.com}"
SYNC_REF="refs/sync-state/origin-${ORIGIN_BRANCH//\//-}"

git fetch origin
git fetch samsung 2>/dev/null || true

ORIGIN_HEAD="origin/$ORIGIN_BRANCH"
if ! git rev-parse "$ORIGIN_HEAD" &>/dev/null; then
  echo "Error: origin/$ORIGIN_BRANCH not found"
  exit 1
fi

# Incremental: only if ref exists, main matches samsung, and not --full
if [[ -z "$FORCE_FULL" ]] && git rev-parse "$SYNC_REF" &>/dev/null; then
  LAST_SYNCED="$(git rev-parse "$SYNC_REF")"
  NEW_COUNT=$(git rev-list --count "$LAST_SYNCED".."$ORIGIN_HEAD" 2>/dev/null || echo 0)

  if [[ "$NEW_COUNT" -eq 0 ]]; then
    echo "Already up to date. origin/$ORIGIN_BRANCH = samsung main."
    exit 0
  fi

  echo "Incremental sync: $NEW_COUNT new commit(s)"
  echo "  author/committer = $AUTHOR_NAME <$AUTHOR_EMAIL>"
  echo "  Removing URLs from messages"
  echo ""

  git checkout main
  git reset --hard samsung/main

  TEMP_MSG=$(mktemp)
  trap 'rm -f "$TEMP_MSG"' EXIT
  for commit in $(git rev-list --reverse "$LAST_SYNCED".."$ORIGIN_HEAD"); do
    APPLIED=1
    if ! git cherry-pick "$commit"; then
      echo "Conflict: auto-resolving with origin (theirs)"
      git checkout --theirs -- .
      git add -A
      if GIT_EDITOR=: git cherry-pick --continue 2>/dev/null; then
        APPLIED=1
      elif git cherry-pick --skip 2>/dev/null; then
        echo "  (skipped empty commit ${commit:0:7})"
        APPLIED=0
      else
        echo "Could not continue or skip cherry-pick. Resolve manually: git cherry-pick --continue or --skip"
        echo "Then re-run this script."
        exit 1
      fi
    fi
    if [[ "$APPLIED" -eq 1 ]]; then
      git log -1 --format=%B "$commit" | sed '/https:\/\/claude\.ai\//d' > "$TEMP_MSG"
      AUTHOR_DATE=$(git log -1 --format=%aI "$commit")
      COMMITTER_DATE=$(git log -1 --format=%cI "$commit")
      GIT_COMMITTER_NAME="$AUTHOR_NAME" GIT_COMMITTER_EMAIL="$AUTHOR_EMAIL" \
        GIT_COMMITTER_DATE="$COMMITTER_DATE" \
        git commit --amend --author="$AUTHOR_NAME <$AUTHOR_EMAIL>" --date="$AUTHOR_DATE" -F "$TEMP_MSG"
    fi
  done
  rm -f "$TEMP_MSG"

  git push samsung main
  git update-ref "$SYNC_REF" "$ORIGIN_HEAD"

  echo ""
  echo "Done. $NEW_COUNT commit(s) synced."
  exit 0
fi

# Full sync (overwrites samsung main with origin only - samsung-only commits will be lost)
export FILTER_BRANCH_SQUELCH_WARNING=1
echo "Full sync: origin/$ORIGIN_BRANCH -> samsung main (overwrites samsung, any samsung-only commits will be lost)"
echo "  - Reset main to origin/$ORIGIN_BRANCH"
echo "  - Rewrite all: author/committer = $AUTHOR_NAME <$AUTHOR_EMAIL>"
echo "  - Remove URLs from messages"
echo ""

git checkout main
git reset --hard "$ORIGIN_HEAD"

git filter-branch -f \
  --env-filter "export GIT_AUTHOR_NAME='$AUTHOR_NAME'; export GIT_AUTHOR_EMAIL='$AUTHOR_EMAIL'; export GIT_COMMITTER_NAME='$AUTHOR_NAME'; export GIT_COMMITTER_EMAIL='$AUTHOR_EMAIL'; export GIT_AUTHOR_DATE=\$(git log -1 --format=%aI \$GIT_COMMIT); export GIT_COMMITTER_DATE=\$(git log -1 --format=%cI \$GIT_COMMIT);" \
  --msg-filter 'sed "/https:\/\/claude\.ai\//d"' \
  main

git push samsung main --force
git update-ref "$SYNC_REF" "$ORIGIN_HEAD"

echo ""
echo "Done. main on samsung synced from origin/$ORIGIN_BRANCH."
