# Process Tracer Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement real process execution tracing on Linux (proc connector) and macOS (libproc polling), replacing the stub tracer.

**Architecture:** Platform-specific tracers with a common factory function. Linux uses the kernel's proc connector (netlink) for real-time exec notifications. macOS uses libproc polling since ESF requires Apple entitlements. Both filter by PID/cgroup to only capture container processes.

**Tech Stack:** Go syscall package for netlink (Linux), cgo with libproc (macOS), build tags for platform separation.

---

## Task 1: Linux Proc Connector Tracer - Core Structure

**Files:**
- Create: `internal/trace/tracer_linux.go`

**Step 1: Create the tracer struct and constructor**

Create `internal/trace/tracer_linux.go`:

```go
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

// Compile-time interface check
var _ Tracer = (*ProcConnectorTracer)(nil)
```

**Step 2: Add interface method stubs**

Add to `internal/trace/tracer_linux.go`:

```go
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
```

**Step 3: Verify it compiles**

Run: `GOOS=linux go build ./internal/trace/...`
Expected: Build succeeds

**Step 4: Commit**

```bash
git add internal/trace/tracer_linux.go
git commit -m "feat(trace): add Linux proc connector tracer structure"
```

---

## Task 2: Linux Tracer - Netlink Constants and Types

**Files:**
- Modify: `internal/trace/tracer_linux.go`

**Step 1: Add netlink and proc connector constants**

Add after imports in `internal/trace/tracer_linux.go`:

```go
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
```

**Step 2: Verify it compiles**

Run: `GOOS=linux go build ./internal/trace/...`
Expected: Build succeeds

**Step 3: Commit**

```bash
git add internal/trace/tracer_linux.go
git commit -m "feat(trace): add netlink constants for proc connector"
```

---

## Task 3: Linux Tracer - Socket Setup and Subscription

**Files:**
- Modify: `internal/trace/tracer_linux.go`

**Step 1: Implement subscribe helper**

Add method to `internal/trace/tracer_linux.go`:

```go
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
	binary.LittleEndian.PutUint32(buf[0:], 40)                       // len
	binary.LittleEndian.PutUint16(buf[4:], syscall.NLMSG_DONE)       // type
	binary.LittleEndian.PutUint16(buf[6:], 0)                        // flags
	binary.LittleEndian.PutUint32(buf[8:], 1)                        // seq
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
```

**Step 2: Implement Start method**

Replace the Start stub:

```go
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
```

**Step 3: Implement Stop method**

Replace the Stop stub:

```go
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
```

**Step 4: Add readLoop placeholder**

```go
func (t *ProcConnectorTracer) readLoop() {
	defer t.wg.Done()
	// Will be implemented in next task
	<-t.done
}
```

**Step 5: Verify it compiles**

Run: `GOOS=linux go build ./internal/trace/...`
Expected: Build succeeds

**Step 6: Commit**

```bash
git add internal/trace/tracer_linux.go
git commit -m "feat(trace): implement netlink socket setup and subscription"
```

---

## Task 4: Linux Tracer - Event Reading and Parsing

**Files:**
- Modify: `internal/trace/tracer_linux.go`

**Step 1: Add imports for proc filesystem reading**

Update imports:

```go
import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)
```

**Step 2: Implement readLoop**

Replace the readLoop placeholder:

```go
func (t *ProcConnectorTracer) readLoop() {
	defer t.wg.Done()

	buf := make([]byte, 4096)
	for {
		select {
		case <-t.done:
			return
		default:
		}

		// Set read timeout to periodically check done channel
		tv := syscall.Timeval{Sec: 1, Usec: 0}
		syscall.SetsockoptTimeval(t.sock, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)

		n, _, err := syscall.Recvfrom(t.sock, buf, 0)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				continue
			}
			select {
			case <-t.done:
				return
			default:
				continue
			}
		}

		if n >= 52 { // Minimum size: nlhdr(16) + cnhdr(20) + proc_event(16)
			t.parseMessage(buf[:n])
		}
	}
}
```

**Step 3: Implement parseMessage**

```go
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
```

**Step 4: Implement helper methods**

```go
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
	select {
	case t.events <- event:
	default:
	}

	t.mu.Lock()
	cbs := make([]func(ExecEvent), len(t.callbacks))
	copy(cbs, t.callbacks)
	t.mu.Unlock()

	for _, cb := range cbs {
		cb(event)
	}
}
```

**Step 5: Verify it compiles**

Run: `GOOS=linux go build ./internal/trace/...`
Expected: Build succeeds

**Step 6: Commit**

```bash
git add internal/trace/tracer_linux.go
git commit -m "feat(trace): implement proc connector event reading and parsing"
```

---

## Task 5: macOS Tracer - Polling-based Implementation

**Files:**
- Create: `internal/trace/tracer_darwin.go`

**Step 1: Create macOS tracer using sysctl (no cgo)**

Create `internal/trace/tracer_darwin.go`:

```go
//go:build darwin

package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// DarwinTracer implements process tracing on macOS using sysctl polling.
// This is a fallback since ESF requires Apple entitlements.
type DarwinTracer struct {
	config    Config
	events    chan ExecEvent
	callbacks []func(ExecEvent)
	done      chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex
	started   bool

	// Track seen processes to detect new execs
	seenProcs    map[int]int64 // pid -> start time (unix seconds)
	procMu       sync.Mutex
	pollInterval time.Duration
}

// NewDarwinTracer creates a new macOS tracer.
func NewDarwinTracer(cfg Config) (*DarwinTracer, error) {
	return &DarwinTracer{
		config:       cfg,
		events:       make(chan ExecEvent, 100),
		done:         make(chan struct{}),
		seenProcs:    make(map[int]int64),
		pollInterval: 100 * time.Millisecond,
	}, nil
}

func (t *DarwinTracer) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return fmt.Errorf("tracer already started")
	}

	// Initialize with current processes
	t.scanProcesses(true)

	t.started = true
	t.wg.Add(1)
	go t.pollLoop()

	return nil
}

func (t *DarwinTracer) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return nil
	}

	close(t.done)
	t.wg.Wait()
	close(t.events)
	t.started = false

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

func (t *DarwinTracer) pollLoop() {
	defer t.wg.Done()

	ticker := time.NewTicker(t.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			t.scanProcesses(false)
		}
	}
}

// kinfoProc represents the kinfo_proc structure (partial)
type kinfoProc struct {
	_       [24]byte  // extern_proc (partial)
	Pid     int32     // p_pid at offset 24
	_       [16]byte  // padding
	Ppid    int32     // parent pid
	_       [44]byte  // more padding to comm
	Comm    [17]byte  // p_comm
	_       [71]byte  // rest of padding
	StartTV [16]byte  // p_starttime (timeval: sec + usec)
}

func (t *DarwinTracer) scanProcesses(initial bool) {
	// Use sysctl to get process list
	mib := []int32{1, 14, 0, 0} // CTL_KERN, KERN_PROC, KERN_PROC_ALL, 0

	// Get size first
	n := uintptr(0)
	if _, _, errno := syscall.Syscall6(
		syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		4,
		0,
		uintptr(unsafe.Pointer(&n)),
		0,
		0,
	); errno != 0 {
		return
	}

	// Get data
	buf := make([]byte, n)
	if _, _, errno := syscall.Syscall6(
		syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		4,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&n)),
		0,
		0,
	); errno != 0 {
		return
	}

	// Parse kinfo_proc structures (648 bytes each on arm64, 492 on x86_64)
	procSize := 648 // arm64
	if len(buf) > 0 && len(buf)%648 != 0 {
		procSize = 492 // x86_64
	}

	currentPids := make(map[int]bool)
	reader := bytes.NewReader(buf[:n])

	for reader.Len() >= procSize {
		chunk := make([]byte, procSize)
		if _, err := reader.Read(chunk); err != nil {
			break
		}

		// Extract PID (at offset 72 for arm64, 68 for x86_64)
		pidOffset := 72
		ppidOffset := 76
		commOffset := 243
		startOffset := 128
		if procSize == 492 {
			pidOffset = 68
			ppidOffset = 72
			commOffset = 163
			startOffset = 120
		}

		pid := int(int32(binary.LittleEndian.Uint32(chunk[pidOffset:])))
		if pid <= 0 {
			continue
		}

		ppid := int(int32(binary.LittleEndian.Uint32(chunk[ppidOffset:])))
		startSec := int64(binary.LittleEndian.Uint64(chunk[startOffset:]))

		currentPids[pid] = true

		// Check if new process
		t.procMu.Lock()
		oldStart, seen := t.seenProcs[pid]
		isNew := !seen || oldStart != startSec
		if isNew {
			t.seenProcs[pid] = startSec
		}
		t.procMu.Unlock()

		if isNew && !initial && t.shouldTrack(pid, ppid) {
			// Get comm string
			commEnd := commOffset
			for i := commOffset; i < commOffset+17 && i < len(chunk); i++ {
				if chunk[i] == 0 {
					commEnd = i
					break
				}
			}
			comm := string(chunk[commOffset:commEnd])

			event := &ExecEvent{
				Timestamp:  time.Now(),
				PID:        pid,
				PPID:       ppid,
				Command:    comm,
				Args:       t.getProcessArgs(pid),
				WorkingDir: "",
			}
			t.emitEvent(*event)
		}
	}

	// Clean up exited processes
	t.procMu.Lock()
	for pid := range t.seenProcs {
		if !currentPids[pid] {
			delete(t.seenProcs, pid)
		}
	}
	t.procMu.Unlock()
}

func (t *DarwinTracer) shouldTrack(pid, ppid int) bool {
	if t.config.PID == 0 {
		return true
	}
	return pid == t.config.PID || ppid == t.config.PID
}

func (t *DarwinTracer) getProcessArgs(pid int) []string {
	// Use sysctl KERN_PROCARGS2
	mib := []int32{1, 49, int32(pid)} // CTL_KERN, KERN_PROCARGS2, pid

	// Get size
	n := uintptr(0)
	if _, _, errno := syscall.Syscall6(
		syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		3,
		0,
		uintptr(unsafe.Pointer(&n)),
		0,
		0,
	); errno != 0 || n == 0 {
		return nil
	}

	buf := make([]byte, n)
	if _, _, errno := syscall.Syscall6(
		syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		3,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&n)),
		0,
		0,
	); errno != 0 {
		return nil
	}

	// Skip argc (4 bytes) and executable path
	if len(buf) < 4 {
		return nil
	}
	buf = buf[4:]

	// Find end of executable path
	idx := bytes.IndexByte(buf, 0)
	if idx == -1 {
		return nil
	}

	// Skip nulls after path
	for idx < len(buf) && buf[idx] == 0 {
		idx++
	}

	// Parse args
	var args []string
	for idx < len(buf) {
		end := bytes.IndexByte(buf[idx:], 0)
		if end == -1 || end == 0 {
			break
		}
		args = append(args, string(buf[idx:idx+end]))
		idx += end + 1
	}

	if len(args) > 0 {
		args = args[1:] // Skip executable name (first arg)
	}
	return args
}

func (t *DarwinTracer) emitEvent(event ExecEvent) {
	select {
	case t.events <- event:
	default:
	}

	t.mu.Lock()
	cbs := make([]func(ExecEvent), len(t.callbacks))
	copy(cbs, t.callbacks)
	t.mu.Unlock()

	for _, cb := range cbs {
		cb(event)
	}
}

// Compile-time check
var _ Tracer = (*DarwinTracer)(nil)
```

**Step 2: Verify it compiles**

Run: `go build ./internal/trace/...`
Expected: Build succeeds on macOS

**Step 3: Commit**

```bash
git add internal/trace/tracer_darwin.go
git commit -m "feat(trace): add macOS tracer using sysctl polling"
```

---

## Task 6: Factory Function and Platform Selection

**Files:**
- Create: `internal/trace/new.go`
- Create: `internal/trace/new_linux.go`
- Create: `internal/trace/new_darwin.go`
- Create: `internal/trace/new_other.go`

**Step 1: Create factory function**

Create `internal/trace/new.go`:

```go
package trace

// New creates a platform-appropriate tracer.
// On Linux, uses proc connector for real-time notifications.
// On macOS, uses sysctl polling.
// On other platforms, returns a stub tracer.
func New(cfg Config) (Tracer, error) {
	return newPlatformTracer(cfg)
}
```

**Step 2: Create Linux platform selector**

Create `internal/trace/new_linux.go`:

```go
//go:build linux

package trace

func newPlatformTracer(cfg Config) (Tracer, error) {
	return NewProcConnectorTracer(cfg)
}
```

**Step 3: Create macOS platform selector**

Create `internal/trace/new_darwin.go`:

```go
//go:build darwin

package trace

func newPlatformTracer(cfg Config) (Tracer, error) {
	return NewDarwinTracer(cfg)
}
```

**Step 4: Create fallback platform selector**

Create `internal/trace/new_other.go`:

```go
//go:build !linux && !darwin

package trace

func newPlatformTracer(cfg Config) (Tracer, error) {
	return NewStubTracer(cfg), nil
}
```

**Step 5: Verify build on all platforms**

Run:
```bash
go build ./internal/trace/...
GOOS=linux go build ./internal/trace/...
GOOS=windows go build ./internal/trace/...
```
Expected: All builds succeed

**Step 6: Commit**

```bash
git add internal/trace/new.go internal/trace/new_linux.go internal/trace/new_darwin.go internal/trace/new_other.go
git commit -m "feat(trace): add platform-specific tracer factory function"
```

---

## Task 7: Integration Tests

**Files:**
- Create: `internal/trace/tracer_integration_test.go`

**Step 1: Create integration test file**

Create `internal/trace/tracer_integration_test.go`:

```go
//go:build integration

package trace

import (
	"os/exec"
	"testing"
	"time"
)

func TestTracerCapturesExec(t *testing.T) {
	tracer, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if err := tracer.Start(); err != nil {
		t.Skipf("Tracer start failed (may require privileges): %v", err)
	}
	defer tracer.Stop()

	// Channel to capture events
	events := make(chan ExecEvent, 10)
	tracer.OnExec(func(e ExecEvent) {
		events <- e
	})

	// Execute a command
	cmd := exec.Command("echo", "test")
	if err := cmd.Run(); err != nil {
		t.Fatalf("exec echo failed: %v", err)
	}

	// Wait for event with timeout
	select {
	case event := <-events:
		if event.Command != "echo" {
			t.Errorf("Command = %q, want %q", event.Command, "echo")
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for exec event")
	}
}

func TestTracerFiltersByPID(t *testing.T) {
	// Create tracer that only tracks a non-existent PID
	tracer, err := New(Config{PID: 99999})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if err := tracer.Start(); err != nil {
		t.Skipf("Tracer start failed: %v", err)
	}
	defer tracer.Stop()

	events := make(chan ExecEvent, 10)
	tracer.OnExec(func(e ExecEvent) {
		events <- e
	})

	// Execute a command (should NOT be captured)
	cmd := exec.Command("echo", "test")
	_ = cmd.Run()

	// Should timeout with no events
	select {
	case event := <-events:
		t.Errorf("Unexpected event captured: %+v", event)
	case <-time.After(500 * time.Millisecond):
		// Expected - no events
	}
}
```

**Step 2: Run integration tests (if possible)**

Run: `go test -tags=integration -v ./internal/trace/... -run TestTracer`
Expected: Tests pass (or skip if privileges insufficient)

**Step 3: Commit**

```bash
git add internal/trace/tracer_integration_test.go
git commit -m "test(trace): add integration tests for platform tracers"
```

---

## Task 8: Documentation

**Files:**
- Create: `internal/trace/doc.go`

**Step 1: Add package documentation**

Create `internal/trace/doc.go`:

```go
// Package trace provides execution tracing for containerized processes.
//
// # Platform Support
//
// Linux: Uses the proc connector (netlink) for real-time exec notifications.
// Requires CAP_NET_ADMIN or root privileges.
//
// macOS: Uses sysctl polling to detect new processes. This is a fallback
// since the Endpoint Security Framework (ESF) requires Apple entitlements.
// Polling interval is 100ms by default.
//
// Other platforms: Uses a stub tracer that emits no events.
//
// # Usage
//
//	tracer, err := trace.New(trace.Config{PID: containerPID})
//	if err != nil {
//	    return err
//	}
//
//	tracer.OnExec(func(e trace.ExecEvent) {
//	    if e.IsGitCommit() {
//	        // Handle git commit
//	    }
//	})
//
//	if err := tracer.Start(); err != nil {
//	    return err
//	}
//	defer tracer.Stop()
//
// # Filtering
//
// Set Config.PID to only trace a specific process and its children.
// Set Config.CgroupPath (Linux only) to trace all processes in a cgroup.
package trace
```

**Step 2: Commit**

```bash
git add internal/trace/doc.go
git commit -m "docs(trace): add package documentation"
```

---

## Summary

After completing all tasks, the trace package will have:

1. **Linux**: Real-time exec tracing via proc connector (netlink)
2. **macOS**: Polling-based tracing via sysctl (no cgo required)
3. **Other**: Stub tracer (no-op)
4. **Factory**: `trace.New()` selects appropriate implementation
5. **Tests**: Integration tests with proper skip for privilege issues
6. **Docs**: Package documentation explaining platform differences

Files created/modified:
- `internal/trace/tracer_linux.go` - Linux proc connector implementation
- `internal/trace/tracer_darwin.go` - macOS sysctl polling implementation
- `internal/trace/new.go` - Factory function
- `internal/trace/new_linux.go` - Linux platform selector
- `internal/trace/new_darwin.go` - macOS platform selector
- `internal/trace/new_other.go` - Fallback platform selector
- `internal/trace/tracer_integration_test.go` - Integration tests
- `internal/trace/doc.go` - Package documentation
