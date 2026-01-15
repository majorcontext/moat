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
func (r *AppleRuntime) GetPortBindings(ctx context.Context, containerID string) (map[int]int, error) {
	cmd := exec.CommandContext(ctx, r.containerBin, "inspect", containerID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	// Parse the JSON output to find port mappings
	// Apple container inspect format may vary - try common structures
	var info []struct {
		Ports []struct {
			ContainerPort int `json:"container_port"`
			HostPort      int `json:"host_port"`
		} `json:"ports"`
		PortBindings map[string][]struct {
			HostPort string `json:"HostPort"`
		} `json:"port_bindings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		// If parsing fails, return empty map - container may not have port bindings
		return make(map[int]int), nil
	}

	result := make(map[int]int)
	if len(info) > 0 {
		// Try "ports" format first
		for _, p := range info[0].Ports {
			if p.ContainerPort > 0 && p.HostPort > 0 {
				result[p.ContainerPort] = p.HostPort
			}
		}
		// Try "port_bindings" format if "ports" was empty
		if len(result) == 0 && info[0].PortBindings != nil {
			for portKey, bindings := range info[0].PortBindings {
				if len(bindings) == 0 {
					continue
				}
				// portKey format is "3000/tcp"
				var containerPort int
				_, _ = fmt.Sscanf(portKey, "%d", &containerPort)
				if containerPort > 0 {
					hostPort, _ := strconv.Atoi(bindings[0].HostPort)
					if hostPort > 0 {
						result[containerPort] = hostPort
					}
				}
			}
		}
	}
	return result, nil
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

// SetupFirewall configures iptables to block all outbound traffic except to the proxy.
// The proxyHost parameter is accepted for interface consistency but not used in the
// iptables rules. This is intentional: the gateway IP can vary between container
// networks. The security model relies on per-run proxy authentication (cryptographic
// token in HTTP_PROXY URL) rather than IP filtering. This is more robust than IP-based
// filtering and prevents unauthorized access even if another service runs on the same port.
func (r *AppleRuntime) SetupFirewall(ctx context.Context, containerID string, proxyHost string, proxyPort int) error {
	// Apple containers run Linux VMs, so iptables should work
	_ = proxyHost // See function comment for why this is unused
	script := fmt.Sprintf(`
		# Flush existing rules
		iptables -F OUTPUT 2>/dev/null || true

		# Allow loopback
		iptables -A OUTPUT -o lo -j ACCEPT

		# Allow established/related connections
		iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

		# Allow DNS (UDP 53) - needed for initial hostname resolution
		iptables -A OUTPUT -p udp --dport 53 -j ACCEPT

		# Allow traffic to proxy port (destination IP not filtered - see function comment)
		iptables -A OUTPUT -p tcp --dport %d -j ACCEPT

		# Drop all other outbound traffic
		iptables -A OUTPUT -j DROP
	`, proxyPort)

	cmd := exec.CommandContext(ctx, r.containerBin, "exec", containerID, "sh", "-c", script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("firewall setup failed: %w: %s (iptables may not be available)", err, stderr.String())
	}

	return nil
}

// ImageExists checks if an image exists locally.
func (r *AppleRuntime) ImageExists(ctx context.Context, tag string) (bool, error) {
	cmd := exec.CommandContext(ctx, r.containerBin, "image", "inspect", tag)
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

// BuildImage is not supported for Apple containers.
// Apple container uses install scripts instead of Dockerfile builds.
func (r *AppleRuntime) BuildImage(ctx context.Context, dockerfile string, tag string) error {
	return fmt.Errorf("building images is not supported for Apple containers; use install scripts instead")
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
