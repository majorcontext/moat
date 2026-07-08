package copilot

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

type mockProxyConfigurer struct {
	headers map[string]map[string]string
	grants  map[string]map[string]string
}

func newMockProxyConfigurer() *mockProxyConfigurer {
	return &mockProxyConfigurer{
		headers: make(map[string]map[string]string),
		grants:  make(map[string]map[string]string),
	}
}

func (m *mockProxyConfigurer) SetCredential(host, value string) {}

func (m *mockProxyConfigurer) SetCredentialHeader(host, headerName, headerValue string) {}

func (m *mockProxyConfigurer) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	if m.headers[host] == nil {
		m.headers[host] = make(map[string]string)
		m.grants[host] = make(map[string]string)
	}
	m.headers[host][headerName] = headerValue
	m.grants[host][headerName] = grant
}

func (m *mockProxyConfigurer) AddExtraHeader(host, headerName, headerValue string) {}

func (m *mockProxyConfigurer) AddResponseTransformer(host string, transformer provider.ResponseTransformer) {
}

func (m *mockProxyConfigurer) RemoveRequestHeader(host, header string) {}

func (m *mockProxyConfigurer) SetTokenSubstitution(host, placeholder, realToken string) {}

func TestProviderName(t *testing.T) {
	p := &Provider{}
	if got := p.Name(); got != "copilot" {
		t.Errorf("Name() = %q, want copilot", got)
	}
}

func TestConfigureProxy(t *testing.T) {
	p := &Provider{}
	proxy := newMockProxyConfigurer()
	p.ConfigureProxy(proxy, &provider.Credential{Provider: "copilot", Token: "github_pat_test"})

	if got := proxy.headers[copilotAPIHost]["Authorization"]; got != "Bearer github_pat_test" {
		t.Errorf("api.github.com Authorization = %q", got)
	}
	if got := proxy.grants[copilotAPIHost]["Authorization"]; got != "copilot" {
		t.Errorf("api.github.com grant = %q, want copilot", got)
	}
	if got := proxy.headers[copilotBusinessHost]["Authorization"]; got != "Bearer github_pat_test" {
		t.Errorf("api.business.githubcopilot.com Authorization = %q", got)
	}
	if got := proxy.headers[copilotGitHost]["Authorization"]; !strings.HasPrefix(got, "Basic ") {
		t.Errorf("github.com Authorization = %q, want Basic auth", got)
	}
}

func TestContainerEnv(t *testing.T) {
	env := (&Provider{}).ContainerEnv(&provider.Credential{Provider: "copilot"})
	for _, want := range []string{
		"COPILOT_GITHUB_TOKEN=" + credential.CopilotTokenPlaceholder,
		"GH_TOKEN=" + credential.CopilotTokenPlaceholder,
		"GIT_TERMINAL_PROMPT=0",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("ContainerEnv missing %q in %v", want, env)
		}
	}
}

func TestDefaultDependenciesAndHosts(t *testing.T) {
	if !slices.Contains(DefaultDependencies(), "copilot-cli") {
		t.Errorf("DefaultDependencies missing copilot-cli: %v", DefaultDependencies())
	}
	if !slices.Contains(NetworkHosts(), copilotAPIHost) || !slices.Contains(NetworkHosts(), copilotBusinessHost) || !slices.Contains(NetworkHosts(), copilotProxyHost) {
		t.Errorf("NetworkHosts missing Copilot hosts: %v", NetworkHosts())
	}
}

func TestBuildCopilotCommand(t *testing.T) {
	origModel, origExperimental, origAutopilot, origAllowAll := copilotResolvedModel, copilotExperimental, copilotAutopilot, copilotAllowAll
	t.Cleanup(func() {
		copilotResolvedModel = origModel
		copilotExperimental = origExperimental
		copilotAutopilot = origAutopilot
		copilotAllowAll = origAllowAll
	})

	copilotResolvedModel = "gpt-5.4"
	copilotExperimental = true
	copilotAutopilot = true
	copilotAllowAll = true

	got := buildCopilotCommand("fix it", "")
	want := []string{"copilot", "--no-auto-update", "--model", "gpt-5.4", "--experimental", "--autopilot", "--allow-all", "-p", "fix it"}
	if !slices.Equal(got, want) {
		t.Errorf("buildCopilotCommand = %v, want %v", got, want)
	}

	got = buildCopilotCommand("", "hello")
	if !slices.Contains(got, "--allow-all") {
		t.Errorf("interactive initial prompt should get --allow-all by default: %v", got)
	}
	if !slices.Contains(got, "-i") || !slices.Contains(got, "hello") {
		t.Errorf("initial prompt not passed via -i: %v", got)
	}
}

func TestFilterGitHubGrant(t *testing.T) {
	got := filterGitHubGrant([]string{"github", "copilot", "ssh:github.com", "aws"}, false)
	want := []string{"copilot", "ssh:github.com", "aws"}
	if !slices.Equal(got, want) {
		t.Errorf("filterGitHubGrant = %v, want %v", got, want)
	}
}

func TestPrepareContainer(t *testing.T) {
	cfg, err := (&Provider{}).PrepareContainer(t.Context(), provider.PrepareOpts{RuntimeContext: "hello"})
	if err != nil {
		t.Fatalf("PrepareContainer() error = %v", err)
	}
	t.Cleanup(cfg.Cleanup)

	if !slices.Contains(cfg.Env, "MOAT_COPILOT_INIT="+CopilotInitMountPath) {
		t.Errorf("env missing MOAT_COPILOT_INIT: %v", cfg.Env)
	}
	if _, err := os.Stat(filepath.Join(cfg.StagingDir, ContextFileName)); err != nil {
		t.Fatalf("context file missing: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cfg.StagingDir, "config.json"))
	if err != nil {
		t.Fatalf("reading config.json: %v", err)
	}
	if !strings.Contains(string(data), "/workspace") {
		t.Errorf("config.json should trust /workspace, got %s", data)
	}
}
