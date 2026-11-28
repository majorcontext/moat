package run

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/storage"
)

// startAttachedRun runs an interactive session whose guest writes fixed
// output and exits, and returns the stored log lines.
func startAttachedRun(t *testing.T, rt *flexibleRuntime, stdout, stderr io.Writer) []string {
	t.Helper()
	store, err := storage.NewRunStore(t.TempDir(), "run_attachedlog")
	if err != nil {
		t.Fatal(err)
	}
	rt.done = make(chan struct{})
	close(rt.done)
	rt.states = map[string]string{"ctr-attached": "exited"}
	m := newEdgeCaseManager(t, rt)
	r := &Run{
		ID:          "run_attachedlog",
		Name:        "attached-log",
		ContainerID: "ctr-attached",
		Runtime:     string(rt.Type()),
		State:       StateCreated,
		Interactive: true,
		Store:       store,
		exitCh:      make(chan struct{}),
	}
	m.runs[r.ID] = r

	if err := m.StartAttached(context.Background(), r.ID, nil, stdout, stderr, container.TTYSize{}); err != nil {
		t.Fatalf("StartAttached: %v", err)
	}
	entries, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = e.Line
	}
	return lines
}

func assertLogLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("log lines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("log line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStartAttachedStreamsAppleOutputToLog(t *testing.T) {
	runtimeLogs := 0
	rt := &flexibleRuntime{
		runtimeType: container.RuntimeApple,
		startAttachedFn: func(_ context.Context, _ string, opts container.AttachOptions) error {
			// Lines arrive split across writes and streams, and the session
			// ends on an unterminated prompt.
			io.WriteString(opts.Stdout, "first ")
			io.WriteString(opts.Stderr, "warning\n")
			io.WriteString(opts.Stdout, "line\r\nprompt$ ")
			return nil
		},
		containerLogsAllFn: func(context.Context, string) ([]byte, error) {
			runtimeLogs++
			return []byte("runtime copy\n"), nil
		},
	}
	var stdout, stderr bytes.Buffer

	lines := startAttachedRun(t, rt, &stdout, &stderr)

	assertLogLines(t, lines, "warning", "first line", "prompt$ ")
	if got := stdout.String(); got != "first line\r\nprompt$ " {
		t.Errorf("terminal stdout = %q", got)
	}
	if got := stderr.String(); got != "warning\n" {
		t.Errorf("terminal stderr = %q", got)
	}
	if runtimeLogs != 0 {
		t.Errorf("runtime logs fetched %d times; the attachment is the only Apple capture", runtimeLogs)
	}
}

func TestStartAttachedLeavesDockerOutputToRuntimeLogs(t *testing.T) {
	runtimeLogs := 0
	rt := &flexibleRuntime{
		runtimeType: container.RuntimeDocker,
		startAttachedFn: func(_ context.Context, _ string, opts container.AttachOptions) error {
			io.WriteString(opts.Stdout, "from attachment\n")
			return nil
		},
		containerLogsAllFn: func(context.Context, string) ([]byte, error) {
			runtimeLogs++
			return []byte("from runtime\n"), nil
		},
	}
	var stdout bytes.Buffer

	lines := startAttachedRun(t, rt, &stdout, &stdout)

	// One capture, from the runtime, with no second copy from the attachment.
	assertLogLines(t, lines, "from runtime")
	if runtimeLogs != 1 {
		t.Errorf("runtime logs fetched %d times, want 1", runtimeLogs)
	}
	if got := stdout.String(); got != "from attachment\n" {
		t.Errorf("terminal stdout = %q", got)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestLogTeeFollowsTerminalNotLog(t *testing.T) {
	var out, logged bytes.Buffer

	// A failing log does not interrupt the terminal.
	tee := &logTee{out: &out, log: failingWriter{errors.New("disk full")}}
	if n, err := tee.Write([]byte("hello")); n != 5 || err != nil {
		t.Errorf("Write with failing log = %d, %v; want 5, nil", n, err)
	}
	if out.String() != "hello" {
		t.Errorf("terminal got %q, want %q", out.String(), "hello")
	}

	// A failing terminal reports its error and logs nothing it did not show.
	termErr := errors.New("terminal closed")
	tee = &logTee{out: failingWriter{termErr}, log: &logged}
	if n, err := tee.Write([]byte("hello")); n != 0 || !errors.Is(err, termErr) {
		t.Errorf("Write with failing terminal = %d, %v; want 0, %v", n, err, termErr)
	}
	if logged.Len() != 0 {
		t.Errorf("logged %q for output the terminal rejected", logged.String())
	}
}
