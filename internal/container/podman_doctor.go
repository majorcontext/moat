package container

import "os"

// PodmanSocketPaths returns the paths of podman Docker-API-compatible sockets
// (see podmanSocketCandidates) that currently exist on disk. It is a
// side-effect-free wrapper for callers outside this package — notably `moat
// doctor` — that want to surface "a podman socket is right there" without
// dialing it or setting DOCKER_HOST. Only stats the filesystem; never probes
// the socket itself.
func PodmanSocketPaths() []string {
	var paths []string
	for _, c := range podmanSocketCandidates() {
		if _, err := os.Stat(c.path); err == nil {
			paths = append(paths, c.path)
		}
	}
	return paths
}
