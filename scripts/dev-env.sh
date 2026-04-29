# Source this file from the repo root:
#
#   . scripts/dev-env.sh
#
# It keeps the Go toolchain, module cache, build cache, and GOPATH scoped to
# this checkout. GOTOOLCHAIN=local prevents Go from auto-installing a different
# toolchain outside the repo.

_repo_root="${AAP_CONFIG_SERVER_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"

export GOROOT="$_repo_root/.tools/go"
export GOPATH="$_repo_root/.cache/go-path"
export GOCACHE="$_repo_root/.cache/go-build"
export GOMODCACHE="$_repo_root/.cache/go-mod"
export GOENV=off
export GOTOOLCHAIN=local
export PATH="$GOROOT/bin:$GOPATH/bin:$PATH"

unset _repo_root
