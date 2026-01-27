# BuildKit Client Integration Plan

**Date:** 2026-01-27
**Status:** Approved
**Context:** Fix image building to use BuildKit Go client instead of Docker SDK when BuildKit sidecar is active

## Problem

The current BuildKit sidecar implementation (from 2026-01-27-buildkit-sidecar.md) sets up a BuildKit sidecar container but doesn't actually use it. The code still uses Docker SDK's `ImageBuild()` which:

1. **Doesn't work with remote BuildKit over TCP** - Results in "no active sessions" errors
2. **Can't leverage BuildKit features** - Cache mounts, multi-platform builds, etc. don't work
3. **Breaks under concurrency** - Docker SDK doesn't properly manage BuildKit sessions

**Current error:**
```
build error: python:3.11.11-slim: failed to resolve source metadata for
docker.io/library/python:3.11.11-slim: no active sessions
```

## Solution

Replace Docker SDK image building with BuildKit Go client (`github.com/moby/buildkit/client`) when `BUILDKIT_HOST` environment variable is set.

### Architecture Decision

**Use BuildKit Go client library** instead of `buildctl` exec:

| Aspect | BuildKit Go Client | buildctl exec |
|--------|-------------------|---------------|
| Type safety | ✅ Compile-time checks | ❌ Runtime string parsing |
| Performance | ✅ Direct API calls | ❌ Fork/exec overhead |
| Testability | ✅ Mock interfaces | ❌ Hard to test |
| Features | ✅ Full API access | ⚠️ Limited to CLI |
| Debugging | ✅ Stack traces | ❌ Parse stdout/stderr |
| Dependencies | ✅ One Go module | ❌ Must bundle binary |

## Implementation Plan

### Phase 1: Add BuildKit Client Dependency

**Goal:** Add BuildKit client library to go.mod

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

**Steps:**
1. Add dependency: `go get github.com/moby/buildkit/client`
2. Verify: `go mod tidy`
3. Test compilation: `go build ./...`

**Commit:** `feat(deps): add buildkit client library`

---

### Phase 2: Create BuildKit Client Wrapper

**Goal:** Abstract BuildKit client operations with proper error handling and logging

**Files:**
- Create: `internal/buildkit/client.go`
- Create: `internal/buildkit/client_test.go`

**Implementation:**

```go
// internal/buildkit/client.go
package buildkit

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/progress/progressui"
)

// Client wraps BuildKit client operations.
type Client struct {
	addr string
}

// NewClient creates a BuildKit client.
// Connects to the address specified in BUILDKIT_HOST env var (e.g., "tcp://buildkit:1234")
func NewClient() (*Client, error) {
	addr := os.Getenv("BUILDKIT_HOST")
	if addr == "" {
		return nil, fmt.Errorf("BUILDKIT_HOST not set")
	}
	return &Client{addr: addr}, nil
}

// BuildOptions configures a BuildKit build.
type BuildOptions struct {
	Dockerfile string            // Dockerfile content
	Tag        string            // Image tag (e.g., "moat/run:abc123")
	ContextDir string            // Build context directory
	NoCache    bool              // Disable build cache
	Platform   string            // Target platform (e.g., "linux/amd64")
	BuildArgs  map[string]string // Build arguments
	Output     io.Writer         // Progress output (default: os.Stdout)
}

// Build executes a build using BuildKit.
func (c *Client) Build(ctx context.Context, opts BuildOptions) error {
	// Connect to BuildKit
	bkClient, err := client.New(ctx, c.addr)
	if err != nil {
		return fmt.Errorf("connecting to buildkit at %s: %w", c.addr, err)
	}
	defer bkClient.Close()

	// Prepare solve options
	solveOpt := client.SolveOpt{
		Frontend: "dockerfile.v0",
		FrontendAttrs: map[string]string{
			"filename": "Dockerfile",
			"platform": opts.Platform,
		},
		LocalDirs: map[string]string{
			"context":    opts.ContextDir,
			"dockerfile": opts.ContextDir,
		},
	}

	// Add build args
	for k, v := range opts.BuildArgs {
		solveOpt.FrontendAttrs["build-arg:"+k] = v
	}

	// Add cache options if not disabled
	if !opts.NoCache {
		solveOpt.FrontendAttrs["no-cache"] = ""
	}

	// Set output (push to local Docker daemon via image exporter)
	solveOpt.Exports = []client.ExportEntry{
		{
			Type: "image",
			Attrs: map[string]string{
				"name": opts.Tag,
				"push": "false",
			},
		},
	}

	// Progress writer
	output := opts.Output
	if output == nil {
		output = os.Stdout
	}

	// Create progress channel
	ch := make(chan *client.SolveStatus)
	eg, ctx := errgroup.WithContext(ctx)

	// Display progress
	eg.Go(func() error {
		display, err := progressui.NewDisplay(output, progressui.AutoMode)
		if err != nil {
			return err
		}
		_, err = display.UpdateFrom(ctx, ch)
		return err
	})

	// Execute build
	eg.Go(func() error {
		_, err := bkClient.Solve(ctx, nil, solveOpt, ch)
		return err
	})

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("buildkit build failed: %w", err)
	}

	return nil
}

// Ping checks if BuildKit is reachable.
func (c *Client) Ping(ctx context.Context) error {
	bkClient, err := client.New(ctx, c.addr)
	if err != nil {
		return fmt.Errorf("connecting to buildkit: %w", err)
	}
	defer bkClient.Close()
	return nil
}
```

**Tests:**

```go
// internal/buildkit/client_test.go
package buildkit

import (
	"context"
	"os"
	"testing"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		envVal  string
		wantErr bool
	}{
		{
			name:    "with BUILDKIT_HOST set",
			envVal:  "tcp://buildkit:1234",
			wantErr: false,
		},
		{
			name:    "without BUILDKIT_HOST",
			envVal:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal != "" {
				os.Setenv("BUILDKIT_HOST", tt.envVal)
				defer os.Unsetenv("BUILDKIT_HOST")
			}

			client, err := NewClient()
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && client.addr != tt.envVal {
				t.Errorf("addr = %v, want %v", client.addr, tt.envVal)
			}
		})
	}
}

// Integration test - requires BuildKit running
func TestClient_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	buildkitHost := os.Getenv("BUILDKIT_HOST")
	if buildkitHost == "" {
		t.Skip("BUILDKIT_HOST not set")
	}

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		t.Errorf("Ping() failed: %v", err)
	}
}
```

**Commit:** `feat(buildkit): add client wrapper for image builds`

---

### Phase 3: Refactor BuildImage to Use BuildKit Client

**Goal:** Modify `DockerRuntime.BuildImage()` to use BuildKit client when available, fall back to Docker SDK otherwise

**Files:**
- Modify: `internal/container/docker.go`

**Implementation Strategy:**

```go
func (r *DockerRuntime) BuildImage(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error {
	// Check if BuildKit is available via BUILDKIT_HOST
	if buildkitHost := os.Getenv("BUILDKIT_HOST"); buildkitHost != "" {
		return r.buildImageWithBuildKit(ctx, dockerfile, tag, opts)
	}

	// Fall back to Docker SDK (existing implementation)
	return r.buildImageWithDockerSDK(ctx, dockerfile, tag, opts)
}

func (r *DockerRuntime) buildImageWithBuildKit(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error {
	// Create temporary build context directory
	tmpDir, err := os.MkdirTemp("", "moat-build-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write Dockerfile
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("writing Dockerfile: %w", err)
	}

	// Determine platform
	platform := "linux/amd64"
	if goruntime.GOARCH == "arm64" {
		platform = "linux/arm64"
	}

	// Create BuildKit client
	bkClient, err := buildkit.NewClient()
	if err != nil {
		return fmt.Errorf("creating buildkit client: %w", err)
	}

	fmt.Printf("Building image %s with BuildKit...\n", tag)

	// Build with BuildKit
	return bkClient.Build(ctx, buildkit.BuildOptions{
		Dockerfile: dockerfilePath,
		Tag:        tag,
		ContextDir: tmpDir,
		NoCache:    opts.NoCache,
		Platform:   platform,
	})
}

func (r *DockerRuntime) buildImageWithDockerSDK(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error {
	// Existing Docker SDK implementation (lines 382-451)
	// ... keep as-is for backward compatibility
}
```

**Testing:**
- Unit test for path selection (BuildKit vs Docker SDK)
- Integration test with BuildKit sidecar
- Regression test for Docker SDK path

**Commit:** `feat(container): use buildkit client for image builds when available`

---

### Phase 4: Handle Build Context Properly

**Goal:** Support copying build context files into temporary directory for BuildKit

**Files:**
- Modify: `internal/container/docker.go`

**Problem:** BuildKit needs actual files on disk, not just a Dockerfile string.

**Solution:** Create temporary build context with all necessary files.

**Implementation:**

```go
// prepareBuildContext creates a temporary directory with Dockerfile and context files.
func prepareBuildContext(dockerfile string, contextFiles map[string][]byte) (string, error) {
	tmpDir, err := os.MkdirTemp("", "moat-build-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}

	// Write Dockerfile
	if err := os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("writing Dockerfile: %w", err)
	}

	// Write additional context files (if any)
	for name, content := range contextFiles {
		path := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("creating directory for %s: %w", name, err)
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			os.RemoveAll(tmpDir)
			return "", fmt.Errorf("writing %s: %w", name, err)
		}
	}

	return tmpDir, nil
}
```

**Commit:** `feat(container): add build context preparation for buildkit`

---

### Phase 5: Add Progress Streaming

**Goal:** Stream BuildKit build progress to user (like Docker SDK does)

**Files:**
- Modify: `internal/buildkit/client.go`

**Implementation:** Use `progressui.NewDisplay()` for formatted output (already in Phase 2 implementation)

**Features:**
- Real-time progress updates
- Layer caching indicators
- Error highlighting
- Build timing

**Commit:** `feat(buildkit): add progress streaming for builds`

---

### Phase 6: Error Handling and Logging

**Goal:** Provide clear error messages and debug logging

**Files:**
- Modify: `internal/buildkit/client.go`
- Modify: `internal/container/docker.go`

**Error Scenarios:**
1. BuildKit not reachable → Clear message about sidecar status
2. Build fails → Include BuildKit error details
3. Network issues → Suggest checking docker:dind configuration
4. Invalid Dockerfile → Show BuildKit validation errors

**Debug Logging:**
- Log when switching between BuildKit and Docker SDK
- Log BuildKit connection address
- Log build options for debugging

**Commit:** `feat(buildkit): improve error messages and logging`

---

### Phase 7: Update Tests

**Goal:** Ensure all tests pass with BuildKit client

**Files:**
- Modify: `internal/e2e/e2e_test.go`
- Create: `internal/buildkit/integration_test.go`

**Test Coverage:**
1. **Unit tests:**
   - BuildKit client creation
   - Path selection (BuildKit vs Docker SDK)
   - Build context preparation

2. **Integration tests:**
   - Build with BuildKit sidecar
   - Verify images are created
   - Test cache behavior
   - Test error scenarios

3. **E2E tests:**
   - Full docker:dind flow with builds
   - Verify python/node/go runtime builds work

**Commit:** `test(buildkit): add integration tests for client`

---

### Phase 8: Documentation

**Goal:** Document BuildKit client usage and troubleshooting

**Files:**
- Modify: `docs/content/reference/02-agent-yaml.md`
- Modify: `docs/content/concepts/docker-access.md` (if exists)
- Create: `docs/content/guides/buildkit-troubleshooting.md` (optional)

**Documentation Updates:**

```markdown
### BuildKit Integration

When using `docker:dind`, Moat automatically uses BuildKit for image builds:

**How it works:**
1. BuildKit sidecar starts: `moby/buildkit:latest`
2. `BUILDKIT_HOST=tcp://buildkit:1234` environment variable set
3. Image builds use BuildKit Go client (not Docker SDK)
4. Full BuildKit features available: cache mounts, multi-platform, etc.

**Fallback Behavior:**
- If `BUILDKIT_HOST` not set → Uses Docker SDK
- If BuildKit unreachable → Clear error message with troubleshooting steps

**Troubleshooting:**
- **"no active sessions"**: Indicates Docker SDK being used instead of BuildKit
- **"connection refused"**: BuildKit sidecar not running or network issue
- **Slow builds**: Check BuildKit cache is working (should see "CACHED" in output)
```

**Commit:** `docs(buildkit): document client integration and troubleshooting`

---

### Phase 9: Performance Testing

**Goal:** Verify BuildKit improves build performance

**Approach:**
1. Time builds with Docker SDK (baseline)
2. Time builds with BuildKit client
3. Measure cache effectiveness
4. Test concurrent builds

**Expected Results:**
- First build: Similar to Docker SDK
- Cached builds: 5-10x faster with BuildKit
- Concurrent builds: No "active sessions" errors

**Document results in:** Performance comparison comment in code or docs

---

### Phase 10: Migration Path

**Goal:** Smooth transition for existing users

**Backward Compatibility:**
- Docker SDK path still works (when BUILDKIT_HOST not set)
- No breaking changes to public APIs
- Existing configurations work unchanged

**Migration:**
- New docker:dind runs automatically use BuildKit
- Existing docker:host runs continue using Docker SDK
- Users can opt-out via `MOAT_DISABLE_BUILDKIT=1` if needed

---

## Testing Strategy

### Unit Tests
- BuildKit client creation
- Path selection logic
- Error handling
- Build context preparation

### Integration Tests
- BuildKit connection
- Image building with sidecar
- Cache behavior
- Progress streaming

### E2E Tests
- Full docker:dind workflow
- Python/node/go runtime builds
- Multi-stage builds
- Cache mount usage

### Manual Testing
- Build performance comparison
- Error message clarity
- Progress output formatting
- Concurrent build behavior

---

## Rollback Plan

If issues arise:

1. **Quick rollback**: Set `MOAT_DISABLE_BUILDKIT=1` environment variable
2. **Code rollback**: Docker SDK path is still intact, just not used by default
3. **Revert commits**: All changes are in discrete commits for easy revert

---

## Success Criteria

1. ✅ No more "no active sessions" errors
2. ✅ Builds work with BuildKit sidecar
3. ✅ All tests pass
4. ✅ Clear error messages for common issues
5. ✅ Performance improvement on cached builds
6. ✅ Backward compatibility maintained

---

## Dependencies

- `github.com/moby/buildkit/client` - BuildKit Go client
- `github.com/moby/buildkit/util/progress/progressui` - Progress display
- `golang.org/x/sync/errgroup` - Concurrent error handling

---

## Open Questions

None - design approved.

---

## References

- BuildKit client documentation: https://pkg.go.dev/github.com/moby/buildkit/client
- BuildKit examples: https://github.com/moby/buildkit/tree/master/examples
- Previous implementation: `docs/plans/2026-01-27-buildkit-sidecar-design.md`
