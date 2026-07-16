// Command gate is the release pipeline's ship-refusal + positive functional
// gate for the embedded moat-init binaries (plan §5). Run AFTER `go generate
// ./internal/initbin` (go run recompiles, so the embedded bytes reflect the
// current embed/ files):
//
//  1. Ship refusal: refuse if either embedded blob is still the committed
//     fail-closed stub — a release must never ship a stub as a container
//     entrypoint candidate.
//  2. Checksum: the embedded bytes must match checksums.txt (catches a
//     stale or hand-edited blob).
//  3. Functional: on a linux host, exec the host-arch binary with --plan
//     against a fixed environment and require the privilege-drop and
//     MOAT_INIT_FILES-scrub lines. Checksum matching alone cannot catch a
//     regenerated-but-defective blob (wrong commit, truncated output) whose
//     checksums.txt was regenerated alongside it — execing the binary can.
//
// Wired into the goreleaser before hooks; exits non-zero with a diagnostic
// on any gate failure.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/majorcontext/moat/internal/initbin"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "initbin gate: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	blobs := map[string][]byte{
		"moat-init-linux-amd64": initbin.BinaryFor("amd64"),
		"moat-init-linux-arm64": initbin.BinaryFor("arm64"),
	}

	// Gate 1: ship refusal.
	for name, b := range blobs {
		if len(b) == 0 {
			fail("%s: no embedded bytes", name)
		}
		if initbin.IsStub(b) {
			fail("%s is the committed stub — run 'go generate ./internal/initbin' before releasing", name)
		}
	}

	// Gate 2: checksums.
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(initbin.Checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			fail("malformed checksums.txt line: %q", line)
		}
		want[fields[1]] = fields[0]
	}
	for name, b := range blobs {
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want[name] {
			fail("%s does not match checksums.txt — stale or hand-edited blob", name)
		}
	}

	// Gate 3: functional (linux hosts only — the blobs are linux
	// executables; macOS release builds rely on gates 1-2 plus CI running
	// this gate on a linux runner).
	if runtime.GOOS == "linux" {
		bin := initbin.Binary()
		if bin == nil {
			fail("no embedded binary for host arch %s", runtime.GOARCH)
		}
		tmp, err := os.MkdirTemp("", "moat-init-gate")
		if err != nil {
			fail("temp dir: %v", err)
		}
		defer os.RemoveAll(tmp)
		path := filepath.Join(tmp, "moat-init")
		if werr := os.WriteFile(path, bin, 0o755); werr != nil {
			fail("writing binary: %v", werr)
		}
		cmd := exec.Command(path, "--plan", "echo", "gate")
		cmd.Env = []string{"HOME=/tmp", "PATH=/usr/bin:/bin", "MOAT_INIT_FILES=/tmp/gate\tYWJj"}
		out, err := cmd.Output()
		if err != nil {
			fail("--plan run failed: %v (a defective blob cannot serve as PID 1)", err)
		}
		plan := string(out)
		for _, wantLine := range []string{"privilege drop:", "scrub MOAT_INIT_FILES"} {
			if !strings.Contains(plan, wantLine) {
				fail("--plan output missing %q — the binary lost a load-bearing phase:\n%s", wantLine, plan)
			}
		}
	}

	fmt.Println("initbin gate: ok")
}
