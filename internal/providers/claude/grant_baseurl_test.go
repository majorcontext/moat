package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateBaseURL(t *testing.T) {
	valid := []struct {
		raw  string
		want string
	}{
		{raw: "https://gw.lunaroute.com", want: "https://gw.lunaroute.com"},
		{raw: "http://localhost:8787", want: "http://localhost:8787"},
		{raw: "https://gw.example.com/anthropic", want: "https://gw.example.com/anthropic"},
		// A trailing slash is stripped: the path is joined onto this later, and
		// "https://gw/" + "/v1/messages" would double the separator.
		{raw: "https://gw.example.com/", want: "https://gw.example.com"},
	}
	for _, tt := range valid {
		got, err := ValidateBaseURL(tt.raw)
		if err != nil {
			t.Errorf("ValidateBaseURL(%q): unexpected error: %v", tt.raw, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ValidateBaseURL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}

	// Companion cases: the same rules moat.yaml's claude.base_url enforces, so
	// the two sources cannot disagree about what a usable endpoint is.
	invalid := []string{
		"",                     // empty
		"gw.lunaroute.com",     // no scheme
		"ftp://gw.example.com", // wrong scheme
		"file:///etc/passwd",   // wrong scheme
		"http://",              // no host
		"https://gw\x7f.com",   // control character
	}
	for _, raw := range invalid {
		if got, err := ValidateBaseURL(raw); err == nil {
			t.Errorf("ValidateBaseURL(%q) = %q, want an error", raw, got)
		}
	}
}

func TestBaseURLContext(t *testing.T) {
	// Absent by default — an ordinary `moat grant anthropic` must not look like
	// a gateway grant.
	if got := BaseURLFromContext(context.Background()); got != "" {
		t.Errorf("BaseURLFromContext(empty) = %q, want empty", got)
	}

	ctx := WithBaseURL(context.Background(), "https://gw.lunaroute.com")
	if got := BaseURLFromContext(ctx); got != "https://gw.lunaroute.com" {
		t.Errorf("BaseURLFromContext = %q, want %q", got, "https://gw.lunaroute.com")
	}
}

func TestValidateGatewayKey(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "accepted", status: http.StatusOK, wantErr: false},
		// A gateway serves its own model catalog, so the validation model is
		// usually unknown to it. The key still authenticated.
		{name: "unknown model 400", status: http.StatusBadRequest, wantErr: false},
		{name: "unknown model 404", status: http.StatusNotFound, wantErr: false},
		{name: "rate limited", status: http.StatusTooManyRequests, wantErr: false},
		// Only auth failures are failures.
		{name: "bad key 401", status: http.StatusUnauthorized, wantErr: true},
		{name: "forbidden 403", status: http.StatusForbidden, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotKey, gotAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotKey = r.Header.Get("x-api-key")
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			auth := &anthropicAuth{HTTPClient: srv.Client()}
			err := auth.ValidateGatewayKey(context.Background(), "lr_test_key", srv.URL)

			if tt.wantErr && err == nil {
				t.Errorf("status %d: expected an error, got nil", tt.status)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("status %d: unexpected error: %v", tt.status, err)
			}
			if gotPath != "/v1/messages" {
				t.Errorf("requested path = %q, want /v1/messages", gotPath)
			}
			// The key must be sent as x-api-key — the header moat injects at
			// runtime — so a Bearer-only gateway fails here, not later.
			if gotKey != "lr_test_key" {
				t.Errorf("x-api-key = %q, want the key", gotKey)
			}
			if gotAuth != "" {
				t.Errorf("Authorization = %q, want it unset", gotAuth)
			}
		})
	}
}

func TestValidateGatewayKeyTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	auth := &anthropicAuth{HTTPClient: srv.Client()}
	if err := auth.ValidateGatewayKey(context.Background(), "lr_test_key", srv.URL+"/"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("requested path = %q, want /v1/messages (no doubled slash)", gotPath)
	}
}

func TestValidateGatewayKeyUnreachable(t *testing.T) {
	auth := &anthropicAuth{}
	// Port 0 is never listening, so this exercises the transport-error path
	// rather than a status mapping.
	if err := auth.ValidateGatewayKey(context.Background(), "lr_test_key", "http://127.0.0.1:0"); err == nil {
		t.Error("expected an error for an unreachable endpoint, got nil")
	}
}
