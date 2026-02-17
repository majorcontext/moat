package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	goruntime "runtime"

	"github.com/creack/pty"
	"github.com/majorcontext/moat/internal/container/output"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/term"
)

// AppleRuntime implements Runtime using Apple's container CLI tool.
type AppleRuntime struct {
	containerBin string
	hostAddress  string

	buildMgr   *appleBuildManager
	networkMgr *appleNetworkManager
	serviceMgr *appleServiceManager

	// activePTY tracks PTY masters for attached containers so ResizeTTY
	// can propagate terminal size changes. Protected by ptyMu.
	ptyMu     sync.Mutex
	activePTY map[string]*os.File
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
	r.networkMgr = &appleNetworkManager{containerBin: binPath}
	r.serviceMgr = &appleServiceManager{containerBin: binPath}

	return r, nil
}

// NetworkManager returns the Apple network manager.
func (r *AppleRuntime) NetworkManager() NetworkManager {
	return r.networkMgr
}

// SidecarManager returns nil - Apple containers don't support sidecars.
func (r *AppleRuntime) SidecarManager() SidecarManager {
	return nil
}

// ServiceManager returns the Apple service manager.
func (r *AppleRuntime) ServiceManager() ServiceManager {
	return r.serviceMgr
}

// BuildManager returns the Apple build manager.
func (r *AppleRuntime) BuildManager() BuildManager {
	return r.buildMgr
}

// Type returns RuntimeApple.
func (r *AppleRuntime) Type() RuntimeType {
	return RuntimeApple
}

// findFreePort asks the OS for an available TCP port.
// Note: there is an inherent TOCTOU race between releasing the port here and
// the container CLI binding to it. This is the best we can do since Apple's
// container CLI does not support automatic port assignment (host-port is required).
func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close() //nolint:errcheck // best-effort close after reading port
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected address type %T", l.Addr())
	}
	return addr.Port, nil
}

// isKernelNotConfiguredError checks if an error message indicates
// that the Apple container kernel is not configured.
func isKernelNotConfiguredError(errMsg string) bool {
	return strings.Contains(errMsg, "kernel not configured") || strings.Contains(errMsg, "default kernel")
}

// kernelNotConfiguredError returns a user-friendly error for missing kernel config.
func kernelNotConfiguredError() error {
	return fmt.Errorf("no Linux kernel configured for Apple containers.\n\nRun this command to install the recommended kernel:\n\n  container system kernel set --recommended\n\nThen retry your moat command")
}

// Ping verifies the Apple container system is running.
func (r *AppleRuntime) Ping(ctx context.Context) error {
	// Try to list containers to verify the system is working
	cmd := exec.CommandContext(ctx, r.containerBin, "list", "--quiet")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if isKernelNotConfiguredError(errMsg) {
			return kernelNotConfiguredError()
		}
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
	args, err := r.buildCreateArgs(cfg)
	if err != nil {
		return "", fmt.Errorf("building container arguments: %w", err)
	}

	cmd := exec.CommandContext(ctx, r.containerBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if isKernelNotConfiguredError(errMsg) {
			return "", kernelNotConfiguredError()
		}
		return "", fmt.Errorf("container create: %w: %s", err, errMsg)
	}

	// The container ID is returned on stdout
	containerID := strings.TrimSpace(stdout.String())
	if containerID == "" {
		return "", fmt.Errorf("container create returned empty ID")
	}

	return containerID, nil
}

// buildCreateArgs constructs the arguments for 'container create'.
func (r *AppleRuntime) buildCreateArgs(cfg Config) ([]string, error) {
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

	// Resource limits (Apple containers only)
	// Default to 4096 MB (4 GB) memory if not specified, since the Apple container
	// default of 1024 MB is often insufficient for AI coding environments.
	memoryMB := cfg.MemoryMB
	if memoryMB == 0 {
		memoryMB = 4096
	}
	args = append(args, "--memory", fmt.Sprintf("%dMB", memoryMB))

	// CPUs - only add if explicitly set, otherwise use Apple container default (typically 4)
	if cfg.CPUs > 0 {
		args = append(args, "--cpus", strconv.Itoa(cfg.CPUs))
	}

	// Working directory
	if cfg.WorkingDir != "" {
		args = append(args, "--workdir", cfg.WorkingDir)
	}

	// User to run as
	if cfg.User != "" {
		args = append(args, "--user", cfg.User)
	}

	// Network - attach to a named network for inter-container communication
	if cfg.NetworkMode != "" && cfg.NetworkMode != "bridge" && cfg.NetworkMode != "host" && cfg.NetworkMode != "none" {
		args = append(args, "--network", cfg.NetworkMode)
	} else {
		// DNS configuration - Apple container's default DNS (gateway) often doesn't work.
		// Use configured DNS or default to Google's public DNS as a reliable fallback.
		// Only set when not on a custom network, since custom networks provide their own DNS.
		for _, dns := range DefaultDNS(cfg.DNS) {
			args = append(args, "--dns", dns)
		}
	}

	// Port bindings
	// Apple container CLI requires explicit host ports (no random assignment).
	// Format: [host-ip:]host-port:container-port
	for containerPort, hostIP := range cfg.PortBindings {
		hostPort, err := findFreePort()
		if err != nil {
			return nil, fmt.Errorf("finding free port for container port %d: %w", containerPort, err)
		}
		if hostIP != "" && hostIP != "0.0.0.0" {
			args = append(args, "--publish", fmt.Sprintf("%s:%d:%d", hostIP, hostPort, containerPort))
		} else {
			args = append(args, "--publish", fmt.Sprintf("%d:%d", hostPort, containerPort))
		}
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

	return args, nil
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
		stderrStr := stderr.String()

		// Ignore "not found" errors - container may have already been removed
		if strings.Contains(stderrStr, "notFound") || strings.Contains(stderrStr, "not found") {
			return nil
		}

		// XPC timeout errors are transient failures from Apple's container runtime.
		// The container might still be running, but the caller will attempt RemoveContainer
		// with --force flag next, which should kill and remove it in one step.
		if strings.Contains(stderrStr, "XPC timeout") {
			return fmt.Errorf("stopping container (XPC timeout - will try force removal): %w: %s", err, stderrStr)
		}

		return fmt.Errorf("stopping container: %w: %s", err, stderrStr)
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
		// Ignore "not found" errors - container may have already been removed
		// The Apple container CLI returns errors like: Error: notFound: "failed to delete one or more containers: ["run_xxx"]"
		stderrStr := stderr.String()
		if strings.Contains(stderrStr, "notFound") || strings.Contains(stderrStr, "not found") {
			return nil
		}

		// XPC timeout errors can occur when Apple's container runtime is under load or having issues.
		// These are transient failures - the container might get cleaned up eventually by the system,
		// or the user can manually clean up with: container rm --force <id>
		if strings.Contains(stderrStr, "XPC timeout") {
			return fmt.Errorf("removing container (XPC timeout - container may require manual cleanup): %w: %s", err, stderrStr)
		}

		return fmt.Errorf("removing container: %w: %s", err, stderrStr)
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
		log.Debug("failed to parse container inspect output for port bindings: " + err.Error())
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
	// Validate port range
	if proxyPort < 1 || proxyPort > 65535 {
		return fmt.Errorf("invalid proxy port %d: must be between 1 and 65535", proxyPort)
	}

	// Apple containers run Linux VMs whose kernel may lack nf_tables modules.
	// Use iptables-legacy if available, falling back to iptables.
	// Use -w flag to wait for xtables lock (avoids exit code 4 from lock contention).
	_ = proxyHost // See function comment for why this is unused
	script := fmt.Sprintf(`
		# Prefer iptables-legacy since Apple container kernels may lack nf_tables
		if command -v iptables-legacy >/dev/null 2>&1; then
			IPT=iptables-legacy
		elif command -v iptables >/dev/null 2>&1; then
			IPT=iptables
		else
			echo "ERROR: iptables not found - container will not be firewalled" >&2
			exit 1
		fi

		# Flush existing rules (may fail if no rules exist, that's OK)
		$IPT -w -F OUTPUT 2>/dev/null || true

		# Allow loopback
		$IPT -w -A OUTPUT -o lo -j ACCEPT

		# Allow established/related connections
		$IPT -w -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

		# Allow DNS (UDP 53) - needed for initial hostname resolution
		$IPT -w -A OUTPUT -p udp --dport 53 -j ACCEPT

		# Allow traffic to proxy port (destination IP not filtered - see function comment)
		$IPT -w -A OUTPUT -p tcp --dport %d -j ACCEPT

		# Drop all other outbound traffic
		$IPT -w -A OUTPUT -j DROP
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
func (m *appleBuildManager) ImageExists(ctx context.Context, tag string) (bool, error) {
	cmd := exec.CommandContext(ctx, m.containerBin, "image", "inspect", tag)
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

// BuildImage builds an image using Apple's container CLI.
func (m *appleBuildManager) BuildImage(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error {
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

	// Write additional context files (e.g., scripts for COPY instead of heredoc)
	for name, content := range opts.ContextFiles {
		path := filepath.Join(tmpDir, name)
		// Create parent directories if needed
		if dir := filepath.Dir(path); dir != tmpDir {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("creating context dir for %s: %w", name, err)
			}
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			return fmt.Errorf("writing context file %s: %w", name, err)
		}
	}

	output.BuildingImage(tag)

	buildErr := m.runBuild(ctx, dockerfilePath, tag, opts.NoCache, tmpDir)
	if buildErr == nil {
		return nil
	}

	// The Apple container builder's gRPC transport can become inactive during
	// long builds. Restart the builder and retry once.
	if !isBuilderTransportError(buildErr) {
		return fmt.Errorf("building image: %w", buildErr)
	}

	log.Debug("builder transport became inactive, restarting builder and retrying build...")
	// Force a full restart since the transport is known-broken, then fix DNS.
	if err := m.restartBuilder(ctx); err != nil {
		return fmt.Errorf("building image (retry failed): builder restart: %w (original error: %v)", err, buildErr)
	}
	if err := m.fixBuilderDNS(ctx, opts.DNS); err != nil {
		return fmt.Errorf("building image (retry failed): %w (original error: %v)", err, buildErr)
	}

	fmt.Println("Retrying build...")
	if err := m.runBuild(ctx, dockerfilePath, tag, opts.NoCache, tmpDir); err != nil {
		return fmt.Errorf("building image (retry failed): %w", err)
	}
	return nil
}

// runBuild executes a single container build attempt, capturing stderr for error detection.
func (m *appleBuildManager) runBuild(ctx context.Context, dockerfilePath, tag string, noCache bool, contextDir string) error {
	cpus := goruntime.NumCPU() / 2
	if cpus < 2 {
		cpus = 2
	}
	args := []string{"build",
		"-f", dockerfilePath,
		"-t", tag,
		"--cpus", strconv.Itoa(cpus),
		"--memory", "8192MB",
	}
	if noCache {
		args = append(args, "--no-cache")
	}
	args = append(args, contextDir)

	cmd := exec.CommandContext(ctx, m.containerBin, args...)
	cmd.Stdout = os.Stdout
	// Capture stderr while also printing it so users see build progress
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)

	if err := cmd.Run(); err != nil {
		// Attach stderr content to the error for transport error detection
		return fmt.Errorf("%w: %s", err, stderrBuf.String())
	}
	return nil
}

// isBuilderTransportError checks if an error is a gRPC transport failure
// from the Apple container builder.
func isBuilderTransportError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Transport became inactive") ||
		strings.Contains(msg, "unavailable (14)")
}

// restartBuilder stops and restarts the Apple container builder.
func (m *appleBuildManager) restartBuilder(ctx context.Context) error {
	// Stop the builder (best-effort, may already be stopped)
	stopCmd := exec.CommandContext(ctx, m.containerBin, "builder", "stop")
	_ = stopCmd.Run()

	// Force-delete to clear stale state. Without --force, delete fails when
	// the builder is "running" but its gRPC transport is dead (e.g., after a
	// "Transport became inactive" error), leaving us stuck with a broken builder.
	delCmd := exec.CommandContext(ctx, m.containerBin, "builder", "delete", "--force")
	_ = delCmd.Run()

	return m.startBuilder(ctx)
}

// startBuilder starts the Apple container builder with appropriate resources.
// Allocates half the host CPUs and 8GB memory (capped at available resources)
// to avoid OOM/transport failures during large image builds.
func (m *appleBuildManager) startBuilder(ctx context.Context) error {
	cpus := goruntime.NumCPU() / 2
	if cpus < 2 {
		cpus = 2
	}

	args := []string{"builder", "start",
		"--cpus", strconv.Itoa(cpus),
		"--memory", "8192MB",
	}

	cmd := exec.CommandContext(ctx, m.containerBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if isKernelNotConfiguredError(errMsg) {
			return kernelNotConfiguredError()
		}
		return fmt.Errorf("starting builder: %w: %s", err, errMsg)
	}

	return m.waitForBuilder(ctx)
}

// fixBuilderDNS ensures the Apple container builder has working DNS.
// This works around apple/container#656 where the builder's default DNS
// (the gateway at 192.168.64.1) doesn't forward queries.
//
// The fix starts the builder if needed, then configures DNS using a simple
// exec command without stdin to avoid corrupting the gRPC transport.
//
// Uses the same DNS servers as runtime containers for consistency.
func (m *appleBuildManager) fixBuilderDNS(ctx context.Context, dns []string) error {
	// Use configured DNS or default to Google's public DNS
	dnsServers := DefaultDNS(dns)

	// Start the builder if it's not already running. Avoid unconditional
	// restarts — stopping and restarting the builder immediately before a
	// build destabilizes the gRPC transport, causing "Transport became
	// inactive" errors during large builds.
	if !m.isBuilderRunning(ctx) {
		if err := m.startBuilder(ctx); err != nil {
			return err
		}
	}

	// Validate and build resolv.conf content using printf format (handles newlines correctly)
	var resolvConf strings.Builder
	for _, server := range dnsServers {
		if net.ParseIP(server) == nil {
			return fmt.Errorf("invalid DNS server %q: not a valid IP address", server)
		}
		resolvConf.WriteString("nameserver ")
		resolvConf.WriteString(server)
		resolvConf.WriteString("\\n") // Escaped for printf
	}

	// Configure DNS using simple exec (no stdin to avoid gRPC transport corruption).
	// Use printf instead of echo to properly handle embedded newlines.
	cmd := exec.CommandContext(ctx, m.containerBin, "exec", "buildkit",
		"sh", "-c", fmt.Sprintf("printf '%s' > /etc/resolv.conf", resolvConf.String()))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		// If the exec failed due to a transport error, the builder is a zombie:
		// it reports "running" but its gRPC transport is dead. Force-restart it
		// so the subsequent build has a working builder.
		if isBuilderTransportError(errors.New(errMsg)) {
			log.Debug("builder transport is dead (zombie builder), force-restarting...")
			if restartErr := m.restartBuilder(ctx); restartErr != nil {
				return fmt.Errorf("restarting zombie builder: %w", restartErr)
			}
			// Re-apply DNS fix after restart
			retryCmd := exec.CommandContext(ctx, m.containerBin, "exec", "buildkit",
				"sh", "-c", fmt.Sprintf("printf '%s' > /etc/resolv.conf", resolvConf.String()))
			if retryErr := retryCmd.Run(); retryErr != nil {
				log.Debug("DNS configuration failed after builder restart", "error", retryErr)
			}
			return nil
		}
		// Non-transport error: log but don't fail - the build may still succeed with default DNS
		log.Debug("DNS configuration failed, build may use default DNS", "error", err, "stderr", errMsg)
		return nil
	}

	return nil
}

// waitForBuilder waits for the builder to be ready and accessible via exec.
func (m *appleBuildManager) waitForBuilder(ctx context.Context) error {
	const maxRetries = 30
	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if m.isBuilderRunning(ctx) {
			// Verify exec works (builder may take a moment to be accessible)
			testCmd := exec.CommandContext(ctx, m.containerBin, "exec", "buildkit", "true")
			if testCmd.Run() == nil {
				return nil
			}
		}

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
	output := string(out)
	if strings.Contains(output, "not running") {
		return false
	}
	return strings.Contains(output, "running")
}

// ensureImage pulls an image if it doesn't exist locally.
func (r *AppleRuntime) ensureImage(ctx context.Context, imageName string) error {
	// Check if image exists
	cmd := exec.CommandContext(ctx, r.containerBin, "image", "inspect", imageName)
	if err := cmd.Run(); err == nil {
		return nil // Image exists
	}

	output.PullingImage(imageName)
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

	output.PullingImage(imageName)
	cmd = exec.CommandContext(ctx, m.containerBin, "image", "pull", imageName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pulling image %s: %w", imageName, err)
	}
	return nil
}

// BuildCreateArgs is exported for testing.
func BuildCreateArgs(cfg Config) ([]string, error) {
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
// For Apple containers, this resizes the PTY master created during StartAttached.
func (r *AppleRuntime) ResizeTTY(ctx context.Context, containerID string, height, width uint) error {
	r.ptyMu.Lock()
	ptmx := r.activePTY[containerID]
	r.ptyMu.Unlock()

	if ptmx == nil {
		return nil
	}

	// #nosec G115 -- height/width are validated positive by callers
	return pty.Setsize(ptmx, &pty.Winsize{
		Rows: uint16(height), // #nosec G115
		Cols: uint16(width),  // #nosec G115
	})
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

	// For TTY mode, use a PTY. For non-TTY mode, use regular pipes.
	// This matches how the container was created (with -t flag for TTY, without for non-TTY).
	if opts.TTY {
		return r.startAttachedWithPTY(ctx, cmd, containerID, opts)
	}
	return r.startAttachedWithPipes(ctx, cmd, opts)
}

// startAttachedWithPTY handles TTY mode using a PTY
func (r *AppleRuntime) startAttachedWithPTY(ctx context.Context, cmd *exec.Cmd, containerID string, opts AttachOptions) error {
	// Create a PTY for the command. This gives the Apple container CLI
	// real PTY file descriptors while allowing us to intercept output.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("starting container with pty: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Track the PTY so ResizeTTY can propagate SIGWINCH.
	r.ptyMu.Lock()
	if r.activePTY == nil {
		r.activePTY = make(map[string]*os.File)
	}
	r.activePTY[containerID] = ptmx
	r.ptyMu.Unlock()
	defer func() {
		r.ptyMu.Lock()
		delete(r.activePTY, containerID)
		r.ptyMu.Unlock()
	}()

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
		// Don't close PTY yet - wait for output to finish copying
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

	// Wait for output copy to finish before closing PTY
	// This ensures all output is captured even if the container exits quickly.
	// Use a timeout to prevent hanging if the copy goroutine gets stuck.
	select {
	case <-outputDone:
		// Output copy finished normally
	case <-time.After(2 * time.Second):
		// Timeout - forcibly close PTY to unblock the copy goroutine
		_ = ptmx.Close()
		<-outputDone
	}
	cancelCopy()

	return result
}

// startAttachedWithPipes handles non-TTY mode using regular pipes
func (r *AppleRuntime) startAttachedWithPipes(ctx context.Context, cmd *exec.Cmd, opts AttachOptions) error {
	// For non-TTY mode, use regular pipes to capture stdout/stderr
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	// Wait for command to complete
	err := cmd.Wait()
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("container attach: %w", err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	return nil
}
