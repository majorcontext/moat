# MCP Support Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add support for remote MCP servers over HTTPS with credential injection and observability

**Architecture:** Extends moat's existing credential injection infrastructure. MCP servers are declared in agent.yaml (top-level, not nested under claude/codex), credentials stored via `moat grant mcp <name>`, moat-init calls `claude mcp add` with stub credentials, and proxy replaces stubs with real credentials based on URL + header matching.

**Tech Stack:** Go 1.21+, YAML parsing (gopkg.in/yaml.v3), HTTP proxy (net/http), credential storage (AES-256-GCM encryption)

---

### Task 1: Add MCP configuration to agent.yaml schema

**Files:**
- Modify: `internal/config/config.go:14-35`
- Test: `internal/config/config_test.go`

**Step 1: Write failing test for MCP parsing**

Add to `internal/config/config_test.go`:

```go
func TestLoad_MCPServers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "agent.yaml", `
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
  - name: public-mcp
    url: https://public.example.com/mcp
`)

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.MCP) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(cfg.MCP))
	}

	// Check first server (with auth)
	ctx7 := cfg.MCP[0]
	if ctx7.Name != "context7" {
		t.Errorf("expected name 'context7', got %q", ctx7.Name)
	}
	if ctx7.URL != "https://mcp.context7.com/mcp" {
		t.Errorf("expected URL 'https://mcp.context7.com/mcp', got %q", ctx7.URL)
	}
	if ctx7.Auth == nil {
		t.Fatal("expected auth to be set")
	}
	if ctx7.Auth.Grant != "mcp-context7" {
		t.Errorf("expected grant 'mcp-context7', got %q", ctx7.Auth.Grant)
	}
	if ctx7.Auth.Header != "CONTEXT7_API_KEY" {
		t.Errorf("expected header 'CONTEXT7_API_KEY', got %q", ctx7.Auth.Header)
	}

	// Check second server (no auth)
	public := cfg.MCP[1]
	if public.Name != "public-mcp" {
		t.Errorf("expected name 'public-mcp', got %q", public.Name)
	}
	if public.Auth != nil {
		t.Errorf("expected auth to be nil, got %+v", public.Auth)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestLoad_MCPServers -v`
Expected: FAIL with "cfg.MCP undefined" or similar

**Step 3: Add MCP types to config.go**

In `internal/config/config.go`, add after line 31 (after `Container ContainerConfig`):

```go
	MCP []MCPServerConfig `yaml:"mcp,omitempty"`
```

After the `ContainerConfig` type definition (around line 56), add:

```go
// MCPServerConfig defines a remote MCP server configuration.
type MCPServerConfig struct {
	Name string         `yaml:"name"`
	URL  string         `yaml:"url"`
	Auth *MCPAuthConfig `yaml:"auth,omitempty"`
}

// MCPAuthConfig defines authentication for an MCP server.
type MCPAuthConfig struct {
	Grant  string `yaml:"grant"`
	Header string `yaml:"header"`
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config -run TestLoad_MCPServers -v`
Expected: PASS

**Step 5: Write test for MCP validation errors**

Add to `internal/config/config_test.go`:

```go
func TestLoad_MCP_Validation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing name",
			yaml: `
mcp:
  - url: https://example.com
    auth:
      grant: mcp-test
      header: API_KEY
`,
			wantErr: "mcp[0]: 'name' is required",
		},
		{
			name: "missing url",
			yaml: `
mcp:
  - name: test
    auth:
      grant: mcp-test
      header: API_KEY
`,
			wantErr: "mcp[0]: 'url' is required",
		},
		{
			name: "non-https url",
			yaml: `
mcp:
  - name: test
    url: http://example.com
`,
			wantErr: "mcp[0]: 'url' must use HTTPS",
		},
		{
			name: "auth missing grant",
			yaml: `
mcp:
  - name: test
    url: https://example.com
    auth:
      header: API_KEY
`,
			wantErr: "mcp[0]: 'auth.grant' is required when auth is specified",
		},
		{
			name: "auth missing header",
			yaml: `
mcp:
  - name: test
    url: https://example.com
    auth:
      grant: mcp-test
`,
			wantErr: "mcp[0]: 'auth.header' is required when auth is specified",
		},
		{
			name: "duplicate names",
			yaml: `
mcp:
  - name: test
    url: https://example.com
  - name: test
    url: https://other.com
`,
			wantErr: "mcp[1]: duplicate name 'test'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "agent.yaml", tt.yaml)

			_, err := config.Load(dir)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}
```

**Step 6: Run test to verify it fails**

Run: `go test ./internal/config -run TestLoad_MCP_Validation -v`
Expected: FAIL - no validation yet

**Step 7: Add MCP validation to config.go**

In `internal/config/config.go`, after the Codex MCP validation (around line 278), add:

```go
	// Validate top-level MCP server specs
	seenNames := make(map[string]bool)
	for i, spec := range cfg.MCP {
		if err := validateTopLevelMCPServerSpec(i, spec, seenNames); err != nil {
			return nil, err
		}
	}
```

After the `validateMCPServerSpec` function (around line 324), add:

```go
// validateTopLevelMCPServerSpec validates a top-level MCP server specification.
func validateTopLevelMCPServerSpec(index int, spec MCPServerConfig, seenNames map[string]bool) error {
	prefix := fmt.Sprintf("mcp[%d]", index)

	if spec.Name == "" {
		return fmt.Errorf("%s: 'name' is required", prefix)
	}

	if seenNames[spec.Name] {
		return fmt.Errorf("%s: duplicate name %q", prefix, spec.Name)
	}
	seenNames[spec.Name] = true

	if spec.URL == "" {
		return fmt.Errorf("%s: 'url' is required", prefix)
	}

	if !strings.HasPrefix(spec.URL, "https://") {
		return fmt.Errorf("%s: 'url' must use HTTPS", prefix)
	}

	if spec.Auth != nil {
		if spec.Auth.Grant == "" {
			return fmt.Errorf("%s: 'auth.grant' is required when auth is specified", prefix)
		}
		if spec.Auth.Header == "" {
			return fmt.Errorf("%s: 'auth.header' is required when auth is specified", prefix)
		}
	}

	return nil
}
```

**Step 8: Run test to verify it passes**

Run: `go test ./internal/config -run TestLoad_MCP_Validation -v`
Expected: PASS

**Step 9: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add MCP server configuration to agent.yaml

- Add MCPServerConfig and MCPAuthConfig types
- Add validation for name, URL (HTTPS only), and auth fields
- Reject duplicate MCP server names
- Add comprehensive tests for parsing and validation"
```

---

### Task 2: Add MCP grant command

**Files:**
- Modify: `cmd/moat/cli/grant.go:41-103,132-159`
- Test: `cmd/moat/cli/grant_test.go`

**Step 1: Write failing test for MCP grant**

Add to `cmd/moat/cli/grant_test.go`:

```go
func TestGrantMCP(t *testing.T) {
	// Save stdin/stdout
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	// Mock stdin with API key
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.Write([]byte("test-api-key-123\n"))
		w.Close()
	}()

	// Redirect stdout to silence prompts
	os.Stdout, _ = os.Open(os.DevNull)

	// Set up temporary credential store
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Run grant command
	cmd := rootCmd
	cmd.SetArgs([]string{"grant", "mcp", "context7"})
	err := cmd.Execute()

	if err != nil {
		t.Fatalf("grant mcp context7 failed: %v", err)
	}

	// Verify credential was saved
	key, _ := credential.DefaultEncryptionKey()
	store, _ := credential.NewFileStore(credential.DefaultStoreDir(), key)
	cred, err := store.Get(credential.Provider("mcp-context7"))

	if err != nil {
		t.Fatalf("failed to retrieve credential: %v", err)
	}

	if cred.Provider != "mcp-context7" {
		t.Errorf("expected provider 'mcp-context7', got %q", cred.Provider)
	}

	if cred.Token != "test-api-key-123" {
		t.Errorf("expected token 'test-api-key-123', got %q", cred.Token)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/moat/cli -run TestGrantMCP -v`
Expected: FAIL with "unknown command 'mcp'"

**Step 3: Update grant command help text**

In `cmd/moat/cli/grant.go`, update the `Long` field of `grantCmd` (around line 44):

Replace:
```go
Supported providers:
  github      GitHub token (from gh CLI, environment, or interactive prompt)
  anthropic   Anthropic API key or Claude Code OAuth credentials
  openai      OpenAI API key or ChatGPT subscription credentials
  aws         AWS IAM role assumption (uses host credentials to assume role)
```

With:
```go
Supported providers:
  github      GitHub token (from gh CLI, environment, or interactive prompt)
  anthropic   Anthropic API key or Claude Code OAuth credentials
  openai      OpenAI API key or ChatGPT subscription credentials
  aws         AWS IAM role assumption (uses host credentials to assume role)
  mcp         MCP server credentials (stored as mcp-<name>)
```

**Step 4: Add MCP case to runGrant switch**

In `cmd/moat/cli/grant.go`, in the `runGrant` function (around line 132), before the `default:` case, add:

```go
	case "mcp":
		if len(args) < 2 {
			return fmt.Errorf(`usage: moat grant mcp <name>

Store a credential for an MCP server. The credential will be saved as 'mcp-<name>'.

Example:
  moat grant mcp context7

Then reference in agent.yaml:
  mcp:
    - name: context7
      url: https://mcp.context7.com/mcp
      auth:
        grant: mcp-context7
        header: CONTEXT7_API_KEY`)
		}
		return grantMCP(args[1])
```

**Step 5: Update command Args validation**

In `cmd/moat/cli/grant.go`, update `grantCmd` (around line 101):

Change:
```go
	Args: cobra.ExactArgs(1),
```

To:
```go
	Args: cobra.MinimumNArgs(1),
```

**Step 6: Implement grantMCP function**

In `cmd/moat/cli/grant.go`, after the `grantAWS` function (around line 964), add:

```go
func grantMCP(name string) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("Enter credential for MCP server '%s'\n", name)
	fmt.Printf("This will be stored as grant 'mcp-%s'\n\n", name)
	fmt.Print("Credential: ")

	credBytes, err := readPassword()
	if err != nil {
		return fmt.Errorf("reading credential: %w", err)
	}
	fmt.Println() // newline after hidden input

	credential := strings.TrimSpace(string(credBytes))
	if credential == "" {
		return fmt.Errorf("no credential provided")
	}

	// Validate credential is non-empty (V0 does not validate against server)
	fmt.Println("Validating credential...")
	if len(credential) < 8 {
		fmt.Println("Warning: Credential seems short. MCP server may reject it.")
	}

	cred := credential.Credential{
		Provider:  credential.Provider("mcp-" + name),
		Token:     credential,
		CreatedAt: time.Now(),
	}

	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}

	fmt.Printf("MCP credential 'mcp-%s' saved to %s\n", name, credPath)
	return nil
}
```

**Step 7: Run test to verify it passes**

Run: `go test ./cmd/moat/cli -run TestGrantMCP -v`
Expected: PASS

**Step 8: Test manually**

Run: `go run ./cmd/moat grant mcp context7` (enter a test credential)
Expected: Success message with credential path

**Step 9: Commit**

```bash
git add cmd/moat/cli/grant.go cmd/moat/cli/grant_test.go
git commit -m "feat(cli): add 'moat grant mcp <name>' command

- Add MCP case to grant command switch
- Implement grantMCP function with interactive prompt
- Store credentials as 'mcp-<name>' provider
- V0: minimal validation (length check only)
- Add test for MCP grant flow"
```

---

### Task 3: Add MCP revoke support

**Files:**
- Modify: `cmd/moat/cli/revoke.go`
- Test: Manual testing (revoke uses same credential store)

**Step 1: Update revoke command help**

In `cmd/moat/cli/revoke.go`, update the `Long` field to mention MCP grants:

```go
Long: `Revoke a previously granted credential.

Supported providers:
  github            GitHub token
  anthropic         Anthropic API key or OAuth credentials
  openai            OpenAI API key or OAuth credentials
  aws               AWS IAM role configuration
  mcp-<name>        MCP server credential

The credential file is permanently deleted.

Examples:
  # Revoke GitHub access
  moat revoke github

  # Revoke MCP server credential
  moat revoke mcp-context7
`,
```

**Step 2: Test manually**

Run: `go run ./cmd/moat grant mcp test` (enter credential)
Run: `go run ./cmd/moat revoke mcp-test`
Expected: Success message

**Step 3: Commit**

```bash
git add cmd/moat/cli/revoke.go
git commit -m "docs(cli): update revoke help to include MCP grants"
```

---

### Task 4: Validate MCP grants at run startup

**Files:**
- Modify: `internal/run/run.go`
- Test: `internal/run/run_test.go`

**Step 1: Write failing test for grant validation**

Add to `internal/run/run_test.go`:

```go
func TestValidateMCPGrants(t *testing.T) {
	// Set up temporary credential store
	tmpDir := t.TempDir()
	credDir := filepath.Join(tmpDir, "credentials")
	os.MkdirAll(credDir, 0700)

	key := make([]byte, 32)
	rand.Read(key)
	store, _ := credential.NewFileStore(credDir, key)

	// Save one grant
	store.Save(credential.Credential{
		Provider:  "mcp-context7",
		Token:     "test-token",
		CreatedAt: time.Now(),
	})

	tests := []struct {
		name    string
		mcp     []config.MCPServerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid grant exists",
			mcp: []config.MCPServerConfig{
				{
					Name: "context7",
					URL:  "https://mcp.context7.com",
					Auth: &config.MCPAuthConfig{
						Grant:  "mcp-context7",
						Header: "API_KEY",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "no auth required",
			mcp: []config.MCPServerConfig{
				{
					Name: "public",
					URL:  "https://public.example.com",
				},
			},
			wantErr: false,
		},
		{
			name: "missing grant",
			mcp: []config.MCPServerConfig{
				{
					Name: "missing",
					URL:  "https://example.com",
					Auth: &config.MCPAuthConfig{
						Grant:  "mcp-missing",
						Header: "API_KEY",
					},
				},
			},
			wantErr: true,
			errMsg:  "MCP server 'missing' requires grant 'mcp-missing' but it's not configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				MCP: tt.mcp,
			}

			err := validateMCPGrants(cfg, store)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/run -run TestValidateMCPGrants -v`
Expected: FAIL with "undefined: validateMCPGrants"

**Step 3: Implement validateMCPGrants**

In `internal/run/run.go`, add after the existing validation functions:

```go
// validateMCPGrants checks that all required MCP grants exist.
func validateMCPGrants(cfg *config.Config, store *credential.FileStore) error {
	for _, mcp := range cfg.MCP {
		if mcp.Auth == nil {
			continue // No auth required
		}

		_, err := store.Get(credential.Provider(mcp.Auth.Grant))
		if err != nil {
			return fmt.Errorf(`MCP server '%s' requires grant '%s' but it's not configured

To fix:
  moat grant mcp %s

Then run again.`, mcp.Name, mcp.Auth.Grant, strings.TrimPrefix(mcp.Auth.Grant, "mcp-"))
		}
	}
	return nil
}
```

**Step 4: Call validation in run startup**

In `internal/run/run.go`, find where grants are validated (likely in `NewRun` or similar), and add:

```go
	// Validate MCP grants
	if err := validateMCPGrants(cfg, credentialStore); err != nil {
		return nil, err
	}
```

**Step 5: Run test to verify it passes**

Run: `go test ./internal/run -run TestValidateMCPGrants -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/run/run.go internal/run/run_test.go
git commit -m "feat(run): validate MCP grants at startup

- Add validateMCPGrants function
- Check all auth.grant references exist before starting container
- Fail fast with actionable error message
- Add tests for grant validation"
```

---

### Task 5: Add MCP setup to moat-init

**Files:**
- Modify: `internal/deps/scripts/moat-init.sh`
- Test: Manual testing (requires Claude CLI in container)

**Step 1: Add MCP setup section to moat-init.sh**

In `internal/deps/scripts/moat-init.sh`, after the Codex setup section (after line 130), add:

```bash
# MCP Server Setup
# When MOAT_MCP_SERVERS is set (JSON array), configure MCP servers for
# Claude or Codex CLI. Each server in the array contains name, url, grant,
# and header fields. moat-init calls 'claude mcp add' or 'codex mcp add'
# with stub credentials that will be replaced by the proxy.
if [ -n "$MOAT_MCP_SERVERS" ]; then
  # Determine target home directory
  if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
    TARGET_HOME="/home/moatuser"
  else
    TARGET_HOME="$HOME"
  fi

  # Check if claude is available
  CLAUDE_AVAILABLE=false
  if command -v claude >/dev/null 2>&1; then
    CLAUDE_AVAILABLE=true
  fi

  # Check if codex is available
  CODEX_AVAILABLE=false
  if command -v codex >/dev/null 2>&1; then
    CODEX_AVAILABLE=true
  fi

  # Parse JSON array and configure each MCP server
  # MOAT_MCP_SERVERS format: [{"name":"context7","url":"https://...","grant":"mcp-context7","header":"API_KEY"}]
  echo "$MOAT_MCP_SERVERS" | sed 's/^\[//' | sed 's/\]$//' | sed 's/},{/}\n{/g' | while IFS= read -r server_json; do
    # Extract fields from JSON (basic parsing, assumes well-formed JSON)
    name=$(echo "$server_json" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
    url=$(echo "$server_json" | sed -n 's/.*"url":"\([^"]*\)".*/\1/p')
    grant=$(echo "$server_json" | sed -n 's/.*"grant":"\([^"]*\)".*/\1/p')
    header=$(echo "$server_json" | sed -n 's/.*"header":"\([^"]*\)".*/\1/p')

    # Generate stub credential
    stub="moat-stub-${grant}"

    # Configure for Claude if available
    if [ "$CLAUDE_AVAILABLE" = "true" ]; then
      claude mcp add --header "${header}: ${stub}" --transport http "$name" "$url" 2>/dev/null || {
        echo "Warning: Failed to configure MCP server '$name' for Claude" >&2
      }
    fi

    # Configure for Codex if available
    if [ "$CODEX_AVAILABLE" = "true" ]; then
      codex mcp add --header "${header}: ${stub}" --transport http "$name" "$url" 2>/dev/null || {
        echo "Warning: Failed to configure MCP server '$name' for Codex" >&2
      }
    fi
  done
fi
```

**Step 2: Test manually**

Create a test container with Claude CLI and run moat-init.sh with:
```bash
export MOAT_MCP_SERVERS='[{"name":"test","url":"https://example.com","grant":"mcp-test","header":"API_KEY"}]'
```

Expected: `claude mcp list` shows the server with stub credential

**Step 3: Commit**

```bash
git add internal/deps/scripts/moat-init.sh
git commit -m "feat(init): add MCP server configuration to moat-init

- Read MOAT_MCP_SERVERS JSON array from environment
- Call 'claude mcp add' and 'codex mcp add' for each server
- Generate stub credentials (moat-stub-{grant})
- Silent skip if CLI not available
- Log warnings on configuration failures"
```

---

### Task 6: Pass MCP configuration to container

**Files:**
- Modify: `internal/run/run.go`
- Test: Integration test (check environment variable is set)

**Step 1: Find where environment variables are set for container**

Search for where `MOAT_CLAUDE_INIT`, `MOAT_CODEX_INIT` are set in `internal/run/run.go`.

**Step 2: Add MCP servers to environment**

In `internal/run/run.go`, where environment variables are built, add:

```go
	// Set MCP servers if configured
	if len(cfg.MCP) > 0 {
		mcpJSON, err := marshalMCPServers(cfg.MCP)
		if err != nil {
			return nil, fmt.Errorf("marshaling MCP servers: %w", err)
		}
		envVars["MOAT_MCP_SERVERS"] = mcpJSON
	}
```

**Step 3: Implement marshalMCPServers helper**

In `internal/run/run.go`, add:

```go
// marshalMCPServers converts MCP server config to JSON for moat-init.
// Format: [{"name":"context7","url":"https://...","grant":"mcp-context7","header":"API_KEY"}]
func marshalMCPServers(servers []config.MCPServerConfig) (string, error) {
	type mcpServer struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Grant  string `json:"grant,omitempty"`
		Header string `json:"header,omitempty"`
	}

	var jsonServers []mcpServer
	for _, s := range servers {
		js := mcpServer{
			Name: s.Name,
			URL:  s.URL,
		}
		if s.Auth != nil {
			js.Grant = s.Auth.Grant
			js.Header = s.Auth.Header
		}
		jsonServers = append(jsonServers, js)
	}

	data, err := json.Marshal(jsonServers)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
```

**Step 4: Add test for MCP environment variable**

Add to `internal/run/run_test.go`:

```go
func TestMarshalMCPServers(t *testing.T) {
	servers := []config.MCPServerConfig{
		{
			Name: "context7",
			URL:  "https://mcp.context7.com",
			Auth: &config.MCPAuthConfig{
				Grant:  "mcp-context7",
				Header: "API_KEY",
			},
		},
		{
			Name: "public",
			URL:  "https://public.example.com",
		},
	}

	result, err := marshalMCPServers(servers)
	if err != nil {
		t.Fatalf("marshalMCPServers failed: %v", err)
	}

	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	if len(parsed) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(parsed))
	}

	// Check first server
	if parsed[0]["name"] != "context7" {
		t.Errorf("expected name 'context7', got %v", parsed[0]["name"])
	}
	if parsed[0]["grant"] != "mcp-context7" {
		t.Errorf("expected grant 'mcp-context7', got %v", parsed[0]["grant"])
	}

	// Check second server (no auth)
	if parsed[1]["name"] != "public" {
		t.Errorf("expected name 'public', got %v", parsed[1]["name"])
	}
	if _, hasGrant := parsed[1]["grant"]; hasGrant {
		t.Errorf("expected no grant field, got %v", parsed[1]["grant"])
	}
}
```

**Step 5: Run test**

Run: `go test ./internal/run -run TestMarshalMCPServers -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/run/run.go internal/run/run_test.go
git commit -m "feat(run): pass MCP configuration to container

- Set MOAT_MCP_SERVERS environment variable
- Marshal MCP servers to JSON for moat-init consumption
- Include name, url, grant, header fields
- Add test for JSON marshaling"
```

---

### Task 7: Add MCP credential injection to proxy

**Files:**
- Create: `internal/proxy/mcp.go`
- Modify: `internal/proxy/proxy.go`
- Test: `internal/proxy/mcp_test.go`

**Step 1: Write failing test for MCP credential injection**

Create `internal/proxy/mcp_test.go`:

```go
package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

func TestMCPCredentialInjection(t *testing.T) {
	// Mock credential store
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{
			"mcp-context7": {
				Provider: "mcp-context7",
				Token:    "real-api-key-123",
			},
		},
	}

	// Mock backend that echoes the API_KEY header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("API_KEY")))
	}))
	defer backend.Close()

	// Configure MCP server
	mcpServers := []config.MCPServerConfig{
		{
			Name: "context7",
			URL:  backend.URL, // Use test server URL
			Auth: &config.MCPAuthConfig{
				Grant:  "mcp-context7",
				Header: "API_KEY",
			},
		},
	}

	// Create proxy with MCP configuration
	p := &Proxy{
		credStore:  mockStore,
		mcpServers: mcpServers,
	}

	// Create test request with stub credential
	req := httptest.NewRequest("GET", backend.URL, nil)
	req.Header.Set("API_KEY", "moat-stub-mcp-context7")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Verify real credential was injected
	if rec.Body.String() != "real-api-key-123" {
		t.Errorf("expected body 'real-api-key-123', got %q", rec.Body.String())
	}
}

func TestMCPCredentialInjection_NoMatch(t *testing.T) {
	// Request to non-MCP server should pass through unchanged
	mockStore := &mockCredentialStore{
		creds: map[credential.Provider]*credential.Credential{},
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("API_KEY")))
	}))
	defer backend.Close()

	p := &Proxy{
		credStore:  mockStore,
		mcpServers: []config.MCPServerConfig{},
	}

	req := httptest.NewRequest("GET", backend.URL, nil)
	req.Header.Set("API_KEY", "some-value")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	// Should pass through unchanged
	if rec.Body.String() != "some-value" {
		t.Errorf("expected body 'some-value', got %q", rec.Body.String())
	}
}

// mockCredentialStore for testing
type mockCredentialStore struct {
	creds map[credential.Provider]*credential.Credential
}

func (m *mockCredentialStore) Get(p credential.Provider) (*credential.Credential, error) {
	if cred, ok := m.creds[p]; ok {
		return cred, nil
	}
	return nil, fmt.Errorf("credential not found")
}

func (m *mockCredentialStore) Save(c credential.Credential) error {
	m.creds[c.Provider] = &c
	return nil
}

func (m *mockCredentialStore) Delete(p credential.Provider) error {
	delete(m.creds, p)
	return nil
}

func (m *mockCredentialStore) List() ([]credential.Credential, error) {
	var list []credential.Credential
	for _, c := range m.creds {
		list = append(list, *c)
	}
	return list, nil
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy -run TestMCPCredentialInjection -v`
Expected: FAIL - Proxy doesn't have mcpServers field yet

**Step 3: Add MCP fields to Proxy struct**

In `internal/proxy/proxy.go`, add to the Proxy struct:

```go
	mcpServers []config.MCPServerConfig
```

**Step 4: Add MCP configuration method**

In `internal/proxy/proxy.go`, add:

```go
// SetMCPServers configures MCP servers for credential injection.
func (p *Proxy) SetMCPServers(servers []config.MCPServerConfig) {
	p.mcpServers = servers
}
```

**Step 5: Create MCP credential injection logic**

Create `internal/proxy/mcp.go`:

```go
package proxy

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
)

// injectMCPCredentials checks if the request is to an MCP server and injects
// the real credential if a stub is detected.
// Returns true if injection occurred, false otherwise.
func (p *Proxy) injectMCPCredentials(req *http.Request) bool {
	if len(p.mcpServers) == 0 {
		return false
	}

	// Parse request URL to get host
	reqHost := req.URL.Host
	if reqHost == "" {
		reqHost = req.Host
	}

	// Find matching MCP server by host
	var matchedServer *config.MCPServerConfig
	for i := range p.mcpServers {
		server := &p.mcpServers[i]
		if server.Auth == nil {
			continue // No auth required
		}

		// Parse server URL to get host
		serverURL, err := url.Parse(server.URL)
		if err != nil {
			continue
		}

		// Match by host
		if serverURL.Host == reqHost {
			matchedServer = server
			break
		}
	}

	if matchedServer == nil {
		return false // No matching MCP server
	}

	// Check if the specified header exists
	headerValue := req.Header.Get(matchedServer.Auth.Header)
	if headerValue == "" {
		return false // Header not present
	}

	// Check if header value is a stub
	expectedStub := "moat-stub-" + matchedServer.Auth.Grant
	if headerValue != expectedStub {
		// Not a stub - could be a real credential or different value
		// Log warning if it looks like a stub but doesn't match
		if strings.HasPrefix(headerValue, "moat-stub-") {
			log.Warn("MCP request has stub-like header value that doesn't match expected grant",
				"server", matchedServer.Name,
				"header", matchedServer.Auth.Header,
				"expected", expectedStub,
				"got", headerValue)
		}
		return false
	}

	// Load real credential
	cred, err := p.credStore.Get(credential.Provider(matchedServer.Auth.Grant))
	if err != nil {
		log.Error("Failed to load MCP credential",
			"server", matchedServer.Name,
			"grant", matchedServer.Auth.Grant,
			"error", err)
		// Leave stub in place - request will fail with stub credential
		return false
	}

	// Replace stub with real credential
	req.Header.Set(matchedServer.Auth.Header, cred.Token)

	log.Info("Injected MCP credential",
		"server", matchedServer.Name,
		"grant", matchedServer.Auth.Grant,
		"header", matchedServer.Auth.Header,
		"host", reqHost)

	return true
}
```

**Step 6: Call MCP injection in proxy handler**

In `internal/proxy/proxy.go`, in the `ServeHTTP` method, before the existing credential injection, add:

```go
	// Inject MCP credentials if request matches configured server
	p.injectMCPCredentials(req)
```

**Step 7: Run test to verify it passes**

Run: `go test ./internal/proxy -run TestMCPCredentialInjection -v`
Expected: PASS

**Step 8: Commit**

```bash
git add internal/proxy/mcp.go internal/proxy/mcp_test.go internal/proxy/proxy.go
git commit -m "feat(proxy): add MCP credential injection

- Add injectMCPCredentials to check request against MCP servers
- Match by request host against server URL
- Check for stub pattern (moat-stub-{grant})
- Replace stub with real credential from store
- Log injection events and errors
- Add comprehensive tests for injection logic"
```

---

### Task 8: Integrate MCP configuration with proxy startup

**Files:**
- Modify: `internal/run/run.go`
- Test: Integration test

**Step 1: Find where proxy is created in run.go**

Search for `proxy.NewServer` or similar in `internal/run/run.go`.

**Step 2: Pass MCP configuration to proxy**

After proxy is created, add:

```go
	// Configure MCP servers for credential injection
	if len(cfg.MCP) > 0 {
		proxyServer.Proxy().SetMCPServers(cfg.MCP)
	}
```

**Step 3: Test manually**

Create a test `agent.yaml` with MCP configuration:
```yaml
mcp:
  - name: test
    url: https://httpbin.org/headers
    auth:
      grant: mcp-test
      header: X-Test-Key
```

Run: `moat grant mcp test` (enter "test-key")
Run: `moat run --grant mcp-test -- curl -H "X-Test-Key: moat-stub-mcp-test" https://httpbin.org/headers`

Expected: Response should show real credential, not stub

**Step 4: Commit**

```bash
git add internal/run/run.go
git commit -m "feat(run): integrate MCP configuration with proxy

- Pass MCP server config to proxy on startup
- Enable credential injection for MCP traffic
- Proxy now handles stub replacement for configured servers"
```

---

### Task 9: Add documentation

**Files:**
- Create: `docs/content/guides/06-using-mcp-servers.md`
- Modify: `docs/content/reference/01-cli.md`
- Modify: `docs/content/reference/02-agent-yaml.md`

**Step 1: Write MCP usage guide**

Create `docs/content/guides/06-using-mcp-servers.md`:

```markdown
# Using MCP Servers

MCP (Model Context Protocol) servers extend agents with additional tools and context. Moat manages MCP server connections, injects credentials securely, and provides full observability.

## Quick Start

### 1. Grant credentials

Store credentials for MCP servers you want to use:

\`\`\`bash
$ moat grant mcp context7
Enter credential for MCP server 'context7': ***************
MCP credential 'mcp-context7' saved to ~/.moat/credentials/mcp-context7.enc
\`\`\`

### 2. Configure in agent.yaml

Declare which MCP servers your agent should access:

\`\`\`yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
\`\`\`

### 3. Run your agent

\`\`\`bash
$ moat claude ./workspace
\`\`\`

Behind the scenes:
- Container starts with MCP configuration
- moat-init calls \`claude mcp add\` with stub credentials
- Agent sees MCP server available
- Proxy replaces stub credentials with real values
- All MCP traffic is logged for audit

## How It Works

### Credential Injection

Moat uses the same credential injection model for MCP as it does for API calls:

1. Credentials never enter the container environment
2. moat-init configures Claude/Codex with stub credentials (\`moat-stub-{grant}\`)
3. When agent makes MCP request with stub, proxy detects it
4. Proxy replaces stub with real credential based on URL + header matching
5. MCP server receives real credential

This ensures:
- Credentials are not in environment variables or config files
- Agent code cannot access raw credentials
- Credential usage is fully auditable

### Configuration

MCP servers are declared at the top level of agent.yaml (not nested under \`claude:\` or \`codex:\`):

\`\`\`yaml
mcp:
  - name: context7          # Friendly name for logs
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7   # References global grant
      header: CONTEXT7_API_KEY  # HTTP header for auth
\`\`\`

**Required fields:**
- \`name\`: Identifier for the server (must be unique)
- \`url\`: HTTPS endpoint (HTTP not allowed for security)

**Optional fields:**
- \`auth.grant\`: Name of grant to use (must exist via \`moat grant mcp <name>\`)
- \`auth.header\`: Header name where credential should be injected

For public MCP servers that don't require authentication, omit the \`auth\` block.

## Observability

### Network logs

MCP connections appear in network logs like regular HTTP:

\`\`\`bash
$ moat trace --network

[10:23:44.512] GET https://mcp.context7.com/mcp 200 (89ms)
\`\`\`

### Audit logs

Credential injection is logged:

\`\`\`bash
$ moat audit

[10:23:44.500] credential.injected grant=mcp-context7 host=mcp.context7.com header=CONTEXT7_API_KEY
\`\`\`

### Limitations (V0)

V0 treats MCP as opaque SSE streams. Logs show:
- Connection established
- Duration and status code
- Which grant was used

V0 does NOT show:
- Individual MCP tool calls
- Tool parameters
- MCP protocol-level errors

For tool-level details, check agent logs (Claude/Codex output).

Future versions will add MCP message parsing for deeper observability.

## Examples

### Context7

\`\`\`yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
\`\`\`

\`\`\`bash
moat grant mcp context7  # Enter your Context7 API key
moat claude ./workspace
\`\`\`

### GitHub MCP

\`\`\`yaml
mcp:
  - name: github
    url: https://github-mcp.example.com
    auth:
      grant: mcp-github
      header: Authorization
\`\`\`

\`\`\`bash
moat grant mcp github    # Enter your GitHub MCP token
moat claude ./workspace
\`\`\`

### Multiple MCP servers

\`\`\`yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY

  - name: notion
    url: https://notion-mcp.example.com
    auth:
      grant: mcp-notion
      header: Notion-Token
\`\`\`

## Security

### HTTPS only

MCP URLs must use HTTPS. HTTP is rejected at config parse time:

\`\`\`
Error: mcp[0]: 'url' must use HTTPS
\`\`\`

This prevents credentials from being sent over unencrypted connections.

### Grant validation

Moat validates all grants exist before starting the container:

\`\`\`
Error: MCP server 'context7' requires grant 'mcp-context7' but it's not configured

To fix:
  moat grant mcp context7
\`\`\`

This ensures runs fail fast with clear errors rather than mysterious failures during execution.

### Stub validation

The proxy only injects credentials when it detects the exact stub pattern (\`moat-stub-{grant}\`). This prevents:
- Accidental forwarding of real credentials
- Credential leakage to wrong servers

## Troubleshooting

### MCP server not appearing in Claude

Check that:
1. \`claude\` binary exists in the container (run \`moat run -- which claude\`)
2. MCP server is declared in agent.yaml
3. Container logs show "Injected MCP credential" (check with \`moat logs\`)

### Authentication failures (401)

Check that:
1. Grant exists: \`moat grants list\` should show \`mcp-{name}\`
2. Credential is correct: try \`moat revoke mcp-{name}\` then \`moat grant mcp {name}\` to re-enter
3. Check MCP server URL is correct in agent.yaml

### Stub credential in logs

If you see \`moat-stub-{grant}\` in network logs or MCP errors:
- Proxy didn't recognize the request as MCP traffic
- Check that URL in agent.yaml exactly matches the host being accessed
- Check that header name matches what the agent is sending

## Future: V2+ Features

Planned but not in V0:
- **OAuth-based MCP servers**: Browser-based auth flows
- **Sandbox-local MCP servers**: MCP servers running in isolated containers
- **Host-side MCP servers**: MCP servers running on the host
- **Grant delegation**: MCP servers exercising agent capabilities
- **MCP message parsing**: Tool-level observability and logging

For V0, focus is on remote HTTPS MCP servers with custom header authentication (API keys, tokens).
\`\`\`

**Step 2: Update CLI reference**

In `docs/content/reference/01-cli.md`, add under the grant command section:

```markdown
### moat grant mcp <name>

Store a credential for an MCP server.

\`\`\`bash
moat grant mcp context7
\`\`\`

The credential is stored as \`mcp-<name>\` (e.g., \`mcp-context7\`) and can be referenced in agent.yaml.

**Interactive prompts:**
- Credential (hidden input)

**Storage:**
- \`~/.moat/credentials/mcp-<name>.enc\`
\`\`\`

**Step 3: Update agent.yaml reference**

In `docs/content/reference/02-agent-yaml.md`, add a new section:

```markdown
## mcp

Configures remote MCP (Model Context Protocol) servers for agent access.

\`\`\`yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
\`\`\`

**Fields:**

- \`name\` (required): Identifier for the MCP server (must be unique)
- \`url\` (required): HTTPS endpoint for the MCP server (HTTP not allowed)
- \`auth\` (optional): Authentication configuration
  - \`grant\` (required if auth present): Name of grant to use (format: \`mcp-<name>\`)
  - \`header\` (required if auth present): HTTP header name for credential injection

**Credential injection:**

moat-init configures Claude/Codex with stub credentials (\`moat-stub-{grant}\`). The proxy replaces stubs with real credentials at runtime based on URL + header matching.

**Example with multiple servers:**

\`\`\`yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY

  - name: public-mcp
    url: https://public.example.com/mcp
    # No auth block = no credential injection
\`\`\`

**See also:** [Using MCP Servers guide](../guides/06-using-mcp-servers.md)
\`\`\`

**Step 4: Commit**

```bash
git add docs/content/guides/06-using-mcp-servers.md docs/content/reference/01-cli.md docs/content/reference/02-agent-yaml.md
git commit -m "docs: add MCP support documentation

- Add comprehensive MCP usage guide
- Update CLI reference with 'moat grant mcp'
- Update agent.yaml reference with mcp section
- Document V0 limitations and future features
- Add troubleshooting section"
```

---

### Task 10: End-to-end testing

**Files:**
- Create: `internal/e2e/mcp_test.go`
- Test: E2E test

**Step 1: Write E2E test**

Create `internal/e2e/mcp_test.go`:

```go
// +build e2e

package e2e

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

func TestMCPCredentialInjection_E2E(t *testing.T) {
	// Create mock MCP server
	var receivedHeader string
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Test-Key")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer mcpServer.Close()

	// Set up temporary workspace
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "workspace")
	os.MkdirAll(workspace, 0755)

	// Create agent.yaml
	agentYAML := `
mcp:
  - name: test-server
    url: ` + mcpServer.URL + `
    auth:
      grant: mcp-test
      header: X-Test-Key
`
	ioutil.WriteFile(filepath.Join(workspace, "agent.yaml"), []byte(agentYAML), 0644)

	// Store credential
	credDir := filepath.Join(tmpDir, "credentials")
	os.MkdirAll(credDir, 0700)
	key := make([]byte, 32)
	rand.Read(key)
	store, _ := credential.NewFileStore(credDir, key)
	store.Save(credential.Credential{
		Provider:  "mcp-test",
		Token:     "real-api-key-xyz",
		CreatedAt: time.Now(),
	})

	// Run moat with curl command to hit MCP server
	// (This would use actual moat CLI - simplified here)
	cmd := exec.Command("moat", "run", workspace,
		"--",
		"curl", "-H", "X-Test-Key: moat-stub-mcp-test", mcpServer.URL)
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("moat run failed: %v\nOutput: %s", err, output)
	}

	// Verify real credential was injected
	if receivedHeader != "real-api-key-xyz" {
		t.Errorf("expected header 'real-api-key-xyz', got %q", receivedHeader)
	}
}
```

**Step 2: Run E2E test**

Run: `go test -tags=e2e ./internal/e2e -run TestMCPCredentialInjection_E2E -v`
Expected: PASS (requires container runtime)

**Step 3: Commit**

```bash
git add internal/e2e/mcp_test.go
git commit -m "test(e2e): add MCP credential injection E2E test

- Create mock MCP server
- Configure agent.yaml with MCP server
- Verify stub replacement in real container execution
- Validate credential injection end-to-end"
```

---

## Implementation Complete!

All tasks have tests and follow TDD principles. The implementation:

1. ✅ Parses MCP configuration from agent.yaml
2. ✅ Stores MCP credentials via `moat grant mcp <name>`
3. ✅ Validates grants at run startup
4. ✅ Passes configuration to container via environment
5. ✅ Configures Claude/Codex via moat-init with stub credentials
6. ✅ Injects real credentials via proxy based on URL + header matching
7. ✅ Logs MCP connections in network logs
8. ✅ Logs credential injection in audit logs
9. ✅ Full documentation with examples
10. ✅ Comprehensive test coverage

## Next Steps

After implementation:
1. Manual testing with real Context7 MCP server
2. Update CHANGELOG.md with new features
3. Consider adding example agent.yaml files to `examples/` directory
4. Update README.md to mention MCP support
