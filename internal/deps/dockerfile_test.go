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

	// Check base image
	if !strings.HasPrefix(dockerfile, "FROM ubuntu:22.04") {
		t.Error("Dockerfile should start with FROM ubuntu:22.04")
	}

	// Check apt packages are batched
	if !strings.Contains(dockerfile, "apt-get install") {
		t.Error("Dockerfile should have apt-get install")
	}
	if !strings.Contains(dockerfile, "postgresql-client") {
		t.Error("Dockerfile should install postgresql-client")
	}

	// Check node setup
	if !strings.Contains(dockerfile, "nodesource") || !strings.Contains(dockerfile, "setup_20") {
		t.Error("Dockerfile should set up Node.js 20")
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
	if !strings.HasPrefix(dockerfile, "FROM ubuntu:22.04") {
		t.Error("Empty deps should still have base image")
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
	if !strings.Contains(dockerfile, "go.dev/dl/go1.22") {
		t.Error("Dockerfile should install Go 1.22")
	}
	if !strings.Contains(dockerfile, "/usr/local/go/bin") {
		t.Error("Dockerfile should set Go PATH")
	}
}

func TestGenerateDockerfilePython(t *testing.T) {
	deps := []Dependency{{Name: "python", Version: "3.10"}}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if !strings.Contains(dockerfile, "python3") {
		t.Error("Dockerfile should install python3")
	}
	if !strings.Contains(dockerfile, "python3-pip") {
		t.Error("Dockerfile should install python3-pip")
	}
	if !strings.Contains(dockerfile, "update-alternatives") {
		t.Error("Dockerfile should set up Python alternatives for PATH")
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

	// Check node is installed
	if !strings.Contains(dockerfile, "nodesource") {
		t.Error("Dockerfile should install Node.js")
	}

	// Check SSH is also installed
	if !strings.Contains(dockerfile, "openssh-client") {
		t.Error("Dockerfile should install openssh-client")
	}
	if !strings.Contains(dockerfile, "ENTRYPOINT") {
		t.Error("Dockerfile should set ENTRYPOINT")
	}
}
