---
title: "Sandboxing"
description: "How Moat isolates agent execution using Docker and Apple containers."
keywords: ["moat", "sandboxing", "containers", "docker", "apple containers", "isolation"]
---

# Sandboxing

Moat runs each agent in an isolated container. The container has its own filesystem, process namespace, and network stack. This page explains how container isolation works and the differences between supported runtimes.

## What isolation provides

When code runs in a Moat container:

- **Filesystem isolation** — The container has its own root filesystem. Your workspace is mounted at `/workspace`, but the container cannot access other directories on your host unless explicitly mounted.

- **Process isolation** — Processes inside the container cannot see or interact with processes on the host. A runaway process in the container does not affect your system.

- **Network isolation** — Container traffic routes through Moat's proxy. The proxy enforces network policies and injects credentials. Direct access to the host network is blocked.

- **Resource limits** — Containers have default CPU and memory limits. A container cannot consume all system resources.

## Supported runtimes

Moat supports two container runtimes:

| Runtime | Platform | How it works |
|---------|----------|--------------|
| Docker | macOS (Intel), Linux, Windows | Uses Docker Engine via the Docker API |
| Apple containers | macOS 15+ with Apple Silicon | Uses macOS native containerization |

Moat detects the available runtime automatically at startup. On macOS 15+ with Apple Silicon, it prefers Apple containers. Otherwise, it uses Docker.

Check the active runtime:

```bash
$ moat status

Runtime: apple  # or "docker"
```

## Docker runtime

Docker provides container isolation through Linux kernel features: namespaces for isolation and cgroups for resource limits. On macOS and Windows, Docker runs a Linux VM to provide these features.

**Proxy access:** The credential-injecting proxy binds to `127.0.0.1` (localhost) on the host. Containers reach the proxy via `host.docker.internal`, a Docker-provided hostname that resolves to the host machine.

**Security model:** The proxy listens only on localhost, so only processes on the host machine can access it. Containers access it through Docker's host networking bridge.

## Apple containers

Apple containers are native to macOS 15+ on Apple Silicon. They use macOS virtualization frameworks rather than Docker.

**Proxy access:** The proxy binds to `0.0.0.0` (all interfaces) because containers access the host via a gateway IP (e.g., `192.168.64.1`) rather than a special hostname.

**Security model:** Since the proxy binds to all interfaces, Moat uses per-run token authentication. Each run generates a cryptographically random 32-byte token. The token is passed to the container via the `HTTP_PROXY` URL:

```
HTTP_PROXY=http://moat:<token>@<host>:<port>
```

The proxy rejects requests without a valid token. This prevents other processes on the network from using the proxy.

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
| `node@20` | `node:20` |
| `node@18` | `node:18` |
| `python@3.11` | `python:3.11` |
| `python@3.12` | `python:3.12` |
| `go@1.22` | `golang:1.22` |
| (none) | `ubuntu:22.04` |

The first dependency determines the base image. Additional dependencies are installed into that image.

```yaml
dependencies:
  - node@20    # Base image: node:20
  - python@3.11  # Installed into node:20
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

Use this mode when you trust the agent and need maximum performance.

### docker:dind

Runs an isolated Docker daemon inside the container with automatic BuildKit sidecar:
- Complete isolation from host Docker
- Cannot see or affect host containers
- Automatic BuildKit sidecar for optimized builds

**Security implications:**
- Requires privileged mode (Moat sets this automatically)
- Agent has full control over its own Docker daemon
- No access to host containers or images

Use this mode for untrusted code or when isolation is required.

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

For high-security scenarios, consider running Moat inside a VM or on a dedicated machine.

## Related concepts

- [Credential management](./02-credentials.md) — How credentials are injected through the proxy
- [Network policies](./05-networking.md) — Controlling outbound network access
