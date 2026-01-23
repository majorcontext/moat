package deps

import (
	"strings"
	"testing"
)

func TestGetRuntimeCommands(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		contains []string
	}{
		{"node", "20", []string{"nodesource", "setup_20.x", "nodejs"}},
		{"go", "1.22", []string{"go.dev/dl", "go1.22", "tar"}},
		{"python", "3.10", []string{"python3", "pip", "venv"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmds := getRuntimeCommands(tt.name, tt.version)
			if len(cmds.Commands) == 0 {
				t.Fatal("expected commands, got none")
			}
			combined := strings.Join(cmds.Commands, " ")
			for _, want := range tt.contains {
				if !strings.Contains(combined, want) {
					t.Errorf("commands missing %q: %v", want, cmds.Commands)
				}
			}
		})
	}
}

func TestGetRuntimeCommandsUnknown(t *testing.T) {
	cmds := getRuntimeCommands("unknown", "1.0")
	if len(cmds.Commands) != 0 {
		t.Errorf("unknown runtime should return empty commands, got %v", cmds.Commands)
	}
}

func TestGetGithubBinaryCommands(t *testing.T) {
	spec := DepSpec{
		Repo:  "cli/cli",
		Asset: "gh_{version}_linux_amd64.tar.gz",
		Bin:   "gh_{version}_linux_amd64/bin/gh",
	}

	cmds := getGithubBinaryCommands("gh", "2.40.0", spec)
	combined := strings.Join(cmds.Commands, " ")

	// Check URL construction
	if !strings.Contains(combined, "https://github.com/cli/cli/releases/download/v2.40.0/gh_2.40.0_linux_amd64.tar.gz") {
		t.Error("missing expected GitHub URL")
	}

	// Check binary placement
	if !strings.Contains(combined, "/usr/local/bin/gh") {
		t.Error("missing binary path")
	}

	// Check chmod
	if !strings.Contains(combined, "chmod +x") {
		t.Error("missing chmod command")
	}
}

func TestGetGithubBinaryCommandsZip(t *testing.T) {
	spec := DepSpec{
		Repo:  "protocolbuffers/protobuf",
		Asset: "protoc-{version}-linux-x86_64.zip",
		Bin:   "bin/protoc",
	}

	cmds := getGithubBinaryCommands("protoc", "25.1", spec)
	combined := strings.Join(cmds.Commands, " ")

	// Should use unzip for .zip files
	if !strings.Contains(combined, "unzip") {
		t.Error("zip asset should use unzip")
	}

	// Cleanup
	if !strings.Contains(combined, "rm -rf /tmp/protoc") {
		t.Error("missing cleanup command")
	}
}

func TestGetGithubBinaryCommandsMultiArch(t *testing.T) {
	spec := DepSpec{
		Repo:       "oven-sh/bun",
		Asset:      "bun-linux-x64.zip",
		AssetARM64: "bun-linux-aarch64.zip",
		Bin:        "bun",
	}

	cmds := getGithubBinaryCommandsLegacy("bun", "1.1.0", spec)
	combined := strings.Join(cmds.Commands, " ")

	// Check architecture detection
	if !strings.Contains(combined, "ARCH=$(uname -m)") {
		t.Error("missing architecture detection")
	}

	// Check both URLs
	if !strings.Contains(combined, "bun-linux-x64.zip") {
		t.Error("missing x64 asset URL")
	}
	if !strings.Contains(combined, "bun-linux-aarch64.zip") {
		t.Error("missing arm64 asset URL")
	}

	// Check conditional
	if !strings.Contains(combined, "x86_64") {
		t.Error("missing x86_64 condition")
	}
}

func TestGetGithubBinaryCommandsMultiArchTarGz(t *testing.T) {
	spec := DepSpec{
		Repo:       "junegunn/fzf",
		Asset:      "fzf-{version}-linux_amd64.tar.gz",
		AssetARM64: "fzf-{version}-linux_arm64.tar.gz",
		Bin:        "fzf",
	}

	cmds := getGithubBinaryCommandsLegacy("fzf", "0.56.0", spec)
	combined := strings.Join(cmds.Commands, " ")

	// Should use tar for .tar.gz
	if !strings.Contains(combined, "tar -xz") {
		t.Error("tar.gz asset should use tar")
	}

	// Check version substitution
	if !strings.Contains(combined, "fzf-0.56.0-linux_amd64.tar.gz") {
		t.Error("missing version substitution in amd64 URL")
	}
	if !strings.Contains(combined, "fzf-0.56.0-linux_arm64.tar.gz") {
		t.Error("missing version substitution in arm64 URL")
	}
}

func TestGetGithubBinaryCommandsRawBinary(t *testing.T) {
	// gofumpt releases raw binaries (no archive)
	spec := DepSpec{
		Repo:  "mvdan/gofumpt",
		Asset: "gofumpt_v{version}_linux_amd64",
		Bin:   "gofumpt_v{version}_linux_amd64",
	}

	cmds := getGithubBinaryCommands("gofumpt", "0.7.0", spec)
	combined := strings.Join(cmds.Commands, " ")

	// Should download directly to /usr/local/bin (no tar/unzip)
	if strings.Contains(combined, "tar") {
		t.Error("raw binary should not use tar")
	}
	if strings.Contains(combined, "unzip") {
		t.Error("raw binary should not use unzip")
	}

	// Should curl directly to destination
	if !strings.Contains(combined, "curl -fsSL") {
		t.Error("should use curl to download")
	}
	if !strings.Contains(combined, "-o /usr/local/bin/gofumpt") {
		t.Error("should download directly to /usr/local/bin")
	}

	// Check chmod
	if !strings.Contains(combined, "chmod +x /usr/local/bin/gofumpt") {
		t.Error("missing chmod command")
	}
}

func TestGetGithubBinaryCommandsWithTargets(t *testing.T) {
	// Test the new targets-based approach for Rust-style target triples
	spec := DepSpec{
		Repo:  "BurntSushi/ripgrep",
		Asset: "ripgrep-{version}-{target}.tar.gz",
		Bin:   "ripgrep-{version}-{target}/rg",
		Targets: map[string]string{
			"amd64": "x86_64-unknown-linux-musl",
			"arm64": "aarch64-unknown-linux-gnu",
		},
	}

	cmds := getGithubBinaryCommandsWithTargets("ripgrep", "14.1.1", spec)
	combined := strings.Join(cmds.Commands, " ")

	// Check architecture detection
	if !strings.Contains(combined, "ARCH=$(uname -m)") {
		t.Error("missing architecture detection")
	}

	// Check amd64 URL with target substitution
	if !strings.Contains(combined, "ripgrep-14.1.1-x86_64-unknown-linux-musl.tar.gz") {
		t.Error("missing amd64 target substitution in URL")
	}

	// Check arm64 URL with target substitution
	if !strings.Contains(combined, "ripgrep-14.1.1-aarch64-unknown-linux-gnu.tar.gz") {
		t.Error("missing arm64 target substitution in URL")
	}

	// Check bin path substitution for amd64
	if !strings.Contains(combined, "ripgrep-14.1.1-x86_64-unknown-linux-musl/rg") {
		t.Error("missing amd64 target substitution in bin path")
	}

	// Check bin path substitution for arm64
	if !strings.Contains(combined, "ripgrep-14.1.1-aarch64-unknown-linux-gnu/rg") {
		t.Error("missing arm64 target substitution in bin path")
	}
}

func TestGetGoInstallCommands(t *testing.T) {
	spec := DepSpec{
		GoPackage: "golang.org/x/vuln/cmd/govulncheck",
	}

	cmds := getGoInstallCommands(spec)
	if len(cmds.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds.Commands))
	}

	cmd := cmds.Commands[0]
	if !strings.Contains(cmd, "GOBIN=/usr/local/bin") {
		t.Error("missing GOBIN setting")
	}
	if !strings.Contains(cmd, "go install golang.org/x/vuln/cmd/govulncheck@latest") {
		t.Error("incorrect go install command")
	}
}

func TestGetCustomCommands(t *testing.T) {
	tests := []struct {
		name     string
		contains []string
		envVars  []string
	}{
		{"playwright", []string{"npm install -g playwright", "npx playwright install"}, nil},
		{"aws", []string{"awscli", "uname -m", "unzip"}, nil},
		{"gcloud", []string{"google-cloud", "tar", "install.sh"}, []string{"PATH"}},
		{"rust", []string{"rustup", "sh -s --", "-y"}, []string{"PATH"}},
		{"kubectl", []string{"dl.k8s.io", "uname -m", "chmod"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmds := getCustomCommands(tt.name, "")
			if len(cmds.Commands) == 0 {
				t.Fatal("expected commands, got none")
			}
			combined := strings.Join(cmds.Commands, " ")
			for _, want := range tt.contains {
				if !strings.Contains(combined, want) {
					t.Errorf("commands missing %q: %v", want, cmds.Commands)
				}
			}
			for _, env := range tt.envVars {
				if _, ok := cmds.EnvVars[env]; !ok {
					t.Errorf("missing env var %q", env)
				}
			}
		})
	}
}

func TestGetCustomCommandsUnknown(t *testing.T) {
	cmds := getCustomCommands("unknown", "1.0")
	if len(cmds.Commands) != 0 {
		t.Errorf("unknown custom dep should return empty commands, got %v", cmds.Commands)
	}
}

func TestGetDynamicPackageCommands(t *testing.T) {
	tests := []struct {
		dep      Dependency
		contains string
	}{
		{Dependency{Type: TypeDynamicNpm, Package: "eslint"}, "npm install -g eslint"},
		{Dependency{Type: TypeDynamicNpm, Package: "eslint", Version: "8.0.0"}, "npm install -g eslint@8.0.0"},
		{Dependency{Type: TypeDynamicPip, Package: "pytest"}, "pip install pytest"},
		{Dependency{Type: TypeDynamicPip, Package: "pytest", Version: "7.0.0"}, "pip install pytest==7.0.0"},
		{Dependency{Type: TypeDynamicUv, Package: "ruff"}, "uv tool install ruff"},
		{Dependency{Type: TypeDynamicUv, Package: "ruff", Version: "0.1.0"}, "uv tool install ruff==0.1.0"},
		{Dependency{Type: TypeDynamicCargo, Package: "ripgrep"}, "cargo install ripgrep"},
		{Dependency{Type: TypeDynamicCargo, Package: "ripgrep", Version: "14.0.0"}, "cargo install ripgrep@14.0.0"},
		{Dependency{Type: TypeDynamicGo, Package: "golang.org/x/tools/gopls"}, "GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@latest"},
		{Dependency{Type: TypeDynamicGo, Package: "golang.org/x/tools/gopls", Version: "1.0.0"}, "GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@v1.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.contains, func(t *testing.T) {
			cmds := getDynamicPackageCommands(tt.dep)
			if len(cmds.Commands) == 0 {
				t.Fatal("expected commands, got none")
			}
			if !strings.Contains(cmds.Commands[0], tt.contains) {
				t.Errorf("command %q missing expected %q", cmds.Commands[0], tt.contains)
			}
		})
	}
}

func TestInstallCommandsFormatForDockerfile(t *testing.T) {
	cmds := InstallCommands{
		Commands: []string{"apt-get update", "apt-get install -y curl"},
		EnvVars:  map[string]string{"PATH": "/custom/bin:$PATH"},
	}

	result := cmds.FormatForDockerfile()

	// Should have RUN prefix
	if !strings.HasPrefix(result, "RUN ") {
		t.Error("should start with RUN")
	}

	// Should chain commands with &&
	if !strings.Contains(result, " && ") {
		t.Error("should chain commands with &&")
	}

	// Should have ENV
	if !strings.Contains(result, "ENV PATH=") {
		t.Error("should have ENV statement")
	}
}

func TestInstallCommandsFormatForDockerfileSingle(t *testing.T) {
	cmds := InstallCommands{
		Commands: []string{"echo hello"},
	}

	result := cmds.FormatForDockerfile()

	// Single command should not have apt cleanup
	if strings.Contains(result, "rm -rf /var/lib/apt/lists") {
		t.Error("single command should not have apt cleanup")
	}
}

func TestInstallCommandsFormatForDockerfileEmpty(t *testing.T) {
	cmds := InstallCommands{}
	result := cmds.FormatForDockerfile()
	if result != "" {
		t.Errorf("empty commands should return empty string, got %q", result)
	}
}

func TestInstallCommandsFormatForScript(t *testing.T) {
	cmds := InstallCommands{
		Commands: []string{"apt-get update", "apt-get install -y curl"},
		EnvVars:  map[string]string{"PATH": "/custom/bin:$PATH"},
	}

	result := cmds.FormatForScript()

	// Should have each command on its own line
	if !strings.Contains(result, "apt-get update\n") {
		t.Error("commands should be on separate lines")
	}

	// Should have export for env vars
	if !strings.Contains(result, "export PATH=") {
		t.Error("should have export statement")
	}
}

func TestInstallCommandsFormatForScriptEmpty(t *testing.T) {
	cmds := InstallCommands{}
	result := cmds.FormatForScript()
	if result != "" {
		t.Errorf("empty commands should return empty string, got %q", result)
	}
}

// Helper function tests
func TestReplaceVersion(t *testing.T) {
	tests := []struct {
		input   string
		version string
		want    string
	}{
		{"file-{version}.tar.gz", "1.0.0", "file-1.0.0.tar.gz"},
		{"file.tar.gz", "1.0.0", "file.tar.gz"},
		{"{version}-{version}", "1.0", "1.0-1.0"},
		{"", "1.0", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := replaceVersion(tt.input, tt.version)
			if got != tt.want {
				t.Errorf("replaceVersion(%q, %q) = %q, want %q", tt.input, tt.version, got, tt.want)
			}
		})
	}
}

func TestOrDefault(t *testing.T) {
	if got := orDefault("value", "default"); got != "value" {
		t.Errorf("orDefault(value, default) = %q, want value", got)
	}
	if got := orDefault("", "default"); got != "default" {
		t.Errorf("orDefault('', default) = %q, want default", got)
	}
}

func TestGithubReleaseURL(t *testing.T) {
	url := githubReleaseURL("owner/repo", "1.2.3", "asset.tar.gz")
	want := "https://github.com/owner/repo/releases/download/v1.2.3/asset.tar.gz"
	if url != want {
		t.Errorf("got %q, want %q", url, want)
	}
}
