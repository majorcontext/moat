//go:build linux

package trace

import (
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
)

// Netlink connector constants for process events
const (
	// Connector multicast group for process events
	_CN_IDX_PROC = 0x1
	_CN_VAL_PROC = 0x1

	// Process event types from linux/cn_proc.h
	_PROC_EVENT_FORK = 0x00000001
	_PROC_EVENT_EXEC = 0x00000002
	_PROC_EVENT_EXIT = 0x80000000

	// Connector subscription operations
	_PROC_CN_MCAST_LISTEN = 1
	_PROC_CN_MCAST_IGNORE = 2

	// NETLINK_CONNECTOR protocol number
	_NETLINK_CONNECTOR = 11
)

// Blank identifiers to satisfy compiler until imports are used
var (
	_ = binary.LittleEndian
	_ = syscall.AF_NETLINK
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
