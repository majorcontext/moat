---
title: "Dependencies"
description: "Declare runtime environments and tools for your agents."
keywords: ["moat", "dependencies", "runtime", "node", "python", "go", "registry"]
---

# Dependencies

Moat builds container images based on the dependencies you declare. Instead of writing Dockerfiles, you list what you need and Moat handles installation, version resolution, and layer caching.

## Declaring dependencies

Add a `dependencies` list to your `agent.yaml`:

```yaml
dependencies:
  - node@20
  - python@3.11
  - git
```

Moat resolves versions, selects an appropriate base image, and generates an optimized Dockerfile.

## Dependency types

### Registry dependencies

Built-in dependencies defined in Moat's registry:

```yaml
dependencies:
  - node@20        # Runtime with version
  - python         # Runtime with default version
  - git            # System package
  - claude-code    # npm package
  - golangci-lint  # GitHub binary
```

Use `moat deps list` to see all available registry dependencies.

### Dynamic dependencies

Install packages directly from package managers:

```yaml
dependencies:
  - node
  - npm:lodash@4.17.21
  - npm:@types/node

  - python
  - pip:requests@2.31.0
  - pip:pandas[excel]

  - uv:flask@2.3.0

  - go
  - go:github.com/junegunn/fzf@latest

  - cargo:ripgrep@14.0
```

Dynamic dependencies use the format `PREFIX:package@version`. Supported prefixes:

| Prefix | Package manager | Requires |
|--------|-----------------|----------|
| `npm:` | npm | node |
| `pip:` | pip | python |
| `uv:` | uv tool | uv |
| `go:` | go install | go |
| `cargo:` | cargo | rust |

Moat validates that the required runtime is present:

```
Error: npm:eslint requires node

  Add 'node' to your dependencies:
    dependencies:
      - node
      - npm:eslint
```

### Meta dependencies

Bundles of related tools for common workflows:

```yaml
dependencies:
  - go-extras       # gofumpt, govulncheck, goreleaser
  - cli-essentials  # jq, yq, fzf, ripgrep, fd, bat
  - python-dev      # uv, ruff, black, mypy, pytest
  - node-dev        # typescript, prettier, eslint
  - k8s             # kubectl, helm
```

Meta dependencies expand to their constituent packages during resolution.

## Runtimes

### Node.js

```yaml
dependencies:
  - node@20    # Specific major version
  - node@18
  - node@22
```

Default: `20`. Moat resolves partial versions (e.g., `node@20` → `node@20.11.0`).

### Python

```yaml
dependencies:
  - python@3.11    # Specific version
  - python@3.12
  - python@3.10
  - python@3.9
```

Default: `3.11`. Moat resolves partial versions.

### Go

```yaml
dependencies:
  - go@1.22    # Specific version
```

Default: `1.22`. Moat resolves partial versions (e.g., `go@1.22` → `go@1.22.12`).

### Rust

```yaml
dependencies:
  - rust    # Installs stable toolchain
```

## AI coding tools

### Claude Code

```yaml
dependencies:
  - node@20
  - claude-code
  - git
```

Or use `moat claude` which includes these automatically.

### Codex CLI

```yaml
dependencies:
  - node@20
  - codex-cli
  - git
```

Or use `moat codex` which includes these automatically.

## Available dependencies

### Runtimes

| Name | Type | Default | Description |
|------|------|---------|-------------|
| `node` | runtime | 20 | Node.js |
| `python` | runtime | 3.11 | Python |
| `go` | runtime | 1.22 | Go |
| `rust` | custom | stable | Rust toolchain |
| `bun` | github-binary | 1.1.38 | Bun JavaScript runtime |

### Package managers

| Name | Type | Description |
|------|------|-------------|
| `uv` | github-binary | Fast Python package manager |
| `yarn` | npm | Yarn package manager |
| `pnpm` | npm | pnpm package manager |

### Development tools

| Name | Type | Description |
|------|------|-------------|
| `git` | apt | Version control |
| `gh` | github-binary | GitHub CLI |
| `lazygit` | github-binary | Terminal UI for git |
| `task` | github-binary | Task runner |
| `act` | github-binary | Run GitHub Actions locally |

### Go tools

| Name | Type | Description |
|------|------|-------------|
| `golangci-lint` | github-binary | Go linter |
| `gofumpt` | github-binary | Go formatter |
| `govulncheck` | go-install | Vulnerability checker |
| `goreleaser` | github-binary | Release automation |
| `air` | github-binary | Live reload |
| `mockgen` | go-install | Mock generator |
| `buf` | github-binary | Protocol buffer tooling |

### Python tools

| Name | Type | Description |
|------|------|-------------|
| `ruff` | uv-tool | Fast linter |
| `black` | uv-tool | Code formatter |
| `mypy` | uv-tool | Type checker |
| `pytest` | uv-tool | Test framework |

### Node tools

| Name | Type | Description |
|------|------|-------------|
| `typescript` | npm | TypeScript compiler |
| `prettier` | npm | Code formatter |
| `eslint` | npm | Linter |
| `claude-code` | npm | Claude Code CLI |
| `codex-cli` | npm | OpenAI Codex CLI |

### CLI tools

| Name | Type | Description |
|------|------|-------------|
| `jq` | apt | JSON processor |
| `yq` | github-binary | YAML processor |
| `fzf` | github-binary | Fuzzy finder |
| `ripgrep` | github-binary | Fast grep |
| `fd` | github-binary | Fast find |
| `bat` | github-binary | cat with syntax highlighting |
| `delta` | github-binary | Git diff viewer |

### Database clients

| Name | Type | Description |
|------|------|-------------|
| `psql` | apt | PostgreSQL client |
| `mysql` | apt | MySQL client |
| `redis-cli` | apt | Redis client |
| `sqlite3` | apt | SQLite client |

### Cloud tools

| Name | Type | Description |
|------|------|-------------|
| `aws` | custom | AWS CLI |
| `gcloud` | custom | Google Cloud CLI |
| `kubectl` | custom | Kubernetes CLI |
| `terraform` | custom | Infrastructure as code |
| `helm` | custom | Kubernetes package manager |

### Build tools

| Name | Type | Description |
|------|------|-------------|
| `protoc` | github-binary | Protocol buffer compiler |
| `sqlc` | github-binary | SQL code generator |

### Testing

| Name | Type | Description |
|------|------|-------------|
| `playwright` | custom | Browser testing |

## CLI commands

### List available dependencies

```bash
$ moat deps list

NAME              TYPE            DEFAULT    DESCRIPTION
node              runtime         20         Node.js JavaScript runtime
python            runtime         3.11       Python programming language
go                runtime         1.22       Go programming language
git               apt             -          Version control system
claude-code       npm             -          Claude Code CLI
...
```

Filter by type:

```bash
moat deps list --type runtime
moat deps list --type npm
moat deps list --type github-binary
```

### Get dependency details

```bash
$ moat deps info node

Name:        node
Type:        runtime
Description: Node.js JavaScript runtime
Default:     20
Versions:    18, 20, 22

Usage in agent.yaml:
  dependencies:
    - node        # Uses default version (20)
    - node@20     # Specific version
    - node@18
```

```bash
$ moat deps info go-extras

Name:        go-extras
Type:        meta
Description: Common Go development tools

Includes:
  - gofumpt
  - govulncheck
  - goreleaser

Usage in agent.yaml:
  dependencies:
    - go-extras
```

## Version resolution

Moat resolves partial versions to the latest matching release:

| You write | Resolves to |
|-----------|-------------|
| `node@20` | `node@20.11.0` |
| `go@1.22` | `go@1.22.12` |
| `python@3.11` | `python@3.11.8` |

Version data is cached locally (`~/.moat/cache/versions.json`) for 24 hours.

## Base image selection

Moat selects the optimal base image based on your dependencies:

| Dependencies | Base image |
|--------------|------------|
| `node` only | `node:20-slim` |
| `python` only | `python:3.11-slim` |
| `go` only | `golang:1.22` |
| Mixed or none | `ubuntu:22.04` |

Using official runtime images reduces build time and image size.

## Layer caching

Moat orders Dockerfile instructions to maximize cache hits:

1. Base packages (curl, ca-certificates)
2. User setup (moatuser)
3. APT packages
4. Runtimes
5. GitHub binaries
6. npm packages
7. Go packages
8. Custom dependencies
9. Dynamic packages

Faster installations come first. When you change a dependency, only layers after it rebuild.

## Example configurations

### Python data science

```yaml
dependencies:
  - python@3.11
  - uv
  - pip:numpy
  - pip:pandas
  - pip:matplotlib
  - pip:jupyter
```

### Go microservice

```yaml
dependencies:
  - go@1.22
  - go-extras
  - golangci-lint
  - protoc
  - buf
  - git
```

### Full-stack JavaScript

```yaml
dependencies:
  - node@20
  - node-dev
  - npm:next@14
  - npm:tailwindcss
  - git
  - psql
```

### DevOps automation

```yaml
dependencies:
  - python@3.11
  - pip:ansible
  - aws
  - kubectl
  - terraform
  - helm
  - cli-essentials
```

### AI coding agent

```yaml
dependencies:
  - node@20
  - claude-code
  - git
  - gh

grants:
  - anthropic
  - github
```

## Related concepts

- [agent.yaml reference](../reference/02-agent-yaml.md) — Full configuration options
- [Running Claude Code](../guides/01-running-claude-code.md) — AI agent setup
- [Running Codex](../guides/02-running-codex.md) — OpenAI Codex setup
