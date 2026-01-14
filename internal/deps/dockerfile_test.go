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

	dockerfile, err := GenerateDockerfile(deps)
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
	dockerfile, err := GenerateDockerfile(nil)
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
	dockerfile, err := GenerateDockerfile(deps)
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
	dockerfile, err := GenerateDockerfile(deps)
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
	deps := []Dependency{{Name: "python", Version: "3.11"}}
	dockerfile, err := GenerateDockerfile(deps)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}
	if !strings.Contains(dockerfile, "python3.11") {
		t.Error("Dockerfile should install Python 3.11")
	}
	if !strings.Contains(dockerfile, "deadsnakes") {
		t.Error("Dockerfile should use deadsnakes PPA")
	}
	if !strings.Contains(dockerfile, "update-alternatives") {
		t.Error("Dockerfile should set up Python alternatives for PATH")
	}
}
