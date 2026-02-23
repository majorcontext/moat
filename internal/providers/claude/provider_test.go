package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/spf13/cobra"
)

func TestOAuthProvider_Name(t *testing.T) {
	p := &OAuthProvider{}
	if got := p.Name(); got != "claude" {
		t.Errorf("Name() = %q, want %q", got, "claude")
	}
}

func TestAnthropicProvider_Name(t *testing.T) {
	p := &AnthropicProvider{}
	if got := p.Name(); got != "anthropic" {
		t.Errorf("Name() = %q, want %q", got, "anthropic")
	}
}

func TestOAuthProvider_ConfigureProxy(t *testing.T) {
	p := &OAuthProvider{}
	mockProxy := &mockProxyConfigurer{
		credentials:  make(map[string]string),
		extraHeaders: make(map[string]map[string]string),
	}
	cred := &provider.Credential{Token: "sk-ant-oat01-abc123"}

	p.ConfigureProxy(mockProxy, cred)

	// OAuth tokens use Bearer auth (stored as "Header: Value" format)
	want := "Authorization: Bearer sk-ant-oat01-abc123"
	if mockProxy.credentials["api.anthropic.com"] != want {
		t.Errorf("api.anthropic.com credential = %q, want %q", mockProxy.credentials["api.anthropic.com"], want)
	}

	// Should strip x-api-key to prevent conflict with Authorization header
	removed := mockProxy.removedHeaders["api.anthropic.com"]
	foundXAPIKey := false
	for _, h := range removed {
		if h == "x-api-key" {
			foundXAPIKey = true
		}
	}
	if !foundXAPIKey {
		t.Error("expected x-api-key to be removed for OAuth tokens")
	}

	// Should have beta header
	if mockProxy.extraHeaders["api.anthropic.com"]["anthropic-beta"] != "oauth-2025-04-20" {
		t.Error("expected anthropic-beta header for OAuth tokens")
	}

	// Should have registered a transformer for OAuth tokens
	if len(mockProxy.transformers["api.anthropic.com"]) == 0 {
		t.Error("expected transformer to be registered for OAuth tokens")
	}
}

func TestAnthropicProvider_ConfigureProxy(t *testing.T) {
	p := &AnthropicProvider{}
	mockProxy := &mockProxyConfigurer{
		credentials:  make(map[string]string),
		extraHeaders: make(map[string]map[string]string),
	}
	cred := &provider.Credential{Token: "sk-ant-api01-abc123"}

	p.ConfigureProxy(mockProxy, cred)

	// API keys use x-api-key header
	if mockProxy.credentials["api.anthropic.com"] != "x-api-key: sk-ant-api01-abc123" {
		t.Errorf("api.anthropic.com credential = %q, want %q", mockProxy.credentials["api.anthropic.com"], "x-api-key: sk-ant-api01-abc123")
	}

	// Should NOT have registered a transformer for API keys
	if len(mockProxy.transformers["api.anthropic.com"]) != 0 {
		t.Error("expected no transformer for API keys")
	}

	// Should NOT have removed any headers
	if len(mockProxy.removedHeaders["api.anthropic.com"]) != 0 {
		t.Error("expected no removed headers for API keys")
	}

	// Should NOT have extra headers
	if len(mockProxy.extraHeaders["api.anthropic.com"]) != 0 {
		t.Error("expected no extra headers for API keys")
	}
}

func TestOAuthProvider_ContainerEnv(t *testing.T) {
	p := &OAuthProvider{}
	cred := &provider.Credential{Token: "sk-ant-oat01-abc123"}

	env := p.ContainerEnv(cred)

	// OAuth should set CLAUDE_CODE_OAUTH_TOKEN with a placeholder
	if len(env) != 1 {
		t.Errorf("ContainerEnv() for OAuth returned %d vars, want 1", len(env))
		return
	}
	if env[0] != "CLAUDE_CODE_OAUTH_TOKEN="+ProxyInjectedPlaceholder {
		t.Errorf("env[0] = %q, want %q", env[0], "CLAUDE_CODE_OAUTH_TOKEN="+ProxyInjectedPlaceholder)
	}
}

func TestAnthropicProvider_ContainerEnv(t *testing.T) {
	p := &AnthropicProvider{}
	cred := &provider.Credential{Token: "sk-ant-api01-abc123"}

	env := p.ContainerEnv(cred)

	// API key should set ANTHROPIC_API_KEY placeholder
	if len(env) != 1 {
		t.Errorf("ContainerEnv() for API key returned %d vars, want 1", len(env))
		return
	}
	if env[0] != "ANTHROPIC_API_KEY="+ProxyInjectedPlaceholder {
		t.Errorf("env[0] = %q, want %q", env[0], "ANTHROPIC_API_KEY="+ProxyInjectedPlaceholder)
	}
}

func TestContainerEnvForCredential(t *testing.T) {
	t.Run("nil credential uses API key placeholder", func(t *testing.T) {
		env := containerEnvForCredential(nil)
		if len(env) != 1 || env[0] != "ANTHROPIC_API_KEY="+ProxyInjectedPlaceholder {
			t.Errorf("env = %v, want ANTHROPIC_API_KEY placeholder", env)
		}
	})

	t.Run("claude provider uses CLAUDE_CODE_OAUTH_TOKEN", func(t *testing.T) {
		cred := &provider.Credential{Provider: "claude", Token: "sk-ant-oat01-abc123"}
		env := containerEnvForCredential(cred)
		if len(env) != 1 || env[0] != "CLAUDE_CODE_OAUTH_TOKEN="+ProxyInjectedPlaceholder {
			t.Errorf("env = %v, want CLAUDE_CODE_OAUTH_TOKEN placeholder", env)
		}
	})

	t.Run("anthropic provider uses ANTHROPIC_API_KEY", func(t *testing.T) {
		cred := &provider.Credential{Provider: "anthropic", Token: "sk-ant-api01-abc123"}
		env := containerEnvForCredential(cred)
		if len(env) != 1 || env[0] != "ANTHROPIC_API_KEY="+ProxyInjectedPlaceholder {
			t.Errorf("env = %v, want ANTHROPIC_API_KEY placeholder", env)
		}
	})
}

func TestOAuthProvider_ImpliedDependencies(t *testing.T) {
	p := &OAuthProvider{}
	deps := p.ImpliedDependencies()
	if deps != nil {
		t.Errorf("ImpliedDependencies() = %v, want nil", deps)
	}
}

func TestAnthropicProvider_ImpliedDependencies(t *testing.T) {
	p := &AnthropicProvider{}
	deps := p.ImpliedDependencies()
	if deps != nil {
		t.Errorf("ImpliedDependencies() = %v, want nil", deps)
	}
}

func TestOAuthProvider_ContainerMounts(t *testing.T) {
	p := &OAuthProvider{}
	cred := &provider.Credential{Token: "sk-ant-oat01-abc123"}

	mounts, cleanupPath, err := p.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Errorf("ContainerMounts() error = %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("ContainerMounts() returned %d mounts, want 0 (uses staging dir)", len(mounts))
	}
	if cleanupPath != "" {
		t.Errorf("ContainerMounts() cleanupPath = %q, want empty (uses staging dir cleanup)", cleanupPath)
	}
}

func TestProvider_Registration(t *testing.T) {
	// OAuthProvider should be registered as "claude"
	p := provider.Get("claude")
	if p == nil {
		t.Fatal("provider.Get(\"claude\") returned nil")
	}
	if p.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", p.Name(), "claude")
	}

	// AnthropicProvider should be registered as "anthropic"
	p2 := provider.Get("anthropic")
	if p2 == nil {
		t.Fatal("provider.Get(\"anthropic\") returned nil")
	}
	if p2.Name() != "anthropic" {
		t.Errorf("Name() = %q, want %q", p2.Name(), "anthropic")
	}

	// ResolveName should pass through canonical names unchanged
	if got := provider.ResolveName("claude"); got != "claude" {
		t.Errorf("ResolveName(\"claude\") = %q, want %q", got, "claude")
	}
	if got := provider.ResolveName("anthropic"); got != "anthropic" {
		t.Errorf("ResolveName(\"anthropic\") = %q, want %q", got, "anthropic")
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

		// Our explicit fields should still take precedence
		if config["hasCompletedOnboarding"] != true {
			t.Error("hasCompletedOnboarding should be true")
		}
		if config["theme"] != "dark" {
			t.Error("theme should be dark")
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
}

func TestWriteCredentialsFile(t *testing.T) {
	t.Run("claude provider creates file", func(t *testing.T) {
		stagingDir := t.TempDir()
		cred := &provider.Credential{
			Provider:  "claude",
			Token:     "sk-ant-oat01-abc123",
			ExpiresAt: time.Now().Add(time.Hour),
			Scopes:    []string{"user:read"},
		}

		err := WriteCredentialsFile(cred, stagingDir)
		if err != nil {
			t.Fatalf("WriteCredentialsFile() error = %v", err)
		}

		// Check credentials file was created
		credsFile := filepath.Join(stagingDir, ".credentials.json")
		if _, err := os.Stat(credsFile); err != nil {
			t.Errorf("credentials file should exist: %v", err)
		}

		// Read and verify content
		data, err := os.ReadFile(credsFile)
		if err != nil {
			t.Fatalf("failed to read credentials file: %v", err)
		}

		var creds oauthCredentials
		if err := json.Unmarshal(data, &creds); err != nil {
			t.Fatalf("failed to parse credentials file: %v", err)
		}

		if creds.ClaudeAiOauth == nil {
			t.Fatal("ClaudeAiOauth should be present")
		}
		if creds.ClaudeAiOauth.AccessToken != ProxyInjectedPlaceholder {
			t.Errorf("AccessToken = %q, want %q", creds.ClaudeAiOauth.AccessToken, ProxyInjectedPlaceholder)
		}
	})

	t.Run("anthropic provider does not create file", func(t *testing.T) {
		stagingDir := t.TempDir()
		cred := &provider.Credential{
			Provider: "anthropic",
			Token:    "sk-ant-api01-abc123",
		}

		err := WriteCredentialsFile(cred, stagingDir)
		if err != nil {
			t.Fatalf("WriteCredentialsFile() error = %v", err)
		}

		// API keys don't need credentials file
		credsFile := filepath.Join(stagingDir, ".credentials.json")
		if _, err := os.Stat(credsFile); err == nil {
			t.Error("credentials file should NOT exist for API keys")
		}
	})
}

func TestIsOAuthToken(t *testing.T) {
	tests := []struct {
		token string
		want  bool
	}{
		{"sk-ant-oat01-abc123xyz", true},
		{"sk-ant-oat02-abc123xyz", true},
		{"sk-ant-api01-abc123xyz", false},
		{"sk-ant-abc123xyz", false},
		{"short", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			if got := credential.IsOAuthToken(tt.token); got != tt.want {
				t.Errorf("IsOAuthToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

// mockProxyConfigurer implements provider.ProxyConfigurer for testing.
type mockProxyConfigurer struct {
	credentials    map[string]string
	extraHeaders   map[string]map[string]string
	transformers   map[string][]provider.ResponseTransformer
	removedHeaders map[string][]string // host -> []headerName
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {
	m.credentials[host] = value
}

func (m *mockProxyConfigurer) SetCredentialHeader(host, headerName, headerValue string) {
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	m.credentials[host] = headerName + ": " + headerValue
}

func (m *mockProxyConfigurer) AddExtraHeader(host, headerName, headerValue string) {
	if m.extraHeaders[host] == nil {
		m.extraHeaders[host] = make(map[string]string)
	}
	m.extraHeaders[host][headerName] = headerValue
}

func (m *mockProxyConfigurer) AddResponseTransformer(host string, transformer provider.ResponseTransformer) {
	if m.transformers == nil {
		m.transformers = make(map[string][]provider.ResponseTransformer)
	}
	m.transformers[host] = append(m.transformers[host], transformer)
}

func (m *mockProxyConfigurer) RemoveRequestHeader(host, header string) {
	if m.removedHeaders == nil {
		m.removedHeaders = make(map[string][]string)
	}
	m.removedHeaders[host] = append(m.removedHeaders[host], header)
}

func (m *mockProxyConfigurer) SetTokenSubstitution(host, placeholder, realToken string) {}

func TestRegisterCLI_ContinueFlag(t *testing.T) {
	p := &OAuthProvider{}
	root := &cobra.Command{Use: "test"}
	p.RegisterCLI(root)

	claudeCmd, _, err := root.Find([]string{"claude"})
	if err != nil {
		t.Fatalf("claude command not found: %v", err)
	}

	f := claudeCmd.Flags().Lookup("continue")
	if f == nil {
		t.Fatal("--continue flag not registered on claude command")
	}
	if f.Shorthand != "c" {
		t.Errorf("--continue shorthand = %q, want %q", f.Shorthand, "c")
	}
	if f.DefValue != "false" {
		t.Errorf("--continue default = %q, want %q", f.DefValue, "false")
	}
}

func TestRegisterCLI_ResumeFlag(t *testing.T) {
	p := &OAuthProvider{}
	root := &cobra.Command{Use: "test"}
	p.RegisterCLI(root)

	claudeCmd, _, err := root.Find([]string{"claude"})
	if err != nil {
		t.Fatalf("claude command not found: %v", err)
	}

	f := claudeCmd.Flags().Lookup("resume")
	if f == nil {
		t.Fatal("--resume flag not registered on claude command")
	}
	if f.Shorthand != "r" {
		t.Errorf("--resume shorthand = %q, want %q", f.Shorthand, "r")
	}
	if f.DefValue != "" {
		t.Errorf("--resume default = %q, want empty", f.DefValue)
	}
}

func TestRegisterCLI_WorktreeFlags(t *testing.T) {
	p := &OAuthProvider{}
	root := &cobra.Command{Use: "test"}
	p.RegisterCLI(root)

	claudeCmd, _, err := root.Find([]string{"claude"})
	if err != nil {
		t.Fatalf("claude command not found: %v", err)
	}

	// --worktree should exist
	wt := claudeCmd.Flags().Lookup("worktree")
	if wt == nil {
		t.Fatal("--worktree flag not registered")
	}

	// --wt alias should exist and be hidden
	wtAlias := claudeCmd.Flags().Lookup("wt")
	if wtAlias == nil {
		t.Fatal("--wt flag not registered")
	}
	if wtAlias.Hidden != true {
		t.Error("--wt flag should be hidden")
	}
}

func TestRegisterCLI_NoYoloFlag(t *testing.T) {
	p := &OAuthProvider{}
	root := &cobra.Command{Use: "test"}
	p.RegisterCLI(root)

	claudeCmd, _, err := root.Find([]string{"claude"})
	if err != nil {
		t.Fatalf("claude command not found: %v", err)
	}

	f := claudeCmd.Flags().Lookup("noyolo")
	if f == nil {
		t.Fatal("--noyolo flag not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("--noyolo default = %q, want %q", f.DefValue, "false")
	}
}

func TestRegisterCLI_PromptFlag(t *testing.T) {
	p := &OAuthProvider{}
	root := &cobra.Command{Use: "test"}
	p.RegisterCLI(root)

	claudeCmd, _, err := root.Find([]string{"claude"})
	if err != nil {
		t.Fatalf("claude command not found: %v", err)
	}

	f := claudeCmd.Flags().Lookup("prompt")
	if f == nil {
		t.Fatal("--prompt flag not registered")
	}
	if f.Shorthand != "p" {
		t.Errorf("--prompt shorthand = %q, want %q", f.Shorthand, "p")
	}
}
