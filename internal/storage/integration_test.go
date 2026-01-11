package storage

import (
	"testing"
	"time"
)

func TestFullObservabilityFlow(t *testing.T) {
	dir := t.TempDir()
	runID := "run-integration-test"

	// Create storage
	store, err := NewRunStore(dir, runID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	// Save metadata
	meta := Metadata{
		Agent:     "test-agent",
		Workspace: "/tmp/workspace",
		Grants:    []string{"github:repo"},
		CreatedAt: time.Now(),
	}
	if err := store.SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	// Write logs
	lw, err := store.LogWriter()
	if err != nil {
		t.Fatalf("LogWriter: %v", err)
	}
	lw.Write([]byte("Starting agent...\n"))
	lw.Write([]byte("Connecting to GitHub...\n"))
	lw.Close()

	// Write network requests
	if err := store.WriteNetworkRequest(NetworkRequest{
		Timestamp:  time.Now(),
		Method:     "GET",
		URL:        "https://api.github.com/user",
		StatusCode: 200,
		Duration:   150,
	}); err != nil {
		t.Fatalf("WriteNetworkRequest: %v", err)
	}

	// Write spans
	if err := store.WriteSpan(Span{
		TraceID:   "trace-1",
		SpanID:    "span-1",
		Name:      "agent.run",
		StartTime: time.Now(),
		EndTime:   time.Now().Add(5 * time.Second),
		Attributes: map[string]interface{}{
			"agent": "test-agent",
		},
	}); err != nil {
		t.Fatalf("WriteSpan: %v", err)
	}

	// Verify all data can be read back
	loadedMeta, err := store.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if loadedMeta.Agent != "test-agent" {
		t.Errorf("Agent = %q, want %q", loadedMeta.Agent, "test-agent")
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("got %d log entries, want 2", len(logs))
	}

	reqs, err := store.ReadNetworkRequests()
	if err != nil {
		t.Fatalf("ReadNetworkRequests: %v", err)
	}
	if len(reqs) != 1 {
		t.Errorf("got %d network requests, want 1", len(reqs))
	}
	if reqs[0].URL != "https://api.github.com/user" {
		t.Errorf("URL = %q, want %q", reqs[0].URL, "https://api.github.com/user")
	}

	spans, err := store.ReadSpans()
	if err != nil {
		t.Fatalf("ReadSpans: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("got %d spans, want 1", len(spans))
	}
	if spans[0].Name != "agent.run" {
		t.Errorf("Name = %q, want %q", spans[0].Name, "agent.run")
	}
}
