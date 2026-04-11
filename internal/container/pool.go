package container

import (
	"fmt"
	"sync"
)

// RuntimePool manages multiple container runtime instances, keyed by RuntimeType.
// It lazily initializes runtimes on first access and provides a default runtime
// for new run creation. Thread-safe for concurrent access.
type RuntimePool struct {
	mu        sync.Mutex
	runtimes  map[RuntimeType]Runtime
	defaultRT Runtime
	opts      RuntimeOptions
	closed    bool
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

	rt, err := NewRuntimeByType(typ, p.opts)
	if err != nil {
		return nil, fmt.Errorf("runtime %s not available: %w", typ, err)
	}

	p.runtimes[typ] = rt
	return rt, nil
}

// ForEachAvailable calls fn for each runtime type that can be successfully
// initialized, skipping unavailable runtimes. Iteration is sequential —
// fn is never called concurrently, so closures may safely append to
// external slices without synchronization.
//
// Note: this lazily initializes runtimes as a side effect. Runtimes
// initialized here will be closed when the pool is closed.
func (p *RuntimePool) ForEachAvailable(fn func(Runtime) error) error {
	for _, typ := range AllRuntimeTypes() {
		rt, err := p.Get(typ)
		if err != nil {
			continue // Runtime not available on this system
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
	return firstErr
}
