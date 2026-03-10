# Remove claude.ai URLs from commit messages in the given branch.
# Usage: .\scripts\remove-commit-urls.ps1 [branch]
#   branch: target branch (default: current branch)
#
# After running, force push: git push samsung <branch> --force

$Branch = if ($args[0]) { $args[0] } else { git branch --show-current }
$RepoRoot = git rev-parse --show-toplevel
Set-Location $RepoRoot

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ShScript = Join-Path $ScriptDir "remove-commit-urls.sh"

bash $ShScript $Branch
