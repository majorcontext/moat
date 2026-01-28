package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/term"
	"github.com/creack/pty"
)

// AppleRuntime implements Runtime using Apple's container CLI tool.
type AppleRuntime struct {
	containerBin string
	hostAddress  string

	buildMgr *appleBuildManager
}

// appleBuildManager implements BuildManager for Apple containers.
type appleBuildManager struct {
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
	r.buildMgr = &appleBuildManager{
		containerBin: binPath,
		hostAddress:  "192.168.64.1",
	}

	return r, nil
}

// NetworkManager returns nil - Apple containers don't support custom networks.
func (r *AppleRuntime) NetworkManager() NetworkManager {
	return nil
}

// SidecarManager returns nil - Apple containers don't support sidecars.
func (r *AppleRuntime) SidecarManager() SidecarManager {
	return nil
}

// BuildManager returns the Apple build manager.
func (r *AppleRuntime) BuildManager() BuildManager {
	return r.buildMgr
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

// CreateContainer creates a new Apple container without starting it.
// The container can later be started with StartContainer (non-interactive)
// or StartAttached (interactive with TTY).
func (r *AppleRuntime) CreateContainer(ctx context.Context, cfg Config) (string, error) {
	// Ensure image is available
	if err := r.ensureImage(ctx, cfg.Image); err != nil {
		return "", err
	}

	// Build command arguments
	args := r.buildCreateArgs(cfg)

	cmd := exec.CommandContext(ctx, r.containerBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("container create: %w: %s", err, stderr.String())
	}

	// The container ID is returned on stdout
	containerID := strings.TrimSpace(stdout.String())
	if containerID == "" {
		return "", fmt.Errorf("container create returned empty ID")
	}

	return containerID, nil
}

// buildCreateArgs constructs the arguments for 'container create'.
func (r *AppleRuntime) buildCreateArgs(cfg Config) []string {
	args := []string{"create"}

	// Interactive mode flags
	// Apple's container CLI requires a real PTY when using -t (TTY) flag.
	// We only add -t if os.Stdin is an actual terminal. This allows programmatic
	// use (tests, scripts) to work with -i alone, while real interactive sessions
	// get full TTY support.
	if cfg.Interactive {
		args = append(args, "-i") // Keep stdin open
		if term.IsTerminal(os.Stdin) {
			args = append(args, "-t") // Allocate TTY only if we have a real terminal
		}
	}

	// Container name
	if cfg.Name != "" {
		args = append(args, "--name", cfg.Name)
	}

	// Working directory
	if cfg.WorkingDir != "" {
		args = append(args, "--workdir", cfg.WorkingDir)
	}

	// User to run as
	if cfg.User != "" {
		args = append(args, "--user", cfg.User)
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

// StartContainer starts a created or stopped container.
func (r *AppleRuntime) StartContainer(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, r.containerBin, "start", containerID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting container: %w: %s", err, stderr.String())
	}
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
	// Use -w flag to wait for xtables lock (avoids exit code 4 from lock contention)
	// Use conntrack module instead of state for better container compatibility
	_ = proxyHost // See function comment for why this is unused
	script := fmt.Sprintf(`
		# Verify iptables is available
		if ! command -v iptables >/dev/null 2>&1; then
			echo "ERROR: iptables not found - container will not be firewalled" >&2
			exit 1
		fi

		# Flush existing rules (may fail if no rules exist, that's OK)
		iptables -w -F OUTPUT 2>/dev/null || true

		# Allow loopback
		iptables -w -A OUTPUT -o lo -j ACCEPT

		# Allow established/related connections (conntrack more reliable than state in containers)
		iptables -w -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

		# Allow DNS (UDP 53) - needed for initial hostname resolution
		iptables -w -A OUTPUT -p udp --dport 53 -j ACCEPT

		# Allow traffic to proxy port (destination IP not filtered - see function comment)
		iptables -w -A OUTPUT -p tcp --dport %d -j ACCEPT

		# Drop all other outbound traffic
		iptables -w -A OUTPUT -j DROP
	`, proxyPort)

	// Run as root since iptables requires root privileges
	cmd := exec.CommandContext(ctx, r.containerBin, "exec", "--user", "root", containerID, "sh", "-c", script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("firewall setup failed: %w: %s (iptables may not be available)", err, stderr.String())
	}

	return nil
}

// ImageExists checks if an image exists locally.
func (r *AppleRuntime) ImageExists(ctx context.Context, tag string) (bool, error) {
	return r.buildMgr.ImageExists(ctx, tag)
}

// ImageExists checks if an image exists locally.
func (m *appleBuildManager) ImageExists(ctx context.Context, tag string) (bool, error) {
	cmd := exec.CommandContext(ctx, m.containerBin, "image", "inspect", tag)
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

// BuildImage builds an image using Apple's container CLI.
// Before building, it fixes the builder's DNS configuration to work around
// a known issue (apple/container#656) where the builder cannot resolve external hosts.
func (r *AppleRuntime) BuildImage(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error {
	return r.buildMgr.BuildImage(ctx, dockerfile, tag, opts)
}

// BuildImage builds an image using Apple's container CLI.
// Before building, it fixes the builder's DNS configuration to work around
// a known issue (apple/container#656) where the builder cannot resolve external hosts.
func (m *appleBuildManager) BuildImage(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error {
	// Fix builder DNS before building
	if err := m.fixBuilderDNS(ctx, opts.DNS); err != nil {
		return fmt.Errorf("configuring builder DNS: %w", err)
	}

	// Write Dockerfile to a temp directory
	tmpDir, err := os.MkdirTemp("", "moat-build-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("writing Dockerfile: %w", err)
	}

	fmt.Printf("Building image %s...\n", tag)

	// Run container build
	args := []string{"build", "-f", dockerfilePath, "-t", tag}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args, tmpDir)

	cmd := exec.CommandContext(ctx, m.containerBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building image: %w", err)
	}
	return nil
}

// builderDNSLockPath is the file used for advisory locking during builder DNS configuration.
// This prevents race conditions when multiple moat processes configure DNS simultaneously.
const builderDNSLockPath = "/tmp/moat-builder-dns.lock"

// fixBuilderDNS ensures the Apple container builder has working DNS.
// This works around apple/container#656 where the builder's default DNS
// (the gateway) doesn't forward queries.
//
// Uses a file lock to prevent race conditions when multiple moat processes
// attempt to configure the builder DNS simultaneously.
func (m *appleBuildManager) fixBuilderDNS(ctx context.Context, configuredDNS []string) error {
	// Acquire file lock to prevent concurrent DNS configuration
	lockFile, err := os.OpenFile(builderDNSLockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening DNS lock file: %w", err)
	}
	defer lockFile.Close()

	// Use blocking flock - will wait if another process holds the lock
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquiring DNS lock: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	// Use configured DNS if provided
	dnsServers := configuredDNS

	// If not configured, try to detect host DNS
	if len(dnsServers) == 0 {
		detected, err := detectHostDNS(ctx)
		if err != nil {
			return fmt.Errorf("cannot detect host DNS for Apple container builder\n\n"+
				"Set DNS explicitly in agent.yaml:\n\n"+
				"  container:\n"+
				"    apple:\n"+
				"      builder_dns: [\"192.168.1.1\"]  # your router/corporate DNS\n\n"+
				"Or use public DNS if you accept the privacy trade-off:\n\n"+
				"  container:\n"+
				"    apple:\n"+
				"      builder_dns: [\"8.8.8.8\"]\n\n"+
				"Error: %w", err)
		}
		dnsServers = detected
	}

	// Ensure builder is running before we try to configure it
	if err := m.ensureBuilderRunning(ctx); err != nil {
		return fmt.Errorf("starting builder: %w", err)
	}

	// Validate and build resolv.conf content
	var resolv strings.Builder
	for _, server := range dnsServers {
		// Validate DNS server is a valid IP address to prevent injection
		if net.ParseIP(server) == nil {
			return fmt.Errorf("invalid DNS server %q: not a valid IP address", server)
		}
		resolv.WriteString("nameserver ")
		resolv.WriteString(server)
		resolv.WriteString("\n")
	}

	// Write to builder's /etc/resolv.conf using stdin to avoid shell injection
	cmd := exec.CommandContext(ctx, m.containerBin, "exec", "-i", "buildkit",
		"sh", "-c", "cat > /etc/resolv.conf")
	cmd.Stdin = strings.NewReader(resolv.String())

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("configuring builder DNS: %w", err)
	}
	return nil
}

// ensureBuilderRunning starts the builder if it's not already running.
func (m *appleBuildManager) ensureBuilderRunning(ctx context.Context) error {
	// Check if builder is already running by checking output (exit code is always 0)
	if m.isBuilderRunning(ctx) {
		return nil
	}

	// Start the builder
	cmd := exec.CommandContext(ctx, m.containerBin, "builder", "start")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting builder: %w", err)
	}

	// Wait for builder to be ready and accessible via exec
	fmt.Println("Waiting for Apple container builder to start...")
	const maxRetries = 30
	for i := 0; i < maxRetries; i++ {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if m.isBuilderRunning(ctx) {
			// Also verify exec works (builder may take a moment to be accessible)
			testCmd := exec.CommandContext(ctx, m.containerBin, "exec", "buildkit", "true")
			if testCmd.Run() == nil {
				return nil
			}
		}

		// Don't sleep on last iteration
		if i < maxRetries-1 {
			time.Sleep(time.Second)
		}
	}
	return fmt.Errorf("builder did not become ready in 30 seconds")
}

// isBuilderRunning checks if the builder container is in running state.
// Note: `container builder status` always returns exit code 0, so we must check output.
func (m *appleBuildManager) isBuilderRunning(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, m.containerBin, "builder", "status")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// When not running: "builder is not running"
	// When running: table with STATE column showing "running"
	// Check for "not running" first to avoid false positive from "running" substring
	output := string(out)
	if strings.Contains(output, "not running") {
		return false
	}
	return strings.Contains(output, "running")
}

// detectHostDNS attempts to detect the host's DNS servers from macOS system config.
// Uses the provided context for timeout/cancellation.
func detectHostDNS(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "scutil", "--dns")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running scutil --dns: %w", err)
	}

	var servers []string
	var skippedIPv6 []string
	var skippedLocalhost []string
	seen := make(map[string]bool)

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver[") {
			// Parse "nameserver[0] : 192.168.1.1"
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				server := strings.TrimSpace(parts[1])
				// Skip IPv6 for now (container networking may not support it well)
				if strings.Contains(server, ":") {
					if !seen[server] {
						skippedIPv6 = append(skippedIPv6, server)
						seen[server] = true
					}
					continue
				}
				// Skip localhost (won't work from container)
				if server == "127.0.0.1" {
					if !seen[server] {
						skippedLocalhost = append(skippedLocalhost, server)
						seen[server] = true
					}
					continue
				}
				// Deduplicate
				if !seen[server] {
					seen[server] = true
					servers = append(servers, server)
				}
			}
		}
	}

	// Log what was found/skipped to help debug DNS detection issues
	if len(skippedIPv6) > 0 {
		log.Debug("DNS detection skipped IPv6 servers", "servers", skippedIPv6)
	}
	if len(skippedLocalhost) > 0 {
		log.Debug("DNS detection skipped localhost", "servers", skippedLocalhost)
	}
	if len(servers) > 0 {
		log.Debug("DNS detection found usable servers", "servers", servers)
	}

	if len(servers) == 0 {
		return nil, fmt.Errorf("no usable DNS servers found in host configuration")
	}
	return servers, nil
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

// ListContainers returns all moat containers.
func (r *AppleRuntime) ListContainers(ctx context.Context) ([]Info, error) {
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

	var result []Info
	for _, c := range containers {
		if isRunID(c.Name) {
			created, _ := time.Parse(time.RFC3339, c.Created)
			result = append(result, Info{
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

// RemoveImage removes an image by ID or tag.
func (r *AppleRuntime) RemoveImage(ctx context.Context, id string) error {
	cmd := exec.CommandContext(ctx, r.containerBin, "image", "delete", id)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("removing image %s: %w: %s", id, err, stderr.String())
	}
	return nil
}

// GetImageHomeDir returns the home directory configured in an image.
// For Apple containers, we inspect the image config similar to Docker.
// Returns "/root" if detection fails or no home is configured.
func (r *AppleRuntime) GetImageHomeDir(ctx context.Context, imageName string) string {
	return r.buildMgr.GetImageHomeDir(ctx, imageName)
}

// GetImageHomeDir returns the home directory configured in an image.
// For Apple containers, we inspect the image config similar to Docker.
// Returns "/root" if detection fails or no home is configured.
func (m *appleBuildManager) GetImageHomeDir(ctx context.Context, imageName string) string {
	const defaultHome = "/root"

	// Ensure image is available first
	if err := m.ensureImage(ctx, imageName); err != nil {
		return defaultHome
	}

	// Try to inspect the image for config
	cmd := exec.CommandContext(ctx, m.containerBin, "image", "inspect", imageName)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return defaultHome
	}

	// Parse the JSON output
	var info []struct {
		Config struct {
			User string   `json:"user"`
			Env  []string `json:"env"`
		} `json:"config"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil || len(info) == 0 {
		return defaultHome
	}

	// Check for explicit HOME in environment
	for _, env := range info[0].Config.Env {
		if strings.HasPrefix(env, "HOME=") {
			return strings.TrimPrefix(env, "HOME=")
		}
	}

	// Check the USER - if non-root, derive home from it
	user := info[0].Config.User
	if user == "" || user == "root" || user == "0" {
		return defaultHome
	}

	// Strip any UID:GID format
	if colonIdx := strings.Index(user, ":"); colonIdx != -1 {
		user = user[:colonIdx]
	}

	// If it's a numeric UID, we can't determine the home directory
	if _, err := strconv.Atoi(user); err == nil {
		return defaultHome
	}

	// Validate username contains only safe characters (POSIX username pattern)
	// This prevents path traversal attacks from malicious image configs
	if !isValidUsername(user) {
		return defaultHome
	}

	return "/home/" + user
}

// ensureImage pulls an image if it doesn't exist locally.
func (m *appleBuildManager) ensureImage(ctx context.Context, imageName string) error {
	// Check if image exists
	cmd := exec.CommandContext(ctx, m.containerBin, "image", "inspect", imageName)
	if err := cmd.Run(); err == nil {
		return nil // Image exists
	}

	fmt.Printf("Pulling image %s...\n", imageName)
	cmd = exec.CommandContext(ctx, m.containerBin, "image", "pull", imageName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pulling image %s: %w", imageName, err)
	}
	return nil
}

// BuildCreateArgs is exported for testing.
func BuildCreateArgs(cfg Config) []string {
	r := &AppleRuntime{}
	return r.buildCreateArgs(cfg)
}

// ContainerState returns the state of a container ("running", "exited", "created", etc).
// Returns an error if the container doesn't exist.
func (r *AppleRuntime) ContainerState(ctx context.Context, containerID string) (string, error) {
	cmd := exec.CommandContext(ctx, r.containerBin, "inspect", containerID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("inspecting container %s: %w", containerID, err)
	}

	// Apple's inspect returns an array of container info objects
	var info []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return "", fmt.Errorf("parsing container %s info: %w", containerID, err)
	}

	if len(info) == 0 {
		return "", fmt.Errorf("container %s not found", containerID)
	}

	return info[0].Status, nil
}

// Attach connects stdin/stdout/stderr to a running container.
func (r *AppleRuntime) Attach(ctx context.Context, containerID string, opts AttachOptions) error {
	// Build attach command arguments
	args := []string{"attach"}
	if opts.Stdin != nil {
		args = append(args, "--stdin")
	}
	args = append(args, containerID)

	cmd := exec.CommandContext(ctx, r.containerBin, args...)

	// Connect stdin/stdout/stderr
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	}

	// Run the attach command
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("attaching to container: %w", err)
	}
	return nil
}

// ResizeTTY resizes the container's TTY to the given dimensions.
// Note: Apple container CLI may not support dynamic resize.
func (r *AppleRuntime) ResizeTTY(ctx context.Context, containerID string, height, width uint) error {
	// Apple container doesn't have a direct resize command.
	// The TTY size is typically inherited from the terminal running the attach command.
	return nil
}

// StartAttached starts a container with stdin/stdout/stderr already attached.
// This is required for TUI applications that need the terminal connected
// before the process starts.
//
// Uses `container start --attach` which starts the container and attaches
// to its primary process. The ENTRYPOINT handles any initialization (SSH agent
// bridge setup, config file copying, privilege dropping via gosu).
//
// The Apple container CLI requires real PTY file descriptors for stdout/stderr.
// To allow callers to intercept output (e.g., for a status bar), we create a
// PTY pair and copy data from the PTY master to the provided writers.
func (r *AppleRuntime) StartAttached(ctx context.Context, containerID string, opts AttachOptions) error {
	// Build start command arguments
	args := []string{"start", "--attach"}
	if opts.Stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, containerID)

	cmd := exec.CommandContext(ctx, r.containerBin, args...)

	// Create a PTY for the command. This gives the Apple container CLI
	// real PTY file descriptors while allowing us to intercept output.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("starting container with pty: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Set PTY size. Prefer explicit initial size from opts, fall back to querying terminal.
	if opts.TTY {
		var width, height uint
		if opts.InitialWidth > 0 && opts.InitialHeight > 0 {
			width, height = opts.InitialWidth, opts.InitialHeight
		} else if term.IsTerminal(os.Stdout) {
			w, h := term.GetSize(os.Stdout)
			if w > 0 && h > 0 {
				// #nosec G115 -- width/height are validated positive above
				width, height = uint(w), uint(h)
			}
		}
		if width > 0 && height > 0 {
			// #nosec G115 -- width/height are validated positive above and come from terminal
			_ = pty.Setsize(ptmx, &pty.Winsize{
				Rows: uint16(height), // #nosec G115
				Cols: uint16(width),  // #nosec G115
			})
		}
	}

	// Create a cancellable context for the copy goroutines
	copyCtx, cancelCopy := context.WithCancel(ctx)
	defer cancelCopy()

	// Channel to capture errors from stdin copy (e.g., escape sequences)
	stdinErr := make(chan error, 1)

	// Copy stdin to PTY master
	if opts.Stdin != nil {
		go func() {
			_, err := io.Copy(ptmx, opts.Stdin)
			select {
			case stdinErr <- err:
			case <-copyCtx.Done():
			}
		}()
	}

	// Copy PTY master to stdout (through the provided writer)
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		if opts.Stdout != nil {
			_, _ = io.Copy(opts.Stdout, ptmx)
		} else {
			_, _ = io.Copy(os.Stdout, ptmx)
		}
	}()

	// Wait for command to finish
	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- cmd.Wait()
	}()

	// Wait for either command completion, stdin error, or context cancellation
	var result error
	select {
	case err := <-stdinErr:
		// Stdin copy finished (possibly with escape error)
		// Close PTY and kill CLI - this detaches from the container without stopping it
		_ = ptmx.Close()
		cancelCopy()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-cmdDone
		if err != nil {
			result = err
		}
	case err := <-cmdDone:
		// Command finished normally
		cancelCopy()
		if err != nil && ctx.Err() == nil {
			result = fmt.Errorf("starting container attached: %w", err)
		}
	case <-ctx.Done():
		// Context canceled
		_ = ptmx.Close()
		cancelCopy()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-cmdDone
		result = ctx.Err()
	}

	// Brief wait for output copy to finish
	<-outputDone

	return result
}
