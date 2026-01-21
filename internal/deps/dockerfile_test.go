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
	// With multiple runtimes, should fall back to Ubuntu and install both
	deps := []Dependency{
		{Name: "node", Version: "20"},
		{Name: "python", Version: "3.10"},
	}
	dockerfile, err := GenerateDockerfile(deps, nil)
	if err != nil {
		t.Fatalf("GenerateDockerfile error: %v", err)
	}

	// Should use Ubuntu as base when multiple runtimes
	if !strings.HasPrefix(dockerfile, "FROM ubuntu:22.04") {
		t.Errorf("Dockerfile should use ubuntu:22.04 for multiple runtimes, got:\n%s", dockerfile[:100])
	}

	// Both runtimes should be installed
	if !strings.Contains(dockerfile, "nodesource") {
		t.Error("Dockerfile should install Node.js")
	}
	if !strings.Contains(dockerfile, "python3") {
		t.Error("Dockerfile should install Python")
	}
}
