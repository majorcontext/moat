package run

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateServiceEnvPostgres(t *testing.T) {
	spec, ok := deps.GetSpec("postgres")
	require.True(t, ok)

	info := container.ServiceInfo{
		Name:  "postgres",
		Host:  "postgres",
		Ports: map[string]int{"default": 5432},
		Env:   map[string]string{"POSTGRES_PASSWORD": "secretpw"},
	}

	env := generateServiceEnv(spec.Service, info, nil)

	assert.Equal(t, "postgres", env["MOAT_POSTGRES_HOST"])
	assert.Equal(t, "5432", env["MOAT_POSTGRES_PORT"])
	assert.Equal(t, "postgres", env["MOAT_POSTGRES_USER"])
	assert.Equal(t, "postgres", env["MOAT_POSTGRES_DB"])
	assert.Equal(t, "secretpw", env["MOAT_POSTGRES_PASSWORD"])
	assert.Equal(t, "postgresql://postgres:secretpw@postgres:5432/postgres", env["MOAT_POSTGRES_URL"])
}

func TestGenerateServiceEnvRedis(t *testing.T) {
	spec, ok := deps.GetSpec("redis")
	require.True(t, ok)

	info := container.ServiceInfo{
		Name:  "redis",
		Host:  "redis",
		Ports: map[string]int{"default": 6379},
		Env:   map[string]string{"password": "redispw"},
	}

	env := generateServiceEnv(spec.Service, info, nil)

	assert.Equal(t, "redis", env["MOAT_REDIS_HOST"])
	assert.Equal(t, "6379", env["MOAT_REDIS_PORT"])
	assert.Equal(t, "redispw", env["MOAT_REDIS_PASSWORD"])
	assert.Equal(t, "redis://:redispw@redis:6379", env["MOAT_REDIS_URL"])
}

func TestGenerateServiceEnvMultiPort(t *testing.T) {
	def := &deps.ServiceDef{
		Ports:     map[string]int{"http": 9200, "transport": 9300},
		EnvPrefix: "ELASTICSEARCH",
	}

	info := container.ServiceInfo{
		Host:  "elasticsearch",
		Ports: map[string]int{"http": 9200, "transport": 9300},
		Env:   map[string]string{},
	}

	env := generateServiceEnv(def, info, nil)

	assert.Equal(t, "9200", env["MOAT_ELASTICSEARCH_HTTP_PORT"])
	assert.Equal(t, "9300", env["MOAT_ELASTICSEARCH_TRANSPORT_PORT"])
}

func TestGenerateServiceEnvWithUserOverride(t *testing.T) {
	spec, ok := deps.GetSpec("postgres")
	require.True(t, ok)

	info := container.ServiceInfo{
		Name:  "postgres",
		Host:  "postgres",
		Ports: map[string]int{"default": 5432},
		Env:   map[string]string{"POSTGRES_PASSWORD": "secretpw"},
	}

	userSpec := &config.ServiceSpec{
		Env: map[string]string{"POSTGRES_DB": "myapp"},
	}

	env := generateServiceEnv(spec.Service, info, userSpec)
	assert.Equal(t, "myapp", env["MOAT_POSTGRES_DB"])
	assert.Contains(t, env["MOAT_POSTGRES_URL"], "myapp")
}

func TestGeneratePassword(t *testing.T) {
	pw, err := generatePassword()
	require.NoError(t, err)
	assert.Len(t, pw, 32)

	pw2, err := generatePassword()
	require.NoError(t, err)
	assert.NotEqual(t, pw, pw2)
}

func TestBuildServiceConfig(t *testing.T) {
	dep := deps.Dependency{Name: "postgres", Version: "17", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-123", nil)
	require.NoError(t, err)

	assert.Equal(t, "postgres", cfg.Name)
	assert.Equal(t, "17", cfg.Version)
	assert.Equal(t, "run-123", cfg.RunID)
	assert.Equal(t, "postgres", cfg.Image)
	assert.Equal(t, 5432, cfg.Ports["default"])
	assert.Equal(t, "POSTGRES_PASSWORD", cfg.PasswordEnv)
	assert.NotEmpty(t, cfg.Env["POSTGRES_PASSWORD"]) // auto-generated password
	assert.Len(t, cfg.Env["POSTGRES_PASSWORD"], 32)
}

func TestBuildServiceConfigRedis(t *testing.T) {
	dep := deps.Dependency{Name: "redis", Version: "7", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-456", nil)
	require.NoError(t, err)

	assert.Equal(t, "redis", cfg.Image)
	assert.NotEmpty(t, cfg.Env["password"]) // redis uses "password" key
	assert.Equal(t, []string{"--requirepass", "{password}"}, cfg.ExtraCmd)
}

func TestBuildServiceConfigMysql(t *testing.T) {
	dep := deps.Dependency{Name: "mysql", Version: "8", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-789", nil)
	require.NoError(t, err)

	assert.Equal(t, "mysql", cfg.Image)
	assert.NotEmpty(t, cfg.Env["MYSQL_ROOT_PASSWORD"])
	assert.Equal(t, "moat", cfg.Env["MYSQL_DATABASE"]) // from extra_env
}

func TestBuildServiceConfigUnknown(t *testing.T) {
	dep := deps.Dependency{Name: "unknown", Version: "1", Type: deps.TypeService}

	_, err := buildServiceConfig(dep, "run-000", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown service")
}
