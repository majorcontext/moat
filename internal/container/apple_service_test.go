package container

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppleServiceManagerImplementsInterface(t *testing.T) {
	var _ ServiceManager = (*appleServiceManager)(nil)
}

func TestAppleBuildRunArgs(t *testing.T) {
	cfg := ServiceConfig{
		Name:    "postgres",
		Version: "17",
		Image:   "postgres",
		Ports:   map[string]int{"default": 5432},
		Env:     map[string]string{"POSTGRES_PASSWORD": "testpass"},
		RunID:   "test-run-123",
	}

	args := buildAppleRunArgs(cfg, "moat-test-net")
	assert.Contains(t, args, "--detach")
	assert.Contains(t, args, "--name")
	assert.Contains(t, args, "moat-postgres-test-run-123")
	assert.Contains(t, args, "--network")
	assert.Contains(t, args, "moat-test-net")
	assert.Contains(t, args, "--hostname")
	assert.Contains(t, args, "postgres")
	assert.Contains(t, args, "--env")
	assert.Contains(t, args, "POSTGRES_PASSWORD=testpass")
	assert.Contains(t, args, "postgres:17")
}

func TestAppleBuildRunArgsWithCmd(t *testing.T) {
	cfg := ServiceConfig{
		Name:     "redis",
		Version:  "7",
		Image:    "redis",
		Ports:    map[string]int{"default": 6379},
		Env:      map[string]string{"password": "redispass"},
		ExtraCmd: []string{"--requirepass", "{password}"},
		RunID:    "test-run-456",
	}

	args := buildAppleRunArgs(cfg, "moat-test-net")
	// Find image position
	imageIdx := -1
	for i, a := range args {
		if a == "redis:7" {
			imageIdx = i
			break
		}
	}
	assert.Greater(t, imageIdx, 0, "image should be in args")
	// Extra cmd args come after image, with placeholders resolved
	assert.Contains(t, args[imageIdx+1:], "--requirepass")
	assert.Contains(t, args[imageIdx+1:], "redispass")
}

func TestAppleBuildRunArgsNoNetwork(t *testing.T) {
	cfg := ServiceConfig{
		Name:    "redis",
		Version: "7",
		Image:   "redis",
		Env:     map[string]string{},
		RunID:   "test-run-789",
	}

	args := buildAppleRunArgs(cfg, "")
	assert.NotContains(t, args, "--network")
}
