package githubscope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupRepos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/old-owner/app":
			nodeID := "MDEwOlJlcG9zaXRvcnk1"
			if r.Header.Get("X-Github-Next-Global-ID") == "1" {
				nodeID = "R_kgDOAAAABQ"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"full_name": "New-Owner/App", "id": 5, "node_id": nodeID})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	meta, errs := LookupRepos(context.Background(), srv.Client(), srv.URL, "tok", []string{"old-owner/app", "acme/missing"})
	require.Contains(t, meta, "old-owner/app")
	m := meta["old-owner/app"]
	assert.Equal(t, "new-owner/app", m.FullName, "canonical name is lowercased")
	assert.Equal(t, int64(5), m.ID)
	assert.ElementsMatch(t, []string{"R_kgDOAAAABQ", "MDEwOlJlcG9zaXRvcnk1"}, m.NodeIDs)

	require.Contains(t, errs, "acme/missing")
	assert.Contains(t, errs["acme/missing"].Error(), "404")
	assert.NotContains(t, meta, "acme/missing")

	// Companion: a bad token fails every lookup instead of returning partial data.
	meta, errs = LookupRepos(context.Background(), srv.Client(), srv.URL, "wrong", []string{"old-owner/app"})
	assert.Empty(t, meta)
	assert.Contains(t, errs, "old-owner/app")
}

func TestLookupFailureReason(t *testing.T) {
	for status, want := range map[int]string{401: "authentication failed", 403: "permissions", 404: "not found", 429: "rate limited", 503: "HTTP 503"} {
		err := fmt.Errorf("wrapped: %w", &LookupHTTPError{Repo: "acme/app", StatusCode: status})
		assert.Contains(t, LookupFailureReason(err), want)
	}
	assert.Contains(t, LookupFailureReason(context.DeadlineExceeded), "timed out")
	assert.Contains(t, LookupFailureReason(&net.DNSError{Err: "no such host", Name: "api.github.com"}), "network failure")
	assert.Contains(t, LookupFailureReason(errors.New("bad response")), "lookup failed")
}

func TestLookupReposRetainsHTTPStatus(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			_, failures := LookupRepos(context.Background(), server.Client(), server.URL, "test-token", []string{"acme/app"})
			var lookup *LookupHTTPError
			require.ErrorAs(t, failures["acme/app"], &lookup)
			require.Equal(t, status, lookup.StatusCode)
		})
	}
}
