package trace

import (
	"log/slog"
	"sync"
)

// StubTracer is a no-op tracer for platforms without native tracing support.
type StubTracer struct {
	events    chan ExecEvent
	callbacks []func(ExecEvent)
	mu        sync.Mutex
	stopped   bool
}

// NewStubTracer creates a stub tracer that does nothing.
func NewStubTracer(cfg Config) *StubTracer {
	return &StubTracer{
		events: make(chan ExecEvent, 100),
	}
}

func (t *StubTracer) Start() error {
	slog.Debug("stub tracer started (no-op)")
	return nil
}

func (t *StubTracer) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stopped {
		return nil
	}
	t.stopped = true
	close(t.events)
	return nil
}

func (t *StubTracer) Events() <-chan ExecEvent {
	return t.events
}

func (t *StubTracer) OnExec(cb func(ExecEvent)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callbacks = append(t.callbacks, cb)
}

// Emit allows manual event injection for testing.
func (t *StubTracer) Emit(event ExecEvent) {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}

	// Copy callbacks under lock
	cbs := make([]func(ExecEvent), len(t.callbacks))
	copy(cbs, t.callbacks)

	// Send to channel (non-blocking) while holding lock to prevent race with Stop()
	select {
	case t.events <- event:
	default:
	}
	t.mu.Unlock()

	// Invoke callbacks outside lock to prevent deadlock
	for _, cb := range cbs {
		cb(event)
	}
}

// Compile-time check
var _ Tracer = (*StubTracer)(nil)
