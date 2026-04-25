package trace

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"
)

func TestRingRecorder_AddAndDump(t *testing.T) {
	r := NewRingRecorder("run_test", []string{"echo", "hi"}, map[string]string{"TERM": "xterm"}, Size{Width: 80, Height: 24}, 1024)
	r.AddEvent(EventStdout, []byte("hello"))
	r.AddEvent(EventStdin, []byte("a"))
	r.AddResize(120, 40)
	r.AddSignal("SIGWINCH")

	path := filepath.Join(t.TempDir(), "dump.json")
	if err := r.Dump(path); err != nil {
		t.Fatalf("Dump: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Metadata.RunID != "run_test" {
		t.Errorf("RunID = %q, want %q", loaded.Metadata.RunID, "run_test")
	}
	if len(loaded.Events) != 4 {
		t.Fatalf("got %d events, want 4", len(loaded.Events))
	}
	if loaded.Events[0].Type != EventStdout || !bytes.Equal(loaded.Events[0].Data, []byte("hello")) {
		t.Errorf("event 0 mismatch: %+v", loaded.Events[0])
	}
	if loaded.Events[2].Type != EventResize || loaded.Events[2].Size == nil || loaded.Events[2].Size.Width != 120 {
		t.Errorf("resize event mismatch: %+v", loaded.Events[2])
	}
	if loaded.Events[3].Signal != "SIGWINCH" {
		t.Errorf("signal event mismatch: %+v", loaded.Events[3])
	}
}

func TestRingRecorder_Eviction(t *testing.T) {
	r := NewRingRecorder("run_test", nil, nil, Size{}, 100)
	payload := bytes.Repeat([]byte("x"), 30)
	for i := 0; i < 10; i++ {
		r.AddEvent(EventStdout, payload)
	}

	path := filepath.Join(t.TempDir(), "dump.json")
	if err := r.Dump(path); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var total int
	for _, e := range loaded.Events {
		total += len(e.Data)
	}
	if total > 100 {
		t.Errorf("retained %d bytes, want <= 100", total)
	}
	if len(loaded.Events) == 10 {
		t.Errorf("no eviction occurred: still have all 10 events")
	}
	if len(loaded.Events) == 0 {
		t.Errorf("evicted everything: ring is empty")
	}
}

func TestRingRecorder_MonotonicTimestamps(t *testing.T) {
	r := NewRingRecorder("run_test", nil, nil, Size{}, 50)
	for i := 0; i < 5; i++ {
		r.AddEvent(EventStdout, bytes.Repeat([]byte("x"), 20))
	}

	path := filepath.Join(t.TempDir(), "dump.json")
	if err := r.Dump(path); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var prev int64 = -1
	for i, e := range loaded.Events {
		if e.TimestampNano < prev {
			t.Errorf("event %d timestamp %d < previous %d (not monotonic)", i, e.TimestampNano, prev)
		}
		prev = e.TimestampNano
	}
}

func TestRingRecorder_ConcurrentAdd(t *testing.T) {
	r := NewRingRecorder("run_test", nil, nil, Size{}, 1<<20)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.AddEvent(EventStdout, []byte("xx"))
			}
		}()
	}
	wg.Wait()

	path := filepath.Join(t.TempDir(), "dump.json")
	if err := r.Dump(path); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Events) != 800 {
		t.Errorf("got %d events, want 800", len(loaded.Events))
	}
}
