# Dependency Handling v2 - Implementation Plan

## Problem Statement

The current dependency system has several limitations:

1. **Version Pinning Issues**: `go@1.25` fails because only major versions are defined. Users must know the exact patch version (e.g., `go@1.25.6`).

2. **Limited Package Managers**: No extensibility for delegating to package managers like `npm:eslint` or `pip:pytest`.

3. **Missing Common Tools**: First-class support for commonly needed tools is incomplete.

4. **Python Tooling**: The Python ecosystem lacks modern tooling (`uv` vs `pip`).

## Design Goals

1. **Smart Version Resolution**: Support major/minor versions that resolve to latest patch (e.g., `go@1.22` → `go@1.22.10`)
2. **Extensible Package Managers**: `npm:package`, `pip:package`, `uv:package`, `cargo:package`, `go:package`
3. **First-Class Common Tools**: Expand registry with frequently requested tools
4. **Modern Python**: Default to `uv` for Python package management
5. **Layer Caching**: Preserve deterministic image hashing while adding version flexibility

---

## Phase 1: Version Resolution System

### Current State
```yaml
# registry.yaml
go:
  type: runtime
  default: "1.22"
  versions: ["1.21", "1.22"]  # Must match exactly
```

User specifies `go@1.25` → error (not in versions list)

### Proposed Solution

Add a version resolver that:
1. Accepts major.minor specifications
2. Resolves to latest available patch version at build time
3. Caches resolution for deterministic builds within a session

```go
// internal/deps/versions/resolver.go

type VersionResolver interface {
    // Resolve turns "1.22" into "1.22.10" (latest patch)
    Resolve(ctx context.Context, dep string, version string) (string, error)

    // Available returns all available versions for a dependency
    Available(ctx context.Context, dep string) ([]string, error)
}

// Implementations per runtime
type GoVersionResolver struct{}
type NodeVersionResolver struct{}
type PythonVersionResolver struct{}
```

### Implementation Details

**Go Version Resolution**:
```go
// Fetch https://go.dev/dl/?mode=json&include=all
// This returns ALL versions back to Go 1.2.2, including:
// - Stable releases (go1.22.12, go1.25.6, etc.)
// - Release candidates (go1.26rc2)
// - Beta releases (go1.22beta1)
//
// The API returns newest first, with `stable: true/false` field.
// Filter by stable=true and match highest patch for requested major.minor.

func (r *GoVersionResolver) Resolve(ctx context.Context, version string) (string, error) {
    // "1.22" → fetch versions → find "1.22.12" (latest patch)
    // "1.25" → find "1.25.6"
    // "1.22.5" → return as-is (explicit patch, verify it exists)
    // "1.18" → works! API has versions back to 1.2.2
}
```

**Node Version Resolution**:
```go
// Use official Node.js releases API
// https://nodejs.org/dist/index.json
// Returns all versions with lts field for LTS releases
```

**Python Version Resolution**:
```go
// Options:
// 1. python.org FTP listing (complex to parse)
// 2. pyenv version list (requires pyenv)
// 3. Docker Hub python image tags API
// 4. Hardcoded list updated periodically (simplest, less maintenance)
//
// Recommendation: Start with hardcoded list, add API later if needed
```

### Registry Changes
```yaml
go:
  type: runtime
  default: "1.25"
  resolver: go  # Uses go.dev/dl API with include=all
  # No need for version-pattern - API provides validation
```

### Caching Strategy

Version resolutions are cached in `~/.moat/cache/versions.json`:
```json
{
  "resolved": {
    "go@1.22": {"version": "1.22.10", "resolved_at": "2024-01-15T..."},
    "node@20": {"version": "20.11.0", "resolved_at": "2024-01-15T..."}
  },
  "ttl": "24h"
}
```

For image hashing, use the **resolved** version (not user-specified) to ensure deterministic builds.

---

## Phase 2: Extensible Package Manager Delegation

### Syntax

```yaml
dependencies:
  - node@20
  - npm:eslint                    # npm install -g eslint
  - npm:@anthropic-ai/claude-code # scoped packages work
  - python@3.11
  - pip:pytest                    # pip install pytest
  - uv:ruff                       # uv tool install ruff
  - cargo:ripgrep                 # cargo install ripgrep
  - go:golang.org/x/tools/gopls   # go install ...@latest
```

### New Install Types

```go
// internal/deps/types.go
const (
    TypeRuntime      InstallType = "runtime"
    TypeGithubBinary InstallType = "github-binary"
    TypeApt          InstallType = "apt"
    TypeNpm          InstallType = "npm"      // Existing: registered packages
    TypeNpmDynamic   InstallType = "npm:"     // New: dynamic npm packages
    TypePip          InstallType = "pip:"     // New: pip packages
    TypeUv           InstallType = "uv:"      // New: uv packages
    TypeCargo        InstallType = "cargo:"   // New: cargo packages
    TypeGoInstall    InstallType = "go-install"
    TypeGoDynamic    InstallType = "go:"      // New: dynamic go install
    TypeCustom       InstallType = "custom"
    TypeMeta         InstallType = "meta"
)
```

### Parser Changes

```go
// internal/deps/parser.go

func Parse(s string) (Dependency, error) {
    // Check for package manager prefix
    if strings.HasPrefix(s, "npm:") {
        return parseDynamicPackage(s, "npm")
    }
    if strings.HasPrefix(s, "pip:") {
        return parseDynamicPackage(s, "pip")
    }
    // ... etc

    // Existing logic for registry-based deps
}

type Dependency struct {
    Name       string      // e.g., "node", "eslint"
    Version    string      // e.g., "20", "8.0.0"
    Type       InstallType // runtime, npm:, pip:, etc.
    PackageRef string      // For dynamic: "npm:eslint" stores "eslint"
}
```

### Implicit Requirements

Package manager prefixes imply runtime requirements:

| Prefix | Requires |
|--------|----------|
| `npm:` | `node`   |
| `pip:` | `python` |
| `uv:`  | `python`, `uv` |
| `cargo:` | `rust` |
| `go:`  | `go` |

```go
func (d Dependency) ImplicitRequires() []string {
    switch d.Type {
    case TypeNpmDynamic:
        return []string{"node"}
    case TypePip:
        return []string{"python"}
    case TypeUv:
        return []string{"python", "uv"}
    // ...
    }
}
```

### Install Commands

```go
// internal/deps/install.go

func getDynamicPackageCommands(dep Dependency) InstallCommands {
    switch dep.Type {
    case TypeNpmDynamic:
        return InstallCommands{
            Commands: []string{
                fmt.Sprintf("npm install -g %s", dep.PackageRef),
            },
        }
    case TypePip:
        return InstallCommands{
            Commands: []string{
                fmt.Sprintf("pip install %s", dep.PackageRef),
            },
        }
    case TypeUv:
        return InstallCommands{
            Commands: []string{
                fmt.Sprintf("uv tool install %s", dep.PackageRef),
            },
        }
    case TypeCargo:
        return InstallCommands{
            Commands: []string{
                fmt.Sprintf("cargo install %s", dep.PackageRef),
            },
        }
    case TypeGoDynamic:
        return InstallCommands{
            Commands: []string{
                fmt.Sprintf("GOBIN=/usr/local/bin go install %s@latest", dep.PackageRef),
            },
        }
    }
}
```

---

## Phase 3: Python Tooling (uv vs pip)

### Tradeoffs

| Feature | pip | uv |
|---------|-----|-----|
| Speed | Slow | 10-100x faster |
| Resolution | Basic | Advanced resolver |
| Lock files | No | Yes (uv.lock) |
| Tool install | `pip install` | `uv tool install` |
| Size | Already in Python | ~10MB binary |
| Maturity | Decades | Young (2024) |
| Virtual envs | Manual | Automatic |

### Recommendation

Default to **`uv`** for moat because:
1. Container builds benefit enormously from speed
2. `uv tool install` provides isolated tool environments
3. Better resolution prevents dependency conflicts
4. Single static binary (easy to install in containers)

### Implementation

```yaml
# registry.yaml
uv:
  description: Fast Python package manager
  type: github-binary
  repo: astral-sh/uv
  asset: "uv-x86_64-unknown-linux-gnu.tar.gz"
  bin: uv
  default: "0.4.0"
  requires: [python]

# pip stays available for compatibility
pip:
  description: Python package installer (legacy)
  type: apt
  package: python3-pip
  requires: [python]
```

**For dynamic packages**:

```yaml
dependencies:
  - python@3.11
  - uv                    # Install uv itself
  - uv:ruff              # Install ruff via uv tool install
  - uv:black             # Install black via uv tool install
```

If user specifies `pip:package` without `uv`, fall back to pip:
```go
func getPythonPackageCommands(dep Dependency) InstallCommands {
    if dep.Type == TypeUv {
        return InstallCommands{
            Commands: []string{
                fmt.Sprintf("uv tool install %s", dep.PackageRef),
            },
        }
    }
    // TypePip fallback
    return InstallCommands{
        Commands: []string{
            fmt.Sprintf("pip install %s", dep.PackageRef),
        },
    }
}
```

---

## Phase 4: Expanded First-Class Dependencies

### New Registry Entries

```yaml
# Rust toolchain
rust:
  description: Rust programming language
  type: custom
  default: "stable"

# Common development tools
jq:
  description: JSON processor
  type: apt
  package: jq

yq:
  description: YAML processor
  type: github-binary
  repo: mikefarah/yq
  asset: "yq_linux_amd64.tar.gz"
  bin: yq_linux_amd64
  default: "4.40.0"

fzf:
  description: Fuzzy finder
  type: github-binary
  repo: junegunn/fzf
  asset: "fzf-{version}-linux_amd64.tar.gz"
  bin: fzf
  default: "0.46.0"

ripgrep:
  description: Fast grep alternative
  type: github-binary
  repo: BurntSushi/ripgrep
  asset: "ripgrep-{version}-x86_64-unknown-linux-musl.tar.gz"
  bin: "ripgrep-{version}-x86_64-unknown-linux-musl/rg"
  default: "14.1.0"

fd:
  description: Fast find alternative
  type: github-binary
  repo: sharkdp/fd
  asset: "fd-v{version}-x86_64-unknown-linux-musl.tar.gz"
  bin: "fd-v{version}-x86_64-unknown-linux-musl/fd"
  default: "10.1.0"

bat:
  description: Cat with syntax highlighting
  type: github-binary
  repo: sharkdp/bat
  asset: "bat-v{version}-x86_64-unknown-linux-musl.tar.gz"
  bin: "bat-v{version}-x86_64-unknown-linux-musl/bat"
  default: "0.24.0"

delta:
  description: Better git diff
  type: github-binary
  repo: dandavison/delta
  asset: "delta-{version}-x86_64-unknown-linux-musl.tar.gz"
  bin: "delta-{version}-x86_64-unknown-linux-musl/delta"
  default: "0.17.0"

lazygit:
  description: Terminal UI for git
  type: github-binary
  repo: jesseduffield/lazygit
  asset: "lazygit_{version}_Linux_x86_64.tar.gz"
  bin: lazygit
  default: "0.44.0"

# Python tools (installed via uv)
uv:
  description: Fast Python package manager
  type: github-binary
  repo: astral-sh/uv
  asset: "uv-x86_64-unknown-linux-gnu.tar.gz"
  bin: uv
  default: "0.4.0"

ruff:
  description: Fast Python linter
  type: uv-tool
  package: ruff
  requires: [uv]

black:
  description: Python formatter
  type: uv-tool
  package: black
  requires: [uv]

mypy:
  description: Python type checker
  type: uv-tool
  package: mypy
  requires: [uv]

pytest:
  description: Python testing framework
  type: uv-tool
  package: pytest
  requires: [uv]

# Node tools
prettier:
  description: Code formatter
  type: npm
  package: prettier
  requires: [node]

eslint:
  description: JavaScript linter
  type: npm
  package: eslint
  requires: [node]

# Database tools
redis-cli:
  description: Redis client
  type: apt
  package: redis-tools

sqlite3:
  description: SQLite client
  type: apt
  package: sqlite3

# Cloud CLIs (already have aws, gcloud)
azure:
  description: Azure CLI
  type: custom
  default: "latest"

terraform:
  description: Infrastructure as Code
  type: github-binary
  repo: hashicorp/terraform
  asset: "terraform_{version}_linux_amd64.zip"
  bin: terraform
  default: "1.7.0"

kubectl:
  description: Kubernetes CLI
  type: custom
  default: "stable"

helm:
  description: Kubernetes package manager
  type: github-binary
  repo: helm/helm
  asset: "helm-v{version}-linux-amd64.tar.gz"
  bin: "linux-amd64/helm"
  default: "3.14.0"

# Meta bundles
python-dev:
  description: Python development tools (ruff, black, mypy, pytest)
  type: meta
  requires: [uv, ruff, black, mypy, pytest]

node-dev:
  description: Node.js development tools (typescript, prettier, eslint)
  type: meta
  requires: [node, typescript, prettier, eslint]

k8s:
  description: Kubernetes tools (kubectl, helm)
  type: meta
  requires: [kubectl, helm]

cli-essentials:
  description: Essential CLI tools (jq, yq, fzf, ripgrep, fd, bat)
  type: meta
  requires: [jq, yq, fzf, ripgrep, fd, bat]
```

---

## Phase 5: Architecture Improvements

### ARM64 Support

Current registry assumes x86_64. Add architecture detection:

```yaml
uv:
  type: github-binary
  repo: astral-sh/uv
  assets:
    amd64: "uv-x86_64-unknown-linux-gnu.tar.gz"
    arm64: "uv-aarch64-unknown-linux-gnu.tar.gz"
```

```go
func getAssetForArch(spec DepSpec, arch string) string {
    if spec.Assets != nil {
        if asset, ok := spec.Assets[arch]; ok {
            return asset
        }
    }
    return spec.Asset // fallback to default
}
```

### Dependency Ordering

Ensure dependencies install in correct order:

```go
func SortForInstall(deps []Dependency) []Dependency {
    // 1. Runtimes first (node, python, go, rust)
    // 2. Package managers (npm is implicit with node, uv after python)
    // 3. Apt packages
    // 4. GitHub binaries
    // 5. Language package installs (npm:, pip:, uv:, cargo:, go:)
    // 6. Custom installers
}
```

### Error Messages

Improve error messages for version resolution:

```
Error: go@1.25 is not available

  Latest Go versions:
    1.22.10 (recommended)
    1.21.13
    1.23.5 (latest)

  Try:
    dependencies:
      - go@1.22   # Resolves to 1.22.10
      - go@1.23   # Resolves to 1.23.5

  See available versions: moat deps list go
```

---

## Implementation Order

### Step 1: Version Resolution (High Impact)
- [ ] Create `internal/deps/versions/` package
- [ ] Implement `GoVersionResolver` with go.dev API
- [ ] Implement `NodeVersionResolver` with nodejs.org API
- [ ] Implement `PythonVersionResolver`
- [ ] Add version caching
- [ ] Update parser to use resolved versions
- [ ] Update image tag generation to use resolved versions

### Step 2: Registry Expansion (Quick Wins)
- [ ] Add `uv` as first-class dependency
- [ ] Add CLI essentials (jq, yq, fzf, ripgrep, fd, bat)
- [ ] Add Python dev tools (ruff, black, mypy, pytest)
- [ ] Add more Node tools (prettier, eslint)
- [ ] Add meta bundles (python-dev, node-dev, cli-essentials)
- [ ] Add ARM64 asset variants

### Step 3: Package Manager Delegation (Extensibility)
- [ ] Add new install types to `types.go`
- [ ] Update parser to handle `npm:`, `pip:`, `uv:`, `cargo:`, `go:` prefixes
- [ ] Implement install commands for each prefix
- [ ] Add implicit requirement resolution
- [ ] Update Dockerfile generation for proper ordering
- [ ] Validate package names for safety

### Step 4: Python Tooling Improvements
- [ ] Add `uv` binary download
- [ ] Add `TypeUvTool` for uv-installed tools
- [ ] Decide pip vs uv default behavior
- [ ] Document Python tooling recommendations

### Step 5: CLI Improvements
- [ ] `moat deps list` - show all available deps
- [ ] `moat deps list <name>` - show versions for specific dep
- [ ] `moat deps search <query>` - search registry
- [ ] `moat deps resolve <dep@version>` - show what version would be used

---

## Testing Strategy

1. **Unit Tests**: Each resolver, parser extension, install command generator
2. **Integration Tests**: Full Dockerfile generation with new dep types
3. **E2E Tests**: Build containers with various dep combinations
4. **Version Resolution Tests**: Mock API responses, test caching

---

## Migration & Compatibility

The new system is **backward compatible**:
- Existing `dependencies:` lists continue to work
- Version-exact specifications still work (`go@1.22.10`)
- New syntax (`npm:eslint`) is additive

**Breaking change consideration**: If we change image tag hashing to use resolved versions, images built with `go@1.22` before and after resolution system would have different hashes. This is acceptable since the actual Go version would be different anyway (old: failed or used default, new: uses latest patch).

---

## Open Questions

1. **Cache TTL**: How long to cache version resolutions? 24h seems reasonable.
2. **Offline mode**: What if version resolution API is unavailable? Use cached or fail?
3. **Pre-release versions**: Should `go@1.24` resolve to `1.24rc1`? Probably not.
4. **Version ranges**: Support `node@>=18`? Probably overkill, keep it simple.
5. **pip vs uv default**: Should bare `pip:` use uv if available? Lean toward explicit.
