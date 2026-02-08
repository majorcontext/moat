package image

import (
	"testing"

	"github.com/majorcontext/moat/internal/deps"
)

func TestResolveNoDeps(t *testing.T) {
	img := Resolve(nil, nil)
	if img != DefaultImage {
		t.Errorf("Resolve(nil) = %q, want %q", img, DefaultImage)
	}
}

func TestResolveWithDeps(t *testing.T) {
	depList := []deps.Dependency{{Name: "node", Version: "20"}}
	img := Resolve(depList, nil)
	// Should return a generated image tag
	if img == DefaultImage {
		t.Error("Resolve with deps should not return default image")
	}
	if img == "" {
		t.Error("Resolve with deps should return non-empty image")
	}
}

func TestResolveEmptyDeps(t *testing.T) {
	img := Resolve([]deps.Dependency{}, nil)
	if img != DefaultImage {
		t.Errorf("Resolve(empty deps) = %q, want %q", img, DefaultImage)
	}
}

func TestResolveMultipleDeps(t *testing.T) {
	depList := []deps.Dependency{
		{Name: "node", Version: "20"},
		{Name: "python", Version: "3.11"},
	}
	img := Resolve(depList, nil)
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

func TestResolveWithSSHOnly(t *testing.T) {
	// SSH grants without other deps should still trigger custom image
	img := Resolve(nil, &ResolveOptions{NeedsSSH: true})
	if img == DefaultImage {
		t.Error("Resolve with SSH should not return default image")
	}
}

func TestResolveWithHooks(t *testing.T) {
	// Hooks without other deps should still trigger custom image
	img := Resolve(nil, &ResolveOptions{
		Hooks: &deps.HooksConfig{
			PostBuild: "git config --global core.autocrlf input",
		},
	})
	if img == DefaultImage {
		t.Error("Resolve with hooks should not return default image")
	}
}

func TestResolveWithHooksAffectHash(t *testing.T) {
	noHooks := Resolve(nil, &ResolveOptions{NeedsSSH: true})
	withHooks := Resolve(nil, &ResolveOptions{
		NeedsSSH: true,
		Hooks: &deps.HooksConfig{
			PostBuild: "git config --global core.autocrlf input",
		},
	})
	if noHooks == withHooks {
		t.Error("hooks should change the image hash")
	}
}

func TestResolveWithPreRunOnly(t *testing.T) {
	// pre_run alone should trigger custom image (needs moat-init)
	img := Resolve(nil, &ResolveOptions{
		Hooks: &deps.HooksConfig{
			PreRun: "npm install",
		},
	})
	if img == DefaultImage {
		t.Error("Resolve with pre_run should not return default image")
	}
}

func TestResolveWithDepsAndSSH(t *testing.T) {
	depList := []deps.Dependency{{Name: "node", Version: "20"}}
	imgWithoutSSH := Resolve(depList, nil)
	imgWithSSH := Resolve(depList, &ResolveOptions{NeedsSSH: true})

	// Both should be custom images but different
	if imgWithoutSSH == DefaultImage || imgWithSSH == DefaultImage {
		t.Error("Both should return custom images")
	}
	if imgWithoutSSH == imgWithSSH {
		t.Error("SSH should produce different image tag")
	}
}
