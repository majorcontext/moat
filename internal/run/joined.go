package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/majorcontext/moat/internal/storage"
)

// attachedAgentsDir is where per-join liveness entries live for a run.
// One file per live joined agent, named by its unique index (1, 2, 3, …).
// The file content is the pid of the agent that claimed the slot.
func attachedAgentsDir(runID string) string {
	return filepath.Join(storage.DefaultBaseDir(), runID, "agents")
}

// pidAlive reports whether the given pid is a live process.
func pidAlive(pid int) bool {
	// Signal 0 performs error checking without actually sending a signal.
	return syscall.Kill(pid, 0) == nil
}

// entryAlive reports whether the entry at path refers to a live process.
// The file content must be a decimal pid string.
func entryAlive(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return false
	}
	return pidAlive(pid)
}

// pruneDead removes entries from dir whose process is no longer alive.
// Errors are ignored (best-effort).
func pruneDead(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !entryAlive(path) {
			_ = os.Remove(path)
		}
	}
}

// maxJoinIndex is the upper bound for the O_EXCL index scan. In practice
// there will never be more than a handful of concurrent joins per run.
const maxJoinIndex = 4096

// registerJoinedAgent records a live joined agent for the run and returns its
// unique 1-based index plus a release func that removes the entry. The index
// is the lowest free integer filename: it is stable and unique for the
// duration of the agent's lifetime.
func registerJoinedAgent(runID string) (index int, release func(), err error) {
	dir := attachedAgentsDir(runID)
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return 0, nil, mkErr
	}
	pruneDead(dir)

	pid := strconv.Itoa(os.Getpid())

	for n := 1; n < maxJoinIndex; n++ {
		path := filepath.Join(dir, strconv.Itoa(n))
		f, openErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr != nil {
			if os.IsExist(openErr) {
				continue // slot taken; try the next one
			}
			return 0, nil, openErr
		}
		_, writeErr := f.WriteString(pid)
		closeErr := f.Close()
		if writeErr != nil {
			_ = os.Remove(path)
			return 0, nil, writeErr
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return 0, nil, closeErr
		}
		release = func() { _ = os.Remove(path) }
		return n, release, nil
	}
	return 0, nil, fmt.Errorf("registerJoinedAgent: exhausted %d index slots for run %s", maxJoinIndex, runID)
}

// attachedCount returns the number of live joined agents for the run, pruning
// entries whose pid is no longer alive.
func attachedCount(runID string) int {
	dir := attachedAgentsDir(runID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	live := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if entryAlive(path) {
			live++
		} else {
			_ = os.Remove(path)
		}
	}
	return live
}
