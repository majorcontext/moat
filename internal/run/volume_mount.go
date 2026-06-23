package run

import (
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
)

// volumeMount maps one configured volume to a container mount. It returns the
// MountConfig and whether the entry is a native Docker named volume (true).
//
// Named volumes need an in-container chown of their mount root (done by moat-init
// via MOAT_VOLUME_CHOWN) and do not get a host directory created for them; bind
// volumes use a host directory at ~/.moat/volumes/<agent>/<name>.
//
// This function is pure (no filesystem side effects); the caller does the
// MkdirAll for bind volumes.
func volumeMount(agentName string, vol config.VolumeConfig) (container.MountConfig, bool) {
	if vol.Type == "volume" {
		return container.MountConfig{
			Source:   config.DockerVolumeName(agentName, vol.Name),
			Target:   vol.Target,
			ReadOnly: vol.ReadOnly,
			Volume:   true,
		}, true
	}
	return container.MountConfig{
		Source:   config.VolumeDir(agentName, vol.Name),
		Target:   vol.Target,
		ReadOnly: vol.ReadOnly,
	}, false
}
