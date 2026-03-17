# Ollama Sidecar Service

## Summary

Add Ollama as a service dependency, following the established postgres/mysql/redis pattern. An agent declares `ollama` in `dependencies:` and lists models under `services.ollama.models`. Moat starts an Ollama sidecar, pulls declared models, caches them on the host, and injects `MOAT_OLLAMA_*` environment variables into the agent container.

## Motivation

AI agents running inside moat containers may need access to local model inference — for embeddings, sub-tasks, code review, or other LLM calls — without requiring external API keys or network access to hosted inference services.

## Design

### User-facing config

```yaml
# moat.yaml
name: my-agent

dependencies:
  - ollama@0.9

services:
  ollama:
    models:
      - qwen2.5-coder:7b
      - nomic-embed-text
```

The agent receives:

```bash
MOAT_OLLAMA_HOST=ollama
MOAT_OLLAMA_PORT=11434
MOAT_OLLAMA_URL=http://ollama:11434
```

No password, user, or database — Ollama has no auth.

### Registry entry

```yaml
# registry.yaml
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

### Generalized service extensions

Rather than adding Ollama-specific fields to `ServiceDef`, this design introduces two general-purpose concepts that any future service can use:

**Provisions** — A pattern for "pull/load N things at startup":
- `provisions_key`: Names the key in user's `services.<name>` config (e.g., `models`). The config layer maps this to an explicit `Provisions []string` field on `ServiceSpec` during parsing.
- `provision_cmd`: Command template with `{item}` placeholder, executed once per item inside the sidecar

**Cache** — Host-side persistence for service data:
- `cache_path`: Container path to mount. Host side is `~/.moat/cache/<service-name>/`

These fields are optional. Existing services (postgres, mysql, redis) don't use them and are unaffected.

Note: this is a simple "pull N items" pattern. Services requiring more complex provisioning (ordered steps, dependencies between items, conditional logic) should use a different mechanism rather than extending these fields.

### Struct changes

**`ServiceDef`** (`internal/deps/types.go`):

```go
type ServiceDef struct {
    // ... existing fields ...
    CachePath     string `yaml:"cache_path,omitempty"`
    ProvisionsKey string `yaml:"provisions_key,omitempty"`
    ProvisionCmd  string `yaml:"provision_cmd,omitempty"`
}
```

**`ServiceConfig`** (`internal/container/runtime.go`):

```go
type ServiceConfig struct {
    // ... existing fields ...
    CachePath     string   // container-side path (e.g., /root/.ollama)
    CacheHostPath string   // host-side path (e.g., ~/.moat/cache/ollama/)
    Provisions    []string
    ProvisionCmd  string
}
```

`CacheHostPath` is resolved in `buildServiceConfig` (run layer), which has access to the moat home directory. Both paths are passed through so `buildSidecarConfig` can construct the bind mount.

**`ServiceSpec`** (`internal/config/config.go`):

```go
type ServiceSpec struct {
    Env        map[string]string `yaml:"env,omitempty"`
    Wait       *bool             `yaml:"wait,omitempty"`
    Provisions []string          `yaml:"provisions,omitempty"`
}
```

`Provisions` is an explicit, typed field. The `config` package parses `ServiceSpec` with deferred handling for unknown keys (using `yaml.Node` or a raw map). The mapping from user-facing key (e.g., `models`) to `Provisions` happens in `buildServiceConfig` in the run layer (`internal/run/services.go`), which has access to both the registry's `provisions_key` and the parsed user spec. This avoids a package dependency from `config` → `deps`. Unknown keys that don't match the registry's `provisions_key` produce a validation error at this stage, catching typos like `model:` instead of `models:`.

**`ServiceManager`** (`internal/container/runtime.go`) — add provisioning method:

```go
type ServiceManager interface {
    StartService(ctx context.Context, cfg ServiceConfig) (ServiceInfo, error)
    CheckReady(ctx context.Context, info ServiceInfo) error
    StopService(ctx context.Context, info ServiceInfo) error
    SetNetworkID(id string)
    ProvisionService(ctx context.Context, info ServiceInfo, cmds []string, stdout io.Writer) error
}
```

`ProvisionService` executes commands sequentially inside the service container. This keeps provisioning within the `ServiceManager` abstraction rather than reaching through to `Runtime.Exec` from the run layer. Both `dockerServiceManager` and `appleServiceManager` implement it using their respective exec mechanisms.

### Password generation guard

The existing `buildServiceConfig` unconditionally generates a password. For services with no auth (like Ollama, where `password_env` is empty), this produces a phantom password env var. Fix: only generate a password when `spec.Service.PasswordEnv` is non-empty. This prevents spurious `MOAT_OLLAMA_PASSWORD` injection.

### Cache volume mount

- **Host path:** `~/.moat/cache/<service-name>/` (e.g., `~/.moat/cache/ollama/`)
- **Container path:** Value of `cache_path` (e.g., `/root/.ollama`)
- **Created:** `os.MkdirAll` before starting the sidecar
- **Wired in:**
  - **Docker:** `buildSidecarConfig` adds a `MountConfig` to `SidecarConfig.Mounts` (already supported)
  - **Apple:** `buildAppleRunArgs` needs new mount plumbing. The Apple container CLI supports `--mount type=bind,source=<path>,target=<path>` but `buildAppleRunArgs` currently has no mount logic. This requires adding mount arg construction, similar to how the main Apple runtime handles mounts in `apple.go`.
- **Permissions:** The `ollama/ollama` image runs as root. The host cache directory is created with default permissions. On Docker this works (root in container = root or remapped UID on host). On Apple containers, ownership semantics should be tested during implementation.
- **Concurrency:** Ollama handles concurrent reads to its model cache. For concurrent pulls from parallel runs, use `flock`-based advisory locking on `~/.moat/cache/<service-name>/.lock` during the provision phase. The lock is held only during provisioning, not for the run lifetime. This is low effort and prevents potential cache corruption.
- **Assumption:** `cache_path` assumes the default Ollama model storage location (`/root/.ollama`). Custom Ollama images with different home directories would need to override this via the `services.ollama.env` mechanism (e.g., `OLLAMA_MODELS=/custom/path`).

### Provisioning and readiness

Two-phase readiness for services with provisions:

1. **Phase 1 — Service healthy:** Poll `readiness_cmd` (`ollama list`) until success. Same `waitForServiceReady` loop (1s interval, 30s timeout).

2. **Phase 2 — Provision items:** For each item in `Provisions`, call `ServiceManager.ProvisionService` which execs `provision_cmd` with `{item}` replaced inside the sidecar. Sequential execution to avoid overwhelming CPU/disk.

**Failure semantics:** Fail-fast — abort the run on the first provision failure. Error message includes the failed command, its output, and a hint to check `moat logs <id>` for service container logs.

**Timeouts:**
- Phase 1: Existing 30s readiness timeout
- Phase 2: Separate 30-minute provision timeout for the entire phase (model pulls can be GBs). This is a known limitation — if total pull time exceeds 30 minutes, the run fails. Users can work around this by pre-warming the cache with a smaller run first. The timeout is not user-configurable in this iteration.

**Implementation:** New `provisionService` function in `internal/run/services.go`, called after `waitForServiceReady`. Builds command list from `ServiceConfig.Provisions` and `ServiceConfig.ProvisionCmd`, then calls `ServiceManager.ProvisionService`. Hooks into `manager.go` in the service readiness loop, right after `waitForServiceReady` returns and before env injection.

**Cache hits:** If a model is already cached from a previous run, `ollama pull` returns near-instantly ("model already exists"). The provision command always runs; caching makes it fast.

**User feedback:** Provision exec stdout/stderr streams raw to the user's stderr (not through `ui.Info` which adds prefixes). This is appropriate for long-running download progress bars. It is the first time the service startup path produces streaming user-visible output — existing service startups are silent.

**`wait: false` interaction:** When a user sets `wait: false` on a service, both readiness polling and provisioning are skipped. This is consistent — `wait: false` means "do not block on this service." The agent must handle model availability itself.

**Command execution:** Provision commands are executed via `sh -c <cmd>` inside the service container, matching the existing readiness check pattern in `CheckReady`.

### Lifecycle

Follows existing service lifecycle exactly:

1. Parse dependencies, identify Ollama as a service
2. Create Docker network
3. Pull `ollama/ollama:0.9` image
4. Start Ollama sidecar with cache volume mounted
5. Phase 1: Wait for Ollama HTTP server (readiness check)
6. Phase 2: Pull each declared model (provision step)
7. Inject `MOAT_OLLAMA_*` environment variables
8. Start agent container

Shutdown: same as other services — force-remove sidecar. Cache persists on host.

### Constraints

- **CPU only.** No GPU passthrough in this implementation. GPU support (Docker `--gpus`, Apple Metal) is a clean follow-up — the config shape doesn't need to change.
- **Ephemeral sidecar, persistent cache.** The Ollama process is destroyed with the run. The model cache survives across runs.

### Future work

- **GPU passthrough** — Docker `--gpus all` (NVIDIA Container Toolkit), Apple Metal (automatic on Apple containers)
- **Cache cleanup** — `moat clean --cache` to reclaim disk from `~/.moat/cache/`. Model caches can grow to tens of GB without a cleanup mechanism.
- **Provision timeout configuration** — User-configurable timeout in `moat.yaml` for services with large provisioning steps.
- **Configurable cache path** — Allow overriding `cache_path` via `services.ollama.env` with `OLLAMA_MODELS` for custom Ollama images.

## Files changed

| File | Change |
|------|--------|
| `internal/deps/registry.yaml` | Add `ollama` service entry with `cache_path`, `provisions_key`, `provision_cmd` |
| `internal/deps/types.go` | Add `CachePath`, `ProvisionsKey`, `ProvisionCmd` to `ServiceDef` |
| `internal/container/runtime.go` | Add `CachePath`, `Provisions`, `ProvisionCmd` to `ServiceConfig`; add `ProvisionService` to `ServiceManager` interface |
| `internal/config/config.go` | Add `Provisions` field to `ServiceSpec`, deferred unknown-key handling in `UnmarshalYAML` |
| `internal/run/services.go` | Add `provisionService` function, populate new `ServiceConfig` fields in `buildServiceConfig`, wire cache path, guard password generation |
| `internal/container/docker_service.go` | Implement `ProvisionService`, mount cache volume in `buildSidecarConfig` |
| `internal/container/apple_service.go` | Implement `ProvisionService`, add mount support to `buildAppleRunArgs` |
| `internal/container/service_helpers.go` | No changes expected |
| `examples/service-ollama/moat.yaml` | New example |
| `examples/service-ollama/demo.sh` | New example |
| `docs/content/guides/08-services.md` | Add Ollama section |
| `docs/content/reference/06-dependencies.md` | Add Ollama to registry table |
| `docs/content/reference/02-moat-yaml.md` | Document `services.<name>.<provisions_key>` pattern |

## Testing

- **Unit tests:** `buildServiceConfig` with Ollama spec, `ServiceSpec` custom unmarshaling (valid key, typo key, missing key), `provisionService` with mock `ServiceManager`, password generation guard for no-auth services
- **Integration test:** `internal/e2e/services_test.go` — start Ollama sidecar, verify env vars injected, verify model accessible via API
- **Manual test:** `moat run examples/service-ollama` — should print model list and generate a response
