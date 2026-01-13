// internal/deps/registry_test.go
package deps

import "testing"

func TestRegistryLoaded(t *testing.T) {
	if len(Registry) == 0 {
		t.Fatal("Registry should not be empty")
	}
}

func TestRegistryHasNode(t *testing.T) {
	node, ok := Registry["node"]
	if !ok {
		t.Fatal("Registry should have 'node'")
	}
	if node.Type != TypeRuntime {
		t.Errorf("node.Type = %v, want %v", node.Type, TypeRuntime)
	}
	if node.Default == "" {
		t.Error("node.Default should not be empty")
	}
}

func TestRegistryHasProtoc(t *testing.T) {
	protoc, ok := Registry["protoc"]
	if !ok {
		t.Fatal("Registry should have 'protoc'")
	}
	if protoc.Type != TypeGithubBinary {
		t.Errorf("protoc.Type = %v, want %v", protoc.Type, TypeGithubBinary)
	}
	if protoc.Repo == "" {
		t.Error("protoc.Repo should not be empty")
	}
}

func TestRegistryHasPlaywright(t *testing.T) {
	pw, ok := Registry["playwright"]
	if !ok {
		t.Fatal("Registry should have 'playwright'")
	}
	if pw.Type != TypeCustom {
		t.Errorf("playwright.Type = %v, want %v", pw.Type, TypeCustom)
	}
	if len(pw.Requires) == 0 || pw.Requires[0] != "node" {
		t.Errorf("playwright.Requires = %v, want [node]", pw.Requires)
	}
}
