# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

AgentOps is a Go project. The codebase is currently in early development.

## Development Commands

```bash
# Build
go build ./...

# Run tests
go test ./...

# Run a single test
go test -run TestName ./path/to/package

# Run tests with coverage
go test -coverprofile=coverage.out ./...

# Lint (if golangci-lint is installed)
golangci-lint run
```

## Code Style

- Follow standard Go conventions and `go fmt` formatting
- Use `go vet` to catch common issues

## Error Messages

- Good error messages are documentation - when config is missing or something fails, tell users exactly what to set and how
- Users shouldn't have to search docs to understand what went wrong
- Include actionable steps: what env var to set, what command to run, where to find more info

## Git Commits

- Use [Conventional Commits](https://www.conventionalcommits.org/) format: `type(scope): description`
  - Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `build`, `ci`, `perf`
  - Scope is optional but encouraged (e.g., `feat(api): add user endpoint`)
- Do not include `Co-Authored-By` lines for Claude in commit messages
