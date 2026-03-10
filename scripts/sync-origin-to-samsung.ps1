# Sync origin branch to samsung main: rewrite author/committer, remove URLs.
# Usage: .\scripts\sync-origin-to-samsung.ps1 [origin_branch] [--full]
#   origin_branch: branch on origin (default: claude/config-server-setup-4kjXZ)
#   --full: force full rewrite (skip incremental)
#
# Incremental by default: only new commits (fast). Use --full for first run or reset.

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ShScript = Join-Path $ScriptDir "sync-origin-to-samsung.sh"
bash $ShScript $args
