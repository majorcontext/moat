package run

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/providers/claude"
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
			result := claude.WorkspaceToClaudeDir(tt.input)
			if result != tt.expected {
				t.Errorf("WorkspaceToClaudeDir(%q) = %q, want %q", tt.input, result, tt.expected)
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
			errMsg:  "aws:arn:aws:iam::123456:role/MyRole: not configured",
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

func TestAppendMCPGrants(t *testing.T) {
	tests := []struct {
		name   string
		grants []string
		cfg    *config.Config
		want   []string
	}{
		{
			name:   "nil config",
			grants: []string{"github"},
			cfg:    nil,
			want:   []string{"github"},
		},
		{
			name:   "no MCP servers",
			grants: []string{"github"},
			cfg:    &config.Config{},
			want:   []string{"github"},
		},
		{
			name:   "MCP server without auth",
			grants: []string{"github"},
			cfg: &config.Config{
				MCP: []config.MCPServerConfig{
					{Name: "public", URL: "https://public.example.com"},
				},
			},
			want: []string{"github"},
		},
		{
			name:   "MCP auth with empty grant",
			grants: []string{"github"},
			cfg: &config.Config{
				MCP: []config.MCPServerConfig{
					{
						Name: "svc",
						URL:  "https://example.com",
						Auth: &config.MCPAuthConfig{Grant: "", Header: "Authorization"},
					},
				},
			},
			want: []string{"github"},
		},
		{
			name:   "MCP auth grant auto-included",
			grants: []string{"github"},
			cfg: &config.Config{
				MCP: []config.MCPServerConfig{
					{
						Name: "render",
						URL:  "https://mcp.render.com/mcp",
						Auth: &config.MCPAuthConfig{Grant: "mcp-render", Header: "Authorization"},
					},
				},
			},
			want: []string{"github", "mcp-render"},
		},
		{
			name:   "MCP auth grant already present",
			grants: []string{"github", "mcp-render"},
			cfg: &config.Config{
				MCP: []config.MCPServerConfig{
					{
						Name: "render",
						URL:  "https://mcp.render.com/mcp",
						Auth: &config.MCPAuthConfig{Grant: "mcp-render", Header: "Authorization"},
					},
				},
			},
			want: []string{"github", "mcp-render"},
		},
		{
			name:   "multiple MCP servers",
			grants: []string{"claude"},
			cfg: &config.Config{
				MCP: []config.MCPServerConfig{
					{
						Name: "render",
						URL:  "https://mcp.render.com/mcp",
						Auth: &config.MCPAuthConfig{Grant: "mcp-render", Header: "Authorization"},
					},
					{
						Name: "linear",
						URL:  "https://mcp.linear.app/mcp",
						Auth: &config.MCPAuthConfig{Grant: "mcp-linear", Header: "Authorization"},
					},
				},
			},
			want: []string{"claude", "mcp-render", "mcp-linear"},
		},
		{
			name:   "duplicate grant across MCP servers",
			grants: []string{"claude"},
			cfg: &config.Config{
				MCP: []config.MCPServerConfig{
					{
						Name: "render",
						URL:  "https://mcp.render.com/mcp",
						Auth: &config.MCPAuthConfig{Grant: "mcp-shared", Header: "Authorization"},
					},
					{
						Name: "linear",
						URL:  "https://mcp.linear.app/mcp",
						Auth: &config.MCPAuthConfig{Grant: "mcp-shared", Header: "Authorization"},
					},
				},
			},
			want: []string{"claude", "mcp-shared"},
		},
		{
			name:   "empty grants with MCP",
			grants: nil,
			cfg: &config.Config{
				MCP: []config.MCPServerConfig{
					{
						Name: "render",
						URL:  "https://mcp.render.com/mcp",
						Auth: &config.MCPAuthConfig{Grant: "mcp-render", Header: "Authorization"},
					},
				},
			},
			want: []string{"mcp-render"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendMCPGrants(tt.grants, tt.cfg)
			if len(got) != len(tt.want) {
				t.Fatalf("appendMCPGrants() = %v, want %v", got, tt.want)
			}
			for i, g := range got {
				if g != tt.want[i] {
					t.Errorf("appendMCPGrants()[%d] = %q, want %q", i, g, tt.want[i])
				}
			}
		})
	}
}

// TestBuildProxyEnv_LoopbackNotBypassed verifies that under host-network mode,
// loopback addresses are NOT in NO_PROXY (so they flow through the proxy for
// network.host enforcement), while moat-proxy IS in NO_PROXY (preventing
// infinite proxy loops).
func TestBuildProxyEnv_LoopbackNotBypassed(t *testing.T) {
	env := buildProxyEnv("test-token", 19080, true)

	var noProxy string
	for _, e := range env {
		if strings.HasPrefix(e, "NO_PROXY=") {
			noProxy = strings.TrimPrefix(e, "NO_PROXY=")
			break
		}
	}
	if noProxy == "" {
		t.Fatal("NO_PROXY not found in env")
	}

	// moat-proxy must be in NO_PROXY so relay traffic reaches the proxy
	// directly without looping through the CONNECT tunnel.
	if !strings.Contains(noProxy, "moat-proxy") {
		t.Errorf("NO_PROXY should contain moat-proxy, got %q", noProxy)
	}

	// In host-network mode, localhost and 127.0.0.1 must NOT be in NO_PROXY
	// because the container shares the host loopback — excluding them lets
	// container processes bypass network.host enforcement.
	if strings.Contains(noProxy, "localhost") {
		t.Errorf("NO_PROXY must NOT contain localhost in host-network mode, got %q", noProxy)
	}
	if strings.Contains(noProxy, "127.0.0.1") {
		t.Errorf("NO_PROXY must NOT contain 127.0.0.1 in host-network mode, got %q", noProxy)
	}
}
