package deps

import (
	"strings"
	"testing"
)

// TestFullPipeline tests the complete dependency pipeline from parsing to image tag generation.
func TestFullPipeline(t *testing.T) {
	// Parse dependencies
	depStrings := []string{"node@20", "typescript", "psql"}
	depList, err := ParseAll(depStrings)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	// Validate
	if err := Validate(depList); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Generate Dockerfile
	dockerfile, err := GenerateDockerfile(depList)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	if !strings.Contains(dockerfile, "FROM ubuntu:22.04") {
		t.Error("Dockerfile missing base image")
	}
	if !strings.Contains(dockerfile, "nodejs") {
		t.Error("Dockerfile missing node install")
	}
	if !strings.Contains(dockerfile, "postgresql") {
		t.Error("Dockerfile missing postgresql install")
	}
	if !strings.Contains(dockerfile, "typescript") {
		t.Error("Dockerfile missing typescript install")
	}

	// Generate script
	script, err := GenerateInstallScript(depList)
	if err != nil {
		t.Fatalf("GenerateInstallScript: %v", err)
	}
	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Script missing shebang")
	}
	if !strings.Contains(script, "nodejs") {
		t.Error("Script missing node install")
	}

	// Generate image tag
	tag := ImageTag(depList)
	if !strings.HasPrefix(tag, "agentops/run:") {
		t.Errorf("unexpected tag format: %s", tag)
	}
}

// TestErrorHandlingMissingRequirements tests that validation catches missing requirements.
func TestErrorHandlingMissingRequirements(t *testing.T) {
	tests := []struct {
		name    string
		deps    []string
		wantErr string
	}{
		{
			name:    "typescript without node",
			deps:    []string{"typescript"},
			wantErr: "typescript requires node",
		},
		{
			name:    "unknown dependency",
			deps:    []string{"nonexistent"},
			wantErr: "unknown dependency",
		},
		{
			name:    "empty dependency string",
			deps:    []string{""},
			wantErr: "empty dependency",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depList, err := ParseAll(tt.deps)
			if err != nil {
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ParseAll error = %v, want error containing %q", err, tt.wantErr)
				}
				return
			}

			err = Validate(depList)
			if err == nil {
				t.Errorf("Validate() expected error containing %q, got nil", tt.wantErr)
				return
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestOrderIndependentImageTags tests that image tags are deterministic regardless of dependency order.
func TestOrderIndependentImageTags(t *testing.T) {
	deps1, err := ParseAll([]string{"node@20", "typescript", "psql"})
	if err != nil {
		t.Fatalf("ParseAll deps1: %v", err)
	}

	deps2, err := ParseAll([]string{"psql", "node@20", "typescript"})
	if err != nil {
		t.Fatalf("ParseAll deps2: %v", err)
	}

	deps3, err := ParseAll([]string{"typescript", "psql", "node@20"})
	if err != nil {
		t.Fatalf("ParseAll deps3: %v", err)
	}

	tag1 := ImageTag(deps1)
	tag2 := ImageTag(deps2)
	tag3 := ImageTag(deps3)

	if tag1 != tag2 || tag1 != tag3 {
		t.Errorf("image tags not order-independent:\n  tag1=%s\n  tag2=%s\n  tag3=%s", tag1, tag2, tag3)
	}
}

// TestValidationAllDependencyTypes tests validation with various dependency types.
func TestValidationAllDependencyTypes(t *testing.T) {
	tests := []struct {
		name    string
		deps    []string
		wantErr bool
	}{
		{
			name:    "apt package",
			deps:    []string{"psql"},
			wantErr: false,
		},
		{
			name:    "runtime with version",
			deps:    []string{"node@20"},
			wantErr: false,
		},
		{
			name:    "runtime without version (uses default)",
			deps:    []string{"node"},
			wantErr: false,
		},
		{
			name:    "npm package with node",
			deps:    []string{"node@20", "typescript"},
			wantErr: false,
		},
		{
			name:    "multiple dependency types",
			deps:    []string{"node@20", "typescript", "psql", "go@1.21"},
			wantErr: false,
		},
		{
			name:    "npm package without node",
			deps:    []string{"typescript"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depList, err := ParseAll(tt.deps)
			if err != nil {
				if !tt.wantErr {
					t.Fatalf("ParseAll: %v", err)
				}
				return
			}

			err = Validate(depList)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestPipelineWithDefaultVersions tests the pipeline handles default versions correctly.
func TestPipelineWithDefaultVersions(t *testing.T) {
	// Parse dependencies without explicit versions
	depList, err := ParseAll([]string{"node", "typescript"})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	// Validate
	if err := Validate(depList); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Generate Dockerfile - should use default versions from registry
	dockerfile, err := GenerateDockerfile(depList)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	if !strings.Contains(dockerfile, "FROM ubuntu:22.04") {
		t.Error("Dockerfile missing base image")
	}

	// Generate script
	script, err := GenerateInstallScript(depList)
	if err != nil {
		t.Fatalf("GenerateInstallScript: %v", err)
	}
	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Script missing shebang")
	}

	// Generate image tag - should use default versions
	tag := ImageTag(depList)
	if !strings.HasPrefix(tag, "agentops/run:") {
		t.Errorf("unexpected tag format: %s", tag)
	}

	// Verify image tag is deterministic
	tag2 := ImageTag(depList)
	if tag != tag2 {
		t.Errorf("image tags not deterministic: %s != %s", tag, tag2)
	}
}

// TestEmptyDependencies tests handling of empty dependency lists.
func TestEmptyDependencies(t *testing.T) {
	depList, err := ParseAll([]string{})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	if len(depList) != 0 {
		t.Errorf("expected empty dependency list, got %d deps", len(depList))
	}

	// Validate empty list
	if err := Validate(depList); err != nil {
		t.Errorf("Validate empty list: %v", err)
	}

	// Generate Dockerfile for empty list
	dockerfile, err := GenerateDockerfile(depList)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	if !strings.Contains(dockerfile, "FROM ubuntu:22.04") {
		t.Error("Dockerfile missing base image")
	}

	// Generate script for empty list
	script, err := GenerateInstallScript(depList)
	if err != nil {
		t.Fatalf("GenerateInstallScript: %v", err)
	}
	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Script missing shebang")
	}

	// Generate image tag for empty list
	tag := ImageTag(depList)
	if !strings.HasPrefix(tag, "agentops/run:") {
		t.Errorf("unexpected tag format: %s", tag)
	}
}

// TestGoInstallDependencies tests that go-install type dependencies generate correct install commands.
func TestGoInstallDependencies(t *testing.T) {
	deps := []string{"go", "govulncheck", "mockgen"}
	depList, err := ParseAll(deps)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	if err := Validate(depList); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	dockerfile, err := GenerateDockerfile(depList)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}

	// Should contain go install commands with GOBIN set for PATH access
	if !strings.Contains(dockerfile, "GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@latest") {
		t.Error("Dockerfile missing govulncheck go install with GOBIN")
	}
	if !strings.Contains(dockerfile, "GOBIN=/usr/local/bin go install go.uber.org/mock/mockgen@latest") {
		t.Error("Dockerfile missing mockgen go install with GOBIN")
	}

	script, err := GenerateInstallScript(depList)
	if err != nil {
		t.Fatalf("GenerateInstallScript: %v", err)
	}

	// Script should also contain go install commands with GOBIN set
	if !strings.Contains(script, "GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@latest") {
		t.Error("Script missing govulncheck go install with GOBIN")
	}
	if !strings.Contains(script, "GOBIN=/usr/local/bin go install go.uber.org/mock/mockgen@latest") {
		t.Error("Script missing mockgen go install with GOBIN")
	}
}

// TestDockerfileAndScriptConsistency tests that Dockerfile and install script generate similar output.
func TestDockerfileAndScriptConsistency(t *testing.T) {
	deps := []string{"node@20", "typescript", "psql"}
	depList, err := ParseAll(deps)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	dockerfile, err := GenerateDockerfile(depList)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}

	script, err := GenerateInstallScript(depList)
	if err != nil {
		t.Fatalf("GenerateInstallScript: %v", err)
	}

	// Both should install nodejs
	if !strings.Contains(dockerfile, "nodejs") {
		t.Error("Dockerfile missing nodejs")
	}
	if !strings.Contains(script, "nodejs") {
		t.Error("Script missing nodejs")
	}

	// Both should install postgresql
	if !strings.Contains(dockerfile, "postgresql") {
		t.Error("Dockerfile missing postgresql")
	}
	if !strings.Contains(script, "postgresql") {
		t.Error("Script missing postgresql")
	}

	// Both should install typescript
	if !strings.Contains(dockerfile, "typescript") {
		t.Error("Dockerfile missing typescript")
	}
	if !strings.Contains(script, "typescript") {
		t.Error("Script missing typescript")
	}
}
