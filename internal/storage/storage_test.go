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
	s.WriteSpan(span2)

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
