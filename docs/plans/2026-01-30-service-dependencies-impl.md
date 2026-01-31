# Service Dependencies Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add runtime-agnostic service dependencies (postgres, mysql, redis) to Moat so agents get zero-config ephemeral databases.

**Architecture:** Extend the existing dependency registry with `type: service` entries containing metadata (image, ports, env_prefix, readiness_cmd, url_format). A new `ServiceManager` interface on `Runtime` abstracts provisioning — Docker composes the existing `SidecarManager`, Apple returns nil. Run manager orchestrates startup/readiness/cleanup using registry-driven logic.

**Tech Stack:** Go, Docker API (via existing container abstractions), YAML registry

**Design Doc:** `docs/plans/2026-01-30-service-dependencies-design.md`

---

### Task 1: Add `TypeService` and `ServiceDef` to dependency types

**Files:**
- Modify: `internal/deps/types.go`
- Test: `internal/deps/types_test.go` (if exists, otherwise `internal/deps/registry_test.go`)

**Step 1: Write the failing test**

Add to `internal/deps/registry_test.go`:

```go
func TestServiceDepSpec(t *testing.T) {
	spec, ok := GetSpec("postgres")
	require.True(t, ok)
	assert.Equal(t, TypeService, spec.Type)
	assert.NotNil(t, spec.Service)
	assert.Equal(t, "postgres", spec.Service.Image)
	assert.Equal(t, 5432, spec.Service.Ports["default"])
	assert.Equal(t, "POSTGRES", spec.Service.EnvPrefix)
	assert.Equal(t, "pg_isready -h localhost -U postgres", spec.Service.ReadinessCmd)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestServiceDepSpec ./internal/deps/ -v`
Expected: FAIL — `TypeService` undefined

**Step 3: Add `TypeService` constant and `ServiceDef` struct**

In `internal/deps/types.go`, add to the `InstallType` constants:

```go
TypeService InstallType = "service"
```

Add the `ServiceDef` struct:

```go
// ServiceDef holds metadata for service-type dependencies (databases, caches).
// Parsed from the `service:` block in registry.yaml entries.
type ServiceDef struct {
	Image        string            `yaml:"image"`
	Ports        map[string]int    `yaml:"ports"`
	EnvPrefix    string            `yaml:"env_prefix"`
	DefaultUser  string            `yaml:"default_user,omitempty"`
	DefaultDB    string            `yaml:"default_db,omitempty"`
	PasswordEnv  string            `yaml:"password_env,omitempty"`
	ExtraEnv     map[string]string `yaml:"extra_env,omitempty"`
	ExtraCmd     []string          `yaml:"extra_cmd,omitempty"`
	ReadinessCmd string            `yaml:"readiness_cmd"`
	URLScheme    string            `yaml:"url_scheme"`
	URLFormat    string            `yaml:"url_format"`
}
```

Add `Service` field to `DepSpec`:

```go
Service *ServiceDef `yaml:"service,omitempty"`
```

**Step 4: Run test — still fails (no registry entry yet)**

Run: `go test -run TestServiceDepSpec ./internal/deps/ -v`
Expected: FAIL — `GetSpec("postgres")` returns false

**Step 5: Add service entries to registry.yaml**

Add to `internal/deps/registry.yaml`:

```yaml
postgres:
  description: PostgreSQL database
  type: service
  default: "17"
  versions: ["15", "16", "17"]
  service:
    image: "postgres"
    ports:
      default: 5432
    env_prefix: POSTGRES
    default_user: postgres
    default_db: postgres
    password_env: POSTGRES_PASSWORD
    readiness_cmd: "pg_isready -h localhost -U postgres"
    url_scheme: "postgresql"
    url_format: "{scheme}://{user}:{password}@{host}:{port}/{db}"

mysql:
  description: MySQL database
  type: service
  default: "8"
  versions: ["8", "9"]
  service:
    image: "mysql"
    ports:
      default: 3306
    env_prefix: MYSQL
    default_user: root
    default_db: moat
    password_env: MYSQL_ROOT_PASSWORD
    extra_env:
      MYSQL_DATABASE: "{db}"
    readiness_cmd: "mysqladmin ping -h localhost -u root --password={password}"
    url_scheme: "mysql"
    url_format: "{scheme}://{user}:{password}@{host}:{port}/{db}"

redis:
  description: Redis key-value store
  type: service
  default: "7"
  versions: ["6", "7"]
  service:
    image: "redis"
    ports:
      default: 6379
    env_prefix: REDIS
    password_env: ""
    extra_cmd: ["--requirepass", "{password}"]
    readiness_cmd: "redis-cli -a {password} PING"
    url_scheme: "redis"
    url_format: "{scheme}://:{password}@{host}:{port}"
```

**Step 6: Run test to verify it passes**

Run: `go test -run TestServiceDepSpec ./internal/deps/ -v`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/deps/types.go internal/deps/registry.yaml internal/deps/registry_test.go
git commit -m "feat(deps): add TypeService and ServiceDef for service dependencies"
```

---

### Task 2: Add `FilterServices` and `FilterInstallable` helpers

**Files:**
- Create: `internal/deps/filter.go`
- Create: `internal/deps/filter_test.go`

**Step 1: Write the failing tests**

Create `internal/deps/filter_test.go`:

```go
package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterServices(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "22", Type: TypeRuntime},
		{Name: "postgres", Version: "17", Type: TypeService},
		{Name: "redis", Version: "7", Type: TypeService},
	}

	services := FilterServices(deps)
	assert.Len(t, services, 2)
	assert.Equal(t, "postgres", services[0].Name)
	assert.Equal(t, "redis", services[1].Name)
}

func TestFilterInstallable(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "22", Type: TypeRuntime},
		{Name: "postgres", Version: "17", Type: TypeService},
		{Name: "redis", Version: "7", Type: TypeService},
	}

	installable := FilterInstallable(deps)
	assert.Len(t, installable, 1)
	assert.Equal(t, "node", installable[0].Name)
}

func TestFilterServicesEmpty(t *testing.T) {
	deps := []Dependency{
		{Name: "node", Version: "22", Type: TypeRuntime},
	}

	services := FilterServices(deps)
	assert.Empty(t, services)

	installable := FilterInstallable(deps)
	assert.Len(t, installable, 1)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestFilter ./internal/deps/ -v`
Expected: FAIL — `FilterServices` undefined

**Step 3: Implement the filters**

Create `internal/deps/filter.go`:

```go
package deps

// FilterServices returns only service-type dependencies.
func FilterServices(deps []Dependency) []Dependency {
	var result []Dependency
	for _, d := range deps {
		if d.Type == TypeService {
			result = append(result, d)
		}
	}
	return result
}

// FilterInstallable returns dependencies excluding services.
func FilterInstallable(deps []Dependency) []Dependency {
	var result []Dependency
	for _, d := range deps {
		if d.Type != TypeService {
			result = append(result, d)
		}
	}
	return result
}
```

**Step 4: Run test to verify it passes**

Run: `go test -run TestFilter ./internal/deps/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/deps/filter.go internal/deps/filter_test.go
git commit -m "feat(deps): add FilterServices and FilterInstallable helpers"
```

---

### Task 3: Add `ServiceManager` interface to runtime

**Files:**
- Modify: `internal/container/runtime.go`

**Step 1: Add the interface and types**

Add to `internal/container/runtime.go` after the existing `SidecarManager` interface:

```go
// ServiceManager provisions services (databases, caches, etc).
// Returned by Runtime.ServiceManager() - nil if not supported.
type ServiceManager interface {
	StartService(ctx context.Context, cfg ServiceConfig) (ServiceInfo, error)
	CheckReady(ctx context.Context, info ServiceInfo) error
	StopService(ctx context.Context, info ServiceInfo) error
}

// ServiceConfig defines what service to provision.
type ServiceConfig struct {
	Name    string
	Version string
	Env     map[string]string
	RunID   string
}

// ServiceInfo contains connection details for a started service.
type ServiceInfo struct {
	ID    string
	Name  string
	Host  string
	Ports map[string]int
	Env   map[string]string
}
```

Add `ServiceManager()` method to the `Runtime` interface:

```go
ServiceManager() ServiceManager
```

**Step 2: Verify it compiles**

Run: `go build ./internal/container/...`
Expected: FAIL — `AppleRuntime` and `DockerRuntime` don't implement `ServiceManager()` yet

**Step 3: Add nil stub to Apple runtime**

In `internal/container/apple.go`, add:

```go
// ServiceManager returns nil - Apple containers don't support service dependencies.
func (r *AppleRuntime) ServiceManager() ServiceManager {
	return nil
}
```

**Step 4: Add nil stub to Docker runtime (temporary)**

In `internal/container/docker.go`, add:

```go
// ServiceManager returns the Docker service manager for database/cache sidecars.
func (r *DockerRuntime) ServiceManager() ServiceManager {
	return nil // TODO: implement in Task 4
}
```

**Step 5: Verify it compiles**

Run: `go build ./...`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/container/runtime.go internal/container/apple.go internal/container/docker.go
git commit -m "feat(container): add ServiceManager interface to Runtime"
```

---

### Task 4: Implement `dockerServiceManager`

**Files:**
- Create: `internal/container/docker_service.go`
- Create: `internal/container/docker_service_test.go`
- Modify: `internal/container/docker.go` (update `ServiceManager()` return)

**Step 1: Write tests for dockerServiceManager**

Create `internal/container/docker_service_test.go`:

```go
package container

import (
	"testing"

	"github.com/anthropics/moat/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSidecarConfigFromService(t *testing.T) {
	spec, ok := deps.GetSpec("postgres")
	require.True(t, ok)
	require.NotNil(t, spec.Service)

	cfg := ServiceConfig{
		Name:    "postgres",
		Version: "17",
		Env:     map[string]string{"POSTGRES_PASSWORD": "testpass"},
		RunID:   "test-run-123",
	}

	sidecarCfg := buildSidecarConfig(cfg, spec.Service, "net-123")
	assert.Equal(t, "postgres:17", sidecarCfg.Image)
	assert.Equal(t, "moat-postgres-test-run-123", sidecarCfg.Name)
	assert.Equal(t, "postgres", sidecarCfg.Hostname)
	assert.Equal(t, "net-123", sidecarCfg.NetworkID)
	assert.Equal(t, "test-run-123", sidecarCfg.RunID)
	assert.Contains(t, sidecarCfg.Env, "POSTGRES_PASSWORD=testpass")
}

func TestBuildSidecarConfigRedis(t *testing.T) {
	spec, ok := deps.GetSpec("redis")
	require.True(t, ok)
	require.NotNil(t, spec.Service)

	cfg := ServiceConfig{
		Name:    "redis",
		Version: "7",
		Env:     map[string]string{},
		RunID:   "test-run-456",
	}

	sidecarCfg := buildSidecarConfig(cfg, spec.Service, "net-456")
	assert.Equal(t, "redis:7", sidecarCfg.Image)
	// Redis uses extra_cmd, no password_env
	assert.Equal(t, []string{"--requirepass", "{password}"}, spec.Service.ExtraCmd)
}

func TestBuildServiceInfo(t *testing.T) {
	spec, ok := deps.GetSpec("postgres")
	require.True(t, ok)

	info := buildServiceInfo("container-abc", "postgres", spec.Service, map[string]string{"POSTGRES_PASSWORD": "testpass"})
	assert.Equal(t, "container-abc", info.ID)
	assert.Equal(t, "postgres", info.Name)
	assert.Equal(t, "postgres", info.Host)
	assert.Equal(t, 5432, info.Ports["default"])
	assert.Equal(t, "testpass", info.Env["POSTGRES_PASSWORD"])
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestBuildSidecar ./internal/container/ -v`
Expected: FAIL — functions undefined

**Step 3: Implement dockerServiceManager**

Create `internal/container/docker_service.go`:

```go
package container

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/moat/internal/deps"
	"github.com/docker/docker/client"
)

// dockerServiceManager provisions database/cache services as Docker sidecar containers.
type dockerServiceManager struct {
	sidecar   SidecarManager
	network   NetworkManager
	cli       *client.Client
	networkID string // Set externally when network is created
}

func (m *dockerServiceManager) StartService(ctx context.Context, cfg ServiceConfig) (ServiceInfo, error) {
	spec, ok := deps.GetSpec(cfg.Name)
	if !ok {
		return ServiceInfo{}, fmt.Errorf("unknown service: %s", cfg.Name)
	}
	if spec.Service == nil {
		return ServiceInfo{}, fmt.Errorf("%s is not a service dependency", cfg.Name)
	}

	sidecarCfg := buildSidecarConfig(cfg, spec.Service, m.networkID)

	containerID, err := m.sidecar.StartSidecar(ctx, sidecarCfg)
	if err != nil {
		return ServiceInfo{}, fmt.Errorf("starting %s service: %w", cfg.Name, err)
	}

	return buildServiceInfo(containerID, cfg.Name, spec.Service, cfg.Env), nil
}

func (m *dockerServiceManager) CheckReady(ctx context.Context, info ServiceInfo) error {
	spec, ok := deps.GetSpec(info.Name)
	if !ok || spec.Service == nil {
		return fmt.Errorf("unknown service: %s", info.Name)
	}

	cmd := spec.Service.ReadinessCmd
	// Substitute placeholders from env
	for k, v := range info.Env {
		placeholder := "{" + strings.ToLower(k) + "}"
		cmd = strings.ReplaceAll(cmd, placeholder, v)
	}
	// Also substitute {password} from common password env vars
	if pw, ok := info.Env[spec.Service.PasswordEnv]; ok && spec.Service.PasswordEnv != "" {
		cmd = strings.ReplaceAll(cmd, "{password}", pw)
	}
	// For redis which has no PasswordEnv, check for password in env
	if pw, ok := info.Env["password"]; ok {
		cmd = strings.ReplaceAll(cmd, "{password}", pw)
	}

	execCfg := ExecConfig{
		ContainerID: info.ID,
		Cmd:         []string{"sh", "-c", cmd},
	}

	return m.exec(ctx, execCfg)
}

func (m *dockerServiceManager) StopService(ctx context.Context, info ServiceInfo) error {
	return m.cli.ContainerRemove(ctx, info.ID, containerRemoveOptions())
}

// exec runs a command inside a container and returns an error if it fails.
func (m *dockerServiceManager) exec(ctx context.Context, cfg ExecConfig) error {
	execID, err := m.cli.ContainerExecCreate(ctx, cfg.ContainerID, containerExecConfig(cfg.Cmd))
	if err != nil {
		return err
	}
	return m.cli.ContainerExecStart(ctx, execID.ID, containerExecStartConfig())
}

// buildSidecarConfig translates a ServiceConfig into a SidecarConfig using registry metadata.
func buildSidecarConfig(cfg ServiceConfig, def *deps.ServiceDef, networkID string) SidecarConfig {
	image := def.Image + ":" + cfg.Version

	var envList []string
	for k, v := range cfg.Env {
		envList = append(envList, k+"="+v)
	}

	sc := SidecarConfig{
		Image:     image,
		Name:      fmt.Sprintf("moat-%s-%s", cfg.Name, cfg.RunID),
		Hostname:  cfg.Name,
		NetworkID: networkID,
		RunID:     cfg.RunID,
		Env:       envList,
		Labels: map[string]string{
			"moat.run-id": cfg.RunID,
			"moat.role":   "service",
		},
	}

	if len(def.ExtraCmd) > 0 {
		// Substitute placeholders in extra_cmd
		cmds := make([]string, len(def.ExtraCmd))
		for i, c := range def.ExtraCmd {
			cmds[i] = c
			for k, v := range cfg.Env {
				cmds[i] = strings.ReplaceAll(cmds[i], "{"+strings.ToLower(k)+"}", v)
			}
		}
		sc.Cmd = cmds
	}

	return sc
}

// buildServiceInfo creates a ServiceInfo from container details and registry metadata.
func buildServiceInfo(containerID, name string, def *deps.ServiceDef, env map[string]string) ServiceInfo {
	return ServiceInfo{
		ID:    containerID,
		Name:  name,
		Host:  name, // Docker hostname = service name
		Ports: def.Ports,
		Env:   env,
	}
}
```

**Step 4: Check what types need updating**

Before this compiles, `SidecarConfig` needs `Env` and `Labels` fields. Check if they exist — if not, add them to `runtime.go`:

```go
// In SidecarConfig struct, add:
Env    []string          // Environment variables
Labels map[string]string // Container labels
```

Also need `ExecConfig` type and helper functions. Check if `containerRemoveOptions`, `containerExecConfig`, `containerExecStartConfig` exist. If not, use Docker client directly:

```go
type ExecConfig struct {
	ContainerID string
	Cmd         []string
}
```

> **Note to implementer:** Inspect `SidecarConfig` and existing Docker helpers in `docker.go` to see which fields/functions already exist. Adapt the implementation to use what's available. The `StartSidecar` method in `docker.go` (line ~781) shows how containers are created — check if it already handles Env and Labels. If `SidecarConfig` doesn't have Env/Labels, add them and update `StartSidecar` to pass them through.

**Step 5: Update DockerRuntime.ServiceManager() to return real implementation**

In `internal/container/docker.go`, change:

```go
func (r *DockerRuntime) ServiceManager() ServiceManager {
	return &dockerServiceManager{
		sidecar: r.SidecarManager(),
		network: r.NetworkManager(),
		cli:     r.cli,
	}
}
```

**Step 6: Run tests**

Run: `go test -run TestBuild ./internal/container/ -v`
Expected: PASS

Run: `go build ./...`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/container/docker_service.go internal/container/docker_service_test.go internal/container/docker.go internal/container/runtime.go
git commit -m "feat(container): implement dockerServiceManager composing SidecarManager"
```

---

### Task 5: Add `Services` config field and validation

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go` (or create)

**Step 1: Write the failing test**

```go
func TestServicesValidation(t *testing.T) {
	// services key not in dependencies should fail
	cfg := &Config{
		Dependencies: []string{"node@22"},
		Services: map[string]ServiceSpec{
			"postgres": {},
		},
	}
	err := cfg.ValidateServices([]string{"node"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "postgres not declared in dependencies")
}

func TestServicesValidationPass(t *testing.T) {
	cfg := &Config{
		Dependencies: []string{"postgres@17"},
		Services: map[string]ServiceSpec{
			"postgres": {
				Env: map[string]string{"POSTGRES_DB": "myapp"},
			},
		},
	}
	err := cfg.ValidateServices([]string{"postgres"})
	assert.NoError(t, err)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestServicesValidation ./internal/config/ -v`
Expected: FAIL

**Step 3: Add ServiceSpec and Services field**

In `internal/config/config.go`, add to `Config` struct:

```go
Services map[string]ServiceSpec `yaml:"services,omitempty"`
```

Add `ServiceSpec` type:

```go
// ServiceSpec allows customizing service behavior.
type ServiceSpec struct {
	Env   map[string]string `yaml:"env,omitempty"`
	Image string            `yaml:"image,omitempty"`
	Wait  *bool             `yaml:"wait,omitempty"`
}

// ServiceWait returns whether to wait for this service to be ready (default: true).
func (s ServiceSpec) ServiceWait() bool {
	if s.Wait == nil {
		return true
	}
	return *s.Wait
}
```

Add validation method:

```go
// ValidateServices checks that services: keys correspond to declared service dependencies.
func (c *Config) ValidateServices(serviceNames []string) error {
	nameSet := make(map[string]bool, len(serviceNames))
	for _, n := range serviceNames {
		nameSet[n] = true
	}
	for name := range c.Services {
		if !nameSet[name] {
			return fmt.Errorf("services.%s configured but %s not declared in dependencies\n\nAdd to dependencies:\n  dependencies:\n    - %s", name, name, name)
		}
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test -run TestServicesValidation ./internal/config/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add Services field and validation"
```

---

### Task 6: Add `ServiceContainers` to storage metadata

**Files:**
- Modify: `internal/storage/storage.go`

**Step 1: Add the field**

In `internal/storage/storage.go`, add to `Metadata` struct:

```go
ServiceContainers map[string]string `json:"service_containers,omitempty"` // service name -> container ID
```

**Step 2: Verify it compiles**

Run: `go build ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/storage/storage.go
git commit -m "feat(storage): add ServiceContainers to run metadata"
```

---

### Task 7: Implement service orchestration logic

**Files:**
- Create: `internal/run/services.go`
- Create: `internal/run/services_test.go`

**Step 1: Write tests for env generation and password generation**

Create `internal/run/services_test.go`:

```go
package run

import (
	"testing"

	"github.com/anthropics/moat/internal/container"
	"github.com/anthropics/moat/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateServiceEnv(t *testing.T) {
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
	assert.Contains(t, env["MOAT_POSTGRES_URL"], "postgresql://postgres:secretpw@postgres:5432/postgres")
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
	assert.Contains(t, env["MOAT_REDIS_URL"], "redis://:redispw@redis:6379")
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

func TestGeneratePassword(t *testing.T) {
	pw, err := generatePassword()
	require.NoError(t, err)
	assert.Len(t, pw, 32)

	pw2, err := generatePassword()
	require.NoError(t, err)
	assert.NotEqual(t, pw, pw2)
}

func TestValidateServiceConfig(t *testing.T) {
	serviceNames := []string{"postgres", "redis"}

	err := validateServiceConfig(map[string]interface{}{
		"postgres": nil,
		"mysql":    nil,
	}, serviceNames)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mysql")
}
```

> **Note to implementer:** Adjust `validateServiceConfig` parameter types to match actual config types.

**Step 2: Run tests to verify they fail**

Run: `go test -run TestGenerate ./internal/run/ -v`
Expected: FAIL

**Step 3: Implement service orchestration**

Create `internal/run/services.go`:

```go
package run

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/anthropics/moat/internal/config"
	"github.com/anthropics/moat/internal/container"
	"github.com/anthropics/moat/internal/deps"
)

const passwordLength = 32
const passwordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// generatePassword creates a cryptographically random alphanumeric password.
func generatePassword() (string, error) {
	b := make([]byte, passwordLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordChars))))
		if err != nil {
			return "", fmt.Errorf("generating password: %w", err)
		}
		b[i] = passwordChars[n.Int64()]
	}
	return string(b), nil
}

// generateServiceEnv creates MOAT_* environment variables from service info and registry metadata.
func generateServiceEnv(def *deps.ServiceDef, info container.ServiceInfo, userSpec *config.ServiceSpec) map[string]string {
	prefix := "MOAT_" + def.EnvPrefix
	env := make(map[string]string)

	// Host
	env[prefix+"_HOST"] = info.Host

	// Ports: single-port (key "default") omits port name, multi-port includes it
	for name, port := range info.Ports {
		portStr := strconv.Itoa(port)
		if name == "default" {
			env[prefix+"_PORT"] = portStr
		} else {
			env[prefix+"_"+strings.ToUpper(name)+"_PORT"] = portStr
		}
	}

	// User
	user := def.DefaultUser
	if user != "" {
		env[prefix+"_USER"] = user
	}

	// DB
	db := def.DefaultDB
	if userSpec != nil {
		if v, ok := userSpec.Env["POSTGRES_DB"]; ok {
			db = v
		}
		if v, ok := userSpec.Env["MYSQL_DATABASE"]; ok {
			db = v
		}
	}
	if db != "" {
		env[prefix+"_DB"] = db
	}

	// Password
	password := ""
	if def.PasswordEnv != "" {
		password = info.Env[def.PasswordEnv]
	}
	if password == "" {
		password = info.Env["password"]
	}
	if password != "" {
		env[prefix+"_PASSWORD"] = password
	}

	// URL from template
	if def.URLFormat != "" {
		defaultPort := 0
		if p, ok := info.Ports["default"]; ok {
			defaultPort = p
		}
		url := def.URLFormat
		url = strings.ReplaceAll(url, "{scheme}", def.URLScheme)
		url = strings.ReplaceAll(url, "{user}", user)
		url = strings.ReplaceAll(url, "{password}", password)
		url = strings.ReplaceAll(url, "{host}", info.Host)
		url = strings.ReplaceAll(url, "{port}", strconv.Itoa(defaultPort))
		url = strings.ReplaceAll(url, "{db}", db)
		env[prefix+"_URL"] = url
	}

	return env
}

// buildServiceConfig creates a ServiceConfig for a service dependency.
func buildServiceConfig(dep deps.Dependency, runID string, userSpec *config.ServiceSpec) (container.ServiceConfig, error) {
	spec, ok := deps.GetSpec(dep.Name)
	if !ok || spec.Service == nil {
		return container.ServiceConfig{}, fmt.Errorf("unknown service: %s", dep.Name)
	}

	password, err := generatePassword()
	if err != nil {
		return container.ServiceConfig{}, err
	}

	env := make(map[string]string)

	// Set password
	if spec.Service.PasswordEnv != "" {
		env[spec.Service.PasswordEnv] = password
	} else {
		env["password"] = password
	}

	// Set extra_env from registry with placeholder substitution
	for k, v := range spec.Service.ExtraEnv {
		v = strings.ReplaceAll(v, "{db}", spec.Service.DefaultDB)
		v = strings.ReplaceAll(v, "{password}", password)
		env[k] = v
	}

	// Apply user overrides
	if userSpec != nil {
		for k, v := range userSpec.Env {
			env[k] = v
		}
	}

	return container.ServiceConfig{
		Name:    dep.Name,
		Version: dep.Version,
		Env:     env,
		RunID:   runID,
	}, nil
}
```

**Step 4: Run tests**

Run: `go test -run "TestGenerate|TestValidate" ./internal/run/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/run/services.go internal/run/services_test.go
git commit -m "feat(run): add service orchestration logic (env generation, passwords, config building)"
```

---

### Task 8: Integrate service lifecycle into run manager

**Files:**
- Modify: `internal/run/manager.go`

This is the integration task — wiring services into `Create()`, `Stop()`, and `Destroy()`.

**Step 1: Add service startup to Create()**

In `manager.go`, find where `deps.ParseAll` is called (around line 850-879). After `deps.Validate(depList)` and version resolution:

```go
// Split dependencies into installable and services
serviceDeps := deps.FilterServices(depList)
installableDeps := deps.FilterInstallable(depList)
```

Replace all subsequent uses of `depList` with `installableDeps` for image selection, Dockerfile generation, etc. (especially the `image.Resolve()` call).

Then, after the network creation section (where BuildKit network is created), add service startup:

```go
// Start service dependencies
if len(serviceDeps) > 0 {
	svcMgr := m.runtime.ServiceManager()
	if svcMgr == nil {
		return nil, fmt.Errorf("service dependencies require Docker runtime\nApple containers don't support service dependencies\n\nEither:\n  - Use Docker runtime\n  - Install services on your host and set MOAT_*_URL manually")
	}

	// Validate services config
	serviceNames := make([]string, len(serviceDeps))
	for i, d := range serviceDeps {
		serviceNames[i] = d.Name
	}
	if err := cfg.ValidateServices(serviceNames); err != nil {
		return nil, err
	}

	// Ensure network exists (share with BuildKit if present)
	if r.NetworkID == "" {
		netMgr := m.runtime.NetworkManager()
		if netMgr == nil {
			return nil, fmt.Errorf("services require network support")
		}
		networkName := fmt.Sprintf("moat-%s", r.ID)
		networkID, err := netMgr.CreateNetwork(ctx, networkName)
		if err != nil {
			return nil, fmt.Errorf("creating service network: %w", err)
		}
		r.NetworkID = networkID
	}

	// Set network on service manager
	if dsm, ok := svcMgr.(*dockerServiceManager); ok {
		dsm.networkID = r.NetworkID
	}

	// Start services
	r.ServiceContainers = make(map[string]string)
	var serviceInfos []container.ServiceInfo

	for _, dep := range serviceDeps {
		userSpec := cfg.Services[dep.Name]
		svcCfg, err := buildServiceConfig(dep, r.ID, &userSpec)
		if err != nil {
			// Cleanup already-started services
			for _, info := range serviceInfos {
				_ = svcMgr.StopService(ctx, info)
			}
			return nil, fmt.Errorf("configuring %s service: %w", dep.Name, err)
		}

		info, err := svcMgr.StartService(ctx, svcCfg)
		if err != nil {
			for _, prev := range serviceInfos {
				_ = svcMgr.StopService(ctx, prev)
			}
			return nil, fmt.Errorf("starting %s service: %w", dep.Name, err)
		}

		serviceInfos = append(serviceInfos, info)
		r.ServiceContainers[dep.Name] = info.ID
	}

	// Wait for readiness
	for i, dep := range serviceDeps {
		userSpec := cfg.Services[dep.Name]
		if !userSpec.ServiceWait() {
			continue
		}

		info := serviceInfos[i]
		if err := waitForServiceReady(ctx, svcMgr, info); err != nil {
			// Cleanup all services
			for _, si := range serviceInfos {
				_ = svcMgr.StopService(ctx, si)
			}
			return nil, fmt.Errorf("%s service failed to become ready: %w\n\nService container logs:\n  moat logs %s --service %s\n\nOr disable wait:\n  services:\n    %s:\n      wait: false", dep.Name, err, r.ID, dep.Name, dep.Name)
		}
	}

	// Inject MOAT_* env vars
	for i, dep := range serviceDeps {
		spec, _ := deps.GetSpec(dep.Name)
		userSpecPtr := &config.ServiceSpec{}
		if us, ok := cfg.Services[dep.Name]; ok {
			userSpecPtr = &us
		}
		svcEnv := generateServiceEnv(spec.Service, serviceInfos[i], userSpecPtr)
		for k, v := range svcEnv {
			envVars = append(envVars, k+"="+v)
		}
	}

	// Use network for main container
	networkMode = r.NetworkID
}
```

> **Note to implementer:** The exact variable names (`envVars`, `networkMode`, `r`, `cfg`) must match the local variables in `Create()`. Read `manager.go` carefully and adapt. The BuildKit section (around line 1400) shows the pattern.

**Step 2: Add `waitForServiceReady` function**

In `internal/run/services.go`, add:

```go
import (
	"context"
	"time"

	"github.com/anthropics/moat/internal/container"
)

const readinessTimeout = 30 * time.Second
const readinessInterval = 1 * time.Second

// waitForServiceReady polls CheckReady until success or timeout.
func waitForServiceReady(ctx context.Context, mgr container.ServiceManager, info container.ServiceInfo) error {
	deadline := time.Now().Add(readinessTimeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if err := mgr.CheckReady(ctx, info); err != nil {
			lastErr = err
			time.Sleep(readinessInterval)
			continue
		}
		return nil
	}

	return fmt.Errorf("timed out after %s: %w", readinessTimeout, lastErr)
}
```

**Step 3: Add service cleanup to Stop()**

In the `Stop()` method, before the BuildKit sidecar cleanup (around line 1898), add:

```go
// Stop service containers
if len(r.ServiceContainers) > 0 {
	svcMgr := m.runtime.ServiceManager()
	if svcMgr != nil {
		for name, containerID := range r.ServiceContainers {
			log.Debug("stopping service", "service", name, "container_id", containerID)
			if err := svcMgr.StopService(ctx, container.ServiceInfo{ID: containerID}); err != nil {
				log.Warn("failed to stop service", "service", name, "error", err)
			}
		}
	}
}
```

**Step 4: Add service cleanup to Destroy()**

In the `Destroy()` method, before the BuildKit container removal (around line 2244), add:

```go
// Remove service containers
for name, containerID := range r.ServiceContainers {
	if err := m.runtime.RemoveContainer(ctx, containerID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: removing %s service container: %v\n", name, err)
	}
}
```

**Step 5: Verify it compiles**

Run: `go build ./...`
Expected: PASS

**Step 6: Run existing tests to check for regressions**

Run: `go test ./internal/run/ -v`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/run/manager.go internal/run/services.go
git commit -m "feat(run): integrate service lifecycle into Create/Stop/Destroy"
```

---

### Task 9: Use `FilterInstallable` in image selection path

**Files:**
- Modify: `internal/run/manager.go`

**Step 1: Find where depList is used for image resolution**

Around line 1037 in `manager.go`, `image.Resolve(depList, ...)` is called. Change `depList` to `installableDeps` (the variable created in Task 8).

Also check any other places `depList` is used that should exclude services — Dockerfile generation, install scripts, etc.

**Step 2: Verify it compiles and tests pass**

Run: `go build ./... && go test ./internal/run/ -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/run/manager.go
git commit -m "fix(run): use FilterInstallable for image selection, excluding service deps"
```

---

### Task 10: Update documentation

**Files:**
- Modify: `docs/content/reference/02-agent-yaml.md`

**Step 1: Add `services:` field documentation**

Add a section documenting the `services:` field with examples for zero-config and override usage. Follow the style guide in `docs/STYLE-GUIDE.md`.

**Step 2: Update dependencies reference**

Document that `postgres`, `mysql`, and `redis` are now valid dependency values.

**Step 3: Commit**

```bash
git add docs/content/reference/02-agent-yaml.md
git commit -m "docs(reference): document service dependencies in agent.yaml"
```

---

### Task 11: Add E2E tests

**Files:**
- Create: `internal/e2e/services_test.go`

**Step 1: Write E2E tests**

```go
//go:build e2e

package e2e

import (
	"testing"
)

func TestServicePostgres(t *testing.T) {
	// Create agent.yaml with postgres@17 dependency
	// Run moat run
	// Verify MOAT_POSTGRES_URL is set
	// Verify postgres is reachable and accepts queries
	// Stop and verify cleanup
}

func TestServiceRedis(t *testing.T) {
	// Similar to postgres but for redis
}

func TestServiceMultiple(t *testing.T) {
	// postgres + redis together
}

func TestServiceCustomConfig(t *testing.T) {
	// Override password and database name via services: block
}

func TestServiceNoWait(t *testing.T) {
	// wait: false — verify service starts but main container doesn't block
}
```

> **Note to implementer:** Follow the patterns in existing E2E tests in `internal/e2e/`. These tests require Docker.

**Step 2: Run E2E tests**

Run: `go test -tags=e2e -run TestService -v ./internal/e2e/`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/e2e/services_test.go
git commit -m "test(e2e): add service dependency E2E tests"
```

---

## Task Dependency Graph

```
Task 1 (types + registry)
  └── Task 2 (filter helpers)
  └── Task 3 (ServiceManager interface)
        └── Task 4 (dockerServiceManager)
  └── Task 5 (config)
  └── Task 6 (storage metadata)
  └── Task 7 (service orchestration)
        └── Task 8 (manager integration) ← depends on 2,3,4,5,6,7
              └── Task 9 (FilterInstallable in image path)
              └── Task 10 (docs)
              └── Task 11 (E2E tests)
```

Tasks 1-7 can be partially parallelized. Tasks 1, 2, 3 are independent foundations. Task 4 depends on 3. Task 7 depends on 1, 4, 5. Task 8 brings everything together.
