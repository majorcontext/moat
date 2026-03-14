# Mount Excludes via tmpfs Overlay

**Date:** 2026-03-14
**Status:** Draft

## Problem

When using Apple Containers with a shared filesystem mount, VirtioFS opens host-side file descriptors for every file the guest touches. Large dependency trees like `node_modules` cause unbounded FD accumulation (~6K/hour), eventually exhausting system limits. This is a known VirtioFS limitation.

## Solution

Allow users to exclude specific directories from shared mounts. Excluded directories are overlaid with tmpfs mounts inside the container, so the guest sees empty in-memory directories instead of host files. Dependencies installed inside the container land on tmpfs, never touching VirtioFS.

## Config Syntax

The `mounts:` field in `moat.yaml` accepts a mixed array of strings and objects:

```yaml
mounts:
  - ./data:/data:ro                    # existing string format (unchanged)
  - source: .
    target: /workspace
    exclude:
      - node_modules
      - .venv
```

### String form (existing, unchanged)

`source:target[:mode]` where mode is `ro` or `rw` (default).

### Object form (new)

| Field     | Type       | Required | Default      | Description                              |
|-----------|------------|----------|--------------|------------------------------------------|
| `source`  | `string`   | yes      |              | Host path (absolute or relative to workspace) |
| `target`  | `string`   | yes      |              | Container path                           |
| `mode`    | `string`   | no       | `rw`         | `ro` or `rw`                             |
| `exclude` | `[]string` | no       | `[]`         | Paths relative to `target` to overlay with tmpfs |

Invalid `mode` values (anything other than `ro` or `rw`) produce a parse-time validation error.

### Workspace mount replacement

When a mount has `source: .` and `target: /workspace`, the manager treats it as replacing the implicit workspace mount. This is the primary mechanism for adding excludes to the project directory:

```yaml
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
```

The implicit workspace mount is skipped when an explicit mount targets `/workspace`. The manager scans all config mounts for a `/workspace` target before adding the implicit mount (scan-then-decide, not add-then-remove).

Workspace replacement does not suppress other automatic mounts (git worktree `.git` mount, provider mounts, etc.).

### Exclude validation rules

Exclude paths are normalized with `filepath.Clean` before validation. This strips leading `./`, trailing `/`, and collapses redundant separators (e.g., `foo//bar` → `foo/bar`).

After normalization:
- Paths must be relative (no leading `/`)
- Paths must not contain `..` components
- Paths must not be empty (or resolve to `.` after cleaning)
- Duplicate excludes on the same mount are rejected
- Duplicate mount targets (two mounts to the same container path) are rejected
- An exclude path must not conflict with a `volumes:` entry targeting the same container path
- Validation happens at config parse time with clear error messages

Exclude paths that do not exist on the host are valid. The tmpfs mount creates the directory inside the container regardless.

### Tmpfs behavior

- No size limits — tmpfs uses the system default (typically 50% of RAM)
- Tmpfs overlays are always writable, even when the parent mount is `mode: ro`. This is intentional: the primary use case is installing dependencies on tmpfs while keeping source files read-only.
- Excluded directories appear as empty tmpfs mounts inside the container
- Users are responsible for installing dependencies inside the container (e.g., via `pre_run` hooks)

### CLI flags

Excludes are yaml-only for v1. The `--mount` CLI flag (string-form) does not support excludes. This avoids complex flag parsing and covers the primary use case (declarative config in `moat.yaml`).

## Implementation

### Config parsing (`internal/config/`)

Replace `Mounts []string` with `Mounts []MountEntry`:

```go
type MountEntry struct {
    Source   string   `yaml:"source"`
    Target   string   `yaml:"target"`
    ReadOnly bool     // derived from Mode
    Mode     string   `yaml:"mode"`
    Exclude  []string `yaml:"exclude"`
}
```

`MountEntry` implements `yaml.Unmarshaler` using `yaml.Node` dispatch to handle both forms in a mixed-type array:
- Check `node.Kind`: if `yaml.ScalarNode` → parse the string value via existing `ParseMount()` logic
- If `yaml.MappingNode` → decode into the struct fields directly
- Any other node kind → return a parse error

This approach works because yaml.v3 calls `UnmarshalYAML` on each element of the `[]MountEntry` slice individually, passing the raw `yaml.Node` for that element.

The existing `ParseMount()` function remains for string parsing but returns `MountEntry` instead of `*Mount`. The old `Mount` type is removed. `ParseMount` is only called from the run manager, so there are no external consumers.

### Container runtime (`internal/container/`)

Add a `TmpfsMount` type and include it in `Config`:

```go
type TmpfsMount struct {
    Target string // absolute container path
}

type Config struct {
    // ... existing fields ...
    TmpfsMounts []TmpfsMount
}
```

**Docker** (`docker.go`): Convert `TmpfsMount` entries to `mount.Mount{Type: mount.TypeTmpfs, Target: path}` in `HostConfig.Mounts`. Add a code comment noting that Docker processes mounts in slice order, so tmpfs entries after bind entries correctly overlay subdirectories.

**Apple** (`apple.go`): Convert `TmpfsMount` entries to `--tmpfs <path>` flags in `buildCreateArgs`. Apple's `container` CLI supports `--tmpfs` on both `create` and `run` commands ([reference](https://github.com/apple/container/blob/main/docs/command-reference.md)).

Tmpfs mounts are added after bind mounts to ensure correct overlay ordering.

### Run manager (`internal/run/manager.go`)

In `Create()`, during mount assembly:

1. **Scan for workspace replacement**: Before adding the implicit workspace mount, scan `Config.Mounts` for any entry with `Target == "/workspace"`. If found, skip the implicit workspace mount.
2. Parse mounts from config — detect entries with excludes
3. For each exclude path, resolve to absolute container path: `filepath.Join(mount.Target, excludePath)`
4. Validate: no conflicts between exclude paths and `Config.Volumes` targets
5. Collect resolved paths into `Config.TmpfsMounts`

### No init-script changes

Both Docker and Apple container CLI support native tmpfs flags. No changes to `moat-init.sh` are required.

## Testing

### Unit tests

- `MountEntry` YAML unmarshaling: string form, object form, mixed array, invalid node kinds
- Exclude validation: relative paths enforced, `..` rejected, empty strings rejected, duplicates rejected, path normalization (`./foo/` → `foo`)
- Mode validation: `ro`, `rw` accepted, invalid values rejected
- Manager: excludes resolved to absolute container paths
- Manager: workspace mount replacement when explicit `/workspace` mount present
- Manager: implicit workspace mount present when no explicit `/workspace` mount
- Manager: duplicate mount target rejection
- Manager: volume/exclude conflict rejection
- Docker runtime: `mount.TypeTmpfs` entries generated for each tmpfs mount
- Apple runtime: `--tmpfs` flags generated for each tmpfs mount

### Integration test

- Mount with excludes, verify tmpfs is mounted inside container (check `mount` output)
- Write files to excluded directory, verify they don't appear on host

## Documentation updates

- `docs/content/reference/02-moat-yaml.md`: Add object mount syntax, `exclude` field, mode validation
- `docs/content/reference/05-mounts.md`: Add example showing VirtioFS use case, explain tmpfs overlay behavior, note that excludes are yaml-only
