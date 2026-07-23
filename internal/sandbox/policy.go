// Package sandbox implements the kernel sandbox (Landlock) policy applied to
// the agent process inside a Moat container (issue #396, in-container mode).
//
// The policy is computed on the host by the run manager (BuildPolicy),
// serialized as JSON into the MOAT_SANDBOX_POLICY environment variable, and
// applied in-container by the moat-sandbox helper binary (cmd/moat-sandbox)
// as the final step of the entrypoint exec chain. Landlock restrictions are
// inherited by every child process and cannot be lifted once applied.
//
// The posture is "read everywhere, write only where allowed": the whole
// container filesystem stays readable, and writes are limited to the
// workspace, the agent's home, scratch/system paths, read-write mounts, and
// any isolation.sandbox.allow_write entries from moat.yaml. Landlock is
// allowlist-only, so deny-style rules inside an allowed tree are not
// expressible; see docs/plans/2026-07-23-kernel-sandbox-design.md.
package sandbox

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
)

// PolicyEnv is the environment variable carrying the JSON-encoded Policy
// from the run manager to the in-container moat-sandbox helper. The helper
// scrubs it from the environment before exec'ing the agent.
const PolicyEnv = "MOAT_SANDBOX_POLICY"

// HelperPath is where the moat-sandbox helper binary is installed inside
// run images.
const HelperPath = "/usr/local/bin/moat-sandbox"

// defaultWritePaths are container paths writable under every kernel-sandbox
// policy, in addition to the workspace, mounts, and user-configured paths.
//
//   - /tmp, /var/tmp: scratch space.
//   - /dev: TTY handling (/dev/tty, /dev/ptmx, /dev/shm); device IOCTLs are
//     granted separately (see apply_linux.go).
//   - /proc: shells write through /dev/stdout -> /proc/self/fd/1; sensitive
//     areas (/proc/sys and friends) are already masked read-only by the
//     container runtime.
//   - /run: connecting to a unix socket requires write access to its path
//     (e.g. the SSH agent bridge at /run/moat/ssh).
//
// The agent's home directory is intentionally absent: it is only known
// in-container (gosu sets $HOME during the privilege drop), so the helper
// adds it at apply time.
var defaultWritePaths = []string{"/tmp", "/var/tmp", "/dev", "/proc", "/run"}

// Policy describes the filesystem restrictions applied to the agent process.
// Reads are always allowed everywhere; AllowWrite lists the directory trees
// that stay writable.
type Policy struct {
	AllowWrite []string `json:"allow_write"`
}

// BuildPolicy computes the in-container policy: the built-in defaults, every
// read-write mount target (the workspace — bind or named volume — always
// arrives as one), and the user-configured allow_write entries, cleaned,
// de-duplicated, and sorted for determinism. All paths are container paths
// and must be absolute. A read-only workspace mount is deliberately not
// added: the mount layer already denies writes there.
func BuildPolicy(rwMountTargets, allowWrite []string) Policy {
	seen := make(map[string]bool)
	var paths []string
	add := func(p string) {
		if p == "" {
			return
		}
		p = path.Clean(p)
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, p := range defaultWritePaths {
		add(p)
	}
	for _, p := range rwMountTargets {
		add(p)
	}
	for _, p := range allowWrite {
		add(p)
	}
	sort.Strings(paths)
	return Policy{AllowWrite: paths}
}

// Encode serializes the policy for transport in PolicyEnv.
func (p Policy) Encode() (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encoding sandbox policy: %w", err)
	}
	return string(data), nil
}

// ParsePolicy decodes a policy previously produced by Encode.
func ParsePolicy(s string) (Policy, error) {
	var p Policy
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return Policy{}, fmt.Errorf("parsing %s: %w", PolicyEnv, err)
	}
	return p, nil
}
