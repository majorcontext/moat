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

// VolumeDir returns the host directory for an agent volume.
// Path: ~/.moat/volumes/<agentName>/<volumeName>/
//
// Callers must create the directory before mounting:
//
//	volDir := config.VolumeDir(agentName, volumeName)
//	if err := os.MkdirAll(volDir, 0755); err != nil { ... }
//
// See internal/run/manager.go for usage.
func VolumeDir(agentName, volumeName string) string {
	return filepath.Join(GlobalConfigDir(), "volumes", agentName, volumeName)
}
