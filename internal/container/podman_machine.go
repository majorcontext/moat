package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// podmanConnectionsFile is the subset of podman's connections file we read.
// Podman 5+ stores it as JSON at $XDG_CONFIG_HOME/containers/podman-connections.json,
// with Connection.Default naming the active connection.
type podmanConnectionsFile struct {
	Connection struct {
		Default string `json:"Default"`
	} `json:"Connection"`
}

// podmanDefaultConnection returns the name of podman's active connection, or ""
// when it can't be determined. CONTAINER_CONNECTION is podman's own per-command
// override and wins over the stored default, matching what `podman` itself
// would talk to.
func podmanDefaultConnection() string {
	if name := os.Getenv("CONTAINER_CONNECTION"); name != "" {
		return name
	}
	path := detectEnv.connectionsPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cf podmanConnectionsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return ""
	}
	return cf.Connection.Default
}

// preferDefaultPodmanMachine reorders machine sockets so the one belonging to
// podman's active connection is probed first. A podman machine's API socket is
// named <connection>-api.sock, and with several machines running the candidate
// list is otherwise sorted by filename — so `dev` would beat
// `podman-machine-default` purely on alphabetical order, and IsPodmanEngine
// can't tell them apart because both are genuinely podman.
//
// Order is otherwise preserved, and an unmatched default changes nothing.
func preferDefaultPodmanMachine(candidates []dockerSocketCandidate) []dockerSocketCandidate {
	name := podmanDefaultConnection()
	if name == "" || len(candidates) < 2 {
		return candidates
	}
	want := name + "-api.sock"
	for i, c := range candidates {
		if filepath.Base(c.path) != want {
			continue
		}
		reordered := make([]dockerSocketCandidate, 0, len(candidates))
		reordered = append(reordered, c)
		reordered = append(reordered, candidates[:i]...)
		reordered = append(reordered, candidates[i+1:]...)
		return reordered
	}
	return candidates
}

// podmanMachineName recovers the machine name from an API socket path, for
// display. Returns "" when path isn't a machine socket.
func podmanMachineName(path string) string {
	base := filepath.Base(path)
	if !strings.HasSuffix(base, "-api.sock") {
		return ""
	}
	return strings.TrimSuffix(base, "-api.sock")
}
