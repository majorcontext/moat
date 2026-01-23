// internal/deps/dockerfile_test.go
package deps

import (
	"strings"
	"testing"
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
