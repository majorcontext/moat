---
title: "Dependencies reference"
navTitle: "Dependencies"
description: "Complete reference for Moat dependency types, version resolution, base image selection, layer caching, and CLI commands."
keywords: ["moat", "dependencies", "runtime", "node", "python", "go", "rust", "docker", "services", "registry", "dynamic", "meta", "layer caching", "base image"]
---

# Dependencies reference

Declares runtime dependencies, services, and packages for container image builds.

## Declaration

Add dependencies to the `dependencies` list in `agent.yaml`:

```yaml
dependencies:
  - node@20
  - python@3.11
  - git
  - npm:lodash@4.17.21
  - postgres@17
```

The `--dep` CLI flag adds dependencies for a single run without modifying `agent.yaml`:

```bash
moat run --dep node@20 --dep git ./my-project
```

See the [agent.yaml reference](./02-agent-yaml.md) for the complete `dependencies` field specification.

## Dependency types

### Registry

Registry dependencies defined in Moat's internal registry. Includes language runtimes, system packages, CLI tools, GitHub binaries, and custom installers.

**Syntax:** `<name>` or `<name>@<version>`

```yaml
dependencies:
  - node@20        # Runtime with version
  - python         # Runtime with default version
  - git            # System package
  - claude-code    # Custom installer
  - golangci-lint  # GitHub binary
```

Run `moat deps list` for the full registry.

### Dynamic

Packages installed from language-specific package managers.

**Syntax:** `<prefix>:<package>` or `<prefix>:<package>@<version>`

```yaml
dependencies:
  - node
  - npm:lodash@4.17.21
  - python
  - pip:requests@2.31.0
  - go
  - go:github.com/junegunn/fzf@latest
```

**Supported prefixes:**

| Prefix | Package manager | Required runtime |
|--------|-----------------|------------------|
| `npm:` | npm | `node` |
| `pip:` | pip | `python` |
| `uv:` | uv tool | `uv` |
| `go:` | go install | `go` |
| `cargo:` | cargo | `rust` |

Moat validates that the required runtime is present and returns an error if it is missing.

### Meta

Bundles that expand to multiple packages during resolution.

**Syntax:** `<bundle-name>`

```yaml
dependencies:
  - go-extras       # gofumpt, govulncheck, goreleaser
  - cli-essentials  # jq, yq, fzf, ripgrep, fd, bat
  - python-dev      # uv, ruff, black, mypy, pytest
```

Run `moat deps info <name>` to see the expanded contents of any meta dependency.

### Service

Sidecar containers (databases, caches) that run alongside the agent.

**Syntax:** `<service>` or `<service>@<version>`

```yaml
dependencies:
  - postgres@17
  - redis@7
```

Moat starts each service as a sidecar, generates random credentials, waits for readiness, and injects `MOAT_{SERVICE}_*` environment variables into the agent container.

See [Available services](#available-services) below for the full list, and the [service dependencies guide](../guides/08-services.md) for configuration, environment variables, and networking details.

## Available dependency categories

| Category | Examples | Notes |
|----------|----------|-------|
| Runtimes | `node`, `python`, `go`, `rust`, `bun` | Version-pinnable with `@version` |
| Package managers | `uv`, `yarn`, `pnpm` | |
| Development tools | `git`, `gh`, `lazygit`, `task` | |
| Language tools | `golangci-lint`, `ruff`, `typescript` | Go, Python, Node tool ecosystems |
| CLI tools | `jq`, `yq`, `ripgrep`, `fd`, `bat` | |
| AI coding tools | `claude-code`, `codex-cli` | Or use `moat claude` / `moat codex` |
| Database clients | `psql`, `mysql-client`, `redis-cli`, `sqlite3` | Pair with corresponding service |
| Cloud tools | `aws`, `gcloud`, `kubectl`, `terraform`, `helm` | |
| Services | `postgres`, `mysql`, `redis` | Run as sidecar containers |

Run `moat deps list --type <type>` to filter by category.

## Version resolution

Partial versions resolve to the latest matching release within the specified major or minor line.

| You write | Resolves to |
|-----------|-------------|
| `node@20` | `node@20.11.0` |
| `go@1.22` | `go@1.22.12` |
| `python@3.11` | `python@3.11.8` |
| `node` | Default version for that runtime |

Version data is cached locally at `~/.moat/cache/versions.json` for 24 hours.

## Base image selection

Moat selects the base image based on declared runtime dependencies.

| Dependencies | Base image |
|--------------|------------|
| `node` only | `node:20-slim` |
| `python` only | `python:3.11-slim` |
| `go` only | `golang:1.22` |
| Mixed or none | `debian:bookworm-slim` |

When multiple runtimes are declared (e.g., both `node` and `python`), Moat uses `debian:bookworm-slim` and installs each runtime as a separate layer.

## Layer caching

Moat orders Dockerfile instructions to maximize BuildKit cache hits. Layers are ordered from least to most frequently changed:

1. Base packages (`curl`, `ca-certificates`)
2. User setup (`moatuser`)
3. APT packages
4. Runtimes
5. GitHub binaries
6. npm packages
7. Go packages
8. Custom dependencies
9. Dynamic packages

When a dependency changes, only that layer and subsequent layers rebuild. BuildKit layer caching is shared across runs.

## Docker dependencies

Dependencies for running Docker inside containers.

| Dependency | Description | Use when |
|------------|-------------|----------|
| `docker:host` | Mounts the host Docker socket | Fast startup; agent is trusted |
| `docker:dind` | Runs an isolated Docker daemon with BuildKit sidecar | Isolation from the host Docker daemon is required |

```yaml
dependencies:
  - docker:host  # or docker:dind
```

Both modes require Docker runtime. Apple containers do not support Docker socket mounting or privileged mode. See the [agent.yaml reference](./02-agent-yaml.md#docker) for detailed configuration.

## Available services

| Service | Default version | Environment variables injected |
|---------|-----------------|-------------------------------|
| `postgres` | 17 | `MOAT_POSTGRES_*` |
| `mysql` | 8 | `MOAT_MYSQL_*` |
| `redis` | 7 | `MOAT_REDIS_*` |

Service dependencies require Docker or Apple container runtime. See the [service dependencies guide](../guides/08-services.md) for environment variable details, networking, and security information.

## Hooks

Hook commands run after dependency installation completes. See the [agent.yaml hooks reference](./02-agent-yaml.md#hooks) for field specifications and the [lifecycle hooks guide](../guides/10-hooks.md) for all hook types.

## CLI commands

### `moat deps list`

List all available dependencies in the registry.

```bash
$ moat deps list
$ moat deps list --type <type>
```

| Flag | Description |
|------|-------------|
| `--type <type>` | Filter by category (e.g., `runtime`, `service`, `cli`) |
| `--json` | Output as JSON |

### `moat deps info`

Show details for a specific dependency, including version, type, and expanded contents for meta dependencies.

```bash
$ moat deps info <name>
$ moat deps info go-extras
```

| Flag | Description |
|------|-------------|
| `--json` | Output as JSON |

## Related pages

- [agent.yaml reference](./02-agent-yaml.md) -- `dependencies` field specification
- [Service dependencies guide](../guides/08-services.md) -- service configuration, environment variables, and networking
- [Lifecycle hooks guide](../guides/10-hooks.md) -- `post_build` hooks for setup after dependency installation
- [CLI reference](./01-cli.md) -- full `moat deps` command reference
