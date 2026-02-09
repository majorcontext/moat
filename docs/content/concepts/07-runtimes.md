---
title: "Container runtimes"
navTitle: "Runtimes"
description: "Docker, Apple containers, and gVisor sandbox configuration."
keywords: ["moat", "runtime", "docker", "apple containers", "gvisor", "sandbox"]
---

# Container runtimes

Moat runs agents in isolated containers using either Docker or Apple containers. This page explains how runtime detection works, the security model for each runtime, and how to configure sandboxing.

## Runtime detection

Moat detects the available runtime automatically:

1. On macOS 26+ with Apple Silicon, it checks for Apple containers
2. If Apple containers are unavailable, it uses Docker
3. On Linux and Windows, it uses Docker

The `MOAT_RUNTIME` environment variable overrides automatic detection, forcing either `docker` or `apple`. If the requested runtime is unavailable, Moat returns an error.

## Docker runtime

Docker provides container isolation through Linux kernel features. On Linux, Docker uses the kernel directly. On macOS and Windows, Docker runs a Linux VM.

### Sandbox modes

Docker runtime supports two sandbox modes:

| Mode | OCI runtime | Isolation level | Default on |
|------|-------------|-----------------|------------|
| gVisor | `runsc` | High | Linux |
| Standard | `runc` | Standard | macOS, Windows |

**gVisor** is an application kernel that adds a security layer between containers and the host kernel. It intercepts system calls and implements them in userspace, reducing the attack surface for container escape exploits.

**Platform defaults:**
- **Linux:** gVisor required by default (enhanced isolation)
- **macOS/Windows:** Standard mode (gVisor unavailable in Docker Desktop)

On Linux, if gVisor is not installed, Moat returns an error with installation instructions. gVisor is available for Debian/Ubuntu via the official `gvisor.dev` package repository, and installation details are included in the error message itself.

### Overriding sandbox mode

The `--no-sandbox` flag (or `sandbox: false` in `agent.yaml`) disables the gVisor requirement on Linux, falling back to the standard `runc` runtime. This is useful in environments where gVisor is not installed, during development where gVisor overhead is undesirable, or when debugging runtime-specific issues.

On macOS and Windows, `--no-sandbox` has no effect since gVisor is unavailable in Docker Desktop and standard mode is already the default.

Standard container isolation (`runc`) protects against accidental damage and provides environment separation, but does not defend against container escape exploits. For untrusted code on Linux, gVisor is the recommended default.

### Proxy security model

The credential-injecting proxy binds to `127.0.0.1` (localhost) on the host. Containers reach the proxy via `host.docker.internal`, a Docker-provided hostname that resolves to the host machine.

Since the proxy listens only on localhost, only processes on the host machine can access it. Containers access it through Docker's host networking bridge.

### Platform support

| Platform | gVisor support | Notes |
|----------|----------------|-------|
| Linux | Yes | Native support via `runsc` |
| macOS | No | Docker Desktop does not support gVisor |
| Windows | No | Docker Desktop does not support gVisor |

On macOS and Windows, Moat automatically uses standard mode. Apple containers (macOS 26+ with Apple Silicon) provide an alternative with native macOS isolation.

## Apple containers

Apple containers require macOS 26+ (Tahoe) on Apple Silicon, with the `container` CLI installed from the [Apple container releases](https://github.com/apple/container/releases) page. They use macOS virtualization frameworks rather than Docker.

**Sandbox mode:** Apple containers use macOS's built-in isolation. gVisor is not applicable to this runtime.

**Performance:** Apple containers start faster than Docker on macOS because they don't require a Linux VM.

**Limitations:**
- macOS 26+ required
- Apple Silicon (M1, M2, M3) required
- Cannot mount Docker socket (no `docker:host` or `docker:dind`)
- Cannot run in privileged mode

### Proxy security model

The proxy binds to `0.0.0.0` (all interfaces) because containers access the host via a gateway IP (e.g., `192.168.64.1`) rather than a special hostname.

Since the proxy binds to all interfaces, Moat uses per-run token authentication. Each run generates a cryptographically random 32-byte token, which is embedded in the `HTTP_PROXY` URL passed to the container. The proxy rejects requests without a valid token, preventing other processes on the network from using the proxy.

## Runtime comparison

| Feature | Docker + gVisor | Docker (standard) | Apple containers | microVMs (planned) |
|---------|-----------------|-------------------|------------------|--------------------|
| Platform | Linux | Linux, macOS, Windows | macOS 26+ (Apple Silicon) | Linux |
| Isolation level | High | Standard | Standard | Hardware-level |
| Docker socket access | Yes | Yes | No | Yes (planned) |
| Privileged mode | Yes | Yes | No | No |
| Startup time | ~2-3s | ~1-2s | ~1s | ~100-200ms |
| Resource overhead | Additional CPU usage | Minimal | Minimal | Low |

## Future: VM and microVM support

Moat currently uses container-based isolation (Docker, Apple containers).
VM-backed isolation via Lima is under consideration for a future release,
providing stronger isolation guarantees on macOS.

**Planned runtime:**

- **Lima** — VM-backed runtime on macOS, offering a strong isolation boundary
  for untrusted workloads

**Why Lima:**

Moat prioritizes strong isolation with minimal user-facing complexity. Lima provides a VM-backed security boundary on macOS using native virtualization, without requiring users to manage VMs, images, or guest operating systems. This makes it a better fit than microVM runtimes designed for cloud multi-tenancy or server environments.

**Use cases for VM-based isolation:**

- Running agents on code from untrusted sources
- Defense-in-depth for high-value credentials

**Current workaround:** For VM-level isolation today, run Moat itself inside a VM. This provides hardware isolation at the cost of heavier resource overhead compared to future native Lima support.

## Choosing a runtime

**For development (macOS):**
- Use Apple containers if available (macOS 26+ with Apple Silicon)
- Use Docker with `--no-sandbox` if gVisor is unavailable

**For production (Linux):**
- Use Docker with gVisor for untrusted code
- Install gVisor via the installation command shown in error messages

**For CI/CD:**
- Use Docker with gVisor where available
- Use `--no-sandbox` in environments without gVisor support
- Document the reduced isolation in your security model

## BuildKit and Docker-in-Docker

When using `docker:dind` dependencies, Moat creates an isolated Docker daemon with an automatic BuildKit sidecar. The sidecar shares the main container's network and sandbox mode, and is cleaned up when the run ends.

Moat uses BuildKit for optimized builds with layer caching. BuildKit sidecars inherit the main container's OCI runtime (gVisor or standard). If BuildKit is unavailable, Moat falls back through a chain of alternatives, ending with the legacy Docker builder. See [Dependencies](../reference/06-dependencies.md#docker-dependencies) for Docker dependency configuration.

## Related concepts

- [Sandboxing](./01-sandboxing.md) — Container isolation and workspace mounting
- [Dependencies](../reference/06-dependencies.md) — Docker access modes and BuildKit configuration
- [Networking](./05-networking.md) — Network policies and proxy configuration
