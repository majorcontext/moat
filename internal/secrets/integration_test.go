//go:build integration

package secrets

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestOnePasswordResolver_Integration(t *testing.T) {
	// Skip if op CLI not available
	if _, err := exec.LookPath("op"); err != nil {
		t.Skip("op CLI not installed, skipping integration test")
	}

	// Skip if not signed in (check with op whoami)
	cmd := exec.Command("op", "whoami")
	if err := cmd.Run(); err != nil {
		t.Skip("not signed in to 1Password, skipping integration test")
	}

	// This test requires a real 1Password item to exist.
	// Create a test item: op item create --category=login --title="AgentOps Test" --vault="Private" password=test-secret
	// Then set this reference:
	testRef := "op://Private/AgentOps Test/password"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resolver := &OnePasswordResolver{}
	val, err := resolver.Resolve(ctx, testRef)
	if err != nil {
		t.Fatalf("failed to resolve: %v", err)
	}

	if val == "" {
		t.Error("resolved value is empty")
	}

	t.Logf("Successfully resolved secret (length: %d)", len(val))
}
