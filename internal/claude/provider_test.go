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

	// Should NOT inject extra headers - client controls API version headers
	if len(mockProxy.extraHeaders["api.anthropic.com"]) != 0 {
		t.Errorf("extra headers should be empty, got %v", mockProxy.extraHeaders["api.anthropic.com"])
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

		err := WriteClaudeConfig(stagingDir, nil, nil)
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

		err := WriteClaudeConfig(stagingDir, mcpServers, nil)
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

	t.Run("with host config", func(t *testing.T) {
		stagingDir := t.TempDir()

		hostConfig := map[string]any{
			"userID":         "user-123",
			"firstStartTime": float64(1700000000),
			"oauthAccount": map[string]any{
				"accountUuid":      "acc-uuid",
				"organizationUuid": "org-uuid",
				"emailAddress":     "test@example.com",
			},
			"sonnet45MigrationComplete": true,
		}

		err := WriteClaudeConfig(stagingDir, nil, hostConfig)
		if err != nil {
			t.Fatalf("WriteClaudeConfig() error = %v", err)
		}

		data, err := os.ReadFile(filepath.Join(stagingDir, ".claude.json"))
		if err != nil {
			t.Fatalf("failed to read .claude.json: %v", err)
		}

		var config map[string]any
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("failed to parse .claude.json: %v", err)
		}

		// Host config fields should be present
		if config["userID"] != "user-123" {
			t.Errorf("userID = %v, want user-123", config["userID"])
		}
		if config["firstStartTime"] != float64(1700000000) {
			t.Errorf("firstStartTime = %v, want 1700000000", config["firstStartTime"])
		}
		if config["sonnet45MigrationComplete"] != true {
			t.Errorf("sonnet45MigrationComplete = %v, want true", config["sonnet45MigrationComplete"])
		}

		oauthAccount, ok := config["oauthAccount"].(map[string]any)
		if !ok {
			t.Fatal("oauthAccount should be present")
		}
		if oauthAccount["accountUuid"] != "acc-uuid" {
			t.Errorf("oauthAccount.accountUuid = %v, want acc-uuid", oauthAccount["accountUuid"])
		}

		// Our explicit fields should still take precedence
		if config["hasCompletedOnboarding"] != true {
			t.Error("hasCompletedOnboarding should be true")
		}
		if config["theme"] != "dark" {
			t.Error("theme should be dark")
		}
	})

	t.Run("host config does not override explicit fields", func(t *testing.T) {
		stagingDir := t.TempDir()

		hostConfig := map[string]any{
			"hasCompletedOnboarding": false,
			"theme":                  "light",
			"userID":                 "user-456",
		}

		err := WriteClaudeConfig(stagingDir, nil, hostConfig)
		if err != nil {
			t.Fatalf("WriteClaudeConfig() error = %v", err)
		}

		data, err := os.ReadFile(filepath.Join(stagingDir, ".claude.json"))
		if err != nil {
			t.Fatalf("failed to read .claude.json: %v", err)
		}

		var config map[string]any
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("failed to parse .claude.json: %v", err)
		}

		// Explicit fields must take precedence
		if config["hasCompletedOnboarding"] != true {
			t.Errorf("hasCompletedOnboarding = %v, want true (should override host config)", config["hasCompletedOnboarding"])
		}
		if config["theme"] != "dark" {
			t.Errorf("theme = %v, want dark (should override host config)", config["theme"])
		}

		// Non-conflicting host config should still be present
		if config["userID"] != "user-456" {
			t.Errorf("userID = %v, want user-456", config["userID"])
		}
	})
}

func TestReadHostConfig(t *testing.T) {
	t.Run("missing file returns nil", func(t *testing.T) {
		result, err := ReadHostConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
		if err != nil {
			t.Fatalf("ReadHostConfig() error = %v, want nil", err)
		}
		if result != nil {
			t.Errorf("ReadHostConfig() = %v, want nil", result)
		}
	})

	t.Run("filters to allowlist", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude.json")

		full := map[string]any{
			"userID":                    "user-789",
			"firstStartTime":            float64(1700000000),
			"sonnet45MigrationComplete": true,
			"theme":                     "light",
			"hasCompletedOnboarding":    false,
			"secretField":               "should-not-appear",
			"oauthAccount": map[string]any{
				"accountUuid": "acc-uuid",
			},
		}

		data, _ := json.Marshal(full)
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		result, err := ReadHostConfig(path)
		if err != nil {
			t.Fatalf("ReadHostConfig() error = %v", err)
		}

		// Allowlisted fields should be present
		if result["userID"] != "user-789" {
			t.Errorf("userID = %v, want user-789", result["userID"])
		}
		if result["firstStartTime"] != float64(1700000000) {
			t.Errorf("firstStartTime = %v, want 1700000000", result["firstStartTime"])
		}
		if result["sonnet45MigrationComplete"] != true {
			t.Errorf("sonnet45MigrationComplete = %v, want true", result["sonnet45MigrationComplete"])
		}

		oauthAccount, ok := result["oauthAccount"].(map[string]any)
		if !ok {
			t.Fatal("oauthAccount should be present")
		}
		if oauthAccount["accountUuid"] != "acc-uuid" {
			t.Errorf("oauthAccount.accountUuid = %v, want acc-uuid", oauthAccount["accountUuid"])
		}

		// Non-allowlisted fields should be filtered out
		if _, ok := result["theme"]; ok {
			t.Error("theme should not be in result (not allowlisted)")
		}
		if _, ok := result["hasCompletedOnboarding"]; ok {
			t.Error("hasCompletedOnboarding should not be in result (not allowlisted)")
		}
		if _, ok := result["secretField"]; ok {
			t.Error("secretField should not be in result (not allowlisted)")
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude.json")

		if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		_, err := ReadHostConfig(path)
		if err == nil {
			t.Error("ReadHostConfig() should return error for invalid JSON")
		}
	})

	t.Run("empty object returns empty map", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude.json")

		if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		result, err := ReadHostConfig(path)
		if err != nil {
			t.Fatalf("ReadHostConfig() error = %v", err)
		}
		if len(result) != 0 {
			t.Errorf("ReadHostConfig() = %v, want empty map", result)
		}
	})
}

// mockProxyConfigurer implements credential.ProxyConfigurer for testing.
type mockProxyConfigurer struct {
	credentials  map[string]string
	extraHeaders map[string]map[string]string
	transformers map[string][]credential.ResponseTransformer
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

func (m *mockProxyConfigurer) AddResponseTransformer(host string, transformer credential.ResponseTransformer) {
	if m.transformers == nil {
		m.transformers = make(map[string][]credential.ResponseTransformer)
	}
	m.transformers[host] = append(m.transformers[host], transformer)
}
