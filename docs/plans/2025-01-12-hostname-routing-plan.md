# Hostname-Based Service Routing Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Expose container services via hostname routing (`http://web.myapp.localhost:8080`) with automatic proxy lifecycle, agent naming, and environment injection.

**Architecture:** Shared reverse proxy routes Host headers to container ports. Agent processes register/deregister routes via file-locked JSON. Proxy starts with first agent, stops with last.

**Tech Stack:** Go stdlib `net/http/httputil.ReverseProxy`, file-based coordination, Docker port bindings.

---

## Task 1: Random Name Generator

**Files:**
- Create: `internal/name/name.go`
- Create: `internal/name/name_test.go`

**Step 1: Write the failing test**

Create `internal/name/name_test.go`:

```go
package name

import (
	"regexp"
	"testing"
)

func TestGenerate(t *testing.T) {
	name := Generate()

	// Should match adjective-animal pattern
	pattern := regexp.MustCompile(`^[a-z]+-[a-z]+$`)
	if !pattern.MatchString(name) {
		t.Errorf("Generate() = %q, want adjective-animal format", name)
	}
}

func TestGenerateUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		name := Generate()
		if seen[name] {
			// Duplicates are possible but unlikely in 100 tries with ~2500 combinations
			t.Logf("Duplicate name after %d generations: %s", i, name)
		}
		seen[name] = true
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/name/... -v
```

Expected: FAIL with "package internal/name is not in std"

**Step 3: Write minimal implementation**

Create `internal/name/name.go`:

```go
// Package name generates random agent names.
package name

import (
	"math/rand"
	"time"
)

var adjectives = []string{
	"bold", "brave", "bright", "calm", "clever",
	"cool", "eager", "fair", "fast", "fierce",
	"fluffy", "gentle", "happy", "jolly", "keen",
	"kind", "lively", "lucky", "merry", "mighty",
	"noble", "proud", "quick", "quiet", "sharp",
	"silly", "sleek", "smart", "snappy", "speedy",
	"steady", "swift", "tender", "tough", "vivid",
	"warm", "wild", "wise", "witty", "zany",
	"zen", "zesty", "agile", "alert", "bold",
	"cosmic", "daring", "epic", "focal", "grand",
}

var animals = []string{
	"badger", "bear", "beaver", "bison", "cat",
	"cheetah", "chicken", "coyote", "crane", "crow",
	"deer", "dog", "dolphin", "dove", "dragon",
	"eagle", "falcon", "ferret", "finch", "fox",
	"frog", "gopher", "hawk", "heron", "horse",
	"jaguar", "koala", "lemur", "lion", "lynx",
	"meerkat", "moose", "narwhal", "octopus", "otter",
	"owl", "panda", "parrot", "penguin", "pigeon",
	"puma", "quail", "rabbit", "raven", "salmon",
	"seal", "shark", "snake", "sparrow", "spider",
	"squid", "swan", "tiger", "turtle", "viper",
	"walrus", "whale", "wolf", "wombat", "yak",
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Generate returns a random name in adjective-animal format.
func Generate() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	animal := animals[rand.Intn(len(animals))]
	return adj + "-" + animal
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/name/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/name/
git commit -m "feat(name): add random name generator for agents"
```

---

## Task 2: Add Name Field to Config

**Files:**
- Modify: `internal/config/config.go:14-22`
- Modify: `internal/config/config_test.go`

**Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestLoadConfigWithName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent.yaml")

	content := `
name: myapp
agent: test-agent
ports:
  web: 3000
  api: 8080
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "myapp" {
		t.Errorf("Name = %q, want %q", cfg.Name, "myapp")
	}
	if len(cfg.Ports) != 2 {
		t.Fatalf("Ports = %d, want 2", len(cfg.Ports))
	}
	if cfg.Ports["web"] != 3000 {
		t.Errorf("Ports[web] = %d, want 3000", cfg.Ports["web"])
	}
	if cfg.Ports["api"] != 8080 {
		t.Errorf("Ports[api] = %d, want 8080", cfg.Ports["api"])
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -run TestLoadConfigWithName -v
```

Expected: FAIL with "cfg.Name undefined"

**Step 3: Write minimal implementation**

Edit `internal/config/config.go`, add `Name` field to Config struct at line 15:

```go
// Config represents an agent.yaml manifest.
type Config struct {
	Name    string            `yaml:"name,omitempty"`
	Agent   string            `yaml:"agent"`
	Version string            `yaml:"version,omitempty"`
	Runtime Runtime           `yaml:"runtime,omitempty"`
	Grants  []string          `yaml:"grants,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Mounts  []string          `yaml:"mounts,omitempty"`
	Ports   map[string]int    `yaml:"ports,omitempty"`
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/config/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add name field to agent.yaml"
```

---

## Task 3: Global Config for Proxy Port

**Files:**
- Create: `internal/config/global.go`
- Create: `internal/config/global_test.go`

**Step 1: Write the failing test**

Create `internal/config/global_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGlobalConfig(t *testing.T) {
	// Create temp home directory
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Create config file
	configDir := filepath.Join(tmpHome, ".agentops")
	os.MkdirAll(configDir, 0755)
	configPath := filepath.Join(configDir, "config.yaml")

	content := `
proxy:
  port: 9000
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if cfg.Proxy.Port != 9000 {
		t.Errorf("Proxy.Port = %d, want 9000", cfg.Proxy.Port)
	}
}

func TestLoadGlobalConfigDefaults(t *testing.T) {
	// Create temp home with no config
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if cfg.Proxy.Port != 8080 {
		t.Errorf("Proxy.Port = %d, want default 8080", cfg.Proxy.Port)
	}
}

func TestLoadGlobalConfigEnvOverride(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	os.Setenv("AGENTOPS_PROXY_PORT", "7000")
	defer os.Unsetenv("AGENTOPS_PROXY_PORT")

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if cfg.Proxy.Port != 7000 {
		t.Errorf("Proxy.Port = %d, want 7000 from env", cfg.Proxy.Port)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -run TestLoadGlobal -v
```

Expected: FAIL with "LoadGlobal undefined"

**Step 3: Write minimal implementation**

Create `internal/config/global.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// GlobalConfig holds global AgentOps settings from ~/.agentops/config.yaml.
type GlobalConfig struct {
	Proxy ProxyConfig `yaml:"proxy"`
}

// ProxyConfig holds reverse proxy settings.
type ProxyConfig struct {
	Port int `yaml:"port"`
}

// DefaultGlobalConfig returns the default global configuration.
func DefaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Proxy: ProxyConfig{
			Port: 8080,
		},
	}
}

// LoadGlobal reads ~/.agentops/config.yaml and applies environment overrides.
func LoadGlobal() (*GlobalConfig, error) {
	cfg := DefaultGlobalConfig()

	// Try to load from file
	homeDir, err := os.UserHomeDir()
	if err == nil {
		configPath := filepath.Join(homeDir, ".agentops", "config.yaml")
		if data, err := os.ReadFile(configPath); err == nil {
			yaml.Unmarshal(data, cfg)
		}
	}

	// Apply environment overrides
	if portStr := os.Getenv("AGENTOPS_PROXY_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			cfg.Proxy.Port = port
		}
	}

	return cfg, nil
}

// GlobalConfigDir returns the path to ~/.agentops.
func GlobalConfigDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".agentops")
	}
	return filepath.Join(homeDir, ".agentops")
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/config/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/global.go internal/config/global_test.go
git commit -m "feat(config): add global config for proxy port"
```

---

## Task 4: Add PortBindings to Container Config

**Files:**
- Modify: `internal/container/runtime.go:63-80`

**Step 1: Add PortBindings field**

Edit `internal/container/runtime.go`, update Config struct:

```go
// Config holds configuration for creating a container.
type Config struct {
	Name         string
	Image        string
	Cmd          []string
	WorkingDir   string
	Env          []string
	Mounts       []MountConfig
	ExtraHosts   []string       // host:ip mappings (Docker-specific)
	NetworkMode  string         // "bridge", "host", "none" (Docker-specific)
	PortBindings map[int]string // container port -> host bind address (e.g., 3000 -> "127.0.0.1")
}
```

**Step 2: Add GetPortBindings to Runtime interface**

Edit `internal/container/runtime.go`, add method to interface after line 47:

```go
	// GetPortBindings returns the actual host ports mapped to container ports.
	// Call after container is started. Returns map[containerPort]hostPort.
	GetPortBindings(ctx context.Context, id string) (map[int]int, error)
```

**Step 3: Commit**

```bash
git add internal/container/runtime.go
git commit -m "feat(container): add port binding config and interface method"
```

---

## Task 5: Implement Docker Port Bindings

**Files:**
- Modify: `internal/container/docker.go:44-91`

**Step 1: Update CreateContainer for port bindings**

Edit `internal/container/docker.go`. First add import for `github.com/docker/go-connections/nat`.

Then update `CreateContainer` method to handle port bindings:

```go
func (r *DockerRuntime) CreateContainer(ctx context.Context, cfg Config) (string, error) {
	// Pull image if not present
	if err := r.ensureImage(ctx, cfg.Image); err != nil {
		return "", err
	}

	// Convert mounts
	mounts := make([]mount.Mount, len(cfg.Mounts))
	for i, m := range cfg.Mounts {
		mounts[i] = mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		}
	}

	// Default to bridge network if not specified
	networkMode := container.NetworkMode(cfg.NetworkMode)
	if cfg.NetworkMode == "" {
		networkMode = "bridge"
	}

	// Build port bindings
	var exposedPorts nat.PortSet
	var portBindings nat.PortMap
	if len(cfg.PortBindings) > 0 {
		exposedPorts = make(nat.PortSet)
		portBindings = make(nat.PortMap)
		for containerPort, hostIP := range cfg.PortBindings {
			port := nat.Port(fmt.Sprintf("%d/tcp", containerPort))
			exposedPorts[port] = struct{}{}
			portBindings[port] = []nat.PortBinding{{
				HostIP:   hostIP,
				HostPort: "", // Let Docker assign random port
			}}
		}
	}

	resp, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image:        cfg.Image,
			Cmd:          cfg.Cmd,
			WorkingDir:   cfg.WorkingDir,
			Env:          cfg.Env,
			Tty:          true,
			OpenStdin:    true,
			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			Mounts:       mounts,
			NetworkMode:  networkMode,
			ExtraHosts:   cfg.ExtraHosts,
			PortBindings: portBindings,
		},
		nil, // network config
		nil, // platform
		cfg.Name,
	)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}

	return resp.ID, nil
}
```

**Step 2: Implement GetPortBindings**

Add after `StartContainer` method:

```go
// GetPortBindings returns the actual host ports assigned to container ports.
func (r *DockerRuntime) GetPortBindings(ctx context.Context, containerID string) (map[int]int, error) {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	result := make(map[int]int)
	for port, bindings := range inspect.NetworkSettings.Ports {
		if len(bindings) == 0 {
			continue
		}
		containerPort := port.Int()
		hostPort, err := strconv.Atoi(bindings[0].HostPort)
		if err != nil {
			continue
		}
		result[containerPort] = hostPort
	}
	return result, nil
}
```

Add `"strconv"` to imports.

**Step 3: Commit**

```bash
git add internal/container/docker.go
git commit -m "feat(container): implement Docker port bindings"
```

---

## Task 6: Implement Apple Container Port Bindings

**Files:**
- Modify: `internal/container/apple.go:88-130`

**Step 1: Update buildRunArgs for port bindings**

Edit `internal/container/apple.go`, update `buildRunArgs`:

```go
func (r *AppleRuntime) buildRunArgs(cfg Config) []string {
	args := []string{"run", "--detach"}

	// Container name
	if cfg.Name != "" {
		args = append(args, "--name", cfg.Name)
	}

	// Working directory
	if cfg.WorkingDir != "" {
		args = append(args, "--workdir", cfg.WorkingDir)
	}

	// DNS configuration
	args = append(args, "--dns", "8.8.8.8")
	args = append(args, "--dns", "8.8.4.4")

	// Port bindings
	for containerPort, hostIP := range cfg.PortBindings {
		// Format: hostIP::containerPort (empty middle = random host port)
		args = append(args, "--publish", fmt.Sprintf("%s::%d", hostIP, containerPort))
	}

	// Environment variables
	for _, env := range cfg.Env {
		args = append(args, "--env", env)
	}

	// Volume mounts
	for _, m := range cfg.Mounts {
		mountStr := m.Source + ":" + m.Target
		if m.ReadOnly {
			mountStr += ":ro"
		}
		args = append(args, "--volume", mountStr)
	}

	// Image
	args = append(args, cfg.Image)

	// Command
	if len(cfg.Cmd) > 0 {
		args = append(args, cfg.Cmd...)
	}

	return args
}
```

**Step 2: Implement GetPortBindings**

Add after `StartContainer` method:

```go
// GetPortBindings returns the actual host ports assigned to container ports.
func (r *AppleRuntime) GetPortBindings(ctx context.Context, containerID string) (map[int]int, error) {
	cmd := exec.CommandContext(ctx, r.containerBin, "inspect", containerID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	// Parse the JSON output to find port mappings
	var info []struct {
		Ports []struct {
			ContainerPort int `json:"container_port"`
			HostPort      int `json:"host_port"`
		} `json:"ports"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return nil, fmt.Errorf("parsing container info: %w", err)
	}

	result := make(map[int]int)
	if len(info) > 0 {
		for _, p := range info[0].Ports {
			result[p.ContainerPort] = p.HostPort
		}
	}
	return result, nil
}
```

**Step 3: Commit**

```bash
git add internal/container/apple.go
git commit -m "feat(container): implement Apple container port bindings"
```

---

## Task 7: Route Management

**Files:**
- Create: `internal/routing/routes.go`
- Create: `internal/routing/routes_test.go`

**Step 1: Write the failing test**

Create `internal/routing/routes_test.go`:

```go
package routing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRouteTable(t *testing.T) {
	dir := t.TempDir()
	rt, err := NewRouteTable(dir)
	if err != nil {
		t.Fatalf("NewRouteTable: %v", err)
	}

	// Add routes
	err = rt.Add("myapp", map[string]string{
		"web": "127.0.0.1:49152",
		"api": "127.0.0.1:49153",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Lookup
	addr, ok := rt.Lookup("myapp", "web")
	if !ok {
		t.Fatal("Lookup(myapp, web) not found")
	}
	if addr != "127.0.0.1:49152" {
		t.Errorf("Lookup(myapp, web) = %q, want 127.0.0.1:49152", addr)
	}

	// Lookup default (first service)
	addr, ok = rt.LookupDefault("myapp")
	if !ok {
		t.Fatal("LookupDefault(myapp) not found")
	}

	// Remove
	err = rt.Remove("myapp")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, ok = rt.Lookup("myapp", "web")
	if ok {
		t.Error("Lookup after Remove should return false")
	}
}

func TestRouteTablePersistence(t *testing.T) {
	dir := t.TempDir()

	// Create and add routes
	rt1, _ := NewRouteTable(dir)
	rt1.Add("myapp", map[string]string{"web": "127.0.0.1:49152"})

	// Create new instance - should load from file
	rt2, err := NewRouteTable(dir)
	if err != nil {
		t.Fatalf("NewRouteTable: %v", err)
	}

	addr, ok := rt2.Lookup("myapp", "web")
	if !ok {
		t.Fatal("Route not persisted")
	}
	if addr != "127.0.0.1:49152" {
		t.Errorf("Lookup = %q, want 127.0.0.1:49152", addr)
	}
}

func TestRouteTableAgentExists(t *testing.T) {
	dir := t.TempDir()
	rt, _ := NewRouteTable(dir)
	rt.Add("myapp", map[string]string{"web": "127.0.0.1:49152"})

	if !rt.AgentExists("myapp") {
		t.Error("AgentExists(myapp) = false, want true")
	}
	if rt.AgentExists("other") {
		t.Error("AgentExists(other) = true, want false")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/routing/... -v
```

Expected: FAIL with "package internal/routing is not in std"

**Step 3: Write minimal implementation**

Create `internal/routing/routes.go`:

```go
// Package routing provides hostname-based reverse proxy routing.
package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// RouteTable manages agent -> service -> host:port mappings.
type RouteTable struct {
	dir    string
	routes map[string]map[string]string // agent -> service -> host:port
	mu     sync.RWMutex
}

// NewRouteTable creates or loads a route table from the given directory.
func NewRouteTable(dir string) (*RouteTable, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	rt := &RouteTable{
		dir:    dir,
		routes: make(map[string]map[string]string),
	}

	// Load existing routes
	path := filepath.Join(dir, "routes.json")
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &rt.routes)
	}

	return rt, nil
}

// Add registers routes for an agent.
func (rt *RouteTable) Add(agent string, services map[string]string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.routes[agent] = services
	return rt.save()
}

// Remove unregisters an agent's routes.
func (rt *RouteTable) Remove(agent string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	delete(rt.routes, agent)
	return rt.save()
}

// Lookup returns the host:port for an agent's service.
func (rt *RouteTable) Lookup(agent, service string) (string, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	services, ok := rt.routes[agent]
	if !ok {
		return "", false
	}
	addr, ok := services[service]
	return addr, ok
}

// LookupDefault returns the first service's address for an agent.
func (rt *RouteTable) LookupDefault(agent string) (string, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	services, ok := rt.routes[agent]
	if !ok || len(services) == 0 {
		return "", false
	}
	// Return first service (map iteration order is random but consistent for small maps)
	for _, addr := range services {
		return addr, true
	}
	return "", false
}

// AgentExists returns true if the agent has registered routes.
func (rt *RouteTable) AgentExists(agent string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	_, ok := rt.routes[agent]
	return ok
}

// Agents returns all registered agent names.
func (rt *RouteTable) Agents() []string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	agents := make([]string, 0, len(rt.routes))
	for agent := range rt.routes {
		agents = append(agents, agent)
	}
	return agents
}

func (rt *RouteTable) save() error {
	data, err := json.MarshalIndent(rt.routes, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(rt.dir, "routes.json")
	return os.WriteFile(path, data, 0644)
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/routing/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/routing/
git commit -m "feat(routing): add route table for agent services"
```

---

## Task 8: Proxy Lock File Management

**Files:**
- Create: `internal/routing/lock.go`
- Create: `internal/routing/lock_test.go`

**Step 1: Write the failing test**

Create `internal/routing/lock_test.go`:

```go
package routing

import (
	"os"
	"testing"
)

func TestProxyLock(t *testing.T) {
	dir := t.TempDir()

	// No lock initially
	info, err := LoadProxyLock(dir)
	if err != nil {
		t.Fatalf("LoadProxyLock: %v", err)
	}
	if info != nil {
		t.Error("Expected nil when no lock exists")
	}

	// Create lock
	err = SaveProxyLock(dir, ProxyLockInfo{
		PID:  12345,
		Port: 8080,
	})
	if err != nil {
		t.Fatalf("SaveProxyLock: %v", err)
	}

	// Load lock
	info, err = LoadProxyLock(dir)
	if err != nil {
		t.Fatalf("LoadProxyLock: %v", err)
	}
	if info == nil {
		t.Fatal("Expected lock info")
	}
	if info.PID != 12345 {
		t.Errorf("PID = %d, want 12345", info.PID)
	}
	if info.Port != 8080 {
		t.Errorf("Port = %d, want 8080", info.Port)
	}

	// Remove lock
	err = RemoveProxyLock(dir)
	if err != nil {
		t.Fatalf("RemoveProxyLock: %v", err)
	}

	info, _ = LoadProxyLock(dir)
	if info != nil {
		t.Error("Expected nil after remove")
	}
}

func TestProxyLockIsAlive(t *testing.T) {
	// Current process should be alive
	info := &ProxyLockInfo{PID: os.Getpid()}
	if !info.IsAlive() {
		t.Error("Current process should be alive")
	}

	// Non-existent process should not be alive
	info = &ProxyLockInfo{PID: 999999999}
	if info.IsAlive() {
		t.Error("Non-existent process should not be alive")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/routing/... -run TestProxyLock -v
```

Expected: FAIL with "LoadProxyLock undefined"

**Step 3: Write minimal implementation**

Create `internal/routing/lock.go`:

```go
package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ProxyLockInfo holds information about a running proxy.
type ProxyLockInfo struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
}

// IsAlive checks if the process is still running.
func (p *ProxyLockInfo) IsAlive() bool {
	process, err := os.FindProcess(p.PID)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Send signal 0 to check if alive.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// LoadProxyLock loads the proxy lock file from the given directory.
// Returns nil, nil if the lock file doesn't exist.
func LoadProxyLock(dir string) (*ProxyLockInfo, error) {
	path := filepath.Join(dir, "proxy.lock")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var info ProxyLockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// SaveProxyLock writes the proxy lock file.
func SaveProxyLock(dir string, info ProxyLockInfo) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if info.StartedAt.IsZero() {
		info.StartedAt = time.Now()
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(dir, "proxy.lock")
	return os.WriteFile(path, data, 0644)
}

// RemoveProxyLock removes the proxy lock file.
func RemoveProxyLock(dir string) error {
	path := filepath.Join(dir, "proxy.lock")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/routing/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/routing/lock.go internal/routing/lock_test.go
git commit -m "feat(routing): add proxy lock file management"
```

---

## Task 9: Reverse Proxy Server

**Files:**
- Create: `internal/routing/proxy.go`
- Create: `internal/routing/proxy_test.go`

**Step 1: Write the failing test**

Create `internal/routing/proxy_test.go`:

```go
package routing

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReverseProxy(t *testing.T) {
	// Create a backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from backend"))
	}))
	defer backend.Close()

	// Create route table with backend
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("myapp", map[string]string{
		"web": backend.Listener.Addr().String(),
	})

	// Create reverse proxy
	rp := NewReverseProxy(routes)

	// Test routing via Host header
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "web.myapp.localhost:8080"
	rec := httptest.NewRecorder()

	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rec.Code)
	}

	body, _ := io.ReadAll(rec.Body)
	if string(body) != "hello from backend" {
		t.Errorf("Body = %q, want 'hello from backend'", body)
	}
}

func TestReverseProxyUnknownAgent(t *testing.T) {
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	rp := NewReverseProxy(routes)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "web.unknown.localhost:8080"
	rec := httptest.NewRecorder()

	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", rec.Code)
	}
}

func TestReverseProxyDefaultService(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("default"))
	}))
	defer backend.Close()

	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("myapp", map[string]string{
		"web": backend.Listener.Addr().String(),
	})

	rp := NewReverseProxy(routes)

	// Request without service prefix
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "myapp.localhost:8080"
	rec := httptest.NewRecorder()

	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rec.Code)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/routing/... -run TestReverseProxy -v
```

Expected: FAIL with "NewReverseProxy undefined"

**Step 3: Write minimal implementation**

Create `internal/routing/proxy.go`:

```go
package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// ReverseProxy routes requests based on Host header to container services.
type ReverseProxy struct {
	routes *RouteTable
}

// NewReverseProxy creates a reverse proxy with the given route table.
func NewReverseProxy(routes *RouteTable) *ReverseProxy {
	return &ReverseProxy{routes: routes}
}

// ServeHTTP handles incoming requests and routes them to backends.
func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse Host header: [service.]agent.localhost[:port]
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx] // Remove port
	}

	// Remove .localhost suffix
	host = strings.TrimSuffix(host, ".localhost")

	parts := strings.SplitN(host, ".", 2)
	var service, agent string
	if len(parts) == 2 {
		service = parts[0]
		agent = parts[1]
	} else {
		// No service prefix, just agent name
		agent = parts[0]
	}

	// Lookup backend address
	var backendAddr string
	var ok bool
	if service != "" {
		backendAddr, ok = rp.routes.Lookup(agent, service)
	}
	if !ok {
		backendAddr, ok = rp.routes.LookupDefault(agent)
	}

	if !ok {
		rp.writeError(w, http.StatusNotFound, "unknown agent", agent)
		return
	}

	// Proxy the request
	target, err := url.Parse("http://" + backendAddr)
	if err != nil {
		rp.writeError(w, http.StatusInternalServerError, "invalid backend", backendAddr)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		rp.writeError(w, http.StatusBadGateway, "service unavailable", err.Error())
	}

	proxy.ServeHTTP(w, r)
}

func (rp *ReverseProxy) writeError(w http.ResponseWriter, code int, errType, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{
		"error":  errType,
		"detail": detail,
	})
}

// ProxyServer wraps the reverse proxy with lifecycle management.
type ProxyServer struct {
	rp       *ReverseProxy
	server   *http.Server
	listener net.Listener
	port     int
}

// NewProxyServer creates a new proxy server.
func NewProxyServer(routes *RouteTable) *ProxyServer {
	return &ProxyServer{
		rp: NewReverseProxy(routes),
	}
}

// Start starts the proxy server on the given port.
func (ps *ProxyServer) Start(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	ps.listener = listener
	ps.port = listener.Addr().(*net.TCPAddr).Port
	ps.server = &http.Server{Handler: ps.rp}

	go ps.server.Serve(listener)
	return nil
}

// Port returns the port the server is listening on.
func (ps *ProxyServer) Port() int {
	return ps.port
}

// Stop gracefully shuts down the proxy server.
func (ps *ProxyServer) Stop(ctx context.Context) error {
	if ps.server == nil {
		return nil
	}
	return ps.server.Shutdown(ctx)
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/routing/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/routing/proxy.go internal/routing/proxy_test.go
git commit -m "feat(routing): add hostname-based reverse proxy"
```

---

## Task 10: Add Name and Ports to Run Struct

**Files:**
- Modify: `internal/run/run.go:26-49`
- Modify: `internal/storage/storage.go:16-25`

**Step 1: Update Run struct**

Edit `internal/run/run.go`, add fields to Run struct:

```go
// Run represents an agent execution environment.
type Run struct {
	ID          string
	Name        string            // Human-friendly name (e.g., "myapp" or "fluffy-chicken")
	Agent       string
	Workspace   string
	Grants      []string
	Ports       map[string]int    // service name -> container port
	HostPorts   map[string]int    // service name -> host port (after binding)
	State       State
	ContainerID string
	ProxyServer *proxy.Server     // Auth proxy for credential injection
	Store       *storage.RunStore // Run data storage
	CreatedAt   time.Time
	StartedAt   time.Time
	StoppedAt   time.Time
	Error       string
}
```

**Step 2: Update Options struct**

Edit `internal/run/run.go`, add Name to Options:

```go
// Options configures a new run.
type Options struct {
	Name      string         // Optional explicit name (--name flag)
	Agent     string
	Workspace string
	Grants    []string
	Cmd       []string       // Command to run (default: /bin/bash)
	Config    *config.Config // Optional agent.yaml config
	Env       []string       // Additional environment variables (KEY=VALUE)
}
```

**Step 3: Update storage Metadata**

Edit `internal/storage/storage.go`, add fields to Metadata:

```go
// Metadata holds information about an agent run.
type Metadata struct {
	Agent     string         `json:"agent"`
	Name      string         `json:"name,omitempty"`
	Workspace string         `json:"workspace"`
	Grants    []string       `json:"grants,omitempty"`
	Ports     map[string]int `json:"ports,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
	StartedAt time.Time      `json:"started_at,omitempty"`
	StoppedAt time.Time      `json:"stopped_at,omitempty"`
	Error     string         `json:"error,omitempty"`
}
```

**Step 4: Commit**

```bash
git add internal/run/run.go internal/storage/storage.go
git commit -m "feat(run): add name and ports fields to Run struct"
```

---

## Task 11: Add --name Flag to CLI

**Files:**
- Modify: `cmd/agent/cli/run.go:17-67`

**Step 1: Add flag variable and flag registration**

Edit `cmd/agent/cli/run.go`, add to var block:

```go
var (
	grants      []string
	runEnv      []string
	runtimeFlag string
	nameFlag    string
)
```

Edit `init()` function, add:

```go
runCmd.Flags().StringVar(&nameFlag, "name", "", "name for this agent instance (default: from agent.yaml or random)")
```

**Step 2: Update runAgent function**

Edit the `runAgent` function to use the name flag, adding after config loading:

```go
	// Determine agent name: --name flag > config.Name > random
	agentInstanceName := nameFlag
	if agentInstanceName == "" && cfg != nil && cfg.Name != "" {
		agentInstanceName = cfg.Name
	}
	// Random name generation happens in manager.Create if still empty
```

Update the Options to include Name:

```go
	opts := run.Options{
		Name:      agentInstanceName,
		Agent:     agentName,
		Workspace: absPath,
		Grants:    grants,
		Cmd:       containerCmd,
		Config:    cfg,
		Env:       runEnv,
	}
```

**Step 3: Commit**

```bash
git add cmd/agent/cli/run.go
git commit -m "feat(cli): add --name flag to agent run"
```

---

## Task 12: Integrate Naming and Port Binding in Manager

**Files:**
- Modify: `internal/run/manager.go`

**Step 1: Add imports**

Add to imports:

```go
	"github.com/andybons/agentops/internal/name"
	"github.com/andybons/agentops/internal/routing"
```

**Step 2: Add route table to Manager**

Update Manager struct:

```go
type Manager struct {
	runtime    container.Runtime
	runs       map[string]*Run
	runsByName map[string]*Run // index by name for collision detection
	routes     *routing.RouteTable
	mu         sync.RWMutex
}
```

Update NewManager:

```go
func NewManager() (*Manager, error) {
	rt, err := container.NewRuntime()
	if err != nil {
		return nil, fmt.Errorf("initializing container runtime: %w", err)
	}

	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")
	routes, err := routing.NewRouteTable(proxyDir)
	if err != nil {
		return nil, fmt.Errorf("initializing route table: %w", err)
	}

	return &Manager{
		runtime:    rt,
		runs:       make(map[string]*Run),
		runsByName: make(map[string]*Run),
		routes:     routes,
	}, nil
}
```

**Step 3: Update Create to handle naming and ports**

At the start of Create, add name resolution:

```go
func (m *Manager) Create(ctx context.Context, opts Options) (*Run, error) {
	// Resolve agent name
	agentName := opts.Name
	if agentName == "" {
		// Generate random name
		for i := 0; i < 3; i++ {
			agentName = name.Generate()
			if !m.routes.AgentExists(agentName) {
				break
			}
		}
		// If still colliding after 3 tries, append random suffix
		if m.routes.AgentExists(agentName) {
			agentName = agentName + "-" + generateID()[4:8]
		}
	} else {
		// Check for collision with explicit name
		if m.routes.AgentExists(agentName) {
			return nil, fmt.Errorf("agent %q is already running. Use --name to specify a different name, or stop the existing agent first", agentName)
		}
	}

	// Get ports from config
	var ports map[string]int
	if opts.Config != nil && len(opts.Config.Ports) > 0 {
		ports = opts.Config.Ports
	}

	r := &Run{
		ID:        generateID(),
		Name:      agentName,
		Agent:     opts.Agent,
		Workspace: opts.Workspace,
		Grants:    opts.Grants,
		Ports:     ports,
		State:     StateCreated,
		CreatedAt: time.Now(),
	}
	// ... rest of existing Create logic
```

**Step 4: Add port bindings to container config**

Before CreateContainer call, build port bindings:

```go
	// Build port bindings for exposed services
	var portBindings map[int]string
	if len(ports) > 0 {
		portBindings = make(map[int]string)
		for _, containerPort := range ports {
			portBindings[containerPort] = "127.0.0.1"
		}
	}
```

Add to the Config struct:

```go
	containerID, err := m.runtime.CreateContainer(ctx, container.Config{
		Name:         r.ID,
		Image:        image.Resolve(opts.Config),
		Cmd:          cmd,
		WorkingDir:   "/workspace",
		Env:          proxyEnv,
		ExtraHosts:   extraHosts,
		NetworkMode:  networkMode,
		Mounts:       mounts,
		PortBindings: portBindings,
	})
```

**Step 5: Add runs to runsByName index**

At the end of Create:

```go
	m.mu.Lock()
	m.runs[r.ID] = r
	m.runsByName[r.Name] = r
	m.mu.Unlock()
```

**Step 6: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): integrate naming and port binding in manager"
```

---

## Task 13: Register Routes and Inject Environment on Start

**Files:**
- Modify: `internal/run/manager.go`

**Step 1: Update Start to get port bindings and register routes**

After container starts in Start method:

```go
func (m *Manager) Start(ctx context.Context, runID string) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %s not found", runID)
	}
	r.State = StateStarting
	m.mu.Unlock()

	if err := m.runtime.StartContainer(ctx, r.ContainerID); err != nil {
		m.mu.Lock()
		r.State = StateFailed
		r.Error = err.Error()
		m.mu.Unlock()
		return err
	}

	// Get actual port bindings after container starts
	if len(r.Ports) > 0 {
		bindings, err := m.runtime.GetPortBindings(ctx, r.ContainerID)
		if err != nil {
			// Log but don't fail - container is running
			fmt.Fprintf(os.Stderr, "Warning: getting port bindings: %v\n", err)
		} else {
			r.HostPorts = make(map[string]int)
			services := make(map[string]string)
			for serviceName, containerPort := range r.Ports {
				if hostPort, ok := bindings[containerPort]; ok {
					r.HostPorts[serviceName] = hostPort
					services[serviceName] = fmt.Sprintf("127.0.0.1:%d", hostPort)
				}
			}
			// Register routes
			if len(services) > 0 {
				if err := m.routes.Add(r.Name, services); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: registering routes: %v\n", err)
				}
			}
		}
	}

	m.mu.Lock()
	r.State = StateRunning
	r.StartedAt = time.Now()
	m.mu.Unlock()

	// Stream logs to stdout
	go m.streamLogs(context.Background(), r)

	return nil
}
```

**Step 2: Add environment injection helper**

Before container creation in Create, add host environment variables:

```go
	// Build AGENTOPS_* environment variables for host injection
	if len(ports) > 0 {
		globalCfg, _ := config.LoadGlobal()
		proxyPort := globalCfg.Proxy.Port

		baseHost := fmt.Sprintf("%s.localhost:%d", agentName, proxyPort)
		proxyEnv = append(proxyEnv, "AGENTOPS_HOST="+baseHost)
		proxyEnv = append(proxyEnv, "AGENTOPS_URL=http://"+baseHost)

		for serviceName := range ports {
			upperName := strings.ToUpper(serviceName)
			serviceHost := fmt.Sprintf("%s.%s.localhost:%d", serviceName, agentName, proxyPort)
			proxyEnv = append(proxyEnv, fmt.Sprintf("AGENTOPS_HOST_%s=%s", upperName, serviceHost))
			proxyEnv = append(proxyEnv, fmt.Sprintf("AGENTOPS_URL_%s=http://%s", upperName, serviceHost))
		}
	}
```

**Step 3: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): register routes and inject host environment"
```

---

## Task 14: Unregister Routes on Destroy

**Files:**
- Modify: `internal/run/manager.go`

**Step 1: Update Destroy to unregister routes**

In the Destroy method, add route removal:

```go
func (m *Manager) Destroy(ctx context.Context, runID string) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %s not found", runID)
	}

	if r.State == StateRunning {
		m.mu.Unlock()
		return fmt.Errorf("cannot destroy running run %s; stop it first", runID)
	}
	m.mu.Unlock()

	// Unregister routes
	if r.Name != "" {
		if err := m.routes.Remove(r.Name); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: removing routes: %v\n", err)
		}
	}

	// Remove container
	if err := m.runtime.RemoveContainer(ctx, r.ContainerID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	// Stop the proxy server if one was created and still running
	if r.ProxyServer != nil {
		if err := r.ProxyServer.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stopping proxy: %v\n", err)
		}
	}

	m.mu.Lock()
	delete(m.runs, runID)
	delete(m.runsByName, r.Name)
	m.mu.Unlock()

	return nil
}
```

**Step 2: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): unregister routes on agent destroy"
```

---

## Task 15: Proxy Lifecycle Management

**Files:**
- Create: `internal/routing/lifecycle.go`
- Create: `internal/routing/lifecycle_test.go`

**Step 1: Write the failing test**

Create `internal/routing/lifecycle_test.go`:

```go
package routing

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestProxyLifecycle(t *testing.T) {
	dir := t.TempDir()

	// Start proxy
	lc, err := NewLifecycle(dir, 0) // 0 = random port
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}

	err = lc.EnsureRunning()
	if err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}

	port := lc.Port()
	if port == 0 {
		t.Error("Port should not be 0")
	}

	// Verify proxy is accessible
	resp, err := http.Get("http://127.0.0.1:" + string(rune(port)))
	if err == nil {
		resp.Body.Close()
	}

	// Stop proxy
	err = lc.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestProxyLifecycleReuse(t *testing.T) {
	dir := t.TempDir()

	// Start first instance
	lc1, _ := NewLifecycle(dir, 0)
	lc1.EnsureRunning()
	port1 := lc1.Port()

	// Second instance should reuse
	lc2, _ := NewLifecycle(dir, 0)
	err := lc2.EnsureRunning()
	if err != nil {
		t.Fatalf("Second EnsureRunning: %v", err)
	}

	if lc2.Port() != port1 {
		t.Errorf("Port = %d, want %d (reused)", lc2.Port(), port1)
	}

	// Cleanup
	lc1.Stop(context.Background())
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/routing/... -run TestProxyLifecycle -v
```

Expected: FAIL with "NewLifecycle undefined"

**Step 3: Write minimal implementation**

Create `internal/routing/lifecycle.go`:

```go
package routing

import (
	"context"
	"fmt"
	"os"
)

// Lifecycle manages the shared reverse proxy lifecycle.
type Lifecycle struct {
	dir    string
	port   int
	server *ProxyServer
	routes *RouteTable
	isOwner bool // true if this instance started the proxy
}

// NewLifecycle creates a lifecycle manager for the proxy.
func NewLifecycle(dir string, desiredPort int) (*Lifecycle, error) {
	routes, err := NewRouteTable(dir)
	if err != nil {
		return nil, err
	}

	if desiredPort == 0 {
		desiredPort = 8080
	}

	return &Lifecycle{
		dir:    dir,
		port:   desiredPort,
		routes: routes,
	}, nil
}

// EnsureRunning starts the proxy if not already running.
func (lc *Lifecycle) EnsureRunning() error {
	// Check for existing proxy
	lock, err := LoadProxyLock(lc.dir)
	if err != nil {
		return fmt.Errorf("loading proxy lock: %w", err)
	}

	if lock != nil && lock.IsAlive() {
		// Proxy already running
		if lock.Port != lc.port && lc.port != 0 {
			return fmt.Errorf("proxy port mismatch: running on %d, requested %d. Either unset AGENTOPS_PROXY_PORT, or stop all agents to restart the proxy", lock.Port, lc.port)
		}
		lc.port = lock.Port
		lc.isOwner = false
		return nil
	}

	// Clean up stale lock if exists
	if lock != nil {
		RemoveProxyLock(lc.dir)
	}

	// Start new proxy
	lc.server = NewProxyServer(lc.routes)
	if err := lc.server.Start(lc.port); err != nil {
		return fmt.Errorf("starting proxy: %w", err)
	}

	lc.port = lc.server.Port()
	lc.isOwner = true

	// Save lock file
	if err := SaveProxyLock(lc.dir, ProxyLockInfo{
		PID:  os.Getpid(),
		Port: lc.port,
	}); err != nil {
		lc.server.Stop(context.Background())
		return fmt.Errorf("saving proxy lock: %w", err)
	}

	return nil
}

// Port returns the port the proxy is running on.
func (lc *Lifecycle) Port() int {
	return lc.port
}

// Routes returns the route table.
func (lc *Lifecycle) Routes() *RouteTable {
	return lc.routes
}

// Stop stops the proxy if this instance owns it.
func (lc *Lifecycle) Stop(ctx context.Context) error {
	if !lc.isOwner || lc.server == nil {
		return nil
	}

	if err := lc.server.Stop(ctx); err != nil {
		return err
	}

	return RemoveProxyLock(lc.dir)
}

// ShouldStop returns true if there are no more registered agents.
func (lc *Lifecycle) ShouldStop() bool {
	return len(lc.routes.Agents()) == 0
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/routing/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/routing/lifecycle.go internal/routing/lifecycle_test.go
git commit -m "feat(routing): add proxy lifecycle management"
```

---

## Task 16: Integrate Proxy Lifecycle in Manager

**Files:**
- Modify: `internal/run/manager.go`

**Step 1: Add proxy lifecycle to Manager**

Update Manager struct:

```go
type Manager struct {
	runtime       container.Runtime
	runs          map[string]*Run
	runsByName    map[string]*Run
	routes        *routing.RouteTable
	proxyLifecycle *routing.Lifecycle
	mu            sync.RWMutex
}
```

Update NewManager:

```go
func NewManager() (*Manager, error) {
	rt, err := container.NewRuntime()
	if err != nil {
		return nil, fmt.Errorf("initializing container runtime: %w", err)
	}

	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")

	globalCfg, _ := config.LoadGlobal()
	proxyPort := globalCfg.Proxy.Port

	lifecycle, err := routing.NewLifecycle(proxyDir, proxyPort)
	if err != nil {
		return nil, fmt.Errorf("initializing proxy lifecycle: %w", err)
	}

	return &Manager{
		runtime:        rt,
		runs:           make(map[string]*Run),
		runsByName:     make(map[string]*Run),
		routes:         lifecycle.Routes(),
		proxyLifecycle: lifecycle,
	}, nil
}
```

**Step 2: Start proxy when first agent with ports starts**

In Create, before returning, if ports are configured:

```go
	// Ensure proxy is running if we have ports to expose
	if len(ports) > 0 {
		if err := m.proxyLifecycle.EnsureRunning(); err != nil {
			// Clean up container
			_ = m.runtime.RemoveContainer(ctx, containerID)
			if proxyServer != nil {
				_ = proxyServer.Stop(context.Background())
			}
			return nil, fmt.Errorf("starting routing proxy: %w", err)
		}
	}
```

**Step 3: Stop proxy when last agent is destroyed**

In Destroy, after unregistering routes:

```go
	// Check if we should stop the proxy
	if m.proxyLifecycle.ShouldStop() {
		if err := m.proxyLifecycle.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stopping proxy: %v\n", err)
		}
	}
```

**Step 4: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): integrate proxy lifecycle in manager"
```

---

## Task 17: Update CLI Output

**Files:**
- Modify: `cmd/agent/cli/run.go`
- Modify: `cmd/agent/cli/list.go`

**Step 1: Update run output**

In `runAgent`, after Start succeeds, add service URLs:

```go
	log.Info("run started", "id", r.ID)
	fmt.Printf("\nStarting agent %q...\n", r.Name)
	fmt.Printf("Container: %s\n", r.ID)

	if len(r.Ports) > 0 {
		globalCfg, _ := config.LoadGlobal()
		proxyPort := globalCfg.Proxy.Port

		fmt.Println("\nServices:")
		for serviceName, containerPort := range r.Ports {
			url := fmt.Sprintf("http://%s.%s.localhost:%d", serviceName, r.Name, proxyPort)
			fmt.Printf("  %s: %s → :%d\n", serviceName, url, containerPort)
		}
		fmt.Printf("\nProxy listening on :%d\n", proxyPort)
	}
	fmt.Println()
```

Add import for `"github.com/andybons/agentops/internal/config"` if not present.

**Step 2: Update list output**

Edit `cmd/agent/cli/list.go` to show name and services:

```go
func listRuns(cmd *cobra.Command, args []string) error {
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runs := manager.List()

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(runs)
	}

	if len(runs) == 0 {
		fmt.Println("No runs found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tRUN ID\tSTATE\tSERVICES")
	for _, r := range runs {
		services := ""
		if len(r.Ports) > 0 {
			names := make([]string, 0, len(r.Ports))
			for name := range r.Ports {
				names = append(names, name)
			}
			services = strings.Join(names, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.Name,
			r.ID,
			r.State,
			services,
		)
	}
	return w.Flush()
}
```

Add `"strings"` to imports.

**Step 3: Commit**

```bash
git add cmd/agent/cli/run.go cmd/agent/cli/list.go
git commit -m "feat(cli): update output to show agent name and services"
```

---

## Task 18: Integration Test

**Files:**
- Create: `internal/routing/integration_test.go`

**Step 1: Write integration test**

Create `internal/routing/integration_test.go`:

```go
//go:build integration

package routing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFullRoutingFlow(t *testing.T) {
	dir := t.TempDir()

	// Create two backend servers simulating container services
	webBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("web service"))
	}))
	defer webBackend.Close()

	apiBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api service"))
	}))
	defer apiBackend.Close()

	// Start proxy lifecycle
	lc, err := NewLifecycle(dir, 0)
	if err != nil {
		t.Fatalf("NewLifecycle: %v", err)
	}
	defer lc.Stop(context.Background())

	if err := lc.EnsureRunning(); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}

	// Register routes for "myapp"
	routes := lc.Routes()
	err = routes.Add("myapp", map[string]string{
		"web": webBackend.Listener.Addr().String(),
		"api": apiBackend.Listener.Addr().String(),
	})
	if err != nil {
		t.Fatalf("Add routes: %v", err)
	}

	// Create HTTP client
	client := &http.Client{Timeout: 5 * time.Second}
	proxyURL := "http://127.0.0.1:" + fmt.Sprint(lc.Port())

	// Test web service
	req, _ := http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "web.myapp.localhost"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request to web service: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "web service" {
		t.Errorf("Web response = %q, want 'web service'", body)
	}

	// Test api service
	req, _ = http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "api.myapp.localhost"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request to api service: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "api service" {
		t.Errorf("API response = %q, want 'api service'", body)
	}

	// Test default service (agent without service prefix)
	req, _ = http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "myapp.localhost"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request to default service: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Default service status = %d, want 200", resp.StatusCode)
	}

	// Test unknown agent
	req, _ = http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "unknown.localhost"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request to unknown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Unknown agent status = %d, want 404", resp.StatusCode)
	}

	// Unregister and verify
	routes.Remove("myapp")
	req, _ = http.NewRequest("GET", proxyURL+"/", nil)
	req.Host = "web.myapp.localhost"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request after remove: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("After remove status = %d, want 404", resp.StatusCode)
	}
}
```

**Step 2: Run integration test**

```bash
go test -tags=integration ./internal/routing/... -v
```

Expected: PASS

**Step 3: Commit**

```bash
git add internal/routing/integration_test.go
git commit -m "test(routing): add integration test for full routing flow"
```

---

## Task 19: Documentation Update

**Files:**
- Modify: `README.md` (if exists) or create documentation

**Step 1: Add hostname routing documentation**

Create or update documentation with usage examples:

```markdown
## Hostname-Based Service Routing

Expose container services with predictable hostnames:

### Configuration

In `agent.yaml`:
```yaml
name: myapp
ports:
  web: 3000
  api: 8080
```

### Usage

```bash
# Start with configured name
agent run my-agent ./project

# Override name with flag
agent run --name myapp my-agent ./project
```

### Accessing Services

Services are available at:
- `http://web.myapp.localhost:8080`
- `http://api.myapp.localhost:8080`
- `http://myapp.localhost:8080` (default service)

### Environment Variables

Inside the container, these environment variables are set:
- `AGENTOPS_HOST=myapp.localhost:8080`
- `AGENTOPS_URL=http://myapp.localhost:8080`
- `AGENTOPS_HOST_WEB=web.myapp.localhost:8080`
- `AGENTOPS_URL_WEB=http://web.myapp.localhost:8080`

### Proxy Configuration

Global config in `~/.agentops/config.yaml`:
```yaml
proxy:
  port: 8080
```

Or via environment: `AGENTOPS_PROXY_PORT=9000`
```

**Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add hostname routing documentation"
```

---

## Task 20: Final Verification

**Step 1: Run all tests**

```bash
go test ./... -v
```

Expected: All tests PASS

**Step 2: Run linter**

```bash
golangci-lint run
```

Expected: No errors

**Step 3: Build**

```bash
go build ./...
```

Expected: Success

**Step 4: Manual smoke test**

```bash
# Create test agent.yaml
mkdir -p /tmp/test-agent
cat > /tmp/test-agent/agent.yaml << 'EOF'
name: testapp
ports:
  web: 3000
EOF

# Run agent (will need a command that listens on 3000)
agent run test-agent /tmp/test-agent -- python -m http.server 3000

# In another terminal, verify routing
curl -H "Host: web.testapp.localhost:8080" http://127.0.0.1:8080/
```

**Step 5: Final commit if any fixes needed**

```bash
git status
# If changes needed:
git add .
git commit -m "fix: address issues found in final verification"
```
