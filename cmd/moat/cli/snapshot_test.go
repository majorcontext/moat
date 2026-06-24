package cli

import "testing"

func TestCheckRestoreAllowed(t *testing.T) {
	// volume mode + no --to → blocked
	if err := checkRestoreAllowed("volume", ""); err == nil {
		t.Error("volume mode in-place restore should be blocked")
	}
	// volume mode + --to → allowed
	if err := checkRestoreAllowed("volume", "/tmp/out"); err != nil {
		t.Errorf("volume mode restore --to should be allowed: %v", err)
	}
	// bind mode in-place → allowed (unchanged)
	if err := checkRestoreAllowed("bind", ""); err != nil {
		t.Errorf("bind mode in-place restore should be allowed: %v", err)
	}
	// empty mode (legacy/bind default) in-place → allowed
	if err := checkRestoreAllowed("", ""); err != nil {
		t.Errorf("empty mode in-place restore should be allowed: %v", err)
	}
}
