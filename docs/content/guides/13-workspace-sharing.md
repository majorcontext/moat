---
title: "Sharing a workspace between host and container"
description: "Use mount excludes to isolate platform-specific dependency directories between host and container."
keywords: ["moat", "workspace", "node_modules", "venv", "dependencies", "mount", "exclude", "tmpfs"]
---

# Sharing a workspace between host and container

Moat bind-mounts your workspace into the container at `/workspace`. This means files are shared, including dependency directories like `node_modules` or `.venv`. Dependencies installed on macOS contain platform-native binaries that do not run in the Linux container, and vice versa.

Use mount excludes to hide host-side dependency directories from the container. Excluded paths are overlaid with empty tmpfs (in-memory) mounts, so the container gets its own isolated copy without affecting the host.

```yaml
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules

hooks:
  pre_run: npm install
```

When the container starts, `/workspace/node_modules` is an empty tmpfs mount. The host's `node_modules` is untouched. The `pre_run` hook installs dependencies inside the container on every start.

## How excludes work

Moat creates a tmpfs overlay at each excluded path inside the container. The bind mount still covers the full source directory, but the tmpfs mounts shadow the specified subdirectories. The host filesystem is not modified.

Exclude paths must be relative to the mount target (no absolute paths, no `..`) and non-overlapping (`node_modules` and `node_modules/foo` are rejected).

Because excludes use tmpfs, anything written to an excluded directory is lost when the container stops. For dependency directories this is usually fine — `pre_run` reinstalls them on the next start. If you need persistence, use a [named volume](../reference/02-moat-yaml.md#volumes) instead.

## Node.js

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

## Python

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

If your virtual environment is outside the workspace (e.g., managed by `poetry` with `virtualenvs.in-project = false`), no exclude is needed.

## Rust

```yaml
dependencies:
  - rust

mounts:
  - source: .
    target: /workspace
    exclude:
      - target

hooks:
  pre_run: cargo build
```

`target/` contains compiled artifacts that are architecture- and OS-specific. A macOS `target/` directory does not work in a Linux container.

> **Note:** `cargo build` against an empty tmpfs `target/` recompiles from scratch on every container start. For large projects, consider using a [named volume](../reference/02-moat-yaml.md#volumes) for `target/` to persist build artifacts across runs.

## Go

The Go module cache (`GOPATH/pkg/mod`) is stored in the user's home directory by default, not in the workspace. No exclude is needed for typical Go projects.

If your project vendors dependencies (`vendor/`), those are pure Go source and work across platforms — no exclude is needed.

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

- [moat.yaml reference: mounts](../reference/02-moat-yaml.md#mounts) — Full mount configuration syntax
- [Hooks](./10-hooks.md) — Configure `pre_run` and other lifecycle hooks
