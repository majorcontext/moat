package container

import "os"

// PodmanSocketPaths returns the podman sockets that currently exist on disk,
// for callers such as `moat doctor` that want to surface an idle socket. Only
// stats the filesystem — never dials the socket or sets DOCKER_HOST.
func PodmanSocketPaths() []string {
	var paths []string
	for _, c := range podmanSocketCandidates() {
		if _, err := os.Stat(c.path); err == nil {
			paths = append(paths, c.path)
		}
	}
	return paths
}
