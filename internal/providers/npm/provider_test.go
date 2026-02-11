package npm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

func TestMarshalUnmarshalEntries(t *testing.T) {
	entries := []RegistryEntry{
		{
			Host:        "registry.npmjs.org",
			Token:       "npm_abc123",
			TokenSource: SourceEnv,
		},
		{
			Host:        "npm.company.com",
			Token:       "npm_xyz789",
			Scopes:      []string{"@myorg", "@other"},
			TokenSource: SourceNpmrc,
		},
	}

	marshaled, err := MarshalEntries(entries)
	if err != nil {
		t.Fatalf("MarshalEntries failed: %v", err)
	}

	unmarshaled, err := UnmarshalEntries(marshaled)
	if err != nil {
		t.Fatalf("UnmarshalEntries failed: %v", err)
	}

	if len(unmarshaled) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(unmarshaled))
	}

	if unmarshaled[0].Host != "registry.npmjs.org" {
		t.Errorf("expected host registry.npmjs.org, got %s", unmarshaled[0].Host)
	}
	if unmarshaled[0].Token != "npm_abc123" {
		t.Errorf("expected token npm_abc123, got %s", unmarshaled[0].Token)
	}
	if unmarshaled[1].Host != "npm.company.com" {
		t.Errorf("expected host npm.company.com, got %s", unmarshaled[1].Host)
	}
	if len(unmarshaled[1].Scopes) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(unmarshaled[1].Scopes))
	}
}

func TestUnmarshalEntries_Invalid(t *testing.T) {
	_, err := UnmarshalEntries("not-json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestMergeEntry_NewHost(t *testing.T) {
	existing := []RegistryEntry{
		{Host: "registry.npmjs.org", Token: "npm_old"},
	}
	newEntry := RegistryEntry{Host: "npm.company.com", Token: "npm_new"}

	result := MergeEntry(existing, newEntry)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
}

func TestMergeEntry_ReplaceExisting(t *testing.T) {
	existing := []RegistryEntry{
		{Host: "registry.npmjs.org", Token: "npm_old"},
		{Host: "npm.company.com", Token: "npm_company"},
	}
	newEntry := RegistryEntry{Host: "registry.npmjs.org", Token: "npm_new"}

	result := MergeEntry(existing, newEntry)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0].Token != "npm_new" {
		t.Errorf("expected updated token npm_new, got %s", result[0].Token)
	}
}

func TestMergeEntry_Empty(t *testing.T) {
	newEntry := RegistryEntry{Host: "registry.npmjs.org", Token: "npm_new"}
	result := MergeEntry(nil, newEntry)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
}

// mockProxy records SetCredentialWithGrant calls.
type mockProxy struct {
	credentials []proxyCall
}

type proxyCall struct {
	host, headerName, headerValue, grant string
}

func (m *mockProxy) SetCredential(host, value string)                         {}
func (m *mockProxy) SetCredentialHeader(host, headerName, headerValue string) {}
func (m *mockProxy) AddExtraHeader(host, headerName, headerValue string)      {}
func (m *mockProxy) AddResponseTransformer(host string, transformer credential.ResponseTransformer) {
}
func (m *mockProxy) RemoveRequestHeader(host, headerName string)              {}
func (m *mockProxy) SetTokenSubstitution(host, placeholder, realToken string) {}
func (m *mockProxy) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	m.credentials = append(m.credentials, proxyCall{host, headerName, headerValue, grant})
}

func TestConfigureProxy(t *testing.T) {
	entries := []RegistryEntry{
		{Host: "registry.npmjs.org", Token: "npm_token1"},
		{Host: "npm.company.com", Token: "npm_token2"},
	}
	token, err := MarshalEntries(entries)
	if err != nil {
		t.Fatalf("MarshalEntries failed: %v", err)
	}

	p := &Provider{}
	proxy := &mockProxy{}
	cred := &provider.Credential{
		Provider:  "npm",
		Token:     token,
		CreatedAt: time.Now(),
	}

	p.ConfigureProxy(proxy, cred)

	if len(proxy.credentials) != 2 {
		t.Fatalf("expected 2 proxy calls, got %d", len(proxy.credentials))
	}

	if proxy.credentials[0].host != "registry.npmjs.org" {
		t.Errorf("expected host registry.npmjs.org, got %s", proxy.credentials[0].host)
	}
	if proxy.credentials[0].headerValue != "Bearer npm_token1" {
		t.Errorf("expected Bearer npm_token1, got %s", proxy.credentials[0].headerValue)
	}
	if proxy.credentials[0].grant != "npm" {
		t.Errorf("expected grant npm, got %s", proxy.credentials[0].grant)
	}

	if proxy.credentials[1].host != "npm.company.com" {
		t.Errorf("expected host npm.company.com, got %s", proxy.credentials[1].host)
	}
	if proxy.credentials[1].headerValue != "Bearer npm_token2" {
		t.Errorf("expected Bearer npm_token2, got %s", proxy.credentials[1].headerValue)
	}
}

func TestContainerEnv(t *testing.T) {
	p := &Provider{}
	env := p.ContainerEnv(nil)

	if len(env) != 0 {
		t.Fatalf("expected 0 env vars, got %d: %v", len(env), env)
	}
}

func TestContainerMounts(t *testing.T) {
	entries := []RegistryEntry{
		{Host: "registry.npmjs.org", Token: "npm_token1"},
		{Host: "npm.company.com", Token: "npm_token2", Scopes: []string{"@myorg"}},
	}
	token, err := MarshalEntries(entries)
	if err != nil {
		t.Fatalf("MarshalEntries failed: %v", err)
	}

	p := &Provider{}
	cred := &provider.Credential{
		Provider:  "npm",
		Token:     token,
		CreatedAt: time.Now(),
	}

	mounts, cleanupPath, err := p.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Fatalf("ContainerMounts failed: %v", err)
	}
	defer func() {
		if cleanupPath != "" {
			os.RemoveAll(cleanupPath)
		}
	}()

	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	if mounts[0].Target != "/home/user/.npmrc" {
		t.Errorf("expected target /home/user/.npmrc, got %s", mounts[0].Target)
	}
	if !mounts[0].ReadOnly {
		t.Error("expected mount to be read-only")
	}

	// Verify the generated .npmrc content
	content, err := os.ReadFile(mounts[0].Source)
	if err != nil {
		t.Fatalf("reading generated .npmrc: %v", err)
	}
	npmrcContent := string(content)

	if !strings.Contains(npmrcContent, "@myorg:registry=https://npm.company.com/") {
		t.Errorf("expected scope routing in .npmrc:\n%s", npmrcContent)
	}
	if !strings.Contains(npmrcContent, "//registry.npmjs.org/:_authToken="+NpmTokenPlaceholder) {
		t.Errorf("expected placeholder token for default registry in .npmrc:\n%s", npmrcContent)
	}
	if !strings.Contains(npmrcContent, "//npm.company.com/:_authToken="+NpmTokenPlaceholder) {
		t.Errorf("expected placeholder token for company registry in .npmrc:\n%s", npmrcContent)
	}

	// Verify cleanup path exists
	if cleanupPath == "" {
		t.Error("expected non-empty cleanup path")
	}
	if _, statErr := os.Stat(cleanupPath); os.IsNotExist(statErr) {
		t.Error("cleanup path does not exist")
	}
}

func TestContainerMounts_EmptyEntries(t *testing.T) {
	token, err := MarshalEntries([]RegistryEntry{})
	if err != nil {
		t.Fatalf("MarshalEntries failed: %v", err)
	}

	p := &Provider{}
	cred := &provider.Credential{
		Provider:  "npm",
		Token:     token,
		CreatedAt: time.Now(),
	}

	mounts, cleanupPath, err := p.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Fatalf("ContainerMounts failed: %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("expected 0 mounts, got %d", len(mounts))
	}
	if cleanupPath != "" {
		t.Errorf("expected empty cleanup path, got %s", cleanupPath)
	}
}

func TestCleanup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "moat-npm-test-*")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	// Create a file to verify cleanup
	if writeErr := os.WriteFile(filepath.Join(tmpDir, "test"), []byte("data"), 0600); writeErr != nil {
		t.Fatalf("creating test file: %v", writeErr)
	}

	p := &Provider{}
	p.Cleanup(tmpDir)

	if _, statErr := os.Stat(tmpDir); !os.IsNotExist(statErr) {
		t.Error("expected cleanup to remove directory")
	}
}

func TestCleanup_EmptyPath(t *testing.T) {
	p := &Provider{}
	// Should not panic
	p.Cleanup("")
}

func TestName(t *testing.T) {
	p := &Provider{}
	if p.Name() != "npm" {
		t.Errorf("expected name npm, got %s", p.Name())
	}
}

func TestImpliedDependencies(t *testing.T) {
	p := &Provider{}
	deps := p.ImpliedDependencies()
	if len(deps) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(deps))
	}
	if deps[0] != "node" || deps[1] != "npm" {
		t.Errorf("expected [node, npm], got %v", deps)
	}
}
