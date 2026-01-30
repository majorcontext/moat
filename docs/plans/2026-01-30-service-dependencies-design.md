# Service Dependencies Design

**Date:** 2026-01-30
**Status:** Approved
**Revised:** 2026-01-30 — Incorporated review feedback (readiness checks, validation, composition, naming, multi-port, data-driven registry)

## Overview

Add runtime-agnostic service dependencies (postgres, mysql, redis) to Moat. Services are ephemeral databases that run alongside the main container, providing zero-config database access for agents.

## Problem

Agents often need databases for testing, development, or data processing. Setting up databases inside the main container:
- Bloats container images
- Slows startup time
- Requires manual configuration
- Complicates cleanup

Users need ephemeral databases that "just work" with zero configuration.

## Solution

Extend Moat's dependency system to support service dependencies. Services like `postgres@17` are declared in `dependencies:` and automatically provisioned by the runtime (Docker sidecars, VM-local installs, etc.) with:
- Zero configuration required
- Auto-generated credentials
- Standardized environment variables
- Automatic cleanup

```yaml
dependencies:
  - node@22
  - postgres@17
  - redis@7
```

Main container automatically gets:
```bash
MOAT_POSTGRES_URL=postgresql://postgres:randompass@postgres:5432/postgres
MOAT_REDIS_URL=redis://:randompass@redis:6379
```

## Design Principles

- **Services as dependencies, not sidecars** - Implementation detail hidden from users
- **Zero config by default** - Aggressive defaults, explicit overrides only when needed
- **Runtime-agnostic API** - Docker gets sidecars, Apple containers return NOT IMPLEMENTED
- **Environment variables as portability layer** - All runtimes inject same env vars
- **Security by default** - All services require authentication, even when network-isolated
- **Agent ergonomics first** - Shaped for agents, not container plumbing

## Architecture

### 1. Registry Integration

Services extend the existing dependency registry pattern in `internal/deps/registry.yaml`. Each service entry includes metadata that drives image resolution, port mapping, env var generation, and readiness checks — avoiding hardcoded per-service switch statements in Go code.

```yaml
postgres:
  description: PostgreSQL database
  type: service
  default: "17"
  versions: ["15", "16", "17"]
  service:
    image: "postgres"                    # Docker image name (version appended as tag)
    ports:
      default: 5432
    env_prefix: POSTGRES                 # → MOAT_POSTGRES_*
    default_user: postgres
    default_db: postgres
    password_env: POSTGRES_PASSWORD      # Env var to set on the service container
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
    extra_env:                           # Additional env vars for the service container
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
    password_env: ""                     # Redis uses --requirepass flag instead
    extra_cmd: ["--requirepass", "{password}"]
    readiness_cmd: "redis-cli -a {password} PING"
    url_scheme: "redis"
    url_format: "{scheme}://:{password}@{host}:{port}"
```

This data-driven approach means adding a new service (e.g. Elasticsearch, RabbitMQ) requires only a registry entry — no Go code changes for the common case. Services with unusual requirements can override behavior via the `services:` config field.

**Multi-port services** use named ports in the registry:
```yaml
elasticsearch:
  description: Elasticsearch search engine
  type: service
  default: "8"
  versions: ["7", "8"]
  service:
    image: "elasticsearch"
    ports:
      http: 9200
      transport: 9300
    env_prefix: ELASTICSEARCH
    # ...
```

This produces `MOAT_ELASTICSEARCH_HTTP_PORT=9200` and `MOAT_ELASTICSEARCH_TRANSPORT_PORT=9300`. Single-port services use the key `default`, which omits the port name from env vars (i.e. `MOAT_POSTGRES_PORT=5432`, not `MOAT_POSTGRES_DEFAULT_PORT`).

**Flow:**
1. `dependencies: ["postgres@17"]` parsed by existing `deps.Parse()`
2. Registry lookup finds `type: service`
3. Service-specific logic triggered in `internal/run/manager.go`
4. Runtime-specific implementation (Docker sidecar vs Apple NOT IMPLEMENTED)

**Benefits:**
- Reuses existing dependency resolution
- Services are first-class dependencies, no special syntax
- Version management works identically to runtimes
- Registry is single source of truth

**Filtering service dependencies:**

Since service dependencies are fundamentally different from installable dependencies (they are separate processes, not packages installed into the main container), all downstream consumers of parsed dependencies must distinguish between them. Add helpers to `internal/deps/`:

```go
// FilterServices returns only service-type dependencies.
func FilterServices(deps []Dependency) []Dependency

// FilterInstallable returns dependencies excluding services.
func FilterInstallable(deps []Dependency) []Dependency
```

These must be used in image selection, Dockerfile generation, and install script paths to prevent service dependencies from leaking into container build logic.

**Validation of `services:` ↔ `dependencies:` coupling:**

If `config.Services` references a service name not present in parsed `dependencies:`, fail with:
```
Error: services.postgres configured but postgres not declared in dependencies

Add to dependencies:
  dependencies:
    - postgres@17
```

### 2. Runtime Abstraction

Add `ServiceManager` interface to `internal/container/runtime.go`:

```go
// ServiceManager provisions services (databases, caches, etc).
// Returned by Runtime.ServiceManager() - nil if not supported.
// How the service is provisioned is a runtime implementation detail:
// Docker uses sidecar containers, a VM runtime might install locally.
type ServiceManager interface {
    // StartService provisions a service and returns connection info.
    StartService(ctx context.Context, cfg ServiceConfig) (ServiceInfo, error)

    // CheckReady returns nil when the service is accepting connections.
    // Implementations use runtime-appropriate mechanisms (e.g. docker exec,
    // local CLI tools). Callers retry with backoff.
    CheckReady(ctx context.Context, info ServiceInfo) error

    // StopService tears down a previously started service.
    StopService(ctx context.Context, info ServiceInfo) error
}

// ServiceConfig defines what service to provision.
// Runtime-agnostic: no container-specific fields (mounts, privileged, etc).
type ServiceConfig struct {
    Name       string            // Service name: "postgres", "mysql", "redis"
    Version    string            // Version: "17", "8", "7"
    Env        map[string]string // Service-specific env vars (after secret resolution)
    RunID      string            // For orphan cleanup / labeling
}

// ServiceInfo contains connection details for a started service.
type ServiceInfo struct {
    ID         string            // Opaque identifier (container ID, PID, etc)
    Name       string            // Service name (for readiness command lookup)
    Host       string            // Hostname or IP to reach the service
    Ports      map[string]int    // Named ports: {"default": 5432} or {"http": 9200, "transport": 9300}
    Env        map[string]string // Generated credentials and connection info
}
```

Note: `ServiceConfig` intentionally omits `Image`, `Hostname`, and `NetworkID`. These are Docker-specific concerns resolved inside the Docker implementation, not passed through the runtime-agnostic interface.

**Docker implementation — composition with `SidecarManager`:**

`dockerServiceManager` composes the existing `SidecarManager` and `NetworkManager`:

```go
type dockerServiceManager struct {
    sidecar SidecarManager
    network NetworkManager
}

func (m *dockerServiceManager) StartService(ctx context.Context, cfg ServiceConfig) (ServiceInfo, error) {
    // Look up ServiceDef from registry by cfg.Name
    // Resolve image: serviceDef.Image + ":" + cfg.Version
    // Build SidecarConfig with image, hostname, network, env, labels, extra_cmd
    // Delegate to m.sidecar.StartSidecar(ctx, sidecarCfg)
    // Return ServiceInfo with container hostname, named ports, and name
}

func (m *dockerServiceManager) CheckReady(ctx context.Context, info ServiceInfo) error {
    // Look up readiness_cmd from registry by info.Name
    // Substitute {password} and other placeholders
    // docker exec into the service container with the resolved command
}

func (m *dockerServiceManager) StopService(ctx context.Context, info ServiceInfo) error {
    // Stop and remove the sidecar container by info.ID
}
```

This keeps the existing `SidecarManager` unchanged — it remains a generic "run a container alongside" primitive. The `dockerServiceManager` adds the service-specific semantics (image resolution, env mapping, readiness commands) on top.

**Apple implementation:**
- `ServiceManager()` returns `nil` (already the pattern for unsupported capabilities)
- Run manager checks for nil and produces a clear error message

**Future VM implementation:**
- Would implement `ServiceManager` by installing services locally (e.g. `apt-get install postgresql`)
- `CheckReady` would run `pg_isready` directly on the host
- Returns `localhost` as `ServiceInfo.Host`

**Runtime isolation:**
- All service-specific logic in runtime packages
- `internal/run/manager.go` calls `ServiceManager` interface, no runtime-specific code
- `SidecarConfig` details (mounts, privileged, cmd) stay inside Docker implementation

### 3. Configuration and Defaults

Add `services:` field to `internal/config/config.go`:

```go
type Config struct {
    // ... existing fields ...
    Services map[string]ServiceSpec `yaml:"services,omitempty"`
}

// ServiceSpec allows customizing service behavior.
type ServiceSpec struct {
    // Env supports inline values and secret references (op://, ssm://, etc)
    // Resolved using same infrastructure as Config.Secrets
    Env     map[string]string `yaml:"env,omitempty"`

    // Image overrides default image (Docker runtime only, ignored by others)
    Image   string            `yaml:"image,omitempty"`

    // Wait controls whether to block main container until service ready
    // Default: true (safe, blocks until usable)
    Wait    *bool             `yaml:"wait,omitempty"`
}
```

**Zero-config example:**
```yaml
dependencies:
  - postgres@17
```

Automatic behavior:
- Ephemeral `postgres:17` container starts
- Random 32-character alphanumeric password generated
- `MOAT_POSTGRES_*` env vars injected into main container
- Readiness check blocks main container start

**Override example:**
```yaml
dependencies:
  - postgres@17

services:
  postgres:
    env:
      POSTGRES_PASSWORD: op://vault/postgres/password
      POSTGRES_DB: myapp
    wait: false  # Start in background, don't block
```

**Secret resolution:**
- Service env values resolved during service startup
- Same provider infrastructure as `Config.Secrets` (1Password, SSM, inline)
- Resolved values passed to service container, never exposed in logs

### 4. Environment Variable Injection

Service connection info injected into main container via standardized env vars.

**Postgres (`postgres@17`):**
```bash
MOAT_POSTGRES_URL=postgresql://postgres:{password}@postgres:5432/postgres
MOAT_POSTGRES_HOST=postgres
MOAT_POSTGRES_PORT=5432
MOAT_POSTGRES_USER=postgres
MOAT_POSTGRES_PASSWORD={password}
MOAT_POSTGRES_DB=postgres
```

**MySQL (`mysql@8`):**
```bash
MOAT_MYSQL_URL=mysql://root:{password}@mysql:3306/moat
MOAT_MYSQL_HOST=mysql
MOAT_MYSQL_PORT=3306
MOAT_MYSQL_USER=root
MOAT_MYSQL_PASSWORD={password}
MOAT_MYSQL_DB=moat
```

**Redis (`redis@7`):**
```bash
MOAT_REDIS_URL=redis://:{password}@redis:6379
MOAT_REDIS_HOST=redis
MOAT_REDIS_PORT=6379
MOAT_REDIS_PASSWORD={password}
```

**Implementation:**
- Env generation driven by registry metadata (`env_prefix`, `url_format`, `ports`)
- `generateServiceEnv(registryEntry, info, userConfig)` in `internal/run/services.go`
- For single-port services (port named `default`): `MOAT_{PREFIX}_PORT=N`
- For multi-port services: `MOAT_{PREFIX}_{PORTNAME}_PORT=N` for each named port
- URL built from `url_format` template with `{scheme}`, `{user}`, `{password}`, `{host}`, `{port}`, `{db}` placeholders
- Called from `manager.go` after service starts, before main container starts
- Appended to main container's env array

**User overrides respected:**
- If `services.postgres.env.POSTGRES_DB=myapp`, then `MOAT_POSTGRES_DB=myapp`
- URL automatically reflects override: `postgresql://...@postgres:5432/myapp`

**Portability:**
- Same env vars across all runtimes (Docker, future Apple/microVM)
- Runtime differences (hostname, networking) hidden below abstraction
- Env var names and URL formats driven by registry, not hardcoded per-service

### 5. Readiness Checks

Block main container start until services are usable, with opt-out for advanced cases.

**Default behavior (`wait: true`):**

Each service has a specific readiness check verifying authentication and command execution:

**Postgres:**
```bash
# Verify port + auth + query execution
PGPASSWORD={password} psql -h postgres -U postgres -d postgres -c 'SELECT 1'
```

**MySQL:**
```bash
# Verify MySQL accepts connections and database exists
mysqladmin ping -h mysql -u root -p{password}
mysql -h mysql -u root -p{password} -e 'USE moat'
```

**Redis:**
```bash
# Verify Redis accepts auth and responds to commands
redis-cli -h redis -a {password} PING
```

**Implementation details:**
- Readiness checks are runtime-specific, invoked via `ServiceManager.CheckReady()`
- Docker: uses `docker exec` into the service container (no temporary containers needed — service images already include their own CLI tools)
- VM: runs check commands directly on the host
- Timeout: 30 seconds per service (MySQL startup is slow)
- Retry interval: 1 second
- Checks run in parallel for multiple services
- Location: `internal/run/services.go` orchestrates retry loop, delegates to `ServiceManager.CheckReady()`

**Opt-out (`wait: false`):**
```yaml
services:
  postgres:
    wait: false  # Start in background, don't block
```

- Service container starts, verify it's running, return immediately
- Main container starts in parallel while service initializes
- Agents need retry logic for early database access
- Useful when agent does setup work before needing database

**Benefits:**
- Default: Safe, agents never see connection failures on first access
- Opt-out: Advanced users can parallelize startup for performance

### 6. Lifecycle Management

Service containers tied to main container lifecycle with proper cleanup.

**Startup sequence:**
1. Parse dependencies, identify services via registry lookup (`type: service`)
2. Create Docker network if services present and Docker runtime: `moat-<run-id>`
3. Start service containers in parallel
4. Run readiness checks in parallel (if `wait: true`)
5. Generate and inject `MOAT_*` env vars for each service
6. Start main container on same network

**Shutdown sequence:**
1. Stop main container
2. Stop all service containers in parallel
3. Remove network (best-effort)

**Container naming (Docker):**
- BuildKit: `moat-buildkit-<run-id>` (existing)
- Postgres: `moat-postgres-<run-id>`
- MySQL: `moat-mysql-<run-id>`
- Redis: `moat-redis-<run-id>`
- Network: `moat-<run-id>` (shared with BuildKit)

All service containers labeled with `moat.run-id=<run-id>` and `moat.role=service` for consistent orphan cleanup.

**Run metadata:**
```go
type RunMetadata struct {
    // ... existing fields ...
    ServiceContainers map[string]string `json:"service_containers,omitempty"` // service -> container ID
    NetworkID         string             `json:"network_id,omitempty"` // Already exists from BuildKit
}
```

**Crash recovery:**
- Service container IDs stored in `~/.moat/runs/<id>/metadata.json`
- On `moat clean` or restart, detect orphaned containers by name prefix
- Cleanup removes service containers and networks

**Network sharing:**
- BuildKit and services share the same network: `moat-<run-id>`
- If both BuildKit and services present, network created once
- Main container attached to network for access to both

### 7. Error Handling

Clear, actionable error messages for common failure modes.

**Service not supported by runtime:**
```
Error: postgres@17 requires Docker runtime
Apple containers don't support service dependencies

Either:
  - Use Docker runtime (moat will auto-select if Docker is available)
  - Install and run postgres on your host, set MOAT_POSTGRES_URL manually
```

**Image pull failure:**
```
Error: Failed to pull postgres:17 image
Ensure Docker can access Docker Hub

Debug steps:
  docker pull postgres:17
```

**Readiness timeout:**
```
Error: postgres service failed to become ready within 30 seconds

Service container logs:
  moat logs <run-id> --service postgres

Or disable wait to start service in background:
  services:
    postgres:
      wait: false
```

**Invalid service configuration:**
```
Error: services.postgres.env.POSTGRES_PASSWORD uses invalid secret reference

Got: ssm:/prod/db/pass
Expected: ssm:///prod/db/pass (note: three slashes for absolute path)
```

**Network creation failure:**
```
Error: Failed to create Docker network for services

This usually means:
  - Docker daemon is not running
  - Insufficient permissions

Try: docker network create test-network
```

**Multiple services, partial failure:**
```
Error: Failed to start 1 of 3 services

✓ postgres - ready
✓ redis - ready
✗ mysql - failed to pull image mysql:8

Already-started services have been cleaned up.
```

## Implementation Components

### 1. Registry (`internal/deps/registry.yaml`)

Add service entries with full service metadata (image, ports, env_prefix, readiness_cmd, url_format, etc.):
- `postgres` with versions 15, 16, 17 (default: 17)
- `mysql` with versions 8, 9 (default: 8)
- `redis` with versions 6, 7 (default: 7)

### 2. Types (`internal/deps/types.go`)

Add `TypeService` constant to `InstallType` enum.

Add `ServiceDef` struct to `DepSpec` for service metadata:
```go
type ServiceDef struct {
    Image        string            `yaml:"image"`
    Ports        map[string]int    `yaml:"ports"`         // "default": 5432 or "http": 9200
    EnvPrefix    string            `yaml:"env_prefix"`    // POSTGRES → MOAT_POSTGRES_*
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

This makes adding new services a registry-only change for the common case.

### 3. Dependency Helpers (`internal/deps/filter.go` - new file)

Add `FilterServices()` and `FilterInstallable()` helpers. Used by image selection, Dockerfile generation, and install scripts to exclude service dependencies.

### 4. Runtime Interface (`internal/container/runtime.go`)

Add `ServiceManager` interface with `StartService`, `CheckReady`, `StopService` methods, plus `ServiceConfig` and `ServiceInfo` types. Add `ServiceManager()` accessor to `Runtime` interface.

### 5. Docker Runtime (`internal/container/docker.go`)

Implement `dockerServiceManager` composing the existing `SidecarManager`:
- `StartService()` - Resolve image, build `SidecarConfig`, delegate to `SidecarManager.StartSidecar()`
- `CheckReady()` - `docker exec` with service-specific commands (`pg_isready`, `mysqladmin ping`, `redis-cli PING`)
- `StopService()` - Stop and remove sidecar container

### 6. Apple Runtime (`internal/container/apple.go`)

Return `nil` from `ServiceManager()` method (already the pattern for unsupported capabilities).

### 7. Service Logic (`internal/run/services.go` - new file)

Runtime-agnostic service orchestration:
- `generateServiceEnv()` - Create `MOAT_*` env vars from `ServiceInfo` and registry `ServiceDef` (env_prefix, url_format, port names)
- `waitForServiceReady()` - Retry loop calling `ServiceManager.CheckReady()`
- `generatePassword()` - Crypto-random password generation
- `validateServiceConfig()` - Verify `services:` keys match declared `dependencies:`
- No hardcoded per-service defaults — all driven by registry `ServiceDef` metadata

### 8. Run Manager (`internal/run/manager.go`)

Integrate service startup into `Create()`:
- Use `deps.FilterServices()` to identify service dependencies
- Use `deps.FilterInstallable()` for image selection and install logic
- Create network if services or BuildKit present
- Start services via `ServiceManager.StartService()`
- Run readiness checks via retry loop on `ServiceManager.CheckReady()` (if `wait: true`)
- Generate and append `MOAT_*` service env vars
- Attach main container to network

Integrate service cleanup into `Stop()` and `Destroy()`:
- Iterate `Run.ServiceContainers` and call `ServiceManager.StopService()` for each
- Remove network (shared with BuildKit cleanup)

### 9. Config (`internal/config/config.go`)

Add `Services` field and `ServiceSpec` type. Validation ensures `services:` keys correspond to declared service dependencies.

### 10. Storage (`internal/storage/storage.go`)

Add `ServiceContainers map[string]string` field to `Metadata` (already has `NetworkID`).

## Compatibility

### Runtime Support
- **Docker:** Full support (service sidecars on shared network)
- **Apple containers:** NOT IMPLEMENTED (clear error message)
- **Future microVMs:** Design accommodates future runtime implementations

### Backward Compatibility
- New feature, no migration needed
- Existing runs without services continue working unchanged
- Network creation logic shared with BuildKit (compatible)

## Security

### Credential Generation
- 32-character alphanumeric passwords via `crypto/rand`
- Unique per run, never reused
- Stored transiently in run metadata, cleared on cleanup

### Network Isolation
- Services isolated on per-run Docker network
- No host network exposure by default
- Main container is only network participant (plus BuildKit if present)

### Authentication
- All services require authentication (postgres, mysql, redis)
- No password-less services, even when network-isolated
- Teaches agents secure patterns

### Secret References
- `services.*.env` supports same secret providers as main container
- Secrets resolved before container start, never logged
- Follows existing secret resolution patterns

## Testing Strategy

### Unit Tests
- `internal/deps/registry_test.go` - Service registry entries parsed correctly
- `internal/run/services_test.go` - Env generation, password generation
- `internal/container/docker_test.go` - ServiceManager methods

### E2E Tests (Docker only)
- `internal/e2e/services_test.go` - Full service lifecycle
  - Postgres: Start, wait for ready, verify env vars, connect and query
  - MySQL: Start, wait for ready, verify env vars, connect and query
  - Redis: Start, wait for ready, verify env vars, connect and execute command
  - Multiple services: postgres + redis together
  - Custom config: Override password, database name
  - No-wait mode: Service starts in background
  - Cleanup: Services removed on stop

### Manual Testing
- Service startup performance (parallel vs sequential)
- Readiness timeout scenarios
- Error messages for all failure modes
- Integration with BuildKit (network sharing)

## Open Questions

None - design approved.

## Future Enhancements

### Persistent Storage

Volume mounts for service data (`volumes:` field on `ServiceSpec`). Requires volume lifecycle management and cleanup semantics.

### Additional Services
- MongoDB
- Memcached
- Elasticsearch
- RabbitMQ

### Custom Services
```yaml
services:
  custom-api:
    image: my-api:latest
    ports:
      - 8080
```

Allow user-defined service containers beyond databases.

### Apple Container Support
Investigate Apple's auxiliary container capabilities and implement service support.

### Health Checks
Beyond readiness, periodic health checks during run lifecycle.

## References

- Existing BuildKit sidecar: `docs/plans/2026-01-27-buildkit-sidecar-design.md`
- Dependency system: `internal/deps/`
- Runtime abstraction: `internal/container/runtime.go`
- Sidecar manager: `internal/container/docker.go` (`dockerSidecarManager`)

## Revision History

- **2026-01-30 (initial):** Original design approved
- **2026-01-30 (rev 1):** Post-review revisions:
  - `ServiceManager` justified as separate from `SidecarManager` — runtime-agnostic (VM could install locally)
  - Docker `ServiceManager` composes `SidecarManager` rather than duplicating container logic
  - Added `CheckReady` and `StopService` to `ServiceManager` interface
  - Removed `Image`/`Hostname`/`NetworkID` from `ServiceConfig` (Docker-specific, resolved internally)
  - Readiness checks use `docker exec` / native CLI instead of temporary containers
  - Removed speculative `Volumes` field from `ServiceSpec`
  - Added `deps.FilterServices()` / `FilterInstallable()` to prevent services leaking into image/install logic
  - Added validation that `services:` keys must match declared `dependencies:`
  - Standardized container naming: `moat-<service>-<run-id>` with `moat.role=service` labels
- **2026-01-30 (rev 2):** Edge case analysis for future services:
  - `ServiceInfo.Port` → `ServiceInfo.Ports map[string]int` for multi-port services (e.g. Elasticsearch 9200+9300)
  - Added `ServiceInfo.Name` for readiness command lookup
  - Registry entries now include full `service:` metadata block (image, ports, env_prefix, readiness_cmd, url_format, etc.)
  - Service behavior is data-driven from registry — adding a new service requires only a YAML entry, no Go code for the common case
  - Added `ServiceDef` struct to `DepSpec` for typed access to service metadata
