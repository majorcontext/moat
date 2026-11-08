---
title: "Dependencies reference"
navTitle: "Dependencies"
description: "Complete reference for Moat dependency types, version resolution, base image selection, layer caching, and CLI commands."
keywords: ["moat", "dependencies", "runtime", "node", "python", "go", "rust", "docker", "services", "registry", "dynamic", "meta", "layer caching", "base image"]
---

# Dependencies reference

Declares runtime dependencies, services, and packages for container image builds.

## Declaration

Add dependencies to the `dependencies` list in `moat.yaml`:

```yaml
dependencies:
  - node@22
  - python@3.11
  - git
  - npm:lodash@4.17.21
  - postgres@17
```

Run Moat with the workspace containing that configuration:

```bash
moat run ./my-project
```

See the [moat.yaml reference](./02-moat-yaml.md) for the complete `dependencies` field specification.

## Dependency types

### Registry

Registry dependencies defined in Moat's internal registry. Includes language runtimes, system packages, CLI tools, GitHub binaries, and custom installers.

**Syntax:** `<name>` or `<name>@<version>`

```yaml
dependencies:
  - node@22        # Runtime with version
  - python         # Runtime with default version
  - git            # System package
  - claude-code    # Custom installer
  - golangci-lint  # GitHub binary
```

Run `moat deps list` for the full registry and `moat deps info <name>` for current defaults and maintained release lines. Explicit patch pins within a maintained line pass through to the upstream image, repository, or installer.

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

`pip:` packages install into the requested Python interpreter, not a virtual environment. Mixed-runtime and custom-base builds install the requested Python through a temporary uv bootstrap; it does not substitute the distribution's system Python or replace an explicitly pinned `uv` dependency.

### Meta

Bundles that expand to multiple packages during resolution.

**Syntax:** `<bundle-name>`

```yaml
dependencies:
  - go-extras       # gofumpt, govulncheck, goreleaser
  - cli-essentials  # jq, yq, fzf, ripgrep, fd, bat
  - python-dev      # uv, ruff, black, mypy, pytest
  - protobuf              # protoc, protoc-gen-go, protoc-gen-go-grpc, validate, doc
  - protobuf-es           # protoc, protoc-gen-es, protoc-gen-connect-es
  - protobuf-grpc-gateway # grpc-gateway, openapiv2, grpc-gateway-ts
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
| Protobuf | `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`, `protoc-gen-es` | Or use `protobuf` / `protobuf-es` meta bundles |
| CLI tools | `jq`, `yq`, `ripgrep`, `fd`, `bat` | |
| AI coding tools | `claude-code`, `codex-cli` | Or use `moat claude` / `moat codex`. `codex-cli` is pinned; override with `codex-cli@<version>` |
| Workflow tools | `graphite-cli` | Implied by `--grant graphite` |
| Database clients | `psql`, `mysql-client`, `redis-cli`, `sqlite3` | Pair with corresponding service |
| Cloud tools | `aws`, `gcloud`, `kubectl`, `terraform`, `opentofu`, `terragrunt`, `helm` | `terragrunt` needs `terraform` or `opentofu` |
| Services | `postgres`, `mysql`, `redis`, `ollama` | Run as sidecar containers |

Run `moat deps list --type <type>` to filter by category.

`terragrunt` is a thin wrapper that delegates every plan/apply/state operation to a Terraform or OpenTofu binary on `PATH`, so pair it with an engine:

```yaml
# Terraform-backed (terragrunt finds `terraform` automatically):
dependencies: [terraform, terragrunt]

# OpenTofu-backed — point terragrunt at the `tofu` binary:
dependencies: [opentofu, terragrunt]
env:
  TERRAGRUNT_TFPATH: tofu
```

`opentofu` installs the OpenTofu CLI as the `tofu` command.

## Version resolution

Node.js, Python, and Go partial versions resolve to the latest matching release within the specified major or minor line. Other dependencies follow their upstream installer syntax; archive-based CLI installers generally require a full release version.

```yaml
dependencies:
  - node@26            # Latest matching Node.js 26 release
  - python@3.15.2      # Exact interpreter
  - postgres@18.1      # Exact service image tag
  - pnpm@10.21.0       # Exact package manager
```

Explicit package-manager pins take precedence over a project's `packageManager` field. Apt dependencies accept distribution revision syntax, for example `jq@1.6-2.1+deb12u1`. Explicit pins override unversioned bundle entries regardless of declaration order; conflicting explicit pins produce an error.

### Upstream support policy

Moat rejects known end-of-life release lines for Node.js, Python, Go, PostgreSQL, MySQL, and Redis. Support includes upstream security maintenance. For example, Node.js 20 reached EOL in April 2026; use a maintained line shown by `moat deps info node`.

Patch versions within maintained lines do not need a catalog update. Unknown newer releases pass through to upstream version resolution, so a stale Moat binary does not block a new release. The upstream image or installer reports nonexistent versions. Projects without published support dates, including Ollama, do not receive an inferred EOL cutoff.

A daily GitHub Actions workflow checks Node.js's official release schedule and index, lifecycle data from endoflife.date, and records projects without lifecycle policies. It opens or updates a reviewed PR with catalog changes. Defaults advance when their current line reaches EOL; unversioned dependencies in existing binaries also select a maintained successor from their embedded catalog. The successor is the oldest maintained newer line with at least 90 days of support left when available; Go uses the latest maintained toolchain. Other defaults stay pinned. Each Moat binary embeds the catalog and evaluates its EOL dates locally without a network lookup. Upgrade Moat to receive revised dates or new release-line metadata.

Maintainers can refresh the catalog manually:

```bash
go run ./internal/deps/cmd/update-support
```

Version data is cached locally at `~/.moat/cache/versions.json` for 24 hours.

## Base image selection

Moat selects the base image based on declared runtime dependencies.

| Dependencies | Base image |
|--------------|------------|
| `node@<version>` only | `node:<version>-slim` |
| `python@<version>` only | `python:<version>-slim` |
| `go@<version>` only | `golang:<version>` |
| Mixed or none | `debian:bookworm-slim` |

Mixed runtimes use `debian:bookworm-slim` and install the requested versions in separate layers. A `base_image` setting overrides the base image without changing requested runtime versions. Listing `python` without a version selects Moat's default interpreter, including on a custom image that already bundles another Python version; pin `python@<version>` to select that interpreter.

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

For complete project examples showing dependencies with volumes and hooks, see [Recipes](../guides/13-recipes.md).

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

Both modes require Docker runtime. Apple containers do not support Docker socket mounting or privileged mode. See the [moat.yaml reference](./02-moat-yaml.md#docker) for detailed configuration.

## Available services

| Service | Default version | Environment variables injected |
|---------|-----------------|-------------------------------|
| `postgres` | 17 | `MOAT_POSTGRES_*` |
| `mysql` | 8 | `MOAT_MYSQL_*` |
| `redis` | 7 | `MOAT_REDIS_*` |
| `ollama` | 0.9 | `MOAT_OLLAMA_URL` |
| `ministack` | 1.3 | `MOAT_MINISTACK_*` |

Service dependencies require Docker or Apple container runtime. See the [service dependencies guide](../guides/08-services.md) for environment variable details, networking, and security information.

## Hooks

Hook commands run after dependency installation completes. See the [moat.yaml hooks reference](./02-moat-yaml.md#hooks) for field specifications and the [lifecycle hooks guide](../guides/10-hooks.md) for all hook types.

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

- [moat.yaml reference](./02-moat-yaml.md) -- `dependencies` field specification
- [Service dependencies guide](../guides/08-services.md) -- service configuration, environment variables, and networking
- [Lifecycle hooks guide](../guides/10-hooks.md) -- `post_build` hooks for setup after dependency installation
- [CLI reference](./01-cli.md) -- full `moat deps` command reference
