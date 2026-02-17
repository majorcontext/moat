package proxy

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
)

// mockServiceManager implements container.ServiceManager for testing.
type mockServiceManager struct {
	mu       sync.Mutex
	started  []container.ServiceConfig
	stopped  []string // container IDs
	startErr error    // if non-nil, StartService returns this error
	failAt   int      // fail StartService on this call index (-1 = never)
	callIdx  int
}

func newMockServiceManager() *mockServiceManager {
	return &mockServiceManager{failAt: -1}
}

func (m *mockServiceManager) StartService(_ context.Context, cfg container.ServiceConfig) (container.ServiceInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.callIdx
	m.callIdx++
	if m.startErr != nil && (m.failAt < 0 || idx == m.failAt) {
		return container.ServiceInfo{}, m.startErr
	}
	m.started = append(m.started, cfg)
	return container.ServiceInfo{ID: fmt.Sprintf("container-%s", cfg.Name)}, nil
}

func (m *mockServiceManager) CheckReady(_ context.Context, _ container.ServiceInfo) error {
	return nil
}

func (m *mockServiceManager) StopService(_ context.Context, info container.ServiceInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = append(m.stopped, info.ID)
	return nil
}

func (m *mockServiceManager) SetNetworkID(_ string) {}

// --- StartChain tests ---

func TestStartChain_EmptyEntries(t *testing.T) {
	chain, err := StartChain(context.Background(), nil, newMockServiceManager(), "run1", "localhost:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chain != nil {
		t.Fatal("expected nil chain for empty entries")
	}
}

func TestStartChain_SingleProxy(t *testing.T) {
	mgr := newMockServiceManager()
	entries := []config.ProxyChainEntry{
		{Name: "squid", Image: "squid:latest", Port: 3128},
	}

	chain, err := StartChain(context.Background(), entries, mgr, "run1", "localhost:9999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}

	// Should have started exactly one container
	mgr.mu.Lock()
	if len(mgr.started) != 1 {
		t.Fatalf("started %d containers, want 1", len(mgr.started))
	}
	cfg := mgr.started[0]
	mgr.mu.Unlock()

	// Container name should include runID and proxy name
	wantName := "moat-proxy-run1-squid"
	if cfg.Name != wantName {
		t.Errorf("container name = %q, want %q", cfg.Name, wantName)
	}

	// Last (only) proxy should point upstream to moat proxy
	wantUpstream := "http://localhost:9999"
	if cfg.Env["HTTP_PROXY"] != wantUpstream {
		t.Errorf("HTTP_PROXY = %q, want %q", cfg.Env["HTTP_PROXY"], wantUpstream)
	}
	if cfg.Env["HTTPS_PROXY"] != wantUpstream {
		t.Errorf("HTTPS_PROXY = %q, want %q", cfg.Env["HTTPS_PROXY"], wantUpstream)
	}

	// Verify chain reports single proxy
	if len(chain.proxies) != 1 {
		t.Fatalf("chain has %d proxies, want 1", len(chain.proxies))
	}
	if chain.proxies[0].Name != "squid" {
		t.Errorf("proxy name = %q, want %q", chain.proxies[0].Name, "squid")
	}
}

func TestStartChain_MultipleProxies_Ordering(t *testing.T) {
	mgr := newMockServiceManager()
	entries := []config.ProxyChainEntry{
		{Name: "filter", Image: "filter:latest", Port: 3128},
		{Name: "cache", Image: "cache:latest", Port: 8080},
		{Name: "logger", Image: "logger:latest", Port: 9090},
	}

	chain, err := StartChain(context.Background(), entries, mgr, "abc", "moat:1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if len(mgr.started) != 3 {
		t.Fatalf("started %d containers, want 3", len(mgr.started))
	}

	// First proxy (filter) -> upstream is second proxy (cache)
	wantUpstream0 := "http://moat-proxy-abc-cache:8080"
	if mgr.started[0].Env["HTTP_PROXY"] != wantUpstream0 {
		t.Errorf("proxy[0] HTTP_PROXY = %q, want %q", mgr.started[0].Env["HTTP_PROXY"], wantUpstream0)
	}

	// Second proxy (cache) -> upstream is third proxy (logger)
	wantUpstream1 := "http://moat-proxy-abc-logger:9090"
	if mgr.started[1].Env["HTTP_PROXY"] != wantUpstream1 {
		t.Errorf("proxy[1] HTTP_PROXY = %q, want %q", mgr.started[1].Env["HTTP_PROXY"], wantUpstream1)
	}

	// Third proxy (logger) -> upstream is moat proxy
	wantUpstream2 := "http://moat:1234"
	if mgr.started[2].Env["HTTP_PROXY"] != wantUpstream2 {
		t.Errorf("proxy[2] HTTP_PROXY = %q, want %q", mgr.started[2].Env["HTTP_PROXY"], wantUpstream2)
	}

	// Chain order matches entry order
	names := chain.Names()
	if len(names) != 3 {
		t.Fatalf("Names() returned %d, want 3", len(names))
	}
	if names[0] != "filter" || names[1] != "cache" || names[2] != "logger" {
		t.Errorf("Names() = %v, want [filter cache logger]", names)
	}
}

func TestStartChain_UserEnvPreserved(t *testing.T) {
	mgr := newMockServiceManager()
	entries := []config.ProxyChainEntry{
		{
			Name:  "myproxy",
			Image: "myproxy:latest",
			Port:  3128,
			Env: map[string]string{
				"CUSTOM_VAR": "custom_value",
				"DEBUG":      "true",
			},
		},
	}

	_, err := StartChain(context.Background(), entries, mgr, "run1", "localhost:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mgr.mu.Lock()
	cfg := mgr.started[0]
	mgr.mu.Unlock()

	// User env should be preserved
	if cfg.Env["CUSTOM_VAR"] != "custom_value" {
		t.Errorf("CUSTOM_VAR = %q, want %q", cfg.Env["CUSTOM_VAR"], "custom_value")
	}
	if cfg.Env["DEBUG"] != "true" {
		t.Errorf("DEBUG = %q, want %q", cfg.Env["DEBUG"], "true")
	}

	// Proxy env vars should also be set
	if cfg.Env["HTTP_PROXY"] == "" {
		t.Error("HTTP_PROXY should be set")
	}
	if cfg.Env["http_proxy"] == "" {
		t.Error("http_proxy (lowercase) should be set")
	}
}

func TestStartChain_AllProxyEnvVarsSet(t *testing.T) {
	mgr := newMockServiceManager()
	entries := []config.ProxyChainEntry{
		{Name: "p1", Image: "p:1", Port: 1111},
	}

	_, err := StartChain(context.Background(), entries, mgr, "run1", "host:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mgr.mu.Lock()
	env := mgr.started[0].Env
	mgr.mu.Unlock()

	expected := "http://host:8080"
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if env[key] != expected {
			t.Errorf("%s = %q, want %q", key, env[key], expected)
		}
	}
}

func TestStartChain_StartFailure_CleansUp(t *testing.T) {
	mgr := newMockServiceManager()
	mgr.startErr = fmt.Errorf("image not found")
	mgr.failAt = 1 // second proxy fails

	entries := []config.ProxyChainEntry{
		{Name: "first", Image: "first:latest", Port: 3128},
		{Name: "second", Image: "missing:latest", Port: 8080},
	}

	chain, err := StartChain(context.Background(), entries, mgr, "run1", "localhost:9999")
	if err == nil {
		t.Fatal("expected error")
	}
	if chain != nil {
		t.Error("expected nil chain on error")
	}

	// Error message should identify the failed proxy
	if got := err.Error(); !containsSubstr(got, "second") {
		t.Errorf("error %q should mention proxy name 'second'", got)
	}

	// The first proxy should have been cleaned up (stopped)
	mgr.mu.Lock()
	if len(mgr.stopped) != 1 {
		t.Fatalf("stopped %d containers, want 1 (cleanup of first)", len(mgr.stopped))
	}
	if mgr.stopped[0] != "container-moat-proxy-run1-first" {
		t.Errorf("stopped container = %q, want %q", mgr.stopped[0], "container-moat-proxy-run1-first")
	}
	mgr.mu.Unlock()
}

func TestStartChain_StartFailure_FirstProxy(t *testing.T) {
	mgr := newMockServiceManager()
	mgr.startErr = fmt.Errorf("pull failed")
	mgr.failAt = 0 // first proxy fails

	entries := []config.ProxyChainEntry{
		{Name: "only", Image: "bad:latest", Port: 3128},
	}

	chain, err := StartChain(context.Background(), entries, mgr, "run1", "localhost:9999")
	if err == nil {
		t.Fatal("expected error")
	}
	if chain != nil {
		t.Error("expected nil chain on error")
	}

	// No proxies were started successfully, so nothing to stop
	mgr.mu.Lock()
	if len(mgr.stopped) != 0 {
		t.Errorf("stopped %d containers, want 0", len(mgr.stopped))
	}
	mgr.mu.Unlock()
}

func TestStartChain_ServiceConfigFields(t *testing.T) {
	mgr := newMockServiceManager()
	entries := []config.ProxyChainEntry{
		{Name: "squid", Image: "squid:6.10", Port: 3128},
	}

	_, err := StartChain(context.Background(), entries, mgr, "myrun", "host:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mgr.mu.Lock()
	cfg := mgr.started[0]
	mgr.mu.Unlock()

	if cfg.Image != "squid:6.10" {
		t.Errorf("Image = %q, want %q", cfg.Image, "squid:6.10")
	}
	if cfg.RunID != "myrun" {
		t.Errorf("RunID = %q, want %q", cfg.RunID, "myrun")
	}
	if cfg.Version != "latest" {
		t.Errorf("Version = %q, want %q", cfg.Version, "latest")
	}
}

// --- EntryAddr tests ---

func TestEntryAddr_NilChain(t *testing.T) {
	var c *Chain
	if addr := c.EntryAddr(); addr != "" {
		t.Errorf("EntryAddr() = %q, want empty", addr)
	}
}

func TestEntryAddr_EmptyChain(t *testing.T) {
	c := &Chain{proxies: nil}
	if addr := c.EntryAddr(); addr != "" {
		t.Errorf("EntryAddr() = %q, want empty", addr)
	}
}

func TestEntryAddr_ReturnsFirstProxy(t *testing.T) {
	c := &Chain{
		proxies: []ChainProxy{
			{Name: "first", Host: "moat-proxy-run1-first", Port: 3128},
			{Name: "second", Host: "moat-proxy-run1-second", Port: 8080},
		},
	}
	want := "moat-proxy-run1-first:3128"
	if got := c.EntryAddr(); got != want {
		t.Errorf("EntryAddr() = %q, want %q", got, want)
	}
}

// --- ContainerIDs tests ---

func TestContainerIDs_NilChain(t *testing.T) {
	var c *Chain
	if ids := c.ContainerIDs(); ids != nil {
		t.Errorf("ContainerIDs() = %v, want nil", ids)
	}
}

func TestContainerIDs_ReturnsAllIDs(t *testing.T) {
	c := &Chain{
		proxies: []ChainProxy{
			{Name: "filter", ContainerID: "abc123"},
			{Name: "cache", ContainerID: "def456"},
		},
	}
	ids := c.ContainerIDs()
	if len(ids) != 2 {
		t.Fatalf("ContainerIDs() has %d entries, want 2", len(ids))
	}
	if ids["filter"] != "abc123" {
		t.Errorf("ids[filter] = %q, want %q", ids["filter"], "abc123")
	}
	if ids["cache"] != "def456" {
		t.Errorf("ids[cache] = %q, want %q", ids["cache"], "def456")
	}
}

// --- Names tests ---

func TestNames_NilChain(t *testing.T) {
	var c *Chain
	if names := c.Names(); names != nil {
		t.Errorf("Names() = %v, want nil", names)
	}
}

func TestNames_PreservesOrder(t *testing.T) {
	c := &Chain{
		proxies: []ChainProxy{
			{Name: "alpha"},
			{Name: "beta"},
			{Name: "gamma"},
		},
	}
	names := c.Names()
	if len(names) != 3 {
		t.Fatalf("Names() has %d entries, want 3", len(names))
	}
	if names[0] != "alpha" || names[1] != "beta" || names[2] != "gamma" {
		t.Errorf("Names() = %v, want [alpha beta gamma]", names)
	}
}

// --- Stop tests ---

func TestStop_NilChain(t *testing.T) {
	// Should not panic
	var c *Chain
	c.Stop(context.Background(), newMockServiceManager())
}

func TestStop_NilServiceManager(t *testing.T) {
	c := &Chain{
		proxies: []ChainProxy{
			{Name: "test", ContainerID: "abc"},
		},
	}
	// Should not panic
	c.Stop(context.Background(), nil)
}

func TestStop_ReverseOrder(t *testing.T) {
	mgr := newMockServiceManager()
	c := &Chain{
		proxies: []ChainProxy{
			{Name: "first", ContainerID: "id-first"},
			{Name: "second", ContainerID: "id-second"},
			{Name: "third", ContainerID: "id-third"},
		},
	}

	c.Stop(context.Background(), mgr)

	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	if len(mgr.stopped) != 3 {
		t.Fatalf("stopped %d containers, want 3", len(mgr.stopped))
	}
	// Reverse order: third, second, first
	if mgr.stopped[0] != "id-third" {
		t.Errorf("stopped[0] = %q, want %q", mgr.stopped[0], "id-third")
	}
	if mgr.stopped[1] != "id-second" {
		t.Errorf("stopped[1] = %q, want %q", mgr.stopped[1], "id-second")
	}
	if mgr.stopped[2] != "id-first" {
		t.Errorf("stopped[2] = %q, want %q", mgr.stopped[2], "id-first")
	}
}

// --- ChainEntryURL tests ---

func TestChainEntryURL_NilChain(t *testing.T) {
	var c *Chain
	if u := c.ChainEntryURL(); u != nil {
		t.Errorf("ChainEntryURL() = %v, want nil", u)
	}
}

func TestChainEntryURL_EmptyChain(t *testing.T) {
	c := &Chain{}
	if u := c.ChainEntryURL(); u != nil {
		t.Errorf("ChainEntryURL() = %v, want nil", u)
	}
}

func TestChainEntryURL_ReturnsHTTPScheme(t *testing.T) {
	c := &Chain{
		proxies: []ChainProxy{
			{Name: "proxy", Host: "myhost", Port: 3128},
		},
	}
	u := c.ChainEntryURL()
	if u == nil {
		t.Fatal("ChainEntryURL() returned nil")
	}
	if u.Scheme != "http" {
		t.Errorf("scheme = %q, want %q", u.Scheme, "http")
	}
	if u.Host != "myhost:3128" {
		t.Errorf("host = %q, want %q", u.Host, "myhost:3128")
	}
}

// --- WrapTransport tests ---

func TestWrapTransport_NilChain(t *testing.T) {
	var c *Chain
	if tr := c.WrapTransport(); tr != nil {
		t.Errorf("WrapTransport() = %v, want nil", tr)
	}
}

func TestWrapTransport_ReturnsTransportWithProxy(t *testing.T) {
	c := &Chain{
		proxies: []ChainProxy{
			{Name: "proxy", Host: "127.0.0.1", Port: 3128},
		},
	}
	tr := c.WrapTransport()
	if tr == nil {
		t.Fatal("WrapTransport() returned nil")
	}
	if tr.Proxy == nil {
		t.Error("Transport.Proxy should be set")
	}
}

// --- Integration: StartChain -> EntryAddr round trip ---

func TestStartChain_EntryAddr_MatchesFirstProxy(t *testing.T) {
	mgr := newMockServiceManager()
	entries := []config.ProxyChainEntry{
		{Name: "entry", Image: "entry:latest", Port: 5555},
		{Name: "exit", Image: "exit:latest", Port: 6666},
	}

	chain, err := StartChain(context.Background(), entries, mgr, "r1", "moat:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "moat-proxy-r1-entry:5555"
	if got := chain.EntryAddr(); got != want {
		t.Errorf("EntryAddr() = %q, want %q", got, want)
	}
}

func TestStartChain_ContainerIDs_MatchStarted(t *testing.T) {
	mgr := newMockServiceManager()
	entries := []config.ProxyChainEntry{
		{Name: "a", Image: "a:1", Port: 1111},
		{Name: "b", Image: "b:2", Port: 2222},
	}

	chain, err := StartChain(context.Background(), entries, mgr, "r2", "moat:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ids := chain.ContainerIDs()
	if ids["a"] != "container-moat-proxy-r2-a" {
		t.Errorf("ids[a] = %q, want %q", ids["a"], "container-moat-proxy-r2-a")
	}
	if ids["b"] != "container-moat-proxy-r2-b" {
		t.Errorf("ids[b] = %q, want %q", ids["b"], "container-moat-proxy-r2-b")
	}
}

// helper
func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
