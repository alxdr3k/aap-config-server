#!/bin/bash
# Rewrite commits: remove URLs from messages, set author/committer.
# Usage: ./scripts/remove-commit-urls.sh [branch]
#   branch: target branch (default: current branch)
#
# Env (optional): AUTHOR_NAME, AUTHOR_EMAIL (default: ygdg.kim, ygdg.kim@samsung.com)
# After running, force push: git push samsung <branch> --force

set -e

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

BRANCH="${1:-$(git branch --show-current)}"
AUTHOR_NAME="${AUTHOR_NAME:-ygdg.kim}"
AUTHOR_EMAIL="${AUTHOR_EMAIL:-ygdg.kim@samsung.com}"
export FILTER_BRANCH_SQUELCH_WARNING=1

echo "Rewriting commits on branch: $BRANCH"
echo "  - Remove URLs from commit messages"
echo "  - Set author/committer: $AUTHOR_NAME <$AUTHOR_EMAIL>"
echo ""

git filter-branch -f \
  --env-filter "export GIT_AUTHOR_NAME='$AUTHOR_NAME'; export GIT_AUTHOR_EMAIL='$AUTHOR_EMAIL'; export GIT_COMMITTER_NAME='$AUTHOR_NAME'; export GIT_COMMITTER_EMAIL='$AUTHOR_EMAIL';" \
  --msg-filter 'sed "/https:\/\/claude\.ai\//d"' \
  "$BRANCH"

echo ""
echo "Done. Force push to samsung:"
echo "  git push samsung $BRANCH --force"
