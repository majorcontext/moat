package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/storage"
)

func TestTruncateDisplay(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{
			name: "short string unchanged",
			s:    "hello",
			max:  10,
			want: "hello",
		},
		{
			name: "exact boundary unchanged",
			s:    "hello",
			max:  5,
			want: "hello",
		},
		{
			name: "long string truncated",
			s:    "hello world this is long",
			max:  10,
			want: "hello worl...",
		},
		{
			name: "empty string",
			s:    "",
			max:  10,
			want: "",
		},
		{
			name: "max of 1",
			s:    "ab",
			max:  1,
			want: "a...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateDisplay(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncateDisplay(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}

// captureStdout captures stdout output from a function call.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	defer func() { os.Stdout = old }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestShowNetworkRequests(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewRunStore(dir, "run_tracetest1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	ts := time.Date(2025, 3, 15, 10, 23, 44, 512000000, time.UTC)
	reqs := []storage.NetworkRequest{
		{
			Timestamp:  ts,
			Method:     "GET",
			URL:        "https://api.github.com/user",
			StatusCode: 200,
			Duration:   89,
		},
		{
			Timestamp:  ts.Add(time.Second),
			Method:     "POST",
			URL:        "https://api.anthropic.com/v1/messages",
			StatusCode: 200,
			Duration:   1200,
		},
	}
	for _, req := range reqs {
		if err := store.WriteNetworkRequest(req); err != nil {
			t.Fatalf("WriteNetworkRequest: %v", err)
		}
	}

	traceVerbose = false
	jsonOut = false
	defer func() { traceVerbose = false; jsonOut = false }()

	output := captureStdout(t, func() {
		if err := showNetworkRequests(store, "run_tracetest1"); err != nil {
			t.Fatalf("showNetworkRequests: %v", err)
		}
	})

	if !strings.Contains(output, "GET") {
		t.Error("output should contain GET method")
	}
	if !strings.Contains(output, "https://api.github.com/user") {
		t.Error("output should contain GitHub URL")
	}
	if !strings.Contains(output, "200") {
		t.Error("output should contain status code 200")
	}
	if !strings.Contains(output, "89ms") {
		t.Error("output should contain duration 89ms")
	}
	if !strings.Contains(output, "POST") {
		t.Error("output should contain POST method")
	}
	// Verbose details should not appear
	if strings.Contains(output, "Headers:") {
		t.Error("output should not contain headers in non-verbose mode")
	}
}

func TestShowNetworkRequestsError(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewRunStore(dir, "run_traceerr1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	ts := time.Date(2025, 3, 15, 10, 23, 44, 0, time.UTC)
	req := storage.NetworkRequest{
		Timestamp: ts,
		Method:    "GET",
		URL:       "https://unreachable.example.com",
		Error:     "dial tcp: no such host",
	}
	if err := store.WriteNetworkRequest(req); err != nil {
		t.Fatalf("WriteNetworkRequest: %v", err)
	}

	traceVerbose = false
	jsonOut = false
	defer func() { traceVerbose = false; jsonOut = false }()

	output := captureStdout(t, func() {
		if err := showNetworkRequests(store, "run_traceerr1"); err != nil {
			t.Fatalf("showNetworkRequests: %v", err)
		}
	})

	if !strings.Contains(output, "ERR") {
		t.Error("output should contain ERR for error requests")
	}
	if !strings.Contains(output, "https://unreachable.example.com") {
		t.Error("output should contain the URL")
	}
}

func TestShowNetworkRequestsVerbose(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewRunStore(dir, "run_traceverb1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	ts := time.Date(2025, 3, 15, 10, 23, 44, 0, time.UTC)
	req := storage.NetworkRequest{
		Timestamp:       ts,
		Method:          "POST",
		URL:             "https://api.example.com/data",
		StatusCode:      201,
		Duration:        150,
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		ResponseHeaders: map[string]string{"X-Request-Id": "abc123"},
		RequestBody:     `{"key":"value"}`,
		ResponseBody:    `{"id":1}`,
		BodyTruncated:   true,
	}
	if err := store.WriteNetworkRequest(req); err != nil {
		t.Fatalf("WriteNetworkRequest: %v", err)
	}

	traceVerbose = true
	defer func() { traceVerbose = false }()

	output := captureStdout(t, func() {
		if err := showNetworkRequests(store, "run_traceverb1"); err != nil {
			t.Fatalf("showNetworkRequests: %v", err)
		}
	})

	if !strings.Contains(output, "Request Headers:") {
		t.Error("output should contain Request Headers")
	}
	if !strings.Contains(output, "Content-Type: application/json") {
		t.Error("output should contain Content-Type header")
	}
	if !strings.Contains(output, "Response Headers:") {
		t.Error("output should contain Response Headers")
	}
	if !strings.Contains(output, "X-Request-Id: abc123") {
		t.Error("output should contain X-Request-Id header")
	}
	if !strings.Contains(output, "Request Body:") {
		t.Error("output should contain Request Body")
	}
	if !strings.Contains(output, `{"key":"value"}`) {
		t.Error("output should contain request body content")
	}
	if !strings.Contains(output, "Response Body:") {
		t.Error("output should contain Response Body")
	}
	if !strings.Contains(output, "[Body truncated") {
		t.Error("output should contain truncation indicator")
	}
}

func TestShowNetworkRequestsJSON(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewRunStore(dir, "run_tracejson1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	ts := time.Date(2025, 3, 15, 10, 23, 44, 0, time.UTC)
	req := storage.NetworkRequest{
		Timestamp:  ts,
		Method:     "GET",
		URL:        "https://api.github.com/user",
		StatusCode: 200,
		Duration:   89,
	}
	if err := store.WriteNetworkRequest(req); err != nil {
		t.Fatalf("WriteNetworkRequest: %v", err)
	}

	jsonOut = true
	defer func() { jsonOut = false }()

	output := captureStdout(t, func() {
		if err := showNetworkRequests(store, "run_tracejson1"); err != nil {
			t.Fatalf("showNetworkRequests: %v", err)
		}
	})

	// Verify it's valid JSON
	var parsed []storage.NetworkRequest
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}
	if len(parsed) != 1 {
		t.Fatalf("got %d requests in JSON, want 1", len(parsed))
	}
	if parsed[0].Method != "GET" {
		t.Errorf("Method = %q, want %q", parsed[0].Method, "GET")
	}
}

func TestShowNetworkRequestsEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewRunStore(dir, "run_traceempty1")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	jsonOut = false
	traceVerbose = false
	defer func() { traceVerbose = false; jsonOut = false }()

	output := captureStdout(t, func() {
		if err := showNetworkRequests(store, "run_traceempty1"); err != nil {
			t.Fatalf("showNetworkRequests: %v", err)
		}
	})

	if !strings.Contains(output, "No network requests recorded") {
		t.Errorf("expected 'No network requests recorded' message, got: %q", output)
	}
}
