package trace

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRecordingWriter(t *testing.T) {
	tests := []struct {
		name      string
		eventType EventType
		writes    []string
		wantData  []string
	}{
		{
			name:      "single write",
			eventType: EventStdout,
			writes:    []string{"hello world"},
			wantData:  []string{"hello world"},
		},
		{
			name:      "multiple writes",
			eventType: EventStderr,
			writes:    []string{"line 1\n", "line 2\n", "line 3\n"},
			wantData:  []string{"line 1\n", "line 2\n", "line 3\n"},
		},
		{
			name:      "empty write ignored",
			eventType: EventStdout,
			writes:    []string{"data", "", "more data"},
			wantData:  []string{"data", "more data"}, // empty write not recorded
		},
		{
			name:      "binary data",
			eventType: EventStdout,
			writes:    []string{"\x00\x01\x02\xff"},
			wantData:  []string{"\x00\x01\x02\xff"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create recorder
			recorder := NewRecorder("test-run", []string{"test"}, nil, Size{Width: 80, Height: 24})

			// Create recording writer wrapping a buffer
			var buf bytes.Buffer
			writer := NewRecordingWriter(&buf, recorder, tt.eventType)

			// Perform writes
			for _, data := range tt.writes {
				n, err := writer.Write([]byte(data))
				if err != nil {
					t.Fatalf("Write() error = %v", err)
				}
				if n != len(data) {
					t.Errorf("Write() wrote %d bytes, want %d", n, len(data))
				}
			}

			// Verify data was written to underlying writer
			got := buf.String()
			want := strings.Join(tt.writes, "")
			if got != want {
				t.Errorf("underlying writer got %q, want %q", got, want)
			}

			// Verify events were recorded (excluding empty writes)
			recorder.mu.Lock()
			events := recorder.trace.Events
			recorder.mu.Unlock()

			if len(events) != len(tt.wantData) {
				t.Fatalf("recorded %d events, want %d", len(events), len(tt.wantData))
			}

			for i, event := range events {
				if event.Type != tt.eventType {
					t.Errorf("event[%d].Type = %v, want %v", i, event.Type, tt.eventType)
				}
				if string(event.Data) != tt.wantData[i] {
					t.Errorf("event[%d].Data = %q, want %q", i, string(event.Data), tt.wantData[i])
				}
			}
		})
	}
}

func TestRecordingReader(t *testing.T) {
	tests := []struct {
		name      string
		eventType EventType
		input     string
		readSizes []int // sizes of buffer for each Read() call
		wantReads []string
		wantData  []string // what should be recorded
	}{
		{
			name:      "single read",
			eventType: EventStdin,
			input:     "hello",
			readSizes: []int{100},
			wantReads: []string{"hello"},
			wantData:  []string{"hello"},
		},
		{
			name:      "multiple small reads",
			eventType: EventStdin,
			input:     "hello world",
			readSizes: []int{5, 6},
			wantReads: []string{"hello", " world"},
			wantData:  []string{"hello", " world"},
		},
		{
			name:      "read with exact buffer size",
			eventType: EventStdin,
			input:     "test",
			readSizes: []int{4},
			wantReads: []string{"test"},
			wantData:  []string{"test"},
		},
		{
			name:      "read with larger buffer",
			eventType: EventStdin,
			input:     "short",
			readSizes: []int{100},
			wantReads: []string{"short"},
			wantData:  []string{"short"},
		},
		{
			name:      "binary data",
			eventType: EventStdin,
			input:     "\x00\x01\x02\xff",
			readSizes: []int{100},
			wantReads: []string{"\x00\x01\x02\xff"},
			wantData:  []string{"\x00\x01\x02\xff"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create recorder
			recorder := NewRecorder("test-run", []string{"test"}, nil, Size{Width: 80, Height: 24})

			// Create recording reader wrapping a strings.Reader
			reader := NewRecordingReader(strings.NewReader(tt.input), recorder, tt.eventType)

			// Perform reads
			var reads []string
			for _, size := range tt.readSizes {
				buf := make([]byte, size)
				n, err := reader.Read(buf)
				if err != nil && err != io.EOF {
					t.Fatalf("Read() error = %v", err)
				}
				if n > 0 {
					reads = append(reads, string(buf[:n]))
				}
				if err == io.EOF {
					break
				}
			}

			// Verify reads match expected
			if len(reads) != len(tt.wantReads) {
				t.Fatalf("got %d reads, want %d", len(reads), len(tt.wantReads))
			}
			for i, got := range reads {
				if got != tt.wantReads[i] {
					t.Errorf("read[%d] = %q, want %q", i, got, tt.wantReads[i])
				}
			}

			// Verify events were recorded
			recorder.mu.Lock()
			events := recorder.trace.Events
			recorder.mu.Unlock()

			if len(events) != len(tt.wantData) {
				t.Fatalf("recorded %d events, want %d", len(events), len(tt.wantData))
			}

			for i, event := range events {
				if event.Type != tt.eventType {
					t.Errorf("event[%d].Type = %v, want %v", i, event.Type, tt.eventType)
				}
				if string(event.Data) != tt.wantData[i] {
					t.Errorf("event[%d].Data = %q, want %q", i, string(event.Data), tt.wantData[i])
				}
			}
		})
	}
}

func TestRecordingReaderEOF(t *testing.T) {
	recorder := NewRecorder("test-run", []string{"test"}, nil, Size{Width: 80, Height: 24})
	reader := NewRecordingReader(strings.NewReader(""), recorder, EventStdin)

	buf := make([]byte, 10)
	n, err := reader.Read(buf)

	if err != io.EOF {
		t.Errorf("Read() error = %v, want io.EOF", err)
	}
	if n != 0 {
		t.Errorf("Read() returned %d bytes, want 0", n)
	}

	// Verify no events recorded for EOF with 0 bytes
	recorder.mu.Lock()
	events := recorder.trace.Events
	recorder.mu.Unlock()

	if len(events) != 0 {
		t.Errorf("recorded %d events, want 0 for EOF", len(events))
	}
}

func TestRecordingWriterDataIsolation(t *testing.T) {
	// Verify that recorded data is copied, not referenced
	recorder := NewRecorder("test-run", []string{"test"}, nil, Size{Width: 80, Height: 24})

	var buf bytes.Buffer
	writer := NewRecordingWriter(&buf, recorder, EventStdout)

	// Write data using a reusable buffer
	data := []byte("original")
	writer.Write(data)

	// Modify the original buffer
	copy(data, []byte("modified"))

	// Write again
	writer.Write(data)

	// Verify recorded events still have original data
	recorder.mu.Lock()
	events := recorder.trace.Events
	recorder.mu.Unlock()

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	if string(events[0].Data) != "original" {
		t.Errorf("first event data = %q, want %q (buffer was reused)", string(events[0].Data), "original")
	}

	if string(events[1].Data) != "modified" {
		t.Errorf("second event data = %q, want %q", string(events[1].Data), "modified")
	}
}

func TestRecordingReaderDataIsolation(t *testing.T) {
	// Verify that recorded data is copied, not referenced
	recorder := NewRecorder("test-run", []string{"test"}, nil, Size{Width: 80, Height: 24})

	reader := NewRecordingReader(strings.NewReader("data1data2"), recorder, EventStdin)

	// Read using a reusable buffer
	buf := make([]byte, 5)
	reader.Read(buf) // reads "data1"
	reader.Read(buf) // reads "data2" into same buffer

	// Verify recorded events have correct data despite buffer reuse
	recorder.mu.Lock()
	events := recorder.trace.Events
	recorder.mu.Unlock()

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	if string(events[0].Data) != "data1" {
		t.Errorf("first event data = %q, want %q (buffer was reused)", string(events[0].Data), "data1")
	}

	if string(events[1].Data) != "data2" {
		t.Errorf("second event data = %q, want %q", string(events[1].Data), "data2")
	}
}
