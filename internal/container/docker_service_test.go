package container

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSidecarConfigPostgres(t *testing.T) {
	cfg := ServiceConfig{
		Name:    "postgres",
		Version: "17",
		Image:   "postgres",
		Ports:   map[string]int{"default": 5432},
		Env:     map[string]string{"POSTGRES_PASSWORD": "testpass"},
		RunID:   "test-run-123",
	}

	sidecarCfg := buildSidecarConfig(cfg, "net-123")
	assert.Equal(t, "postgres:17", sidecarCfg.Image)
	assert.Equal(t, "moat-postgres-test-run-123", sidecarCfg.Name)
	assert.Equal(t, "postgres", sidecarCfg.Hostname)
	assert.Equal(t, "net-123", sidecarCfg.NetworkID)
	assert.Equal(t, "test-run-123", sidecarCfg.RunID)
	assert.Contains(t, sidecarCfg.Env, "POSTGRES_PASSWORD=testpass")
	assert.Equal(t, "service", sidecarCfg.Labels["moat.role"])
}

func TestBuildSidecarConfigRedis(t *testing.T) {
	cfg := ServiceConfig{
		Name:     "redis",
		Version:  "7",
		Image:    "redis",
		Ports:    map[string]int{"default": 6379},
		Env:      map[string]string{"password": "redispass"},
		ExtraCmd: []string{"--requirepass", "{password}"},
		RunID:    "test-run-456",
	}

	sidecarCfg := buildSidecarConfig(cfg, "net-456")
	assert.Equal(t, "redis:7", sidecarCfg.Image)
	assert.Equal(t, []string{"--requirepass", "redispass"}, sidecarCfg.Cmd)
}

func TestBuildSidecarConfigWithCachePath(t *testing.T) {
	cfg := ServiceConfig{
		Name:          "ollama",
		Version:       "0.18.1",
		Image:         "ollama/ollama",
		Ports:         map[string]int{"default": 11434},
		Env:           map[string]string{},
		RunID:         "test-run-789",
		CachePath:     "/root/.ollama",
		CacheHostPath: "/tmp/test-cache/ollama",
	}

	sidecarCfg := buildSidecarConfig(cfg, "net-789")
	assert.Equal(t, "ollama/ollama:0.18.1", sidecarCfg.Image)
	assert.Equal(t, "moat-ollama-test-run-789", sidecarCfg.Name)

	require.Len(t, sidecarCfg.Mounts, 1)
	assert.Equal(t, "/tmp/test-cache/ollama", sidecarCfg.Mounts[0].Source)
	assert.Equal(t, "/root/.ollama", sidecarCfg.Mounts[0].Target)
	assert.False(t, sidecarCfg.Mounts[0].ReadOnly)
}

func TestBuildSidecarConfigNoCachePath(t *testing.T) {
	cfg := ServiceConfig{
		Name:    "postgres",
		Version: "17",
		Image:   "postgres",
		Env:     map[string]string{},
		RunID:   "test-run-000",
	}

	sidecarCfg := buildSidecarConfig(cfg, "net-000")
	assert.Empty(t, sidecarCfg.Mounts)
}
