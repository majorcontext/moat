---
title: "Mount syntax"
navTitle: "Mounts"
description: "Reference for Moat mount syntax: host-to-container directory mapping with access mode control."
keywords: ["moat", "mounts", "volumes", "filesystem", "mount syntax", "read-only"]
---

# Mount syntax

Mounts control which host directories are available inside the container. By default, Moat mounts the workspace directory at `/workspace`. Additional mounts are configured with the `--mount` CLI flag or the `mounts` field in `agent.yaml`.

To persist data across runs, use [volumes](./02-agent-yaml.md#volumes) instead. Volumes are managed by moat and survive container destruction.

## Mount string format

Each mount is a colon-separated string:

```text
<source>:<target>[:<mode>]
```

| Field | Description |
|-------|-------------|
| `source` | Path on the host. Absolute or relative to the workspace directory. |
| `target` | Path inside the container. Must be absolute. |
| `mode` | `ro` (read-only) or `rw` (read-write). Default: `rw`. |

The mode field is optional. When omitted, the mount is read-write.

### Examples

| Mount string | Source | Target | Mode |
|--------------|--------|--------|------|
| `./data:/data` | `./data` (relative) | `/data` | read-write |
| `./data:/data:ro` | `./data` (relative) | `/data` | read-only |
| `/host/path:/container/path` | `/host/path` (absolute) | `/container/path` | read-write |
| `./cache:/cache:rw` | `./cache` (relative) | `/cache` | read-write |

## CLI usage

The `--mount` flag adds mounts from the command line. It is repeatable.

```bash
# Mount a directory read-only
moat run --mount ./data:/data:ro ./my-project

# Mount multiple directories
moat run --mount ./configs:/app/configs:ro --mount /tmp/output:/output:rw ./my-project

# Combine with other flags
moat run --grant github --mount ./data:/data:ro ./my-project
```

## agent.yaml usage

The `mounts` field accepts a list of mount strings.

```yaml
mounts:
  - ./data:/data:ro
  - /host/path:/container/path:rw
  - ./cache:/cache
```

CLI `--mount` flags are additive with `agent.yaml` `mounts`. Both sources are combined at runtime.

## Default workspace mount

Moat always mounts the workspace directory at `/workspace` as read-write. This mount is added automatically and does not need to be specified.

```bash
$ moat run ./my-project -- pwd
/workspace

$ moat run ./my-project -- ls
agent.yaml
src/
package.json
```

The workspace path is resolved to an absolute path on the host before mounting. Changes the agent makes in `/workspace` are written directly to the host filesystem and persist after the run completes.

## Path resolution

Relative `source` paths are resolved against the workspace directory. The `target` path must be absolute.

| Source in mount string | Resolved host path (workspace: `/home/user/my-project`) |
|------------------------|-------------------|
| `./data` | `/home/user/my-project/data` |
| `../shared` | `/home/user/shared` |
| `/opt/datasets` | `/opt/datasets` |

## Access modes

| Mode | Behavior |
|------|----------|
| `rw` | Container reads and writes to the mounted directory. Changes are reflected on the host. |
| `ro` | Container reads from the mounted directory. Write attempts fail. |

`rw` is the default when no mode is specified.

## Runtime differences

Both Docker and Apple containers support directory mounts with read-only and read-write modes. The mount syntax is identical across runtimes.

One difference: Apple containers only support directory mounts, not individual file mounts. Moat handles this internally (for example, mounting a directory containing a CA certificate rather than the certificate file directly). If a mount source is a file, Moat mounts the containing directory instead.

## Related pages

- [CLI reference](./01-cli.md) -- `moat run` flags including `--mount`
- [agent.yaml reference](./02-agent-yaml.md) -- `mounts` field, `volumes` field, and all configuration options
- [Sandboxing](../concepts/01-sandboxing.md) -- Workspace mounting and filesystem isolation
- [Security model](../concepts/08-security.md) -- Trust boundaries and defense in depth
