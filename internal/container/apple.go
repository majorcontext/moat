package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AppleRuntime implements Runtime using Apple's container CLI tool.
type AppleRuntime struct {
	containerBin string
	hostAddress  string
}

// NewAppleRuntime creates a new Apple container runtime.
func NewAppleRuntime() (*AppleRuntime, error) {
	// Find the container binary
	binPath, err := exec.LookPath("container")
	if err != nil {
		return nil, fmt.Errorf("container CLI not found: %w", err)
	}

	r := &AppleRuntime{
		containerBin: binPath,
		hostAddress:  "192.168.64.1", // Default gateway for Apple containers
	}

	return r, nil
}

// Type returns RuntimeApple.
func (r *AppleRuntime) Type() RuntimeType {
	return RuntimeApple
}

// Ping verifies the Apple container system is running.
func (r *AppleRuntime) Ping(ctx context.Context) error {
	// Try to list containers to verify the system is working
	cmd := exec.CommandContext(ctx, r.containerBin, "list", "--quiet")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apple container system not accessible: %w", err)
	}
	return nil
}

// CreateContainer creates a new Apple container.
// Note: Apple's container CLI combines create+start in "run --detach".
func (r *AppleRuntime) CreateContainer(ctx context.Context, cfg Config) (string, error) {
	// Ensure image is available
	if err := r.ensureImage(ctx, cfg.Image); err != nil {
		return "", err
	}

	// Build command arguments
	args := r.buildRunArgs(cfg)

	cmd := exec.CommandContext(ctx, r.containerBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("container run: %w: %s", err, stderr.String())
	}

	// The container ID is returned on stdout
	containerID := strings.TrimSpace(stdout.String())
	if containerID == "" {
		return "", fmt.Errorf("container run returned empty ID")
	}

	// Try to get the actual gateway address for this container
	if gateway := r.getContainerGateway(ctx, containerID); gateway != "" {
		r.hostAddress = gateway
	}

	return containerID, nil
}

// buildRunArgs constructs the arguments for 'container run'.
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

	// DNS configuration - Apple container's default DNS (gateway) often doesn't work
	// Use Google's public DNS as a reliable fallback
	args = append(args, "--dns", "8.8.8.8")
	args = append(args, "--dns", "8.8.4.4")

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

// StartContainer is a no-op for Apple container since run --detach already starts it.
func (r *AppleRuntime) StartContainer(ctx context.Context, containerID string) error {
	// Apple's "container run --detach" already starts the container
	// If we need to start a stopped container, we'd use "container start"
	// For now, this is a no-op since CreateContainer uses run --detach
	return nil
}

// StopContainer stops a running container.
func (r *AppleRuntime) StopContainer(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, r.containerBin, "stop", containerID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stopping container: %w: %s", err, stderr.String())
	}
	return nil
}

// WaitContainer blocks until the container exits and returns the exit code.
func (r *AppleRuntime) WaitContainer(ctx context.Context, containerID string) (int64, error) {
	// Apple's container CLI may have a "wait" command, or we poll with inspect
	// Try "container wait" first
	cmd := exec.CommandContext(ctx, r.containerBin, "wait", containerID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// If wait is not available, fall back to polling
		return r.waitByPolling(ctx, containerID)
	}

	// Parse exit code from output
	exitStr := strings.TrimSpace(stdout.String())
	exitCode, err := strconv.ParseInt(exitStr, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("parsing exit code %q: %w", exitStr, err)
	}

	return exitCode, nil
}

// waitByPolling polls the container status until it exits.
func (r *AppleRuntime) waitByPolling(ctx context.Context, containerID string) (int64, error) {
	for {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		default:
		}

		// Check container status
		// Apple's container inspect outputs JSON directly (no --format flag)
		cmd := exec.CommandContext(ctx, r.containerBin, "inspect", containerID)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if err := cmd.Run(); err != nil {
			return -1, fmt.Errorf("inspecting container: %w", err)
		}

		// Apple's inspect returns an array of container info objects
		var info []struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
			return -1, fmt.Errorf("parsing container info: %w", err)
		}

		if len(info) > 0 && (info[0].Status == "exited" || info[0].Status == "stopped") {
			// Apple's container CLI doesn't provide exit code in inspect output
			// Return 0 for stopped containers (best we can do)
			return 0, nil
		}

		// Sleep before next poll to avoid hammering the CLI
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(500 * time.Millisecond):
			// Continue polling
		}
	}
}

// RemoveContainer removes a container.
func (r *AppleRuntime) RemoveContainer(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, r.containerBin, "rm", "--force", containerID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("removing container: %w: %s", err, stderr.String())
	}
	return nil
}

// ContainerLogs returns a reader for the container's logs (follows output).
func (r *AppleRuntime) ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, r.containerBin, "logs", "--follow", containerID)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("getting stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		return nil, fmt.Errorf("getting stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("starting logs command: %w", err)
	}

	// Combine stdout and stderr into a single reader
	return &combinedReadCloser{
		readers: []io.Reader{stdout, stderr},
		cmd:     cmd,
		stdout:  stdout,
		stderr:  stderr,
	}, nil
}

// ContainerLogsAll returns all logs from a container (does not follow).
func (r *AppleRuntime) ContainerLogsAll(ctx context.Context, containerID string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.containerBin, "logs", containerID)
	return cmd.Output()
}

// GetPortBindings returns the actual host ports assigned to container ports.
// Apple containers don't support port publishing in the same way Docker does,
// so this returns an empty map.
func (r *AppleRuntime) GetPortBindings(ctx context.Context, containerID string) (map[int]int, error) {
	// Apple containers don't have Docker-style port bindings.
	// Container ports are accessed via the container's IP directly.
	return make(map[int]int), nil
}

// GetHostAddress returns the gateway IP for containers to reach the host.
func (r *AppleRuntime) GetHostAddress() string {
	return r.hostAddress
}

// SupportsHostNetwork returns false - Apple containers don't support host network mode.
func (r *AppleRuntime) SupportsHostNetwork() bool {
	return false
}

// Close is a no-op for Apple container (no persistent connection).
func (r *AppleRuntime) Close() error {
	return nil
}

// ensureImage pulls an image if it doesn't exist locally.
func (r *AppleRuntime) ensureImage(ctx context.Context, imageName string) error {
	// Check if image exists
	cmd := exec.CommandContext(ctx, r.containerBin, "image", "inspect", imageName)
	if err := cmd.Run(); err == nil {
		return nil // Image exists
	}

	fmt.Printf("Pulling image %s...\n", imageName)
	cmd = exec.CommandContext(ctx, r.containerBin, "image", "pull", imageName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pulling image %s: %w", imageName, err)
	}
	return nil
}

// getContainerGateway retrieves the gateway IP for a container.
func (r *AppleRuntime) getContainerGateway(ctx context.Context, containerID string) string {
	cmd := exec.CommandContext(ctx, r.containerBin, "inspect", containerID, "--format", "json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return ""
	}

	var info struct {
		Network struct {
			Gateway string `json:"gateway"`
		} `json:"network"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return ""
	}

	return info.Network.Gateway
}

// combinedReadCloser combines multiple readers and handles cleanup.
type combinedReadCloser struct {
	readers []io.Reader
	cmd     *exec.Cmd
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	once    sync.Once
	mr      io.Reader
}

func (c *combinedReadCloser) Read(p []byte) (int, error) {
	c.once.Do(func() {
		c.mr = io.MultiReader(c.readers...)
	})
	return c.mr.Read(p)
}

func (c *combinedReadCloser) Close() error {
	c.stdout.Close()
	c.stderr.Close()
	return c.cmd.Wait()
}

// BuildRunArgs is exported for testing.
func BuildRunArgs(cfg Config) []string {
	r := &AppleRuntime{}
	return r.buildRunArgs(cfg)
}
