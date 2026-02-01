package trace

import (
	"io"
	"os"
	"sync"
	"time"
)

// Recorder captures I/O events to a trace.
type Recorder struct {
	trace     *Trace
	startTime time.Time
	mu        sync.Mutex
}

// NewRecorder creates a new recorder with the given metadata.
func NewRecorder(runID string, command []string, env map[string]string, initialSize Size) *Recorder {
	return &Recorder{
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
	}
}

// AddEvent records an I/O event.
func (r *Recorder) AddEvent(eventType EventType, data []byte) {
	if len(data) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Copy data to avoid issues with reused buffers
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	r.trace.Events = append(r.trace.Events, Event{
		TimestampNano: time.Since(r.startTime).Nanoseconds(),
		Type:          eventType,
		Data:          dataCopy,
	})
}

// AddResize records a terminal resize event.
func (r *Recorder) AddResize(width, height int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.trace.Events = append(r.trace.Events, Event{
		TimestampNano: time.Since(r.startTime).Nanoseconds(),
		Type:          EventResize,
		Size:          &Size{Width: width, Height: height},
	})
}

// AddSignal records a signal event.
func (r *Recorder) AddSignal(sig string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.trace.Events = append(r.trace.Events, Event{
		TimestampNano: time.Since(r.startTime).Nanoseconds(),
		Type:          EventSignal,
		Signal:        sig,
	})
}

// Save writes the trace to a file.
func (r *Recorder) Save(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.trace.Save(path)
}

// RecordingWriter wraps an io.Writer and records all writes to the trace.
type RecordingWriter struct {
	w         io.Writer
	recorder  *Recorder
	eventType EventType
}

// NewRecordingWriter creates a writer that records to the trace.
func NewRecordingWriter(w io.Writer, recorder *Recorder, eventType EventType) io.Writer {
	return &RecordingWriter{
		w:         w,
		recorder:  recorder,
		eventType: eventType,
	}
}

func (rw *RecordingWriter) Write(p []byte) (n int, err error) {
	// Record the write
	rw.recorder.AddEvent(rw.eventType, p)

	// Pass through to actual writer
	return rw.w.Write(p)
}

// RecordingReader wraps an io.Reader and records all reads to the trace.
type RecordingReader struct {
	r         io.Reader
	recorder  *Recorder
	eventType EventType
}

// NewRecordingReader creates a reader that records to the trace.
func NewRecordingReader(r io.Reader, recorder *Recorder, eventType EventType) io.Reader {
	return &RecordingReader{
		r:         r,
		recorder:  recorder,
		eventType: eventType,
	}
}

func (rr *RecordingReader) Read(p []byte) (n int, err error) {
	n, err = rr.r.Read(p)
	if n > 0 {
		rr.recorder.AddEvent(rr.eventType, p[:n])
	}
	return n, err
}

// GetTraceEnv extracts relevant environment variables for trace metadata.
func GetTraceEnv() map[string]string {
	env := make(map[string]string)
	keys := []string{"TERM", "LANG", "LC_ALL", "COLUMNS", "LINES", "COLORTERM"}
	for _, key := range keys {
		if val := os.Getenv(key); val != "" {
			env[key] = val
		}
	}
	return env
}
