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
	// or ping in GetDockerAt, keyed by host, mirroring the unavailable map's
	// per-process, no-TTL semantics. Without this, every reconnect attempt to
	// a dead endpoint (e.g. a stopped podman machine) pays the full ping
	// timeout again.
	dockerHostsUnavailable map[string]error
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
	}
	return pool, nil
}

// NewRuntimePoolWithDefault creates a pool with a pre-existing runtime as default.
// Used in tests to inject a stub runtime.
func NewRuntimePoolWithDefault(rt Runtime) *RuntimePool {
	return &RuntimePool{
		runtimes:  map[RuntimeType]Runtime{rt.Type(): rt},
		defaultRT: rt,
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

// dockerAtPingTimeout bounds how long GetDockerAt waits for a pinned
// DOCKER_HOST endpoint to answer a ping. Derived from the caller's ctx (via
// context.WithTimeout) so callers with a shorter deadline aren't held open
// longer than they asked for.
const dockerAtPingTimeout = 5 * time.Second

// GetDockerAt returns a Docker runtime pinned to the given DOCKER_HOST
// endpoint, lazily creating and caching it. Used to reconnect to runs whose
// containers live on a non-default endpoint (podman or Rancher Desktop
// sockets) recorded in their metadata, without mutating the process-wide
// DOCKER_HOST environment variable.
//
// If host is empty, this is equivalent to Get(RuntimeDocker) — the
// default-socket case.
//
// Construction and the readiness ping happen OUTSIDE the pool mutex, so a
// slow or wedged endpoint (e.g. a stopped podman machine, whose ping can
// take the full dockerAtPingTimeout) doesn't block unrelated Get/Default/
// Close calls from other goroutines. Failures are negatively cached per
// host (no TTL, mirroring Get's unavailable map) so repeated reconnect
// attempts to the same dead endpoint fail fast instead of re-paying the
// ping timeout.
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

	if err, failed := p.dockerHostsUnavailable[host]; failed {
		p.mu.Unlock()
		return nil, err
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

// cacheDockerHostFailure records a GetDockerAt failure for host so
// subsequent calls fail fast instead of re-attempting construction/ping.
func (p *RuntimePool) cacheDockerHostFailure(host string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dockerHostsUnavailable == nil {
		p.dockerHostsUnavailable = make(map[string]error)
	}
	p.dockerHostsUnavailable[host] = err
}

// podmanUnreachableHint returns a recovery hint appended to GetDockerAt
// errors when host looks like a podman endpoint (path/URL containing
// "podman"). Empty for hosts that don't look like podman. The hint notes
// that the endpoint came from the run's recorded metadata, and how to
// restart podman on this platform.
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

// ForEachAvailable calls fn for each runtime type that can be successfully
// initialized, skipping unavailable runtimes, and then for each host-pinned
// Docker runtime cached via GetDockerAt (e.g. a podman or Rancher Desktop
// endpoint recorded in a run's metadata) — otherwise those engines are
// invisible to commands like `moat clean`/`status` that enumerate images,
// containers, and networks across all available runtimes. A host-pinned
// runtime whose endpoint matches the already-visited default Docker
// runtime's DaemonHost is skipped, to avoid visiting the same engine twice.
// Iteration is sequential — fn is never called concurrently, so closures may
// safely append to external slices without synchronization.
//
// Note: this lazily initializes runtimes as a side effect. Runtimes
// initialized here will be closed when the pool is closed.
func (p *RuntimePool) ForEachAvailable(fn func(Runtime) error) error {
	var visitedDockerEndpoint string
	for _, typ := range AllRuntimeTypes() {
		rt, err := p.Get(typ)
		if err != nil {
			continue // Runtime not available (or pool closed)
		}
		if typ == RuntimeDocker {
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
func (p *RuntimePool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	var firstErr error
	for _, rt := range p.runtimes {
		if err := rt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, rt := range p.dockerHosts {
		if err := rt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
