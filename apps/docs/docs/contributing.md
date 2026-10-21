---
id: contributing
title: Contributing
sidebar_label: Contributing
---

# Contributing

Thank you for your interest in contributing to `d-cron`! We welcome contributions from the community and value every contributor, whether you are fixing a bug, adding a feature, improving documentation, or reporting an issue.

## Code of Conduct

We are committed to providing a welcoming and inclusive experience for everyone, and we expect all contributors to be respectful and constructive in all interactions.

### Expected Behavior

- Be respectful and considerate toward other people
- Welcome newcomers and help them get started
- Focus on what is best not just for us as individuals, but for the overall community
- Show empathy and kindness toward other community members

### Unacceptable Behavior

- Harassment, discrimination, or offensive comments
- Personal attacks or trolling
- Public or private harassment
- Publishing others' private information without their explicit permission

### Reporting Issues

If you experience or witness unacceptable behavior, please report it by opening an issue in the [d-cron repository](https://github.com/mindfire-test/d-cron/issues). All reports will be reviewed and investigated promptly and fairly.

## Getting Started

### 1. Fork the Repository

Fork the [d-cron repository](https://github.com/mindfire-test/d-cron) on GitHub.

### 2. Clone Your Fork

```bash
git clone https://github.com/YOUR_USERNAME/d-cron.git
cd d-cron
```

### 3. Set Up Development Tools

```bash
# Code formatting
go install mvdan.cc/gofumpt@latest

# Import management
go install github.com/daixiang0/gci@latest

# Linting
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Git hooks
go install github.com/evilmartians/lefthook@latest && lefthook install
```

### 4. Sync and Create a Branch

Keep your fork up to date with the base repository:

```bash
# Add the upstream remote (one-time setup)
git remote add upstream https://github.com/mindfire-test/d-cron.git

# Sync with the latest main branch
git checkout main
git pull upstream main
git push origin main

# Create your feature branch
git checkout -b feat/your-feature-name
```

## Development Workflow

### Code Formatting

All Go code must be formatted with `gofumpt`:

```bash
gofumpt -l -w .
```

### Import Management

Imports must be organized with `gci`:

```bash
gci write --skip-vendor -s standard -s default -s "prefix(github.com/mindfire-test/d-cron)" .
```

### Linting

Run the linter before committing:

```bash
golangci-lint run ./...
```

### Testing

Run all package unit and integration tests with the race detector:

```bash
go test -v -race ./...
```

### Documentation Site Testing

If you modified documentation in `apps/docs`:

```bash
cd apps/docs
npm install
npm run build
```

### Commit Messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add new feature
fix: resolve issue with X
docs: update documentation
refactor: improve code structure
test: add tests for X
chore: update dependencies
```

## Pull Request Process

1. Ensure your code passes all linting (`golangci-lint run ./...`) and tests (`go test -v -race ./...`)
2. Update documentation in `apps/docs/docs/` if your changes alter public APIs or configuration options
3. Submit a pull request with a clear description of changes referencing any related issues
4. Your PR will be reviewed by maintainers

## Project Structure

```
dcron/              # Core distributed scheduler and public API
internal/
  elector/          # PostgreSQL session advisory lock backend and election state machine
  engine/           # In-memory min-heap timer wheel and cron expression evaluator
  fencing/          # Monotonic epoch tokens and transaction fencing
executor/           # Job execution runner, panic recovery, retries, and timeouts
metrics/            # Pluggable metrics recorder interface and outcome types
otel/               # OpenTelemetry distributed tracing adapter
store/              # PostgreSQL durable execution history store and migrations
ui/                 # Embedded Web Dashboard UI handler and static templates
apps/
  docs/             # Docusaurus documentation website
examples/           # Ready-to-run standalone examples
test/               # Integration tests and test suites
```

## Attribution

This Code of Conduct is adapted from the [Contributor Covenant](https://www.contributor-covenant.org/).
