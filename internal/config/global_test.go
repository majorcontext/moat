package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGlobalConfig(t *testing.T) {
	// Create temp home directory
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Create config file
	configDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(configDir, 0755)
	configPath := filepath.Join(configDir, "config.yaml")

	content := `
proxy:
  port: 9000
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if cfg.Proxy.Port != 9000 {
		t.Errorf("Proxy.Port = %d, want 9000", cfg.Proxy.Port)
	}
}

func TestLoadGlobalConfigDefaults(t *testing.T) {
	// Create temp home with no config
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if cfg.Proxy.Port != 8080 {
		t.Errorf("Proxy.Port = %d, want default 8080", cfg.Proxy.Port)
	}
}

func TestLoadGlobalConfigEnvOverride(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	os.Setenv("MOAT_PROXY_PORT", "7000")
	defer os.Unsetenv("MOAT_PROXY_PORT")

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if cfg.Proxy.Port != 7000 {
		t.Errorf("Proxy.Port = %d, want 7000 from env", cfg.Proxy.Port)
	}
}

func TestLoadGlobal_DebugConfig(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte("debug:\n  retention_days: 7\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Override home dir for test
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create .moat directory and move config
	moatDir := filepath.Join(tmpDir, ".moat")
	os.MkdirAll(moatDir, 0755)
	os.Rename(configPath, filepath.Join(moatDir, "config.yaml"))

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal failed: %v", err)
	}

	if cfg.Debug.RetentionDays != 7 {
		t.Errorf("expected RetentionDays=7, got %d", cfg.Debug.RetentionDays)
	}
}

func TestLoadGlobal_Mounts(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	content := `
mounts:
  - source: /home/user/.moat/claude/statusline.js
    target: /home/user/.claude/moat/statusline.js
    mode: ro
  - /home/user/.moat/scripts/helper.sh:/home/user/.local/bin/helper.sh:ro
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}

	if len(cfg.Mounts) != 2 {
		t.Fatalf("Mounts = %d, want 2", len(cfg.Mounts))
	}

	// Object form
	if cfg.Mounts[0].Source != "/home/user/.moat/claude/statusline.js" {
		t.Errorf("mount[0].Source = %q", cfg.Mounts[0].Source)
	}
	if cfg.Mounts[0].Target != "/home/user/.claude/moat/statusline.js" {
		t.Errorf("mount[0].Target = %q", cfg.Mounts[0].Target)
	}
	if !cfg.Mounts[0].ReadOnly {
		t.Error("mount[0] should be read-only")
	}

	// String form
	if cfg.Mounts[1].Source != "/home/user/.moat/scripts/helper.sh" {
		t.Errorf("mount[1].Source = %q", cfg.Mounts[1].Source)
	}
	if !cfg.Mounts[1].ReadOnly {
		t.Error("mount[1] should be read-only")
	}
}

func TestLoadGlobal_MountsRelativeSourceRejected(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	content := `
mounts:
  - source: ./relative/path
    target: /container/path
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	_, err := LoadGlobal()
	if err == nil {
		t.Fatal("expected error for relative source path")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("error should mention absolute path, got: %v", err)
	}
}

func TestLoadGlobal_MountsExcludeRejected(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	content := `
mounts:
  - source: /home/user/data
    target: /data
    exclude:
      - node_modules
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	_, err := LoadGlobal()
	if err == nil {
		t.Fatal("expected error for excludes on global mount")
	}
	if !strings.Contains(err.Error(), "excludes") {
		t.Errorf("error should mention excludes, got: %v", err)
	}
}

func TestLoadGlobal_MountsTildeExpansion(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	content := `
mounts:
  - source: ~/.moat/scripts/statusline.js
    target: /home/user/.claude/moat/statusline.js
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}

	if len(cfg.Mounts) != 1 {
		t.Fatalf("Mounts = %d, want 1", len(cfg.Mounts))
	}

	expected := filepath.Join(tmpHome, ".moat/scripts/statusline.js")
	if cfg.Mounts[0].Source != expected {
		t.Errorf("Source = %q, want %q", cfg.Mounts[0].Source, expected)
	}
}

func TestLoadGlobal_MountsEnforcesReadOnly(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	// Mount specified as rw — should be forced to ro
	content := `
mounts:
  - source: /home/user/data
    target: /data
    mode: rw
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}

	if !cfg.Mounts[0].ReadOnly {
		t.Error("global mount should be forced to read-only")
	}
}

func TestDefaultGlobalConfig_DebugDefaults(t *testing.T) {
	cfg := DefaultGlobalConfig()
	if cfg.Debug.RetentionDays != 14 {
		t.Errorf("expected default RetentionDays=14, got %d", cfg.Debug.RetentionDays)
	}
}
