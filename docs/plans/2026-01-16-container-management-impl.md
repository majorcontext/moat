# Container Management Tools Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add commands for managing container resources while preserving the run abstraction.

**Architecture:** Extend the `container.Runtime` interface with listing/removal methods. Add new CLI commands that operate on moat-managed resources only. Use the `moat/` image prefix and run ID container names for filtering.

**Tech Stack:** Go, Cobra CLI, Docker SDK, Apple container CLI

---

## Task 1: Extend Runtime Interface with Container/Image Listing

**Files:**
- Modify: `internal/container/runtime.go`

**Step 1: Add new types and interface methods**

Add after line 99 in `runtime.go`:

```go
// ImageInfo contains information about a container image.
type ImageInfo struct {
	ID      string
	Tag     string
	Size    int64
	Created time.Time
}

// ContainerInfo contains information about a container.
type ContainerInfo struct {
	ID      string
	Name    string
	Image   string
	Status  string // "running", "exited", "created"
	Created time.Time
}
```

Add to the `Runtime` interface:

```go
// ListImages returns all moat-managed images.
ListImages(ctx context.Context) ([]ImageInfo, error)

// ListContainers returns all moat containers (running + stopped).
ListContainers(ctx context.Context) ([]ContainerInfo, error)

// RemoveImage removes an image by ID or tag.
RemoveImage(ctx context.Context, id string) error
```

**Step 2: Verify it compiles**

Run: `go build ./...`
Expected: Compile errors for DockerRuntime and AppleRuntime (expected - they don't implement new methods yet)

**Step 3: Commit interface changes**

```bash
git add internal/container/runtime.go
git commit -m "feat(container): add interface methods for listing images/containers"
```

---

## Task 2: Implement Docker Runtime Listing Methods

**Files:**
- Modify: `internal/container/docker.go`
- Test: `internal/container/docker_list_test.go` (create)

**Step 1: Write test for ListImages**

Create `internal/container/docker_list_test.go`:

```go
//go:build integration

package container

import (
	"context"
	"testing"
)

func TestDockerRuntime_ListImages(t *testing.T) {
	rt, err := NewDockerRuntime()
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}
	defer rt.Close()

	ctx := context.Background()
	images, err := rt.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}

	// Should return without error (may be empty if no moat images)
	t.Logf("Found %d moat images", len(images))
}

func TestDockerRuntime_ListContainers(t *testing.T) {
	rt, err := NewDockerRuntime()
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}
	defer rt.Close()

	ctx := context.Background()
	containers, err := rt.ListContainers(ctx)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}

	t.Logf("Found %d moat containers", len(containers))
}
```

**Step 2: Implement ListImages for Docker**

Add to `docker.go`:

```go
// ListImages returns all moat-managed images.
// Filters to images with "moat/" prefix in any tag.
func (r *DockerRuntime) ListImages(ctx context.Context) ([]ImageInfo, error) {
	images, err := r.cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}

	var result []ImageInfo
	for _, img := range images {
		// Check if any tag has moat/ prefix
		for _, tag := range img.RepoTags {
			if strings.HasPrefix(tag, "moat/") {
				result = append(result, ImageInfo{
					ID:      img.ID,
					Tag:     tag,
					Size:    img.Size,
					Created: time.Unix(img.Created, 0),
				})
				break // Only add once per image
			}
		}
	}
	return result, nil
}
```

Add import `"strings"` if not present.

**Step 3: Implement ListContainers for Docker**

Add to `docker.go`:

```go
// ListContainers returns all moat containers.
// Filters to containers whose name matches an 8-char hex run ID pattern.
func (r *DockerRuntime) ListContainers(ctx context.Context) ([]ContainerInfo, error) {
	containers, err := r.cli.ContainerList(ctx, container.ListOptions{
		All: true, // Include stopped containers
	})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	var result []ContainerInfo
	for _, c := range containers {
		// Check if any name looks like an moat run ID (8 hex chars)
		for _, name := range c.Names {
			// Names have leading slash, e.g., "/a1b2c3d4"
			name = strings.TrimPrefix(name, "/")
			if isRunID(name) {
				result = append(result, ContainerInfo{
					ID:      c.ID[:12],
					Name:    name,
					Image:   c.Image,
					Status:  c.State,
					Created: time.Unix(c.Created, 0),
				})
				break
			}
		}
	}
	return result, nil
}

// isRunID checks if a string looks like an moat run ID (8 hex chars).
func isRunID(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
```

**Step 4: Implement RemoveImage for Docker**

Add to `docker.go`:

```go
// RemoveImage removes an image by ID or tag.
func (r *DockerRuntime) RemoveImage(ctx context.Context, id string) error {
	_, err := r.cli.ImageRemove(ctx, id, image.RemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	if err != nil {
		return fmt.Errorf("removing image %s: %w", id, err)
	}
	return nil
}
```

**Step 5: Run tests**

Run: `go test -tags=integration -v ./internal/container/ -run "List|Remove"`
Expected: PASS (or skip if Docker unavailable)

**Step 6: Commit**

```bash
git add internal/container/docker.go internal/container/docker_list_test.go
git commit -m "feat(container): implement Docker listing methods"
```

---

## Task 3: Implement Apple Runtime Listing Methods

**Files:**
- Modify: `internal/container/apple.go`

**Step 1: Implement ListImages for Apple**

Add to `apple.go`:

```go
// ListImages returns all moat-managed images.
func (r *AppleRuntime) ListImages(ctx context.Context) ([]ImageInfo, error) {
	cmd := exec.CommandContext(ctx, r.containerBin, "image", "list", "--format", "json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}

	var images []struct {
		ID      string `json:"id"`
		Tag     string `json:"tag"`
		Size    int64  `json:"size"`
		Created string `json:"created"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &images); err != nil {
		// Try line-by-line JSON if array parse fails
		return r.parseImageLines(stdout.Bytes())
	}

	var result []ImageInfo
	for _, img := range images {
		if strings.HasPrefix(img.Tag, "moat/") {
			created, _ := time.Parse(time.RFC3339, img.Created)
			result = append(result, ImageInfo{
				ID:      img.ID,
				Tag:     img.Tag,
				Size:    img.Size,
				Created: created,
			})
		}
	}
	return result, nil
}

func (r *AppleRuntime) parseImageLines(data []byte) ([]ImageInfo, error) {
	var result []ImageInfo
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var img struct {
			ID      string `json:"id"`
			Tag     string `json:"tag"`
			Size    int64  `json:"size"`
			Created string `json:"created"`
		}
		if err := json.Unmarshal(line, &img); err != nil {
			continue
		}
		if strings.HasPrefix(img.Tag, "moat/") {
			created, _ := time.Parse(time.RFC3339, img.Created)
			result = append(result, ImageInfo{
				ID:      img.ID,
				Tag:     img.Tag,
				Size:    img.Size,
				Created: created,
			})
		}
	}
	return result, nil
}
```

**Step 2: Implement ListContainers for Apple**

Add to `apple.go`:

```go
// ListContainers returns all moat containers.
func (r *AppleRuntime) ListContainers(ctx context.Context) ([]ContainerInfo, error) {
	cmd := exec.CommandContext(ctx, r.containerBin, "list", "--all", "--format", "json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	var containers []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Image   string `json:"image"`
		Status  string `json:"status"`
		Created string `json:"created"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &containers); err != nil {
		return nil, fmt.Errorf("parsing container list: %w", err)
	}

	var result []ContainerInfo
	for _, c := range containers {
		if isRunID(c.Name) {
			created, _ := time.Parse(time.RFC3339, c.Created)
			result = append(result, ContainerInfo{
				ID:      c.ID,
				Name:    c.Name,
				Image:   c.Image,
				Status:  c.Status,
				Created: created,
			})
		}
	}
	return result, nil
}
```

**Step 3: Implement RemoveImage for Apple**

Add to `apple.go`:

```go
// RemoveImage removes an image by ID or tag.
func (r *AppleRuntime) RemoveImage(ctx context.Context, id string) error {
	cmd := exec.CommandContext(ctx, r.containerBin, "image", "rm", "--force", id)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("removing image %s: %w: %s", id, err, stderr.String())
	}
	return nil
}
```

**Step 4: Add isRunID helper (shared)**

Move `isRunID` function to a shared location or duplicate in `apple.go`:

```go
// isRunID checks if a string looks like an moat run ID (8 hex chars).
func isRunID(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
```

Note: This will cause a duplicate if in same package. Either make it a package-level function once, or move to a `helpers.go` file.

**Step 5: Verify compilation**

Run: `go build ./...`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/container/apple.go
git commit -m "feat(container): implement Apple runtime listing methods"
```

---

## Task 4: Add `moat status` Command

**Files:**
- Create: `cmd/agent/cli/status.go`

**Step 1: Create basic status command**

Create `cmd/agent/cli/status.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/andybons/moat/internal/container"
	"github.com/andybons/moat/internal/run"
	"github.com/andybons/moat/internal/storage"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show runs, images, disk usage, and health",
	Long: `Display the current state of moat resources including:
- Active and stopped runs
- Cached container images
- Disk usage
- Health indicators and cleanup suggestions`,
	RunE: showStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

type statusOutput struct {
	Runtime    string           `json:"runtime"`
	Runs       []runInfo        `json:"runs"`
	Images     []imageInfo      `json:"images"`
	Health     []healthItem     `json:"health"`
	TotalDisk  int64            `json:"total_disk_bytes"`
}

type runInfo struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	State   string `json:"state"`
	Age     string `json:"age"`
	DiskMB  int64  `json:"disk_mb"`
}

type imageInfo struct {
	Tag     string `json:"tag"`
	Created string `json:"created"`
	SizeMB  int64  `json:"size_mb"`
	UsedBy  string `json:"used_by"`
}

type healthItem struct {
	Status  string `json:"status"` // "ok", "warning"
	Message string `json:"message"`
}

func showStatus(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get runtime
	rt, err := container.NewRuntime()
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer rt.Close()

	// Get runs
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runs := manager.List()

	// Get images
	images, err := rt.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	// Calculate disk usage per run
	runDiskUsage := make(map[string]int64)
	baseDir := storage.DefaultBaseDir()
	for _, r := range runs {
		runDir := filepath.Join(baseDir, r.ID)
		size := getDirSize(runDir)
		runDiskUsage[r.ID] = size
	}

	// Build output
	output := statusOutput{
		Runtime: string(rt.Type()),
	}

	// Runs section
	var runningCount, stoppedCount int
	var stoppedDisk int64
	for _, r := range runs {
		age := formatAge(r.CreatedAt)
		diskMB := runDiskUsage[r.ID] / (1024 * 1024)
		output.Runs = append(output.Runs, runInfo{
			Name:   r.Name,
			ID:     r.ID,
			State:  string(r.State),
			Age:    age,
			DiskMB: diskMB,
		})
		if r.State == run.StateRunning {
			runningCount++
		} else {
			stoppedCount++
			stoppedDisk += runDiskUsage[r.ID]
		}
	}

	// Images section
	var totalImageSize int64
	runNames := make(map[string]string) // image tag -> run name
	for _, r := range runs {
		// TODO: map images to runs more accurately
	}
	for _, img := range images {
		sizeMB := img.Size / (1024 * 1024)
		totalImageSize += img.Size
		output.Images = append(output.Images, imageInfo{
			Tag:     img.Tag,
			Created: formatAge(img.Created),
			SizeMB:  sizeMB,
			UsedBy:  runNames[img.Tag],
		})
	}

	// Health section
	if stoppedCount > 0 {
		output.Health = append(output.Health, healthItem{
			Status:  "warning",
			Message: fmt.Sprintf("%d stopped runs can be cleaned (%d MB)", stoppedCount, stoppedDisk/(1024*1024)),
		})
	}

	// Check for orphaned containers
	containers, _ := rt.ListContainers(ctx)
	knownRunIDs := make(map[string]bool)
	for _, r := range runs {
		knownRunIDs[r.ID] = true
	}
	orphanedCount := 0
	for _, c := range containers {
		if !knownRunIDs[c.Name] {
			orphanedCount++
		}
	}
	if orphanedCount > 0 {
		output.Health = append(output.Health, healthItem{
			Status:  "warning",
			Message: fmt.Sprintf("%d orphaned containers", orphanedCount),
		})
	} else {
		output.Health = append(output.Health, healthItem{
			Status:  "ok",
			Message: "No orphaned containers",
		})
	}

	output.TotalDisk = totalImageSize
	for _, size := range runDiskUsage {
		output.TotalDisk += size
	}

	// Output
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(output)
	}

	// Human-readable output
	fmt.Printf("Runtime: %s\n\n", output.Runtime)

	// Runs table
	if len(output.Runs) == 0 {
		fmt.Println("Runs (0)")
	} else {
		fmt.Printf("Runs (%d total, %d running)\n", len(output.Runs), runningCount)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tRUN ID\tSTATE\tAGE\tDISK")
		for _, r := range output.Runs {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%d MB\n",
				r.Name, r.ID, r.State, r.Age, r.DiskMB)
		}
		w.Flush()
	}
	fmt.Println()

	// Images table
	if len(output.Images) == 0 {
		fmt.Println("Images (0)")
	} else {
		fmt.Printf("Images (%d total, %d MB)\n", len(output.Images), totalImageSize/(1024*1024))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  TAG\tCREATED\tSIZE")
		for _, img := range output.Images {
			fmt.Fprintf(w, "  %s\t%s\t%d MB\n",
				img.Tag, img.Created, img.SizeMB)
		}
		w.Flush()
	}
	fmt.Println()

	// Health section
	fmt.Println("Health")
	for _, h := range output.Health {
		icon := "✓"
		if h.Status == "warning" {
			icon = "⚠"
		}
		fmt.Printf("  %s %s\n", icon, h.Message)
	}

	return nil
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func getDirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}
```

**Step 2: Verify it compiles and runs**

Run: `go build ./... && ./agent status`
Expected: Shows status output (may be empty if no runs)

**Step 3: Commit**

```bash
git add cmd/agent/cli/status.go
git commit -m "feat(cli): add agent status command"
```

---

## Task 5: Add `moat clean` Command

**Files:**
- Create: `cmd/agent/cli/clean.go`

**Step 1: Create clean command**

Create `cmd/agent/cli/clean.go`:

```go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/andybons/moat/internal/container"
	"github.com/andybons/moat/internal/run"
	"github.com/spf13/cobra"
)

var (
	cleanForce bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove stopped runs and unused images",
	Long: `Interactively remove stopped runs and unused moat images.

Shows what will be removed and asks for confirmation before proceeding.
Use --force to skip confirmation (for scripts).
Use --dry-run to see what would be removed without prompting.`,
	RunE: cleanResources,
}

func init() {
	rootCmd.AddCommand(cleanCmd)
	cleanCmd.Flags().BoolVarP(&cleanForce, "force", "f", false, "skip confirmation prompt")
}

func cleanResources(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get runtime
	rt, err := container.NewRuntime()
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer rt.Close()

	// Get runs
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	fmt.Println("Scanning for resources to clean...")
	fmt.Println()

	runs := manager.List()

	// Find stopped runs
	var stoppedRuns []*run.Run
	runningImages := make(map[string]bool)
	for _, r := range runs {
		if r.State == run.StateRunning {
			// Track images used by running containers
			// TODO: get actual image tag from container
		} else if r.State == run.StateStopped {
			stoppedRuns = append(stoppedRuns, r)
		}
	}

	// Find unused images
	images, err := rt.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	var unusedImages []container.ImageInfo
	for _, img := range images {
		if !runningImages[img.Tag] {
			unusedImages = append(unusedImages, img)
		}
	}

	// Nothing to clean?
	if len(stoppedRuns) == 0 && len(unusedImages) == 0 {
		fmt.Println("Nothing to clean.")
		return nil
	}

	// Show what will be removed
	var totalSize int64

	if len(stoppedRuns) > 0 {
		fmt.Printf("Stopped runs (%d):\n", len(stoppedRuns))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, r := range stoppedRuns {
			age := formatAge(r.CreatedAt)
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", r.Name, r.ID, "stopped", age)
		}
		w.Flush()
		fmt.Println()
	}

	if len(unusedImages) > 0 {
		fmt.Printf("Unused images (%d):\n", len(unusedImages))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, img := range unusedImages {
			sizeMB := img.Size / (1024 * 1024)
			totalSize += img.Size
			fmt.Fprintf(w, "  %s\t%s\t%d MB\n", img.Tag, formatAge(img.Created), sizeMB)
		}
		w.Flush()
		fmt.Println()
	}

	resourceCount := len(stoppedRuns) + len(unusedImages)
	fmt.Printf("Total: %d resources, %d MB\n\n", resourceCount, totalSize/(1024*1024))

	// Dry run - just show, don't prompt
	if dryRun {
		fmt.Println("Dry run - no changes made")
		return nil
	}

	// Confirm
	if !cleanForce {
		fmt.Print("Remove these resources? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Cancelled")
			return nil
		}
		fmt.Println()
	}

	// Remove stopped runs
	var freedSize int64
	var removedCount int
	for _, r := range stoppedRuns {
		fmt.Printf("Removing run %s (%s)... ", r.Name, r.ID)
		if err := manager.Destroy(ctx, r.ID); err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		fmt.Println("done")
		removedCount++
	}

	// Remove unused images
	for _, img := range unusedImages {
		fmt.Printf("Removing image %s... ", img.Tag)
		if err := rt.RemoveImage(ctx, img.ID); err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		fmt.Println("done")
		removedCount++
		freedSize += img.Size
	}

	fmt.Printf("\nCleaned %d resources, freed %d MB\n", removedCount, freedSize/(1024*1024))
	return nil
}
```

**Step 2: Verify it compiles and runs**

Run: `go build ./... && ./agent clean --dry-run`
Expected: Shows what would be cleaned

**Step 3: Commit**

```bash
git add cmd/agent/cli/clean.go
git commit -m "feat(cli): add agent clean command"
```

---

## Task 6: Add `moat system` Parent Command and Subcommands

**Files:**
- Create: `cmd/agent/cli/system.go`
- Create: `cmd/agent/cli/system_images.go`
- Create: `cmd/agent/cli/system_containers.go`

**Step 1: Create parent command**

Create `cmd/agent/cli/system.go`:

```go
package cli

import (
	"github.com/spf13/cobra"
)

var systemCmd = &cobra.Command{
	Use:   "system",
	Short: "Low-level container system commands",
	Long: `Access underlying container system resources.

These commands are escape hatches for debugging and advanced use.
For normal operations, use 'agent status' and 'agent clean' instead.`,
}

func init() {
	rootCmd.AddCommand(systemCmd)
}
```

**Step 2: Create images subcommand**

Create `cmd/agent/cli/system_images.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/andybons/moat/internal/container"
	"github.com/spf13/cobra"
)

var systemImagesCmd = &cobra.Command{
	Use:   "images",
	Short: "List moat-managed images",
	Long: `List all container images created by moat.

This is an escape hatch for debugging. Use 'agent status' for normal operations.

To remove an image, use the native container CLI:
  docker rmi <image-id>
  container image rm <image-id>`,
	RunE: listSystemImages,
}

func init() {
	systemCmd.AddCommand(systemImagesCmd)
}

func listSystemImages(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	rt, err := container.NewRuntime()
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer rt.Close()

	images, err := rt.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(images)
	}

	if len(images) == 0 {
		fmt.Println("No moat images found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "IMAGE ID\tTAG\tSIZE\tCREATED")
	for _, img := range images {
		id := img.ID
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%d MB\t%s\n",
			id, img.Tag, img.Size/(1024*1024), formatAge(img.Created))
	}
	w.Flush()

	fmt.Println()
	if rt.Type() == container.RuntimeDocker {
		fmt.Println("To remove an image: docker rmi <image-id>")
	} else {
		fmt.Println("To remove an image: container image rm <image-id>")
	}

	return nil
}
```

**Step 3: Create containers subcommand**

Create `cmd/agent/cli/system_containers.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/andybons/moat/internal/container"
	"github.com/spf13/cobra"
)

var systemContainersCmd = &cobra.Command{
	Use:   "containers",
	Short: "List moat-managed containers",
	Long: `List all containers created by moat.

This is an escape hatch for debugging. Use 'agent status' for normal operations.

To remove a container, use the native container CLI:
  docker rm <container-id>
  container rm <container-id>`,
	RunE: listSystemContainers,
}

func init() {
	systemCmd.AddCommand(systemContainersCmd)
}

func listSystemContainers(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	rt, err := container.NewRuntime()
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer rt.Close()

	containers, err := rt.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(containers)
	}

	if len(containers) == 0 {
		fmt.Println("No moat containers found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CONTAINER ID\tNAME\tSTATUS\tCREATED")
	for _, c := range containers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			c.ID, c.Name, c.Status, formatAge(c.Created))
	}
	w.Flush()

	fmt.Println()
	if rt.Type() == container.RuntimeDocker {
		fmt.Println("To remove a container: docker rm <container-id>")
		fmt.Println("To view logs: docker logs <container-id>")
	} else {
		fmt.Println("To remove a container: container rm <container-id>")
		fmt.Println("To view logs: container logs <container-id>")
	}

	return nil
}
```

**Step 4: Verify it compiles and runs**

Run: `go build ./... && ./agent system images && ./agent system containers`
Expected: Lists images and containers (may be empty)

**Step 5: Commit**

```bash
git add cmd/agent/cli/system.go cmd/agent/cli/system_images.go cmd/agent/cli/system_containers.go
git commit -m "feat(cli): add agent system images/containers commands"
```

---

## Task 7: Add `--rebuild` Flag to `moat run`

**Files:**
- Modify: `cmd/agent/cli/run.go`
- Modify: `internal/run/manager.go`
- Modify: `internal/run/options.go` (or wherever Options is defined)

**Step 1: Find and read Options definition**

The Options struct is likely in `internal/run/run.go` or `internal/run/options.go`. Check and add a Rebuild field.

**Step 2: Add --rebuild flag to run.go**

In `cmd/agent/cli/run.go`, add flag:

```go
var (
	grants      []string
	runEnv      []string
	nameFlag    string
	rebuildFlag bool  // Add this
)

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringSliceVar(&grants, "grant", nil, "capabilities to grant (e.g., github, aws:s3.read)")
	runCmd.Flags().StringArrayVarP(&runEnv, "env", "e", nil, "environment variables (KEY=VALUE)")
	runCmd.Flags().StringVar(&nameFlag, "name", "", "name for this agent instance (default: from agent.yaml or random)")
	runCmd.Flags().BoolVar(&rebuildFlag, "rebuild", false, "force rebuild of container image (ignores cache)")  // Add this
}
```

Pass to Options:

```go
opts := run.Options{
	Name:      agentInstanceName,
	Workspace: absPath,
	Grants:    grants,
	Cmd:       containerCmd,
	Config:    cfg,
	Env:       runEnv,
	Rebuild:   rebuildFlag,  // Add this
}
```

**Step 3: Add Rebuild field to Options**

In `internal/run/run.go` (or wherever Options is defined), add:

```go
type Options struct {
	Name      string
	Workspace string
	Grants    []string
	Cmd       []string
	Config    *config.AgentConfig
	Env       []string
	Rebuild   bool  // Add this - force image rebuild
}
```

**Step 4: Handle rebuild in manager.go**

In the `Create` function of `internal/run/manager.go`, after parsing dependencies and before the image build check, add:

```go
// Handle --rebuild: delete existing image to force fresh build
if opts.Rebuild && len(depList) > 0 && m.runtime.Type() == container.RuntimeDocker {
	exists, _ := m.runtime.ImageExists(ctx, containerImage)
	if exists {
		fmt.Printf("Removing cached image %s...\n", containerImage)
		if err := m.runtime.RemoveImage(ctx, containerImage); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove image: %v\n", err)
		}
	}
}
```

And modify the BuildImage call to use NoCache when rebuilding:

This requires adding a NoCache option to BuildImage. For simplicity, we can add a separate method or modify the signature. The cleanest approach is to add a BuildOptions struct.

**Alternative simpler approach:** Just delete the image (already done above) - this forces a rebuild since ImageExists will return false.

**Step 5: Verify it compiles**

Run: `go build ./...`
Expected: PASS

**Step 6: Test manually**

Run: `./agent run --rebuild .`
Expected: If there's an agent.yaml with dependencies, it should remove the cached image and rebuild

**Step 7: Commit**

```bash
git add cmd/agent/cli/run.go internal/run/manager.go internal/run/run.go
git commit -m "feat(cli): add --rebuild flag to agent run"
```

---

## Task 8: Final Testing and Polish

**Step 1: Run all tests**

Run: `go test ./...`
Expected: PASS

**Step 2: Run linter**

Run: `golangci-lint run`
Expected: No major issues (fix any that arise)

**Step 3: Manual end-to-end test**

```bash
# Build
go build -o agent ./cmd/agent

# Test status (should show empty or existing runs)
./agent status

# Test system commands
./agent system images
./agent system containers

# Test clean with dry-run
./agent clean --dry-run

# Test help text
./agent status --help
./agent clean --help
./agent system --help
./agent system images --help
```

**Step 4: Commit any fixes**

```bash
git add -A
git commit -m "fix: address test and lint issues"
```

---

## Summary

This plan implements:
1. **Runtime interface extensions** for listing images/containers
2. **Docker implementation** of listing methods
3. **Apple runtime implementation** of listing methods
4. **`moat status`** - overview of runs, images, disk usage, health
5. **`moat clean`** - interactive cleanup with y/N confirmation
6. **`moat system images/containers`** - read-only escape hatches
7. **`moat run --rebuild`** - nuclear option for cache issues

Total: 8 tasks, approximately 25-30 commits following TDD approach.
