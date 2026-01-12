package container

import (
	"context"
	"fmt"
	"io"
	"os"
	goruntime "runtime"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
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
func (r *DockerRuntime) CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error) {
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

	resp, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image:      cfg.Image,
			Cmd:        cfg.Cmd,
			WorkingDir: cfg.WorkingDir,
			Env:        cfg.Env,
			Tty:        true,
			OpenStdin:  true,
		},
		&container.HostConfig{
			Mounts:      mounts,
			NetworkMode: networkMode,
			ExtraHosts:  cfg.ExtraHosts,
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

// ContainerLogs returns the logs from a container.
func (r *DockerRuntime) ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	return r.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
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
