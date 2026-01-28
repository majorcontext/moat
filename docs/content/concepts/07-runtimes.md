---
title: "Container runtimes"
description: "Docker, Apple containers, and gVisor sandbox configuration."
keywords: ["moat", "runtime", "docker", "apple containers", "gvisor", "sandbox"]
---

# Container runtimes

Moat runs agents in isolated containers using either Docker or Apple containers. This page explains how runtime detection works, the security model for each runtime, and how to configure sandboxing.

## Runtime detection

Moat detects the available runtime automatically:

1. On macOS 15+ with Apple Silicon, it checks for Apple containers
2. If Apple containers are unavailable, it uses Docker
3. On Linux and Windows, it uses Docker

Check the active runtime:

```bash
$ moat status

Runtime: docker+gvisor  # or "docker" or "apple"
```

Override automatic detection:

```bash
export MOAT_RUNTIME=docker  # Force Docker runtime
export MOAT_RUNTIME=apple   # Force Apple containers
```

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

On Linux, if gVisor is not installed, you'll see:

```
Error: gVisor (runsc) is required but not available

To install on Linux (Debian/Ubuntu), copy and run:

  curl -fsSL https://gvisor.dev/archive.key | sudo gpg --dearmor -o /usr/share/keyrings/gvisor.gpg && \
    echo "deb [signed-by=/usr/share/keyrings/gvisor.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
    sudo tee /etc/apt/sources.list.d/gvisor.list && \
    sudo apt update && sudo apt install -y runsc && \
    sudo runsc install && \
    sudo systemctl reload docker

For Docker Desktop (macOS/Windows):
  See https://gvisor.dev/docs/user_guide/install/

To bypass (reduced isolation):
  moat run --no-sandbox
```

### Overriding sandbox mode

On Linux, you can disable gVisor with `--no-sandbox`:

```bash
moat run --no-sandbox ./my-project
```

This uses the standard `runc` runtime. Moat displays a warning:

```
WARN running without gVisor sandbox - reduced isolation
```

Use `--no-sandbox` on Linux when:
- Running in environments without gVisor installed
- Debugging issues specific to the standard runtime
- Working in development where gVisor overhead is undesirable

**Note:** On macOS and Windows, `--no-sandbox` has no effect since gVisor is unavailable by default.

**Security note:** Standard container isolation protects against accidental damage and provides environment separation, but does not defend against container escape exploits. For untrusted code on Linux, install gVisor.

### Proxy security model

The credential-injecting proxy binds to `127.0.0.1` (localhost) on the host. Containers reach the proxy via `host.docker.internal`, a Docker-provided hostname that resolves to the host machine.

Since the proxy listens only on localhost, only processes on the host machine can access it. Containers access it through Docker's host networking bridge.

### Platform support

| Platform | gVisor support | Notes |
|----------|----------------|-------|
| Linux | Yes | Native support via `runsc` |
| macOS | No | Docker Desktop does not support gVisor |
| Windows | No | Docker Desktop does not support gVisor |

On macOS and Windows, use `--no-sandbox` or switch to Apple containers (macOS 15+ with Apple Silicon).

## Apple containers

Apple containers are native to macOS 15+ on Apple Silicon. They use macOS virtualization frameworks rather than Docker.

**Sandbox mode:** Apple containers use macOS's built-in isolation. gVisor is not applicable to this runtime.

**Performance:** Apple containers start faster than Docker on macOS because they don't require a Linux VM.

**Limitations:**
- macOS 15+ required
- Apple Silicon (M1, M2, M3) required
- Cannot mount Docker socket (no `docker:host` or `docker:dind`)
- Cannot run in privileged mode

### Proxy security model

The proxy binds to `0.0.0.0` (all interfaces) because containers access the host via a gateway IP (e.g., `192.168.64.1`) rather than a special hostname.

Since the proxy binds to all interfaces, Moat uses per-run token authentication. Each run generates a cryptographically random 32-byte token. The token is passed to the container via the `HTTP_PROXY` URL:

```
HTTP_PROXY=http://moat:<token>@<host>:<port>
```

The proxy rejects requests without a valid token. This prevents other processes on the network from using the proxy.

## Runtime comparison

| Feature | Docker + gVisor | Docker (standard) | Apple containers |
|---------|-----------------|-------------------|------------------|
| Platform | Linux | Linux, macOS, Windows | macOS 15+ (Apple Silicon) |
| Isolation level | High | Standard | Standard |
| Docker socket access | Yes | Yes | No |
| Privileged mode | Yes | Yes | No |
| Startup time | ~2-3s | ~1-2s | ~1s |
| Resource overhead | ~10-30% CPU | Minimal | Minimal |

## Choosing a runtime

**For development (macOS):**
- Use Apple containers if available (macOS 15+ with Apple Silicon)
- Use Docker with `--no-sandbox` if gVisor is unavailable

**For production (Linux):**
- Use Docker with gVisor for untrusted code
- Install gVisor via the installation command shown in error messages

**For CI/CD:**
- Use Docker with gVisor where available
- Use `--no-sandbox` in environments without gVisor support
- Document the reduced isolation in your security model

## BuildKit and Docker-in-Docker

When using `docker:dind` dependencies, Moat creates an isolated Docker daemon with an automatic BuildKit sidecar for optimized builds.

**Sidecar behavior:**
- Created automatically when the main container starts
- Shares the same network as the main container
- Uses the same sandbox mode (gVisor or standard)
- Cleaned up when the run ends

**Note on gVisor compatibility:** BuildKit sidecars inherit the main container's OCI runtime (gVisor or standard). While BuildKit is expected to work with gVisor, this combination has not been extensively tested in production. If you encounter issues with BuildKit builds on Linux, try `--no-sandbox` to use the standard runtime.

**Fallback chain:**
1. Try BuildKit sidecar (dind mode only)
2. Fall back to host BuildKit (if `BUILDKIT_HOST` is set)
3. Fall back to Docker SDK with BuildKit
4. Fall back to legacy builder (if `MOAT_DISABLE_BUILDKIT=1`)

See [Dependencies](./06-dependencies.md#docker-dependencies) for Docker dependency configuration.

## Runtime configuration

### Force a specific runtime

```bash
export MOAT_RUNTIME=docker  # Force Docker
export MOAT_RUNTIME=apple   # Force Apple containers
```

If the requested runtime is unavailable, Moat returns an error:

```
Error: Apple container runtime not available: container CLI not found
```

### Override sandbox mode (Linux only)

On Linux, disable gVisor requirement:

```bash
moat run --no-sandbox ./my-project
```

Or set it in `agent.yaml`:

```yaml
# agent.yaml
sandbox: false
```

The CLI flag overrides the `agent.yaml` setting.

**Note:** On macOS and Windows, gVisor is automatically disabled (unavailable in Docker Desktop), so this flag has no effect.

## Troubleshooting

### Docker daemon not running

```
Error: no container runtime available: Docker error: Cannot connect to the Docker daemon
```

Start Docker Desktop or the Docker daemon:

```bash
# macOS
open -a Docker

# Linux (systemd)
sudo systemctl start docker
```

### gVisor not available (Linux only)

```
Error: gVisor (runsc) is required but not available
```

This error only occurs on Linux. Install gVisor or use `--no-sandbox`:

```bash
moat run --no-sandbox ./my-project
```

**Note:** On macOS and Windows, this error will not occur - Moat automatically uses standard Docker runtime since gVisor is unavailable.

### Apple container system not running

```
Error: Apple container system not running
```

Moat attempts to start the system automatically. If this fails, start it manually:

```bash
sudo container system start
```

Wait 10-30 seconds for the system to initialize, then retry.

## Related concepts

- [Sandboxing](./01-sandboxing.md) — Container isolation and workspace mounting
- [Dependencies](./06-dependencies.md) — Docker access modes and BuildKit configuration
- [Networking](./05-networking.md) — Network policies and proxy configuration
