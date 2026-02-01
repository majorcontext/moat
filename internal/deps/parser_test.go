package deps

import (
	"strings"
	"testing"
)

func TestParseDependency(t *testing.T) {
	tests := []struct {
		input   string
		name    string
		version string
		wantErr bool
	}{
		{"node", "node", "", false},
		{"node@20", "node", "20", false},
		{"node@20.11", "node", "20.11", false},
		{"protoc@25.1", "protoc", "25.1", false},
		{"golangci-lint@1.55.2", "golangci-lint", "1.55.2", false},
		{"", "", "", true},
		{"@20", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if dep.Name != tt.name {
				t.Errorf("Name = %q, want %q", dep.Name, tt.name)
			}
			if dep.Version != tt.version {
				t.Errorf("Version = %q, want %q", dep.Version, tt.version)
			}
		})
	}
}

func TestParseServiceType(t *testing.T) {
	dep, err := Parse("postgres@17")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if dep.Type != TypeService {
		t.Errorf("Type = %q, want %q", dep.Type, TypeService)
	}
	if dep.Name != "postgres" {
		t.Errorf("Name = %q, want %q", dep.Name, "postgres")
	}

	// Non-service dep should get its type from registry
	dep2, err := Parse("node@20")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if dep2.Type != TypeRuntime {
		t.Errorf("Type = %q, want %q", dep2.Type, TypeRuntime)
	}
}

func TestParseAll(t *testing.T) {
	deps, err := ParseAll([]string{"node@20", "protoc", "typescript"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	if len(deps) != 3 {
		t.Fatalf("len(deps) = %d, want 3", len(deps))
	}
}

func TestParseAllUnknown(t *testing.T) {
	_, err := ParseAll([]string{"node", "unknowndep"})
	if err == nil {
		t.Error("ParseAll should error for unknown dependency")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		deps    []string
		wantErr bool
		errMsg  string
	}{
		// Valid cases
		{[]string{"node"}, false, ""},
		{[]string{"node", "typescript"}, false, ""},
		{[]string{"node@20", "yarn", "pnpm"}, false, ""},

		// Missing requirement
		{[]string{"typescript"}, true, "requires node"},
		{[]string{"playwright"}, true, "requires node"},
		{[]string{"yarn", "pnpm"}, true, "requires node"},

		// Unknown dependency
		{[]string{"unknown"}, true, "unknown dependency"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.deps, ","), func(t *testing.T) {
			deps, parseErr := ParseAll(tt.deps)
			var err error
			if parseErr == nil {
				err = Validate(deps)
			} else {
				err = parseErr
			}
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate(%v) should error", tt.deps)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("Validate(%v) error: %v", tt.deps, err)
			}
		})
	}
}

func TestValidateSuggestion(t *testing.T) {
	_, err := ParseAll([]string{"nodejs"})
	if err == nil {
		t.Fatal("should error for nodejs")
	}
	if !strings.Contains(err.Error(), "node") {
		t.Errorf("error should suggest 'node', got: %v", err)
	}
}

func TestVersionValidation(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		errMsg  string
	}{
		// Valid versions
		{"node@20", false, ""},
		{"node@20.11", false, ""},
		{"go@1.22", false, ""},
		{"protoc@25.1", false, ""},
		{"python@3.11", false, ""},
		{"python@3_11", false, ""}, // underscore allowed

		// Invalid versions (shell injection attempts)
		{"node@20; rm -rf /", true, "invalid character"},
		{"node@$(whoami)", true, "invalid character"},
		{"node@`id`", true, "invalid character"},
		{"node@20|cat /etc/passwd", true, "invalid character"},
		{"node@20&echo pwned", true, "invalid character"},
		{"node@20\necho pwned", true, "invalid character"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("Parse(%q) error: %v", tt.input, err)
			}
		})
	}
}

func TestMetaDependencyExpansion(t *testing.T) {
	// go-extras is a meta dependency that expands to gofumpt, govulncheck, goreleaser, golangci-lint
	deps, err := ParseAll([]string{"go", "go-extras"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	// Should have go + 4 expanded deps = 5 total
	if len(deps) != 5 {
		t.Errorf("expected 5 deps after expansion, got %d: %v", len(deps), deps)
	}

	// Verify the expanded deps
	names := make(map[string]bool)
	for _, d := range deps {
		names[d.Name] = true
	}
	expected := []string{"go", "gofumpt", "govulncheck", "goreleaser", "golangci-lint"}
	for _, exp := range expected {
		if !names[exp] {
			t.Errorf("expected %q in expanded deps, got %v", exp, deps)
		}
	}

	// Validate should pass since go is included
	if err := Validate(deps); err != nil {
		t.Errorf("Validate error: %v", err)
	}
}

func TestMetaDependencyDeduplication(t *testing.T) {
	// Including both go-extras and gofumpt should not duplicate gofumpt
	deps, err := ParseAll([]string{"go", "gofumpt", "go-extras"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	// Should have go + gofumpt + govulncheck + goreleaser + golangci-lint = 5 total (no duplicate gofumpt)
	if len(deps) != 5 {
		t.Errorf("expected 5 deps (no duplicates), got %d: %v", len(deps), deps)
	}
}

func TestMetaDependencyVersionRejected(t *testing.T) {
	// Meta dependencies should not accept version specifiers
	_, err := ParseAll([]string{"go-extras@1.0"})
	if err == nil {
		t.Error("ParseAll should error for meta dependency with version")
	}
	if !strings.Contains(err.Error(), "does not support version") {
		t.Errorf("error should mention version not supported, got: %v", err)
	}
}

func TestGoInstallRequiresGoRuntime(t *testing.T) {
	// go-install dependencies require Go runtime
	deps, err := ParseAll([]string{"govulncheck"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	err = Validate(deps)
	if err == nil {
		t.Error("Validate should error for go-install without go runtime")
	}
	if !strings.Contains(err.Error(), "requires Go runtime") {
		t.Errorf("error should mention Go runtime requirement, got: %v", err)
	}

	// Should pass when go is included
	deps, err = ParseAll([]string{"go", "govulncheck"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	if err := Validate(deps); err != nil {
		t.Errorf("Validate should pass with go runtime: %v", err)
	}
}

func TestVersionConstraintValidation(t *testing.T) {
	// Node has versions ["18", "20", "22"] in registry
	tests := []struct {
		deps    []string
		wantErr bool
		errMsg  string
	}{
		// Valid versions
		{[]string{"node@20"}, false, ""},
		{[]string{"node@18"}, false, ""},
		{[]string{"node@22"}, false, ""},
		{[]string{"node"}, false, ""}, // No version = use default

		// Invalid version for node (not in allowed list)
		{[]string{"node@16"}, true, "invalid version"},
		{[]string{"node@21"}, true, "invalid version"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.deps, ","), func(t *testing.T) {
			deps, parseErr := ParseAll(tt.deps)
			if parseErr != nil {
				t.Fatalf("ParseAll error: %v", parseErr)
			}
			err := Validate(deps)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate(%v) should error", tt.deps)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("Validate(%v) error: %v", tt.deps, err)
			}
		})
	}
}

// Tests for dynamic package manager prefixes

func TestParseDynamicNpm(t *testing.T) {
	tests := []struct {
		input   string
		pkg     string
		version string
		wantErr bool
	}{
		// Simple packages
		{"npm:eslint", "eslint", "", false},
		{"npm:eslint@8.0.0", "eslint", "8.0.0", false},
		{"npm:prettier", "prettier", "", false},

		// Scoped packages
		{"npm:@anthropic-ai/claude-code", "@anthropic-ai/claude-code", "", false},
		{"npm:@anthropic-ai/claude-code@1.0.0", "@anthropic-ai/claude-code", "1.0.0", false},
		{"npm:@types/node", "@types/node", "", false},
		{"npm:@types/node@20.0.0", "@types/node", "20.0.0", false},

		// Invalid
		{"npm:", "", "", true},
		{"npm:@invalid", "", "", true}, // scoped without name
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if dep.Package != tt.pkg {
				t.Errorf("Package = %q, want %q", dep.Package, tt.pkg)
			}
			if dep.Version != tt.version {
				t.Errorf("Version = %q, want %q", dep.Version, tt.version)
			}
			if dep.Type != TypeDynamicNpm {
				t.Errorf("Type = %v, want TypeDynamicNpm", dep.Type)
			}
			if !dep.IsDynamic() {
				t.Error("IsDynamic() should be true")
			}
		})
	}
}

func TestParseDynamicPip(t *testing.T) {
	tests := []struct {
		input   string
		pkg     string
		version string
		wantErr bool
	}{
		{"pip:pytest", "pytest", "", false},
		{"pip:pytest@7.0.0", "pytest", "7.0.0", false},
		{"pip:requests", "requests", "", false},
		{"pip:", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if dep.Package != tt.pkg {
				t.Errorf("Package = %q, want %q", dep.Package, tt.pkg)
			}
			if dep.Type != TypeDynamicPip {
				t.Errorf("Type = %v, want TypeDynamicPip", dep.Type)
			}
		})
	}
}

func TestParseDynamicUv(t *testing.T) {
	tests := []struct {
		input   string
		pkg     string
		version string
		wantErr bool
	}{
		{"uv:ruff", "ruff", "", false},
		{"uv:ruff@0.1.0", "ruff", "0.1.0", false},
		{"uv:black", "black", "", false},
		{"uv:", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if dep.Package != tt.pkg {
				t.Errorf("Package = %q, want %q", dep.Package, tt.pkg)
			}
			if dep.Type != TypeDynamicUv {
				t.Errorf("Type = %v, want TypeDynamicUv", dep.Type)
			}
		})
	}
}

func TestParseDynamicGo(t *testing.T) {
	tests := []struct {
		input   string
		pkg     string
		name    string // display name (last component)
		version string
		wantErr bool
	}{
		{"go:golang.org/x/tools/gopls", "golang.org/x/tools/gopls", "gopls", "", false},
		{"go:github.com/user/repo/cmd/tool@1.0.0", "github.com/user/repo/cmd/tool", "tool", "1.0.0", false},
		{"go:invalid", "", "", "", true}, // no slash
		{"go:", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if dep.Package != tt.pkg {
				t.Errorf("Package = %q, want %q", dep.Package, tt.pkg)
			}
			if dep.Name != tt.name {
				t.Errorf("Name = %q, want %q", dep.Name, tt.name)
			}
			if dep.Type != TypeDynamicGo {
				t.Errorf("Type = %v, want TypeDynamicGo", dep.Type)
			}
		})
	}
}

func TestParseDynamicCargo(t *testing.T) {
	tests := []struct {
		input   string
		pkg     string
		version string
		wantErr bool
	}{
		{"cargo:ripgrep", "ripgrep", "", false},
		{"cargo:ripgrep@14.0.0", "ripgrep", "14.0.0", false},
		{"cargo:fd-find", "fd-find", "", false},
		{"cargo:", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if dep.Package != tt.pkg {
				t.Errorf("Package = %q, want %q", dep.Package, tt.pkg)
			}
			if dep.Type != TypeDynamicCargo {
				t.Errorf("Type = %v, want TypeDynamicCargo", dep.Type)
			}
		})
	}
}

func TestDynamicDepImplicitRequires(t *testing.T) {
	tests := []struct {
		input    string
		requires []string
	}{
		{"npm:eslint", []string{"node"}},
		{"pip:pytest", []string{"python"}},
		{"uv:ruff", []string{"python", "uv"}},
		{"cargo:ripgrep", []string{"rust"}},
		{"go:golang.org/x/tools/gopls", []string{"go"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			reqs := dep.ImplicitRequires()
			if len(reqs) != len(tt.requires) {
				t.Errorf("ImplicitRequires() = %v, want %v", reqs, tt.requires)
				return
			}
			for i, r := range reqs {
				if r != tt.requires[i] {
					t.Errorf("ImplicitRequires()[%d] = %q, want %q", i, r, tt.requires[i])
				}
			}
		})
	}
}

func TestDynamicDepValidation(t *testing.T) {
	tests := []struct {
		deps    []string
		wantErr bool
		errMsg  string
	}{
		// Valid: has required runtime
		{[]string{"node", "npm:eslint"}, false, ""},
		{[]string{"python", "pip:pytest"}, false, ""},
		{[]string{"python", "uv", "uv:ruff"}, false, ""},
		{[]string{"go", "go:golang.org/x/tools/gopls"}, false, ""},

		// Invalid: missing required runtime
		{[]string{"npm:eslint"}, true, "requires node"},
		{[]string{"pip:pytest"}, true, "requires python"},
		{[]string{"uv:ruff"}, true, "requires python"},
		{[]string{"python", "uv:ruff"}, true, "requires uv"}, // uv: needs both python and uv
		{[]string{"go:golang.org/x/tools/gopls"}, true, "requires go"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.deps, ","), func(t *testing.T) {
			deps, parseErr := ParseAll(tt.deps)
			if parseErr != nil {
				t.Fatalf("ParseAll error: %v", parseErr)
			}
			err := Validate(deps)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate(%v) should error", tt.deps)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("Validate(%v) error: %v", tt.deps, err)
			}
		})
	}
}

func TestParseAllWithDynamicDeps(t *testing.T) {
	// Mix of registry and dynamic deps
	deps, err := ParseAll([]string{"node@20", "npm:eslint", "npm:prettier", "git"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	if len(deps) != 4 {
		t.Fatalf("expected 4 deps, got %d", len(deps))
	}

	// Check types
	if deps[0].Type != TypeRuntime { // registry deps get type from registry
		t.Errorf("node should have TypeRuntime, got %v", deps[0].Type)
	}
	if deps[1].Type != TypeDynamicNpm {
		t.Errorf("eslint should be TypeDynamicNpm, got %v", deps[1].Type)
	}
}

func TestDynamicDepDeduplication(t *testing.T) {
	// Same package specified twice should be deduplicated
	deps, err := ParseAll([]string{"node", "npm:eslint", "npm:eslint"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	// Should have node + 1 eslint (not 2)
	if len(deps) != 2 {
		t.Errorf("expected 2 deps (deduplicated), got %d: %v", len(deps), deps)
	}
}

func TestCliEssentialsMetaBundle(t *testing.T) {
	// cli-essentials expands to jq, yq, fzf, ripgrep, fd, bat
	deps, err := ParseAll([]string{"cli-essentials"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	expected := []string{"jq", "yq", "fzf", "ripgrep", "fd", "bat"}
	if len(deps) != len(expected) {
		t.Errorf("expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}

	names := make(map[string]bool)
	for _, d := range deps {
		names[d.Name] = true
	}
	for _, exp := range expected {
		if !names[exp] {
			t.Errorf("expected %q in expanded deps", exp)
		}
	}

	// Validate should pass (no requirements)
	if err := Validate(deps); err != nil {
		t.Errorf("Validate error: %v", err)
	}
}

func TestPythonDevMetaBundle(t *testing.T) {
	// python-dev expands to uv, ruff, black, mypy, pytest
	// Needs python as dependency since uv requires python
	deps, err := ParseAll([]string{"python", "python-dev"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	expected := []string{"python", "uv", "ruff", "black", "mypy", "pytest"}
	if len(deps) != len(expected) {
		t.Errorf("expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}

	// Validate should pass
	if err := Validate(deps); err != nil {
		t.Errorf("Validate error: %v", err)
	}
}

func TestNodeDevMetaBundle(t *testing.T) {
	// node-dev expands to typescript, prettier, eslint
	// All require node
	deps, err := ParseAll([]string{"node", "node-dev"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	expected := []string{"node", "typescript", "prettier", "eslint"}
	if len(deps) != len(expected) {
		t.Errorf("expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}

	// Validate should pass
	if err := Validate(deps); err != nil {
		t.Errorf("Validate error: %v", err)
	}
}

func TestK8sMetaBundle(t *testing.T) {
	// k8s expands to kubectl, helm
	deps, err := ParseAll([]string{"k8s"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	expected := []string{"kubectl", "helm"}
	if len(deps) != len(expected) {
		t.Errorf("expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}

	// Validate should pass
	if err := Validate(deps); err != nil {
		t.Errorf("Validate error: %v", err)
	}
}

func TestDockerDependency(t *testing.T) {
	// docker requires explicit mode - "docker" alone should error
	_, err := Parse("docker")
	if err == nil {
		t.Fatal("Parse(docker) should error without explicit mode")
	}
	if !strings.Contains(err.Error(), "requires explicit mode") {
		t.Errorf("error should mention 'requires explicit mode', got: %v", err)
	}

	// docker:host should work
	dep, err := Parse("docker:host")
	if err != nil {
		t.Fatalf("Parse(docker:host) error: %v", err)
	}
	if dep.Name != "docker" {
		t.Errorf("Name = %q, want %q", dep.Name, "docker")
	}
	if dep.Version != "" {
		t.Errorf("Version = %q, want empty", dep.Version)
	}
	if dep.IsDynamic() {
		t.Error("docker should not be a dynamic dependency")
	}
	if dep.DockerMode != DockerModeHost {
		t.Errorf("DockerMode = %q, want %q", dep.DockerMode, DockerModeHost)
	}

	// ParseAll should work with explicit mode
	deps, err := ParseAll([]string{"docker:host"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Name != "docker" {
		t.Errorf("deps[0].Name = %q, want %q", deps[0].Name, "docker")
	}

	// Validate should pass (docker has no requirements)
	if err := Validate(deps); err != nil {
		t.Errorf("Validate error: %v", err)
	}

	// Verify it's in the registry with correct type
	spec, ok := GetSpec("docker")
	if !ok {
		t.Fatal("docker should be in registry")
	}
	if spec.Type != TypeDocker {
		t.Errorf("spec.Type = %q, want %q", spec.Type, TypeDocker)
	}
}

func TestDockerModes(t *testing.T) {
	tests := []struct {
		input      string
		mode       DockerMode
		wantErr    bool
		errContain string
	}{
		// Valid modes
		{"docker:host", DockerModeHost, false, ""}, // Explicit host
		{"docker:dind", DockerModeDind, false, ""}, // DinD mode

		// Invalid modes
		{"docker", "", true, "requires explicit mode"}, // No default - must specify mode
		{"docker:invalid", "", true, "invalid docker mode"},
		{"docker:HOST", "", true, "invalid docker mode"}, // Case sensitive
		{"docker:", "", true, "invalid docker mode"},     // Empty mode
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dep, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) should error", tt.input)
				} else if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if dep.Name != "docker" {
				t.Errorf("Name = %q, want %q", dep.Name, "docker")
			}
			if dep.DockerMode != tt.mode {
				t.Errorf("DockerMode = %q, want %q", dep.DockerMode, tt.mode)
			}
			if dep.IsDynamic() {
				t.Error("docker should not be a dynamic dependency")
			}
		})
	}
}

func TestDockerModeParseAll(t *testing.T) {
	// Test that ParseAll works with docker modes
	tests := []struct {
		deps       []string
		expectMode DockerMode
	}{
		{[]string{"docker:host"}, DockerModeHost},
		{[]string{"docker:dind"}, DockerModeDind},
		{[]string{"node", "docker:dind"}, DockerModeDind},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.deps, ","), func(t *testing.T) {
			deps, err := ParseAll(tt.deps)
			if err != nil {
				t.Fatalf("ParseAll error: %v", err)
			}

			// Find docker dep
			var dockerDep *Dependency
			for i := range deps {
				if deps[i].Name == "docker" {
					dockerDep = &deps[i]
					break
				}
			}
			if dockerDep == nil {
				t.Fatal("docker dependency not found")
			}
			if dockerDep.DockerMode != tt.expectMode {
				t.Errorf("DockerMode = %q, want %q", dockerDep.DockerMode, tt.expectMode)
			}

			// Validate should pass
			if err := Validate(deps); err != nil {
				t.Errorf("Validate error: %v", err)
			}
		})
	}
}

func TestDockerModeDeduplication(t *testing.T) {
	// If docker is specified multiple times, last one wins for the mode
	// but they should be deduplicated
	deps, err := ParseAll([]string{"docker:host", "docker:dind"})
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}

	// Should have only 1 docker dependency
	dockerCount := 0
	for _, d := range deps {
		if d.Name == "docker" {
			dockerCount++
		}
	}
	if dockerCount != 1 {
		t.Errorf("expected 1 docker dep, got %d", dockerCount)
	}
}
