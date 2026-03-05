package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

func TestRunPersister_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")

	reg := NewRegistry()
	rc1 := NewRunContext("run-1")
	rc1.Grants = []string{"github"}
	rc1.NetworkPolicy = "strict"
	rc1.NetworkAllow = []string{"api.github.com"}
	rc1.MCPServers = []config.MCPServerConfig{{Name: "test", URL: "https://mcp.example.com"}}
	token1 := reg.Register(rc1)

	rc2 := NewRunContext("run-2")
	rc2.ContainerID = "container-abc"
	rc2.Grants = []string{"claude"}
	rc2.AWSConfig = &AWSConfig{RoleARN: "arn:aws:iam::123:role/test", Region: "us-east-1"}
	rc2.TransformerSpecs = []TransformerSpec{
		{Host: "api.github.com", Kind: "response-scrub"},
		{Host: "api.anthropic.com", Kind: "oauth-endpoint-workaround"},
	}
	token2 := reg.Register(rc2)

	p := NewRunPersister(path, reg)
	if err := p.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("Load returned %d runs, want 2", len(loaded))
	}

	byID := make(map[string]PersistedRun)
	for _, pr := range loaded {
		byID[pr.RunID] = pr
	}

	pr1, ok := byID["run-1"]
	if !ok {
		t.Fatal("missing run-1 in loaded data")
	}
	if pr1.AuthToken != token1 {
		t.Errorf("run-1 AuthToken = %q, want %q", pr1.AuthToken, token1)
	}
	if pr1.NetworkPolicy != "strict" {
		t.Errorf("run-1 NetworkPolicy = %q, want %q", pr1.NetworkPolicy, "strict")
	}
	if len(pr1.Grants) != 1 || pr1.Grants[0] != "github" {
		t.Errorf("run-1 Grants = %v, want [github]", pr1.Grants)
	}
	if len(pr1.MCPServers) != 1 || pr1.MCPServers[0].Name != "test" {
		t.Errorf("run-1 MCPServers = %v, want [{test https://mcp.example.com}]", pr1.MCPServers)
	}

	pr2, ok := byID["run-2"]
	if !ok {
		t.Fatal("missing run-2 in loaded data")
	}
	if pr2.AuthToken != token2 {
		t.Errorf("run-2 AuthToken = %q, want %q", pr2.AuthToken, token2)
	}
	if pr2.ContainerID != "container-abc" {
		t.Errorf("run-2 ContainerID = %q, want %q", pr2.ContainerID, "container-abc")
	}
	if pr2.AWSConfig == nil || pr2.AWSConfig.RoleARN != "arn:aws:iam::123:role/test" {
		t.Errorf("run-2 AWSConfig = %v, want role ARN arn:aws:iam::123:role/test", pr2.AWSConfig)
	}
	if len(pr2.TransformerSpecs) != 2 {
		t.Errorf("run-2 TransformerSpecs len = %d, want 2", len(pr2.TransformerSpecs))
	} else if pr2.TransformerSpecs[0].Kind != "response-scrub" {
		t.Errorf("run-2 TransformerSpecs[0].Kind = %q, want %q", pr2.TransformerSpecs[0].Kind, "response-scrub")
	}
}

func TestRunPersister_LoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	reg := NewRegistry()
	p := NewRunPersister(path, reg)

	runs, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if runs != nil {
		t.Errorf("Load returned %v, want nil", runs)
	}
}

func TestRunPersister_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")

	reg := NewRegistry()
	rc := NewRunContext("run-1")
	reg.Register(rc)

	p := NewRunPersister(path, reg)
	if err := p.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify no temp files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "runs.json" {
			t.Errorf("unexpected file in dir: %s", e.Name())
		}
	}
}

func TestRunPersister_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")

	reg := NewRegistry()
	rc := NewRunContext("run-1")
	reg.Register(rc)

	p := NewRunPersister(path, reg)
	if err := p.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

func TestRunPersister_SaveEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")

	reg := NewRegistry()
	p := NewRunPersister(path, reg)

	if err := p.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("Load returned %d runs, want 0", len(loaded))
	}
}

func TestRestoreRuns_Empty(t *testing.T) {
	reg := NewRegistry()
	n := RestoreRuns(context.Background(), reg, nil)
	if n != 0 {
		t.Errorf("RestoreRuns(nil) = %d, want 0", n)
	}
	n = RestoreRuns(context.Background(), reg, []PersistedRun{})
	if n != 0 {
		t.Errorf("RestoreRuns([]) = %d, want 0", n)
	}
}

func TestRunPersister_SaveDebouncedCoalesces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")

	reg := NewRegistry()
	p := NewRunPersister(path, reg)

	// Register a run, then call SaveDebounced multiple times rapidly.
	rc := NewRunContext("run-1")
	reg.Register(rc)
	p.SaveDebounced()
	p.SaveDebounced()
	p.SaveDebounced()

	// File should not exist yet (within debounce window).
	if _, err := os.Stat(path); err == nil {
		t.Error("file exists before debounce timer fires")
	}

	// Poll for the file to appear (avoids flaky time.Sleep on slow CI).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// File should now exist with the final state.
	loaded, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("Load returned %d runs, want 1", len(loaded))
	}
	if loaded[0].RunID != "run-1" {
		t.Errorf("RunID = %q, want %q", loaded[0].RunID, "run-1")
	}
}

func TestRunPersister_Flush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")

	reg := NewRegistry()
	rc := NewRunContext("run-1")
	reg.Register(rc)

	p := NewRunPersister(path, reg)
	p.SaveDebounced()

	// Flush should write immediately without waiting for the timer.
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	loaded, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("Load returned %d runs, want 1", len(loaded))
	}
}

func TestRunPersister_SaveOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")

	reg := NewRegistry()
	rc := NewRunContext("run-1")
	token := reg.Register(rc)

	p := NewRunPersister(path, reg)
	if err := p.Save(); err != nil {
		t.Fatalf("Save 1: %v", err)
	}

	// Unregister and save again — file should reflect empty registry.
	reg.Unregister(token)
	if err := p.Save(); err != nil {
		t.Fatalf("Save 2: %v", err)
	}

	loaded, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("Load after unregister returned %d runs, want 0", len(loaded))
	}
}

func TestRunPersister_VersionedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")

	reg := NewRegistry()
	rc := NewRunContext("run-1")
	reg.Register(rc)

	p := NewRunPersister(path, reg)
	if err := p.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Read raw JSON and verify structure.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := raw["version"]; !ok {
		t.Error("missing 'version' key in persisted file")
	}
	if _, ok := raw["runs"]; !ok {
		t.Error("missing 'runs' key in persisted file")
	}
}

func TestRunPersister_LoadLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")

	// Write a legacy bare-array format.
	legacy := `[{"auth_token":"tok-1","run_id":"run-1"}]`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := NewRegistry()
	p := NewRunPersister(path, reg)
	loaded, err := p.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].RunID != "run-1" {
		t.Errorf("Load legacy = %v, want [{run-1}]", loaded)
	}
}

// mockStore is a minimal credential.Store for testing resolveCredentials.
type mockStore struct {
	creds map[credential.Provider]*credential.Credential
}

func (m *mockStore) Save(_ credential.Credential) error { return nil }
func (m *mockStore) Get(p credential.Provider) (*credential.Credential, error) {
	c, ok := m.creds[p]
	if !ok {
		return nil, fmt.Errorf("not found: %s", p)
	}
	return c, nil
}
func (m *mockStore) Delete(_ credential.Provider) error { return nil }
func (m *mockStore) List() ([]credential.Credential, error) {
	out := make([]credential.Credential, 0, len(m.creds))
	for _, c := range m.creds {
		out = append(out, *c)
	}
	return out, nil
}

func TestResolveCredentials_SSHSkipped(t *testing.T) {
	rc := NewRunContext("run-1")
	store := &mockStore{creds: map[credential.Provider]*credential.Credential{}}

	// SSH grants should be silently skipped.
	if err := resolveCredentials(rc, []string{"ssh"}, nil, store); err != nil {
		t.Fatalf("resolveCredentials(ssh) = %v, want nil", err)
	}
	if len(rc.Credentials) != 0 {
		t.Errorf("Credentials = %v, want empty", rc.Credentials)
	}
}

func TestResolveCredentials_MissingCredential(t *testing.T) {
	rc := NewRunContext("run-1")
	store := &mockStore{creds: map[credential.Provider]*credential.Credential{}}

	err := resolveCredentials(rc, []string{"nonexistent"}, nil, store)
	if err == nil {
		t.Fatal("resolveCredentials(nonexistent) = nil, want error")
	}
}

func TestResolveCredentials_MCPGrant(t *testing.T) {
	rc := NewRunContext("run-1")
	store := &mockStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-test": {Provider: "mcp-test", Token: "mcp-token-123"},
		},
	}
	mcpServers := []config.MCPServerConfig{
		{
			Name: "test-mcp",
			URL:  "https://mcp.example.com/v1",
			Auth: &config.MCPAuthConfig{
				Grant:  "mcp-test",
				Header: "Authorization",
			},
		},
	}

	if err := resolveCredentials(rc, []string{"mcp-test"}, mcpServers, store); err != nil {
		t.Fatalf("resolveCredentials(mcp-test) = %v", err)
	}

	cred, ok := rc.GetCredential("mcp.example.com")
	if !ok {
		t.Fatal("credential not set for mcp.example.com")
	}
	if cred.Value != "mcp-token-123" {
		t.Errorf("credential value = %q, want %q", cred.Value, "mcp-token-123")
	}
	if cred.Grant != "mcp-test" {
		t.Errorf("credential grant = %q, want %q", cred.Grant, "mcp-test")
	}
}

func TestResolveCredentials_EmptyGrants(t *testing.T) {
	rc := NewRunContext("run-1")
	store := &mockStore{creds: map[credential.Provider]*credential.Credential{}}

	if err := resolveCredentials(rc, nil, nil, store); err != nil {
		t.Fatalf("resolveCredentials(nil) = %v, want nil", err)
	}
	if err := resolveCredentials(rc, []string{}, nil, store); err != nil {
		t.Fatalf("resolveCredentials([]) = %v, want nil", err)
	}
}
