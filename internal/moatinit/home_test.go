package moatinit

import (
	"os"
	"path/filepath"
	"testing"
)

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

// TestResolveHome covers the HOME repair: only a non-root entrypoint whose
// HOME is Docker's no-passwd-entry default "/" is redirected, and only when
// the image has /home/moatuser. A HOME from the UID's own passwd entry (node
// images: UID 1000 is node, HOME=/home/node) must be left alone.
func TestResolveHome(t *testing.T) {
	tests := []struct {
		name        string
		euid        int
		home        string
		moatuserDir bool
		want        string
	}{
		{"no passwd entry, moatuser home present", 1000, "/", true, "/home/moatuser"},
		{"no passwd entry, no moatuser home", 1000, "/", false, "/"},
		{"passwd entry supplies HOME", 1000, "/home/node", true, "/home/node"},
		{"root keeps HOME", 0, "/", true, "/"},
		{"moatuser keeps its HOME", 5000, "/home/moatuser", true, "/home/moatuser"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestSys(t, tt.euid, true)
			if tt.moatuserDir {
				if err := os.MkdirAll(filepath.Join(ts.Root, "home", "moatuser"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			ts.env["HOME"] = tt.home

			ResolveHome(ts)

			if got := ts.Getenv("HOME"); got != tt.want {
				t.Errorf("HOME = %q, want %q", got, tt.want)
			}
			// LoadConfig must see the repaired value, since staging reads cfg.Home.
			if got := LoadConfig(ts).Home; got != tt.want {
				t.Errorf("LoadConfig().Home = %q, want %q", got, tt.want)
			}
		})
	}
}
