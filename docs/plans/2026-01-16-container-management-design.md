# Container Management Tools Design

## Problem

Users need to manage underlying container resources (images, containers) for:
- Debugging image cache issues (built images not updating)
- Cleaning up orphaned containers from crashed runs
- Disk space management

Currently, users drop to Docker Desktop GUI or raw Docker CLI, which breaks the agentops abstraction and requires Docker knowledge.

## Design Philosophy

**Stay at the "run" abstraction.** Users should think in runs, not containers. The container/image layer is an implementation detail.

Rather than exposing Docker internals, we provide higher-level operations that address the underlying needs:
- Force fresh build → `agent run --rebuild`
- Clean up old stuff → `agent clean`
- See resource usage → `agent status`
- Debug escape hatch → `agent system images/containers` (read-only)

## Commands

### `agent status`

Shows runs, images, disk usage, and health indicators.

```
Runtime: docker

Runs (3 total, 1 running)
  NAME        RUN ID      STATE     AGE      DISK
  my-agent    a1b2c3d4    running   2h ago   124 MB
  test-bot    e5f6g7h8    stopped   1d ago   89 MB
  scraper     i9j0k1l2    stopped   3d ago   201 MB

Images (4 total, 892 MB)
  IMAGE                        CREATED     SIZE      USED BY
  agentops/my-agent:latest     2h ago      312 MB    my-agent
  agentops/test-bot:latest     1d ago      298 MB    test-bot
  agentops/scraper:latest      3d ago      282 MB    scraper
  agentops/base-node:22        5d ago      -         (base)

Health
  ⚠ 2 stopped runs can be cleaned (290 MB)
  ✓ No orphaned containers
  ✓ No dangling images
```

**Behaviors:**
- Shows detected runtime (Docker or Apple)
- Groups output into Runs, Images, Health
- Health section provides actionable hints
- Supports `--json` for scripting
- "USED BY" column links images to runs

### `agent clean`

Interactively removes stopped runs and unused images.

```
$ agent clean

Scanning for resources to clean...

Stopped runs (2):
  test-bot    e5f6g7h8    stopped   1d ago    89 MB
  scraper     i9j0k1l2    stopped   3d ago   201 MB

Unused images (1):
  agentops/scraper:latest    3d ago    282 MB    (no active run)

Total: 3 resources, 572 MB

Remove these resources? [y/N]: y

Removing run test-bot (e5f6g7h8)... done
Removing run scraper (i9j0k1l2)... done
Removing image agentops/scraper:latest... done

Cleaned 3 resources, freed 572 MB
```

**Behaviors:**
- Shows exactly what will be removed before asking
- Groups by type (runs, images)
- Shows size impact
- Single y/N confirmation (no per-item prompts)
- `--dry-run` flag to see what would happen without prompting
- `--force` flag to skip confirmation (for scripts)
- Never removes running containers or images used by running containers
- Never touches base images still referenced by other agent images

**Future:** Can evolve to TUI-style interface for bulk selection.

### `agent system images`

Read-only listing of agentops-managed images.

```
$ agent system images

IMAGE ID       TAG                          SIZE      CREATED
sha256:a1b2    agentops/my-agent:latest     312 MB    2h ago
sha256:c3d4    agentops/test-bot:latest     298 MB    1d ago
sha256:e5f6    agentops/base-node:22        245 MB    5d ago

To remove an image: docker rmi <image-id>
```

### `agent system containers`

Read-only listing of agentops-managed containers.

```
$ agent system containers

CONTAINER ID   NAME                    STATUS     CREATED
abc123def456   agentops-a1b2c3d4       running    2h ago
fed654cba321   agentops-e5f6g7h8       exited     1d ago

To remove a container: docker rm <container-id>
To view logs: docker logs <container-id>
```

**Behaviors for both `agent system` commands:**
- Read-only—just identification, no delete operations
- Filters to only agentops resources (by label or naming convention)
- Includes helpful hint showing native commands
- Supports `--json` for scripting
- Shows appropriate native commands based on runtime (Docker vs Apple)

### `agent run --rebuild`

Nuclear option: delete cached image, rebuild from scratch.

```
$ agent run --rebuild my-agent .

Removing cached image agentops/my-agent:latest... done
Building image (no cache)...
  Step 1/5: FROM node:22-slim
  Step 2/5: ...
  ...
Image built: agentops/my-agent:latest (312 MB)

Starting run a1b2c3d4...
```

**Behaviors:**
- Deletes existing `agentops/<agent-name>:latest` image if present
- Builds with `--no-cache` (Docker) or equivalent
- Does not re-pull base images (user can `docker pull` manually if needed)
- Works even if no cached image exists (just builds normally)
- Verbose by default when rebuilding (shows build progress)

## Implementation

### Resource Identification

Agentops resources are identified by:
- **Containers:** Named with `agentops-` prefix and/or labeled with `agentops=true`
- **Images:** Tagged with `agentops/` prefix

Labels should be added during creation for reliable filtering.

### Runtime Interface Additions

The `container.Runtime` interface needs:

```go
// ListImages returns all agentops-managed images.
ListImages(ctx context.Context) ([]ImageInfo, error)

// ListContainers returns all agentops containers (running + stopped).
ListContainers(ctx context.Context) ([]ContainerInfo, error)

// RemoveImage removes an image by ID or tag.
RemoveImage(ctx context.Context, id string) error

// ImageSize returns the size of an image in bytes.
ImageSize(ctx context.Context, id string) (int64, error)
```

### File Structure

```
cmd/agent/cli/
  status.go              # agent status
  clean.go               # agent clean
  system.go              # agent system (parent command)
  system_images.go       # agent system images
  system_containers.go   # agent system containers
  run.go                 # modify for --rebuild flag
```

### Not Included

- Delete operations in `agent system`—users use native Docker/container CLI
- Management of non-agentops resources
- `--pull` flag to refresh base images (too much complexity)
