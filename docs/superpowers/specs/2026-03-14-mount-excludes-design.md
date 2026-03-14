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

### Workspace mount replacement

When a mount has `source: .` and `target: /workspace`, the manager treats it as replacing the implicit workspace mount. This is the primary mechanism for adding excludes to the project directory:

```yaml
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
```

The implicit workspace mount is skipped when an explicit mount targets `/workspace`.

### Exclude validation rules

- Paths must be relative (no leading `/`)
- Paths must not contain `..`
- Paths must not be empty
- Duplicate excludes on the same mount are rejected
- Validation happens at config parse time with clear error messages

### Tmpfs behavior

- No size limits — tmpfs uses the system default (typically 50% of RAM)
- Excluded directories appear as empty tmpfs mounts inside the container
- Users are responsible for installing dependencies inside the container (e.g., via `pre_run` hooks)

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

`MountEntry` implements `yaml.Unmarshaler` to handle both forms:
- String input → parsed via existing `ParseMount()` logic into `MountEntry`
- Object input → decoded directly from YAML fields

The existing `ParseMount()` function remains for string parsing but returns `MountEntry` instead of `*Mount`. The old `Mount` type is removed.

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

**Docker** (`docker.go`): Convert `TmpfsMount` entries to `mount.Mount{Type: mount.TypeTmpfs, Target: path}` in `HostConfig.Mounts`.

**Apple** (`apple.go`): Convert `TmpfsMount` entries to `--tmpfs <path>` flags in `buildCreateArgs`.

Tmpfs mounts are added after bind mounts to ensure correct overlay ordering.

### Run manager (`internal/run/manager.go`)

In `Create()`, during mount assembly:

1. Parse mounts from config — detect object-style mounts with excludes
2. For each exclude path, resolve to absolute container path: `filepath.Join(mount.Target, excludePath)`
3. Collect resolved paths into `Config.TmpfsMounts`
4. Detect workspace mount replacement: if any explicit mount targets `/workspace`, skip the implicit workspace mount

### No init-script changes

Both Docker and Apple container CLI support native tmpfs flags. No changes to `moat-init.sh` are required.

## Testing

### Unit tests

- `MountEntry` YAML unmarshaling: string form, object form, mixed array
- Exclude validation: relative paths enforced, `..` rejected, empty strings rejected, duplicates rejected
- Manager: excludes resolved to absolute container paths
- Manager: workspace mount replacement when explicit `/workspace` mount present
- Docker runtime: `mount.TypeTmpfs` entries generated for each tmpfs mount
- Apple runtime: `--tmpfs` flags generated for each tmpfs mount

### Integration test

- Mount with excludes, verify tmpfs is mounted inside container (check `mount` output)
- Write files to excluded directory, verify they don't appear on host

## Documentation updates

- `docs/content/reference/02-moat-yaml.md`: Add object mount syntax, `exclude` field
- `docs/content/reference/05-mounts.md`: Add example showing VirtioFS use case, explain tmpfs overlay behavior
