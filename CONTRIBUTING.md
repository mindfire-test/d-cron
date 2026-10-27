# Contributing to d-cron

Thank you for your interest in contributing to `d-cron`! This document provides guidelines and instructions for contributing to this project.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Project Structure](#project-structure)
- [How to Contribute](#how-to-contribute)
- [Pull Request Process](#pull-request-process)
- [Coding Standards](#coding-standards)
- [Testing](#testing)
- [Reporting Bugs](#reporting-bugs)
- [Suggesting Features](#suggesting-features)
- [Questions?](#questions)

---

## Code of Conduct

By participating in this project, you agree to abide by the [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Please be respectful and constructive in all interactions.

---

## Getting Started

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/d-cron.git
   cd d-cron
   ```
3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/mindfire-test/d-cron.git
   ```

---

## Development Setup

### Prerequisites

- **Go** (version 1.23 or higher)
- **Git**
- **Docker** (optional, required for PostgreSQL testcontainers integration tests)

### Installation

1. Install dependencies:
   ```bash
   go mod download
   ```

2. Install development tools:
   ```bash
   go install mvdan.cc/gofumpt@latest
   go install github.com/daixiang0/gci@latest
   go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
   go install github.com/evilmartians/lefthook@latest
   ```

3. Install git hooks:
   ```bash
   lefthook install
   ```

4. Verify formatting and linting:
   ```bash
   gofumpt -l .
   golangci-lint run
   ```

5. Run unit tests:
   ```bash
   go test -v -race ./...
   ```

---

## Project Structure

```
d-cron/
├── dcron/                # Primary public Go library package (Scheduler, Job, Options)
│   ├── dcron.go          # Constructor and lifecycle (Start, Stop)
│   ├── options.go        # Functional options (WithNamespace, WithTimeout, etc.)
│   ├── job.go            # Job registration and configuration definitions
│   ├── loop.go           # Leader poll loop, dispatch, and idempotency key
│   ├── context.go        # Context helpers (Epoch, IdempotencyKey, Fence)
│   ├── leadership.go     # Leadership() accessor & health check
│   ├── admin.go          # REST HTTP management API (Pause, Resume, Add, Delete)
│   └── hooks.go          # Completion & failure notification webhooks
├── internal/             # Internal core implementation packages
│   ├── clock/            # Cron parser, schedule interfaces, min-heap timer
│   ├── elector/          # Session advisory lock elector, state machine, gates
│   ├── executor/         # Invocation boundary, recover(), timeout, backoff retry
│   ├── store/            # Execution history store & idempotent SQL migrations
│   └── tools/godoccheck/ # Godoc comment coverage verification tool
├── metrics/              # Metrics seam & Prometheus adapter interfaces
├── otel/                 # OpenTelemetry tracing adapter interfaces
├── ui/                   # Embedded server-rendered dashboard UI
├── examples/             # Ready-to-run integration examples
│   ├── minimal/          # Minimal 10-line integration example
│   ├── pgx/              # Native pgx driver connection pool example
│   ├── prometheus-adapter/ # Prometheus metrics exporter example
│   ├── with-dashboard/   # Router-mounted dashboard example
│   └── kubernetes/       # 5-replica Kubernetes deployment manifest
├── apps/
│   └── docs/             # Docusaurus documentation website
├── test/                 # Test suites
│   ├── clock/            # Cron parser and schedule calculation tests
│   ├── elector/          # Election state machine and safety gate unit tests
│   ├── executor/         # Panic recovery, timeout, and backoff retry tests
│   ├── scheduler/        # Scheduler integration and lifecycle tests
│   ├── store/            # History persistence and migration tests
│   ├── ui/               # Dashboard HTML template rendering tests
│   └── integration/      # Real PostgreSQL testcontainers & failover suite
├── .github/
│   └── workflows/        # GitHub Actions CI/CD workflows
├── .lefthook.yml         # Git hooks configuration (pre-commit, commit-msg, pre-push)
├── go.mod                # Go module definition
├── go.sum                # Go module checksums
├── LICENSE               # MIT License
└── PROJECT_STATUS.txt    # Project status, architecture, and testing guide
```

---

## How to Contribute

### Reporting Bugs

If you find a bug, please create an issue using the [bug report template](.github/ISSUE_TEMPLATE/bug_report.md). Include:

- Clear description of the bug
- Steps to reproduce
- Expected vs actual behavior
- Environment details (OS, Go version, PostgreSQL version)
- Relevant log output

### Suggesting Features

Feature requests are welcome! Please use the [feature request template](.github/ISSUE_TEMPLATE/feature_request.md) and include:

- Clear description of the proposed feature
- Problem statement or use case
- Proposed API design or solution
- Consideration of backward compatibility

### Contributing Code

1. Create a new branch from `main`:
   ```bash
   git checkout main
   git pull upstream main
   git checkout -b feat/your-feature-name
   ```

2. Make your changes following the [coding standards](#coding-standards)

3. Add or update tests covering your changes

4. Verify all tests pass with the race detector enabled:
   ```bash
   go test -v -race ./...
   ```

5. Verify code formatting, linting, and godoc coverage:
   ```bash
   gofumpt -w .
   golangci-lint run
   go run ./internal/tools/godoccheck
   ```

6. Commit your changes using Conventional Commits:
   ```bash
   git add .
   git commit -m "feat(scheduler): add dynamic job rescheduling"
   ```

7. Push to your fork:
   ```bash
   git push origin feat/your-feature-name
   ```

8. Open a Pull Request targeting `main` using the Pull Request template.

---

## Pull Request Process

1. Ensure the PR description clearly explains the changes and rationale.
2. Link any related issues (e.g., "Closes #42").
3. All automated CI checks must pass (`lint`, `cross-build`, `unit-tests`, `integration-tests`, `vulnerability-scan`, `codeql`).
4. Code must follow project standards and pass `gofumpt` formatting.
5. All exported symbols must include descriptive godoc comments verified by `godoccheck`.
6. At least one maintainer must review and approve the PR before merging.

### Branch Protection & Governance Rules

To protect the integrity of the default branch (`main`), repository settings enforce:
- **Mandatory Pull Requests**: Direct pushes to `main` are disabled; all changes must merge via pull request.
- **Independent Peer Review**: At least 1 review approval from a project maintainer is required before merging. Self-approval is disallowed.
- **Required Status Checks**: The following automated CI checks must pass before merging:
  - `Lint & Format Check` (`gofumpt`, `golangci-lint`, and `godoccheck`)
  - `Cross-Compilation Matrix` (`linux/darwin/windows` across `amd64/arm64`)
  - `Unit Tests` (with race detector enabled)
  - `Postgres Integration Matrix` (PostgreSQL 14, 15, 16)
  - `Vulnerability Scan` (`govulncheck`)
  - `CodeQL Analysis` (SAST security analysis)
- **Linear History**: Branches must be up to date before merging; force pushes and branch deletions are disabled.

### PR Title Convention

Use the [Conventional Commits](https://www.conventionalcommits.org/) format:

- `feat:` - New features
- `fix:` - Bug fixes
- `docs:` - Documentation changes
- `refactor:` - Code refactoring
- `test:` - Adding or updating tests
- `chore:` - Maintenance tasks

---

## Coding Standards

### Go Code Style

- Follow [Effective Go](https://go.dev/doc/effective_go) guidelines.
- Use `gofumpt` (strict gofmt) to format code:
  ```bash
  gofumpt -w .
  ```
- Use `gci` or `goimports` for import grouping and management.
- Follow [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments).

### Core Architectural Boundaries

- **Zero Core External Dependencies**: The scheduler core (`dcron/` and `internal/`) must never import third-party metrics SDKs, distributed stores, or external frameworks. Standard library `database/sql` is the primary persistence seam.
- **Panic Isolation**: All scheduled job invocations must be guarded by a deferred `recover()` boundary so a crashing job cannot terminate the host process.
- **Context Cancellation**: Always propagate `context.Context` to job handlers, database queries, and coordination operations.
- **Zero Credentials in Logs**: Never log database connection strings, passwords, or sensitive credentials.
- **Exported Godoc**: Every exported type, function, method, and constant must have a clear doc comment starting with the symbol's name.

### Commit Messages

Follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:

```
<type>(<scope>): <subject>

<body>

<footer>
```

**Types:**

- `feat` - New feature
- `fix` - Bug fix
- `docs` - Documentation changes
- `style` - Code style changes (formatting, etc.)
- `refactor` - Code refactoring
- `test` - Adding or updating tests
- `chore` - Maintenance tasks

**Example:**

```
feat(executor): add exponential backoff retry with jitter

- Introduce Retry configuration with attempts, backoff, and jitter
- Apply exponential backoff delay between retryable execution attempts
- Add unit tests verifying retry exhaustion and jitter bounds

Closes #19
```

---

## Testing

### Running Tests

Run all unit tests with race detection:
```bash
go test -v -race ./...
```

Run tests for a specific package:
```bash
go test -v ./test/scheduler/...
go test -v ./test/elector/...
```

Run real PostgreSQL integration tests (requires Docker or a live database):
```bash
go test -v -race -tags integration ./test/integration/...
```

### Writing Tests

- Place test files in their corresponding package test directory under `test/`.
- Use table-driven tests for comprehensive input/output coverage.
- Test both success and failure error paths.
- Ensure all concurrent tests run cleanly under the Go race detector (`-race`).

---

## Reporting Bugs

Found a bug? Help us fix it:

1. Search existing issues to ensure it hasn't already been reported.
2. Open an issue using the [bug report template](.github/ISSUE_TEMPLATE/bug_report.md).
3. Include reproducible steps, error logs, and environment details.

---

## Suggesting Features

Have an idea for `d-cron`? We'd love to hear it:

1. Check existing feature requests and discussions.
2. Open an issue using the [feature request template](.github/ISSUE_TEMPLATE/feature_request.md).
3. Clearly describe the problem and your proposed design.

---

## Questions?

If you have questions about contributing or need guidance:

- Open a discussion or issue on GitHub
- Reach out to the maintainers

Thank you for contributing to d-cron! 🚀
