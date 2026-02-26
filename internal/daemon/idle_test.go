package daemon

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestIdleTimer_Fires(t *testing.T) {
	var fired atomic.Bool
	done := make(chan struct{})

	timer := NewIdleTimer(50*time.Millisecond, func() {
		fired.Store(true)
		close(done)
	})
	timer.Reset()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for idle timer to fire")
	}

	if !fired.Load() {
		t.Error("expected callback to have fired")
	}
}

func TestIdleTimer_Cancel(t *testing.T) {
	var fired atomic.Bool

	timer := NewIdleTimer(50*time.Millisecond, func() {
		fired.Store(true)
	})
	timer.Reset()
	timer.Cancel()

	// Wait long enough for the timer to have fired if it wasn't canceled.
	time.Sleep(150 * time.Millisecond)

	if fired.Load() {
		t.Error("callback should not fire after Cancel")
	}
}

func TestIdleTimer_ResetExtends(t *testing.T) {
	var count atomic.Int32
	done := make(chan struct{})

	timer := NewIdleTimer(100*time.Millisecond, func() {
		count.Add(1)
		close(done)
	})
	timer.Reset()

	// Reset before the first timer would fire.
	time.Sleep(60 * time.Millisecond)
	timer.Reset()

	// At this point, if Reset didn't extend, the callback would fire at ~100ms.
	// With the extension, it should fire at ~160ms from start.
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for idle timer to fire")
	}

	if c := count.Load(); c != 1 {
		t.Errorf("expected callback to fire exactly once, got %d", c)
	}
}

func TestIdleTimer_ResetAfterFire(t *testing.T) {
	var count atomic.Int32
	done1 := make(chan struct{})
	done2 := make(chan struct{})

	first := true
	timer := NewIdleTimer(50*time.Millisecond, func() {
		count.Add(1)
		if first {
			first = false
			close(done1)
		} else {
			close(done2)
		}
	})

	// First fire.
	timer.Reset()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first fire")
	}

	// Reset should allow it to fire again.
	timer.Reset()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second fire")
	}

	if c := count.Load(); c != 2 {
		t.Errorf("expected callback to fire twice, got %d", c)
	}
}

func TestIdleTimer_CancelBeforeReset(t *testing.T) {
	// Calling Cancel on a timer that was never started should not panic.
	timer := NewIdleTimer(50*time.Millisecond, func() {
		t.Error("callback should not fire")
	})
	timer.Cancel() // should not panic
}
