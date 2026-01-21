//go:build darwin

package trace

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// kinfo_proc structure offsets vary by architecture.
// These are the offsets for the fields we need to parse.
//
// The structure is defined in sys/sysctl.h. These offsets are validated at
// runtime by checking PID 1 (launchd) properties. If offsets are incorrect
// for the running macOS version, Start() will log a warning.
var (
	procSize    int
	pidOffset   int
	ppidOffset  int
	commOffset  int
	startOffset int
)

func init() {
	switch runtime.GOARCH {
	case "arm64":
		procSize = 648
		pidOffset = 72
		ppidOffset = 76
		commOffset = 243
		startOffset = 128
	default: // amd64/x86_64
		procSize = 492
		pidOffset = 68
		ppidOffset = 72
		commOffset = 163
		startOffset = 120
	}
}

// sysctl MIB constants
const (
	ctlKern       = 1
	kernProc      = 14
	kernProcAll   = 0
	kernProcArgs2 = 49
	maxCommLen    = 16 // MAXCOMLEN in sys/param.h
	defaultPollMs = 100
)

// DarwinTracer implements process tracing using sysctl polling on macOS.
// This approach doesn't require cgo or special entitlements (unlike ESF).
type DarwinTracer struct {
	config       Config
	events       chan ExecEvent
	callbacks    []func(ExecEvent)
	done         chan struct{}
	wg           sync.WaitGroup
	mu           sync.Mutex
	started      bool
	stopped      bool
	seenProcs    map[int]int64 // pid -> start time (to detect exec)
	trackedPIDs  map[int]bool  // PIDs we're actively tracking (for descendant tracking)
	procMu       sync.Mutex
	pollInterval time.Duration

	// Error tracking
	consecutiveErrors int

	// Metrics for observability
	droppedEvents int64
}

// NewDarwinTracer creates a new sysctl-based tracer for macOS.
func NewDarwinTracer(cfg Config) (*DarwinTracer, error) {
	return &DarwinTracer{
		config:       cfg,
		events:       make(chan ExecEvent, 100),
		done:         make(chan struct{}),
		seenProcs:    make(map[int]int64),
		trackedPIDs:  make(map[int]bool),
		pollInterval: defaultPollMs * time.Millisecond,
	}, nil
}

func (t *DarwinTracer) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return fmt.Errorf("tracer already started")
	}

	// Validate kinfo_proc offsets by checking PID 1 (launchd)
	// This catches offset mismatches on unsupported macOS versions
	if err := t.validateOffsets(); err != nil {
		slog.Warn("darwin tracer offset validation failed, process tracing may be unreliable",
			"error", err, "arch", runtime.GOARCH)
	}

	// Initialize with current processes (don't emit events for existing ones)
	// Ignore error - we'll start with empty process list if scan fails
	_ = t.scanProcesses(true)

	t.started = true
	t.wg.Add(1)
	go t.pollLoop()

	return nil
}

func (t *DarwinTracer) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started || t.stopped {
		return nil
	}

	t.stopped = true
	close(t.done)
	t.wg.Wait()
	close(t.events)
	t.started = false

	if t.droppedEvents > 0 {
		slog.Debug("tracer stopped", "dropped_events", t.droppedEvents)
	}

	return nil
}

func (t *DarwinTracer) Events() <-chan ExecEvent {
	return t.events
}

func (t *DarwinTracer) OnExec(cb func(ExecEvent)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callbacks = append(t.callbacks, cb)
}

const maxConsecutiveDarwinErrors = 10

// pollLoop periodically scans for new processes.
func (t *DarwinTracer) pollLoop() {
	defer t.wg.Done()

	ticker := time.NewTicker(t.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			if err := t.scanProcesses(false); err != nil {
				t.consecutiveErrors++
				if t.consecutiveErrors >= maxConsecutiveDarwinErrors {
					slog.Error("too many consecutive errors in darwin tracer, stopping",
						"error", err, "count", t.consecutiveErrors)
					return
				}
				slog.Debug("error scanning processes", "error", err)
			} else {
				t.consecutiveErrors = 0
			}
		}
	}
}

// scanProcesses uses sysctl to get all processes and detect new ones.
func (t *DarwinTracer) scanProcesses(initial bool) error {
	procs, err := t.getAllProcesses()
	if err != nil {
		return err
	}

	t.procMu.Lock()
	defer t.procMu.Unlock()

	// Track which PIDs we've seen this scan (for cleanup)
	currentPIDs := make(map[int]bool)

	// Initialize target PID tracking on first scan
	if initial && t.config.PID > 0 {
		t.trackedPIDs[t.config.PID] = true
	}

	for _, proc := range procs {
		currentPIDs[proc.pid] = true

		// Check if this is a new process or an exec (new start time for same PID)
		prevStartTime, seen := t.seenProcs[proc.pid]
		isNew := !seen || prevStartTime != proc.startTime

		// Update tracking
		t.seenProcs[proc.pid] = proc.startTime

		// Check if we should track this process
		shouldTrack := t.shouldTrackLocked(proc.pid, proc.ppid)

		// If parent is tracked, track this process too (for descendants)
		if shouldTrack && t.config.PID > 0 {
			t.trackedPIDs[proc.pid] = true
		}

		// Emit event for new processes (but not on initial scan)
		if isNew && !initial && shouldTrack {
			event := t.buildExecEvent(proc)
			t.emitEvent(event)
		}
	}

	// Clean up exited processes
	for pid := range t.seenProcs {
		if !currentPIDs[pid] {
			delete(t.seenProcs, pid)
			delete(t.trackedPIDs, pid)
		}
	}

	return nil
}

// processInfo holds basic process information from kinfo_proc.
type processInfo struct {
	pid       int
	ppid      int
	comm      string
	startTime int64
}

// getAllProcesses returns all processes using sysctl KERN_PROC_ALL.
func (t *DarwinTracer) getAllProcesses() ([]processInfo, error) {
	mib := []int32{ctlKern, kernProc, kernProcAll, 0}

	// Get required buffer size
	var size uintptr
	_, _, errno := unix.Syscall6(
		unix.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		uintptr(len(mib)),
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		0,
	)
	if errno != 0 {
		return nil, fmt.Errorf("sysctl size: %w", errno)
	}

	if size == 0 {
		return nil, nil
	}

	// Allocate buffer and get data
	buf := make([]byte, size)
	_, _, errno = unix.Syscall6(
		unix.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		uintptr(len(mib)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		0,
	)
	if errno != 0 {
		return nil, fmt.Errorf("sysctl: %w", errno)
	}

	// Parse process list
	numProcs := int(size) / procSize
	procs := make([]processInfo, 0, numProcs)

	for i := 0; i < numProcs; i++ {
		offset := i * procSize
		if offset+procSize > int(size) {
			break
		}

		procBuf := buf[offset : offset+procSize]
		info := t.parseKinfoProc(procBuf)
		if info.pid > 0 {
			procs = append(procs, info)
		}
	}

	return procs, nil
}

// parseKinfoProc extracts process info from a kinfo_proc structure.
func (t *DarwinTracer) parseKinfoProc(buf []byte) processInfo {
	info := processInfo{}

	if len(buf) < procSize {
		return info
	}

	// Extract PID (p_pid) - PIDs are always positive and fit in int32
	info.pid = int(int32(binary.LittleEndian.Uint32(buf[pidOffset:]))) //nolint:gosec // G115: PID fits in int32

	// Extract PPID (p_ppid)
	info.ppid = int(int32(binary.LittleEndian.Uint32(buf[ppidOffset:]))) //nolint:gosec // G115: PID fits in int32

	// Extract command name (p_comm) - null-terminated string
	commEnd := commOffset
	for i := commOffset; i < commOffset+maxCommLen && i < len(buf); i++ {
		if buf[i] == 0 {
			break
		}
		commEnd = i + 1
	}
	info.comm = string(buf[commOffset:commEnd])

	// Extract start time (p_starttime.tv_sec) - timeval struct
	// On Darwin, timeval has tv_sec (int64 on 64-bit) at offset 0
	if startOffset+8 <= len(buf) {
		info.startTime = int64(binary.LittleEndian.Uint64(buf[startOffset:])) //nolint:gosec // G115: timestamp fits in int64
	}

	return info
}

// validateOffsets checks that kinfo_proc offsets are correct by examining PID 1.
// PID 1 (launchd on macOS) always exists and has known properties we can validate.
func (t *DarwinTracer) validateOffsets() error {
	procs, err := t.getAllProcesses()
	if err != nil {
		return fmt.Errorf("get processes: %w", err)
	}

	// Find PID 1 (launchd)
	var pid1 *processInfo
	for i := range procs {
		if procs[i].pid == 1 {
			pid1 = &procs[i]
			break
		}
	}

	if pid1 == nil {
		return fmt.Errorf("PID 1 not found in process list")
	}

	// Validate PID 1 properties:
	// - PPID should be 0 (init process has no parent)
	// - Command should be "launchd"
	// - Start time should be positive and reasonable (after year 2000)
	if pid1.ppid != 0 {
		return fmt.Errorf("PID 1 has unexpected PPID %d (expected 0)", pid1.ppid)
	}

	if pid1.comm != "launchd" {
		return fmt.Errorf("PID 1 has unexpected command %q (expected launchd)", pid1.comm)
	}

	// Year 2000 in Unix time
	const year2000 = 946684800
	if pid1.startTime < year2000 {
		return fmt.Errorf("PID 1 has invalid start time %d", pid1.startTime)
	}

	return nil
}

// shouldTrackLocked returns true if the process should be tracked.
// Must be called while holding procMu.
func (t *DarwinTracer) shouldTrackLocked(pid, ppid int) bool {
	// If no filtering configured, track everything
	if t.config.PID == 0 {
		return true
	}

	// Track if it's the target PID
	if pid == t.config.PID {
		return true
	}

	// Track if parent is in our tracked lineage (handles grandchildren and deeper)
	if t.trackedPIDs[ppid] {
		return true
	}

	return false
}

// buildExecEvent creates an ExecEvent from process info.
func (t *DarwinTracer) buildExecEvent(info processInfo) ExecEvent {
	event := ExecEvent{
		Timestamp: time.Now(),
		PID:       info.pid,
		PPID:      info.ppid,
		Command:   info.comm,
	}

	// Try to get full arguments
	if args := t.getProcessArgs(info.pid); len(args) > 0 {
		event.Command = args[0]
		if len(args) > 1 {
			event.Args = args[1:]
		}
	}

	return event
}

// getProcessArgs retrieves command line arguments using raw sysctl.
func (t *DarwinTracer) getProcessArgs(pid int) []string {
	mib := []int32{ctlKern, kernProcArgs2, int32(pid)} //nolint:gosec // G115: PID fits in int32

	// Get required buffer size
	var size uintptr
	_, _, errno := unix.Syscall6(
		unix.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		uintptr(len(mib)),
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		0,
	)
	if errno != 0 || size == 0 {
		return nil
	}

	// Allocate buffer and get data
	buf := make([]byte, size)
	_, _, errno = unix.Syscall6(
		unix.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		uintptr(len(mib)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		0,
	)
	if errno != 0 {
		return nil
	}

	if size < 4 {
		return nil
	}

	// First 4 bytes is argc
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	if argc <= 0 || argc > 1000 {
		return nil
	}

	// Skip argc and find the executable path (null-terminated)
	pos := 4
	for pos < int(size) && buf[pos] != 0 {
		pos++
	}

	// Skip null terminators and padding after executable path
	for pos < int(size) && buf[pos] == 0 {
		pos++
	}

	// Parse null-terminated argument strings
	args := make([]string, 0, argc)
	for i := 0; i < argc && pos < int(size); i++ {
		start := pos
		for pos < int(size) && buf[pos] != 0 {
			pos++
		}
		if start < pos {
			args = append(args, string(buf[start:pos]))
		}
		pos++ // skip null terminator
	}

	return args
}

// emitEvent sends an event to the channel and callbacks.
func (t *DarwinTracer) emitEvent(event ExecEvent) {
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
var _ Tracer = (*DarwinTracer)(nil)
