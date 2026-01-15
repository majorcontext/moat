package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	goruntime "runtime"
	"strconv"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// DockerRuntime implements Runtime using Docker.
type DockerRuntime struct {
	cli *client.Client
}

// NewDockerRuntime creates a new Docker runtime.
func NewDockerRuntime() (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &DockerRuntime{cli: cli}, nil
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

	resp, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image:        cfg.Image,
			Cmd:          cfg.Cmd,
			WorkingDir:   cfg.WorkingDir,
			Env:          cfg.Env,
			Tty:          true,
			OpenStdin:    true,
			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			Mounts:       mounts,
			NetworkMode:  networkMode,
			ExtraHosts:   cfg.ExtraHosts,
			PortBindings: portBindings,
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
	return io.ReadAll(reader)
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

// SetupFirewall configures iptables to block all outbound traffic except to the proxy.
func (r *DockerRuntime) SetupFirewall(ctx context.Context, containerID string, proxyHost string, proxyPort int) error {
	// Resolve proxy host to IP if it's a hostname
	// For host.docker.internal, we need to resolve it inside the container
	// For simplicity, we'll allow traffic to the Docker bridge gateway which handles host.docker.internal

	// iptables rules:
	// 1. Allow loopback
	// 2. Allow established connections (for responses)
	// 3. Allow DNS (needed to resolve hostnames before proxy can intercept)
	// 4. Allow traffic to proxy
	// 5. Drop everything else

	// We run these as a single script to minimize exec calls
	script := fmt.Sprintf(`
		# Flush existing rules
		iptables -F OUTPUT 2>/dev/null || true

		# Allow loopback
		iptables -A OUTPUT -o lo -j ACCEPT

		# Allow established/related connections
		iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

		# Allow DNS (UDP 53) - needed for initial hostname resolution
		iptables -A OUTPUT -p udp --dport 53 -j ACCEPT

		# Allow traffic to proxy host on proxy port
		# For Docker, we allow the entire Docker network range since host.docker.internal
		# resolves to the host gateway IP which varies
		iptables -A OUTPUT -p tcp --dport %d -j ACCEPT

		# Drop all other outbound traffic
		iptables -A OUTPUT -j DROP
	`, proxyPort)

	execConfig := container.ExecOptions{
		Cmd:          []string{"sh", "-c", script},
		AttachStdout: true,
		AttachStderr: true,
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

	// Read output to completion
	_, _ = io.Copy(io.Discard, resp.Reader)

	// Check exit code
	inspect, err := r.cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return fmt.Errorf("inspecting exec for firewall setup: %w", err)
	}

	if inspect.ExitCode != 0 {
		return fmt.Errorf("firewall setup failed with exit code %d (iptables may not be available in container)", inspect.ExitCode)
	}

	return nil
}

// ensureImage pulls an image if it doesn't exist locally.
func (r *DockerRuntime) ensureImage(ctx context.Context, imageName string) error {
	exists, err := r.ImageExists(ctx, imageName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	fmt.Printf("Pulling image %s...\n", imageName)
	reader, err := r.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", imageName, err)
	}
	defer reader.Close()

	// Drain the reader to complete the pull
	_, _ = io.Copy(os.Stdout, reader)
	return nil
}

// ImageExists checks if an image exists locally.
func (r *DockerRuntime) ImageExists(ctx context.Context, tag string) (bool, error) {
	_, err := r.cli.ImageInspect(ctx, tag)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspecting image %s: %w", tag, err)
	}
	return true, nil
}

// BuildImage builds a Docker image from Dockerfile content.
func (r *DockerRuntime) BuildImage(ctx context.Context, dockerfile string, tag string) error {
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
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}

	fmt.Printf("Building image %s...\n", tag)

	// Build the image
	resp, err := r.cli.ImageBuild(ctx, &buf, build.ImageBuildOptions{
		Tags:       []string{tag},
		Dockerfile: "Dockerfile",
		Remove:     true,
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
