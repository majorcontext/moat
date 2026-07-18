package moatinit

import "testing"

func TestDockerMutexViolated(t *testing.T) {
	tests := []struct {
		dind, gid string
		want      bool
	}{
		// DOCKER-01: any non-empty pair violates — the guard tests
		// emptiness, not "1".
		{"1", "999", true},
		{"0", "999", true},
		{"true", "x", true},
		// Companions: one or neither set never violates.
		{"1", "", false},
		{"", "999", false},
		{"", "", false},
	}
	for _, tt := range tests {
		if got := dockerMutexViolated(tt.dind, tt.gid); got != tt.want {
			t.Errorf("dockerMutexViolated(%q, %q) = %v, want %v", tt.dind, tt.gid, got, tt.want)
		}
	}
}

// TestDindActive covers the DOCKER-02 matrix {1,0,true,”}×{root,non-root}.
func TestDindActive(t *testing.T) {
	tests := []struct {
		dind string
		euid int
		want bool
	}{
		{"1", 0, true},
		// Non-root with DIND=1 silently skips (no dockerd, no error).
		{"1", 1000, false},
		{"0", 0, false},
		{"true", 0, false},
		{"", 0, false},
		{"", 1000, false},
	}
	for _, tt := range tests {
		if got := dindActive(tt.dind, tt.euid); got != tt.want {
			t.Errorf("dindActive(%q, %d) = %v, want %v", tt.dind, tt.euid, got, tt.want)
		}
	}
}

// TestHostGIDActive covers the DOCKER-09 matrix
// {gid set/unset}×{root/non-root}×{socket present/absent}.
func TestHostGIDActive(t *testing.T) {
	tests := []struct {
		gid    string
		euid   int
		socket bool
		want   bool
	}{
		{"999", 0, true, true},
		// Any value activates, not just numeric — the guard tests emptiness.
		{"docker", 0, true, true},
		{"999", 0, false, false},
		{"999", 1000, true, false},
		{"", 0, true, false},
		{"", 1000, false, false},
	}
	for _, tt := range tests {
		if got := hostGIDActive(tt.gid, tt.euid, tt.socket); got != tt.want {
			t.Errorf("hostGIDActive(%q, %d, %v) = %v, want %v", tt.gid, tt.euid, tt.socket, got, tt.want)
		}
	}
}
