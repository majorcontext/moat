package container

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
