//go:build linux

package trace

import (
	"fmt"
	"sync"
)

// ProcConnectorTracer implements process tracing using Linux proc connector.
// Requires CAP_NET_ADMIN or root privileges.
type ProcConnectorTracer struct {
	config    Config
	sock      int
	events    chan ExecEvent
	callbacks []func(ExecEvent)
	done      chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex
	started   bool

	// Tracked PIDs (container processes and their children)
	trackedPIDs map[int]bool
	pidMu       sync.RWMutex
}

// NewProcConnectorTracer creates a new proc connector tracer.
func NewProcConnectorTracer(cfg Config) (*ProcConnectorTracer, error) {
	return &ProcConnectorTracer{
		config:      cfg,
		events:      make(chan ExecEvent, 100),
		done:        make(chan struct{}),
		trackedPIDs: make(map[int]bool),
	}, nil
}

func (t *ProcConnectorTracer) Start() error {
	return fmt.Errorf("not implemented")
}

func (t *ProcConnectorTracer) Stop() error {
	return nil
}

func (t *ProcConnectorTracer) Events() <-chan ExecEvent {
	return t.events
}

func (t *ProcConnectorTracer) OnExec(cb func(ExecEvent)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callbacks = append(t.callbacks, cb)
}

// Compile-time interface check
var _ Tracer = (*ProcConnectorTracer)(nil)
