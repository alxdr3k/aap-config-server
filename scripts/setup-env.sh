#!/usr/bin/env bash
# setup-env.sh — One-shot dev environment setup for AAP Config Server.
#
# Modes (pick one):
#   --install              Install Go toolchain + module cache + (optional) tools.
#                          Default mode if none given. Requires internet.
#   --bundle PATH          Build an offline bundle (.tar.gz) at PATH for transfer
#                          to air-gapped stg/prd hosts. Implies --install first.
#   --from-bundle PATH     Install from an offline bundle. No internet needed.
#
# Common options:
#   --go-version VER       Override Go version (default: read from go.mod).
#   --os OS                linux|darwin (default: auto-detect).
#   --arch ARCH            amd64|arm64 (default: auto-detect).
#   --with-lint            Also install golangci-lint (pinned, see LINT_VERSION).
#   --with-vuln            Also install govulncheck (latest).
#   --skip-build           Don't pre-build bin/ binaries.
#   --skip-modcache        Don't pre-populate module cache (smaller bundle).
#   --force                Overwrite existing .tools/go install.
#   -h, --help             Show this help.
#
# Layout (relative to repo root, all .gitignored):
#   .tools/go/             Go SDK
#   .tools/bin/            golangci-lint, govulncheck (if requested)
#   .cache/go-mod/         pre-fetched module cache
#   .cache/go-build/       build cache
#   bin/                   pre-built config-server, config-agent
#
# Bundle layout (tar.gz):
#   MANIFEST               key=value pairs (versions, os, arch, created_at)
#   tools/go/              Go SDK (per OS/arch — bundle is NOT cross-platform)
#   tools/bin/             optional aux binaries
#   cache/go-mod/          populated module cache
#   bin/                   optional pre-built binaries
set -euo pipefail

LINT_VERSION="v2.11.4"
DEFAULT_GO_VERSION=""   # filled from go.mod if not overridden

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# ---------------------------------------------------------------- helpers ----
log()  { printf '[setup] %s\n' "$*" >&2; }
warn() { printf '[setup] WARN: %s\n' "$*" >&2; }
die()  { printf '[setup] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '2,/^set -euo pipefail/p' "$0" | sed 's/^# \{0,1\}//' | sed '$d'
  exit "${1:-0}"
}

detect_os() {
  case "$(uname -s)" in
    Linux)   echo linux ;;
    Darwin)  echo darwin ;;
    *) die "unsupported OS: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported arch: $(uname -m)" ;;
  esac
}

read_go_version_from_gomod() {
  # Prefer the toolchain directive when present — it pins a specific
  # toolchain version that may be newer than the language baseline `go` line.
  local v
  v="$(awk '/^toolchain[ \t]+go/ { sub(/^go/, "", $2); print $2; exit }' go.mod)"
  if [ -n "$v" ]; then
    printf '%s\n' "$v"
    return 0
  fi
  v="$(awk '/^go[ \t]+[0-9]/ { print $2; exit }' go.mod)"
  [ -n "$v" ] || return 1
  printf '%s\n' "$v"
}

# Portable SHA-256 helpers — Linux ships `sha256sum` (coreutils), stock macOS
# only ships `shasum -a 256`. Probe once and adapt.
sha256_compute() {
  # sha256_compute FILE → stdout: "<hex>  <basename>"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1"
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1"
  else
    die "neither sha256sum nor shasum is installed; cannot compute SHA-256"
  fi
}

sha256_check_stdin() {
  # Reads "<hex>  <name>" lines from stdin and verifies.
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c -
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -c -
  else
    die "neither sha256sum nor shasum is installed; cannot verify SHA-256"
  fi
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

# --------------------------------------------------------------- arg parse ---
MODE="install"
BUNDLE_PATH=""
GO_VERSION=""
TARGET_OS=""
TARGET_ARCH=""
WITH_LINT=false
WITH_VULN=false
SKIP_BUILD=false
SKIP_MODCACHE=false
FORCE=false

need_value() {
  [ -n "${2:-}" ] || die "$1 requires a value"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --install)        MODE="install"; shift ;;
    --bundle)         need_value "$1" "${2:-}"; MODE="bundle";      BUNDLE_PATH="$2"; shift 2 ;;
    --from-bundle)    need_value "$1" "${2:-}"; MODE="from-bundle"; BUNDLE_PATH="$2"; shift 2 ;;
    --go-version)     need_value "$1" "${2:-}"; GO_VERSION="$2";    shift 2 ;;
    --os)             need_value "$1" "${2:-}"; TARGET_OS="$2";     shift 2 ;;
    --arch)           need_value "$1" "${2:-}"; TARGET_ARCH="$2";   shift 2 ;;
    --with-lint)      WITH_LINT=true; shift ;;
    --with-vuln)      WITH_VULN=true; shift ;;
    --skip-build)     SKIP_BUILD=true; shift ;;
    --skip-modcache)  SKIP_MODCACHE=true; shift ;;
    --force)          FORCE=true; shift ;;
    -h|--help)        usage 0 ;;
    *) die "unknown argument: $1 (use --help)" ;;
  esac
done

case "$MODE" in
  bundle|from-bundle)
    [ -n "$BUNDLE_PATH" ] || die "--$MODE requires a PATH argument"
    ;;
esac

if [ "$MODE" = "bundle" ]; then
  case "$BUNDLE_PATH" in
    *.tar.gz|*.tgz) ;;
    *) die "--bundle PATH must end in .tar.gz or .tgz (got: $BUNDLE_PATH)" ;;
  esac
fi

TARGET_OS="${TARGET_OS:-$(detect_os)}"
TARGET_ARCH="${TARGET_ARCH:-$(detect_arch)}"

if [ -z "$GO_VERSION" ]; then
  GO_VERSION="$(read_go_version_from_gomod)" || die "could not read Go version from go.mod"
fi
DEFAULT_GO_VERSION="$GO_VERSION"

# --------------------------------------------------------------- env paths ---
TOOLS_DIR="$REPO_ROOT/.tools"
GO_DIR="$TOOLS_DIR/go"
TOOLS_BIN="$TOOLS_DIR/bin"
CACHE_DIR="$REPO_ROOT/.cache"
GOMODCACHE_DIR="$CACHE_DIR/go-mod"
GOBUILDCACHE_DIR="$CACHE_DIR/go-build"
BIN_DIR="$REPO_ROOT/bin"

mkdir -p "$TOOLS_DIR" "$TOOLS_BIN" "$CACHE_DIR" "$GOMODCACHE_DIR" "$GOBUILDCACHE_DIR" "$BIN_DIR"

# Activate repo-local Go env for any go-using step that follows.
activate_go_env() {
  export GOROOT="$GO_DIR"
  export GOPATH="$CACHE_DIR/go-path"
  export GOCACHE="$GOBUILDCACHE_DIR"
  export GOMODCACHE="$GOMODCACHE_DIR"
  export GOLANGCI_LINT_CACHE="$CACHE_DIR/golangci-lint"
  export GOENV=off
  export GOTOOLCHAIN=local
  export PATH="$GO_DIR/bin:$GOPATH/bin:$TOOLS_BIN:$PATH"
  mkdir -p "$GOPATH" "$GOLANGCI_LINT_CACHE"
}

# --------------------------------------------------------- install actions ---
install_go() {
  local archive="go${GO_VERSION}.${TARGET_OS}-${TARGET_ARCH}.tar.gz"
  local url="https://go.dev/dl/${archive}"

  if [ -x "$GO_DIR/bin/go" ] && [ "$FORCE" = false ]; then
    local existing
    existing="$("$GO_DIR/bin/go" version 2>/dev/null | awk '{print $3}' | sed 's/^go//')"
    if [ "$existing" = "$GO_VERSION" ]; then
      log "Go $GO_VERSION already installed at $GO_DIR (use --force to reinstall)"
      return 0
    fi
    warn "found Go $existing at $GO_DIR; replacing with $GO_VERSION"
    rm -rf "$GO_DIR"
  elif [ -e "$GO_DIR" ]; then
    rm -rf "$GO_DIR"
  fi

  require_cmd curl
  require_cmd tar

  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  log "downloading $url"
  curl -fsSL -o "$tmp/$archive" "$url" \
    || die "failed to download Go archive"
  curl -fsSL -o "$tmp/$archive.sha256" "$url.sha256" \
    || die "failed to download Go checksum"

  ( cd "$tmp" && printf '%s  %s\n' "$(cat "$archive.sha256")" "$archive" \
      | sha256_check_stdin ) >/dev/null \
    || die "Go archive checksum mismatch"

  mkdir -p "$GO_DIR"
  tar -xzf "$tmp/$archive" -C "$tmp"
  mv "$tmp/go/"* "$GO_DIR/"
  log "installed Go $GO_VERSION at $GO_DIR"
}

install_modcache() {
  $SKIP_MODCACHE && { log "skipping module cache prefetch"; return 0; }
  log "prefetching Go module cache (go mod download)"
  go mod download all
}

install_lint() {
  $WITH_LINT || return 0
  if [ -x "$TOOLS_BIN/golangci-lint" ]; then
    local existing
    existing="$("$TOOLS_BIN/golangci-lint" version --short 2>/dev/null || echo unknown)"
    log "golangci-lint already present (version: $existing)"
    return 0
  fi
  log "installing golangci-lint $LINT_VERSION"
  GOBIN="$TOOLS_BIN" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${LINT_VERSION}"
}

install_vuln() {
  $WITH_VULN || return 0
  if [ -x "$TOOLS_BIN/govulncheck" ]; then
    log "govulncheck already present"
    return 0
  fi
  log "installing govulncheck@latest"
  GOBIN="$TOOLS_BIN" go install golang.org/x/vuln/cmd/govulncheck@latest
}

build_binaries() {
  $SKIP_BUILD && { log "skipping pre-build"; return 0; }
  log "building config-server and config-agent"
  go build -o "$BIN_DIR/config-server" ./cmd/config-server
  go build -o "$BIN_DIR/config-agent"  ./cmd/config-agent
}

verify_install() {
  log "verifying install"
  "$GO_DIR/bin/go" version
  [ -x "$BIN_DIR/config-server" ] && "$BIN_DIR/config-server" -h >/dev/null 2>&1 || true
  log "OK — source 'scripts/dev-env.sh' before running make targets"
}

# ----------------------------------------------------------------- bundle ----
write_manifest() {
  local out="$1"
  cat > "$out" <<EOF
# AAP Config Server offline bundle manifest
created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go_version=$GO_VERSION
target_os=$TARGET_OS
target_arch=$TARGET_ARCH
with_lint=$WITH_LINT
with_vuln=$WITH_VULN
skip_build=$SKIP_BUILD
skip_modcache=$SKIP_MODCACHE
git_commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
EOF
}

build_bundle() {
  local out="$BUNDLE_PATH"
  local stage
  stage="$(mktemp -d)"
  trap 'rm -rf "$stage"' RETURN
  log "staging bundle in $stage"

  mkdir -p "$stage/tools" "$stage/cache"
  cp -a "$GO_DIR" "$stage/tools/go"
  if [ -d "$TOOLS_BIN" ] && [ -n "$(ls -A "$TOOLS_BIN" 2>/dev/null || true)" ]; then
    cp -a "$TOOLS_BIN" "$stage/tools/bin"
  fi
  if [ "$SKIP_MODCACHE" = false ] && [ -d "$GOMODCACHE_DIR" ]; then
    cp -a "$GOMODCACHE_DIR" "$stage/cache/go-mod"
  fi
  if [ "$SKIP_BUILD" = false ] && [ -d "$BIN_DIR" ]; then
    cp -a "$BIN_DIR" "$stage/bin"
  fi
  write_manifest "$stage/MANIFEST"

  out="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
  log "writing $out"
  tar -czf "$out" -C "$stage" .
  ( cd "$(dirname "$out")" && sha256_compute "$(basename "$out")" > "$(basename "$out").sha256" )
  log "bundle ready: $out"
  log "checksum:     $out.sha256"
}

restore_bundle() {
  local in_path="$BUNDLE_PATH"
  [ -f "$in_path" ] || die "bundle not found: $in_path"
  require_cmd tar

  if [ -f "$in_path.sha256" ]; then
    log "verifying bundle checksum"
    ( cd "$(dirname "$in_path")" && sha256_check_stdin < "$(basename "$in_path").sha256" ) >/dev/null \
      || die "bundle checksum mismatch"
  else
    warn "no .sha256 sidecar found at $in_path.sha256 — skipping checksum verify"
  fi

  local stage
  stage="$(mktemp -d)"
  trap 'rm -rf "$stage"' RETURN
  log "extracting bundle"
  tar -xzf "$in_path" -C "$stage"

  [ -f "$stage/MANIFEST" ] || die "bundle missing MANIFEST — refusing to install"
  log "manifest:"
  sed 's/^/    /' "$stage/MANIFEST" >&2

  # Cross-platform safety check.
  local b_os b_arch
  b_os="$(awk -F= '/^target_os=/{print $2}'   "$stage/MANIFEST")"
  b_arch="$(awk -F= '/^target_arch=/{print $2}' "$stage/MANIFEST")"
  if [ "$b_os" != "$TARGET_OS" ] || [ "$b_arch" != "$TARGET_ARCH" ]; then
    die "bundle is for $b_os/$b_arch but this host is $TARGET_OS/$TARGET_ARCH"
  fi

  if [ -e "$GO_DIR" ] && [ "$FORCE" = false ]; then
    die "$GO_DIR exists — pass --force to overwrite"
  fi
  rm -rf "$GO_DIR"
  mv "$stage/tools/go" "$GO_DIR"

  if [ -d "$stage/tools/bin" ]; then
    mkdir -p "$TOOLS_BIN"
    cp -a "$stage/tools/bin/." "$TOOLS_BIN/"
  fi
  if [ -d "$stage/cache/go-mod" ]; then
    mkdir -p "$GOMODCACHE_DIR"
    cp -a "$stage/cache/go-mod/." "$GOMODCACHE_DIR/"
  fi
  if [ -d "$stage/bin" ]; then
    mkdir -p "$BIN_DIR"
    cp -a "$stage/bin/." "$BIN_DIR/"
  fi
  log "restore complete"
}

# ------------------------------------------------------------------- main ----
case "$MODE" in
  install)
    install_go
    activate_go_env
    install_modcache
    install_lint
    install_vuln
    build_binaries
    verify_install
    ;;

  bundle)
    install_go
    activate_go_env
    install_modcache
    install_lint
    install_vuln
    build_binaries
    build_bundle
    ;;

  from-bundle)
    restore_bundle
    activate_go_env
    verify_install
    ;;
esac
