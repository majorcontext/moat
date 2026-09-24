package container

import (
	"context"
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
)

// RuntimePool manages multiple container runtime instances, keyed by RuntimeType.
// It lazily initializes runtimes on first access and provides a default runtime
// for new run creation. Thread-safe for concurrent access.
type RuntimePool struct {
	mu          sync.Mutex
	runtimes    map[RuntimeType]Runtime
	unavailable map[RuntimeType]struct{} // runtimes that failed initialization
	defaultRT   Runtime
	opts        RuntimeOptions
	closed      bool

	// dockerHosts caches Docker runtimes pinned to a specific non-default
	// DOCKER_HOST endpoint (podman or Rancher Desktop sockets), keyed by host.
	// Populated by GetDockerAt.
	dockerHosts map[string]Runtime

	// dockerHostsUnavailable negatively caches hosts that failed construction
	// or ping in GetDockerAt, so a dead endpoint isn't re-pinged on every
	// reconnect. Unlike the unavailable map above (which caches runtime
	// *types* — these don't appear mid-process), a podman machine restarting
	// mid-session is routine, and moat runs as a long-lived daemon, so
	// entries expire after dockerHostNegativeCacheTTL rather than living for
	// the life of the process.
	dockerHostsUnavailable map[string]dockerHostFailure

	// now returns the current time; overridable so tests can exercise TTL
	// expiry in dockerHostsUnavailable without sleeping. An unexported field
	// rather than a package-global clock, since tests here are in-package and
	// can set it directly per-pool. Defaults to time.Now in every
	// constructor.
	now func() time.Time
}

// dockerHostFailure is a cached GetDockerAt failure, timestamped so it can
// expire (see dockerHostNegativeCacheTTL).
type dockerHostFailure struct {
	err error
	at  time.Time
}

// NewRuntimePool creates a pool with the auto-detected default runtime.
// The default runtime is initialized immediately; other runtimes are
// created lazily when first requested via Get().
func NewRuntimePool(opts RuntimeOptions) (*RuntimePool, error) {
	rt, err := NewRuntimeWithOptions(opts)
	if err != nil {
		return nil, err
	}

	pool := &RuntimePool{
		runtimes:  map[RuntimeType]Runtime{rt.Type(): rt},
		defaultRT: rt,
		opts:      opts,
		now:       time.Now,
	}
	return pool, nil
}

// NewRuntimePoolWithDefault creates a pool with a pre-existing runtime as default.
// Used in tests to inject a stub runtime.
func NewRuntimePoolWithDefault(rt Runtime) *RuntimePool {
	return &RuntimePool{
		runtimes:  map[RuntimeType]Runtime{rt.Type(): rt},
		defaultRT: rt,
		now:       time.Now,
	}
}

// NewRuntimePoolWithDockerHost is NewRuntimePoolWithDefault with rt also
// resolvable as the host-pinned runtime for host — GetDockerAt(ctx, host)
// returns the same rt rather than dialing a real engine. Used by
// internal/run's tests so a run carrying a recorded DockerHost resolves to
// the stub.
//
// rt is deliberately the identical object in both p.runtimes and
// p.dockerHosts — a test needs Get(RuntimeDocker) and GetDockerAt(ctx, host)
// to both return the same stub. That double-insertion used to cost two
// things it shouldn't have: Close would close rt twice (fixed below by
// deduping by pointer identity before closing), and ForEachAvailable would
// invoke fn on it twice, because its dedupe only recognized *DockerRuntime
// and this constructor's rt is typically an arbitrary test stub (fixed in
// ForEachAvailable by also comparing against the visited default runtime by
// identity, not just by *DockerRuntime.DaemonHost()).
func NewRuntimePoolWithDockerHost(rt Runtime, host string) *RuntimePool {
	return &RuntimePool{
		runtimes:    map[RuntimeType]Runtime{rt.Type(): rt},
		defaultRT:   rt,
		dockerHosts: map[string]Runtime{host: rt},
		now:         time.Now,
	}
}

// Default returns the auto-detected default runtime.
// Used for creating new runs. Returns an error if the pool has been closed.
func (p *RuntimePool) Default() (Runtime, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, fmt.Errorf("runtime pool is closed")
	}
	return p.defaultRT, nil
}

// Get returns the runtime for the given type, lazily initializing it if needed.
// Returns the default runtime if typ is empty (legacy runs without a runtime field).
func (p *RuntimePool) Get(typ RuntimeType) (Runtime, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, fmt.Errorf("runtime pool is closed")
	}

	if typ == "" {
		return p.defaultRT, nil
	}

	if rt, ok := p.runtimes[typ]; ok {
		return rt, nil
	}

	if _, failed := p.unavailable[typ]; failed {
		return nil, fmt.Errorf("runtime %s not available", typ)
	}

	rt, err := NewRuntimeByType(typ, p.opts)
	if err != nil {
		if p.unavailable == nil {
			p.unavailable = make(map[RuntimeType]struct{})
		}
		p.unavailable[typ] = struct{}{}
		return nil, fmt.Errorf("runtime %s not available: %w", typ, err)
	}

	p.runtimes[typ] = rt
	return rt, nil
}

// dockerAtPingTimeout bounds how long GetDockerAt waits for a pinned endpoint
// to answer, capped further by the caller's ctx.
const dockerAtPingTimeout = 5 * time.Second

// dockerHostNegativeCacheTTL bounds how long a GetDockerAt failure is cached
// in dockerHostsUnavailable before being retried. 30s is short enough that a
// podman machine or Rancher Desktop VM restarting mid-session — routine, and
// something moat's long-lived daemon will observe — recovers on its own
// within a session, while still being long enough to fail fast against a
// genuinely dead endpoint that's polled repeatedly in a tight loop.
const dockerHostNegativeCacheTTL = 30 * time.Second

// GetDockerAt returns a Docker runtime pinned to the given endpoint, lazily
// creating and caching it, without mutating the process-wide DOCKER_HOST. Used
// to reconnect to runs recorded against a podman or Rancher Desktop socket. An
// empty host is equivalent to Get(RuntimeDocker).
//
// Construction and the ping happen outside the pool mutex, so a wedged endpoint
// doesn't block unrelated callers. Failures are negatively cached per host so
// repeat attempts fail fast rather than re-paying the timeout, but the cache
// entry expires after dockerHostNegativeCacheTTL — unlike the unavailable map
// above, whose entries are valid for the process lifetime because a runtime
// *type* doesn't come and go, a specific endpoint can recover mid-process.
func (p *RuntimePool) GetDockerAt(ctx context.Context, host string) (Runtime, error) {
	if host == "" {
		return p.Get(RuntimeDocker)
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("runtime pool is closed")
	}

	// If the process-wide DOCKER_HOST already matches and a default docker
	// runtime is cached, reuse it rather than creating a second client.
	if os.Getenv("DOCKER_HOST") == host {
		if rt, ok := p.runtimes[RuntimeDocker]; ok {
			p.mu.Unlock()
			return rt, nil
		}
	}

	if rt, ok := p.dockerHosts[host]; ok {
		p.mu.Unlock()
		return rt, nil
	}

	if failure, failed := p.dockerHostsUnavailable[host]; failed {
		if p.now().Sub(failure.at) < dockerHostNegativeCacheTTL {
			p.mu.Unlock()
			return nil, failure.err
		}
		// Entry has expired: fall through and retry construction/ping below,
		// same as if nothing had been cached for this host.
	}
	p.mu.Unlock()

	// Construct and ping outside the lock — this is the part that can take
	// up to dockerAtPingTimeout against a wedged endpoint, and must not
	// starve other pool callers.
	dockerRT, err := NewDockerRuntimeWithHost(host, p.opts.Sandbox)
	if err != nil {
		wrapped := fmt.Errorf("docker runtime for host %s: %w%s", host, err, podmanUnreachableHint(host))
		p.cacheDockerHostFailure(host, wrapped)
		return nil, wrapped
	}

	pingCtx, cancel := context.WithTimeout(ctx, dockerAtPingTimeout)
	defer cancel()
	if err := dockerRT.Ping(pingCtx); err != nil {
		dockerRT.Close()
		wrapped := fmt.Errorf("docker host %s not accessible: %w%s", host, err, podmanUnreachableHint(host))
		p.cacheDockerHostFailure(host, wrapped)
		return nil, wrapped
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		dockerRT.Close()
		return nil, fmt.Errorf("runtime pool is closed")
	}

	// Another goroutine may have raced us and already inserted a runtime for
	// this host while we were pinging outside the lock. Prefer theirs and
	// close our duplicate rather than leaking a second client.
	if existing, ok := p.dockerHosts[host]; ok {
		dockerRT.Close()
		return existing, nil
	}

	if p.dockerHosts == nil {
		p.dockerHosts = make(map[string]Runtime)
	}
	p.dockerHosts[host] = dockerRT
	// A later successful connection supersedes any earlier cached failure.
	delete(p.dockerHostsUnavailable, host)
	return dockerRT, nil
}

// cacheDockerHostFailure records a GetDockerAt failure for host, timestamped
// so it expires after dockerHostNegativeCacheTTL, so subsequent calls within
// the TTL fail fast instead of re-attempting construction/ping.
func (p *RuntimePool) cacheDockerHostFailure(host string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dockerHostsUnavailable == nil {
		p.dockerHostsUnavailable = make(map[string]dockerHostFailure)
	}
	p.dockerHostsUnavailable[host] = dockerHostFailure{err: err, at: p.now()}
}

// podmanUnreachableHint returns a recovery hint for GetDockerAt errors when
// host looks like a podman endpoint, and "" otherwise.
func podmanUnreachableHint(host string) string {
	if !strings.Contains(host, "podman") {
		return ""
	}
	hint := "\n\nThis endpoint was recorded in the run's metadata (~/.moat/runs/<id>/metadata.json). To restart podman:\n"
	if goruntime.GOOS == "linux" {
		hint += "  systemctl --user enable --now podman.socket"
	} else {
		hint += "  podman machine start"
	}
	return hint
}

// ForEachAvailable calls fn for each runtime type that initializes, then for
// each host-pinned Docker runtime already sitting in dockerHosts. It does not
// populate dockerHosts itself — that only happens via GetDockerAt — so a
// podman or Rancher Desktop engine is visited here only if something already
// called GetDockerAt for its host before this ran. An engine that's up but
// has zero persisted runs pinned to it (so nothing has ever called
// GetDockerAt for its host) is NOT discovered and will NOT be visited: this
// method has no way to enumerate engines it hasn't been told about.
//
// In practice this works for `moat clean` and `moat status` today because
// NewManagerWithOptions calls loadPersistedRuns — which calls GetDockerAt for
// every run's recorded DockerHost — before either command calls
// ForEachAvailable. That's an ordering contract between internal/run and this
// method that ForEachAvailable itself neither documents in code nor can
// verify; a caller that invoked it before loadPersistedRuns would silently
// see only the default runtime's engine.
//
// A pinned runtime matching the already-visited default runtime — by
// identity, or by DaemonHost() for *DockerRuntime — is skipped. Iteration is
// sequential, so fn may append to external slices unsynchronized.
//
// Note: this lazily initializes runtimes as a side effect. Runtimes
// initialized here will be closed when the pool is closed.
func (p *RuntimePool) ForEachAvailable(fn func(Runtime) error) error {
	var visitedDockerRuntime Runtime
	var visitedDockerEndpoint string
	for _, typ := range AllRuntimeTypes() {
		rt, err := p.Get(typ)
		if err != nil {
			continue // Runtime not available (or pool closed)
		}
		if typ == RuntimeDocker {
			visitedDockerRuntime = rt
			if dr, ok := rt.(*DockerRuntime); ok {
				visitedDockerEndpoint = dr.DaemonHost()
			}
		}
		if err := fn(rt); err != nil {
			return err
		}
	}

	p.mu.Lock()
	hostRuntimes := make([]Runtime, 0, len(p.dockerHosts))
	for _, rt := range p.dockerHosts {
		hostRuntimes = append(hostRuntimes, rt)
	}
	p.mu.Unlock()

	for _, rt := range hostRuntimes {
		// Pointer-identity check first: robust regardless of the runtime's
		// concrete type, and catches e.g. NewRuntimePoolWithDockerHost, which
		// seeds the very same object as both the default runtime and a
		// host-pinned one (typically a test stub, not a *DockerRuntime).
		if visitedDockerRuntime != nil && rt == visitedDockerRuntime {
			continue // identical object already visited as the default runtime
		}
		if dr, ok := rt.(*DockerRuntime); ok && visitedDockerEndpoint != "" && dr.DaemonHost() == visitedDockerEndpoint {
			continue // same engine as the already-visited default Docker runtime
		}
		if err := fn(rt); err != nil {
			return err
		}
	}
	return nil
}

// Close closes all runtimes in the pool. After Close, Get and Default
// return errors.
//
// A runtime is closed at most once even if it appears in both p.runtimes and
// p.dockerHosts — which NewRuntimePoolWithDockerHost deliberately does, to
// make a single stub resolve from both Get(RuntimeDocker) and GetDockerAt.
// Deduping by pointer identity (rather than, say, only checking
// *DockerRuntime) keeps that safe for any Runtime implementation, including
// test stubs.
func (p *RuntimePool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	// Runtime is an interface, so keying a map by it is only safe while
	// every implementation is a pointer type — true today (*DockerRuntime,
	// *AppleRuntime, and the test stubs). A future value-receiver Runtime
	// would still satisfy the interface but panic with "hash of unhashable
	// type" the moment it's inserted here.
	closed := make(map[Runtime]struct{}, len(p.runtimes)+len(p.dockerHosts))
	var firstErr error
	closeOnce := func(rt Runtime) {
		if _, ok := closed[rt]; ok {
			return
		}
		closed[rt] = struct{}{}
		if err := rt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, rt := range p.runtimes {
		closeOnce(rt)
	}
	for _, rt := range p.dockerHosts {
		closeOnce(rt)
	}
	return firstErr
}
