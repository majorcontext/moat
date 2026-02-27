package daemon

import (
	"sync"
	"time"
)

// IdleTimer triggers a callback after a period of inactivity.
type IdleTimer struct {
	duration time.Duration
	callback func()
	timer    *time.Timer
	mu       sync.Mutex
}

// NewIdleTimer creates an idle timer (does not start automatically).
func NewIdleTimer(duration time.Duration, callback func()) *IdleTimer {
	return &IdleTimer{
		duration: duration,
		callback: callback,
	}
}

// Reset restarts the idle countdown.
func (t *IdleTimer) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(t.duration, t.callback)
}

// Cancel stops the timer without firing.
func (t *IdleTimer) Cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
}
