//go:build linux

// Command moat-sandbox applies the Moat kernel sandbox (Landlock) to itself
// and then execs the agent command, which inherits the restriction along
// with every process it spawns. It is installed into run images at
// /usr/local/bin/moat-sandbox and invoked by the moat-init entrypoint as the
// last link of the exec chain (after the privilege drop), so the restriction
// covers exactly the agent process tree.
//
// The policy arrives JSON-encoded in MOAT_SANDBOX_POLICY (see
// internal/sandbox), which is scrubbed from the environment before exec.
//
// Restricting and exec'ing from a single goroutine sidesteps go-landlock's
// multi-thread caveat: execve replaces the whole process, and the new
// program inherits the Landlock domain of the exec'ing thread.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/majorcontext/moat/internal/sandbox"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "moat: moat-sandbox: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	if len(args) == 0 {
		return fmt.Errorf("usage: moat-sandbox <command> [args...]")
	}

	// Fail closed: this binary is only ever invoked when a kernel sandbox
	// was requested. Exec'ing the agent unrestricted because the policy went
	// missing would silently void the guarantee.
	policyStr := os.Getenv(sandbox.PolicyEnv)
	if policyStr == "" {
		return fmt.Errorf("%s is not set; refusing to run the command unsandboxed", sandbox.PolicyEnv)
	}
	policy, err := sandbox.ParsePolicy(policyStr)
	if err != nil {
		return err
	}
	os.Unsetenv(sandbox.PolicyEnv)

	status, err := sandbox.Apply(policy)
	if err != nil {
		// Landlock is available but enforcement failed — fail closed rather
		// than degrade an explicitly requested security boundary.
		return err
	}
	if status.ABI == 0 {
		fmt.Fprintln(os.Stderr, "moat: kernel sandbox requested but Landlock is unavailable "+
			"(requires Linux 5.13+ with the landlock syscalls allowed; gVisor and Docker <23 do not support it); "+
			"continuing WITHOUT the kernel sandbox")
	} else {
		fmt.Fprintf(os.Stderr, "moat: kernel sandbox active (Landlock ABI v%d, filesystem write allowlist)\n", status.ABI)
	}

	path, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("finding %q: %w", args[0], err)
	}
	return syscall.Exec(path, args, os.Environ())
}
