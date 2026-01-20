//go:build linux

package trace

import (
	"testing"
)

// TestProcConnectorTracerCleanupStalePIDs tests that stale PIDs are removed
// from trackedPIDs when they no longer exist in /proc.
func TestProcConnectorTracerCleanupStalePIDs(t *testing.T) {
	tracer := &ProcConnectorTracer{
		trackedPIDs: make(map[int]bool),
	}

	// Add some PIDs - use PID 1 (init, always exists) and a fake high PID
	tracer.trackedPIDs[1] = true           // Should survive cleanup (exists)
	tracer.trackedPIDs[999999999] = true   // Should be removed (doesn't exist)

	// Run cleanup
	tracer.cleanupStalePIDs()

	// PID 1 should still be tracked
	if !tracer.trackedPIDs[1] {
		t.Error("PID 1 should still be tracked after cleanup")
	}

	// Fake PID should be removed
	if tracer.trackedPIDs[999999999] {
		t.Error("non-existent PID should have been removed by cleanup")
	}
}

// TestProcConnectorTracerCleanupUpdatesTimestamp tests that cleanup updates lastCleanup.
func TestProcConnectorTracerCleanupUpdatesTimestamp(t *testing.T) {
	tracer := &ProcConnectorTracer{
		trackedPIDs: make(map[int]bool),
	}

	if !tracer.lastCleanup.IsZero() {
		t.Error("lastCleanup should be zero initially")
	}

	tracer.cleanupStalePIDs()

	if tracer.lastCleanup.IsZero() {
		t.Error("lastCleanup should be set after cleanup")
	}
}
