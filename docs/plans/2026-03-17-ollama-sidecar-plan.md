# Ollama Sidecar Service Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Ollama as a service dependency with model provisioning and host-side caching, following the existing postgres/mysql/redis sidecar pattern.

**Architecture:** Extend the service dependency system with two general-purpose concepts — provisions (pull N items at startup) and cache (host-side persistence). Ollama is the first consumer. The registry entry, config parsing, service manager interface, and both Docker/Apple implementations are all modified.

**Tech Stack:** Go, Docker API, Apple container CLI, YAML parsing

**Spec:** `docs/plans/2026-03-17-ollama-sidecar-design.md`

---

## Chunk 1: Registry and type changes

### Task 1: Add provision/cache fields to ServiceDef

**Files:**
- Modify: `internal/deps/types.go:106-119` (ServiceDef struct)

- [ ] **Step 1: Write the failing test**

Add to `internal/deps/registry_test.go`:

```go
func TestRegistryHasOllama(t *testing.T) {
	ollama, ok := GetSpec("ollama")
	if !ok {
		t.Fatal("Registry should have 'ollama'")
	}
	if ollama.Type != TypeService {
		t.Errorf("ollama.Type = %v, want %v", ollama.Type, TypeService)
	}
	if ollama.Service == nil {
		t.Fatal("ollama.Service should not be nil")
	}
	assert.Equal(t, "ollama/ollama", ollama.Service.Image)
	assert.Equal(t, 11434, ollama.Service.Ports["default"])
	assert.Equal(t, "OLLAMA", ollama.Service.EnvPrefix)
	assert.Equal(t, "/root/.ollama", ollama.Service.CachePath)
	assert.Equal(t, "models", ollama.Service.ProvisionsKey)
	assert.Equal(t, "ollama pull {item}", ollama.Service.ProvisionCmd)
	assert.Empty(t, ollama.Service.PasswordEnv)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deps/ -run TestRegistryHasOllama -v`
Expected: FAIL — ollama not found in registry, and `CachePath`/`ProvisionsKey`/`ProvisionCmd` fields don't exist on `ServiceDef`.

- [ ] **Step 3: Add fields to ServiceDef**

In `internal/deps/types.go`, add three fields to `ServiceDef` after the `URLFormat` field:

```go
	// CachePath is the container-side path to mount for host-side caching.
	// If set, ~/.moat/cache/<service-name>/ is bind-mounted here.
	// This allows data to persist across runs (e.g., downloaded models).
	CachePath string `yaml:"cache_path,omitempty"`

	// ProvisionsKey names the key in user's services.<name> config that contains
	// a list of items to provision (e.g., "models"). Used with ProvisionCmd.
	ProvisionsKey string `yaml:"provisions_key,omitempty"`

	// ProvisionCmd is a command template executed once per provision item.
	// The placeholder {item} is replaced with each item value.
	// Commands run via sh -c inside the service container after readiness.
	ProvisionCmd string `yaml:"provision_cmd,omitempty"`
```

- [ ] **Step 4: Add ollama entry to registry.yaml**

Add to `internal/deps/registry.yaml` in the "Service dependencies" section, after redis:

```yaml
ollama:
  description: Ollama local model server
  type: service
  default: "0.9"
  service:
    image: ollama/ollama
    ports:
      default: 11434
    env_prefix: OLLAMA
    readiness_cmd: "ollama list"
    url_scheme: "http"
    url_format: "{scheme}://{host}:{port}"
    cache_path: /root/.ollama
    provisions_key: models
    provision_cmd: "ollama pull {item}"
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/deps/ -run TestRegistryHasOllama -v`
Expected: PASS

- [ ] **Step 6: Run all deps tests**

Run: `go test ./internal/deps/ -v -count=1`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/deps/types.go internal/deps/registry.yaml internal/deps/registry_test.go
git commit -m "feat(deps): add ollama service and provision/cache fields to ServiceDef"
```

---

### Task 2: Add provision/cache fields to ServiceConfig

**Files:**
- Modify: `internal/container/runtime.go:209-221` (ServiceConfig struct)

- [ ] **Step 1: Add fields to ServiceConfig**

In `internal/container/runtime.go`, add four fields to `ServiceConfig` after `ReadinessCmd`:

```go
	// CachePath is the container-side path for cache mounting (e.g., "/root/.ollama").
	CachePath string
	// CacheHostPath is the resolved host-side path (e.g., "~/.moat/cache/ollama/").
	CacheHostPath string
	// Provisions is the list of items to provision (e.g., model names).
	Provisions []string
	// ProvisionCmd is the command template with {item} placeholder.
	ProvisionCmd string
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/container/...`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add internal/container/runtime.go
git commit -m "feat(container): add provision and cache fields to ServiceConfig"
```

---

### Task 3: Add ProvisionService to ServiceManager interface

**Files:**
- Modify: `internal/container/runtime.go:199-206` (ServiceManager interface)
- Modify: `internal/container/docker_service.go` (add method)
- Modify: `internal/container/apple_service.go` (add method)
- Modify: `internal/container/docker_service_test.go` (add test)
- Modify: `internal/container/apple_service_test.go` (add test)

- [ ] **Step 1: Write failing tests**

Add to `internal/container/docker_service_test.go`:

```go
func TestBuildSidecarConfigWithCachePath(t *testing.T) {
	cfg := ServiceConfig{
		Name:          "ollama",
		Version:       "0.9",
		Image:         "ollama/ollama",
		Ports:         map[string]int{"default": 11434},
		Env:           map[string]string{},
		RunID:         "test-run-789",
		CachePath:     "/root/.ollama",
		CacheHostPath: "/tmp/test-cache/ollama",
	}

	sidecarCfg := buildSidecarConfig(cfg, "net-789")
	assert.Equal(t, "ollama/ollama:0.9", sidecarCfg.Image)
	assert.Equal(t, "moat-ollama-test-run-789", sidecarCfg.Name)

	// Verify cache mount is present
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
```

Add to `internal/container/apple_service_test.go`:

```go
func TestAppleBuildRunArgsWithCachePath(t *testing.T) {
	cfg := ServiceConfig{
		Name:          "ollama",
		Version:       "0.9",
		Image:         "ollama/ollama",
		Env:           map[string]string{},
		RunID:         "test-run-789",
		CachePath:     "/root/.ollama",
		CacheHostPath: "/tmp/test-cache/ollama",
	}

	args := buildAppleRunArgs(cfg, "moat-test-net")
	assert.Contains(t, args, "--volume")
	// Find the volume arg value
	for i, a := range args {
		if a == "--volume" && i+1 < len(args) {
			assert.Equal(t, "/tmp/test-cache/ollama:/root/.ollama", args[i+1])
			return
		}
	}
	t.Fatal("--volume flag not found in args")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/container/ -run "TestBuildSidecarConfigWithCachePath|TestBuildSidecarConfigNoCachePath|TestAppleBuildRunArgsWithCachePath" -v`
Expected: FAIL — cache mount not implemented

- [ ] **Step 3: Add ProvisionService to ServiceManager interface**

In `internal/container/runtime.go`, add to `ServiceManager` interface:

```go
	// ProvisionService executes commands sequentially inside the service container.
	// Each command is run via sh -c. stdout receives streaming output for user feedback.
	// Returns on first command failure (fail-fast).
	ProvisionService(ctx context.Context, info ServiceInfo, cmds []string, stdout io.Writer) error
```

- [ ] **Step 4: Add cache mount to Docker buildSidecarConfig**

In `internal/container/docker_service.go`, in `buildSidecarConfig`, add after the `SidecarConfig` struct literal is built (before `if len(cfg.ExtraCmd) > 0`):

```go
	// Add cache mount if configured
	if cfg.CachePath != "" && cfg.CacheHostPath != "" {
		sc.Mounts = append(sc.Mounts, MountConfig{
			Source: cfg.CacheHostPath,
			Target: cfg.CachePath,
		})
	}
```

- [ ] **Step 5: Implement ProvisionService on dockerServiceManager**

Add `"github.com/docker/docker/pkg/stdcopy"` to imports. Then add to `internal/container/docker_service.go`:

```go
// ProvisionService executes commands sequentially inside the service container.
func (m *dockerServiceManager) ProvisionService(ctx context.Context, info ServiceInfo, cmds []string, stdout io.Writer) error {
	for _, cmd := range cmds {
		execCreateResp, err := m.cli.ContainerExecCreate(ctx, info.ID, dockercontainer.ExecOptions{
			Cmd:          []string{"sh", "-c", cmd},
			AttachStdout: true,
			AttachStderr: true,
		})
		if err != nil {
			return fmt.Errorf("creating exec for provision command %q: %w", cmd, err)
		}

		resp, err := m.cli.ContainerExecAttach(ctx, execCreateResp.ID, dockercontainer.ExecAttachOptions{})
		if err != nil {
			return fmt.Errorf("attaching to provision command %q: %w", cmd, err)
		}
		// Use stdcopy to demultiplex Docker's stream protocol headers.
		// Without this, binary framing bytes would be mixed into the output.
		_, _ = stdcopy.StdCopy(stdout, stdout, resp.Reader)
		resp.Close()

		execInspect, err := m.cli.ContainerExecInspect(ctx, execCreateResp.ID)
		if err != nil {
			return fmt.Errorf("inspecting provision command %q: %w", cmd, err)
		}
		if execInspect.ExitCode != 0 {
			return fmt.Errorf("provision command %q failed with exit code %d", cmd, execInspect.ExitCode)
		}
	}
	return nil
}
```

- [ ] **Step 6: Add cache mount to Apple buildAppleRunArgs**

In `internal/container/apple_service.go`, in `buildAppleRunArgs`, add after the network block and before env sorting:

```go
	// Add cache mount if configured
	if cfg.CachePath != "" && cfg.CacheHostPath != "" {
		args = append(args, "--volume", cfg.CacheHostPath+":"+cfg.CachePath)
	}
```

- [ ] **Step 7: Implement ProvisionService on appleServiceManager**

Add to `internal/container/apple_service.go`:

```go
// ProvisionService executes commands sequentially inside the service container.
func (m *appleServiceManager) ProvisionService(ctx context.Context, info ServiceInfo, cmds []string, stdout io.Writer) error {
	for _, cmd := range cmds {
		execCmd := exec.CommandContext(ctx, m.containerBin, "exec", info.ID, "sh", "-c", cmd)
		execCmd.Stdout = stdout
		execCmd.Stderr = stdout
		if err := execCmd.Run(); err != nil {
			return fmt.Errorf("provision command %q failed: %w", cmd, err)
		}
	}
	return nil
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `go test ./internal/container/ -run "TestBuildSidecarConfigWithCachePath|TestBuildSidecarConfigNoCachePath|TestAppleBuildRunArgsWithCachePath" -v`
Expected: PASS

- [ ] **Step 9: Run all container tests**

Run: `go test ./internal/container/ -v -count=1`
Expected: All PASS

- [ ] **Step 10: Commit**

```bash
git add internal/container/runtime.go internal/container/docker_service.go internal/container/apple_service.go internal/container/docker_service_test.go internal/container/apple_service_test.go
git commit -m "feat(container): add ProvisionService and cache mount support to service managers"
```

---

## Chunk 2: Config parsing and service orchestration

### Task 4: Add Provisions field to ServiceSpec with deferred unknown-key handling

**Files:**
- Modify: `internal/config/config.go:150-163` (ServiceSpec struct)
- Modify: `internal/config/config_test.go` (add tests)

- [ ] **Step 1: Write failing tests**

Add to `internal/config/config_test.go`:

```go
func TestServiceSpecUnmarshalWithExtra(t *testing.T) {
	input := `
env:
  FOO: bar
wait: false
models:
  - qwen2.5-coder:7b
  - nomic-embed-text
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	assert.Equal(t, "bar", spec.Env["FOO"])
	assert.False(t, spec.ServiceWait())
	assert.Equal(t, []string{"qwen2.5-coder:7b", "nomic-embed-text"}, spec.Extra["models"])
}

func TestServiceSpecUnmarshalNoExtra(t *testing.T) {
	input := `
env:
  POSTGRES_DB: myapp
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	assert.Equal(t, "myapp", spec.Env["POSTGRES_DB"])
	assert.Empty(t, spec.Extra)
}

func TestServiceSpecUnmarshalPreservesImage(t *testing.T) {
	input := `
image: custom-postgres:17
env:
  POSTGRES_DB: myapp
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	assert.Equal(t, "custom-postgres:17", spec.Image)
	assert.Equal(t, "myapp", spec.Env["POSTGRES_DB"])
}

func TestServiceSpecUnmarshalEmptyExtra(t *testing.T) {
	input := `
env:
  FOO: bar
models: []
`
	var spec ServiceSpec
	err := yaml.Unmarshal([]byte(input), &spec)
	require.NoError(t, err)
	assert.Empty(t, spec.Extra["models"])
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run "TestServiceSpecUnmarshal" -v`
Expected: FAIL — `Extra` field doesn't exist on `ServiceSpec`

- [ ] **Step 3: Implement ServiceSpec changes**

In `internal/config/config.go`, replace the `ServiceSpec` struct and add `UnmarshalYAML`:

```go
// ServiceSpec allows customizing service behavior.
type ServiceSpec struct {
	Env   map[string]string `yaml:"env,omitempty"`
	Image string            `yaml:"image,omitempty"`
	Wait  *bool             `yaml:"wait,omitempty"`
	// Extra holds unknown list-valued keys (e.g., "models" for ollama).
	// Populated by UnmarshalYAML. The run layer maps these to provisions
	// using the registry's provisions_key.
	Extra map[string][]string `yaml:"-"`
}

// UnmarshalYAML implements custom unmarshaling to capture unknown list-valued keys
// into Extra. Known keys (env, image, wait) are parsed normally.
func (s *ServiceSpec) UnmarshalYAML(value *yaml.Node) error {
	// First, decode known fields using an alias to avoid recursion.
	type plain ServiceSpec
	if err := value.Decode((*plain)(s)); err != nil {
		return err
	}

	// Then scan for unknown keys that have sequence values.
	if value.Kind != yaml.MappingNode {
		return nil
	}
	known := map[string]bool{"env": true, "image": true, "wait": true}
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i].Value
		val := value.Content[i+1]
		if known[key] {
			continue
		}
		if val.Kind == yaml.SequenceNode {
			items := make([]string, 0, len(val.Content))
			for _, item := range val.Content {
				items = append(items, item.Value)
			}
			if s.Extra == nil {
				s.Extra = make(map[string][]string)
			}
			s.Extra[key] = items
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run "TestServiceSpecUnmarshal" -v`
Expected: PASS

- [ ] **Step 5: Run all config tests**

Run: `go test ./internal/config/ -v -count=1`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add Extra field to ServiceSpec for provision lists"
```

---

### Task 5: Update buildServiceConfig for provisions, cache, and password guard

**Files:**
- Modify: `internal/run/services.go:101-175`
- Modify: `internal/run/services_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/run/services_test.go`:

```go
func TestBuildServiceConfigOllama(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.9", Type: deps.TypeService}

	userSpec := &config.ServiceSpec{
		Extra: map[string][]string{
			"models": {"qwen2.5-coder:7b", "nomic-embed-text"},
		},
	}

	cfg, err := buildServiceConfig(dep, "run-ollama", userSpec)
	require.NoError(t, err)

	assert.Equal(t, "ollama", cfg.Name)
	assert.Equal(t, "0.9", cfg.Version)
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
	dep := deps.Dependency{Name: "ollama", Version: "0.9", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-ollama", nil)
	require.NoError(t, err)

	assert.Empty(t, cfg.Provisions)
	assert.Equal(t, "ollama pull {item}", cfg.ProvisionCmd)
}

func TestBuildServiceConfigNoPasswordForNoAuth(t *testing.T) {
	dep := deps.Dependency{Name: "ollama", Version: "0.9", Type: deps.TypeService}

	cfg, err := buildServiceConfig(dep, "run-test", nil)
	require.NoError(t, err)

	// No password env var should be set when PasswordEnv is empty
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
	dep := deps.Dependency{Name: "ollama", Version: "0.9", Type: deps.TypeService}

	userSpec := &config.ServiceSpec{
		Extra: map[string][]string{
			"model": {"qwen2.5-coder:7b"}, // typo: "model" instead of "models"
		},
	}

	_, err := buildServiceConfig(dep, "run-ollama", userSpec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/run/ -run "TestBuildServiceConfigOllama|TestBuildServiceConfigNoPassword|TestBuildServiceConfigPostgresStillHas|TestBuildServiceConfigValidatesProvisionsKey" -v`
Expected: FAIL

- [ ] **Step 3: Update buildServiceConfig**

Replace `buildServiceConfig` in `internal/run/services.go`:

```go
// buildServiceConfig creates a ServiceConfig for a service dependency.
// Populates both generic fields and service definition fields from the registry.
func buildServiceConfig(dep deps.Dependency, runID string, userSpec *config.ServiceSpec) (container.ServiceConfig, error) {
	spec, ok := deps.GetSpec(dep.Name)
	if !ok || spec.Service == nil {
		return container.ServiceConfig{}, fmt.Errorf("unknown service: %s", dep.Name)
	}
	if spec.Type != deps.TypeService {
		return container.ServiceConfig{}, fmt.Errorf("%s has type %q but expected %q", dep.Name, spec.Type, deps.TypeService)
	}

	env := make(map[string]string)

	// Only generate password for services that have auth
	if spec.Service.PasswordEnv != "" {
		password, err := generatePassword()
		if err != nil {
			return container.ServiceConfig{}, err
		}
		env[spec.Service.PasswordEnv] = password

		// Set extra_env from registry with placeholder substitution
		for k, v := range spec.Service.ExtraEnv {
			v = strings.ReplaceAll(v, "{db}", spec.Service.DefaultDB)
			v = strings.ReplaceAll(v, "{password}", password)
			env[k] = v
		}
	} else {
		// No auth — still apply extra_env without password substitution
		for k, v := range spec.Service.ExtraEnv {
			v = strings.ReplaceAll(v, "{db}", spec.Service.DefaultDB)
			env[k] = v
		}
	}

	// Apply user overrides
	if userSpec != nil {
		for k, v := range userSpec.Env {
			env[k] = v
		}
	}

	// Resolve provisions from user spec Extra using registry's provisions_key
	var provisions []string
	if userSpec != nil && spec.Service.ProvisionsKey != "" {
		provisions = userSpec.Extra[spec.Service.ProvisionsKey]

		// Validate: reject unknown Extra keys that don't match provisions_key
		for key := range userSpec.Extra {
			if key != spec.Service.ProvisionsKey {
				return container.ServiceConfig{}, fmt.Errorf(
					"services.%s.%s is not a valid key (did you mean %q?)",
					dep.Name, key, spec.Service.ProvisionsKey,
				)
			}
		}
	} else if userSpec != nil && len(userSpec.Extra) > 0 {
		// Service doesn't support provisions but user provided extra keys
		for key := range userSpec.Extra {
			return container.ServiceConfig{}, fmt.Errorf(
				"services.%s.%s is not a valid configuration key",
				dep.Name, key,
			)
		}
	}

	// Resolve cache host path
	var cacheHostPath string
	if spec.Service.CachePath != "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return container.ServiceConfig{}, fmt.Errorf("resolving home directory for cache: %w", err)
		}
		cacheHostPath = filepath.Join(homeDir, ".moat", "cache", dep.Name)
	}

	return container.ServiceConfig{
		Name:          dep.Name,
		Version:       dep.Version,
		Env:           env,
		RunID:         runID,
		Image:         spec.Service.Image,
		Ports:         spec.Service.Ports,
		PasswordEnv:   spec.Service.PasswordEnv,
		ExtraCmd:      spec.Service.ExtraCmd,
		ReadinessCmd:  spec.Service.ReadinessCmd,
		CachePath:     spec.Service.CachePath,
		CacheHostPath: cacheHostPath,
		Provisions:    provisions,
		ProvisionCmd:  spec.Service.ProvisionCmd,
	}, nil
}
```

Add `"os"` and `"path/filepath"` to the imports in `services.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/run/ -run "TestBuildServiceConfigOllama|TestBuildServiceConfigNoPassword|TestBuildServiceConfigPostgresStillHas|TestBuildServiceConfigValidatesProvisionsKey" -v`
Expected: PASS

- [ ] **Step 5: Run all run package tests**

Run: `go test ./internal/run/ -v -count=1`
Expected: All PASS (existing postgres/redis/mysql tests must still pass)

- [ ] **Step 6: Commit**

```bash
git add internal/run/services.go internal/run/services_test.go
git commit -m "feat(run): wire provisions, cache, and password guard into buildServiceConfig"
```

---

### Task 6: Add provisionService function and wire into manager

**Files:**
- Modify: `internal/run/services.go` (add provisionService)
- Modify: `internal/run/services_test.go` (add tests)
- Modify: `internal/run/manager.go:1922-1948` (wire provisioning after readiness)

- [ ] **Step 1: Write failing test for provisionService**

Add to `internal/run/services_test.go`:

```go
func TestProvisionServiceBuildsCommands(t *testing.T) {
	cmds := buildProvisionCmds("ollama pull {item}", []string{"qwen2.5-coder:7b", "nomic-embed-text"})
	assert.Equal(t, []string{"ollama pull qwen2.5-coder:7b", "ollama pull nomic-embed-text"}, cmds)
}

func TestProvisionServiceEmptyList(t *testing.T) {
	cmds := buildProvisionCmds("ollama pull {item}", nil)
	assert.Empty(t, cmds)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/run/ -run "TestProvisionService" -v`
Expected: FAIL — `buildProvisionCmds` doesn't exist

- [ ] **Step 3: Implement provisionService and buildProvisionCmds**

Add to `internal/run/services.go`:

```go
const provisionTimeout = 30 * time.Minute

// buildProvisionCmds creates concrete commands from a template and item list.
func buildProvisionCmds(cmdTemplate string, items []string) []string {
	if len(items) == 0 {
		return nil
	}
	cmds := make([]string, len(items))
	for i, item := range items {
		cmds[i] = strings.ReplaceAll(cmdTemplate, "{item}", item)
	}
	return cmds
}

// provisionService runs provision commands inside a started service container.
// Uses flock-based advisory locking on the cache directory to prevent concurrent
// corruption from parallel runs.
func provisionService(ctx context.Context, mgr container.ServiceManager, info container.ServiceInfo, cfg container.ServiceConfig, stdout io.Writer) error {
	cmds := buildProvisionCmds(cfg.ProvisionCmd, cfg.Provisions)
	if len(cmds) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, provisionTimeout)
	defer cancel()

	// Advisory lock on cache directory to prevent concurrent pull corruption.
	// Cache directory is already created by the manager before starting the sidecar.
	if cfg.CacheHostPath != "" {
		lockPath := filepath.Join(cfg.CacheHostPath, ".lock")
		lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return fmt.Errorf("opening cache lock %s: %w", lockPath, err)
		}
		defer lockFile.Close()
		if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
			return fmt.Errorf("acquiring cache lock: %w", err)
		}
		defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()
	}

	return mgr.ProvisionService(ctx, info, cmds, stdout)
}
```

Add `"io"`, `"os"`, `"syscall"` to the imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/run/ -run "TestProvisionService" -v`
Expected: PASS

- [ ] **Step 5: Wire provisioning into manager.go**

In `internal/run/manager.go`, in the "Wait for readiness" loop (around line 1922-1948), add provisioning after the `waitForServiceReady` call. The modified loop should look like:

```go
		// Wait for readiness and provision
		for i, dep := range serviceDeps {
			wait := true
			if opts.Config != nil {
				if s, ok := opts.Config.Services[dep.Name]; ok {
					wait = s.ServiceWait()
				}
			}
			if !wait {
				continue
			}

			info := serviceInfos[i]
			log.Debug("waiting for service to be ready", "service", dep.Name)
			if err := waitForServiceReady(ctx, svcMgr, info); err != nil {
				cleanupServices()
				cleanupDaemonRun()
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, fmt.Errorf("%s service failed to become ready: %w\n\n"+
					"Service container logs:\n  moat logs %s --service %s\n\n"+
					"Or disable wait:\n  services:\n    %s:\n      wait: false",
					dep.Name, err, r.ID, dep.Name, dep.Name)
			}

			// Provision items (e.g., pull models) if configured
			if svcConfigs[i].ProvisionCmd != "" && len(svcConfigs[i].Provisions) > 0 {
				log.Debug("provisioning service", "service", dep.Name, "items", svcConfigs[i].Provisions)
				if err := provisionService(ctx, svcMgr, info, svcConfigs[i], os.Stderr); err != nil {
					cleanupServices()
					cleanupDaemonRun()
					cleanupSSH(sshServer)
					cleanupAgentConfig(claudeConfig)
					cleanupAgentConfig(codexConfig)
					cleanupAgentConfig(geminiConfig)
					return nil, fmt.Errorf("%s service provisioning failed: %w\n\n"+
						"Service container logs:\n  moat logs %s --service %s",
						dep.Name, err, r.ID, dep.Name)
				}
			}
		}
```

**Important:** The manager needs to store `ServiceConfig` values to access them in the readiness loop. In the "Start services" loop (around line 1888), save `svcCfg` into a slice:

Before the loop, add:
```go
		var svcConfigs []container.ServiceConfig
```

Inside the loop, after `svcCfg, err := buildServiceConfig(...)`, add:
```go
			svcConfigs = append(svcConfigs, svcCfg)
```

Also, before starting the sidecar, create the cache directory:
```go
			// Create cache directory if needed
			if svcCfg.CacheHostPath != "" {
				if err := os.MkdirAll(svcCfg.CacheHostPath, 0o755); err != nil {
					cleanupServices()
					cleanupDaemonRun()
					cleanupSSH(sshServer)
					cleanupAgentConfig(claudeConfig)
					cleanupAgentConfig(codexConfig)
					cleanupAgentConfig(geminiConfig)
					return nil, fmt.Errorf("creating cache directory for %s: %w", dep.Name, err)
				}
			}
```

- [ ] **Step 6: Verify compilation**

Run: `go build ./...`
Expected: Success

- [ ] **Step 7: Run all tests**

Run: `go test ./internal/run/ -v -count=1`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/run/services.go internal/run/services_test.go internal/run/manager.go
git commit -m "feat(run): add service provisioning with flock-based cache locking"
```

---

## Chunk 3: Env generation fix, example, and documentation

### Task 7: Fix generateServiceEnv for no-auth services

**Files:**
- Modify: `internal/run/services.go` (generateServiceEnv)
- Modify: `internal/run/services_test.go` (add test)

- [ ] **Step 1: Write failing test**

Add to `internal/run/services_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it passes (or fails)**

Run: `go test ./internal/run/ -run TestGenerateServiceEnvOllama -v`
Expected: This test should PASS with the current `generateServiceEnv` code since the env map is empty and `PasswordEnv` is empty. The `password` fallback (`info.Env["password"]`) returns empty string, so `MOAT_OLLAMA_PASSWORD` won't be set. If it fails, the password guard in Task 5 already fixed the upstream issue. Verify and move on.

- [ ] **Step 3: Run all run tests**

Run: `go test ./internal/run/ -v -count=1`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add internal/run/services_test.go
git commit -m "test(run): add env generation test for Ollama no-auth service"
```

---

### Task 8: Add example

**Files:**
- Create: `examples/service-ollama/moat.yaml`
- Create: `examples/service-ollama/demo.sh`

- [ ] **Step 1: Create example moat.yaml**

Create `examples/service-ollama/moat.yaml`:

```yaml
# Example: Ollama service dependency
#
# Demonstrates local model inference using Ollama as a service dependency.
# Moat starts an Ollama sidecar, pulls the declared model, and injects
# MOAT_OLLAMA_* environment variables into the agent container.
#
# Run with:
#   moat run examples/service-ollama
#
# The demo script queries the Ollama API to list models and generate a response.

name: service-ollama

dependencies:
  - ollama@0.9
  - curl

services:
  ollama:
    models:
      - qwen2.5-coder:1.5b

command: ["sh", "/workspace/demo.sh"]
```

- [ ] **Step 2: Create demo script**

Create `examples/service-ollama/demo.sh`:

```bash
#!/bin/sh
set -e

echo "=== Ollama Service Demo ==="
echo "MOAT_OLLAMA_URL=$MOAT_OLLAMA_URL"
echo

echo "--- Available models ---"
curl -s "$MOAT_OLLAMA_URL/api/tags" | head -c 200
echo
echo

echo "--- Generating response ---"
curl -s "$MOAT_OLLAMA_URL/api/generate" \
  -d '{"model":"qwen2.5-coder:1.5b","prompt":"Write hello world in Go","stream":false}' \
  | head -c 500
echo
```

- [ ] **Step 3: Commit**

```bash
git add examples/service-ollama/
git commit -m "docs(examples): add Ollama service dependency example"
```

---

### Task 9: Update documentation

**Files:**
- Modify: `docs/content/guides/08-services.md`
- Modify: `docs/content/reference/06-dependencies.md`
- Modify: `docs/content/reference/02-moat-yaml.md`

- [ ] **Step 1: Add Ollama to services guide**

In `docs/content/guides/08-services.md`, add after the Redis example in "Multiple services" section and before "Custom configuration":

Add an Ollama section after the "Readiness checks" table:

```markdown
### Ollama (local models)

Ollama provides local model inference without external API keys:

\`\`\`yaml
dependencies:
  - ollama@0.9

services:
  ollama:
    models:
      - qwen2.5-coder:7b
      - nomic-embed-text
\`\`\`

Models are pulled during startup and cached at `~/.moat/cache/ollama/` on the host. Subsequent runs skip the download.

\`\`\`python
import requests
import os

url = os.environ["MOAT_OLLAMA_URL"]
resp = requests.post(f"{url}/api/generate", json={
    "model": "qwen2.5-coder:7b",
    "prompt": "Write hello world in Go",
    "stream": False,
})
print(resp.json()["response"])
\`\`\`
```

Add Ollama to the readiness table:

```
| `ollama` | `ollama list` + pull declared models |
```

- [ ] **Step 2: Add Ollama to the dependencies reference**

In `docs/content/reference/06-dependencies.md`, add ollama to the services section of the dependency registry table.

- [ ] **Step 3: Document provisions pattern in moat.yaml reference**

In `docs/content/reference/02-moat-yaml.md`, in the `services:` section, add documentation for the provisions pattern (list-valued keys like `models`).

- [ ] **Step 4: Commit**

```bash
git add docs/content/guides/08-services.md docs/content/reference/06-dependencies.md docs/content/reference/02-moat-yaml.md
git commit -m "docs: add Ollama service documentation and provisions pattern"
```

---

### Task 10: Lint and final verification

- [ ] **Step 1: Run linter**

Run: `make lint`
Expected: No errors. Fix any issues.

- [ ] **Step 2: Run all unit tests**

Run: `make test-unit`
Expected: All PASS

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: Success

- [ ] **Step 4: Final commit if lint fixes needed**

```bash
git add -A
git commit -m "style: fix lint issues"
```
