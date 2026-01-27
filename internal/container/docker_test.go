package container

import (
	"testing"
)

func TestConfig_GroupAdd(t *testing.T) {
	// Verify that GroupAdd field can be set on Config struct
	// and is properly typed as []string
	cfg := Config{
		Name:     "test-container",
		Image:    "ubuntu:22.04",
		GroupAdd: []string{"999", "docker"},
	}

	// Verify the GroupAdd field is set correctly
	if len(cfg.GroupAdd) != 2 {
		t.Errorf("expected GroupAdd to have 2 elements, got %d", len(cfg.GroupAdd))
	}
	if cfg.GroupAdd[0] != "999" {
		t.Errorf("expected GroupAdd[0] to be '999', got %q", cfg.GroupAdd[0])
	}
	if cfg.GroupAdd[1] != "docker" {
		t.Errorf("expected GroupAdd[1] to be 'docker', got %q", cfg.GroupAdd[1])
	}
}

func TestConfig_GroupAddEmpty(t *testing.T) {
	// Verify that Config works correctly with empty GroupAdd
	cfg := Config{
		Name:  "test-container",
		Image: "ubuntu:22.04",
	}

	// GroupAdd should be nil by default
	if cfg.GroupAdd != nil {
		t.Errorf("expected GroupAdd to be nil by default, got %v", cfg.GroupAdd)
	}
}

func TestConfig_Privileged(t *testing.T) {
	// Verify that Privileged field can be set on Config struct
	cfg := Config{
		Name:       "test-container",
		Image:      "ubuntu:22.04",
		Privileged: true,
	}

	// Verify the Privileged field is set correctly
	if !cfg.Privileged {
		t.Errorf("expected Privileged to be true, got false")
	}
}

func TestConfig_PrivilegedDefault(t *testing.T) {
	// Verify that Config defaults to non-privileged mode
	cfg := Config{
		Name:  "test-container",
		Image: "ubuntu:22.04",
	}

	// Privileged should be false by default
	if cfg.Privileged {
		t.Errorf("expected Privileged to be false by default, got true")
	}
}

func TestDockerRuntime_Type(t *testing.T) {
	// Test that DockerRuntime returns correct type
	// Note: This doesn't require a Docker daemon
	r := &DockerRuntime{}
	if r.Type() != RuntimeDocker {
		t.Errorf("Type() = %v, want %v", r.Type(), RuntimeDocker)
	}
}
