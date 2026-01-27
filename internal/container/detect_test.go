package container

import (
	"context"
	"testing"
	"time"
)

func TestGVisorAvailable(t *testing.T) {
	// This test verifies the function exists and returns a boolean.
	// Actual gVisor detection depends on Docker daemon configuration.
	// Use a timeout to prevent CI hangs if Docker is unreachable.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should not panic, should return bool
	_ = GVisorAvailable(ctx)
}
