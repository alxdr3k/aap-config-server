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
  for commit in $(git rev-list --reverse "$LAST_SYNCED".."$ORIGIN_HEAD"); do
    git cherry-pick "$commit" || {
      echo "Conflict on cherry-pick. Resolve and: git cherry-pick --continue"
      echo "Then re-run this script."
      rm -f "$TEMP_MSG"
      exit 1
    }
    git log -1 --format=%B "$commit" | sed '/https:\/\/claude\.ai\//d' > "$TEMP_MSG"
    GIT_COMMITTER_NAME="$AUTHOR_NAME" GIT_COMMITTER_EMAIL="$AUTHOR_EMAIL" \
      git commit --amend --author="$AUTHOR_NAME <$AUTHOR_EMAIL>" -F "$TEMP_MSG"
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
  --env-filter "export GIT_AUTHOR_NAME='$AUTHOR_NAME'; export GIT_AUTHOR_EMAIL='$AUTHOR_EMAIL'; export GIT_COMMITTER_NAME='$AUTHOR_NAME'; export GIT_COMMITTER_EMAIL='$AUTHOR_EMAIL';" \
  --msg-filter 'sed "/https:\/\/claude\.ai\//d"' \
  main

git push samsung main --force
git update-ref "$SYNC_REF" "$ORIGIN_HEAD"

echo ""
echo "Done. main on samsung synced from origin/$ORIGIN_BRANCH."
