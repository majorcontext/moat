package run

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/majorcontext/moat/internal/storage"
)

// attachedAgentsDir is where per-join liveness entries live for a run.
// One file per live joined agent, named by its pid (or pid-N for multiple
// registrations from the same process).
func attachedAgentsDir(runID string) string {
	return filepath.Join(storage.DefaultBaseDir(), runID, "agents")
}

// pidAlive reports whether the given pid is a live process.
func pidAlive(pid int) bool {
	// Signal 0 performs error checking without actually sending a signal.
	return syscall.Kill(pid, 0) == nil
}

// entryPID extracts the PID from an agent entry filename.
// Filenames are either plain pids ("12345") or pid-N pairs ("12345-2").
func entryPID(name string) (int, bool) {
	part := name
	if idx := strings.IndexByte(name, '-'); idx >= 0 {
		part = name[:idx]
	}
	pid, err := strconv.Atoi(part)
	if err != nil {
		return 0, false
	}
	return pid, true
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
	pidStr := strconv.Itoa(pid)

	// Find a unique filename for this registration.
	// Use "pid" if no prior entry exists, otherwise "pid-N" for N=2,3,...
	name := pidStr
	if _, statErr := os.Stat(filepath.Join(dir, name)); statErr == nil {
		// Collision: find next available suffix.
		for n := 2; ; n++ {
			candidate := fmt.Sprintf("%s-%d", pidStr, n)
			if _, statErr2 := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(statErr2) {
				name = candidate
				break
			}
		}
	}

	entry := filepath.Join(dir, name)
	if wErr := os.WriteFile(entry, []byte(pidStr), 0o600); wErr != nil {
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
		pid, ok := entryPID(e.Name())
		if !ok {
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
