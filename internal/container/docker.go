package container

import (
	"context"
	"fmt"
	"io"
	"os"
	goruntime "runtime"
	"strconv"

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

// ensureImage pulls an image if it doesn't exist locally.
func (r *DockerRuntime) ensureImage(ctx context.Context, imageName string) error {
	_, err := r.cli.ImageInspect(ctx, imageName)
	if err == nil {
		return nil // Image exists
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
