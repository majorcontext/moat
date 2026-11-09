package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileResetPreservesSettingsAndRemoveProtectsChildren(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MOAT_HOME", home)
	configPath := filepath.Join(home, "config.yaml")
	original := "# keep comment\nunknown_setting: retained\nprofiles:\n  parent:\n    inherit_default: true\n    env:\n      API_KEY: secret-value\n  child:\n    inherit: parent\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(home, "credentials", "profiles", "parent")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"github.enc", "ssh.json", "oauth:linear.revoked", "github.enc.lock"} {
		if err := os.WriteFile(filepath.Join(store, file), []byte("private"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveProfile("parent"); err == nil || !strings.Contains(err.Error(), "child") {
		t.Fatalf("removed parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store, "github.enc")); err != nil {
		t.Fatal("failed removal changed credentials", err)
	}
	if err := ResetProfile("parent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("store not cleared: %v", err)
	}
	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Profiles["parent"].InheritDefault || cfg.Profiles["parent"].Env["API_KEY"] != "secret-value" {
		t.Fatal("settings changed", cfg.Profiles)
	}
	if err := RemoveProfile("child"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProfile("parent"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# keep comment") || !strings.Contains(string(data), "unknown_setting: retained") {
		t.Fatalf("lost unrelated config: %s", data)
	}
	cfg, err = LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 0 {
		t.Fatal(cfg.Profiles)
	}
}

func TestImplicitProfileResetAndRemove(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	store := filepath.Join(GlobalConfigDir(), "credentials", "profiles", "implicit")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ResetProfile("implicit"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Profiles["implicit"]; !ok {
		t.Fatal("reset lost implicit profile")
	}
	if err := RemoveProfile("implicit"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProfile("implicit"); err == nil {
		t.Fatal("removed nonexistent profile")
	}
	for _, name := range []string{"", "../other", "/tmp/other"} {
		if err := ResetProfile(name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}

func TestProfileClearDoesNotFollowSymlinksOrDeleteOnBadConfig(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(map[bool]string{false: "symlink", true: "bad-config"}[malformed], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("MOAT_HOME", home)
			external := t.TempDir()
			secret := filepath.Join(external, "github.enc")
			if err := os.WriteFile(secret, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join(home, "credentials", "profiles")
			if err := os.MkdirAll(base, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, filepath.Join(base, "work")); err != nil {
				t.Fatal(err)
			}
			if malformed {
				if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("profiles: ["), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := RemoveProfile("work"); err == nil {
				t.Fatal("accepted unsafe store/config")
			}
			if data, err := os.ReadFile(secret); err != nil || string(data) != "keep" {
				t.Fatalf("changed external store: %s %v", data, err)
			}
		})
	}
}

func TestProfileClearRestoresStoreWhenConfigCannotBeSaved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires unprivileged filesystem permissions")
	}
	home := t.TempDir()
	t.Setenv("MOAT_HOME", home)
	if err := CreateProfile("work", ProfileConfig{}); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(home, "credentials", "profiles", "work")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store, "github.enc")
	if err := os.WriteFile(path, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(home, 0o700) }()
	if err := ResetProfile("work"); err == nil {
		t.Fatal("reset succeeded with unwritable config")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserved" {
		t.Fatalf("rollback lost store: %q %v", data, err)
	}
}
