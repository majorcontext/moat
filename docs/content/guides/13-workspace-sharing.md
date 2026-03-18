---
title: "Sharing a workspace between host and container"
description: "Strategies for using the same workspace directory for both native development and Moat containers."
keywords: ["moat", "workspace", "node_modules", "venv", "dependencies", "mount", "exclude", "tmpfs"]
---

# Sharing a workspace between host and container

Moat bind-mounts your workspace into the container at `/workspace`. This means files are shared between the host and the container, including dependency directories like `node_modules` or `.venv`. Dependencies installed on macOS contain platform-native binaries that do not run in the Linux container, and vice versa.

This guide covers two strategies for handling this.

## Strategy 1: Exclude dependency directories

Use mount excludes to hide host-side dependency directories from the container. Excluded paths are overlaid with empty tmpfs (in-memory) mounts, so the container gets its own isolated copy of those directories without affecting the host.

Add an explicit workspace mount in `moat.yaml` with an `exclude` list:

```yaml
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
```

When the container starts, `/workspace/node_modules` is an empty tmpfs mount. The host's `node_modules` directory is untouched. Install dependencies inside the container with a `pre_run` hook:

```yaml
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules

hooks:
  pre_run: npm install
```

`npm install` runs on every container start and performs a full install since `node_modules` starts empty on the tmpfs overlay.

### How excludes work

When a mount entry includes `exclude` paths, Moat creates a tmpfs overlay at each excluded path inside the container. The bind mount still covers the full source directory, but the tmpfs mounts shadow the specified subdirectories. The host filesystem is not modified.

Exclude paths must be:

- Relative to the mount target (no absolute paths, no `..`)
- Unique and non-overlapping (`node_modules` and `node_modules/foo` are rejected)

See the [moat.yaml reference](../reference/02-moat-yaml.md#mounts) for the full mount object specification.

### Excluded data does not persist

Because excludes use tmpfs, anything written to an excluded directory is lost when the container stops. For dependency directories this is usually fine -- `pre_run` reinstalls them on the next start. If you need persistence, use a [named volume](../reference/02-moat-yaml.md#volumes) instead.

## Strategy 2: Separate workspace directory

Maintain a dedicated directory for Moat runs, separate from your native development workspace. Clone or copy your repository into a Moat-specific path:

```bash
$ git clone git@github.com:my-org/my-project.git ~/moat-workspaces/my-project
$ moat run ~/moat-workspaces/my-project
```

Trade-offs:

- Host and container dependencies never conflict
- Uses more disk space (full copy of the repository)
- Changes must be synced between directories (via git push/pull or file copy)

This approach works well for CI pipelines or batch runs where you do not need to edit files on the host while the container is running.

## Per-ecosystem tips

### Node.js

Exclude `node_modules` and install with a `pre_run` hook:

```yaml
dependencies:
  - node@20

mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules

hooks:
  pre_run: npm install
```

`node_modules` contains platform-native binaries (compiled C++ addons, architecture-specific optional dependencies). A macOS `node_modules` directory does not work in a Linux container.

### Python

Exclude `.venv` and create the virtual environment in `pre_run`:

```yaml
dependencies:
  - python@3.12

mounts:
  - source: .
    target: /workspace
    exclude:
      - .venv

hooks:
  pre_run: python -m venv .venv && .venv/bin/pip install -r requirements.txt
```

Virtual environments contain platform-specific binaries (the Python interpreter itself, compiled C extensions). A macOS `.venv` does not work in a Linux container.

If your virtual environment is outside the workspace (e.g., managed by `pyenv` or `poetry` with `virtualenvs.in-project = false`), no exclude is needed.

### Go

The Go module cache (`GOPATH/pkg/mod`) is stored in the user's home directory by default, not in the workspace. No exclude is needed for typical Go projects.

If your project vendors dependencies (`vendor/`), those are pure Go source and work across platforms -- no exclude is needed.

### Rust

Exclude the `target/` directory:

```yaml
dependencies:
  - rust

mounts:
  - source: .
    target: /workspace
    exclude:
      - target
```

`target/` contains compiled artifacts that are architecture- and OS-specific. A macOS `target/` directory does not work in a Linux container.

> **Note:** Unlike `npm install`, `cargo build` against an empty tmpfs `target/` recompiles from scratch on every container start. For large projects, consider using a [named volume](../reference/02-moat-yaml.md#volumes) for `target/` to persist build artifacts across runs.

## Multiple excludes

Combine excludes for projects with multiple dependency directories:

```yaml
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
      - .venv
      - target

hooks:
  pre_run: npm install && python -m venv .venv && .venv/bin/pip install -r requirements.txt
```

## Related guides

- [moat.yaml reference: mounts](../reference/02-moat-yaml.md#mounts) -- Full mount configuration syntax
- [Hooks](./10-hooks.md) -- Configure `pre_run` and other lifecycle hooks
