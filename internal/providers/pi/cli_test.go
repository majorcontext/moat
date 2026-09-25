package pi

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
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

func TestConfigurePiBackend(t *testing.T) {
	hosts := func(cfg *config.Config) []string {
		out := make([]string, 0, len(cfg.Network.Rules))
		for _, r := range cfg.Network.Rules {
			out = append(out, r.Host)
		}
		return out
	}

	t.Run("lunaroute", func(t *testing.T) {
		cfg := &config.Config{}
		configurePiBackend(cfg, "lunaroute")
		if cfg.Pi.Provider != "lunaroute" {
			t.Errorf("Pi.Provider = %q, want lunaroute", cfg.Pi.Provider)
		}
		if !HasLunaRouteExtension(cfg.Pi.Packages) {
			t.Errorf("extension not added: %v", cfg.Pi.Packages)
		}
		for _, h := range []string{"gw.lunaroute.com", "mcp.lunaroute.com"} {
			if !slices.Contains(hosts(cfg), h) {
				t.Errorf("network rules missing %s: %v", h, hosts(cfg))
			}
		}
	})

	// Companion: other backends get neither the extension nor LunaRoute's
	// hosts, so a strict run can't reach the gateway.
	for _, backend := range []string{"anthropic", "openai"} {
		t.Run(backend, func(t *testing.T) {
			cfg := &config.Config{}
			configurePiBackend(cfg, backend)
			if cfg.Pi.Provider != backend {
				t.Errorf("Pi.Provider = %q, want %s", cfg.Pi.Provider, backend)
			}
			if len(cfg.Pi.Packages) != 0 {
				t.Errorf("packages = %v, want none", cfg.Pi.Packages)
			}
			for _, h := range hosts(cfg) {
				if strings.Contains(h, "lunaroute") {
					t.Errorf("LunaRoute host %s allowed for the %s backend", h, backend)
				}
			}
		})
	}
	for _, h := range NetworkHosts() {
		if strings.Contains(h, "lunaroute") {
			t.Errorf("NetworkHosts (every backend) includes %s", h)
		}
	}
}

func TestPiGrantsFromStore(t *testing.T) {
	store := func(creds map[credential.Provider]*credential.Credential, failOn credential.Provider) func(credential.Provider) (*credential.Credential, error) {
		return func(p credential.Provider) (*credential.Credential, error) {
			if p == failOn {
				return nil, errors.New("cipher: message authentication failed")
			}
			if c, ok := creds[p]; ok {
				return c, nil
			}
			return nil, fmt.Errorf("%w: %s", credential.ErrNotFound, p)
		}
	}

	t.Run("none configured", func(t *testing.T) {
		g, err := piGrantsFromStore(store(nil, ""))
		if err != nil || g != (piGrants{}) {
			t.Errorf("got %+v, %v; want empty grants, nil", g, err)
		}
	})
	t.Run("all configured", func(t *testing.T) {
		g, err := piGrantsFromStore(store(map[credential.Provider]*credential.Credential{
			credential.ProviderAnthropic: {},
			credential.ProviderOpenAI:    {},
			credential.ProviderLunaRoute: {},
		}, ""))
		want := piGrants{Anthropic: true, OpenAI: true, LunaRoute: true}
		if err != nil || g != want {
			t.Errorf("got %+v, %v; want %+v", g, err, want)
		}
	})
	t.Run("gateway anthropic key is not a plain anthropic key", func(t *testing.T) {
		g, err := piGrantsFromStore(store(map[credential.Provider]*credential.Credential{
			credential.ProviderAnthropic: {Metadata: map[string]string{credential.MetaKeyBaseURL: "https://gw.lunaroute.com"}},
		}, ""))
		want := piGrants{AnthropicGatewayURL: "https://gw.lunaroute.com"}
		if err != nil || g != want {
			t.Errorf("got %+v, %v; want %+v", g, err, want)
		}
	})
	// A credential that exists but can't be read must not look "not
	// configured" — that would tell the user to grant a key they already have.
	for _, p := range []credential.Provider{credential.ProviderAnthropic, credential.ProviderOpenAI, credential.ProviderLunaRoute} {
		t.Run("read failure on "+string(p), func(t *testing.T) {
			_, err := piGrantsFromStore(store(nil, p))
			if err == nil || !strings.Contains(err.Error(), "authentication failed") || !strings.Contains(err.Error(), string(p)) {
				t.Errorf("err = %v, want the %s read failure", err, p)
			}
		})
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

func TestAnthropicGatewayErr(t *testing.T) {
	for _, u := range []string{"https://gw.lunaroute.com", "https://GW.LunaRoute.com/", "https://lunaroute.com"} {
		if err := anthropicGatewayErr(u); !strings.Contains(err.Error(), "moat grant lunaroute\n  Then: moat pi --provider lunaroute") {
			t.Errorf("%s: want LunaRoute advice, got:\n%v", u, err)
		}
	}
	// Companion: any other gateway must not be sent to LunaRoute.
	for _, u := range []string{"https://gateway.example.com", "https://lunaroute.com.evil.example", "https://notlunaroute.com", "::bad"} {
		err := anthropicGatewayErr(u)
		if strings.Contains(err.Error(), "--provider lunaroute") {
			t.Errorf("%s: non-LunaRoute gateway got LunaRoute advice:\n%v", u, err)
		}
		if !strings.Contains(err.Error(), "without --base-url") {
			t.Errorf("%s: want the generic backend list, got:\n%v", u, err)
		}
	}
}
