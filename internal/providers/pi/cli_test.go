package pi

import (
	"slices"
	"strings"
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

func TestNetworkHostsIncludeLunaRoute(t *testing.T) {
	for _, h := range []string{"gw.lunaroute.com", "mcp.lunaroute.com"} {
		if !slices.Contains(NetworkHosts(), h) {
			t.Errorf("NetworkHosts missing %s: %v", h, NetworkHosts())
		}
	}
}

func TestBuildPiCommandLunaRoute(t *testing.T) {
	origProv, origModel := piResolvedProvider, piResolvedModel
	t.Cleanup(func() { piResolvedProvider, piResolvedModel = origProv, origModel })

	piResolvedProvider, piResolvedModel = "lunaroute", "glm-5.3"
	got := buildPiCommand("it's a \"prompt\"", "")
	// The catalog warm-up runs first; pi's own args ride as positional
	// parameters so the prompt is never re-parsed by the shell.
	want := []string{
		"sh", "-c", lunaRouteLaunchScript, "pi",
		"--provider", "lunaroute", "--model", "glm-5.3",
		"--append-system-prompt", PiInitMountPath + "/" + ContextFileName,
		"-p", "it's a \"prompt\"",
	}
	if !slices.Equal(got, want) {
		t.Errorf("buildPiCommand = %q, want %q", got, want)
	}

	// Companion: other backends launch pi directly, with no warm-up.
	piResolvedProvider = "anthropic"
	if got := buildPiCommand("", ""); got[0] != "pi" {
		t.Errorf("anthropic backend should exec pi directly, got %q", got)
	}
}

func TestLunaRouteLaunchScript(t *testing.T) {
	for _, want := range []string{
		"models-store.json", // readiness signal written by the extension
		"pi --mode rpc",     // the warm-up that triggers the catalog fetch
		`exec pi "$@"`,      // hands off to the real command with its args intact
	} {
		if !strings.Contains(lunaRouteLaunchScript, want) {
			t.Errorf("launch script missing %q:\n%s", want, lunaRouteLaunchScript)
		}
	}
}

func TestWithLunaRouteExtension(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty adds it", nil, []string{LunaRouteExtensionPackage}},
		{"keeps user packages", []string{"npm:other"}, []string{"npm:other", LunaRouteExtensionPackage}},
		{"no duplicate when listed", []string{LunaRouteExtensionPackage}, []string{LunaRouteExtensionPackage}},
		{"no duplicate when pinned", []string{"npm:@lunaroute/pi-extension@0.12.1"}, []string{"npm:@lunaroute/pi-extension@0.12.1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := slices.Clone(tt.in)
			got := withLunaRouteExtension(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Errorf("withLunaRouteExtension(%v) = %v, want %v", tt.in, got, tt.want)
			}
			if !slices.Equal(tt.in, in) {
				t.Errorf("input mutated: %v", tt.in)
			}
		})
	}
}

func TestHasLunaRouteExtension(t *testing.T) {
	for _, pkgs := range [][]string{
		{LunaRouteExtensionPackage},
		{"npm:@lunaroute/pi-extension@0.12.1"},
	} {
		if !HasLunaRouteExtension(pkgs) {
			t.Errorf("HasLunaRouteExtension(%v) = false, want true", pkgs)
		}
	}
	for _, pkgs := range [][]string{
		nil,
		{"npm:@lunaroute/pi-extension-fork"},
		{"npm:@lunaroute/cli"},
	} {
		if HasLunaRouteExtension(pkgs) {
			t.Errorf("HasLunaRouteExtension(%v) = true, want false", pkgs)
		}
	}
}

func TestCheckAnthropicGatewayKey(t *testing.T) {
	if err := checkAnthropicGatewayKey("anthropic", "https://gw.lunaroute.com"); err == nil {
		t.Error("gateway anthropic key with the anthropic backend: want error")
	} else if !strings.Contains(err.Error(), "moat grant lunaroute") {
		t.Errorf("error should point at moat grant lunaroute: %v", err)
	}
	// Companions: a plain Anthropic key, and a gateway key on another backend.
	if err := checkAnthropicGatewayKey("anthropic", ""); err != nil {
		t.Errorf("plain anthropic key: unexpected error %v", err)
	}
	if err := checkAnthropicGatewayKey("lunaroute", "https://gw.lunaroute.com"); err != nil {
		t.Errorf("lunaroute backend: unexpected error %v", err)
	}
}
