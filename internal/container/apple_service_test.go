package container

import (
	"context"
	"errors"
	"testing"
	"time"

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
	mgr := &appleServiceManager{containerBin: "false", ipRetryBase: time.Millisecond}
	_, err := mgr.getContainerIP(context.Background(), "nonexistent")
	// Expected to fail — we just verify the method exists and returns an error
	assert.Error(t, err)
}

func TestParseRunContainerID(t *testing.T) {
	// container 0.12.3 emits run progress (to stderr, but defensively we also
	// tolerate it on stdout) ahead of the container ID; the ID is the last
	// non-empty line.
	blob := "[0/6] [0s]\n[1/6] Fetching image [0s]\n[6/6] Starting container [0s]\nmoat-postgres-run_8fe9526909b5"
	assert.Equal(t, "moat-postgres-run_8fe9526909b5", parseRunContainerID(blob))

	// Clean single-line output.
	assert.Equal(t, "abc123", parseRunContainerID("abc123\n"))

	// Trailing blank lines are ignored.
	assert.Equal(t, "abc123", parseRunContainerID("abc123\n\n"))

	// Empty output yields empty id.
	assert.Equal(t, "", parseRunContainerID("  \n"))
}

func TestParseContainerIPv4(t *testing.T) {
	// CIDR prefix is stripped (legacy schema: top-level networks, status string).
	addr, ok, err := parseContainerIPv4([]byte(`[{"networks":[{"ipv4Address":"192.168.68.2/24"}],"status":"running"}]`))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "192.168.68.2", addr)

	// container 1.0.0 schema: networks nested under the status object.
	addr, ok, err = parseContainerIPv4([]byte(`[{"id":"run_abc","status":{"state":"running","networks":[{"ipv4Address":"192.168.64.2/24","ipv4Gateway":"192.168.64.1"}]}}]`))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "192.168.64.2", addr)

	// Container started but no address assigned yet (the race we retry on) —
	// both schemas.
	for _, empty := range []string{
		`[{"networks":[],"status":"running"}]`,
		`[{"networks":[{"ipv4Address":""}],"status":"running"}]`,
		`[{"id":"run_abc","status":{"state":"running","networks":[]}}]`,
		`[{"id":"run_abc","status":{"state":"stopped"}}]`,
		`[]`,
	} {
		_, ok, err := parseContainerIPv4([]byte(empty))
		require.NoError(t, err)
		assert.False(t, ok, "expected no address for %s", empty)
	}

	// Malformed JSON is a real error.
	_, _, err = parseContainerIPv4([]byte(`not json`))
	assert.Error(t, err)
}

// ipInspectSchemas builds a `container inspect` response carrying the given
// IPv4 address (empty = no address assigned yet) in each CLI schema
// getContainerIP must handle: the legacy top-level `networks` array and the
// 1.0.0 `status.networks` object. Each retry test runs against both so a
// regression in how networks() traverses either layout is caught.
var ipInspectSchemas = map[string]func(addr string) []byte{
	"legacy": func(addr string) []byte {
		return []byte(`[{"networks":[{"ipv4Address":"` + addr + `"}],"status":"running"}]`)
	},
	"v1": func(addr string) []byte {
		return []byte(`[{"id":"run_x","status":{"state":"running","networks":[{"ipv4Address":"` + addr + `"}]}}]`)
	},
}

func TestGetContainerIPRetriesUntilAssigned(t *testing.T) {
	for name, build := range ipInspectSchemas {
		t.Run(name, func(t *testing.T) {
			calls := 0
			mgr := &appleServiceManager{
				ipRetryBase: time.Millisecond,
				inspectFn: func(_ context.Context, _ string) ([]byte, error) {
					calls++
					if calls < 3 {
						return build(""), nil // address not assigned yet
					}
					return build("192.168.81.2/24"), nil
				},
			}
			addr, err := mgr.getContainerIP(context.Background(), "moat-postgres-run_x")
			require.NoError(t, err)
			assert.Equal(t, "192.168.81.2", addr)
			assert.Equal(t, 3, calls, "should poll until the address appears")
		})
	}
}

func TestGetContainerIPTimesOutWithoutAddress(t *testing.T) {
	for name, build := range ipInspectSchemas {
		t.Run(name, func(t *testing.T) {
			calls := 0
			mgr := &appleServiceManager{
				ipRetryBase: time.Millisecond,
				inspectFn: func(_ context.Context, _ string) ([]byte, error) {
					calls++
					return build(""), nil
				},
			}
			_, err := mgr.getContainerIP(context.Background(), "moat-postgres-run_x")
			require.Error(t, err)
			assert.Equal(t, containerIPMaxAttempts, calls)
		})
	}
}

func TestGetContainerIPRetriesTransientInspectError(t *testing.T) {
	for name, build := range ipInspectSchemas {
		t.Run(name, func(t *testing.T) {
			calls := 0
			mgr := &appleServiceManager{
				ipRetryBase: time.Millisecond,
				inspectFn: func(_ context.Context, _ string) ([]byte, error) {
					calls++
					if calls < 2 {
						return nil, errors.New("exit status 1")
					}
					return build("192.168.81.2/24"), nil
				},
			}
			addr, err := mgr.getContainerIP(context.Background(), "moat-postgres-run_x")
			require.NoError(t, err)
			assert.Equal(t, "192.168.81.2", addr)
		})
	}
}

func TestAppleBuildRunArgsWithMemory(t *testing.T) {
	cfg := ServiceConfig{
		Name:     "ollama",
		Version:  "0.18.1",
		Image:    "ollama/ollama",
		Env:      map[string]string{},
		MemoryMB: 2048,
	}

	args := buildAppleRunArgs(cfg, "")
	for i, a := range args {
		if a == "--memory" && i+1 < len(args) {
			assert.Equal(t, "2048MB", args[i+1])
			return
		}
	}
	t.Fatal("--memory flag not found in args")
}

func TestAppleBuildRunArgsNoMemoryByDefault(t *testing.T) {
	cfg := ServiceConfig{
		Name:    "ollama",
		Version: "0.18.1",
		Image:   "ollama/ollama",
		Env:     map[string]string{},
	}

	args := buildAppleRunArgs(cfg, "")
	assert.NotContains(t, args, "--memory")
}

func TestAppleBuildRunArgsWithCachePath(t *testing.T) {
	cfg := ServiceConfig{
		Name:          "ollama",
		Version:       "0.18.1",
		Image:         "ollama/ollama",
		Env:           map[string]string{},
		RunID:         "test-run-789",
		CachePath:     "/root/.ollama",
		CacheHostPath: "/tmp/test-cache/ollama",
	}

	args := buildAppleRunArgs(cfg, "moat-test-net")
	assert.Contains(t, args, "--volume")
	for i, a := range args {
		if a == "--volume" && i+1 < len(args) {
			assert.Equal(t, "/tmp/test-cache/ollama:/root/.ollama", args[i+1])
			return
		}
	}
	t.Fatal("--volume flag not found in args")
}
