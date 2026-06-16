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
	const token = "0123456789abcdef0123456789abcdef" // stand-in for a run's ProxyAuthToken

	var reached bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
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
	// Mirror cmd/moat/cli/daemon.go: resolve the proxy auth token to the run's
	// proxy context. This is what enables the token-aware MCP relay.
	p.SetContextResolver(func(tok string) (*proxy.RunContextData, bool) {
		if tok != token {
			return nil, false
		}
		return rc.ToProxyContextData(), true
	})

	// moat's relay URL shape: /mcp/{token}/{server}.
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+token+"/render",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("relay returned %d, want 200 — issue #348 regression: the token was misrouted as the server name.\nbody: %s",
			res.StatusCode, strings.TrimSpace(string(body)))
	}
	if !reached {
		t.Fatalf("relay did not forward to the upstream MCP server.\nbody: %s", strings.TrimSpace(string(body)))
	}
}
