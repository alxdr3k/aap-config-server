# Testing

Status: active.

## Install

Prerequisites:

- Go 1.24+; `go.mod` pins `toolchain go1.24.7`.
- `golangci-lint` only if running `make lint` locally.

For a repo-local Go toolchain and caches, source the local dev environment:

```bash
. scripts/dev-env.sh
```

This expects Go at `.tools/go` and keeps `GOCACHE`, `GOMODCACHE`, and `GOPATH`
under `.cache/`. It also sets `GOTOOLCHAIN=local` so Go does not auto-install a
different toolchain outside the repo.

## Build

```bash
make build
```

Equivalent:

```bash
go build -o bin/config-server ./cmd/config-server
```

## Typecheck

No separate typecheck command is currently defined. Go compilation and tests
perform type checking.

## Lint

```bash
make lint
```

This runs `golangci-lint run ./...`.

## Unit tests

```bash
make test
```

Equivalent:

```bash
go test ./... -timeout 60s
```

## Race tests

```bash
make test-race
```

Equivalent:

```bash
go test ./... -race -timeout 60s
```

## Integration tests

```bash
make test-integration
```

Equivalent:

```bash
go test -tags=integration ./... -timeout 120s
```

## E2E tests

```bash
make test-e2e
```

Equivalent:

```bash
go test -tags=e2e ./... -timeout 300s
```

No dedicated e2e test package is currently present.

## Coverage

```bash
make coverage
```

Generates `coverage.out` and `coverage.html`.

## Persistence checks

No database or schema migration command is currently defined.

## CI

`.github/workflows/ci.yml` runs:

- `golangci/golangci-lint-action`
- `go vet ./...`
- `go test -race ./... -timeout 60s`
- `govulncheck`

## Before opening a PR

- Run `make test`.
- Run `make test-race` for behavior/concurrency changes.
- Run `make lint` if `golangci-lint` is installed.
- Update relevant docs if behavior/schema/runtime changed.
