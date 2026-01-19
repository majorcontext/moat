//go:build integration

package secrets

import (
	"context"
	"os"
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
	// Create a test item: op item create --category=login --title="Moat Test" --vault="Private" password=test-secret
	//
	// Configure via environment variables:
	//   OP_TEST_VAULT - vault name (default: "Private")
	//   OP_TEST_ITEM  - item name (default: "Moat Test")
	//   OP_TEST_FIELD - field name (default: "password")
	vault := os.Getenv("OP_TEST_VAULT")
	if vault == "" {
		vault = "Private"
	}
	item := os.Getenv("OP_TEST_ITEM")
	if item == "" {
		item = "Moat Test"
	}
	field := os.Getenv("OP_TEST_FIELD")
	if field == "" {
		field = "password"
	}
	testRef := "op://" + vault + "/" + item + "/" + field

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resolver := &OnePasswordResolver{}
	val, err := resolver.Resolve(ctx, testRef)
	if err != nil {
		t.Fatalf("failed to resolve %s: %v", testRef, err)
	}

	if val == "" {
		t.Error("resolved value is empty")
	}

	t.Logf("Successfully resolved secret (length: %d)", len(val))
}

func TestSSMResolver_Integration(t *testing.T) {
	// Skip if aws CLI not available
	if _, err := exec.LookPath("aws"); err != nil {
		t.Skip("aws CLI not installed, skipping integration test")
	}

	// Skip if not authenticated (check with aws sts get-caller-identity)
	cmd := exec.Command("aws", "sts", "get-caller-identity")
	if err := cmd.Run(); err != nil {
		t.Skip("not authenticated to AWS, skipping integration test")
	}

	// Configure via environment variables:
	//   SSM_TEST_PARAM - parameter path (default: "/myapp/test-secret")
	//   SSM_TEST_REGION - AWS region (optional)
	paramPath := os.Getenv("SSM_TEST_PARAM")
	if paramPath == "" {
		paramPath = "/myapp/test-secret"
	}

	testRef := "ssm://" + paramPath
	if region := os.Getenv("SSM_TEST_REGION"); region != "" {
		testRef = "ssm://" + region + paramPath
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resolver := &SSMResolver{}
	val, err := resolver.Resolve(ctx, testRef)
	if err != nil {
		t.Fatalf("failed to resolve %s: %v", testRef, err)
	}

	if val == "" {
		t.Error("resolved value is empty")
	}

	t.Logf("Successfully resolved secret (length: %d)", len(val))
}
