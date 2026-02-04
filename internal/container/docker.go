package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/majorcontext/moat/internal/buildkit"
	"github.com/majorcontext/moat/internal/container/output"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/term"
)

// ErrGVisorNotAvailable is returned when gVisor is required but not installed.
var ErrGVisorNotAvailable = errors.New(`gVisor (runsc) is required but not available

To install on Linux (Debian/Ubuntu), copy and run:

  curl -fsSL https://gvisor.dev/archive.key | sudo gpg --dearmor -o /usr/share/keyrings/gvisor.gpg && \
    echo "deb [signed-by=/usr/share/keyrings/gvisor.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
    sudo tee /etc/apt/sources.list.d/gvisor.list && \
    sudo apt update && sudo apt install -y runsc && \
    sudo runsc install && \
    sudo systemctl reload docker

For Docker Desktop (macOS/Windows):
  See https://gvisor.dev/docs/user_guide/install/

To bypass (reduced isolation):
  moat run --no-sandbox`)

// DockerRuntime implements Runtime using Docker.
type DockerRuntime struct {
	cli        *client.Client
	ociRuntime string // "runsc" or "runc"

	// gVisor availability cache (initialized once via sync.Once, safe for concurrent reads)
	gvisorOnce  sync.Once
	gvisorAvail bool

	networkMgr *dockerNetworkManager
	sidecarMgr *dockerSidecarManager
	buildMgr   *dockerBuildManager
}

// dockerNetworkManager implements NetworkManager for Docker.
type dockerNetworkManager struct {
	cli *client.Client
}

// dockerSidecarManager implements SidecarManager for Docker.
type dockerSidecarManager struct {
	cli        *client.Client
	ociRuntime string // Same OCI runtime as main container ("runsc" or "")
	// Note: BuildKit sidecars inherit the main container's OCI runtime.
	// BuildKit is expected to work with gVisor (runsc), though this
	// combination has not been extensively tested in production.
}

// dockerBuildManager implements BuildManager for Docker.
type dockerBuildManager struct {
	cli *client.Client
}

// NewDockerRuntime creates a new Docker runtime.
// If sandbox is true, requires gVisor (runsc) and fails if unavailable.
// If sandbox is false, uses standard runc runtime with a warning.
func NewDockerRuntime(sandbox bool) (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}

	r := &DockerRuntime{
		cli: cli,
	}

	var ociRuntime string // empty string = Docker's default runtime
	if !sandbox {
		// Only warn on Linux where gVisor is available but explicitly disabled
		// On macOS/Windows, gVisor is unavailable by default (not a security downgrade)
		if goruntime.GOOS == "linux" {
			log.Warn("running without gVisor sandbox - reduced isolation")
		}
		// Leave ociRuntime empty to use Docker's default (usually runc)
	} else {
		// Verify gVisor is available using cached check
		if !r.gvisorAvailable() {
			cli.Close()
			return nil, fmt.Errorf("%w", ErrGVisorNotAvailable)
		}
		ociRuntime = "runsc"
	}

	r.ociRuntime = ociRuntime
	r.networkMgr = &dockerNetworkManager{cli: cli}
	r.sidecarMgr = &dockerSidecarManager{cli: cli, ociRuntime: ociRuntime}
	r.buildMgr = &dockerBuildManager{cli: cli}
	return r, nil
}

// NetworkManager returns the Docker network manager.
func (r *DockerRuntime) NetworkManager() NetworkManager {
	return r.networkMgr
}

// SidecarManager returns the Docker sidecar manager.
func (r *DockerRuntime) SidecarManager() SidecarManager {
	return r.sidecarMgr
}

// BuildManager returns the Docker build manager.
func (r *DockerRuntime) BuildManager() BuildManager {
	return r.buildMgr
}

// ServiceManager returns the Docker service manager for database/cache sidecars.
func (r *DockerRuntime) ServiceManager() ServiceManager {
	return &dockerServiceManager{
		sidecar: r.SidecarManager(),
		network: r.NetworkManager(),
		cli:     r.cli,
	}
}

// Type returns RuntimeDocker.
func (r *DockerRuntime) Type() RuntimeType {
	return RuntimeDocker
}

// Ping verifies the Docker daemon is accessible.
func (r *DockerRuntime) Ping(ctx context.Context) error {
	_, err := r.cli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker daemon not accessible: %w", err)
	}
	return nil
}

// CreateContainer creates a new Docker container.
func (r *DockerRuntime) CreateContainer(ctx context.Context, cfg Config) (string, error) {
	// Verify gVisor is still available if we're configured to use it
	if r.ociRuntime == "runsc" && !r.gvisorAvailable() {
		return "", fmt.Errorf("gVisor was available at startup but is no longer configured - did Docker daemon configuration change? %w", ErrGVisorNotAvailable)
	}

	// Pull image if not present
	if err := r.ensureImage(ctx, cfg.Image); err != nil {
		return "", err
	}

	// Convert mounts
	mounts := make([]mount.Mount, len(cfg.Mounts))
	for i, m := range cfg.Mounts {
		mounts[i] = mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		}
	}

	// Default to bridge network if not specified
	networkMode := container.NetworkMode(cfg.NetworkMode)
	if cfg.NetworkMode == "" {
		networkMode = "bridge"
	}

	// Build port bindings
	var exposedPorts nat.PortSet
	var portBindings nat.PortMap
	if len(cfg.PortBindings) > 0 {
		exposedPorts = make(nat.PortSet)
		portBindings = make(nat.PortMap)
		for containerPort, hostIP := range cfg.PortBindings {
			port := nat.Port(fmt.Sprintf("%d/tcp", containerPort))
			exposedPorts[port] = struct{}{}
			portBindings[port] = []nat.PortBinding{{
				HostIP:   hostIP,
				HostPort: "", // Let Docker assign random port
			}}
		}
	}

	// Only use TTY mode if os.Stdin is a real terminal.
	// Docker returns "the input device is not a TTY" when you try to use
	// TTY mode with non-TTY stdin (e.g., piped input, tests).
	useTTY := cfg.Interactive && term.IsTerminal(os.Stdin)

	// Configure DNS servers (defaults to Google DNS if not specified)
	dns := DefaultDNS(cfg.DNS)

	// Configure resource limits
	var memoryBytes int64
	if cfg.MemoryMB > 0 {
		memoryBytes = int64(cfg.MemoryMB) * 1024 * 1024
	}

	// CPU quota: CPUs * 100000 microseconds per 100ms period
	// Docker uses a period of 100000 microseconds (100ms)
	// If you want 2 CPUs, set quota to 200000 (2 * 100000)
	var cpuQuota int64
	var cpuPeriod int64
	if cfg.CPUs > 0 {
		cpuPeriod = 100000 // 100ms period
		cpuQuota = int64(cfg.CPUs) * cpuPeriod
	}

	resp, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image:        cfg.Image,
			Cmd:          cfg.Cmd,
			WorkingDir:   cfg.WorkingDir,
			Env:          cfg.Env,
			User:         cfg.User,
			Tty:          useTTY,
			OpenStdin:    cfg.Interactive,
			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			Runtime:      r.ociRuntime, // "runsc" or "runc" or ""
			Mounts:       mounts,
			NetworkMode:  networkMode,
			ExtraHosts:   cfg.ExtraHosts,
			PortBindings: portBindings,
			CapAdd:       cfg.CapAdd,
			GroupAdd:     cfg.GroupAdd,
			Privileged:   cfg.Privileged,
			DNS:          dns,
			Resources: container.Resources{
				Memory:    memoryBytes,
				CPUQuota:  cpuQuota,
				CPUPeriod: cpuPeriod,
			},
		},
		nil, // network config
		nil, // platform
		cfg.Name,
	)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}

	return resp.ID, nil
}

// StartContainer starts an existing container.
func (r *DockerRuntime) StartContainer(ctx context.Context, containerID string) error {
	if err := r.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}
	return nil
}

// GetPortBindings returns the actual host ports assigned to container ports.
func (r *DockerRuntime) GetPortBindings(ctx context.Context, containerID string) (map[int]int, error) {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	result := make(map[int]int)
	for port, bindings := range inspect.NetworkSettings.Ports {
		if len(bindings) == 0 {
			continue
		}
		containerPort := port.Int()
		hostPort, err := strconv.Atoi(bindings[0].HostPort)
		if err != nil {
			continue
		}
		result[containerPort] = hostPort
	}
	return result, nil
}

// StopContainer stops a running container.
func (r *DockerRuntime) StopContainer(ctx context.Context, containerID string) error {
	if err := r.cli.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}
	return nil
}

// WaitContainer blocks until the container exits.
func (r *DockerRuntime) WaitContainer(ctx context.Context, containerID string) (int64, error) {
	statusCh, errCh := r.cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		return -1, fmt.Errorf("waiting for container: %w", err)
	case status := <-statusCh:
		return status.StatusCode, nil
	}
}

// RemoveContainer removes a container.
func (r *DockerRuntime) RemoveContainer(ctx context.Context, containerID string) error {
	if err := r.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force: true,
	}); err != nil {
		// Ignore "not found" errors - container may have already been removed
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing container: %w", err)
	}
	return nil
}

// ContainerLogs returns the logs from a container (follows output).
func (r *DockerRuntime) ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	return r.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
}

// ContainerLogsAll returns all logs from a container (does not follow).
// The logs are demultiplexed from Docker's format (removes 8-byte headers).
func (r *DockerRuntime) ContainerLogsAll(ctx context.Context, containerID string) ([]byte, error) {
	reader, err := r.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
	})
	if err != nil {
		return nil, fmt.Errorf("getting container logs: %w", err)
	}
	defer reader.Close()

	// Check if container was created with TTY to determine if logs are multiplexed
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspecting container to determine log format: %w", err)
	}

	if inspect.Config.Tty {
		// TTY mode: logs are not multiplexed, read directly
		return io.ReadAll(reader)
	}

	// Non-TTY mode: demux Docker's multiplexed format
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, reader); err != nil {
		return nil, fmt.Errorf("demuxing logs: %w", err)
	}

	// Combine stdout and stderr
	// NOTE: Interleaved ordering between stdout/stderr is lost during demuxing.
	// Docker's multiplexed format preserves ordering within each stream but not across
	// streams. This is acceptable for logs.jsonl (audit/observability) where having all
	// content matters more than perfect ordering. TTY mode preserves ordering naturally.
	combined := append(stdout.Bytes(), stderr.Bytes()...)
	return combined, nil
}

// GetHostAddress returns the address for containers to reach the host.
func (r *DockerRuntime) GetHostAddress() string {
	if goruntime.GOOS == "linux" {
		// On Linux with host network mode, use localhost
		return "127.0.0.1"
	}
	// On macOS/Windows, Docker Desktop provides host.docker.internal
	return "host.docker.internal"
}

// SupportsHostNetwork returns true on Linux where host network mode is available.
func (r *DockerRuntime) SupportsHostNetwork() bool {
	return goruntime.GOOS == "linux"
}

// Close releases Docker client resources.
func (r *DockerRuntime) Close() error {
	return r.cli.Close()
}

// gvisorAvailable checks if gVisor (runsc) is available, using cached result if available.
// The cache prevents repeated Docker client creation and API calls during runtime initialization
// and container creation. Thread-safe via sync.Once.
//
// Note: The result is cached permanently for this runtime instance. If the Docker daemon
// is temporarily unreachable during the first check, gVisor will be cached as unavailable
// for the lifetime of this runtime. This is acceptable because runtime instances are
// typically short-lived (one per moat run).
func (r *DockerRuntime) gvisorAvailable() bool {
	r.gvisorOnce.Do(func() {
		// Use background context for the check since the result is cached permanently.
		// This avoids issues with canceled/expired contexts from concurrent callers.
		checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		info, err := r.cli.Info(checkCtx)
		if err != nil {
			log.Error("gVisor availability check failed - caching as unavailable", "error", err)
			r.gvisorAvail = false
			return
		}

		for name := range info.Runtimes {
			if name == "runsc" {
				r.gvisorAvail = true
				return
			}
		}
		r.gvisorAvail = false
	})
	return r.gvisorAvail
}

// SetupFirewall configures iptables to block all outbound traffic except to the proxy.
// The proxyHost parameter is accepted for interface consistency but not used in the
// iptables rules. This is intentional: host.docker.internal resolves to a dynamic IP
// that varies per Docker installation, and resolving it inside the container would
// add complexity. The security model relies on the proxy port being unique (randomly
// assigned per-run) rather than IP filtering. Combined with the proxy's authentication
// for Apple containers, this provides sufficient protection.
func (r *DockerRuntime) SetupFirewall(ctx context.Context, containerID string, proxyHost string, proxyPort int) error {
	// Validate port range
	if proxyPort < 1 || proxyPort > 65535 {
		return fmt.Errorf("invalid proxy port %d: must be between 1 and 65535", proxyPort)
	}

	// iptables rules:
	// 1. Allow loopback
	// 2. Allow established connections (for responses)
	// 3. Allow DNS (needed to resolve hostnames before proxy can intercept)
	// 4. Allow traffic to proxy port (any destination - see function comment)
	// 5. Drop everything else

	// We run these as a single script to minimize exec calls
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

	execConfig := container.ExecOptions{
		Cmd:          []string{"sh", "-c", script},
		AttachStdout: true,
		AttachStderr: true,
		User:         "root", // iptables requires root privileges
	}

	execID, err := r.cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return fmt.Errorf("creating exec for firewall setup: %w", err)
	}

	resp, err := r.cli.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("attaching to exec for firewall setup: %w", err)
	}
	defer resp.Close()

	// Read output to capture error messages
	var output bytes.Buffer
	_, _ = io.Copy(&output, resp.Reader)

	// Check exit code
	inspect, err := r.cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return fmt.Errorf("inspecting exec for firewall setup: %w", err)
	}

	if inspect.ExitCode != 0 {
		return fmt.Errorf("firewall setup failed with exit code %d: %s", inspect.ExitCode, output.String())
	}

	return nil
}

// ensureImage pulls an image if it doesn't exist locally.
func (r *DockerRuntime) ensureImage(ctx context.Context, imageName string) error {
	exists, err := r.buildMgr.ImageExists(ctx, imageName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	output.PullingImage(imageName)
	reader, err := r.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", imageName, err)
	}
	defer reader.Close()

	// Drain the reader to complete the pull (discard JSON progress output)
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

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

// ListContainers returns all moat containers.
// Filters to containers whose name matches an 8-char hex run ID pattern.
func (r *DockerRuntime) ListContainers(ctx context.Context) ([]Info, error) {
	containers, err := r.cli.ContainerList(ctx, container.ListOptions{
		All: true, // Include stopped containers
	})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	var result []Info
	for _, c := range containers {
		// Check if any name looks like a moat run ID (8 hex chars)
		for _, name := range c.Names {
			// Names have leading slash, e.g., "/a1b2c3d4"
			name = strings.TrimPrefix(name, "/")
			if isRunID(name) {
				result = append(result, Info{
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

// ContainerState returns the state of a container ("running", "exited", "created", etc).
// Returns an error if the container doesn't exist.
func (r *DockerRuntime) ContainerState(ctx context.Context, containerID string) (string, error) {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("inspecting container: %w", err)
	}
	return inspect.State.Status, nil
}

// Attach connects stdin/stdout/stderr to a running container.
func (r *DockerRuntime) Attach(ctx context.Context, containerID string, opts AttachOptions) error {
	resp, err := r.cli.ContainerAttach(ctx, containerID, container.AttachOptions{
		Stream: true,
		Stdin:  opts.Stdin != nil,
		Stdout: opts.Stdout != nil,
		Stderr: opts.Stderr != nil,
	})
	if err != nil {
		return fmt.Errorf("attaching to container: %w", err)
	}
	defer resp.Close()

	// Set up bidirectional copy
	outputDone := make(chan error, 1)
	stdinDone := make(chan error, 1)

	// Copy container output to stdout/stderr.
	// Since containers are always created with Tty: true (see CreateContainer),
	// output is always raw (not multiplexed). Use io.Copy unconditionally.
	go func() {
		_, err := io.Copy(opts.Stdout, resp.Reader)
		outputDone <- err
	}()

	// Copy stdin to container (if provided)
	if opts.Stdin != nil {
		go func() {
			_, err := io.Copy(resp.Conn, opts.Stdin)
			// Close write side when stdin ends
			if closeWriter, ok := resp.Conn.(interface{ CloseWrite() error }); ok {
				if closeErr := closeWriter.CloseWrite(); closeErr != nil && err == nil {
					err = closeErr
				}
			}
			stdinDone <- err
		}()
	}

	// Wait for context cancellation, stdin error (e.g., escape sequence), or output completion
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-stdinDone:
			// Stdin error - could be escape sequence or EOF
			if err != nil && err != io.EOF {
				return err
			}
			// Normal stdin EOF - continue waiting for output
		case err := <-outputDone:
			if err != nil && err != io.EOF {
				return err
			}
			return nil
		}
	}
}

// ResizeTTY resizes the container's TTY to the given dimensions.
func (r *DockerRuntime) ResizeTTY(ctx context.Context, containerID string, height, width uint) error {
	return r.cli.ContainerResize(ctx, containerID, container.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// StartAttached starts a container with stdin/stdout/stderr already attached.
// This is required for TUI applications that need the terminal connected
// before the process starts. The attach happens first, then start, ensuring
// the I/O streams are ready when the container's process begins.
func (r *DockerRuntime) StartAttached(ctx context.Context, containerID string, opts AttachOptions) error {
	// Attach first (before starting) - this is the key difference from Attach()
	resp, err := r.cli.ContainerAttach(ctx, containerID, container.AttachOptions{
		Stream: true,
		Stdin:  opts.Stdin != nil,
		Stdout: opts.Stdout != nil,
		Stderr: opts.Stderr != nil,
	})
	if err != nil {
		return fmt.Errorf("attaching to container: %w", err)
	}
	defer resp.Close()

	// Set connection deadline from context to ensure I/O doesn't hang.
	// This is particularly important for non-TTY mode where reads can stall.
	if deadline, ok := ctx.Deadline(); ok {
		if err := resp.Conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("setting connection deadline: %w", err)
		}
	}

	// Set up bidirectional copy BEFORE starting the container.
	// This ensures the goroutines are ready to receive output as soon as
	// the container starts, avoiding race conditions with fast-exiting containers.
	outputDone := make(chan error, 1)
	stdinDone := make(chan error, 1)

	// Copy container output to stdout/stderr
	go func() {
		if opts.TTY {
			// In TTY mode, output is raw (single stream)
			_, err := io.Copy(opts.Stdout, resp.Reader)
			outputDone <- err
		} else {
			// In non-TTY mode, Docker multiplexes stdout/stderr with headers.
			// Use stdcopy.StdCopy to demux the stream.
			_, err := stdcopy.StdCopy(opts.Stdout, opts.Stderr, resp.Reader)
			outputDone <- err
		}
	}()

	// Copy stdin to container (if provided)
	if opts.Stdin != nil {
		go func() {
			_, err := io.Copy(resp.Conn, opts.Stdin)
			// Close write side when stdin ends
			if closeWriter, ok := resp.Conn.(interface{ CloseWrite() error }); ok {
				if closeErr := closeWriter.CloseWrite(); closeErr != nil && err == nil {
					err = closeErr
				}
			}
			stdinDone <- err
		}()
	}

	// Start the container now that I/O streams are ready
	if err := r.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	// Resize TTY immediately if initial size was provided.
	// This ensures the container process sees the correct terminal dimensions
	// from the very start, before it has a chance to query and cache the size.
	if opts.TTY && opts.InitialWidth > 0 && opts.InitialHeight > 0 {
		if err := r.ResizeTTY(ctx, containerID, opts.InitialHeight, opts.InitialWidth); err != nil {
			// Log but don't fail - the container has started successfully
			// and a later resize from SIGWINCH will fix it
			_ = err // Intentionally ignored; resize is best-effort
		}
	}

	// Wait for context cancellation, stdin error (e.g., escape sequence), or output completion
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-stdinDone:
			// Stdin error - could be escape sequence or EOF
			if err != nil && err != io.EOF {
				return err
			}
			// Normal stdin EOF - continue waiting for output
		case err := <-outputDone:
			if err != nil && err != io.EOF {
				return err
			}
			return nil
		}
	}
}

// dockerNetworkManager methods

// CreateNetwork creates a Docker network for inter-container communication.
// Returns the network ID.
func (m *dockerNetworkManager) CreateNetwork(ctx context.Context, name string) (string, error) {
	resp, err := m.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge", // Bridge network for inter-container communication
	})
	if err != nil {
		return "", fmt.Errorf("creating network: %w", err)
	}
	return resp.ID, nil
}

// RemoveNetwork removes a Docker network by ID.
// Best-effort: does not fail if network doesn't exist or has active endpoints.
func (m *dockerNetworkManager) RemoveNetwork(ctx context.Context, networkID string) error {
	err := m.cli.NetworkRemove(ctx, networkID)
	if err != nil {
		// Ignore "not found" and "conflict" errors - network may already be
		// removed or may have active endpoints during cleanup
		if errdefs.IsNotFound(err) || errdefs.IsConflict(err) {
			return nil
		}
		// Docker doesn't always return a proper conflict error code for active endpoints.
		// Check the error message as a fallback.
		if strings.Contains(err.Error(), "active endpoints") {
			return nil
		}
		return fmt.Errorf("removing network: %w", err)
	}
	return nil
}

// dockerSidecarManager methods

// StartSidecar starts a sidecar container (pull, create, start).
// The container is attached to the specified network and assigned a hostname.
// Returns the container ID.
func (m *dockerSidecarManager) StartSidecar(ctx context.Context, cfg SidecarConfig) (string, error) {
	// Validate input
	if cfg.Image == "" {
		return "", fmt.Errorf("sidecar image cannot be empty")
	}
	if cfg.NetworkID == "" {
		return "", fmt.Errorf("sidecar network ID cannot be empty")
	}
	if cfg.Name == "" {
		return "", fmt.Errorf("sidecar name cannot be empty")
	}

	// Pull image if not present
	if err := m.ensureImage(ctx, cfg.Image); err != nil {
		return "", fmt.Errorf("pulling sidecar image: %w", err)
	}

	// Prepare mounts
	mounts := make([]mount.Mount, 0, len(cfg.Mounts))
	for _, mt := range cfg.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   mt.Source,
			Target:   mt.Target,
			ReadOnly: mt.ReadOnly,
		})
	}

	// Create container with labels for orphan cleanup
	labels := make(map[string]string)
	if cfg.RunID != "" {
		labels["moat.run-id"] = cfg.RunID
		labels["moat.role"] = "buildkit-sidecar" // default, can be overridden
	}
	for k, v := range cfg.Labels {
		labels[k] = v
	}

	resp, err := m.cli.ContainerCreate(ctx,
		&container.Config{
			Image:    cfg.Image,
			Cmd:      cfg.Cmd,
			Hostname: cfg.Hostname,
			Labels:   labels,
			Env:      cfg.Env,
		},
		&container.HostConfig{
			Runtime:     m.ociRuntime, // Use same OCI runtime as main container
			NetworkMode: container.NetworkMode(cfg.NetworkID),
			Privileged:  cfg.Privileged,
			Mounts:      mounts,
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				cfg.NetworkID: {
					Aliases: []string{cfg.Hostname},
				},
			},
		},
		nil, // platform
		cfg.Name,
	)
	if err != nil {
		return "", fmt.Errorf("creating sidecar container: %w", err)
	}

	// Start container
	if err := m.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up on failure
		_ = m.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("starting sidecar container: %w", err)
	}

	return resp.ID, nil
}

// InspectContainer returns container inspection data.
func (m *dockerSidecarManager) InspectContainer(ctx context.Context, containerID string) (InspectResponse, error) {
	inspect, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return InspectResponse{}, fmt.Errorf("inspecting container: %w", err)
	}
	// Convert Docker's inspect response to our common type
	return InspectResponse{
		State: &State{
			Running: inspect.State.Running,
		},
	}, nil
}

// ensureImage pulls an image if it doesn't exist locally.
func (m *dockerSidecarManager) ensureImage(ctx context.Context, imageName string) error {
	exists, err := m.imageExists(ctx, imageName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	output.PullingImage(imageName)
	reader, err := m.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", imageName, err)
	}
	defer reader.Close()

	// Drain the reader to complete the pull (discard JSON progress output)
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

// imageExists checks if an image exists locally.
func (m *dockerSidecarManager) imageExists(ctx context.Context, tag string) (bool, error) {
	_, err := m.cli.ImageInspect(ctx, tag)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspecting image %s: %w", tag, err)
	}
	return true, nil
}

// dockerBuildManager methods

// ImageExists checks if an image exists locally.
func (m *dockerBuildManager) ImageExists(ctx context.Context, tag string) (bool, error) {
	_, err := m.cli.ImageInspect(ctx, tag)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspecting image %s: %w", tag, err)
	}
	return true, nil
}

// BuildImage builds a Docker image from Dockerfile content.
// Routes to BuildKit client if BUILDKIT_HOST is set, otherwise uses Docker SDK.
// Note: opts.DNS is ignored for Docker builds; Docker uses daemon-level DNS configuration.
func (m *dockerBuildManager) BuildImage(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error {
	// Use BuildKit client if BUILDKIT_HOST is set (docker:dind mode with BuildKit sidecar)
	if buildkitHost := os.Getenv("BUILDKIT_HOST"); buildkitHost != "" {
		log.Debug("using buildkit client for image build", "buildkit_host", buildkitHost, "tag", tag)
		return m.buildImageWithBuildKit(ctx, dockerfile, tag, opts)
	}
	// Otherwise use Docker SDK (will use BuildKit by default, unless MOAT_DISABLE_BUILDKIT=1)
	log.Debug("using docker sdk for image build", "tag", tag, "no_cache", opts.NoCache)
	return m.buildImageWithDockerSDK(ctx, dockerfile, tag, opts)
}

// buildImageWithBuildKit builds an image using the BuildKit client.
func (m *dockerBuildManager) buildImageWithBuildKit(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error {
	// Create temp directory for build context
	tmpDir, err := os.MkdirTemp("", "moat-build-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write Dockerfile to temp directory
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if writeErr := os.WriteFile(dockerfilePath, []byte(dockerfile), 0644); writeErr != nil {
		return fmt.Errorf("writing Dockerfile: %w", writeErr)
	}

	// Write additional context files
	for name, content := range opts.ContextFiles {
		path := filepath.Join(tmpDir, name)
		if dir := filepath.Dir(path); dir != tmpDir {
			if mkdirErr := os.MkdirAll(dir, 0755); mkdirErr != nil {
				return fmt.Errorf("creating context dir for %s: %w", name, mkdirErr)
			}
		}
		if writeErr := os.WriteFile(path, content, 0644); writeErr != nil {
			return fmt.Errorf("writing context file %s: %w", name, writeErr)
		}
	}

	// Determine platform based on host architecture
	platform := "linux/amd64"
	if goruntime.GOARCH == "arm64" {
		platform = "linux/arm64"
	}

	// Create BuildKit client
	bkClient, err := buildkit.NewClient()
	if err != nil {
		return fmt.Errorf("creating buildkit client: %w", err)
	}

	// Build the image
	return bkClient.Build(ctx, buildkit.BuildOptions{
		Tag:        tag,
		ContextDir: tmpDir,
		NoCache:    opts.NoCache,
		Platform:   platform,
		BuildArgs:  map[string]string{}, // No build args exposed in container.BuildOptions yet
	})
}

// buildImageWithDockerSDK builds an image using the Docker SDK.
func (m *dockerBuildManager) buildImageWithDockerSDK(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error {
	// Determine platform based on host architecture
	platform := "linux/amd64"
	if goruntime.GOARCH == "arm64" {
		platform = "linux/arm64"
	}

	// Use BuildKit by default for faster builds, but allow opting out via MOAT_DISABLE_BUILDKIT=1
	// for environments where Docker SDK's BuildKit integration has issues (e.g., some Docker Desktop configs).
	builderVersion := build.BuilderBuildKit
	if os.Getenv("MOAT_DISABLE_BUILDKIT") == "1" {
		builderVersion = build.BuilderV1
	}

	// Try building with the selected builder
	err := m.buildImageWithBuilder(ctx, dockerfile, tag, platform, builderVersion, opts)

	// If BuildKit fails, provide helpful error message with clear resolution steps.
	// The error will occur quickly (usually within seconds) when BuildKit isn't available.
	if err != nil && builderVersion == build.BuilderBuildKit {
		errMsg := err.Error()

		// "no active sessions": BuildKit has stale sessions (intermittent issue)
		if strings.Contains(errMsg, "no active sessions") {
			return fmt.Errorf(`BuildKit has stale build sessions.

This is usually caused by interrupted builds or Docker daemon state issues.

Quick fix: Clean BuildKit cache
  docker builder prune -f

Or: Restart Docker Desktop

Alternative: Use legacy builder (slower, no layer caching):
  export MOAT_DISABLE_BUILDKIT=1
  moat run ...

Original error: %w`, err)
		}

		// "mount option requires BuildKit": Dockerfile has BuildKit syntax but builder doesn't support it
		if strings.Contains(errMsg, "mount option requires BuildKit") {
			return fmt.Errorf(`BuildKit not available.

Recommended: Install Docker buildx (included in Docker Desktop 20.10+)
  https://docs.docker.com/buildx/working-with-buildx/

Alternative: Use legacy builder (slower, no layer caching):
  export MOAT_DISABLE_BUILDKIT=1
  moat run ...

Original error: %w`, err)
		}
	}

	return err
}

// buildImageWithBuilder performs the actual image build with the specified builder.
func (m *dockerBuildManager) buildImageWithBuilder(ctx context.Context, dockerfile string, tag string, platform string, builderVersion build.BuilderVersion, opts BuildOptions) error {
	// Create a tar archive with the Dockerfile
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Add Dockerfile to tar
	header := &tar.Header{
		Name: "Dockerfile",
		Mode: 0644,
		Size: int64(len(dockerfile)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}
	if _, err := tw.Write([]byte(dockerfile)); err != nil {
		return fmt.Errorf("writing Dockerfile to tar: %w", err)
	}

	// Add context files to tar
	for name, content := range opts.ContextFiles {
		h := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(h); err != nil {
			return fmt.Errorf("writing tar header for %s: %w", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			return fmt.Errorf("writing %s to tar: %w", name, err)
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}

	output.BuildingImage(tag)

	// Build the image
	resp, err := m.cli.ImageBuild(ctx, &buf, build.ImageBuildOptions{
		Tags:       []string{tag},
		Dockerfile: "Dockerfile",
		Remove:     true,
		Platform:   platform,
		Version:    builderVersion,
		NoCache:    opts.NoCache,
	})
	if err != nil {
		return fmt.Errorf("building image: %w", err)
	}
	defer resp.Body.Close()

	// Stream build output and check for errors
	decoder := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("reading build output: %w", err)
		}
		if msg.Error != "" {
			return fmt.Errorf("build error: %s", msg.Error)
		}
		if msg.Stream != "" {
			fmt.Print(msg.Stream)
		}
	}

	return nil
}

// GetImageHomeDir returns the home directory configured in an image.
// It inspects the image config for the USER directive and determines the home directory.
// Returns "/root" if detection fails or the user is root.
func (m *dockerBuildManager) GetImageHomeDir(ctx context.Context, imageName string) string {
	// Default to /root
	const defaultHome = "/root"

	// Ensure image is available first
	if err := m.ensureImage(ctx, imageName); err != nil {
		return defaultHome
	}

	inspect, err := m.cli.ImageInspect(ctx, imageName)
	if err != nil {
		return defaultHome
	}

	// Check for explicit HOME in environment
	for _, env := range inspect.Config.Env {
		if strings.HasPrefix(env, "HOME=") {
			return strings.TrimPrefix(env, "HOME=")
		}
	}

	// Check the USER directive - if non-root, derive home from it
	user := inspect.Config.User
	if user == "" || user == "root" || user == "0" {
		return defaultHome
	}

	// For non-root users, common convention is /home/<user>
	// Strip any UID:GID format (e.g., "1000:1000" or "node:node")
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
func (m *dockerBuildManager) ensureImage(ctx context.Context, imageName string) error {
	exists, err := m.ImageExists(ctx, imageName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	output.PullingImage(imageName)
	reader, err := m.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", imageName, err)
	}
	defer reader.Close()

	// Drain the reader to complete the pull (discard JSON progress output)
	_, _ = io.Copy(io.Discard, reader)
	return nil
}
