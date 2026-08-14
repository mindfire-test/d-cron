# Development toolchain for d-cron.
#
# The Makefile assumes the following tool versions are installed (see
# DEVELOPMENT.md):
#   - Go 1.23+            (go)
#   - gofumpt v0.10.0     (gofumpt)
#   - golangci-lint v1.64.8 (golangci-lint)
#
# It delegates to the standard commands so local dev and CI stay in sync.

GO        ?= go
GOFUMPT   ?= gofumpt
GOLANGCI  ?= golangci-lint

.PHONY: all fmt check vet lint test build tidy hooks ci

# Default: the full pre-commit / CI gate.
all: fmt check vet lint test build

# Format all Go source with gofumpt (in place).
fmt:
	$(GOFUMPT) -w .

# Report (do not modify) files that deviate from gofumpt formatting.
check:
	$(GOFUMPT) -l .

# go vet ./...
vet:
	$(GO) vet ./...

# golangci-lint with the committed .golangci.yml baseline.
lint:
	$(GOLANGCI) run

# Run all tests.
test:
	$(GO) test ./...

# Build everything (including all example commands).
build:
	$(GO) build ./...

# Synchronize go.mod/go.sum and prune unused dependencies.
tidy:
	$(GO) mod tidy

# Install the local git hooks (requires lefthook).
hooks:
	lefthook install

# Run every commit gate locally: format -> vet -> lint -> build -> test, then
# validate a Conventional Commits message through the committed commit-msg hook.
ci: fmt vet lint build test
	@printf 'chore(ci): validate gates locally' > /tmp/dcron-commitmsg.txt
	lefthook run commit-msg /tmp/dcron-commitmsg.txt
	@rm -f /tmp/dcron-commitmsg.txt


