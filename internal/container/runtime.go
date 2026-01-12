// Package container provides an abstraction over container runtimes.
// It supports Docker and Apple's container tool, with automatic detection.
package container

import (
	"context"
	"io"
)

// RuntimeType identifies the container runtime being used.
type RuntimeType string

const (
	RuntimeDocker RuntimeType = "docker"
	RuntimeApple  RuntimeType = "apple"
)

// Runtime is the interface for container runtime operations.
type Runtime interface {
	// Type returns the runtime type (Docker or Apple).
	Type() RuntimeType

	// Ping verifies the runtime is accessible.
	Ping(ctx context.Context) error

	// CreateContainer creates a new container without starting it.
	// Returns the container ID.
	CreateContainer(ctx context.Context, cfg Config) (string, error)

	// StartContainer starts an existing container.
	StartContainer(ctx context.Context, id string) error

	// StopContainer stops a running container.
	StopContainer(ctx context.Context, id string) error

	// WaitContainer blocks until the container exits and returns the exit code.
	WaitContainer(ctx context.Context, id string) (int64, error)

	// RemoveContainer removes a container.
	RemoveContainer(ctx context.Context, id string) error

	// ContainerLogs returns a reader for the container's logs (follows output).
	ContainerLogs(ctx context.Context, id string) (io.ReadCloser, error)

	// ContainerLogsAll returns all logs from a container (does not follow).
	// Use this after the container has exited to ensure all logs are captured.
	ContainerLogsAll(ctx context.Context, id string) ([]byte, error)

	// GetHostAddress returns the address containers use to reach the host.
	// For Docker on Linux, this is "127.0.0.1" (with host network mode).
	// For Docker on macOS/Windows, this is "host.docker.internal".
	// For Apple container, this is the gateway IP (e.g., "192.168.64.1").
	GetHostAddress() string

	// SupportsHostNetwork returns true if the runtime supports host network mode.
	// Docker on Linux supports this; Apple container does not.
	SupportsHostNetwork() bool

	// Close releases runtime resources.
	Close() error
}

// Config holds configuration for creating a container.
type Config struct {
	Name        string
	Image       string
	Cmd         []string
	WorkingDir  string
	Env         []string
	Mounts      []MountConfig
	ExtraHosts  []string // host:ip mappings (Docker-specific)
	NetworkMode string   // "bridge", "host", "none" (Docker-specific)
}

// MountConfig describes a volume mount.
type MountConfig struct {
	Source   string
	Target   string
	ReadOnly bool
}
