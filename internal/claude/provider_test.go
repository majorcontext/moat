package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andybons/moat/internal/credential"
)

func TestAnthropicSetup_Provider(t *testing.T) {
	setup := &AnthropicSetup{}
	if setup.Provider() != credential.ProviderAnthropic {
		t.Errorf("Provider() = %v, want %v", setup.Provider(), credential.ProviderAnthropic)
	}
}

func TestAnthropicSetup_ConfigureProxy_OAuth(t *testing.T) {
	setup := &AnthropicSetup{}
	mockProxy := &mockProxyConfigurer{
		credentials:  make(map[string]string),
		extraHeaders: make(map[string]map[string]string),
	}
	cred := &credential.Credential{Token: "sk-ant-oat01-abc123"}

	setup.ConfigureProxy(mockProxy, cred)

	// OAuth tokens use Bearer auth
	if mockProxy.credentials["api.anthropic.com"] != "Bearer sk-ant-oat01-abc123" {
		t.Errorf("api.anthropic.com credential = %q, want %q", mockProxy.credentials["api.anthropic.com"], "Bearer sk-ant-oat01-abc123")
	}

	// Should also set anthropic-beta header
	if mockProxy.extraHeaders["api.anthropic.com"]["anthropic-beta"] != OAuthBetaHeader {
		t.Errorf("anthropic-beta header = %q, want %q", mockProxy.extraHeaders["api.anthropic.com"]["anthropic-beta"], OAuthBetaHeader)
	}
}

func TestAnthropicSetup_ConfigureProxy_APIKey(t *testing.T) {
	setup := &AnthropicSetup{}
	mockProxy := &mockProxyConfigurer{
		credentials:  make(map[string]string),
		extraHeaders: make(map[string]map[string]string),
	}
	cred := &credential.Credential{Token: "sk-ant-api01-abc123"}

	setup.ConfigureProxy(mockProxy, cred)

	// API keys use x-api-key header
	if mockProxy.credentials["api.anthropic.com"] != "x-api-key: sk-ant-api01-abc123" {
		t.Errorf("api.anthropic.com credential = %q, want %q", mockProxy.credentials["api.anthropic.com"], "x-api-key: sk-ant-api01-abc123")
	}

	// Should NOT set anthropic-beta header for API keys
	if len(mockProxy.extraHeaders["api.anthropic.com"]) != 0 {
		t.Errorf("extra headers should be empty for API keys, got %v", mockProxy.extraHeaders["api.anthropic.com"])
	}
}

func TestAnthropicSetup_ContainerEnv_OAuth(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{Token: "sk-ant-oat01-abc123"}

	env := setup.ContainerEnv(cred)

	// OAuth should not set ANTHROPIC_API_KEY
	if len(env) != 0 {
		t.Errorf("ContainerEnv() for OAuth returned %d vars, want 0", len(env))
	}
}

func TestAnthropicSetup_ContainerEnv_APIKey(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{Token: "sk-ant-api01-abc123"}

	env := setup.ContainerEnv(cred)

	// API key should set ANTHROPIC_API_KEY placeholder
	if len(env) != 1 {
		t.Errorf("ContainerEnv() for API key returned %d vars, want 1", len(env))
		return
	}
	if env[0] != "ANTHROPIC_API_KEY="+ProxyInjectedPlaceholder {
		t.Errorf("env[0] = %q, want %q", env[0], "ANTHROPIC_API_KEY="+ProxyInjectedPlaceholder)
	}
}

func TestAnthropicSetup_RegistrationViaInit(t *testing.T) {
	// The init() function should have registered AnthropicSetup
	setup := credential.GetProviderSetup(credential.ProviderAnthropic)
	if setup == nil {
		t.Fatal("GetProviderSetup(ProviderAnthropic) returned nil - init() may not have run")
	}
	if setup.Provider() != credential.ProviderAnthropic {
		t.Errorf("Provider() = %v, want %v", setup.Provider(), credential.ProviderAnthropic)
	}
}

func TestAnthropicSetup_ContainerMounts_APIKey(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{Token: "sk-ant-api01-abc123"}

	mounts, cleanupPath, err := setup.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Errorf("ContainerMounts() error = %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("ContainerMounts() for API key returned %d mounts, want 0", len(mounts))
	}
	if cleanupPath != "" {
		t.Errorf("ContainerMounts() cleanupPath = %q, want empty", cleanupPath)
	}
}

func TestAnthropicSetup_ContainerMounts_OAuth(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{
		Token:     "sk-ant-oat01-abc123",
		ExpiresAt: time.Now().Add(time.Hour),
		Scopes:    []string{"user:read"},
	}

	// ContainerMounts now returns empty because we use PopulateStagingDir instead
	mounts, cleanupPath, err := setup.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Errorf("ContainerMounts() error = %v", err)
	}

	// Should return empty - staging directory approach is used instead
	if len(mounts) != 0 {
		t.Errorf("ContainerMounts() for OAuth returned %d mounts, want 0 (uses staging dir)", len(mounts))
	}

	// Should return empty cleanup path - staging directory is cleaned up by caller
	if cleanupPath != "" {
		t.Error("ContainerMounts() cleanupPath should be empty (staging dir cleanup handled by caller)")
	}
}

func TestAnthropicSetup_PopulateStagingDir(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{
		Token:     "sk-ant-oat01-abc123",
		ExpiresAt: time.Now().Add(time.Hour),
		Scopes:    []string{"user:read"},
	}

	// Create temp staging directory
	stagingDir := t.TempDir()

	err := setup.PopulateStagingDir(cred, stagingDir)
	if err != nil {
		t.Fatalf("PopulateStagingDir() error = %v", err)
	}

	// Check credentials file was created
	credsFile := filepath.Join(stagingDir, ".credentials.json")
	if _, err := os.Stat(credsFile); err != nil {
		t.Errorf("credentials file should exist: %v", err)
	}
}

func TestAnthropicSetup_PopulateStagingDir_APIKey(t *testing.T) {
	setup := &AnthropicSetup{}
	cred := &credential.Credential{
		Token: "sk-ant-api01-abc123", // API key, not OAuth
	}

	stagingDir := t.TempDir()

	err := setup.PopulateStagingDir(cred, stagingDir)
	if err != nil {
		t.Fatalf("PopulateStagingDir() error = %v", err)
	}

	// API keys don't need credentials file
	credsFile := filepath.Join(stagingDir, ".credentials.json")
	if _, err := os.Stat(credsFile); err == nil {
		t.Error("credentials file should NOT exist for API keys")
	}
}

func TestCopyHostClaudeFiles(t *testing.T) {
	// Create a temp "home" directory with Claude files
	tmpHome := t.TempDir()

	// Create ~/.claude.json
	claudeJSON := filepath.Join(tmpHome, ".claude.json")
	if err := os.WriteFile(claudeJSON, []byte(`{"hasCompletedOnboarding": true}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create ~/.claude/statsig/ with a file
	statsigDir := filepath.Join(tmpHome, ".claude", "statsig")
	if err := os.MkdirAll(statsigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(statsigDir, "flags.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create ~/.claude/stats-cache.json
	statsCache := filepath.Join(tmpHome, ".claude", "stats-cache.json")
	if err := os.WriteFile(statsCache, []byte(`{"firstSessionDate": "2025-01-01"}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Override HOME for the test
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	// Create staging directory
	stagingDir := t.TempDir()

	// Call CopyHostClaudeFiles
	CopyHostClaudeFiles(stagingDir)

	// Verify files were copied
	if _, err := os.Stat(filepath.Join(stagingDir, ".claude.json")); err != nil {
		t.Errorf(".claude.json should have been copied: %v", err)
	}

	if _, err := os.Stat(filepath.Join(stagingDir, "statsig", "flags.json")); err != nil {
		t.Errorf("statsig/flags.json should have been copied: %v", err)
	}

	if _, err := os.Stat(filepath.Join(stagingDir, "stats-cache.json")); err != nil {
		t.Errorf("stats-cache.json should have been copied: %v", err)
	}
}

func TestCopyHostClaudeFiles_MissingFiles(t *testing.T) {
	// Create empty home directory (no Claude files)
	tmpHome := t.TempDir()

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	stagingDir := t.TempDir()

	// Should not panic or error when files don't exist
	CopyHostClaudeFiles(stagingDir)

	// Staging dir should still be empty (no files to copy)
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("staging dir should be empty when no host files exist, got %d entries", len(entries))
	}
}

// mockProxyConfigurer implements credential.ProxyConfigurer for testing.
type mockProxyConfigurer struct {
	credentials  map[string]string
	extraHeaders map[string]map[string]string
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {
	m.credentials[host] = value
}

func (m *mockProxyConfigurer) SetCredentialHeader(host, headerName, headerValue string) {
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) AddExtraHeader(host, headerName, headerValue string) {
	if m.extraHeaders[host] == nil {
		m.extraHeaders[host] = make(map[string]string)
	}
	m.extraHeaders[host][headerName] = headerValue
}
