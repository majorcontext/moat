package quickstart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanWorkspace_detectsFileTypes(t *testing.T) {
	dir := t.TempDir()

	// Create files of various types.
	files := map[string]string{
		"main.go":          "package main",
		"main_test.go":     "package main",
		"util.go":          "package util",
		"script.py":        "import os",
		"test.bats":        "#!/usr/bin/env bats",
		"Makefile":         "all: build",
		"internal/foo.go":  "package foo",
		"internal/bar.go":  "package bar",
		".claude/hooks.sh": "#!/bin/bash",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte(content), 0644)
	}

	result := ScanWorkspace(dir)

	checks := []string{
		"Go:",
		"Python:",
		"Bats (Bash tests):",
		"Makefile (make):",
		"Shell:",
	}
	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("missing %q in scan result:\n%s", check, result)
		}
	}
}

func TestScanWorkspace_detectsManifests(t *testing.T) {
	dir := t.TempDir()

	manifests := []string{"go.mod", "Makefile", "Dockerfile"}
	for _, name := range manifests {
		os.WriteFile(filepath.Join(dir, name), []byte("content"), 0644)
	}

	result := ScanWorkspace(dir)

	checks := []string{
		"Go (go.mod)",
		"Makefile",
		"Dockerfile",
	}
	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("missing %q in scan result:\n%s", check, result)
		}
	}
}

func TestScanWorkspace_detectsCIConfigs(t *testing.T) {
	dir := t.TempDir()

	ciDir := filepath.Join(dir, ".github", "workflows")
	os.MkdirAll(ciDir, 0755)
	os.WriteFile(filepath.Join(ciDir, "ci.yml"), []byte("name: CI"), 0644)

	result := ScanWorkspace(dir)

	if !strings.Contains(result, ".github/workflows/ci.yml") {
		t.Errorf("missing CI config in scan result:\n%s", result)
	}
}

func TestScanWorkspace_skipsNodeModules(t *testing.T) {
	dir := t.TempDir()

	// Create a JS file in node_modules (should be skipped).
	nmDir := filepath.Join(dir, "node_modules", "pkg")
	os.MkdirAll(nmDir, 0755)
	os.WriteFile(filepath.Join(nmDir, "index.js"), []byte("module.exports = {}"), 0644)

	// Create a JS file in src (should be counted).
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "app.js"), []byte("console.log()"), 0644)

	result := ScanWorkspace(dir)

	if !strings.Contains(result, "JavaScript: 1 file") {
		t.Errorf("expected exactly 1 JS file (not counting node_modules):\n%s", result)
	}
}

func TestScanWorkspace_emptyProject(t *testing.T) {
	dir := t.TempDir()

	result := ScanWorkspace(dir)

	// Should still have the header.
	if !strings.Contains(result, "Project Scan Results") {
		t.Errorf("missing header in scan result:\n%s", result)
	}

	// Should NOT have file types section.
	if strings.Contains(result, "Detected File Types") {
		t.Error("empty project should not have file types section")
	}
}
