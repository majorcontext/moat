# Apple container service dependencies implementation plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement `NetworkManager` and `ServiceManager` for the Apple container runtime, enabling service dependencies (postgres, mysql, redis) on macOS 26+.

**Architecture:** The Apple `container` CLI (macOS 26+) supports `container network create` for inter-container networking and `container exec` for running commands inside containers. We implement `appleNetworkManager` and `appleServiceManager` that shell out to the CLI — the same pattern used throughout `apple.go`. The existing run manager orchestration code (`manager.go`) already works against the abstract interfaces and requires no changes.

**Tech Stack:** Go, Apple `container` CLI, `os/exec`

---

## Context

**How Docker does it:** `dockerServiceManager` (in `docker_service.go`) delegates to `SidecarManager` + `NetworkManager` + Docker client API. `dockerNetworkManager` (in `docker.go:756-786`) creates bridge networks via Docker SDK. The run manager (`manager.go:1517-1660`) orchestrates: check `ServiceManager() != nil`, check `NetworkManager() != nil`, create network, call `SetNetworkID()`, start services, wait for readiness, inject env vars. All runtime-agnostic.

**What blocks Apple today:** `AppleRuntime` returns `nil` for `NetworkManager()`, `SidecarManager()`, and `ServiceManager()` (in `apple.go:60-73`). The run manager hits the nil check at `manager.go:1520` and returns an Apple-specific error.

**What we're building:** Two new managers that implement the existing interfaces. Once `AppleRuntime` returns non-nil for `NetworkManager()` and `ServiceManager()`, the entire service dependency flow works without any changes to the run manager.

**Shared helpers:** `buildSidecarConfig()`, `buildServiceInfo()`, and `resolvePlaceholders()` in `docker_service.go` are pure functions with no Docker dependency. `buildSidecarConfig` produces a `SidecarConfig` which is Docker-specific, so the Apple implementation will do its own arg building. `buildServiceInfo` and `resolvePlaceholders` are reusable.

**`SidecarManager` stays nil:** The Apple `ServiceManager` is implemented independently. `SidecarManager` remains unimplemented — it's a Docker-specific abstraction used for BuildKit sidecars.

---

### Task 1: Extract shared helpers from `docker_service.go`

Move `buildServiceInfo()` and `resolvePlaceholders()` out of `docker_service.go` into a new `service_helpers.go` so both Docker and Apple implementations can use them without import cycles.

**Files:**
- Create: `internal/container/service_helpers.go`
- Create: `internal/container/service_helpers_test.go`
- Modify: `internal/container/docker_service.go` — remove `buildServiceInfo` and `resolvePlaceholders`
- Modify: `internal/container/docker_service_test.go` — move `TestBuildServiceInfo` and `TestResolvePlaceholders` to new test file

**Step 1: Create `service_helpers.go`**

Move these two functions verbatim from `docker_service.go:124-151`:

```go
// internal/container/service_helpers.go
package container

import "strings"

// buildServiceInfo creates a ServiceInfo from a started container.
func buildServiceInfo(containerID string, cfg ServiceConfig, host string) ServiceInfo {
	return ServiceInfo{
		ID:           containerID,
		Name:         cfg.Name,
		Host:         host,
		Ports:        cfg.Ports,
		Env:          cfg.Env,
		ReadinessCmd: cfg.ReadinessCmd,
		PasswordEnv:  cfg.PasswordEnv,
	}
}

// resolvePlaceholders replaces {key} placeholders in template with values from
// env, matching keys case-insensitively (using lowercased keys). If passwordEnv
// is set (e.g. "POSTGRES_PASSWORD"), its value is also available as {password}.
func resolvePlaceholders(template string, env map[string]string, passwordEnv string) string {
	if passwordEnv != "" {
		if pw, ok := env[passwordEnv]; ok {
			template = strings.ReplaceAll(template, "{password}", pw)
		}
	}
	for k, v := range env {
		template = strings.ReplaceAll(template, "{"+strings.ToLower(k)+"}", v)
	}
	return template
}
```

**Step 2: Create `service_helpers_test.go`**

Move `TestBuildServiceInfo` and `TestResolvePlaceholders` from `docker_service_test.go:45-83` into `service_helpers_test.go`. Keep them identical.

**Step 3: Remove from `docker_service.go` and `docker_service_test.go`**

Delete `buildServiceInfo` and `resolvePlaceholders` from `docker_service.go` (lines 124-151). Delete the corresponding tests from `docker_service_test.go` (lines 45-83). The `docker_service.go` file keeps `buildSidecarConfig` since it produces Docker-specific `SidecarConfig`.

**Step 4: Run tests to verify nothing broke**

Run: `go test ./internal/container/ -v -run "TestBuildServiceInfo|TestResolvePlaceholders|TestBuildSidecarConfig"`

Expected: All pass — same functions, different file.

**Step 5: Run full package tests**

Run: `go test ./internal/container/`

Expected: PASS

**Step 6: Commit**

```bash
git add internal/container/service_helpers.go internal/container/service_helpers_test.go internal/container/docker_service.go internal/container/docker_service_test.go
git commit -m "refactor(container): extract shared service helpers from docker_service.go"
```

---

### Task 2: Implement `appleNetworkManager`

**Files:**
- Create: `internal/container/apple_network.go`
- Create: `internal/container/apple_network_test.go`

**Step 1: Write tests**

```go
// internal/container/apple_network_test.go
package container

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppleNetworkManagerCreateAndRemove(t *testing.T) {
	bin, err := findContainerBin()
	if err != nil {
		t.Skip("Apple container CLI not available")
	}

	mgr := &appleNetworkManager{containerBin: bin}
	ctx := context.Background()

	// Create
	id, err := mgr.CreateNetwork(ctx, "moat-test-net")
	require.NoError(t, err)
	assert.Equal(t, "moat-test-net", id)

	// Remove
	err = mgr.RemoveNetwork(ctx, "moat-test-net")
	assert.NoError(t, err)

	// Remove again — should not error (best-effort)
	err = mgr.RemoveNetwork(ctx, "moat-test-net")
	assert.NoError(t, err)
}
```

Note: We need a small helper `findContainerBin()` to look up the binary path. We could inline `exec.LookPath("container")` in each test, but a helper is cleaner. Add it to `service_helpers.go` or `apple_network.go` — wherever it fits. Actually, `NewAppleRuntime` already does `exec.LookPath("container")`. The tests can just call that. But we need the bin path without constructing a full runtime. Add a package-level helper:

```go
// findContainerBin returns the path to the Apple container CLI, or an error if not found.
func findContainerBin() (string, error) {
	return exec.LookPath("container")
}
```

Put this in `apple_network.go` since it's Apple-specific.

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/container/ -v -run TestAppleNetworkManager`

Expected: Compilation error — `appleNetworkManager` not defined.

**Step 3: Implement `appleNetworkManager`**

```go
// internal/container/apple_network.go
package container

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/majorcontext/moat/internal/log"
)

// appleNetworkManager implements NetworkManager using the Apple container CLI.
type appleNetworkManager struct {
	containerBin string
}

// findContainerBin returns the path to the Apple container CLI.
func findContainerBin() (string, error) {
	return exec.LookPath("container")
}

// CreateNetwork creates an Apple container network.
// Returns the network name as the identifier (Apple CLI uses names, not opaque IDs).
func (m *appleNetworkManager) CreateNetwork(ctx context.Context, name string) (string, error) {
	cmd := exec.CommandContext(ctx, m.containerBin, "network", "create", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("creating network %s: %s: %w", name, strings.TrimSpace(string(output)), err)
	}
	log.Debug("created apple container network", "name", name)
	return name, nil
}

// RemoveNetwork removes an Apple container network by name.
// Best-effort: does not fail if network doesn't exist.
func (m *appleNetworkManager) RemoveNetwork(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, m.containerBin, "network", "delete", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(output))
		// Ignore "not found" errors during cleanup
		if strings.Contains(outStr, "not found") || strings.Contains(outStr, "No such") {
			return nil
		}
		log.Warn("failed to remove apple network", "name", name, "error", outStr)
		return nil
	}
	log.Debug("removed apple container network", "name", name)
	return nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/container/ -v -run TestAppleNetworkManager`

Expected: PASS (or skip if not on macOS with Apple container CLI).

**Step 5: Commit**

```bash
git add internal/container/apple_network.go internal/container/apple_network_test.go
git commit -m "feat(container): implement appleNetworkManager"
```

---

### Task 3: Implement `appleServiceManager`

**Files:**
- Create: `internal/container/apple_service.go`
- Create: `internal/container/apple_service_test.go`

**Step 1: Write unit tests for arg building**

The `StartService` method needs to build CLI args from a `ServiceConfig`. Test the arg-building logic as a pure function:

```go
// internal/container/apple_service_test.go
package container

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
	// Image should appear before extra cmd args
	imageIdx := -1
	for i, a := range args {
		if a == "redis:7" {
			imageIdx = i
			break
		}
	}
	assert.Greater(t, imageIdx, 0, "image should be in args")
	// Extra cmd args come after image
	assert.Contains(t, args[imageIdx+1:], "--requirepass")
	assert.Contains(t, args[imageIdx+1:], "redispass")
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/container/ -v -run TestAppleBuildRunArgs`

Expected: Compilation error — `buildAppleRunArgs` not defined.

**Step 3: Implement `appleServiceManager`**

```go
// internal/container/apple_service.go
package container

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/log"
)

// appleServiceManager implements ServiceManager using the Apple container CLI.
type appleServiceManager struct {
	containerBin string
	networkID    string
}

// SetNetworkID sets the network for service containers.
func (m *appleServiceManager) SetNetworkID(id string) {
	m.networkID = id
}

// StartService starts a service container on the Apple container runtime.
func (m *appleServiceManager) StartService(ctx context.Context, cfg ServiceConfig) (ServiceInfo, error) {
	if cfg.Image == "" {
		return ServiceInfo{}, fmt.Errorf("service %s: image is required", cfg.Name)
	}

	// Pull image if needed
	image := cfg.Image + ":" + cfg.Version
	pullCmd := exec.CommandContext(ctx, m.containerBin, "image", "pull", image)
	if pullOutput, pullErr := pullCmd.CombinedOutput(); pullErr != nil {
		return ServiceInfo{}, fmt.Errorf("pulling image %s: %s: %w", image, strings.TrimSpace(string(pullOutput)), pullErr)
	}

	args := buildAppleRunArgs(cfg, m.networkID)
	cmd := exec.CommandContext(ctx, m.containerBin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ServiceInfo{}, fmt.Errorf("starting %s service: %s: %w", cfg.Name, strings.TrimSpace(string(output)), err)
	}

	containerID := strings.TrimSpace(string(output))
	log.Debug("started apple service container", "service", cfg.Name, "container", containerID)

	return buildServiceInfo(containerID, cfg), nil
}

// CheckReady runs the readiness command inside the service container.
func (m *appleServiceManager) CheckReady(ctx context.Context, info ServiceInfo) error {
	if info.ReadinessCmd == "" {
		return nil
	}

	cmd := resolvePlaceholders(info.ReadinessCmd, info.Env, info.PasswordEnv)

	execCmd := exec.CommandContext(ctx, m.containerBin, "exec", info.ID, "sh", "-c", cmd)
	output, err := execCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("readiness check failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return nil
}

// StopService force-removes the service container.
func (m *appleServiceManager) StopService(ctx context.Context, info ServiceInfo) error {
	cmd := exec.CommandContext(ctx, m.containerBin, "rm", "--force", info.ID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(output))
		if strings.Contains(outStr, "not found") || strings.Contains(outStr, "No such") {
			return nil
		}
		return fmt.Errorf("removing service container %s: %s: %w", info.Name, outStr, err)
	}
	return nil
}

// buildAppleRunArgs constructs the CLI args for `container run`.
func buildAppleRunArgs(cfg ServiceConfig, networkID string) []string {
	image := cfg.Image + ":" + cfg.Version
	containerName := fmt.Sprintf("moat-%s-%s", cfg.Name, cfg.RunID)

	args := []string{
		"run", "--detach",
		"--name", containerName,
		"--hostname", cfg.Name,
	}

	if networkID != "" {
		args = append(args, "--network", networkID)
	}

	// Sort env keys for deterministic ordering
	envKeys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)

	for _, k := range envKeys {
		args = append(args, "--env", k+"="+cfg.Env[k])
	}

	// Image
	args = append(args, image)

	// Extra cmd args (after image)
	if len(cfg.ExtraCmd) > 0 {
		for _, c := range cfg.ExtraCmd {
			args = append(args, resolvePlaceholders(c, cfg.Env, cfg.PasswordEnv))
		}
	}

	return args
}
```

**Step 4: Run unit tests**

Run: `go test ./internal/container/ -v -run TestAppleBuildRunArgs`

Expected: PASS

**Step 5: Commit**

```bash
git add internal/container/apple_service.go internal/container/apple_service_test.go
git commit -m "feat(container): implement appleServiceManager"
```

---

### Task 4: Wire managers into `AppleRuntime`

**Files:**
- Modify: `internal/container/apple.go:40-73` — add manager fields, initialize in constructor, update return methods
- Modify: `internal/container/feature_managers_test.go:19-28` — update test expectations

**Step 1: Update the test expectations**

In `feature_managers_test.go`, the `TestAppleRuntimeNoFeatureManagers` test currently asserts both managers are nil. Update it:

```go
func TestAppleRuntimeFeatureManagers(t *testing.T) {
	rt, err := NewAppleRuntime()
	if err != nil {
		t.Skip("Apple containers not available")
	}
	defer rt.Close()

	assert.NotNil(t, rt.NetworkManager(), "Apple should provide NetworkManager")
	assert.Nil(t, rt.SidecarManager(), "Apple should not provide SidecarManager")
	assert.NotNil(t, rt.ServiceManager(), "Apple should provide ServiceManager")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/container/ -v -run TestAppleRuntimeFeatureManagers`

Expected: FAIL — `NetworkManager()` still returns nil.

**Step 3: Update `AppleRuntime`**

In `apple.go`, add fields to the struct and initialize in the constructor:

```go
// AppleRuntime struct — add fields:
type AppleRuntime struct {
	containerBin string
	hostAddress  string

	buildMgr   *appleBuildManager
	networkMgr *appleNetworkManager
	serviceMgr *appleServiceManager
}

// In NewAppleRuntime, after creating buildMgr:
r.networkMgr = &appleNetworkManager{containerBin: binPath}
r.serviceMgr = &appleServiceManager{containerBin: binPath}
```

Update the three methods:

```go
// NetworkManager returns the Apple network manager.
func (r *AppleRuntime) NetworkManager() NetworkManager {
	return r.networkMgr
}

// SidecarManager returns nil - not implemented for Apple containers.
func (r *AppleRuntime) SidecarManager() SidecarManager {
	return nil
}

// ServiceManager returns the Apple service manager.
func (r *AppleRuntime) ServiceManager() ServiceManager {
	return r.serviceMgr
}
```

**Step 4: Run tests**

Run: `go test ./internal/container/ -v -run TestAppleRuntimeFeatureManagers`

Expected: PASS (or skip if not on macOS).

**Step 5: Run full package tests**

Run: `go test ./internal/container/`

Expected: PASS

**Step 6: Commit**

```bash
git add internal/container/apple.go internal/container/feature_managers_test.go
git commit -m "feat(container): wire NetworkManager and ServiceManager into AppleRuntime"
```

---

### Task 5: Update error messages and documentation

Now that Apple containers support service dependencies, update the error message and docs.

**Files:**
- Modify: `internal/run/manager.go:1525-1527` — update error message (no longer Apple-specific)
- Modify: `docs/content/guides/07-service-dependencies.md` — remove "Apple containers do not support service dependencies" prereq, update Apple-specific troubleshooting
- Modify: `docs/content/concepts/06-dependencies.md` — update runtime requirements section

**Step 1: Update error message in `manager.go`**

The error at line 1525-1527 currently says "Apple containers don't support service dependencies". Since Apple now supports them, this nil check only fires if a future runtime doesn't implement `ServiceManager`. Make it generic:

```go
return nil, fmt.Errorf("service dependencies require a runtime with service support\n\n" +
    "Either:\n  - Use Docker or Apple container runtime\n  - Install services on your host and set MOAT_*_URL manually")
```

**Step 2: Update guide prerequisites**

In `docs/content/guides/07-service-dependencies.md`, the Prerequisites section says "Docker runtime is required. Apple containers do not support service dependencies." Change to:

```markdown
## Prerequisites

Docker or Apple container runtime is required. Apple container networking requires macOS 26 or later.
```

**Step 3: Update concepts page**

In `docs/content/concepts/06-dependencies.md`, the "Runtime requirements" section (around line 248-259) says Apple containers do not support services. Update:

```markdown
### Runtime requirements

Service dependencies require Docker or Apple container runtime. Apple container networking requires macOS 26 or later.
```

Remove the error example that says "Apple containers don't support service dependencies".

**Step 4: Update troubleshooting**

In `docs/content/guides/07-service-dependencies.md`, the "Apple containers" troubleshooting section says they're not supported. Replace with a note about macOS version:

```markdown
### macOS version

Apple container networking requires macOS 26 or later. On macOS 15, use Docker runtime instead.

```text
Error: creating service network: ...
```

If you see network creation errors on Apple containers, verify you're running macOS 26+.
```

**Step 5: Run tests**

Run: `go test ./internal/run/ -v -run TestCreate`

Expected: PASS (error message change doesn't affect test logic).

**Step 6: Commit**

```bash
git add internal/run/manager.go docs/content/guides/07-service-dependencies.md docs/content/concepts/06-dependencies.md
git commit -m "docs: update service dependencies for Apple container support"
```

---

### Task 6: E2E tests

The existing E2E tests in `internal/e2e/services_test.go` use `skipIfNoDocker`. Add Apple-compatible variants or make existing tests runtime-agnostic.

**Files:**
- Modify: `internal/e2e/services_test.go` — make tests work on both runtimes

**Step 1: Check what `skipIfNoDocker` does**

Read `internal/e2e/services_test.go` and the helper. The existing tests call `skipIfNoDocker(t)` which skips if Docker isn't available. The `run.NewManager()` call auto-detects the runtime. If we remove `skipIfNoDocker` and replace with a check for any supported runtime, the same tests work on Apple containers.

**Step 2: Add a `skipIfNoServiceRuntime` helper**

```go
func skipIfNoServiceRuntime(t *testing.T) {
	t.Helper()
	mgr, err := run.NewManager()
	if err != nil {
		t.Skipf("No runtime available: %v", err)
	}
	defer mgr.Close()
	// The runtime is available — services will work on Docker or Apple (macOS 26+)
}
```

Or simpler: just use the existing `skipIfNoDocker` for now and add `skipIfNoApple` tests separately if we want Apple-specific coverage. The service tests should work on either runtime since they go through the abstract interface.

**Step 3: Replace `skipIfNoDocker` with runtime-agnostic skip**

In each service test (`TestServicePostgres`, `TestServiceRedis`, `TestServiceMySQL`, etc.), replace `skipIfNoDocker(t)` with a skip that checks for either runtime. The simplest approach: try to create a manager, if it fails skip.

Actually — check if `skipIfNoDocker` is used because the tests specifically need Docker, or just because services only worked on Docker. If the latter, we can just let the tests run on whatever runtime is available.

Look at `createTestWorkspaceWithDeps` — it may force Docker. If the helper is runtime-agnostic, the tests already work on Apple. If it forces Docker, update it.

**Step 4: Run E2E tests on available runtime**

Run: `go test -tags=e2e -v ./internal/e2e/ -run TestServicePostgres -timeout 120s`

Expected: PASS on whichever runtime is available.

**Step 5: Commit**

```bash
git add internal/e2e/services_test.go
git commit -m "test(e2e): make service tests runtime-agnostic"
```

---

### Task 7: Lint and verify

**Step 1: Run linter**

Run: `golangci-lint run ./internal/container/ ./internal/run/`

Expected: No new issues.

**Step 2: Run all unit tests**

Run: `go test ./...`

Expected: PASS

**Step 3: Commit any fixes if needed**

---

## File summary

| File | Action |
|------|--------|
| `internal/container/service_helpers.go` | Create — shared `buildServiceInfo`, `resolvePlaceholders` |
| `internal/container/service_helpers_test.go` | Create — tests moved from `docker_service_test.go` |
| `internal/container/apple_network.go` | Create — `appleNetworkManager` |
| `internal/container/apple_network_test.go` | Create — network manager tests |
| `internal/container/apple_service.go` | Create — `appleServiceManager` |
| `internal/container/apple_service_test.go` | Create — service manager unit tests |
| `internal/container/apple.go` | Modify — wire managers, update return methods |
| `internal/container/docker_service.go` | Modify — remove extracted helpers |
| `internal/container/docker_service_test.go` | Modify — remove extracted tests |
| `internal/container/feature_managers_test.go` | Modify — update Apple assertions |
| `internal/run/manager.go` | Modify — update error message |
| `docs/content/guides/07-service-dependencies.md` | Modify — update prerequisites and troubleshooting |
| `docs/content/concepts/06-dependencies.md` | Modify — update runtime requirements |
