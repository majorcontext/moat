// Package storage provides run storage infrastructure for AgentOps.
// It handles persisting and loading run metadata, logs, and traces.
package storage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Metadata holds information about an agent run.
type Metadata struct {
	Name      string         `json:"name"`
	Workspace string         `json:"workspace"`
	Grants    []string       `json:"grants,omitempty"`
	Ports     map[string]int `json:"ports,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
	StartedAt time.Time      `json:"started_at,omitempty"`
	StoppedAt time.Time      `json:"stopped_at,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// RunStore manages storage for a single agent run.
type RunStore struct {
	dir   string
	runID string
}

// NewRunStore creates a new RunStore for the given run ID.
// It creates the run directory under baseDir if it doesn't exist.
func NewRunStore(baseDir, runID string) (*RunStore, error) {
	runDir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, err
	}
	return &RunStore{
		dir:   runDir,
		runID: runID,
	}, nil
}

// RunID returns the run identifier.
func (s *RunStore) RunID() string {
	return s.runID
}

// Dir returns the directory path for this run's storage.
func (s *RunStore) Dir() string {
	return s.dir
}

// SaveMetadata writes the metadata to metadata.json in the run directory.
func (s *RunStore) SaveMetadata(m Metadata) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "metadata.json"), data, 0644)
}

// LoadMetadata reads the metadata from metadata.json in the run directory.
func (s *RunStore) LoadMetadata() (Metadata, error) {
	var m Metadata
	data, err := os.ReadFile(filepath.Join(s.dir, "metadata.json"))
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(data, &m)
	return m, err
}

// DefaultBaseDir returns the default base directory for run storage.
// This is ~/.agentops/runs.
func DefaultBaseDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory if home dir cannot be determined
		return filepath.Join(".", ".agentops", "runs")
	}
	return filepath.Join(homeDir, ".agentops", "runs")
}

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

	lines := bufio.NewScanner(strings.NewReader(string(p)))
	for lines.Scan() {
		entry := LogEntry{
			Timestamp: time.Now().UTC(),
			Line:      lines.Text(),
		}
		data, _ := json.Marshal(entry)
		if _, err := w.file.Write(data); err != nil {
			return 0, err
		}
		if _, err := w.file.Write([]byte("\n")); err != nil {
			return 0, err
		}
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
	if _, writeErr := f.Write(data); writeErr != nil {
		return fmt.Errorf("writing span: %w", writeErr)
	}
	_, err = f.Write([]byte("\n"))
	return err
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

// NetworkRequest represents a logged HTTP request.
type NetworkRequest struct {
	Timestamp       time.Time         `json:"ts"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	StatusCode      int               `json:"status_code"`
	Duration        int64             `json:"duration_ms"`
	Error           string            `json:"error,omitempty"`
	RequestHeaders  map[string]string `json:"req_headers,omitempty"`
	ResponseHeaders map[string]string `json:"resp_headers,omitempty"`
	RequestBody     string            `json:"req_body,omitempty"`
	ResponseBody    string            `json:"resp_body,omitempty"`
	BodyTruncated   bool              `json:"truncated,omitempty"`
}

// WriteNetworkRequest appends a network request to the log.
func (s *RunStore) WriteNetworkRequest(req NetworkRequest) error {
	f, err := os.OpenFile(
		filepath.Join(s.dir, "network.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		return fmt.Errorf("opening network file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling network request: %w", err)
	}
	if _, writeErr := f.Write(data); writeErr != nil {
		return fmt.Errorf("writing network request: %w", writeErr)
	}
	_, err = f.Write([]byte("\n"))
	return err
}

// SecretResolution records a resolved secret (without the value).
type SecretResolution struct {
	Timestamp time.Time `json:"ts"`
	Name      string    `json:"name"`    // env var name
	Backend   string    `json:"backend"` // e.g., "1password"
}

// WriteSecretResolution records that a secret was resolved.
func (s *RunStore) WriteSecretResolution(res SecretResolution) error {
	f, err := os.OpenFile(
		filepath.Join(s.dir, "secrets.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		return err
	}
	defer f.Close()
	data, _ := json.Marshal(res)
	if _, err := f.Write(data); err != nil {
		return err
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		return err
	}
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
