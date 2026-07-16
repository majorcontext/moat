package moatinit

// dockerMutexViolated mirrors DOCKER-01: MOAT_DOCKER_DIND and MOAT_DOCKER_GID
// are mutually exclusive whenever BOTH are non-empty (any values — the guard
// tests emptiness, not "1").
func dockerMutexViolated(dind, gid string) bool {
	return dind != "" && gid != ""
}

// dindActive mirrors DOCKER-02: dind mode activates only when
// MOAT_DOCKER_DIND is exactly "1" AND the process is root. A non-root
// process with DIND=1 silently skips dind setup (no dockerd, no error).
func dindActive(dind string, euid int) bool {
	return dind == "1" && euid == 0
}

// hostGIDActive mirrors DOCKER-09: host-socket mode activates only when
// MOAT_DOCKER_GID is non-empty (any value) AND the process is root AND
// /var/run/docker.sock is a socket. Any missing condition silently skips
// host-mode setup.
func hostGIDActive(gid string, euid int, socketPresent bool) bool {
	return gid != "" && euid == 0 && socketPresent
}
