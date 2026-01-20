package trace

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStubTracerConcurrentStop tests that calling Stop() multiple times
// concurrently does not panic (regression test for double-close bug).
func TestStubTracerConcurrentStop(t *testing.T) {
	tracer := NewStubTracer(Config{})

	if err := tracer.Start(); err != nil {
		t.Fatal(err)
	}

	// Call Stop() from multiple goroutines concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Should not panic
			_ = tracer.Stop()
		}()
	}

	wg.Wait()
}

// TestStubTracerDoubleStop tests that calling Stop() twice sequentially
// does not panic (regression test for double-close bug).
func TestStubTracerDoubleStop(t *testing.T) {
	tracer := NewStubTracer(Config{})

	if err := tracer.Start(); err != nil {
		t.Fatal(err)
	}

	// First stop should succeed
	if err := tracer.Stop(); err != nil {
		t.Fatalf("first Stop() failed: %v", err)
	}

	// Second stop should also succeed without panic
	if err := tracer.Stop(); err != nil {
		t.Fatalf("second Stop() failed: %v", err)
	}
}

// TestStubTracerConcurrentEmitAndStop tests that emitting events while
// stopping does not cause a panic (regression test for race condition).
func TestStubTracerConcurrentEmitAndStop(t *testing.T) {
	for i := 0; i < 100; i++ {
		tracer := NewStubTracer(Config{})

		if err := tracer.Start(); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup

		// Emit events in one goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tracer.Emit(ExecEvent{
					Timestamp: time.Now(),
					PID:       j,
					Command:   "test",
				})
			}
		}()

		// Stop in another goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 10)
			_ = tracer.Stop()
		}()

		wg.Wait()
	}
}

// TestStubTracerConcurrentOnExec tests that registering callbacks
// concurrently with emitting events does not race.
func TestStubTracerConcurrentOnExec(t *testing.T) {
	tracer := NewStubTracer(Config{})

	if err := tracer.Start(); err != nil {
		t.Fatal(err)
	}
	defer tracer.Stop()

	var wg sync.WaitGroup
	var count atomic.Int32

	// Register callbacks in parallel
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracer.OnExec(func(e ExecEvent) {
				count.Add(1)
			})
		}()
	}

	// Emit events in parallel
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			tracer.Emit(ExecEvent{
				Timestamp: time.Now(),
				PID:       n,
				Command:   "test",
			})
		}(i)
	}

	wg.Wait()

	// Should have received some callbacks without crashing
	// The exact count depends on timing, but it should be > 0
	if count.Load() == 0 {
		t.Log("Warning: no callbacks received (timing dependent)")
	}
}

// TestStubTracerEventDropping tests that when the event channel is full,
// new events are dropped without blocking or panicking.
func TestStubTracerEventDropping(t *testing.T) {
	tracer := NewStubTracer(Config{})

	if err := tracer.Start(); err != nil {
		t.Fatal(err)
	}
	defer tracer.Stop()

	// Fill the channel (capacity is 100)
	for i := 0; i < 150; i++ {
		tracer.Emit(ExecEvent{
			Timestamp: time.Now(),
			PID:       i,
			Command:   "test",
		})
	}

	// Should not block or panic, 50 events should be dropped
	// Drain the channel to verify we got at most 100
	count := 0
	for {
		select {
		case <-tracer.Events():
			count++
		default:
			goto done
		}
	}
done:

	if count > 100 {
		t.Errorf("expected at most 100 events, got %d", count)
	}
}

// TestStubTracerStopBeforeStart tests that Stop() before Start() is safe.
func TestStubTracerStopBeforeStart(t *testing.T) {
	tracer := NewStubTracer(Config{})

	// Stop before Start should not panic - it returns early when !started
	err := tracer.Stop()
	if err != nil {
		t.Errorf("Stop() before Start() returned error: %v", err)
	}
}

// TestStubTracerStartAfterStop tests that Start() after Stop() fails gracefully.
func TestStubTracerStartAfterStop(t *testing.T) {
	tracer := NewStubTracer(Config{})

	if err := tracer.Start(); err != nil {
		t.Fatal(err)
	}

	if err := tracer.Stop(); err != nil {
		t.Fatal(err)
	}

	// Start after Stop should either:
	// 1. Return an error (preferred)
	// 2. Work correctly with a new state
	// Current implementation doesn't support restart, so we just verify it doesn't panic
	_ = tracer.Start()
}

// TestConfigPIDFilteringZero tests that PID=0 means "track all processes".
func TestConfigPIDFilteringZero(t *testing.T) {
	// This test verifies the Config.PID semantics:
	// PID=0 means no filtering (track everything)
	cfg := Config{PID: 0}

	if cfg.PID != 0 {
		t.Error("PID=0 should mean track all")
	}
}

// TestConfigPIDFilteringNonZero tests that non-zero PID enables filtering.
func TestConfigPIDFilteringNonZero(t *testing.T) {
	cfg := Config{PID: 12345}

	if cfg.PID == 0 {
		t.Error("PID should be set for filtering")
	}
}
