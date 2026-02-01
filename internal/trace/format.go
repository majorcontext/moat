package trace

import (
	"encoding/json"
	"os"
	"time"
)

// Trace captures terminal I/O with timing for replay and debugging.
type Trace struct {
	Metadata Metadata `json:"metadata"`
	Events   []Event  `json:"events"`
}

// Metadata describes the environment and context of a trace.
type Metadata struct {
	Timestamp   time.Time         `json:"timestamp"`
	RunID       string            `json:"run_id"`
	Command     []string          `json:"command"`
	Environment map[string]string `json:"environment"` // TERM, LANG, COLUMNS, LINES, etc.
	InitialSize Size              `json:"initial_size"`
}

// Size represents terminal dimensions.
type Size struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Event represents a single I/O or control event.
type Event struct {
	TimestampNano int64     `json:"ts"`               // nanoseconds since trace start
	Type          EventType `json:"type"`             // "stdout", "stdin", "stderr", "resize", "signal"
	Data          []byte    `json:"data,omitempty"`   // raw bytes (base64 encoded in JSON)
	Size          *Size     `json:"size,omitempty"`   // for resize events
	Signal        string    `json:"signal,omitempty"` // for signal events (e.g., "SIGWINCH")
}

// EventType categorizes trace events.
type EventType string

const (
	EventStdout EventType = "stdout"
	EventStderr EventType = "stderr"
	EventStdin  EventType = "stdin"
	EventResize EventType = "resize"
	EventSignal EventType = "signal"
)

// Load reads a trace from a JSON file.
func Load(path string) (*Trace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var trace Trace
	if err := json.Unmarshal(data, &trace); err != nil {
		return nil, err
	}
	return &trace, nil
}

// Save writes a trace to a JSON file.
func (t *Trace) Save(path string) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
