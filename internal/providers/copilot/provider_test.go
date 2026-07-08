package copilot

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/spf13/cobra"
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
	if got := proxy.grants[copilotAPIHost]["Authorization"]; got != "github" {
		t.Errorf("api.github.com grant = %q, want github", got)
	}
	if got := proxy.headers[copilotBusinessHost]["Authorization"]; got != "Bearer github_pat_test" {
		t.Errorf("api.business.githubcopilot.com Authorization = %q", got)
	}
	if got := proxy.headers[copilotMCPHost]["Authorization"]; got == "" {
		t.Errorf("api.mcp.github.com Authorization was not configured")
	}
	if got := proxy.headers[copilotGitHost]["Authorization"]; !strings.HasPrefix(got, "Basic ") {
		t.Errorf("github.com Authorization = %q, want Basic auth", got)
	}
	if _, ok := proxy.headers[copilotProxyHost]; ok {
		t.Error("copilotProxyHost should NOT have credentials; it uses Copilot session tokens")
	}
	if _, ok := proxy.headers[copilotTelemetry]; ok {
		t.Error("copilotTelemetry should NOT have credentials; it uses Copilot session tokens")
	}
}

func TestContainerEnv(t *testing.T) {
	env := (&Provider{}).ContainerEnv(&provider.Credential{Provider: "copilot"})
	for _, want := range []string{
		"COPILOT_GITHUB_TOKEN=" + credential.CopilotTokenPlaceholder,
	} {
		if !slices.Contains(env, want) {
			t.Errorf("ContainerEnv missing %q in %v", want, env)
		}
	}
	for _, unwanted := range []string{"GH_TOKEN=", "GIT_TERMINAL_PROMPT="} {
		for _, got := range env {
			if strings.HasPrefix(got, unwanted) {
				t.Errorf("ContainerEnv contains %q; github grant owns this env: %v", unwanted, env)
			}
		}
	}
}

func TestProviderNoopMethods(t *testing.T) {
	p := &Provider{}
	if mounts, cleanupPath, err := p.ContainerMounts(&provider.Credential{}, "/home/moatuser"); err != nil || mounts != nil || cleanupPath != "" {
		t.Fatalf("ContainerMounts() = (%v, %q, %v), want nil empty nil", mounts, cleanupPath, err)
	}
	p.Cleanup("/tmp/unused")
	if deps := p.ImpliedDependencies(); !slices.Equal(deps, []string{"gh", "git"}) {
		t.Fatalf("ImpliedDependencies() = %v, want [gh git]", deps)
	}
}

func TestDefaultDependenciesAndHosts(t *testing.T) {
	if !slices.Contains(DefaultDependencies(), "copilot-cli") {
		t.Errorf("DefaultDependencies missing copilot-cli: %v", DefaultDependencies())
	}
	if !slices.Contains(NetworkHosts(), copilotAPIHost) || !slices.Contains(NetworkHosts(), copilotBusinessHost) || !slices.Contains(NetworkHosts(), copilotMCPHost) || !slices.Contains(NetworkHosts(), copilotProxyHost) {
		t.Errorf("NetworkHosts missing Copilot hosts: %v", NetworkHosts())
	}
}

func TestRegisterCLI(t *testing.T) {
	root := &cobra.Command{Use: "moat"}
	(&Provider{}).RegisterCLI(root)
	cmd, _, err := root.Find([]string{"copilot"})
	if err != nil {
		t.Fatalf("Find(copilot) error = %v", err)
	}
	if cmd == nil || cmd.Use != "copilot [workspace] [flags] [-- initial-prompt]" {
		t.Fatalf("registered command = %#v", cmd)
	}
	for _, flag := range []string{"prompt", "allow-all", "model", "experimental", "autopilot", "worktree"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Fatalf("copilot command missing --%s flag", flag)
		}
	}
}

func TestResolveCopilotPreflight(t *testing.T) {
	origModelFlag, origResolvedModel := copilotModelFlag, copilotResolvedModel
	origExperimental, origAutopilot := copilotExperimental, copilotAutopilot
	origFlagGrants := copilotFlags.Grants
	origDryRun := cli.DryRun
	origConfigured := copilotCredentialConfigured
	t.Cleanup(func() {
		copilotModelFlag = origModelFlag
		copilotResolvedModel = origResolvedModel
		copilotExperimental = origExperimental
		copilotAutopilot = origAutopilot
		copilotFlags.Grants = origFlagGrants
		cli.DryRun = origDryRun
		copilotCredentialConfigured = origConfigured
	})

	copilotModelFlag = ""
	copilotExperimental = false
	copilotAutopilot = false
	copilotFlags.Grants = []string{"copilot", "ssh:github.com"}
	cli.DryRun = false
	copilotCredentialConfigured = func() bool { return false }
	cfg := &config.Config{
		Grants:  []string{"github", "copilot"},
		Copilot: config.CopilotConfig{Model: "gpt-5.4", Experimental: true, Autopilot: true},
	}

	if err := resolveCopilotPreflight(cfg); err != nil {
		t.Fatalf("resolveCopilotPreflight() error = %v", err)
	}
	if copilotResolvedModel != "gpt-5.4" || !copilotExperimental || !copilotAutopilot {
		t.Fatalf("resolved state = model:%q experimental:%v autopilot:%v", copilotResolvedModel, copilotExperimental, copilotAutopilot)
	}
	if !slices.Equal(cfg.Grants, []string{"github"}) {
		t.Fatalf("config grants = %v, want [github]", cfg.Grants)
	}
	if !slices.Equal(copilotFlags.Grants, []string{"github", "ssh:github.com"}) {
		t.Fatalf("flag grants = %v, want [github ssh:github.com]", copilotFlags.Grants)
	}
}

func TestResolveCopilotPreflightModelFlagOverridesConfig(t *testing.T) {
	origModelFlag, origResolvedModel := copilotModelFlag, copilotResolvedModel
	origDryRun := cli.DryRun
	origConfigured := copilotCredentialConfigured
	t.Cleanup(func() {
		copilotModelFlag = origModelFlag
		copilotResolvedModel = origResolvedModel
		cli.DryRun = origDryRun
		copilotCredentialConfigured = origConfigured
	})

	copilotModelFlag = "gpt-5.5"
	cli.DryRun = true
	copilotCredentialConfigured = func() bool { return true }
	cfg := &config.Config{
		Copilot: config.CopilotConfig{Model: "claude-sonnet-4"},
	}

	if err := resolveCopilotPreflight(cfg); err != nil {
		t.Fatalf("resolveCopilotPreflight() error = %v", err)
	}
	if copilotResolvedModel != "gpt-5.5" {
		t.Fatalf("CLI --model flag should override config model, got %q", copilotResolvedModel)
	}
}

func TestResolveCopilotPreflightAddsRequiredGrantWhenMissing(t *testing.T) {
	origFlagGrants := copilotFlags.Grants
	origDryRun := cli.DryRun
	origConfigured := copilotCredentialConfigured
	t.Cleanup(func() {
		copilotFlags.Grants = origFlagGrants
		cli.DryRun = origDryRun
		copilotCredentialConfigured = origConfigured
	})

	copilotFlags.Grants = nil
	cli.DryRun = false
	copilotCredentialConfigured = func() bool { return false }

	if err := resolveCopilotPreflight(nil); err != nil {
		t.Fatalf("resolveCopilotPreflight(nil) error = %v", err)
	}
	if !slices.Equal(copilotFlags.Grants, []string{"github"}) {
		t.Fatalf("flag grants = %v, want [github]", copilotFlags.Grants)
	}
}

func TestResolveCopilotPreflightDryRunDoesNotAddMissingGrant(t *testing.T) {
	origFlagGrants := copilotFlags.Grants
	origDryRun := cli.DryRun
	origConfigured := copilotCredentialConfigured
	t.Cleanup(func() {
		copilotFlags.Grants = origFlagGrants
		cli.DryRun = origDryRun
		copilotCredentialConfigured = origConfigured
	})

	copilotFlags.Grants = nil
	cli.DryRun = true
	copilotCredentialConfigured = func() bool { return false }

	if err := resolveCopilotPreflight(nil); err != nil {
		t.Fatalf("resolveCopilotPreflight(nil) error = %v", err)
	}
	if len(copilotFlags.Grants) != 0 {
		t.Fatalf("dry-run flag grants = %v, want empty", copilotFlags.Grants)
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

func TestGetCredentialName(t *testing.T) {
	origConfigured := copilotCredentialConfigured
	t.Cleanup(func() { copilotCredentialConfigured = origConfigured })
	copilotCredentialConfigured = func() bool { return true }
	if got := GetCredentialName(); got != "github" {
		t.Fatalf("GetCredentialName() = %q, want github", got)
	}
	copilotCredentialConfigured = func() bool { return false }
	if got := GetCredentialName(); got != "" {
		t.Fatalf("GetCredentialName() = %q, want empty when missing", got)
	}
}

func TestNormalizeCopilotGrants(t *testing.T) {
	got := normalizeCopilotGrants([]string{"github", "copilot", "ssh:github.com", "aws"}, false)
	want := []string{"github", "ssh:github.com", "aws"}
	if !slices.Equal(got, want) {
		t.Errorf("normalizeCopilotGrants = %v, want %v", got, want)
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
