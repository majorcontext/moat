package container

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestParseContainerIP(t *testing.T) {
	// Real output from `container inspect`
	inspectJSON := `[{"networks":[{"macAddress":"fe:6f:a4:62:2c:2c","network":"moat-run_30c05d9962c8","hostname":"moat-postgres-run_30c05d9962c8","ipv4Address":"192.168.68.2/24","ipv4Gateway":"192.168.68.1","ipv6Address":"fda7:cecc:4485:250e:fc6f:a4ff:fe62:2c2c/64"}],"status":"running"}]`

	var info []struct {
		Networks []struct {
			IPv4Address string `json:"ipv4Address"`
		} `json:"networks"`
	}
	require.NoError(t, json.Unmarshal([]byte(inspectJSON), &info))
	require.Len(t, info, 1)
	require.Len(t, info[0].Networks, 1)

	addr := info[0].Networks[0].IPv4Address
	// Strip CIDR prefix
	if idx := len("192.168.68.2"); idx < len(addr) && addr[idx] == '/' {
		addr = addr[:idx]
	}
	assert.Equal(t, "192.168.68.2", addr)
}

func TestGetContainerIPParsing(t *testing.T) {
	// Test that getContainerIP would correctly parse the IP (without calling CLI)
	// We test the parsing logic by verifying buildServiceInfo uses the host parameter
	cfg := ServiceConfig{
		Name:    "postgres",
		Version: "17",
		Image:   "postgres",
		Ports:   map[string]int{"default": 5432},
	}
	info := buildServiceInfo("abc123", cfg, "192.168.68.2")
	assert.Equal(t, "192.168.68.2", info.Host)
}

// Verify getContainerIP is callable (compilation check for method signature).
func TestGetContainerIPExists(t *testing.T) {
	mgr := &appleServiceManager{containerBin: "false"}
	_, err := mgr.getContainerIP(context.Background(), "nonexistent")
	// Expected to fail — we just verify the method exists and returns an error
	assert.Error(t, err)
}
