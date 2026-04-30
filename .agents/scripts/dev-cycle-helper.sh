#!/usr/bin/env bash
# Shared entrypoint for dev-cycle commands/skills.

set -euo pipefail

source_path="${BASH_SOURCE[0]}"
while [[ -L "$source_path" ]]; do
  source_dir="$(cd -P "$(dirname "$source_path")" && pwd)"
  source_path="$(readlink "$source_path")"
  [[ "$source_path" != /* ]] && source_path="$source_dir/$source_path"
done

DEV_CYCLE_HELPER_SCRIPT_DIR="$(cd -P "$(dirname "$source_path")" && pwd)"
DEV_CYCLE_HELPER_LIB_DIR="$DEV_CYCLE_HELPER_SCRIPT_DIR/dev-cycle-helper"

source "$DEV_CYCLE_HELPER_LIB_DIR/core.sh"
source "$DEV_CYCLE_HELPER_LIB_DIR/brief-state.sh"
source "$DEV_CYCLE_HELPER_LIB_DIR/brief-render.sh"
source "$DEV_CYCLE_HELPER_LIB_DIR/dispatch.sh"

dev_cycle_helper_main "$@"
