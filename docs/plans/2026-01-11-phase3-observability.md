# Phase 3: Observability Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Capture everything that happens in a run and provide CLI commands to view logs and traces.

**Architecture:** Run data is stored in `~/.moat/runs/<run-id>/`. Logs are captured as timestamped lines. Traces use OpenTelemetry spans stored as JSON. The proxy logs network calls. CLI commands (`moat logs`, `moat trace`) read from this storage.

**Tech Stack:** Go, OpenTelemetry SDK, JSON storage, Cobra CLI

---

## Task 1: Run Storage Infrastructure

Create the storage layer for run data (logs, traces, metadata).

**Files:**
- Create: `internal/storage/storage.go`
- Create: `internal/storage/storage_test.go`

**Step 1: Write failing tests**

```go
// internal/storage/storage_test.go
package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewRunStore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewRunStore(dir, "run-test123")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	if s.RunID() != "run-test123" {
		t.Errorf("RunID = %q, want %q", s.RunID(), "run-test123")
	}

	// Check directory was created
	runDir := filepath.Join(dir, "run-test123")
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		t.Error("run directory was not created")
	}
}

func TestRunStoreMetadata(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run-test456")

	meta := Metadata{
		Agent:     "claude-code",
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
	if loaded.Agent != meta.Agent {
		t.Errorf("Agent = %q, want %q", loaded.Agent, meta.Agent)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test -v ./internal/storage/...`
Expected: FAIL (package does not exist)

**Step 3: Implement the storage package**

```go
// internal/storage/storage.go
package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Metadata contains run metadata.
type Metadata struct {
	Agent     string    `json:"agent"`
	Workspace string    `json:"workspace"`
	Grants    []string  `json:"grants"`
	CreatedAt time.Time `json:"created_at"`
	StartedAt time.Time `json:"started_at,omitempty"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// RunStore handles storage for a single run.
type RunStore struct {
	dir   string
	runID string
}

// NewRunStore creates storage for a run.
func NewRunStore(baseDir, runID string) (*RunStore, error) {
	dir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating run dir: %w", err)
	}
	return &RunStore{dir: dir, runID: runID}, nil
}

// RunID returns the run identifier.
func (s *RunStore) RunID() string {
	return s.runID
}

// Dir returns the run storage directory.
func (s *RunStore) Dir() string {
	return s.dir
}

// SaveMetadata writes run metadata.
func (s *RunStore) SaveMetadata(m Metadata) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling metadata: %w", err)
	}
	return os.WriteFile(filepath.Join(s.dir, "metadata.json"), data, 0600)
}

// LoadMetadata reads run metadata.
func (s *RunStore) LoadMetadata() (*Metadata, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "metadata.json"))
	if err != nil {
		return nil, fmt.Errorf("reading metadata: %w", err)
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshaling metadata: %w", err)
	}
	return &m, nil
}

// DefaultBaseDir returns ~/.moat/runs
func DefaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".moat", "runs")
	}
	return filepath.Join(home, ".moat", "runs")
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/storage/...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/storage/
git commit -m "feat(storage): add run storage infrastructure"
```

---

## Task 2: Log Writer and Reader

Capture logs with timestamps and provide reading capability.

**Files:**
- Modify: `internal/storage/storage.go`
- Modify: `internal/storage/storage_test.go`

**Step 1: Add failing tests for log operations**

```go
// Add to internal/storage/storage_test.go
func TestLogWriter(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run-logs")

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
	s, _ := NewRunStore(dir, "run-logs-offset")

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
```

**Step 2: Run tests to verify they fail**

Run: `go test -v ./internal/storage/...`
Expected: FAIL (LogWriter undefined)

**Step 3: Implement log writer and reader**

```go
// Add to internal/storage/storage.go
import (
	"bufio"
	"sync"
)

// LogEntry represents a single log line with timestamp.
type LogEntry struct {
	Timestamp time.Time `json:"ts"`
	Line      string    `json:"line"`
}

// LogWriter wraps writes to add timestamps.
type LogWriter struct {
	file *os.File
	mu   sync.Mutex
}

// Write implements io.Writer, adding timestamps to each line.
func (w *LogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	lines := bufio.NewScanner(strings.NewReader(string(p)))
	for lines.Scan() {
		entry := LogEntry{
			Timestamp: time.Now().UTC(),
			Line:      lines.Text(),
		}
		data, _ := json.Marshal(entry)
		w.file.Write(data)
		w.file.Write([]byte("\n"))
	}
	return len(p), nil
}

// Close closes the underlying file.
func (w *LogWriter) Close() error {
	return w.file.Close()
}

// LogWriter returns a writer that timestamps log entries.
func (s *RunStore) LogWriter() (*LogWriter, error) {
	f, err := os.OpenFile(
		filepath.Join(s.dir, "logs.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	return &LogWriter{file: f}, nil
}

// ReadLogs reads log entries with offset and limit.
func (s *RunStore) ReadLogs(offset, limit int) ([]LogEntry, error) {
	f, err := os.Open(filepath.Join(s.dir, "logs.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	defer f.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		if lineNum < offset {
			lineNum++
			continue
		}
		if len(entries) >= limit {
			break
		}
		var entry LogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // Skip malformed entries
		}
		entries = append(entries, entry)
		lineNum++
	}
	return entries, scanner.Err()
}
```

Also add `"strings"` to the imports.

**Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/storage/...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/storage/
git commit -m "feat(storage): add log writer and reader with timestamps"
```

---

## Task 3: Trace Span Storage

Store OpenTelemetry-style spans for tracing.

**Files:**
- Modify: `internal/storage/storage.go`
- Modify: `internal/storage/storage_test.go`

**Step 1: Add failing tests for trace spans**

```go
// Add to internal/storage/storage_test.go
func TestTraceSpans(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewRunStore(dir, "run-traces")

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
```

**Step 2: Run tests to verify they fail**

Run: `go test -v ./internal/storage/...`
Expected: FAIL (Span undefined)

**Step 3: Implement span storage**

```go
// Add to internal/storage/storage.go

// Span represents a trace span (OpenTelemetry-compatible).
type Span struct {
	TraceID    string                 `json:"trace_id"`
	SpanID     string                 `json:"span_id"`
	ParentID   string                 `json:"parent_id,omitempty"`
	Name       string                 `json:"name"`
	Kind       string                 `json:"kind,omitempty"` // client, server, internal
	StartTime  time.Time              `json:"start_time"`
	EndTime    time.Time              `json:"end_time"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
	Status     string                 `json:"status,omitempty"` // ok, error
	StatusMsg  string                 `json:"status_msg,omitempty"`
}

// WriteSpan appends a span to the trace file.
func (s *RunStore) WriteSpan(span Span) error {
	f, err := os.OpenFile(
		filepath.Join(s.dir, "traces.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		return fmt.Errorf("opening trace file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(span)
	if err != nil {
		return fmt.Errorf("marshaling span: %w", err)
	}
	f.Write(data)
	f.Write([]byte("\n"))
	return nil
}

// ReadSpans reads all spans from the trace file.
func (s *RunStore) ReadSpans() ([]Span, error) {
	f, err := os.Open(filepath.Join(s.dir, "traces.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening trace file: %w", err)
	}
	defer f.Close()

	var spans []Span
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var span Span
		if err := json.Unmarshal(scanner.Bytes(), &span); err != nil {
			continue
		}
		spans = append(spans, span)
	}
	return spans, scanner.Err()
}
```

**Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/storage/...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/storage/
git commit -m "feat(storage): add trace span storage"
```

---

## Task 4: Integrate Log Capture with Run Manager

Wire log capture into the run lifecycle.

**Files:**
- Modify: `internal/run/run.go` (add Store field)
- Modify: `internal/run/manager.go` (create storage, wire up logs)

**Step 1: Update Run struct**

```go
// internal/run/run.go - add Store field
import (
	"github.com/andybons/moat/internal/storage"
)

type Run struct {
	// ... existing fields ...
	Store       *storage.RunStore // Run data storage
}
```

**Step 2: Update Manager to create storage and capture logs**

In `internal/run/manager.go`:

1. Import storage package
2. In Create(), create RunStore
3. In Create(), save metadata
4. In streamLogs(), write to both stdout and storage

```go
// In Create() after generating ID:
store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
if err != nil {
	return nil, fmt.Errorf("creating run storage: %w", err)
}
r.Store = store

// Save initial metadata
store.SaveMetadata(storage.Metadata{
	Agent:     opts.Agent,
	Workspace: opts.Workspace,
	Grants:    opts.Grants,
	CreatedAt: r.CreatedAt,
})
```

Update streamLogs:
```go
func (m *Manager) streamLogs(ctx context.Context, r *Run) {
	logs, err := m.docker.ContainerLogs(ctx, r.ContainerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting logs: %v\n", err)
		return
	}
	defer logs.Close()

	// Write to both stdout and storage
	var dest io.Writer = os.Stdout
	if r.Store != nil {
		if lw, err := r.Store.LogWriter(); err == nil {
			dest = io.MultiWriter(os.Stdout, lw)
			defer lw.Close()
		}
	}
	io.Copy(dest, logs)
}
```

**Step 3: Run tests**

Run: `go test -v ./internal/run/...`
Expected: PASS (or fix any issues)

**Step 4: Commit**

```bash
git add internal/run/
git commit -m "feat(run): integrate log capture with storage"
```

---

## Task 5: Add Network Request Logging to Proxy

Log HTTP requests through the proxy for tracing.

**Files:**
- Modify: `internal/proxy/proxy.go`
- Modify: `internal/storage/storage.go` (add NetworkRequest type)

**Step 1: Add NetworkRequest type to storage**

```go
// internal/storage/storage.go

// NetworkRequest represents a logged HTTP request.
type NetworkRequest struct {
	Timestamp  time.Time `json:"ts"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	StatusCode int       `json:"status_code"`
	Duration   int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
}

// WriteNetworkRequest appends a network request to the log.
func (s *RunStore) WriteNetworkRequest(req NetworkRequest) error {
	f, err := os.OpenFile(
		filepath.Join(s.dir, "network.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		return err
	}
	defer f.Close()
	data, _ := json.Marshal(req)
	f.Write(data)
	f.Write([]byte("\n"))
	return nil
}

// ReadNetworkRequests reads all network requests.
func (s *RunStore) ReadNetworkRequests() ([]NetworkRequest, error) {
	f, err := os.Open(filepath.Join(s.dir, "network.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var reqs []NetworkRequest
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var req NetworkRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		reqs = append(reqs, req)
	}
	return reqs, nil
}
```

**Step 2: Add request logging callback to Proxy**

```go
// internal/proxy/proxy.go

// RequestLogger is called for each request.
type RequestLogger func(method, url string, statusCode int, duration time.Duration, err error)

type Proxy struct {
	credentials map[string]string
	mu          sync.RWMutex
	ca          *CA
	logger      RequestLogger // Optional request logger
}

// SetLogger sets the request logger.
func (p *Proxy) SetLogger(logger RequestLogger) {
	p.logger = logger
}

// In handleHTTP, after getting response:
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	// ... existing code ...

	resp, err := http.DefaultTransport.RoundTrip(outReq)
	duration := time.Since(start)

	if p.logger != nil {
		statusCode := 0
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else {
			statusCode = resp.StatusCode
		}
		p.logger(r.Method, r.URL.String(), statusCode, duration, err)
	}

	// ... rest of existing code ...
}
```

**Step 3: Wire up in Manager**

In manager.go Create(), after creating proxy:
```go
if r.Store != nil {
	p.SetLogger(func(method, url string, statusCode int, duration time.Duration, reqErr error) {
		errStr := ""
		if reqErr != nil {
			errStr = reqErr.Error()
		}
		r.Store.WriteNetworkRequest(storage.NetworkRequest{
			Timestamp:  time.Now().UTC(),
			Method:     method,
			URL:        url,
			StatusCode: statusCode,
			Duration:   duration.Milliseconds(),
			Error:      errStr,
		})
	})
}
```

**Step 4: Run tests**

Run: `go test -v ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/proxy/ internal/storage/ internal/run/
git commit -m "feat(proxy): add network request logging"
```

---

## Task 6: CLI `moat logs` Command

Add command to view run logs.

**Files:**
- Create: `cmd/agent/cli/logs.go`

**Step 1: Implement logs command**

```go
// cmd/agent/cli/logs.go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/storage"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsLines  int
)

var logsCmd = &cobra.Command{
	Use:   "logs [run-id]",
	Short: "View logs from a run",
	Long: `View logs from a run. If no run-id is specified, shows logs from the most recent run.

Examples:
  agent logs                    # Logs from most recent run
  agent logs run-abc123         # Logs from specific run
  agent logs -f                 # Follow logs (like tail -f)
  agent logs -n 50              # Show last 50 lines`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLogs,
}

func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "follow log output")
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", 100, "number of lines to show")
}

func runLogs(cmd *cobra.Command, args []string) error {
	baseDir := storage.DefaultBaseDir()

	var runID string
	if len(args) > 0 {
		runID = args[0]
	} else {
		// Find most recent run
		var err error
		runID, err = findLatestRun(baseDir)
		if err != nil {
			return err
		}
	}

	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		return fmt.Errorf("opening run storage: %w", err)
	}

	entries, err := store.ReadLogs(0, logsLines)
	if err != nil {
		return fmt.Errorf("reading logs: %w", err)
	}

	log.Info("Logs for %s", runID)
	for _, entry := range entries {
		ts := entry.Timestamp.Format("15:04:05.000")
		fmt.Printf("[%s] %s\n", ts, entry.Line)
	}

	if logsFollow {
		// TODO: Implement follow mode with file watching
		log.Info("Follow mode not yet implemented")
	}

	return nil
}

// findLatestRun finds the most recently modified run directory.
func findLatestRun(baseDir string) (string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", fmt.Errorf("reading runs dir: %w", err)
	}

	if len(entries) == 0 {
		return "", fmt.Errorf("no runs found")
	}

	// Sort by modification time (newest first)
	type runInfo struct {
		name    string
		modTime time.Time
	}
	var runs []runInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		runs = append(runs, runInfo{name: e.Name(), modTime: info.ModTime()})
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i].modTime.After(runs[j].modTime)
	})

	if len(runs) == 0 {
		return "", fmt.Errorf("no runs found")
	}

	return runs[0].name, nil
}
```

**Step 2: Test manually**

Run: `go build -o agent ./cmd/agent && ./agent logs --help`
Expected: Shows help for logs command

**Step 3: Commit**

```bash
git add cmd/agent/cli/logs.go
git commit -m "feat(cli): add agent logs command"
```

---

## Task 7: CLI `moat trace` Command

Add command to view trace spans.

**Files:**
- Create: `cmd/agent/cli/trace.go`

**Step 1: Implement trace command**

```go
// cmd/agent/cli/trace.go
package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/storage"
	"github.com/spf13/cobra"
)

var (
	traceNetwork bool
)

var traceCmd = &cobra.Command{
	Use:   "trace [run-id]",
	Short: "View trace spans from a run",
	Long: `View trace spans from a run. If no run-id is specified, shows traces from the most recent run.

Examples:
  agent trace                   # Traces from most recent run
  agent trace run-abc123        # Traces from specific run
  agent trace --network         # Show network requests
  agent trace --json            # Output as JSON`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTrace,
}

func init() {
	rootCmd.AddCommand(traceCmd)
	traceCmd.Flags().BoolVar(&traceNetwork, "network", false, "show network requests")
}

func runTrace(cmd *cobra.Command, args []string) error {
	baseDir := storage.DefaultBaseDir()

	var runID string
	if len(args) > 0 {
		runID = args[0]
	} else {
		var err error
		runID, err = findLatestRun(baseDir)
		if err != nil {
			return err
		}
	}

	store, err := storage.NewRunStore(baseDir, runID)
	if err != nil {
		return fmt.Errorf("opening run storage: %w", err)
	}

	if traceNetwork {
		return showNetworkRequests(store, runID)
	}

	return showSpans(store, runID)
}

func showSpans(store *storage.RunStore, runID string) error {
	spans, err := store.ReadSpans()
	if err != nil {
		return fmt.Errorf("reading spans: %w", err)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(spans, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	log.Info("Trace spans for %s", runID)
	if len(spans) == 0 {
		fmt.Println("No spans recorded")
		return nil
	}

	for i, span := range spans {
		duration := span.EndTime.Sub(span.StartTime)
		fmt.Printf("%d. %s (%s)\n", i+1, span.Name, duration)
		if span.ParentID != "" {
			fmt.Printf("   Parent: %s\n", span.ParentID)
		}
		if len(span.Attributes) > 0 {
			for k, v := range span.Attributes {
				fmt.Printf("   %s: %v\n", k, v)
			}
		}
	}
	return nil
}

func showNetworkRequests(store *storage.RunStore, runID string) error {
	reqs, err := store.ReadNetworkRequests()
	if err != nil {
		return fmt.Errorf("reading network requests: %w", err)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(reqs, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	log.Info("Network requests for %s", runID)
	if len(reqs) == 0 {
		fmt.Println("No network requests recorded")
		return nil
	}

	for _, req := range reqs {
		ts := req.Timestamp.Format("15:04:05.000")
		status := fmt.Sprintf("%d", req.StatusCode)
		if req.Error != "" {
			status = "ERR"
		}
		fmt.Printf("[%s] %s %s %s (%dms)\n", ts, req.Method, req.URL, status, req.Duration)
	}
	return nil
}
```

**Step 2: Test manually**

Run: `go build -o agent ./cmd/agent && ./agent trace --help`
Expected: Shows help for trace command

**Step 3: Commit**

```bash
git add cmd/agent/cli/trace.go
git commit -m "feat(cli): add agent trace command"
```

---

## Task 8: Integration Test

End-to-end test of observability.

**Files:**
- Create: `internal/storage/integration_test.go`

**Step 1: Write integration test**

```go
// internal/storage/integration_test.go
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
	lw, _ := store.LogWriter()
	lw.Write([]byte("Starting agent...\n"))
	lw.Write([]byte("Connecting to GitHub...\n"))
	lw.Close()

	// Write network requests
	store.WriteNetworkRequest(NetworkRequest{
		Timestamp:  time.Now(),
		Method:     "GET",
		URL:        "https://api.github.com/user",
		StatusCode: 200,
		Duration:   150,
	})

	// Write spans
	store.WriteSpan(Span{
		TraceID:   "trace-1",
		SpanID:    "span-1",
		Name:      "agent.run",
		StartTime: time.Now(),
		EndTime:   time.Now().Add(5 * time.Second),
		Attributes: map[string]interface{}{
			"agent": "test-agent",
		},
	})

	// Verify all data can be read back
	loadedMeta, _ := store.LoadMetadata()
	if loadedMeta.Agent != "test-agent" {
		t.Errorf("Agent = %q, want %q", loadedMeta.Agent, "test-agent")
	}

	logs, _ := store.ReadLogs(0, 100)
	if len(logs) != 2 {
		t.Errorf("got %d log entries, want 2", len(logs))
	}

	reqs, _ := store.ReadNetworkRequests()
	if len(reqs) != 1 {
		t.Errorf("got %d network requests, want 1", len(reqs))
	}

	spans, _ := store.ReadSpans()
	if len(spans) != 1 {
		t.Errorf("got %d spans, want 1", len(spans))
	}
}
```

**Step 2: Run integration test**

Run: `go test -v ./internal/storage/... -run Integration`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/storage/integration_test.go
git commit -m "test(storage): add observability integration test"
```

---

## Final Verification

Run all tests and build:

```bash
go test ./...
go build -o agent ./cmd/agent
./agent logs --help
./agent trace --help
```

Expected: All tests pass, CLI commands show help.
