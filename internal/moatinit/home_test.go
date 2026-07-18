package moatinit

import "testing"

// TestTargetHome covers the X-TARGETHOME-IDIOM matrix over
// {root, non-root} × {moatuser present, absent}.
func TestTargetHome(t *testing.T) {
	tests := []struct {
		name     string
		euid     int
		moatuser bool
		home     string
		want     string
	}{
		// Root + moatuser: the hardcoded literal, NOT moatuser's passwd home.
		{"root with moatuser", 0, true, "/root", "/home/moatuser"},
		// Root without moatuser: falls through to $HOME (files stage there;
		// the exec dispatch later fails closed).
		{"root without moatuser", 0, false, "/root", "/root"},
		{"non-root with moatuser", 1000, true, "/tmp/h", "/tmp/h"},
		{"non-root without moatuser", 1000, false, "/tmp/h", "/tmp/h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := targetHome(tt.euid, tt.moatuser, tt.home); got != tt.want {
				t.Errorf("targetHome(%d, %v, %q) = %q, want %q", tt.euid, tt.moatuser, tt.home, got, tt.want)
			}
			// The chown predicate mirrors the same branch: fixups run only
			// on the root+moatuser path.
			wantChown := tt.euid == 0 && tt.moatuser
			if got := chownToMoatuser(tt.euid, tt.moatuser); got != wantChown {
				t.Errorf("chownToMoatuser(%d, %v) = %v, want %v", tt.euid, tt.moatuser, got, wantChown)
			}
		})
	}
}

// TestInitFilesOwnership covers the INIT-02 four-branch table.
func TestInitFilesOwnership(t *testing.T) {
	tests := []struct {
		name         string
		euid         int
		moatuser     bool
		home         string
		wantChown    bool
		wantInitHome string
	}{
		{"root with moatuser", 0, true, "/root", true, "/home/moatuser"},
		{"root without moatuser", 0, false, "/root", false, "/root"},
		{"non-root with moatuser", 1000, true, "/tmp/h", false, "/tmp/h"},
		{"non-root without moatuser", 1000, false, "/tmp/h", false, "/tmp/h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chown, initHome := initFilesOwnership(tt.euid, tt.moatuser, tt.home)
			if chown != tt.wantChown || initHome != tt.wantInitHome {
				t.Errorf("initFilesOwnership(%d, %v, %q) = (%v, %q), want (%v, %q)",
					tt.euid, tt.moatuser, tt.home, chown, initHome, tt.wantChown, tt.wantInitHome)
			}
		})
	}
}
