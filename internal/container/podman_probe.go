package container

import (
	"context"
	"net/url"
	"os"
	"time"

	"github.com/docker/docker/client"
)

// probeOverallDeadline bounds the total cost of
// ReachablePodmanEndpointOtherThan. It runs on Manager.Stop's not-found
// path, so it must not turn an already-slow "container is gone" case into a
// long hang while it dials sockets nobody answers on.
const probeOverallDeadline = 3 * time.Second

// probeCandidateDeadline bounds a single candidate's ping-and-identify, so
// one wedged socket (accepting the connection but never responding) can't
// eat the whole overall deadline and starve the remaining candidates.
const probeCandidateDeadline = 1 * time.Second

// ReachablePodmanEndpointOtherThan reports whether a live podman endpoint,
// distinct from endpoint, is reachable on this host. It exists to tell a
// genuinely ambiguous not-found — a second, different engine that really
// could be holding the container — from the common case where podman is
// either absent or is the only engine present (i.e. its socket resolves to
// endpoint, the very one the caller already queried).
//
// Walks podmanSocketCandidates(), skipping anything that doesn't stat as a
// socket and anything that names the same socket as endpoint by filesystem
// identity (see sameUnixEndpoint) — not just by matching string, since e.g.
// a podman-docker host's /run/docker.sock is a symlink to
// /run/podman/podman.sock, and the two would otherwise look like distinct,
// genuinely ambiguous engines. For the rest, it constructs a bare Docker API
// client pinned to the candidate host — never through anything that reads or
// mutates the process-wide DOCKER_HOST, since this runs mid-Stop on an
// unrelated run and must not have side effects on it — pings it, and
// confirms it identifies as podman via versionIsPodman. Returns the first
// endpoint that passes both checks. Every constructed client is closed
// before returning, on every path.
//
// ctx supplies cancellation, and also bounds the deadline: WithTimeout below
// takes the earlier of ctx's own deadline and now+probeOverallDeadline, so a
// caller already close to its own deadline shortens the probe rather than
// extending it — context.WithTimeout can only tighten a parent deadline,
// never loosen one. In the unlikely case that leaves too little time to
// finish probing, the probe returns false (no other engine found), and the
// ambiguous not-found is downgraded to a warning rather than a hard error —
// the same fate as the "podman genuinely absent" case, so it's a graceful
// degradation, not a correctness break, but it does mean a very tight caller
// deadline can cause a genuinely-orphaned container to be missed rather than
// reported. It may only slow (never fail on its own) an already-in-flight
// Stop.
func ReachablePodmanEndpointOtherThan(ctx context.Context, endpoint string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, probeOverallDeadline)
	defer cancel()

	for _, c := range podmanSocketCandidates() {
		// Use os.Stat (not Lstat) to follow symlinks, matching the socket
		// checks elsewhere in this package.
		info, err := os.Stat(c.path)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}

		host := "unix://" + c.path
		if sameUnixEndpoint(host, endpoint, info) {
			continue
		}

		if isReachablePodman(ctx, host) {
			return host, true
		}
	}
	return "", false
}

// sameUnixEndpoint reports whether host and endpoint name the same unix
// socket. The string comparison is checked first as a cheap, always-correct
// short-circuit; when it doesn't match, endpoint is parsed as a URL and, if
// it has the unix:// scheme, compared against candidateInfo (host's
// already-stat'd os.FileInfo, from the ModeSocket check the caller already
// did) by filesystem identity rather than by path string — so
// /run/docker.sock and /run/podman/podman.sock are recognized as the same
// engine when the former is a symlink to the latter (the podman-docker
// package's layout), and likewise when /var/run is itself a symlink to
// /run. If endpoint isn't a unix:// URL (tcp://, npipe://, empty, or
// unparseable) or os.Stat on its path fails, the two are never treated as
// the same: a stat error here must not cause a candidate to be silently
// skipped, or a genuine orphan could go unreported.
func sameUnixEndpoint(host, endpoint string, candidateInfo os.FileInfo) bool {
	if host == endpoint {
		return true
	}

	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "unix" || u.Path == "" {
		return false
	}

	endpointInfo, statErr := os.Stat(u.Path)
	if statErr != nil {
		return false
	}

	return os.SameFile(candidateInfo, endpointInfo)
}

// isReachablePodman pings host and confirms it identifies as podman, under
// its own sub-timeout derived from ctx so a single wedged candidate can't
// consume the rest of ctx's overall deadline.
//
// This is purely an identity check — Ping plus ServerVersion — so it builds
// a bare Docker API client directly rather than a full DockerRuntime: no OCI
// runtime selection, no network/sidecar/build managers, and critically, none
// of newDockerRuntimeFromClient's sandbox=false handling, which on Linux
// unconditionally prints "Running without gVisor sandbox" — a false alarm
// here, since this probe creates and runs no container at all. Always
// closes the client it constructs, including on every failure path.
func isReachablePodman(ctx context.Context, host string) bool {
	// FromEnv must precede WithHost: opts apply in order, and FromEnv
	// supplies TLS config (DOCKER_TLS_VERIFY/DOCKER_CERT_PATH) for a secured
	// tcp:// endpoint, while the later WithHost always wins on the host
	// field — see NewDockerRuntimeWithHost's comment in docker.go, which
	// this mirrors.
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithHost(host), client.WithAPIVersionNegotiation())
	if err != nil {
		return false
	}
	defer cli.Close()

	candCtx, cancel := context.WithTimeout(ctx, probeCandidateDeadline)
	defer cancel()

	if _, pingErr := cli.Ping(candCtx); pingErr != nil {
		return false
	}
	version, err := cli.ServerVersion(candCtx)
	return err == nil && versionIsPodman(version)
}
