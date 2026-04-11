package container

import (
	"fmt"
	"sync"
)

// RuntimePool manages multiple container runtime instances, keyed by RuntimeType.
// It lazily initializes runtimes on first access and provides a default runtime
// for new run creation. Thread-safe for concurrent access.
type RuntimePool struct {
	mu       sync.Mutex
	runtimes map[RuntimeType]Runtime
	dflt     Runtime
	opts     RuntimeOptions
	closed   bool
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
		runtimes: map[RuntimeType]Runtime{rt.Type(): rt},
		dflt:     rt,
		opts:     opts,
	}
	return pool, nil
}

// NewRuntimePoolWithDefault creates a pool with a pre-existing runtime as default.
// Used in tests to inject a stub runtime.
func NewRuntimePoolWithDefault(rt Runtime) *RuntimePool {
	return &RuntimePool{
		runtimes: map[RuntimeType]Runtime{rt.Type(): rt},
		dflt:     rt,
	}
}

// Default returns the auto-detected default runtime.
// Used for creating new runs.
func (p *RuntimePool) Default() Runtime {
	return p.dflt
}

// Get returns the runtime for the given type, lazily initializing it if needed.
// Returns the default runtime if typ is empty (legacy runs without a runtime field).
func (p *RuntimePool) Get(typ RuntimeType) (Runtime, error) {
	if typ == "" {
		return p.dflt, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, fmt.Errorf("runtime pool is closed")
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

// Available returns all runtimes that are currently initialized in the pool.
// Does not probe for new runtimes — only returns what has been lazily created
// via Get() or the default from construction.
func (p *RuntimePool) Available() []Runtime {
	p.mu.Lock()
	defer p.mu.Unlock()

	rts := make([]Runtime, 0, len(p.runtimes))
	for _, rt := range p.runtimes {
		rts = append(rts, rt)
	}
	return rts
}

// ForEachAvailable calls fn for each runtime type, initializing it if possible.
// Errors from unavailable runtimes are silently skipped — fn is only called
// for runtimes that can be successfully initialized.
// Used by status/clean commands that need to query all runtimes.
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

// Close closes all runtimes in the pool.
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
