---
id: contributing
title: Contributor & Developer Guide
sidebar_label: Contributor Guide
---

# 🤝 Contributor & Developer Guide

Thank you for your interest in contributing to **`d-cron`**! This document provides instructions for setting up a local development environment, running tests, formatting code, and submitting pull requests.

---

## 🛠️ Prerequisites

Before building `d-cron` locally, ensure you have the following installed:

- **Go**: 1.21+ (1.23+ recommended)
- **PostgreSQL**: 12.0+ (or Docker to launch local test DB)
- **`golangci-lint`**: `v1.64.8+`
- **`gofumpt`**: `v0.10.0+`
- **`lefthook`**: Git hook manager

---

## 🚀 Local Setup & Workflow

### 1. Clone Repository & Install Dependencies

```bash
git clone https://github.com/mindfiredigital/d-cron.git
cd d-cron
go mod download
```

---

### 2. Install Git Hooks

Install `lefthook` pre-commit and commit-msg validation hooks:

```bash
make hooks
```

---

### 3. Spin Up Test PostgreSQL Container

```bash
docker run -d --name dcron-test-postgres \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=dcron_test \
  -p 5432:5432 \
  postgres:16-alpine
```

---

### 4. Running Tests & Quality Gates

Run standard unit tests, linters, and formatting checks using the `Makefile`:

```bash
# Format code with gofumpt
make fmt

# Run golangci-lint
make lint

# Run all package unit tests
make test

# Build all packages and examples
make build

# Run full CI gate locally
make ci
```

---

## 📜 Commit Message Guidelines

`d-cron` enforces **Conventional Commits** syntax:

Format: `<type>(<scope>): <short summary>`

### Allowed Types:
- `feat`: A new feature
- `fix`: A bug fix
- `docs`: Documentation changes
- `test`: Adding or refactoring tests
- `refactor`: Code restructuring without functional changes
- `chore`: Build system or maintenance tasks

**Example Commit:**
```bash
git commit -m "feat(executor): add support for OverlapCancelPrevious policy"
```

---

## 📄 Documentation Site Development

To run the Docusaurus documentation website locally:

```bash
cd apps/docs
npm install
npm start
```

Navigate browser to `http://localhost:3000/d-cron/`.
