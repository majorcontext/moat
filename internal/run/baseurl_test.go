package run

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/keep"
	"github.com/majorcontext/moat/internal/netrules"
	"github.com/majorcontext/moat/internal/provider"
)

func TestResolveClaudeBaseURL(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantContainer string
		wantCredHost  string
		wantHostPort  int
	}{
		// Remote endpoints pass through untouched: the proxy sees an ordinary
		// request to that host and injects on the way through.
		{
			name:          "remote https gateway",
			raw:           "https://gw.lunaroute.com",
			wantContainer: "https://gw.lunaroute.com",
			wantCredHost:  "gw.lunaroute.com",
			wantHostPort:  0,
		},
		{
			name:          "remote gateway with port and path",
			raw:           "https://gw.example.com:8443/anthropic",
			wantContainer: "https://gw.example.com:8443/anthropic",
			wantCredHost:  "gw.example.com",
			wantHostPort:  0,
		},
		{
			name:          "remote http gateway",
			raw:           "http://llm.internal",
			wantContainer: "http://llm.internal",
			wantCredHost:  "llm.internal",
			wantHostPort:  0,
		},
		// Loopback endpoints are rewritten to the synthetic host gateway, which
		// is absent from NO_PROXY so the request still traverses the proxy.
		{
			name:          "localhost with port",
			raw:           "http://localhost:8787",
			wantContainer: "http://" + syntheticHostGateway + ":8787",
			wantCredHost:  syntheticHostGateway,
			wantHostPort:  8787,
		},
		{
			name:          "127.0.0.1 with port",
			raw:           "http://127.0.0.1:8787",
			wantContainer: "http://" + syntheticHostGateway + ":8787",
			wantCredHost:  syntheticHostGateway,
			wantHostPort:  8787,
		},
		{
			name:          "IPv6 loopback with port",
			raw:           "http://[::1]:8787",
			wantContainer: "http://" + syntheticHostGateway + ":8787",
			wantCredHost:  syntheticHostGateway,
			wantHostPort:  8787,
		},
		{
			name:          "non-standard loopback address",
			raw:           "http://127.0.0.2:9000",
			wantContainer: "http://" + syntheticHostGateway + ":9000",
			wantCredHost:  syntheticHostGateway,
			wantHostPort:  9000,
		},
		{
			name:          "loopback keeps its path",
			raw:           "http://localhost:8787/v1/proxy",
			wantContainer: "http://" + syntheticHostGateway + ":8787/v1/proxy",
			wantCredHost:  syntheticHostGateway,
			wantHostPort:  8787,
		},
		// A loopback URL with no port still needs one: the rewritten URL has to
		// name the port, and that exact port is what gets allowed.
		{
			name:          "loopback http defaults to port 80",
			raw:           "http://localhost",
			wantContainer: "http://" + syntheticHostGateway + ":80",
			wantCredHost:  syntheticHostGateway,
			wantHostPort:  80,
		},
		{
			name:          "loopback https defaults to port 443",
			raw:           "https://localhost",
			wantContainer: "https://" + syntheticHostGateway + ":443",
			wantCredHost:  syntheticHostGateway,
			wantHostPort:  443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveClaudeBaseURL(tt.raw)
			if err != nil {
				t.Fatalf("resolveClaudeBaseURL(%q): unexpected error: %v", tt.raw, err)
			}
			if got.ContainerURL != tt.wantContainer {
				t.Errorf("ContainerURL = %q, want %q", got.ContainerURL, tt.wantContainer)
			}
			if got.CredentialHost != tt.wantCredHost {
				t.Errorf("CredentialHost = %q, want %q", got.CredentialHost, tt.wantCredHost)
			}
			if got.HostPort != tt.wantHostPort {
				t.Errorf("HostPort = %d, want %d", got.HostPort, tt.wantHostPort)
			}
		})
	}
}

// TestResolveClaudeBaseURLNeverRelays guards the regression this fix exists for:
// the resolved URL must point at the endpoint (directly or via the host
// gateway), never at a /relay/ path on the proxy. No relay is ever registered
// with the shared daemon, so a relay URL fails with 407.
func TestResolveClaudeBaseURLNeverRelays(t *testing.T) {
	for _, raw := range []string{"https://gw.lunaroute.com", "http://localhost:8787"} {
		got, err := resolveClaudeBaseURL(raw)
		if err != nil {
			t.Fatalf("resolveClaudeBaseURL(%q): %v", raw, err)
		}
		if got.ContainerURL == "" {
			t.Fatalf("resolveClaudeBaseURL(%q): empty ContainerURL", raw)
		}
		if strings.Contains(got.ContainerURL, "/relay/") {
			t.Errorf("resolveClaudeBaseURL(%q) = %q, must not use a relay path", raw, got.ContainerURL)
		}
		if strings.Contains(got.ContainerURL, syntheticProxyHost+":") {
			t.Errorf("resolveClaudeBaseURL(%q) = %q, must not point at the proxy host", raw, got.ContainerURL)
		}
	}
}

func TestResolveClaudeBaseURLErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "no host", raw: "http://"},
		{name: "empty", raw: ""},
		{name: "control character", raw: "http://local\x7fhost:8787"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := resolveClaudeBaseURL(tt.raw); err == nil {
				t.Errorf("resolveClaudeBaseURL(%q): expected an error, got nil", tt.raw)
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	loopback := []string{"localhost", "127.0.0.1", "127.0.0.2", "::1"}
	for _, h := range loopback {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false, want true", h)
		}
	}
	// Companion case: hosts that must NOT be treated as host-local, including
	// ones that merely look like localhost.
	remote := []string{"gw.lunaroute.com", "localhost.evil.com", "notlocalhost", "10.0.0.1", "8.8.8.8", ""}
	for _, h := range remote {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true, want false", h)
		}
	}
}

func TestConfigureClaudeBaseURL(t *testing.T) {
	apiKeyCred := &provider.Credential{Provider: "anthropic", Token: "sk-ant-api03-secret"}

	t.Run("no config", func(t *testing.T) {
		rc := daemon.NewRunContext("run_test")
		env, err := configureClaudeBaseURL(rc, nil, apiKeyCred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if env != "" {
			t.Errorf("env = %q, want empty", env)
		}
		if len(rc.Credentials) != 0 {
			t.Errorf("registered credentials for %d hosts, want 0", len(rc.Credentials))
		}
	})

	t.Run("no base_url", func(t *testing.T) {
		rc := daemon.NewRunContext("run_test")
		env, err := configureClaudeBaseURL(rc, &config.Config{}, apiKeyCred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if env != "" {
			t.Errorf("env = %q, want empty", env)
		}
		if len(rc.Credentials) != 0 {
			t.Errorf("registered credentials for %d hosts, want 0", len(rc.Credentials))
		}
	})

	t.Run("remote gateway registers credential for the gateway host", func(t *testing.T) {
		rc := daemon.NewRunContext("run_test")
		cfg := &config.Config{}
		cfg.Claude.BaseURL = "https://gw.lunaroute.com"

		env, err := configureClaudeBaseURL(rc, cfg, apiKeyCred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := "ANTHROPIC_BASE_URL=https://gw.lunaroute.com"; env != want {
			t.Errorf("env = %q, want %q", env, want)
		}
		if len(rc.Credentials["gw.lunaroute.com"]) == 0 {
			t.Errorf("no credential registered for gw.lunaroute.com; hosts = %v", credHosts(rc))
		}
		if len(rc.AllowedHostPorts) != 0 {
			t.Errorf("AllowedHostPorts = %v, want none for a remote gateway", rc.AllowedHostPorts)
		}
	})

	t.Run("loopback endpoint registers credential for the gateway host and allows the port", func(t *testing.T) {
		rc := daemon.NewRunContext("run_test")
		cfg := &config.Config{}
		cfg.Claude.BaseURL = "http://localhost:8787"

		env, err := configureClaudeBaseURL(rc, cfg, apiKeyCred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := "ANTHROPIC_BASE_URL=http://" + syntheticHostGateway + ":8787"; env != want {
			t.Errorf("env = %q, want %q", env, want)
		}
		// The container connects to the host gateway, so that is the host the
		// proxy matches on — registering "localhost" would never fire.
		if len(rc.Credentials[syntheticHostGateway]) == 0 {
			t.Errorf("no credential registered for %s; hosts = %v", syntheticHostGateway, credHosts(rc))
		}
		if len(rc.Credentials["localhost"]) != 0 {
			t.Error("credential registered for localhost, which the proxy never sees")
		}
		if len(rc.AllowedHostPorts) != 1 || rc.AllowedHostPorts[0] != 8787 {
			t.Errorf("AllowedHostPorts = %v, want [8787]", rc.AllowedHostPorts)
		}
	})

	t.Run("loopback port is added alongside configured host ports", func(t *testing.T) {
		rc := daemon.NewRunContext("run_test")
		cfg := &config.Config{}
		cfg.Claude.BaseURL = "http://localhost:8787"
		cfg.Network.Host = []int{5432}
		rc.AllowedHostPorts = cfg.Network.Host

		if _, err := configureClaudeBaseURL(rc, cfg, apiKeyCred); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rc.AllowedHostPorts) != 2 || rc.AllowedHostPorts[0] != 5432 || rc.AllowedHostPorts[1] != 8787 {
			t.Errorf("AllowedHostPorts = %v, want [5432 8787]", rc.AllowedHostPorts)
		}
		// The run context slice aliased cfg.Network.Host; appending must not
		// have written through to the config.
		if len(cfg.Network.Host) != 1 || cfg.Network.Host[0] != 5432 {
			t.Errorf("cfg.Network.Host = %v, want [5432] — config was mutated", cfg.Network.Host)
		}
	})

	t.Run("already allowed loopback port is not duplicated", func(t *testing.T) {
		rc := daemon.NewRunContext("run_test")
		cfg := &config.Config{}
		cfg.Claude.BaseURL = "http://localhost:8787"
		rc.AllowedHostPorts = []int{8787}

		if _, err := configureClaudeBaseURL(rc, cfg, apiKeyCred); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rc.AllowedHostPorts) != 1 {
			t.Errorf("AllowedHostPorts = %v, want a single 8787", rc.AllowedHostPorts)
		}
	})

	// Companion case to the credential-registering tests: with no grant the
	// endpoint still applies, there is simply nothing to inject.
	t.Run("no credential still sets the endpoint", func(t *testing.T) {
		rc := daemon.NewRunContext("run_test")
		cfg := &config.Config{}
		cfg.Claude.BaseURL = "https://gw.lunaroute.com"

		env, err := configureClaudeBaseURL(rc, cfg, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := "ANTHROPIC_BASE_URL=https://gw.lunaroute.com"; env != want {
			t.Errorf("env = %q, want %q", env, want)
		}
		if len(rc.Credentials) != 0 {
			t.Errorf("registered credentials for %v, want none without a grant", credHosts(rc))
		}
	})

	t.Run("oauth credential injects a bearer token", func(t *testing.T) {
		rc := daemon.NewRunContext("run_test")
		cfg := &config.Config{}
		cfg.Claude.BaseURL = "https://gw.example.com"

		oauthCred := &provider.Credential{Provider: "claude", Token: "sk-ant-oat01-secret"}
		if _, err := configureClaudeBaseURL(rc, cfg, oauthCred); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		creds := rc.Credentials["gw.example.com"]
		if len(creds) == 0 {
			t.Fatalf("no credential registered for gw.example.com; hosts = %v", credHosts(rc))
		}
	})
}

// TestConfigureClaudeBaseURLReachesRegisterRequest is the regression test for
// the ordering bug: the base-URL credential used to be registered after the run
// was already registered with the daemon, so it never reached the proxy.
func TestConfigureClaudeBaseURLReachesRegisterRequest(t *testing.T) {
	rc := daemon.NewRunContext("run_test")
	cfg := &config.Config{}
	cfg.Claude.BaseURL = "https://gw.lunaroute.com"
	cfg.Network.Host = []int{}

	if _, err := configureClaudeBaseURL(rc, cfg, &provider.Credential{Provider: "anthropic", Token: "sk-ant-api03-secret"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := buildRegisterRequest(rc, []string{"anthropic"})

	var found bool
	for _, c := range req.Credentials {
		if c.Host == "gw.lunaroute.com" {
			found = true
			if c.Value == "" {
				t.Errorf("credential for %s has an empty value", c.Host)
			}
		}
	}
	if !found {
		t.Errorf("register request has no credential for gw.lunaroute.com; got %+v", req.Credentials)
	}
}

// TestConfigureClaudeBaseURLLoopbackPortReachesRegisterRequest is the companion
// to the credential ordering test: the auto-allowed host port has to survive
// into the registration request too, or the container cannot reach the endpoint.
func TestConfigureClaudeBaseURLLoopbackPortReachesRegisterRequest(t *testing.T) {
	rc := daemon.NewRunContext("run_test")
	cfg := &config.Config{}
	cfg.Claude.BaseURL = "http://localhost:8787"

	if _, err := configureClaudeBaseURL(rc, cfg, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := buildRegisterRequest(rc, nil)
	if len(req.AllowedHostPorts) != 1 || req.AllowedHostPorts[0] != 8787 {
		t.Errorf("register request AllowedHostPorts = %v, want [8787]", req.AllowedHostPorts)
	}
}

func credHosts(rc *daemon.RunContext) []string {
	hosts := make([]string, 0, len(rc.Credentials))
	for h := range rc.Credentials {
		hosts = append(hosts, h)
	}
	return hosts
}

// TestProxyRequiredForConfig covers both directions: each proxy-enforced
// feature on its own must start the proxy, and a config with none of them must
// not. A one-sided version of this test would let a new feature silently
// no-op on a grant-less run — the bug the helper exists to prevent.
func TestProxyRequiredForConfig(t *testing.T) {
	requires := []struct {
		name string
		cfg  *config.Config
	}{
		{
			name: "claude.base_url",
			cfg: func() *config.Config {
				c := &config.Config{}
				c.Claude.BaseURL = "https://gw.lunaroute.com"
				return c
			}(),
		},
		{
			name: "network.host",
			cfg:  &config.Config{Network: config.NetworkConfig{Host: []int{5432}}},
		},
		{
			name: "network.rules",
			cfg: &config.Config{Network: config.NetworkConfig{
				Rules: []netrules.NetworkRuleEntry{{HostRules: netrules.HostRules{Host: "example.com"}}},
			}},
		},
		{
			name: "mcp servers",
			cfg:  &config.Config{MCP: []config.MCPServerConfig{{Name: "notion", URL: "https://mcp.notion.com"}}},
		},
		{
			name: "network.keep_policy",
			cfg:  &config.Config{Network: config.NetworkConfig{KeepPolicy: &keep.PolicyConfig{}}},
		},
		{
			name: "claude.llm_gateway policy",
			cfg: func() *config.Config {
				c := &config.Config{}
				c.Claude.LLMGateway = &config.LLMGatewayConfig{Policy: &keep.PolicyConfig{}}
				return c
			}(),
		},
	}
	for _, tt := range requires {
		t.Run(tt.name, func(t *testing.T) {
			if !proxyRequiredForConfig(tt.cfg) {
				t.Errorf("proxyRequiredForConfig(%s) = false, want true", tt.name)
			}
		})
	}

	// Companion cases: nothing the proxy owns, so a grant-less run stays
	// proxy-free.
	t.Run("nil config", func(t *testing.T) {
		if proxyRequiredForConfig(nil) {
			t.Error("proxyRequiredForConfig(nil) = true, want false")
		}
	})
	t.Run("empty config", func(t *testing.T) {
		if proxyRequiredForConfig(&config.Config{}) {
			t.Error("proxyRequiredForConfig(empty) = true, want false")
		}
	})
	t.Run("unrelated fields only", func(t *testing.T) {
		cfg := &config.Config{Agent: "claude", Dependencies: []string{"node@22"}}
		cfg.Claude.SkipPermissionsPrompt = true
		if proxyRequiredForConfig(cfg) {
			t.Error("proxyRequiredForConfig(unrelated fields) = true, want false")
		}
	})
	t.Run("llm_gateway without a policy", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Claude.LLMGateway = &config.LLMGatewayConfig{}
		if proxyRequiredForConfig(cfg) {
			t.Error("proxyRequiredForConfig(llm_gateway with no policy) = true, want false")
		}
	})
}

// TestConfigureClaudeBaseURLInvalidURL is the companion to the success cases:
// an unresolvable base_url must surface as an error rather than silently
// leaving the run pointed at api.anthropic.com.
func TestConfigureClaudeBaseURLInvalidURL(t *testing.T) {
	rc := daemon.NewRunContext("run_test")
	cfg := &config.Config{}
	cfg.Claude.BaseURL = "http://"

	env, err := configureClaudeBaseURL(rc, cfg, &provider.Credential{Provider: "anthropic", Token: "sk-ant-api03-secret"})
	if err == nil {
		t.Fatal("expected an error for a base_url with no host, got nil")
	}
	if env != "" {
		t.Errorf("env = %q, want empty on error", env)
	}
	if len(rc.Credentials) != 0 {
		t.Errorf("registered credentials for %v, want none on error", credHosts(rc))
	}
}
