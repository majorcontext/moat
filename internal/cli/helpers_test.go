package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestResolveWorkspacePath(t *testing.T) {
	// Create a temp directory for testing
	tmpDir, err := os.MkdirTemp("", "test-workspace-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Resolve tmpDir through EvalSymlinks to handle macOS /var -> /private/var
	resolvedTmpDir, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("failed to resolve temp dir symlinks: %v", err)
	}

	tests := []struct {
		name      string
		workspace string
		setup     func() string // returns expected path
		wantErr   bool
	}{
		{
			name:      "valid directory",
			workspace: tmpDir,
			setup:     func() string { return resolvedTmpDir },
			wantErr:   false,
		},
		{
			name:      "current directory",
			workspace: ".",
			setup: func() string {
				cwd, _ := os.Getwd()
				return cwd
			},
			wantErr: false,
		},
		{
			name:      "non-existent path",
			workspace: "/nonexistent/path/that/does/not/exist",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveWorkspacePath(tt.workspace)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveWorkspacePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.setup != nil {
				want := tt.setup()
				if got != want {
					t.Errorf("ResolveWorkspacePath() = %v, want %v", got, want)
				}
			}
		})
	}
}

func TestResolveWorkspacePath_NotDirectory(t *testing.T) {
	// Create a temp file (not a directory)
	tmpFile, err := os.CreateTemp("", "test-file-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	_, err = ResolveWorkspacePath(tmpFile.Name())
	if err == nil {
		t.Error("ResolveWorkspacePath() expected error for file, got nil")
	}
}

func TestResolveWorkspacePath_Symlink(t *testing.T) {
	// Create a temp directory and symlink
	tmpDir, err := os.MkdirTemp("", "test-workspace-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	symlinkDir, err := os.MkdirTemp("", "test-symlink-*")
	if err != nil {
		t.Fatalf("failed to create symlink parent dir: %v", err)
	}
	defer os.RemoveAll(symlinkDir)

	symlink := filepath.Join(symlinkDir, "link")
	if err := os.Symlink(tmpDir, symlink); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	got, err := ResolveWorkspacePath(symlink)
	if err != nil {
		t.Errorf("ResolveWorkspacePath() error = %v", err)
		return
	}
	// Resolve tmpDir through EvalSymlinks to handle macOS /var -> /private/var
	want, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("failed to resolve temp dir symlinks: %v", err)
	}
	// Should resolve to the actual directory, not the symlink
	if got != want {
		t.Errorf("ResolveWorkspacePath() = %v, want %v", got, want)
	}
}

func TestParseEnvFlags(t *testing.T) {
	tests := []struct {
		name     string
		envFlags []string
		wantEnv  map[string]string
		wantErr  bool
	}{
		{
			name:     "empty flags",
			envFlags: []string{},
			wantEnv:  nil,
			wantErr:  false,
		},
		{
			name:     "single valid flag",
			envFlags: []string{"FOO=bar"},
			wantEnv:  map[string]string{"FOO": "bar"},
			wantErr:  false,
		},
		{
			name:     "multiple valid flags",
			envFlags: []string{"FOO=bar", "BAZ=qux"},
			wantEnv:  map[string]string{"FOO": "bar", "BAZ": "qux"},
			wantErr:  false,
		},
		{
			name:     "value with equals sign",
			envFlags: []string{"FOO=bar=baz"},
			wantEnv:  map[string]string{"FOO": "bar=baz"},
			wantErr:  false,
		},
		{
			name:     "empty value",
			envFlags: []string{"FOO="},
			wantEnv:  map[string]string{"FOO": ""},
			wantErr:  false,
		},
		{
			name:     "underscore prefix",
			envFlags: []string{"_FOO=bar"},
			wantEnv:  map[string]string{"_FOO": "bar"},
			wantErr:  false,
		},
		{
			name:     "missing equals",
			envFlags: []string{"FOO"},
			wantErr:  true,
		},
		{
			name:     "invalid key - starts with digit",
			envFlags: []string{"1FOO=bar"},
			wantErr:  true,
		},
		{
			name:     "invalid key - contains hyphen",
			envFlags: []string{"FOO-BAR=baz"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			err := ParseEnvFlags(tt.envFlags, cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEnvFlags() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.wantEnv != nil {
				if len(cfg.Env) != len(tt.wantEnv) {
					t.Errorf("ParseEnvFlags() env count = %d, want %d", len(cfg.Env), len(tt.wantEnv))
				}
				for k, v := range tt.wantEnv {
					if cfg.Env[k] != v {
						t.Errorf("ParseEnvFlags() env[%s] = %q, want %q", k, cfg.Env[k], v)
					}
				}
			}
		})
	}
}

func TestHasDependency(t *testing.T) {
	tests := []struct {
		name   string
		deps   []string
		prefix string
		want   bool
	}{
		{
			name:   "exact match",
			deps:   []string{"node", "python", "go"},
			prefix: "node",
			want:   true,
		},
		{
			name:   "version match",
			deps:   []string{"node@20", "python@3.11"},
			prefix: "node",
			want:   true,
		},
		{
			name:   "no match",
			deps:   []string{"python", "go"},
			prefix: "node",
			want:   false,
		},
		{
			name:   "empty deps",
			deps:   []string{},
			prefix: "node",
			want:   false,
		},
		{
			name:   "partial name no match",
			deps:   []string{"nodejs"},
			prefix: "node",
			want:   false,
		},
		{
			name:   "prefix without version after @",
			deps:   []string{"node@"},
			prefix: "node",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasDependency(tt.deps, tt.prefix)
			if got != tt.want {
				t.Errorf("HasDependency(%v, %q) = %v, want %v", tt.deps, tt.prefix, got, tt.want)
			}
		})
	}
}
