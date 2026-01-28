package buildkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/andybons/moat/internal/log"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"
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
		return nil, fmt.Errorf("BUILDKIT_HOST not set - this should not happen when BuildKit routing is enabled")
	}
	log.Debug("creating buildkit client", "address", addr)
	return &Client{addr: addr}, nil
}

// BuildOptions configures a BuildKit build.
type BuildOptions struct {
	Dockerfile string            // Reserved for future use - currently unused
	Tag        string            // Image tag (e.g., "moat/run:abc123")
	ContextDir string            // Build context directory
	NoCache    bool              // Disable build cache
	Platform   string            // Target platform (e.g., "linux/amd64")
	BuildArgs  map[string]string // Build arguments
	Output     io.Writer         // Progress output (default: os.Stdout)
}

// Build executes a build using BuildKit.
//
// The build process:
//  1. Connects to BuildKit sidecar via BUILDKIT_HOST (tcp://buildkit:1234)
//  2. Prepares build context from ContextDir using LocalMounts (BuildKit manages session internally)
//  3. Executes build with dockerfile.v0 frontend
//  4. Exports result as Docker image tar, piping directly to `docker load`
//
// This approach avoids manual session management and filesync complexity by:
//   - Using LocalMounts for build context (BuildKit auto-manages the filesync session)
//   - Using Output function to stream the docker exporter tar to `docker load`
func (c *Client) Build(ctx context.Context, opts BuildOptions) error {
	log.Debug("starting buildkit build", "tag", opts.Tag, "platform", opts.Platform)

	// Wait for BuildKit to become ready (daemon init takes ~5-10s)
	if err := c.WaitForReady(ctx); err != nil {
		return fmt.Errorf("BuildKit not ready: %w", err)
	}

	// Connect to BuildKit sidecar
	bkClient, err := client.New(ctx, c.addr)
	if err != nil {
		return fmt.Errorf("failed to connect to BuildKit at %s - check if docker:dind sidecar is running and BUILDKIT_HOST is configured correctly: %w", c.addr, err)
	}
	defer bkClient.Close()

	// Prepare filesystem for build context
	fs, err := fsutil.NewFS(opts.ContextDir)
	if err != nil {
		return fmt.Errorf("creating filesystem for context: %w", err)
	}

	// Configure BuildKit solve operation
	// LocalMounts triggers BuildKit to automatically create and manage a filesync session
	solveOpt := client.SolveOpt{
		Frontend: "dockerfile.v0",
		FrontendAttrs: map[string]string{
			"filename": "Dockerfile",
			"platform": opts.Platform,
		},
		LocalMounts: map[string]fsutil.FS{
			"context":    fs,
			"dockerfile": fs,
		},
	}

	// Add build args
	for k, v := range opts.BuildArgs {
		solveOpt.FrontendAttrs["build-arg:"+k] = v
	}

	// Disable cache if requested
	if opts.NoCache {
		solveOpt.FrontendAttrs["no-cache"] = ""
	}

	// Export as Docker image tar, piped to `docker load`
	// The Output function receives the tar stream from BuildKit's docker exporter
	// and pipes it to `docker load` for import into the Docker daemon
	solveOpt.Exports = []client.ExportEntry{
		{
			Type: client.ExporterDocker,
			Attrs: map[string]string{
				"name": opts.Tag,
			},
			Output: func(m map[string]string) (io.WriteCloser, error) {
				return c.createDockerLoadPipe(ctx)
			},
		},
	}

	// Progress writer
	output := opts.Output
	if output == nil {
		output = os.Stdout
	}

	// Execute build with concurrent progress display
	ch := make(chan *client.SolveStatus)
	eg, ctx := errgroup.WithContext(ctx)

	// Display build progress
	eg.Go(func() error {
		display, err := progressui.NewDisplay(output, progressui.AutoMode)
		if err != nil {
			return fmt.Errorf("failed to initialize progress display: %w", err)
		}
		_, err = display.UpdateFrom(ctx, ch)
		return err
	})

	// Execute build
	eg.Go(func() error {
		_, err := bkClient.Solve(ctx, nil, solveOpt, ch)
		if err != nil {
			return fmt.Errorf("build failed - check Dockerfile syntax and build context at %s: %w", opts.ContextDir, err)
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	log.Debug("buildkit build completed", "tag", opts.Tag)
	return nil
}

// createDockerLoadPipe creates a pipe to `docker load` for importing the built image.
//
// BuildKit writes the image tar stream to the returned WriteCloser, which feeds
// directly into `docker load` stdin. This approach:
//   - Avoids intermediate tar files on disk
//   - Streams the image directly to the Docker daemon
//   - Ensures the image is imported atomically with the build
func (c *Client) createDockerLoadPipe(ctx context.Context) (io.WriteCloser, error) {
	cmd := exec.CommandContext(ctx, "docker", "load")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe for docker load: %w", err)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting docker load: %w", err)
	}

	return &dockerLoadWriter{
		WriteCloser: stdin,
		cmd:         cmd,
	}, nil
}

// dockerLoadWriter wraps the stdin pipe and ensures docker load completes successfully.
type dockerLoadWriter struct {
	io.WriteCloser
	cmd *exec.Cmd
}

func (w *dockerLoadWriter) Close() error {
	// Close stdin to signal EOF
	if err := w.WriteCloser.Close(); err != nil {
		return err
	}

	// Wait for docker load to complete
	if err := w.cmd.Wait(); err != nil {
		return fmt.Errorf("docker load failed: %w", err)
	}

	return nil
}

// Ping checks if BuildKit is reachable.
func (c *Client) Ping(ctx context.Context) error {
	bkClient, err := client.New(ctx, c.addr)
	if err != nil {
		return fmt.Errorf("BuildKit not reachable at %s - verify docker:dind sidecar is running and network configuration is correct: %w", c.addr, err)
	}
	defer bkClient.Close()
	return nil
}

// WaitForReady waits for BuildKit to become ready with exponential backoff.
// BuildKit daemon takes ~5-10s to initialize after sidecar starts.
func (c *Client) WaitForReady(ctx context.Context) error {
	backoff := 100 * time.Millisecond
	maxBackoff := 2 * time.Second
	timeout := 30 * time.Second

	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for BuildKit to become ready at %s", c.addr)
		}

		err := c.Ping(ctx)
		if err == nil {
			return nil
		}

		log.Debug("waiting for BuildKit to become ready", "addr", c.addr, "backoff", backoff)

		select {
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
