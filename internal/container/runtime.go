// Package container provides an abstraction over container runtimes.
// It supports Docker and Apple's container tool, with automatic detection.
package container

import (
	"context"
	"io"
	"time"
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

	// GetPortBindings returns the actual host ports mapped to container ports.
	// Call after container is started. Returns map[containerPort]hostPort.
	GetPortBindings(ctx context.Context, id string) (map[int]int, error)

	// GetHostAddress returns the address containers use to reach the host.
	// For Docker on Linux, this is "127.0.0.1" (with host network mode).
	// For Docker on macOS/Windows, this is "host.docker.internal".
	// For Apple container, this is the gateway IP (e.g., "192.168.64.1").
	GetHostAddress() string

	// SupportsHostNetwork returns true if the runtime supports host network mode.
	// Docker on Linux supports this; Apple container does not.
	SupportsHostNetwork() bool

	// BuildImage builds an image from a Dockerfile content.
	// Returns the image ID. The tag is applied to the built image.
	BuildImage(ctx context.Context, dockerfile string, tag string, opts BuildOptions) error

	// ImageExists checks if an image with the given tag exists locally.
	ImageExists(ctx context.Context, tag string) (bool, error)

	// NetworkManager returns the network manager if supported, nil otherwise.
	// Docker provides this, Apple containers return nil.
	NetworkManager() NetworkManager

	// SidecarManager returns the sidecar manager if supported, nil otherwise.
	// Docker provides this, Apple containers return nil.
	SidecarManager() SidecarManager

	// Close releases runtime resources.
	Close() error

	// SetupFirewall configures iptables to only allow traffic to the proxy.
	// proxyHost is the address the container uses to reach the proxy (e.g., "host.docker.internal").
	// proxyPort is the proxy's port number.
	// This blocks all other outbound traffic, forcing everything through the proxy.
	SetupFirewall(ctx context.Context, id string, proxyHost string, proxyPort int) error

	// ListImages returns all moat-managed images.
	ListImages(ctx context.Context) ([]ImageInfo, error)

	// ListContainers returns all moat containers (running + stopped).
	ListContainers(ctx context.Context) ([]Info, error)

	// ContainerState returns the state of a container ("running", "exited", "created", etc).
	// Returns an error if the container doesn't exist.
	ContainerState(ctx context.Context, id string) (string, error)

	// RemoveImage removes an image by ID or tag.
	RemoveImage(ctx context.Context, id string) error

	// GetImageHomeDir returns the home directory configured in an image.
	// Returns "/root" if detection fails or no home is configured.
	GetImageHomeDir(ctx context.Context, imageName string) string

	// Attach connects stdin/stdout/stderr to a running container.
	// Returns when the attachment ends (container exits or context canceled).
	Attach(ctx context.Context, id string, opts AttachOptions) error

	// StartAttached starts a container with stdin/stdout/stderr already attached.
	// This is required for TUI applications that need the terminal connected
	// before the process starts (e.g., to read cursor position).
	// The attachment runs until the container exits or context is canceled.
	StartAttached(ctx context.Context, id string, opts AttachOptions) error

	// ResizeTTY resizes the container's TTY to the given dimensions.
	ResizeTTY(ctx context.Context, id string, height, width uint) error

	// CreateNetwork creates a Docker network for inter-container communication.
	// Returns the network ID.
	CreateNetwork(ctx context.Context, name string) (string, error)

	// RemoveNetwork removes a Docker network by ID.
	// Best-effort: does not fail if network doesn't exist or has active endpoints.
	RemoveNetwork(ctx context.Context, networkID string) error

	// StartSidecar starts a sidecar container (pull, create, start).
	// The container is attached to the specified network and assigned a hostname.
	// Returns the container ID.
	StartSidecar(ctx context.Context, cfg SidecarConfig) (string, error)
}

// NetworkManager handles Docker network operations.
// Returned by Runtime.NetworkManager() - nil if not supported.
type NetworkManager interface {
	// CreateNetwork creates a network for inter-container communication.
	// Returns the network ID.
	CreateNetwork(ctx context.Context, name string) (string, error)

	// RemoveNetwork removes a network by ID.
	// Best-effort: does not fail if network doesn't exist.
	RemoveNetwork(ctx context.Context, networkID string) error
}

// SidecarManager handles sidecar container operations.
// Returned by Runtime.SidecarManager() - nil if not supported.
type SidecarManager interface {
	// StartSidecar starts a sidecar container (pull, create, start).
	// The container is attached to the specified network and assigned a hostname.
	// Returns the container ID.
	StartSidecar(ctx context.Context, cfg SidecarConfig) (string, error)

	// InspectContainer returns detailed container information.
	// Useful for checking sidecar state (running, health, etc).
	InspectContainer(ctx context.Context, containerID string) (ContainerInspectResponse, error)
}

// ContainerInspectResponse holds detailed container state.
type ContainerInspectResponse struct {
	State *ContainerState
}

// ContainerState holds container execution state.
type ContainerState struct {
	Running bool
}

// AttachOptions configures container attachment.
type AttachOptions struct {
	Stdin  io.Reader // If non-nil, forward input to container
	Stdout io.Writer // If non-nil, receive stdout from container
	Stderr io.Writer // If non-nil, receive stderr from container (may be same as Stdout)
	TTY    bool      // If true, use TTY mode (raw terminal)

	// InitialWidth and InitialHeight set the initial terminal size for TTY mode.
	// If both are > 0, the TTY is resized immediately after the container starts,
	// before the process has a chance to query terminal dimensions.
	InitialWidth  uint
	InitialHeight uint
}

// Config holds configuration for creating a container.
type Config struct {
	Name         string
	Image        string
	Cmd          []string
	WorkingDir   string
	Env          []string
	User         string // User to run as (e.g., "1000:1000" or "moatuser")
	Mounts       []MountConfig
	ExtraHosts   []string       // host:ip mappings (Docker-specific)
	NetworkMode  string         // "bridge", "host", "none" (Docker-specific)
	PortBindings map[int]string // container port -> host bind address (e.g., 3000 -> "127.0.0.1")
	CapAdd       []string       // Linux capabilities to add (e.g., "NET_ADMIN")
	GroupAdd     []string       // Supplementary group IDs for the container process (e.g., "999" for docker group)
	Privileged   bool           // If true, run container in privileged mode (required for Docker-in-Docker)
	Interactive  bool           // If true, container will be attached interactively (Apple runtime: uses exec workaround; Docker: handled natively)
	HasMoatUser  bool           // If true, image has moatuser (moat-built images); used for exec --user in Apple containers
}

// SidecarConfig holds configuration for starting a sidecar container.
type SidecarConfig struct {
	// Image is the container image to use (e.g., "moby/buildkit:latest")
	Image string

	// Name is the container name
	Name string

	// Hostname is the network hostname for the container
	Hostname string

	// NetworkID is the Docker network to attach to
	NetworkID string

	// Cmd is the command to run
	Cmd []string

	// Privileged indicates if the sidecar needs privileged mode
	Privileged bool

	// Mounts are volume mounts for the sidecar
	Mounts []MountConfig
}

// MountConfig describes a volume mount.
type MountConfig struct {
	Source   string
	Target   string
	ReadOnly bool
}

// ImageInfo contains information about a container image.
type ImageInfo struct {
	ID      string
	Tag     string
	Size    int64
	Created time.Time
}

// Info contains information about a container.
type Info struct {
	ID      string
	Name    string
	Image   string
	Status  string // "running", "exited", "created"
	Created time.Time
}

// BuildOptions configures image building.
type BuildOptions struct {
	// DNS servers to use during build (Apple containers only).
	// If empty, host DNS is auto-detected.
	DNS []string

	// NoCache disables build cache, forcing a fresh build of all layers.
	NoCache bool
}
