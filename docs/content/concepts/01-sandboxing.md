---
title: "Sandboxing"
description: "How Moat isolates agent execution using Docker and Apple containers."
keywords: ["moat", "sandboxing", "containers", "docker", "apple containers", "isolation"]
---

# Sandboxing

Moat runs each agent in an isolated container with its own filesystem, process tree, and network namespace. All outbound traffic routes through Moat's proxy. This page explains the isolation model, its security properties, and its limitations.

## What isolation provides

Moat containers enforce four layers of isolation:

- **Filesystem isolation** — The container has its own root filesystem. Only explicitly mounted directories are shared with the host.

- **Process isolation** — Processes inside the container cannot see or interact with host processes. A runaway process does not affect your system.

- **Network isolation** — All container traffic routes through Moat's proxy, which injects credentials and enforces network policies. The default policy is `permissive` (all outbound traffic allowed). A `strict` policy blocks everything except explicitly allowed hosts. See [Network policies](./05-networking.md) for details.

- **Resource isolation** — The container runtime manages CPU, memory, and disk consumption independently from the host.

## Supported runtimes

Moat supports Docker (Linux, macOS, Windows) and Apple containers (macOS 26+ with Apple Silicon). It detects the available runtime automatically, preferring Apple containers when available. See [Runtimes](./07-runtimes.md) for details.

## Workspace mounting

Your project directory is mounted into the container at `/workspace`. This is the container's working directory. Changes made by the agent are written back to your host filesystem and persist after the run.

The workspace mount is a direct host bind mount. Files the agent reads or writes there are trusted input/output. Moat does not sandbox file access within the mounted directory -- the agent has the same read/write access as the user who started the run.

When running multiple agents on the same repository, use git worktrees to give each agent its own working directory on a separate branch. This prevents agents from interfering with each other's file changes. See [Git worktrees](../guides/12-worktrees.md) for details.

Additional host directories can be mounted into the container. See [Mount syntax](../reference/05-mounts.md) for the full specification.

## Image selection

Moat selects a container base image from the `dependencies` field in `moat.yaml`. The first recognized runtime dependency (Node.js, Python, or Go) determines the base image. When no recognized runtime is declared, Moat falls back to a minimal Debian image. Additional dependencies are installed into the selected base image during the build step.

This means you declare what the agent needs, not which Docker image to use. See [Dependencies](../reference/06-dependencies.md) for the full resolution model and supported dependency types.

## Docker access modes

Some workloads need Docker access inside the container. Moat provides two modes with different security trade-offs.

**Host socket mounting** (`docker:host`) shares the host's Docker daemon with the container. The agent can see and manage all host containers, pull and push images, and create containers with access to host network and resources. This is equivalent to root access on the host. Do not use it with untrusted agents.

**Docker-in-Docker** (`docker:dind`) runs an isolated Docker daemon inside the container with an automatic BuildKit sidecar. The agent cannot see or affect host containers, and the daemon and image cache live entirely inside the container. This mode requires privileged mode, which Moat sets automatically. Privileged mode grants broad kernel capabilities, but security is maintained because the inner daemon is isolated from host resources.

When using `docker+gvisor`, the container runs inside gVisor, but Docker-in-Docker still requires privileged mode inside that sandbox.

Both modes require Docker as the container runtime. Apple containers do not support Docker socket mounting or privileged mode. See [Dependencies](../reference/06-dependencies.md#docker-dependencies) for configuration details.

## Kernel sandbox (Landlock)

The container boundary is Moat's outer wall. `isolation.kernel_sandbox` adds an inner one: a [Landlock](https://docs.kernel.org/userspace-api/landlock.html) filesystem sandbox applied to the agent process itself, just after the container entrypoint drops privileges. Landlock restrictions are enforced by the kernel, inherited by every child process, and cannot be widened once applied — even code with arbitrary execution inside the run stays behind them.

```yaml
# moat.yaml
isolation:
  kernel_sandbox: true
  sandbox:
    allow_write:      # extra writable container paths (optional)
      - /data
```

Or ad hoc: `moat run --kernel-sandbox -- <command>`.

The policy is a write allowlist: the whole container filesystem stays readable, and writes are limited to `/workspace`, the agent's home directory, `/tmp`, `/var/tmp`, `/dev`, `/proc`, `/run`, every read-write mount target, and any `allow_write` entries. Everything else — `/usr`, `/etc`, paths owned by the run user but outside the allowlist — is write-denied by the kernel regardless of file permissions.

What it guarantees, and what it does not:

- **Best-effort by design.** Landlock needs Linux 5.13+ with the `landlock_*` syscalls permitted (Docker 23+ allows them by default; gVisor does not implement them). When unavailable, the run starts anyway and logs a warning — check `moat logs` for the `kernel sandbox active (Landlock ABI vN)` line to confirm enforcement.
- **Allowlist-only.** Landlock cannot deny a subpath inside an allowed tree, so there is no `deny_paths` support yet (tracked in [#396](https://github.com/majorcontext/moat/issues/396)). Use read-only mounts or mount `exclude:` lists to mask paths instead.
- **Filesystem only.** Network policy stays with the proxy (see [Network policies](./05-networking.md)); the kernel sandbox does not restrict sockets in this first cut.
- **`sudo` stops working.** Landlock requires `no_new_privs`, which disables setuid binaries inside the sandboxed process tree. Install dependencies at build time or via `hooks.pre_run` (which runs before the sandbox is applied).
- **Complements, not replaces, the container.** Under gVisor the guest kernel provides no Landlock and the gVisor boundary is already stronger; the kernel sandbox matters most with `sandbox: none`/`--no-sandbox` and on Apple containers.

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
- [Git worktrees](../guides/12-worktrees.md) — Workspace isolation for parallel agents on separate branches
