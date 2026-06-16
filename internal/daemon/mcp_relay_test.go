package daemon

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/majorcontext/gatekeeper/proxy"

	"github.com/majorcontext/moat/internal/config"
)

// TestMCPRelayTokenPathRouting is a regression test for issue #348.
//
// moat builds MCP relay URLs as /mcp/{token}/{server} (internal/run/manager.go),
// and the credential proxy must route them to the per-run MCP server resolved
// from {token} — not treat the token as the server name. The bug surfaced as a
// 404 from the relay:
//
//	MOAT: MCP server '<token>' not configured. Available servers: N. Check moat.yaml.
//
// because the token-aware relay path (proxy.handleDirectMCPRelay, enabled by a
// non-nil context resolver) was not taken, so the request fell through to the
// plain relay which read the first path segment (the token) as the server name.
//
// This drives moat's real RunContext.ToProxyContextData() conversion through the
// gatekeeper proxy with a token-in-path request, so it guards both moat's
// run-context wiring and the relay-routing contract. It is pure HTTP — no
// container runtime required.
func TestMCPRelayTokenPathRouting(t *testing.T) {
	// 64 hex chars, matching a real run's ProxyAuthToken (32 bytes, hex-encoded).
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	newBackend := func(reached *bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			*reached = true
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		}))
	}

	// moat's relay URL shape: /mcp/{token}/{server}.
	newReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/mcp/"+token+"/render",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		r.Header.Set("Content-Type", "application/json")
		return r
	}

	t.Run("token path routes to the per-run server", func(t *testing.T) {
		var reached bool
		backend := newBackend(&reached)
		defer backend.Close()

		// Build the run context the way registration does: per-run MCP servers,
		// keyed by the run's auth token.
		rc := NewRunContext("run_test348")
		rc.AuthToken = token
		rc.NetworkPolicy = "permissive"
		rc.MCPServers = []config.MCPServerConfig{
			{Name: "render", URL: backend.URL},
			{Name: "linear", URL: backend.URL},
		}

		p := proxy.NewProxy()
		// Mirror cmd/moat/cli/daemon.go: resolve the proxy auth token to the
		// run's proxy context. This is what enables the token-aware MCP relay.
		p.SetContextResolver(func(tok string) (*proxy.RunContextData, bool) {
			if tok != token {
				return nil, false
			}
			return rc.ToProxyContextData(), true
		})

		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, newReq())

		res := rec.Result()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("relay returned %d, want 200 — issue #348 regression: the token was misrouted as the server name.\nbody: %s",
				res.StatusCode, strings.TrimSpace(string(body)))
		}
		if !reached {
			t.Fatalf("relay did not forward to the upstream MCP server.\nbody: %s", strings.TrimSpace(string(body)))
		}
	})

	// The red half: without a context resolver the token-aware relay is never
	// taken, the token is read as the server name, and the request 404s — the
	// exact failure mode from #348. This proves the test above would fail on the
	// broken path, not pass incidentally.
	t.Run("without a resolver the token is misrouted (issue #348 failure mode)", func(t *testing.T) {
		p := proxy.NewProxy() // no SetContextResolver
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, newReq())

		res := rec.Result()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 without a context resolver, got %d\nbody: %s", res.StatusCode, strings.TrimSpace(string(body)))
		}
		// The token (first path segment) is what got looked up as the server name.
		// Note: this asserts gatekeeper's current 404 body shape; if a gatekeeper
		// update changes the relay's error wording, update this expectation.
		if !strings.Contains(string(body), token) {
			t.Fatalf("expected the token to be misread as the server name in the 404 body\nbody: %s", strings.TrimSpace(string(body)))
		}
	})
}
