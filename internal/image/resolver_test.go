package image

import (
	"testing"

	"github.com/andybons/agentops/internal/deps"
)

func TestResolveNoDeps(t *testing.T) {
	img := Resolve(nil)
	if img != DefaultImage {
		t.Errorf("Resolve(nil) = %q, want %q", img, DefaultImage)
	}
}

func TestResolveWithDeps(t *testing.T) {
	depList := []deps.Dependency{{Name: "node", Version: "20"}}
	img := Resolve(depList)
	// Should return a generated image tag
	if img == DefaultImage {
		t.Error("Resolve with deps should not return default image")
	}
	if img == "" {
		t.Error("Resolve with deps should return non-empty image")
	}
}

func TestResolveEmptyDeps(t *testing.T) {
	img := Resolve([]deps.Dependency{})
	if img != DefaultImage {
		t.Errorf("Resolve(empty deps) = %q, want %q", img, DefaultImage)
	}
}

func TestResolveMultipleDeps(t *testing.T) {
	depList := []deps.Dependency{
		{Name: "node", Version: "20"},
		{Name: "python", Version: "3.11"},
	}
	img := Resolve(depList)
	// Should return a generated image tag for multiple deps
	if img == DefaultImage {
		t.Error("Resolve with multiple deps should not return default image")
	}
	if img == "" {
		t.Error("Resolve with multiple deps should return non-empty image")
	}
	// Log the actual format for verification
	t.Logf("Image tag format: %s", img)
}
