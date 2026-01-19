# Declarative Dependencies Design

## Overview

Agents need runtimes and tools (Node, protoc, Playwright, etc.) but users shouldn't have to write Dockerfiles or manage installation scripts.

**Solution:** A `dependencies` field in `agent.yaml` using human-readable names with smart defaults.

```yaml
# agent.yaml
name: my-agent
agent: ./src/index.ts

dependencies:
  - node@20        # major version
  - go             # latest stable
  - typescript     # latest, requires node
  - yarn
  - protoc@25
  - sqlc
  - playwright     # installs browsers too
  - gh             # GitHub CLI
  - aws            # AWS CLI
  - psql           # Postgres client
```

**Goals:**
1. Developer convenience (primary) - reduce boilerplate for common tools
2. Reproducibility (secondary) - version pinning for consistent environments

## Versioning

Homebrew-style with smart defaults:

| Syntax | Meaning |
|--------|---------|
| `node` | Latest LTS / stable default |
| `node@20` | Latest 20.x |
| `node@20.11` | Specific minor |
| `node@20.11.1` | Exact version (rare) |

Each dependency defines its own default version in the registry.

## Dependency Requirements

Some tools require a runtime. If missing, error with actionable message:

```
Error: playwright requires node
  Add 'node' to your dependencies list
```

No auto-inference - explicit is better than implicit.

## Initial Dependencies

| Dependency | Type | Notes |
|------------|------|-------|
| `node` | runtime | Default: LTS (20) |
| `go` | runtime | Default: 1.22 |
| `typescript` | npm | Requires node |
| `yarn` | npm | Requires node |
| `pnpm` | npm | Requires node |
| `bun` | github-binary | Alternative runtime/PM |
| `protoc` | github-binary | Protocol Buffers compiler |
| `sqlc` | github-binary | SQL compiler |
| `golangci-lint` | github-binary | Go linter |
| `gh` | apt/binary | GitHub CLI |
| `aws` | custom | AWS CLI |
| `gcloud` | custom | Google Cloud CLI |
| `psql` | apt | PostgreSQL client |
| `mysql` | apt | MySQL client |
| `playwright` | custom | Requires node, installs browsers |

## Registry Format

Dependencies are defined in `internal/deps/registry.yaml`, embedded into the binary at compile time.

```yaml
# Runtimes - use official Docker images or apt
node:
  description: Node.js runtime
  type: runtime
  default: "20"
  versions: [18, 20, 22]

go:
  description: Go programming language
  type: runtime
  default: "1.22"

# Binary downloads from GitHub releases
protoc:
  description: Protocol Buffers compiler
  type: github-binary
  repo: protocolbuffers/protobuf
  asset: "protoc-{version}-linux-x86_64.zip"
  bin: bin/protoc
  default: "25.1"

sqlc:
  description: SQL compiler
  type: github-binary
  repo: sqlc-dev/sqlc
  asset: "sqlc_{version}_linux_amd64.tar.gz"
  default: "1.25.0"

# Apt packages
psql:
  description: PostgreSQL client
  type: apt
  package: postgresql-client

mysql:
  description: MySQL client
  type: apt
  package: mysql-client

# npm global packages
typescript:
  description: TypeScript compiler
  type: npm
  package: typescript
  requires: [node]

yarn:
  type: npm
  package: yarn
  requires: [node]

# Custom handlers for complex installs
playwright:
  description: Browser testing framework
  type: custom
  requires: [node]
  default: "latest"

gcloud:
  description: Google Cloud CLI
  type: custom
  default: "latest"
```

**Install types:**
- `runtime` - Base image selection or apt install
- `github-binary` - Download from GitHub releases
- `apt` - System package
- `npm` - Global npm package
- `pip` - Global pip package
- `custom` - Go handler for complex logic

**Embedded registry:**

```go
// internal/deps/registry.go
package deps

import (
    _ "embed"
    "gopkg.in/yaml.v3"
)

//go:embed registry.yaml
var registryData []byte

var Registry map[string]DepSpec

func init() {
    if err := yaml.Unmarshal(registryData, &Registry); err != nil {
        panic("invalid registry.yaml: " + err.Error())
    }
}
```

## Docker Implementation

Each dependency becomes a Dockerfile instruction. Docker's build cache handles layer caching.

**Generated Dockerfile pattern:**

```dockerfile
FROM ubuntu:22.04

# Layer 1: apt packages (combined for efficiency)
RUN apt-get update && apt-get install -y \
    postgresql-client \
    mysql-client \
    && rm -rf /var/lib/apt/lists/*

# Layer 2: runtimes
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs
RUN curl -fsSL https://go.dev/dl/go1.22.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:$PATH"

# Layer 3: binary downloads (each cached separately)
RUN curl -fsSL https://github.com/protocolbuffers/protobuf/releases/download/v25.1/protoc-25.1-linux-x86_64.zip -o /tmp/protoc.zip \
    && unzip /tmp/protoc.zip -d /usr/local && rm /tmp/protoc.zip

RUN curl -fsSL https://github.com/sqlc-dev/sqlc/releases/download/v1.25.0/sqlc_1.25.0_linux_amd64.tar.gz | tar -C /usr/local/bin -xz

# Layer 4: npm globals (after node)
RUN npm install -g typescript yarn

# Layer 5: custom installs
RUN npm install -g playwright && npx playwright install --with-deps
```

**Caching strategy:**
- Dependencies sorted deterministically for stable layer order
- Changing one version only invalidates that layer and below
- Apt packages batched into one layer

**Image tagging:**
- Generated images tagged with hash of dependency list: `moat/run:<hash>`
- Subsequent runs with same dependencies reuse cached image

## Apple Containers Implementation

Apple containers lack Docker's layer caching.

**MVP approach:** Install at runtime.

```
Container starts → Run install script → Agent executes
```

The install script is generated from the same registry, executed at container start:

```bash
#!/bin/bash
set -e

# apt packages
apt-get update && apt-get install -y postgresql-client mysql-client

# runtimes
curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt-get install -y nodejs
# ... etc
```

**Trade-off:** First run is slower (30-60s for full stack), but keeps implementation simple.

**Future optimization (TODO):**
- After successful install, snapshot container to `~/.moat/images/<hash>.tar`
- On subsequent runs, restore from snapshot if dependency hash matches
- Invalidate when dependency list changes

## Error Handling

**Unknown dependency:**
```
Error: unknown dependency 'nodejs'
  Did you mean 'node'?

  Available runtimes: node, go, python, bun
  Run 'agent deps list' for all available dependencies
```

**Missing requirement:**
```
Error: playwright requires node

  Add 'node' to your dependencies:
    dependencies:
      - node
      - playwright
```

**Invalid version:**
```
Error: invalid version 'node@19'

  Node.js 19 is not an LTS release. Available versions:
    - node@18 (LTS)
    - node@20 (LTS, default)
    - node@22 (LTS)
```

**Install failure:**
```
Error: failed to install protoc@25.1

  Download failed: 404 Not Found
  URL: https://github.com/protocolbuffers/protobuf/releases/download/v25.1/protoc-25.1-linux-x86_64.zip

  Check available versions at:
  https://github.com/protocolbuffers/protobuf/releases
```

## CLI Commands

```bash
agent deps list              # show all available dependencies
agent deps list --runtimes   # show just runtimes
agent deps info playwright   # show details, versions, requirements
```

## Migration

**Breaking change:** The `runtime` field is removed.

**Old format (no longer works):**
```yaml
runtime:
  node: "20"
```

**New format:**
```yaml
dependencies:
  - node@20
```

**Error on old format:**
```
Error: 'runtime' field is no longer supported

  Replace this:
    runtime:
      node: "20"

  With this:
    dependencies:
      - node@20
```

## Implementation Structure

```
internal/
  deps/
    registry.yaml       # dependency definitions (embedded)
    registry.go         # load and parse registry
    resolver.go         # parse "node@20" syntax, resolve versions
    installer.go        # generate install scripts/Dockerfile snippets
    custom/
      playwright.go     # custom handler
      gcloud.go         # custom handler
      aws.go            # custom handler

  config/
    config.go           # replace Runtime with Dependencies []string

  image/
    builder.go          # generate Dockerfile, build and cache image

  container/
    docker.go           # use built image instead of base image
    apple.go            # run install script at container start

cmd/agent/
  deps.go               # 'agent deps list', 'agent deps info'
```

**Key interfaces:**

```go
// Dependency represents a parsed dependency spec
type Dependency struct {
    Name    string  // "node"
    Version string  // "20" or "" for default
}

// Installer generates install commands
type Installer interface {
    Dockerfile(dep Dependency) (string, error)
    Script(dep Dependency) (string, error)  // for Apple containers
}

// CustomHandler for complex dependencies
type CustomHandler interface {
    Installer
    Validate(version string) error
}
```
