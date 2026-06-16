# Multi-agent join (`moat join`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `moat join <run> <agent>` — launch a second agent inside an already-running container, reusing its workspace, grants, config, and proxy credential context, with no new container or proxy registration.

**Architecture:** `join` is the run-first sibling of `exec` (`join : exec :: claude : run`). It resolves a running run, looks up the agent provider, validates the run was created by that provider, builds the in-container command, and runs it through a new **PTY-backed interactive exec** on the container `Runtime` (the `docker exec -it` equivalent — today's `Exec` is non-interactive). Joined agents are exec children, so the original agent keeps owning the container lifecycle (Decision 2 of the design). Observability is hybrid: console split per agent, network/audit interleaved automatically via the shared proxy token. The status footer gains a session role/count.

**Tech Stack:** Go, Cobra CLI, Docker SDK (`github.com/docker/docker/client`), Apple `container` CLI + `github.com/creack/pty`, `golang.org/x/term`.

**Design doc:** `docs/plans/2026-06-16-multi-agent-join-design.md`

**Scope (v1):** same-provider join only (claude into a claude run). Cross-provider, container-side worktree, and persistent sessions are deferred (see design doc "Future extensions").

---

## File Structure

**Create:**
- `internal/container/exec_options.go` — `ExecOptions` and `TTYSize` types for interactive exec.
- `cmd/moat/cli/join_cmd.go` — the `moat join` command + interactive/headless join loops.
- `internal/run/joined.go` — filesystem-backed attached-agent registry (display-only count).
- `internal/run/joined_test.go` — registry unit tests.
- `internal/e2e/join_test.go` — E2E test for an interactive + headless join (requires a container runtime).

**Modify:**
- `internal/container/runtime.go` — add `ExecInteractive` to the `Runtime` interface.
- `internal/container/docker.go` — implement `DockerRuntime.ExecInteractive`.
- `internal/container/apple.go` — implement `AppleRuntime.ExecInteractive`.
- `internal/provider/interfaces.go` — add the optional `JoinableAgent` interface + `JoinOpts`.
- `internal/providers/claude/cli.go` (or a new `internal/providers/claude/join.go`) — implement `JoinableAgent` on the claude provider.
- `internal/run/manager.go` — add `Manager.ExecInteractive` + attached-count accessors.
- `internal/tui/statusbar.go` — add session role/count fields + rendering.
- `internal/tui/statusbar_test.go` — footer rendering tests.
- `docs/content/reference/01-cli.md`, `docs/content/guides/*`, `CHANGELOG.md` — docs.

---

## Phase A — Runtime: PTY-backed interactive exec

### Task A1: Define `ExecOptions`/`TTYSize` and add `ExecInteractive` to the Runtime interface

**Files:**
- Create: `internal/container/exec_options.go`
- Modify: `internal/container/runtime.go` (interface, after the `Exec` method at line ~143)
- Modify: `internal/container/docker.go`, `internal/container/apple.go` (temporary stubs so the package compiles)

- [ ] **Step 1: Write the new option types**

Create `internal/container/exec_options.go`:

```go
package container

import "io"

// TTYSize is a terminal dimension update for an interactive exec session.
type TTYSize struct {
	Width  uint
	Height uint
}

// ExecOptions configures an interactive (PTY-backed) exec session.
// It mirrors AttachOptions but targets an exec'd process rather than the
// container's main process, and carries a Resize channel so the caller can
// propagate SIGWINCH to this specific exec session (a container may have many).
type ExecOptions struct {
	Stdin  io.Reader // forwarded to the exec'd process
	Stdout io.Writer // receives the exec'd process output
	Stderr io.Writer // receives stderr (ignored in TTY mode, where it merges into Stdout)
	TTY    bool      // allocate a PTY (raw terminal) for the exec

	// InitialWidth/InitialHeight size the PTY before the process queries it.
	InitialWidth  uint
	InitialHeight uint

	// Resize, if non-nil, delivers terminal size changes for this exec session.
	// The runtime applies each value until the channel is closed or the exec ends.
	Resize <-chan TTYSize
}
```

- [ ] **Step 2: Add the method to the `Runtime` interface**

In `internal/container/runtime.go`, immediately after the `Exec(...)` method (line ~143), add:

```go
	// ExecInteractive runs a command inside a running container with a PTY,
	// streaming opts.Stdin/Stdout and applying terminal resizes from opts.Resize.
	// Use this for interactive TUI agents joined into an existing container.
	// Returns *ExecError for non-zero exit codes.
	ExecInteractive(ctx context.Context, id string, cmd []string, opts ExecOptions) error
```

- [ ] **Step 3: Add temporary stubs so both runtimes still satisfy the interface**

In `internal/container/docker.go`, add:

```go
// ExecInteractive runs a command inside a running container with a PTY.
func (r *DockerRuntime) ExecInteractive(ctx context.Context, containerID string, cmd []string, opts ExecOptions) error {
	return fmt.Errorf("not implemented")
}
```

In `internal/container/apple.go`, add:

```go
// ExecInteractive runs a command inside a running container with a PTY.
func (r *AppleRuntime) ExecInteractive(ctx context.Context, containerID string, cmd []string, opts ExecOptions) error {
	return fmt.Errorf("not implemented")
}
```

- [ ] **Step 4: Verify it compiles**

Run: `go build ./internal/container/...`
Expected: builds with no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/container/exec_options.go internal/container/runtime.go internal/container/docker.go internal/container/apple.go
git commit -m "feat(container): add ExecInteractive to Runtime interface (stubs)"
```

---

### Task A2: Implement `DockerRuntime.ExecInteractive`

**Files:**
- Modify: `internal/container/docker.go` (replace the stub from A1)
- Test: `internal/e2e/join_test.go` (E2E — added in Task D-final; logic verified here by build + the existing exec patterns)

Mirror `DockerRuntime.Exec` (exec create/attach/inspect) but with `Tty: true` and the raw TTY copy loop from `StartAttached`.

- [ ] **Step 1: Replace the stub with the real implementation**

```go
// ExecInteractive runs a command inside a running container with a PTY.
func (r *DockerRuntime) ExecInteractive(ctx context.Context, containerID string, cmd []string, opts ExecOptions) error {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		User:         "moatuser",
		Tty:          opts.TTY,
		AttachStdin:  opts.Stdin != nil,
		AttachStdout: true,
		AttachStderr: !opts.TTY, // in TTY mode stderr is merged into stdout
	}

	execID, err := r.cli.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return fmt.Errorf("creating exec: %w", err)
	}

	resp, err := r.cli.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{Tty: opts.TTY})
	if err != nil {
		return fmt.Errorf("attaching to exec: %w", err)
	}
	defer resp.Close()

	// Apply the initial size before output starts, then service resizes.
	if opts.TTY && opts.InitialWidth > 0 && opts.InitialHeight > 0 {
		_ = r.cli.ContainerExecResize(ctx, execID.ID, container.ResizeOptions{
			Height: opts.InitialHeight,
			Width:  opts.InitialWidth,
		})
	}
	if opts.TTY && opts.Resize != nil {
		go func() {
			for size := range opts.Resize {
				if size.Width == 0 || size.Height == 0 {
					continue
				}
				_ = r.cli.ContainerExecResize(ctx, execID.ID, container.ResizeOptions{
					Height: size.Height,
					Width:  size.Width,
				})
			}
		}()
	}

	outputDone := make(chan error, 1)
	go func() {
		if opts.TTY {
			// TTY mode is a single raw stream.
			_, copyErr := io.Copy(opts.Stdout, resp.Reader)
			outputDone <- copyErr
		} else {
			_, copyErr := stdcopy.StdCopy(opts.Stdout, opts.Stderr, resp.Reader)
			outputDone <- copyErr
		}
	}()

	if opts.Stdin != nil {
		go func() {
			_, _ = io.Copy(resp.Conn, opts.Stdin)
			if cw, ok := resp.Conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			}
		}()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case copyErr := <-outputDone:
		if copyErr != nil && copyErr != io.EOF {
			return copyErr
		}
	}

	inspect, err := r.cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return fmt.Errorf("inspecting exec: %w", err)
	}
	if inspect.ExitCode != 0 {
		return &ExecError{ExitCode: inspect.ExitCode}
	}
	return nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/container/...`
Expected: builds clean. (`stdcopy` and `container` are already imported by docker.go — confirm; if `go build` complains about an unused import it means stdcopy was only used here, which is not the case since `Exec` uses it.)

- [ ] **Step 3: Vet**

Run: `go vet ./internal/container/...`
Expected: no findings.

- [ ] **Step 4: Commit**

```bash
git add internal/container/docker.go
git commit -m "feat(container): implement DockerRuntime.ExecInteractive (PTY exec)"
```

---

### Task A3: Implement `AppleRuntime.ExecInteractive`

**Files:**
- Modify: `internal/container/apple.go` (replace the stub from A1)

Mirror `startAttachedWithPTY`: shell out to `container exec -t -i --user moatuser <id> <cmd>` under a `pty.Start`, copy streams, resize the local PTY from `opts.Resize`, and surface the exit code via the existing `exitError` helper.

- [ ] **Step 1: Replace the stub with the real implementation**

```go
// ExecInteractive runs a command inside a running container with a PTY.
func (r *AppleRuntime) ExecInteractive(ctx context.Context, containerID string, cmd []string, opts ExecOptions) error {
	if !opts.TTY {
		// Non-TTY interactive exec: stream stdin/stdout without a PTY.
		args := append([]string{"exec", "--user", "moatuser", "-i", containerID}, cmd...)
		c := exec.CommandContext(ctx, r.containerBin, args...)
		c.Stdin = opts.Stdin
		c.Stdout = opts.Stdout
		c.Stderr = opts.Stderr
		if err := c.Run(); err != nil {
			return exitError(err)
		}
		return nil
	}

	args := append([]string{"exec", "-t", "-i", "--user", "moatuser", containerID}, cmd...)
	c := exec.CommandContext(ctx, r.containerBin, args...)

	ptmx, err := pty.Start(c)
	if err != nil {
		return fmt.Errorf("starting exec with pty: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Initial size, then service resizes for this exec's PTY directly (no shared map).
	if opts.InitialWidth > 0 && opts.InitialHeight > 0 {
		_ = pty.Setsize(ptmx, &pty.Winsize{
			Rows: uint16(opts.InitialHeight), // #nosec G115
			Cols: uint16(opts.InitialWidth),  // #nosec G115
		})
	}
	if opts.Resize != nil {
		go func() {
			for size := range opts.Resize {
				if size.Width == 0 || size.Height == 0 {
					continue
				}
				_ = pty.Setsize(ptmx, &pty.Winsize{
					Rows: uint16(size.Height), // #nosec G115
					Cols: uint16(size.Width),  // #nosec G115
				})
			}
		}()
	}

	if opts.Stdin != nil {
		go func() { _, _ = io.Copy(ptmx, opts.Stdin) }()
	}
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		_, _ = io.Copy(opts.Stdout, ptmx)
	}()

	cmdDone := make(chan error, 1)
	go func() { cmdDone <- c.Wait() }()

	select {
	case <-ctx.Done():
		_ = ptmx.Close()
		if c.Process != nil {
			_ = c.Process.Kill()
		}
		<-cmdDone
		return ctx.Err()
	case werr := <-cmdDone:
		// Drain output briefly so nothing is lost on fast exit.
		select {
		case <-outputDone:
		case <-time.After(2 * time.Second):
			_ = ptmx.Close()
			<-outputDone
		}
		if werr != nil {
			return exitError(werr)
		}
		return nil
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/container/...`
Expected: builds clean. (`exec`, `io`, `time`, `pty` are already imported by apple.go.)

- [ ] **Step 3: Vet**

Run: `go vet ./internal/container/...`
Expected: no findings.

- [ ] **Step 4: Commit**

```bash
git add internal/container/apple.go
git commit -m "feat(container): implement AppleRuntime.ExecInteractive (PTY exec)"
```

---

## Phase B — Manager: interactive exec + attached-agent registry

### Task B1: Filesystem-backed attached-agent registry

**Files:**
- Create: `internal/run/joined.go`
- Test: `internal/run/joined_test.go`

A join is a separate process from the primary, so the count must be shared on disk under the run directory. Entries are named by pid; liveness is checked with `syscall.Kill(pid, 0)` so a crashed join is pruned instead of inflating the count. **Display-only — never used for teardown.**

- [ ] **Step 1: Write the failing test**

Create `internal/run/joined_test.go`:

```go
package run

import (
	"os"
	"testing"
)

func TestAttachedAgents_RegisterCountRelease(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	const runID = "run_test_join"

	if got := attachedCount(runID); got != 0 {
		t.Fatalf("initial count = %d, want 0", got)
	}

	idx1, release1, err := registerJoinedAgent(runID)
	if err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if idx1 != 1 {
		t.Fatalf("first index = %d, want 1", idx1)
	}
	if got := attachedCount(runID); got != 1 {
		t.Fatalf("count after 1 register = %d, want 1", got)
	}

	idx2, release2, err := registerJoinedAgent(runID)
	if err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if idx2 != 2 {
		t.Fatalf("second index = %d, want 2", idx2)
	}
	if got := attachedCount(runID); got != 2 {
		t.Fatalf("count after 2 registers = %d, want 2", got)
	}

	release1()
	if got := attachedCount(runID); got != 1 {
		t.Fatalf("count after 1 release = %d, want 1", got)
	}
	release2()
	if got := attachedCount(runID); got != 0 {
		t.Fatalf("count after 2 releases = %d, want 0", got)
	}
}

func TestAttachedAgents_PrunesDeadPid(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	const runID = "run_test_join_dead"

	dir := attachedAgentsDir(runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A pid that is essentially never alive.
	if err := os.WriteFile(dir+"/999999", []byte("999999"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := attachedCount(runID); got != 0 {
		t.Fatalf("count with only a dead pid = %d, want 0 (pruned)", got)
	}
	if _, err := os.Stat(dir + "/999999"); !os.IsNotExist(err) {
		t.Fatalf("dead pid entry should have been pruned")
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/run/ -run TestAttachedAgents -v`
Expected: FAIL — `undefined: attachedCount` / `registerJoinedAgent` / `attachedAgentsDir`.

- [ ] **Step 3: Implement the registry**

Create `internal/run/joined.go`:

```go
package run

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"

	"github.com/majorcontext/moat/internal/storage"
)

// attachedAgentsDir is where per-join liveness entries live for a run.
// One file per live joined agent, named by its pid.
func attachedAgentsDir(runID string) string {
	return filepath.Join(storage.DefaultBaseDir(), runID, "agents")
}

// pidAlive reports whether the given pid is a live process.
func pidAlive(pid int) bool {
	// Signal 0 performs error checking without actually sending a signal.
	return syscall.Kill(pid, 0) == nil
}

// registerJoinedAgent records a live joined agent for the run and returns its
// 1-based index (the count after registration) plus a release func that removes
// the entry. The count is for display only; it never affects teardown.
func registerJoinedAgent(runID string) (index int, release func(), err error) {
	dir := attachedAgentsDir(runID)
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return 0, nil, mkErr
	}
	pid := os.Getpid()
	entry := filepath.Join(dir, strconv.Itoa(pid))
	if wErr := os.WriteFile(entry, []byte(strconv.Itoa(pid)), 0o600); wErr != nil {
		return 0, nil, wErr
	}
	release = func() { _ = os.Remove(entry) }
	return attachedCount(runID), release, nil
}

// attachedCount returns the number of live joined agents for the run, pruning
// entries whose pid is no longer alive.
func attachedCount(runID string) int {
	dir := attachedAgentsDir(runID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	// Deterministic order keeps index assignment stable in tests.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	live := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil {
			continue
		}
		if pidAlive(pid) {
			live++
		} else {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return live
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/run/ -run TestAttachedAgents -v`
Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/run/joined.go internal/run/joined_test.go
git commit -m "feat(run): filesystem-backed attached-agent registry (display-only)"
```

---

### Task B2: `Manager.ExecInteractive` and count accessor

**Files:**
- Modify: `internal/run/manager.go` (add after `Exec`, ~line 3886)
- Test: `internal/run/manager_join_test.go` (new — validation paths only, no container)

- [ ] **Step 1: Write the failing test (validation paths)**

Create `internal/run/manager_join_test.go`:

```go
package run

import (
	"context"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/container"
)

func TestManagerExecInteractive_RunNotFound(t *testing.T) {
	m := &Manager{runs: map[string]*Run{}}
	err := m.ExecInteractive(context.Background(), "run_missing", []string{"claude"}, container.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v, want a 'not found' error", err)
	}
}

func TestManagerExecInteractive_NotRunning(t *testing.T) {
	r := &Run{ID: "run_stopped", State: StateStopped}
	m := &Manager{runs: map[string]*Run{"run_stopped": r}}
	err := m.ExecInteractive(context.Background(), "run_stopped", []string{"claude"}, container.ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("got %v, want a 'not running' error", err)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/run/ -run TestManagerExecInteractive -v`
Expected: FAIL — `m.ExecInteractive undefined`.

- [ ] **Step 3: Implement `ExecInteractive` and `AttachedCount`**

In `internal/run/manager.go`, after the `Exec` method, add:

```go
// ExecInteractive runs a command inside a running container with a PTY,
// streaming the provided opts. Used by `moat join` for interactive agents.
func (m *Manager) ExecInteractive(ctx context.Context, runID string, cmd []string, opts container.ExecOptions) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("run %s not found", runID)
	}
	containerID := r.ContainerID
	auditStore := r.AuditStore
	state := r.GetState()
	m.mu.RUnlock()

	if state != StateRunning {
		return fmt.Errorf("run %s is not running (state: %s)", runID, state)
	}

	rt, rtErr := m.runtimeForRun(r)
	if rtErr != nil {
		return fmt.Errorf("resolving runtime for run %s: %w", runID, rtErr)
	}

	execErr := rt.ExecInteractive(ctx, containerID, cmd, opts)

	if auditStore != nil {
		exitCode := 0
		var ee *container.ExecError
		if errors.As(execErr, &ee) {
			exitCode = ee.ExitCode
		}
		_, _ = auditStore.AppendExec(audit.ExecData{
			Command:  cmd,
			HasStdin: opts.Stdin != nil,
			ExitCode: exitCode,
		})
	}
	return execErr
}

// AttachedCount returns the number of live joined agents for a run (display-only).
func (m *Manager) AttachedCount(runID string) int {
	return attachedCount(runID)
}

// RegisterJoinedAgent records a joined agent for a run and returns its index and
// a release func. Display-only; never affects teardown.
func (m *Manager) RegisterJoinedAgent(runID string) (int, func(), error) {
	return registerJoinedAgent(runID)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/run/ -run TestManagerExecInteractive -v`
Expected: PASS for both.

- [ ] **Step 5: Commit**

```bash
git add internal/run/manager.go internal/run/manager_join_test.go
git commit -m "feat(run): Manager.ExecInteractive + attached-agent accessors"
```

---

## Phase C — Provider: joinable agent contract + claude implementation

### Task C1: Add the optional `JoinableAgent` interface

**Files:**
- Modify: `internal/provider/interfaces.go` (add near `AgentProvider`, line ~93)

- [ ] **Step 1: Add the interface and options type**

```go
// JoinOpts carries the parsed flags for a joined agent session.
type JoinOpts struct {
	Continue bool
	Resume   string
	Prompt   string
}

// JoinableAgent is implemented by agent providers that support `moat join` —
// launching a second instance of the agent into an existing run's container.
// It is optional: providers that don't implement it cannot be joined.
type JoinableAgent interface {
	// JoinCommand returns the in-container command to launch a joined session.
	JoinCommand(opts JoinOpts) ([]string, error)

	// IdentifiesAs reports whether a run whose recorded Agent field is `agent`
	// was created by this provider. This absorbs the difference between the
	// default provider name ("claude") and an explicit moat.yaml agent value
	// ("claude-code").
	IdentifiesAs(agent string) bool
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/provider/...`
Expected: builds clean.

- [ ] **Step 3: Commit**

```bash
git add internal/provider/interfaces.go
git commit -m "feat(provider): add optional JoinableAgent interface"
```

---

### Task C2: Implement `JoinableAgent` on the claude provider

**Files:**
- Create: `internal/providers/claude/join.go`
- Test: `internal/providers/claude/join_test.go`

The in-container command mirrors the `BuildCommand` closure in `runClaudeCode` (`cli.go`): `claude --dangerously-skip-permissions [--continue] [--resume <id>] [-p <prompt>]`. v1 keeps it simple — no `--resume` session-id translation (the raw id is passed through; that translation is a host-side convenience in the create path).

- [ ] **Step 1: Write the failing test**

Create `internal/providers/claude/join_test.go`:

```go
package claude

import (
	"reflect"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestOAuthProvider_JoinCommand(t *testing.T) {
	p := &OAuthProvider{}

	tests := []struct {
		name string
		opts provider.JoinOpts
		want []string
	}{
		{
			name: "bare",
			opts: provider.JoinOpts{},
			want: []string{"claude", "--dangerously-skip-permissions"},
		},
		{
			name: "continue",
			opts: provider.JoinOpts{Continue: true},
			want: []string{"claude", "--dangerously-skip-permissions", "--continue"},
		},
		{
			name: "resume",
			opts: provider.JoinOpts{Resume: "abc123"},
			want: []string{"claude", "--dangerously-skip-permissions", "--resume", "abc123"},
		},
		{
			name: "prompt",
			opts: provider.JoinOpts{Prompt: "hi"},
			want: []string{"claude", "--dangerously-skip-permissions", "-p", "hi"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.JoinCommand(tt.opts)
			if err != nil {
				t.Fatalf("JoinCommand: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("JoinCommand = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOAuthProvider_IdentifiesAs(t *testing.T) {
	p := &OAuthProvider{}
	for _, agent := range []string{"claude", "claude-code"} {
		if !p.IdentifiesAs(agent) {
			t.Errorf("IdentifiesAs(%q) = false, want true", agent)
		}
	}
	for _, agent := range []string{"codex", "gemini", ""} {
		if p.IdentifiesAs(agent) {
			t.Errorf("IdentifiesAs(%q) = true, want false", agent)
		}
	}
}

// Compile-time assertion that the provider satisfies JoinableAgent.
var _ provider.JoinableAgent = (*OAuthProvider)(nil)
```

> NOTE: confirm the concrete claude provider type name is `OAuthProvider` (it is, per `cli.go`'s `func (p *OAuthProvider) RegisterCLI`). If a constructor is required to make a usable value, use it; `JoinCommand`/`IdentifiesAs` must not depend on unset fields.

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/providers/claude/ -run 'TestOAuthProvider_(JoinCommand|IdentifiesAs)' -v`
Expected: FAIL — `p.JoinCommand undefined` / `p.IdentifiesAs undefined`.

- [ ] **Step 3: Implement the methods**

Create `internal/providers/claude/join.go`:

```go
package claude

import "github.com/majorcontext/moat/internal/provider"

// JoinCommand builds the in-container command for a joined claude session.
// Mirrors the BuildCommand closure in runClaudeCode (cli.go). Joined sessions
// always run with --dangerously-skip-permissions, matching the create-path
// default (the container provides isolation).
func (p *OAuthProvider) JoinCommand(opts provider.JoinOpts) ([]string, error) {
	cmd := []string{"claude", "--dangerously-skip-permissions"}
	if opts.Continue {
		cmd = append(cmd, "--continue")
	}
	if opts.Resume != "" {
		cmd = append(cmd, "--resume", opts.Resume)
	}
	if opts.Prompt != "" {
		cmd = append(cmd, "-p", opts.Prompt)
	}
	return cmd, nil
}

// IdentifiesAs reports whether a run with the given recorded Agent field was
// created by the claude provider. cfg.Agent defaults to the provider name
// ("claude") but is "claude-code" when set explicitly in moat.yaml.
func (p *OAuthProvider) IdentifiesAs(agent string) bool {
	return agent == "claude" || agent == "claude-code"
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/providers/claude/ -run 'TestOAuthProvider_(JoinCommand|IdentifiesAs)' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/claude/join.go internal/providers/claude/join_test.go
git commit -m "feat(claude): implement JoinableAgent (JoinCommand + IdentifiesAs)"
```

---

## Phase D — CLI: the `moat join` command

### Task D1: Command skeleton + run/provider resolution + validation

**Files:**
- Create: `cmd/moat/cli/join_cmd.go`
- Test: `cmd/moat/cli/join_cmd_test.go`

- [ ] **Step 1: Write the failing test (pure validation helper)**

Create `cmd/moat/cli/join_cmd_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

// fakeJoinable implements provider.JoinableAgent for validation tests.
type fakeJoinable struct{ identifies bool }

func (f fakeJoinable) JoinCommand(provider.JoinOpts) ([]string, error) { return []string{"x"}, nil }
func (f fakeJoinable) IdentifiesAs(string) bool                        { return f.identifies }

func TestValidateJoinAgent_OK(t *testing.T) {
	if err := validateJoinAgent(fakeJoinable{identifies: true}, "claude", "claude-code"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateJoinAgent_WrongProvider(t *testing.T) {
	err := validateJoinAgent(fakeJoinable{identifies: false}, "codex", "claude-code")
	if err == nil || !strings.Contains(err.Error(), "no codex configuration") {
		t.Fatalf("got %v, want a clear 'no codex configuration' error", err)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./cmd/moat/cli/ -run TestValidateJoinAgent -v`
Expected: FAIL — `validateJoinAgent undefined`.

- [ ] **Step 3: Implement the command + helper**

Create `cmd/moat/cli/join_cmd.go`:

```go
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/term"
)

var (
	joinContinue bool
	joinResume   string
	joinPrompt   string
)

var joinCmd = &cobra.Command{
	Use:   "join <run> <agent> [flags]",
	Short: "Launch another agent inside a running container",
	Long: `Launch a second agent inside an already-running container, reusing its
workspace, grants, and credentials — without creating a new container.

The agent must match the one the run was started with (v1 supports same-agent
joins, e.g. joining claude into a run started by 'moat claude').

Examples:
  moat join run_a1b2c3d4e5f6 claude
  moat join my-feature claude --continue
  moat join run_a1b2c3d4e5f6 claude -p "summarize the diff"`,
	Args: cobra.MinimumNArgs(2),
	RunE: runJoin,
}

func init() {
	joinCmd.Flags().BoolVarP(&joinContinue, "continue", "c", false, "continue the most recent conversation")
	joinCmd.Flags().StringVarP(&joinResume, "resume", "r", "", "resume a specific session by ID")
	joinCmd.Flags().StringVarP(&joinPrompt, "prompt", "p", "", "run with prompt (non-interactive)")
	rootCmd.AddCommand(joinCmd)
}

// validateJoinAgent checks that the run (whose recorded agent field is runAgent)
// was created by the requested provider. agentArg is the user-typed agent name,
// used only for the error message.
func validateJoinAgent(j provider.JoinableAgent, agentArg, runAgent string) error {
	if !j.IdentifiesAs(runAgent) {
		return fmt.Errorf("run has no %s configuration.\n"+
			"v1 join only attaches an agent the run was started with (run agent: %q).\n"+
			"To run %s here, start the run with %s configured.",
			agentArg, runAgent, agentArg, agentArg)
	}
	return nil
}

func runJoin(cmd *cobra.Command, args []string) error {
	if joinContinue && joinResume != "" {
		return fmt.Errorf("--continue and --resume are mutually exclusive")
	}

	runArg := args[0]
	agentArg := args[1]

	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runID, err := resolveRunArgSingle(manager, runArg)
	if err != nil {
		return err
	}

	r, gErr := manager.Get(runID)
	if gErr != nil {
		return gErr
	}
	if r.GetState() != run.StateRunning {
		return fmt.Errorf("run %s is not running (state: %s)", runID, r.GetState())
	}

	agent := provider.GetAgent(agentArg)
	if agent == nil {
		return fmt.Errorf("unknown agent %q", agentArg)
	}
	joinable, ok := agent.(provider.JoinableAgent)
	if !ok {
		return fmt.Errorf("agent %q does not support join yet", agentArg)
	}
	if err := validateJoinAgent(joinable, agentArg, r.Agent); err != nil {
		return err
	}

	containerCmd, err := joinable.JoinCommand(provider.JoinOpts{
		Continue: joinContinue,
		Resume:   joinResume,
		Prompt:   joinPrompt,
	})
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Headless (--prompt with no TTY) vs interactive.
	if joinPrompt != "" || !term.IsTerminal(os.Stdin) || !term.IsTerminal(os.Stdout) {
		execErr := manager.ExecInteractive(ctx, runID, containerCmd, container.ExecOptions{
			Stdin:  nil,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			TTY:    false,
		})
		return handleJoinExecErr(manager, execErr)
	}

	return runJoinInteractive(ctx, manager, r, containerCmd)
}

func handleJoinExecErr(manager *run.Manager, execErr error) error {
	if execErr == nil {
		return nil
	}
	var ee *container.ExecError
	if errors.As(execErr, &ee) {
		manager.Close()
		os.Exit(ee.ExitCode)
	}
	return execErr
}
```

> NOTE: add `"errors"` to imports (matches `exec_cmd.go`'s `errors.As` exit-code handling). `manager.Get(runID)` returns `(*Run, error)`. `runJoinInteractive` is implemented in Task D2; to keep this task compiling, add a temporary stub at the bottom of the file:
>
> ```go
> func runJoinInteractive(ctx context.Context, manager *run.Manager, r *run.Run, cmd []string) error {
> 	return fmt.Errorf("interactive join not implemented")
> }
> ```
>
> `manager.Get(runID)` is confirmed at `internal/run/manager.go:3362` with signature `Get(runID string) (*Run, error)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/moat/cli/ -run TestValidateJoinAgent -v`
Expected: PASS.

- [ ] **Step 5: Build the whole CLI**

Run: `go build ./cmd/...`
Expected: builds clean (with the `runJoinInteractive` stub).

- [ ] **Step 6: Commit**

```bash
git add cmd/moat/cli/join_cmd.go cmd/moat/cli/join_cmd_test.go
git commit -m "feat(cli): moat join command — resolution, validation, headless path"
```

---

### Task D2: Interactive join loop

**Files:**
- Modify: `cmd/moat/cli/join_cmd.go` (replace the `runJoinInteractive` stub)

A trimmed version of `RunInteractiveAttached` (exec.go): raw mode, status footer, SIGWINCH → a resize channel feeding `container.ExecOptions.Resize`, and `manager.ExecInteractive` instead of `StartAttached`. Registers the join in the attached-agent registry for the footer count. No snapshot/trace/clipboard machinery in v1.

- [ ] **Step 1: Replace the stub**

```go
func runJoinInteractive(ctx context.Context, manager *run.Manager, r *run.Run, command []string) error {
	// Register this join for the display-only attached count.
	index, release, err := manager.RegisterJoinedAgent(r.ID)
	if err == nil {
		defer release()
	}

	// Raw mode so keystrokes reach the agent unbuffered.
	var rawState *term.RawModeState
	if term.IsTerminal(os.Stdin) {
		if rs, rErr := term.EnableRawMode(os.Stdin); rErr == nil {
			rawState = rs
		}
	}
	defer func() {
		if rawState != nil {
			_ = term.RestoreTerminal(rawState)
		}
	}()

	// Status footer: this is a joined session.
	statusWriter, statusCleanup, stdout := setupJoinStatusBar(manager, r, index)
	defer statusCleanup()

	// Resize channel fed by SIGWINCH; closed on exit so the runtime goroutine ends.
	resize := make(chan container.TTYSize, 1)
	var initialW, initialH uint
	if term.IsTerminal(os.Stdout) {
		if w, h := term.GetSize(os.Stdout); w > 0 && h > 0 {
			// Reserve the footer row.
			ch := containerTTYHeight(statusWriter, h)
			initialW, initialH = uint(w), uint(ch) // #nosec G115
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		for sig := range sigCh {
			if sig != syscall.SIGWINCH {
				continue
			}
			if statusWriter != nil && term.IsTerminal(os.Stdout) {
				if w, h := term.GetSize(os.Stdout); w > 0 && h > 0 {
					_ = statusWriter.Resize(w, h)
					ch := containerTTYHeight(statusWriter, h)
					select {
					case resize <- container.TTYSize{Width: uint(w), Height: uint(ch)}: // #nosec G115
					default:
					}
				}
			}
		}
	}()

	execErr := manager.ExecInteractive(execCtx, r.ID, command, container.ExecOptions{
		Stdin:         os.Stdin,
		Stdout:        stdout,
		Stderr:        os.Stderr,
		TTY:           true,
		InitialWidth:  initialW,
		InitialHeight: initialH,
		Resize:        resize,
	})
	close(resize)

	if execErr != nil && ctx.Err() == nil {
		var ee *container.ExecError
		if errors.As(execErr, &ee) {
			// Agent exited non-zero; surface it but don't treat as a moat error.
			fmt.Printf("\r\nJoined agent exited (code %d)\r\n", ee.ExitCode)
			return nil
		}
		return fmt.Errorf("join failed: %w", execErr)
	}
	fmt.Printf("\r\nJoined agent session ended\r\n")
	return nil
}
```

> NOTE: add imports `"os/signal"`, `"syscall"`, `"errors"`. `containerTTYHeight` already exists in `cmd/moat/cli/exec.go` (same package) — reuse it. `setupJoinStatusBar` is implemented in Task E2; to keep this task compiling, add a temporary stub:
>
> ```go
> func setupJoinStatusBar(manager *run.Manager, r *run.Run, index int) (*tui.Writer, func(), io.Writer) {
> 	return nil, func() {}, os.Stdout
> }
> ```
> and add imports `"io"` and the `tui` package. (Task E2 replaces the stub with the real footer.)

- [ ] **Step 2: Build**

Run: `go build ./cmd/...`
Expected: builds clean.

- [ ] **Step 3: Vet**

Run: `go vet ./cmd/...`
Expected: no findings.

- [ ] **Step 4: Commit**

```bash
git add cmd/moat/cli/join_cmd.go
git commit -m "feat(cli): interactive join loop (PTY, resize, footer hook)"
```

---

## Phase E — Status footer: session role + count

### Task E1: StatusBar session fields and rendering

**Files:**
- Modify: `internal/tui/statusbar.go`
- Test: `internal/tui/statusbar_test.go`

Add a session segment prepended to the right cluster (before run-id): primary shows `+N` only when N>0; joined shows `joined · N`. Color-accented; dropped late in the truncation cascade.

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/statusbar_test.go`:

```go
func TestStatusBar_SessionSegment(t *testing.T) {
	// Joined session shows "joined · N" before the run-id.
	bar := NewStatusBar("run_123", "happy-otter", "apple")
	bar.SetDimensions(120, 24)
	bar.SetSession("joined · 2")
	out := stripANSI(bar.Content())
	if !strings.Contains(out, "joined · 2") {
		t.Fatalf("joined session segment missing: %q", out)
	}
	if strings.Index(out, "joined · 2") > strings.Index(out, "run_123") {
		t.Fatalf("session segment should appear before the run-id: %q", out)
	}

	// Primary with joins shows "+2"; primary solo shows neither.
	primary := NewStatusBar("run_123", "happy-otter", "apple")
	primary.SetDimensions(120, 24)
	primary.SetJoinedCount(2)
	if !strings.Contains(stripANSI(primary.Content()), "+2") {
		t.Fatalf("primary +2 count missing")
	}

	solo := NewStatusBar("run_123", "happy-otter", "apple")
	solo.SetDimensions(120, 24)
	if strings.Contains(stripANSI(solo.Content()), "+0") {
		t.Fatalf("solo primary should not show a +0 count")
	}
}
```

> NOTE: if a `stripANSI` helper doesn't already exist in `statusbar_test.go`, add one:
> ```go
> func stripANSI(s string) string {
> 	re := regexp.MustCompile("\x1b\\[[0-9;]*m")
> 	return re.ReplaceAllString(s, "")
> }
> ```
> with imports `"regexp"`, `"strings"`. Check the existing test file first — it likely already has a similar helper; reuse it rather than duplicating.

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/tui/ -run TestStatusBar_SessionSegment -v`
Expected: FAIL — `bar.SetSession undefined` / `bar.SetJoinedCount undefined`.

- [ ] **Step 3: Implement the fields, setters, color, and rendering**

In `internal/tui/statusbar.go`:

(a) Add fields to the struct (after `warning string`):

```go
	session     string // explicit session role for a joined session, e.g. "joined · 2"
	joinedCount int    // number of joined agents (shown as "+N" on the primary)
```

(b) Add a color constant (in the ANSI `const` block):

```go
	fgGreen = "\x1b[32m"
```

(c) Add setters (near `SetWarning`):

```go
// SetSession sets an explicit session-role label shown before the run-id
// (e.g. "joined · 2"). Empty means a primary session.
func (s *StatusBar) SetSession(label string) {
	s.session = label
}

// SetJoinedCount sets the number of joined agents, rendered as "+N" before the
// run-id on a primary session. Zero renders nothing.
func (s *StatusBar) SetJoinedCount(n int) {
	s.joinedCount = n
}

// sessionPlain returns the plain-text session segment (with a trailing space),
// or "" when there is nothing to show.
func (s *StatusBar) sessionPlain() string {
	if s.session != "" {
		return s.session + " "
	}
	if s.joinedCount > 0 {
		return fmt.Sprintf("+%d ", s.joinedCount)
	}
	return ""
}
```

(d) In `buildContent`, thread a `showSession` flag. Change the signature and the right-segment construction. Replace the right-segment plain build:

```go
	// Build right segment with optional hint
	var rightPlain string
	hintPlain := ""
	if showHint {
		hintPlain = " (ctrl+/)"
	}

	sessionSeg := ""
	if showSession {
		sessionSeg = s.sessionPlain()
	}

	if showName && s.name != "" {
		rightPlain = fmt.Sprintf(" %s%s · %s%s ", sessionSeg, s.runID, s.name, hintPlain)
	} else {
		rightPlain = fmt.Sprintf(" %s%s%s ", sessionSeg, s.runID, hintPlain)
	}
```

Update the function signature to `buildContent(showGrants, showName, showHint, showWarning, showSession bool)` and the `Content()` call to `s.buildContent(true, true, true, true, true)`. In the truncation cascade, drop the session segment LAST among right-side items (after name):

```go
	if totalPlain > s.width {
		if showHint {
			return s.buildContent(showGrants, showName, false, showWarning, showSession)
		}
		if showGrants && len(s.grants) > 0 {
			return s.buildContent(false, showName, false, showWarning, showSession)
		}
		if showName && s.name != "" {
			return s.buildContent(false, false, false, showWarning, showSession)
		}
		if showSession && s.sessionPlain() != "" {
			return s.buildContent(false, false, false, showWarning, false)
		}
		if showWarning && s.warning != "" {
			return s.buildContent(false, false, false, false, false)
		}
		if runeLen(leftPlain)+runeLen(rightPlain) > s.width {
			return bgDarkGray + strings.Repeat(" ", s.width) + reset
		}
	}
```

(e) In the styled right-segment build, color the session segment. Replace the styled right build:

```go
	// Build right segment with styling
	buf.WriteString(" ")
	if showSession {
		if seg := s.sessionPlain(); seg != "" {
			buf.WriteString(fgGreen)
			buf.WriteString(strings.TrimRight(seg, " "))
			buf.WriteString(reset)
			buf.WriteString(bgDarkGray)
			buf.WriteString(" ")
		}
	}
	if showName && s.name != "" {
		buf.WriteString(fmt.Sprintf("%s · %s", s.runID, s.name))
	} else {
		buf.WriteString(s.runID)
	}
```

> NOTE: this replaces the prior `buf.WriteString(fmt.Sprintf(" %s · %s", s.runID, s.name))` block. Keep the subsequent hint/space/reset writes unchanged. Because the styled and plain builders must agree on width, ensure the leading/trailing spaces match `rightPlain` exactly (one leading space, session segment + trailing space, then run-id). Run the test to confirm alignment isn't broken.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS — the new test plus all existing statusbar tests (alignment unchanged when no session is set).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/statusbar.go internal/tui/statusbar_test.go
git commit -m "feat(tui): status footer session role + joined count"
```

---

### Task E2: Wire the footer into the join loop (and count into the primary)

**Files:**
- Modify: `cmd/moat/cli/join_cmd.go` (replace the `setupJoinStatusBar` stub)
- Modify: `cmd/moat/cli/exec.go` (`setupStatusBar` + the interactive loop: show live joined count on the primary)

- [ ] **Step 1: Implement `setupJoinStatusBar`**

Replace the stub in `join_cmd.go`:

```go
func setupJoinStatusBar(manager *run.Manager, r *run.Run, index int) (*tui.Writer, func(), io.Writer) {
	stdout := io.Writer(os.Stdout)
	cleanup := func() {}
	if !term.IsTerminal(os.Stdout) {
		return nil, cleanup, stdout
	}
	width, height := term.GetSize(os.Stdout)
	if width <= 0 || height <= 0 {
		return nil, cleanup, stdout
	}
	runtimeType := r.Runtime
	if runtimeType == "" {
		runtimeType = manager.RuntimeType()
	}
	bar := tui.NewStatusBar(r.ID, r.Name, runtimeType)
	bar.SetGrants(r.Grants)
	bar.SetSession(fmt.Sprintf("joined · %d", index))
	bar.SetDimensions(width, height)
	writer := tui.NewWriter(os.Stdout, bar, runtimeType)
	if err := writer.Setup(); err != nil {
		return nil, cleanup, os.Stdout
	}
	_ = os.Stdout.Sync()
	cleanup = func() { _ = writer.Cleanup() }
	return writer, cleanup, writer
}
```

- [ ] **Step 2: Show the live joined count on the primary**

In `cmd/moat/cli/exec.go` `setupStatusBar`, after `bar.SetGrants(r.Grants)`, add:

```go
	bar.SetJoinedCount(manager.AttachedCount(r.ID))
```

And in `RunInteractiveAttached`, inside the `SIGWINCH` handling branch (where `statusWriter.Resize` is already called), refresh the count so it tracks joins arriving/leaving on redraw:

```go
				// Refresh joined-agent count on redraw (display-only).
				// (Place alongside the existing statusWriter.Resize call.)
```

> Implementation note: the `StatusBar` is owned by the `tui.Writer`; to update the count live, add a tiny pass-through on the writer if one isn't present (e.g. `statusWriter.SetJoinedCount(n)` that forwards to the bar), OR re-create/refresh on SIGWINCH. Check `internal/tui/writer.go` for an existing accessor pattern (it already forwards `Resize` and `SetMessage`-style calls). If adding a forwarder is non-trivial, ship E2 with the count captured at startup only and open a follow-up for live refresh — the join's own `joined · N` footer is the must-have; the primary's live `+N` is the nice-to-have per the design doc.

- [ ] **Step 3: Build + test**

Run: `go build ./... && go test ./internal/tui/ ./cmd/moat/cli/ -v`
Expected: builds clean; tests PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/moat/cli/join_cmd.go cmd/moat/cli/exec.go
git commit -m "feat(cli): wire join/primary status footer (session role + count)"
```

---

## Phase F — Split-console capture for joins

### Task F1: Tee a join's console to its own log file

**Files:**
- Modify: `cmd/moat/cli/join_cmd.go` (`runJoinInteractive` and the headless path)

Per the design, console is split per agent: the join writes to `<run-dir>/logs.<index>.jsonl` rather than interleaving into the primary's `logs.jsonl`. Reuse the timestamped `storage.LogWriter` pattern, but with an indexed filename.

- [ ] **Step 1: Add an indexed log-writer accessor to storage**

In `internal/storage/storage.go`, add alongside `LogWriter()`:

```go
// JoinLogWriter returns a timestamped writer for a joined agent's console,
// written to logs.<index>.jsonl so it stays separate from the primary's log.
func (s *RunStore) JoinLogWriter(index int) (*LogWriter, error) {
	f, err := os.OpenFile(
		filepath.Join(s.dir, fmt.Sprintf("logs.%d.jsonl", index)),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		return nil, fmt.Errorf("opening join log file: %w", err)
	}
	return &LogWriter{file: f}, nil
}
```

> NOTE: `fmt` is already imported by storage.go. Add a focused unit test `TestRunStore_JoinLogWriter` mirroring any existing `LogWriter` test (write a line, read back the file, assert the JSON `line` field and the `logs.<index>.jsonl` filename).

- [ ] **Step 2: Tee join output to the indexed log**

In `runJoinInteractive`, after computing `stdout` from `setupJoinStatusBar`, wrap it:

```go
	if r.Store != nil {
		if lw, lerr := r.Store.JoinLogWriter(index); lerr == nil {
			defer lw.Close()
			stdout = io.MultiWriter(stdout, lw)
		}
	}
```

For the headless path in `runJoin`, do the same around `os.Stdout` using the registered index (register a join there too so headless sessions also get an index and count). Refactor so both paths register the join and obtain `index` before building `ExecOptions`.

> NOTE: confirm a loaded run exposes `r.Store` (`*storage.RunStore`). If `r.Store` is nil for runs reconstructed in a separate `moat join` process, construct one with `storage.NewRunStore(storage.DefaultBaseDir(), r.ID)`.

- [ ] **Step 3: Build + test**

Run: `go build ./... && go test ./internal/storage/ -run TestRunStore_JoinLogWriter -v`
Expected: builds clean; test PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/storage/storage.go internal/storage/storage_test.go cmd/moat/cli/join_cmd.go
git commit -m "feat(run): split-console capture for joined agents (logs.N.jsonl)"
```

---

## Phase G — E2E, docs, changelog

### Task G1: E2E test for join (interactive + headless)

**Files:**
- Create: `internal/e2e/join_test.go`

> Requires a container runtime; runs under `-tags=e2e`, not in unit CI. Mirror an existing e2e test's harness (look at `internal/e2e/` for the helper that starts a `moat claude`-style run and the assertion helpers).

- [ ] **Step 1: Write the E2E test**

```go
//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestJoinHeadless starts a run, joins a headless claude agent with a prompt,
// and asserts the join runs in the SAME container (no new container, no new
// proxy registration) and the original run still owns teardown.
func TestJoinHeadless(t *testing.T) {
	// 1. Start a long-lived claude run (e.g. `moat claude -- sleep`-style harness,
	//    or a run whose primary stays up) and capture its run ID and container ID.
	// 2. Run: moat join <run> claude -p "echo HELLO_FROM_JOIN"
	//    Assert exit code 0 and output contains the expected marker.
	// 3. Assert container count did not increase (same container ID services the join).
	// 4. Assert no new proxy run registration was created for the join.
	// 5. Stop the original run; assert the container tears down.
	t.Skip("fill in against the existing e2e harness helpers in internal/e2e")
	_ = strings.TrimSpace
}
```

> NOTE: this is a scaffold — wire it to the real `internal/e2e` harness (start-run helper, container-list helper, run-registration assertion). Remove the `t.Skip` once wired. The assertions that matter: (a) same container, (b) no new proxy registration, (c) original-owns-teardown.

- [ ] **Step 2: Build the e2e tag**

Run: `go test -tags=e2e -run TestJoinHeadless ./internal/e2e/ -v` (on a host with a runtime)
Expected: compiles; skips (until wired) or passes.

- [ ] **Step 3: Commit**

```bash
git add internal/e2e/join_test.go
git commit -m "test(e2e): scaffold join headless/interactive e2e"
```

---

### Task G2: Documentation + changelog

**Files:**
- Modify: `docs/content/reference/01-cli.md` (add `moat join`)
- Modify: a guide under `docs/content/guides/` (multi-agent / joining a second agent) — add a short section, or create `docs/content/guides/NN-multi-agent.md` following the folder's numbering
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add the CLI reference entry**

Document `moat join <run> <agent> [flags]` with the `--continue/-c`, `--resume/-r`, `--prompt/-p` flags, the same-agent v1 constraint, and the `join : exec :: claude : run` relationship. Mirror the existing `moat exec` entry's structure/tone.

- [ ] **Step 2: Add/extend a guide**

Show the workflow: start `moat claude`, then in another terminal `moat join <run> claude` to get a second agent in the same workspace; note the footer shows `joined · N` and the primary shows `+N`; note the lifecycle (original owns the container) and the v1 same-agent constraint.

- [ ] **Step 3: Add the changelog entry**

Under the unreleased section, in `### Added`:

```markdown
- **`moat join`** — launch a second agent inside an already-running container,
  reusing its workspace, grants, and credentials without a new container. v1
  supports same-agent joins (e.g. joining claude into a `moat claude` run). The
  status footer shows the session role and joined-agent count.
  ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

> NOTE: per CLAUDE.md, commit with the issue link first if there is one, then swap to `/pull/NNN` after the PR is opened.

- [ ] **Step 4: Verify docs build/links (if a docs check exists) and run the full suite**

Run: `make lint && make test-unit`
Expected: lint clean (gofmt/vet/golangci-lint), all unit tests pass.

- [ ] **Step 5: Commit**

```bash
git add docs/ CHANGELOG.md
git commit -m "docs: document moat join (CLI reference, guide, changelog)"
```

---

## Final verification

- [ ] **Run the full unit suite with the race detector**

Run: `make test-unit`
Expected: all packages pass.

- [ ] **Lint**

Run: `make lint`
Expected: no issues (gofmt, vet, golangci-lint; remember "canceled" not "cancelled").

- [ ] **Manual smoke test (on a host with a runtime)**

```bash
# terminal 1
moat claude
# note the run id, e.g. run_abcdef123456

# terminal 2 — interactive join
moat join run_abcdef123456 claude
#   → second claude TUI in the same workspace; footer shows "joined · 1"
#   → terminal 1 footer shows "+1"

# terminal 3 — headless join
moat join run_abcdef123456 claude -p "print the current branch"
#   → prints output, exits 0

# wrong-agent error
moat join run_abcdef123456 codex
#   → clear error: "run has no codex configuration ..."

# stop the original; joins are torn down with the container
moat stop run_abcdef123456
```

---

## Notes for the implementer

- **Lifecycle:** never make the attached count drive teardown. The original agent owns the container (design Decision 2); joins are exec children that die with it.
- **Same-provider only (v1):** cross-provider join, container-side worktree (`moat join … --wt`), and persistent sessions are explicitly deferred — see the design doc's "Future extensions". Don't build them here.
- **Network/audit interleaving is automatic:** joins share the run's proxy token, so their traffic already attributes to the run. No proxy/daemon changes — this preserves the daemon backwards-compatibility contract.
- **Daemon API untouched:** `moat join` adds no daemon endpoints or fields.
```
