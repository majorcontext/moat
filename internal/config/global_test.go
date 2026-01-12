package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGlobalConfig(t *testing.T) {
	// Create temp home directory
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Create config file
	configDir := filepath.Join(tmpHome, ".agentops")
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

	os.Setenv("AGENTOPS_PROXY_PORT", "7000")
	defer os.Unsetenv("AGENTOPS_PROXY_PORT")

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if cfg.Proxy.Port != 7000 {
		t.Errorf("Proxy.Port = %d, want 7000 from env", cfg.Proxy.Port)
	}
}
