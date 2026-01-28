//go:build integration

package container

import (
	"context"
	"testing"
)

func TestDockerRuntime_ListImages(t *testing.T) {
	rt, err := NewDockerRuntime(false) // Use runc for tests (no gVisor requirement)
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}
	defer rt.Close()

	ctx := context.Background()
	images, err := rt.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}

	// Should return without error (may be empty if no moat images)
	t.Logf("Found %d moat images", len(images))
}

func TestDockerRuntime_ListContainers(t *testing.T) {
	rt, err := NewDockerRuntime(false) // Use runc for tests (no gVisor requirement)
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}
	defer rt.Close()

	ctx := context.Background()
	containers, err := rt.ListContainers(ctx)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}

	t.Logf("Found %d moat containers", len(containers))
}
