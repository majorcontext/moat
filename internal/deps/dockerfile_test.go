// internal/deps/dockerfile_test.go
package deps

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/claude"
)

func TestGenerateDockerfile(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "typescript"},
		{Name: "protoc", Version: "25.1"},
		{Name: "psql"},
	}

	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// With single runtime (node), should use official node image as base
	if !strings.HasPrefix(dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should start with FROM node:20-slim, got:\n%s", dockerfile[:100])
	}

	// Check apt packages are batched
	if !strings.Contains(dockerfile, "apt-get install") {
		t.Error("Dockerfile should have apt-get install")
	}
	if !strings.Contains(dockerfile, "postgresql-client") {
		t.Error("Dockerfile should install postgresql-client")
	}

	// Node should be provided by base image, not installed
	if strings.Contains(dockerfile, "nodesource") {
		t.Error("Dockerfile should NOT install Node.js when using node base image")
	}
	if !strings.Contains(dockerfile, "provided by base image") {
		t.Error("Dockerfile should note that node is provided by base image")
	}

	// Check npm globals
	if !strings.Contains(dockerfile, "npm install -g typescript") {
		t.Error("Dockerfile should install typescript via npm")
	}

	// Check protoc
	if !strings.Contains(dockerfile, "protoc") || !strings.Contains(dockerfile, "25.1") {
		t.Error("Dockerfile should install protoc 25.1")
	}
}

func TestGenerateDockerfileEmpty(t *testing.T) {
	dockerfile, err := GenerateDockerfile(nil, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if !strings.HasPrefix(dockerfile, "FROM debian:bookworm-slim") {
		t.Error("Empty deps should still have base image")
	}
}

func TestGenerateDockerfileHasIptables(t *testing.T) {
	// Verify iptables is installed in base packages for firewall support
	deps := []Dependency{
		{Name: "python", Version: "3.11"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if !strings.Contains(dockerfile, "iptables") {
		t.Errorf("Dockerfile should install iptables for firewall support.\nGenerated Dockerfile:\n%s", dockerfile)
	}
}

func TestGenerateDockerfilePlaywright(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "playwright"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	// Should use node as base
	if !strings.HasPrefix(dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should use node:20-slim as base, got:\n%s", dockerfile[:100])
	}
	if !strings.Contains(dockerfile, "npm install -g playwright") {
		t.Error("Dockerfile should install playwright")
	}
	if !strings.Contains(dockerfile, "npx playwright install") {
		t.Error("Dockerfile should install playwright browsers")
	}
}

func TestGenerateDockerfileGo(t *testing.T) {
	deps := []Dependency{{Name: "go", Version: "1.22"}}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	// Should use official golang image as base
	if !strings.HasPrefix(dockerfile, "FROM golang:1.22") {
		t.Errorf("Dockerfile should start with FROM golang:1.22, got:\n%s", dockerfile[:100])
	}
	// Go should be provided by base image, not installed
	if strings.Contains(dockerfile, "go.dev/dl") {
		t.Error("Dockerfile should NOT install Go when using golang base image")
	}
	if !strings.Contains(dockerfile, "provided by base image") {
		t.Error("Dockerfile should note that go is provided by base image")
	}
}

func TestGenerateDockerfilePython(t *testing.T) {
	deps := []Dependency{{Name: "python", Version: "3.10"}}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	// Should use official python image as base
	if !strings.HasPrefix(dockerfile, "FROM python:3.10-slim") {
		t.Errorf("Dockerfile should start with FROM python:3.10-slim, got:\n%s", dockerfile[:100])
	}
	// Python should be provided by base image, not installed
	if strings.Contains(dockerfile, "apt-get install -y python3") {
		t.Error("Dockerfile should NOT install python3 when using python base image")
	}
	if !strings.Contains(dockerfile, "provided by base image") {
		t.Error("Dockerfile should note that python is provided by base image")
	}
}

func TestGenerateDockerfileWithSSH(t *testing.T) {
	// Test with NeedsSSH option (triggered by SSH grants)
	dockerfile, err := GenerateDockerfile(nil, &DockerfileOptions{NeedsSSH: true})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Check that openssh-client and socat are installed
	if !strings.Contains(dockerfile, "openssh-client") {
		t.Error("Dockerfile should install openssh-client")
	}
	if !strings.Contains(dockerfile, "socat") {
		t.Error("Dockerfile should install socat")
	}

	// Check that the entrypoint script is created (via base64)
	if !strings.Contains(dockerfile, "moat-init") {
		t.Error("Dockerfile should create moat-init entrypoint")
	}
	if !strings.Contains(dockerfile, "base64 -d") {
		t.Error("Dockerfile should use base64 decoding for entrypoint script")
	}
	if !strings.Contains(dockerfile, "ENTRYPOINT") {
		t.Error("Dockerfile should set ENTRYPOINT to moat-init")
	}
}

func TestGenerateDockerfileWithDepsAndSSH(t *testing.T) {
	// Test combining regular deps with SSH
	deps := []Dependency{{Name: "node", Version: "20"}}
	dockerfile, err := GenerateDockerfile(deps, &DockerfileOptions{NeedsSSH: true})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Node should be provided by base image
	if !strings.HasPrefix(dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should use node:20-slim as base, got:\n%s", dockerfile[:100])
	}
	if !strings.Contains(dockerfile, "provided by base image") {
		t.Error("Dockerfile should note that node is provided by base image")
	}

	// Check SSH is also installed
	if !strings.Contains(dockerfile, "openssh-client") {
		t.Error("Dockerfile should install openssh-client")
	}
	if !strings.Contains(dockerfile, "ENTRYPOINT") {
		t.Error("Dockerfile should set ENTRYPOINT")
	}
}

func TestGenerateDockerfileMultipleRuntimes(t *testing.T) {
	// With multiple runtimes, should fall back to Debian and install both
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "python", Version: "3.10"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should use Debian as base when multiple runtimes
	if !strings.HasPrefix(dockerfile, "FROM debian:bookworm-slim") {
		t.Errorf("Dockerfile should use debian:bookworm-slim for multiple runtimes, got:\n%s", dockerfile[:100])
	}

	// Both runtimes should be installed
	if !strings.Contains(dockerfile, "nodesource") {
		t.Error("Dockerfile should install Node.js")
	}
	if !strings.Contains(dockerfile, "python3") {
		t.Error("Dockerfile should install Python")
	}
}

// Tests for dynamic package manager dependencies

func TestGenerateDockerfileDynamicNpm(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Type: TypeDynamicNpm, Package: "eslint", Name: "eslint"},
		{Type: TypeDynamicNpm, Package: "@anthropic-ai/claude-code", Name: "@anthropic-ai/claude-code", Version: "1.0.0"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic npm section
	if !strings.Contains(dockerfile, "npm packages (dynamic)") {
		t.Error("Dockerfile should have npm packages (dynamic) section")
	}

	// Should install packages
	if !strings.Contains(dockerfile, "npm install -g eslint") {
		t.Error("Dockerfile should install eslint")
	}
	if !strings.Contains(dockerfile, "npm install -g @anthropic-ai/claude-code@1.0.0") {
		t.Error("Dockerfile should install scoped package with version")
	}
}

func TestGenerateDockerfileDynamicPip(t *testing.T) {
	deps := []Dependency{
		{Name: "python", Version: "3.11"},
		{Type: TypeDynamicPip, Package: "pytest", Name: "pytest"},
		{Type: TypeDynamicPip, Package: "requests", Name: "requests", Version: "2.28.0"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic pip section
	if !strings.Contains(dockerfile, "pip packages (dynamic)") {
		t.Error("Dockerfile should have pip packages (dynamic) section")
	}

	// Should install packages with correct syntax
	if !strings.Contains(dockerfile, "pip install pytest") {
		t.Error("Dockerfile should install pytest")
	}
	if !strings.Contains(dockerfile, "pip install requests==2.28.0") {
		t.Error("Dockerfile should install requests with version specifier")
	}
}

func TestGenerateDockerfileDynamicUv(t *testing.T) {
	deps := []Dependency{
		{Name: "python", Version: "3.11"},
		{Name: "uv"},
		{Type: TypeDynamicUv, Package: "ruff", Name: "ruff"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic uv section
	if !strings.Contains(dockerfile, "uv packages (dynamic)") {
		t.Error("Dockerfile should have uv packages (dynamic) section")
	}

	// Should use uv tool install
	if !strings.Contains(dockerfile, "uv tool install ruff") {
		t.Error("Dockerfile should install ruff via uv tool")
	}
}

func TestGenerateDockerfileDynamicCargo(t *testing.T) {
	deps := []Dependency{
		{Name: "rust"},
		{Type: TypeDynamicCargo, Package: "ripgrep", Name: "ripgrep"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic cargo section
	if !strings.Contains(dockerfile, "cargo packages (dynamic)") {
		t.Error("Dockerfile should have cargo packages (dynamic) section")
	}

	// Should use cargo install
	if !strings.Contains(dockerfile, "cargo install ripgrep") {
		t.Error("Dockerfile should install ripgrep via cargo")
	}
}

func TestGenerateDockerfileDynamicGo(t *testing.T) {
	deps := []Dependency{
		{Name: "go", Version: "1.22"},
		{Type: TypeDynamicGo, Package: "golang.org/x/tools/gopls", Name: "gopls"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic go section
	if !strings.Contains(dockerfile, "go packages (dynamic)") {
		t.Error("Dockerfile should have go packages (dynamic) section")
	}

	// Should use go install with GOBIN
	if !strings.Contains(dockerfile, "GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@latest") {
		t.Error("Dockerfile should install gopls via go install with GOBIN")
	}
}

func TestGenerateDockerfileMixedDependencies(t *testing.T) {
	// Test a realistic mix of registry and dynamic deps
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "typescript"},
		{Name: "git"},
		{Name: "gh"},
		{Type: TypeDynamicNpm, Package: "eslint", Name: "eslint"},
		{Type: TypeDynamicNpm, Package: "prettier", Name: "prettier"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Check node base image
	if !strings.HasPrefix(dockerfile, "FROM node:20-slim") {
		t.Error("Dockerfile should use node:20-slim as base")
	}

	// Check registry npm packages are batched
	if !strings.Contains(dockerfile, "npm install -g typescript") {
		t.Error("Dockerfile should install typescript")
	}

	// Check apt packages
	if !strings.Contains(dockerfile, "git") {
		t.Error("Dockerfile should install git")
	}

	// Check github binary (gh)
	if !strings.Contains(dockerfile, "cli/cli/releases") {
		t.Error("Dockerfile should install gh from GitHub")
	}

	// Check dynamic npm packages are in separate section
	if !strings.Contains(dockerfile, "npm packages (dynamic)") {
		t.Error("Dockerfile should have dynamic npm section")
	}
}

func TestGenerateDockerfileUvToolPackages(t *testing.T) {
	// Test uv-tool type deps from registry (not dynamic)
	deps := []Dependency{
		{Name: "python", Version: "3.11"},
		{Name: "uv"},
		{Name: "ruff"},
		{Name: "black"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have uv tool packages section
	if !strings.Contains(dockerfile, "uv tool packages") {
		t.Error("Dockerfile should have uv tool packages section")
	}

	// Should install via uv tool install
	if !strings.Contains(dockerfile, "uv tool install ruff") {
		t.Error("Dockerfile should install ruff via uv tool")
	}
	if !strings.Contains(dockerfile, "uv tool install black") {
		t.Error("Dockerfile should install black via uv tool")
	}
}

func TestGenerateDockerfileGoInstallPackages(t *testing.T) {
	deps := []Dependency{
		{Name: "go", Version: "1.22"},
		{Name: "govulncheck"},
		{Name: "mockgen"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have go install packages section
	if !strings.Contains(dockerfile, "go install packages") {
		t.Error("Dockerfile should have go install packages section")
	}

	// Should use GOBIN for installation
	if !strings.Contains(dockerfile, "GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@latest") {
		t.Error("Dockerfile should install govulncheck with GOBIN")
	}
	if !strings.Contains(dockerfile, "GOBIN=/usr/local/bin go install go.uber.org/mock/mockgen@latest") {
		t.Error("Dockerfile should install mockgen with GOBIN")
	}
}

func TestGenerateDockerfileMultiArchBinary(t *testing.T) {
	// Test that multi-arch binaries include arch detection
	deps := []Dependency{
		{Name: "bun"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have architecture detection
	if !strings.Contains(dockerfile, "uname -m") {
		t.Error("Dockerfile should detect architecture")
	}
	if !strings.Contains(dockerfile, "x86_64") {
		t.Error("Dockerfile should have x86_64 condition")
	}
}

func TestGenerateDockerfileWithoutBuildKit(t *testing.T) {
	// Test that disabling BuildKit removes cache mounts
	deps := []Dependency{
		{Name: "git"},
		{Name: "curl"},
	}
	useBuildKit := false
	dockerfile, err := GenerateDockerfile(deps, &DockerfileOptions{
		UseBuildKit: &useBuildKit,
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should NOT have BuildKit-specific cache mounts
	if strings.Contains(dockerfile, "--mount=type=cache") {
		t.Error("Dockerfile should not contain --mount=type=cache when BuildKit is disabled")
	}

	// Should still have apt-get commands
	if !strings.Contains(dockerfile, "apt-get update") {
		t.Error("Dockerfile should have apt-get update")
	}
	if !strings.Contains(dockerfile, "apt-get install") {
		t.Error("Dockerfile should have apt-get install")
	}
}

func TestGenerateDockerfileWithBuildKit(t *testing.T) {
	// Test that enabling BuildKit includes cache mounts (default behavior)
	deps := []Dependency{
		{Name: "git"},
	}
	useBuildKit := true
	dockerfile, err := GenerateDockerfile(deps, &DockerfileOptions{
		UseBuildKit: &useBuildKit,
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have BuildKit-specific cache mounts
	if !strings.Contains(dockerfile, "--mount=type=cache") {
		t.Error("Dockerfile should contain --mount=type=cache when BuildKit is enabled")
	}
}

func TestGenerateDockerfileClaudeCodeNativeInstall(t *testing.T) {
	// claude-code should use the native installer as moatuser, not npm
	deps := []Dependency{
		{Name: "claude-code"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should NOT use npm
	if strings.Contains(dockerfile, "npm install") {
		t.Error("claude-code should not be installed via npm")
	}

	// Should use native installer
	if !strings.Contains(dockerfile, "curl -fsSL https://claude.ai/install.sh | bash") {
		t.Error("Dockerfile should use native Claude installer")
	}

	// Should run as moatuser
	lines := strings.Split(dockerfile, "\n")
	installerIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "claude.ai/install.sh") {
			installerIdx = i
			break
		}
	}
	if installerIdx < 0 {
		t.Fatal("installer line not found")
	}
	// Find the nearest USER directive before the installer
	foundUser := ""
	for i := installerIdx - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "USER ") {
			foundUser = strings.TrimSpace(lines[i])
			break
		}
	}
	if foundUser != "USER moatuser" {
		t.Errorf("claude-code installer should run as moatuser, got %q", foundUser)
	}

	// Should add PATH from install commands' EnvVars
	if !strings.Contains(dockerfile, `ENV PATH="$HOME/.local/bin:$PATH"`) {
		t.Error("Dockerfile should add installer's PATH to environment")
	}

	// Should switch back to root after user-space install
	foundRoot := false
	for i := installerIdx + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "USER root" {
			foundRoot = true
			break
		}
	}
	if !foundRoot {
		t.Error("Dockerfile should switch back to USER root after user-space install")
	}
}

func TestGenerateDockerfileMixedUserAndRootCustomDeps(t *testing.T) {
	// Verify that user-install and root custom deps coexist correctly.
	deps := []Dependency{
		{Name: "rust"},        // root custom dep
		{Name: "claude-code"}, // user-install custom dep
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Rust should be installed as root (no USER switch around it)
	if !strings.Contains(dockerfile, "rustup.rs") {
		t.Error("Dockerfile should install rust")
	}

	// Claude should be installed as moatuser
	if !strings.Contains(dockerfile, "claude.ai/install.sh") {
		t.Error("Dockerfile should install claude-code")
	}

	// Rust section should come before user-space section
	rustIdx := strings.Index(dockerfile, "rustup.rs")
	claudeIdx := strings.Index(dockerfile, "claude.ai/install.sh")
	if rustIdx > claudeIdx {
		t.Error("root custom deps should be written before user-space custom deps")
	}

	// The USER moatuser block should only surround claude, not rust
	lines := strings.Split(dockerfile, "\n")
	for i, line := range lines {
		if strings.Contains(line, "rustup.rs") {
			// Walk backwards to find nearest USER directive
			for j := i - 1; j >= 0; j-- {
				trimmed := strings.TrimSpace(lines[j])
				if strings.HasPrefix(trimmed, "USER ") {
					if trimmed != "USER root" {
						t.Errorf("rust should run as root, but nearest USER is %q", trimmed)
					}
					break
				}
			}
			break
		}
	}
}

func TestGenerateDockerfileWithClaudePlugins(t *testing.T) {
	// Test that Claude plugins are baked into the image
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "claude-code"},
	}

	marketplaces := []claude.MarketplaceConfig{
		{Name: "claude-plugins-official", Source: "github", Repo: "anthropics/claude-plugins-official"},
		{Name: "aws-agent-skills", Source: "github", Repo: "itsmostafa/aws-agent-skills"},
	}
	plugins := []string{
		"claude-md-management@claude-plugins-official",
		"aws-agent-skills@aws-agent-skills",
	}

	dockerfile, err := GenerateDockerfile(deps, &DockerfileOptions{
		ClaudeMarketplaces: marketplaces,
		ClaudePlugins:      plugins,
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have section header
	if !strings.Contains(dockerfile, "# Claude Code plugins") {
		t.Error("Dockerfile should have Claude Code plugins section")
	}

	// Should switch to moatuser for plugin installation
	if !strings.Contains(dockerfile, "USER moatuser") {
		t.Error("Dockerfile should switch to moatuser for plugin installation")
	}

	// Should add marketplaces with error handling (in sorted order)
	if !strings.Contains(dockerfile, "claude plugin marketplace add anthropics/claude-plugins-official && echo 'Added marketplace claude-plugins-official' || echo 'WARNING: Could not add marketplace claude-plugins-official") {
		t.Error("Dockerfile should add claude-plugins-official marketplace with error handling")
	}
	if !strings.Contains(dockerfile, "claude plugin marketplace add itsmostafa/aws-agent-skills && echo 'Added marketplace aws-agent-skills' || echo 'WARNING: Could not add marketplace aws-agent-skills") {
		t.Error("Dockerfile should add aws-agent-skills marketplace with error handling")
	}

	// Should install plugins with error handling (in sorted order)
	if !strings.Contains(dockerfile, "claude plugin install aws-agent-skills@aws-agent-skills && echo 'Installed plugin aws-agent-skills@aws-agent-skills' || echo 'WARNING: Could not install plugin aws-agent-skills@aws-agent-skills") {
		t.Error("Dockerfile should install aws-agent-skills plugin with error handling")
	}
	if !strings.Contains(dockerfile, "claude plugin install claude-md-management@claude-plugins-official && echo 'Installed plugin claude-md-management@claude-plugins-official' || echo 'WARNING: Could not install plugin claude-md-management@claude-plugins-official") {
		t.Error("Dockerfile should install claude-md-management plugin with error handling")
	}

	// Should switch back to root after plugin installation
	lines := strings.Split(dockerfile, "\n")
	foundUserMoatuser := false
	foundUserRoot := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "USER moatuser" {
			foundUserMoatuser = true
		}
		if foundUserMoatuser && strings.TrimSpace(line) == "USER root" {
			foundUserRoot = true
			break
		}
	}
	if !foundUserRoot {
		t.Error("Dockerfile should switch back to USER root after plugin installation")
	}
}

func TestGenerateDockerfileNoPlugins(t *testing.T) {
	// Verify no plugin section when no plugins configured
	deps := []Dependency{
		{Name: "node", Version: "20"},
	}

	dockerfile, err := GenerateDockerfile(deps, &DockerfileOptions{})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	if strings.Contains(dockerfile, "Claude Code plugins") {
		t.Error("Dockerfile should NOT have Claude plugins section when none configured")
	}
	if strings.Contains(dockerfile, "claude plugin") {
		t.Error("Dockerfile should NOT have claude plugin commands when none configured")
	}
}

func TestGenerateDockerfilePluginValidation(t *testing.T) {
	// Test that invalid plugin/marketplace names are rejected
	deps := []Dependency{{Name: "node", Version: "20"}}

	t.Run("invalid marketplace repo", func(t *testing.T) {
		marketplaces := []claude.MarketplaceConfig{
			{Name: "good", Source: "github", Repo: "valid/repo"},
			{Name: "evil", Source: "github", Repo: "; rm -rf /"},
		}
		dockerfile, err := GenerateDockerfile(deps, &DockerfileOptions{
			ClaudeMarketplaces: marketplaces,
		})
		if err != nil {
			t.Fatalf("GenerateDockerfile error: %v", err)
		}
		// Valid repo should be included
		if !strings.Contains(dockerfile, "marketplace add valid/repo") {
			t.Error("valid marketplace should be included")
		}
		// Invalid repo should trigger error message (note: uses marketplace name, not repo)
		if !strings.Contains(dockerfile, "Invalid marketplace repo format: evil") {
			t.Error("invalid marketplace should show error message with name")
		}
		// The malicious repo value should NOT appear in the dockerfile
		if strings.Contains(dockerfile, "; rm -rf /") {
			t.Error("invalid repo value should not appear in output")
		}
	})

	t.Run("invalid plugin key", func(t *testing.T) {
		plugins := []string{
			"valid-plugin@valid-market",
			"bad;rm -rf /@market",
		}
		dockerfile, err := GenerateDockerfile(deps, &DockerfileOptions{
			ClaudePlugins: plugins,
		})
		if err != nil {
			t.Fatalf("GenerateDockerfile error: %v", err)
		}
		// Valid plugin should be included
		if !strings.Contains(dockerfile, "plugin install valid-plugin@valid-market") {
			t.Error("valid plugin should be included")
		}
		// Invalid plugin should trigger error message
		if !strings.Contains(dockerfile, "Invalid plugin format") {
			t.Error("invalid plugin should show error message")
		}
	})
}

// Test helper functions

func TestCategorizeDeps(t *testing.T) {
	deps := []Dependency{
		{Name: "git"},                 // apt
		{Name: "node", Version: "20"}, // runtime
		{Name: "gh"},                  // github binary
		{Name: "typescript"},          // npm
		{Name: "go", Version: "1.22"}, // runtime
		{Name: "govulncheck"},         // go-install
		{Type: TypeDynamicNpm, Package: "eslint", Name: "eslint"}, // dynamic npm
		{Type: TypeDynamicPip, Package: "pytest", Name: "pytest"}, // dynamic pip
	}

	c := categorizeDeps(deps)

	if len(c.aptPkgs) != 1 || c.aptPkgs[0] != "git" {
		t.Errorf("apt packages: got %v, want [git]", c.aptPkgs)
	}
	if len(c.runtimes) != 2 {
		t.Errorf("runtimes: got %d, want 2", len(c.runtimes))
	}
	if len(c.githubBins) != 1 {
		t.Errorf("github binaries: got %d, want 1", len(c.githubBins))
	}
	if len(c.npmPkgs) != 1 {
		t.Errorf("npm packages: got %d, want 1", len(c.npmPkgs))
	}
	if len(c.goInstallPkgs) != 1 {
		t.Errorf("go install packages: got %d, want 1", len(c.goInstallPkgs))
	}
	if len(c.dynamicNpm) != 1 {
		t.Errorf("dynamic npm: got %d, want 1", len(c.dynamicNpm))
	}
	if len(c.dynamicPip) != 1 {
		t.Errorf("dynamic pip: got %d, want 1", len(c.dynamicPip))
	}
	if c.dockerMode != "" {
		t.Errorf("dockerMode: got %q, want empty string", c.dockerMode)
	}
}

func TestCategorizeDepsWithDockerNoMode(t *testing.T) {
	// Test that a docker dependency without mode doesn't crash categorizeDeps.
	// Note: Parser now requires explicit mode, but categorizeDeps should still
	// handle empty DockerMode gracefully (no dockerfile output for docker CLI).
	deps := []Dependency{
		{Name: "docker"}, // No mode set - would error in parser, but test internal behavior
		{Name: "git"},
	}

	c := categorizeDeps(deps)

	// Empty mode should not be defaulted - no docker CLI output
	if c.dockerMode != "" {
		t.Errorf("dockerMode: got %q, want empty string", c.dockerMode)
	}
	// docker should not be added to aptPkgs by categorizeDeps
	if len(c.aptPkgs) != 1 || c.aptPkgs[0] != "git" {
		t.Errorf("apt packages: got %v, want [git]", c.aptPkgs)
	}
}

func TestCategorizeDepsWithDockerDind(t *testing.T) {
	deps := []Dependency{
		{Name: "docker", DockerMode: DockerModeDind},
		{Name: "git"},
	}

	c := categorizeDeps(deps)

	if c.dockerMode != DockerModeDind {
		t.Errorf("dockerMode: got %q, want %q", c.dockerMode, DockerModeDind)
	}
	// docker should not be added to aptPkgs by categorizeDeps
	if len(c.aptPkgs) != 1 || c.aptPkgs[0] != "git" {
		t.Errorf("apt packages: got %v, want [git]", c.aptPkgs)
	}
}

func TestCategorizeDepsWithDockerHost(t *testing.T) {
	deps := []Dependency{
		{Name: "docker", DockerMode: DockerModeHost},
	}

	c := categorizeDeps(deps)

	if c.dockerMode != DockerModeHost {
		t.Errorf("dockerMode: got %q, want %q", c.dockerMode, DockerModeHost)
	}
}

func TestSelectBaseImage(t *testing.T) {
	tests := []struct {
		name     string
		runtimes []Dependency
		wantImg  string
		wantRT   bool // whether baseRuntime should be non-nil
	}{
		{"empty", nil, "debian:bookworm-slim", false},
		{"multiple", []Dependency{{Name: "node"}, {Name: "python"}}, "debian:bookworm-slim", false},
		{"node only", []Dependency{{Name: "node", Version: "20"}}, "node:20-slim", true},
		{"python only", []Dependency{{Name: "python", Version: "3.11"}}, "python:3.11-slim", true},
		{"go only", []Dependency{{Name: "go", Version: "1.22"}}, "golang:1.22", true},
		{"unknown runtime", []Dependency{{Name: "rust"}}, "debian:bookworm-slim", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img, rt := selectBaseImage(tt.runtimes)
			if img != tt.wantImg {
				t.Errorf("image = %q, want %q", img, tt.wantImg)
			}
			if (rt != nil) != tt.wantRT {
				t.Errorf("baseRuntime nil = %v, want %v", rt == nil, !tt.wantRT)
			}
		})
	}
}

func TestWriteSSHKnownHosts(t *testing.T) {
	tests := []struct {
		name      string
		hosts     []string
		wantEmpty bool
		wantHosts []string // hosts that should appear in output
	}{
		{
			name:      "empty hosts",
			hosts:     nil,
			wantEmpty: true,
		},
		{
			name:      "empty slice",
			hosts:     []string{},
			wantEmpty: true,
		},
		{
			name:      "unknown host",
			hosts:     []string{"unknown.example.com"},
			wantEmpty: true,
		},
		{
			name:      "github only",
			hosts:     []string{"github.com"},
			wantEmpty: false,
			wantHosts: []string{"github.com ssh-ed25519", "github.com ecdsa-sha2", "github.com ssh-rsa"},
		},
		{
			name:      "gitlab only",
			hosts:     []string{"gitlab.com"},
			wantEmpty: false,
			wantHosts: []string{"gitlab.com ssh-ed25519", "gitlab.com ecdsa-sha2", "gitlab.com ssh-rsa"},
		},
		{
			name:      "multiple hosts",
			hosts:     []string{"github.com", "gitlab.com"},
			wantEmpty: false,
			wantHosts: []string{"github.com ssh-ed25519", "gitlab.com ssh-ed25519"},
		},
		{
			name:      "mixed known and unknown",
			hosts:     []string{"github.com", "unknown.example.com"},
			wantEmpty: false,
			wantHosts: []string{"github.com ssh-ed25519"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeSSHKnownHosts(&b, tt.hosts)
			output := b.String()

			if tt.wantEmpty {
				if output != "" {
					t.Errorf("expected empty output, got:\n%s", output)
				}
				return
			}

			// Should have mkdir and echo commands
			if !strings.Contains(output, "mkdir -p /etc/ssh") {
				t.Error("missing mkdir command")
			}
			if !strings.Contains(output, "/etc/ssh/ssh_known_hosts") {
				t.Error("missing known_hosts path")
			}

			// Check expected hosts appear
			for _, host := range tt.wantHosts {
				if !strings.Contains(output, host) {
					t.Errorf("missing expected host key: %s", host)
				}
			}
		})
	}
}

func TestGenerateDockerfileWithSSHHosts(t *testing.T) {
	// Test that SSHHosts option adds known_hosts to the Dockerfile
	opts := &DockerfileOptions{
		NeedsSSH: true,
		SSHHosts: []string{"github.com"},
	}

	dockerfile, err := GenerateDockerfile(nil, opts)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have SSH packages
	if !strings.Contains(dockerfile, "openssh-client") {
		t.Error("missing openssh-client")
	}

	// Should have known_hosts for github.com
	if !strings.Contains(dockerfile, "github.com ssh-ed25519") {
		t.Error("missing github.com ssh-ed25519 key")
	}
	if !strings.Contains(dockerfile, "/etc/ssh/ssh_known_hosts") {
		t.Error("missing known_hosts path")
	}
}

func TestGenerateDockerfileSSHHostsWithoutSSH(t *testing.T) {
	// SSHHosts without NeedsSSH should still work (hosts are added regardless)
	opts := &DockerfileOptions{
		NeedsSSH: false,
		SSHHosts: []string{"github.com"},
	}

	dockerfile, err := GenerateDockerfile(nil, opts)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have known_hosts even without SSH packages
	if !strings.Contains(dockerfile, "github.com ssh-ed25519") {
		t.Error("missing github.com ssh-ed25519 key")
	}
}

func TestGenerateDockerfileWithDockerHost(t *testing.T) {
	// Test that docker:host installs docker-ce-cli from official repo
	deps := []Dependency{
		{Name: "docker", DockerMode: DockerModeHost},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should install docker-ce-cli from Docker's official repo
	if !strings.Contains(dockerfile, "docker-ce-cli") {
		t.Error("Dockerfile should install docker-ce-cli package")
	}
	if !strings.Contains(dockerfile, "download.docker.com") {
		t.Error("Dockerfile should use Docker's official repo")
	}
}

func TestGenerateDockerfileDockerWithOtherDeps(t *testing.T) {
	// Test docker dependency combined with other dependencies
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "docker", DockerMode: DockerModeHost},
		{Name: "git"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should use node as base image
	if !strings.HasPrefix(dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should use node:20-slim as base, got:\n%s", dockerfile[:100])
	}

	// Should install docker-ce-cli from Docker's official repo
	if !strings.Contains(dockerfile, "docker-ce-cli") {
		t.Error("Dockerfile should install docker-ce-cli package")
	}

	// Should also install git via apt
	if !strings.Contains(dockerfile, "git") {
		t.Error("Dockerfile should install git package")
	}
}

func TestGenerateDockerfileDockerDind(t *testing.T) {
	// Test docker:dind dependency installs full Docker daemon
	deps := []Dependency{
		{Name: "docker", DockerMode: DockerModeDind},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dind mode comment
	if !strings.Contains(dockerfile, "dind mode") {
		t.Error("Dockerfile should mention dind mode in comment")
	}

	// Should install docker-ce (full daemon)
	if !strings.Contains(dockerfile, "docker-ce ") || !strings.Contains(dockerfile, "docker-ce-cli") {
		t.Error("Dockerfile should install docker-ce and docker-ce-cli packages")
	}

	// Should install containerd.io
	if !strings.Contains(dockerfile, "containerd.io") {
		t.Error("Dockerfile should install containerd.io package")
	}

	// Should use Docker's official repo
	if !strings.Contains(dockerfile, "download.docker.com") {
		t.Error("Dockerfile should use Docker's official repo")
	}
}

func TestGenerateDockerfileDockerHost(t *testing.T) {
	// Test docker:host dependency installs CLI only
	deps := []Dependency{
		{Name: "docker", DockerMode: DockerModeHost},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should NOT have dind mode comment
	if strings.Contains(dockerfile, "dind mode") {
		t.Error("Dockerfile should not mention dind mode for host mode")
	}

	// Should install docker-ce-cli only (not docker-ce daemon)
	if !strings.Contains(dockerfile, "docker-ce-cli") {
		t.Error("Dockerfile should install docker-ce-cli package")
	}

	// Should NOT install containerd.io (not needed for CLI only)
	if strings.Contains(dockerfile, "containerd.io") {
		t.Error("Dockerfile should not install containerd.io for host mode")
	}
}

func TestGenerateDockerfileDindWithOtherDeps(t *testing.T) {
	// Test docker:dind combined with other dependencies
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "docker", DockerMode: DockerModeDind},
		{Name: "git"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should use node as base image
	if !strings.HasPrefix(dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should use node:20-slim as base, got:\n%s", dockerfile[:100])
	}

	// Should install full docker suite for dind
	if !strings.Contains(dockerfile, "docker-ce ") {
		t.Error("Dockerfile should install docker-ce package")
	}
	if !strings.Contains(dockerfile, "containerd.io") {
		t.Error("Dockerfile should install containerd.io package")
	}

	// Should also install git via apt
	if !strings.Contains(dockerfile, "git") {
		t.Error("Dockerfile should install git package")
	}
}

func TestKnownSSHHostKeysComplete(t *testing.T) {
	// Verify that all expected hosts have keys defined
	expectedHosts := []string{"github.com", "gitlab.com", "bitbucket.org"}

	for _, host := range expectedHosts {
		keys, ok := knownSSHHostKeys[host]
		if !ok {
			t.Errorf("missing keys for %s", host)
			continue
		}
		if len(keys) == 0 {
			t.Errorf("empty keys for %s", host)
		}

		// Each host should have at least ed25519 and ecdsa keys
		hasEd25519 := false
		hasEcdsa := false
		for _, key := range keys {
			if strings.Contains(key, "ssh-ed25519") {
				hasEd25519 = true
			}
			if strings.Contains(key, "ecdsa-sha2") {
				hasEcdsa = true
			}
		}
		if !hasEd25519 {
			t.Errorf("%s missing ssh-ed25519 key", host)
		}
		if !hasEcdsa {
			t.Errorf("%s missing ecdsa key", host)
		}
	}
}
