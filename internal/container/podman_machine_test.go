package container

import (
	"os"
	"path/filepath"
	"testing"
)

// writePodmanConnections points the connections seam at a scratch file holding
// def as the active connection. An empty def writes a file with no default.
func writePodmanConnections(t *testing.T, def string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "podman-connections.json")
	body := `{"Connection":{"Default":"` + def + `","Connections":{}},"Farm":{}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write connections file: %v", err)
	}
	prev := podmanConnectionsPath
	podmanConnectionsPath = func() string { return path }
	t.Cleanup(func() { podmanConnectionsPath = prev })
}

func machineCandidates(names ...string) []dockerSocketCandidate {
	cands := make([]dockerSocketCandidate, 0, len(names))
	for _, n := range names {
		cands = append(cands, dockerSocketCandidate{"/tmp/podman/" + n + "-api.sock", "Podman machine " + n})
	}
	return cands
}

func firstMachine(t *testing.T, cands []dockerSocketCandidate) string {
	t.Helper()
	if len(cands) == 0 {
		t.Fatal("no candidates")
	}
	return podmanMachineName(cands[0].path)
}

// TestPreferDefaultPodmanMachine pins the fix for two running machines: the
// alphabetically-first socket must not win over podman's actual default.
func TestPreferDefaultPodmanMachine(t *testing.T) {
	t.Setenv("CONTAINER_CONNECTION", "")
	writePodmanConnections(t, "podman-machine-default")

	// "dev" sorts before "podman-machine-default" and would win on Glob order.
	got := preferDefaultPodmanMachine(machineCandidates("dev", "podman-machine-default"))
	if name := firstMachine(t, got); name != "podman-machine-default" {
		t.Errorf("default connection should be probed first, got %q", name)
	}
	if len(got) != 2 {
		t.Errorf("no candidate should be dropped, got %d", len(got))
	}
	// The non-default machine must still be reachable as a fallback.
	if name := podmanMachineName(got[1].path); name != "dev" {
		t.Errorf("remaining candidate should be preserved, got %q", name)
	}
}

// TestPreferDefaultPodmanMachineContainerConnectionWins is the companion:
// podman's own CONTAINER_CONNECTION override beats the stored default, so moat
// follows the same machine podman would.
func TestPreferDefaultPodmanMachineContainerConnectionWins(t *testing.T) {
	writePodmanConnections(t, "podman-machine-default")
	t.Setenv("CONTAINER_CONNECTION", "dev")

	got := preferDefaultPodmanMachine(machineCandidates("dev", "podman-machine-default"))
	if name := firstMachine(t, got); name != "dev" {
		t.Errorf("CONTAINER_CONNECTION should win, got %q", name)
	}
}

// TestPreferDefaultPodmanMachineNoDefault covers the cases where there is
// nothing to prefer — order must be left exactly as found rather than
// reshuffled arbitrarily.
func TestPreferDefaultPodmanMachineNoDefault(t *testing.T) {
	t.Setenv("CONTAINER_CONNECTION", "")

	t.Run("no connections file", func(t *testing.T) {
		prev := podmanConnectionsPath
		podmanConnectionsPath = func() string { return filepath.Join(t.TempDir(), "absent.json") }
		t.Cleanup(func() { podmanConnectionsPath = prev })

		got := preferDefaultPodmanMachine(machineCandidates("dev", "prod"))
		if name := firstMachine(t, got); name != "dev" {
			t.Errorf("order should be unchanged, got %q", name)
		}
	})

	t.Run("default names an absent machine", func(t *testing.T) {
		writePodmanConnections(t, "not-running")
		got := preferDefaultPodmanMachine(machineCandidates("dev", "prod"))
		if name := firstMachine(t, got); name != "dev" {
			t.Errorf("order should be unchanged when the default isn't present, got %q", name)
		}
		if len(got) != 2 {
			t.Errorf("no candidate should be dropped, got %d", len(got))
		}
	})

	t.Run("malformed connections file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "podman-connections.json")
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		prev := podmanConnectionsPath
		podmanConnectionsPath = func() string { return path }
		t.Cleanup(func() { podmanConnectionsPath = prev })

		got := preferDefaultPodmanMachine(machineCandidates("dev", "prod"))
		if name := firstMachine(t, got); name != "dev" {
			t.Errorf("malformed file should be ignored, got %q", name)
		}
	})

	t.Run("single candidate", func(t *testing.T) {
		writePodmanConnections(t, "podman-machine-default")
		got := preferDefaultPodmanMachine(machineCandidates("dev"))
		if len(got) != 1 || firstMachine(t, got) != "dev" {
			t.Errorf("single candidate should pass through untouched, got %v", got)
		}
	})
}

func TestPodmanMachineName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/tmp/podman/podman-machine-default-api.sock", "podman-machine-default"},
		{"/tmp/podman/dev-api.sock", "dev"},
		{"/tmp/podman/podman.sock", ""},
		{"/var/run/docker.sock", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := podmanMachineName(tt.path); got != tt.want {
			t.Errorf("podmanMachineName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
