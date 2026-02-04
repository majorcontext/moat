// internal/deps/script_test.go
package deps

import (
	"strings"
	"testing"
)

func TestGenerateInstallScript_Empty(t *testing.T) {
	script, err := GenerateInstallScript([]Dependency{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "#!/bin/bash\nset -e\n"
	if script != want {
		t.Errorf("empty dependencies should return minimal script\ngot:\n%s\nwant:\n%s", script, want)
	}
}

func TestGenerateInstallScript_SingleAptPackage(t *testing.T) {
	deps := []Dependency{
		{Name: "psql"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain apt-get update and install
	if !strings.Contains(script, "apt-get update") {
		t.Error("script should contain apt-get update")
	}
	if !strings.Contains(script, "apt-get install -y") {
		t.Error("script should contain apt-get install")
	}
	if !strings.Contains(script, "postgresql-client") {
		t.Error("script should contain postgresql-client package")
	}
}

func TestGenerateInstallScript_SingleRuntime(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain Node.js setup
	if !strings.Contains(script, "https://deb.nodesource.com/setup_20.x") {
		t.Error("script should contain Node.js setup URL")
	}
	if !strings.Contains(script, "apt-get install -y nodejs") {
		t.Error("script should install nodejs package")
	}
}

func TestGenerateInstallScript_RuntimeDefaultVersion(t *testing.T) {
	deps := []Dependency{
		{Name: "node"}, // no version specified
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should use default version from registry (20)
	if !strings.Contains(script, "setup_20.x") {
		t.Error("script should use default Node.js version 20")
	}
}

func TestGenerateInstallScript_MultipleDependencies(t *testing.T) {
	deps := []Dependency{
		{Name: "psql"},
		{Name: "mysql-client"},
		{Name: "node", Version: "20"},
		{Name: "go", Version: "1.22"},
		{Name: "protoc"},
		{Name: "typescript"},
		{Name: "yarn"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check apt packages are grouped
	if !strings.Contains(script, "postgresql-client") {
		t.Error("script should contain postgresql-client")
	}
	if !strings.Contains(script, "mysql-client") {
		t.Error("script should contain mysql-client")
	}

	// Check runtimes
	if !strings.Contains(script, "setup_20.x") {
		t.Error("script should install Node.js")
	}
	// Go now detects architecture at build time via uname -m
	if !strings.Contains(script, "go1.22.linux-${ARCH}") {
		t.Error("script should install Go")
	}

	// Check github-binary (protoc now uses {arch} placeholder with targets)
	if !strings.Contains(script, "protoc-25.1-linux-") {
		t.Error("script should install protoc")
	}

	// Check npm packages are grouped
	if !strings.Contains(script, "npm install -g") {
		t.Error("script should have npm install command")
	}
	if !strings.Contains(script, "typescript") {
		t.Error("script should install typescript")
	}
	if !strings.Contains(script, "yarn") {
		t.Error("script should install yarn")
	}
}

func TestGenerateInstallScript_NpmPackages(t *testing.T) {
	deps := []Dependency{
		{Name: "typescript"},
		{Name: "yarn"},
		{Name: "pnpm"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// typescript should be installed via npm
	if !strings.Contains(script, "npm install -g typescript") {
		t.Error("typescript should be installed via npm")
	}

	// yarn and pnpm should be enabled via corepack (custom installers)
	if !strings.Contains(script, "corepack enable") {
		t.Error("corepack should be enabled for yarn/pnpm")
	}
	if !strings.Contains(script, "corepack prepare yarn@stable") {
		t.Error("yarn should be installed via corepack")
	}
	if !strings.Contains(script, "corepack prepare pnpm@latest") {
		t.Error("pnpm should be installed via corepack")
	}
}

func TestGenerateInstallScript_GithubBinary(t *testing.T) {
	deps := []Dependency{
		{Name: "protoc", Version: "25.1"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should download and install protoc
	if !strings.Contains(script, "https://github.com/protocolbuffers/protobuf/releases/download/v25.1/protoc-25.1-linux-x86_64.zip") {
		t.Error("script should contain protoc download URL")
	}
	if !strings.Contains(script, "unzip") {
		t.Error("script should unzip protoc")
	}
	if !strings.Contains(script, "/usr/local") {
		t.Error("script should install to /usr/local")
	}
}

func TestGenerateInstallScript_GithubBinaryTarGz(t *testing.T) {
	deps := []Dependency{
		{Name: "sqlc"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should download and extract tar.gz
	if !strings.Contains(script, "sqlc_1.25.0_linux_amd64.tar.gz") {
		t.Error("script should contain sqlc download asset")
	}
	if !strings.Contains(script, "tar -xz") {
		t.Error("script should extract tar.gz")
	}
}

func TestGenerateInstallScript_CustomPlaywright(t *testing.T) {
	deps := []Dependency{
		{Name: "playwright"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should install playwright globally and install chromium
	if !strings.Contains(script, "npm install -g playwright") {
		t.Error("script should install playwright globally")
	}
	if !strings.Contains(script, "npx playwright install --with-deps chromium") {
		t.Error("script should install chromium with dependencies")
	}
}

func TestGenerateInstallScript_CustomAWS(t *testing.T) {
	deps := []Dependency{
		{Name: "aws"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should download and install AWS CLI
	if !strings.Contains(script, "awscli.amazonaws.com") {
		t.Error("script should download AWS CLI")
	}
	if !strings.Contains(script, "aws/install") {
		t.Error("script should run AWS CLI installer")
	}
}

func TestGenerateInstallScript_CustomGCloud(t *testing.T) {
	deps := []Dependency{
		{Name: "gcloud"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should download and install gcloud (now detects architecture at build time)
	if !strings.Contains(script, "google-cloud-cli-linux-${ARCH}") {
		t.Error("script should download gcloud")
	}
	if !strings.Contains(script, "google-cloud-sdk/install.sh") {
		t.Error("script should run gcloud installer")
	}
	if !strings.Contains(script, "export PATH") {
		t.Error("script should export PATH for gcloud")
	}
}

func TestGenerateInstallScript_InstallOrder(t *testing.T) {
	deps := []Dependency{
		{Name: "playwright"}, // custom
		{Name: "typescript"}, // npm
		{Name: "protoc"},     // github-binary
		{Name: "node"},       // runtime
		{Name: "psql"},       // apt
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find positions in script
	aptPos := strings.Index(script, "postgresql-client")
	nodePos := strings.Index(script, "setup_20.x")
	protocPos := strings.Index(script, "protoc-")
	tsPos := strings.Index(script, "typescript")
	playwrightPos := strings.Index(script, "playwright")

	// Verify install order: apt < runtime < github-binary < npm < custom
	if aptPos == -1 || nodePos == -1 || protocPos == -1 || tsPos == -1 || playwrightPos == -1 {
		t.Fatal("script missing expected content")
	}

	if aptPos > nodePos {
		t.Error("apt packages should be installed before runtimes")
	}
	if nodePos > protocPos {
		t.Error("runtimes should be installed before github binaries")
	}
	if protocPos > tsPos {
		t.Error("github binaries should be installed before npm packages")
	}
	if tsPos > playwrightPos {
		t.Error("npm packages should be installed before custom installs")
	}
}

func TestGenerateInstallScript_IdempotentCommands(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "20"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Script should have set -e for error handling
	if !strings.HasPrefix(script, "#!/bin/bash\nset -e\n") {
		t.Error("script should start with shebang and set -e")
	}
}

func TestGenerateInstallScript_BasePackages(t *testing.T) {
	deps := []Dependency{
		{Name: "psql"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include base packages for general use
	if !strings.Contains(script, "curl") {
		t.Error("script should install curl")
	}
	if !strings.Contains(script, "ca-certificates") {
		t.Error("script should install ca-certificates")
	}
	if !strings.Contains(script, "gnupg") {
		t.Error("script should install gnupg")
	}
	if !strings.Contains(script, "unzip") {
		t.Error("script should install unzip")
	}
}

func TestGenerateInstallScript_Python(t *testing.T) {
	deps := []Dependency{
		{Name: "python", Version: "3.10"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should install Python from Ubuntu's default packages
	if !strings.Contains(script, "python3") {
		t.Error("script should install python3")
	}
	if !strings.Contains(script, "python3-pip") {
		t.Error("script should install python3-pip")
	}
	// Should set up update-alternatives for PATH
	if !strings.Contains(script, "update-alternatives") {
		t.Error("script should set up Python alternatives for PATH")
	}
}

// Example test to demonstrate full script output
func TestGenerateInstallScript_Example(t *testing.T) {
	deps := []Dependency{
		{Name: "psql"},
		{Name: "node", Version: "20"},
		{Name: "protoc"},
		{Name: "typescript"},
		{Name: "yarn"},
		{Name: "playwright"},
	}

	script, err := GenerateInstallScript(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Print for visual inspection
	t.Logf("Generated script:\n%s", script)
}
