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

func TestBuildServiceConfigOllama(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	userSpec := &config.ServiceSpec{
		Extra: map[string][]string{
			"models": {"qwen2.5-coder:7b", "nomic-embed-text"},
		},
	}

	cfg, err := buildServiceConfig(dep, "run-ollama", userSpec)
	require.NoError(t, err)

	assert.Equal(t, "ollama", cfg.Name)
	assert.Equal(t, "0.18.1", cfg.Version)
	assert.Equal(t, "ollama/ollama", cfg.Image)
	assert.Equal(t, 11434, cfg.Ports["default"])
	assert.Equal(t, "/root/.ollama", cfg.CachePath)
	assert.Equal(t, "ollama pull {item}", cfg.ProvisionCmd)
	assert.Equal(t, []string{"qwen2.5-coder:7b", "nomic-embed-text"}, cfg.Provisions)

	// Ollama has no auth — no password should be set
	assert.Empty(t, cfg.Env)
	assert.Empty(t, cfg.PasswordEnv)
}

func TestBuildServiceConfigOllamaNoModels(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-ollama", nil)
	require.NoError(t, err)

	assert.Empty(t, cfg.Provisions)
	assert.Equal(t, "ollama pull {item}", cfg.ProvisionCmd)
}

func TestBuildServiceConfigMemory(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-test", &config.ServiceSpec{Memory: 2048})
	require.NoError(t, err)
	assert.Equal(t, 2048, cfg.MemoryMB)
}

func TestBuildServiceConfigMemoryDefault(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-test", nil)
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.MemoryMB, "zero means runtime default")
}

func TestBuildServiceConfigNoPasswordForNoAuth(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-test", nil)
	require.NoError(t, err)

	_, hasPassword := cfg.Env["password"]
	assert.False(t, hasPassword, "should not set phantom password for no-auth services")
}

func TestBuildServiceConfigPostgresStillHasPassword(t *testing.T) {
	dep := deps.Dependency{Name: "postgres", Version: "17", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-pg", nil)
	require.NoError(t, err)

	assert.NotEmpty(t, cfg.Env["POSTGRES_PASSWORD"], "postgres should still get a password")
}

func TestBuildServiceConfigValidatesProvisionsKey(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	userSpec := &config.ServiceSpec{
		Extra: map[string][]string{
			"model": {"qwen2.5-coder:7b"},
		},
	}

	_, err := buildServiceConfig(dep, "run-ollama", userSpec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model")
}

func TestBuildServiceConfigRejectsScalarProvisions(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	userSpec := &config.ServiceSpec{
		Extra: map[string][]string{
			"models": nil, // scalar value captured as nil by UnmarshalYAML
		},
	}

	_, err := buildServiceConfig(dep, "run-ollama", userSpec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a list")
}

func TestBuildServiceConfigRejectsExtraKeysOnNonProvisionService(t *testing.T) {
	dep := deps.Dependency{Name: "postgres", Version: "17", Type: deps.TypeService}

	userSpec := &config.ServiceSpec{
		Extra: map[string][]string{
			"plugins": {"pg_trgm"},
		},
	}

	_, err := buildServiceConfig(dep, "run-pg", userSpec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plugins")
	assert.Contains(t, err.Error(), "not a valid")
}

func TestBuildServiceConfigRejectsShellInjection(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	tests := []struct {
		name  string
		model string
	}{
		{"semicolon", "foo; rm -rf /"},
		{"pipe", "foo | cat /etc/passwd"},
		{"dollar", "foo$(whoami)"},
		{"backtick", "foo`whoami`"},
		{"ampersand", "foo && echo pwned"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userSpec := &config.ServiceSpec{
				Extra: map[string][]string{
					"models": {tt.model},
				},
			}
			_, err := buildServiceConfig(dep, "run-test", userSpec)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "disallowed characters")
		})
	}
}

func TestBuildServiceConfigAcceptsValidModelNames(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	validModels := []string{
		"qwen2.5-coder:7b",
		"nomic-embed-text",
		"llama3.1:70b",
		"library/model:latest",
		"user/repo:v1.2.3",
	}

	userSpec := &config.ServiceSpec{
		Extra: map[string][]string{
			"models": validModels,
		},
	}

	cfg, err := buildServiceConfig(dep, "run-test", userSpec)
	require.NoError(t, err)
	assert.Equal(t, validModels, cfg.Provisions)
}

func TestBuildServiceConfigOllamaProvisionsIncompatibleWithWaitFalse(t *testing.T) {
	// Validates the fields the manager's wait:false guard checks.
	// The guard rejects ProvisionCmd != "" && len(Provisions) > 0 when wait: false.
	dep := deps.Dependency{Name: "ollama", Version: "0.18.1", Type: deps.TypeService}

	userSpec := &config.ServiceSpec{
		Extra: map[string][]string{
			"models": {"qwen2.5-coder:7b"},
		},
	}

	cfg, err := buildServiceConfig(dep, "run-test", userSpec)
	require.NoError(t, err)

	// These are the exact conditions the wait:false guard checks
	assert.NotEmpty(t, cfg.ProvisionCmd, "ProvisionCmd must be set for guard to trigger")
	assert.NotEmpty(t, cfg.Provisions, "Provisions must be set for guard to trigger")

	// Without provisions, the guard should not trigger
	cfgNoProv, err := buildServiceConfig(dep, "run-test", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, cfgNoProv.ProvisionCmd, "ProvisionCmd is set even without user models")
	assert.Empty(t, cfgNoProv.Provisions, "No provisions when user doesn't specify models")
}

func TestBuildProvisionCmds(t *testing.T) {
	cmds := buildProvisionCmds("ollama pull {item}", []string{"qwen2.5-coder:7b", "nomic-embed-text"})
	assert.Equal(t, []string{"ollama pull qwen2.5-coder:7b", "ollama pull nomic-embed-text"}, cmds)
}

func TestBuildProvisionCmdsEmpty(t *testing.T) {
	cmds := buildProvisionCmds("ollama pull {item}", nil)
	assert.Empty(t, cmds)
}

func TestGenerateServiceEnvOllama(t *testing.T) {
	spec, ok := deps.GetSpec("ollama")
	require.True(t, ok)

	info := container.ServiceInfo{
		Name:  "ollama",
		Host:  "ollama",
		Ports: map[string]int{"default": 11434},
		Env:   map[string]string{},
	}

	env := generateServiceEnv(spec.Service, info, nil)

	assert.Equal(t, "ollama", env["MOAT_OLLAMA_HOST"])
	assert.Equal(t, "11434", env["MOAT_OLLAMA_PORT"])
	assert.Equal(t, "http://ollama:11434", env["MOAT_OLLAMA_URL"])

	// No auth — should not have password, user, or db
	_, hasPassword := env["MOAT_OLLAMA_PASSWORD"]
	assert.False(t, hasPassword, "should not inject password for no-auth services")
	_, hasUser := env["MOAT_OLLAMA_USER"]
	assert.False(t, hasUser)
	_, hasDB := env["MOAT_OLLAMA_DB"]
	assert.False(t, hasDB)
}
