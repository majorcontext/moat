package trace

import (
	"testing"
	"time"
)

func TestStubTracer(t *testing.T) {
	tracer := NewStubTracer(Config{})

	var received []ExecEvent
	tracer.OnExec(func(e ExecEvent) {
		received = append(received, e)
	})

	if err := tracer.Start(); err != nil {
		t.Fatal(err)
	}

	// Emit test event
	tracer.Emit(ExecEvent{
		Timestamp: time.Now(),
		PID:       1234,
		Command:   "test",
	})

	if len(received) != 1 {
		t.Errorf("expected 1 event, got %d", len(received))
	}

	if err := tracer.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestStubTracerEventsChannel(t *testing.T) {
	tracer := NewStubTracer(Config{})

	if err := tracer.Start(); err != nil {
		t.Fatal(err)
	}

	// Emit event
	tracer.Emit(ExecEvent{
		Timestamp: time.Now(),
		PID:       5678,
		Command:   "ls",
		Args:      []string{"-la"},
	})

	// Read from channel
	select {
	case event := <-tracer.Events():
		if event.Command != "ls" {
			t.Errorf("expected command 'ls', got '%s'", event.Command)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for event")
	}

	if err := tracer.Stop(); err != nil {
		t.Fatal(err)
	}
}
