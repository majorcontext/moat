package config

import (
	"os"
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
