// internal/deps/dockerfile_test.go
package deps

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/providers/claude"
)

func TestGenerateDockerfile(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "typescript"},
		{Name: "protoc", Version: "25.1"},
		{Name: "psql"},
	}

	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// With single runtime (node), should use official node image as base
	if !strings.HasPrefix(result.Dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should start with FROM node:20-slim, got:\n%s", result.Dockerfile[:100])
	}

	// Check apt packages are batched
	if !strings.Contains(result.Dockerfile, "apt-get install") {
		t.Error("Dockerfile should have apt-get install")
	}
	if !strings.Contains(result.Dockerfile, "postgresql-client") {
		t.Error("Dockerfile should install postgresql-client")
	}

	// Node should be provided by base image, not installed
	if strings.Contains(result.Dockerfile, "nodesource") {
		t.Error("Dockerfile should NOT install Node.js when using node base image")
	}
	if !strings.Contains(result.Dockerfile, "provided by base image") {
		t.Error("Dockerfile should note that node is provided by base image")
	}

	// Check npm globals
	if !strings.Contains(result.Dockerfile, "npm install -g typescript") {
		t.Error("Dockerfile should install typescript via npm")
	}

	// Check protoc
	if !strings.Contains(result.Dockerfile, "protoc") || !strings.Contains(result.Dockerfile, "25.1") {
		t.Error("Dockerfile should install protoc 25.1")
	}
}

func TestGenerateDockerfileEmpty(t *testing.T) {
	result, err := GenerateDockerfile(nil, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if !strings.HasPrefix(result.Dockerfile, "FROM debian:bookworm-slim") {
		t.Error("Empty deps should still have base image")
	}
}

func TestGenerateDockerfileHasIptables(t *testing.T) {
	// Verify iptables is installed when NeedsFirewall is true
	deps := []Dependency{
		{Name: "python", Version: "3.11"},
	}
	result, err := GenerateDockerfile(deps, &DockerfileOptions{NeedsFirewall: true})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if !strings.Contains(result.Dockerfile, "iptables") {
		t.Errorf("Dockerfile should install iptables when NeedsFirewall is true.\nGenerated Dockerfile:\n%s", result.Dockerfile)
	}
}

func TestGenerateDockerfileNoIptablesWithoutFirewall(t *testing.T) {
	// Verify iptables is NOT installed when NeedsFirewall is false (default)
	deps := []Dependency{
		{Name: "python", Version: "3.11"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if strings.Contains(result.Dockerfile, "iptables") {
		t.Errorf("Dockerfile should NOT install iptables when NeedsFirewall is false.\nGenerated Dockerfile:\n%s", result.Dockerfile)
	}
}

func TestGenerateDockerfileMergedAptPackages(t *testing.T) {
	// Verify base and user apt packages are merged into a single layer
	deps := []Dependency{
		{Name: "git"},
		{Name: "psql"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	// Should have exactly one apt-get update
	count := strings.Count(result.Dockerfile, "apt-get update")
	if count != 1 {
		t.Errorf("Dockerfile should have exactly 1 apt-get update, got %d.\nGenerated Dockerfile:\n%s", count, result.Dockerfile)
	}
	// Base packages and user packages should be in the same section
	if !strings.Contains(result.Dockerfile, "ca-certificates") {
		t.Error("Dockerfile should include base package ca-certificates")
	}
	if !strings.Contains(result.Dockerfile, "git") {
		t.Error("Dockerfile should include user package git")
	}
	if !strings.Contains(result.Dockerfile, "postgresql-client") {
		t.Error("Dockerfile should include user package postgresql-client")
	}
}

func TestGenerateDockerfileBuildKitNoAptCleanup(t *testing.T) {
	// Verify rm -rf /var/lib/apt/lists/* is NOT present when BuildKit is enabled
	useBuildKit := true
	result, err := GenerateDockerfile(nil, &DockerfileOptions{UseBuildKit: &useBuildKit})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if strings.Contains(result.Dockerfile, "rm -rf /var/lib/apt/lists") {
		t.Error("Dockerfile should NOT have apt lists cleanup when BuildKit is enabled")
	}
}

func TestGenerateDockerfileNoBuildKitHasAptCleanup(t *testing.T) {
	// Verify rm -rf /var/lib/apt/lists/* IS present when BuildKit is disabled
	useBuildKit := false
	result, err := GenerateDockerfile(nil, &DockerfileOptions{UseBuildKit: &useBuildKit})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if !strings.Contains(result.Dockerfile, "rm -rf /var/lib/apt/lists") {
		t.Error("Dockerfile should have apt lists cleanup when BuildKit is disabled")
	}
}

func TestGenerateDockerfilePlaywright(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "playwright"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	// Should use node as base
	if !strings.HasPrefix(result.Dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should use node:20-slim as base, got:\n%s", result.Dockerfile[:100])
	}
	if !strings.Contains(result.Dockerfile, "npm install -g playwright") {
		t.Error("Dockerfile should install playwright")
	}
	if !strings.Contains(result.Dockerfile, "npx playwright install") {
		t.Error("Dockerfile should install playwright browsers")
	}
}

func TestGenerateDockerfileGo(t *testing.T) {
	deps := []Dependency{{Name: "go", Version: "1.22"}}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	// Should use official golang image as base
	if !strings.HasPrefix(result.Dockerfile, "FROM golang:1.22") {
		t.Errorf("Dockerfile should start with FROM golang:1.22, got:\n%s", result.Dockerfile[:100])
	}
	// Go should be provided by base image, not installed
	if strings.Contains(result.Dockerfile, "go.dev/dl") {
		t.Error("Dockerfile should NOT install Go when using golang base image")
	}
	if !strings.Contains(result.Dockerfile, "provided by base image") {
		t.Error("Dockerfile should note that go is provided by base image")
	}
}

func TestGenerateDockerfilePython(t *testing.T) {
	deps := []Dependency{{Name: "python", Version: "3.10"}}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	// Should use official python image as base
	if !strings.HasPrefix(result.Dockerfile, "FROM python:3.10-slim") {
		t.Errorf("Dockerfile should start with FROM python:3.10-slim, got:\n%s", result.Dockerfile[:100])
	}
	// Python should be provided by base image, not installed
	if strings.Contains(result.Dockerfile, "apt-get install -y python3") {
		t.Error("Dockerfile should NOT install python3 when using python base image")
	}
	if !strings.Contains(result.Dockerfile, "provided by base image") {
		t.Error("Dockerfile should note that python is provided by base image")
	}
}

func TestGenerateDockerfileWithSSH(t *testing.T) {
	// Test with NeedsSSH option (triggered by SSH grants)
	result, err := GenerateDockerfile(nil, &DockerfileOptions{NeedsSSH: true})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Check that openssh-client and socat are installed
	if !strings.Contains(result.Dockerfile, "openssh-client") {
		t.Error("Dockerfile should install openssh-client")
	}
	if !strings.Contains(result.Dockerfile, "socat") {
		t.Error("Dockerfile should install socat")
	}

	// Check that the entrypoint script is COPYed (not inline base64)
	if !strings.Contains(result.Dockerfile, "COPY moat-init.sh /usr/local/bin/moat-init") {
		t.Error("Dockerfile should COPY moat-init script")
	}
	if !strings.Contains(result.Dockerfile, "ENTRYPOINT") {
		t.Error("Dockerfile should set ENTRYPOINT to moat-init")
	}

	// Check that context files include the init script
	if _, ok := result.ContextFiles["moat-init.sh"]; !ok {
		t.Error("ContextFiles should include moat-init.sh")
	}
}

func TestGenerateDockerfileContextFiles(t *testing.T) {
	// Verify that all init-triggering options produce context files with non-empty content
	tests := []struct {
		name string
		opts *DockerfileOptions
		deps []Dependency
	}{
		{"SSH", &DockerfileOptions{NeedsSSH: true}, nil},
		{"ClaudeInit", &DockerfileOptions{NeedsClaudeInit: true}, nil},
		{"CodexInit", &DockerfileOptions{NeedsCodexInit: true}, nil},
		{"DockerHost", nil, []Dependency{{Name: "docker", DockerMode: DockerModeHost}}},
		{"DockerDind", nil, []Dependency{{Name: "docker", DockerMode: DockerModeDind}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateDockerfile(tt.deps, tt.opts)
			if err != nil {
				t.Fatalf("GenerateDockerfile error: %v", err)
			}

			content, ok := result.ContextFiles["moat-init.sh"]
			if !ok {
				t.Fatal("ContextFiles should include moat-init.sh")
			}
			if len(content) == 0 {
				t.Error("moat-init.sh content should not be empty")
			}
			if !strings.Contains(result.Dockerfile, "COPY moat-init.sh /usr/local/bin/moat-init") {
				t.Error("Dockerfile should COPY moat-init.sh")
			}
		})
	}

	// No init script when none of the triggers are active
	t.Run("NoInit", func(t *testing.T) {
		result, err := GenerateDockerfile(nil, nil)
		if err != nil {
			t.Fatalf("GenerateDockerfile error: %v", err)
		}
		if _, ok := result.ContextFiles["moat-init.sh"]; ok {
			t.Error("ContextFiles should not include moat-init.sh when no init is needed")
		}
		if strings.Contains(result.Dockerfile, "COPY moat-init.sh") {
			t.Error("Dockerfile should not COPY moat-init.sh when no init is needed")
		}
	})
}

func TestGenerateDockerfileWithDepsAndSSH(t *testing.T) {
	// Test combining regular deps with SSH
	deps := []Dependency{{Name: "node", Version: "20"}}
	result, err := GenerateDockerfile(deps, &DockerfileOptions{NeedsSSH: true})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Node should be provided by base image
	if !strings.HasPrefix(result.Dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should use node:20-slim as base, got:\n%s", result.Dockerfile[:100])
	}
	if !strings.Contains(result.Dockerfile, "provided by base image") {
		t.Error("Dockerfile should note that node is provided by base image")
	}

	// Check SSH is also installed
	if !strings.Contains(result.Dockerfile, "openssh-client") {
		t.Error("Dockerfile should install openssh-client")
	}
	if !strings.Contains(result.Dockerfile, "ENTRYPOINT") {
		t.Error("Dockerfile should set ENTRYPOINT")
	}
}

func TestGenerateDockerfileMultipleRuntimes(t *testing.T) {
	// With multiple runtimes, should fall back to Debian and install both
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "python", Version: "3.10"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should use Debian as base when multiple runtimes
	if !strings.HasPrefix(result.Dockerfile, "FROM debian:bookworm-slim") {
		t.Errorf("Dockerfile should use debian:bookworm-slim for multiple runtimes, got:\n%s", result.Dockerfile[:100])
	}

	// Both runtimes should be installed
	if !strings.Contains(result.Dockerfile, "nodesource") {
		t.Error("Dockerfile should install Node.js")
	}
	if !strings.Contains(result.Dockerfile, "python3") {
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
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic npm section
	if !strings.Contains(result.Dockerfile, "npm packages (dynamic)") {
		t.Error("Dockerfile should have npm packages (dynamic) section")
	}

	// Should install packages
	if !strings.Contains(result.Dockerfile, "npm install -g eslint") {
		t.Error("Dockerfile should install eslint")
	}
	if !strings.Contains(result.Dockerfile, "npm install -g @anthropic-ai/claude-code@1.0.0") {
		t.Error("Dockerfile should install scoped package with version")
	}
}

func TestGenerateDockerfileDynamicPip(t *testing.T) {
	deps := []Dependency{
		{Name: "python", Version: "3.11"},
		{Type: TypeDynamicPip, Package: "pytest", Name: "pytest"},
		{Type: TypeDynamicPip, Package: "requests", Name: "requests", Version: "2.28.0"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic pip section
	if !strings.Contains(result.Dockerfile, "pip packages (dynamic)") {
		t.Error("Dockerfile should have pip packages (dynamic) section")
	}

	// Should install packages with correct syntax
	if !strings.Contains(result.Dockerfile, "pip install pytest") {
		t.Error("Dockerfile should install pytest")
	}
	if !strings.Contains(result.Dockerfile, "pip install requests==2.28.0") {
		t.Error("Dockerfile should install requests with version specifier")
	}
}

func TestGenerateDockerfileDynamicUv(t *testing.T) {
	deps := []Dependency{
		{Name: "python", Version: "3.11"},
		{Name: "uv"},
		{Type: TypeDynamicUv, Package: "ruff", Name: "ruff"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic uv section
	if !strings.Contains(result.Dockerfile, "uv packages (dynamic)") {
		t.Error("Dockerfile should have uv packages (dynamic) section")
	}

	// Should use uv tool install
	if !strings.Contains(result.Dockerfile, "uv tool install ruff") {
		t.Error("Dockerfile should install ruff via uv tool")
	}
}

func TestGenerateDockerfileDynamicCargo(t *testing.T) {
	deps := []Dependency{
		{Name: "rust"},
		{Type: TypeDynamicCargo, Package: "ripgrep", Name: "ripgrep"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic cargo section
	if !strings.Contains(result.Dockerfile, "cargo packages (dynamic)") {
		t.Error("Dockerfile should have cargo packages (dynamic) section")
	}

	// Should use cargo install
	if !strings.Contains(result.Dockerfile, "cargo install ripgrep") {
		t.Error("Dockerfile should install ripgrep via cargo")
	}
}

func TestGenerateDockerfileDynamicGo(t *testing.T) {
	deps := []Dependency{
		{Name: "go", Version: "1.22"},
		{Type: TypeDynamicGo, Package: "golang.org/x/tools/gopls", Name: "gopls"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dynamic go section
	if !strings.Contains(result.Dockerfile, "go packages (dynamic)") {
		t.Error("Dockerfile should have go packages (dynamic) section")
	}

	// Should use go install with GOBIN
	if !strings.Contains(result.Dockerfile, "GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@latest") {
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
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Check node base image
	if !strings.HasPrefix(result.Dockerfile, "FROM node:20-slim") {
		t.Error("Dockerfile should use node:20-slim as base")
	}

	// Check registry npm packages are batched
	if !strings.Contains(result.Dockerfile, "npm install -g typescript") {
		t.Error("Dockerfile should install typescript")
	}

	// Check apt packages
	if !strings.Contains(result.Dockerfile, "git") {
		t.Error("Dockerfile should install git")
	}

	// Check github binary (gh)
	if !strings.Contains(result.Dockerfile, "cli/cli/releases") {
		t.Error("Dockerfile should install gh from GitHub")
	}

	// Check dynamic npm packages are in separate section
	if !strings.Contains(result.Dockerfile, "npm packages (dynamic)") {
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
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have uv tool packages section
	if !strings.Contains(result.Dockerfile, "uv tool packages") {
		t.Error("Dockerfile should have uv tool packages section")
	}

	// Should install via uv tool install
	if !strings.Contains(result.Dockerfile, "uv tool install ruff") {
		t.Error("Dockerfile should install ruff via uv tool")
	}
	if !strings.Contains(result.Dockerfile, "uv tool install black") {
		t.Error("Dockerfile should install black via uv tool")
	}
}

func TestGenerateDockerfileGoInstallPackages(t *testing.T) {
	deps := []Dependency{
		{Name: "go", Version: "1.22"},
		{Name: "govulncheck"},
		{Name: "mockgen"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have go install packages section
	if !strings.Contains(result.Dockerfile, "go install packages") {
		t.Error("Dockerfile should have go install packages section")
	}

	// Should use GOBIN for installation
	if !strings.Contains(result.Dockerfile, "GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@latest") {
		t.Error("Dockerfile should install govulncheck with GOBIN")
	}
	if !strings.Contains(result.Dockerfile, "GOBIN=/usr/local/bin go install go.uber.org/mock/mockgen@latest") {
		t.Error("Dockerfile should install mockgen with GOBIN")
	}
}

func TestGenerateDockerfileMultiArchBinary(t *testing.T) {
	// Test that multi-arch binaries include arch detection
	deps := []Dependency{
		{Name: "bun"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have architecture detection
	if !strings.Contains(result.Dockerfile, "uname -m") {
		t.Error("Dockerfile should detect architecture")
	}
	if !strings.Contains(result.Dockerfile, "x86_64") {
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
	result, err := GenerateDockerfile(deps, &DockerfileOptions{
		UseBuildKit: &useBuildKit,
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should NOT have BuildKit-specific cache mounts
	if strings.Contains(result.Dockerfile, "--mount=type=cache") {
		t.Error("Dockerfile should not contain --mount=type=cache when BuildKit is disabled")
	}

	// Should still have apt-get commands
	if !strings.Contains(result.Dockerfile, "apt-get update") {
		t.Error("Dockerfile should have apt-get update")
	}
	if !strings.Contains(result.Dockerfile, "apt-get install") {
		t.Error("Dockerfile should have apt-get install")
	}
}

func TestGenerateDockerfileWithBuildKit(t *testing.T) {
	// Test that enabling BuildKit includes cache mounts (default behavior)
	deps := []Dependency{
		{Name: "git"},
	}
	useBuildKit := true
	result, err := GenerateDockerfile(deps, &DockerfileOptions{
		UseBuildKit: &useBuildKit,
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have BuildKit-specific cache mounts
	if !strings.Contains(result.Dockerfile, "--mount=type=cache") {
		t.Error("Dockerfile should contain --mount=type=cache when BuildKit is enabled")
	}
}

func TestGenerateDockerfileClaudeCodeNativeInstall(t *testing.T) {
	// claude-code should use the native installer as moatuser, not npm
	deps := []Dependency{
		{Name: "claude-code"},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should NOT use npm
	if strings.Contains(result.Dockerfile, "npm install") {
		t.Error("claude-code should not be installed via npm")
	}

	// Should use native installer
	if !strings.Contains(result.Dockerfile, "curl -fsSL https://claude.ai/install.sh | bash") {
		t.Error("Dockerfile should use native Claude installer")
	}

	// Should run as moatuser
	lines := strings.Split(result.Dockerfile, "\n")
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
	if !strings.Contains(result.Dockerfile, `ENV PATH="/home/moatuser/.local/bin:$PATH"`) {
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
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Rust should be installed as root (no USER switch around it)
	if !strings.Contains(result.Dockerfile, "rustup.rs") {
		t.Error("Dockerfile should install rust")
	}

	// Claude should be installed as moatuser
	if !strings.Contains(result.Dockerfile, "claude.ai/install.sh") {
		t.Error("Dockerfile should install claude-code")
	}

	// Rust section should come before user-space section
	rustIdx := strings.Index(result.Dockerfile, "rustup.rs")
	claudeIdx := strings.Index(result.Dockerfile, "claude.ai/install.sh")
	if rustIdx > claudeIdx {
		t.Error("root custom deps should be written before user-space custom deps")
	}

	// The USER moatuser block should only surround claude, not rust
	lines := strings.Split(result.Dockerfile, "\n")
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
	// Test that Claude plugins are baked into the image via a script context file
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

	result, err := GenerateDockerfile(deps, &DockerfileOptions{
		ClaudeMarketplaces: marketplaces,
		ClaudePlugins:      plugins,
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have section header
	if !strings.Contains(result.Dockerfile, "# Claude Code plugins") {
		t.Error("Dockerfile should have Claude Code plugins section")
	}

	// Should switch to moatuser for plugin installation
	if !strings.Contains(result.Dockerfile, "USER moatuser") {
		t.Error("Dockerfile should switch to moatuser for plugin installation")
	}

	// Should COPY and run the plugin install script
	if !strings.Contains(result.Dockerfile, "COPY --chown=moatuser claude-plugins.sh") {
		t.Error("Dockerfile should COPY plugin install script with correct ownership")
	}
	if !strings.Contains(result.Dockerfile, "RUN bash /tmp/claude-plugins.sh") {
		t.Error("Dockerfile should run plugin install script")
	}

	// Plugin script should be in context files
	scriptContent, ok := result.ContextFiles["claude-plugins.sh"]
	if !ok {
		t.Fatal("ContextFiles should include claude-plugins.sh")
	}
	script := string(scriptContent)

	// Script should add marketplaces with error handling (in sorted order)
	if !strings.Contains(script, "claude plugin marketplace add anthropics/claude-plugins-official && echo 'Added marketplace claude-plugins-official' || echo 'WARNING: Could not add marketplace claude-plugins-official") {
		t.Error("script should add claude-plugins-official marketplace with error handling")
	}
	if !strings.Contains(script, "claude plugin marketplace add itsmostafa/aws-agent-skills && echo 'Added marketplace aws-agent-skills' || echo 'WARNING: Could not add marketplace aws-agent-skills") {
		t.Error("script should add aws-agent-skills marketplace with error handling")
	}

	// Script should install plugins with error handling (in sorted order)
	if !strings.Contains(script, "claude plugin install aws-agent-skills@aws-agent-skills && echo 'Installed plugin aws-agent-skills@aws-agent-skills' || echo 'WARNING: Could not install plugin aws-agent-skills@aws-agent-skills") {
		t.Error("script should install aws-agent-skills plugin with error handling")
	}
	if !strings.Contains(script, "claude plugin install claude-md-management@claude-plugins-official && echo 'Installed plugin claude-md-management@claude-plugins-official' || echo 'WARNING: Could not install plugin claude-md-management@claude-plugins-official") {
		t.Error("script should install claude-md-management plugin with error handling")
	}

	// Should switch back to root after plugin installation
	lines := strings.Split(result.Dockerfile, "\n")
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

	result, err := GenerateDockerfile(deps, &DockerfileOptions{})
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	if strings.Contains(result.Dockerfile, "Claude Code plugins") {
		t.Error("Dockerfile should NOT have Claude plugins section when none configured")
	}
	if strings.Contains(result.Dockerfile, "claude plugin") {
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
		result, err := GenerateDockerfile(deps, &DockerfileOptions{
			ClaudeMarketplaces: marketplaces,
		})
		if err != nil {
			t.Fatalf("GenerateDockerfile error: %v", err)
		}
		script := string(result.ContextFiles["claude-plugins.sh"])
		// Valid repo should be included in script
		if !strings.Contains(script, "marketplace add valid/repo") {
			t.Error("valid marketplace should be included")
		}
		// Invalid repo should trigger error message (note: uses marketplace name, not repo)
		if !strings.Contains(script, "Invalid marketplace repo format: evil") {
			t.Error("invalid marketplace should show error message with name")
		}
		// The malicious repo value should NOT appear in the script
		if strings.Contains(script, "; rm -rf /") {
			t.Error("invalid repo value should not appear in output")
		}
	})

	t.Run("invalid plugin key", func(t *testing.T) {
		plugins := []string{
			"valid-plugin@valid-market",
			"bad;rm -rf /@market",
		}
		result, err := GenerateDockerfile(deps, &DockerfileOptions{
			ClaudePlugins: plugins,
		})
		if err != nil {
			t.Fatalf("GenerateDockerfile error: %v", err)
		}
		script := string(result.ContextFiles["claude-plugins.sh"])
		// Valid plugin should be included in script
		if !strings.Contains(script, "plugin install valid-plugin@valid-market") {
			t.Error("valid plugin should be included")
		}
		// Invalid plugin should trigger error message
		if !strings.Contains(script, "Invalid plugin format") {
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
	// handle empty DockerMode gracefully (no result.Dockerfile output for docker CLI).
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

	result, err := GenerateDockerfile(nil, opts)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have SSH packages
	if !strings.Contains(result.Dockerfile, "openssh-client") {
		t.Error("missing openssh-client")
	}

	// Should have known_hosts for github.com
	if !strings.Contains(result.Dockerfile, "github.com ssh-ed25519") {
		t.Error("missing github.com ssh-ed25519 key")
	}
	if !strings.Contains(result.Dockerfile, "/etc/ssh/ssh_known_hosts") {
		t.Error("missing known_hosts path")
	}
}

func TestGenerateDockerfileSSHHostsWithoutSSH(t *testing.T) {
	// SSHHosts without NeedsSSH should still work (hosts are added regardless)
	opts := &DockerfileOptions{
		NeedsSSH: false,
		SSHHosts: []string{"github.com"},
	}

	result, err := GenerateDockerfile(nil, opts)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have known_hosts even without SSH packages
	if !strings.Contains(result.Dockerfile, "github.com ssh-ed25519") {
		t.Error("missing github.com ssh-ed25519 key")
	}
}

func TestGenerateDockerfileWithDockerHost(t *testing.T) {
	// Test that docker:host installs docker-ce-cli from official repo
	deps := []Dependency{
		{Name: "docker", DockerMode: DockerModeHost},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should install docker-ce-cli from Docker's official repo
	if !strings.Contains(result.Dockerfile, "docker-ce-cli") {
		t.Error("Dockerfile should install docker-ce-cli package")
	}
	if !strings.Contains(result.Dockerfile, "download.docker.com") {
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
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should use node as base image
	if !strings.HasPrefix(result.Dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should use node:20-slim as base, got:\n%s", result.Dockerfile[:100])
	}

	// Should install docker-ce-cli from Docker's official repo
	if !strings.Contains(result.Dockerfile, "docker-ce-cli") {
		t.Error("Dockerfile should install docker-ce-cli package")
	}

	// Should also install git via apt
	if !strings.Contains(result.Dockerfile, "git") {
		t.Error("Dockerfile should install git package")
	}
}

func TestGenerateDockerfileDockerDind(t *testing.T) {
	// Test docker:dind dependency installs full Docker daemon
	deps := []Dependency{
		{Name: "docker", DockerMode: DockerModeDind},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should have dind mode comment
	if !strings.Contains(result.Dockerfile, "dind mode") {
		t.Error("Dockerfile should mention dind mode in comment")
	}

	// Should install docker-ce (full daemon)
	if !strings.Contains(result.Dockerfile, "docker-ce ") || !strings.Contains(result.Dockerfile, "docker-ce-cli") {
		t.Error("Dockerfile should install docker-ce and docker-ce-cli packages")
	}

	// Should install containerd.io
	if !strings.Contains(result.Dockerfile, "containerd.io") {
		t.Error("Dockerfile should install containerd.io package")
	}

	// Should use Docker's official repo
	if !strings.Contains(result.Dockerfile, "download.docker.com") {
		t.Error("Dockerfile should use Docker's official repo")
	}
}

func TestGenerateDockerfileDockerHost(t *testing.T) {
	// Test docker:host dependency installs CLI only
	deps := []Dependency{
		{Name: "docker", DockerMode: DockerModeHost},
	}
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should NOT have dind mode comment
	if strings.Contains(result.Dockerfile, "dind mode") {
		t.Error("Dockerfile should not mention dind mode for host mode")
	}

	// Should install docker-ce-cli only (not docker-ce daemon)
	if !strings.Contains(result.Dockerfile, "docker-ce-cli") {
		t.Error("Dockerfile should install docker-ce-cli package")
	}

	// Should NOT install containerd.io (not needed for CLI only)
	if strings.Contains(result.Dockerfile, "containerd.io") {
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
	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should use node as base image
	if !strings.HasPrefix(result.Dockerfile, "FROM node:20-slim") {
		t.Errorf("Dockerfile should use node:20-slim as base, got:\n%s", result.Dockerfile[:100])
	}

	// Should install full docker suite for dind
	if !strings.Contains(result.Dockerfile, "docker-ce ") {
		t.Error("Dockerfile should install docker-ce package")
	}
	if !strings.Contains(result.Dockerfile, "containerd.io") {
		t.Error("Dockerfile should install containerd.io package")
	}

	// Should also install git via apt
	if !strings.Contains(result.Dockerfile, "git") {
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

func TestGenerateDockerfile_HooksPostBuild(t *testing.T) {
	result, err := GenerateDockerfile(nil, &DockerfileOptions{
		Hooks: &HooksConfig{
			PostBuild: "git config --global core.autocrlf input",
		},
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	if !strings.Contains(result.Dockerfile, "# Build hook: post_build") {
		t.Error("Dockerfile should contain post_build comment")
	}
	if !strings.Contains(result.Dockerfile, "git config --global core.autocrlf input") {
		t.Error("Dockerfile should contain the post_build command")
	}
}

func TestGenerateDockerfile_HooksPostBuildRoot(t *testing.T) {
	result, err := GenerateDockerfile(nil, &DockerfileOptions{
		Hooks: &HooksConfig{
			PostBuildRoot: "apt-get install -y figlet",
		},
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	if !strings.Contains(result.Dockerfile, "# Build hook: post_build_root") {
		t.Error("Dockerfile should contain post_build_root comment")
	}
	if !strings.Contains(result.Dockerfile, "apt-get install -y figlet") {
		t.Error("Dockerfile should contain the post_build_root command")
	}
	if !strings.Contains(result.Dockerfile, "WORKDIR /workspace\nRUN apt-get") {
		t.Error("Dockerfile should set WORKDIR /workspace before post_build_root command")
	}
}

func TestGenerateDockerfile_HooksBothBuild(t *testing.T) {
	result, err := GenerateDockerfile(nil, &DockerfileOptions{
		Hooks: &HooksConfig{
			PostBuild:     "git config --global core.autocrlf input",
			PostBuildRoot: "apt-get install -y figlet",
		},
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	// post_build_root should appear before post_build (root runs first, then user)
	rootIdx := strings.Index(result.Dockerfile, "post_build_root")
	userIdx := strings.Index(result.Dockerfile, "# Build hook: post_build\n")
	if rootIdx == -1 || userIdx == -1 {
		t.Fatal("Both hooks should be present")
	}
	if rootIdx > userIdx {
		t.Error("post_build_root should appear before post_build in Dockerfile")
	}
}

func TestGenerateDockerfile_HooksNil(t *testing.T) {
	result, err := GenerateDockerfile(nil, &DockerfileOptions{})
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	if strings.Contains(result.Dockerfile, "Build hook") {
		t.Error("Dockerfile should not contain build hooks when none configured")
	}
}

func TestGenerateDockerfile_HooksWorkdir(t *testing.T) {
	// Verify that post_build runs in /workspace
	result, err := GenerateDockerfile(nil, &DockerfileOptions{
		Hooks: &HooksConfig{
			PostBuild: "echo hello",
		},
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	// Find the WORKDIR /workspace before the build hook command
	lines := strings.Split(result.Dockerfile, "\n")
	hookIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "echo hello") {
			hookIdx = i
			break
		}
	}
	if hookIdx < 0 {
		t.Fatal("hook command not found in Dockerfile")
	}
	// Walk backwards to find nearest WORKDIR
	foundWorkdir := ""
	for i := hookIdx - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "WORKDIR ") {
			foundWorkdir = strings.TrimSpace(lines[i])
			break
		}
	}
	if foundWorkdir != "WORKDIR /workspace" {
		t.Errorf("post_build hook should run in /workspace, but nearest WORKDIR is %q", foundWorkdir)
	}
}

func TestGenerateDockerfile_HooksPreRunTriggersInit(t *testing.T) {
	// pre_run should trigger moat-init entrypoint even without other init features
	result, err := GenerateDockerfile(nil, &DockerfileOptions{
		Hooks: &HooksConfig{
			PreRun: "npm install",
		},
	})
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	if !strings.Contains(result.Dockerfile, "moat-init") {
		t.Error("Dockerfile should include moat-init entrypoint when pre_run is set")
	}
}

func TestGenerateDockerfileYarnPnpmCorepack(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "typescript"},
		{Name: "prettier"},
		{Name: "eslint"},
		{Name: "yarn"},
		{Name: "pnpm"},
	}

	result, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Verify npm packages (typescript, prettier, eslint) are grouped
	if !strings.Contains(result.Dockerfile, "npm install -g typescript prettier eslint") {
		t.Error("npm packages should be grouped into single install command")
		t.Logf("Dockerfile:\n%s", result.Dockerfile)
	}

	// Verify yarn is installed via corepack, NOT npm
	if strings.Contains(result.Dockerfile, "npm install -g yarn") {
		t.Error("yarn should NOT be installed via npm (conflicts with corepack)")
	}
	if !strings.Contains(result.Dockerfile, "corepack enable") {
		t.Error("corepack should be enabled for yarn")
	}
	if !strings.Contains(result.Dockerfile, "corepack prepare yarn@stable") {
		t.Error("yarn should be installed via corepack")
	}

	// Verify pnpm is installed via corepack, NOT npm
	if strings.Contains(result.Dockerfile, "npm install -g pnpm") {
		t.Error("pnpm should NOT be installed via npm (conflicts with corepack)")
	}
	if !strings.Contains(result.Dockerfile, "corepack prepare pnpm@latest") {
		t.Error("pnpm should be installed via corepack")
	}
}
