package versions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestPythonResolver(server *httptest.Server) *PythonResolver {
	return &PythonResolver{
		HTTPClient: server.Client(),
		url:        server.URL,
	}
}

func TestPythonResolver_Resolve(t *testing.T) {
	mockResponse := `[
		{"cycle": "3.13", "latest": "3.13.1"},
		{"cycle": "3.12", "latest": "3.12.8"},
		{"cycle": "3.11", "latest": "3.11.11"},
		{"cycle": "3.10", "latest": "3.10.16"},
		{"cycle": "3.9", "latest": "3.9.21"},
		{"cycle": "3.8", "latest": "3.8.20"},
		{"cycle": "2.7", "latest": "2.7.18"}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := newTestPythonResolver(server)

	tests := []struct {
		name    string
		version string
		want    string
		wantErr bool
	}{
		{
			name:    "partial 3.12 resolves to latest patch",
			version: "3.12",
			want:    "3.12.8",
		},
		{
			name:    "partial 3.11 resolves to latest patch",
			version: "3.11",
			want:    "3.11.11",
		},
		{
			name:    "partial 3.9 resolves to latest patch",
			version: "3.9",
			want:    "3.9.21",
		},
		{
			name:    "exact version passes through",
			version: "3.11.5",
			want:    "3.11.5",
		},
		{
			name:    "exact version not found",
			version: "3.11.99",
			wantErr: true,
		},
		{
			name:    "nonexistent minor version",
			version: "3.99",
			wantErr: true,
		},
		{
			name:    "invalid format - single part",
			version: "3",
			wantErr: true,
		},
		{
			name:    "python 2.x cycle is accessible",
			version: "2.7",
			want:    "2.7.18",
		},
		{
			name:    "exact version at boundary (patch 0)",
			version: "3.13.0",
			want:    "3.13.0",
		},
		{
			name:    "exact version at boundary (latest patch)",
			version: "3.12.8",
			want:    "3.12.8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.Resolve(context.Background(), tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("Resolve() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Resolve() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPythonResolver_Resolve_malformedLatest(t *testing.T) {
	mockResponse := `[
		{"cycle": "3.12", "latest": "bad-version"}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := newTestPythonResolver(server)

	// Partial version still returns the raw latest (caller can validate downstream)
	got, err := resolver.Resolve(context.Background(), "3.12")
	if err != nil {
		t.Fatalf("Resolve(partial) error = %v", err)
	}
	if got != "bad-version" {
		t.Errorf("Resolve(partial) = %v, want bad-version", got)
	}

	// Exact version with malformed latest returns a clear error
	_, err = resolver.Resolve(context.Background(), "3.12.5")
	if err == nil {
		t.Fatal("Resolve(exact) expected error for malformed latest")
	}
	if got := err.Error(); got != `malformed version "bad-version" in API response for cycle 3.12` {
		t.Errorf("Resolve(exact) error = %q, want malformed version error", got)
	}
}

func TestPythonResolver_LatestStable(t *testing.T) {
	mockResponse := `[
		{"cycle": "3.13", "latest": "3.13.1"},
		{"cycle": "3.12", "latest": "3.12.8"},
		{"cycle": "2.7", "latest": "2.7.18"}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := newTestPythonResolver(server)

	got, err := resolver.LatestStable(context.Background())
	if err != nil {
		t.Fatalf("LatestStable() error = %v", err)
	}

	// Should return 3.13.1 (newest Python 3.x, not 2.7)
	if got != "3.13.1" {
		t.Errorf("LatestStable() = %v, want 3.13.1", got)
	}
}

func TestPythonResolver_LatestStable_noPython3(t *testing.T) {
	mockResponse := `[
		{"cycle": "2.7", "latest": "2.7.18"}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := newTestPythonResolver(server)

	_, err := resolver.LatestStable(context.Background())
	if err == nil {
		t.Error("LatestStable() expected error for no Python 3.x cycles")
	}
}

func TestPythonResolver_Available(t *testing.T) {
	mockResponse := `[
		{"cycle": "3.13", "latest": "3.13.1"},
		{"cycle": "3.12", "latest": "3.12.2"},
		{"cycle": "2.7", "latest": "2.7.18"}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := newTestPythonResolver(server)

	versions, err := resolver.Available(context.Background())
	if err != nil {
		t.Fatalf("Available() error = %v", err)
	}

	if len(versions) == 0 {
		t.Fatal("Available() returned empty list")
	}

	// Should start with 3.13.x (newest first), not include 2.7.x
	if versions[0] != "3.13.1" {
		t.Errorf("Available()[0] = %v, want 3.13.1", versions[0])
	}

	// Should have 2 + 3 = 5 versions (3.13.0-1 + 3.12.0-2), no 2.7.x
	want := 5
	if len(versions) != want {
		t.Errorf("Available() returned %d versions, want %d", len(versions), want)
	}

	// Last version should be 3.12.0
	if versions[len(versions)-1] != "3.12.0" {
		t.Errorf("Available() last = %v, want 3.12.0", versions[len(versions)-1])
	}
}

func TestPythonResolver_Available_ordering(t *testing.T) {
	mockResponse := `[
		{"cycle": "3.11", "latest": "3.11.3"},
		{"cycle": "3.12", "latest": "3.12.1"}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := newTestPythonResolver(server)

	versions, err := resolver.Available(context.Background())
	if err != nil {
		t.Fatalf("Available() error = %v", err)
	}

	// Even though API returns 3.11 before 3.12, output should be sorted newest first
	expected := []string{
		"3.12.1", "3.12.0",
		"3.11.3", "3.11.2", "3.11.1", "3.11.0",
	}

	if len(versions) != len(expected) {
		t.Fatalf("Available() returned %d versions, want %d", len(versions), len(expected))
	}

	for i, v := range versions {
		if v != expected[i] {
			t.Errorf("Available()[%d] = %v, want %v", i, v, expected[i])
		}
	}
}

func TestPythonResolver_httpError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	resolver := newTestPythonResolver(server)

	_, err := resolver.Resolve(context.Background(), "3.12")
	if err == nil {
		t.Error("Resolve() expected error for HTTP 500")
	}

	_, err = resolver.Available(context.Background())
	if err == nil {
		t.Error("Available() expected error for HTTP 500")
	}

	_, err = resolver.LatestStable(context.Background())
	if err == nil {
		t.Error("LatestStable() expected error for HTTP 500")
	}
}
