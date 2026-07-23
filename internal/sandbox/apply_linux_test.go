//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

// TestApplyEnforcement verifies real Landlock enforcement in a sacrificial
// subprocess (the restriction is irreversible, so it must not be applied to
// the test process itself): writes inside the allowlist succeed, writes
// outside fail, and reads outside stay allowed.
func TestApplyEnforcement(t *testing.T) {
	if abi, err := llsys.LandlockGetABIVersion(); err != nil || abi < 1 {
		t.Skip("Landlock not available on this kernel; skipping enforcement test")
	}

	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	denied := filepath.Join(base, "denied")
	for _, d := range []string{allowed, denied} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestApplyHelperProcess", "-test.v")
	cmd.Env = append(os.Environ(),
		"GO_SANDBOX_HELPER=1",
		"SANDBOX_TEST_ALLOWED="+allowed,
		"SANDBOX_TEST_DENIED="+denied,
		// Apply adds $HOME to the writable set; clear it so the assertions
		// below only reflect the explicit policy.
		"HOME=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper process failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"RESULT allowed-write=ok",
		"RESULT denied-write=denied",
		"RESULT outside-read=ok",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("helper output missing %q:\n%s", want, out)
		}
	}
}

// TestApplyHelperProcess is the sacrificial subprocess for
// TestApplyEnforcement; it self-restricts and reports what the kernel
// actually enforces. It only runs when re-exec'd with GO_SANDBOX_HELPER=1.
func TestApplyHelperProcess(t *testing.T) {
	if os.Getenv("GO_SANDBOX_HELPER") != "1" {
		t.Skip("helper process for TestApplyEnforcement")
	}
	allowed := os.Getenv("SANDBOX_TEST_ALLOWED")
	denied := os.Getenv("SANDBOX_TEST_DENIED")

	status, err := Apply(Policy{AllowWrite: []string{allowed}})
	if err != nil {
		fmt.Printf("RESULT apply-error=%v\n", err)
		os.Exit(1)
	}
	if status.ABI < 1 {
		fmt.Println("RESULT apply-error=landlock unexpectedly unavailable")
		os.Exit(1)
	}

	if err := os.WriteFile(filepath.Join(allowed, "f"), []byte("x"), 0o644); err == nil {
		fmt.Println("RESULT allowed-write=ok")
	} else {
		fmt.Printf("RESULT allowed-write=denied (%v)\n", err)
	}
	if err := os.WriteFile(filepath.Join(denied, "f"), []byte("x"), 0o644); err != nil {
		fmt.Println("RESULT denied-write=denied")
	} else {
		fmt.Println("RESULT denied-write=ok")
	}
	if _, err := os.ReadFile("/etc/hostname"); err == nil {
		fmt.Println("RESULT outside-read=ok")
	} else {
		fmt.Printf("RESULT outside-read=denied (%v)\n", err)
	}

	// Exit before test-framework teardown: the restricted process may not be
	// able to write wherever `go test` wants to (profiles, temp files).
	os.Exit(0)
}

func TestApplyUnavailableKernelReportsZeroABI(t *testing.T) {
	// Companion contract check: Status.ABI == 0 means "nothing enforced" and
	// Apply must not error in that case. We can't force unavailability here,
	// so this pins the available-kernel side: ABI is probed, not hardcoded.
	abi, err := llsys.LandlockGetABIVersion()
	if err != nil || abi < 1 {
		st, applyErr := Apply(Policy{})
		if applyErr != nil {
			t.Errorf("Apply on non-Landlock kernel: %v, want nil (warn-and-degrade contract)", applyErr)
		}
		if st.ABI != 0 {
			t.Errorf("Status.ABI = %d on non-Landlock kernel, want 0", st.ABI)
		}
		return
	}
	t.Skip("Landlock available; unavailability path exercised only on pre-5.13 kernels")
}
