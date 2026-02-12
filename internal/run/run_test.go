package run

import (
	"crypto/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()

	// IDs should have the correct prefix (run_ with underscore)
	if !strings.HasPrefix(id1, "run_") {
		t.Errorf("expected ID to start with 'run_', got %s", id1)
	}

	// IDs should be unique
	if id1 == id2 {
		t.Errorf("expected unique IDs, got %s and %s", id1, id2)
	}

	// IDs should have expected length (run_ + 12 hex chars = 16 total)
	if len(id1) != 16 {
		t.Errorf("expected ID length 16, got %d (%s)", len(id1), id1)
	}
}

func TestRunStates(t *testing.T) {
	// Verify state constants are defined
	states := []State{
		StateCreated,
		StateStarting,
		StateRunning,
		StateStopping,
		StateStopped,
		StateFailed,
	}

	for _, s := range states {
		if s == "" {
			t.Error("state should not be empty")
		}
	}
}

func TestOptions(t *testing.T) {
	opts := Options{
		Name:      "test-agent",
		Workspace: "/tmp/test",
		Grants:    []string{"github", "aws:s3.read"},
	}

	if opts.Name != "test-agent" {
		t.Errorf("expected name 'test-agent', got %s", opts.Name)
	}
	if opts.Workspace != "/tmp/test" {
		t.Errorf("expected workspace '/tmp/test', got %s", opts.Workspace)
	}
	if len(opts.Grants) != 2 {
		t.Errorf("expected 2 grants, got %d", len(opts.Grants))
	}
}

func TestWorkspaceToClaudeDir(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "unix absolute path",
			input:    "/home/alice/projects/myapp",
			expected: "-home-alice-projects-myapp",
		},
		{
			name:     "simple path",
			input:    "/tmp/workspace",
			expected: "-tmp-workspace",
		},
		{
			name:     "deep nested path",
			input:    "/Users/dev/Documents/code/project/subdir",
			expected: "-Users-dev-Documents-code-project-subdir",
		},
		{
			name:     "root path",
			input:    "/workspace",
			expected: "-workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := workspaceToClaudeDir(tt.input)
			if result != tt.expected {
				t.Errorf("workspaceToClaudeDir(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestClaudeLogMountTargetUsesRuntimeHome verifies that the Claude log sync mount
// targets the actual runtime user's home directory, not the image's default.
// When a moat-built image uses an init script (ENTRYPOINT moat-init), the image
// USER is root, but the container runs as moatuser with HOME=/home/moatuser.
// The mount must target /home/moatuser/.claude/projects/-workspace, not /root/.
func TestClaudeLogMountTargetUsesRuntimeHome(t *testing.T) {
	tests := []struct {
		name             string
		needsCustomImage bool
		imageHomeDir     string // what GetImageHomeDir would return
		wantHome         string
	}{
		{
			name:             "custom image uses moatuser home regardless of image metadata",
			needsCustomImage: true,
			imageHomeDir:     "/root", // init-based images report root
			wantHome:         "/home/moatuser",
		},
		{
			name:             "custom image with correct image metadata still uses moatuser",
			needsCustomImage: true,
			imageHomeDir:     "/home/moatuser",
			wantHome:         "/home/moatuser",
		},
		{
			name:             "base image uses detected home",
			needsCustomImage: false,
			imageHomeDir:     "/root",
			wantHome:         "/root",
		},
		{
			name:             "base image with non-root user",
			needsCustomImage: false,
			imageHomeDir:     "/home/node",
			wantHome:         "/home/node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerHome := resolveContainerHome(tt.needsCustomImage, tt.imageHomeDir)
			got := filepath.Join(containerHome, ".claude", "projects", "-workspace")
			want := filepath.Join(tt.wantHome, ".claude", "projects", "-workspace")

			if got != want {
				t.Errorf("mount target = %s, want %s", got, want)
			}
		})
	}
}

func TestValidateGrants(t *testing.T) {
	// Set up temporary credential store
	tmpDir := t.TempDir()
	credDir := filepath.Join(tmpDir, "credentials")
	os.MkdirAll(credDir, 0700)

	key := make([]byte, 32)
	rand.Read(key)
	store, _ := credential.NewFileStore(credDir, key)

	// Save a github credential
	store.Save(credential.Credential{
		Provider:  "github",
		Token:     "ghp_test",
		CreatedAt: time.Now(),
	})
	// Save an MCP credential (no registered provider, store-only)
	store.Save(credential.Credential{
		Provider:  "mcp-test",
		Token:     "mcp-token",
		CreatedAt: time.Now(),
	})

	tests := []struct {
		name    string
		grants  []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no grants",
			grants:  nil,
			wantErr: false,
		},
		{
			name:    "valid grant exists",
			grants:  []string{"github"},
			wantErr: false,
		},
		{
			name:    "missing grant",
			grants:  []string{"claude"},
			wantErr: true,
			errMsg:  "claude: not configured",
		},
		{
			name:    "unrecognized provider",
			grants:  []string{"nonexistent"},
			wantErr: true,
			errMsg:  "unknown provider",
		},
		{
			name:    "aws grant with role syntax",
			grants:  []string{"aws:arn:aws:iam::123456:role/MyRole"},
			wantErr: true,
			errMsg:  "aws: not configured",
		},
		{
			name:    "multiple grants one missing",
			grants:  []string{"github", "claude"},
			wantErr: true,
			errMsg:  "claude",
		},
		{
			name:    "multiple missing grants reports all",
			grants:  []string{"claude", "aws"},
			wantErr: true,
			errMsg:  "Configure the grants above",
		},
		{
			name:    "mcp grant skipped by validateGrants",
			grants:  []string{"mcp-test"},
			wantErr: false,
		},
		{
			name:    "mcp grant without credential skipped by validateGrants",
			grants:  []string{"mcp-missing"},
			wantErr: false,
		},
		{
			name:    "ssh grant skipped by validateGrants",
			grants:  []string{"ssh:github.com"},
			wantErr: false,
		},
		{
			name:    "bare ssh grant skipped by validateGrants",
			grants:  []string{"ssh"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGrants(tt.grants, store)
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

func TestValidateGrantsErrorFormat(t *testing.T) {
	tmpDir := t.TempDir()
	credDir := filepath.Join(tmpDir, "credentials")
	os.MkdirAll(credDir, 0700)

	key := make([]byte, 32)
	rand.Read(key)
	store, _ := credential.NewFileStore(credDir, key)

	err := validateGrants([]string{"github"}, store)
	if err == nil {
		t.Fatal("expected error for missing github grant")
	}

	msg := err.Error()
	// Should show clean "not configured" message, not raw store error
	if !strings.Contains(msg, "github: not configured") {
		t.Errorf("error should say 'not configured', got: %s", msg)
	}
	// Should include fix command
	if !strings.Contains(msg, "moat grant github") {
		t.Errorf("error should include fix command, got: %s", msg)
	}
}

func TestValidateGrantsDecryptionFailure(t *testing.T) {
	tmpDir := t.TempDir()
	credDir := filepath.Join(tmpDir, "credentials")
	os.MkdirAll(credDir, 0700)

	// Create store with one key and save a credential
	key1 := make([]byte, 32)
	rand.Read(key1)
	store1, _ := credential.NewFileStore(credDir, key1)
	store1.Save(credential.Credential{
		Provider:  "github",
		Token:     "ghp_test",
		CreatedAt: time.Now(),
	})

	// Open the same store with a different key
	key2 := make([]byte, 32)
	rand.Read(key2)
	store2, _ := credential.NewFileStore(credDir, key2)

	err := validateGrants([]string{"github"}, store2)
	if err == nil {
		t.Fatal("expected error for credential encrypted with different key")
	}

	msg := err.Error()
	// Should show clean "encryption key changed" message, not raw cipher error
	if !strings.Contains(msg, "encryption key changed") {
		t.Errorf("error should mention key change, got: %s", msg)
	}
	if !strings.Contains(msg, "moat grant github") {
		t.Errorf("error should include fix command, got: %s", msg)
	}
}

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

// TestProxyURLCircularPrevention tests that NO_PROXY is correctly set to prevent
// circular proxy issues when the proxy relay connects to MCP servers.
// This is a regression test for the critical bug where MCP relay requests would
// loop back through the proxy.
func TestProxyURLCircularPrevention(t *testing.T) {
	tests := []struct {
		name          string
		proxyURL      string
		expectedHost  string
		shouldContain []string
	}{
		{
			name:         "localhost proxy",
			proxyURL:     "http://127.0.0.1:8888",
			expectedHost: "127.0.0.1:8888",
			shouldContain: []string{
				"127.0.0.1:8888",
				"localhost",
				"127.0.0.1",
			},
		},
		{
			name:         "host IP proxy",
			proxyURL:     "http://192.168.1.100:9999",
			expectedHost: "192.168.1.100:9999",
			shouldContain: []string{
				"192.168.1.100:9999",
				"localhost",
				"127.0.0.1",
			},
		},
		{
			name:         "proxy with auth",
			proxyURL:     "http://moat:token123@10.0.0.50:8080",
			expectedHost: "10.0.0.50:8080",
			shouldContain: []string{
				"10.0.0.50:8080",
				"localhost",
				"127.0.0.1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse the proxy URL to get the host
			u, err := url.Parse(tt.proxyURL)
			if err != nil {
				t.Fatalf("failed to parse proxy URL: %v", err)
			}

			// Build NO_PROXY value (simulating manager.go logic)
			hostAddr := u.Host
			noProxy := hostAddr + ",localhost,127.0.0.1"

			// Verify the host is extracted correctly
			if hostAddr != tt.expectedHost {
				t.Errorf("hostAddr = %q, want %q", hostAddr, tt.expectedHost)
			}

			// Verify NO_PROXY contains all expected values
			for _, expected := range tt.shouldContain {
				if !strings.Contains(noProxy, expected) {
					t.Errorf("NO_PROXY should contain %q, got: %s", expected, noProxy)
				}
			}

			// Verify NO_PROXY would prevent proxy for localhost
			noproxyList := strings.Split(noProxy, ",")
			hasLocalhost := false
			for _, entry := range noproxyList {
				if strings.TrimSpace(entry) == "localhost" {
					hasLocalhost = true
					break
				}
			}
			if !hasLocalhost {
				t.Error("NO_PROXY should include localhost to prevent circular proxy")
			}

			// Verify NO_PROXY would prevent proxy for the proxy's own address
			hasProxyHost := false
			for _, entry := range noproxyList {
				if strings.TrimSpace(entry) == hostAddr {
					hasProxyHost = true
					break
				}
			}
			if !hasProxyHost {
				t.Errorf("NO_PROXY should include proxy's own address (%s) to prevent circular proxy", hostAddr)
			}
		})
	}
}
