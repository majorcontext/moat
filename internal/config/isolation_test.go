package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadIsolationConfig writes a moat.yaml with the given isolation block and
// loads it.
func loadIsolationConfig(t *testing.T, isolationYAML string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	content := "agent: test\n" + isolationYAML
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
}

func TestLoadConfigIsolationKernelSandbox(t *testing.T) {
	cfg, err := loadIsolationConfig(t, `
isolation:
  kernel_sandbox: true
  sandbox:
    allow_write:
      - /data
      - /var/cache/custom
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Isolation.KernelSandbox {
		t.Error("Isolation.KernelSandbox = false, want true")
	}
	if len(cfg.Isolation.Sandbox.AllowWrite) != 2 {
		t.Errorf("AllowWrite = %v, want 2 entries", cfg.Isolation.Sandbox.AllowWrite)
	}
}

func TestLoadConfigIsolationDefaultsOff(t *testing.T) {
	// Companion: a config without an isolation block leaves the sandbox off.
	cfg, err := loadIsolationConfig(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Isolation.KernelSandbox {
		t.Error("Isolation.KernelSandbox = true for empty config, want false")
	}
	if cfg.Isolation.Mode != "" {
		t.Errorf("Isolation.Mode = %q for empty config, want empty", cfg.Isolation.Mode)
	}
}

func TestLoadConfigIsolationMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr string
	}{
		{"container accepted", "container", ""},
		{"local rejected with pointer to issue", "local", "not yet supported"},
		{"unknown rejected", "chroot", "invalid isolation.mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadIsolationConfig(t, "isolation:\n  mode: "+tt.mode+"\n")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load accepted isolation.mode %q, want error", tt.mode)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigIsolationRejectsDenyPaths(t *testing.T) {
	// deny_paths must fail loudly, not be silently ignored: a user who wrote
	// it would otherwise believe those paths are protected.
	_, err := loadIsolationConfig(t, `
isolation:
  kernel_sandbox: true
  sandbox:
    deny_paths:
      - /home/moatuser/.ssh
`)
	if err == nil {
		t.Fatal("Load accepted deny_paths, want error")
	}
	if !strings.Contains(err.Error(), "deny_paths is not yet supported") {
		t.Errorf("error = %v, want it to mention deny_paths being unsupported", err)
	}
}

func TestLoadConfigIsolationAllowWriteValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			"relative path rejected",
			"isolation:\n  kernel_sandbox: true\n  sandbox:\n    allow_write: [./data]\n",
			"not an absolute path",
		},
		{
			"empty entry rejected",
			"isolation:\n  kernel_sandbox: true\n  sandbox:\n    allow_write: [\"\"]\n",
			"must not be empty",
		},
		{
			"allow_write without kernel_sandbox rejected",
			"isolation:\n  sandbox:\n    allow_write: [/data]\n",
			"kernel_sandbox is false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadIsolationConfig(t, tt.yaml)
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}

	// Companion: absolute paths pass, and an empty allow_write list is fine
	// with the sandbox enabled.
	if _, err := loadIsolationConfig(t, "isolation:\n  kernel_sandbox: true\n"); err != nil {
		t.Errorf("kernel_sandbox without allow_write should load, got: %v", err)
	}
}
