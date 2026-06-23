package run

import (
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
)

// configHasNamedVolumes reports whether any configured volume uses type: volume
// (a native Docker named volume). Named volumes are created root-owned, so the run
// needs the moat-init entrypoint to chown the volume root to the run user — see
// ImageSpec.HasNamedVolumes / needsInit.
func configHasNamedVolumes(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	for _, v := range cfg.Volumes {
		if v.IsNamedVolume() {
			return true
		}
	}
	return false
}

// volumeMount maps one configured volume to a container mount. It returns the
// MountConfig and whether the entry is a native Docker named volume (true).
//
// Named volumes do not get a host directory created for them and need an
// in-container chown of their mount root (done by moat-init on the root path, or
// the ownership helper on the non-root path); bind volumes use a host directory
// at ~/.moat/volumes/<agent>/<name>.
//
// This function is pure (no filesystem side effects); the caller does the
// MkdirAll for bind volumes.
func volumeMount(agentName string, vol config.VolumeConfig) (container.MountConfig, bool) {
	if vol.IsNamedVolume() {
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

// volumeChownEnv returns the MOAT_VOLUME_CHOWN env entry for moat-init and whether
// to inject it. It is injected only on the root-entrypoint path (containerUser == "")
// where moat-init performs the chown; on the non-root path the ownership helper
// container does it instead, so the env var is omitted. The two mechanisms are
// mutually exclusive — exactly one chowns the volume roots.
func volumeChownEnv(containerUser string, chownPaths []string) (string, bool) {
	if containerUser != "" || len(chownPaths) == 0 {
		return "", false
	}
	return "MOAT_VOLUME_CHOWN=" + strings.Join(chownPaths, " "), true
}
