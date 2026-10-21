package config

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProfileInheritance(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	if err := CreateProfile("base", ProfileConfig{Env: map[string]string{"SHARED": "yes", "MODEL": "base"}, InheritDefault: true}); err != nil {
		t.Fatal(err)
	}
	if err := CreateProfile("child", ProfileConfig{Inherit: "base", Env: map[string]string{"MODEL": "child"}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	chain, err := cfg.ProfileChain("child")
	if err != nil || !reflect.DeepEqual(chain, []string{"child", "base", ""}) {
		t.Fatalf("chain = %v, %v", chain, err)
	}
	if got := cfg.ProfileEnv("child"); !reflect.DeepEqual(got, map[string]string{"SHARED": "yes", "MODEL": "child"}) {
		t.Fatal(got)
	}
	if cfg.ProfileEnv("base")["MODEL"] != "base" {
		t.Fatal("mutated parent env")
	}
	if cfg.ProfileEnv("") != nil {
		t.Fatal("default store must not have profile env")
	}
}

// Separate processes exercise the advisory file lock, not just goroutine
// synchronization. Every writer must keep the profiles created by its peers.
func TestCreateProfilesConcurrently(t *testing.T) {
	const processCount, profilesPerProcess = 4, 3
	if prefix := os.Getenv("MOAT_TEST_CREATE_PROFILE_PREFIX"); prefix != "" {
		for i := 0; i < profilesPerProcess; i++ {
			if err := CreateProfile(fmt.Sprintf("%s-%d", prefix, i), ProfileConfig{InheritDefault: true}); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	t.Setenv("MOAT_HOME", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var commands [processCount]*exec.Cmd
	var outputs [processCount]bytes.Buffer
	for i := range commands {
		cmd := exec.Command(executable, "-test.run=^TestCreateProfilesConcurrently$")
		cmd.Env = append(os.Environ(), fmt.Sprintf("MOAT_TEST_CREATE_PROFILE_PREFIX=writer%d", i))
		cmd.Stdout, cmd.Stderr = &outputs[i], &outputs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands[i] = cmd
		t.Cleanup(func() { _ = cmd.Process.Kill() })
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("writer %d: %v\n%s", i, err, &outputs[i])
		}
	}
	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != processCount*profilesPerProcess {
		t.Fatalf("lost concurrent profile writes: got %d profiles", len(cfg.Profiles))
	}
	for i := range commands {
		for j := 0; j < profilesPerProcess; j++ {
			name := fmt.Sprintf("writer%d-%d", i, j)
			if !cfg.Profiles[name].InheritDefault {
				t.Errorf("missing profile %s", name)
			}
		}
	}
}

func TestProfileInheritanceValidation(t *testing.T) {
	for _, body := range []string{
		"profiles: {child: {inherit: missing}}",
		"profiles: {child: {inherit: child}}",
		"profiles: {a: {inherit: b}, b: {inherit: a}}",
		"profiles: {child: {inherit: '../escape'}}",
		"profiles: {base: {}, child: {inherit: base, inherit_default: true}}",
	} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("MOAT_HOME", dir)
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadGlobal(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCreateProfilePreservesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOAT_HOME", dir)
	path := filepath.Join(dir, "config.yaml")
	body := "# keep this comment\nproxy:\n  port: 9012\nfuture_setting: keep-me\nprofiles:\n  base:\n    env: {MODEL: test}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateProfile("child", ProfileConfig{Inherit: "base"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# keep this comment", "port: 9012", "future_setting: keep-me", "MODEL: test", "inherit: base"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("lost %q in %s", want, data)
		}
	}
	for _, name := range []string{"child", "base", "../escape", ""} {
		if err := CreateProfile(name, ProfileConfig{}); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("failed create changed config")
	}
}

func TestCreateProfileFromImplicitParent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOAT_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "credentials", "profiles", "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CreateProfile("child", ProfileConfig{Inherit: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := CreateProfile("work", ProfileConfig{}); err == nil {
		t.Fatal("overwrote implicit profile")
	}
}

func TestCreateProfileRefusesMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOAT_HOME", dir)
	path := filepath.Join(dir, "config.yaml")
	body := "profiles: [{{"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateProfile("child", ProfileConfig{InheritDefault: true}); err == nil {
		t.Fatal("expected error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != body {
		t.Fatal("changed malformed config")
	}
}
