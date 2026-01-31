package container

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildServiceInfo(t *testing.T) {
	cfg := ServiceConfig{
		Name:         "postgres",
		Version:      "17",
		Image:        "postgres",
		Ports:        map[string]int{"default": 5432},
		Env:          map[string]string{"POSTGRES_PASSWORD": "testpass"},
		RunID:        "test-run-123",
		ReadinessCmd: "pg_isready",
		PasswordEnv:  "POSTGRES_PASSWORD",
	}

	info := buildServiceInfo("container-abc", cfg, "postgres")
	assert.Equal(t, "container-abc", info.ID)
	assert.Equal(t, "postgres", info.Name)
	assert.Equal(t, "postgres", info.Host)

	assert.Equal(t, 5432, info.Ports["default"])
	assert.Equal(t, "testpass", info.Env["POSTGRES_PASSWORD"])
	assert.Equal(t, "pg_isready", info.ReadinessCmd)
	assert.Equal(t, "POSTGRES_PASSWORD", info.PasswordEnv)
}

func TestResolvePlaceholders(t *testing.T) {
	// Postgres - uses PasswordEnv to alias {password}
	cmd := resolvePlaceholders("pg_isready -U {password}", map[string]string{"POSTGRES_PASSWORD": "pw123"}, "POSTGRES_PASSWORD")
	assert.Equal(t, "pg_isready -U pw123", cmd)

	// Redis - uses "password" key directly (lowercase match)
	cmd = resolvePlaceholders("redis-cli -a {password} PING", map[string]string{"password": "redispw"}, "")
	assert.Equal(t, "redis-cli -a redispw PING", cmd)

	// MySQL - uses PasswordEnv for {password} alias
	cmd = resolvePlaceholders("mysqladmin ping --password={password}", map[string]string{"MYSQL_ROOT_PASSWORD": "mysqlpw"}, "MYSQL_ROOT_PASSWORD")
	assert.Equal(t, "mysqladmin ping --password=mysqlpw", cmd)

	// Generic env substitution via lowercase keys
	cmd = resolvePlaceholders("connect -h {my_host} -p {my_port}", map[string]string{"MY_HOST": "localhost", "MY_PORT": "5432"}, "")
	assert.Equal(t, "connect -h localhost -p 5432", cmd)
}
