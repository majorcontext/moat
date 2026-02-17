package oauthrelay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	cfg := Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		ProxyPort:    8080,
	}
	relay := New(cfg)
	if relay.Hostname() != "oauthrelay.localhost:8080" {
		t.Errorf("Hostname() = %q, want %q", relay.Hostname(), "oauthrelay.localhost:8080")
	}
}

func TestHandleStart_MissingApp(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	req := httptest.NewRequest("GET", "/start", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "missing required parameter") {
		t.Errorf("body = %q, want error about missing parameter", w.Body.String())
	}
}

func TestHandleStart_RedirectsToGoogle(t *testing.T) {
	relay := New(Config{
		ClientID:     "my-client-id",
		ClientSecret: "my-secret",
		ProxyPort:    8080,
	})

	req := httptest.NewRequest("GET", "/start?app=myapp", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}

	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://accounts.google.com/o/oauth2/v2/auth") {
		t.Errorf("redirect location = %q, want Google OAuth URL", loc)
	}
	if !strings.Contains(loc, "client_id=my-client-id") {
		t.Errorf("redirect location missing client_id: %q", loc)
	}
	if !strings.Contains(loc, url.QueryEscape("oauthrelay.localhost:8080/callback")) {
		t.Errorf("redirect location missing redirect_uri: %q", loc)
	}
	if !strings.Contains(loc, "state=") {
		t.Errorf("redirect location missing state: %q", loc)
	}

	// Verify a flow was recorded
	if relay.PendingFlows() != 1 {
		t.Errorf("PendingFlows() = %d, want 1", relay.PendingFlows())
	}
}

func TestHandleStart_CustomCallbackPath(t *testing.T) {
	relay := New(Config{
		ClientID:     "cid",
		ClientSecret: "cs",
		ProxyPort:    9090,
	})

	req := httptest.NewRequest("GET", "/start?app=myapp&callback_path=/auth/google/callback", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}

	// The callback path is stored but not visible in the Google redirect.
	// It will be used when routing the auth code back to the app.
	if relay.PendingFlows() != 1 {
		t.Errorf("PendingFlows() = %d, want 1", relay.PendingFlows())
	}
}

func TestHandleCallback_MissingParams(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	req := httptest.NewRequest("GET", "/callback", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCallback_UnknownState(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	req := httptest.NewRequest("GET", "/callback?code=authcode&state=unknownstate0000000000000000000000000000000000000000000000000000000000", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "unknown or expired") {
		t.Errorf("body = %q, want error about unknown state", w.Body.String())
	}
}

func TestHandleCallback_RoutesToApp(t *testing.T) {
	relay := New(Config{
		ClientID:     "cid",
		ClientSecret: "cs",
		ProxyPort:    8080,
	})

	// Start a flow
	startReq := httptest.NewRequest("GET", "/start?app=myapp", nil)
	startW := httptest.NewRecorder()
	relay.ServeHTTP(startW, startReq)

	if startW.Code != http.StatusFound {
		t.Fatalf("start: status = %d, want %d", startW.Code, http.StatusFound)
	}

	// Extract state from the Google redirect URL
	loc, err := url.Parse(startW.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parsing redirect URL: %v", err)
	}
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("state parameter not found in redirect URL")
	}

	// Simulate Google callback
	callbackReq := httptest.NewRequest("GET", "/callback?code=abc123&state="+state, nil)
	callbackW := httptest.NewRecorder()
	relay.ServeHTTP(callbackW, callbackReq)

	if callbackW.Code != http.StatusFound {
		t.Fatalf("callback: status = %d, want %d", callbackW.Code, http.StatusFound)
	}

	appRedirect := callbackW.Header().Get("Location")
	if !strings.Contains(appRedirect, "web.myapp.localhost:8080") {
		t.Errorf("app redirect = %q, want to contain web.myapp.localhost:8080", appRedirect)
	}
	if !strings.Contains(appRedirect, "/__auth/callback") {
		t.Errorf("app redirect = %q, want to contain /__auth/callback", appRedirect)
	}
	if !strings.Contains(appRedirect, "code=abc123") {
		t.Errorf("app redirect = %q, want to contain code=abc123", appRedirect)
	}

	// Flow should be consumed
	if relay.PendingFlows() != 0 {
		t.Errorf("PendingFlows() = %d, want 0", relay.PendingFlows())
	}
}

func TestHandleCallback_CustomCallbackPath(t *testing.T) {
	relay := New(Config{
		ClientID:     "cid",
		ClientSecret: "cs",
		ProxyPort:    8080,
	})

	// Start a flow with custom callback path
	startReq := httptest.NewRequest("GET", "/start?app=myapp&callback_path=/auth/google/done", nil)
	startW := httptest.NewRecorder()
	relay.ServeHTTP(startW, startReq)

	loc, _ := url.Parse(startW.Header().Get("Location"))
	state := loc.Query().Get("state")

	// Simulate callback
	callbackReq := httptest.NewRequest("GET", "/callback?code=xyz&state="+state, nil)
	callbackW := httptest.NewRecorder()
	relay.ServeHTTP(callbackW, callbackReq)

	appRedirect := callbackW.Header().Get("Location")
	if !strings.Contains(appRedirect, "/auth/google/done") {
		t.Errorf("app redirect = %q, want custom callback path /auth/google/done", appRedirect)
	}
}

func TestHandleCallback_GoogleError(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	req := httptest.NewRequest("GET", "/callback?error=access_denied&error_description=User+denied+access", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "oauth_error") {
		t.Errorf("body = %q, want oauth_error", w.Body.String())
	}
}

func TestCleanExpired(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	// Manually inject an expired flow
	relay.mu.Lock()
	relay.flows["expired-state"] = pendingFlow{
		App:       "old-app",
		CreatedAt: time.Now().Add(-20 * time.Minute),
	}
	relay.flows["fresh-state"] = pendingFlow{
		App:       "new-app",
		CreatedAt: time.Now(),
	}
	relay.mu.Unlock()

	removed := relay.CleanExpired(10 * time.Minute)
	if removed != 1 {
		t.Errorf("CleanExpired removed %d, want 1", removed)
	}
	if relay.PendingFlows() != 1 {
		t.Errorf("PendingFlows() = %d, want 1", relay.PendingFlows())
	}
}

func TestServeHTTP_NotFound(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	req := httptest.NewRequest("GET", "/unknown", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleCallback_ExpiredFlow(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	// Manually inject an expired flow
	state := "expired0000000000000000000000000000000000000000000000000000000000"
	relay.mu.Lock()
	relay.flows[state] = pendingFlow{
		App:       "myapp",
		CreatedAt: time.Now().Add(-15 * time.Minute),
	}
	relay.mu.Unlock()

	req := httptest.NewRequest("GET", "/callback?code=abc&state="+state, nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("body = %q, want expiry error", w.Body.String())
	}
}

func TestHandleCallback_ReusedState(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	// Start a flow and extract the state
	startReq := httptest.NewRequest("GET", "/start?app=myapp", nil)
	startW := httptest.NewRecorder()
	relay.ServeHTTP(startW, startReq)

	loc, _ := url.Parse(startW.Header().Get("Location"))
	state := loc.Query().Get("state")

	// First callback should succeed
	cb1 := httptest.NewRequest("GET", "/callback?code=code1&state="+state, nil)
	w1 := httptest.NewRecorder()
	relay.ServeHTTP(w1, cb1)
	if w1.Code != http.StatusFound {
		t.Fatalf("first callback: status = %d, want %d", w1.Code, http.StatusFound)
	}

	// Second callback with same state should fail (state is consumed)
	cb2 := httptest.NewRequest("GET", "/callback?code=code2&state="+state, nil)
	w2 := httptest.NewRecorder()
	relay.ServeHTTP(w2, cb2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("reused state: status = %d, want %d", w2.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w2.Body.String(), "unknown or expired") {
		t.Errorf("reused state: body = %q, want unknown/expired error", w2.Body.String())
	}
}

func TestConcurrentOAuthFlows(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	const numFlows = 20
	states := make([]string, numFlows)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Start numFlows concurrent OAuth flows
	wg.Add(numFlows)
	for i := 0; i < numFlows; i++ {
		go func(idx int) {
			defer wg.Done()
			app := "app" + string(rune('A'+idx))
			req := httptest.NewRequest("GET", "/start?app="+app, nil)
			w := httptest.NewRecorder()
			relay.ServeHTTP(w, req)

			if w.Code != http.StatusFound {
				t.Errorf("flow %d: status = %d, want %d", idx, w.Code, http.StatusFound)
				return
			}
			loc, _ := url.Parse(w.Header().Get("Location"))
			mu.Lock()
			states[idx] = loc.Query().Get("state")
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if relay.PendingFlows() != numFlows {
		t.Errorf("PendingFlows() = %d, want %d", relay.PendingFlows(), numFlows)
	}

	// Complete all flows concurrently
	wg.Add(numFlows)
	for i := 0; i < numFlows; i++ {
		go func(idx int) {
			defer wg.Done()
			mu.Lock()
			s := states[idx]
			mu.Unlock()
			if s == "" {
				return
			}
			req := httptest.NewRequest("GET", "/callback?code=code&state="+s, nil)
			w := httptest.NewRecorder()
			relay.ServeHTTP(w, req)
			if w.Code != http.StatusFound {
				t.Errorf("callback %d: status = %d, want %d", idx, w.Code, http.StatusFound)
			}
		}(i)
	}
	wg.Wait()

	if relay.PendingFlows() != 0 {
		t.Errorf("after all callbacks: PendingFlows() = %d, want 0", relay.PendingFlows())
	}
}

func TestHandleCallback_MissingCode(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	req := httptest.NewRequest("GET", "/callback?state=somestate", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "missing required parameters") {
		t.Errorf("body = %q, want missing parameters error", w.Body.String())
	}
}

func TestHandleCallback_MissingState(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	req := httptest.NewRequest("GET", "/callback?code=abc123", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleStart_DefaultCallbackPath(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	// Start without callback_path, should default to /__auth/callback
	req := httptest.NewRequest("GET", "/start?app=myapp", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	loc, _ := url.Parse(w.Header().Get("Location"))
	state := loc.Query().Get("state")

	// Complete the flow
	cbReq := httptest.NewRequest("GET", "/callback?code=abc&state="+state, nil)
	cbW := httptest.NewRecorder()
	relay.ServeHTTP(cbW, cbReq)

	appRedirect := cbW.Header().Get("Location")
	if !strings.Contains(appRedirect, "/__auth/callback") {
		t.Errorf("default callback path should be /__auth/callback, got redirect: %s", appRedirect)
	}
}

func TestCleanExpired_AllExpired(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	relay.mu.Lock()
	relay.flows["s1"] = pendingFlow{App: "a1", CreatedAt: time.Now().Add(-30 * time.Minute)}
	relay.flows["s2"] = pendingFlow{App: "a2", CreatedAt: time.Now().Add(-20 * time.Minute)}
	relay.flows["s3"] = pendingFlow{App: "a3", CreatedAt: time.Now().Add(-15 * time.Minute)}
	relay.mu.Unlock()

	removed := relay.CleanExpired(10 * time.Minute)
	if removed != 3 {
		t.Errorf("CleanExpired removed %d, want 3", removed)
	}
	if relay.PendingFlows() != 0 {
		t.Errorf("PendingFlows() = %d, want 0", relay.PendingFlows())
	}
}

func TestCleanExpired_NoneExpired(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	relay.mu.Lock()
	relay.flows["s1"] = pendingFlow{App: "a1", CreatedAt: time.Now()}
	relay.flows["s2"] = pendingFlow{App: "a2", CreatedAt: time.Now()}
	relay.mu.Unlock()

	removed := relay.CleanExpired(10 * time.Minute)
	if removed != 0 {
		t.Errorf("CleanExpired removed %d, want 0", removed)
	}
	if relay.PendingFlows() != 2 {
		t.Errorf("PendingFlows() = %d, want 2", relay.PendingFlows())
	}
}

func TestCleanExpired_EmptyFlows(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	removed := relay.CleanExpired(10 * time.Minute)
	if removed != 0 {
		t.Errorf("CleanExpired on empty = %d, want 0", removed)
	}
}

func TestHandleStart_StateUniqueness(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	states := make(map[string]bool)
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest("GET", "/start?app=myapp", nil)
		w := httptest.NewRecorder()
		relay.ServeHTTP(w, req)

		loc, _ := url.Parse(w.Header().Get("Location"))
		state := loc.Query().Get("state")
		if states[state] {
			t.Fatalf("duplicate state generated at iteration %d: %s", i, state)
		}
		states[state] = true
	}
	// Each state should be 64 hex chars (32 bytes)
	for s := range states {
		if len(s) != 64 {
			t.Errorf("state length = %d, want 64", len(s))
		}
	}
}

func TestServeHTTP_JSONContentType(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	// Error responses should be JSON
	req := httptest.NewRequest("GET", "/callback", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	// Body should be valid JSON
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Errorf("body is not valid JSON: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Error("JSON response should contain 'error' key")
	}
}

func TestHandleCallback_ExpiredFlowIsRemovedFromMap(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	state := "expiredstate00000000000000000000000000000000000000000000000000000"
	relay.mu.Lock()
	relay.flows[state] = pendingFlow{
		App:       "myapp",
		CreatedAt: time.Now().Add(-15 * time.Minute),
	}
	relay.mu.Unlock()

	// The expired flow is still in the map before callback
	if relay.PendingFlows() != 1 {
		t.Fatalf("before callback: PendingFlows() = %d, want 1", relay.PendingFlows())
	}

	req := httptest.NewRequest("GET", "/callback?code=abc&state="+state, nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	// After expired callback, the state should have been deleted (it was consumed)
	if relay.PendingFlows() != 0 {
		t.Errorf("after expired callback: PendingFlows() = %d, want 0 (expired flow should be removed)", relay.PendingFlows())
	}
}

func TestHandleStart_SpecialCharsInApp(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 8080})

	// App name with hyphens (common pattern)
	req := httptest.NewRequest("GET", "/start?app=my-cool-app", nil)
	w := httptest.NewRecorder()
	relay.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}

	// Extract state and complete flow
	loc, _ := url.Parse(w.Header().Get("Location"))
	state := loc.Query().Get("state")

	cbReq := httptest.NewRequest("GET", "/callback?code=abc&state="+state, nil)
	cbW := httptest.NewRecorder()
	relay.ServeHTTP(cbW, cbReq)

	redirect := cbW.Header().Get("Location")
	if !strings.Contains(redirect, "web.my-cool-app.localhost:8080") {
		t.Errorf("redirect = %q, want to contain 'web.my-cool-app.localhost:8080'", redirect)
	}
}

func TestHostname_DifferentPort(t *testing.T) {
	relay := New(Config{ClientID: "cid", ClientSecret: "cs", ProxyPort: 9999})
	want := "oauthrelay.localhost:9999"
	if relay.Hostname() != want {
		t.Errorf("Hostname() = %q, want %q", relay.Hostname(), want)
	}
}
