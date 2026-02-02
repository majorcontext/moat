---
title: "Sandboxing"
description: "How Moat isolates agent execution using Docker and Apple containers."
keywords: ["moat", "sandboxing", "containers", "docker", "apple containers", "isolation"]
---

# Sandboxing

Moat runs each agent in an isolated container. The container runs in an isolated network namespace. All outbound traffic is routed through Moat's proxy. This page explains how container isolation works and the differences between supported runtimes.

## What isolation provides

When code runs in a Moat container:

- **Filesystem isolation** — The container has its own root filesystem. Your workspace is mounted at `/workspace`, but the container cannot access other directories on your host unless explicitly mounted.

- **Process isolation** — Processes inside the container cannot see or interact with processes on the host. A runaway process in the container does not affect your system.

- **Network isolation** — Container traffic routes through Moat's proxy, which injects credentials and can enforce network policies. By default, the network policy is `permissive` — all outbound traffic is allowed. Set the policy to `strict` to block all traffic except hosts in an explicit allow list. See [Network policies](./05-networking.md) for details.

- **Resource limits** — Container resource consumption is managed by the container runtime. Docker and Apple containers can be configured with CPU and memory limits if needed.

## Supported runtimes

Moat supports two container runtimes:

| Runtime | Platform | Notes |
|---------|----------|-------|
| Docker | Linux, macOS, Windows | Supports gVisor sandboxing on Linux |
| Apple containers | macOS 26+ (Apple Silicon) | macOS native containerization |

Moat detects the available runtime automatically. On macOS 26+ with Apple Silicon, it prefers Apple containers. Otherwise, it uses Docker.

Check the active runtime:

```bash
$ moat status

Runtime: docker+gvisor  # or "docker" or "apple"
```

See [Container runtimes](./07-runtimes.md) for runtime selection, gVisor configuration, and security models.

## Workspace mounting

Your project directory is mounted into the container at `/workspace`. This is the container's working directory.

```bash
$ moat run ./my-project -- pwd
/workspace

$ moat run ./my-project -- ls
agent.yaml
src/
package.json
```

Changes made by the container are written to your host filesystem. If the agent modifies files, those changes persist after the run.

> [!NOTE]
> `/workspace` is a direct host mount. Any files the agent can read or write there should be considered trusted input/output.

### Additional mounts

Mount additional directories with the `mounts` field in `agent.yaml`:

```yaml
mounts:
  - ./data:/data:ro      # Read-only
  - /host/path:/container/path:rw  # Read-write
```

Or via CLI:

```bash
moat run --mount ./data:/data:ro ./my-project
```

Mount format: `<host-path>:<container-path>:<mode>`

- `ro` — Read-only (container cannot modify)
- `rw` — Read-write (default)

## Image selection

Moat selects a base image based on the `dependencies` field in `agent.yaml`:

| Dependency | Base image |
|------------|------------|
| `node@20` | `node:20-slim` |
| `node@18` | `node:18-slim` |
| `python@3.11` | `python:3.11-slim` |
| `python@3.12` | `python:3.12-slim` |
| `go@1.23` | `golang:1.23` |
| (none or unknown) | `debian:bookworm-slim` |

The first recognized dependency determines the base image. Additional dependencies are installed into that image.

```yaml
dependencies:
  - node@20    # Base image: node:20-slim
  - python@3.11  # Installed into node:20-slim
```

## Docker access modes

Some workloads require Docker access inside the container. Moat supports two modes:

### docker:host

Mounts the host Docker socket (`/var/run/docker.sock`) into the container. This provides:
- Fast startup (no daemon initialization)
- Shared image cache with host
- Full access to host Docker daemon

**Security implications:**
- Agent can see and interact with all host containers
- Agent can create containers that access host network and resources
- Images built inside are cached on host

**`docker:host` is equivalent to root access on the host. Do not use with untrusted agents.**

### docker:dind

Runs an isolated Docker daemon inside the container with automatic BuildKit sidecar:
- Complete isolation from the host Docker daemon (the daemon and image cache live entirely inside the container)
- Cannot see or affect host containers
- Automatic BuildKit sidecar for optimized builds

**Security implications:**
- Requires privileged mode (Moat sets this automatically). Privileged mode grants the container broad kernel capabilities and device access. This is safe only because the Docker daemon is isolated and does not expose host resources.
- Agent has full control over its own Docker daemon
- No access to host containers or images

Use this mode when you need isolation from the host Docker daemon or don't want agents to access host containers.

> When using `docker+gvisor`, the container runs inside gVisor, but `docker:dind` still requires privileged mode inside that sandbox.

**Runtime requirement:** Both modes require Docker runtime. Apple containers cannot mount the Docker socket or run in privileged mode.

See [Dependencies](./06-dependencies.md#docker-dependencies) for configuration details.

## Limitations

Container isolation is not a security boundary against a determined attacker. It provides:

- Protection against accidental damage (runaway processes, disk filling)
- Separation of agent environments
- Controlled network access through the proxy

It does not provide:

- Protection against container escape exploits
- Isolation equivalent to a separate physical machine
- Defense against malicious code specifically designed to escape containers

For running code from unknown sources, run Moat inside a VM or on a dedicated machine. True-VM support (via Lima) is planned for an upcoming release. See [Container runtimes](./07-runtimes.md#future-vm-and-microvm-support) for details.

## Related concepts

- [Container runtimes](./07-runtimes.md) — Runtime selection, gVisor sandboxing, and security models
- [Credential management](./02-credentials.md) — How credentials are injected through the proxy
- [Network policies](./05-networking.md) — Controlling outbound network access
