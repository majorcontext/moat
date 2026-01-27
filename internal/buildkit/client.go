package buildkit

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/andybons/moat/internal/log"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/progress/progressui"
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
func (c *Client) Build(ctx context.Context, opts BuildOptions) error {
	log.Debug("starting buildkit build",
		"tag", opts.Tag,
		"context_dir", opts.ContextDir,
		"platform", opts.Platform,
		"no_cache", opts.NoCache)

	// Connect to BuildKit
	bkClient, err := client.New(ctx, c.addr)
	if err != nil {
		return fmt.Errorf("failed to connect to BuildKit at %s - check if docker:dind sidecar is running and BUILDKIT_HOST is configured correctly: %w", c.addr, err)
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

	// Disable cache if requested
	if opts.NoCache {
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
			return fmt.Errorf("failed to initialize progress display: %w", err)
		}
		_, err = display.UpdateFrom(ctx, ch)
		return err
	})

	// Execute build
	eg.Go(func() error {
		_, err := bkClient.Solve(ctx, nil, solveOpt, ch)
		if err != nil {
			// Provide context about common build failures
			return fmt.Errorf("build failed - check Dockerfile syntax and build context at %s: %w", opts.ContextDir, err)
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		log.Error("buildkit build failed", "tag", opts.Tag, "error", err)
		return err
	}

	log.Debug("buildkit build completed successfully", "tag", opts.Tag)
	return nil
}

// Ping checks if BuildKit is reachable.
func (c *Client) Ping(ctx context.Context) error {
	log.Debug("pinging buildkit", "address", c.addr)
	bkClient, err := client.New(ctx, c.addr)
	if err != nil {
		return fmt.Errorf("BuildKit not reachable at %s - verify docker:dind sidecar is running and network configuration is correct: %w", c.addr, err)
	}
	defer bkClient.Close()
	log.Debug("buildkit ping successful", "address", c.addr)
	return nil
}
