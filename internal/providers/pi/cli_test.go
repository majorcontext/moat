package pi

import (
	"slices"
	"testing"
)

func TestDefaultDependenciesAndHosts(t *testing.T) {
	if !slices.Contains(DefaultDependencies(), "pi-cli") {
		t.Errorf("DefaultDependencies missing pi-cli: %v", DefaultDependencies())
	}
	if !slices.Contains(NetworkHosts(), "api.anthropic.com") || !slices.Contains(NetworkHosts(), "api.openai.com") {
		t.Errorf("NetworkHosts missing a backend host: %v", NetworkHosts())
	}
}

func TestBuildPiCommand(t *testing.T) {
	// Save/restore package resolution state.
	origProv, origModel := piResolvedProvider, piResolvedModel
	t.Cleanup(func() { piResolvedProvider, piResolvedModel = origProv, origModel })

	ctx := PiInitMountPath + "/" + ContextFileName

	t.Run("prompt with model", func(t *testing.T) {
		piResolvedProvider, piResolvedModel = "openai", "gpt-5"
		got := buildPiCommand("do it", "")
		want := []string{"pi", "--provider", "openai", "--model", "gpt-5", "--append-system-prompt", ctx, "-p", "do it"}
		if !slices.Equal(got, want) {
			t.Errorf("buildPiCommand = %v, want %v", got, want)
		}
	})

	t.Run("interactive no model", func(t *testing.T) {
		piResolvedProvider, piResolvedModel = "anthropic", ""
		got := buildPiCommand("", "")
		want := []string{"pi", "--provider", "anthropic", "--append-system-prompt", ctx}
		if !slices.Equal(got, want) {
			t.Errorf("buildPiCommand = %v, want %v", got, want)
		}
	})

	t.Run("initial prompt passthrough", func(t *testing.T) {
		piResolvedProvider, piResolvedModel = "anthropic", ""
		got := buildPiCommand("", "hello there")
		want := []string{"pi", "--provider", "anthropic", "--append-system-prompt", ctx, "hello there"}
		if !slices.Equal(got, want) {
			t.Errorf("buildPiCommand = %v, want %v", got, want)
		}
	})
}
