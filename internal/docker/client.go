package docker

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

// Client wraps the Docker client with AgentOps-specific operations.
type Client struct {
	cli *client.Client
}

// NewClient creates a new Docker client.
func NewClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &Client{cli: cli}, nil
}

// Close releases Docker client resources.
func (c *Client) Close() error {
	return c.cli.Close()
}

// Ping verifies the Docker daemon is accessible.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker daemon not accessible: %w", err)
	}
	return nil
}

// ContainerConfig holds configuration for creating a container.
type ContainerConfig struct {
	Name       string
	Image      string
	Cmd        []string
	WorkingDir string
	Env        []string
	Mounts     []MountConfig
}

// MountConfig describes a volume mount.
type MountConfig struct {
	Source   string
	Target   string
	ReadOnly bool
}

// CreateContainer creates a new container without starting it.
func (c *Client) CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error) {
	// Pull image if not present
	if err := c.ensureImage(ctx, cfg.Image); err != nil {
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

	resp, err := c.cli.ContainerCreate(ctx,
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
			NetworkMode: "bridge",
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
func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}
	return nil
}

// StopContainer stops a running container.
func (c *Client) StopContainer(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}
	return nil
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force: true,
	}); err != nil {
		return fmt.Errorf("removing container: %w", err)
	}
	return nil
}

// WaitContainer blocks until the container exits.
func (c *Client) WaitContainer(ctx context.Context, containerID string) (int64, error) {
	statusCh, errCh := c.cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		return -1, fmt.Errorf("waiting for container: %w", err)
	case status := <-statusCh:
		return status.StatusCode, nil
	}
}

// ContainerLogs returns the logs from a container.
func (c *Client) ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	return c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
}

// ensureImage pulls an image if it doesn't exist locally.
func (c *Client) ensureImage(ctx context.Context, imageName string) error {
	_, _, err := c.cli.ImageInspectWithRaw(ctx, imageName)
	if err == nil {
		return nil // Image exists
	}

	fmt.Printf("Pulling image %s...\n", imageName)
	reader, err := c.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", imageName, err)
	}
	defer reader.Close()

	// Drain the reader to complete the pull
	io.Copy(os.Stdout, reader)
	return nil
}
