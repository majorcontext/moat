# Kernel Sandbox (Landlock) — In-Container Defense-in-Depth

**Issue:** [#396](https://github.com/majorcontext/moat/issues/396)
**Status:** First cut — Linux Landlock, in-container mode only.

## Goal

Apply an OS-native, kernel-enforced filesystem sandbox to the agent process
inside the Moat container, so that even with arbitrary code execution the agent
cannot widen its own restrictions. This is the first slice of issue #396:
in-container Landlock enforcement. macOS Seatbelt, the containerless "local
mode", `deny_paths`, and kernel-level network rules are follow-ups.

## Prior art surveyed

- **OpenAI Codex CLI** — Rust `landlock` crate + seccomp: "read everywhere,
  write only workspace + tmp" posture. No deny-list inside allowed trees
  (Landlock cannot express it).
- **Anthropic sandbox-runtime** — bubblewrap (not Landlock) on Linux, Seatbelt
  on macOS. Write-denied-by-default with `allowWrite` paths; reads allowed.
- **go-landlock** (github.com/landlock-lsm/go-landlock, v0.9.0, MIT, by the
  Landlock maintainer) — pure Go, no cgo, best-effort ABI downgrade, handles
  `no_new_privs` and multi-thread restriction internally.
- **systemd / OpenSSH / Chrome** — allowlist-style Landlock policies layered
  under a stronger outer boundary, same shape as this design.

Key constraint discovered: **Landlock is allowlist-only.** A `deny_paths`
entry inside an allowed tree is not expressible (v1–v6 ABIs). Rather than
silently ignoring the issue-sketch field, `isolation.sandbox.deny_paths`
parses but returns a clear "not yet supported" error.

## Configuration surface

```yaml
# moat.yaml
isolation:
  kernel_sandbox: true     # apply Landlock to the agent process (default: false)
  sandbox:
    allow_write:           # extra absolute container paths writable by the agent
      - /data
```

- `isolation.mode` is reserved (accepts `container` or empty; `local` errors
  with a pointer to #396 until the containerless mode ships).
- `isolation.sandbox.deny_paths` errors with an explanation (allowlist-only).
- CLI: `moat run --kernel-sandbox` enables it ad hoc (shared ExecFlags, so all
  agent commands get it).

## Default policy (in-container)

Reads are allowed everywhere; writes are allowlisted (Codex-style posture,
adapted to Moat's container layout):

| Access | Paths |
|--------|-------|
| Read-only | `/` (everything) |
| Read-write | `/workspace`, `$HOME`, `/tmp`, `/var/tmp`, `/dev` (+ioctl), `/proc`, `/run`, rw bind-mount targets, named-volume targets, `isolation.sandbox.allow_write` entries |

Rationale:

- `$HOME` rw: agents write config, caches, logs (`~/.claude`, `~/.npm`, …).
- `/dev` rw + ioctl: TTY handling (`/dev/tty`, `/dev/ptmx`, `/dev/shm`).
- `/proc`, `/run` rw: `/dev/stdout` → `/proc/self/fd/1`; unix sockets (SSH
  agent bridge at `/run/moat/ssh`) need write to connect. Both are already
  masked/read-only-protected by the container runtime where it matters.
- rw mount targets: a mount the user asked for must stay writable; `:ro`
  mounts are excluded (already read-only at the mount layer).
- Renames across directories need the Landlock `refer` right (ABI v2+); rw
  rules include it.
- Network (TCP bind/connect, ABI v4) is deliberately **not** restricted:
  domain-level policy stays with the credential proxy. First cut is
  filesystem-only.

The policy is computed **host-side** (`internal/sandbox`, unit-testable pure
function), serialized as JSON into `MOAT_SANDBOX_POLICY`, and applied
in-container by a small helper binary.

## Enforcement mechanics

1. `internal/sandbox` — `Policy` type, `BuildPolicy(...)` from config + mounts,
   JSON round-trip. Host-side, cross-platform.
2. `cmd/moat-sandbox` — tiny static Go binary. Reads `MOAT_SANDBOX_POLICY`,
   scrubs it from the environment, applies Landlock via go-landlock
   best-effort, prints one status line to stderr (`kernel sandbox active
   (Landlock ABI vN)` or a degradation warning), then `exec`s the agent
   command. Single-threaded restrict-then-exec avoids the Go thread race;
   restrictions are inherited by all children and cannot be lifted.
3. `internal/sandboxbin` — embeds prebuilt linux/amd64 + linux/arm64
   `moat-sandbox` binaries in the moat CLI, mirroring the `internal/initbin`
   pattern from PR #441: committed fail-closed stubs, `go generate` builds the
   real blobs (wired into `make build-cli` / goreleaser's `go generate ./...`),
   checksums verified by a unit test. When #441 merges, this can fold into the
   Go moat-init.
4. `internal/deps` — `ImageSpec.NeedsKernelSandbox` triggers the moat-init
   entrypoint, COPYs `moat-sandbox` into the image, and contributes to the
   image tag hash (toggling the sandbox rebuilds the image).
5. `moat-init.sh` — final exec chain becomes
   `exec [gosu moatuser] /usr/local/bin/moat-sandbox "$@"` when
   `MOAT_SANDBOX_POLICY` is set; fails closed if the helper is missing.
6. `internal/run/manager_create.go` — builds the policy (workspace target,
   home, rw mounts, config extras), sets the env var and the ImageSpec flag,
   and fails fast with rebuild instructions if the embedded helper is a stub
   or the architecture is unsupported.

## Degradation & non-guarantees (documented honestly)

- **Best-effort by design**: if Landlock is unavailable (kernel < 5.13,
  seccomp blocking the syscalls — Docker < 23 — or gVisor), the helper warns
  and continues unsandboxed. The warning is visible in `moat logs`.
- Features degrade with kernel ABI (e.g. `refer` needs 5.19+, ioctl grouping
  6.10+); go-landlock's best-effort handles this.
- `no_new_privs` (required by Landlock) disables setuid binaries — `sudo`
  stops working inside a sandboxed run. Build-time deps and `pre_run` hooks
  are unaffected (they run before the sandbox is applied).
- Under gVisor the guest kernel does not provide Landlock; the container
  boundary + gVisor is already the stronger wall there. The kernel sandbox
  matters most with `--no-sandbox`/`sandbox: none` and on Apple containers.
- Already-open file descriptors (stdio, the TTY) are not affected — expected
  and desirable.

## Testing

- Unit: config validation (both directions per invariant #1), policy
  construction (defaults, rw-mount inclusion **and** ro-mount exclusion,
  normalization, JSON round-trip), Dockerfile generation with/without the
  flag, image-tag divergence, stub detection + checksums.
- Linux-only in-process: subprocess test applies a policy and asserts allowed
  writes succeed, denied writes fail, reads still work (skips when Landlock
  is unavailable).
- E2E (Docker): real `moat run` with `kernel_sandbox: true` asserting
  workspace writes succeed, `/etc` writes fail, and the status line appears;
  companion run without the flag asserts no restriction and no status line.

## Follow-ups (tracked in #396)

- macOS Seatbelt profile for Apple containers' agent process.
- Containerless `isolation.mode: local`.
- `deny_paths` (needs a masking mechanism — tmpfs overlay mounts — or future
  kernel support).
- Landlock TCP rules scoped to the proxy port (needs answers to the
  proxy-interaction open questions in #396).
- Surfacing kernel denials into the audit store.
