package trace

import (
	"sync"
	"time"
)

// RingRecorder records events into a bounded byte budget, evicting oldest
// events FIFO when the budget would be exceeded. Used for always-on capture
// of recent TTY activity in interactive sessions, so users can dump on demand
// after a rendering bug manifests.
//
// Only Event.Data bytes count against the budget. Per-event overhead (struct
// size, JSON encoding) is small relative to data and ignored.
type RingRecorder struct {
	mu        sync.Mutex
	trace     *Trace
	startTime time.Time
	maxBytes  int
	curBytes  int
}

// NewRingRecorder creates a ring recorder with the given byte budget.
// maxBytes <= 0 disables eviction (unbounded).
func NewRingRecorder(runID string, command []string, env map[string]string, initialSize Size, maxBytes int) *RingRecorder {
	return &RingRecorder{
		trace: &Trace{
			Metadata: Metadata{
				Timestamp:   time.Now(),
				RunID:       runID,
				Command:     command,
				Environment: env,
				InitialSize: initialSize,
			},
			Events: make([]Event, 0),
		},
		startTime: time.Now(),
		maxBytes:  maxBytes,
	}
}

// AddEvent records an I/O event, evicting oldest events if the byte budget is exceeded.
func (r *RingRecorder) AddEvent(eventType EventType, data []byte) {
	if len(data) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	r.trace.Events = append(r.trace.Events, Event{
		TimestampNano: time.Since(r.startTime).Nanoseconds(),
		Type:          eventType,
		Data:          dataCopy,
	})
	r.curBytes += len(dataCopy)
	r.evictLocked()
}

// AddResize records a terminal resize event.
func (r *RingRecorder) AddResize(width, height int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Events = append(r.trace.Events, Event{
		TimestampNano: time.Since(r.startTime).Nanoseconds(),
		Type:          EventResize,
		Size:          &Size{Width: width, Height: height},
	})
}

// AddSignal records a signal event.
func (r *RingRecorder) AddSignal(sig string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace.Events = append(r.trace.Events, Event{
		TimestampNano: time.Since(r.startTime).Nanoseconds(),
		Type:          EventSignal,
		Signal:        sig,
	})
}

// Dump writes the current ring contents to a file as a Trace JSON.
func (r *RingRecorder) Dump(path string) error {
	r.mu.Lock()
	snapshot := Trace{
		Metadata: r.trace.Metadata,
		Events:   make([]Event, len(r.trace.Events)),
	}
	copy(snapshot.Events, r.trace.Events)
	r.mu.Unlock()
	return snapshot.Save(path)
}

// evictLocked drops oldest events until curBytes <= maxBytes. Caller holds the mutex.
func (r *RingRecorder) evictLocked() {
	if r.maxBytes <= 0 || r.curBytes <= r.maxBytes {
		return
	}
	drop := 0
	for drop < len(r.trace.Events) && r.curBytes > r.maxBytes {
		r.curBytes -= len(r.trace.Events[drop].Data)
		drop++
	}
	if drop > 0 {
		r.trace.Events = r.trace.Events[drop:]
	}
}
