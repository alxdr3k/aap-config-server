# shellcheck shell=bash

usage() {
  cat <<'EOF'
usage: dev-cycle-helper.sh <command>

commands:
  direct-push-list
  repo-name
  repo-type
  default-branch
  review-base
  sync
  init-brief
  validate-brief <run-id> <brief-log>
  finish-cycle
  finish-cycle-json
  summary
  summary-json
EOF
}

dev_cycle_helper_main() {
  local cmd
  cmd="${1:-}"
  case "$cmd" in
    direct-push-list) direct_push_repos ;;
    repo-name) repo_name ;;
    repo-type) repo_type ;;
    default-branch) default_branch ;;
    review-base) review_base ;;
    sync) sync_repo ;;
    init-brief) init_brief ;;
    validate-brief) shift; validate_brief "$@" ;;
    finish-cycle) finish_cycle ;;
    finish-cycle-json) finish_cycle_json ;;
    summary) summary ;;
    summary-json) summary_json ;;
    help|-h|--help|"") usage ;;
    *) echo "unknown command: $cmd" >&2; usage >&2; return 2 ;;
  esac
}
