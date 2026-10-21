package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/providers/oauth"
)

func TestInheritedOAuthRefreshSharedAcrossRuns(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	previous := provider.Get("oauth")
	provider.Register(&oauth.Provider{})
	t.Cleanup(func() {
		provider.Unregister("oauth")
		if previous != nil {
			provider.Register(previous)
		}
	})
	var exchanges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if got := r.Form.Get("refresh_token"); got != "original-refresh" {
			t.Errorf("unexpected refresh token %q", got)
		}
		if exchanges.Add(1) != 1 {
			t.Error("redeemed rotating refresh token more than once")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600}`)
	}))
	defer server.Close()
	for _, name := range []string{"one", "two"} {
		if err := config.CreateProfile(name, config.ProfileConfig{InheritDefault: true}); err != nil {
			t.Fatal(err)
		}
	}
	key := make([]byte, 32)
	parent, err := credential.NewProfileStore("", key)
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Save(credential.Credential{
		Provider: "oauth:linear", Token: "old", ExpiresAt: time.Now().Add(-time.Minute),
		Metadata: map[string]string{provider.MetaKeyTokenSource: "oauth", "token_url": server.URL, "client_id": "test", "refresh_token": "original-refresh"},
	}); err != nil {
		t.Fatal(err)
	}
	runs := make([]*RunContext, 0, 2)
	stores := make([]*credential.FileStore, 0, 2)
	for _, name := range []string{"one", "two"} {
		rc := NewRunContext(name)
		rc.CredProfile = name
		rc.MCPServers = []config.MCPServerConfig{{Name: "linear", URL: "https://mcp.linear.app/mcp", Auth: &config.MCPAuthConfig{Grant: "oauth:linear", Header: "Authorization"}}}
		rc.SetCredentialWithGrant("mcp.linear.app", "Authorization", "old", "oauth:linear")
		store, err := credential.NewProfileStore(name, key)
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, rc)
		stores = append(stores, store)
	}
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refreshTokensForRun(context.Background(), runs[i], []string{"oauth:linear"}, stores[i])
		}()
	}
	wg.Wait()
	if exchanges.Load() != 1 {
		t.Fatalf("exchanges = %d", exchanges.Load())
	}
	for _, rc := range runs {
		entry, ok := rc.GetCredential("mcp.linear.app")
		if !ok || entry.Value != "rotated-access" {
			t.Fatalf("run %s credential = %v", rc.RunID, entry)
		}
		local, err := credential.NewFileStore(credential.StoreDirForProfile(rc.CredProfile), key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := local.Get("oauth:linear"); !errors.Is(err, credential.ErrNotFound) {
			t.Fatalf("refresh copied inherited grant: %v", err)
		}
	}
	updated, err := parent.Get("oauth:linear")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Token != "rotated-access" || updated.Metadata["refresh_token"] != "rotated-refresh" {
		t.Fatal("parent was not refreshed")
	}
}
