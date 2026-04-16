package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewRunStore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_test1234")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	if s.RunID() != "run_test1234" {
		t.Errorf("RunID = %q, want %q", s.RunID(), "run_test1234")
	}

	// Check directory was created
	runDir := filepath.Join(dir, "run_test1234")
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		t.Error("run directory was not created")
	}
}

func TestRunStoreMetadata(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run_test4567")

	meta := Metadata{
		Name:      "claude-code",
		Workspace: "/home/user/project",
		Grants:    []string{"github:repo"},
	}
	if err := s.SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	loaded, err := s.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if loaded.Name != meta.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, meta.Name)
	}
}

func TestRunStoreDir(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_dirtest1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	expectedDir := filepath.Join(dir, "run_dirtest1")
	if s.Dir() != expectedDir {
		t.Errorf("Dir = %q, want %q", s.Dir(), expectedDir)
	}
}

func TestDefaultBaseDir(t *testing.T) {
	// Clear MOAT_HOME so the default ~/.moat/runs path is exercised regardless
	// of the shell environment.
	t.Setenv("MOAT_HOME", "")

	baseDir := DefaultBaseDir()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	expected := filepath.Join(homeDir, ".moat", "runs")
	if baseDir != expected {
		t.Errorf("DefaultBaseDir = %q, want %q", baseDir, expected)
	}
}

func TestDefaultBaseDir_MoatHomeOverride(t *testing.T) {
	override := t.TempDir()
	t.Setenv("MOAT_HOME", override)

	baseDir := DefaultBaseDir()
	expected := filepath.Join(override, "runs")
	if baseDir != expected {
		t.Errorf("DefaultBaseDir = %q, want %q", baseDir, expected)
	}
}

func TestLoadMetadataPreservesAllFields(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run_allfield")

	meta := Metadata{
		Name:      "test-agent",
		Workspace: "/workspace",
		Grants:    []string{"grant1", "grant2"},
		Error:     "some error",
	}
	if err := s.SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	loaded, err := s.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}

	if loaded.Workspace != meta.Workspace {
		t.Errorf("Workspace = %q, want %q", loaded.Workspace, meta.Workspace)
	}
	if len(loaded.Grants) != len(meta.Grants) {
		t.Errorf("Grants length = %d, want %d", len(loaded.Grants), len(meta.Grants))
	}
	if loaded.Error != meta.Error {
		t.Errorf("Error = %q, want %q", loaded.Error, meta.Error)
	}
}

func TestLogWriter(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run_logs1234")

	w, err := s.LogWriter()
	if err != nil {
		t.Fatalf("LogWriter: %v", err)
	}

	w.Write([]byte("hello world\n"))
	w.Write([]byte("second line\n"))
	w.Close()

	// Read back
	entries, err := s.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Line != "hello world" {
		t.Errorf("Line = %q, want %q", entries[0].Line, "hello world")
	}
	if entries[0].Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestReadLogsWithOffset(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run_logsoffset1")

	w, _ := s.LogWriter()
	for i := 0; i < 10; i++ {
		fmt.Fprintf(w, "line %d\n", i)
	}
	w.Close()

	// Read with offset
	entries, _ := s.ReadLogs(5, 3)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].Line != "line 5" {
		t.Errorf("Line = %q, want %q", entries[0].Line, "line 5")
	}
}

func TestTraceSpans(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run_traces12")

	span1 := Span{
		TraceID:   "trace-123",
		SpanID:    "span-1",
		Name:      "http.request",
		StartTime: time.Now(),
		EndTime:   time.Now().Add(100 * time.Millisecond),
		Attributes: map[string]interface{}{
			"http.method": "GET",
			"http.url":    "https://api.github.com/user",
		},
	}
	if err := s.WriteSpan(span1); err != nil {
		t.Fatalf("WriteSpan: %v", err)
	}

	span2 := Span{
		TraceID:   "trace-123",
		SpanID:    "span-2",
		ParentID:  "span-1",
		Name:      "dns.lookup",
		StartTime: time.Now(),
		EndTime:   time.Now().Add(10 * time.Millisecond),
	}
	if err := s.WriteSpan(span2); err != nil {
		t.Fatalf("WriteSpan: %v", err)
	}

	spans, err := s.ReadSpans()
	if err != nil {
		t.Fatalf("ReadSpans: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	if spans[0].Name != "http.request" {
		t.Errorf("Name = %q, want %q", spans[0].Name, "http.request")
	}
}

func TestWriteExecEvent(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_exec1234")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	exitCode := 0
	duration := 100 * time.Millisecond
	event := ExecEvent{
		Timestamp:  time.Now().UTC(),
		PID:        1234,
		PPID:       1,
		Command:    "git",
		Args:       []string{"status"},
		WorkingDir: "/workspace",
		ExitCode:   &exitCode,
		Duration:   &duration,
	}

	if err := s.WriteExecEvent(event); err != nil {
		t.Fatalf("WriteExecEvent: %v", err)
	}

	events, err := s.ReadExecEvents()
	if err != nil {
		t.Fatalf("ReadExecEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}

	got := events[0]
	if got.PID != event.PID {
		t.Errorf("PID = %d, want %d", got.PID, event.PID)
	}
	if got.PPID != event.PPID {
		t.Errorf("PPID = %d, want %d", got.PPID, event.PPID)
	}
	if got.Command != event.Command {
		t.Errorf("Command = %q, want %q", got.Command, event.Command)
	}
	if len(got.Args) != len(event.Args) || got.Args[0] != event.Args[0] {
		t.Errorf("Args = %v, want %v", got.Args, event.Args)
	}
	if got.WorkingDir != event.WorkingDir {
		t.Errorf("WorkingDir = %q, want %q", got.WorkingDir, event.WorkingDir)
	}
	if got.ExitCode == nil || *got.ExitCode != *event.ExitCode {
		t.Errorf("ExitCode = %v, want %v", got.ExitCode, event.ExitCode)
	}
	if got.Duration == nil || *got.Duration != *event.Duration {
		t.Errorf("Duration = %v, want %v", got.Duration, event.Duration)
	}
}

func TestReadExecEventsMultiple(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_execmulti1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	// Write multiple events
	events := []ExecEvent{
		{
			Timestamp: time.Now().UTC(),
			PID:       100,
			PPID:      1,
			Command:   "npm",
			Args:      []string{"install"},
		},
		{
			Timestamp: time.Now().UTC().Add(time.Second),
			PID:       101,
			PPID:      100,
			Command:   "node",
			Args:      []string{"index.js"},
		},
		{
			Timestamp: time.Now().UTC().Add(2 * time.Second),
			PID:       102,
			PPID:      1,
			Command:   "git",
			Args:      []string{"commit", "-m", "test"},
		},
	}

	for _, event := range events {
		if err := s.WriteExecEvent(event); err != nil {
			t.Fatalf("WriteExecEvent: %v", err)
		}
	}

	// Read all back
	readEvents, err := s.ReadExecEvents()
	if err != nil {
		t.Fatalf("ReadExecEvents: %v", err)
	}
	if len(readEvents) != len(events) {
		t.Fatalf("got %d events, want %d", len(readEvents), len(events))
	}

	// Verify order and content
	for i, got := range readEvents {
		want := events[i]
		if got.PID != want.PID {
			t.Errorf("event[%d].PID = %d, want %d", i, got.PID, want.PID)
		}
		if got.Command != want.Command {
			t.Errorf("event[%d].Command = %q, want %q", i, got.Command, want.Command)
		}
	}
}

func TestReadExecEventsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_execempty1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	// Read from non-existent file should return nil, nil
	events, err := s.ReadExecEvents()
	if err != nil {
		t.Fatalf("ReadExecEvents: %v", err)
	}
	if events != nil {
		t.Errorf("expected nil events, got %v", events)
	}
}

func TestRunStoreRemove(t *testing.T) {
	dir := t.TempDir()
	runID := "run_remove123"
	s, err := NewRunStore(dir, runID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	// Write some data to the store
	meta := Metadata{
		Name:      "test-agent",
		Workspace: "/workspace",
	}
	if err := s.SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	// Verify directory exists
	runDir := filepath.Join(dir, runID)
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		t.Fatal("run directory should exist before removal")
	}

	// Remove the store
	if err := s.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Verify directory is gone
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Error("run directory should not exist after removal")
	}
}

func TestRunStoreRemoveWithContents(t *testing.T) {
	dir := t.TempDir()
	runID := "run_removefull1"
	s, err := NewRunStore(dir, runID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	// Write metadata
	meta := Metadata{
		Name:      "test-agent",
		Workspace: "/workspace",
		Grants:    []string{"github"},
	}
	if err := s.SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}

	// Write logs
	w, err := s.LogWriter()
	if err != nil {
		t.Fatalf("LogWriter: %v", err)
	}
	w.Write([]byte("test log line\n"))
	w.Close()

	// Write a span
	span := Span{
		TraceID: "trace-1",
		SpanID:  "span-1",
		Name:    "test",
	}
	if err := s.WriteSpan(span); err != nil {
		t.Fatalf("WriteSpan: %v", err)
	}

	// Verify files exist
	runDir := filepath.Join(dir, runID)
	files, _ := os.ReadDir(runDir)
	if len(files) == 0 {
		t.Fatal("expected files in run directory")
	}

	// Remove the store
	if err := s.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Verify directory and all contents are gone
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Error("run directory should not exist after removal")
	}
}

func TestRunStoreRemoveEmptyRunID(t *testing.T) {
	// Test that Remove() fails safely when runID is empty.
	// This prevents accidental deletion of the base directory.
	s := &RunStore{
		dir:   "/some/base/dir",
		runID: "",
	}

	err := s.Remove()
	if err == nil {
		t.Fatal("Remove() should fail with empty runID")
	}
	if err.Error() != "cannot remove run storage: empty run ID" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestWriteNetworkRequest(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_net1234")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	req := NetworkRequest{
		Timestamp:       time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Method:          "POST",
		URL:             "https://api.github.com/repos/org/repo/pulls",
		StatusCode:      201,
		Duration:        142,
		Error:           "",
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		ResponseHeaders: map[string]string{"X-RateLimit-Remaining": "4999"},
		RequestBody:     `{"title":"test PR"}`,
		ResponseBody:    `{"id":1,"number":42}`,
		BodyTruncated:   false,
	}
	if err := s.WriteNetworkRequest(req); err != nil {
		t.Fatalf("WriteNetworkRequest: %v", err)
	}

	reqs, err := s.ReadNetworkRequests()
	if err != nil {
		t.Fatalf("ReadNetworkRequests: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}

	got := reqs[0]
	if got.Method != req.Method {
		t.Errorf("Method = %q, want %q", got.Method, req.Method)
	}
	if got.URL != req.URL {
		t.Errorf("URL = %q, want %q", got.URL, req.URL)
	}
	if got.StatusCode != req.StatusCode {
		t.Errorf("StatusCode = %d, want %d", got.StatusCode, req.StatusCode)
	}
	if got.Duration != req.Duration {
		t.Errorf("Duration = %d, want %d", got.Duration, req.Duration)
	}
	if got.RequestHeaders["Content-Type"] != "application/json" {
		t.Errorf("RequestHeaders[Content-Type] = %q, want %q", got.RequestHeaders["Content-Type"], "application/json")
	}
	if got.ResponseHeaders["X-RateLimit-Remaining"] != "4999" {
		t.Errorf("ResponseHeaders[X-RateLimit-Remaining] = %q, want %q", got.ResponseHeaders["X-RateLimit-Remaining"], "4999")
	}
	if got.RequestBody != req.RequestBody {
		t.Errorf("RequestBody = %q, want %q", got.RequestBody, req.RequestBody)
	}
	if got.ResponseBody != req.ResponseBody {
		t.Errorf("ResponseBody = %q, want %q", got.ResponseBody, req.ResponseBody)
	}
	if got.BodyTruncated != false {
		t.Errorf("BodyTruncated = %v, want false", got.BodyTruncated)
	}
}

func TestWriteNetworkRequestWithError(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_neterr1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	req := NetworkRequest{
		Timestamp: time.Now().UTC(),
		Method:    "GET",
		URL:       "https://unreachable.example.com/api",
		Error:     "dial tcp: no such host",
	}
	if err := s.WriteNetworkRequest(req); err != nil {
		t.Fatalf("WriteNetworkRequest: %v", err)
	}

	reqs, err := s.ReadNetworkRequests()
	if err != nil {
		t.Fatalf("ReadNetworkRequests: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	if reqs[0].Error != "dial tcp: no such host" {
		t.Errorf("Error = %q, want %q", reqs[0].Error, "dial tcp: no such host")
	}
	if reqs[0].BodyTruncated {
		t.Error("BodyTruncated should be false for error requests")
	}
}

func TestWriteNetworkRequestMultiple(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_netmulti1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	reqs := []NetworkRequest{
		{
			Timestamp:  time.Now().UTC(),
			Method:     "GET",
			URL:        "https://api.github.com/user",
			StatusCode: 200,
			Duration:   89,
		},
		{
			Timestamp:  time.Now().UTC().Add(time.Second),
			Method:     "POST",
			URL:        "https://api.anthropic.com/v1/messages",
			StatusCode: 200,
			Duration:   1200,
		},
		{
			Timestamp:  time.Now().UTC().Add(2 * time.Second),
			Method:     "GET",
			URL:        "https://api.github.com/repos/org/repo",
			StatusCode: 404,
			Duration:   45,
		},
	}

	for _, req := range reqs {
		if err := s.WriteNetworkRequest(req); err != nil {
			t.Fatalf("WriteNetworkRequest: %v", err)
		}
	}

	readReqs, err := s.ReadNetworkRequests()
	if err != nil {
		t.Fatalf("ReadNetworkRequests: %v", err)
	}
	if len(readReqs) != len(reqs) {
		t.Fatalf("got %d requests, want %d", len(readReqs), len(reqs))
	}

	for i, got := range readReqs {
		want := reqs[i]
		if got.Method != want.Method {
			t.Errorf("request[%d].Method = %q, want %q", i, got.Method, want.Method)
		}
		if got.URL != want.URL {
			t.Errorf("request[%d].URL = %q, want %q", i, got.URL, want.URL)
		}
		if got.StatusCode != want.StatusCode {
			t.Errorf("request[%d].StatusCode = %d, want %d", i, got.StatusCode, want.StatusCode)
		}
	}
}

func TestReadNetworkRequestsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_netempty1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	reqs, err := s.ReadNetworkRequests()
	if err != nil {
		t.Fatalf("ReadNetworkRequests: %v", err)
	}
	if reqs != nil {
		t.Errorf("expected nil requests, got %v", reqs)
	}
}

func TestReadNetworkRequestsMalformed(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run_netmal1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	// Write a valid request first
	valid := NetworkRequest{
		Timestamp:  time.Now().UTC(),
		Method:     "GET",
		URL:        "https://api.github.com/user",
		StatusCode: 200,
		Duration:   50,
	}
	if err := s.WriteNetworkRequest(valid); err != nil {
		t.Fatalf("WriteNetworkRequest: %v", err)
	}

	// Append a malformed line directly to the file
	f, err := os.OpenFile(
		filepath.Join(dir, "run_netmal1", "network.jsonl"),
		os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	fmt.Fprintln(f, "not valid json {{{")
	f.Close()

	// Write another valid request after the malformed line
	valid2 := NetworkRequest{
		Timestamp:  time.Now().UTC(),
		Method:     "POST",
		URL:        "https://api.example.com/data",
		StatusCode: 201,
		Duration:   100,
	}
	if err := s.WriteNetworkRequest(valid2); err != nil {
		t.Fatalf("WriteNetworkRequest: %v", err)
	}

	reqs, err := s.ReadNetworkRequests()
	if err != nil {
		t.Fatalf("ReadNetworkRequests: %v", err)
	}
	// Should skip the malformed line and return the two valid ones
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2", len(reqs))
	}
	if reqs[0].Method != "GET" {
		t.Errorf("request[0].Method = %q, want %q", reqs[0].Method, "GET")
	}
	if reqs[1].Method != "POST" {
		t.Errorf("request[1].Method = %q, want %q", reqs[1].Method, "POST")
	}
}

func TestMetadata_BuildKitFields(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewRunStore(tmpDir, "test-run")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Save metadata with buildkit fields
	original := Metadata{
		Name:                "test",
		BuildkitContainerID: "buildkit-123",
		NetworkID:           "net-456",
	}

	if err := store.SaveMetadata(original); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}

	// Load and verify
	loaded, err := store.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata failed: %v", err)
	}

	if loaded.BuildkitContainerID != original.BuildkitContainerID {
		t.Errorf("BuildkitContainerID: got %q, want %q", loaded.BuildkitContainerID, original.BuildkitContainerID)
	}
	if loaded.NetworkID != original.NetworkID {
		t.Errorf("NetworkID: got %q, want %q", loaded.NetworkID, original.NetworkID)
	}
}
