//go:build linux

package trace

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
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
	stopped   bool

	// Tracked PIDs (container processes and their children)
	trackedPIDs map[int]bool
	pidMu       sync.RWMutex
	lastCleanup time.Time

	// Metrics for observability
	droppedEvents int64
}

const (
	// cleanupInterval is how often to check for stale PIDs
	cleanupInterval = 60 * time.Second
)

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

	if !t.started || t.stopped {
		return nil
	}

	t.stopped = true
	close(t.done)
	_ = t.subscribe(false)
	syscall.Close(t.sock)

	t.wg.Wait()
	close(t.events)
	t.started = false

	if t.droppedEvents > 0 {
		slog.Debug("tracer stopped", "dropped_events", t.droppedEvents)
	}

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

	buf := make([]byte, 4096)
	consecutiveErrors := 0
	const maxConsecutiveErrors = 10

	for {
		select {
		case <-t.done:
			return
		default:
		}

		// Periodically cleanup stale PIDs (in case EXIT events were missed)
		if time.Since(t.lastCleanup) > cleanupInterval {
			t.cleanupStalePIDs()
		}

		// Set read timeout to periodically check done channel
		tv := syscall.Timeval{Sec: 1, Usec: 0}
		if err := syscall.SetsockoptTimeval(t.sock, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
			slog.Debug("failed to set socket timeout", "error", err)
		}

		n, _, err := syscall.Recvfrom(t.sock, buf, 0)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				consecutiveErrors = 0
				continue
			}
			select {
			case <-t.done:
				return
			default:
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					slog.Error("too many consecutive errors in tracer read loop, stopping",
						"error", err, "count", consecutiveErrors)
					return
				}
				slog.Debug("error reading from netlink socket", "error", err)
				continue
			}
		}
		consecutiveErrors = 0

		if n >= 52 { // Minimum size: nlhdr(16) + cnhdr(20) + proc_event(16)
			t.parseMessage(buf[:n])
		}
	}
}

func (t *ProcConnectorTracer) parseMessage(buf []byte) {
	// Skip netlink header (16) and connector header (20)
	offset := 36

	if len(buf) < offset+16 {
		return
	}

	// Parse process event header
	what := binary.LittleEndian.Uint32(buf[offset:])
	offset += 16 // Skip: what(4) + cpu(4) + timestamp(8)

	switch what {
	case _PROC_EVENT_EXEC:
		if len(buf) < offset+8 {
			return
		}
		pid := int(binary.LittleEndian.Uint32(buf[offset:]))

		if t.shouldTrack(pid) {
			if event := t.buildExecEvent(pid); event != nil {
				t.emitEvent(*event)
			}
		}

	case _PROC_EVENT_FORK:
		if len(buf) < offset+16 {
			return
		}
		parentPid := int(binary.LittleEndian.Uint32(buf[offset:]))
		childPid := int(binary.LittleEndian.Uint32(buf[offset+8:]))

		// Track children of tracked processes
		t.pidMu.RLock()
		tracked := t.trackedPIDs[parentPid]
		t.pidMu.RUnlock()
		if tracked {
			t.pidMu.Lock()
			t.trackedPIDs[childPid] = true
			t.pidMu.Unlock()
		}

	case _PROC_EVENT_EXIT:
		if len(buf) < offset+8 {
			return
		}
		pid := int(binary.LittleEndian.Uint32(buf[offset:]))
		t.pidMu.Lock()
		delete(t.trackedPIDs, pid)
		t.pidMu.Unlock()
	}
}

func (t *ProcConnectorTracer) shouldTrack(pid int) bool {
	// If no filtering configured, track everything
	if t.config.PID == 0 && t.config.CgroupPath == "" {
		return true
	}

	t.pidMu.RLock()
	tracked := t.trackedPIDs[pid]
	t.pidMu.RUnlock()
	return tracked
}

// cleanupStalePIDs removes PIDs from trackedPIDs that no longer exist in /proc.
// This handles cases where EXIT events are missed (e.g., buffer overflow).
func (t *ProcConnectorTracer) cleanupStalePIDs() {
	t.pidMu.Lock()
	defer t.pidMu.Unlock()

	for pid := range t.trackedPIDs {
		procPath := fmt.Sprintf("/proc/%d", pid)
		if _, err := os.Stat(procPath); os.IsNotExist(err) {
			delete(t.trackedPIDs, pid)
		}
	}
	t.lastCleanup = time.Now()
}

func (t *ProcConnectorTracer) buildExecEvent(pid int) *ExecEvent {
	procPath := fmt.Sprintf("/proc/%d", pid)

	// Read cmdline (null-separated)
	cmdline, err := os.ReadFile(filepath.Join(procPath, "cmdline"))
	if err != nil {
		return nil
	}

	parts := strings.Split(string(cmdline), "\x00")
	if len(parts) == 0 || parts[0] == "" {
		return nil
	}

	cmd := filepath.Base(parts[0])
	var args []string
	if len(parts) > 1 {
		args = parts[1:]
		if len(args) > 0 && args[len(args)-1] == "" {
			args = args[:len(args)-1]
		}
	}

	// Read cwd
	cwd, _ := os.Readlink(filepath.Join(procPath, "cwd"))

	// Read ppid from status
	ppid := 0
	if f, err := os.Open(filepath.Join(procPath, "status")); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "PPid:") {
				fields := strings.Fields(scanner.Text())
				if len(fields) >= 2 {
					ppid, _ = strconv.Atoi(fields[1])
				}
				break
			}
		}
		f.Close()
	}

	return &ExecEvent{
		Timestamp:  time.Now(),
		PID:        pid,
		PPID:       ppid,
		Command:    cmd,
		Args:       args,
		WorkingDir: cwd,
	}
}

func (t *ProcConnectorTracer) emitEvent(event ExecEvent) {
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
		t.droppedEvents++
	}
	t.mu.Unlock()

	// Invoke callbacks outside lock to prevent deadlock
	for _, cb := range cbs {
		cb(event)
	}
}

// Compile-time interface check
var _ Tracer = (*ProcConnectorTracer)(nil)
