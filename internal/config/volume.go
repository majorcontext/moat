package config

import (
	"fmt"
	"path/filepath"
)

// DockerVolumeName returns the Docker volume name for an agent volume.
// Format: moat_<agentName>_<volumeName>
func DockerVolumeName(agentName, volumeName string) string {
	return fmt.Sprintf("moat_%s_%s", agentName, volumeName)
}

// VolumeDir returns the host directory for an Apple container volume.
// Path: ~/.moat/volumes/<agentName>/<volumeName>/
func VolumeDir(agentName, volumeName string) string {
	return filepath.Join(GlobalConfigDir(), "volumes", agentName, volumeName)
}
