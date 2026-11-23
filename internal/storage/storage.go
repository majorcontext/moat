// Package storage provides run storage infrastructure for Moat.
// It handles persisting and loading run metadata, logs, and traces.
package storage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/majorcontext/moat/internal/config"
)

// Metadata holds information about an agent run.
type Metadata struct {
	Name      string   `json:"name"`
	Workspace string   `json:"workspace"`
	Grants    []string `json:"grants,omitempty"`
	Agent     string   `json:"agent,omitempty"` // Agent type from config (e.g., "claude-code")

	// JoinableAgents lists the agents moat provisioned into the container —
	// staged config plus an installed CLI. `moat join` tests membership here.
	//
	// Deliberately NOT omitempty: an empty slice (no joinable agents) must stay
	// distinguishable from an absent field (a run created before this existed,
	// which falls back to the legacy agent-string check).
	JoinableAgents []string       `json:"joinable_agents"`
	Image          string         `json:"image,omitempty"` // Container image used
	Ports          map[string]int `json:"ports,omitempty"`
	HostPorts      map[string]int `json:"host_ports,omitempty"`
	ContainerID    string         `json:"container_id,omitempty"`
	State          string         `json:"state,omitempty"`
	CleanupPending bool           `json:"cleanup_pending,omitempty"`
	KeepContainer  bool           `json:"keep_container,omitempty"`
	Interactive    bool           `json:"interactive,omitempty"`
	Clipboard      bool           `json:"clipboard,omitempty"` // Missing in older runs: leave bridging disabled on join.
	CreatedAt      time.Time      `json:"created_at,omitempty"`
	StartedAt      time.Time      `json:"started_at,omitempty"`
	StoppedAt      time.Time      `json:"stopped_at,omitempty"`
	Error          string         `json:"error,omitempty"`

	// ProviderMeta holds provider-specific metadata captured during the run lifecycle.
	// For example, the Claude provider stores {"claude_session_id": "<uuid>"}.
	ProviderMeta map[string]string `json:"provider_meta,omitempty"`

	// Worktree fields (set when run was created via moat wt or --wt)
	WorktreeBranch string `json:"worktree_branch,omitempty"`
	WorktreePath   string `json:"worktree_path,omitempty"`
	WorktreeRepoID string `json:"worktree_repo_id,omitempty"`

	// Service dependency fields
	ServiceContainers map[string]string `json:"service_containers,omitempty"` // service name -> container ID

	// Runtime records which container runtime was used ("docker" or "apple").
	// Used during reconciliation to skip cross-runtime container state checks.
	Runtime string `json:"runtime,omitempty"`

	// BuildKit sidecar fields (docker:dind only)
	BuildkitContainerID string `json:"buildkit_container_id,omitempty"`
	NetworkID           string `json:"network_id,omitempty"`

	// Workspace mode fields (set when workspace.mode: volume).
	// WorkspaceMode records the resolved mode ("bind" or "volume").
	// WorkspaceVolume is the per-run Docker volume name backing /workspace,
	// removed during cleanup.
	WorkspaceMode   string `json:"workspace_mode,omitempty"`
	WorkspaceVolume string `json:"workspace_volume,omitempty"`
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
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, err
	}
	return &RunStore{
		dir:   runDir,
		runID: runID,
	}, nil
}

// LoadRunStore opens the metadata for an existing run without creating its directory.
func LoadRunStore(baseDir, runID string) (*RunStore, Metadata, error) {
	store := &RunStore{dir: filepath.Join(baseDir, runID), runID: runID}
	meta, err := store.LoadMetadata()
	if err != nil {
		return nil, Metadata{}, err
	}
	return store, meta, nil
}

// RunID returns the run identifier.
func (s *RunStore) RunID() string {
	return s.runID
}

// Dir returns the directory path for this run's storage.
func (s *RunStore) Dir() string {
	return s.dir
}

// Remove deletes the run's storage directory and all its contents.
// Returns an error if the run ID is empty to prevent accidental deletion
// of the base directory.
func (s *RunStore) Remove() error {
	if s.runID == "" {
		return fmt.Errorf("cannot remove run storage: empty run ID")
	}
	return os.RemoveAll(s.dir)
}

// SaveMetadata writes the metadata to metadata.json in the run directory.
//
// The write is atomic (unique temp file + rename): a crash or interrupt
// mid-write used to leave a truncated/empty metadata.json, which LoadMetadata
// then failed to parse ("unexpected end of JSON input") and the run was
// silently dropped from listings. Readers do not take a lock, so the rename is
// what makes a concurrent read see either the old file or the new one, never a
// partial write.
func (s *RunStore) SaveMetadata(m Metadata) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "metadata.json")
	tmp, err := os.CreateTemp(s.dir, ".metadata-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	// Sync before rename: rename is atomic against interrupts and crashes, but
	// without an fsync a power loss or kernel panic could persist the rename
	// before the data blocks, leaving the 0-byte metadata.json this fix is
	// about. Matches internal/daemon/persist.go.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	// CreateTemp defaults to 0600, matching the previous metadata.json mode.
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
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
// This is <GlobalConfigDir>/runs — by default ~/.moat/runs, or $MOAT_HOME/runs
// when MOAT_HOME is set.
func DefaultBaseDir() string {
	return filepath.Join(config.GlobalConfigDir(), "runs")
}

// ListRunDirs returns all run IDs that have stored metadata.
// It scans baseDir for directories containing metadata.json.
func ListRunDirs(baseDir string) ([]string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No runs directory yet
		}
		return nil, err
	}

	var runIDs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Check if metadata.json exists
		metaPath := filepath.Join(baseDir, entry.Name(), "metadata.json")
		if _, err := os.Stat(metaPath); err == nil {
			runIDs = append(runIDs, entry.Name())
		}
	}
	return runIDs, nil
}

// ListRunDirNames returns the set of subdirectory names under baseDir.
// Unlike ListRunDirs, it does NOT require metadata.json — Create() makes the
// directory before writing metadata, so this catches in-flight runs that
// ListRunDirs would miss. Used by orphan resource sweeps where it's important
// to treat newly-created (but not yet persisted) runs as alive.
func ListRunDirNames(baseDir string) (map[string]struct{}, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	names := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names[e.Name()] = struct{}{}
		}
	}
	return names, nil
}

// LogEntry represents a single log line with timestamp.
type LogEntry struct {
	Timestamp time.Time `json:"ts"`
	Line      string    `json:"line"`
}

// maxLogLineBytes bounds the raw text of one log entry. Output that goes
// longer without a newline, such as a full-screen TUI, continues in the next
// entry, so a writer never holds more than this per stream. Lines up to
// this length are stored whole; it matches the longest entry earlier
// versions could read back.
const maxLogLineBytes = 64 * 1024

// maxLogEntryBytes is the largest encoded entry ReadLogs accepts. JSON
// escaping expands control and invalid bytes up to sixfold.
const maxLogEntryBytes = 8 * maxLogLineBytes

// LogWriter appends timestamped lines to a log file. It assembles lines
// across Write calls, so callers can stream output as it arrives. Close
// writes any final line that has no trailing newline.
type LogWriter struct {
	mu      sync.Mutex
	file    *os.File
	closed  bool
	primary *logStream   // lines written through Write
	streams []*logStream // every stream, primary first, for the final flush
}

// logStream assembles lines for one source so that partial lines from
// different sources are not joined.
type logStream struct {
	w       *LogWriter
	pending []byte
}

func newLogWriter(file *os.File) *LogWriter {
	w := &LogWriter{file: file}
	w.primary = &logStream{w: w}
	w.streams = []*logStream{w.primary}
	return w
}

// Write implements io.Writer. Each completed line becomes one entry stamped
// with the time it completed.
func (w *LogWriter) Write(p []byte) (int, error) {
	return w.primary.Write(p)
}

// Stream returns a writer for a second source, such as stderr, that shares
// the file and entry ordering but assembles its own lines.
func (w *LogWriter) Stream() io.Writer {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := &logStream{w: w}
	w.streams = append(w.streams, s)
	return s
}

func (s *logStream) Write(p []byte) (int, error) {
	w := s.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}

	n := len(p)
	for len(p) > 0 {
		room := maxLogLineBytes - len(s.pending)
		// Look one byte past the limit so a line of exactly the limit is
		// not split from its newline.
		look := p
		if len(look) > room+1 {
			look = look[:room+1]
		}
		if i := bytes.IndexByte(look, '\n'); i >= 0 {
			s.pending = append(s.pending, p[:i]...)
			if err := w.writeEntry(bytes.TrimSuffix(s.pending, []byte{'\r'})); err != nil {
				return 0, err
			}
			s.pending = s.pending[:0]
			p = p[i+1:]
			continue
		}
		if len(look) <= room {
			s.pending = append(s.pending, look...)
			break
		}
		s.pending = append(s.pending, p[:room]...)
		p = p[room:]
		cut := completeRunePrefix(s.pending)
		if err := w.writeEntry(s.pending[:cut]); err != nil {
			return 0, err
		}
		s.pending = append(s.pending[:0], s.pending[cut:]...)
	}
	return n, nil
}

// completeRunePrefix returns the length of b without a trailing incomplete
// UTF-8 sequence, so splitting a long line does not corrupt a character.
func completeRunePrefix(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if i > 0 && !utf8.FullRune(b[i:]) {
				return i
			}
			break
		}
	}
	return len(b)
}

// writeEntry appends one entry with a single write. Callers hold w.mu.
func (w *LogWriter) writeEntry(line []byte) error {
	data, err := json.Marshal(LogEntry{Timestamp: time.Now().UTC(), Line: string(line)})
	if err != nil {
		return err
	}
	_, err = w.file.Write(append(data, '\n'))
	return err
}

// Close writes any unterminated final lines and closes the file. Later
// calls do nothing.
func (w *LogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var flushErr error
	for _, s := range w.streams {
		if len(s.pending) > 0 && flushErr == nil {
			flushErr = w.writeEntry(s.pending)
		}
		s.pending = nil
	}
	return errors.Join(flushErr, w.file.Close())
}

// LogWriter returns a writer that timestamps log entries.
func (s *RunStore) LogWriter() (*LogWriter, error) {
	f, err := os.OpenFile(
		filepath.Join(s.dir, "logs.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	return newLogWriter(f), nil
}

// JoinLogWriter returns a timestamped writer for a joined agent's console,
// written to logs.<index>.jsonl so it stays separate from the primary's log.
func (s *RunStore) JoinLogWriter(index int) (*LogWriter, error) {
	f, err := os.OpenFile(
		filepath.Join(s.dir, fmt.Sprintf("logs.%d.jsonl", index)),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("opening join log file: %w", err)
	}
	return newLogWriter(f), nil
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
	scanner.Buffer(nil, maxLogEntryBytes)
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
		0o600,
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
		0o600,
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
		0o600,
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
	return reqs, scanner.Err()
}

// ReadSecretResolutions reads all secret resolutions.
func (s *RunStore) ReadSecretResolutions() ([]SecretResolution, error) {
	f, err := os.Open(filepath.Join(s.dir, "secrets.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var resolutions []SecretResolution
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var res SecretResolution
		if err := json.Unmarshal(scanner.Bytes(), &res); err != nil {
			continue
		}
		resolutions = append(resolutions, res)
	}
	return resolutions, scanner.Err()
}

// ExecEvent represents a command execution captured by the tracer.
// This is a duplicate of trace.ExecEvent to avoid circular imports.
type ExecEvent struct {
	Timestamp  time.Time      `json:"timestamp"`
	PID        int            `json:"pid"`
	PPID       int            `json:"ppid"`
	Command    string         `json:"command"`
	Args       []string       `json:"args"`
	WorkingDir string         `json:"working_dir,omitempty"`
	ExitCode   *int           `json:"exit_code,omitempty"`
	Duration   *time.Duration `json:"duration,omitempty"`
}

// WriteExecEvent writes an execution event to exec.jsonl.
func (s *RunStore) WriteExecEvent(event ExecEvent) error {
	f, err := os.OpenFile(
		filepath.Join(s.dir, "exec.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("opening exec file: %w", err)
	}

	data, err := json.Marshal(event)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("marshaling exec event: %w", err)
	}
	if _, writeErr := f.Write(data); writeErr != nil {
		_ = f.Close()
		return fmt.Errorf("writing exec event: %w", writeErr)
	}
	if _, writeErr := f.Write([]byte("\n")); writeErr != nil {
		_ = f.Close()
		return fmt.Errorf("writing exec event newline: %w", writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("closing exec file: %w", closeErr)
	}
	return nil
}

// ReadExecEvents reads all execution events from exec.jsonl.
func (s *RunStore) ReadExecEvents() ([]ExecEvent, error) {
	f, err := os.Open(filepath.Join(s.dir, "exec.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening exec file: %w", err)
	}
	defer f.Close()

	var events []ExecEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event ExecEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue // Skip malformed entries
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}

// SaveDockerfile saves the Dockerfile used to build the container image.
func (s *RunStore) SaveDockerfile(dockerfile string) error {
	return os.WriteFile(filepath.Join(s.dir, "Dockerfile"), []byte(dockerfile), 0o644)
}

// ReadDockerfile reads the Dockerfile from the run directory.
func (s *RunStore) ReadDockerfile() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "Dockerfile"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DeviceEvent records something that happened on a serial device session.
type DeviceEvent struct {
	Timestamp time.Time `json:"ts"`
	Device    string    `json:"device"`           // config name, e.g. "esp32"
	Kind      string    `json:"kind"`             // attach, detach, settings, modem, control, break, purge, error, conflict
	Detail    string    `json:"detail,omitempty"` // e.g. "dtr-on", "921600 8N1", the device path
	TxBytes   int64     `json:"tx_bytes,omitempty"`
	RxBytes   int64     `json:"rx_bytes,omitempty"`
}

// WriteDeviceEvent appends a serial device event to the log.
func (s *RunStore) WriteDeviceEvent(ev DeviceEvent) error {
	f, err := os.OpenFile(
		filepath.Join(s.dir, "devices.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("opening devices file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshaling device event: %w", err)
	}
	// One write, newline included. Every approved device in a run emits from
	// its own session goroutine into this same file, so splitting the record
	// and its terminator into two O_APPEND writes lets two events interleave
	// into a line that neither ReadDeviceEvents nor `moat devices` can parse —
	// and the reader drops unparseable lines silently, so both events vanish.
	if _, writeErr := f.Write(append(data, '\n')); writeErr != nil {
		return fmt.Errorf("writing device event: %w", writeErr)
	}
	return nil
}

// ReadDeviceEvents returns all recorded serial device events.
func (s *RunStore) ReadDeviceEvents() ([]DeviceEvent, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "devices.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading devices file: %w", err)
	}
	var events []DeviceEvent
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var ev DeviceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // skip malformed lines rather than losing the whole log
		}
		events = append(events, ev)
	}
	return events, nil
}
