# Contributing to d-cron

Thank you for your interest in contributing to `d-cron`! This document provides guidelines and instructions for contributing to this project.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [How to Contribute](#how-to-contribute)
- [Pull Request Process](#pull-request-process)
- [Coding Standards](#coding-standards)
- [Testing](#testing)
- [Reporting Bugs](#reporting-bugs)
- [Suggesting Features](#suggesting-features)

---

## Code of Conduct

By participating in this project, you agree to abide by the [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Please be respectful and constructive in all interactions.

---

## Getting Started

1. Fork the repository on GitHub.
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

- **Go** (version 1.22 or higher)
- **Git**
- **Docker** (optional, required for running Testcontainers integration tests)

### Installation & Tools

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

4. Run unit tests:
   ```bash
   go test ./test/... -count=1
   ```

---

## Project Structure

```
d-cron/
├── dcron/                # Public Scheduler API, options, and job management
├── internal/
│   ├── elector/          # Leader election & PostgreSQL advisory lock coordination
│   ├── clock/            # Min-heap scheduler clock & vixie cron parser
│   ├── executor/         # Execution pipeline (panic recovery, timeout, retry)
│   └── store/            # Opt-in history store & DDL migrations
├── metrics/              # Prometheus metrics subpackage
├── ui/                   # Embedded Web UI dashboard as http.Handler
├── otel/                 # OpenTelemetry tracing subpackage
├── examples/             # Minimal, pgx, and Kubernetes examples
├── test/                 # Test packages (unit, integration, chaos)
├── docs/                 # SRS and SDS specification documents
├── go.mod                # Go module definition
└── LICENSE               # Apache-2.0 License
```

---

## Pull Request Process

1. Create a feature branch off `development`:
   ```bash
   git checkout -b feat/your-feature-name
   ```
2. Follow **Conventional Commits** for commit messages (e.g. `feat(elector): ...`, `fix(clock): ...`, `test(scheduler): ...`).
3. Ensure all linters and tests pass locally before submitting:
   ```bash
   golangci-lint run ./...
   go test ./test/... -count=1
   ```
4. Push your branch to your fork and open a Pull Request targeting the `development` branch.

---

## Coding Standards

- **Formatting**: Run `gofumpt` on all Go source files.
- **Errors**: Return typed, actionable errors from `dcron/errors.go`.
- **Zero Credentials in Logs**: Never log database connection strings, credentials, or sensitive job payloads (`NFR-501`).
- **Context Awareness**: Always propagate and respect `context.Context` cancellation.

---

## Reporting Bugs

If you find a bug, please create a GitHub issue using the [Bug Report Template](.github/ISSUE_TEMPLATE/bug_report.md). Include steps to reproduce, expected vs actual behavior, and environment details.

## Suggesting Features

Feature requests are welcome! Please open an issue using the [Feature Request Template](.github/ISSUE_TEMPLATE/feature_request.md).
