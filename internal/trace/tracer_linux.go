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
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return fmt.Errorf("tracer already started")
	}

	// Create netlink socket
	sock, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM, _NETLINK_CONNECTOR)
	if err != nil {
		return fmt.Errorf("create netlink socket: %w (requires CAP_NET_ADMIN or root)", err)
	}
	t.sock = sock

	// Bind to process events group
	addr := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: _CN_IDX_PROC,
		Pid:    uint32(syscall.Getpid()),
	}
	if err := syscall.Bind(sock, addr); err != nil {
		syscall.Close(sock)
		return fmt.Errorf("bind netlink socket: %w", err)
	}

	// Subscribe to process events
	if err := t.subscribe(true); err != nil {
		syscall.Close(sock)
		return fmt.Errorf("subscribe to process events: %w", err)
	}

	// Initialize tracked PIDs
	if t.config.PID > 0 {
		t.trackedPIDs[t.config.PID] = true
	}

	t.started = true
	t.wg.Add(1)
	go t.readLoop()

	return nil
}

func (t *ProcConnectorTracer) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return nil
	}

	close(t.done)
	_ = t.subscribe(false)
	syscall.Close(t.sock)

	t.wg.Wait()
	close(t.events)
	t.started = false

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

// subscribe sends a message to subscribe/unsubscribe from process events.
func (t *ProcConnectorTracer) subscribe(listen bool) error {
	op := uint32(_PROC_CN_MCAST_IGNORE)
	if listen {
		op = uint32(_PROC_CN_MCAST_LISTEN)
	}

	// Build message: nlmsghdr + cn_msg + op
	// Total size: 16 (nlhdr) + 20 (cnhdr) + 4 (op) = 40 bytes
	buf := make([]byte, 40)

	// Netlink header
	binary.LittleEndian.PutUint32(buf[0:], 40)                        // len
	binary.LittleEndian.PutUint16(buf[4:], syscall.NLMSG_DONE)        // type
	binary.LittleEndian.PutUint16(buf[6:], 0)                         // flags
	binary.LittleEndian.PutUint32(buf[8:], 1)                         // seq
	binary.LittleEndian.PutUint32(buf[12:], uint32(syscall.Getpid())) // pid

	// Connector header
	binary.LittleEndian.PutUint32(buf[16:], _CN_IDX_PROC) // id.idx
	binary.LittleEndian.PutUint32(buf[20:], _CN_VAL_PROC) // id.val
	binary.LittleEndian.PutUint32(buf[24:], 1)            // seq
	binary.LittleEndian.PutUint32(buf[28:], 0)            // ack
	binary.LittleEndian.PutUint16(buf[32:], 4)            // len (op size)
	binary.LittleEndian.PutUint16(buf[34:], 0)            // flags

	// Operation
	binary.LittleEndian.PutUint32(buf[36:], op)

	// Send to kernel (pid=0)
	addr := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: _CN_IDX_PROC,
		Pid:    0,
	}
	return syscall.Sendto(t.sock, buf, 0, addr)
}

func (t *ProcConnectorTracer) readLoop() {
	defer t.wg.Done()
	// Will be implemented in next task
	<-t.done
}

// Compile-time interface check
var _ Tracer = (*ProcConnectorTracer)(nil)
