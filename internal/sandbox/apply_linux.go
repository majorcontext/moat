//go:build linux

package sandbox

import (
	"fmt"
	"os"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

// Status reports what enforcement was actually achieved.
type Status struct {
	// ABI is the Landlock ABI version supported by the running kernel.
	// 0 means Landlock is unavailable (kernel < 5.13, or the landlock
	// syscalls are blocked by seccomp — e.g. Docker < 23) and nothing was
	// enforced.
	ABI int
}

// Apply enforces the policy on the calling process via Landlock. The
// restriction is inherited by all children and cannot be lifted.
//
// If Landlock is unavailable, Apply returns Status{ABI: 0} with a nil error
// and enforces nothing — the caller decides how loudly to warn. This probe
// exists because go-landlock's best-effort mode also succeeds silently on
// kernels without Landlock, which would hide the degradation entirely.
//
// The agent's home directory ($HOME, set by the entrypoint's privilege drop)
// is added to the writable set: agents write config, caches, and logs there.
func Apply(p Policy) (Status, error) {
	abi, err := llsys.LandlockGetABIVersion()
	if err != nil || abi < 1 {
		return Status{ABI: 0}, nil
	}

	writable := p.AllowWrite
	if home := os.Getenv("HOME"); home != "" {
		writable = append(writable, home)
	}

	rules := []landlock.Rule{
		// Read everywhere: domain-level secrets policy stays with the
		// credential proxy; the kernel wall is about writes.
		landlock.RODirs("/"),
	}
	for _, dir := range writable {
		rule := landlock.RWDirs(dir).
			// Renames/links across directory boundaries (git gc, package
			// managers moving staged trees) need the v2 "refer" right.
			WithRefer().
			// Slim images may lack /var/tmp etc.; a missing path must not
			// abort the whole restriction.
			IgnoreIfMissing()
		if dir == "/dev" {
			// IOCTLs on TTYs and /dev/null are grouped under a dedicated
			// right from ABI v5; without it terminal handling breaks.
			rule = rule.WithIoctlDev()
		}
		rules = append(rules, rule)
	}

	// V5 is the newest ABI whose filesystem rights we use; BestEffort
	// downgrades (dropping refer/truncate/ioctl grouping as needed) on older
	// kernels. RestrictPaths only handles filesystem access — TCP restriction
	// (v4) is deliberately left to the credential proxy in this first cut.
	if err := landlock.V5.BestEffort().RestrictPaths(rules...); err != nil {
		return Status{ABI: abi}, fmt.Errorf("applying landlock policy: %w", err)
	}
	return Status{ABI: abi}, nil
}
