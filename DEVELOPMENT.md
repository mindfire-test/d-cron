# Development

This project is a Go library and currently ships no third-party runtime
dependencies (see SDS NFR-401). The toolchain below is **required** for a
consistent pre-commit / CI gate; versions are pinned for reproducibility.

## Required tools

| Tool            | Pinned version          | Verify with        |
| --------------- | ----------------------- | ------------------ |
| Go              | `go 1.23` (go.mod)      | `go version`       |
| gofumpt         | `v0.10.0`               | `gofumpt -version` |
| golangci-lint   | `v1.64.8`               | `golangci-lint version` |

`gofumpt` reads the Go language version and module path from `go.mod`, so no
`.gofumpt.toml` is required. The `gofumpt` linter inside `golangci-lint` is
configured with matching `lang-version`/`module-path` in `.golangci.yml` so the
formatter and the linter never disagree.

## Standard workflow

Anything the CI runs can be run locally via the Makefile:

```sh
make fmt        # gofumpt -w .          — format in place
make check      # gofumpt -l .          — report non-compliant files
make vet        # go vet ./...
make lint       # golangci-lint run     — uses ./.golangci.yml
make test       # go test ./...
make build      # go build ./...
make tidy       # go mod tidy
```

Or, equivalently, the canonical sequence:

```sh
go mod tidy
gofumpt -w .
gofumpt -l .
go vet ./...
golangci-lint run
go test ./...
go build ./...
```

## Package layout

See the SDS §9. `internal/elector`, `internal/clock`, `internal/executor` are
Phase-1 work; `internal/store`, `metrics`, `ui`, and `otel` land in later
phases. The directories exist now so the namespace is reserved and builds stay
green.

## Commit hooks (lefthook)

Commits are gated locally with [lefthook](https://github.com/lefthook/lefthook),
mirroring the organisation's DeepScanBot workflow. Install the tool and the
hooks once:

```sh
go install github.com/lefthook/lefthook@latest   # or: npm i -g @lefthook/lefthook
lefthook install
make hooks        # alias for `lefthook install`
```

The committed `.lefthook.yml` enforces four blocking gates:

| Gate          | Hook        | When                  | Commands                                   |
| ------------- | ----------- | --------------------- | ------------------------------------------ |
| `format`      | pre-commit  | before a commit lands | `gofumpt -w` on staged `.go` files          |
| `lint`        | pre-commit  | before a commit lands | `golangci-lint run ./...`                   |
| `build`       | pre-commit  | before a commit lands | `go build ./...`                            |
| **message**   | commit-msg  | on every commit        | Conventional Commits regex on the msg file  |
| `build`+`test`| pre-push    | before pushing         | `go build ./...` and `go test ./...`        |

Run all gates locally (including the commit-msg check) with:

```sh
make ci        # format -> vet -> lint -> build -> test -> commit-msg
```

### Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>[(<scope>)]: <subject>
```

`<subject>` is 1–100 characters. Accepted types: `feat`, `fix`, `docs`,
`style`, `refactor`, `test`, `chore`, `perf`, `ci`, `build`, `revert`,
`merge`. A commit whose message does not match is rejected before it is
recorded. Example:

```
feat(clock): add cron expression parser

Add a 5-field cron parser with timezone lookup, plus table-driven tests for
valid and ambiguous schedules.
```

