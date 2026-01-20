package trace

import "log/slog"

// StubTracer is a no-op tracer for platforms without native tracing support.
type StubTracer struct {
	events    chan ExecEvent
	callbacks []func(ExecEvent)
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
	close(t.events)
	return nil
}

func (t *StubTracer) Events() <-chan ExecEvent {
	return t.events
}

func (t *StubTracer) OnExec(cb func(ExecEvent)) {
	t.callbacks = append(t.callbacks, cb)
}

// Emit allows manual event injection for testing.
func (t *StubTracer) Emit(event ExecEvent) {
	for _, cb := range t.callbacks {
		cb(event)
	}
	select {
	case t.events <- event:
	default:
	}
}

// Compile-time check
var _ Tracer = (*StubTracer)(nil)
