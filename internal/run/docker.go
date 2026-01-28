package run

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"

	"github.com/andybons/moat/internal/container"
	"github.com/andybons/moat/internal/deps"
	"github.com/andybons/moat/internal/log"
)

// DockerSocketPath is the standard path for the Docker socket on Linux.
const DockerSocketPath = "/var/run/docker.sock"

// DockerDependencyConfig holds the configuration needed to enable docker
// inside a container.
type DockerDependencyConfig struct {
	// Mode is the docker access mode: "host" (socket mount) or "dind" (privileged).
	Mode deps.DockerMode

	// SocketMount is the mount configuration for the Docker socket.
	// Only set for host mode.
	SocketMount container.MountConfig

	// GroupID is the GID of the docker socket, as a string for GroupAdd.
	// Only set for host mode. Example: "999"
	GroupID string

	// Privileged indicates the container needs privileged mode.
	// Only set for dind mode.
	Privileged bool
}

// ErrDockerHostRequiresDockerRuntime is returned when docker:host mode is used
// with Apple containers runtime, which cannot access the host Docker socket.
type ErrDockerHostRequiresDockerRuntime struct{}

func (e ErrDockerHostRequiresDockerRuntime) Error() string {
	return `'docker:host' dependency requires Docker runtime

Apple containers cannot access the host Docker socket.
Either:
  - Use 'docker:dind' mode (runs isolated Docker daemon), or
  - Use Docker runtime: moat run --runtime docker`
}

// ErrDockerRequiresDockerRuntime is an alias for backward compatibility.
// Deprecated: Use ErrDockerHostRequiresDockerRuntime instead.
type ErrDockerRequiresDockerRuntime = ErrDockerHostRequiresDockerRuntime

// ErrDockerDindRequiresDockerRuntime is returned when docker:dind mode is used
// with Apple containers runtime, which does not support privileged mode.
type ErrDockerDindRequiresDockerRuntime struct{}

func (e ErrDockerDindRequiresDockerRuntime) Error() string {
	return `'docker:dind' dependency requires Docker runtime

Apple containers do not support privileged mode, which is required for
Docker-in-Docker to start its own dockerd daemon.
Use Docker runtime: moat run --runtime docker`
}

// HasDockerDependency checks if the dependency list includes the docker dependency.
// Returns true if docker dependency is present, false otherwise.
func HasDockerDependency(depList []deps.Dependency) bool {
	return GetDockerDependency(depList) != nil
}

// GetDockerDependency returns the docker dependency from the list, or nil if not present.
// This allows callers to access the DockerMode field.
func GetDockerDependency(depList []deps.Dependency) *deps.Dependency {
	for i := range depList {
		if depList[i].Name == "docker" {
			return &depList[i]
		}
	}
	return nil
}

// ValidateDockerDependency checks if the docker dependency can be used with
// the given runtime and mode.
//
// Both docker modes require Docker runtime:
// - Host mode needs socket access (Apple containers cannot mount host socket)
// - Dind mode needs privileged mode (Apple containers don't support this)
func ValidateDockerDependency(runtimeType container.RuntimeType, mode deps.DockerMode) error {
	if runtimeType == container.RuntimeApple {
		if mode == deps.DockerModeDind {
			return ErrDockerDindRequiresDockerRuntime{}
		}
		return ErrDockerHostRequiresDockerRuntime{}
	}
	return nil
}

// GetDockerSocketGID returns the GID of the Docker socket.
// This is needed to add the container user to the docker group so they can
// access the socket.
//
// Returns an error if the socket doesn't exist or cannot be stat'd.
func GetDockerSocketGID() (uint32, error) {
	info, err := os.Stat(DockerSocketPath)
	if err != nil {
		return 0, fmt.Errorf("docker socket not found at %s: %w", DockerSocketPath, err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("failed to get docker socket stats (unsupported platform)")
	}

	gid := stat.Gid

	// Validate GID is reasonable on Linux
	if runtime.GOOS == "linux" && gid == 0 {
		log.Warn("docker socket owned by root group (gid 0) - this is unusual and may indicate permission issues")
	}

	// Check if socket is accessible by checking file permissions
	// For Unix sockets, check read+write permission bits
	mode := info.Mode()
	if mode&0060 != 0060 { // Check group read+write
		log.Warn("docker socket has unexpected group permissions",
			"mode", mode.String(),
			"gid", gid)
	}

	return gid, nil
}

// ResolveDockerDependency validates the docker dependency against the runtime
// and returns the configuration needed to enable docker in the container.
//
// For host mode:
// 1. Validates that the runtime supports socket access (not Apple containers)
// 2. Gets the GID of the docker socket for group permissions
// 3. Returns the mount config and group ID
//
// For dind mode:
// 1. Returns config indicating privileged mode is needed
// 2. No socket mount or GID needed (runs its own daemon)
//
// Returns nil if docker dependency is not present in depList.
// Returns an error if validation fails or the socket cannot be accessed.
func ResolveDockerDependency(depList []deps.Dependency, runtimeType container.RuntimeType) (*DockerDependencyConfig, error) {
	dockerDep := GetDockerDependency(depList)
	if dockerDep == nil {
		return nil, nil
	}

	mode := dockerDep.DockerMode
	if mode == "" {
		return nil, fmt.Errorf("docker dependency requires explicit mode: use 'docker:host' or 'docker:dind'")
	}

	// Validate runtime supports this docker mode
	if err := ValidateDockerDependency(runtimeType, mode); err != nil {
		return nil, err
	}

	// Handle dind mode - privileged container with its own Docker daemon
	if mode == deps.DockerModeDind {
		log.Debug("resolved docker dependency",
			"mode", "dind",
			"privileged", true)

		return &DockerDependencyConfig{
			Mode:       deps.DockerModeDind,
			Privileged: true,
		}, nil
	}

	// Handle host mode - mount the Docker socket
	gid, err := GetDockerSocketGID()
	if err != nil {
		return nil, err
	}

	log.Debug("resolved docker dependency",
		"mode", "host",
		"socket", DockerSocketPath,
		"gid", gid)

	return &DockerDependencyConfig{
		Mode: deps.DockerModeHost,
		SocketMount: container.MountConfig{
			Source:   DockerSocketPath,
			Target:   DockerSocketPath,
			ReadOnly: false,
		},
		GroupID: strconv.FormatUint(uint64(gid), 10),
	}, nil
}

// BuildKitConfig holds configuration for BuildKit sidecar.
type BuildKitConfig struct {
	Enabled      bool
	NetworkName  string
	NetworkID    string
	SidecarName  string
	SidecarImage string
}

// computeBuildKitConfig determines if BuildKit sidecar should be used.
// BuildKit is automatically enabled for docker:dind mode.
func computeBuildKitConfig(dockerConfig *DockerDependencyConfig, runID string) BuildKitConfig {
	// Only enable for dind mode
	if dockerConfig == nil || dockerConfig.Mode != deps.DockerModeDind {
		return BuildKitConfig{Enabled: false}
	}

	return BuildKitConfig{
		Enabled:      true,
		NetworkName:  "moat-" + runID,
		SidecarName:  "moat-buildkit-" + runID,
		SidecarImage: "moby/buildkit:latest",
	}
}

// computeBuildKitEnv returns environment variables for BuildKit integration.
func computeBuildKitEnv(enabled bool) []string {
	if !enabled {
		return nil
	}
	return []string{
		"BUILDKIT_HOST=tcp://buildkit:1234",
	}
}
