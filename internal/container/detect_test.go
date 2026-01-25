package container

import (
	"context"
	"testing"
)

func TestGVisorAvailable(t *testing.T) {
	// This test verifies the function exists and returns a boolean.
	// Actual gVisor detection depends on Docker daemon configuration.
	ctx := context.Background()

	// Should not panic, should return bool
	_ = GVisorAvailable(ctx)
}
