package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
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

	// OAuth should set CLAUDE_CODE_OAUTH_TOKEN with a placeholder
	// The real token is injected by the proxy at the network layer
	if len(env) != 1 {
		t.Errorf("ContainerEnv() for OAuth returned %d vars, want 1", len(env))
		return
	}
	if env[0] != "CLAUDE_CODE_OAUTH_TOKEN="+credential.ProxyInjectedPlaceholder {
		t.Errorf("env[0] = %q, want %q", env[0], "CLAUDE_CODE_OAUTH_TOKEN="+credential.ProxyInjectedPlaceholder)
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
	if env[0] != "ANTHROPIC_API_KEY="+credential.ProxyInjectedPlaceholder {
		t.Errorf("env[0] = %q, want %q", env[0], "ANTHROPIC_API_KEY="+credential.ProxyInjectedPlaceholder)
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

func TestWriteClaudeConfig(t *testing.T) {
	t.Run("without MCP servers", func(t *testing.T) {
		stagingDir := t.TempDir()

		err := WriteClaudeConfig(stagingDir, nil)
		if err != nil {
			t.Fatalf("WriteClaudeConfig() error = %v", err)
		}

		// Read and parse the file
		data, err := os.ReadFile(filepath.Join(stagingDir, ".claude.json"))
		if err != nil {
			t.Fatalf("failed to read .claude.json: %v", err)
		}

		var config map[string]any
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("failed to parse .claude.json: %v", err)
		}

		// Verify required fields
		if config["hasCompletedOnboarding"] != true {
			t.Error("hasCompletedOnboarding should be true")
		}
		if config["theme"] != "dark" {
			t.Error("theme should be dark")
		}

		// Should not have mcpServers field
		if _, ok := config["mcpServers"]; ok {
			t.Error("mcpServers should not be present when nil")
		}
	})

	t.Run("with MCP servers", func(t *testing.T) {
		stagingDir := t.TempDir()

		mcpServers := map[string]MCPServerForContainer{
			"context7": {
				Type: "http",
				URL:  "https://mcp.context7.com/mcp",
				Headers: map[string]string{
					"CONTEXT7_API_KEY": "moat-stub-mcp-context7",
				},
			},
		}

		err := WriteClaudeConfig(stagingDir, mcpServers)
		if err != nil {
			t.Fatalf("WriteClaudeConfig() error = %v", err)
		}

		// Read and parse the file
		data, err := os.ReadFile(filepath.Join(stagingDir, ".claude.json"))
		if err != nil {
			t.Fatalf("failed to read .claude.json: %v", err)
		}

		var config map[string]any
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("failed to parse .claude.json: %v", err)
		}

		// Verify MCP servers are included
		mcpData, ok := config["mcpServers"].(map[string]any)
		if !ok {
			t.Fatal("mcpServers should be present")
		}

		ctx7, ok := mcpData["context7"].(map[string]any)
		if !ok {
			t.Fatal("context7 server should be present")
		}

		if ctx7["type"] != "http" {
			t.Errorf("expected type 'http', got %v", ctx7["type"])
		}
		if ctx7["url"] != "https://mcp.context7.com/mcp" {
			t.Errorf("expected correct URL, got %v", ctx7["url"])
		}

		headers, ok := ctx7["headers"].(map[string]any)
		if !ok {
			t.Fatal("headers should be present")
		}
		if headers["CONTEXT7_API_KEY"] != "moat-stub-mcp-context7" {
			t.Errorf("expected stub credential, got %v", headers["CONTEXT7_API_KEY"])
		}
	})
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
